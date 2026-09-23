//go:build integration

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

package repositories

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

func newTestPublication(agentName string) *models.A2APublication {
	return &models.A2APublication{
		OUID:            "ou-" + uuid.New().String()[:8],
		ProjectName:     "checkout",
		AgentName:       agentName,
		EnvironmentName: "Development",
		EnvironmentUUID: uuid.New(),
		ArtifactUUID:    uuid.New(),
	}
}

func cleanupPublication(t *testing.T, repo A2APublicationRepository, pub *models.A2APublication) {
	t.Helper()
	t.Cleanup(func() {
		_ = repo.DeleteForAgent(context.Background(), pub.OUID, pub.ProjectName, pub.AgentName)
	})
}

// newTestPublicationFor is the second Enqueue for a pair that already has a row:
// same identity, fresh artifact, which is what a redeploy submits.
func newTestPublicationFor(pub *models.A2APublication) *models.A2APublication {
	return &models.A2APublication{
		OUID:            pub.OUID,
		ProjectName:     pub.ProjectName,
		AgentName:       pub.AgentName,
		EnvironmentName: pub.EnvironmentName,
		EnvironmentUUID: pub.EnvironmentUUID,
		ArtifactUUID:    uuid.New(),
	}
}

// A redeploy must re-publish, and a pair that exhausted its budget must get
// another chance — so a second enqueue resets the existing row rather than
// adding a second one for the same pair.
func TestA2APublicationEnqueueResetsTheExistingRow(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()

	pub := newTestPublication("trip-planner-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))
	require.NoError(t, repo.MarkFailed(ctx, pub.ID, "gave up"))

	requeued := newTestPublication(pub.AgentName)
	requeued.OUID = pub.OUID
	require.NoError(t, repo.Enqueue(ctx, requeued))

	due, err := repo.FindDue(ctx, time.Now(), 100)
	require.NoError(t, err)

	var found []models.A2APublication
	for _, row := range due {
		if row.AgentName == pub.AgentName {
			found = append(found, row)
		}
	}
	require.Len(t, found, 1, "one row per agent-environment pair, not one per deploy")
	assert.Equal(t, models.A2APublicationStatusPending, found[0].Status)
	assert.Equal(t, 0, found[0].AttemptCount, "the attempt budget is fresh")
	assert.Empty(t, found[0].LastError)
	assert.Equal(t, requeued.ArtifactUUID, found[0].ArtifactUUID, "the newest deploy's artifact wins")
}

// A row whose retry is scheduled for later must not be handed out until then;
// otherwise the backoff has no effect and the reconciler spins.
func TestA2APublicationFindDueRespectsBackoff(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()

	pub := newTestPublication("backoff-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))
	require.NoError(t, repo.MarkAttemptFailed(ctx, pub.ID, "binding not ready", time.Now().Add(time.Hour)))

	due, err := repo.FindDue(ctx, time.Now(), 100)
	require.NoError(t, err)
	for _, row := range due {
		assert.NotEqual(t, pub.AgentName, row.AgentName, "a backed-off row is not due yet")
	}

	later, err := repo.FindDue(ctx, time.Now().Add(2*time.Hour), 100)
	require.NoError(t, err)
	var attempts int
	for _, row := range later {
		if row.AgentName == pub.AgentName {
			attempts = row.AttemptCount
		}
	}
	assert.Equal(t, 1, attempts, "the failed attempt was counted")
}

// A published row is done: leaving it due would republish the same agent on
// every tick forever.
func TestA2APublicationMarkPublishedRemovesItFromTheQueue(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()

	pub := newTestPublication("published-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))
	require.NoError(t, repo.MarkPublished(ctx, pub.ID))

	due, err := repo.FindDue(ctx, time.Now(), 100)
	require.NoError(t, err)
	for _, row := range due {
		assert.NotEqual(t, pub.AgentName, row.AgentName, "a published row is no longer due")
	}
}

// A redeploy resets routing and retry state but must preserve the stored card:
// phase 1 republishes it so discovery keeps working while the new pod starts.
// Losing it here downgrades every redeploy to passthrough.
func TestA2APublicationEnqueuePreservesTheCardAndClearsRoutingState(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()

	pub := newTestPublication("card-keeper-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))

	fetchedAt := time.Now().UTC().Truncate(time.Second)
	card := json.RawMessage(`{"name":"Trip Planner"}`)
	require.NoError(t, repo.MarkRouted(ctx, pub.ID, fetchedAt))
	require.NoError(t, repo.SetCardDeploymentID(ctx, pub.ID, uuid.New()))
	require.NoError(t, repo.MarkCardPublished(ctx, pub.ID, card, fetchedAt))

	require.NoError(t, repo.Enqueue(ctx, newTestPublicationFor(pub)))

	got, err := repo.GetForAgentEnv(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.JSONEq(t, string(card), string(got.AgentCard), "the card survives a redeploy")
	assert.NotNil(t, got.CardFetchedAt, "and so does when it was fetched")
	assert.Equal(t, models.A2APublicationStatusPending, got.Status)
	assert.Nil(t, got.RoutedAt, "routing state is reset")
	assert.Nil(t, got.CardDeploymentID,
		"and the row waits on no ack — a late ack for the previous publish must not reject this one")
}

// Phase 2 rows are due alongside phase 1 rows; a scan that still filtered on
// pending alone would route every agent and fetch no cards.
func TestA2APublicationFindDueIncludesRoutedRows(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()

	pub := newTestPublication("due-routed-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))
	require.NoError(t, repo.MarkRouted(ctx, pub.ID, time.Now()))

	due, err := repo.FindDue(ctx, time.Now().Add(time.Minute), 50)
	require.NoError(t, err)

	var found bool
	for _, row := range due {
		if row.ID == pub.ID {
			found = true
			assert.Equal(t, models.A2APublicationStatusRouted, row.Status)
			assert.Zero(t, row.AttemptCount, "the phase boundary resets the retry budget")
		}
	}
	assert.True(t, found, "a routed row is due for its card fetch")
}

// The ack handler matches on deployment ID alone. A stale ID must match no row,
// or an ack for a superseded publish could reject a live one.
func TestA2APublicationMarkCardRejectedMatchesOnlyTheOutstandingDeployment(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()

	pub := newTestPublication("reject-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))

	card := json.RawMessage(`{"name":"Trip Planner"}`)
	outstanding := uuid.New()
	require.NoError(t, repo.MarkRouted(ctx, pub.ID, time.Now()))
	require.NoError(t, repo.SetCardDeploymentID(ctx, pub.ID, outstanding))
	require.NoError(t, repo.MarkCardPublished(ctx, pub.ID, card, time.Now()))

	require.NoError(t, repo.MarkCardRejected(ctx, uuid.New(), "INVALID_CARD"))
	got, err := repo.GetForAgentEnv(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	require.NoError(t, err)
	assert.Equal(t, models.A2APublicationStatusPublished, got.Status, "a stale ack changes nothing")

	require.NoError(t, repo.MarkCardRejected(ctx, outstanding, "INVALID_CARD"))
	got, err = repo.GetForAgentEnv(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	require.NoError(t, err)
	assert.Equal(t, models.A2APublicationStatusRejected, got.Status)
	assert.Equal(t, "INVALID_CARD", got.LastError)
	assert.JSONEq(t, string(card), string(got.AgentCard),
		"the rejected document is kept — it is what an operator needs to see")
}

// The refresh endpoint re-runs phase 2 without touching live routing.
func TestA2APublicationRequeueCardReturnsTheRowToPhaseTwo(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()

	pub := newTestPublication("requeue-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))

	card := json.RawMessage(`{"name":"Trip Planner"}`)
	require.NoError(t, repo.MarkRouted(ctx, pub.ID, time.Now()))
	require.NoError(t, repo.MarkCardPublished(ctx, pub.ID, card, time.Now()))
	require.NoError(t, repo.MarkFailed(ctx, pub.ID, "fetch timed out"))

	require.NoError(t, repo.RequeueCard(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID))

	got, err := repo.GetForAgentEnv(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	require.NoError(t, err)
	assert.Equal(t, models.A2APublicationStatusRouted, got.Status)
	assert.Zero(t, got.AttemptCount)
	assert.Empty(t, got.LastError)
	assert.NotNil(t, got.NextAttemptAt)
	assert.JSONEq(t, string(card), string(got.AgentCard), "the live card is untouched")
}
