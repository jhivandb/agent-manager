package tools

import (
	"context"
<<<<<<< HEAD
	"time"
=======
>>>>>>> e9b61488 (mcp tools v1)

	"github.com/wso2/ai-agent-management-platform/agent-manager-service/models"
	"github.com/wso2/ai-agent-management-platform/agent-manager-service/spec"
)

// Toolsets groups MCP tool handlers and configuration.
type Toolsets struct {
	AgentToolset      AgentToolsetHandler
	ProjectToolset    ProjectToolsetHandler
	BuildToolset      BuildToolsetHandler
	DeploymentToolset DeploymentToolsetHandler
	TraceToolset      TraceToolsetHandler
	RuntimeLogToolset RuntimeLogToolsetHandler
	DefaultOrg        string
}

// AgentToolsetHandler is the minimal surface needed by MCP agent tools.
type AgentToolsetHandler interface {
	ListAgents(ctx context.Context, orgName string, projName string, limit int32, offset int32) ([]*models.AgentResponse, int32, error)
	GenerateName(ctx context.Context, orgName string, payload spec.ResourceNameRequest) (string, error)
	CreateAgent(ctx context.Context, orgName string, projectName string, req *spec.CreateAgentRequest) error
	GetAgent(ctx context.Context, orgName string, projectName string, agentName string) (*models.AgentResponse, error)
	GenerateToken(ctx context.Context, orgName string, projectName string, agentName string, environment string, expiresIn string) (*spec.TokenResponse, error)
}

type ProjectToolsetHandler interface {
	ListProjects(ctx context.Context, orgName string, limit int, offset int) ([]*models.ProjectResponse, int32, error)
}

type BuildToolsetHandler interface {
	ListAgentBuilds(ctx context.Context, orgName string, projectName string, agentName string, limit int32, offset int32) ([]*models.BuildResponse, int32, error)
	GetBuildLogs(ctx context.Context, orgName string, projectName string, agentName string, buildName string) (*models.LogsResponse, error)
}

type DeploymentToolsetHandler interface {
	GetAgentDeployments(ctx context.Context, orgName string, projectName string, agentName string) ([]*models.DeploymentResponse, error)
}

type TraceToolsetHandler interface {
	ListTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (*models.TraceOverviewResponse, error)
	ExportTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (*models.TraceExportResponse, error)
	GetTraceDetails(ctx context.Context, orgName string, projectName string, agentName string, traceID string, environment string) (*models.TraceResponse, error)
}

type RuntimeLogToolsetHandler interface {
	GetRuntimeLogs(ctx context.Context, orgName string, projectName string, agentName string, payload spec.LogFilterRequest) (*models.LogsResponse, error)
}