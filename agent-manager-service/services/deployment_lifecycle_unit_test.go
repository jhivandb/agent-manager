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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/eventhub"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// lifecycleRig drives the LLM provider and proxy deployment services over mocks, with one
// artifact on one gateway.
type lifecycleRig struct {
	ouID         string
	handle       string
	artifactUUID uuid.UUID
	gatewayID    uuid.UUID
	deploymentID uuid.UUID
	// performedAt is the performed_at the repository writes for every transition.
	performedAt time.Time
	deployments *repomocks.DeploymentRepositoryMock
	hub         *recordingEventHub
	// intents is what the deployment services recorded, once one is built.
	intents *memIntents
}

func newLifecycleRig() *lifecycleRig {
	return &lifecycleRig{
		ouID: "org-1", handle: "my-artifact",
		artifactUUID: uuid.New(), gatewayID: uuid.New(), deploymentID: uuid.New(),
		performedAt: time.Date(2026, 10, 28, 9, 30, 0, 123_000_000, time.UTC),
		deployments: &repomocks.DeploymentRepositoryMock{},
		hub:         &recordingEventHub{},
	}
}

func (r *lifecycleRig) providerService() *LLMProviderDeploymentService {
	provider := &models.LLMProvider{UUID: r.artifactUUID}
	providers := &repomocks.LLMProviderRepositoryMock{
		GetByUUIDFunc:   func(string, string) (*models.LLMProvider, error) { return provider, nil },
		GetByHandleFunc: func(string, string) (*models.LLMProvider, error) { return provider, nil },
	}
	gateways := &repomocks.GatewayRepositoryMock{
		GetByUUIDFunc: func(string) (*models.Gateway, error) {
			return &models.Gateway{UUID: r.gatewayID, OUID: r.ouID, GatewayFunctionalityType: models.GatewayRoleEgress}, nil
		},
	}
	events := NewGatewayEventsService(r.hub)
	kind := NewLLMProviderKind(providers)
	core, intents := newTestDeployCore(r.deployments, events, kind)
	r.intents = intents
	return NewLLMProviderDeploymentService(r.deployments, providers, nil, gateways, events, core, kind)
}

func (r *lifecycleRig) proxyService() *LLMProxyDeploymentService {
	proxy := &models.LLMProxy{UUID: r.artifactUUID}
	proxies := &repomocks.LLMProxyRepositoryMock{
		GetByIDFunc:    func(string, string) (*models.LLMProxy, error) { return proxy, nil },
		GetByIDCtxFunc: func(context.Context, string, string) (*models.LLMProxy, error) { return proxy, nil },
	}
	return NewLLMProxyDeploymentService(r.deployments, proxies, nil, nil, NewGatewayEventsService(r.hub))
}

// withDeployment makes the deployment's status row read as status with the given desired state.
func (r *lifecycleRig) withDeployment(status, desired models.DeploymentStatus) {
	deployment := &models.Deployment{
		DeploymentID: r.deploymentID, ArtifactUUID: r.artifactUUID, OUID: r.ouID, GatewayUUID: r.gatewayID,
		Status: &status, StatusDesired: &desired,
	}
	r.deployments.GetWithStateFunc = func(string, string, string) (*models.Deployment, error) { return deployment, nil }
	r.deployments.GetWithStateCtxFunc = func(context.Context, string, string, string) (*models.Deployment, error) {
		return deployment, nil
	}
	r.deployments.GetWithContentFunc = func(string, string, string) (*models.Deployment, error) { return deployment, nil }
	r.deployments.GetStatusFunc = func(string, string, string) (string, models.DeploymentStatus, *time.Time, error) {
		return r.deploymentID.String(), status, nil, nil
	}
}

// storesDeployment stands in for DeploymentRepository.CreateWithLimitEnforcement: it starts the
// new deployment's transition as the repository does.
func storesDeployment(d *models.Deployment, _ int) error {
	performedAt := models.NewPerformedAt()
	d.PerformedAt = &performedAt
	return nil
}

