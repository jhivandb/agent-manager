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
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/orgctx"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const testKind = "Widget"

// fakeKind renders "kind: Widget" for every target and records what it was asked.
type fakeKind struct {
	mu        sync.Mutex
	targets   []uuid.UUID
	targetErr error
	renderErr error
	rendered  int
	describe  func(error) string
}

func (k *fakeKind) Name() string { return testKind }

func (k *fakeKind) Targets(context.Context, models.DeployIntent) ([]uuid.UUID, error) {
	return k.targets, k.targetErr
}

func (k *fakeKind) Render(_ context.Context, intent models.DeployIntent, gatewayID uuid.UUID) (Rendered, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rendered++
	if k.renderErr != nil {
		return Rendered{}, k.renderErr
	}
	return Rendered{Name: "widget-" + gatewayID.String()[:4], Content: []byte("kind: Widget\n")}, nil
}

func (k *fakeKind) DeployEvent(ref EventRef) Event {
	return Event{Type: "widget.deployed", Action: "CREATE", EntityID: ref.Intent.ArtifactUUID.String(), Payload: ref}
}

func (k *fakeKind) UndeployEvent(ref EventRef) Event {
	return Event{Type: "widget.undeployed", Action: "DELETE", EntityID: ref.Intent.ArtifactUUID.String(), Payload: ref}
}

func (k *fakeKind) DeleteEvent(ref EventRef) Event {
	return Event{Type: "widget.deleted", Action: "DELETE", EntityID: ref.Intent.ArtifactUUID.String(), Payload: ref}
}

// hookedKind is a fakeKind with every optional hook.
type hookedKind struct {
	*fakeKind
	mu       sync.Mutex
	live     []uuid.UUID
	failed   []string
	drifted  map[uuid.UUID]string
	liveOrgs []string
}

func (k *hookedKind) AfterLive(ctx context.Context, _ models.DeployIntent, _, deploymentID uuid.UUID) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.live = append(k.live, deploymentID)
	org, _ := orgctx.GetResolvedOrg(ctx)
	k.liveOrgs = append(k.liveOrgs, org.OUID)
}

func (k *hookedKind) AfterFailed(_ context.Context, _ models.DeployIntent, lastErr string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.failed = append(k.failed, lastErr)
}

func (k *hookedKind) Drifted(_ context.Context, intent models.DeployIntent) (string, bool) {
	reason, ok := k.drifted[intent.ID]
	return reason, ok
}

func (k *hookedKind) DescribeError(err error) string {
	if k.describe != nil {
		return k.describe(err)
	}
	return "described: " + err.Error()
}

type publishedEvent struct {
	gatewayID string
	event     Event
}

type fakePublisher struct {
	mu     sync.Mutex
	events []publishedEvent
	err    error
}

func (p *fakePublisher) PublishGatewayEvent(_ context.Context, gatewayID string, event Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, publishedEvent{gatewayID, event})
	return p.err
}

// harness is a Core over mocks: one widget intent, pending, with one target gateway.
type harness struct {
	intents     *repomocks.DeployIntentRepositoryMock
	deployments *repomocks.DeploymentRepositoryMock
	publisher   *fakePublisher
	kind        *fakeKind
	core        *Core
	intent      models.DeployIntent
	gateway     uuid.UUID
	now         time.Time
	txs         int
}

var txSentinel = &gorm.DB{}

