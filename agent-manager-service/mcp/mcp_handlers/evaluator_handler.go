package mcp_handlers

import (
	"context"

	"github.com/wso2/agent-manager/agent-manager-service/catalog"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

// EvaluatorHandler bridges MCP evaluator tools to the evaluator service layer.
type EvaluatorHandler struct {
	evaluatorSvc services.EvaluatorManagerService
}

func NewEvaluatorHandler(evaluatorSvc services.EvaluatorManagerService) *EvaluatorHandler {
	return &EvaluatorHandler{evaluatorSvc: evaluatorSvc}
}

func (h *EvaluatorHandler) ListEvaluators(ctx context.Context, orgName string, limit int32, offset int32, search string, provider string, source string, tags []string) ([]*models.EvaluatorResponse, int32, error) {
	filters := services.EvaluatorFilters{
		Limit:    limit,
		Offset:   offset,
		Tags:     tags,
		Search:   search,
		Provider: provider,
		Source:   source,
	}
	return h.evaluatorSvc.ListEvaluators(ctx, orgName, filters)
}

func (h *EvaluatorHandler) GetEvaluator(ctx context.Context, orgName string, evaluatorID string) (*models.EvaluatorResponse, error) {
	return h.evaluatorSvc.GetEvaluator(ctx, orgName, evaluatorID)
}

func (h *EvaluatorHandler) ListLLMProviders(_ context.Context, _ string) ([]spec.EvaluatorLLMProvider, error) {
	providers := catalog.AllProviders()
	list := make([]spec.EvaluatorLLMProvider, 0, len(providers))
	for _, p := range providers {
		fields := make([]spec.LLMConfigField, 0, len(p.ConfigFields))
		for _, f := range p.ConfigFields {
			fields = append(fields, spec.LLMConfigField{
				Key:       f.Key,
				Label:     f.Label,
				FieldType: f.FieldType,
				Required:  f.Required,
				EnvVar:    f.EnvVar,
			})
		}
		list = append(list, spec.EvaluatorLLMProvider{
			Name:         p.Name,
			DisplayName:  p.DisplayName,
			ConfigFields: fields,
			Models:       p.Models,
		})
	}
	return list, nil
}

func (h *EvaluatorHandler) CreateCustomEvaluator(ctx context.Context, orgName string, req *models.CreateCustomEvaluatorRequest) (*models.EvaluatorResponse, error) {
	return h.evaluatorSvc.CreateCustomEvaluator(ctx, orgName, req)
}

func (h *EvaluatorHandler) UpdateCustomEvaluator(ctx context.Context, orgName string, identifier string, req *models.UpdateCustomEvaluatorRequest) (*models.EvaluatorResponse, error) {
	return h.evaluatorSvc.UpdateCustomEvaluator(ctx, orgName, identifier, req)
}
