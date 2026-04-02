package tools

import (
	"context"
	"fmt"
	"strings"

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

func (t *Toolsets) registerRuntimeLogTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_runtime_logs",
		Description: "Fetch runtime logs for an agent. Facilitate time range, log level, or search filtering if needed.",
		InputSchema: createSchema(map[string]any{
			"org_name":      stringProperty("Required. Organization name."),
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
	}, getRuntimeLogs(t.RuntimeLogToolset, t.DefaultOrg))
}

func getRuntimeLogs(handler RuntimeLogToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, runtimeLogsInput) (*gomcp.CallToolResult, any, error) {
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
