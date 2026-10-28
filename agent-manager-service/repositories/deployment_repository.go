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
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// DeploymentRepository defines the interface for deployment data operations
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/deployment_repository_mock.go . DeploymentRepository:DeploymentRepositoryMock
type DeploymentRepository interface {
	// Deployment artifact methods (immutable deployments)
	CreateWithLimitEnforcement(deployment *models.Deployment, hardLimit int) error
	GetWithContent(deploymentID, artifactUUID, orgUUID string) (*models.Deployment, error)
	GetWithState(deploymentID, artifactUUID, orgUUID string) (*models.Deployment, error)
	// GetWithStateCtx is GetWithState with context propagation, for call paths
	// (e.g. UndeployLLMProxyDeployment) that need cancellation to reach the
	// query. GetWithState itself is left as-is to avoid forcing ctx onto its
	// other existing caller.
	GetWithStateCtx(ctx context.Context, deploymentID, artifactUUID, orgUUID string) (*models.Deployment, error)
	GetDeploymentsWithState(artifactUUID, orgUUID string, gatewayID *string, status *string, maxPerAPIGW int) ([]*models.Deployment, error)
	Delete(deploymentID, artifactUUID, orgUUID string) error
	// GetCurrentByGateway returns the deployment the gateway is meant to hold (desired DEPLOYED,
	// in any status), or nil when there is none. It is what the gateway's pull serves: the
	// gateway fetches a deployment before it acks it.
	GetCurrentByGateway(artifactUUID, gatewayID, orgUUID string) (*models.Deployment, error)

	// Deployment status methods (mutable state tracking)
	// SetCurrent points the status row at deploymentID and starts a transition to status's
	// desired state: DEPLOYING for a restore.
	SetCurrent(artifactUUID, orgUUID, gatewayID, deploymentID string, status models.DeploymentStatus) (updatedAt time.Time, err error)
	// SetCurrentCtx is SetCurrent with context propagation — see GetWithStateCtx.
	SetCurrentCtx(ctx context.Context, artifactUUID, orgUUID, gatewayID, deploymentID string, status models.DeploymentStatus) (updatedAt time.Time, err error)
	GetStatus(artifactUUID, orgUUID, gatewayID string) (deploymentID string, status models.DeploymentStatus, updatedAt *time.Time, err error)
	DeleteStatus(artifactUUID, orgUUID, gatewayID string) error

	// BeginUndeploy moves the artifact's row on the gateway to UNDEPLOYING, desired UNDEPLOYED,
	// and returns the deployment id the undeploy event must carry: the row's id when the gateway
	// acked it (DEPLOYED), "" otherwise, so the gateway undeploys whichever version it holds.
	// It returns utils.ErrDeploymentNotActive when there is no row or it is already being undeployed.
	BeginUndeploy(ctx context.Context, artifactUUID, orgUUID, gatewayID string) (eventDeploymentID string, err error)

	// Deployment ack methods
	// ApplyAck settles the transition the ack answers, and reports whether a row changed. An ack
	// for a superseded deployment, or for a row no longer waiting on it, changes nothing.
	ApplyAck(ctx context.Context, gatewayID string, ack models.DeploymentAck) (applied bool, err error)
	// FailStaleTransitional fails every DEPLOYING or UNDEPLOYING row whose transition began before
	// olderThan, with reason DEPLOYMENT_TIMEOUT, and returns how many it failed.
	FailStaleTransitional(ctx context.Context, olderThan time.Time) (int64, error)

	// Gateway mapping methods (derived from deployment status)
	// GetDeployedGatewaysByProvider returns the gateways the artifact is placed on: desired
	// DEPLOYED, whether DEPLOYING, DEPLOYED or FAILED.
	GetDeployedGatewaysByProvider(artifactUUID uuid.UUID, orgUUID string) ([]string, error)
	// GetLiveGatewaysByProvider returns the gateways that acked holding the artifact (DEPLOYED).
	GetLiveGatewaysByProvider(artifactUUID uuid.UUID, orgUUID string) ([]string, error)
	GetTrackedGatewaysByProvider(artifactUUID uuid.UUID, orgUUID string) ([]string, error)
	GetTrackedGatewaysByProviderCtx(ctx context.Context, artifactUUID uuid.UUID, orgUUID string) ([]string, error)
	GetDeployedProvidersByGateway(gatewayUUID uuid.UUID, orgUUID string) ([]string, error)
	IsProviderDeployedToGateway(artifactUUID uuid.UUID, gatewayUUID uuid.UUID, orgUUID string) (bool, error)
	GetByArtifactAndGateway(artifactUUID, gatewayUUID, orgUUID string) (*models.Deployment, error)

	// DeleteForArtifact removes every deployment of the artifact; their deployment_status rows cascade.
	DeleteForArtifact(ctx context.Context, artifactUUID uuid.UUID, orgUUID string) error
	// WithTx returns the repository bound to tx.
	WithTx(tx *gorm.DB) DeploymentRepository
}

