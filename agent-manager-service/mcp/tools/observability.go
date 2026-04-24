package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

type runtimeLogsInput struct {
	OrgName      string   `json:"org_name"`
	ProjectName  string   `json:"project_name"`
	AgentName    string   `json:"agent_name"`
	Environment  string   `json:"environment"`
	StartTime    string   `json:"start_time"`
	EndTime      string   `json:"end_time"`
	Limit        *int     `json:"limit"`
	SortOrder    string   `json:"sort_order"`
	LogLevels    []string `json:"log_levels"`
	SearchPhrase string   `json:"search_phrase"`
}

type getMetricsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	Environment string `json:"environment"`
	TimeRange   string `json:"time_range"`
}

func (t *Toolsets) registerMetricsTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name: "get_metrics",
		Description: "Return CPU and memory usage metrics for an agent over a selected time range. " +
			"Metrics describe runtime resource consumption for a deployment in a specific environment.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"environment":  stringProperty("Optional. Environment name."),
			"time_range": map[string]any{
				"type":        "string",
				"description": "Optional. Time range preset. One of: 10m, 30m, 1h, 3h, 6h, 12h, 1d, 3d, 7d, 30d. Defaults to 7d.",
				"enum":        []any{"10m", "30m", "1h", "3h", "6h", "12h", "1d", "3d", "7d", "30d"},
			},
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("get_metrics", getMetrics(t.AgentToolset)))
}

func (t *Toolsets) registerRuntimeLogTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name: "get_runtime_logs",
		Description: "Return runtime logs for an agent. " +
			"Runtime logs are the application logs emitted by a deployed agent, and they can be filtered by time window, log level, sort order, or text search.",
		InputSchema: createSchema(map[string]any{
			"org_name":      stringProperty("Optional. Organization name."),
			"project_name":  stringProperty("Required. Project name where the agent exists."),
			"agent_name":    stringProperty("Required. Agent name to fetch runtime logs for."),
			"environment":   stringProperty("Optional. Environment name."),
			"start_time":    stringProperty("Optional. RFC3339 start time (UTC). Defaults to last 24h if omitted."),
			"end_time":      stringProperty("Optional. RFC3339 end time (UTC). Defaults to now if omitted."),
			"limit":         intProperty("Optional. Max number of log entries (1-10000)."),
			"sort_order":    stringProperty("Optional. Sort order: asc or desc."),
			"log_levels":    arrayProperty("Optional. Filter by log levels (DEBUG, INFO, WARN, ERROR).", map[string]any{"type": "string"}),
			"search_phrase": stringProperty("Optional. Search phrase to filter logs by content."),
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("get_runtime_logs", getRuntimeLogs(t.RuntimeLogToolset)))
}

func getMetrics(handler AgentToolsetHandler) func(context.Context, *gomcp.CallToolRequest, getMetricsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getMetricsInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}

		env := resolveEnv(input.Environment)
		start, end, err := resolveMetricsTimeRange(input.TimeRange)
		if err != nil {
			return nil, nil, err
		}

		payload := spec.MetricsFilterRequest{
			EnvironmentName: env,
			StartTime:       start,
			EndTime:         end,
		}

		result, err := handler.GetAgentMetrics(ctx, orgName, input.ProjectName, input.AgentName, payload)
		if err != nil {
			return nil, nil, wrapToolError("get_metrics", err)
		}
		return handleToolResult(result, nil)
	}
}

func getRuntimeLogs(handler RuntimeLogToolsetHandler) func(context.Context, *gomcp.CallToolRequest, runtimeLogsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input runtimeLogsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.Limit != nil && (*input.Limit < 1 || *input.Limit > 10000) {
			return nil, nil, fmt.Errorf("limit must be between 1 and 10000")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		env := resolveEnv(input.Environment)
		start, end, err := resolveTimeWindow(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}
		sortOrder := defaultSortOrder(input.SortOrder)

		levels, err := normalizeLogLevels(input.LogLevels)
		if err != nil {
			return nil, nil, err
		}

		var limit *int32
		if input.Limit != nil {
			value := int32(*input.Limit)
			limit = &value
		}

		var search *string
		if strings.TrimSpace(input.SearchPhrase) != "" {
			value := strings.TrimSpace(input.SearchPhrase)
			search = &value
		}

		req := spec.LogFilterRequest{
			EnvironmentName: env,
			StartTime:       start,
			EndTime:         end,
			Limit:           limit,
			SortOrder:       &sortOrder,
			LogLevels:       levels,
			SearchPhrase:    search,
		}

		result, err := handler.GetRuntimeLogs(ctx, orgName, input.ProjectName, input.AgentName, req)
		if err != nil {
			return nil, nil, wrapToolError("get_runtime_logs", err)
		}

		reduced := reduceLogsResponse(result)
		return handleToolResult(reduced, nil)
	}
}

