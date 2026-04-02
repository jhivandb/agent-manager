package tools

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type listDeploymentsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
}

func (t *Toolsets) registerDeploymentTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_deployments",
		Description: "List current deployments for an agent per environment.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Required. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent exists."),
			"agent_name":   stringProperty("Required. Agent name to check deployments for."),
		}, []string{"project_name", "agent_name"}),
	}, listDeployments(t.DeploymentToolset, t.DefaultOrg))
}

func listDeployments(handler DeploymentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listDeploymentsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listDeploymentsInput) (*gomcp.CallToolResult, any, error) {
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

		deployments, err := handler.GetAgentDeployments(ctx, orgName, input.ProjectName, input.AgentName)
		if err != nil {
			return nil, nil, wrapToolError("list_deployments", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"deployments":  utils.ConvertToDeploymentDetailsResponse(deployments),
		}

		return handleToolResult(response, nil)
	}
}