func newHarness(t *testing.T, kinds ...Kind) *harness {
	t.Helper()
	env := uuid.New()
	h := &harness{
		gateway: uuid.New(),
		now:     time.Now().Truncate(time.Second),
		intent: models.DeployIntent{
			ID: uuid.New(), OUID: "ou-1", Kind: testKind, ArtifactUUID: uuid.New(), EnvironmentUUID: &env,
			Desired: models.DeploymentStatusDeployed, State: models.DeployIntentPending,
			UpdatedAt: time.Date(2026, 10, 28, 11, 0, 0, 0, time.UTC),
		},
		publisher: &fakePublisher{},
	}
	h.kind = &fakeKind{targets: []uuid.UUID{h.gateway}}
	h.intents = &repomocks.DeployIntentRepositoryMock{}
	h.intents.WithTxFunc = func(tx *gorm.DB) repositories.DeployIntentRepository {
		assert.Same(t, txSentinel, tx)
		return h.intents
	}
	h.intents.MarkDoneFunc = func(_ context.Context, read models.DeployIntent) (models.DeployIntent, error) {
		done := read
		done.State, done.UpdatedAt = models.DeployIntentDone, read.UpdatedAt.Add(time.Second)
		return done, nil
	}
	h.intents.MarkPublishedFunc = func(context.Context, models.DeployIntent) error { return nil }
	h.intents.GetFunc = func(context.Context, uuid.UUID) (*models.DeployIntent, error) { return &h.intent, nil }
	h.intents.MarkWaitingFunc = func(context.Context, models.DeployIntent, string, time.Time) error { return nil }
	h.intents.MarkAttemptFailedFunc = func(context.Context, models.DeployIntent, string, time.Time) error { return nil }
	h.intents.MarkFailedFunc = func(context.Context, models.DeployIntent, string) error { return nil }
	h.deployments = &repomocks.DeploymentRepositoryMock{}
	h.deployments.WithTxFunc = func(tx *gorm.DB) repositories.DeploymentRepository {
		assert.Same(t, txSentinel, tx)
		return h.deployments
	}
	h.deployments.CreateWithLimitEnforcementFunc = func(d *models.Deployment, _ int) error {
		performedAt := h.now
		d.PerformedAt = &performedAt
		return nil
	}
	if len(kinds) == 0 {
		kinds = []Kind{h.kind}
	}
	tx := func(_ context.Context, fn func(tx *gorm.DB) error) error {
		h.txs++
		return fn(txSentinel)
	}
	h.core = NewCore(tx, h.intents, h.deployments, h.publisher, DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), kinds...)
	h.core.now = func() time.Time { return h.now }
	return h
}

func (h *harness) hooked() *hookedKind {
	return &hookedKind{fakeKind: h.kind, drifted: map[uuid.UUID]string{}}
}

func TestReconcileWritesADeployingDeploymentPerTargetThenSendsItsEvent(t *testing.T) {
	h := newHarness(t)
	second := uuid.New()
	h.kind.targets = []uuid.UUID{h.gateway, second}

	require.NoError(t, h.core.Reconcile(context.Background(), h.intent))

	require.Len(t, h.intents.MarkDoneCalls(), 1)
	assert.Equal(t, h.intent, h.intents.MarkDoneCalls()[0].Read, "the intent as read guards the write")
	created := h.deployments.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 2)
	assert.Equal(t, 1, h.txs, "the deployments and the intent commit together")
	for i, gw := range []uuid.UUID{h.gateway, second} {
		d := created[i].Deployment
		assert.Equal(t, gw, d.GatewayUUID)
		assert.Equal(t, h.intent.ArtifactUUID, d.ArtifactUUID)
		assert.Equal(t, "ou-1", d.OUID)
		assert.Equal(t, "kind: Widget\n", string(d.Content))
		assert.Equal(t, "widget-"+gw.String()[:4], d.Name)
		assert.Nil(t, d.Status, "the repository starts it DEPLOYING")
		assert.Equal(t, 25, created[i].HardLimit)
	}

	require.Len(t, h.publisher.events, 2)
	for i, e := range h.publisher.events {
		assert.Equal(t, created[i].Deployment.GatewayUUID.String(), e.gatewayID)
		assert.Equal(t, "widget.deployed", e.event.Type)
		ref := e.event.Payload.(EventRef)
		assert.Equal(t, created[i].Deployment.DeploymentID.String(), ref.DeploymentID)
		assert.True(t, h.now.Equal(ref.PerformedAt), "the event carries the row's performed_at")
		assert.Equal(t, models.DeployIntentDone, ref.Intent.State)
	}
	require.Len(t, h.intents.MarkPublishedCalls(), 1)
	assert.Equal(t, h.intent.UpdatedAt.Add(time.Second), h.intents.MarkPublishedCalls()[0].Done.UpdatedAt)
}