func normalizeLogLevels(levels []string) ([]string, error) {
	if len(levels) == 0 {
		return nil, nil
	}
	allowed := map[string]bool{
		"DEBUG": true,
		"INFO":  true,
		"WARN":  true,
		"ERROR": true,
	}
	out := make([]string, 0, len(levels))
	for _, lvl := range levels {
		value := strings.ToUpper(strings.TrimSpace(lvl))
		if value == "" {
			continue
		}
		if !allowed[value] {
			return nil, fmt.Errorf("invalid log level: %s", lvl)
		}
		out = append(out, value)
	}
	return out, nil
}

func resolveMetricsTimeRange(timeRange string) (string, string, error) {
	preset := strings.TrimSpace(strings.ToLower(timeRange))
	if preset == "" {
		preset = "7d"
	}

	duration, ok := map[string]time.Duration{
		"10m": 10 * time.Minute,
		"30m": 30 * time.Minute,
		"1h":  time.Hour,
		"3h":  3 * time.Hour,
		"6h":  6 * time.Hour,
		"12h": 12 * time.Hour,
		"1d":  24 * time.Hour,
		"3d":  3 * 24 * time.Hour,
		"7d":  7 * 24 * time.Hour,
		"30d": 30 * 24 * time.Hour,
	}[preset]
	if !ok {
		return "", "", fmt.Errorf("invalid time_range: %s", timeRange)
	}

	endTime := time.Now().UTC()
	startTime := endTime.Add(-duration)
	return startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), nil
}

const (
	defaultTraceListLimit   = 10
	defaultTraceExportLimit = 100
	maxTraceListLimit       = 100
	maxTraceExportLimit     = 1000

	defaultMaxLatencyMs = 30000.0
	defaultMaxTokens    = 10000
	defaultMinLength    = 1
	defaultMaxLength    = 10000
	defaultMaxSpanCount = 40
)

// Input structures for trace tools
type listTracesInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`

	Environment string `json:"environment"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Limit       *int   `json:"limit"`
	Offset      *int   `json:"offset"`
	SortOrder   string `json:"sort_order"`
	IncludeIO   *bool  `json:"include_io"`
}

type getTracesInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`

	Environment string `json:"environment"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Limit       *int   `json:"limit"`
	Offset      *int   `json:"offset"`
	SortOrder   string `json:"sort_order"`
}

type filterTracesInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`

	Environment string `json:"environment"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Condition   string `json:"condition"`
	Limit       *int   `json:"limit"`

	MaxLatencyMs *int `json:"max_latency_ms"`
	MaxTokens    *int `json:"max_tokens"`
	MinLength    *int `json:"min_length"`
	MaxLength    *int `json:"max_length"`
	MaxSpanCount *int `json:"max_span_count"`
}

type getTraceDetailsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	TraceID     string `json:"trace_id"`
	Environment string `json:"environment"`
}

