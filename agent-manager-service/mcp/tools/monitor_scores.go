package tools

import (
	"context"
	"fmt"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const (
	defaultAgentTraceScoresLimit = 100
	maxAgentTraceScoresLimit     = 100
)

type monitorScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Evaluator   string `json:"evaluator,omitempty"`
	Level       string `json:"level,omitempty"`
}

type monitorRunScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
	RunID       string `json:"run_id"`
}

type monitorScoresTimeSeriesInput struct {
	OrgName     string   `json:"org_name"`
	ProjectName string   `json:"project_name"`
	AgentName   string   `json:"agent_name"`
	MonitorName string   `json:"monitor_name"`
	StartTime   string   `json:"start_time"`
	EndTime     string   `json:"end_time"`
	Evaluators  []string `json:"evaluators"`
}

// type monitorScoresBreakdownInput struct {
// 	OrgName     string `json:"org_name"`
// 	ProjectName string `json:"project_name"`
// 	AgentName   string `json:"agent_name"`
// 	MonitorName string `json:"monitor_name"`
// 	StartTime   string `json:"start_time"`
// 	EndTime     string `json:"end_time"`
// 	Level       string `json:"level"`
// }

type agentTraceScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Limit       *int   `json:"limit,omitempty"`
	Offset      *int   `json:"offset,omitempty"`
}

type traceScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	TraceID     string `json:"trace_id"`
}

func (t *Toolsets) registerMonitorScoresTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "monitor_scores",
		Description: "Get evaluation scores for a monitor within a time range.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"start_time":   stringProperty("Required. RFC3339 start time (UTC)."),
			"end_time":     stringProperty("Required. RFC3339 end time (UTC)."),
			"evaluator":    stringProperty("Optional. Filter by evaluator display name (unique within monitor)."),
			"level":        enumProperty("Optional. Filter by evaluation level.", []string{"trace", "agent", "llm"}),
		}, []string{"project_name", "agent_name", "monitor_name", "start_time", "end_time"}),
	}, withToolLogging("monitor_scores", getMonitorScores(t.MonitorScoresToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "monitor_run_scores",
		Description: "Get aggregated evaluation scores for a specific monitor run.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"run_id":       stringProperty("Required. Monitor run ID."),
		}, []string{"project_name", "agent_name", "monitor_name", "run_id"}),
	}, withToolLogging("monitor_run_scores", getMonitorRunScores(t.MonitorScoresToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "monitor_scores_timeseries",
		Description: "Get time-series evaluation scores for one or more evaluators.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"start_time":   stringProperty("Required. RFC3339 start time (UTC)."),
			"end_time":     stringProperty("Required. RFC3339 end time (UTC)."),
			"evaluators":   arrayProperty("Required. Evaluator display names (unique within monitor).", stringProperty("Evaluator name.")),
		}, []string{"project_name", "agent_name", "monitor_name", "start_time", "end_time", "evaluators"}),
	}, withToolLogging("monitor_scores_timeseries", getMonitorScoresTimeSeries(t.MonitorScoresToolset, t.DefaultOrg)))


	//per trace aggregrated score
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "agent_trace_scores",
		Description: "Get aggregated evaluation scores per trace within a time range.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"start_time":   stringProperty("Required. RFC3339 start time (UTC)."),
			"end_time":     stringProperty("Required. RFC3339 end time (UTC)."),
			"limit":        intProperty(fmt.Sprintf("Optional. Max trace scores to return (default %d, min 1, max %d).", defaultAgentTraceScoresLimit, maxAgentTraceScoresLimit)),
			"offset":       intProperty("Optional. Pagination offset (>= 0)."),
		}, []string{"project_name", "agent_name", "start_time", "end_time"}),
	}, withToolLogging("agent_trace_scores", getAgentTraceScores(t.MonitorScoresToolset, t.DefaultOrg)))


	//detailed overview
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "trace_scores",
		Description: "Get evaluation scores for a trace across all monitors.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"trace_id":     stringProperty("Required. Trace ID."),
		}, []string{"project_name", "agent_name", "trace_id"}),
	}, withToolLogging("trace_scores", getTraceScores(t.MonitorScoresToolset, t.DefaultOrg)))
}

