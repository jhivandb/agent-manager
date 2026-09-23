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

package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const cardCtrlEnvID = "dev"

var cardCtrlEnvUUID = uuid.MustParse("22222222-2222-2222-2222-222222222222")

// a2aAgentCardTestController wires the real service behind the controller so
// these tests exercise the whole row -> HTTP response mapping.
func a2aAgentCardTestController(pubRepo *repomocks.A2APublicationRepositoryMock) A2AAgentCardController {
	ocClient := &clientmocks.OpenChoreoClientMock{
		GetEnvironmentFunc: func(_ context.Context, _, envID string) (*models.EnvironmentResponse, error) {
			if envID != cardCtrlEnvID {
				return nil, utils.ErrNotFound
			}
			return &models.EnvironmentResponse{UUID: cardCtrlEnvUUID.String()}, nil
		},
	}
	svc := services.NewA2AAgentCardService(pubRepo, ocClient)
	return NewA2AAgentCardController(svc)
}

func cardRequest(method, envID string) *http.Request {
	req := httptest.NewRequest(method,
		"/orgs/default/projects/proj/agents/agent/environments/"+envID+"/agent-card", nil)
	req.SetPathValue(utils.PathParamOrgName, "default")
	req.SetPathValue(utils.PathParamProjName, "proj")
	req.SetPathValue(utils.PathParamAgentName, "agent")
	req.SetPathValue(utils.PathParamEnvID, envID)
	return req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-1"}))
}

// TestGetAgentCard_SixStates covers every (card, status) row the API contract
// distinguishes, all as 200s.
func TestGetAgentCard_SixStates(t *testing.T) {
	routedAt := time.Date(2026, 9, 22, 10, 10, 0, 0, time.UTC)
	fetchedAt := time.Date(2026, 9, 22, 10, 14, 3, 0, time.UTC)
	rawCard := json.RawMessage(`{"name":"weather-agent"}`)

	tests := []struct {
		name        string
		pub         *models.A2APublication
		wantCard    bool // whether the response's card should be non-nil
		wantStatus  string
		wantRoutedA bool // whether the response's routedAt should be non-nil
	}{
		{"pending, no card", &models.A2APublication{Status: models.A2APublicationStatusPending}, false, "pending", false},
		{
			"routed, no card",
			&models.A2APublication{Status: models.A2APublicationStatusRouted, RoutedAt: &routedAt},
			false, "routed", true,
		},
		{
			// Never routed, so routedAt stays absent — the signal that
			// distinguishes this from "routed, card never arrived" below.
			"failed on first deploy, no card",
			&models.A2APublication{Status: models.A2APublicationStatusFailed, LastError: "never arrived"},
			false, "failed", false,
		},
		{
			"published, card set",
			&models.A2APublication{
				Status: models.A2APublicationStatusPublished, RoutedAt: &routedAt,
				AgentCard: rawCard, CardFetchedAt: &fetchedAt,
			},
			true, "published", true,
		},
		{
			"failed, stale card kept",
			&models.A2APublication{
				Status: models.A2APublicationStatusFailed, RoutedAt: &routedAt,
				AgentCard: rawCard, CardFetchedAt: &fetchedAt,
				LastError: "not ready",
			},
			true, "failed", true,
		},
		{
			"rejected, card kept",
			&models.A2APublication{
				Status: models.A2APublicationStatusRejected, RoutedAt: &routedAt,
				AgentCard: rawCard, CardFetchedAt: &fetchedAt,
				LastError: "INVALID_INTERFACE_URL",
			},
			true, "rejected", true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pubRepo := &repomocks.A2APublicationRepositoryMock{
				GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
					return tc.pub, nil
				},
			}
			ctrl := a2aAgentCardTestController(pubRepo)

			w := httptest.NewRecorder()
			ctrl.GetAgentCard(w, cardRequest(http.MethodGet, cardCtrlEnvID))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp spec.A2AAgentCardResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, tc.wantStatus, resp.Status)
			assert.Equal(t, tc.pub.LastError, resp.LastError)
			if tc.wantCard {
				require.NotNil(t, resp.Card)
				assert.Equal(t, "weather-agent", resp.Card["name"])
				require.True(t, resp.FetchedAt.IsSet())
				require.NotNil(t, resp.FetchedAt.Get())
				assert.True(t, tc.pub.CardFetchedAt.Equal(*resp.FetchedAt.Get()))
			} else {
				assert.Nil(t, resp.Card)
			}
			if tc.wantRoutedA {
				require.NotNil(t, resp.RoutedAt.Get())
				assert.True(t, tc.pub.RoutedAt.Equal(*resp.RoutedAt.Get()))
			} else {
				assert.Nil(t, resp.RoutedAt.Get())
			}
		})
	}
}

func TestGetAgentCard_NoRow_Returns404(t *testing.T) {
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			return nil, nil
		},
	}
	ctrl := a2aAgentCardTestController(pubRepo)

	w := httptest.NewRecorder()
	ctrl.GetAgentCard(w, cardRequest(http.MethodGet, cardCtrlEnvID))

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestRefreshAgentCard_ExistingRow covers refresh from every status the brief
// calls out (recovery from failed, retry from rejected, pickup from published),
// plus pending, each returning 202 and requeuing exactly once.
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
				RequeueCardFunc: func(context.Context, string, string, string, uuid.UUID) error {
					requeueCalls++
					return nil
				},
			}
			ctrl := a2aAgentCardTestController(pubRepo)

			w := httptest.NewRecorder()
			ctrl.RefreshAgentCard(w, cardRequest(http.MethodPost, cardCtrlEnvID))

			assert.Equal(t, http.StatusAccepted, w.Code)
			assert.Equal(t, 1, requeueCalls)
		})
	}
}

func TestRefreshAgentCard_NoRow_Returns404(t *testing.T) {
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		GetForAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			return nil, nil
		},
		RequeueCardFunc: func(context.Context, string, string, string, uuid.UUID) error {
			t.Fatal("RequeueCard must not be called for a pair with no row")
			return nil
		},
	}
	ctrl := a2aAgentCardTestController(pubRepo)

	w := httptest.NewRecorder()
	ctrl.RefreshAgentCard(w, cardRequest(http.MethodPost, cardCtrlEnvID))

	assert.Equal(t, http.StatusNotFound, w.Code)
}
