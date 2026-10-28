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

package services

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// memIntents is an in-memory DeployIntentRepository for service tests: one intent per artifact
// and scope, as the table keeps them. Its writes ignore the transaction they are bound to.
type memIntents struct {
	mu      sync.Mutex
	intents []*models.DeployIntent
	// statusRows answers StatusRows; nil reads one placed row on a gateway intent's gateway.
	statusRows func(models.DeployIntent) []models.DeploymentStatusRecord
}

var _ repositories.DeployIntentRepository = (*memIntents)(nil)

func (m *memIntents) WithTx(*gorm.DB) repositories.DeployIntentRepository { return m }

func sameScope(a, b models.DeployScope) bool {
	eq := func(x, y *uuid.UUID) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	return eq(a.EnvironmentUUID, b.EnvironmentUUID) && eq(a.GatewayUUID, b.GatewayUUID)
}

func (m *memIntents) find(ouID string, artifactUUID uuid.UUID, scope models.DeployScope) *models.DeployIntent {
	for _, intent := range m.intents {
		if intent.OUID == ouID && intent.ArtifactUUID == artifactUUID && sameScope(intent.Scope(), scope) {
			return intent
		}
	}
	return nil
}

func (m *memIntents) Upsert(_ context.Context, intent *models.DeployIntent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	stored := m.find(intent.OUID, intent.ArtifactUUID, intent.Scope())
	if stored == nil {
		stored = &models.DeployIntent{ID: uuid.New(), CreatedAt: now}
		m.intents = append(m.intents, stored)
	}
	id, createdAt := stored.ID, stored.CreatedAt
	*stored = *intent
	stored.ID, stored.CreatedAt, stored.UpdatedAt = id, createdAt, now
	stored.State, stored.AttemptCount, stored.LastError, stored.NextAttemptAt = models.DeployIntentPending, 0, "", &now
	*intent = *stored
	return nil
}

func (m *memIntents) Get(_ context.Context, id uuid.UUID) (*models.DeployIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, intent := range m.intents {
		if intent.ID == id {
			found := *intent
			return &found, nil
		}
	}
	return nil, repositories.ErrDeployIntentNotFound
}

func (m *memIntents) GetByScope(_ context.Context, ouID string, artifactUUID uuid.UUID, scope models.DeployScope) (*models.DeployIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if intent := m.find(ouID, artifactUUID, scope); intent != nil {
		found := *intent
		return &found, nil
	}
	return nil, repositories.ErrDeployIntentNotFound
}

func (m *memIntents) matching(filter repositories.DeployIntentFilter) []models.DeployIntent {
	var out []models.DeployIntent
	for _, intent := range m.intents {
		if intent.Kind != filter.Kind || intent.OUID != filter.OUID {
			continue
		}
		if filter.EnvironmentUUID != nil && (intent.EnvironmentUUID == nil || *intent.EnvironmentUUID != *filter.EnvironmentUUID) {
			continue
		}
		if filter.ArtifactUUID != nil && intent.ArtifactUUID != *filter.ArtifactUUID {
			continue
		}
		out = append(out, *intent)
	}
	return out
}

func (m *memIntents) List(_ context.Context, filter repositories.DeployIntentFilter) ([]models.DeployIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.matching(filter), nil
}

func (m *memIntents) ListEnvironmentUUIDs(_ context.Context, filter repositories.DeployIntentFilter) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for _, intent := range m.matching(filter) {
		if intent.EnvironmentUUID != nil {
			out = append(out, *intent.EnvironmentUUID)
		}
	}
	return out, nil
}

func (m *memIntents) Delete(_ context.Context, filter repositories.DeployIntentFilter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	drop := map[uuid.UUID]bool{}
	for _, intent := range m.matching(filter) {
		drop[intent.ID] = true
	}
	kept := m.intents[:0]
	for _, intent := range m.intents {
		if !drop[intent.ID] {
			kept = append(kept, intent)
		}
	}
	m.intents = kept
	return nil
}

func (m *memIntents) update(read models.DeployIntent, change func(*models.DeployIntent)) (models.DeployIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, intent := range m.intents {
		if intent.ID == read.ID {
			if !intent.UpdatedAt.Equal(read.UpdatedAt) {
				return models.DeployIntent{}, repositories.ErrDeployIntentSuperseded
			}
			change(intent)
			intent.UpdatedAt = intent.UpdatedAt.Add(time.Microsecond)
			return *intent, nil
		}
	}
	return models.DeployIntent{}, repositories.ErrDeployIntentSuperseded
}

