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

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// upsertAsStored makes the harness's Upsert return the intent as stored: pending, with an id.
func (h *harness) upsertAsStored() {
	h.intents.UpsertFunc = func(_ context.Context, intent *models.DeployIntent) error {
		intent.ID, intent.State, intent.UpdatedAt = h.intent.ID, models.DeployIntentPending, h.intent.UpdatedAt
		return nil
	}
}

func TestApplyWritesTheIntentAndItsDeploymentsInTheCallersTransaction(t *testing.T) {
	h := newHarness(t)
	h.upsertAsStored()
	intent := h.intent

	applied, err := h.core.Apply(context.Background(), txSentinel, &intent, nil)

	require.NoError(t, err)
	assert.Zero(t, h.txs, "the caller's transaction, not one of its own")
	require.Len(t, h.intents.UpsertCalls(), 1)
	require.Len(t, h.intents.MarkDoneCalls(), 1)
	require.Len(t, applied.Deployments, 1)
	d := applied.Deployments[0]
	assert.Equal(t, h.gateway, d.GatewayUUID)
	assert.Equal(t, "kind: Widget\n", string(d.Content))
	assert.Equal(t, models.DeployIntentDone, applied.Intent.State)
	assert.Empty(t, h.publisher.events, "nothing is sent before the caller commits")

	applied.Publish(context.Background())

	require.Len(t, h.publisher.events, 1)
	ref := h.publisher.events[0].event.Payload.(EventRef)
	assert.Equal(t, d.DeploymentID.String(), ref.DeploymentID)
	assert.True(t, h.now.Equal(ref.PerformedAt))
	assert.Len(t, h.intents.MarkPublishedCalls(), 1)
}

func TestApplyOpensItsOwnTransactionWithoutTheCallers(t *testing.T) {
	h := newHarness(t)
	h.upsertAsStored()
	intent := h.intent

	_, err := h.core.Apply(context.Background(), nil, &intent, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, h.txs)
}

func TestApplyDeploysTheOverride(t *testing.T) {
	h := newHarness(t)
	h.upsertAsStored()
	intent := h.intent
	base := uuid.New()

	applied, err := h.core.Apply(context.Background(), txSentinel, &intent, &Override{
		Name: "my-deploy", Content: []byte("kind: Widget\nold: true\n"), BaseDeploymentID: &base,
		Metadata: map[string]interface{}{"team": "a"},
	})

	require.NoError(t, err)
	assert.Zero(t, h.kind.rendered, "a deploy of earlier content is not rendered")
	d := applied.Deployments[0]
	assert.Equal(t, "my-deploy", d.Name)
	assert.Equal(t, "kind: Widget\nold: true\n", string(d.Content))
	assert.Equal(t, &base, d.BaseDeploymentID)
	assert.Equal(t, map[string]interface{}{"team": "a"}, d.Metadata)

	h = newHarness(t)
	h.upsertAsStored()
	intent = h.intent
	applied, err = h.core.Apply(context.Background(), txSentinel, &intent, &Override{Name: "renamed"})
	require.NoError(t, err)
	assert.Equal(t, 1, h.kind.rendered)
	assert.Equal(t, "renamed", applied.Deployments[0].Name)
	assert.Equal(t, "kind: Widget\n", string(applied.Deployments[0].Content))
}

func TestApplyWritesNothingWhenTheArtifactCannotBeRendered(t *testing.T) {
	invalid := errors.New("invalid security config")
	for name, setup := range map[string]func(h *harness){
		"render":    func(h *harness) { h.kind.renderErr = Permanent(invalid) },
		"targets":   func(h *harness) { h.kind.targetErr = invalid },
		"no target": func(h *harness) { h.kind.targets = nil },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.upsertAsStored()
			setup(h)
			intent := h.intent

			applied, err := h.core.Apply(context.Background(), txSentinel, &intent, nil)

			require.Error(t, err)
			assert.Nil(t, applied)
			if name != "no target" {
				assert.ErrorIs(t, err, invalid, "the caller maps the kind's error")
			}
			assert.Empty(t, h.intents.MarkDoneCalls())
			assert.Empty(t, h.deployments.CreateWithLimitEnforcementCalls())
		})
	}
}

func TestApplyLeavesAnIntentWaitingForAnInputPending(t *testing.T) {
	h := newHarness(t)
	h.upsertAsStored()
	h.kind.renderErr = InputNotReady("waiting for the agent's endpoint")
	intent := h.intent

	applied, err := h.core.Apply(context.Background(), txSentinel, &intent, nil)

	require.NoError(t, err)
	assert.Equal(t, "waiting for the agent's endpoint", applied.Waiting)
	assert.Empty(t, applied.Deployments)
	require.Len(t, h.intents.MarkWaitingCalls(), 1)
	assert.Empty(t, h.intents.MarkDoneCalls())
	applied.Publish(context.Background())
	assert.Empty(t, h.publisher.events)
}

