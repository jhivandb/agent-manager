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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

const (
	a2aCardTickInterval = 5 * time.Second
	a2aCardBatch        = 50
	a2aCardBaseBackoff  = 5 * time.Second
	a2aCardMaxBackoff   = time.Minute
	// a2aCardAttemptBudget is about 10 minutes of backoff.
	a2aCardAttemptBudget = 14
	// a2aCardLastErrorMaxRunes keeps last_error readable in the console.
	a2aCardLastErrorMaxRunes = 1024
	// a2aCardUnrecordableError replaces a cause the store refused to record.
	a2aCardUnrecordableError = "agent card fetch failed; the error could not be recorded"
)

// errA2ACardEndpointNotReady means the agent's environment has no public endpoint URL yet.
var errA2ACardEndpointNotReady = errors.New("agent has no public endpoint in this environment yet")

// A2ACardReconcilerService drains the a2a_agent_cards fetch queue.
type A2ACardReconcilerService interface {
	Start(ctx context.Context) error
	Stop() error
	// RunOnce drains the currently-due batch once.
	RunOnce(ctx context.Context)
}

type a2aCardReconcilerService struct {
	cardRepo repositories.A2AAgentCardRepository
	fetcher  A2ACardFetcher
	ocClient client.OpenChoreoClient
	logger   *slog.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewA2ACardReconcilerService creates an A2ACardReconcilerService.
func NewA2ACardReconcilerService(
	cardRepo repositories.A2AAgentCardRepository,
	fetcher A2ACardFetcher,
	ocClient client.OpenChoreoClient,
	logger *slog.Logger,
) A2ACardReconcilerService {
	return &a2aCardReconcilerService{
		cardRepo: cardRepo,
		fetcher:  fetcher,
		ocClient: ocClient,
		logger:   logger,
		stopCh:   make(chan struct{}),
		stopOnce: sync.Once{},
	}
}

func (s *a2aCardReconcilerService) Start(ctx context.Context) error {
	go s.runLoop(ctx)
	s.logger.Info("A2A card reconciler started")
	return nil
}

func (s *a2aCardReconcilerService) Stop() error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.logger.Info("A2A card reconciler stopped")
	})
	return nil
}

func (s *a2aCardReconcilerService) runLoop(ctx context.Context) {
	ticker := time.NewTicker(a2aCardTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.RunOnce(ctx)
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *a2aCardReconcilerService) RunOnce(ctx context.Context) {
	due, err := s.cardRepo.ClaimDue(ctx, time.Now(), a2aCardBatch)
	if err != nil {
		s.logger.Error("Failed to claim due A2A agent cards", "error", err)
		return
	}
	for _, row := range due {
		s.fetchOne(ctx, row)
	}
}

// fetchOne fetches one row's card and records the outcome.
func (s *a2aCardReconcilerService) fetchOne(ctx context.Context, row models.A2AAgentCard) {
	url, body, err := s.attemptFetch(ctx, row)
	if err != nil {
		s.recordAttemptFailure(ctx, row, err)
		return
	}
	hash, err := a2aCardHash(body)
	if err != nil {
		s.recordAttemptFailure(ctx, row, err)
		return
	}
	err = s.cardRepo.MarkFetched(ctx, row, body, hash, url)
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardSuperseded):
		s.logSuperseded(row)
	case err != nil:
		s.recordAttemptFailure(ctx, row, fmt.Errorf("failed to store agent card: %w", err))
	}
}

// attemptFetch returns the URL it fetched and the card it got.
func (s *a2aCardReconcilerService) attemptFetch(ctx context.Context, row models.A2AAgentCard) (string, json.RawMessage, error) {
	switch row.Source {
	case models.A2AAgentCardSourceExternal:
		body, err := s.fetcher.Fetch(ctx, row.SourceURL, true)
		return row.SourceURL, body, err
	case models.A2AAgentCardSourcePlatform:
		url, err := s.platformCardURL(ctx, row)
		if err != nil {
			return "", nil, err
		}
		body, err := s.fetcher.Fetch(ctx, url, false)
		if err != nil {
			// The derived URL is otherwise invisible to the user.
			return url, nil, fmt.Errorf("%s: %w", url, err)
		}
		return url, body, nil
	default:
		return "", nil, fmt.Errorf("unknown agent card source %q", row.Source)
	}
}