func (m *memIntents) MarkWaiting(_ context.Context, read models.DeployIntent, reason string, next time.Time) error {
	_, err := m.update(read, func(i *models.DeployIntent) { i.LastError, i.NextAttemptAt = reason, &next })
	return err
}

func (m *memIntents) MarkAttemptFailed(_ context.Context, read models.DeployIntent, lastErr string, next time.Time) error {
	_, err := m.update(read, func(i *models.DeployIntent) { i.AttemptCount++; i.LastError, i.NextAttemptAt = lastErr, &next })
	return err
}

func (m *memIntents) MarkFailed(_ context.Context, read models.DeployIntent, lastErr string) error {
	_, err := m.update(read, func(i *models.DeployIntent) {
		i.AttemptCount++
		i.State, i.LastError, i.NextAttemptAt = models.DeployIntentFailed, lastErr, nil
	})
	return err
}

func (m *memIntents) MarkDone(_ context.Context, read models.DeployIntent) (models.DeployIntent, error) {
	return m.update(read, func(i *models.DeployIntent) {
		i.State, i.AttemptCount, i.LastError, i.NextAttemptAt = models.DeployIntentDone, 0, "", nil
		i.PublishCount, i.PublishedAt = 0, nil
	})
}

func (m *memIntents) MarkPublished(context.Context, models.DeployIntent) error { return nil }

func (m *memIntents) Requeue(_ context.Context, read models.DeployIntent) error {
	now := time.Now()
	_, err := m.update(read, func(i *models.DeployIntent) {
		i.State, i.AttemptCount, i.LastError, i.NextAttemptAt = models.DeployIntentPending, 0, "", &now
	})
	return err
}

func (m *memIntents) ClaimDue(context.Context, time.Time, int) ([]models.DeployIntent, error) {
	return nil, nil
}

func (m *memIntents) ClaimUnacked(context.Context, time.Time, time.Time, int) ([]models.DeployIntent, error) {
	return nil, nil
}

func (m *memIntents) ClaimLive(context.Context, []string, int) ([]models.DeployIntent, error) {
	return nil, nil
}

func (m *memIntents) ListDone(context.Context, string, uuid.UUID, int) ([]models.DeployIntent, error) {
	return nil, nil
}

func (m *memIntents) StatusRows(_ context.Context, intent models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
	if m.statusRows != nil {
		return m.statusRows(intent), nil
	}
	if intent.GatewayUUID == nil {
		return nil, nil
	}
	return []models.DeploymentStatusRecord{{
		ArtifactUUID: intent.ArtifactUUID, GatewayUUID: *intent.GatewayUUID, OUID: intent.OUID,
		Status: models.DeploymentStatusDeployed, StatusDesired: models.DeploymentStatusDeployed,
	}}, nil
}

// all returns a copy of every stored intent.
func (m *memIntents) all() []models.DeployIntent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.DeployIntent, 0, len(m.intents))
	for _, intent := range m.intents {
		out = append(out, *intent)
	}
	return out
}

// newTestDeployCore is a reconcile core over an in-memory intent store and deployments, for the
// given kinds. A deployments mock without WithTx is bound to transactions as itself.
func newTestDeployCore(deployments *repomocks.DeploymentRepositoryMock, events *GatewayEventsService, kinds ...reconcile.Kind) (*reconcile.Core, *memIntents) {
	if events == nil {
		events = NewGatewayEventsService(&recordingEventHub{})
	}
	if deployments.WithTxFunc == nil {
		deployments.WithTxFunc = func(*gorm.DB) repositories.DeploymentRepository { return deployments }
	}
	intents := &memIntents{}
	core := reconcile.NewCore(reconcile.TxRunner(sentinelTx), intents, deployments, events, reconcile.DefaultConfig(), testLogger(), kinds...)
	return core, intents
}

// withTestCore gives a provider deployment service built field by field its reconcile core, over
// its own deployments mock.
func (s *LLMProviderDeploymentService) withTestCore() *LLMProviderDeploymentService {
	s.kind = NewLLMProviderKind(s.providerRepo)
	s.deploys, _ = newTestDeployCore(s.deploymentRepo.(*repomocks.DeploymentRepositoryMock), s.gatewayEventsService, s.kind)
	return s
}
