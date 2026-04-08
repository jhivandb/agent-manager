package tools

import (
	"context"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

// Toolsets groups MCP tool handlers and configuration.
type Toolsets struct {
	AgentToolset         AgentToolsetHandler
	ProjectToolset       ProjectToolsetHandler
	BuildToolset         BuildToolsetHandler
	DeploymentToolset    DeploymentToolsetHandler
	TraceToolset         TraceToolsetHandler
	RuntimeLogToolset    RuntimeLogToolsetHandler
	EvaluatorToolset     EvaluatorToolsetHandler
	MonitorToolset       MonitorToolsetHandler
	MonitorScoresToolset MonitorScoresToolsetHandler
	DefaultOrg           string
}

// AgentToolsetHandler is the minimal surface needed by MCP agent tools.
type AgentToolsetHandler interface {
	ListAgents(ctx context.Context, orgName string, projName string, limit int32, offset int32) ([]*models.AgentResponse, int32, error)
	GenerateName(ctx context.Context, orgName string, payload spec.ResourceNameRequest) (string, error)
	CreateAgent(ctx context.Context, orgName string, projectName string, req *spec.CreateAgentRequest) error
	GetAgent(ctx context.Context, orgName string, projectName string, agentName string) (*models.AgentResponse, error)
	GenerateToken(ctx context.Context, orgName string, projectName string, agentName string, environment string, expiresIn string) (*spec.TokenResponse, error)
	GetAgentMetrics(ctx context.Context, orgName string, projectName string, agentName string, payload spec.MetricsFilterRequest) (*spec.MetricsResponse, error)
}

type ProjectToolsetHandler interface {
	ListProjects(ctx context.Context, orgName string, limit int, offset int) ([]*models.ProjectResponse, int32, error)
	ListOrganizations(ctx context.Context, limit int, offset int) ([]*models.OrganizationResponse, int32, error)
	CreateProject(ctx context.Context, orgName string, payload spec.CreateProjectRequest) (*models.ProjectResponse, error)
	ListEnvironments(ctx context.Context, orgName string) ([]*models.EnvironmentResponse, error)
}

type BuildToolsetHandler interface {
	ListAgentBuilds(ctx context.Context, orgName string, projectName string, agentName string, limit int32, offset int32) ([]*models.BuildResponse, int32, error)
	GetBuildLogs(ctx context.Context, orgName string, projectName string, agentName string, buildName string) (*models.LogsResponse, error)
	GetBuild(ctx context.Context, orgName string, projectName string, agentName string, buildName string) (*models.BuildDetailsResponse, error)
	BuildAgent(ctx context.Context, orgName string, projectName string, agentName string, commitId string) (*models.BuildResponse, error)
}

type DeploymentToolsetHandler interface {
	GetAgentDeployments(ctx context.Context, orgName string, projectName string, agentName string) ([]*models.DeploymentResponse, error)
	DeployAgent(ctx context.Context, orgName string, projectName string, agentName string, req *spec.DeployAgentRequest) (string, error)
	UpdateDeploymentState(ctx context.Context, orgName string, projectName string, agentName string, environment string, state string) error
}

type TraceToolsetHandler interface {
	ListTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (map[string]any, error)
	ExportTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (map[string]any, error)
	GetTraceDetails(ctx context.Context, orgName string, projectName string, agentName string, traceID string, environment string) (map[string]any, error)
}

type RuntimeLogToolsetHandler interface {
	GetRuntimeLogs(ctx context.Context, orgName string, projectName string, agentName string, payload spec.LogFilterRequest) (*models.LogsResponse, error)
}

type EvaluatorToolsetHandler interface {
	ListEvaluators(ctx context.Context, orgName string, limit int32, offset int32, search string, provider string, source string, tags []string) ([]*models.EvaluatorResponse, int32, error)
	GetEvaluator(ctx context.Context, orgName string, evaluatorID string) (*models.EvaluatorResponse, error)
	ListLLMProviders(ctx context.Context, orgName string) ([]spec.EvaluatorLLMProvider, error)
	CreateCustomEvaluator(ctx context.Context, orgName string, req *models.CreateCustomEvaluatorRequest) (*models.EvaluatorResponse, error)
	UpdateCustomEvaluator(ctx context.Context, orgName string, identifier string, req *models.UpdateCustomEvaluatorRequest) (*models.EvaluatorResponse, error)
}

type MonitorToolsetHandler interface {
	CreateMonitor(ctx context.Context, orgName string, req *models.CreateMonitorRequest) (*models.MonitorResponse, error)
	GetMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string) (*models.MonitorResponse, error)
	ListMonitors(ctx context.Context, orgName, projectName, agentName string) (*models.MonitorListResponse, error)
	UpdateMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string, req *models.UpdateMonitorRequest) (*models.MonitorResponse, error)
	StopMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string) (*models.MonitorResponse, error)
	StartMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string) (*models.MonitorResponse, error)
	ListMonitorRuns(ctx context.Context, orgName, projectName, agentName, monitorName string, limit, offset int, includeScores bool) (*models.MonitorRunsListResponse, error)
	RerunMonitor(ctx context.Context, orgName, projectName, agentName, monitorName, runID string) (*models.MonitorRunResponse, error)
	GetMonitorRunLogs(ctx context.Context, orgName, projectName, agentName, monitorName, runID string) (*models.LogsResponse, error)
}

type MonitorScoresToolsetHandler interface {
	GetMonitorScores(ctx context.Context, orgName, projectName, agentName, monitorName string, startTime, endTime time.Time, evaluator string, level string) (*models.MonitorScoresResponse, error)
	GetMonitorRunScores(ctx context.Context, orgName, projectName, agentName, monitorName, runID string) (*models.MonitorRunScoresResponse, error)
	GetMonitorScoresTimeSeries(ctx context.Context, orgName, projectName, agentName, monitorName string, startTime, endTime time.Time, evaluators []string) (*models.BatchTimeSeriesResponse, error)
	GetGroupedScores(ctx context.Context, orgName, projectName, agentName, monitorName string, startTime, endTime time.Time, level string) (*models.GroupedScoresResponse, error)
	GetAgentTraceScores(ctx context.Context, orgName, projectName, agentName string, startTime, endTime time.Time, limit, offset int) (*models.AgentTraceScoresResponse, error)
	GetTraceScores(ctx context.Context, orgName, projectName, agentName, traceID string) (*models.TraceScoresResponse, error)
}