func TestReconcileWritesNothingForAnIntentChangedMeanwhile(t *testing.T) {
	h := newHarness(t)
	h.intents.MarkDoneFunc = func(context.Context, models.DeployIntent) (models.DeployIntent, error) {
		return models.DeployIntent{}, repositories.ErrDeployIntentSuperseded
	}

	h.core.Process(context.Background(), h.intent)

	assert.Empty(t, h.deployments.CreateWithLimitEnforcementCalls())
	assert.Empty(t, h.publisher.events)
	assert.Empty(t, h.intents.MarkAttemptFailedCalls(), "the newer intent is carried out on its own")
	assert.Empty(t, h.intents.MarkFailedCalls())
}

func TestReconcileLeavesAFailedPublishForTheWorker(t *testing.T) {
	h := newHarness(t)
	h.publisher.err = errors.New("event hub down")

	h.core.Process(context.Background(), h.intent)

	assert.Len(t, h.deployments.CreateWithLimitEnforcementCalls(), 1)
	assert.Empty(t, h.intents.MarkPublishedCalls(), "the worker sends it again")
	assert.Empty(t, h.intents.MarkAttemptFailedCalls(), "the deploy itself succeeded")
}

func TestReconcileWithdrawsWhatItSentForAnIntentTornDownMeanwhile(t *testing.T) {
	h := newHarness(t)
	h.intents.GetFunc = func(context.Context, uuid.UUID) (*models.DeployIntent, error) {
		return nil, repositories.ErrDeployIntentNotFound
	}
	h.deployments.BeginUndeployFunc = func(_ context.Context, artifactUUID, ouID, gatewayID string) (string, time.Time, error) {
		assert.Equal(t, h.gateway.String(), gatewayID)
		return "", h.now.Add(time.Second), nil
	}

	require.NoError(t, h.core.Reconcile(context.Background(), h.intent))

	require.Len(t, h.publisher.events, 2)
	assert.Equal(t, "widget.deployed", h.publisher.events[0].event.Type)
	assert.Equal(t, "widget.undeployed", h.publisher.events[1].event.Type)
	ref := h.publisher.events[1].event.Payload.(EventRef)
	assert.Empty(t, ref.DeploymentID, "never acked, so the gateway undeploys whatever it holds")
	assert.True(t, h.now.Add(time.Second).Equal(ref.PerformedAt))
}

func TestProcessWaitsUnchargedForAnInputThatIsNotReady(t *testing.T) {
	h := newHarness(t)
	h.kind.renderErr = InputNotReady("waiting for the agent's endpoint")
	h.intent.AttemptCount = 1000

	h.core.Process(context.Background(), h.intent)

	require.Len(t, h.intents.MarkWaitingCalls(), 1)
	call := h.intents.MarkWaitingCalls()[0]
	assert.Equal(t, "waiting for the agent's endpoint", call.Reason)
	assert.Equal(t, h.now.Add(25*time.Second), call.NextAttemptAt)
	assert.Empty(t, h.intents.MarkFailedCalls(), "waiting never times out, whatever the attempt count")
	assert.Empty(t, h.intents.MarkAttemptFailedCalls())
	assert.Empty(t, h.deployments.CreateWithLimitEnforcementCalls())
}

func TestProcessRetriesATransientFailureWithoutLeakingIt(t *testing.T) {
	h := newHarness(t)
	h.kind.renderErr = errors.New("dial tcp 10.0.0.5:5432: password authentication failed")

	h.core.Process(context.Background(), h.intent)

	require.Len(t, h.intents.MarkAttemptFailedCalls(), 1)
	call := h.intents.MarkAttemptFailedCalls()[0]
	assert.Equal(t, internalErrorMessage, call.LastErr)
	assert.Equal(t, h.now.Add(25*time.Second), call.NextAttemptAt)
}

