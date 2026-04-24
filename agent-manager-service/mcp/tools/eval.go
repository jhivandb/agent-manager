package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type listEvaluatorsInput struct {
	OrgName  string   `json:"org_name"`
	Limit    *int     `json:"limit,omitempty"`
	Offset   *int     `json:"offset,omitempty"`
	Search   string   `json:"search,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Source   string   `json:"source,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

type listEvaluatorItem struct {
	Identifier  string   `json:"identifier"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Provider    string   `json:"provider"`
	Level       string   `json:"level"`
	Tags        []string `json:"tags"`
	IsBuiltin   bool     `json:"is_builtin"`
	Type        string   `json:"type,omitempty"`
}

type listEvaluatorsOutput struct {
	OrgName    string              `json:"org_name"`
	Evaluators []listEvaluatorItem `json:"evaluators"`
	Total      int32               `json:"total"`
	Limit      int32               `json:"limit"`
	Offset     int32               `json:"offset"`
}

type getEvaluatorInput struct {
	OrgName     string `json:"org_name"`
	EvaluatorID string `json:"evaluator_id"`
}

type listEvaluatorLLMProvidersInput struct {
	OrgName string `json:"org_name"`
}

type createCustomEvaluatorInput struct {
	OrgName      string                        `json:"org_name"`
	Identifier   string                        `json:"identifier,omitempty"`
	DisplayName  string                        `json:"display_name"`
	Description  string                        `json:"description,omitempty"`
	Type         string                        `json:"type"`
	Level        string                        `json:"level"`
	Source       string                        `json:"source"`
	ConfigSchema []models.EvaluatorConfigParam `json:"config_schema,omitempty"`
	Tags         []string                      `json:"tags,omitempty"`
}

type updateCustomEvaluatorInput struct {
	OrgName      string                         `json:"org_name"`
	Identifier   string                         `json:"identifier"`
	DisplayName  *string                        `json:"display_name,omitempty"`
	Description  *string                        `json:"description,omitempty"`
	Source       *string                        `json:"source,omitempty"`
	ConfigSchema *[]models.EvaluatorConfigParam `json:"config_schema,omitempty"`
	Tags         *[]string                      `json:"tags,omitempty"`
}

type evaluatorDetail struct {
	Identifier   string                        `json:"identifier"`
	DisplayName  string                        `json:"display_name"`
	Description  string                        `json:"description"`
	Version      string                        `json:"version"`
	Provider     string                        `json:"provider"`
	Level        string                        `json:"level"`
	Tags         []string                      `json:"tags"`
	IsBuiltin    bool                          `json:"is_builtin"`
	ConfigSchema []models.EvaluatorConfigParam `json:"config_schema"`
	Type         string                        `json:"type,omitempty"`
	Source       string                        `json:"source,omitempty"`
}

type getEvaluatorOutput struct {
	OrgName   string          `json:"org_name"`
	Evaluator evaluatorDetail `json:"evaluator"`
}

func (t *Toolsets) registerEvaluatorTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name: "list_evaluators",
		Description: "List evaluators available in an organization. " +
			"An evaluator is a scoring component that reviews traces or spans and produces quality scores. " +
			"Results include both built-in evaluators provided by the platform and custom evaluators created by users.",
		InputSchema: createSchema(map[string]any{
			"org_name": stringProperty("Optional. Organization name."),
			"limit":    intProperty(fmt.Sprintf("Optional. Max evaluators to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
			"offset":   intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
			"search":   stringProperty("Optional. Filter evaluators by a search term."),
			"provider": stringProperty("Optional. Filter by evaluator provider, such as a built-in provider or a custom evaluator provider like custom_code or custom_llm_judge."),
			"source":   stringProperty("Optional. Filter by source of evaluators: all, builtin, or custom."),
			"tags":     arrayProperty("Optional. Filter evaluators by tags.", stringProperty("Tag value.")),
		}, nil),
	}, withToolLogging("list_evaluators", listEvaluators(t.EvaluatorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "get_evaluator",
		Description: "Get the full definition of an evaluator. " +
			"The result includes its type, evaluation level, provider, configuration schema, tags, and, for custom evaluators, the stored source.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"evaluator_id": stringProperty("Required. Evaluator identifier."),
		}, []string{"evaluator_id"}),
	}, withToolLogging("get_evaluator", getEvaluator(t.EvaluatorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "list_evaluator_llm_providers",
		Description: "List LLM providers that can run llm_judge evaluators. " +
			"Each provider entry describes the credentials or settings required to call that provider and the models available through it.",
		InputSchema: createSchema(map[string]any{
			"org_name": stringProperty("Optional. Organization name."),
		}, nil),
	}, withToolLogging("list_evaluator_llm_providers", listEvaluatorLLMProviders(t.EvaluatorToolset)))

	configSchemaItem := createSchema(map[string]any{
		"key":         stringProperty("Required. Config key."),
		"type":        stringProperty("Required. Value type (string, integer, float, boolean, array, enum)."),
		"description": stringProperty("Optional. Description."),
		"required":    boolProperty("Optional. Whether this field is required."),
		"default":     map[string]any{"description": "Optional default value."},
		"min":         map[string]any{"type": "number", "description": "Optional min value."},
		"max":         map[string]any{"type": "number", "description": "Optional max value."},
		"enum_values": arrayProperty("Optional. Allowed values for enum.", stringProperty("Enum value.")),
	}, []string{"key", "type"})

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "create_custom_evaluator",
		Description: "Create a custom evaluator. " +
			"A custom evaluator is a user-defined scoring component. " +
			"Use type `code` for Python function evaluators and `llm_judge` for prompt-template evaluators. " +
			"Use level `trace` to score a whole trace, `agent` to score one agent span, and `llm` to score one LLM call. " +
			"`config_schema` defines configurable parameters available to the evaluator source, and `source` must already contain the full implementation or prompt template.",
		InputSchema: createSchema(map[string]any{
			"org_name":      stringProperty("Optional. Organization name."),
			"identifier":    stringProperty("Optional. Custom evaluator identifier (slug)."),
			"display_name":  stringProperty("Required. Human-readable display name."),
			"description":   stringProperty("Optional. Short explanation of what the evaluator does."),
			"type":          enumProperty("Required. Evaluator type.", []string{"code", "llm_judge"}),
			"level":         enumProperty("Required. Evaluation level.", []string{"trace", "agent", "llm"}),
			"source":        stringProperty("Required. Full Python source code or prompt template."),
			"config_schema": arrayProperty("Optional. Evaluator configuration schema. It must align with the source.", configSchemaItem),
			"tags":          arrayProperty("Optional. Tags for search and grouping.", stringProperty("Tag value.")),
		}, []string{"display_name", "type", "level", "source"}),
	}, withToolLogging("create_custom_evaluator", createCustomEvaluator(t.EvaluatorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "update_custom_evaluator",
		Description: "Update an existing custom evaluator. " +
			"You can change its display metadata, source, configuration schema, or tags. The evaluator type and evaluation level stay fixed after creation.",
		InputSchema: createSchema(map[string]any{
			"org_name":      stringProperty("Optional. Organization name."),
			"identifier":    stringProperty("Required. Custom evaluator identifier."),
			"display_name":  stringProperty("Optional. Human-readable display name."),
			"description":   stringProperty("Optional. Description on what the evaluator does."),
			"source":        stringProperty("Optional. Updated source code or llm prompt template."),
			"config_schema": arrayProperty("Optional. Updated evaluator configuration schema.", configSchemaItem),
			"tags":          arrayProperty("Optional. Updated tags list.", stringProperty("Tag value.")),
		}, []string{"identifier"}),
	}, withToolLogging("update_custom_evaluator", updateCustomEvaluator(t.EvaluatorToolset)))
}

func listEvaluators(handler EvaluatorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, listEvaluatorsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listEvaluatorsInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(input.OrgName)
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

		evaluators, total, err := handler.ListEvaluators(ctx, orgName, int32(limit), int32(offset), input.Search, input.Provider, input.Source, input.Tags)
		if err != nil {
			return nil, nil, wrapToolError("list_evaluators", err)
		}

		formatted := make([]listEvaluatorItem, 0, len(evaluators))
		for _, evaluator := range evaluators {
			if evaluator == nil {
				continue
			}
			formatted = append(formatted, listEvaluatorItem{
				Identifier:  evaluator.Identifier,
				DisplayName: evaluator.DisplayName,
				Description: evaluator.Description,
				Provider:    evaluator.Provider,
				Level:       evaluator.Level,
				Tags:        evaluator.Tags,
				IsBuiltin:   evaluator.IsBuiltin,
				Type:        evaluator.Type,
			})
		}

		response := listEvaluatorsOutput{
			OrgName:    orgName,
			Evaluators: formatted,
			Total:      total,
			Limit:      int32(limit),
			Offset:     int32(offset),
		}
		return handleToolResult(response, nil)
	}
}

func getEvaluator(handler EvaluatorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, getEvaluatorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getEvaluatorInput) (*gomcp.CallToolResult, any, error) {
		if input.EvaluatorID == "" {
			return nil, nil, fmt.Errorf("evaluator_id is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		evaluator, err := handler.GetEvaluator(ctx, orgName, input.EvaluatorID)
		if err != nil {
			return nil, nil, wrapToolError("get_evaluator", err)
		}

		response := getEvaluatorOutput{
			OrgName:   orgName,
			Evaluator: formatEvaluatorDetail(evaluator),
		}
		return handleToolResult(response, nil)
	}
}

func listEvaluatorLLMProviders(handler EvaluatorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, listEvaluatorLLMProvidersInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listEvaluatorLLMProvidersInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		list, err := handler.ListLLMProviders(ctx, orgName)
		if err != nil {
			return nil, nil, wrapToolError("list_evaluator_llm_providers", err)
		}

		response := spec.EvaluatorLLMProviderListResponse{
			Count: int32(len(list)),
			List:  list,
		}
		return handleToolResult(response, nil)
	}
}

func createCustomEvaluator(handler EvaluatorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, createCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input createCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
		if input.DisplayName == "" {
			return nil, nil, fmt.Errorf("display_name is required")
		}
		if input.Type == "" {
			return nil, nil, fmt.Errorf("type is required")
		}
		if input.Type != "code" && input.Type != "llm_judge" {
			return nil, nil, fmt.Errorf("type must be one of: code, llm_judge")
		}
		if input.Level == "" {
			return nil, nil, fmt.Errorf("level is required")
		}
		if input.Level != "trace" && input.Level != "agent" && input.Level != "llm" {
			return nil, nil, fmt.Errorf("level must be one of: trace, agent, llm")
		}
		if input.Source == "" {
			return nil, nil, fmt.Errorf("source is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		req := &models.CreateCustomEvaluatorRequest{
			Identifier:   input.Identifier,
			DisplayName:  input.DisplayName,
			Description:  input.Description,
			Type:         input.Type,
			Level:        input.Level,
			Source:       input.Source,
			ConfigSchema: input.ConfigSchema,
			Tags:         input.Tags,
		}

		evaluator, err := handler.CreateCustomEvaluator(ctx, orgName, req)
		if err != nil {
			return nil, nil, wrapToolError("create_custom_evaluator", err)
		}

		response := getEvaluatorOutput{
			OrgName:   orgName,
			Evaluator: formatEvaluatorDetail(evaluator),
		}
		return handleToolResult(response, nil)
	}
}

func updateCustomEvaluator(handler EvaluatorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, updateCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input updateCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
		if input.Identifier == "" {
			return nil, nil, fmt.Errorf("identifier is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		req := &models.UpdateCustomEvaluatorRequest{
			DisplayName:  input.DisplayName,
			Description:  input.Description,
			Source:       input.Source,
			ConfigSchema: input.ConfigSchema,
			Tags:         input.Tags,
		}

		evaluator, err := handler.UpdateCustomEvaluator(ctx, orgName, input.Identifier, req)
		if err != nil {
			return nil, nil, wrapToolError("update_custom_evaluator", err)
		}

		response := getEvaluatorOutput{
			OrgName:   orgName,
			Evaluator: formatEvaluatorDetail(evaluator),
		}
		return handleToolResult(response, nil)
	}
}

func formatEvaluatorDetail(evaluator *models.EvaluatorResponse) evaluatorDetail {
	if evaluator == nil {
		return evaluatorDetail{}
	}
	return evaluatorDetail{
		Identifier:   evaluator.Identifier,
		DisplayName:  evaluator.DisplayName,
		Description:  evaluator.Description,
		Version:      evaluator.Version,
		Provider:     evaluator.Provider,
		Level:        evaluator.Level,
		Tags:         evaluator.Tags,
		IsBuiltin:    evaluator.IsBuiltin,
		ConfigSchema: evaluator.ConfigSchema,
		Type:         evaluator.Type,
		Source:       evaluator.Source,
	}
}

const (
	defaultMonitorRunsLimit = 20
	maxMonitorRunsLimit     = 100
)

type listMonitorsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
}

type monitorRunSummary struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type monitorSummary struct {
	Name            string             `json:"name"`
	DisplayName     string             `json:"display_name"`
	Description     string             `json:"description,omitempty"`
	Type            string             `json:"type"`
	EnvironmentName string             `json:"environment_name"`
	Evaluators      []string           `json:"evaluators,omitempty"`
	Status          string             `json:"status"`
	CreatedAt       time.Time          `json:"created_at"`
	LatestRun       *monitorRunSummary `json:"latest_run,omitempty"`
}

type getMonitorInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
}

type monitorEvaluatorInput struct {
	Identifier  string                 `json:"identifier"`
	DisplayName string                 `json:"display_name"`
	Config      map[string]interface{} `json:"config,omitempty"`
}

type monitorLLMProviderConfigInput struct {
	ProviderName string `json:"provider_name"`
	EnvVar       string `json:"env_var"`
	Value        string `json:"value"`
}

type createMonitorInput struct {
	OrgName            string                          `json:"org_name"`
	ProjectName        string                          `json:"project_name"`
	AgentName          string                          `json:"agent_name"`
	DisplayName        string                          `json:"display_name"`
	Description        *string                         `json:"description,omitempty"`
	Evaluators         []monitorEvaluatorInput         `json:"evaluators"`
	LLMProviderConfigs []monitorLLMProviderConfigInput `json:"llm_provider_configs,omitempty"`
	Type               string                          `json:"type"`
	IntervalMinutes    *int                            `json:"interval_minutes,omitempty"`
	TraceStart         string                          `json:"trace_start,omitempty"`
	TraceEnd           string                          `json:"trace_end,omitempty"`
	SamplingRate       *float64                        `json:"sampling_rate,omitempty"`
}

type updateMonitorInput struct {
	OrgName            string                           `json:"org_name"`
	ProjectName        string                           `json:"project_name"`
	AgentName          string                           `json:"agent_name"`
	MonitorName        string                           `json:"monitor_name"`
	DisplayName        *string                          `json:"display_name,omitempty"`
	Evaluators         *[]monitorEvaluatorInput         `json:"evaluators,omitempty"`
	LLMProviderConfigs *[]monitorLLMProviderConfigInput `json:"llm_provider_configs,omitempty"`
	IntervalMinutes    *int                             `json:"interval_minutes,omitempty"`
	SamplingRate       *float64                         `json:"sampling_rate,omitempty"`
}

type listMonitorRunsInput struct {
	OrgName       string `json:"org_name"`
	ProjectName   string `json:"project_name"`
	AgentName     string `json:"agent_name"`
	MonitorName   string `json:"monitor_name"`
	Limit         *int   `json:"limit,omitempty"`
	Offset        *int   `json:"offset,omitempty"`
	IncludeScores *bool  `json:"include_scores,omitempty"`
}

type rerunMonitorInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
	RunID       string `json:"run_id"`
}

type monitorRunLogsInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
	RunID       string `json:"run_id"`
}

type startStopMonitorInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
}

func (t *Toolsets) registerMonitorTools(server *gomcp.Server) {
	evaluatorSchema := createSchema(map[string]any{
		"identifier":   stringProperty("Required. Evaluator identifier."),
		"display_name": stringProperty("Required. Display name unique within the monitor."),
		"config": map[string]any{
			"type":                 "object",
			"description":          "Optional. Evaluator-specific configuration.",
			"additionalProperties": true,
		},
	}, []string{"identifier", "display_name"})

	llmProviderSchema := createSchema(map[string]any{
		"provider_name": stringProperty("Required. LLM provider name (from evaluator LLM providers list)."),
		"env_var":       stringProperty("Required. Env var name expected by the provider."),
		"value":         stringProperty("Required. Secret value (API key)."),
	}, []string{"provider_name", "env_var", "value"})

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "list_monitors",
		Description: "List monitors configured for an agent. " +
			"A monitor is an evaluation setup that runs one or more evaluators against an agent's traces and keeps track of the resulting scores over time.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("list_monitors", listMonitors(t.MonitorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "get_monitor",
		Description: "Get the full definition of a monitor. " +
			"The result includes its evaluator configuration, scheduling or historical time window, current status, and latest run information.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("get_monitor", getMonitor(t.MonitorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "create_monitor",
		Description: "Create a monitor for evaluating an agent with one or more evaluators. " +
			"A `future` monitor runs continuously on new traces at a configured interval, while a `past` monitor evaluates a historical trace window once. " +
			"Sampling rate is the percentage of traces selected for evaluation, and the monitor identifier is generated from the display name.",
		InputSchema: createSchema(map[string]any{
			"org_name":             stringProperty("Optional. Organization name."),
			"project_name":         stringProperty("Required. Project name."),
			"agent_name":           stringProperty("Required. Agent name."),
			"display_name":         stringProperty("Required. display name for the monitor."),
			"description":          stringProperty("Optional. Description of the monitor."),
			"evaluators":           arrayProperty("Required. List of evaluators with optional configuration.", evaluatorSchema),
			"llm_provider_configs": arrayProperty("Optional. LLM provider credentials for LLM-judge evaluators.", llmProviderSchema),
			"type":                 enumProperty("Required. Monitor type. Use `future` for continuous monitoring or `past` for a one-time historical evaluation.", []string{"future", "past"}),
			"interval_minutes":     intProperty("Optional. Interval in minutes between runs for future monitors. Defaults to 10 minutes for MCP-created future monitors."),
			"trace_start":          stringProperty("Optional. RFC3339 start time for past monitors. Defaults to the last 24 hours if omitted."),
			"trace_end":            stringProperty("Optional. RFC3339 end time for past monitors. Defaults to now if omitted and must not be in the future."),
			"sampling_rate": map[string]any{
				"type": "number",
				"description": "Optional. Sampling rate as a percentage (0 to 100). Defaults to 25." +
					" Sampling rate is the percentage of agent traces selected for evaluation in each monitor run, 100 evaluates all traces and lower values evaluate a subset to reduce cost and load.",
				"minimum": 0.0,
				"maximum": 100.0,
			},
		}, []string{"project_name", "agent_name", "display_name", "evaluators", "type"}),
	}, withToolLogging("create_monitor", createMonitor(t.MonitorToolset, t.ProjectToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "update_monitor",
		Description: "Update an existing monitor configuration. " +
			"You can change its display name, evaluator settings, LLM provider credentials, interval, and sampling rate. " +
			"For past monitors, updating the configuration triggers a new evaluation run over the stored historical window.",
		InputSchema: createSchema(map[string]any{
			"org_name":             stringProperty("Optional. Organization name."),
			"project_name":         stringProperty("Required. Project name."),
			"agent_name":           stringProperty("Required. Agent name."),
			"monitor_name":         stringProperty("Required. Monitor name."),
			"display_name":         stringProperty("Optional. Human-readable display name."),
			"evaluators":           arrayProperty("Optional. Updated evaluators.", evaluatorSchema),
			"llm_provider_configs": arrayProperty("Optional. Updated LLM provider credentials.", llmProviderSchema),
			"interval_minutes":     intProperty("Optional. Interval in minutes for future monitors."),
			"sampling_rate": map[string]any{
				"type":        "number",
				"description": "Optional. Sampling rate as a percentage (0 to 100). 100 evaluates all traces and lower values evaluate a subset.",
				"minimum":     0.0,
				"maximum":     100.0,
			},
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("update_monitor", updateMonitor(t.MonitorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "start_monitor",
		Description: "Start a stopped future monitor. " +
			"This resumes continuous monitoring by scheduling the next monitor run.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("start_monitor", startMonitor(t.MonitorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "stop_monitor",
		Description: "Stop an active future monitor. " +
			"This suspends future monitor runs without deleting the monitor definition.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("stop_monitor", stopMonitor(t.MonitorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "list_monitor_runs",
		Description: "List runs for a monitor. " +
			"A monitor run is one execution of the monitor over a selected trace window, with its own status, timestamps, and optional score summary.",
		InputSchema: createSchema(map[string]any{
			"org_name":       stringProperty("Optional. Organization name."),
			"project_name":   stringProperty("Required. Project name."),
			"agent_name":     stringProperty("Required. Agent name."),
			"monitor_name":   stringProperty("Required. Monitor name."),
			"limit":          intProperty(fmt.Sprintf("Optional. Max runs to return (default %d, min 1, max %d).", defaultMonitorRunsLimit, maxMonitorRunsLimit)),
			"offset":         intProperty("Optional. Pagination offset (>= 0)."),
			"include_scores": boolProperty("Optional. Include evaluator score summaries per run."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("list_monitor_runs", listMonitorRuns(t.MonitorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "rerun_monitor",
		Description: "Create a new monitor run using the same trace window and evaluator snapshot as a previous run.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"run_id":       stringProperty("Required. Run ID of the specific monitor run to rerun."),
		}, []string{"project_name", "agent_name", "monitor_name", "run_id"}),
	}, withToolLogging("rerun_monitor", rerunMonitor(t.MonitorToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "get_monitor_run_logs",
		Description: "Get execution logs for a monitor run. " +
			"These logs are produced by the evaluation workflow for that run, not by the monitored agent's runtime itself.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"run_id":       stringProperty("Required. Run ID of the specific monitor run."),
		}, []string{"project_name", "agent_name", "monitor_name", "run_id"}),
	}, withToolLogging("get_monitor_run_logs", getMonitorRunLogs(t.MonitorToolset)))
}

func listMonitors(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, listMonitorsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listMonitorsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		monitors, err := handler.ListMonitors(ctx, orgName, input.ProjectName, input.AgentName)
		if err != nil {
			return nil, nil, wrapToolError("list_monitors", err)
		}

		total := 0
		summaries := []monitorSummary{}
		if monitors != nil && len(monitors.Monitors) > 0 {
			total = monitors.Total
			summaries = make([]monitorSummary, 0, len(monitors.Monitors))
			for _, monitor := range monitors.Monitors {
				summary := monitorSummary{
					Name:            monitor.Name,
					DisplayName:     monitor.DisplayName,
					Description:     monitor.Description,
					Type:            monitor.Type,
					EnvironmentName: monitor.EnvironmentName,
					Status:          string(monitor.Status),
					CreatedAt:       monitor.CreatedAt,
				}
				if len(monitor.Evaluators) > 0 {
					evaluators := make([]string, 0, len(monitor.Evaluators))
					for _, eval := range monitor.Evaluators {
						name := eval.DisplayName
						if name == "" {
							name = eval.Identifier
						}
						if name != "" {
							evaluators = append(evaluators, name)
						}
					}
					if len(evaluators) > 0 {
						summary.Evaluators = evaluators
					}
				}
				if monitor.LatestRun != nil {
					summary.LatestRun = &monitorRunSummary{
						ID:          monitor.LatestRun.ID,
						Status:      monitor.LatestRun.Status,
						StartedAt:   monitor.LatestRun.StartedAt,
						CompletedAt: monitor.LatestRun.CompletedAt,
					}
				}
				summaries = append(summaries, summary)
			}
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitors":     summaries,
			"total":        total,
		}
		return handleToolResult(response, nil)
	}
}

func getMonitor(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, getMonitorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getMonitorInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		monitor, err := handler.GetMonitor(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName)
		if err != nil {
			return nil, nil, wrapToolError("get_monitor", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"monitor":      utils.ConvertToMonitorResponse(monitor),
		}
		return handleToolResult(response, nil)
	}
}

func createMonitor(handler MonitorToolsetHandler, projectHandler ProjectToolsetHandler) func(context.Context, *gomcp.CallToolRequest, createMonitorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input createMonitorInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if strings.TrimSpace(input.DisplayName) == "" {
			return nil, nil, fmt.Errorf("display_name is required")
		}
		if len(input.Evaluators) == 0 {
			return nil, nil, fmt.Errorf("evaluators must include at least one evaluator")
		}
		if input.Type == "" {
			return nil, nil, fmt.Errorf("type is required")
		}
		if input.Type != models.MonitorTypeFuture && input.Type != models.MonitorTypePast {
			return nil, nil, fmt.Errorf("type must be 'future' or 'past'")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		monitorName := slugifyMonitorName(input.DisplayName)
		if len(monitorName) < 3 {
			return nil, nil, fmt.Errorf("display_name must produce a valid name (min 3 characters)")
		}

		environmentName, err := resolveMonitorEnvironment(ctx, projectHandler, orgName)
		if err != nil {
			return nil, nil, err
		}

		traceStart, traceEnd, err := resolveMonitorTimeRange(input.Type, input.TraceStart, input.TraceEnd)
		if err != nil {
			return nil, nil, err
		}

		var evaluators []models.MonitorEvaluator
		for _, eval := range input.Evaluators {
			if eval.Identifier == "" || eval.DisplayName == "" {
				return nil, nil, fmt.Errorf("each evaluator must include identifier and display_name")
			}
			evaluators = append(evaluators, models.MonitorEvaluator{
				Identifier:  eval.Identifier,
				DisplayName: eval.DisplayName,
				Config:      eval.Config,
			})
		}

		var llmConfigs []models.MonitorLLMProviderConfig
		for _, cfg := range input.LLMProviderConfigs {
			if cfg.ProviderName == "" || cfg.EnvVar == "" || cfg.Value == "" {
				return nil, nil, fmt.Errorf("each llm_provider_config must include provider_name, env_var, and value")
			}
			llmConfigs = append(llmConfigs, models.MonitorLLMProviderConfig{
				ProviderName: cfg.ProviderName,
				EnvVar:       cfg.EnvVar,
				Value:        cfg.Value,
			})
		}

		description := ""
		if input.Description != nil {
			description = *input.Description
		}

		samplingRate := resolveSamplingRate(input.SamplingRate)
		if samplingRate < 0 || samplingRate > 1 {
			return nil, nil, fmt.Errorf("sampling_rate must be between 0 and 100")
		}
		intervalMinutes := input.IntervalMinutes
		if input.Type == models.MonitorTypeFuture && intervalMinutes == nil {
			defaultInterval := 10
			intervalMinutes = &defaultInterval
		}

		req := &models.CreateMonitorRequest{
			Name:               monitorName,
			DisplayName:        strings.TrimSpace(input.DisplayName),
			Description:        description,
			ProjectName:        input.ProjectName,
			AgentName:          input.AgentName,
			EnvironmentName:    environmentName,
			Evaluators:         evaluators,
			LLMProviderConfigs: llmConfigs,
			Type:               input.Type,
			IntervalMinutes:    intervalMinutes,
			TraceStart:         traceStart,
			TraceEnd:           traceEnd,
			SamplingRate:       &samplingRate,
		}

		monitor, err := handler.CreateMonitor(ctx, orgName, req)
		if err != nil {
			return nil, nil, wrapToolError("create_monitor", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor":      utils.ConvertToMonitorResponse(monitor),
		}
		return handleToolResult(response, nil)
	}
}

func updateMonitor(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, updateMonitorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input updateMonitorInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		req := &models.UpdateMonitorRequest{
			DisplayName:     input.DisplayName,
			IntervalMinutes: input.IntervalMinutes,
		}

		if input.SamplingRate != nil {
			rate := *input.SamplingRate / 100
			if rate < 0 || rate > 1 {
				return nil, nil, fmt.Errorf("sampling_rate must be between 0 and 100")
			}
			req.SamplingRate = &rate
		}

		if input.Evaluators != nil && len(*input.Evaluators) > 0 {
			converted := make([]models.MonitorEvaluator, 0, len(*input.Evaluators))
			for _, eval := range *input.Evaluators {
				if eval.Identifier == "" || eval.DisplayName == "" {
					return nil, nil, fmt.Errorf("each evaluator must include identifier and display_name")
				}
				converted = append(converted, models.MonitorEvaluator{
					Identifier:  eval.Identifier,
					DisplayName: eval.DisplayName,
					Config:      eval.Config,
				})
			}
			req.Evaluators = &converted
		}

		if input.LLMProviderConfigs != nil && len(*input.LLMProviderConfigs) > 0 {
			converted := make([]models.MonitorLLMProviderConfig, 0, len(*input.LLMProviderConfigs))
			for _, cfg := range *input.LLMProviderConfigs {
				if cfg.ProviderName == "" || cfg.EnvVar == "" || cfg.Value == "" {
					return nil, nil, fmt.Errorf("each llm_provider_config must include provider_name, env_var, and value")
				}
				converted = append(converted, models.MonitorLLMProviderConfig{
					ProviderName: cfg.ProviderName,
					EnvVar:       cfg.EnvVar,
					Value:        cfg.Value,
				})
			}
			req.LLMProviderConfigs = &converted
		}

		monitor, err := handler.UpdateMonitor(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, req)
		if err != nil {
			return nil, nil, wrapToolError("update_monitor", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"monitor":      utils.ConvertToMonitorResponse(monitor),
		}
		return handleToolResult(response, nil)
	}
}

func startMonitor(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, startStopMonitorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input startStopMonitorInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		monitor, err := handler.StartMonitor(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName)
		if err != nil {
			return nil, nil, wrapToolError("start_monitor", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"monitor":      utils.ConvertToMonitorResponse(monitor),
		}
		return handleToolResult(response, nil)
	}
}

func stopMonitor(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, startStopMonitorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input startStopMonitorInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		monitor, err := handler.StopMonitor(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName)
		if err != nil {
			return nil, nil, wrapToolError("stop_monitor", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"monitor":      utils.ConvertToMonitorResponse(monitor),
		}
		return handleToolResult(response, nil)
	}
}

func slugifyMonitorName(value string) string {
	slug := strings.ToLower(value)
	slug = strings.TrimSpace(slug)
	slug = strings.ReplaceAll(slug, " ", "-")
	var b strings.Builder
	b.Grow(len(slug))
	lastDash := false
	for _, r := range slug {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 60 {
		return result[:60]
	}
	return result
}

func resolveMonitorEnvironment(ctx context.Context, projectHandler ProjectToolsetHandler, orgName string) (string, error) {
	if env := strings.TrimSpace(os.Getenv("AMP_ENV")); env != "" {
		return env, nil
	}
	if projectHandler != nil {
		environments, err := projectHandler.ListEnvironments(ctx, orgName)
		if err != nil {
			return "", wrapToolError("create_monitor", err)
		}
		if len(environments) > 0 && environments[0] != nil && environments[0].Name != "" {
			return environments[0].Name, nil
		}
	}
	return defaultEnvName, nil
}

func resolveMonitorTimeRange(monitorType, traceStart, traceEnd string) (*time.Time, *time.Time, error) {
	if monitorType != models.MonitorTypePast {
		return nil, nil, nil
	}
	if strings.TrimSpace(traceStart) == "" || strings.TrimSpace(traceEnd) == "" {
		end := time.Now().UTC()
		start := end.Add(-24 * time.Hour)
		return &start, &end, nil
	}
	return parseOptionalTimeRange(traceStart, traceEnd)
}

func resolveSamplingRate(rate *float64) float64 {
	if rate == nil {
		return 0.25
	}
	return *rate / 100
}

func listMonitorRuns(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, listMonitorRunsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listMonitorRunsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		limit := defaultMonitorRunsLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		if limit < 1 || limit > maxMonitorRunsLimit {
			return nil, nil, fmt.Errorf("limit must be between 1 and %d", maxMonitorRunsLimit)
		}

		offset := utils.DefaultOffset
		if input.Offset != nil {
			offset = *input.Offset
		}
		if offset < utils.MinOffset {
			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
		}

		includeScores := false
		if input.IncludeScores != nil {
			includeScores = *input.IncludeScores
		}

		runs, err := handler.ListMonitorRuns(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, limit, offset, includeScores)
		if err != nil {
			return nil, nil, wrapToolError("list_monitor_runs", err)
		}

		response := map[string]any{
			"org_name":       orgName,
			"project_name":   input.ProjectName,
			"agent_name":     input.AgentName,
			"monitor_name":   input.MonitorName,
			"include_scores": includeScores,
			"runs":           utils.ConvertToMonitorRunListResponse(runs),
		}
		return handleToolResult(response, nil)
	}
}

func rerunMonitor(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, rerunMonitorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input rerunMonitorInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}
		if input.RunID == "" {
			return nil, nil, fmt.Errorf("run_id is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		run, err := handler.RerunMonitor(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, input.RunID)
		if err != nil {
			return nil, nil, wrapToolError("rerun_monitor", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"run":          utils.ConvertToMonitorRunResponse(run),
		}
		return handleToolResult(response, nil)
	}
}

func getMonitorRunLogs(handler MonitorToolsetHandler) func(context.Context, *gomcp.CallToolRequest, monitorRunLogsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorRunLogsInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}
		if input.RunID == "" {
			return nil, nil, fmt.Errorf("run_id is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		logs, err := handler.GetMonitorRunLogs(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, input.RunID)
		if err != nil {
			return nil, nil, wrapToolError("get_monitor_run_logs", err)
		}

		return handleToolResult(reduceLogsResponse(logs), nil)
	}
}

const (
	defaultAgentTraceScoresLimit = 100
	maxAgentTraceScoresLimit     = 100
)

type monitorScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Evaluator   string `json:"evaluator,omitempty"`
	Level       string `json:"level,omitempty"`
}

type monitorRunScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	MonitorName string `json:"monitor_name"`
	RunID       string `json:"run_id"`
}

type monitorScoresTimeSeriesInput struct {
	OrgName     string   `json:"org_name"`
	ProjectName string   `json:"project_name"`
	AgentName   string   `json:"agent_name"`
	MonitorName string   `json:"monitor_name"`
	StartTime   string   `json:"start_time"`
	EndTime     string   `json:"end_time"`
	Evaluators  []string `json:"evaluators"`
}

// type monitorScoresBreakdownInput struct {
// 	OrgName     string `json:"org_name"`
// 	ProjectName string `json:"project_name"`
// 	AgentName   string `json:"agent_name"`
// 	MonitorName string `json:"monitor_name"`
// 	StartTime   string `json:"start_time"`
// 	EndTime     string `json:"end_time"`
// 	Level       string `json:"level"`
// }

type agentTraceScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Limit       *int   `json:"limit,omitempty"`
	Offset      *int   `json:"offset,omitempty"`
}

type traceScoresInput struct {
	OrgName     string `json:"org_name"`
	ProjectName string `json:"project_name"`
	AgentName   string `json:"agent_name"`
	TraceID     string `json:"trace_id"`
}

func (t *Toolsets) registerMonitorScoresTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name: "monitor_scores",
		Description: "Get aggregated evaluator scores for a monitor within a time range. " +
			"A monitor score summarizes how each evaluator performed across the traces selected by that monitor during the requested period.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"start_time":   stringProperty("Required. RFC3339 start time (UTC)."),
			"end_time":     stringProperty("Required. RFC3339 end time (UTC)."),
			"evaluator":    stringProperty("Optional. Filter by evaluator display name (unique within monitor)."),
			"level":        enumProperty("Optional. Filter by evaluation level. Use `trace` for whole-trace scores, `agent` for agent-span scores, and `llm` for LLM-call scores.", []string{"trace", "agent", "llm"}),
		}, []string{"project_name", "agent_name", "monitor_name", "start_time", "end_time"}),
	}, withToolLogging("monitor_scores", getMonitorScores(t.MonitorScoresToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "monitor_run_scores",
		Description: "Get aggregated evaluator scores for a monitor run. " +
			"A monitor run is one execution of a monitor over a specific trace window.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"run_id":       stringProperty("Required. Monitor run ID."),
		}, []string{"project_name", "agent_name", "monitor_name", "run_id"}),
	}, withToolLogging("monitor_run_scores", getMonitorRunScores(t.MonitorScoresToolset)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "monitor_scores_timeseries",
		Description: "Get time-series score trends for one or more evaluators in a monitor. " +
			"A time series groups scores into time buckets so you can see how evaluator performance changes over time.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"start_time":   stringProperty("Required. RFC3339 start time (UTC)."),
			"end_time":     stringProperty("Required. RFC3339 end time (UTC)."),
			"evaluators":   arrayProperty("Required. Evaluator display names (unique within monitor).", stringProperty("Evaluator name.")),
		}, []string{"project_name", "agent_name", "monitor_name", "start_time", "end_time", "evaluators"}),
	}, withToolLogging("monitor_scores_timeseries", getMonitorScoresTimeSeries(t.MonitorScoresToolset)))

	//per trace aggregrated score
	gomcp.AddTool(server, &gomcp.Tool{
		Name: "agent_trace_scores",
		Description: "Get aggregated scores per trace for an agent within a time range. " +
			"Each result summarizes the evaluation outcome recorded for one trace.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"start_time":   stringProperty("Required. RFC3339 start time (UTC)."),
			"end_time":     stringProperty("Required. RFC3339 end time (UTC)."),
			"limit":        intProperty(fmt.Sprintf("Optional. Max trace scores to return (default %d, min 1, max %d).", defaultAgentTraceScoresLimit, maxAgentTraceScoresLimit)),
			"offset":       intProperty("Optional. Pagination offset (>= 0)."),
		}, []string{"project_name", "agent_name", "start_time", "end_time"}),
	}, withToolLogging("agent_trace_scores", getAgentTraceScores(t.MonitorScoresToolset)))

	//detailed overview
	gomcp.AddTool(server, &gomcp.Tool{
		Name: "trace_scores",
		Description: "Get all evaluation scores recorded for a single trace across monitors. " +
			"Results are grouped by monitor and, when available, by evaluated spans such as agent spans or LLM calls.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"trace_id":     stringProperty("Required. Trace ID."),
		}, []string{"project_name", "agent_name", "trace_id"}),
	}, withToolLogging("trace_scores", getTraceScores(t.MonitorScoresToolset)))
}

func getMonitorScores(handler MonitorScoresToolsetHandler) func(context.Context, *gomcp.CallToolRequest, monitorScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorScoresInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}

		level := strings.TrimSpace(input.Level)
		if level != "" && level != "trace" && level != "agent" && level != "llm" {
			return nil, nil, fmt.Errorf("level must be one of: trace, agent, llm")
		}

		result, err := handler.GetMonitorScores(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, startTime, endTime, strings.TrimSpace(input.Evaluator), level)
		if err != nil {
			return nil, nil, wrapToolError("monitor_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"scores":       utils.ConvertToMonitorScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

func getMonitorRunScores(handler MonitorScoresToolsetHandler) func(context.Context, *gomcp.CallToolRequest, monitorRunScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorRunScoresInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}
		if input.RunID == "" {
			return nil, nil, fmt.Errorf("run_id is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		result, err := handler.GetMonitorRunScores(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, input.RunID)
		if err != nil {
			return nil, nil, wrapToolError("monitor_run_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"run_id":       input.RunID,
			"scores":       utils.ConvertToMonitorRunScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

func getMonitorScoresTimeSeries(handler MonitorScoresToolsetHandler) func(context.Context, *gomcp.CallToolRequest, monitorScoresTimeSeriesInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorScoresTimeSeriesInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.MonitorName == "" {
			return nil, nil, fmt.Errorf("monitor_name is required")
		}
		if len(input.Evaluators) == 0 {
			return nil, nil, fmt.Errorf("evaluators must include at least one evaluator name")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}

		evaluators := make([]string, 0, len(input.Evaluators))
		for _, name := range input.Evaluators {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" {
				evaluators = append(evaluators, trimmed)
			}
		}
		if len(evaluators) == 0 {
			return nil, nil, fmt.Errorf("evaluators must include at least one evaluator name")
		}

		result, err := handler.GetMonitorScoresTimeSeries(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, startTime, endTime, evaluators)
		if err != nil {
			return nil, nil, wrapToolError("monitor_scores_timeseries", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"monitor_name": input.MonitorName,
			"series":       utils.ConvertToBatchTimeSeriesResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

// func getMonitorScoresBreakdown(handler MonitorScoresToolsetHandler) func(context.Context, *gomcp.CallToolRequest, monitorScoresBreakdownInput) (*gomcp.CallToolResult, any, error) {
// 	return func(ctx context.Context, _ *gomcp.CallToolRequest, input monitorScoresBreakdownInput) (*gomcp.CallToolResult, any, error) {
// 		if input.ProjectName == "" {
// 			return nil, nil, fmt.Errorf("project_name is required")
// 		}
// 		if input.AgentName == "" {
// 			return nil, nil, fmt.Errorf("agent_name is required")
// 		}
// 		if input.MonitorName == "" {
// 			return nil, nil, fmt.Errorf("monitor_name is required")
// 		}
// 		if strings.TrimSpace(input.Level) == "" {
// 			return nil, nil, fmt.Errorf("level is required")
// 		}
// 		level := strings.TrimSpace(input.Level)
// 		if level != "agent" && level != "llm" {
// 			return nil, nil, fmt.Errorf("level must be one of: agent, llm")
// 		}

// 		orgName := resolveOrgName(input.OrgName)
// 		if orgName == "" {
// 			return nil, nil, fmt.Errorf("org_name is required")
// 		}

// 		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
// 		if err != nil {
// 			return nil, nil, err
// 		}

// 		result, err := handler.GetGroupedScores(ctx, orgName, input.ProjectName, input.AgentName, input.MonitorName, startTime, endTime, level)
// 		if err != nil {
// 			return nil, nil, wrapToolError("monitor_scores_breakdown", err)
// 		}

// 		response := map[string]any{
// 			"org_name":     orgName,
// 			"project_name": input.ProjectName,
// 			"agent_name":   input.AgentName,
// 			"monitor_name": input.MonitorName,
// 			"scores":       utils.ConvertToGroupedScoresResponse(result),
// 		}
// 		return handleToolResult(response, nil)
// 	}
// }

func getAgentTraceScores(handler MonitorScoresToolsetHandler) func(context.Context, *gomcp.CallToolRequest, agentTraceScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input agentTraceScoresInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		startTime, endTime, err := parseRequiredTimeRange(input.StartTime, input.EndTime)
		if err != nil {
			return nil, nil, err
		}

		limit := defaultAgentTraceScoresLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		if limit < 1 || limit > maxAgentTraceScoresLimit {
			return nil, nil, fmt.Errorf("limit must be between 1 and %d", maxAgentTraceScoresLimit)
		}

		offset := utils.DefaultOffset
		if input.Offset != nil {
			offset = *input.Offset
		}
		if offset < utils.MinOffset {
			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
		}

		result, err := handler.GetAgentTraceScores(ctx, orgName, input.ProjectName, input.AgentName, startTime, endTime, limit, offset)
		if err != nil {
			return nil, nil, wrapToolError("agent_trace_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"limit":        limit,
			"offset":       offset,
			"scores":       utils.ConvertToAgentTraceScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}

func getTraceScores(handler MonitorScoresToolsetHandler) func(context.Context, *gomcp.CallToolRequest, traceScoresInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input traceScoresInput) (*gomcp.CallToolResult, any, error) {
		if input.ProjectName == "" {
			return nil, nil, fmt.Errorf("project_name is required")
		}
		if input.AgentName == "" {
			return nil, nil, fmt.Errorf("agent_name is required")
		}
		if input.TraceID == "" {
			return nil, nil, fmt.Errorf("trace_id is required")
		}

		orgName := resolveOrgName(input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		result, err := handler.GetTraceScores(ctx, orgName, input.ProjectName, input.AgentName, input.TraceID)
		if err != nil {
			return nil, nil, wrapToolError("trace_scores", err)
		}

		response := map[string]any{
			"org_name":     orgName,
			"project_name": input.ProjectName,
			"agent_name":   input.AgentName,
			"trace_id":     input.TraceID,
			"scores":       utils.ConvertToTraceScoresResponse(result),
		}
		return handleToolResult(response, nil)
	}
}
