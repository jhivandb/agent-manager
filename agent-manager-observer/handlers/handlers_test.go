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

package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newHandler creates a Handler with nil controllers — safe for validation-only
// tests because neither controller is ever called when a 400/405 is returned first.
func newHandler() *Handler {
	return NewHandler(nil, nil)
}

// baseParams returns a query string with all required parameters present,
// using the field names the handlers actually read (organization, agent).
func baseParams() string {
	return "organization=default&project=myproject&agent=myagent&environment=dev" +
		"&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z"
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("expected %d, got %d (body: %s)", want, rec.Code, rec.Body.String())
	}
}

func assertBadRequest(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// ── GetTraceOverviews ────────────────────────────────────────────────────────

func TestGetTraceOverviews_MissingNamespace(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?project=p&component=c&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceOverviews(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceOverviews_MissingProject(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?namespace=default&component=c&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceOverviews(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceOverviews_MissingComponent(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?namespace=default&project=p&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceOverviews(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceOverviews_MissingEnvironment(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?namespace=default&project=p&component=c&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceOverviews(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceOverviews_MissingStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?namespace=default&project=p&component=c&environment=e&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceOverviews(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceOverviews_MissingEndTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?namespace=default&project=p&component=c&environment=e&startTime=2026-04-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceOverviews(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceOverviews_InvalidSortOrder(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?"+baseParams()+"&sortOrder=invalid", nil)
	rec := httptest.NewRecorder()
	h.GetTraceOverviews(rec, r)
	assertBadRequest(t, rec)
}

// ── GetTraceSpans ────────────────────────────────────────────────────────────

func TestGetTraceSpans_MissingNamespace(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/abc123/spans?startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceSpans(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceSpans_MissingStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/abc123/spans?namespace=default&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceSpans(rec, r)
	assertBadRequest(t, rec)
}

func TestGetTraceSpans_MissingEndTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/abc123/spans?namespace=default&startTime=2026-04-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.GetTraceSpans(rec, r)
	assertBadRequest(t, rec)
}

// ── ExportTraces ─────────────────────────────────────────────────────────────

func TestExportTraces_MissingNamespace(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/export?project=p&component=c&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.ExportTraces(rec, r)
	assertBadRequest(t, rec)
}

func TestExportTraces_MissingProject(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/export?namespace=default&component=c&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.ExportTraces(rec, r)
	assertBadRequest(t, rec)
}

func TestExportTraces_MissingComponent(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/export?namespace=default&project=p&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.ExportTraces(rec, r)
	assertBadRequest(t, rec)
}

func TestExportTraces_MissingEnvironment(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/export?namespace=default&project=p&component=c&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.ExportTraces(rec, r)
	assertBadRequest(t, rec)
}

func TestExportTraces_MissingStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/export?namespace=default&project=p&component=c&environment=e&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.ExportTraces(rec, r)
	assertBadRequest(t, rec)
}

func TestExportTraces_MissingEndTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/traces/export?namespace=default&project=p&component=c&environment=e&startTime=2026-04-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.ExportTraces(rec, r)
	assertBadRequest(t, rec)
}

// ── GetLogs ──────────────────────────────────────────────────────────────────

func TestGetLogs_MissingOrganization(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?project=p&agent=a&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_MissingProject(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&agent=a&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_MissingAgent(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_MissingEnvironment(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_MissingStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&environment=e&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_MissingEndTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&environment=e&startTime=2026-04-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_InvalidStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&environment=e&startTime=not-a-time&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_InvalidEndTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&environment=e&startTime=2026-04-01T00:00:00Z&endTime=not-a-time", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_EndTimeBeforeStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&environment=e&startTime=2026-04-06T23:59:59Z&endTime=2026-04-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_StartTimeInFuture(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&environment=e&startTime=2099-01-01T00:00:00Z&endTime=2099-01-02T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_TimeRangeExceeds14Days(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?organization=default&project=p&agent=a&environment=e&startTime=2026-01-01T00:00:00Z&endTime=2026-01-20T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_InvalidLogLevel(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?"+baseParams()+"&logLevels=INFO,BOGUS", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_LimitTooHigh(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?"+baseParams()+"&limit=10001", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_LimitZero(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?"+baseParams()+"&limit=0", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_InvalidSortOrder(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs?"+baseParams()+"&sortOrder=invalid", nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetLogs_MethodNotAllowed(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/logs?"+baseParams(), nil)
	rec := httptest.NewRecorder()
	h.GetLogs(rec, r)
	assertStatus(t, rec, http.StatusMethodNotAllowed)
}

// ── GetBuildLogs ─────────────────────────────────────────────────────────────

func TestGetBuildLogs_MissingOrganization(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/build-logs?buildName=build-1", nil)
	rec := httptest.NewRecorder()
	h.GetBuildLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetBuildLogs_MissingBuildName(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/build-logs?organization=default", nil)
	rec := httptest.NewRecorder()
	h.GetBuildLogs(rec, r)
	assertBadRequest(t, rec)
}

func TestGetBuildLogs_MethodNotAllowed(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/build-logs?organization=default&buildName=build-1", nil)
	rec := httptest.NewRecorder()
	h.GetBuildLogs(rec, r)
	assertStatus(t, rec, http.StatusMethodNotAllowed)
}

// ── GetMetrics ───────────────────────────────────────────────────────────────

func TestGetMetrics_MissingOrganization(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?project=p&agent=a&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertBadRequest(t, rec)
}

func TestGetMetrics_MissingProject(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?organization=default&agent=a&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertBadRequest(t, rec)
}

func TestGetMetrics_MissingAgent(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?organization=default&project=p&environment=e&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertBadRequest(t, rec)
}

func TestGetMetrics_MissingEnvironment(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?organization=default&project=p&agent=a&startTime=2026-04-01T00:00:00Z&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertBadRequest(t, rec)
}

func TestGetMetrics_MissingStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?organization=default&project=p&agent=a&environment=e&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertBadRequest(t, rec)
}

func TestGetMetrics_MissingEndTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?organization=default&project=p&agent=a&environment=e&startTime=2026-04-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertBadRequest(t, rec)
}

func TestGetMetrics_InvalidStartTime(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?organization=default&project=p&agent=a&environment=e&startTime=not-a-time&endTime=2026-04-06T23:59:59Z", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertBadRequest(t, rec)
}

func TestGetMetrics_MethodNotAllowed(t *testing.T) {
	h := newHandler()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/metrics?"+baseParams(), nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, r)
	assertStatus(t, rec, http.StatusMethodNotAllowed)
}
