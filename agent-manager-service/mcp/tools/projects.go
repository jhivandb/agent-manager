package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/ai-agent-management-platform/agent-manager-service/spec"
	"github.com/wso2/ai-agent-management-platform/agent-manager-service/utils"
)

type listProjectsInput struct {
	OrgName string `json:"org_name"`
	Limit   *int   `json:"limit,omitempty"`
	Offset  *int   `json:"offset,omitempty"`
}

type listProjectItem struct {
	Name      string    `json:"name"`
	OrgName   string    `json:"orgName"`
	CreatedAt time.Time `json:"createdAt"`
}

type listProjectsOutput struct {
	OrgName  string            `json:"org_name"`
	Projects []listProjectItem `json:"projects"`
	Total    int32             `json:"total"`
}

type listOrganizationsInput struct {
	Limit  *int `json:"limit,omitempty"`
	Offset *int `json:"offset,omitempty"`
}

type createProjectInput struct {
	OrgName     string  `json:"org_name"`
	DisplayName string  `json:"display_name"`
	Description *string `json:"description"`
}

type listEnvironmentsInput struct {
	OrgName string `json:"org_name"`
}

func (t *Toolsets) registerProjectTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_project",
		Description: "List all projects registered within an organization.",
		InputSchema: createSchema(map[string]any{
			"org_name": stringProperty("Optional. Organization name."),
			"limit":    intProperty(fmt.Sprintf("Optional. Max projects to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
			"offset":   intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
		}, nil),
	}, withToolLogging("list_project", listProjects(t.ProjectToolset, t.DefaultOrg)))

	// gomcp.AddTool(server, &gomcp.Tool{
	// 	Name:        "list_organizations",
	// 	Description: "List organizations available to the current user.",
	// 	InputSchema: createSchema(map[string]any{
	// 		"limit":  intProperty(fmt.Sprintf("Optional. Max organizations to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
	// 		"offset": intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
	// 	}, nil),
	// }, listOrganizations(t.ProjectToolset))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "create_project",
		Description: "Create a new project within an organization. The project name is auto-generated from the display name.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"display_name": stringProperty("Required. Project display name."),
			"description":  stringProperty("Optional. Project description."),
		}, []string{"display_name"}),
	}, withToolLogging("create_project", createProject(t.ProjectToolset, t.AgentToolset, t.DefaultOrg)))

	// gomcp.AddTool(server, &gomcp.Tool{
	// 	Name:        "list_environments",
	// 	Description: "List environments available for an organization.",
	// 	InputSchema: createSchema(map[string]any{
	// 		"org_name": stringProperty("Optional. Organization name."),
	// 	}, nil),
	// }, listEnvironments(t.ProjectToolset, t.DefaultOrg))
}

func listProjects(handler ProjectToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listProjectsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listProjectsInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		// Apply default limit. Validate bounds.
		limit := utils.DefaultLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		if limit < utils.MinLimit || limit > utils.MaxLimit {
			return nil, nil, fmt.Errorf("limit must be between %d and %d", utils.MinLimit, utils.MaxLimit)
		}

		// Apply default offset. Validate bounds.
		offset := utils.DefaultOffset
		if input.Offset != nil {
			offset = *input.Offset
		}
		if offset < utils.MinOffset {
			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
		}

		// Calls the service-layer interface
		projects, total, err := handler.ListProjects(ctx, orgName, limit, offset)
		if err != nil {
			return nil, nil, wrapToolError("list_project", err)
		}

		// Format the response recieved from service layer.
		formatted := make([]listProjectItem, 0, len(projects))
		for _, project := range projects {
			if project == nil {
				continue
			}
			formatted = append(formatted, listProjectItem{
				Name:      project.Name,
				// OrgName:   project.OrgName,
				CreatedAt: project.CreatedAt,
			})
		}

		response := listProjectsOutput{
			OrgName:  orgName,
			Projects: formatted,
			Total:    total,
		}
		return handleToolResult(response, nil)
	}
}

// func listOrganizations(handler ProjectToolsetHandler) func(context.Context, *gomcp.CallToolRequest, listOrganizationsInput) (*gomcp.CallToolResult, any, error) {
// 	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listOrganizationsInput) (*gomcp.CallToolResult, any, error) {
// 		limit := utils.DefaultLimit
// 		if input.Limit != nil {
// 			limit = *input.Limit
// 		}
// 		if limit < utils.MinLimit || limit > utils.MaxLimit {
// 			return nil, nil, fmt.Errorf("limit must be between %d and %d", utils.MinLimit, utils.MaxLimit)
// 		}

// 		offset := utils.DefaultOffset
// 		if input.Offset != nil {
// 			offset = *input.Offset
// 		}
// 		if offset < utils.MinOffset {
// 			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
// 		}

// 		orgs, total, err := handler.ListOrganizations(ctx, limit, offset)
// 		if err != nil {
// 			return nil, nil, wrapToolError("list_organizations", err)
// 		}

// 		response := map[string]any{
// 			"organizations": utils.ConvertToOrganizationListResponse(orgs),
// 			"total":         total,
// 			"limit":         int32(limit),
// 			"offset":        int32(offset),
// 		}
// 		return handleToolResult(response, nil)
// 	}
// }

func createProject(handler ProjectToolsetHandler, agentHandler AgentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, createProjectInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input createProjectInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}
		if strings.TrimSpace(input.DisplayName) == "" {
			return nil, nil, fmt.Errorf("display_name is required")
		}
		if agentHandler == nil {
			return nil, nil, fmt.Errorf("create_project requires agent toolset for name generation")
		}

		resourceReq := spec.ResourceNameRequest{
			DisplayName:  strings.TrimSpace(input.DisplayName),
			ResourceType: "project",
		}
		projectName, err := agentHandler.GenerateName(ctx, orgName, resourceReq)
		if err != nil {
			return nil, nil, wrapToolError("create_project", err)
		}

		req := spec.CreateProjectRequest{
			Name:               projectName,
			DisplayName:        strings.TrimSpace(input.DisplayName),
			DeploymentPipeline: "default",
			Description:        normalizeOptionalString(input.Description),
		}

		project, err := handler.CreateProject(ctx, orgName, req)
		if err != nil {
			return nil, nil, wrapToolError("create_project", err)
		}

		response := map[string]any{
			"org_name": orgName,
			"project":  utils.ConvertToProjectResponse(project),
		}
		return handleToolResult(response, nil)
	}
}

// func listEnvironments(handler ProjectToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listEnvironmentsInput) (*gomcp.CallToolResult, any, error) {
// 	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listEnvironmentsInput) (*gomcp.CallToolResult, any, error) {
// 		orgName := resolveOrgName(defaultOrg, input.OrgName)
// 		if orgName == "" {
// 			return nil, nil, fmt.Errorf("org_name is required")
// 		}

// 		environments, err := handler.ListEnvironments(ctx, orgName)
// 		if err != nil {
// 			return nil, nil, wrapToolError("list_environments", err)
// 		}

// 		response := map[string]any{
// 			"org_name":     orgName,
// 			"environments": utils.ConvertToEnvironmentListResponse(environments),
// 		}
// 		return handleToolResult(response, nil)
// 	}
// }