func TestProcessGivesUpPastTheBudgetOrOnAPermanentFailure(t *testing.T) {
	for name, setup := range map[string]func(h *harness){
		"budget spent": func(h *harness) {
			h.kind.renderErr = errors.New("unreachable")
			h.intent.AttemptCount = DefaultConfig().AttemptBudget - 1
		},
		"permanent": func(h *harness) { h.kind.renderErr = Permanent(errors.New("not proxyable")) },
		"no target": func(h *harness) { h.kind.targets, h.intent.AttemptCount = nil, DefaultConfig().AttemptBudget-1 },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			kind := h.hooked()
			h = newHarnessWith(t, h, kind)
			setup(h)

			h.core.Process(context.Background(), h.intent)

			require.Len(t, h.intents.MarkFailedCalls(), 1)
			lastErr := h.intents.MarkFailedCalls()[0].LastErr
			assert.Contains(t, lastErr, "described: ")
			assert.Equal(t, []string{lastErr}, kind.failed, "the kind hears that it gave up")
			assert.Empty(t, h.intents.MarkAttemptFailedCalls())
		})
	}
}

// newHarnessWith rebuilds h's core over kind, keeping its mocks.
func newHarnessWith(t *testing.T, h *harness, kind Kind) *harness {
	t.Helper()
	if hk, ok := kind.(*hookedKind); ok {
		h.kind = hk.fakeKind
	}
	tx := func(_ context.Context, fn func(tx *gorm.DB) error) error {
		h.txs++
		return fn(txSentinel)
	}
	h.core = NewCore(tx, h.intents, h.deployments, h.publisher, DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), kind)
	h.core.now = func() time.Time { return h.now }
	return h
}

func TestProcessStoresAValidBoundedLastError(t *testing.T) {
	h := newHarness(t)
	kind := h.hooked()
	h = newHarnessWith(t, h, kind)
	kind.describe = func(error) string { return "bad \x00byte \xff" + string(make([]rune, 2000)) }
	h.kind.renderErr = errors.New("x")

	h.core.Process(context.Background(), h.intent)

	lastErr := h.intents.MarkAttemptFailedCalls()[0].LastErr
	assert.NotContains(t, lastErr, "\x00")
	assert.LessOrEqual(t, len([]rune(lastErr)), lastErrorMaxRunes)
}

func TestProcessBoundsTheAttemptAndActsAsTheIntentsOrg(t *testing.T) {
	h := newHarness(t)
	var org string
	var deadline time.Time
	h.intents.MarkDoneFunc = func(ctx context.Context, read models.DeployIntent) (models.DeployIntent, error) {
		resolved, _ := orgctx.GetResolvedOrg(ctx)
		org = resolved.OUID
		deadline, _ = ctx.Deadline()
		return read, nil
	}

	h.core.Process(context.Background(), h.intent)

	assert.Equal(t, "ou-1", org)
	assert.False(t, deadline.IsZero())
}

func TestReconcileUndeploysEachPlacedRow(t *testing.T) {
	h := newHarness(t)
	h.intent.Desired = models.DeploymentStatusUndeployed
	acked, unacked, gone := uuid.New(), uuid.New(), uuid.New()
	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{{GatewayUUID: acked}, {GatewayUUID: unacked}, {GatewayUUID: gone}}, nil
	}
	h.deployments.BeginUndeployFunc = func(_ context.Context, _, _, gatewayID string) (string, time.Time, error) {
		switch gatewayID {
		case acked.String():
			return "dep-1", h.now, nil
		case unacked.String():
			return "", h.now, nil
		}
		return "", time.Time{}, utils.ErrDeploymentNotActive
	}

	require.NoError(t, h.core.Reconcile(context.Background(), h.intent))

	assert.Len(t, h.intents.MarkDoneCalls(), 1)
	assert.Empty(t, h.deployments.CreateWithLimitEnforcementCalls())
	require.Len(t, h.publisher.events, 2)
	for i, want := range []struct{ gateway, id string }{{acked.String(), "dep-1"}, {unacked.String(), ""}} {
		assert.Equal(t, "widget.undeployed", h.publisher.events[i].event.Type)
		assert.Equal(t, want.gateway, h.publisher.events[i].gatewayID)
		assert.Equal(t, want.id, h.publisher.events[i].event.Payload.(EventRef).DeploymentID)
	}
}

