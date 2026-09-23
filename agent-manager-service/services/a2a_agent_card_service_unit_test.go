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
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const cardTestEnvID = "dev"

var cardTestEnvUUID = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// cardServiceFixture wires an A2AAgentCardService whose environment lookup
// resolves cardTestEnvID to cardTestEnvUUID.
func cardServiceFixture(pubRepo *repomocks.A2APublicationRepositoryMock) A2AAgentCardServiceInterface {
	ocClient := &clientmocks.OpenChoreoClientMock{
		GetEnvironmentFunc: func(_ context.Context, _, envID string) (*models.EnvironmentResponse, error) {
			if envID != cardTestEnvID {
				return nil, utils.ErrNotFound
			}
			return &models.EnvironmentResponse{UUID: cardTestEnvUUID.String()}, nil
		},
	}
	return NewA2AAgentCardService(pubRepo, ocClient)
}

func TestGetAgentCard_NoRow_ReturnsNilNil(t *testing.T) {
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			return nil, nil
		},
	}
	svc := cardServiceFixture(pubRepo)

	view, err := svc.GetAgentCard(context.Background(), "ou-1", "proj", "agent", cardTestEnvID)

	require.NoError(t, err)
	assert.Nil(t, view)
}

// TestGetAgentCard_SixStates covers every (card, status) combination the API
// contract distinguishes.
func TestGetAgentCard_SixStates(t *testing.T) {
	routedAt := time.Date(2026, 9, 22, 10, 10, 0, 0, time.UTC)
	fetchedAt := time.Date(2026, 9, 22, 10, 14, 3, 0, time.UTC)
	rawCard := json.RawMessage(`{"name":"weather-agent"}`)

	tests := []struct {
		name       string
		pub        *models.A2APublication
		wantCard   map[string]interface{}
		wantStatus models.A2APublicationStatus
	}{
		{
			name:       "pending, no card",
			pub:        &models.A2APublication{Status: models.A2APublicationStatusPending},
			wantCard:   nil,
			wantStatus: models.A2APublicationStatusPending,
		},
		{
			name:       "routed, no card",
			pub:        &models.A2APublication{Status: models.A2APublicationStatusRouted, RoutedAt: &routedAt},
			wantCard:   nil,
			wantStatus: models.A2APublicationStatusRouted,
		},
		{
			// Never routed: RoutedAt stays nil, which is what distinguishes this
			// from "routed, card never arrived" below.
			name:       "failed on first deploy, no card",
			pub:        &models.A2APublication{Status: models.A2APublicationStatusFailed, LastError: "startup probe never passed"},
			wantCard:   nil,
			wantStatus: models.A2APublicationStatusFailed,
		},
		{
			name: "published, card set",
			pub: &models.A2APublication{
				Status: models.A2APublicationStatusPublished, RoutedAt: &routedAt,
				AgentCard: rawCard, CardFetchedAt: &fetchedAt,
			},
			wantCard:   map[string]interface{}{"name": "weather-agent"},
			wantStatus: models.A2APublicationStatusPublished,
		},
		{
			name: "failed after a stale publish, card kept",
			pub: &models.A2APublication{
				Status: models.A2APublicationStatusFailed, RoutedAt: &routedAt,
				AgentCard: rawCard, CardFetchedAt: &fetchedAt,
				LastError: "release binding has not published a service URL yet",
			},
			wantCard:   map[string]interface{}{"name": "weather-agent"},
			wantStatus: models.A2APublicationStatusFailed,
		},
		{
			name: "rejected, card kept",
			pub: &models.A2APublication{
				Status: models.A2APublicationStatusRejected, RoutedAt: &routedAt,
				AgentCard: rawCard, CardFetchedAt: &fetchedAt,
				LastError: "INVALID_INTERFACE_URL",
			},
			wantCard:   map[string]interface{}{"name": "weather-agent"},
			wantStatus: models.A2APublicationStatusRejected,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pubRepo := &repomocks.A2APublicationRepositoryMock{
				GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
					return tc.pub, nil
				},
			}
			svc := cardServiceFixture(pubRepo)

			view, err := svc.GetAgentCard(context.Background(), "ou-1", "proj", "agent", cardTestEnvID)

			require.NoError(t, err)
			require.NotNil(t, view)
			assert.Equal(t, tc.wantCard, view.Card)
			assert.Equal(t, tc.wantStatus, view.Status)
			assert.Equal(t, tc.pub.LastError, view.LastError)
			assert.Equal(t, tc.pub.RoutedAt, view.RoutedAt)
			assert.Equal(t, tc.pub.CardFetchedAt, view.FetchedAt)
		})
	}
}

