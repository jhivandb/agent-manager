
package mcp_handlers

import (
	"context"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

// RuntimeLogHandler bridges MCP runtime log tools to the agent manager service layer.
type RuntimeLogHandler struct {
	agentSvc services.AgentManagerService
}

func NewRuntimeLogHandler(agentSvc services.AgentManagerService) *RuntimeLogHandler {
	return &RuntimeLogHandler{agentSvc: agentSvc}
}

func (h *RuntimeLogHandler) GetRuntimeLogs(ctx context.Context, orgName string, projectName string, agentName string, payload spec.LogFilterRequest) (*models.LogsResponse, error) {
	return h.agentSvc.GetAgentRuntimeLogs(ctx, orgName, projectName, agentName, payload)
}