func (t *Toolsets) registerTraceTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name: "list_traces",
		Description: "List recent traces for an agent within a time window. " +
			"A trace is a single end-to-end execution record for an agent request. " +
			"This summary view returns high-level trace metadata and can optionally include inputs and outputs of the execution.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("optional. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent exists."),
			"agent_name":   stringProperty("Required. Agent name to check traces for."),
			"environment":  stringProperty("Optional. Environment name."),
			"start_time":   stringProperty("Optional. RFC3339 start time (UTC). Defaults to 24h ago."),
			"end_time":     stringProperty("Optional. RFC3339 end time (UTC). Defaults to current time."),
			"limit": map[string]any{
				"type":        "integer",
				"description": "Optional. Max number of traces to return.",
				"minimum":     1,
				"maximum":     maxTraceListLimit,
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Optional. Pagination offset (>= 0).",
				"minimum":     0,
			},
			"sort_order": enumProperty("Optional. Sort order for traces: desc (newest first) or asc (oldest first).", []string{"desc", "asc"}),
			"include_io": map[string]any{
				"type":        "boolean",
				"description": "Optional. Include input/output fields in the traces.",
			},
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("list_traces", listTraces(t.TraceToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "get_traces",
		Description: "List recent traces for an agent in a time window with full span details. " +
			"A trace is a single end-to-end execution record for an agent which contains spans that record the internal steps of an execution, such as agent, tool, retriever, or LLM activity.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Required. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent exists."),
			"agent_name":   stringProperty("Required. Agent name to export traces for."),
			"environment":  stringProperty("Optional. Environment name."),
			"start_time":   stringProperty("Optional. RFC3339 start time (UTC). Defaults to 24h ago."),
			"end_time":     stringProperty("Optional. RFC3339 end time (UTC). Defaults to current time."),
			"limit": map[string]any{
				"type":        "integer",
				"description": "Optional. Max number of traces to return (1-1000).",
				"minimum":     1,
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Optional. Pagination offset (>= 0).",
				"minimum":     0,
			},
			"sort_order": enumProperty("Optional. Sort order for traces: desc (newest first) or asc (oldest first).", []string{"desc", "asc"}),
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("get_traces", getTraces(t.TraceToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "filter_traces",
		Description: "List traces in a time window that match a specific condition. " +
			"Conditions identify traces with patterns such as errors, high latency, high token usage, tool call failures, output length violations, or excessive span counts. " +
			"Returns full trace details and spans for matching traces.",
		InputSchema: createSchema(map[string]any{
			"org_name":       stringProperty("Optional. Organization name."),
			"project_name":   stringProperty("Required. Project name."),
			"agent_name":     stringProperty("Required. Agent name."),
			"environment":    stringProperty("Optional. Environment name."),
			"start_time":     stringProperty("Optional. RFC3339 start time (UTC). Defaults to 24h ago."),
			"end_time":       stringProperty("Optional. RFC3339 end time (UTC). Defaults to current time."),
			"condition":      stringProperty("Required. Filter condition: `error_status` for traces with errors, `length_compliance_violation` for outputs outside the configured length range, `high_latency` for slow traces, `high_token_usage` for token-heavy traces, `tool_call_fails` for traces with failed tool calls, or `excessive_steps` for traces with too many spans."),
			"limit":          intProperty("Optional. Max number of traces to return after filtering."),
			"max_latency_ms": intProperty("Optional. Max latency in milliseconds for high_latency. Defaults to 30000."),
			"max_tokens":     intProperty("Optional. Max tokens for high_token_usage. Defaults to 10000."),
			"min_length":     intProperty("Optional. Min output length for length_compliance_violation. Defaults to 1."),
			"max_length":     intProperty("Optional. Max output length for length_compliance_violation. Defaults to 10000."),
			"max_span_count": intProperty("Optional. Max span count for excessive_steps. Defaults to 40."),
		}, []string{"project_name", "agent_name", "condition"}),
	}, withToolLogging("filter_traces", filterTraces(t.TraceToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "get_trace_details",
		Description: "Return the full details for a single trace. " +
			"A trace ID identifies one end-to-end execution record, and the response includes trace metadata plus its spans.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Required. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"trace_id":     stringProperty("Required. Trace ID to fetch."),
			"environment":  stringProperty("Optional. Environment name."),
		}, []string{"project_name", "agent_name", "trace_id"}),
	}, withToolLogging("get_trace_details", getTraceDetails(t.TraceToolset)))
}

func listTraces(handler TraceToolsetHandler) func(context.Context, *gomcp.CallToolRequest, listTracesInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listTracesInput) (*gomcp.CallToolResult, any, error) {

		// Input validation
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.Limit != nil && (*input.Limit < 1 || *input.Limit > maxTraceListLimit) {
			return nil, nil, fmt.Errorf("limit must be between 1 and %d", maxTraceListLimit)
		}
		if input.Offset != nil && *input.Offset < 0 {
			return nil, nil, fmt.Errorf("offset must be >= 0")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		env := resolveEnv(input.Environment)
		start, end, err := resolveTimeWindow(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}
		sortOrder := defaultSortOrder(input.SortOrder)

		limit := defaultTraceListLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		offset := 0
		if input.Offset != nil {
			offset = *input.Offset
		}

		// Call service layer
		result, err := handler.ListTraces(ctx, orgName, input.ProjectName, input.AgentName, env, start, end, sortOrder, limit, offset)
		if err != nil {
			return nil, nil, wrapToolError("list_traces", err)
		}

		includeIO := input.IncludeIO != nil && *input.IncludeIO
		reduced := reduceTraceOverviewResponseRaw(result, includeIO)
		reduced["org_name"] = orgName
		reduced["project_name"] = input.ProjectName
		reduced["agent_name"] = input.AgentName
		reduced["environment"] = env
		reduced["start_time"] = start
		reduced["end_time"] = end
		reduced["limit"] = limit
		reduced["offset"] = offset

		return handleToolResult(reduced, nil)
	}
}

