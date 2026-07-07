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
)

// ErrAgentThunderClientNotFound is returned when no binding exists for the given
// agent and environment.
var ErrAgentThunderClientNotFound = errors.New("agent thunder client not found")

// AgentThunderClientRepository persists the AgentID binding described in the
// AgentID architecture doc (Section 7/8): one row per agent per environment,
// tracking provisioning status and retry state.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/agent_thunder_client_repository_mock.go . AgentThunderClientRepository:AgentThunderClientRepositoryMock
type AgentThunderClientRepository interface {
	// Upsert creates or updates a binding row, keyed on (org, project, agent, environment).
	Upsert(ctx context.Context, client *models.AgentThunderClient) error

	// FindByAgent returns every binding for the given agent, across all environments.
	FindByAgent(ctx context.Context, orgName, projectName, agentName string) ([]models.AgentThunderClient, error)

	// FindByOrgAndEnvironment returns every binding row for the org+environment,
	// the source for the agent-identity assignment picker (name, status, ThunderAgentID).
	FindByOrgAndEnvironment(ctx context.Context, orgName, environmentName string) ([]models.AgentThunderClient, error)

	// Get returns the binding for a specific agent in a specific environment.
	Get(ctx context.Context, orgName, projectName, agentName, environmentName string) (*models.AgentThunderClient, error)

	// FindDue returns up to limit bindings ready for an attempt: PENDING rows
	// whose next retry time has arrived (or was never set — the write-ahead row
	// before its first attempt), plus IN_PROGRESS rows abandoned by a crashed
	// attempt (last_attempted_at older than inProgressStaleThreshold) so a crash
	// mid-attempt cannot strand a binding forever.
	FindDue(ctx context.Context, now time.Time, limit int) ([]models.AgentThunderClient, error)

	// ClaimForAttempt atomically transitions a binding to IN_PROGRESS so the
	// inline fast-path goroutine and the reconciler's sweep can never both run
	// AttemptProvision on the same binding concurrently. Succeeds from PENDING,
	// or from a stale IN_PROGRESS row (a crashed prior attempt). claimed=false
	// means someone else already holds this binding (or it is no longer
	// pending/stale) — the caller must skip the attempt.
	ClaimForAttempt(ctx context.Context, id uuid.UUID) (claimed bool, err error)

	// UpdateAfterAttempt records the outcome of a provisioning attempt: the resolved
	// Thunder identity (on success), status, attempt bookkeeping, and the next retry
	// time (nil once the binding is COMPLETED or FAILED).
	UpdateAfterAttempt(ctx context.Context, id uuid.UUID, fields AgentThunderAttemptUpdate) error

	// MarkClaimed atomically marks an external agent's transient secret as
	// retrieved, but only if it hasn't been claimed already (compare-and-swap on
	// claimed_at IS NULL). claimed=false means a concurrent caller already won
	// the claim — the caller must not read or return the secret in that case.
	MarkClaimed(ctx context.Context, id uuid.UUID, claimedAt time.Time) (claimed bool, err error)

	// UpdateSecretRef updates only the stored secret location, without touching
	// status or retry bookkeeping. Used by regenerate (new path) and revoke
	// (cleared to "" — there is no currently valid stored secret).
	UpdateSecretRef(ctx context.Context, id uuid.UUID, secretRefPath string) error

	// ClearClaim resets claimed_at to NULL — used by regenerate immediately
	// after storing a brand-new secret, so that new secret is eligible for the
	// one-time claim again (MarkClaimed from a previous secret must not carry
	// over and make a *different*, never-yet-seen secret look already-claimed).
	ClearClaim(ctx context.Context, id uuid.UUID) error

	// DeleteByAgent removes every binding for the given agent. Test-cleanup use
	// only — production deletion goes through DeleteByIDs so a concurrent
	// recreate of the same agent name can't have its fresh rows swept up too.
	DeleteByAgent(ctx context.Context, orgName, projectName, agentName string) error

	// DeleteByIDs removes exactly the given binding rows. No-op if ids is empty.
	DeleteByIDs(ctx context.Context, ids []uuid.UUID) error
}

