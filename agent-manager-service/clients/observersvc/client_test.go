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

package observersvc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAuthProvider is a minimal occlient.AuthProvider that counts invalidations,
// letting tests assert the 401-retry path actually refreshes the token.
type fakeAuthProvider struct {
	token         string
	invalidations int
}

func (f *fakeAuthProvider) GetToken(_ context.Context) (string, error) {
	return f.token, nil
}

func (f *fakeAuthProvider) InvalidateToken() {
	f.invalidations++
}

func newTestClient(t *testing.T, baseURL string, authProvider *fakeAuthProvider) *observerSvcClient {
	t.Helper()
	c, err := NewObserverClient(&Config{BaseURL: baseURL, AuthProvider: authProvider})
	require.NoError(t, err)
	client, ok := c.(*observerSvcClient)
	require.True(t, ok)
	return client
}

// TestGetWorkflowRunLogs_SendsExpectedQueryParamsAndDecodesResponse proves the
// query is exactly organization+buildName (not the legacy client's
// namespace/workflowRunName shape) and that the observer's wire response
// decodes into models.LogsResponse.
func TestGetWorkflowRunLogs_SendsExpectedQueryParamsAndDecodesResponse(t *testing.T) {
	var gotOrg, gotBuildName string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/build-logs", func(w http.ResponseWriter, r *http.Request) {
		gotOrg = r.URL.Query().Get("organization")
		gotBuildName = r.URL.Query().Get("buildName")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"logs":[{"timestamp":"2026-01-01T00:00:00Z","log":"hello","logLevel":"INFO"}],"totalCount":1,"tookMs":12.5}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(t, server.URL, &fakeAuthProvider{token: "tok"})

	got, err := client.GetWorkflowRunLogs(context.Background(), "org-1", "run-1")
	require.NoError(t, err)

	assert.Equal(t, "org-1", gotOrg)
	assert.Equal(t, "run-1", gotBuildName)
	require.Len(t, got.Logs, 1)
	assert.Equal(t, "hello", got.Logs[0].Log)
	assert.Equal(t, "INFO", got.Logs[0].LogLevel)
	assert.Equal(t, int32(1), got.TotalCount)
	assert.InDelta(t, float32(12.5), got.TookMs, 0.001)
}

// TestGetWorkflowRunLogs_RetriesOnceAfter401 mirrors doGetMap's existing
// 401-retry behavior: on a first 401 response the token is invalidated and the
// request retried once before giving up.
func TestGetWorkflowRunLogs_RetriesOnceAfter401(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/build-logs", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"logs":[],"totalCount":0,"tookMs":1}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	authProvider := &fakeAuthProvider{token: "tok"}
	client := newTestClient(t, server.URL, authProvider)

	got, err := client.GetWorkflowRunLogs(context.Background(), "org-1", "run-1")
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "expected exactly one retry after the 401")
	assert.Equal(t, 1, authProvider.invalidations)
	assert.NotNil(t, got)
}

// TestGetWorkflowRunLogs_WrapsErrorWithMethodPrefix proves failures surface
// through the observersvc.GetWorkflowRunLogs: %w wrapper so callers (and their
// error-log lines) can identify the failing client method.
func TestGetWorkflowRunLogs_WrapsErrorWithMethodPrefix(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/build-logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(t, server.URL, &fakeAuthProvider{token: "tok"})

	_, err := client.GetWorkflowRunLogs(context.Background(), "org-1", "run-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "observersvc.GetWorkflowRunLogs:")
}
