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

// deployableProvider is a provider whose current configuration renders.
func deployableProvider(id uuid.UUID) *models.LLMProvider {
	return &models.LLMProvider{
		UUID: id, TemplateHandle: "openai", Artifact: &models.Artifact{Handle: "openai-provider"},
		Configuration: models.LLMProviderConfig{
			Name: "OpenAI", Version: "v1.0",
			Upstream: &models.UpstreamConfig{Main: &models.UpstreamEndpoint{URL: "https://api.openai.com"}},
		},
	}
}

func TestLLMProviderDeployOfTheCurrentConfigRecordsAGatewayIntent(t *testing.T) {
	r := newLifecycleRig()
	r.deployments.GetDeployedGatewaysByProviderFunc = func(uuid.UUID, string) ([]string, error) { return nil, nil }
	r.deployments.CreateWithLimitEnforcementFunc = storesDeployment
	svc := r.providerService()
	provider := deployableProvider(r.artifactUUID)
	svc.providerRepo.(*repomocks.LLMProviderRepositoryMock).GetByUUIDFunc = func(string, string) (*models.LLMProvider, error) {
		return provider, nil
	}

	got, err := svc.DeployLLMProvider(r.handle, &models.DeployAPIRequest{
		Name: "dep-1", Base: "current", GatewayID: r.gatewayID.String(), Metadata: map[string]interface{}{"k": "v"},
	}, r.ouID)

	require.NoError(t, err)
	assert.Equal(t, "dep-1", got.Name)
	assert.Contains(t, string(got.Content), "kind: LlmProvider")
	assert.Equal(t, map[string]interface{}{"k": "v"}, got.Metadata)
	intents := r.intents.all()
	require.Len(t, intents, 1)
	assert.Equal(t, models.KindLLMProvider, intents[0].Kind)
	assert.Equal(t, &r.gatewayID, intents[0].GatewayUUID, "the route names a gateway, not an environment")
	assert.Equal(t, models.DeployIntentDone, intents[0].State)
	data, err := decodeLLMIntentData(intents[0])
	require.NoError(t, err)
	assert.Equal(t, "dep-1", data.Name, "kept for a later redeploy")
	var event models.LLMProviderDeploymentEvent
	evt := r.payload(t, &event)
	assert.Equal(t, "llmprovider.deployed", string(evt.EventType))
	assert.Equal(t, got.DeploymentID.String(), event.DeploymentID)
}

func TestLLMProviderDeployOfAnInvalidConfigFailsWithNothingWritten(t *testing.T) {
	r := newLifecycleRig()
	r.deployments.GetDeployedGatewaysByProviderFunc = func(uuid.UUID, string) ([]string, error) { return nil, nil }
	svc := r.providerService()
	provider := deployableProvider(r.artifactUUID)
	provider.Configuration.Upstream = nil
	svc.providerRepo.(*repomocks.LLMProviderRepositoryMock).GetByUUIDFunc = func(string, string) (*models.LLMProvider, error) {
		return provider, nil
	}

	_, err := svc.DeployLLMProvider(r.handle, &models.DeployAPIRequest{
		Name: "dep-1", Base: "current", GatewayID: r.gatewayID.String(),
	}, r.ouID)

	assert.ErrorIs(t, err, utils.ErrInvalidInput, "a 400, as before")
	assert.Empty(t, r.deployments.CreateWithLimitEnforcementCalls())
	assert.Empty(t, r.hub.published)
	for _, intent := range r.intents.all() {
		assert.NotEqual(t, models.DeployIntentDone, intent.State, "the transaction rolls the intent back")
	}
}

func TestLLMProviderUndeployAndRestoreRecordTheGatewayIntent(t *testing.T) {
	r := newLifecycleRig()
	r.withDeployment(models.DeploymentStatusDeployed, models.DeploymentStatusDeployed)
	r.beginsUndeploy(r.deploymentID.String())
	svc := r.providerService()

	_, err := svc.UndeployLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
	require.NoError(t, err)
	intents := r.intents.all()
	require.Len(t, intents, 1)
	assert.Equal(t, models.DeploymentStatusUndeployed, intents[0].Desired)
	assert.Equal(t, &r.gatewayID, intents[0].GatewayUUID)

	r.withDeployment(models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed)
	r.deployments.SetCurrentCtxFunc = func(context.Context, string, string, string, string, models.DeploymentStatus) (time.Time, error) {
		return r.performedAt, nil
	}
	_, err = svc.RestoreLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
	require.NoError(t, err)
	intents = r.intents.all()
	require.Len(t, intents, 1, "the same intent, now to deploy")
	assert.Equal(t, models.DeploymentStatusDeployed, intents[0].Desired)
	assert.Equal(t, models.DeployIntentDone, intents[0].State)
}

