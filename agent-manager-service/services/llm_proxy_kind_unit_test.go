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
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// proxyRig is the LLM proxy deployment service over mocks: one proxy of one provider, whose
// configuration renders, on one egress gateway.
type proxyRig struct {
	*lifecycleRig
	proxy     *models.LLMProxy
	artifacts *repomocks.ArtifactRepositoryMock
	svc       *LLMProxyDeploymentService
}

func newProxyRig(t *testing.T) *proxyRig {
	t.Helper()
	r := &proxyRig{lifecycleRig: newLifecycleRig()}
	vhost := "api.example.com"
	providerUUID := uuid.New()
	r.proxy = &models.LLMProxy{
		UUID: r.artifactUUID, Handle: r.handle,
		Configuration: models.LLMProxyConfig{Name: r.handle, Version: "v1.0", Provider: providerUUID.String(), Vhost: &vhost},
	}
	proxies := &repomocks.LLMProxyRepositoryMock{
		GetByIDFunc:    func(string, string) (*models.LLMProxy, error) { return r.proxy, nil },
		GetByIDCtxFunc: func(context.Context, string, string) (*models.LLMProxy, error) { return r.proxy, nil },
	}
	providers := &repomocks.LLMProviderRepositoryMock{
		GetByUUIDFunc: func(string, string) (*models.LLMProvider, error) { return deployableProvider(providerUUID), nil },
	}
	gateways := &repomocks.GatewayRepositoryMock{
		GetByUUIDFunc: func(string) (*models.Gateway, error) {
			return &models.Gateway{UUID: r.gatewayID, OUID: r.ouID, GatewayFunctionalityType: models.GatewayRoleEgress}, nil
		},
	}
	r.artifacts = &repomocks.ArtifactRepositoryMock{
		GetByUUIDFunc: func(string, string) (*models.Artifact, error) { return &models.Artifact{Handle: r.handle}, nil },
	}
	r.deployments.GetDeployedGatewaysByProviderFunc = func(uuid.UUID, string) ([]string, error) { return nil, nil }
	r.deployments.CreateWithLimitEnforcementFunc = storesDeployment
	events := NewGatewayEventsService(r.hub)
	kind := NewLLMProxyKind(proxies, providers, r.artifacts, r.deployments, gateways)
	core, intents := newTestDeployCore(r.deployments, events, kind)
	r.intents = intents
	r.svc = NewLLMProxyDeploymentService(r.deployments, proxies, providers, gateways, events, core, kind)
	return r
}

func TestLLMProxyDeployForAnEnvironmentRecordsTheEnvironmentIntent(t *testing.T) {
	r := newProxyRig(t)
	env := uuid.New()

	got, err := r.svc.DeployLLMProxyInEnvironment(context.Background(), r.handle, env, &models.DeployAPIRequest{
		Name: "agent-dev-deployment", Base: "current", GatewayID: r.gatewayID.String(),
	}, r.ouID)

	require.NoError(t, err)
	assert.Equal(t, r.gatewayID, got.GatewayUUID)
	intents := r.intents.all()
	require.Len(t, intents, 1)
	assert.Equal(t, &env, intents[0].EnvironmentUUID, "an agent's proxy is addressed by its environment")
	data, err := decodeLLMIntentData(intents[0])
	require.NoError(t, err)
	assert.Equal(t, llmIntentData{Name: "agent-dev-deployment", GatewayID: r.gatewayID.String(), Handle: r.handle, Vhost: "api.example.com"}, data)
	var event models.LLMProxyDeploymentEvent
	evt := r.payload(t, &event)
	assert.Equal(t, r.handle, evt.EntityID, "proxies stay keyed by handle")
	assert.Equal(t, r.handle, event.ProxyID)
	assert.Equal(t, "api.example.com", event.Vhost)
}

func TestLLMProxyDeployToAGatewayReusesTheIntentPlacingItThere(t *testing.T) {
	r := newProxyRig(t)
	env := uuid.New()
	req := &models.DeployAPIRequest{Name: "d", Base: "current", GatewayID: r.gatewayID.String()}
	_, err := r.svc.DeployLLMProxyInEnvironment(context.Background(), r.handle, env, req, r.ouID)
	require.NoError(t, err)
	r.intents.statusRows = func(intent models.DeployIntent) []models.DeploymentStatusRecord {
		return []models.DeploymentStatusRecord{{GatewayUUID: r.gatewayID, StatusDesired: models.DeploymentStatusDeployed}}
	}

	_, err = r.svc.DeployLLMProxy(r.handle, req, r.ouID)

	require.NoError(t, err)
	intents := r.intents.all()
	require.Len(t, intents, 1, "one intent per status row, never two")
	assert.Equal(t, &env, intents[0].EnvironmentUUID)
}