func TestApplyUndeploysEachPlacedRow(t *testing.T) {
	h := newHarness(t)
	h.upsertAsStored()
	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{{GatewayUUID: h.gateway}}, nil
	}
	h.deployments.BeginUndeployFunc = func(context.Context, string, string, string) (string, time.Time, error) {
		return "dep-1", h.now, nil
	}
	intent := h.intent
	intent.Desired = models.DeploymentStatusUndeployed

	applied, err := h.core.Apply(context.Background(), txSentinel, &intent, nil)
	require.NoError(t, err)
	assert.Empty(t, h.publisher.events)
	applied.Publish(context.Background())

	require.Len(t, h.publisher.events, 1)
	assert.Equal(t, "widget.undeployed", h.publisher.events[0].event.Type)
	assert.Equal(t, "dep-1", h.publisher.events[0].event.Payload.(EventRef).DeploymentID)
}

func TestRestorePointsTheRowAtTheChosenDeployment(t *testing.T) {
	h := newHarness(t)
	h.upsertAsStored()
	older := uuid.New()
	h.deployments.SetCurrentCtxFunc = func(_ context.Context, artifactUUID, ouID, gatewayID, deploymentID string, status models.DeploymentStatus) (time.Time, error) {
		return h.now, nil
	}
	intent := h.intent

	applied, err := h.core.Restore(context.Background(), nil, &intent, h.gateway, older)

	require.NoError(t, err)
	assert.Equal(t, 1, h.txs)
	assert.Zero(t, h.kind.rendered, "a restore sends the older content as is")
	require.Len(t, h.deployments.SetCurrentCtxCalls(), 1)
	call := h.deployments.SetCurrentCtxCalls()[0]
	assert.Equal(t, older.String(), call.DeploymentID)
	assert.Equal(t, h.gateway.String(), call.GatewayID)
	assert.Equal(t, models.DeploymentStatusDeploying, call.Status)
	assert.Len(t, h.intents.MarkDoneCalls(), 1)

	applied.Publish(context.Background())
	require.Len(t, h.publisher.events, 1)
	ref := h.publisher.events[0].event.Payload.(EventRef)
	assert.Equal(t, "widget.deployed", h.publisher.events[0].event.Type)
	assert.Equal(t, older.String(), ref.DeploymentID)
	assert.True(t, h.now.Equal(ref.PerformedAt))
}

// adoptingKind fills in the kind data of an intent the core adopts.
type adoptingKind struct{ *fakeKind }

func (adoptingKind) Adopt(_ context.Context, intent *models.DeployIntent) error {
	intent.KindData = json.RawMessage(`{"handle":"w"}`)
	return nil
}

func TestMarkStaleAdoptsADeploymentThatPredatesIntents(t *testing.T) {
	h := newHarness(t)
	h = newHarnessWith(t, h, adoptingKind{h.kind})
	ctx := context.Background()
	scope := models.GatewayScope(h.gateway)
	h.intents.GetByScopeFunc = func(context.Context, string, uuid.UUID, models.DeployScope) (*models.DeployIntent, error) {
		return nil, repositories.ErrDeployIntentNotFound
	}
	h.intents.StatusRowsFunc = func(_ context.Context, probe models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		assert.Equal(t, scope, probe.Scope())
		return []models.DeploymentStatusRecord{{GatewayUUID: h.gateway, StatusDesired: models.DeploymentStatusDeployed}}, nil
	}
	var adopted models.DeployIntent
	h.intents.UpsertFunc = func(_ context.Context, intent *models.DeployIntent) error {
		adopted = *intent
		intent.ID, intent.State, intent.UpdatedAt = h.intent.ID, models.DeployIntentPending, h.intent.UpdatedAt
		return nil
	}
	h.intents.RequeueFunc = func(context.Context, models.DeployIntent) error { return nil }

	require.NoError(t, h.core.MarkStale(ctx, testKind, "ou-1", h.intent.ArtifactUUID, scope, "provider auth changed"))

	assert.Equal(t, testKind, adopted.Kind)
	assert.Equal(t, models.DeploymentStatusDeployed, adopted.Desired)
	assert.Equal(t, &h.gateway, adopted.GatewayUUID)
	assert.JSONEq(t, `{"handle":"w"}`, string(adopted.KindData))
	assert.Len(t, h.deployments.CreateWithLimitEnforcementCalls(), 1, "re-rendered at once")
}

