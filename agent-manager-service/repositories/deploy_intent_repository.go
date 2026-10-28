// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// ErrDeployIntentNotFound means no intent exists for the artifact and scope.
var ErrDeployIntentNotFound = errors.New("deploy intent not found")

// ErrDeployIntentSuperseded means the intent changed after the caller read it — it was upserted,
// requeued or deleted since — so the caller's outcome belongs to work that is no longer current
// and was not recorded.
var ErrDeployIntentSuperseded = errors.New("deploy intent changed since it was read")

// DeployIntentClaimLease comfortably outlasts one reconcile attempt.
const DeployIntentClaimLease = 5 * time.Minute

// DeployIntentFilter selects intents of one kind in one org.
type DeployIntentFilter struct {
	Kind string
	OUID string
	// EnvironmentUUID, when set, keeps the intents scoped to that environment.
	EnvironmentUUID *uuid.UUID
	// ArtifactUUID, when set, keeps the intents of that artifact.
	ArtifactUUID *uuid.UUID
	// KindData, when set, keeps the intents whose kind_data holds these fields.
	KindData map[string]any
}

// DeployIntentRepository stores the reconcile core's intents.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/deploy_intent_repository_mock.go . DeployIntentRepository:DeployIntentRepositoryMock
type DeployIntentRepository interface {
	// WithTx returns the repository bound to tx, so its writes commit or roll back with the caller's.
	WithTx(tx *gorm.DB) DeployIntentRepository

	// Upsert records the intent for its artifact and scope, replacing the desired state and kind
	// data of any existing one, and makes it pending and due now with a fresh attempt budget. It
	// fills in the stored row's id and timestamps.
	Upsert(ctx context.Context, intent *models.DeployIntent) error

	// Get and GetByScope return ErrDeployIntentNotFound when there is no such intent.
	Get(ctx context.Context, id uuid.UUID) (*models.DeployIntent, error)
	GetByScope(ctx context.Context, ouID string, artifactUUID uuid.UUID, scope models.DeployScope) (*models.DeployIntent, error)
	List(ctx context.Context, filter DeployIntentFilter) ([]models.DeployIntent, error)
	// ListEnvironmentUUIDs returns the distinct environments of the matching intents.
	ListEnvironmentUUIDs(ctx context.Context, filter DeployIntentFilter) ([]uuid.UUID, error)
	Delete(ctx context.Context, filter DeployIntentFilter) error

	// ClaimDue returns up to limit pending intents whose next attempt time has arrived, pushing
	// each one's next attempt out by DeployIntentClaimLease so no other replica claims it
	// mid-attempt. An attempt that dies without recording an outcome is retried once the lease
	// runs out. The claim leaves updated_at alone.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]models.DeployIntent, error)

	// The Mark* methods and Requeue record an outcome on the intent as it was read. Every change
	// to what an intent asks for moves updated_at, so an intent whose updated_at no longer matches
	// was changed mid-attempt: it is left alone and ErrDeployIntentSuperseded is returned.

	// MarkWaiting schedules the next try without charging the attempt budget.
	MarkWaiting(ctx context.Context, read models.DeployIntent, reason string, nextAttemptAt time.Time) error
	// MarkAttemptFailed charges one attempt and schedules the next try.
	MarkAttemptFailed(ctx context.Context, read models.DeployIntent, lastErr string, nextAttemptAt time.Time) error
	// MarkFailed charges the last attempt and stops retrying.
	MarkFailed(ctx context.Context, read models.DeployIntent, lastErr string) error
	// MarkDone hands the intent to deployment_status: its deployments were written and their
	// events are yet to be sent. It returns the intent as stored.
	MarkDone(ctx context.Context, read models.DeployIntent) (models.DeployIntent, error)
	// MarkPublished records that the events of the deployments MarkDone handed over were sent. It
	// does not move updated_at.
	MarkPublished(ctx context.Context, done models.DeployIntent) error
	// Requeue makes the intent pending and due now with a fresh attempt budget.
	Requeue(ctx context.Context, read models.DeployIntent) error

	// ClaimUnacked returns up to limit done intents whose deployment still waits for the gateway's
	// ack and whose event was sent at most once and not since olderThan (or never, and the intent
	// was done before olderThan), counting the send it claims them for.
	ClaimUnacked(ctx context.Context, now, olderThan time.Time, limit int) ([]models.DeployIntent, error)
	// ClaimLive returns up to limit done intents of the given kinds whose gateway acked a
	// deployment they have not been claimed live for, recording that deployment in
	// LiveDeploymentID. Each deployment is claimed once.
	ClaimLive(ctx context.Context, kinds []string, limit int) ([]models.DeployIntent, error)
	// ListDone pages through the kind's done intents to deploy, in id order, after afterID.
	ListDone(ctx context.Context, kind string, afterID uuid.UUID, limit int) ([]models.DeployIntent, error)

	// StatusRows returns the deployment_status rows of the intent's artifact in its scope.
	StatusRows(ctx context.Context, intent models.DeployIntent) ([]models.DeploymentStatusRecord, error)
}

