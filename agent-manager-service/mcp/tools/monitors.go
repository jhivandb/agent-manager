package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

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
		Description: "List all evaluation monitors for an agent." +
			"Monitor is a configured evaluation job that runs one or more evaluators against agent traces for a specific agent and environment, producing scores tracked over time.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
		}, []string{"project_name", "agent_name"}),
	}, withToolLogging("list_monitors", listMonitors(t.MonitorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_monitor",
		Description: "Get details for a specific monitor including its configurations, status and timestamps.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("get_monitor", getMonitor(t.MonitorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name: "create_monitor",
		Description: "Create a new evaluation monitor using one or more evaluators against agent traces. " +
			"Monitors can be 'future' (continuous, interval-based runs) or 'past' (one-time historical evaluation over a trace time range).",
		InputSchema: createSchema(map[string]any{
			"org_name":             stringProperty("Optional. Organization name."),
			"project_name":         stringProperty("Required. Project name."),
			"agent_name":           stringProperty("Required. Agent name."),
			"display_name":         stringProperty("Required. display name for the monitor."),
			"description":          stringProperty("Optional. Description of the monitor."),
			"evaluators":           arrayProperty("Required. List of evaluators with optional configuration.", evaluatorSchema),
			"llm_provider_configs": arrayProperty("Optional. LLM provider credentials for LLM-judge evaluators.", llmProviderSchema),
			"type":                 enumProperty("Required. Type of the monitor to be created.", []string{"future", "past"}),
			"interval_minutes":     intProperty("Optional. Interval in minutes for future monitors."),
			"trace_start":          stringProperty("Optional. RFC3339 start time for past monitors."),
			"trace_end":            stringProperty("Optional. RFC3339 end time for past monitors. Must be in the past; the latest allowed value is the current time."),
			"sampling_rate": map[string]any{
				"type":        "number",
				"description": "Optional. Sampling rate as a percentage (0 to 100). Defaults to 25."+
				" Sampling rate is the percentage of agent traces selected for evaluation in each monitor run, 100 evaluates all traces and lower values evaluate a subset to reduce cost and load.",
				"minimum":     0.0,
				"maximum":     100.0,
			},
		}, []string{"project_name", "agent_name", "display_name", "evaluators", "type"}),
	}, withToolLogging("create_monitor", createMonitor(t.MonitorToolset, t.ProjectToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "update_monitor",
		Description: "Update an existing monitor configurations.",
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
				"description": "Optional. Sampling rate (0.0 to 1.0).",
				"minimum":     0.0,
				"maximum":     1.0,
			},
		}, []string{"project_name", "agent_n`ame", "monitor_name"}),
	}, withToolLogging("update_monitor", updateMonitor(t.MonitorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "start_monitor",
		Description: "Restart a stopped monitor. Only applicable for future monitors",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("start_monitor", startMonitor(t.MonitorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "stop_monitor",
		Description: "Stop a currently running monitor. Only applicable for future monitors",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("stop_monitor", stopMonitor(t.MonitorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_monitor_runs",
		Description: "List execution runs for a specific monitor.",
		InputSchema: createSchema(map[string]any{
			"org_name":       stringProperty("Optional. Organization name."),
			"project_name":   stringProperty("Required. Project name."),
			"agent_name":     stringProperty("Required. Agent name."),
			"monitor_name":   stringProperty("Required. Monitor name."),
			"limit":          intProperty(fmt.Sprintf("Optional. Max runs to return (default %d, min 1, max %d).", defaultMonitorRunsLimit, maxMonitorRunsLimit)),
			"offset":         intProperty("Optional. Pagination offset (>= 0)."),
			"include_scores": boolProperty("Optional. Include evaluator score summaries per run."),
		}, []string{"project_name", "agent_name", "monitor_name"}),
	}, withToolLogging("list_monitor_runs", listMonitorRuns(t.MonitorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "rerun_monitor",
		Description: "Rerun a monitor with the same parameters as a previous run.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"run_id":       stringProperty("Required. Run ID of the specific monitor run to rerun."),
		}, []string{"project_name", "agent_name", "monitor_name", "run_id"}),
	}, withToolLogging("rerun_monitor", rerunMonitor(t.MonitorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_monitor_run_logs",
		Description: "Get execution logs for a specific monitor run.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"project_name": stringProperty("Required. Project name."),
			"agent_name":   stringProperty("Required. Agent name."),
			"monitor_name": stringProperty("Required. Monitor name."),
			"run_id":       stringProperty("Required. Run ID of the specific monitor run."),
		}, []string{"project_name", "agent_name", "monitor_name", "run_id"}),
	}, withToolLogging("get_monitor_run_logs", getMonitorRunLogs(t.MonitorToolset, t.DefaultOrg)))
}

func listMonitors(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listMonitorsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listMonitorsInput) (*gomcp.CallToolResult, any, error) {
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

func getMonitor(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, getMonitorInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func createMonitor(handler MonitorToolsetHandler, projectHandler ProjectToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, createMonitorInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func updateMonitor(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, updateMonitorInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		req := &models.UpdateMonitorRequest{
			DisplayName:     input.DisplayName,
			IntervalMinutes: input.IntervalMinutes,
			SamplingRate:    input.SamplingRate,
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

func startMonitor(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, startStopMonitorInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func stopMonitor(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, startStopMonitorInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func listMonitorRuns(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listMonitorRunsInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func rerunMonitor(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, rerunMonitorInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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

func getMonitorRunLogs(handler MonitorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, monitorRunLogsInput) (*gomcp.CallToolResult, any, error) {
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

		orgName := resolveOrgName(defaultOrg, input.OrgName)
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
