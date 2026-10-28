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
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// idleScans makes every worker scan but the one under test find nothing.
func (h *harness) idleScans() {
	h.intents.ClaimDueFunc = func(context.Context, time.Time, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ClaimUnackedFunc = func(context.Context, time.Time, time.Time, int) ([]models.DeployIntent, error) {
		return nil, nil
	}
	h.intents.ClaimLiveFunc = func(context.Context, []string, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ListDoneFunc = func(context.Context, string, uuid.UUID, int) ([]models.DeployIntent, error) { return nil, nil }
}

func TestWorkerProcessesTheDueIntents(t *testing.T) {
	h := newHarness(t)
	h.idleScans()
	h.intents.ClaimDueFunc = func(_ context.Context, now time.Time, limit int) ([]models.DeployIntent, error) {
		assert.Equal(t, h.now, now)
		assert.Equal(t, 50, limit)
		return []models.DeployIntent{h.intent}, nil
	}

	NewWorker(h.core).RunOnce(context.Background())

	assert.Len(t, h.deployments.CreateWithLimitEnforcementCalls(), 1)
}

func TestWorkerStopsTheBatchOnceTheLeaseBudgetIsSpent(t *testing.T) {
	h := newHarness(t)
	h.idleScans()
	h.core.cfg.BatchBudget = -time.Second
	h.intents.ClaimDueFunc = func(context.Context, time.Time, int) ([]models.DeployIntent, error) {
		return []models.DeployIntent{h.intent, h.intent}, nil
	}

	NewWorker(h.core).RunOnce(context.Background())

	assert.Empty(t, h.intents.MarkDoneCalls(), "nothing starts past the budget")
}

func TestWorkerSendsAnUnackedEventAgain(t *testing.T) {
	h := newHarness(t)
	h.idleScans()
	performedAt := h.now.Add(-time.Minute)
	deploying := uuid.New()
	undeploy := h.intent
	undeploy.ID, undeploy.Desired = uuid.New(), models.DeploymentStatusUndeployed
	h.intents.ClaimUnackedFunc = func(_ context.Context, now, olderThan time.Time, _ int) ([]models.DeployIntent, error) {
		assert.Equal(t, h.now.Add(-30*time.Second), olderThan)
		return []models.DeployIntent{h.intent, undeploy}, nil
	}
	h.intents.StatusRowsFunc = func(_ context.Context, intent models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		if intent.Desired == models.DeploymentStatusUndeployed {
			return []models.DeploymentStatusRecord{{
				GatewayUUID: h.gateway, DeploymentID: uuid.New(), Status: models.DeploymentStatusUndeploying,
				StatusDesired: models.DeploymentStatusUndeployed, PerformedAt: performedAt,
			}}, nil
		}
		return []models.DeploymentStatusRecord{
			{
				GatewayUUID: h.gateway, DeploymentID: deploying, Status: models.DeploymentStatusDeploying,
				StatusDesired: models.DeploymentStatusDeployed, PerformedAt: performedAt,
			},
			{
				GatewayUUID: uuid.New(), DeploymentID: uuid.New(), Status: models.DeploymentStatusDeployed,
				StatusDesired: models.DeploymentStatusDeployed,
			},
		}, nil
	}

	NewWorker(h.core).RunOnce(context.Background())

	require.Len(t, h.publisher.events, 2, "only the rows still waiting for an ack")
	deploy := h.publisher.events[0].event
	assert.Equal(t, "widget.deployed", deploy.Type)
	assert.Equal(t, deploying.String(), deploy.Payload.(EventRef).DeploymentID)
	assert.True(t, performedAt.Equal(deploy.Payload.(EventRef).PerformedAt), "the same performedAt, so its ack still matches")
	undeployEvent := h.publisher.events[1].event
	assert.Equal(t, "widget.undeployed", undeployEvent.Type)
	assert.Empty(t, undeployEvent.Payload.(EventRef).DeploymentID, "which version the gateway holds is unknown")
}

func TestWorkerRunsAfterLiveForEachAckedDeployment(t *testing.T) {
	h := newHarness(t)
	kind := h.hooked()
	h = newHarnessWith(t, h, kind)
	h.idleScans()
	live := uuid.New()
	acked := h.intent
	acked.LiveDeploymentID = &live
	h.intents.ClaimLiveFunc = func(_ context.Context, kinds []string, _ int) ([]models.DeployIntent, error) {
		assert.Equal(t, []string{testKind}, kinds)
		return []models.DeployIntent{acked}, nil
	}
	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{{GatewayUUID: h.gateway, DeploymentID: live, Status: models.DeploymentStatusDeployed}}, nil
	}

	NewWorker(h.core).RunOnce(context.Background())

	assert.Equal(t, []uuid.UUID{live}, kind.live)
	assert.Equal(t, []string{"ou-1"}, kind.liveOrgs, "the hook acts as the intent's org")
}

func TestWorkerSkipsAfterLiveWhenNoKindHasIt(t *testing.T) {
	h := newHarness(t)
	h.idleScans()

	NewWorker(h.core).RunOnce(context.Background())

	assert.Empty(t, h.intents.ClaimLiveCalls())
}

func TestWorkerRedeploysDriftedIntentsAPageAtATime(t *testing.T) {
	h := newHarness(t)
	kind := h.hooked()
	h = newHarnessWith(t, h, kind)
	h.idleScans()
	h.core.cfg.Batch = 2
	page := []models.DeployIntent{h.intent, h.intent}
	page[0].ID, page[1].ID = uuid.New(), uuid.New()
	kind.drifted[page[1].ID] = "the endpoint moved"
	var cursors []uuid.UUID
	h.intents.ListDoneFunc = func(_ context.Context, name string, afterID uuid.UUID, limit int) ([]models.DeployIntent, error) {
		assert.Equal(t, testKind, name)
		cursors = append(cursors, afterID)
		if afterID == uuid.Nil {
			return page, nil
		}
		return nil, nil
	}
	h.intents.RequeueFunc = func(context.Context, models.DeployIntent) error { return nil }
	worker := NewWorker(h.core)

	worker.RunOnce(context.Background())

	require.Len(t, h.intents.RequeueCalls(), 1)
	assert.Equal(t, page[1].ID, h.intents.RequeueCalls()[0].Read.ID)
	assert.Len(t, h.deployments.CreateWithLimitEnforcementCalls(), 1)

	worker.RunOnce(context.Background())
	worker.RunOnce(context.Background())
	assert.Equal(t, []uuid.UUID{uuid.Nil, page[1].ID, uuid.Nil}, cursors, "a full page resumes after it; a short one wraps")
}

func TestWorkerRunsACycleAtStartAndOnANudge(t *testing.T) {
	h := newHarness(t)
	h.idleScans()
	h.core.cfg.TickInterval = time.Hour
	var cycles atomic.Int32
	h.intents.ClaimDueFunc = func(context.Context, time.Time, int) ([]models.DeployIntent, error) {
		cycles.Add(1)
		return nil, nil
	}
	worker := NewWorker(h.core)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, worker.Start(ctx))
	require.Eventually(t, func() bool { return cycles.Load() == 1 }, time.Second, time.Millisecond)
	worker.Nudge()
	require.Eventually(t, func() bool { return cycles.Load() == 2 }, time.Second, time.Millisecond)
	require.NoError(t, worker.Stop())
	require.NoError(t, worker.Stop(), "stopping twice is harmless")
}

// An intent still running when its lease ends could be claimed again and deployed twice.
func TestDefaultBatchBudgetFitsInsideTheClaimLease(t *testing.T) {
	assert.Less(t, DefaultConfig().BatchBudget, repositories.DeployIntentClaimLease)
	assert.Less(t, DefaultConfig().RetryIn, DefaultConfig().TickInterval, "a retry is due by the next tick")
}