// AgentThunderAttemptUpdate carries the fields written after one provisioning
// attempt. ThunderAgentID/ThunderClientID/SecretRefPath are pointers so
// "leave the stored value alone" (nil) is explicit and distinct from
// "overwrite it with an empty string" (a non-nil pointer to "").
type AgentThunderAttemptUpdate struct {
	Status          models.AgentThunderStatus
	ThunderAgentID  *string
	ThunderClientID *string
	SecretRefPath   *string
	LastError       string
	NextRetryAt     *time.Time
}

// inProgressStaleThreshold bounds how long a binding may sit in IN_PROGRESS
// before FindDue treats it as an abandoned attempt (e.g. the process crashed
// mid-attempt) and makes it eligible to be claimed again. Comfortably longer
// than a single attempt ever legitimately takes (Thunder's HTTP client timeout
// is 30s), so a live in-flight attempt is never mistakenly reclaimed.
const inProgressStaleThreshold = 5 * time.Minute

// AgentThunderClientRepo implements AgentThunderClientRepository using GORM.
type AgentThunderClientRepo struct {
	db *gorm.DB
}

// NewAgentThunderClientRepo creates a new agent Thunder client repository.
func NewAgentThunderClientRepo(db *gorm.DB) AgentThunderClientRepository {
	return &AgentThunderClientRepo{db: db}
}

func (r *AgentThunderClientRepo) Upsert(ctx context.Context, c *models.AgentThunderClient) error {
	// DoNothing (not DoUpdates): Upsert is only ever used to write-ahead a brand
	// new binding. If a concurrent caller already wrote one for this
	// (org,project,agent,environment) — e.g. the explicit provision endpoint
	// racing PromoteAgent's hook — silently overwriting the winner's row would
	// clobber an already-completed Thunder identity/secret reference back to
	// blank, orphaning it. On conflict, GORM leaves c's fields (including ID)
	// unpopulated from the DB; callers must treat that as "someone else owns
	// this binding" rather than assume c now reflects a real row.
	if err := r.db.WithContext(ctx).Select("*").Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "org_name"}, {Name: "project_name"}, {Name: "agent_name"}, {Name: "environment_name"},
		},
		DoNothing: true,
	}).Create(c).Error; err != nil {
		return fmt.Errorf("upsert agent thunder client: %w", err)
	}
	return nil
}

func (r *AgentThunderClientRepo) FindByAgent(ctx context.Context, orgName, projectName, agentName string) ([]models.AgentThunderClient, error) {
	var rows []models.AgentThunderClient
	if err := r.db.WithContext(ctx).Where("org_name = ? AND project_name = ? AND agent_name = ?", orgName, projectName, agentName).
		Order("environment_name").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("find agent thunder clients by agent: %w", err)
	}
	return rows, nil
}

func (r *AgentThunderClientRepo) FindByOrgAndEnvironment(ctx context.Context, orgName, environmentName string) ([]models.AgentThunderClient, error) {
	var rows []models.AgentThunderClient
	if err := r.db.WithContext(ctx).
		Where("org_name = ? AND environment_name = ?", orgName, environmentName).
		Order("project_name asc, agent_name asc").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list agent thunder bindings for %s/%s: %w", orgName, environmentName, err)
	}
	return rows, nil
}

func (r *AgentThunderClientRepo) Get(ctx context.Context, orgName, projectName, agentName, environmentName string) (*models.AgentThunderClient, error) {
	var c models.AgentThunderClient
	err := r.db.WithContext(ctx).Where("org_name = ? AND project_name = ? AND agent_name = ? AND environment_name = ?",
		orgName, projectName, agentName, environmentName).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentThunderClientNotFound
		}
		return nil, fmt.Errorf("get agent thunder client: %w", err)
	}
	return &c, nil
}

