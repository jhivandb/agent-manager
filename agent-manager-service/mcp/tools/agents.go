package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/config"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type envVarInput struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type createInternalAgentPythonInput struct {
	OrgName     string  `json:"org_name"`
	ProjectName string  `json:"project_name"`
	DisplayName string  `json:"display_name"`
	Description *string `json:"description"`

	RepositoryURL string `json:"repository_url"`
	Branch        string `json:"branch"`
	AppPath       string `json:"app_path"`

	LanguageVersion string `json:"language_version"`
	RunCommand      string `json:"run_command"`

	InterfaceType string `json:"interface_type"`
	Port          *int   `json:"port"`
	BasePath      string `json:"base_path"`
	OpenAPIPath   string `json:"openapi_path"`

	EnableAutoInstrumentation *bool         `json:"enable_auto_instrumentation"`
	Env                       []envVarInput `json:"env"`
}

type createInternalAgentDockerInput struct {
	OrgName     string  `json:"org_name"`
	ProjectName string  `json:"project_name"`
	DisplayName string  `json:"display_name"`
	Description *string `json:"description"`

	RepositoryURL  string `json:"repository_url"`
	Branch         string `json:"branch"`
	AppPath        string `json:"app_path"`
	DockerfilePath string `json:"dockerfile_path"`

	InterfaceType string `json:"interface_type"`
	Port          *int   `json:"port"`
	BasePath      string `json:"base_path"`
	OpenAPIPath   string `json:"openapi_path"`

	EnableAutoInstrumentation *bool         `json:"enable_auto_instrumentation"`
	Env                       []envVarInput `json:"env"`
}

type createExternalAgentInput struct {
	OrgName     string  `json:"org_name"`
	ProjectName string  `json:"project_name"`
	DisplayName string  `json:"display_name"`
	Description *string `json:"description"`
	Language    string  `json:"language"`
}

type listProjectAgentPairsInput struct {
	OrgName       string `json:"org_name"`
	ProjectSearch string `json:"project_search"`
	AgentSearch   string `json:"agent_search"`
	ProjectLimit  *int   `json:"project_limit"`
	ProjectOffset *int   `json:"project_offset"`
	AgentLimit    *int   `json:"agent_limit"`
	AgentOffset   *int   `json:"agent_offset"`
}

type listAgentsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	Limit       *int   `json:"limit,omitempty"`
	Offset      *int   `json:"offset,omitempty"`
}

type listAgentItem struct {
	Name         string            `json:"name"`
	Provisioning spec.Provisioning `json:"provisioning"`
}

type listAgentsOutput struct {
	OrgName     string          `json:"org_name"`
	ProjectName string          `json:"project_name"`
	Agents      []listAgentItem `json:"agents"`
	Total       int32           `json:"total"`
}

type projectAgentPair struct {
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
}

type listProjectAgentPairsOutput struct {
	Pairs []projectAgentPair `json:"pairs"`
	Count int                `json:"count"`
	Note  string             `json:"note,omitempty"`
}

