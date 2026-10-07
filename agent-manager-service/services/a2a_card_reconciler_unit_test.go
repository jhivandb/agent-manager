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
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

func pendingCard(source models.A2AAgentCardSource) models.A2AAgentCard {
	return models.A2AAgentCard{
		ID:              uuid.New(),
		OUID:            "org-1",
		ProjectName:     "checkout",
		AgentName:       "trip-planner",
		EnvironmentName: "dev",
		Source:          source,
		Status:          models.A2AAgentCardStatusPending,
		UpdatedAt:       time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC),
	}
}

type cardReconcilerHarness struct {
	svc      *a2aCardReconcilerService
	repo     *repomocks.A2AAgentCardRepositoryMock
	fetcher  *fakeA2ACardFetcher
	ocClient *clientmocks.OpenChoreoClientMock
}

// newCardReconcilerHarness wires a reconciler whose fetch succeeds with validTestCard
// and whose agent exposes one endpoint at endpointURL.
func newCardReconcilerHarness(endpointURL string) *cardReconcilerHarness {
	repo := &repomocks.A2AAgentCardRepositoryMock{
		MarkFetchedFunc: func(context.Context, models.A2AAgentCard, json.RawMessage, string, string) error { return nil },
		MarkAttemptFailedFunc: func(context.Context, models.A2AAgentCard, string, time.Time) error {
			return nil
		},
		MarkFailedFunc: func(context.Context, models.A2AAgentCard, string) error { return nil },
	}
	fetcher := &fakeA2ACardFetcher{
		FetchFunc: func(context.Context, string, bool) (json.RawMessage, error) {
			return json.RawMessage(validTestCard), nil
		},
	}
	oc := &clientmocks.OpenChoreoClientMock{
		GetComponentEndpointsFunc: func(_ context.Context, _, _, _, _ string) (map[string]models.EndpointsResponse, error) {
			return map[string]models.EndpointsResponse{
				"trip-planner-endpoint": {Endpoint: models.Endpoint{Name: "trip-planner-endpoint", URL: endpointURL}},
			}, nil
		},
	}
	return &cardReconcilerHarness{
		repo: repo, fetcher: fetcher, ocClient: oc,
		svc: &a2aCardReconcilerService{cardRepo: repo, fetcher: fetcher, ocClient: oc, logger: testLogger()},
	}
}

func TestCardReconcilerFetchesAPlatformCardThroughTheEndpoint(t *testing.T) {
	h := newCardReconcilerHarness("http://dev-org.gw.example/trip-planner/")
	row := pendingCard(models.A2AAgentCardSourcePlatform)

	h.svc.fetchOne(context.Background(), row)

	require.Len(t, h.fetcher.calls, 1)
	assert.Equal(t, "http://dev-org.gw.example/trip-planner/.well-known/agent-card.json", h.fetcher.calls[0].URL)
	assert.False(t, h.fetcher.calls[0].Guarded, "platform URLs are platform-derived")

	marked := h.repo.MarkFetchedCalls()
	require.Len(t, marked, 1)
	assert.Equal(t, row, marked[0].Read)
	assert.JSONEq(t, validTestCard, string(marked[0].Card))
	wantHash, err := a2aCardHash(json.RawMessage(validTestCard))
	require.NoError(t, err)
	assert.Equal(t, wantHash, marked[0].CardHash)
	assert.Equal(t, h.fetcher.calls[0].URL, marked[0].FetchedURL)
}

func TestCardReconcilerFetchesAnExternalCardGuarded(t *testing.T) {
	h := newCardReconcilerHarness("")
	h.ocClient.GetComponentEndpointsFunc = nil // external rows never ask OpenChoreo
	row := pendingCard(models.A2AAgentCardSourceExternal)
	row.SourceURL = "https://agent.example/.well-known/agent-card.json"

	h.svc.fetchOne(context.Background(), row)

	require.Len(t, h.fetcher.calls, 1)
	assert.Equal(t, row.SourceURL, h.fetcher.calls[0].URL)
	assert.True(t, h.fetcher.calls[0].Guarded)
	require.Len(t, h.repo.MarkFetchedCalls(), 1)
}

func TestCardReconcilerPicksTheFirstNamedEndpointWithAURL(t *testing.T) {
	h := newCardReconcilerHarness("")
	h.ocClient.GetComponentEndpointsFunc = func(_ context.Context, _, _, _, _ string) (map[string]models.EndpointsResponse, error) {
		return map[string]models.EndpointsResponse{
			"c-endpoint": {Endpoint: models.Endpoint{URL: "http://c.example"}},
			"a-endpoint": {Endpoint: models.Endpoint{URL: ""}},
			"b-endpoint": {Endpoint: models.Endpoint{URL: "http://b.example"}},
		}, nil
	}

	const iterations = 5
	for i := 0; i < iterations; i++ {
		h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))
	}

	require.Len(t, h.fetcher.calls, iterations)
	for _, call := range h.fetcher.calls {
		assert.Equal(t, "http://b.example/.well-known/agent-card.json", call.URL)
	}
}

func TestCardReconcilerRetriesWhenNoEndpointIsReady(t *testing.T) {
	h := newCardReconcilerHarness("")
	before := time.Now()

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	assert.Empty(t, h.fetcher.calls)
	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.WithinDuration(t, before.Add(5*time.Second), retries[0].NextAttemptAt, time.Second)
}

