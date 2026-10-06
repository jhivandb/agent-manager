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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

func newTestCard(agentName string, source models.A2AAgentCardSource) *models.A2AAgentCard {
	return &models.A2AAgentCard{
		OUID:            "ou-" + uuid.New().String()[:8],
		ProjectName:     "checkout",
		AgentName:       agentName,
		EnvironmentName: "Development",
		Source:          source,
	}
}

func cleanupCard(t *testing.T, repo A2AAgentCardRepository, card *models.A2AAgentCard) {
	t.Helper()
	t.Cleanup(func() {
		_ = repo.DeleteForAgent(context.Background(), card.OUID, card.ProjectName, card.AgentName)
	})
}

// dueCardFor claims the due row for agentName, as the reconciler would.
func dueCardFor(t *testing.T, repo A2AAgentCardRepository, agentName string) models.A2AAgentCard {
	t.Helper()
	due, err := repo.ClaimDue(context.Background(), time.Now(), 100)
	require.NoError(t, err)
	for _, row := range due {
		if row.AgentName == agentName {
			return row
		}
	}
	require.FailNow(t, "no due card row", "agent %s", agentName)
	return models.A2AAgentCard{}
}

func cardRowOf(t *testing.T, card *models.A2AAgentCard) models.A2AAgentCard {
	t.Helper()
	var row models.A2AAgentCard
	require.NoError(t, db.GetDB().Where("id = ?", card.ID).First(&row).Error)
	return row
}

func claimedNames(rows []models.A2AAgentCard) []string {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.AgentName)
	}
	return names
}

const sampleCard = `{"name":"Trip Planner","supportedInterfaces":[{"url":"https://a.example/rpc"}],"skills":[]}`

// A redeploy settles over minutes; readers must keep the last good card meanwhile.
func TestA2AAgentCardEnqueuePreservesTheLastGoodCard(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("keep-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)

	require.NoError(t, repo.Enqueue(ctx, card))
	read := dueCardFor(t, repo, card.AgentName)
	require.NoError(t, repo.MarkFetched(ctx, read, json.RawMessage(sampleCard), "hash-1", "https://gw.example/a/.well-known/agent-card.json"))
	fetched := cardRowOf(t, card)
	require.NotNil(t, fetched.FetchedAt)

	again := newTestCard(card.AgentName, models.A2AAgentCardSourcePlatform)
	again.OUID = card.OUID
	require.NoError(t, repo.Enqueue(ctx, again))

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusPending, row.Status)
	assert.Equal(t, 0, row.AttemptCount, "the attempt budget is fresh")
	assert.Empty(t, row.LastError)
	assert.JSONEq(t, sampleCard, string(row.Card), "the last good card survives the re-enqueue")
	assert.Equal(t, "hash-1", row.CardHash)
	require.NotNil(t, row.FetchedAt)
	assert.WithinDuration(t, *fetched.FetchedAt, *row.FetchedAt, time.Millisecond)
	assert.Equal(t, "https://gw.example/a/.well-known/agent-card.json", row.SourceURL,
		"a platform re-enqueue keeps the diagnostic URL")
}

// A refresh or platform trigger must not erase a registered external URL.
func TestA2AAgentCardEnqueueKeepsSourceURLUnlessGiven(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("src-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	card.SourceURL = "https://agent.example/.well-known/agent-card.json"
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))

	refresh := newTestCard(card.AgentName, models.A2AAgentCardSourceExternal)
	refresh.OUID = card.OUID
	require.NoError(t, repo.Enqueue(ctx, refresh))
	assert.Equal(t, card.SourceURL, cardRowOf(t, card).SourceURL)

	moved := newTestCard(card.AgentName, models.A2AAgentCardSourceExternal)
	moved.OUID = card.OUID
	moved.SourceURL = "https://other.example/.well-known/agent-card.json"
	require.NoError(t, repo.Enqueue(ctx, moved))
	assert.Equal(t, moved.SourceURL, cardRowOf(t, card).SourceURL)
}

