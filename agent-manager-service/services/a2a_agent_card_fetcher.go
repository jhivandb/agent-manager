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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// a2aCardWellKnownPath is where every A2A agent serves its own card.
	a2aCardWellKnownPath = "/.well-known/agent-card.json"

	// a2aCardFetchTimeout bounds one attempt, not the wait for a card. The
	// reconciler's retry loop is the real patience — a long per-attempt timeout
	// would only hold a reconciler slot open while a pod starts.
	a2aCardFetchTimeout = 10 * time.Second

	// a2aCardMaxResponseBytes is the ceiling the gateway applies to a card, and
	// the largest object Kubernetes stores by default: a card past it could not
	// be configured on this platform either.
	a2aCardMaxResponseBytes = 1 << 20
)

// A2AAgentCardFetcher reads a running agent's own Agent Card.
//
// An interface so the reconciler is testable with a fake: the reconciler's
// interesting behaviour is what it does with a card that does not arrive, and
// that should not need a live workload to exercise.
type A2AAgentCardFetcher interface {
	Fetch(ctx context.Context, upstreamURL string) (map[string]any, error)
}

type a2aAgentCardFetcher struct {
	client *http.Client
}

// NewA2AAgentCardFetcher creates an A2AAgentCardFetcher.
func NewA2AAgentCardFetcher() A2AAgentCardFetcher {
	return &a2aAgentCardFetcher{client: &http.Client{Timeout: a2aCardFetchTimeout}}
}

// Fetch GETs the agent's card. Every failure is retryable by construction:
// fetching the card doubles as the liveness probe the release binding's
// ServiceURL is not, so "not answering yet" is the expected early condition.
func (f *a2aAgentCardFetcher) Fetch(ctx context.Context, upstreamURL string) (map[string]any, error) {
	endpoint := strings.TrimSuffix(strings.TrimSpace(upstreamURL), "/") + a2aCardWellKnownPath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build agent card request for %s: %w", endpoint, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent card from %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent card fetch from %s returned %d", endpoint, resp.StatusCode)
	}

	// One byte past the cap is read so a document exactly at it still succeeds
	// while anything larger is refused rather than silently truncated.
	body, err := io.ReadAll(io.LimitReader(resp.Body, a2aCardMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read agent card from %s: %w", endpoint, err)
	}
	if len(body) > a2aCardMaxResponseBytes {
		return nil, fmt.Errorf("agent card from %s exceeds %d bytes", endpoint, a2aCardMaxResponseBytes)
	}

	var card map[string]any
	if err := json.Unmarshal(body, &card); err != nil {
		return nil, fmt.Errorf("agent card from %s is not a JSON object: %w", endpoint, err)
	}
	return card, nil
}