func TestCardReconcilerBacksOffExponentially(t *testing.T) {
	assert.Equal(t, 5*time.Second, a2aCardRetryIn(0))
	assert.Equal(t, 10*time.Second, a2aCardRetryIn(1))
	assert.Equal(t, 20*time.Second, a2aCardRetryIn(2))
	assert.Equal(t, 40*time.Second, a2aCardRetryIn(3))
	assert.Equal(t, time.Minute, a2aCardRetryIn(4))
	assert.Equal(t, time.Minute, a2aCardRetryIn(13))
	assert.Equal(t, time.Minute, a2aCardRetryIn(1000), "no overflow")
}

func TestCardReconcilerGivesUpPastTheBudget(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("agent card request returned HTTP 503")
	}
	row := pendingCard(models.A2AAgentCardSourcePlatform)
	row.AttemptCount = a2aCardAttemptBudget - 1

	h.svc.fetchOne(context.Background(), row)

	failed := h.repo.MarkFailedCalls()
	require.Len(t, failed, 1)
	assert.Contains(t, failed[0].LastErr, "503")
	assert.Empty(t, h.repo.MarkAttemptFailedCalls())
}

func TestCardReconcilerPlatformFailuresNameTheURL(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("agent card request returned HTTP 404")
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.Contains(t, retries[0].LastErr, "http://gw.example/a/.well-known/agent-card.json")
}

func TestCardReconcilerTreatsSupersededAsBenign(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.repo.MarkFetchedFunc = func(context.Context, models.A2AAgentCard, json.RawMessage, string, string) error {
		return repositories.ErrA2AAgentCardSuperseded
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	assert.Empty(t, h.repo.MarkAttemptFailedCalls(), "a superseded outcome is not a failure")
	assert.Empty(t, h.repo.MarkFailedCalls())
}

func TestCardReconcilerRunOnceDrainsTheDueBatch(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.repo.ClaimDueFunc = func(_ context.Context, _ time.Time, limit int) ([]models.A2AAgentCard, error) {
		assert.Equal(t, a2aCardBatch, limit)
		return []models.A2AAgentCard{pendingCard(models.A2AAgentCardSourcePlatform), pendingCard(models.A2AAgentCardSourcePlatform)}, nil
	}

	h.svc.RunOnce(context.Background())

	assert.Len(t, h.repo.MarkFetchedCalls(), 2)
}

func TestA2ACardHashIgnoresKeyOrderAndWhitespace(t *testing.T) {
	a, err := a2aCardHash(json.RawMessage(`{"name":"n","skills":[],"n":12345678901234567890}`))
	require.NoError(t, err)
	b, err := a2aCardHash(json.RawMessage("{ \"n\": 12345678901234567890,\n \"skills\": [], \"name\": \"n\" }"))
	require.NoError(t, err)
	c, err := a2aCardHash(json.RawMessage(`{"name":"m","skills":[],"n":12345678901234567890}`))
	require.NoError(t, err)

	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	assert.Len(t, a, 64)
}

// A card the store rejects (e.g. JSONB refusing \u0000) must charge an attempt, not loop pending.
func TestCardReconcilerChargesAnAttemptWhenTheCardCannotBeStored(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.repo.MarkFetchedFunc = func(context.Context, models.A2AAgentCard, json.RawMessage, string, string) error {
		return errors.New("ERROR: unsupported Unicode escape sequence (SQLSTATE 22P05)")
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.Contains(t, retries[0].LastErr, "failed to store agent card")
	assert.Contains(t, retries[0].LastErr, "22P05")
}

func TestCardReconcilerFailsTheRowWhenTheLastCardCannotBeStored(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.repo.MarkFetchedFunc = func(context.Context, models.A2AAgentCard, json.RawMessage, string, string) error {
		return errors.New("SQLSTATE 22003")
	}
	row := pendingCard(models.A2AAgentCardSourcePlatform)
	row.AttemptCount = a2aCardAttemptBudget - 1

	h.svc.fetchOne(context.Background(), row)

	require.Len(t, h.repo.MarkFailedCalls(), 1)
}

func TestCardReconcilerStoresAValidUTF8LastError(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("dial tcp: lookup \xff\x00host: no such host")
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.True(t, utf8.ValidString(retries[0].LastErr))
	assert.NotContains(t, retries[0].LastErr, "\x00")
	assert.Contains(t, retries[0].LastErr, "no such host")
}

func TestCardReconcilerCapsTheLastErrorLength(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New(strings.Repeat("é", 10*a2aCardLastErrorMaxRunes))
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.LessOrEqual(t, utf8.RuneCountInString(retries[0].LastErr), a2aCardLastErrorMaxRunes)
	assert.True(t, utf8.ValidString(retries[0].LastErr))
}

// If recording the error itself fails, a fixed message must still charge the attempt.
func TestCardReconcilerFallsBackWhenTheErrorCannotBeRecorded(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("HTTP 503")
	}
	h.repo.MarkAttemptFailedFunc = func(_ context.Context, _ models.A2AAgentCard, lastErr string, _ time.Time) error {
		if lastErr == a2aCardUnrecordableError {
			return nil
		}
		return errors.New("SQLSTATE 22021")
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 2)
	assert.Equal(t, a2aCardUnrecordableError, retries[1].LastErr)
}

func TestCardReconcilerDoesNotRetryTheRecordWhenSuperseded(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("HTTP 503")
	}
	h.repo.MarkAttemptFailedFunc = func(context.Context, models.A2AAgentCard, string, time.Time) error {
		return repositories.ErrA2AAgentCardSuperseded
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	assert.Len(t, h.repo.MarkAttemptFailedCalls(), 1)
}
