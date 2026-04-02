package tools

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type listBuildsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	Limit       *int   `json:"limit,omitempty"`
	Offset      *int   `json:"offset,omitempty"`
}

type getBuildLogsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	BuildName   string `json:"build_name"`
}

func (t *Toolsets) registerBuildTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_builds",
		Description: "List current builds for a specific agent.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Required. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent exists."),
			"agent_name":   stringProperty("Required. Agent name to list builds for."),
			"limit":        intProperty(fmt.Sprintf("Optional. Max builds to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
			"offset":       intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
		}, []string{"project_name", "agent_name"}),
	}, listBuilds(t.BuildToolset, t.DefaultOrg))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_build_logs",
		Description: "Fetch build logs for a specific build of an internal agent.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Required. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent exists."),
			"agent_name":   stringProperty("Required. Agent name that owns the build."),
			"build_name":   stringProperty("Required. Build name to fetch logs for."),
		}, []string{"project_name", "agent_name", "build_name"}),
	}, getBuildLogs(t.BuildToolset, t.DefaultOrg))
}

func listBuilds(handler BuildToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listBuildsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listBuildsInput) (*gomcp.CallToolResult, any, error) {
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

		limit := utils.DefaultLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		if limit < utils.MinLimit || limit > utils.MaxLimit {
			return nil, nil, fmt.Errorf("limit must be between %d and %d", utils.MinLimit, utils.MaxLimit)
		}

		offset := utils.DefaultOffset
		if input.Offset != nil {
			offset = *input.Offset
		}
		if offset < utils.MinOffset {
			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
		}

		builds, total, err := handler.ListAgentBuilds(ctx, orgName, input.ProjectName, input.AgentName, int32(limit), int32(offset))
		if err != nil {
			return nil, nil, wrapToolError("list_builds", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"builds":       utils.ConvertToBuildListResponse(builds),
			"total":        total,
			"limit":        int32(limit),
			"offset":       int32(offset),
		}

		return handleToolResult(response, nil)
	}
}

func getBuildLogs(handler BuildToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, getBuildLogsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getBuildLogsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.BuildName == "" {
			return nil, nil, fmt.Errorf("build_name is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		result, err := handler.GetBuildLogs(ctx, orgName, input.ProjectName, input.AgentName, input.BuildName)
		if err != nil {
			return nil, nil, wrapToolError("get_build_logs", err)
		}

		reduced := reduceLogsResponse(result)
		return handleToolResult(reduced, nil)
	}
}
