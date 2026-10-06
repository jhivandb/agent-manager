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

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// ErrA2AAgentCardSuperseded means the row was re-enqueued after the caller read it.
var ErrA2AAgentCardSuperseded = errors.New("a2a agent card was re-enqueued since it was read")

// ErrA2AAgentCardNotFound means no card row exists for the agent and environment.
var ErrA2AAgentCardNotFound = errors.New("a2a agent card not found")

// A2AAgentCardRepository stores each A2A agent's card per environment and queues its fetches.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/a2a_agent_card_repository_mock.go . A2AAgentCardRepository:A2AAgentCardRepositoryMock
type A2AAgentCardRepository interface {
	// Enqueue upserts the row as pending with a fresh budget, keeping card, card_hash and fetched_at.
	// source_url is overwritten on conflict only for an external row that names one.
	Enqueue(ctx context.Context, card *models.A2AAgentCard) error

	// ClaimDue leases up to limit pending rows whose next_attempt_at has passed; NULL is never due.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]models.A2AAgentCard, error)

	// The Mark* methods act on the row as ClaimDue returned it and return
	// ErrA2AAgentCardSuperseded when updated_at moved (a newer Enqueue).

	// MarkFetched rewrites card and card_hash only when cardHash differs from read.CardHash.
	MarkFetched(ctx context.Context, read models.A2AAgentCard, card json.RawMessage, cardHash, fetchedURL string) error
	MarkAttemptFailed(ctx context.Context, read models.A2AAgentCard, lastErr string, nextAttemptAt time.Time) error
	MarkFailed(ctx context.Context, read models.A2AAgentCard, lastErr string) error

	// Get returns ErrA2AAgentCardNotFound when no row exists.
	Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AAgentCard, error)
	// ListForAgent returns the agent's rows across environments, ordered by environment name.
	ListForAgent(ctx context.Context, ouID, projectName, agentName string) ([]models.A2AAgentCard, error)
	DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error
	DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName, environmentName string) error
}

// a2aAgentCardClaimLease outlasts a full batch of sequential 5s fetches.
const a2aAgentCardClaimLease = 5 * time.Minute

type a2aAgentCardRepository struct {
	db *gorm.DB
}

// NewA2AAgentCardRepository creates an A2AAgentCardRepository.
func NewA2AAgentCardRepository(db *gorm.DB) A2AAgentCardRepository {
	return &a2aAgentCardRepository{db: db}
}

func (r *a2aAgentCardRepository) Enqueue(ctx context.Context, card *models.A2AAgentCard) error {
	// Postgres keeps microseconds; the Mark* guards compare against this value.
	now := time.Now().Truncate(time.Microsecond)
	card.Status = models.A2AAgentCardStatusPending
	card.AttemptCount = 0
	card.LastError = ""
	card.NextAttemptAt = &now
	card.UpdatedAt = now

	updates := []string{"status", "attempt_count", "last_error", "next_attempt_at", "updated_at"}
	if card.Source == models.A2AAgentCardSourceExternal && card.SourceURL != "" {
		updates = append(updates, "source_url")
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ou_id"},
			{Name: "project_name"},
			{Name: "agent_name"},
			{Name: "environment_name"},
		},
		DoUpdates: clause.AssignmentColumns(updates),
	}).Create(card).Error
}

func (r *a2aAgentCardRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]models.A2AAgentCard, error) {
	var claimed []models.A2AAgentCard
	err := r.db.WithContext(ctx).Raw(
		`
		UPDATE a2a_agent_cards SET next_attempt_at = ?
		WHERE id IN (
			SELECT id FROM a2a_agent_cards
			WHERE status = ? AND next_attempt_at <= ?
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING *`,
		now.Add(a2aAgentCardClaimLease), models.A2AAgentCardStatusPending, now, limit,
	).Scan(&claimed).Error
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *a2aAgentCardRepository) MarkFetched(ctx context.Context, read models.A2AAgentCard, card json.RawMessage, cardHash, fetchedURL string) error {
	updates := map[string]interface{}{
		"status":          models.A2AAgentCardStatusFetched,
		"fetched_at":      time.Now(),
		"last_error":      "",
		"next_attempt_at": nil,
		"source_url":      fetchedURL,
	}
	if cardHash != read.CardHash {
		updates["card"] = card
		updates["card_hash"] = cardHash
	}
	return r.updateIfUnchanged(ctx, read, updates)
}

func (r *a2aAgentCardRepository) MarkAttemptFailed(ctx context.Context, read models.A2AAgentCard, lastErr string, nextAttemptAt time.Time) error {
	return r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"attempt_count":   gorm.Expr("attempt_count + 1"),
		"last_error":      lastErr,
		"next_attempt_at": nextAttemptAt,
	})
}

func (r *a2aAgentCardRepository) MarkFailed(ctx context.Context, read models.A2AAgentCard, lastErr string) error {
	return r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"status":          models.A2AAgentCardStatusFailed,
		"attempt_count":   gorm.Expr("attempt_count + 1"),
		"last_error":      lastErr,
		"next_attempt_at": nil,
	})
}

func (r *a2aAgentCardRepository) updateIfUnchanged(ctx context.Context, read models.A2AAgentCard, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	result := r.db.WithContext(ctx).Model(&models.A2AAgentCard{}).
		Where("id = ? AND updated_at = ?", read.ID, read.UpdatedAt).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrA2AAgentCardSuperseded
	}
	return nil
}

func (r *a2aAgentCardRepository) Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AAgentCard, error) {
	var row models.A2AAgentCard
	err := r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_name = ?",
			ouID, projectName, agentName, environmentName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrA2AAgentCardNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *a2aAgentCardRepository) ListForAgent(ctx context.Context, ouID, projectName, agentName string) ([]models.A2AAgentCard, error) {
	var rows []models.A2AAgentCard
	err := r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ?", ouID, projectName, agentName).
		Order("environment_name ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *a2aAgentCardRepository) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ?", ouID, projectName, agentName).
		Delete(&models.A2AAgentCard{}).Error
}

func (r *a2aAgentCardRepository) DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName, environmentName string) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_name = ?",
			ouID, projectName, agentName, environmentName).
		Delete(&models.A2AAgentCard{}).Error
}
