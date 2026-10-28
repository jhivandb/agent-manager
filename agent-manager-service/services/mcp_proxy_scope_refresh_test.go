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
	"errors"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// infraResourceManagerStubForScopeRefresh implements InfraResourceManager by
// embedding the (nil) interface and overriding only ListOrgEnvironments — the
// only method refreshAgentsBoundToProxy calls (same pattern as
// stubAgentConfigurationServiceForPromote in agent_manager_test.go).
type infraResourceManagerStubForScopeRefresh struct {
	InfraResourceManager
	ListOrgEnvironmentsFunc func(ctx context.Context, ouID string) ([]*models.EnvironmentResponse, error)
}

func (s *infraResourceManagerStubForScopeRefresh) ListOrgEnvironments(ctx context.Context, ouID string) ([]*models.EnvironmentResponse, error) {
	return s.ListOrgEnvironmentsFunc(ctx, ouID)
}

func TestRefreshAgentsBoundToProxy_NoMappings_NoOp(t *testing.T) {
	svc := (&MCPProxyService{
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByMCPProxyFunc: func(context.Context, uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				return nil, nil
			},
		},
		infraManager: &infraResourceManagerStubForScopeRefresh{
			ListOrgEnvironmentsFunc: func(context.Context, string) ([]*models.EnvironmentResponse, error) {
				t.Fatal("must not resolve environments when no agents are bound to the proxy")
				return nil, nil
			},
		},
		agentIdentityInjection: &agentIdentityInjectorStub{
			ReconcileForEnvironmentFunc: func(context.Context, string, string, string, string) error {
				t.Fatal("must not refresh any agent when none are bound to the proxy")
				return nil
			},
		},
		logger: discardLogger(),
	}).withTestCore()

	svc.refreshAgentsBoundToProxy(context.Background(), &models.MCPProxy{UUID: uuid.New()}, "acme")
}

func TestRefreshAgentsBoundToProxy_RefreshesEveryBoundAgent(t *testing.T) {
	proxyUUID := uuid.New()
	devEnvUUID := uuid.New()
	prodEnvUUID := uuid.New()
	configA := uuid.New()
	configB := uuid.New()

	svc := (&MCPProxyService{
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByMCPProxyFunc: func(_ context.Context, gotProxyUUID uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				assert.Equal(t, proxyUUID, gotProxyUUID)
				return []models.EnvAgentMCPMapping{
					{ConfigUUID: configA, EnvironmentUUID: devEnvUUID, MCPProxyUUID: proxyUUID},
					{ConfigUUID: configB, EnvironmentUUID: prodEnvUUID, MCPProxyUUID: proxyUUID},
				}, nil
			},
		},
		infraManager: &infraResourceManagerStubForScopeRefresh{
			ListOrgEnvironmentsFunc: func(_ context.Context, ouID string) ([]*models.EnvironmentResponse, error) {
				assert.Equal(t, "acme", ouID)
				return []*models.EnvironmentResponse{
					{UUID: devEnvUUID.String(), Name: "dev"},
					{UUID: prodEnvUUID.String(), Name: "production"},
				}, nil
			},
		},
		agentConfigRepo: &repomocks.AgentConfigurationRepositoryMock{
			GetByUUIDFunc: func(_ context.Context, gotConfigUUID uuid.UUID, ouID string) (*models.AgentConfiguration, error) {
				assert.Equal(t, "acme", ouID)
				switch gotConfigUUID {
				case configA:
					return &models.AgentConfiguration{ProjectName: "proj1", AgentID: "agent-a"}, nil
				case configB:
					return &models.AgentConfiguration{ProjectName: "proj2", AgentID: "agent-b"}, nil
				default:
					t.Fatalf("unexpected configUUID %s", gotConfigUUID)
					return nil, nil //nolint:nilnil // unreachable: t.Fatalf stops the goroutine
				}
			},
		},
		logger: discardLogger(),
	}).withTestCore()

	type call struct{ project, agent, env string }
	var calls []call
	svc.agentIdentityInjection = &agentIdentityInjectorStub{
		ReconcileForEnvironmentFunc: func(_ context.Context, ouID, projectName, agentName, envName string) error {
			assert.Equal(t, "acme", ouID)
			calls = append(calls, call{projectName, agentName, envName})
			return nil
		},
	}

	svc.refreshAgentsBoundToProxy(context.Background(), &models.MCPProxy{UUID: proxyUUID}, "acme")

	sort.Slice(calls, func(i, j int) bool { return calls[i].agent < calls[j].agent })
	assert.Equal(t, []call{
		{"proj1", "agent-a", "dev"},
		{"proj2", "agent-b", "production"},
	}, calls)
}