type deployIntentRepository struct {
	db *gorm.DB
}

// NewDeployIntentRepository creates a DeployIntentRepository.
func NewDeployIntentRepository(db *gorm.DB) DeployIntentRepository {
	return &deployIntentRepository{db: db}
}

func (r *deployIntentRepository) WithTx(tx *gorm.DB) DeployIntentRepository {
	return &deployIntentRepository{db: tx}
}

// intentClock is the time an intent change is stamped with, at the microsecond precision
// Postgres keeps, so a value read back compares equal to the one written.
func intentClock() time.Time {
	return time.Now().Truncate(time.Microsecond)
}

func (r *deployIntentRepository) Upsert(ctx context.Context, intent *models.DeployIntent) error {
	if (intent.EnvironmentUUID == nil) == (intent.GatewayUUID == nil) {
		return errors.New("a deploy intent is scoped to exactly one of an environment or a gateway")
	}
	kindData := intent.KindData
	if len(kindData) == 0 {
		kindData = json.RawMessage(`{}`)
	}
	conflict := "(ou_id, artifact_uuid, environment_uuid) WHERE environment_uuid IS NOT NULL"
	if intent.GatewayUUID != nil {
		conflict = "(ou_id, artifact_uuid, gateway_uuid) WHERE gateway_uuid IS NOT NULL"
	}
	now := intentClock()
	var stored models.DeployIntent
	err := r.db.WithContext(ctx).Raw(`
		INSERT INTO deploy_intents (ou_id, kind, artifact_uuid, environment_uuid, gateway_uuid, desired, state,
			attempt_count, next_attempt_at, last_error, kind_data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, '', ?, ?, ?)
		ON CONFLICT `+conflict+` DO UPDATE SET
			kind = EXCLUDED.kind, desired = EXCLUDED.desired, state = EXCLUDED.state, attempt_count = 0,
			next_attempt_at = EXCLUDED.next_attempt_at, last_error = '', kind_data = EXCLUDED.kind_data,
			updated_at = EXCLUDED.updated_at
		RETURNING *`,
		intent.OUID, intent.Kind, intent.ArtifactUUID, intent.EnvironmentUUID, intent.GatewayUUID, intent.Desired,
		models.DeployIntentPending, now, string(kindData), now, now,
	).Scan(&stored).Error
	if err != nil {
		return err
	}
	*intent = stored
	return nil
}

func (r *deployIntentRepository) Get(ctx context.Context, id uuid.UUID) (*models.DeployIntent, error) {
	return r.first(r.db.WithContext(ctx).Where("id = ?", id))
}

func (r *deployIntentRepository) GetByScope(
	ctx context.Context, ouID string, artifactUUID uuid.UUID, scope models.DeployScope,
) (*models.DeployIntent, error) {
	return r.first(whereScope(r.db.WithContext(ctx).Where("ou_id = ? AND artifact_uuid = ?", ouID, artifactUUID), scope))
}

