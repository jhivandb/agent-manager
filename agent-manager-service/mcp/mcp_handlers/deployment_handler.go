package mcp_handlers

import (
	"context"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
)

// DeploymentHandler bridges MCP deployment tools to the agent manager service layer.
type DeploymentHandler struct {
	agentSvc services.AgentManagerService
}

func NewDeploymentHandler(agentSvc services.AgentManagerService) *DeploymentHandler {
	return &DeploymentHandler{agentSvc: agentSvc}
}

func (h *DeploymentHandler) GetAgentDeployments(ctx context.Context, orgName string, projectName string, agentName string) ([]*models.DeploymentResponse, error) {
	return h.agentSvc.GetAgentDeployments(ctx, orgName, projectName, agentName)

}