func (t *Toolsets) registerAgentTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "create_internal_agent_python",
		Description: "Register an agent as a platform-hosted (internal) Python buildpack agent. This will trigger an initial build automatically and return the created agent details.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent will be created."),
			"display_name": stringProperty("Required. Human-readable display name for the agent."),
			"description":  stringProperty("Optional Short description about what the agent does."),

			"repository_url": stringProperty("Required. GitHub root repository URL. Do not enter .git and the end of repo name(eg: https://github.com/user/repo)"),
			"branch":         stringProperty("Required. Github repository branch name."),
			"app_path":       stringProperty("Required. Path of the project where agent code lives within the repository (use / for root. specify path if not)."),

			"language_version": stringProperty("Optional. Python version (default: 3.11)."),
			"run_command":      stringProperty("Optional. Start command to run the agent (default: python main.py)."),

			"interface_type": enumProperty("Optional. API interface type of the agent. DEFAULT (standard chat interface with /chat endpoint on port 8000) or CUSTOM (custom API with user-provided OpenAPI spec). Default: DEFAULT.", []string{"DEFAULT", "CUSTOM"}),
			"port":           intProperty("Required when interface_type is CUSTOM. Port number where the agent will be listening."),
			"base_path":      stringProperty("Optional. API base path (default: /). Required when interface_type is CUSTOM."),
			"openapi_path":   stringProperty("Required when interface_type is CUSTOM. OpenAPI specification file path within the repository (must start with /)."),

			"enable_auto_instrumentation": boolProperty("Automatically enables OTEL tracing instrumentation to your agent for observability."),
			"env": arrayProperty("Required. Environment variables and other configurations for the agent.(eg: api keys, database URLs, support service URLs). Can be obtained from the .env file in the project repository", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":   stringProperty("Environment variable key."),
					"value": stringProperty("Environment variable value."),
				},
				"required": []string{"key", "value"},
			}),
		}, []string{"project_name", "display_name", "repository_url", "branch", "app_path", "interface_type", "env"}),
	}, withToolLogging("create_internal_agent_python", createInternalAgentPython(t.AgentToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "create_internal_agent_docker",
		Description: "Register an agent as platform-hosted (internal) Docker agent. This will trigger an initial build automatically and return the created agent details.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent will be created."),
			"display_name": stringProperty("Required. Human-readable display name for the agent."),
			"description":  stringProperty("Optional. Short description about what the agent does."),

			"repository_url":  stringProperty("Required. GitHub repository URL (e.g., https://github.com/owner/repo)."),
			"branch":          stringProperty("Required. Repository branch name."),
			"app_path":        stringProperty("Required. Path within the repository (use / for root)."),
			"dockerfile_path": stringProperty("Required. Path to Dockerfile in repo (must start with /)."),

			"interface_type": enumProperty("Optional. DEFAULT (chat API on /chat, port 8000) or CUSTOM (user-provided OpenAPI). Default: DEFAULT.", []string{"DEFAULT", "CUSTOM"}),
			"port":           intProperty("Required when interface_type is CUSTOM. Port number where the agent listens."),
			"base_path":      stringProperty("Optional. API base path (default: /). Required when interface_type is CUSTOM."),
			"openapi_path":   stringProperty("Required when interface_type is CUSTOM. OpenAPI spec file path within the repo (must start with /)."),

			"enable_auto_instrumentation": boolProperty("Optional. Enable OTEL auto instrumentation for observability."),
			"env": arrayProperty("Required. Environment variables (from .env).", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":   stringProperty("Environment variable key."),
					"value": stringProperty("Environment variable value."),
				},
				"required": []string{"key", "value"},
			}),
		}, []string{"project_name", "display_name", "repository_url", "branch", "app_path", "dockerfile_path", "interface_type", "env"}),
	}, createInternalAgentDocker(t.AgentToolset, t.DefaultOrg))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "create_external_agent",
		Description: "Register an agent as externally-hosted and return setup steps for enabling instrumentation. This will allow getting observalibity and evaluation for the agents",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name where the agent will be registered."),
			"display_name": stringProperty("Required. Human-readable display name for the agent."),
			"description":  stringProperty("Optional. Short description about what the agent does."),
			"language":     stringProperty("Required. Agent language for setup guide (python or ballerina)."),
		}, []string{"project_name", "display_name", "language"}),
	}, createExternalAgent(t.AgentToolset, t.DefaultOrg))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_agents",
		Description: "List agents within a specific project.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name to list agents from."),
			"limit":        intProperty(fmt.Sprintf("Optional. Max agents to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
			"offset":       intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
		}, []string{"project_name"}),
	}, listAgents(t.AgentToolset, t.DefaultOrg))

	if t.ProjectToolset != nil {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_project_agent_pairs",
		Description: "List (project, agent) pairs registered within an organization.",
			InputSchema: createSchema(map[string]any{
				"org_name":       stringProperty("Optional. Organization name."),
				"project_search": stringProperty("Optional. Filter project names by substring (case-insensitive)."),
				"agent_search":   stringProperty("Optional. Filter agent names by substring (case-insensitive)."),
				"project_limit":  intProperty("Optional. Project pagination limit (1-50)."),
				"project_offset": intProperty("Optional. Project pagination offset (>= 0)."),
				"agent_limit":    intProperty("Optional. Agent pagination limit (1-50)."),
				"agent_offset":   intProperty("Optional. Agent pagination offset (>= 0)."),
			}, nil),
		}, listProjectAgentPairs(t.AgentToolset, t.ProjectToolset, t.DefaultOrg))
	}
}