// beginsUndeploy makes BeginUndeploy succeed, handing the event eventDeploymentID.
func (r *lifecycleRig) beginsUndeploy(eventDeploymentID string) {
	r.deployments.BeginUndeployFunc = func(context.Context, string, string, string) (string, time.Time, error) {
		return eventDeploymentID, r.performedAt, nil
	}
}

// payload decodes the single published event's payload into v and returns the event.
func (r *lifecycleRig) payload(t *testing.T, v any) eventhub.Event {
	t.Helper()
	require.Len(t, r.hub.published, 1)
	var envelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.hub.published[0].EventData), &envelope))
	require.NoError(t, json.Unmarshal(envelope.Payload, v))
	return r.hub.published[0]
}

func TestLLMProviderDeployStartsDeployingAndNamesTheProviderByUUID(t *testing.T) {
	r := newLifecycleRig()
	r.withDeployment(models.DeploymentStatusDeployed, models.DeploymentStatusDeployed)
	r.deployments.GetDeployedGatewaysByProviderFunc = func(uuid.UUID, string) ([]string, error) { return nil, nil }
	r.deployments.CreateWithLimitEnforcementFunc = storesDeployment

	_, err := r.providerService().DeployLLMProvider(r.handle, &models.DeployAPIRequest{
		Name: "dep", Base: r.deploymentID.String(), GatewayID: r.gatewayID.String(),
	}, r.ouID)
	require.NoError(t, err)

	created := r.deployments.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	assert.Nil(t, created[0].Deployment.Status, "the repository starts it DEPLOYING")
	var event models.LLMProviderDeploymentEvent
	r.payload(t, &event)
	assert.Equal(t, r.artifactUUID.String(), event.ProviderID, "the gateway stores providers by UUID")
}

func TestLLMProviderUndeploy(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status, desired models.DeploymentStatus
		eventDeployID   string
	}{
		{"an acked deployment, by its id", models.DeploymentStatusDeployed, models.DeploymentStatusDeployed, "row-id"},
		{"a deployment still deploying, without an id", models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, ""},
		{"a failed deployment, without an id", models.DeploymentStatusFailed, models.DeploymentStatusDeployed, ""},
		{"a failed undeploy, again without an id", models.DeploymentStatusFailed, models.DeploymentStatusUndeployed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLifecycleRig()
			r.withDeployment(tc.status, tc.desired)
			r.beginsUndeploy(tc.eventDeployID)

			got, err := r.providerService().UndeployLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
			require.NoError(t, err)

			require.Len(t, r.deployments.BeginUndeployCalls(), 1)
			assert.Equal(t, r.gatewayID.String(), r.deployments.BeginUndeployCalls()[0].GatewayID)
			assert.Equal(t, models.DeploymentStatusUndeploying, *got.Status)
			var event models.LLMProviderUndeploymentEvent
			r.payload(t, &event)
			assert.Equal(t, tc.eventDeployID, event.DeploymentID)
			assert.Equal(t, r.artifactUUID.String(), event.ProviderID)
		})
	}
}

func TestLLMProviderUndeployRefusesWhatIsNotPlaced(t *testing.T) {
	for _, tc := range []struct{ status, desired models.DeploymentStatus }{
		{models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed},
		{models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed},
	} {
		r := newLifecycleRig()
		r.withDeployment(tc.status, tc.desired)
		// BeginUndeployFunc is nil: undeploying would panic.

		_, err := r.providerService().UndeployLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
		assert.ErrorIs(t, err, utils.ErrDeploymentNotActive, "%s/%s", tc.status, tc.desired)
		assert.Empty(t, r.hub.published)
	}
}

