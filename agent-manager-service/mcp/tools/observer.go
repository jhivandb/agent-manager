package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		Name:        "get_metrics",
		Description: "Fetch CPU and memory resource metrics for an agent in a specific time range.",
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
	}, withToolLogging("get_metrics", getMetrics(t.AgentToolset, t.DefaultOrg)))
}

func (t *Toolsets) registerRuntimeLogTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_runtime_logs",
		Description: "Fetch runtime logs for an agent. Facilitate time range, log level, or search filtering if needed.",
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
	}, withToolLogging("get_runtime_logs", getRuntimeLogs(t.RuntimeLogToolset, t.DefaultOrg)))
}

func getMetrics(handler AgentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, getMetricsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getMetricsInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func resolveMetricsTimeRange(timeRange string) (string, string, error) {
	preset := strings.TrimSpace(strings.ToLower(timeRange))
	if preset == "" {
		preset = "7d"
	}

	duration, ok := map[string]time.Duration{
		"10m":  10 * time.Minute,
		"30m":  30 * time.Minute,
		"1h":   time.Hour,
		"3h":   3 * time.Hour,
		"6h":   6 * time.Hour,
		"12h":  12 * time.Hour,
		"1d":   24 * time.Hour,
		"3d":   3 * 24 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"30d":  30 * 24 * time.Hour,
	}[preset]
	if !ok {
		return "", "", fmt.Errorf("invalid time_range: %s", timeRange)
	}

	endTime := time.Now().UTC()
	startTime := endTime.Add(-duration)
	return startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), nil
}
