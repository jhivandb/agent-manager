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

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// mcpRig is an MCP proxy with one endpoint bound to one environment, stored and served by mocks,
// on one egress gateway.
type mcpRig struct {
	proxy       *models.MCPProxy
	binding     *models.MCPProxyEndpointEnvironment
	env         uuid.UUID
	gateway     *models.Gateway
	deployments *repomocks.DeploymentRepositoryMock
	hub         *recordingEventHub
	svc         *MCPProxyService
	intents     *memIntents
}

func newMCPRig(t *testing.T) *mcpRig {
	t.Helper()
	r := &mcpRig{env: uuid.New(), hub: &recordingEventHub{}}
	r.gateway = &models.Gateway{UUID: uuid.New(), OUID: "org", GatewayFunctionalityType: models.GatewayRoleEgress}
	r.binding = &models.MCPProxyEndpointEnvironment{EnvironmentUUID: r.env, ArtifactUUID: uuid.New()}
	r.proxy = &models.MCPProxy{
		UUID: uuid.New(), Handle: "weather", Artifact: &models.Artifact{Handle: "weather"},
		Configuration: models.MCPProxyConfig{Name: "Weather", Version: "v1"},
		Endpoints: []models.MCPProxyEndpoint{{
			Handle:        "main",
			Configuration: models.MCPEndpointConfig{Upstream: &models.UpstreamEndpoint{URL: "https://mcp.example.com"}},
			Environments:  []models.MCPProxyEndpointEnvironment{*r.binding},
		}},
	}
	r.binding.MCPProxyUUID = r.proxy.UUID
	r.proxy.Endpoints[0].Environments[0].MCPProxyUUID = r.proxy.UUID
	r.deployments = &repomocks.DeploymentRepositoryMock{
		GetDeployedGatewaysByProviderFunc: func(uuid.UUID, string) ([]string, error) { return nil, nil },
		CreateWithLimitEnforcementFunc:    storesDeployment,
	}
	r.svc = &MCPProxyService{
		repo: &repomocks.MCPProxyRepositoryMock{
			GetByUUIDFunc: func(context.Context, string, string) (*models.MCPProxy, error) { return r.proxy, nil },
		},
		endpointRepo: &repomocks.MCPProxyEndpointRepositoryMock{
			GetEndpointEnvByArtifactFunc: func(context.Context, uuid.UUID) (*models.MCPProxyEndpointEnvironment, error) {
				return r.binding, nil
			},
		},
		mcpProxyScopeRepo: &repomocks.MCPProxyScopeRepositoryMock{
			ListByProxyFunc: func(context.Context, uuid.UUID) ([]models.MCPProxyScope, error) { return nil, nil },
		},
		gatewayRepo:          gatewayFixtureRepo(t, r.env.String(), []*models.Gateway{r.gateway}),
		deploymentRepo:       r.deployments,
		gatewayEventsService: NewGatewayEventsService(r.hub),
		logger:               discardLogger(),
	}
	r.svc.kind = NewMCPProxyKind(r.svc.repo, r.svc.endpointRepo, r.svc.mcpProxyScopeRepo, r.deployments, r.svc.gatewayRepo)
	r.svc.deploys, r.intents = newTestDeployCore(r.deployments, r.svc.gatewayEventsService, r.svc.kind)
	return r
}

func TestMCPProxyDeployRecordsAnEnvironmentIntentPerBinding(t *testing.T) {
	r := newMCPRig(t)

	require.NoError(t, r.svc.deployMCPProxyEndpoints(context.Background(), r.proxy, "org"))

	intents := r.intents.all()
	require.Len(t, intents, 1)
	assert.Equal(t, models.KindMCPProxy, intents[0].Kind)
	assert.Equal(t, r.binding.ArtifactUUID, intents[0].ArtifactUUID)
	assert.Equal(t, &r.env, intents[0].EnvironmentUUID)
	assert.Equal(t, models.DeployIntentDone, intents[0].State)
	created := r.deployments.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	assert.Equal(t, r.gateway.UUID, created[0].Deployment.GatewayUUID)
	require.Len(t, r.hub.published, 1)
	assert.Equal(t, "mcpproxy.deployed", string(r.hub.published[0].EventType))
	assert.Equal(t, r.binding.ArtifactUUID.String(), r.hub.published[0].EntityID)
}