// A row whose retry is scheduled for later is not handed out until then.
func TestA2AAgentCardClaimDueRespectsBackoff(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("backoff-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, repo.MarkAttemptFailed(ctx, *card, "HTTP 503", time.Now().Add(time.Hour)))

	due, err := repo.ClaimDue(ctx, time.Now(), 100)
	require.NoError(t, err)
	assert.NotContains(t, claimedNames(due), card.AgentName)

	later, err := repo.ClaimDue(ctx, time.Now().Add(2*time.Hour), 100)
	require.NoError(t, err)
	assert.Contains(t, claimedNames(later), card.AgentName)
	assert.Equal(t, 1, cardRowOf(t, card).AttemptCount)
}

// The proxy spec parks rows with a NULL next_attempt_at; they must never be claimed.
func TestA2AAgentCardClaimDueSkipsAParkedRow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("parked-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, db.GetDB().Model(&models.A2AAgentCard{}).
		Where("id = ?", card.ID).Update("next_attempt_at", nil).Error)

	due, err := repo.ClaimDue(ctx, time.Now().Add(24*time.Hour), 100)
	require.NoError(t, err)
	assert.NotContains(t, claimedNames(due), card.AgentName)
}

// Replicas tick independently; concurrent claimers together claim every row exactly once.
func TestA2AAgentCardConcurrentClaimersNeverShareARow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	prefix := "race-" + uuid.New().String()[:6] + "-"
	const seeded = 30
	ours := map[string]bool{}
	for i := 0; i < seeded; i++ {
		card := newTestCard(prefix+uuid.New().String()[:6], models.A2AAgentCardSourcePlatform)
		cleanupCard(t, repo, card)
		require.NoError(t, repo.Enqueue(ctx, card))
		ours[card.AgentName] = true
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				rows, err := repo.ClaimDue(ctx, time.Now(), 4)
				if !assert.NoError(t, err) || len(rows) == 0 {
					return
				}
				mu.Lock()
				for _, r := range rows {
					if ours[r.AgentName] {
						seen[r.AgentName]++
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Len(t, seen, seeded, "every seeded row was claimed")
	for name, n := range seen {
		assert.Equal(t, 1, n, "row %s claimed %d times", name, n)
	}
}

// A claimed row is leased: not handed out again until the lease runs out.
func TestA2AAgentCardClaimDueLeasesTheRow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("lease-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	claimed := dueCardFor(t, repo, card.AgentName)

	again, err := repo.ClaimDue(ctx, time.Now(), 100)
	require.NoError(t, err)
	assert.NotContains(t, claimedNames(again), card.AgentName)

	afterLease, err := repo.ClaimDue(ctx, time.Now().Add(a2aAgentCardClaimLease+time.Minute), 100)
	require.NoError(t, err)
	assert.Contains(t, claimedNames(afterLease), card.AgentName)
	require.NoError(t, repo.MarkFetched(ctx, claimed, json.RawMessage(sampleCard), "h", "u"),
		"claiming is not mistaken for a re-enqueue")
}

// An unchanged hash flips the status but does not rewrite the stored card.
func TestA2AAgentCardMarkFetchedSkipsTheWriteForAnUnchangedHash(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("hash-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, repo.MarkFetched(ctx, dueCardFor(t, repo, card.AgentName), json.RawMessage(sampleCard), "same", "u"))

	again := newTestCard(card.AgentName, models.A2AAgentCardSourcePlatform)
	again.OUID = card.OUID
	require.NoError(t, repo.Enqueue(ctx, again))
	read := dueCardFor(t, repo, card.AgentName)
	require.NoError(t, repo.MarkFetched(ctx, read, json.RawMessage(`{"name":"other"}`), "same", "u"))

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusFetched, row.Status)
	assert.JSONEq(t, sampleCard, string(row.Card), "same hash, so the body was not rewritten")
}

// A fetch of the old URL must not overwrite a newer enqueue (e.g. a changed source URL).
func TestA2AAgentCardMarkFetchedDoesNotSwallowANewerEnqueue(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("race-fetch-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	card.SourceURL = "https://old.example/.well-known/agent-card.json"
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	read := dueCardFor(t, repo, card.AgentName)

	moved := *card
	moved.SourceURL = "https://new.example/.well-known/agent-card.json"
	require.NoError(t, repo.Enqueue(ctx, &moved))

	require.ErrorIs(t, repo.MarkFetched(ctx, read, json.RawMessage(sampleCard), "h", read.SourceURL), ErrA2AAgentCardSuperseded)
	require.ErrorIs(t, repo.MarkAttemptFailed(ctx, read, "x", time.Now().Add(time.Hour)), ErrA2AAgentCardSuperseded)
	require.ErrorIs(t, repo.MarkFailed(ctx, read, "x"), ErrA2AAgentCardSuperseded)

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusPending, row.Status)
	assert.Equal(t, moved.SourceURL, row.SourceURL)
	assert.Nil(t, row.Card)
}

func TestA2AAgentCardMarkFailedEndsTheCycle(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("failed-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, repo.MarkFailed(ctx, dueCardFor(t, repo, card.AgentName), "HTTP 404"))

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusFailed, row.Status)
	assert.Equal(t, "HTTP 404", row.LastError)
	assert.Nil(t, row.NextAttemptAt)
}

func TestA2AAgentCardListForAgentOrdersByEnvironment(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	dev := newTestCard("list-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	prod := newTestCard(dev.AgentName, models.A2AAgentCardSourcePlatform)
	prod.OUID, prod.EnvironmentName = dev.OUID, "Production"
	other := newTestCard("other-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	other.OUID = dev.OUID
	cleanupCard(t, repo, dev)
	cleanupCard(t, repo, other)
	require.NoError(t, repo.Enqueue(ctx, prod))
	require.NoError(t, repo.Enqueue(ctx, dev))
	require.NoError(t, repo.Enqueue(ctx, other))

	rows, err := repo.ListForAgent(ctx, dev.OUID, dev.ProjectName, dev.AgentName)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "Development", rows[0].EnvironmentName)
	assert.Equal(t, "Production", rows[1].EnvironmentName)

	none, err := repo.ListForAgent(ctx, "other-org", dev.ProjectName, dev.AgentName)
	require.NoError(t, err)
	assert.Empty(t, none, "rows are org-scoped")
}

func TestA2AAgentCardGetAndDelete(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	dev := newTestCard("get-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	prod := newTestCard(dev.AgentName, models.A2AAgentCardSourcePlatform)
	prod.OUID, prod.EnvironmentName = dev.OUID, "Production"
	cleanupCard(t, repo, dev)
	require.NoError(t, repo.Enqueue(ctx, dev))
	require.NoError(t, repo.Enqueue(ctx, prod))

	got, err := repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Development")
	require.NoError(t, err)
	assert.Equal(t, dev.ID, got.ID)

	_, err = repo.Get(ctx, "other-org", dev.ProjectName, dev.AgentName, "Development")
	assert.ErrorIs(t, err, ErrA2AAgentCardNotFound, "rows are org-scoped")

	require.NoError(t, repo.DeleteForAgentEnv(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Development"))
	_, err = repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Development")
	assert.ErrorIs(t, err, ErrA2AAgentCardNotFound)
	_, err = repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Production")
	require.NoError(t, err, "only that environment's row is gone")

	require.NoError(t, repo.DeleteForAgent(ctx, dev.OUID, dev.ProjectName, dev.AgentName))
	_, err = repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Production")
	assert.ErrorIs(t, err, ErrA2AAgentCardNotFound)
}