func (r *deployIntentRepository) first(query *gorm.DB) (*models.DeployIntent, error) {
	var intent models.DeployIntent
	err := query.First(&intent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDeployIntentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &intent, nil
}

func whereScope(query *gorm.DB, scope models.DeployScope) *gorm.DB {
	if scope.GatewayUUID != nil {
		return query.Where("gateway_uuid = ?", *scope.GatewayUUID)
	}
	return query.Where("environment_uuid = ?", scope.EnvironmentUUID)
}

func (r *deployIntentRepository) filtered(ctx context.Context, filter DeployIntentFilter) (*gorm.DB, error) {
	query := r.db.WithContext(ctx).Model(&models.DeployIntent{}).Where("kind = ? AND ou_id = ?", filter.Kind, filter.OUID)
	if filter.EnvironmentUUID != nil {
		query = query.Where("environment_uuid = ?", *filter.EnvironmentUUID)
	}
	if filter.ArtifactUUID != nil {
		query = query.Where("artifact_uuid = ?", *filter.ArtifactUUID)
	}
	if len(filter.KindData) > 0 {
		match, err := json.Marshal(filter.KindData)
		if err != nil {
			return nil, fmt.Errorf("invalid kind data filter: %w", err)
		}
		query = query.Where("kind_data @> ?::jsonb", string(match))
	}
	return query, nil
}

func (r *deployIntentRepository) List(ctx context.Context, filter DeployIntentFilter) ([]models.DeployIntent, error) {
	query, err := r.filtered(ctx, filter)
	if err != nil {
		return nil, err
	}
	var intents []models.DeployIntent
	if err := query.Order("created_at ASC, id ASC").Find(&intents).Error; err != nil {
		return nil, err
	}
	return intents, nil
}

func (r *deployIntentRepository) ListEnvironmentUUIDs(ctx context.Context, filter DeployIntentFilter) ([]uuid.UUID, error) {
	query, err := r.filtered(ctx, filter)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	err = query.Where("environment_uuid IS NOT NULL").Distinct().Pluck("environment_uuid", &ids).Error
	return ids, err
}

func (r *deployIntentRepository) Delete(ctx context.Context, filter DeployIntentFilter) error {
	query, err := r.filtered(ctx, filter)
	if err != nil {
		return err
	}
	return query.Delete(&models.DeployIntent{}).Error
}

func (r *deployIntentRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]models.DeployIntent, error) {
	var claimed []models.DeployIntent
	err := r.db.WithContext(ctx).Raw(`
		UPDATE deploy_intents SET next_attempt_at = ?
		WHERE id IN (
			SELECT id FROM deploy_intents
			WHERE state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING *`,
		now.Add(DeployIntentClaimLease), models.DeployIntentPending, now, limit,
	).Scan(&claimed).Error
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *deployIntentRepository) MarkWaiting(ctx context.Context, read models.DeployIntent, reason string, nextAttemptAt time.Time) error {
	_, err := r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"last_error":      reason,
		"next_attempt_at": nextAttemptAt,
	})
	return err
}

func (r *deployIntentRepository) MarkAttemptFailed(ctx context.Context, read models.DeployIntent, lastErr string, nextAttemptAt time.Time) error {
	_, err := r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"attempt_count":   gorm.Expr("attempt_count + 1"),
		"last_error":      lastErr,
		"next_attempt_at": nextAttemptAt,
	})
	return err
}

func (r *deployIntentRepository) MarkFailed(ctx context.Context, read models.DeployIntent, lastErr string) error {
	_, err := r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"state":           models.DeployIntentFailed,
		"attempt_count":   gorm.Expr("attempt_count + 1"),
		"last_error":      lastErr,
		"next_attempt_at": nil,
	})
	return err
}

func (r *deployIntentRepository) MarkDone(ctx context.Context, read models.DeployIntent) (models.DeployIntent, error) {
	updatedAt, err := r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"state":           models.DeployIntentDone,
		"attempt_count":   0,
		"last_error":      "",
		"next_attempt_at": nil,
		"publish_count":   0,
		"published_at":    nil,
	})
	if err != nil {
		return models.DeployIntent{}, err
	}
	done := read
	done.State, done.AttemptCount, done.LastError, done.NextAttemptAt = models.DeployIntentDone, 0, "", nil
	done.PublishCount, done.PublishedAt, done.UpdatedAt = 0, nil, updatedAt
	return done, nil
}

func (r *deployIntentRepository) MarkPublished(ctx context.Context, done models.DeployIntent) error {
	result := r.db.WithContext(ctx).Model(&models.DeployIntent{}).
		Where("id = ? AND updated_at = ?", done.ID, done.UpdatedAt).
		UpdateColumns(map[string]interface{}{
			"publish_count": gorm.Expr("publish_count + 1"),
			"published_at":  time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDeployIntentSuperseded
	}
	return nil
}

func (r *deployIntentRepository) Requeue(ctx context.Context, read models.DeployIntent) error {
	_, err := r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"state":           models.DeployIntentPending,
		"attempt_count":   0,
		"last_error":      "",
		"next_attempt_at": time.Now(),
	})
	return err
}

