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
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// A2APublicationRepository is the queue of outstanding A2A gateway publications.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/a2a_publication_repository_mock.go . A2APublicationRepository:A2APublicationRepositoryMock
type A2APublicationRepository interface {
	// Enqueue records that an agent-environment pair needs publishing, resetting
	// any existing row for that pair to pending with a fresh attempt budget. A
	// redeploy must re-publish, and a pair that previously exhausted its budget
	// must get another chance.
	Enqueue(ctx context.Context, pub *models.A2APublication) error

	// FindDue returns pending rows whose next attempt time has arrived, oldest
	// first, capped at limit.
	FindDue(ctx context.Context, now time.Time, limit int) ([]models.A2APublication, error)

	// MarkRouted ends phase 1. The row is immediately due again so the card
	// fetch runs on the next tick rather than after a fresh retry wait, and the
	// retry budget resets because phase 2 is a different failure to survive.
	MarkRouted(ctx context.Context, id uuid.UUID, routedAt time.Time) error

	// SetCardDeploymentID records which publish the gateway's next ack will name.
	// It must be written before the broadcast: an ack that arrives before the row
	// knows what it is waiting on is dropped, which is exactly the rejection the
	// ack feedback exists to catch.
	SetCardDeploymentID(ctx context.Context, id, deploymentID uuid.UUID) error

	// MarkCardPublished ends phase 2 and stores the document the gateway serves.
	MarkCardPublished(ctx context.Context, id uuid.UUID, card json.RawMessage, fetchedAt time.Time) error

	// MarkCardRejected records that the gateway refused the outstanding card
	// publish. Matching is on deployment ID alone, so an ack for any superseded
	// publish matches no row. The rejected document is deliberately kept.
	MarkCardRejected(ctx context.Context, deploymentID uuid.UUID, errorCode string) error

	// RequeueCard re-queues the row for its next attempt: phase 1 (pending) when
	// it was never routed, phase 2 (routed) otherwise; it never disturbs live
	// routing, and keeps the stored card unless the gateway rejected it. This is
	// what the refresh endpoint calls.
	RequeueCard(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error

	// GetForAgentEnv reads one pair's row. Nil, nil when there is none — the
	// agent is not A2A, or was never deployed to that environment.
	GetForAgentEnv(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (*models.A2APublication, error)

	// MarkAttemptFailed records a retryable failure and schedules the next try.
	MarkAttemptFailed(ctx context.Context, id uuid.UUID, lastErr string, nextAttemptAt time.Time) error

	// MarkFailed ends the retry cycle. The row is kept as the record of an agent
	// that never reached its gateway.
	MarkFailed(ctx context.Context, id uuid.UUID, lastErr string) error

	// DeleteForAgent removes every environment's row for a deleted agent.
	DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error
}

type a2aPublicationRepository struct {
	db *gorm.DB
}

// NewA2APublicationRepository creates an A2APublicationRepository.
func NewA2APublicationRepository(db *gorm.DB) A2APublicationRepository {
	return &a2aPublicationRepository{db: db}
}

func (r *a2aPublicationRepository) Enqueue(ctx context.Context, pub *models.A2APublication) error {
	now := time.Now()
	pub.Status = models.A2APublicationStatusPending
	pub.AttemptCount = 0
	pub.LastError = ""
	pub.NextAttemptAt = &now
	pub.UpdatedAt = now
	pub.RoutedAt = nil
	pub.CardDeploymentID = nil

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ou_id"},
			{Name: "project_name"},
			{Name: "agent_name"},
			{Name: "environment_name"},
		},
		DoUpdates: append(clause.AssignmentColumns([]string{
			"environment_uuid", "artifact_uuid", "status",
			"attempt_count", "last_error", "next_attempt_at", "updated_at",
			// Routing state is per-deploy and resets; card state is not and must
			// not be listed here, or a redeploy loses the card phase 1
			// republishes and downgrades itself to passthrough.
			"routed_at", "card_deployment_id",
		}), clause.Assignment{
			// A rejected card is dropped, or phase 1 republishes a known-bad document with the new revision.
			Column: clause.Column{Name: "agent_card"},
			Value: gorm.Expr("CASE WHEN a2a_publications.status = ? THEN NULL ELSE a2a_publications.agent_card END",
				models.A2APublicationStatusRejected),
		}),
	}).Create(pub).Error
}

func (r *a2aPublicationRepository) FindDue(ctx context.Context, now time.Time, limit int) ([]models.A2APublication, error) {
	var due []models.A2APublication
	err := r.db.WithContext(ctx).
		Where("status IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)",
			[]models.A2APublicationStatus{
				models.A2APublicationStatusPending,
				models.A2APublicationStatusRouted,
			}, now).
		Order("next_attempt_at ASC, created_at ASC").
		Limit(limit).
		Find(&due).Error
	if err != nil {
		return nil, err
	}
	return due, nil
}

