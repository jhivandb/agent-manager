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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// cardServiceFor builds the service over an agent of the given provisioning and subtype.
func cardServiceFor(provisioning, subType string) (*a2aAgentCardService, *repomocks.A2AAgentCardRepositoryMock, *clientmocks.OpenChoreoClientMock) {
	oc := &clientmocks.OpenChoreoClientMock{
		GetComponentFunc: func(_ context.Context, _, _, name string) (*models.AgentResponse, error) {
			return &models.AgentResponse{
				UUID: "agent-uuid", Name: name,
				Provisioning: models.Provisioning{Type: provisioning},
				Type:         models.AgentType{SubType: subType},
			}, nil
		},
		GetEnvironmentFunc: func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
			return &models.EnvironmentResponse{UUID: "env-uuid", Name: name}, nil
		},
	}
	repo := &repomocks.A2AAgentCardRepositoryMock{
		EnqueueFunc:           func(context.Context, *models.A2AAgentCard) error { return nil },
		DeleteForAgentEnvFunc: func(context.Context, string, string, string, string) error { return nil },
	}
	return &a2aAgentCardService{ocClient: oc, cardRepo: repo, logger: testLogger()}, repo, oc
}

func TestGetA2AAgentCardReturnsTheStoredRow(t *testing.T) {
	svc, repo, oc := cardServiceFor("internal", "a2a-agent")
	stored := &models.A2AAgentCard{Status: models.A2AAgentCardStatusFetched, Card: json.RawMessage(validTestCard)}
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return stored, nil
	}
	oc.GetEnvironmentFunc = nil // a stored row proves the environment

	got, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	require.NoError(t, err)
	assert.Same(t, stored, got)
}

// No row means no fetch is queued, so a synthetic pending would be polled forever.
func TestGetA2AAgentCardIsNotFoundForAPlatformAgentWithNoRow(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}

	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardNotFound)
	assert.Empty(t, repo.EnqueueCalls(), "a read never queues a fetch")
}

func TestGetA2AAgentCardIsNotFoundForAnExternalAgentWithNoSource(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardNotFound)
}

func TestGetA2AAgentCardIsNotFoundForANonA2AAgent(t *testing.T) {
	svc, _, _ := cardServiceFor("internal", "chat-api")
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardNotFound)
}

func TestGetA2AAgentCardDoesNotMaskRealErrors(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, assert.AnError
	}
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotErrorIs(t, err, utils.ErrAgentCardNotFound)
}

func TestGetA2AAgentCardMapsAMissingAgent(t *testing.T) {
	svc, _, oc := cardServiceFor("internal", "a2a-agent")
	oc.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) {
		return nil, utils.ErrNotFound
	}
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentNotFound)
}

func TestRefreshA2AAgentCardQueuesAPlatformFetch(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	require.NoError(t, svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	queued := repo.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2AAgentCardSourcePlatform, queued[0].Card.Source)
	assert.Empty(t, queued[0].Card.SourceURL)
}

func TestRefreshA2AAgentCardKeepsTheExternalSource(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{Source: models.A2AAgentCardSourceExternal, SourceURL: "https://a.example/x"}, nil
	}
	require.NoError(t, svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	queued := repo.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2AAgentCardSourceExternal, queued[0].Card.Source)
	assert.Empty(t, queued[0].Card.SourceURL, "an empty URL leaves the stored one in place")
}

func TestRefreshA2AAgentCardNeedsAnExternalSource(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}
	err := svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardNotFound)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestRefreshA2AAgentCardRejectsANonA2AAgent(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "custom-api")
	err := svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentNotA2A)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestRefreshA2AAgentCardEnforcesTheEnvironmentTier(t *testing.T) {
	svc, repo, oc := cardServiceFor("internal", "a2a-agent")
	oc.GetEnvironmentFunc = func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
		return &models.EnvironmentResponse{Name: name, IsProduction: true}, nil
	}
	err := svc.RefreshA2AAgentCard(tierCtx(t, rbac.AgentEnvNonProduction), "org", "proj", "agent", "prod")
	assert.ErrorIs(t, err, utils.ErrForbidden)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestSetA2ACardSourceQueuesTheExternalURL(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	const url = "https://93.184.215.14/.well-known/agent-card.json"

	require.NoError(t, svc.SetA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev", "  "+url+" "))

	queued := repo.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2AAgentCardSourceExternal, queued[0].Card.Source)
	assert.Equal(t, url, queued[0].Card.SourceURL)
	assert.Equal(t, "dev", queued[0].Card.EnvironmentName)
}

func TestSetA2ACardSourceRejects(t *testing.T) {
	cases := []struct {
		name, provisioning, subType, url string
		want                             error
	}{
		{"non-A2A agent", "external", "custom-api", "https://93.184.215.14/c", utils.ErrAgentNotA2A},
		{"platform agent", "internal", "a2a-agent", "https://93.184.215.14/c", utils.ErrAgentCardSourceNotExternal},
		{"loopback URL", "external", "a2a-agent", "http://127.0.0.1/c", utils.ErrInvalidURL},
		{"non-http scheme", "external", "a2a-agent", "ftp://93.184.215.14/c", utils.ErrInvalidURL},
		{"empty URL", "external", "a2a-agent", "   ", utils.ErrInvalidURL},
		{"too long", "external", "a2a-agent", "https://93.184.215.14/" + strings.Repeat("a", 2048), utils.ErrInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _ := cardServiceFor(tc.provisioning, tc.subType)
			err := svc.SetA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev", tc.url)
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, repo.EnqueueCalls())
		})
	}
}

func TestSetA2ACardSourceRejectsAnUnknownEnvironment(t *testing.T) {
	svc, repo, oc := cardServiceFor("external", "a2a-agent")
	oc.GetEnvironmentFunc = func(context.Context, string, string) (*models.EnvironmentResponse, error) {
		return nil, utils.ErrNotFound
	}
	err := svc.SetA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "nope", "https://93.184.215.14/c")
	assert.ErrorIs(t, err, utils.ErrEnvironmentNotFound)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestDeleteA2ACardSourceRemovesThatEnvironmentsRow(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	require.NoError(t, svc.DeleteA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	deleted := repo.DeleteForAgentEnvCalls()
	require.Len(t, deleted, 1)
	assert.Equal(t, "dev", deleted[0].EnvironmentName)
}

func TestDeleteA2ACardSourceRejectsAPlatformAgent(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	err := svc.DeleteA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardSourceNotExternal)
	assert.Empty(t, repo.DeleteForAgentEnvCalls())
}