// statusColumns selects the deployment_status fields models.Deployment reads, from a join aliased s.
const statusColumns = "s.status, s.status_desired, s.status_reason, s.updated_at AS status_updated_at"

// DeploymentRepo implements DeploymentRepository using GORM
type DeploymentRepo struct {
	db *gorm.DB
}

// NewDeploymentRepo creates a new deployment repository
func NewDeploymentRepo(db *gorm.DB) DeploymentRepository {
	return &DeploymentRepo{
		db: db,
	}
}

// CreateWithLimitEnforcement atomically creates a deployment with hard limit enforcement
func (r *DeploymentRepo) CreateWithLimitEnforcement(deployment *models.Deployment, hardLimit int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Generate UUID for deployment if not already set
		if deployment.DeploymentID == uuid.Nil {
			deployment.DeploymentID = uuid.New()
		}
		deployment.CreatedAt = time.Now()

		// A new deployment waits for the gateway's ack.
		if deployment.Status == nil {
			deploying := models.DeploymentStatusDeploying
			deployment.Status = &deploying
		}
		desired := models.DeploymentStatusDeployed
		deployment.StatusDesired = &desired
		deployment.StatusReason = nil

		updatedAt := time.Now()
		deployment.UpdatedAt = &updatedAt

		// Count total deployments for this artifact+Gateway
		var count int64
		if err := tx.Model(&models.Deployment{}).
			Where("artifact_uuid = ? AND gateway_uuid = ? AND ou_id = ?",
				deployment.ArtifactUUID, deployment.GatewayUUID, deployment.OUID).
			Count(&count).Error; err != nil {
			return err
		}

		// If at/over hard limit, delete oldest 5 ARCHIVED deployments
		if count >= int64(hardLimit) {
			// Get oldest 5 ARCHIVED deployment IDs
			var idsToDelete []uuid.UUID
			if err := tx.Table("deployments d").
				Select("d.deployment_id").
				Joins("LEFT JOIN deployment_status s ON d.deployment_id = s.deployment_id "+
					"AND d.artifact_uuid = s.artifact_uuid "+
					"AND d.ou_id = s.ou_id "+
					"AND d.gateway_uuid = s.gateway_uuid").
				Where("d.artifact_uuid = ? AND d.gateway_uuid = ? AND d.ou_id = ? AND s.deployment_id IS NULL",
					deployment.ArtifactUUID, deployment.GatewayUUID, deployment.OUID).
				Order("d.created_at ASC").
				Limit(5).
				Pluck("d.deployment_id", &idsToDelete).Error; err != nil {
				return err
			}

			// Delete one-by-one to use row-level locks
			for _, id := range idsToDelete {
				if err := tx.Where("deployment_id = ?", id).Delete(&models.Deployment{}).Error; err != nil {
					return err
				}
			}
		}

		// Insert new deployment
		if err := tx.Create(deployment).Error; err != nil {
			return err
		}

		return upsertStatus(tx, &models.DeploymentStatusRecord{
			ArtifactUUID:  deployment.ArtifactUUID,
			OUID:          deployment.OUID,
			GatewayUUID:   deployment.GatewayUUID,
			DeploymentID:  deployment.DeploymentID,
			Status:        *deployment.Status,
			StatusDesired: desired,
			PerformedAt:   updatedAt,
			UpdatedAt:     updatedAt,
		})
	})
}