// platformCardURL is the agent's public endpoint (routed through the gateway) plus the card path.
func (s *a2aCardReconcilerService) platformCardURL(ctx context.Context, row models.A2AAgentCard) (string, error) {
	endpoints, err := s.ocClient.GetComponentEndpoints(ctx, row.OUID, row.ProjectName, row.AgentName, row.EnvironmentName)
	if err != nil {
		return "", fmt.Errorf("failed to read agent endpoints: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(endpoints)) {
		if u := endpoints[name].URL; u != "" {
			return strings.TrimRight(u, "/") + a2aAgentCardPath, nil
		}
	}
	return "", errA2ACardEndpointNotReady
}

func (s *a2aCardReconcilerService) recordAttemptFailure(ctx context.Context, row models.A2AAgentCard, cause error) {
	lastErr := sanitizeA2ACardError(cause.Error())
	err := s.markAttemptFailed(ctx, row, cause, lastErr)
	if err != nil && !errors.Is(err, repositories.ErrA2AAgentCardSuperseded) {
		s.logger.Error("Failed to record A2A agent card attempt; retrying with a fixed message",
			"agentName", row.AgentName, "error", err)
		err = s.markAttemptFailed(ctx, row, cause, a2aCardUnrecordableError)
	}
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardSuperseded):
		s.logSuperseded(row)
	case err != nil:
		s.logger.Error("Failed to record A2A agent card attempt", "agentName", row.AgentName, "error", err)
	}
}

// markAttemptFailed charges one attempt, failing the row once the budget is spent.
func (s *a2aCardReconcilerService) markAttemptFailed(ctx context.Context, row models.A2AAgentCard, cause error, lastErr string) error {
	if row.AttemptCount+1 >= a2aCardAttemptBudget {
		s.logger.Warn("A2A agent card could not be fetched within the attempt budget",
			"agentName", row.AgentName, "environment", row.EnvironmentName, "error", cause)
		return s.cardRepo.MarkFailed(ctx, row, lastErr)
	}
	s.logger.Debug("A2A agent card not fetchable yet, will retry",
		"agentName", row.AgentName, "environment", row.EnvironmentName,
		"attempt", row.AttemptCount+1, "reason", cause)
	return s.cardRepo.MarkAttemptFailed(ctx, row, lastErr, time.Now().Add(a2aCardRetryIn(row.AttemptCount)))
}

// sanitizeA2ACardError makes msg storable in a Postgres TEXT column and bounded in length.
func sanitizeA2ACardError(msg string) string {
	msg = strings.ReplaceAll(strings.ToValidUTF8(msg, "\uFFFD"), "\x00", "")
	if runes := []rune(msg); len(runes) > a2aCardLastErrorMaxRunes {
		msg = string(runes[:a2aCardLastErrorMaxRunes-1]) + "…"
	}
	return msg
}

func (s *a2aCardReconcilerService) logSuperseded(row models.A2AAgentCard) {
	s.logger.Info("A2A agent card was re-enqueued during the attempt; leaving it for the next tick",
		"agentName", row.AgentName, "environment", row.EnvironmentName)
}

// a2aCardRetryIn is min(5s · 2^attempt, 60s).
func a2aCardRetryIn(attempt int) time.Duration {
	delay := a2aCardBaseBackoff
	for i := 0; i < attempt && delay < a2aCardMaxBackoff; i++ {
		delay *= 2
	}
	return min(delay, a2aCardMaxBackoff)
}

// a2aCardHash is SHA-256 over the card re-encoded with sorted keys, so reformatting is not a change.
func a2aCardHash(card json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(card))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("failed to hash agent card: %w", err)
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to hash agent card: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
