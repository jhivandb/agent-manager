package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

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
		Name:        "list_traces",
		Description: "List recent traces for an agent in a specific time window (summary view).",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("optional. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent exists."),
			"agent_name":   stringProperty("Required. Agent name to check traces for."),
			"environment":  stringProperty("Optional. Environment name."),
			"start_time":   stringProperty("Optional. RFC3339 start time (UTC). Defaults to last 24h."),
			"end_time":     stringProperty("Optional. RFC3339 end time (UTC). Defaults to now."),
			"limit": map[string]any{
				"type":        "integer",
				"description": "Optional. Max number of traces to return (1-100).",
				"minimum":     1,
				"maximum":     maxTraceListLimit,
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Optional. Pagination offset (>= 0).",
				"minimum":     0,
			},
			"sort_order": stringProperty("Optional. Sort order: desc (newest first) or asc."),
			"include_io": map[string]any{
				"type":        "boolean",
				"description": "Optional. Include input/output fields in the trace list.",
			},
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("list_traces", listTraces(t.TraceToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_traces",
		Description: "List recent traces for an agent in a specific time window with span details (detailed view)",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Required. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent exists."),
			"agent_name":   stringProperty("Required. Agent name to export traces for."),
			"environment":  stringProperty("Optional. Environment name."),
			"start_time":   stringProperty("Optional. RFC3339 start time (UTC). Defaults to last 24h."),
			"end_time":     stringProperty("Optional. RFC3339 end time (UTC). Defaults to now."),
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
			"sort_order": stringProperty("Optional. Sort order: desc (newest first) or asc."),
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("get_traces", getTraces(t.TraceToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "filter_traces",
		Description: "Filter traces by a specific condition for a time window. Returns full traces (traces + spans) that match the condition.",
		InputSchema: createSchema(map[string]any{
			"org_name":       stringProperty("Optional. Organization name."),
			"project_name":   stringProperty("Required. Project name."),
			"agent_name":     stringProperty("Required. Agent name."),
			"environment":    stringProperty("Optional. Environment name."),
			"start_time":     stringProperty("Optional. RFC3339 start time (UTC). Defaults to last 24h."),
			"end_time":       stringProperty("Optional. RFC3339 end time (UTC). Defaults to now."),
			"condition":      stringProperty("Required. One of: error_status, length_compliance_violation, high_latency, high_token_usage, tool_call_fails, excessive_steps."),
			"limit":          intProperty("Optional. Max number of traces to return after filtering."),
			"max_latency_ms": intProperty("Optional. Max latency in milliseconds for high_latency. Defaults to 30000."),
			"max_tokens":     intProperty("Optional. Max tokens for high_token_usage. Defaults to 10000."),
			"min_length":     intProperty("Optional. Min output length for length_compliance_violation. Defaults to 1."),
			"max_length":     intProperty("Optional. Max output length for length_compliance_violation. Defaults to 10000."),
			"max_span_count": intProperty("Optional. Max span count for excessive_steps. Defaults to 40."),
		}, []string{"project_name", "agent_name", "condition"}),
	}, withToolLogging("filter_traces", filterTraces(t.TraceToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_trace_details",
		Description: "Fetch a single trace by trace_id for a specific agent. Returns full trace details and spans.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Required. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"trace_id":     stringProperty("Required. Trace ID to fetch."),
			"environment":  stringProperty("Optional. Environment name."),
		}, []string{"project_name", "agent_name", "trace_id"}),
	}, withToolLogging("get_trace_details", getTraceDetails(t.TraceToolset, t.DefaultOrg)))
}

func listTraces(handler TraceToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listTracesInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func getTraces(handler TraceToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, getTracesInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func filterTraces(handler TraceToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, filterTracesInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func getTraceDetails(handler TraceToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, getTraceDetailsInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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