// upsertStatus writes the artifact's status row on the gateway, starting a new transition: the
// reason of the previous one is cleared.
func upsertStatus(db *gorm.DB, record *models.DeploymentStatusRecord) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "artifact_uuid"}, {Name: "ou_id"}, {Name: "gateway_uuid"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"deployment_id":  record.DeploymentID,
			"status":         record.Status,
			"status_desired": record.StatusDesired,
			"status_reason":  nil,
			"performed_at":   record.PerformedAt,
			"updated_at":     record.UpdatedAt,
		}),
	}).Omit("status_reason").Create(record).Error
}

// DeleteForArtifact removes every deployment of the artifact; their deployment_status rows cascade.
func (r *DeploymentRepo) DeleteForArtifact(ctx context.Context, artifactUUID uuid.UUID, orgUUID string) error {
	return r.db.WithContext(ctx).
		Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, orgUUID).
		Delete(&models.Deployment{}).Error
}

// WithTx returns the repository bound to tx.
func (r *DeploymentRepo) WithTx(tx *gorm.DB) DeploymentRepository {
	return &DeploymentRepo{db: tx}
}

// GetWithContent retrieves a deployment including its content
func (r *DeploymentRepo) GetWithContent(deploymentID, artifactUUID, orgUUID string) (*models.Deployment, error) {
	var deployment models.Deployment
	err := r.db.Where("deployment_id = ? AND artifact_uuid = ? AND ou_id = ?",
		deploymentID, artifactUUID, orgUUID).
		First(&deployment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("deployment not found")
		}
		return nil, err
	}
	return &deployment, nil
}

// Delete deletes a deployment record
func (r *DeploymentRepo) Delete(deploymentID, artifactUUID, orgUUID string) error {
	result := r.db.Where("deployment_id = ? AND artifact_uuid = ? AND ou_id = ?",
		deploymentID, artifactUUID, orgUUID).
		Delete(&models.Deployment{})

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return errors.New("deployment not found")
	}

	return nil
}

// GetCurrentByGateway retrieves the deployment the gateway is meant to hold for an artifact
func (r *DeploymentRepo) GetCurrentByGateway(artifactUUID, gatewayID, orgUUID string) (*models.Deployment, error) {
	var deployment models.Deployment
	result := r.db.Table("deployments d").
		Select("d.deployment_id, d.name, d.artifact_uuid, d.ou_id, d.gateway_uuid, "+
			"d.base_deployment_id, d.content, d.metadata, d.created_at, "+
			statusColumns).
		Joins("INNER JOIN deployment_status s ON d.deployment_id = s.deployment_id "+
			"AND d.artifact_uuid = s.artifact_uuid "+
			"AND d.ou_id = s.ou_id "+
			"AND d.gateway_uuid = s.gateway_uuid").
		Where("d.artifact_uuid = ? AND d.gateway_uuid = ? AND d.ou_id = ? AND s.status_desired = ?",
			artifactUUID, gatewayID, orgUUID, string(models.DeploymentStatusDeployed)).
		Order("d.created_at DESC").
		Limit(1).
		Scan(&deployment)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		//nolint:nilnil // A nil deployment with nil error means no active deployment; callers handle deployment == nil.
		return nil, nil
	}
	return &deployment, nil
}

// SetCurrent inserts or updates the deployment status record
func (r *DeploymentRepo) SetCurrent(artifactUUID, orgUUID, gatewayID, deploymentID string, status models.DeploymentStatus) (time.Time, error) {
	return r.SetCurrentCtx(context.Background(), artifactUUID, orgUUID, gatewayID, deploymentID, status)
}

