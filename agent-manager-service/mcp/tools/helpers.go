package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	reqlogger "github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

func createSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProperty(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func boolProperty(description string) map[string]any {
	return map[string]any{
		"type":        "boolean",
		"description": description,
	}
}

func intProperty(description string) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
	}
}

func arrayProperty(description string, itemSchema map[string]any) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       itemSchema,
	}
}

func enumProperty(description string, values []string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        values,
	}
}

func resolveOrgName(defaultOrg, provided string) string {
	if provided != "" {
		return provided
	}
	return defaultOrg
}

func wrapToolError(toolName string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, utils.ErrOrganizationNotFound):
		return fmt.Errorf("%s: invalid org name. Use a valid org name or omit it to use the default org", toolName)
	case errors.Is(err, utils.ErrProjectNotFound):
		return fmt.Errorf("%s: invalid project name. Call list_project to see valid projects", toolName)
	case errors.Is(err, utils.ErrAgentNotFound):
		return fmt.Errorf("%s: invalid agent name. Call list_agents or list_project_agent_pairs", toolName)
	case errors.Is(err, utils.ErrEvaluatorNotFound):
		return fmt.Errorf("%s: invalid evaluator id. Call list_evaluators to see valid evaluators", toolName)
	case errors.Is(err, utils.ErrCustomEvaluatorNotFound):
		return fmt.Errorf("%s: custom evaluator not found. Call list_evaluators to see valid evaluators", toolName)
	case errors.Is(err, utils.ErrCustomEvaluatorAlreadyExists):
		return fmt.Errorf("%s: custom evaluator already exists with this identifier or display name", toolName)
	case errors.Is(err, utils.ErrCustomEvaluatorIdentifierTaken):
		return fmt.Errorf("%s: evaluator identifier conflicts with a built-in evaluator", toolName)
	case errors.Is(err, utils.ErrMonitorNotFound):
		return fmt.Errorf("%s: monitor not found. Call list_monitors to see valid monitors", toolName)
	case errors.Is(err, utils.ErrMonitorRunNotFound):
		return fmt.Errorf("%s: monitor run not found. Call list_monitor_runs to see valid runs", toolName)
	case errors.Is(err, utils.ErrMonitorAlreadyStopped):
		return fmt.Errorf("%s: monitor is already stopped", toolName)
	case errors.Is(err, utils.ErrMonitorAlreadyActive):
		return fmt.Errorf("%s: monitor is already active", toolName)
	case errors.Is(err, utils.ErrNotFound):
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "namespace not found") || strings.Contains(msg, "organization not found"):
			return fmt.Errorf("%s: invalid org name. Use a valid org name or omit it to use the default org", toolName)
		case strings.Contains(msg, "project not found"):
			return fmt.Errorf("%s: invalid project name. Call list_project to see valid projects", toolName)
		case strings.Contains(msg, "agent not found") || strings.Contains(msg, "component not found"):
			return fmt.Errorf("%s: invalid agent name. Call list_agents or list_project_agent_pairs", toolName)
		}
	}
	return fmt.Errorf("%s: %w", toolName, err)
}

func withToolLogging[T any](toolName string, handler func(context.Context, *gomcp.CallToolRequest, T) (*gomcp.CallToolResult, any, error)) func(context.Context, *gomcp.CallToolRequest, T) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *gomcp.CallToolRequest, input T) (*gomcp.CallToolResult, any, error) {
		start := time.Now()
		result, meta, err := handler(ctx, req, input)
		log := reqlogger.GetLogger(ctx)
		duration := time.Since(start).Milliseconds()
		if err != nil {
			log.Error("mcp tool failed", "tool", toolName, "duration_ms", duration, "error", err)
		} else {
			log.Info("mcp tool succeeded", "tool", toolName, "duration_ms", duration)
		}
		return result, meta, err
	}
}

func handleToolResult(result any, err error) (*gomcp.CallToolResult, any, error) {
	if err != nil {
		return nil, nil, err
	}
	jsonData, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: string(jsonData)},
		},
	}, result, nil
}

func reduceLogsResponse(resp *models.LogsResponse) map[string]any {
	if resp == nil {
		return map[string]any{
			"logs":       []map[string]any{},
			"totalCount": 0,
			"tookMs":     0,
		}
	}

	logs := make([]map[string]any, 0, len(resp.Logs))
	for _, entry := range resp.Logs {
		logs = append(logs, map[string]any{
			"timestamp": entry.Timestamp,
			"logLevel":  entry.LogLevel,
			"log":       entry.Log,
		})
	}
	return map[string]any{
		"logs":       logs,
		"totalCount": resp.TotalCount,
		"tookMs":     resp.TookMs,
	}
}

const defaultEnvName = "default"

func resolveEnv(value string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	if env := strings.TrimSpace(os.Getenv("AMP_ENV")); env != "" {
		return env
	}
	return defaultEnvName
}

func resolveTimeWindow(start, end string) (string, string, error) {
	if start == "" && end == "" {
		return defaultWindow()
	}
	if start == "" || end == "" {
		return "", "", fmt.Errorf("start_time and end_time must be provided together")
	}
	if _, err := time.Parse(time.RFC3339, start); err != nil {
		return "", "", fmt.Errorf("invalid start_time format (use RFC3339)")
	}
	if _, err := time.Parse(time.RFC3339, end); err != nil {
		return "", "", fmt.Errorf("invalid end_time format (use RFC3339)")
	}
	return start, end, nil
}

func defaultWindow() (string, string, error) {
	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	return start.Format(time.RFC3339), end.Format(time.RFC3339), nil
}

func defaultSortOrder(order string) string {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "asc":
		return "asc"
	default:
		return "desc"
	}
}

func parseRequiredTimeRange(start, end string) (time.Time, time.Time, error) {
	if strings.TrimSpace(start) == "" || strings.TrimSpace(end) == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("start_time and end_time are required")
	}
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid start_time format (use RFC3339)")
	}
	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid end_time format (use RFC3339)")
	}
	return startTime, endTime, nil
}

func parseOptionalTimeRange(start, end string) (*time.Time, *time.Time, error) {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	if start == "" && end == "" {
		return nil, nil, nil
	}
	if start == "" || end == "" {
		return nil, nil, fmt.Errorf("trace_start and trace_end must be provided together")
	}
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid trace_start format (use RFC3339)")
	}
	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid trace_end format (use RFC3339)")
	}
	return &startTime, &endTime, nil
}
