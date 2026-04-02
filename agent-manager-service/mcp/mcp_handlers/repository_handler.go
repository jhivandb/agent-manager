package mcp_handlers

// import (
// 	"context"

// 	"github.com/wso2/ai-agent-management-platform/agent-manager-service/clients/gitprovider"
// 	"github.com/wso2/ai-agent-management-platform/agent-manager-service/services"
// 	"github.com/wso2/ai-agent-management-platform/agent-manager-service/spec"
// )

// // RepositoryHandler bridges MCP repository tools to the repository service.
// type RepositoryHandler struct {
// 	repoSvc services.RepositoryService
// }

// func NewRepositoryHandler(repoSvc services.RepositoryService) *RepositoryHandler {
// 	return &RepositoryHandler{repoSvc: repoSvc}
// }

// func (h *RepositoryHandler) ListCommits(ctx context.Context, req spec.ListCommitsRequest, limit int, offset int) (*spec.ListCommitsResponse, error) {
// 	return h.repoSvc.ListCommits(ctx, req, gitprovider.ProviderGitHub, limit, offset)
// }