// SetCurrentCtx is SetCurrent with context propagation — see the interface doc comment.
func (r *DeploymentRepo) SetCurrentCtx(ctx context.Context, artifactUUID, orgUUID, gatewayID, deploymentID string, status models.DeploymentStatus) (time.Time, error) {
	updatedAt := time.Now()

	artifactUUID_uuid, err := uuid.Parse(artifactUUID)
	if err != nil {
		return time.Time{}, err
	}
	gatewayUUID_uuid, err := uuid.Parse(gatewayID)
	if err != nil {
		return time.Time{}, err
	}
	deploymentUUID_uuid, err := uuid.Parse(deploymentID)
	if err != nil {
		return time.Time{}, err
	}

	desired := status.ImpliedDesired()
	if desired == "" {
		return time.Time{}, fmt.Errorf("cannot start a transition to status %s", status)
	}

	err = upsertStatus(r.db.WithContext(ctx), &models.DeploymentStatusRecord{
		ArtifactUUID:  artifactUUID_uuid,
		OUID:          orgUUID,
		GatewayUUID:   gatewayUUID_uuid,
		DeploymentID:  deploymentUUID_uuid,
		Status:        status,
		StatusDesired: desired,
		PerformedAt:   updatedAt,
		UpdatedAt:     updatedAt,
	})

	return updatedAt, err
}

