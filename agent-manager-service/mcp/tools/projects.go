package tools

import (
	"context"
	"fmt"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/utils"
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

func (t *Toolsets) registerProjectTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_project",
		Description: "List all projects registered within an organization.",
		InputSchema: createSchema(map[string]any{
			"org_name": stringProperty("Required. Organization name."),
			"limit":    intProperty(fmt.Sprintf("Optional. Max projects to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
			"offset":   intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
		}, nil),
	}, listProjects(t.ProjectToolset, t.DefaultOrg))
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
				OrgName:   project.OrgName,
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