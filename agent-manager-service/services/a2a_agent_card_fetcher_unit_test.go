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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestA2AAgentCardFetcherReadsTheWellKnownCard(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"Trip Planner","protocolVersion":"1.0"}`)
	}))
	defer srv.Close()

	got, err := NewA2AAgentCardFetcher().Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "/.well-known/agent-card.json", gotPath)
	assert.Equal(t, "Trip Planner", got["name"])
}

// A trailing slash on the binding's ServiceURL must not produce a double slash
// the agent's router will not match.
func TestA2AAgentCardFetcherNormalisesTheUpstream(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, `{"name":"Trip Planner"}`)
	}))
	defer srv.Close()

	_, err := NewA2AAgentCardFetcher().Fetch(context.Background(), srv.URL+"/")
	require.NoError(t, err)
	assert.Equal(t, "/.well-known/agent-card.json", gotPath)
}

// Anything that is not 200-plus-a-JSON-object is a retryable failure: a pod that
// is still starting answers 404 or 503 long before it answers a card.
func TestA2AAgentCardFetcherRejectsNonCardResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not found", http.StatusNotFound, `{"name":"x"}`},
		{"unavailable", http.StatusServiceUnavailable, ""},
		{"not json", http.StatusOK, "<html>starting</html>"},
		{"json but not an object", http.StatusOK, `["a","b"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			_, err := NewA2AAgentCardFetcher().Fetch(context.Background(), srv.URL)
			assert.Error(t, err)
		})
	}
}

// 1 MiB is the ceiling the gateway applies to a card, because it is the largest
// object Kubernetes stores by default — a card past it could not be configured
// on this platform either.
func TestA2AAgentCardFetcherCapsTheResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"name":"%s"}`, strings.Repeat("x", a2aCardMaxResponseBytes))
	}))
	defer srv.Close()

	_, err := NewA2AAgentCardFetcher().Fetch(context.Background(), srv.URL)
	assert.Error(t, err)
}