func (r *deployIntentRepository) updateIfUnchanged(
	ctx context.Context, read models.DeployIntent, updates map[string]interface{},
) (time.Time, error) {
	updatedAt := intentClock()
	updates["updated_at"] = updatedAt
	result := r.db.WithContext(ctx).Model(&models.DeployIntent{}).
		Where("id = ? AND updated_at = ?", read.ID, read.UpdatedAt).
		UpdateColumns(updates)
	if result.Error != nil {
		return time.Time{}, result.Error
	}
	if result.RowsAffected == 0 {
		return time.Time{}, ErrDeployIntentSuperseded
	}
	return updatedAt, nil
}

// inScope joins deployment_status rows s to the intent i they belong to: the intent's artifact on
// any gateway, or on its gateway when it is gateway-scoped.
const inScope = `s.artifact_uuid = i.artifact_uuid AND s.ou_id = i.ou_id
	AND (i.gateway_uuid IS NULL OR s.gateway_uuid = i.gateway_uuid)`

func (r *deployIntentRepository) ClaimUnacked(ctx context.Context, now, olderThan time.Time, limit int) ([]models.DeployIntent, error) {
	var claimed []models.DeployIntent
	err := r.db.WithContext(ctx).Raw(`
		UPDATE deploy_intents SET publish_count = publish_count + 1, published_at = ?
		WHERE id IN (
			SELECT i.id FROM deploy_intents i
			WHERE i.state = ? AND i.publish_count < 2 AND COALESCE(i.published_at, i.updated_at) < ?
			  AND EXISTS (
				SELECT 1 FROM deployment_status s
				WHERE `+inScope+` AND s.status_desired = i.desired AND s.status IN (?, ?))
			ORDER BY COALESCE(i.published_at, i.updated_at) ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING *`,
		now, models.DeployIntentDone, olderThan,
		models.DeploymentStatusDeploying, models.DeploymentStatusUndeploying, limit,
	).Scan(&claimed).Error
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *deployIntentRepository) ClaimLive(ctx context.Context, kinds []string, limit int) ([]models.DeployIntent, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	var claimed []models.DeployIntent
	err := r.db.WithContext(ctx).Raw(`
		UPDATE deploy_intents i SET live_deployment_id = s.deployment_id
		FROM deployment_status s
		WHERE i.id IN (
			SELECT i.id FROM deploy_intents i
			WHERE i.kind IN ? AND i.state = ? AND i.desired = ?
			  AND EXISTS (
				SELECT 1 FROM deployment_status s
				WHERE `+inScope+` AND s.status = ? AND s.deployment_id IS DISTINCT FROM i.live_deployment_id)
			LIMIT ?
			FOR UPDATE OF i SKIP LOCKED
		)
		  AND `+inScope+` AND s.status = ? AND s.deployment_id IS DISTINCT FROM i.live_deployment_id
		RETURNING i.*`,
		kinds, models.DeployIntentDone, models.DeploymentStatusDeployed, models.DeploymentStatusDeployed, limit,
		models.DeploymentStatusDeployed,
	).Scan(&claimed).Error
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *deployIntentRepository) ListDone(ctx context.Context, kind string, afterID uuid.UUID, limit int) ([]models.DeployIntent, error) {
	var intents []models.DeployIntent
	err := r.db.WithContext(ctx).
		Where("kind = ? AND state = ? AND desired = ? AND id > ?",
			kind, models.DeployIntentDone, models.DeploymentStatusDeployed, afterID).
		Order("id ASC").
		Limit(limit).
		Find(&intents).Error
	if err != nil {
		return nil, err
	}
	return intents, nil
}

func (r *deployIntentRepository) StatusRows(ctx context.Context, intent models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
	query := r.db.WithContext(ctx).Where("artifact_uuid = ? AND ou_id = ?", intent.ArtifactUUID, intent.OUID)
	if intent.GatewayUUID != nil {
		query = query.Where("gateway_uuid = ?", *intent.GatewayUUID)
	}
	var rows []models.DeploymentStatusRecord
	if err := query.Order("performed_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
