package mcp_handlers

import (
	"context"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

// ObserverHandler bridges MCP observer tools (logs/metrics) to the agent manager service layer.
type ObserverHandler struct {
	agentSvc services.AgentManagerService
}

func NewObserverHandler(agentSvc services.AgentManagerService) *ObserverHandler {
	return &ObserverHandler{agentSvc: agentSvc}
}

func (h *ObserverHandler) GetRuntimeLogs(ctx context.Context, orgName string, projectName string, agentName string, payload spec.LogFilterRequest) (*models.LogsResponse, error) {
	return h.agentSvc.GetAgentRuntimeLogs(ctx, orgName, projectName, agentName, payload)
}