func TestLLMProviderDeploymentsShowAnIntentThatGaveUp(t *testing.T) {
	r := newLifecycleRig()
	deployed, desired := models.DeploymentStatusDeployed, models.DeploymentStatusDeployed
	current := &models.Deployment{DeploymentID: r.deploymentID, GatewayUUID: r.gatewayID, Status: &deployed, StatusDesired: &desired}
	r.deployments.GetDeploymentsWithStateFunc = func(string, string, *string, *string, int) ([]*models.Deployment, error) {
		return []*models.Deployment{current}, nil
	}
	svc := r.providerService()
	intent, err := llmIntent(models.KindLLMProvider, r.ouID, r.artifactUUID, models.GatewayScope(r.gatewayID),
		models.DeploymentStatusDeployed, llmIntentData{})
	require.NoError(t, err)
	require.NoError(t, r.intents.Upsert(context.Background(), &intent))
	require.NoError(t, r.intents.MarkFailed(context.Background(), intent, "the provider's configuration is invalid"))

	got, err := svc.GetLLMProviderDeployments(r.handle, r.ouID, nil, nil)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, models.DeploymentStatusFailed, *got[0].Status)
	assert.Equal(t, "the provider's configuration is invalid", *got[0].StatusReason)
}

func TestLLMProviderKindRender(t *testing.T) {
	id := uuid.New()
	gateway := uuid.New()
	provider := deployableProvider(id)
	providers := &repomocks.LLMProviderRepositoryMock{
		GetByUUIDFunc: func(string, string) (*models.LLMProvider, error) { return provider, nil },
	}
	kind := NewLLMProviderKind(providers)
	intent, err := llmIntent(models.KindLLMProvider, "org-1", id, models.GatewayScope(gateway), models.DeploymentStatusDeployed, llmIntentData{})
	require.NoError(t, err)

	targets, err := kind.Targets(context.Background(), intent)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{gateway}, targets)
	rendered, err := kind.Render(context.Background(), intent, gateway)
	require.NoError(t, err)
	assert.Equal(t, "openai-provider-deployment", rendered.Name, "named after the provider without a request's name")

	provider.TemplateHandle = ""
	_, err = kind.Render(context.Background(), intent, gateway)
	assert.ErrorIs(t, err, reconcile.ErrPermanent, "retrying an invalid configuration cannot help")
	assert.ErrorIs(t, err, utils.ErrLLMProviderTemplateNotFound)

	provider = nil
	_, err = kind.Render(context.Background(), intent, gateway)
	assert.ErrorIs(t, err, reconcile.ErrPermanent)
}

func TestLLMProviderKindEventsKeepTheirWireShape(t *testing.T) {
	kind := NewLLMProviderKind(nil)
	id, gateway := uuid.New(), uuid.New()
	performedAt := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	ref := reconcile.EventRef{
		Intent: models.DeployIntent{OUID: "org-1", ArtifactUUID: id}, GatewayID: gateway.String(),
		DeploymentID: "dep-1", PerformedAt: performedAt,
	}

	deploy := kind.DeployEvent(ref)
	assert.Equal(t, "llmprovider.deployed", deploy.Type)
	assert.Equal(t, "CREATE", deploy.Action)
	assert.Equal(t, id.String(), deploy.EntityID)
	assert.Equal(t, &models.LLMProviderDeploymentEvent{
		ProviderID: id.String(), DeploymentID: "dep-1", PerformedAt: performedAt, GatewayID: gateway.String(),
		OrganizationID: "org-1", Status: "DEPLOYED",
	}, deploy.Payload)

	undeploy := kind.UndeployEvent(ref)
	assert.Equal(t, "llmprovider.undeployed", undeploy.Type)
	assert.Equal(t, "DELETE", undeploy.Action)
	assert.Equal(t, &models.LLMProviderUndeploymentEvent{
		ProviderID: id.String(), DeploymentID: "dep-1", PerformedAt: performedAt, GatewayID: gateway.String(),
		OrganizationID: "org-1",
	}, undeploy.Payload)

	deleted := kind.DeleteEvent(ref)
	assert.Equal(t, "llmprovider.deleted", deleted.Type)
	assert.Equal(t, &models.LLMProviderDeletionEvent{ProviderID: id.String()}, deleted.Payload)
}
