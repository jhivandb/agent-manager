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
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

func deploymentIn(gatewayID uuid.UUID, status, desired models.DeploymentStatus) *models.Deployment {
	return &models.Deployment{GatewayUUID: gatewayID, Status: &status, StatusDesired: &desired}
}

// An agent's proxy URL is handed out as soon as it is deployed: the gateway it is placed on
// anchors it, whether or not the gateway acked yet, and one it was undeployed from does not.
func TestResolveGatewayForProxyAnchorsOnThePlacedDeployment(t *testing.T) {
	envUUID := uuid.New()
	undeployedFrom := newGateway(t, models.GatewayRoleEgress, true)
	placedOn := newGateway(t, models.GatewayRoleEgress, true)
	svc := &agentConfigurationService{
		llmProxyDeploymentService: (&LLMProxyDeploymentService{
			proxyRepo: &repomocks.LLMProxyRepositoryMock{
				GetByIDFunc: func(string, string) (*models.LLMProxy, error) { return &models.LLMProxy{UUID: uuid.New()}, nil },
			},
			deploymentRepo: &repomocks.DeploymentRepositoryMock{
				GetDeploymentsWithStateFunc: func(_, _ string, _, status *string, _ int) ([]*models.Deployment, error) {
					assert.Nil(t, status, "placement is not a single status")
					return []*models.Deployment{
						deploymentIn(undeployedFrom.UUID, models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed),
						deploymentIn(placedOn.UUID, models.DeploymentStatusDeploying, models.DeploymentStatusDeployed),
					}, nil
				},
			},
		}).withTestCore(),
		gatewayRepo: gatewayFixtureRepo(t, envUUID.String(), []*models.Gateway{undeployedFrom, placedOn}),
	}

	got, err := svc.resolveGatewayForProxy(context.Background(), "proxy", "org", envUUID)

	require.NoError(t, err)
	assert.Equal(t, placedOn.UUID, got.UUID)
}

// The MCP endpoint's per-environment status is what the gateway acked; its gateway is where it is placed.
func TestMCPProxyGetReportsEachEnvironmentsLiveStatus(t *testing.T) {
	gatewayID := uuid.New()
	envs := map[models.DeploymentStatus]uuid.UUID{
		models.DeploymentStatusDeploying: uuid.New(),
		models.DeploymentStatusDeployed:  uuid.New(),
		models.DeploymentStatusFailed:    uuid.New(),
	}
	notPlaced := uuid.New()
	statusOf := map[uuid.UUID]models.DeploymentStatus{}
	endpoint := models.MCPProxyEndpoint{UUID: uuid.New(), Handle: "primary"}
	for status, env := range envs {
		artifact := uuid.New()
		statusOf[artifact] = status
		endpoint.Environments = append(endpoint.Environments, models.MCPProxyEndpointEnvironment{EnvironmentUUID: env, ArtifactUUID: artifact})
	}
	endpoint.Environments = append(endpoint.Environments, models.MCPProxyEndpointEnvironment{EnvironmentUUID: notPlaced, ArtifactUUID: uuid.New()})

	svc := (&MCPProxyService{
		repo: &repomocks.MCPProxyRepositoryMock{
			GetByHandleFunc: func(context.Context, string, string) (*models.MCPProxy, error) {
				return &models.MCPProxy{UUID: uuid.New(), Handle: "weather", Endpoints: []models.MCPProxyEndpoint{endpoint}}, nil
			},
		},
		deploymentRepo: &repomocks.DeploymentRepositoryMock{
			GetDeployedGatewaysByProviderFunc: func(artifact uuid.UUID, _ string) ([]string, error) {
				if _, ok := statusOf[artifact]; ok {
					return []string{gatewayID.String()}, nil
				}
				return []string{}, nil
			},
			GetStatusFunc: func(artifact, _, gw string) (string, models.DeploymentStatus, *time.Time, error) {
				assert.Equal(t, gatewayID.String(), gw)
				return uuid.NewString(), statusOf[uuid.MustParse(artifact)], nil, nil
			},
		},
		logger: testLogger(),
	}).withTestCore()

	dto, err := svc.Get(context.Background(), "org", "weather")

	require.NoError(t, err)
	got := map[string]string{}
	for _, env := range dto.Endpoints[0].Environments {
		got[env.EnvironmentUUID] = env.DeploymentStatus
	}
	assert.Equal(t, map[string]string{
		envs[models.DeploymentStatusDeploying].String(): models.MCPDeploymentStatusDeploying,
		envs[models.DeploymentStatusDeployed].String():  models.MCPDeploymentStatusDeployed,
		envs[models.DeploymentStatusFailed].String():    models.MCPDeploymentStatusFailed,
		notPlaced.String(): models.MCPDeploymentStatusUndeployed,
	}, got)
}