func TestLLMProviderRestore(t *testing.T) {
	t.Run("over a failed deployment, as DEPLOYING", func(t *testing.T) {
		r := newLifecycleRig()
		r.withDeployment(models.DeploymentStatusFailed, models.DeploymentStatusDeployed)
		r.deployments.SetCurrentCtxFunc = func(context.Context, string, string, string, string, models.DeploymentStatus) (time.Time, error) {
			return time.Now(), nil
		}

		got, err := r.providerService().RestoreLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
		require.NoError(t, err)

		require.Len(t, r.deployments.SetCurrentCtxCalls(), 1)
		assert.Equal(t, models.DeploymentStatusDeploying, r.deployments.SetCurrentCtxCalls()[0].Status)
		assert.Equal(t, models.DeploymentStatusDeploying, *got.Status)
		var event models.LLMProviderDeploymentEvent
		r.payload(t, &event)
		assert.Equal(t, r.artifactUUID.String(), event.ProviderID)
	})

	for _, status := range []models.DeploymentStatus{models.DeploymentStatusDeployed, models.DeploymentStatusDeploying} {
		t.Run("not over itself while "+string(status), func(t *testing.T) {
			r := newLifecycleRig()
			r.withDeployment(status, models.DeploymentStatusDeployed)

			_, err := r.providerService().RestoreLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
			assert.ErrorIs(t, err, utils.ErrDeploymentAlreadyDeployed)
		})
	}
}

func TestLLMProviderDeleteDeploymentRefusesAPlacedOne(t *testing.T) {
	for _, status := range []models.DeploymentStatus{
		models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, models.DeploymentStatusFailed,
	} {
		r := newLifecycleRig()
		r.withDeployment(status, models.DeploymentStatusDeployed)

		err := r.providerService().DeleteLLMProviderDeployment(r.handle, r.deploymentID.String(), r.ouID)
		assert.ErrorIs(t, err, utils.ErrDeploymentIsDeployed, status)
	}
}

func TestDeploymentListingsAcceptTheLifecycleStatuses(t *testing.T) {
	r := newLifecycleRig()
	r.deployments.GetDeploymentsWithStateFunc = func(string, string, *string, *string, int) ([]*models.Deployment, error) {
		return []*models.Deployment{}, nil
	}
	for _, status := range []string{"DEPLOYING", "DEPLOYED", "UNDEPLOYING", "UNDEPLOYED", "FAILED", "ARCHIVED"} {
		_, err := r.providerService().GetLLMProviderDeployments(r.handle, r.ouID, nil, &status)
		assert.NoError(t, err, status)
		_, err = r.proxyService().GetLLMProxyDeployments(r.handle, r.ouID, nil, &status)
		assert.NoError(t, err, status)
	}
	bogus := "LIVE"
	_, err := r.providerService().GetLLMProviderDeployments(r.handle, r.ouID, nil, &bogus)
	assert.ErrorIs(t, err, utils.ErrInvalidDeploymentStatus)
	_, err = r.proxyService().GetLLMProxyDeployments(r.handle, r.ouID, nil, &bogus)
	assert.ErrorIs(t, err, utils.ErrInvalidDeploymentStatus)
}