func TestMCPProxySkipsAnEnvironmentWithoutAGateway(t *testing.T) {
	r := newMCPRig(t)
	r.svc.gatewayRepo = gatewayFixtureRepo(t, r.env.String(), nil)
	r.svc.kind.gatewayRepo = r.svc.gatewayRepo

	require.NoError(t, r.svc.deployMCPProxyEndpoints(context.Background(), r.proxy, "org"))

	assert.Empty(t, r.deployments.CreateWithLimitEnforcementCalls())
	for _, intent := range r.intents.all() {
		assert.NotEqual(t, models.DeployIntentDone, intent.State, "the transaction rolls the intent back")
	}
}

func TestMCPProxyScopeChangeRedeploysTheStoredProxy(t *testing.T) {
	r := newMCPRig(t)
	require.NoError(t, r.svc.deployMCPProxyEndpoints(context.Background(), r.proxy, "org"))
	r.intents.statusRows = func(models.DeployIntent) []models.DeploymentStatusRecord { return nil }
	r.proxy.Configuration.Context = strPtr("/weather-v2")

	require.NoError(t, r.svc.RedeployMCPProxy(context.Background(), r.proxy, "org"))

	created := r.deployments.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 2)
	assert.Contains(t, string(created[1].Deployment.Content), "/weather-v2", "the kind renders the stored proxy")
	assert.Len(t, r.svc.mcpProxyScopeRepo.(*repomocks.MCPProxyScopeRepositoryMock).ListByProxyCalls(), 2,
		"with its current scopes")
	assert.Len(t, r.intents.all(), 1, "the binding's intent, redeployed")
}

func TestMCPProxyScopeChangeAdoptsABindingDeployedBeforeIntents(t *testing.T) {
	r := newMCPRig(t)
	r.intents.statusRows = func(models.DeployIntent) []models.DeploymentStatusRecord {
		return []models.DeploymentStatusRecord{{GatewayUUID: r.gateway.UUID, StatusDesired: models.DeploymentStatusDeployed}}
	}

	require.NoError(t, r.svc.RedeployMCPProxy(context.Background(), r.proxy, "org"))

	intents := r.intents.all()
	require.Len(t, intents, 1)
	data, err := decodeMCPIntentData(intents[0])
	require.NoError(t, err)
	assert.Equal(t, r.proxy.UUID, data.ProxyUUID, "found through its binding")
	assert.Len(t, r.deployments.CreateWithLimitEnforcementCalls(), 1)
}

func TestMCPProxyScopeChangeDeploysABindingNeverDeployed(t *testing.T) {
	r := newMCPRig(t)
	r.intents.statusRows = func(models.DeployIntent) []models.DeploymentStatusRecord { return nil }

	require.NoError(t, r.svc.RedeployMCPProxy(context.Background(), r.proxy, "org"))

	assert.Len(t, r.deployments.CreateWithLimitEnforcementCalls(), 1)
}

func TestMCPEndpointStatusShowsAFailedRedeploy(t *testing.T) {
	r := newMCPRig(t)
	intent, err := mcpIntent("org", r.proxy.UUID, r.binding)
	require.NoError(t, err)
	require.NoError(t, r.intents.Upsert(context.Background(), &intent))
	require.NoError(t, r.intents.MarkFailed(context.Background(), intent, "invalid"))

	got := r.svc.mcpIntentStatus(context.Background(), "org", r.binding.ArtifactUUID, r.env.String(), models.MCPDeploymentStatusDeployed)
	assert.Equal(t, models.MCPDeploymentStatusFailed, got)

	other := uuid.New()
	got = r.svc.mcpIntentStatus(context.Background(), "org", other, r.env.String(), models.MCPDeploymentStatusDeployed)
	assert.Equal(t, models.MCPDeploymentStatusDeployed, got, "a binding deployed before intents keeps its row's status")
}

func TestMCPProxyKindEventsKeepTheirWireShape(t *testing.T) {
	kind := NewMCPProxyKind(nil, nil, nil, nil, nil)
	artifact := uuid.New()
	ref := reconcile.EventRef{Intent: models.DeployIntent{ArtifactUUID: artifact}, DeploymentID: "dep-1"}

	deploy := kind.DeployEvent(ref)
	assert.Equal(t, "mcpproxy.deployed", deploy.Type)
	assert.Equal(t, &models.MCPProxyDeploymentEvent{ProxyID: artifact.String(), DeploymentID: "dep-1"}, deploy.Payload)
	for _, event := range []reconcile.Event{kind.DeleteEvent(ref), kind.UndeployEvent(ref)} {
		assert.Equal(t, "mcpproxy.deleted", event.Type, "MCP has no undeploy")
		assert.Equal(t, &models.MCPProxyDeletionEvent{ProxyID: artifact.String()}, event.Payload)
	}
}
