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

package observer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient starts a stub OAuth2 token server and returns a Client wired
// against srv, so the auth round trip in doWithAuth exercises real HTTP.
func newTestClient(t *testing.T, srv *httptest.Server) Client {
	t.Helper()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"t","expires_in":3600}`))
	}))
	t.Cleanup(tokenSrv.Close)

	auth := NewAuthProvider(tokenSrv.URL, "client-id", "client-secret")
	return NewClient(srv.URL, auth, "default")
}

func TestQueryLogs_ComponentSearchScope(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"logs": [
				{"timestamp": "2026-01-01T00:00:00Z", "log": "hello", "level": "INFO"},
				{"timestamp": "2026-01-01T00:00:01Z", "log": "world"}
			],
			"total": 2,
			"tookMs": 5
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	project := "proj"
	req := LogsQueryRequest{
		StartTime: start,
		EndTime:   end,
		SearchScope: ComponentSearchScope{
			Namespace: "ns",
			Project:   &project,
		},
	}

	resp, err := client.QueryLogs(context.Background(), req)
	if err != nil {
		t.Fatalf("QueryLogs returned error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v1/logs/query" {
		t.Errorf("expected path /api/v1/logs/query, got %s", gotPath)
	}

	var decoded map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	scope, ok := decoded["searchScope"].(map[string]any)
	if !ok {
		t.Fatalf("expected searchScope object in request body, got %v", decoded["searchScope"])
	}
	if scope["namespace"] != "ns" {
		t.Errorf("expected searchScope.namespace = ns, got %v", scope["namespace"])
	}
	if scope["project"] != "proj" {
		t.Errorf("expected searchScope.project = proj, got %v", scope["project"])
	}
	if _, hasWorkflowRunName := scope["workflowRunName"]; hasWorkflowRunName {
		t.Errorf("expected no workflowRunName field for a ComponentSearchScope, got %v", scope)
	}

	if len(resp.Logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(resp.Logs))
	}
	if resp.Logs[0].Log == nil || *resp.Logs[0].Log != "hello" {
		t.Errorf("expected first log entry Log=hello, got %+v", resp.Logs[0])
	}
	if resp.Logs[0].Level == nil || *resp.Logs[0].Level != "INFO" {
		t.Errorf("expected first log entry Level=INFO, got %+v", resp.Logs[0])
	}
	if resp.Logs[1].Level != nil {
		t.Errorf("expected second (workflow-style) log entry to have no Level, got %+v", resp.Logs[1])
	}
	if resp.Total == nil || *resp.Total != 2 {
		t.Errorf("expected Total=2, got %v", resp.Total)
	}
	if resp.TookMs == nil || *resp.TookMs != 5 {
		t.Errorf("expected TookMs=5, got %v", resp.TookMs)
	}
}

func TestQueryLogs_WorkflowSearchScope(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs": []}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	runName := "wf-run-1"
	req := LogsQueryRequest{
		StartTime: time.Now(),
		EndTime:   time.Now(),
		SearchScope: WorkflowSearchScope{
			Namespace:       "ns",
			WorkflowRunName: &runName,
		},
	}

	if _, err := client.QueryLogs(context.Background(), req); err != nil {
		t.Fatalf("QueryLogs returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	scope, ok := decoded["searchScope"].(map[string]any)
	if !ok {
		t.Fatalf("expected searchScope object in request body, got %v", decoded["searchScope"])
	}
	if scope["namespace"] != "ns" {
		t.Errorf("expected searchScope.namespace = ns, got %v", scope["namespace"])
	}
	if scope["workflowRunName"] != runName {
		t.Errorf("expected searchScope.workflowRunName = %s, got %v", runName, scope["workflowRunName"])
	}
	if _, hasProject := scope["project"]; hasProject {
		t.Errorf("expected no project field for a WorkflowSearchScope, got %v", scope)
	}
}

func TestQueryMetrics(t *testing.T) {
	var gotMethod, gotPath string
	var gotReq MetricsQueryRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"cpuUsage": [{"timestamp": "2026-01-01T00:00:00Z", "value": 0.5}],
			"memoryUsage": [{"timestamp": "2026-01-01T00:00:00Z", "value": 1024}]
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	component := "comp"
	req := MetricsQueryRequest{
		StartTime: time.Now(),
		EndTime:   time.Now(),
		Metric:    "resource",
		SearchScope: ComponentSearchScope{
			Namespace: "ns",
			Component: &component,
		},
	}

	resp, err := client.QueryMetrics(context.Background(), req)
	if err != nil {
		t.Fatalf("QueryMetrics returned error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v1/metrics/query" {
		t.Errorf("expected path /api/v1/metrics/query, got %s", gotPath)
	}
	if gotReq.SearchScope.Namespace != "ns" || gotReq.SearchScope.Component == nil || *gotReq.SearchScope.Component != "comp" {
		t.Errorf("expected round-tripped searchScope ns/comp, got %+v", gotReq.SearchScope)
	}
	if gotReq.Metric != "resource" {
		t.Errorf("expected metric=resource, got %q", gotReq.Metric)
	}

	if resp.CpuUsage == nil || len(*resp.CpuUsage) != 1 || (*resp.CpuUsage)[0].Value == nil || *(*resp.CpuUsage)[0].Value != 0.5 {
		t.Errorf("expected CpuUsage[0].Value=0.5, got %+v", resp.CpuUsage)
	}
	if resp.MemoryUsage == nil || len(*resp.MemoryUsage) != 1 || (*resp.MemoryUsage)[0].Value == nil || *(*resp.MemoryUsage)[0].Value != 1024 {
		t.Errorf("expected MemoryUsage[0].Value=1024, got %+v", resp.MemoryUsage)
	}
	if resp.CpuRequests != nil {
		t.Errorf("expected CpuRequests nil (absent from JSON), got %+v", resp.CpuRequests)
	}
}
