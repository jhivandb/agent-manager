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
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

func routedPublication() models.A2APublication {
	pub := pendingPublication()
	pub.Status = models.A2APublicationStatusRouted
	routed := time.Now()
	pub.RoutedAt = &routed
	return pub
}

// A first deploy routes before it has a card. Gating routing on the card would
// delay the route by minutes and turn a 503 into a 404.
func TestReconcilerPhaseOneRoutesWithoutACard(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")

	h.svc.publishOne(context.Background(), pendingPublication())

	require.Len(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), 1)
	assert.NotContains(t, string(h.deploymentRepo.CreateWithLimitEnforcementCalls()[0].Deployment.Content), "agentCard")
	assert.Empty(t, h.cardFetcher.calls, "phase 1 never fetches")
	assert.Empty(t, h.pubRepo.SetCardDeploymentIDCalls(), "and waits on no card ack")
	require.Len(t, h.pubRepo.MarkRoutedCalls(), 1)
	assert.Empty(t, h.pubRepo.MarkCardPublishedCalls(), "routed is not published")
}

// A redeploy republishes the stored card in phase 1. Passthrough during a pod
// restart proxies the card request to an upstream that is not answering, so
// discovery fails outright; a previous revision's card keeps it working.
func TestReconcilerPhaseOneRepublishesTheStoredCard(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")

	pub := pendingPublication()
	pub.AgentCard = json.RawMessage(`{"name":"Trip Planner","protocolVersion":"1.0"}`)

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), 1)
	assert.Contains(t, string(h.deploymentRepo.CreateWithLimitEnforcementCalls()[0].Deployment.Content), "mode: managed")
	require.Len(t, h.pubRepo.SetCardDeploymentIDCalls(), 1, "a card publish records what it waits on")
	require.Len(t, h.pubRepo.MarkRoutedCalls(), 1)
}

// The gateway can ack faster than a later write commits, and an ack that
// arrives before the row knows its deployment ID is dropped silently — which is
// exactly the rejection the ack feedback exists to catch. This test fails if the
// broadcast ever moves ahead of the write.
func TestReconcilerRecordsTheCardDeploymentIDBeforeBroadcasting(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")

	var broadcastsAtWrite int
	h.pubRepo.SetCardDeploymentIDFunc = func(ctx context.Context, id, deploymentID uuid.UUID) error {
		broadcastsAtWrite = len(h.hub.published)
		return nil
	}

	pub := pendingPublication()
	pub.AgentCard = json.RawMessage(`{"name":"Trip Planner"}`)
	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.pubRepo.SetCardDeploymentIDCalls(), 1)
	assert.Zero(t, broadcastsAtWrite, "the row records the deployment before the gateway is told")
	assert.Len(t, h.hub.published, 1, "and the broadcast still happens")
}

// Phase 2 fetches, rewrites the two gateway-owned parts, republishes as managed
// and stores what it published.
func TestReconcilerPhaseTwoPublishesTheFetchedCard(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")

	h.svc.publishOne(context.Background(), routedPublication())

	assert.Equal(t, []string{"http://trip-planner.dp-default:9099"}, h.cardFetcher.calls)
	require.Len(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), 1)
	published := string(h.deploymentRepo.CreateWithLimitEnforcementCalls()[0].Deployment.Content)
	assert.Contains(t, published, "mode: managed")
	assert.Contains(t, published, "https://agents.example.com/trip-planner/rpc")

	stored := h.pubRepo.MarkCardPublishedCalls()
	require.Len(t, stored, 1)
	assert.Contains(t, string(stored[0].Card), "supportedInterfaces")
	assert.False(t, stored[0].FetchedAt.IsZero())
}

// gatewayCardFor is the document phase 2 would build from the harness's card.
func gatewayCardFor(t *testing.T, h *a2aReconcilerHarness) json.RawMessage {
	t.Helper()
	built, err := buildGatewayAgentCard(h.cardFetcher.card, GatewayAgentCardInput{
		PublicBaseURL:        "https://agents.example.com/trip-planner",
		EnableAPIKeySecurity: true,
	})
	require.NoError(t, err)
	encoded, err := json.Marshal(built)
	require.NoError(t, err)
	return encoded
}

// On a redeploy where the agent's card has not changed — the common case —
// two-phase costs no extra gateway traffic.
func TestReconcilerPhaseTwoSkipsTheRepublishWhenTheCardIsUnchanged(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")

	pub := routedPublication()
	pub.AgentCard = gatewayCardFor(t, h)
	accepted := uuid.New()
	pub.CardDeploymentID = &accepted

	h.svc.publishOne(context.Background(), pub)

	assert.Empty(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), "nothing to republish")
	assert.Empty(t, h.hub.published, "and no gateway is told again")
	require.Len(t, h.pubRepo.MarkCardPublishedCalls(), 1, "but the fetch is still recorded")
}

// A refresh from rejected clears the outstanding deployment: the gateway serves
// nothing, so an unchanged card must still be republished.
func TestReconcilerPhaseTwoRepublishesAnUnchangedCardWithNoAcceptedPublish(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")

	pub := routedPublication()
	pub.AgentCard = gatewayCardFor(t, h)

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), 1)
	assert.Len(t, h.hub.published, 1)
	require.Len(t, h.pubRepo.SetCardDeploymentIDCalls(), 1)
	require.Len(t, h.pubRepo.MarkCardPublishedCalls(), 1)
}

// A failed last attempt may have recorded a deployment ID it never broadcast,
// so an unchanged card is republished rather than trusted.
func TestReconcilerPhaseTwoRepublishesAnUnchangedCardAfterAFailedAttempt(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")

	pub := routedPublication()
	pub.AgentCard = gatewayCardFor(t, h)
	attempted := uuid.New()
	pub.CardDeploymentID = &attempted
	pub.LastError = "failed to broadcast agent deployment event: hub unavailable"

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), 1)
	assert.Len(t, h.hub.published, 1)
	require.Len(t, h.pubRepo.MarkCardPublishedCalls(), 1)
}

// Phase 2 exhausting its budget leaves routing live and the stored card intact:
// a stale card beats a broken discovery endpoint.
func TestReconcilerPhaseTwoFailureLeavesRoutingLive(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")
	h.cardFetcher.err = errors.New("connection refused")

	pub := routedPublication()
	pub.AttemptCount = a2aPublicationAttemptBudget - 1

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.pubRepo.MarkFailedCalls(), 1)
	assert.Contains(t, h.pubRepo.MarkFailedCalls()[0].LastErr, "connection refused")
	assert.Empty(t, h.pubRepo.MarkCardPublishedCalls(), "the stored card is untouched")
	assert.Empty(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), "and routing is not disturbed")
}

// Inside the budget a card that has not arrived is a retry, not a failure — the
// fetch doubles as the liveness probe the ServiceURL is not.
func TestReconcilerPhaseTwoRetriesWhileTheAgentIsStarting(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")
	h.cardFetcher.err = errors.New("connection refused")

	h.svc.publishOne(context.Background(), routedPublication())

	require.Len(t, h.pubRepo.MarkAttemptFailedCalls(), 1)
	assert.Empty(t, h.pubRepo.MarkFailedCalls())
}
