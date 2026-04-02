package mcp_handlers

import (
	"context"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
)

// ProjectHandler bridges MCP project tools to the infra resource manager service.
type ProjectHandler struct {
	infraSvc services.InfraResourceManager
}

func NewProjectHandler(infraSvc services.InfraResourceManager) *ProjectHandler {
	return &ProjectHandler{infraSvc: infraSvc}
}

func (h *ProjectHandler) ListProjects(ctx context.Context, orgName string, limit int, offset int) ([]*models.ProjectResponse, int32, error) {
	return h.infraSvc.ListProjects(ctx, orgName, limit, offset)
}