func TestMarkStaleFindsNoIntentWhereNothingIsPlaced(t *testing.T) {
	h := newHarness(t)
	h.intents.GetByScopeFunc = func(context.Context, string, uuid.UUID, models.DeployScope) (*models.DeployIntent, error) {
		return nil, repositories.ErrDeployIntentNotFound
	}
	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{{GatewayUUID: h.gateway, StatusDesired: models.DeploymentStatusUndeployed}}, nil
	}

	err := h.core.MarkStale(context.Background(), testKind, "ou-1", h.intent.ArtifactUUID, models.GatewayScope(h.gateway), "x")

	assert.ErrorIs(t, err, repositories.ErrDeployIntentNotFound)
	assert.Empty(t, h.intents.UpsertCalls())
}

func TestMarkStaleReturnsTheFailedAttempt(t *testing.T) {
	h := newHarness(t)
	done := h.intent
	done.State = models.DeployIntentDone
	h.intents.GetByScopeFunc = func(context.Context, string, uuid.UUID, models.DeployScope) (*models.DeployIntent, error) {
		return &done, nil
	}
	h.intents.RequeueFunc = func(context.Context, models.DeployIntent) error { return nil }
	h.kind.renderErr = Permanent(utils.ErrInvalidInput)

	err := h.core.MarkStale(context.Background(), testKind, "ou-1", h.intent.ArtifactUUID, done.Scope(), "x")

	assert.ErrorIs(t, err, utils.ErrInvalidInput)
	assert.Len(t, h.intents.MarkFailedCalls(), 1, "and records it")

	h.kind.renderErr = InputNotReady("waiting")
	assert.NoError(t, h.core.MarkStale(context.Background(), testKind, "ou-1", h.intent.ArtifactUUID, done.Scope(), "x"),
		"waiting is not a failure")
}

func TestScopeForFindsTheIntentPlacingTheArtifactOnTheGateway(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	env, other := uuid.New(), uuid.New()
	envIntent := models.DeployIntent{ID: uuid.New(), Kind: testKind, EnvironmentUUID: &env}
	gwIntent := models.DeployIntent{ID: uuid.New(), Kind: testKind, GatewayUUID: &other}
	h.intents.ListFunc = func(_ context.Context, filter repositories.DeployIntentFilter) ([]models.DeployIntent, error) {
		assert.Equal(t, testKind, filter.Kind)
		assert.Equal(t, h.intent.ArtifactUUID, *filter.ArtifactUUID)
		return []models.DeployIntent{gwIntent, envIntent}, nil
	}
	h.intents.StatusRowsFunc = func(_ context.Context, intent models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{{GatewayUUID: h.gateway}}, nil
	}

	scope, found, err := h.core.ScopeFor(ctx, testKind, "ou-1", h.intent.ArtifactUUID, h.gateway)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, models.EnvironmentScope(env), scope, "the environment intent placed it there")

	scope, found, err = h.core.ScopeFor(ctx, testKind, "ou-1", h.intent.ArtifactUUID, other)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, models.GatewayScope(other), scope)

	fresh := uuid.New()
	scope, found, err = h.core.ScopeFor(ctx, testKind, "ou-1", h.intent.ArtifactUUID, fresh)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, models.GatewayScope(fresh), scope)
}

func TestOpenStatusesReportsIntentsNotYetHandedToTheirRows(t *testing.T) {
	h := newHarness(t)
	env := uuid.New()
	placed, gone := uuid.New(), uuid.New()
	retrying := models.DeployIntent{
		ID: uuid.New(), EnvironmentUUID: &env, Desired: models.DeploymentStatusDeployed,
		State: models.DeployIntentPending, LastError: "internal error",
	}
	failed := models.DeployIntent{
		ID: uuid.New(), GatewayUUID: &h.gateway, Desired: models.DeploymentStatusDeployed,
		State: models.DeployIntentFailed, LastError: "invalid config",
	}
	done := models.DeployIntent{ID: uuid.New(), GatewayUUID: &gone, State: models.DeployIntentDone}
	h.intents.ListFunc = func(context.Context, repositories.DeployIntentFilter) ([]models.DeployIntent, error) {
		return []models.DeployIntent{retrying, failed, done}, nil
	}
	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{
			{GatewayUUID: placed, StatusDesired: models.DeploymentStatusDeployed},
			{GatewayUUID: gone, StatusDesired: models.DeploymentStatusUndeployed},
		}, nil
	}

	open, err := h.core.OpenStatuses(context.Background(), testKind, "ou-1", h.intent.ArtifactUUID)

	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]Status{
		placed:    {models.DeploymentStatusDeploying, "internal error"},
		h.gateway: {models.DeploymentStatusFailed, "invalid config"},
	}, open)
}