// BeginUndeploy moves the artifact's placed row on the gateway to UNDEPLOYING — see the interface doc comment.
func (r *DeploymentRepo) BeginUndeploy(ctx context.Context, artifactUUID, orgUUID, gatewayID string) (string, error) {
	var eventDeploymentID string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row struct {
			DeploymentID  string
			Status        models.DeploymentStatus
			StatusDesired models.DeploymentStatus
		}
		result := tx.Table("deployment_status").
			Select("deployment_id, status, status_desired").
			Where("artifact_uuid = ? AND ou_id = ? AND gateway_uuid = ?", artifactUUID, orgUUID, gatewayID).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Scan(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 || row.StatusDesired != models.DeploymentStatusDeployed {
			return utils.ErrDeploymentNotActive
		}

		now := time.Now()
		if err := tx.Table("deployment_status").
			Where("artifact_uuid = ? AND ou_id = ? AND gateway_uuid = ?", artifactUUID, orgUUID, gatewayID).
			Updates(map[string]interface{}{
				"status":         models.DeploymentStatusUndeploying,
				"status_desired": models.DeploymentStatusUndeployed,
				"status_reason":  nil,
				"performed_at":   now,
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		// Only an acked deployment is known to be what the gateway holds; for anything else the
		// gateway's stale-id check would refuse the undeploy, and an empty id skips that check.
		if row.Status == models.DeploymentStatusDeployed {
			eventDeploymentID = row.DeploymentID
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return eventDeploymentID, nil
}

// ApplyAck settles the transition a gateway ack answers — see the interface doc comment.
//
// An ack applies only to a row whose desired state matches the action and which is either
// still in that action's transitional state, or was failed by the timeout sweeper: a gateway
// that was offline delivers queued events late, and the row must end up saying what the
// gateway holds. An ack with an id matches that deployment; an undeploy ack without one
// answers an undeploy sent without one and matches the artifact on the gateway instead.
func (r *DeploymentRepo) ApplyAck(ctx context.Context, gatewayID string, ack models.DeploymentAck) (bool, error) {
	var transitional, desired, settled models.DeploymentStatus
	switch ack.Action {
	case models.DeploymentAckActionDeploy:
		transitional, desired, settled = models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, models.DeploymentStatusDeployed
	case models.DeploymentAckActionUndeploy:
		transitional, desired, settled = models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed
	default:
		return false, fmt.Errorf("unknown deployment ack action %q", ack.Action)
	}
	var reason interface{}
	if !ack.Success {
		settled = models.DeploymentStatusFailed
		if ack.ErrorCode != "" {
			reason = ack.ErrorCode
		}
	}

	query := r.db.WithContext(ctx).Table("deployment_status").
		Where("gateway_uuid = ? AND status_desired = ?", gatewayID, desired).
		Where("status = ? OR (status = ? AND status_reason = ?)",
			transitional, models.DeploymentStatusFailed, models.DeploymentReasonTimeout)
	switch {
	case ack.DeploymentID != "":
		if _, err := uuid.Parse(ack.DeploymentID); err != nil {
			return false, fmt.Errorf("invalid deployment id %q in ack: %w", ack.DeploymentID, err)
		}
		query = query.Where("deployment_id = ?", ack.DeploymentID)
	case ack.Action == models.DeploymentAckActionUndeploy && ack.ArtifactID != "":
		// The gateway names an LLM proxy by its handle and everything else by its UUID.
		query = query.Where("artifact_uuid IN (?)", r.db.Table("artifacts a").
			Select("a.uuid").
			Where("a.ou_id = deployment_status.ou_id AND (a.uuid::text = ? OR a.handle = ?)", ack.ArtifactID, ack.ArtifactID))
	default:
		return false, nil
	}

	result := query.Updates(map[string]interface{}{
		"status":        settled,
		"status_reason": reason,
		"updated_at":    time.Now(),
	})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// FailStaleTransitional fails rows that never got an ack — see the interface doc comment. One
// guarded UPDATE, so replicas sweeping at once fail each row once and never touch a row an ack
// or a new transition got to first.
func (r *DeploymentRepo) FailStaleTransitional(ctx context.Context, olderThan time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Table("deployment_status").
		Where("status IN ? AND performed_at < ?",
			[]models.DeploymentStatus{models.DeploymentStatusDeploying, models.DeploymentStatusUndeploying}, olderThan).
		Updates(map[string]interface{}{
			"status":        models.DeploymentStatusFailed,
			"status_reason": models.DeploymentReasonTimeout,
			"updated_at":    time.Now(),
		})
	return result.RowsAffected, result.Error
}

// GetStatus retrieves the current deployment status for an artifact on a gateway
func (r *DeploymentRepo) GetStatus(artifactUUID, orgUUID, gatewayID string) (string, models.DeploymentStatus, *time.Time, error) {
	var result struct {
		DeploymentID string
		Status       string
		UpdatedAt    time.Time
	}

	err := r.db.Table("deployment_status").
		Select("deployment_id, status, updated_at").
		Where("artifact_uuid = ? AND ou_id = ? AND gateway_uuid = ?",
			artifactUUID, orgUUID, gatewayID).
		Scan(&result).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", nil, err
		}
		return "", "", nil, err
	}

	return result.DeploymentID, models.DeploymentStatus(result.Status), &result.UpdatedAt, nil
}

// DeleteStatus deletes the status entry for an artifact on a gateway
func (r *DeploymentRepo) DeleteStatus(artifactUUID, orgUUID, gatewayID string) error {
	return r.db.Where("artifact_uuid = ? AND ou_id = ? AND gateway_uuid = ?",
		artifactUUID, orgUUID, gatewayID).
		Delete(&models.DeploymentStatusRecord{}).Error
}

// GetWithState retrieves a deployment with its lifecycle state populated (without content)
func (r *DeploymentRepo) GetWithState(deploymentID, artifactUUID, orgUUID string) (*models.Deployment, error) {
	return r.GetWithStateCtx(context.Background(), deploymentID, artifactUUID, orgUUID)
}

// GetWithStateCtx is GetWithState with context propagation — see the interface doc comment.
func (r *DeploymentRepo) GetWithStateCtx(ctx context.Context, deploymentID, artifactUUID, orgUUID string) (*models.Deployment, error) {
	var deployment models.Deployment
	err := r.db.WithContext(ctx).Table("deployments d").
		Select("d.deployment_id, d.name, d.artifact_uuid, d.ou_id, d.gateway_uuid, "+
			"d.base_deployment_id, d.metadata, d.created_at, "+
			statusColumns).
		Joins("LEFT JOIN deployment_status s ON d.deployment_id = s.deployment_id "+
			"AND d.artifact_uuid = s.artifact_uuid "+
			"AND d.ou_id = s.ou_id "+
			"AND d.gateway_uuid = s.gateway_uuid").
		Where("d.deployment_id = ? AND d.artifact_uuid = ? AND d.ou_id = ?",
			deploymentID, artifactUUID, orgUUID).
		Scan(&deployment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("deployment not found")
		}
		return nil, err
	}

	// If status is NULL, it's ARCHIVED
	if deployment.Status == nil {
		archived := models.DeploymentStatusArchived
		deployment.Status = &archived
	}

	return &deployment, nil
}

// GetDeploymentsWithState retrieves deployments with their lifecycle states
func (r *DeploymentRepo) GetDeploymentsWithState(artifactUUID, orgUUID string, gatewayID *string, status *string, maxPerAPIGW int) ([]*models.Deployment, error) {
	// Validation Logic
	if status != nil && !models.IsValidDeploymentStatusFilter(*status) {
		return nil, fmt.Errorf("invalid deployment status: %s", *status)
	}

	// Build query with CTE for ranking
	query := `
        WITH AnnotatedDeployments AS (
            SELECT
				d.deployment_id, d.name, d.artifact_uuid, d.ou_id, d.gateway_uuid,
                d.base_deployment_id, d.metadata, d.created_at,
                s.status as current_status,
                s.status_desired, s.status_reason,
                s.updated_at as status_updated_at,
                ROW_NUMBER() OVER (
                    PARTITION BY d.gateway_uuid
                    ORDER BY
                        (CASE WHEN s.status IS NOT NULL THEN 0 ELSE 1 END) ASC,
                        d.created_at DESC
                ) as rank_idx
			FROM deployments d
			LEFT JOIN deployment_status s
                ON d.deployment_id = s.deployment_id
                AND d.gateway_uuid = s.gateway_uuid
				AND d.artifact_uuid = s.artifact_uuid
				AND d.ou_id = s.ou_id
			WHERE d.artifact_uuid = ? AND d.ou_id = ?`

	args := []interface{}{artifactUUID, orgUUID}

	if gatewayID != nil {
		query += " AND d.gateway_uuid = ?"
		args = append(args, *gatewayID)
	}

	query += `
        )
        SELECT
			deployment_id, name, artifact_uuid, ou_id, gateway_uuid,
            base_deployment_id, metadata, created_at,
            current_status as status, status_desired, status_reason, status_updated_at
        FROM AnnotatedDeployments
        WHERE rank_idx <= ?`

	args = append(args, maxPerAPIGW)

	if status != nil {
		if *status == string(models.DeploymentStatusArchived) {
			query += " AND current_status IS NULL"
		} else {
			query += " AND current_status = ?"
			args = append(args, *status)
		}
	}

	query += " ORDER BY gateway_uuid ASC, rank_idx ASC"

	var deployments []*models.Deployment
	err := r.db.Raw(query, args...).Scan(&deployments).Error

	// Map ARCHIVED status for records with NULL current_status
	for _, d := range deployments {
		if d.Status == nil {
			archived := models.DeploymentStatusArchived
			d.Status = &archived
		}
	}

	return deployments, err
}

// GetDeployedGatewaysByProvider returns all gateway UUIDs this provider is placed on (desired DEPLOYED)
func (r *DeploymentRepo) GetDeployedGatewaysByProvider(artifactUUID uuid.UUID, orgUUID string) ([]string, error) {
	var gatewayUUIDs []string
	err := r.db.Table("deployment_status").
		Where("artifact_uuid = ? AND ou_id = ? AND status_desired = ?",
			artifactUUID, orgUUID, models.DeploymentStatusDeployed).
		Pluck("gateway_uuid", &gatewayUUIDs).Error
	if err != nil {
		return nil, err
	}

	return gatewayUUIDs, nil
}

// GetLiveGatewaysByProvider returns all gateway UUIDs that acked holding this provider (DEPLOYED)
func (r *DeploymentRepo) GetLiveGatewaysByProvider(artifactUUID uuid.UUID, orgUUID string) ([]string, error) {
	var gatewayUUIDs []string
	err := r.db.Table("deployment_status").
		Where("artifact_uuid = ? AND ou_id = ? AND status = ?",
			artifactUUID, orgUUID, models.DeploymentStatusDeployed).
		Pluck("gateway_uuid", &gatewayUUIDs).Error
	if err != nil {
		return nil, err
	}

	return gatewayUUIDs, nil
}

// GetTrackedGatewaysByProvider returns all gateway UUIDs where this provider has a
// deployment_status row, regardless of whether the current status is DEPLOYED or
// UNDEPLOYED. Used by save-time redeploy flows that must reach every gateway the proxy
// has ever been pushed to, including ones whose last deploy ack failed.
func (r *DeploymentRepo) GetTrackedGatewaysByProvider(artifactUUID uuid.UUID, orgUUID string) ([]string, error) {
	return r.GetTrackedGatewaysByProviderCtx(context.Background(), artifactUUID, orgUUID)
}

func (r *DeploymentRepo) GetTrackedGatewaysByProviderCtx(ctx context.Context, artifactUUID uuid.UUID, orgUUID string) ([]string, error) {
	var gatewayUUIDs []string
	err := r.db.WithContext(ctx).Table("deployment_status").
		Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, orgUUID).
		Pluck("gateway_uuid", &gatewayUUIDs).Error
	if err != nil {
		return nil, err
	}

	return gatewayUUIDs, nil
}

// GetDeployedProvidersByGateway returns all provider UUIDs currently deployed to this gateway
func (r *DeploymentRepo) GetDeployedProvidersByGateway(gatewayUUID uuid.UUID, orgUUID string) ([]string, error) {
	var providerUUIDs []string
	err := r.db.Table("deployment_status").
		Where("gateway_uuid = ? AND ou_id = ? AND status = ?",
			gatewayUUID, orgUUID, models.DeploymentStatusDeployed).
		Pluck("artifact_uuid", &providerUUIDs).Error
	if err != nil {
		return nil, err
	}

	return providerUUIDs, nil
}

// IsProviderDeployedToGateway checks if a provider is currently deployed to a gateway
func (r *DeploymentRepo) IsProviderDeployedToGateway(artifactUUID uuid.UUID, gatewayUUID uuid.UUID, orgUUID string) (bool, error) {
	var count int64
	err := r.db.Table("deployment_status").
		Where("artifact_uuid = ? AND gateway_uuid = ? AND ou_id = ? AND status = ?",
			artifactUUID, gatewayUUID, orgUUID, models.DeploymentStatusDeployed).
		Count(&count).Error

	return count > 0, err
}

// GetByArtifactAndGateway retrieves deployment by artifact and gateway
func (r *DeploymentRepo) GetByArtifactAndGateway(artifactUUID, gatewayUUID, orgUUID string) (*models.Deployment, error) {
	var deploymentID string
	var status models.DeploymentStatus
	var updatedAt *time.Time

	// First get the deployment ID and status from deployment_status table
	err := r.db.Table("deployment_status").
		Select("deployment_id, status, updated_at").
		Where("artifact_uuid = ? AND gateway_uuid = ? AND ou_id = ?",
			artifactUUID, gatewayUUID, orgUUID).
		Row().
		Scan(&deploymentID, &status, &updatedAt)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrArtifactNotFound
		}
		return nil, fmt.Errorf("failed to query deployment status: %w", err)
	}

	// Return a minimal deployment object with status information
	deployment := &models.Deployment{
		Status:    &status,
		UpdatedAt: updatedAt,
	}

	return deployment, nil
}