func createInternalAgentPython(handler AgentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, createInternalAgentPythonInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input createInternalAgentPythonInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if strings.TrimSpace(input.DisplayName) == "" {
			return nil, nil, fmt.Errorf("display_name is required")
		}
		if strings.TrimSpace(input.RepositoryURL) == "" {
			return nil, nil, fmt.Errorf("repository_url is required")
		}
		if strings.TrimSpace(input.Branch) == "" {
			return nil, nil, fmt.Errorf("branch is required")
		}
		if strings.TrimSpace(input.AppPath) == "" {
			return nil, nil, fmt.Errorf("app_path is required")
		}
		if strings.TrimSpace(input.LanguageVersion) == "" {
			input.LanguageVersion = "3.11"
		}
		if strings.TrimSpace(input.RunCommand) == "" {
			input.RunCommand = "python main.py"
		}
		if strings.TrimSpace(input.InterfaceType) == "" {
			return nil, nil, fmt.Errorf("interface_type is required")
		}
		if input.Env == nil {
			return nil, nil, fmt.Errorf("env is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		resourceReq := spec.ResourceNameRequest{
			DisplayName:  strings.TrimSpace(input.DisplayName),
			ResourceType: "agent",
			ProjectName:  &input.ProjectName,
		}
		agentName, err := handler.GenerateName(ctx, orgName, resourceReq)
		if err != nil {
			return nil, nil, wrapToolError("create_internal_agent_python", err)
		}

		req, err := buildInternalAgentRequest(agentName, input.DisplayName, normalizeOptionalString(input.Description), internalAgentInput{
			RepositoryURL:             input.RepositoryURL,
			Branch:                    input.Branch,
			AppPath:                   input.AppPath,
			Language:                  "python",
			LanguageVersion:           input.LanguageVersion,
			RunCommand:                input.RunCommand,
			InterfaceType:             input.InterfaceType,
			Port:                      input.Port,
			BasePath:                  input.BasePath,
			OpenAPIPath:               input.OpenAPIPath,
			EnableAutoInstrumentation: input.EnableAutoInstrumentation,
			Env:                       input.Env,
		})
		if err != nil {
			return nil, nil, err
		}

		if err := utils.ValidateAgentCreatePayload(*req); err != nil {
			return nil, nil, err
		}

		if err := handler.CreateAgent(ctx, orgName, input.ProjectName, req); err != nil {
			return nil, nil, wrapToolError("create_internal_agent_python", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   agentName,
			"display_name": input.DisplayName,
			"note":         "agent creation accepted; initial build triggered",
		}
		return handleToolResult(response, nil)
	}
}

func createInternalAgentDocker(handler AgentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, createInternalAgentDockerInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input createInternalAgentDockerInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if strings.TrimSpace(input.DisplayName) == "" {
			return nil, nil, fmt.Errorf("display_name is required")
		}
		if strings.TrimSpace(input.RepositoryURL) == "" {
			return nil, nil, fmt.Errorf("repository_url is required")
		}
		if strings.TrimSpace(input.Branch) == "" {
			return nil, nil, fmt.Errorf("branch is required")
		}
		if strings.TrimSpace(input.AppPath) == "" {
			return nil, nil, fmt.Errorf("app_path is required")
		}
		if strings.TrimSpace(input.DockerfilePath) == "" {
			input.DockerfilePath = "/Dockerfile"
		}
		if strings.TrimSpace(input.InterfaceType) == "" {
			return nil, nil, fmt.Errorf("interface_type is required")
		}
		if input.Env == nil {
			return nil, nil, fmt.Errorf("env is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		resourceReq := spec.ResourceNameRequest{
			DisplayName:  strings.TrimSpace(input.DisplayName),
			ResourceType: "agent",
			ProjectName:  &input.ProjectName,
		}
		agentName, err := handler.GenerateName(ctx, orgName, resourceReq)
		if err != nil {
			return nil, nil, wrapToolError("create_internal_agent_docker", err)
		}

		req, err := buildInternalAgentRequest(agentName, input.DisplayName, normalizeOptionalString(input.Description), internalAgentInput{
			RepositoryURL:             input.RepositoryURL,
			Branch:                    input.Branch,
			AppPath:                   input.AppPath,
			Language:                  "docker",
			DockerfilePath:            input.DockerfilePath,
			InterfaceType:             input.InterfaceType,
			Port:                      input.Port,
			BasePath:                  input.BasePath,
			OpenAPIPath:               input.OpenAPIPath,
			EnableAutoInstrumentation: input.EnableAutoInstrumentation,
			Env:                       input.Env,
		})
		if err != nil {
			return nil, nil, err
		}

		if err := utils.ValidateAgentCreatePayload(*req); err != nil {
			return nil, nil, err
		}

		if err := handler.CreateAgent(ctx, orgName, input.ProjectName, req); err != nil {
			return nil, nil, wrapToolError("create_internal_agent_docker", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   agentName,
			"display_name": input.DisplayName,
			"note":         "agent creation accepted; initial build triggered",
		}
		return handleToolResult(response, nil)
	}
}

func createExternalAgent(handler AgentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, createExternalAgentInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input createExternalAgentInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if strings.TrimSpace(input.DisplayName) == "" {
			return nil, nil, fmt.Errorf("display_name is required")
		}
		if strings.TrimSpace(input.Language) == "" {
			return nil, nil, fmt.Errorf("language is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		resourceReq := spec.ResourceNameRequest{
			DisplayName:  strings.TrimSpace(input.DisplayName),
			ResourceType: "agent",
			ProjectName:  &input.ProjectName,
		}
		agentName, err := handler.GenerateName(ctx, orgName, resourceReq)
		if err != nil {
			return nil, nil, wrapToolError("create_external_agent", err)
		}

		req := buildExternalAgentRequest(agentName, input.DisplayName, normalizeOptionalString(input.Description))
		if err := utils.ValidateAgentCreatePayload(req); err != nil {
			return nil, nil, err
		}

		if err := handler.CreateAgent(ctx, orgName, input.ProjectName, &req); err != nil {
			return nil, nil, wrapToolError("create_external_agent", err)
		}

		expiresIn := "8760h"
		tokenResp, err := handler.GenerateToken(ctx, orgName, input.ProjectName, agentName, "", expiresIn)
		if err != nil {
			return nil, nil, wrapToolError("create_external_agent", err)
		}

		cfg := config.GetConfig()
		otelEndpoint := resolveConsoleOtelEndpoint(cfg.OTEL.ExporterEndpoint)
		instructions := buildSetupInstructions(otelEndpoint, tokenResp.Token, expiresIn)

		language := strings.ToLower(strings.TrimSpace(input.Language))
		selected, ok := instructions.Guides[language]
		if !ok {
			return nil, nil, fmt.Errorf("create_external_agent: unsupported language %q (use python or ballerina)", language)
		}

		response := map[string]any{
			"org_name":         orgName,
			"project_name":     input.ProjectName,
			"agent_name":       agentName,
			"token":            tokenResp.Token,
			"token_expires_at": tokenResp.ExpiresAt,
			"token_duration":   expiresIn,
			"otel_endpoint":    instructions.OtelEndpoint,
			"language":         selected.Language,
			"steps":            selected.Steps,
		}
		return handleToolResult(response, nil)
	}
}

func listProjectAgentPairs(agentHandler AgentToolsetHandler, projectHandler ProjectToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listProjectAgentPairsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listProjectAgentPairsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectLimit != nil && (*input.ProjectLimit < utils.MinLimit || *input.ProjectLimit > utils.MaxLimit) {
			return nil, nil, fmt.Errorf("project_limit must be between %d and %d", utils.MinLimit, utils.MaxLimit)
		}
		if input.ProjectOffset != nil && *input.ProjectOffset < utils.MinOffset {
			return nil, nil, fmt.Errorf("project_offset must be >= %d", utils.MinOffset)
		}
		if input.AgentLimit != nil && (*input.AgentLimit < utils.MinLimit || *input.AgentLimit > utils.MaxLimit) {
			return nil, nil, fmt.Errorf("agent_limit must be between %d and %d", utils.MinLimit, utils.MaxLimit)
		}
		if input.AgentOffset != nil && *input.AgentOffset < utils.MinOffset {
			return nil, nil, fmt.Errorf("agent_offset must be >= %d", utils.MinOffset)
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		projectLimit := utils.DefaultLimit
		if input.ProjectLimit != nil {
			projectLimit = *input.ProjectLimit
		}
		projectOffset := utils.DefaultOffset
		if input.ProjectOffset != nil {
			projectOffset = *input.ProjectOffset
		}
		agentLimit := utils.DefaultLimit
		if input.AgentLimit != nil {
			agentLimit = *input.AgentLimit
		}
		agentOffset := utils.DefaultOffset
		if input.AgentOffset != nil {
			agentOffset = *input.AgentOffset
		}

		projects, _, err := projectHandler.ListProjects(ctx, orgName, projectLimit, projectOffset)
		if err != nil {
			return nil, nil, wrapToolError("list_project_agent_pairs", err)
		}

		pairs := []projectAgentPair{}
		for _, project := range projects {
			if !matchesSearch(project.Name, input.ProjectSearch) {
				continue
			}
			agents, _, err := agentHandler.ListAgents(ctx, orgName, project.Name, int32(agentLimit), int32(agentOffset))
			if err != nil {
				return nil, nil, wrapToolError("list_project_agent_pairs", err)
			}
			for _, agent := range agents {
				if !matchesSearch(agent.Name, input.AgentSearch) {
					continue
				}
				pairs = append(pairs, projectAgentPair{
					ProjectName: project.Name,
					AgentName:   agent.Name,
				})
			}
		}

		note := ""
		if len(pairs) == 0 && (input.ProjectSearch != "" || input.AgentSearch != "") {
			note = "no pairs matched the provided filters; try a broader search"
		}

		return handleToolResult(listProjectAgentPairsOutput{
			Pairs: pairs,
			Count: len(pairs),
			Note:  note,
		}, nil)
	}
}

func listAgents(handler AgentToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listAgentsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listAgentsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
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

		// Calls the service-layer interface
		agents, total, err := handler.ListAgents(ctx, orgName, input.ProjectName, int32(limit), int32(offset))
		if err != nil {
			return nil, nil, wrapToolError("list_agents", err)
		}

		formatted := make([]listAgentItem, 0, len(agents))
		for _, agent := range agents {
			if agent == nil {
				continue
			}
			formatted = append(formatted, listAgentItem{
				Name:        agent.Name,
				Provisioning: spec.Provisioning{
					Type: agent.Provisioning.Type,
				},
			})
		}

		response := listAgentsOutput{
			OrgName:     orgName,
			ProjectName: input.ProjectName,
			Agents:      formatted,
			Total:       total,
		}

		return handleToolResult(response, nil)
	}
}

type internalAgentInput struct {
	RepositoryURL string
	Branch        string
	AppPath       string

	Language        string
	LanguageVersion string
	RunCommand      string
	DockerfilePath  string

	InterfaceType string
	Port          *int
	BasePath      string
	OpenAPIPath   string

	EnableAutoInstrumentation *bool
	Env                       []envVarInput
}

func buildInternalAgentRequest(name, displayName string, description *string, input internalAgentInput) (*spec.CreateAgentRequest, error) {
	repoURL := strings.TrimSpace(input.RepositoryURL)
	if repoURL == "" {
		return nil, fmt.Errorf("repository_url is required")
	}
	branch := strings.TrimSpace(input.Branch)
	if branch == "" {
		branch = "main"
	}
	appPath := strings.TrimSpace(input.AppPath)
	if appPath == "" {
		appPath = "/"
	}

	interfaceType := strings.ToUpper(strings.TrimSpace(input.InterfaceType))
	if interfaceType == "" {
		interfaceType = "DEFAULT"
	}
	if interfaceType != "DEFAULT" && interfaceType != "CUSTOM" {
		return nil, fmt.Errorf("interface_type must be DEFAULT or CUSTOM")
	}

	provisioning := spec.Provisioning{
		Type: "internal",
		Repository: &spec.RepositoryConfig{
			Url:     repoURL,
			Branch:  branch,
			AppPath: appPath,
		},
	}

	subType := "chat-api"
	if interfaceType == "CUSTOM" {
		subType = "custom-api"
	}

	agentType := spec.AgentType{
		Type:    "agent-api",
		SubType: &subType,
	}

	build, err := buildCreateAgentBuild(input)
	if err != nil {
		return nil, err
	}

	configurations := buildConfigurations(input)

	inputInterface, err := buildInputInterface(interfaceType, input)
	if err != nil {
		return nil, err
	}

	return &spec.CreateAgentRequest{
		Name:           name,
		DisplayName:    displayName,
		Description:    description,
		Provisioning:   provisioning,
		AgentType:      agentType,
		Build:          build,
		Configurations: configurations,
		InputInterface: inputInterface,
	}, nil
}

func buildExternalAgentRequest(name, displayName string, description *string) spec.CreateAgentRequest {
	subType := "custom-api"
	return spec.CreateAgentRequest{
		Name:        name,
		DisplayName: displayName,
		Description: description,
		Provisioning: spec.Provisioning{
			Type: "external",
		},
		AgentType: spec.AgentType{
			Type:    "external-agent-api",
			SubType: &subType,
		},
	}
}

func buildCreateAgentBuild(input internalAgentInput) (*spec.Build, error) {
	switch strings.ToLower(strings.TrimSpace(input.Language)) {
	case "python":
		runCommand := strings.TrimSpace(input.RunCommand)
		if runCommand == "" {
			return nil, fmt.Errorf("run_command is required for python buildpack")
		}
		languageVersion := strings.TrimSpace(input.LanguageVersion)
		if languageVersion == "" {
			return nil, fmt.Errorf("language_version is required for python buildpack")
		}
		return &spec.Build{
			BuildpackBuild: &spec.BuildpackBuild{
				Type: "buildpack",
				Buildpack: spec.BuildpackConfig{
					Language:        "python",
					LanguageVersion: &languageVersion,
					RunCommand:      &runCommand,
				},
			},
		}, nil
	case "docker":
		dockerfilePath := strings.TrimSpace(input.DockerfilePath)
		if dockerfilePath == "" {
			return nil, fmt.Errorf("dockerfile_path is required for docker builds")
		}
		if !strings.HasPrefix(dockerfilePath, "/") {
			return nil, fmt.Errorf("dockerfile_path must start with '/' for docker builds")
		}
		return &spec.Build{
			DockerBuild: &spec.DockerBuild{
				Type: "docker",
				Docker: spec.DockerConfig{
					DockerfilePath: dockerfilePath,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", input.Language)
	}
}

func buildConfigurations(input internalAgentInput) *spec.Configurations {
	envVars := sanitizeEnvVars(input.Env)
	if len(envVars) == 0 && input.EnableAutoInstrumentation == nil {
		return nil
	}
	config := &spec.Configurations{Env: envVars}
	if input.EnableAutoInstrumentation != nil {
		config.EnableAutoInstrumentation = input.EnableAutoInstrumentation
	}
	return config
}

func buildInputInterface(interfaceType string, input internalAgentInput) (*spec.InputInterface, error) {
	inputInterface := &spec.InputInterface{Type: "HTTP"}
	if interfaceType != "CUSTOM" {
		return inputInterface, nil
	}

	if input.Port == nil || *input.Port < 1 || *input.Port > 65535 {
		return nil, fmt.Errorf("port is required for CUSTOM interface and must be 1-65535")
	}
	basePath := strings.TrimSpace(input.BasePath)
	if basePath == "" {
		return nil, fmt.Errorf("base_path is required for CUSTOM interface")
	}
	openAPIPath := strings.TrimSpace(input.OpenAPIPath)
	if openAPIPath == "" {
		return nil, fmt.Errorf("openapi_path is required for CUSTOM interface")
	}
	if !strings.HasPrefix(openAPIPath, "/") {
		return nil, fmt.Errorf("openapi_path must start with '/'")
	}

	port := int32(*input.Port)
	inputInterface.Port = &port
	inputInterface.BasePath = &basePath
	inputInterface.Schema = &spec.InputInterfaceSchema{Path: openAPIPath}
	return inputInterface, nil
}

func sanitizeEnvVars(env []envVarInput) []spec.EnvironmentVariable {
	if len(env) == 0 {
		return nil
	}

	sanitized := make([]spec.EnvironmentVariable, 0, len(env))
	for _, item := range env {
		key := strings.TrimSpace(item.Key)
		key = strings.ReplaceAll(key, "\\n", "")
		key = strings.ReplaceAll(key, "\\r", "")
		key = strings.ReplaceAll(key, "\n", "")
		key = strings.ReplaceAll(key, "\r", "")
		value := strings.TrimSpace(item.Value)
		if key == "" || value == "" {
			continue
		}
		key = strings.Join(strings.Fields(key), "_")
		valueCopy := value
		sanitized = append(sanitized, spec.EnvironmentVariable{Key: key, Value: &valueCopy})
	}
	return sanitized
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func matchesSearch(value, search string) bool {
	needle := strings.ToLower(strings.TrimSpace(search))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(value), needle)
}

// setupInstructions contains UI-aligned setup steps for external agents.
type setupInstructions struct {
	TokenDuration string
	OtelEndpoint  string
	Guides        map[string]setupGuide
}

type setupGuide struct {
	Language string
	Steps    []setupStep
}

type setupStep struct {
	StepNumber  int    `json:"step_number"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Code        string `json:"code"`
}

func buildSetupInstructions(otelEndpoint, apiKey, tokenDuration string) setupInstructions {
	pythonSteps := []setupStep{
		{
			StepNumber:  1,
			Title:       "Install AMP Instrumentation Package",
			Description: "Provides the ability to instrument your agent and export traces.",
			Code:        "pip install amp-instrumentation",
		},
		{
			StepNumber:  2,
			Title:       "Generate API Key",
			Description: "Token generated successfully. Copy it now as you won't be able to see it again.",
			Code:        fmt.Sprintf("AMP_AGENT_API_KEY=\"%s\"", apiKey),
		},
		{
			StepNumber:  3,
			Title:       "Set environment variables",
			Description: "Sets the agent endpoint and API key so traces can be exported securely.",
			Code:        fmt.Sprintf("export AMP_OTEL_ENDPOINT=\"%s\"\nexport AMP_AGENT_API_KEY=\"%s\"", otelEndpoint, apiKey),
		},
		{
			StepNumber:  4,
			Title:       "Run Agent with Instrumentation Enabled",
			Description: "Replace <run_command> with your agent's start command.",
			Code:        "amp-instrument <run_command>",
		},
	}

	ballerinaSteps := []setupStep{
		{
			StepNumber:  1,
			Title:       "Import Amp Module",
			Description: "Add the import to your Ballerina program.",
			Code:        "import ballerinax/amp as _;",
		},
		{
			StepNumber:  2,
			Title:       "Set environment variables",
			Description: "Sets the agent endpoint and API key so traces can be exported securely.",
			Code:        fmt.Sprintf("export AMP_OTEL_ENDPOINT=\"%s\"\nexport AMP_AGENT_API_KEY=\"%s\"", otelEndpoint, apiKey),
		},
		{
			StepNumber:  3,
			Title:       "Run Agent",
			Description: "Run your Ballerina agent with instrumentation enabled.",
			Code:        "bal run",
		},
	}

	return setupInstructions{
		TokenDuration: tokenDuration,
		OtelEndpoint:  otelEndpoint,
		Guides: map[string]setupGuide{
			"python":    {Language: "python", Steps: pythonSteps},
			"ballerina": {Language: "ballerina", Steps: ballerinaSteps},
		},
	}
}

// resolveConsoleOtelEndpoint mirrors the console behavior:
// use INSTRUMENTATION_URL when set, otherwise fall back to localhost:22893/otel.
func resolveConsoleOtelEndpoint(defaultEndpoint string) string {
	if env := strings.TrimSpace(os.Getenv("INSTRUMENTATION_URL")); env != "" {
		return env
	}
	return "http://localhost:22893/otel"
}