func TestGetAgentCard_EnvironmentNotFound(t *testing.T) {
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			t.Fatal("repo must not be consulted when the environment cannot be resolved")
			return nil, nil
		},
	}
	svc := cardServiceFixture(pubRepo)

	view, err := svc.GetAgentCard(context.Background(), "ou-1", "proj", "agent", "no-such-env")

	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrEnvironmentNotFound)
	assert.Nil(t, view)
}

// A real repository failure must propagate, not be swallowed into a 404.
func TestGetAgentCard_RepoError_IsNotSwallowed(t *testing.T) {
	repoErr := errors.New("connection reset")
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			return nil, repoErr
		},
	}
	svc := cardServiceFixture(pubRepo)

	view, err := svc.GetAgentCard(context.Background(), "ou-1", "proj", "agent", cardTestEnvID)

	require.Error(t, err)
	assert.ErrorIs(t, err, repoErr)
	assert.NotErrorIs(t, err, utils.ErrA2APublicationNotFound)
	assert.Nil(t, view)
}

func TestRefreshAgentCard_NoRow_ReturnsNotFound(t *testing.T) {
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			return nil, nil
		},
		RequeueCardFunc: func(context.Context, string, string, string, uuid.UUID) error {
			t.Fatal("RequeueCard must not be called for a pair with no row")
			return nil
		},
	}
	svc := cardServiceFixture(pubRepo)

	err := svc.RefreshAgentCard(context.Background(), "ou-1", "proj", "agent", cardTestEnvID)

	assert.ErrorIs(t, err, utils.ErrA2APublicationNotFound)
}

// TestRefreshAgentCard_ExistingRow covers refresh from every status the brief
// calls out as a valid recovery/retry/pickup trigger, plus pending: the
// service's job is deciding whether a row exists, not which phase RequeueCard
// puts it in — that CASE lives in the repository and is covered there.
func TestRefreshAgentCard_ExistingRow(t *testing.T) {
	for _, status := range []models.A2APublicationStatus{
		models.A2APublicationStatusPending,
		models.A2APublicationStatusFailed,
		models.A2APublicationStatusRejected,
		models.A2APublicationStatusPublished,
	} {
		t.Run(string(status), func(t *testing.T) {
			requeueCalls := 0
			pubRepo := &repomocks.A2APublicationRepositoryMock{
				GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
					return &models.A2APublication{Status: status}, nil
				},
				RequeueCardFunc: func(_ context.Context, ouID, projectName, agentName string, envUUID uuid.UUID) error {
					requeueCalls++
					assert.Equal(t, "ou-1", ouID)
					assert.Equal(t, "proj", projectName)
					assert.Equal(t, "agent", agentName)
					assert.Equal(t, cardTestEnvUUID, envUUID)
					return nil
				},
			}
			svc := cardServiceFixture(pubRepo)

			err := svc.RefreshAgentCard(context.Background(), "ou-1", "proj", "agent", cardTestEnvID)

			require.NoError(t, err)
			assert.Equal(t, 1, requeueCalls)
		})
	}
}

func TestRefreshAgentCard_EnvironmentNotFound(t *testing.T) {
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		RequeueCardFunc: func(context.Context, string, string, string, uuid.UUID) error {
			t.Fatal("RequeueCard must not be called when the environment cannot be resolved")
			return nil
		},
	}
	svc := cardServiceFixture(pubRepo)

	err := svc.RefreshAgentCard(context.Background(), "ou-1", "proj", "agent", "no-such-env")

	assert.ErrorIs(t, err, utils.ErrEnvironmentNotFound)
}