func TestWantRecordsTheIntentInTheCallersTransaction(t *testing.T) {
	h := newHarness(t)
	h.intents.UpsertFunc = func(context.Context, *models.DeployIntent) error { return nil }
	intent := h.intent

	require.NoError(t, h.core.Want(context.Background(), txSentinel, &intent))
	assert.Len(t, h.intents.WithTxCalls(), 1)
	assert.Len(t, h.intents.UpsertCalls(), 1)

	intent.Kind = "Unknown"
	assert.Error(t, h.core.Want(context.Background(), nil, &intent))
}

func TestStatusOfAnIntent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	newer := models.DeploymentStatusRecord{Status: models.DeploymentStatusFailed, StatusReason: models.DeploymentReasonTimeout}
	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{newer, {Status: models.DeploymentStatusDeployed}}, nil
	}

	for _, tc := range []struct {
		name    string
		state   models.DeployIntentState
		desired models.DeploymentStatus
		want    Status
	}{
		{
			"waiting", models.DeployIntentPending, models.DeploymentStatusDeployed,
			Status{models.DeploymentStatusDeploying, "waiting"},
		},
		{
			"undeploy pending", models.DeployIntentPending, models.DeploymentStatusUndeployed,
			Status{models.DeploymentStatusUndeploying, "waiting"},
		},
		{
			"gave up", models.DeployIntentFailed, models.DeploymentStatusDeployed,
			Status{models.DeploymentStatusFailed, "waiting"},
		},
		{
			"done: the newest row", models.DeployIntentDone, models.DeploymentStatusDeployed,
			Status{models.DeploymentStatusFailed, models.DeploymentReasonTimeout},
		},
	} {
		intent := h.intent
		intent.State, intent.Desired, intent.LastError = tc.state, tc.desired, "waiting"
		got, err := h.core.Status(ctx, intent)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, tc.name)
	}

	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return nil, nil
	}
	intent := h.intent
	intent.State = models.DeployIntentDone
	got, err := h.core.Status(ctx, intent)
	require.NoError(t, err)
	assert.Equal(t, Status{Status: models.DeploymentStatusUndeployed}, got, "done, but its deployments are gone")
}

func TestMarkStaleRequeuesAndRedeploys(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	done := h.intent
	done.State = models.DeployIntentDone
	h.intents.GetByScopeFunc = func(context.Context, string, uuid.UUID, models.DeployScope) (*models.DeployIntent, error) {
		return &done, nil
	}
	h.intents.RequeueFunc = func(context.Context, models.DeployIntent) error { return nil }

	require.NoError(t, h.core.MarkStale(ctx, testKind, "ou-1", h.intent.ArtifactUUID, done.Scope(), "the upstream moved"))

	require.Len(t, h.intents.RequeueCalls(), 1)
	assert.Equal(t, done, h.intents.RequeueCalls()[0].Read)
	assert.Len(t, h.deployments.CreateWithLimitEnforcementCalls(), 1, "re-rendered at once")

	assert.Error(t, h.core.MarkStale(ctx, "Other", "ou-1", h.intent.ArtifactUUID, done.Scope(), "x"))

	done.Desired = models.DeploymentStatusUndeployed
	require.NoError(t, h.core.MarkStale(ctx, testKind, "ou-1", h.intent.ArtifactUUID, done.Scope(), "x"))
	assert.Len(t, h.intents.RequeueCalls(), 1, "an undeployed artifact is not redeployed")

	h.intents.GetByScopeFunc = func(context.Context, string, uuid.UUID, models.DeployScope) (*models.DeployIntent, error) {
		return nil, repositories.ErrDeployIntentNotFound
	}
	assert.ErrorIs(t, h.core.MarkStale(ctx, testKind, "ou-1", h.intent.ArtifactUUID, done.Scope(), "x"),
		repositories.ErrDeployIntentNotFound)
}