func (r *a2aPublicationRepository) MarkAttemptFailed(ctx context.Context, id uuid.UUID, lastErr string, nextAttemptAt time.Time) error {
	return r.db.WithContext(ctx).Model(&models.A2APublication{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"attempt_count":   gorm.Expr("attempt_count + 1"),
			"last_error":      lastErr,
			"next_attempt_at": nextAttemptAt,
			"updated_at":      time.Now(),
		}).Error
}

func (r *a2aPublicationRepository) MarkFailed(ctx context.Context, id uuid.UUID, lastErr string) error {
	return r.db.WithContext(ctx).Model(&models.A2APublication{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          models.A2APublicationStatusFailed,
			"attempt_count":   gorm.Expr("attempt_count + 1"),
			"last_error":      lastErr,
			"next_attempt_at": nil,
			"updated_at":      time.Now(),
		}).Error
}

func (r *a2aPublicationRepository) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ?", ouID, projectName, agentName).
		Delete(&models.A2APublication{}).Error
}

func (r *a2aPublicationRepository) MarkRouted(ctx context.Context, id uuid.UUID, routedAt time.Time) error {
	now := time.Now()
	// A fast rejection ack may land before this write; rejected stays terminal.
	return r.db.WithContext(ctx).Model(&models.A2APublication{}).
		Where("id = ? AND status <> ?", id, models.A2APublicationStatusRejected).
		Updates(map[string]interface{}{
			"status":          models.A2APublicationStatusRouted,
			"routed_at":       routedAt,
			"attempt_count":   0,
			"last_error":      "",
			"next_attempt_at": now,
			"updated_at":      now,
		}).Error
}

func (r *a2aPublicationRepository) SetCardDeploymentID(ctx context.Context, id, deploymentID uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&models.A2APublication{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"card_deployment_id": deploymentID,
			"updated_at":         time.Now(),
		}).Error
}

func (r *a2aPublicationRepository) MarkCardPublished(
	ctx context.Context, id uuid.UUID, card json.RawMessage, fetchedAt time.Time,
) error {
	// Updates(map) bypasses GORM's serializer, so the card must be cast to jsonb explicitly.
	// A rejection ack may land first; keep its status and error but still store the document.
	rejected := models.A2APublicationStatusRejected
	return r.db.WithContext(ctx).Model(&models.A2APublication{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          gorm.Expr("CASE WHEN status = ? THEN status ELSE ? END", rejected, models.A2APublicationStatusPublished),
			"agent_card":      gorm.Expr("?::jsonb", string(card)),
			"card_fetched_at": fetchedAt,
			"last_error":      gorm.Expr("CASE WHEN status = ? THEN last_error ELSE '' END", rejected),
			"next_attempt_at": nil,
			"updated_at":      time.Now(),
		}).Error
}

func (r *a2aPublicationRepository) MarkCardRejected(ctx context.Context, deploymentID uuid.UUID, errorCode string) error {
	return r.db.WithContext(ctx).Model(&models.A2APublication{}).
		Where("card_deployment_id = ?", deploymentID).
		Updates(map[string]interface{}{
			"status":          models.A2APublicationStatusRejected,
			"last_error":      errorCode,
			"next_attempt_at": nil,
			"updated_at":      time.Now(),
		}).Error
}

func (r *a2aPublicationRepository) RequeueCard(
	ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID,
) error {
	now := time.Now()
	rejected := models.A2APublicationStatusRejected
	// Every CASE reads the pre-update row.
	return r.db.WithContext(ctx).Model(&models.A2APublication{}).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_uuid = ?",
			ouID, projectName, agentName, environmentUUID).
		Updates(map[string]interface{}{
			// A row that never routed has no live Agent resource to leave alone,
			// so a refresh must re-run phase 1, not resume at phase 2.
			"status": gorm.Expr("CASE WHEN routed_at IS NULL THEN ? ELSE ? END",
				models.A2APublicationStatusPending, models.A2APublicationStatusRouted),
			"attempt_count": 0,
			"last_error":    "",
			// A rejected card was never accepted, so phase 2 must republish even an unchanged one.
			"card_deployment_id": gorm.Expr("CASE WHEN status = ? THEN NULL ELSE card_deployment_id END", rejected),
			// The refused card is dropped, or phase 1 republishes it forever and phase 2 misreports it as live.
			"agent_card":      gorm.Expr("CASE WHEN status = ? THEN NULL ELSE agent_card END", rejected),
			"card_fetched_at": gorm.Expr("CASE WHEN status = ? THEN NULL ELSE card_fetched_at END", rejected),
			"next_attempt_at": now,
			"updated_at":      now,
		}).Error
}

//nolint:nilnil // absence is not an error here; the caller turns it into a 404
func (r *a2aPublicationRepository) GetForAgentEnv(
	ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID,
) (*models.A2APublication, error) {
	var pub models.A2APublication
	err := r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_uuid = ?",
			ouID, projectName, agentName, environmentUUID).
		First(&pub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pub, nil
}