func TestLLMProxyUndeployCarriesTheDeploymentID(t *testing.T) {
	for _, tc := range []struct {
		status, desired models.DeploymentStatus
		eventDeployID   string
	}{
		{models.DeploymentStatusDeployed, models.DeploymentStatusDeployed, "row-id"},
		{models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, ""},
		{models.DeploymentStatusFailed, models.DeploymentStatusDeployed, ""},
		{models.DeploymentStatusFailed, models.DeploymentStatusUndeployed, ""},
	} {
		r := newLifecycleRig()
		r.withDeployment(tc.status, tc.desired)
		r.beginsUndeploy(tc.eventDeployID)

		got, err := r.proxyService().UndeployLLMProxyDeployment(context.Background(), r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
		require.NoError(t, err, tc.status)

		assert.Equal(t, models.DeploymentStatusUndeploying, *got.Status)
		var event models.LLMProxyUndeploymentEvent
		evt := r.payload(t, &event)
		assert.Equal(t, tc.eventDeployID, event.DeploymentID, tc.status)
		assert.Equal(t, r.handle, event.ProxyID, "proxies stay keyed by handle")
		assert.Equal(t, r.handle, evt.EntityID)
	}
}

func TestLLMProxyRestoreStartsDeploying(t *testing.T) {
	r := newLifecycleRig()
	r.withDeployment(models.DeploymentStatusFailed, models.DeploymentStatusDeployed)
	r.deployments.SetCurrentFunc = func(string, string, string, string, models.DeploymentStatus) (time.Time, error) {
		return time.Now(), nil
	}

	got, err := r.proxyService().RestoreLLMProxyDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
	require.NoError(t, err)
	assert.Equal(t, models.DeploymentStatusDeploying, r.deployments.SetCurrentCalls()[0].Status)
	assert.Equal(t, models.DeploymentStatusDeploying, *got.Status)
}

// Every deploy and undeploy event carries the status row's performed_at: the gateway echoes it in
// its ack, which must match the row to settle it.
func TestLLMEventsCarryTheRowPerformedAt(t *testing.T) {
	restores := func(r *lifecycleRig) {
		r.withDeployment(models.DeploymentStatusFailed, models.DeploymentStatusDeployed)
		r.deployments.SetCurrentCtxFunc = func(context.Context, string, string, string, string, models.DeploymentStatus) (time.Time, error) {
			return r.performedAt, nil
		}
		r.deployments.SetCurrentFunc = func(string, string, string, string, models.DeploymentStatus) (time.Time, error) {
			return r.performedAt, nil
		}
	}
	undeploys := func(r *lifecycleRig) {
		r.withDeployment(models.DeploymentStatusDeployed, models.DeploymentStatusDeployed)
		r.beginsUndeploy("row-id")
	}
	for _, tc := range []struct {
		name  string
		setup func(r *lifecycleRig)
		run   func(r *lifecycleRig) error
	}{
		{"provider deploy", func(r *lifecycleRig) {
			r.withDeployment(models.DeploymentStatusDeployed, models.DeploymentStatusDeployed)
			r.deployments.GetDeployedGatewaysByProviderFunc = func(uuid.UUID, string) ([]string, error) { return nil, nil }
			r.deployments.CreateWithLimitEnforcementFunc = func(d *models.Deployment, _ int) error {
				d.PerformedAt = &r.performedAt
				return nil
			}
		}, func(r *lifecycleRig) error {
			_, err := r.providerService().DeployLLMProvider(r.handle, &models.DeployAPIRequest{
				Name: "dep", Base: r.deploymentID.String(), GatewayID: r.gatewayID.String(),
			}, r.ouID)
			return err
		}},
		{"provider undeploy", undeploys, func(r *lifecycleRig) error {
			_, err := r.providerService().UndeployLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
			return err
		}},
		{"provider restore", restores, func(r *lifecycleRig) error {
			_, err := r.providerService().RestoreLLMProviderDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
			return err
		}},
		{"proxy undeploy", undeploys, func(r *lifecycleRig) error {
			_, err := r.proxyService().UndeployLLMProxyDeployment(context.Background(), r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
			return err
		}},
		{"proxy restore", restores, func(r *lifecycleRig) error {
			_, err := r.proxyService().RestoreLLMProxyDeployment(r.handle, r.deploymentID.String(), r.gatewayID.String(), r.ouID)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLifecycleRig()
			tc.setup(r)
			require.NoError(t, tc.run(r))

			var event struct {
				PerformedAt time.Time `json:"performedAt"`
			}
			r.payload(t, &event)
			assert.True(t, r.performedAt.Equal(event.PerformedAt), "event performedAt %s", event.PerformedAt)
		})
	}
}