func getMonitorScores(handler MonitorScoresToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, monitorScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorScoresInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}

		level := strings.TrimSpace(input.Level)
		if level != "" && level != "trace" && level != "agent" && level != "llm" {
			return nil, nil, fmt.Errorf("level must be one of: trace, agent, llm")
		}

		result, err := handler.GetMonitorScores(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, startTime, endTime, strings.TrimSpace(input.Evaluator), level)
		if err != nil {
			return nil, nil, wrapToolError("monitor_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"scores":       utils.ConvertToMonitorScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

func getMonitorRunScores(handler MonitorScoresToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, monitorRunScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorRunScoresInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}
		if input.RunID == "" {
			return nil, nil, fmt.Errorf("run_id is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		result, err := handler.GetMonitorRunScores(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, input.RunID)
		if err != nil {
			return nil, nil, wrapToolError("monitor_run_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"run_id":       input.RunID,
			"scores":       utils.ConvertToMonitorRunScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

func getMonitorScoresTimeSeries(handler MonitorScoresToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, monitorScoresTimeSeriesInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorScoresTimeSeriesInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}
		if len(input.Evaluators) == 0 {
			return nil, nil, fmt.Errorf("evaluators must include at least one evaluator name")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}

		evaluators := make([]string, 0, len(input.Evaluators))
		for _, name := range input.Evaluators {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" {
				evaluators = append(evaluators, trimmed)
			}
		}
		if len(evaluators) == 0 {
			return nil, nil, fmt.Errorf("evaluators must include at least one evaluator name")
		}

		result, err := handler.GetMonitorScoresTimeSeries(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, startTime, endTime, evaluators)
		if err != nil {
			return nil, nil, wrapToolError("monitor_scores_timeseries", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"series":       utils.ConvertToBatchTimeSeriesResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

// func getMonitorScoresBreakdown(handler MonitorScoresToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, monitorScoresBreakdownInput) (*gomcp.CallToolResult, any, error) {
// 	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorScoresBreakdownInput) (*gomcp.CallToolResult, any, error) {
// 		if input.ProjectName == "" {
// 			return nil, nil, fmt.Errorf("project_name is required")
// 		}
// 		if input.AgentName == "" {
// 			return nil, nil, fmt.Errorf("agent_name is required")
// 		}
// 		if input.MonitorName == "" {
// 			return nil, nil, fmt.Errorf("monitor_name is required")
// 		}
// 		if strings.TrimSpace(input.Level) == "" {
// 			return nil, nil, fmt.Errorf("level is required")
// 		}
// 		level := strings.TrimSpace(input.Level)
// 		if level != "agent" && level != "llm" {
// 			return nil, nil, fmt.Errorf("level must be one of: agent, llm")
// 		}

// 		orgName := resolveOrgName(defaultOrg, input.OrgName)
// 		if orgName == "" {
// 			return nil, nil, fmt.Errorf("org_name is required")
// 		}

// 		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
// 		if err != nil {
// 			return nil, nil, err
// 		}

// 		result, err := handler.GetGroupedScores(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, startTime, endTime, level)
// 		if err != nil {
// 			return nil, nil, wrapToolError("monitor_scores_breakdown", err)
// 		}

// 		response := map[string]any{
// 			"org_name":     orgName,
// 			"project_name": input.ProjectName,
// 			"agent_name":   input.AgentName,
// 			"monitor_name": input.MonitorName,
// 			"scores":       utils.ConvertToGroupedScoresResponse(result),
// 		}
// 		return handleToolResult(response, nil)
// 	}
// }

func getAgentTraceScores(handler MonitorScoresToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, agentTraceScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input agentTraceScoresInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}

		limit := defaultAgentTraceScoresLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		if limit < 1 || limit > maxAgentTraceScoresLimit {
			return nil, nil, fmt.Errorf("limit must be between 1 and %d", maxAgentTraceScoresLimit)
		}

		offset := utils.DefaultOffset
		if input.Offset != nil {
			offset = *input.Offset
		}
		if offset < utils.MinOffset {
			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
		}

		result, err := handler.GetAgentTraceScores(ctx, orgName, input.ProjectName, input.AgentName, startTime, endTime, limit, offset)
		if err != nil {
			return nil, nil, wrapToolError("agent_trace_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"limit":        limit,
			"offset":       offset,
			"scores":       utils.ConvertToAgentTraceScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

func getTraceScores(handler MonitorScoresToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, traceScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input traceScoresInput) (*gomcp.CallToolResult, any, error) {
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

		result, err := handler.GetTraceScores(ctx, orgName, input.ProjectName, input.AgentName, input.TraceID)
		if err != nil {
			return nil, nil, wrapToolError("trace_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"trace_id":     input.TraceID,
			"scores":       utils.ConvertToTraceScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}