func TestRefreshAgentsBoundToProxy_SkipsMappingWhenEnvironmentDeleted(t *testing.T) {
	proxyUUID := uuid.New()
	staleEnvUUID := uuid.New()

	svc := (&MCPProxyService{
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByMCPProxyFunc: func(context.Context, uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				return []models.EnvAgentMCPMapping{
					{ConfigUUID: uuid.New(), EnvironmentUUID: staleEnvUUID, MCPProxyUUID: proxyUUID},
				}, nil
			},
		},
		infraManager: &infraResourceManagerStubForScopeRefresh{
			ListOrgEnvironmentsFunc: func(context.Context, string) ([]*models.EnvironmentResponse, error) {
				return []*models.EnvironmentResponse{}, nil // staleEnvUUID no longer exists
			},
		},
		agentConfigRepo: &repomocks.AgentConfigurationRepositoryMock{
			GetByUUIDFunc: func(context.Context, uuid.UUID, string) (*models.AgentConfiguration, error) {
				t.Fatal("must not resolve agent configuration for a mapping whose environment no longer exists")
				return nil, nil //nolint:nilnil // unreachable: t.Fatal stops the goroutine
			},
		},
		agentIdentityInjection: &agentIdentityInjectorStub{
			ReconcileForEnvironmentFunc: func(context.Context, string, string, string, string) error {
				t.Fatal("must not refresh a mapping whose environment no longer exists")
				return nil
			},
		},
		logger: discardLogger(),
	}).withTestCore()

	svc.refreshAgentsBoundToProxy(context.Background(), &models.MCPProxy{UUID: proxyUUID}, "acme")
}

func TestRefreshAgentsBoundToProxy_ContinuesAfterOneAgentFails(t *testing.T) {
	proxyUUID := uuid.New()
	envUUID := uuid.New()
	configA := uuid.New()
	configB := uuid.New()

	svc := (&MCPProxyService{
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByMCPProxyFunc: func(context.Context, uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				return []models.EnvAgentMCPMapping{
					{ConfigUUID: configA, EnvironmentUUID: envUUID, MCPProxyUUID: proxyUUID},
					{ConfigUUID: configB, EnvironmentUUID: envUUID, MCPProxyUUID: proxyUUID},
				}, nil
			},
		},
		infraManager: &infraResourceManagerStubForScopeRefresh{
			ListOrgEnvironmentsFunc: func(context.Context, string) ([]*models.EnvironmentResponse, error) {
				return []*models.EnvironmentResponse{{UUID: envUUID.String(), Name: "dev"}}, nil
			},
		},
		agentConfigRepo: &repomocks.AgentConfigurationRepositoryMock{
			GetByUUIDFunc: func(_ context.Context, gotConfigUUID uuid.UUID, _ string) (*models.AgentConfiguration, error) {
				if gotConfigUUID == configA {
					return &models.AgentConfiguration{ProjectName: "proj1", AgentID: "agent-a"}, nil
				}
				return &models.AgentConfiguration{ProjectName: "proj2", AgentID: "agent-b"}, nil
			},
		},
		logger: discardLogger(),
	}).withTestCore()

	var refreshed []string
	svc.agentIdentityInjection = &agentIdentityInjectorStub{
		ReconcileForEnvironmentFunc: func(_ context.Context, _, _, agentName, _ string) error {
			if agentName == "agent-a" {
				return errors.New("release binding conflict")
			}
			refreshed = append(refreshed, agentName)
			return nil
		},
	}

	assert.NotPanics(t, func() {
		svc.refreshAgentsBoundToProxy(context.Background(), &models.MCPProxy{UUID: proxyUUID}, "acme")
	})
	assert.Equal(t, []string{"agent-b"}, refreshed, "a failure refreshing one agent must not stop the others from being refreshed")
}
