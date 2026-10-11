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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// deploySettingsService wires UpdateAgentDeploySettings for the external A2A agent in externalA2AAgent.
func deploySettingsService(configs *repomocks.AgentConfigRepositoryMock, publisher *A2APublisher) *agentManagerService {
	oc := &clientmocks.OpenChoreoClientMock{
		GetOrganizationFunc: func(_ context.Context, name string) (*models.OrganizationResponse, error) {
			return &models.OrganizationResponse{Name: name}, nil
		},
		GetComponentFunc: func(context.Context, string, string, string) (*models.AgentResponse, error) {
			return externalA2AAgent(), nil
		},
		GetEnvironmentFunc: func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
			return &models.EnvironmentResponse{Name: name, UUID: proxyTestEnvUUID}, nil
		},
		// UpdateReleaseBindingTraitConfigsFunc is nil: an external agent has no release binding.
	}
	artifacts := &repomocks.ArtifactRepositoryMock{
		GetByHandleFunc: func(handle, ouID string) (*models.Artifact, error) {
			return &models.Artifact{UUID: uuid.New(), Handle: handle, Kind: models.KindAgent, OUID: ouID}, nil
		},
	}
	return NewAgentManagerService(nil, oc, nil, nil, nil, configs, nil, nil, artifacts, nil, nil, nil, nil, nil, nil,
		publisher, nil, nil, nil, testLogger()).(*agentManagerService)
}

func savedConfigs() *repomocks.AgentConfigRepositoryMock {
	return &repomocks.AgentConfigRepositoryMock{
		GetFunc: func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
			return &models.AgentConfig{EnableApiKeySecurity: true, CORSEnabled: true}, nil
		},
		UpsertFunc: func(context.Context, *models.AgentConfig) error { return nil },
	}
}

func TestUpdateAgentDeploySettingsRepublishesAProxiedExternalAgent(t *testing.T) {
	var queued []models.A2APublication
	pubs := &repomocks.A2APublicationRepositoryMock{
		EnqueueFunc: func(_ context.Context, pub *models.A2APublication) error {
			queued = append(queued, *pub)
			return nil
		},
	}
	proxies := &repomocks.A2AProxyRepositoryMock{
		GetByEnvUUIDFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
			return &models.A2AProxy{}, nil
		},
	}
	configs := savedConfigs()
	svc := deploySettingsService(configs, NewA2APublisher(nil, pubs, proxies, nil, nil, nil, nil, testLogger()))
	apiKeys := false

	err := svc.UpdateAgentDeploySettings(tierGrantedCtx(t), "org-1", "checkout", "weather",
		&spec.UpdateAgentDeploySettingsRequest{EnvironmentName: "dev", EnableApiKeySecurity: &apiKeys})

	require.NoError(t, err)
	require.Len(t, configs.UpsertCalls(), 1)
	assert.False(t, configs.UpsertCalls()[0].Config.EnableApiKeySecurity)
	require.Len(t, queued, 1, "the gateway picks the new settings up from a republish")
	assert.Equal(t, models.A2APublicationSourceExternal, queued[0].Source)
}

func TestUpdateAgentDeploySettingsRejectsADirectExternalEnvironment(t *testing.T) {
	proxies := &repomocks.A2AProxyRepositoryMock{
		GetByEnvUUIDFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
			return nil, repositories.ErrA2AProxyNotFound
		},
	}
	configs := savedConfigs()
	svc := deploySettingsService(configs, NewA2APublisher(nil, &repomocks.A2APublicationRepositoryMock{}, proxies, nil, nil, nil, nil, testLogger()))

	err := svc.UpdateAgentDeploySettings(tierGrantedCtx(t), "org-1", "checkout", "weather",
		&spec.UpdateAgentDeploySettingsRequest{EnvironmentName: "dev"})

	require.ErrorIs(t, err, utils.ErrInvalidInput)
	assert.Contains(t, err.Error(), "gateway")
	assert.Empty(t, configs.UpsertCalls())
}
