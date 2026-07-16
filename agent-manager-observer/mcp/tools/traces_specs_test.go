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

package tools

import (
	"context"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Returns the test specs for tools registered by registerTraceTools.
// New tools added to traces.go must have a spec here — registration_test.go fails otherwise.
func tracesToolSpecs() []toolTestSpec {
	baseTraceArgs := map[string]any{
		"organization": testOrgName,
		"project":      testProjectName,
		"agent":        testAgentName,
		"environment":  testEnvName,
	}

	return []toolTestSpec{
		{
			name:                "list_traces",
			descriptionKeywords: []string{"trace"},
			descriptionMinLen:   20,
			requiredParams:      []string{"organization", "project", "agent", "environment"},
			optionalParams:      []string{"start_time", "end_time", "limit", "sort_order"},
			testArgs:            baseTraceArgs,
		},
		{
			name:                "get_traces",
			descriptionKeywords: []string{"trace", "span"},
			descriptionMinLen:   20,
			requiredParams:      []string{"organization", "project", "agent", "environment"},
			optionalParams:      []string{"start_time", "end_time", "limit", "sort_order"},
			testArgs:            baseTraceArgs,
		},
		{
			name:                "get_trace_details",
			descriptionKeywords: []string{"trace", "span"},
			descriptionMinLen:   20,
			requiredParams:      []string{"organization", "project", "agent", "environment", "trace_id"},
			optionalParams:      []string{"start_time", "end_time"},
			testArgs: map[string]any{
				"organization": testOrgName,
				"project":      testProjectName,
				"agent":        testAgentName,
				"environment":  testEnvName,
				"trace_id":     testTraceID,
			},
		},
		{
			name:                "get_span_details",
			descriptionKeywords: []string{"span"},
			descriptionMinLen:   20,
			requiredParams:      []string{"trace_id", "span_id"},
			testArgs: map[string]any{
				"trace_id": testTraceID,
				"span_id":  testSpanID,
			},
		},
	}
}

// Verifies that a malformed start_time is rejected as a tool error rather
// than reaching the observer client.
func TestBadRFC3339Rejected(t *testing.T) {
	clientSession, fake := setupTestServer(t)
	ctx := context.Background()

	result, err := clientSession.CallTool(ctx, &gomcp.CallToolParams{
		Name: "list_traces",
		Arguments: map[string]any{
			"organization": testOrgName,
			"project":      testProjectName,
			"agent":        testAgentName,
			"environment":  testEnvName,
			"start_time":   "not-a-timestamp",
			"end_time":     "2026-01-02T00:00:00Z",
		},
	})
	switch {
	case err != nil:
		// Protocol-level rejection — fine.
	case result != nil && result.IsError:
		// Handler-level tool error — fine.
	default:
		t.Error("expected error for malformed start_time; got success")
	}

	if calls := fake.calls["QueryTraces"]; len(calls) != 0 {
		t.Errorf("expected no QueryTraces call for a rejected request, got %d", len(calls))
	}
}

// Verifies that list_traces rejects a limit outside [1, maxTraceListLimit].
func TestTraceListLimitBounds(t *testing.T) {
	cases := []struct {
		name  string
		limit int
	}{
		{"zero", 0},
		{"negative", -1},
		{"over max", maxTraceListLimit + 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clientSession, fake := setupTestServer(t)
			ctx := context.Background()

			result, err := clientSession.CallTool(ctx, &gomcp.CallToolParams{
				Name: "list_traces",
				Arguments: map[string]any{
					"organization": testOrgName,
					"project":      testProjectName,
					"agent":        testAgentName,
					"environment":  testEnvName,
					"limit":        tc.limit,
				},
			})
			switch {
			case err != nil:
				// Protocol-level rejection — fine.
			case result != nil && result.IsError:
				// Handler-level tool error — fine.
			default:
				t.Errorf("expected error for limit=%d; got success", tc.limit)
			}

			if calls := fake.calls["QueryTraces"]; len(calls) != 0 {
				t.Errorf("expected no QueryTraces call for a rejected request, got %d", len(calls))
			}
		})
	}

	t.Run("within bounds succeeds", func(t *testing.T) {
		clientSession, fake := setupTestServer(t)
		ctx := context.Background()

		result, err := clientSession.CallTool(ctx, &gomcp.CallToolParams{
			Name: "list_traces",
			Arguments: map[string]any{
				"organization": testOrgName,
				"project":      testProjectName,
				"agent":        testAgentName,
				"environment":  testEnvName,
				"limit":        maxTraceListLimit,
			},
		})
		if err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got tool error: %+v", result.Content)
		}
		if calls := fake.calls["QueryTraces"]; len(calls) != 1 {
			t.Errorf("expected exactly one QueryTraces call, got %d", len(calls))
		}
	})
}

// Verifies that list_traces enforces the locked 30-day cap on the
// start_time..end_time span (resolveTraceTimeWindow).
func TestTraceTimeWindowCap(t *testing.T) {
	end := time.Now().UTC()

	t.Run("31 days is rejected", func(t *testing.T) {
		clientSession, fake := setupTestServer(t)
		ctx := context.Background()

		start := end.Add(-31 * 24 * time.Hour)
		result, err := clientSession.CallTool(ctx, &gomcp.CallToolParams{
			Name: "list_traces",
			Arguments: map[string]any{
				"organization": testOrgName,
				"project":      testProjectName,
				"agent":        testAgentName,
				"environment":  testEnvName,
				"start_time":   start.Format(time.RFC3339),
				"end_time":     end.Format(time.RFC3339),
			},
		})
		switch {
		case err != nil:
			// Protocol-level rejection — fine.
		case result != nil && result.IsError:
			// Handler-level tool error — fine.
		default:
			t.Error("expected error for a 31-day window; got success")
		}
		if calls := fake.calls["QueryTraces"]; len(calls) != 0 {
			t.Errorf("expected no QueryTraces call for a rejected request, got %d", len(calls))
		}
	})

	t.Run("29 days succeeds", func(t *testing.T) {
		clientSession, fake := setupTestServer(t)
		ctx := context.Background()

		start := end.Add(-29 * 24 * time.Hour)
		result, err := clientSession.CallTool(ctx, &gomcp.CallToolParams{
			Name: "list_traces",
			Arguments: map[string]any{
				"organization": testOrgName,
				"project":      testProjectName,
				"agent":        testAgentName,
				"environment":  testEnvName,
				"start_time":   start.Format(time.RFC3339),
				"end_time":     end.Format(time.RFC3339),
			},
		})
		if err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got tool error: %+v", result.Content)
		}
		if calls := fake.calls["QueryTraces"]; len(calls) != 1 {
			t.Errorf("expected exactly one QueryTraces call, got %d", len(calls))
		}
	})
}
