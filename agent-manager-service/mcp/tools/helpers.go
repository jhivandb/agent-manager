package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

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