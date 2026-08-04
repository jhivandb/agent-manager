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
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

func TestSystemManagedMCPURLReturnsProxyURLForMatchingMapping(t *testing.T) {
	configUUID := uuid.New()
	envUUID := uuid.New()
	mappingArtifactUUID := uuid.New()
	sharedArtifactUUID := uuid.New()
	gatewayUUID := uuid.New()
	contextPath := "/shared-mcp"
	svc := &agentConfigurationService{
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByConfigFunc: func(_ context.Context, gotConfigUUID uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				require.Equal(t, configUUID, gotConfigUUID)
				return []models.EnvAgentMCPMapping{
					{
						ConfigUUID:      configUUID,
						EnvironmentUUID: envUUID,
						MCPProxyUUID:    uuid.New(),
						ArtifactUUID:    mappingArtifactUUID,
						MCPProxy: &models.MCPProxy{
							Configuration: models.MCPProxyConfig{
								Context: &contextPath,
							},
							Endpoints: []models.MCPProxyEndpoint{
								{
									Environments: []models.MCPProxyEndpointEnvironment{
										{EnvironmentUUID: envUUID, ArtifactUUID: sharedArtifactUUID},
									},
								},
							},
						},
					},
				}, nil
			},
		},
		mcpProxyService: &MCPProxyService{
			deploymentRepo: &repomocks.DeploymentRepositoryMock{
				GetDeployedGatewaysByProviderFunc: func(gotArtifactUUID uuid.UUID, gotOrg string) ([]string, error) {
					require.Equal(t, sharedArtifactUUID, gotArtifactUUID)
					require.Equal(t, "org", gotOrg)
					return []string{gatewayUUID.String()}, nil
				},
			},
		},
		gatewayRepo: &repomocks.GatewayRepositoryMock{
			EnvironmentMappingExistsFunc: func(gotGatewayID string, gotEnvID string) (bool, error) {
				require.Equal(t, gatewayUUID.String(), gotGatewayID)
				require.Equal(t, envUUID.String(), gotEnvID)
				return true, nil
			},
			GetByUUIDFunc: func(gotGatewayID string) (*models.Gateway, error) {
				require.Equal(t, gatewayUUID.String(), gotGatewayID)
				return &models.Gateway{UUID: gatewayUUID, Vhost: "https://gateway.example.com"}, nil
			},
		},
	}

	binding, err := svc.systemManagedMCPBinding(context.Background(), &models.AgentConfiguration{
		UUID:        configUUID,
		Name:        "tools",
		ProjectName: "project",
		AgentID:     "agent",
	}, "org", "dev", envUUID)

	require.NoError(t, err)
	require.Equal(t, "https://gateway.example.com/shared-mcp/mcp", binding.url)
}

func TestSystemManagedMCPURLMissingSharedArtifactReturnsError(t *testing.T) {
	configUUID := uuid.New()
	envUUID := uuid.New()
	svc := &agentConfigurationService{
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByConfigFunc: func(_ context.Context, gotConfigUUID uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				require.Equal(t, configUUID, gotConfigUUID)
				return []models.EnvAgentMCPMapping{
					{
						ConfigUUID:      configUUID,
						EnvironmentUUID: envUUID,
						MCPProxyUUID:    uuid.New(),
						ArtifactUUID:    uuid.New(),
						MCPProxy: &models.MCPProxy{
							Endpoints: []models.MCPProxyEndpoint{
								{
									Environments: []models.MCPProxyEndpointEnvironment{
										{EnvironmentUUID: envUUID, ArtifactUUID: uuid.Nil},
									},
								},
							},
						},
					},
				}, nil
			},
		},
	}

	binding, err := svc.systemManagedMCPBinding(context.Background(), &models.AgentConfiguration{
		UUID: configUUID,
	}, "org", "dev", envUUID)

	require.ErrorContains(t, err, "MCP proxy shared artifact not found")
	require.Empty(t, binding.url)
}

func TestSystemManagedMCPURLMissingEnvMappingReturnsEmptyURL(t *testing.T) {
	configUUID := uuid.New()
	targetEnvUUID := uuid.New()
	otherEnvUUID := uuid.New()
	svc := &agentConfigurationService{
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByConfigFunc: func(_ context.Context, gotConfigUUID uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				require.Equal(t, configUUID, gotConfigUUID)
				return []models.EnvAgentMCPMapping{
					{
						ConfigUUID:      configUUID,
						EnvironmentUUID: otherEnvUUID,
						MCPProxyUUID:    uuid.New(),
						ArtifactUUID:    uuid.New(),
					},
				}, nil
			},
		},
	}

	binding, err := svc.systemManagedMCPBinding(context.Background(), &models.AgentConfiguration{
		UUID: configUUID,
	}, "org", "dev", targetEnvUUID)

	require.NoError(t, err)
	require.Empty(t, binding.url)
}