func getTraces(handler TraceToolsetHandler) func(context.Context, *gomcp.CallToolRequest, getTracesInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getTracesInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.Limit != nil && (*input.Limit < 1 || *input.Limit > maxTraceExportLimit) {
			return nil, nil, fmt.Errorf("limit must be between 1 and %d", maxTraceExportLimit)
		}
		if input.Offset != nil && *input.Offset < 0 {
			return nil, nil, fmt.Errorf("offset must be >= 0")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		env := resolveEnv(input.Environment)

		start, end, err := resolveTimeWindow(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}
		sortOrder := defaultSortOrder(input.SortOrder)

		limit := defaultTraceExportLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		offset := 0
		if input.Offset != nil {
			offset = *input.Offset
		}

		result, err := handler.ExportTraces(ctx, orgName, input.ProjectName, input.AgentName, env, start, end, sortOrder, limit, offset)
		if err != nil {
			return nil, nil, wrapToolError("get_traces", err)
		}

		reduced := reduceTracesWithSpansRaw(result, input.Limit)
		reduced["totalCount"] = result["totalCount"]
		reduced["org_name"] = orgName
		reduced["project_name"] = input.ProjectName
		reduced["agent_name"] = input.AgentName
		reduced["environment"] = env
		reduced["start_time"] = start
		reduced["end_time"] = end

		return handleToolResult(reduced, nil)
	}
}

func filterTraces(handler TraceToolsetHandler) func(context.Context, *gomcp.CallToolRequest, filterTracesInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input filterTracesInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if strings.TrimSpace(input.Condition) == "" {
			return nil, nil, fmt.Errorf("condition is required")
		}
		condition := strings.TrimSpace(strings.ToLower(input.Condition))
		if !isSupportedCondition(condition) {
			return nil, nil, fmt.Errorf("unsupported condition: %s", condition)
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		env := resolveEnv(input.Environment)
		start, end, err := resolveTimeWindow(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}

		scanLimit := maxTraceExportLimit
		result, err := handler.ExportTraces(ctx, orgName, input.ProjectName, input.AgentName, env, start, end, "desc", scanLimit, 0)
		if err != nil {
			return nil, nil, wrapToolError("filter_traces", err)
		}

		filtered := make([]map[string]any, 0)
		for _, traceAny := range getSlice(result["traces"]) {
			traceMap := getMap(traceAny)
			if traceMap == nil {
				continue
			}
			if !traceMatchesConditionRaw(traceMap, condition, input) {
				continue
			}
			filtered = append(filtered, reduceTraceWithAMPAttributesRaw(traceMap))
			if input.Limit != nil && len(filtered) >= *input.Limit {
				break
			}
		}

		response := map[string]any{
			"condition":    condition,
			"count":        len(filtered),
			"traces":       filtered,
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"environment":  env,
			"start_time":   start,
			"end_time":     end,
		}
		return handleToolResult(response, nil)
	}
}