func (r *AgentThunderClientRepo) FindDue(ctx context.Context, now time.Time, limit int) ([]models.AgentThunderClient, error) {
	staleBefore := now.Add(-inProgressStaleThreshold)
	var rows []models.AgentThunderClient
	if err := r.db.WithContext(ctx).Where(
		"(status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (status = ? AND last_attempted_at <= ?)",
		models.AgentThunderStatusPending, now,
		models.AgentThunderStatusInProgress, staleBefore,
	).
		Order("created_at").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("find due agent thunder clients: %w", err)
	}
	return rows, nil
}

func (r *AgentThunderClientRepo) ClaimForAttempt(ctx context.Context, id uuid.UUID) (bool, error) {
	staleBefore := time.Now().Add(-inProgressStaleThreshold)
	result := r.db.WithContext(ctx).Model(&models.AgentThunderClient{}).
		Where("id = ? AND (status = ? OR (status = ? AND last_attempted_at <= ?))",
			id, models.AgentThunderStatusPending, models.AgentThunderStatusInProgress, staleBefore).
		Updates(map[string]interface{}{
			"status":            models.AgentThunderStatusInProgress,
			"last_attempted_at": clause.Expr{SQL: "NOW()"},
			"updated_at":        clause.Expr{SQL: "NOW()"},
		})
	if result.Error != nil {
		return false, fmt.Errorf("claim agent thunder client for attempt: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *AgentThunderClientRepo) UpdateAfterAttempt(ctx context.Context, id uuid.UUID, f AgentThunderAttemptUpdate) error {
	updates := map[string]interface{}{
		"status":            f.Status,
		"last_error":        f.LastError,
		"next_retry_at":     f.NextRetryAt,
		"last_attempted_at": clause.Expr{SQL: "NOW()"},
		"attempt_count":     gorm.Expr("attempt_count + 1"),
		"updated_at":        clause.Expr{SQL: "NOW()"},
	}
	if f.ThunderAgentID != nil {
		updates["thunder_agent_id"] = *f.ThunderAgentID
	}
	if f.ThunderClientID != nil {
		updates["thunder_client_id"] = *f.ThunderClientID
	}
	if f.SecretRefPath != nil {
		updates["secret_ref_path"] = *f.SecretRefPath
	}
	if err := r.db.WithContext(ctx).Model(&models.AgentThunderClient{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("update agent thunder client after attempt: %w", err)
	}
	return nil
}

func (r *AgentThunderClientRepo) MarkClaimed(ctx context.Context, id uuid.UUID, claimedAt time.Time) (bool, error) {
	result := r.db.WithContext(ctx).Model(&models.AgentThunderClient{}).
		Where("id = ? AND claimed_at IS NULL", id).
		Update("claimed_at", claimedAt)
	if result.Error != nil {
		return false, fmt.Errorf("mark agent thunder client claimed: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *AgentThunderClientRepo) UpdateSecretRef(ctx context.Context, id uuid.UUID, secretRefPath string) error {
	if err := r.db.WithContext(ctx).Model(&models.AgentThunderClient{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"secret_ref_path": secretRefPath,
			"updated_at":      clause.Expr{SQL: "NOW()"},
		}).Error; err != nil {
		return fmt.Errorf("update agent thunder client secret ref: %w", err)
	}
	return nil
}

func (r *AgentThunderClientRepo) ClearClaim(ctx context.Context, id uuid.UUID) error {
	if err := r.db.WithContext(ctx).Model(&models.AgentThunderClient{}).Where("id = ?", id).
		Update("claimed_at", nil).Error; err != nil {
		return fmt.Errorf("clear agent thunder client claim: %w", err)
	}
	return nil
}

func (r *AgentThunderClientRepo) DeleteByAgent(ctx context.Context, orgName, projectName, agentName string) error {
	if err := r.db.WithContext(ctx).Where("org_name = ? AND project_name = ? AND agent_name = ?", orgName, projectName, agentName).
		Delete(&models.AgentThunderClient{}).Error; err != nil {
		return fmt.Errorf("delete agent thunder clients by agent: %w", err)
	}
	return nil
}

func (r *AgentThunderClientRepo) DeleteByIDs(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Delete(&models.AgentThunderClient{}).Error; err != nil {
		return fmt.Errorf("delete agent thunder clients by ids: %w", err)
	}
	return nil
}
