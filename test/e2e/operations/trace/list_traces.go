package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
)

// httpStatusFromErr extracts the HTTP status code from an error produced by
// ListTraces, which formats non-2xx responses as "status %d: <body>". Returns
// (code, true) when a leading status code is present, (0, false) otherwise.
func httpStatusFromErr(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var code int
	if _, scanErr := fmt.Sscanf(err.Error(), "status %d:", &code); scanErr == nil {
		return code, true
	}
	return 0, false
}

// ListTracesParams holds query parameters for listing traces.
type ListTracesParams struct {
	Organization string
	Project      string
	Agent        string
	Environment  string
	StartTime    string // ISO 8601
	EndTime      string // ISO 8601
	Limit        int
}

// ListTraces attempts to list traces from agent-manager-observer.
// Returns the response and an error on failure, allowing callers to decide
// whether to retry or fail.
func ListTraces(client *framework.AMPClient, params *ListTracesParams) (framework.TraceOverviewListResponse, error) {
	q := url.Values{}
	q.Set("organization", params.Organization)
	q.Set("project", params.Project)
	q.Set("agent", params.Agent)
	q.Set("environment", params.Environment)
	q.Set("startTime", params.StartTime)
	q.Set("endTime", params.EndTime)
	if params.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", params.Limit))
	}
	q.Set("sortOrder", "desc")

	tracesURL := fmt.Sprintf("%s/api/v1/traces?%s", client.Cfg().ObserverBaseURL, q.Encode())

	resp, err := client.DoRaw("GET", tracesURL)
	if err != nil {
		return framework.TraceOverviewListResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return framework.TraceOverviewListResponse{}, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return framework.TraceOverviewListResponse{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result framework.TraceOverviewListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return framework.TraceOverviewListResponse{}, fmt.Errorf("decode: %w", err)
	}
	return result, nil
}

// WaitForTracesParams holds parameters for waiting on traces to appear.
type WaitForTracesParams struct {
	Organization string
	Project      string
	Agent        string
	Environment  string
	Timeout      time.Duration // default: 2 minutes
}

// WaitForTraces polls the traces API until at least one trace appears.
func WaitForTraces(client *framework.AMPClient, params *WaitForTracesParams) framework.TraceOverviewListResponse {
	timeout := params.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}

	startTime := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	endTime := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)

	// scope describes exactly what we queried so a failure message is self-explanatory.
	scope := fmt.Sprintf("org=%s project=%s agent=%s env=%s window=%s..%s",
		params.Organization, params.Project, params.Agent, params.Environment, startTime, endTime)

	var lastDiag string
	framework.AttachOnFailure("traces: last query result", func() string { return lastDiag })

	var result framework.TraceOverviewListResponse
	attempt := 0
	Eventually(func(g Gomega) {
		attempt++
		var err error
		result, err = ListTraces(client, &ListTracesParams{
			Organization: params.Organization,
			Project:      params.Project,
			Agent:        params.Agent,
			Environment:  params.Environment,
			StartTime:    startTime,
			EndTime:      endTime,
			Limit:        10,
		})
		if err != nil {
			lastDiag = fmt.Sprintf("%s | attempt %d | request error: %v", scope, attempt, err)
			// A 4xx is a client/config error (bad scope, auth, missing route) that won't
			// fix itself by waiting — fail fast with the full status+body from ListTraces.
			// ListTraces formats non-2xx as "status %d: <body>", so parse the code rather
			// than substring-matching (a body containing "status 4" must not trip this).
			if code, ok := httpStatusFromErr(err); ok && code >= 400 && code < 500 {
				StopTrying(fmt.Sprintf("list traces request failed (%s): %v", scope, err)).Now()
			}
			ginkgo.GinkgoWriter.Printf("[traces] attempt %d (%s): request error: %v\n", attempt, scope, err)
		} else {
			lastDiag = fmt.Sprintf("%s | attempt %d | 200 OK, %d trace(s)", scope, attempt, len(result.Traces))
			ginkgo.GinkgoWriter.Printf("[traces] attempt %d (%s): 200 OK, %d trace(s)\n", attempt, scope, len(result.Traces))
		}
		g.Expect(err).NotTo(HaveOccurred(), "list traces request failed (%s)", scope)
		g.Expect(result.Traces).NotTo(BeEmpty(),
			"observer returned 200 with 0 traces for %s — the agent may be invoked successfully but its "+
				"spans attributed to a different environment_uid (check the agent token's environment_uid "+
				"and the per-env OTEL ingest route)", scope)
	}).WithTimeout(timeout).WithPolling(10 * time.Second).Should(Succeed())

	return result
}