func getTraceDetails(handler TraceToolsetHandler) func(context.Context, *gomcp.CallToolRequest, getTraceDetailsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getTraceDetailsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.TraceID == "" {
			return nil, nil, fmt.Errorf("trace_id is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		env := resolveEnv(input.Environment)

		result, err := handler.GetTraceDetails(ctx, orgName, input.ProjectName, input.AgentName, input.TraceID, env)
		if err != nil {
			return nil, nil, wrapToolError("get_trace_details", err)
		}

		reduced := reduceTraceDetailsRaw(result, input.TraceID)
		reduced["org_name"] = orgName
		reduced["project_name"] = input.ProjectName
		reduced["agent_name"] = input.AgentName
		reduced["environment"] = env

		return handleToolResult(reduced, nil)
	}
}

func reduceTraceOverviewResponseRaw(resp map[string]any, includeIO bool) map[string]any {
	if resp == nil {
		return map[string]any{"traces": []map[string]any{}, "count": 0, "totalCount": 0}
	}
	tracesAny := getSlice(resp["traces"])
	traces := make([]map[string]any, 0, len(tracesAny))
	for _, traceAny := range tracesAny {
		traceMap := getMap(traceAny)
		if traceMap == nil {
			continue
		}
		item := map[string]any{
			"traceId":         getString(traceMap["traceId"]),
			"rootSpanId":      getString(traceMap["rootSpanId"]),
			"rootSpanName":    getString(traceMap["rootSpanName"]),
			"rootSpanKind":    getString(traceMap["rootSpanKind"]),
			"startTime":       traceMap["startTime"],
			"endTime":         traceMap["endTime"],
			"durationInNanos": traceMap["durationInNanos"],
			"spanCount":       traceMap["spanCount"],
			"tokenUsage":      traceMap["tokenUsage"],
			"status":          traceMap["status"],
		}
		if includeIO {
			if v, ok := traceMap["input"]; ok {
				item["input"] = v
			}
			if v, ok := traceMap["output"]; ok {
				item["output"] = v
			}
		}
		traces = append(traces, item)
	}
	return map[string]any{
		"traces":     traces,
		"count":      len(traces),
		"totalCount": resp["totalCount"],
	}
}

func reduceTracesWithSpansRaw(resp map[string]any, limit *int) map[string]any {
	tracesAny := getSlice(resp["traces"])
	if limit != nil && *limit < len(tracesAny) {
		tracesAny = tracesAny[:*limit]
	}

	reduced := make([]map[string]any, 0, len(tracesAny))
	for _, traceAny := range tracesAny {
		traceMap := getMap(traceAny)
		if traceMap == nil {
			continue
		}
		reduced = append(reduced, reduceTraceWithAMPAttributesRaw(traceMap))
	}

	return map[string]any{
		"traces": reduced,
		"count":  len(reduced),
	}
}

func reduceTraceWithAMPAttributesRaw(trace map[string]any) map[string]any {
	spansAny := getSlice(trace["spans"])
	reducedSpans := make([]map[string]any, 0, len(spansAny))
	for _, spanAny := range spansAny {
		spanMap := getMap(spanAny)
		if spanMap == nil {
			continue
		}
		parent := getString(spanMap["parentSpanId"])
		reducedSpans = append(reducedSpans, map[string]any{
			"spanId":          getString(spanMap["spanId"]),
			"parentSpanId":    parent,
			"name":            getString(spanMap["name"]),
			"durationInNanos": spanMap["durationInNanos"],
			"ampAttributes":   spanMap["ampAttributes"],
		})
	}

	return map[string]any{
		"traceId":         getString(trace["traceId"]),
		"rootSpanId":      getString(trace["rootSpanId"]),
		"durationInNanos": trace["durationInNanos"],
		"spanCount":       trace["spanCount"],
		"tokenUsage":      trace["tokenUsage"],
		"status":          trace["status"],
		"input":           trace["input"],
		"output":          trace["output"],
		"spans":           reducedSpans,
	}
}

func reduceTraceDetailsRaw(resp map[string]any, traceID string) map[string]any {
	reducedSpans := make([]map[string]any, 0)
	if rawSpans, ok := resp["spans"].([]any); ok {
		for _, span := range rawSpans {
			spanMap, ok := span.(map[string]any)
			if !ok {
				continue
			}
			parent := ""
			if v, ok := spanMap["parentSpanId"]; ok && v != nil {
				parent = asString(v)
			}
			reducedSpans = append(reducedSpans, map[string]any{
				"spanId":          asString(spanMap["spanId"]),
				"parentSpanId":    parent,
				"name":            asString(spanMap["name"]),
				"durationInNanos": spanMap["durationInNanos"],
				"ampAttributes":   spanMap["ampAttributes"],
			})
		}
	}
	return map[string]any{
		"traceId":    traceID,
		"spanCount":  resp["totalCount"],
		"tokenUsage": resp["tokenUsage"],
		"status":     resp["status"],
		"spans":      reducedSpans,
	}
}

func asString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func isSupportedCondition(condition string) bool {
	switch condition {
	case "error_status", "length_compliance_violation", "high_latency", "high_token_usage", "tool_call_fails", "excessive_steps":
		return true
	default:
		return false
	}
}

func traceMatchesConditionRaw(trace map[string]any, condition string, input filterTracesInput) bool {
	switch condition {
	case "error_status":
		status := getMap(trace["status"])
		return getInt(status["errorCount"]) > 0
	case "length_compliance_violation":
		minLength := defaultMinLength
		if input.MinLength != nil {
			minLength = *input.MinLength
		}
		maxLength := defaultMaxLength
		if input.MaxLength != nil {
			maxLength = *input.MaxLength
		}
		length := outputLength(trace["output"])
		return length < minLength || length > maxLength
	case "high_latency":
		maxLatency := defaultMaxLatencyMs
		if input.MaxLatencyMs != nil {
			maxLatency = float64(*input.MaxLatencyMs)
		}
		nanos := getFloat(trace["durationInNanos"])
		latencyMs := nanos / 1_000_000.0
		return latencyMs > maxLatency
	case "high_token_usage":
		maxTokens := defaultMaxTokens
		if input.MaxTokens != nil {
			maxTokens = *input.MaxTokens
		}
		tokenUsage := getMap(trace["tokenUsage"])
		totalTokens := getInt(tokenUsage["totalTokens"])
		return totalTokens > maxTokens
	case "tool_call_fails":
		return hasToolCallFailuresRaw(getSlice(trace["spans"]))
	case "excessive_steps":
		maxSpanCount := defaultMaxSpanCount
		if input.MaxSpanCount != nil {
			maxSpanCount = *input.MaxSpanCount
		}
		spanCount := getInt(trace["spanCount"])
		return spanCount > maxSpanCount
	default:
		return false
	}
}

func outputLength(value any) int {
	switch v := value.(type) {
	case nil:
		return 0
	case string:
		return utf8.RuneCountInString(v)
	case []byte:
		return len(v)
	case []any:
		return len(v)
	case map[string]any:
		return len(v)
	default:
		return 0
	}
}

func hasToolCallFailuresRaw(spans []any) bool {
	for _, spanAny := range spans {
		span := getMap(spanAny)
		if span == nil {
			continue
		}
		ampAttrs := getMap(span["ampAttributes"])
		if ampAttrs == nil {
			continue
		}
		if strings.ToLower(getString(ampAttrs["kind"])) != "tool" {
			continue
		}
		status := getMap(ampAttrs["status"])
		if status == nil {
			continue
		}
		if isTruthy(status["error"]) {
			return true
		}
	}
	return false
}

type toolCall struct {
	name string
	args string
}

func extractMessagesFromAny(value any) []spec.PromptMessage {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return nil
		}
		var msgs []spec.PromptMessage
		if err := json.Unmarshal([]byte(raw), &msgs); err == nil {
			return msgs
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil
		}
		return extractMessagesFromMap(payload)
	case map[string]any:
		return extractMessagesFromMap(v)
	case []any:
		return decodePromptMessages(v)
	default:
		return nil
	}
}

func extractMessagesFromMap(payload map[string]any) []spec.PromptMessage {
	if payload == nil {
		return nil
	}
	if msgs, ok := payload["messages"]; ok {
		return decodePromptMessages(msgs)
	}
	if inputs, ok := payload["inputs"].(map[string]any); ok {
		if msgs, ok := inputs["messages"]; ok {
			return decodePromptMessages(msgs)
		}
	}
	return nil
}

func decodePromptMessages(value any) []spec.PromptMessage {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var msgs []spec.PromptMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil
	}
	return msgs
}

func getMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func getSlice(value any) []any {
	if value == nil {
		return nil
	}
	if s, ok := value.([]any); ok {
		return s
	}
	return nil
}

func getString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func getInt(value any) int {
	switch v := value.(type) {
	case nil:
		return 0
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
		if f, err := v.Float64(); err == nil {
			return int(f)
		}
		return 0
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int(f)
		}
		return 0
	default:
		return 0
	}
}

func getFloat(value any) float64 {
	switch v := value.(type) {
	case nil:
		return 0
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
		return 0
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
		return 0
	default:
		return 0
	}
}

func isTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	case int:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case float32:
		return v != 0
	default:
		return false
	}
}

func toMap(value any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
