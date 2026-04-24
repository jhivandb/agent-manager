package mcp_handlers

import (
	"context"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
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

func (h *ProjectHandler) ListOrganizations(ctx context.Context, limit int, offset int) ([]*models.OrganizationResponse, int32, error) {
	return h.infraSvc.ListOrganizations(ctx, limit, offset)
}

func (h *ProjectHandler) CreateProject(ctx context.Context, orgName string, payload spec.CreateProjectRequest) (*models.ProjectResponse, error) {
	return h.infraSvc.CreateProject(ctx, orgName, payload)
}

func (h *ProjectHandler) ListEnvironments(ctx context.Context, orgName string) ([]*models.EnvironmentResponse, error) {
	return h.infraSvc.ListOrgEnvironments(ctx, orgName)
}
