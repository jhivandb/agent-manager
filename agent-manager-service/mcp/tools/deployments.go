package tools

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type listDeploymentsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
}

type deployEnvVarInput struct {
	Key         string  `json:"key"`
	Value       *string `json:"value,omitempty"`
	IsSensitive *bool   `json:"is_sensitive,omitempty"`
	SecretRef   *string `json:"secret_ref,omitempty"`
}

type deployAgentInput struct {
	OrgName                   string              `json:"org_name"`
	ProjectName               string              `json:"project_name"`
	AgentName                 string              `json:"agent_name"`
	ImageID                   string              `json:"image_id"`
	Env                       []deployEnvVarInput `json:"env,omitempty"`
	EnableAutoInstrumentation *bool               `json:"enable_auto_instrumentation,omitempty"`
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
	}, withToolLogging("list_deployments", listDeployments(t.DeploymentToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "deploy_agent",
		Description: "Deploy an existing internal agent. (Deployed to the lowest environment in the deployment pipeline.)",
		InputSchema: createSchema(map[string]any{
			"org_name":                    stringProperty("Optional. Organization name."),
			"project_name":                stringProperty("Required. Project name where the agent exists."),
			"agent_name":                  stringProperty("Required. Agent name to deploy."),
			"image_id":                    stringProperty("Required. Image ID to deploy."),
			"enable_auto_instrumentation": boolProperty("Optional. Enable auto instrumentation for observability."),
			"env": arrayProperty("Optional. Environment variables for deployment.", createSchema(map[string]any{
				"key":          stringProperty("Required. Environment variable key."),
				"value":        stringProperty("Optional. Environment variable value."),
				"is_sensitive": boolProperty("Optional. If true, value is stored as a secret."),
				"secret_ref":   stringProperty("Optional. Reference to existing secret."),
			}, []string{"key"})),
		}, []string{"project_name", "agent_name", "image_id"}),
	}, withToolLogging("deploy_agent", deployAgent(t.DeploymentToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "update_deployment_state",
		Description: "Update deployment state for an agent in a specific environment (Active or Undeploy).",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"environment":  stringProperty("Required. Environment name."),
			"state":        enumProperty("Required. Deployment state.", []string{"Active", "Undeploy"}),
		}, []string{"project_name", "agent_name", "environment", "state"}),
	}, withToolLogging("update_deployment_state", updateDeploymentState(t.DeploymentToolset, t.DefaultOrg)))
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

func deployAgent(handler DeploymentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, deployAgentInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input deployAgentInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.ImageID == "" {
			return nil, nil, fmt.Errorf("image_id is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		env := make([]spec.EnvironmentVariable, 0, len(input.Env))
		for _, item := range input.Env {
			ev := spec.EnvironmentVariable{
				Key: item.Key,
			}
			if item.Value != nil {
				ev.Value = item.Value
			}
			if item.IsSensitive != nil {
				ev.IsSensitive = item.IsSensitive
			}
			if item.SecretRef != nil {
				ev.SecretRef = item.SecretRef
			}
			env = append(env, ev)
		}

		req := &spec.DeployAgentRequest{
			ImageId:                   input.ImageID,
			Env:                       env,
			EnableAutoInstrumentation: input.EnableAutoInstrumentation,
		}

		environment, err := handler.DeployAgent(ctx, orgName, input.ProjectName, input.AgentName, req)
		if err != nil {
			return nil, nil, wrapToolError("deploy_agent", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"environment":  environment,
		}

		return handleToolResult(response, nil)
	}
}

type updateDeploymentStateInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	Environment string `json:"environment"`
	State       string `json:"state"`
}

func updateDeploymentState(handler DeploymentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, updateDeploymentStateInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input updateDeploymentStateInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.Environment == "" {
			return nil, nil, fmt.Errorf("environment is required")
		}
		if input.State != "Active" && input.State != "Undeploy" {
			return nil, nil, fmt.Errorf("state must be Active or Undeploy")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		if err := handler.UpdateDeploymentState(ctx, orgName, input.ProjectName, input.AgentName, input.Environment, input.State); err != nil {
			return nil, nil, wrapToolError("update_deployment_state", err)
		}

		response := map[string]any{
			"message":     "Deployment state transition request accepted",
			"environment": input.Environment,
			"state":       input.State,
		}

		return handleToolResult(response, nil)
	}
}