func TestLLMProxyDeployOfAnInvalidSecurityConfigIsABadRequest(t *testing.T) {
	r := newProxyRig(t)
	r.proxy.Configuration.Security = &models.SecurityConfig{
		Enabled: boolPtr(true),
		APIKey:  &models.APIKeySecurity{Enabled: boolPtr(true), Key: "X-Key", In: "cookie"},
	}

	_, err := r.svc.DeployLLMProxy(r.handle, &models.DeployAPIRequest{Name: "d", Base: "current", GatewayID: r.gatewayID.String()}, r.ouID)

	assert.ErrorIs(t, err, utils.ErrInvalidInput)
	assert.Empty(t, r.deployments.CreateWithLimitEnforcementCalls())
	assert.Empty(t, r.hub.published)
}

func TestLLMProxyRedeployStaleAdoptsADeploymentMadeBeforeIntents(t *testing.T) {
	r := newProxyRig(t)
	r.deployments.GetCurrentByGatewayFunc = func(string, string, string) (*models.Deployment, error) {
		return &models.Deployment{Name: "agent-dev-deployment"}, nil
	}

	err := r.svc.RedeployStale(context.Background(), r.proxy, r.gatewayID, r.ouID, "the provider's auth header changed")

	require.NoError(t, err)
	intents := r.intents.all()
	require.Len(t, intents, 1)
	assert.Equal(t, &r.gatewayID, intents[0].GatewayUUID, "adopted where its status row places it")
	assert.Equal(t, models.DeployIntentDone, intents[0].State)
	created := r.deployments.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	assert.Equal(t, "agent-dev-deployment", created[0].Deployment.Name, "keeps its deployment's name")
	var event models.LLMProxyDeploymentEvent
	r.payload(t, &event)
	assert.Equal(t, r.handle, event.ProxyID)

	r.proxy.Configuration.Provider = ""
	err = r.svc.RedeployStale(context.Background(), r.proxy, r.gatewayID, r.ouID, "again")
	assert.ErrorIs(t, err, utils.ErrInvalidInput, "the caller hears the attempt failed")
	assert.Equal(t, models.DeployIntentFailed, r.intents.all()[0].State, "and readers see it FAILED")
}

func TestLLMProxyKindEventsKeepTheirWireShape(t *testing.T) {
	kind := NewLLMProxyKind(nil, nil, nil, nil, nil)
	gateway := uuid.New()
	performedAt := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	intent, err := llmIntent(models.KindLLMProxy, "org-1", uuid.New(), models.GatewayScope(gateway), models.DeploymentStatusDeployed,
		llmIntentData{Handle: "my-proxy", Vhost: "v.example.com"})
	require.NoError(t, err)
	ref := reconcile.EventRef{Intent: intent, GatewayID: gateway.String(), DeploymentID: "dep-1", PerformedAt: performedAt}

	deploy := kind.DeployEvent(ref)
	assert.Equal(t, "llmproxy.deployed", deploy.Type)
	assert.Equal(t, "my-proxy", deploy.EntityID)
	assert.Equal(t, &models.LLMProxyDeploymentEvent{
		ProxyID: "my-proxy", DeploymentID: "dep-1", PerformedAt: performedAt, Vhost: "v.example.com", Environment: "production",
		GatewayID: gateway.String(), OrganizationID: "org-1", Status: "DEPLOYED",
	}, deploy.Payload)

	undeploy := kind.UndeployEvent(ref)
	assert.Equal(t, "llmproxy.undeployed", undeploy.Type)
	assert.Equal(t, "DELETE", undeploy.Action)
	assert.Equal(t, &models.LLMProxyUndeploymentEvent{
		ProxyID: "my-proxy", DeploymentID: "dep-1", PerformedAt: performedAt, Vhost: "v.example.com", Environment: "production",
		GatewayID: gateway.String(), OrganizationID: "org-1",
	}, undeploy.Payload)

	deleted := kind.DeleteEvent(ref)
	assert.Equal(t, "llmproxy.deleted", deleted.Type)
	assert.Equal(t, &models.LLMProxyDeletionEvent{ProxyID: "my-proxy"}, deleted.Payload)
}
