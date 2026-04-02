package tools

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type listEvaluatorsInput struct {
	OrgName  string   `json:"org_name"`
	Limit    *int     `json:"limit,omitempty"`
	Offset   *int     `json:"offset,omitempty"`
	Search   string   `json:"search,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Source   string   `json:"source,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

type listEvaluatorItem struct {
	Identifier  string   `json:"identifier"`
	// DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	// Version     string   `json:"version"`
	// Provider    string   `json:"provider"`
	Level       string   `json:"level"`
	// Tags        []string `json:"tags"`
	IsBuiltin   bool     `json:"is_builtin"`
	Type        string   `json:"type,omitempty"`
}

type listEvaluatorsOutput struct {
	OrgName    string              `json:"org_name"`
	Evaluators []listEvaluatorItem `json:"evaluators"`
	Total      int32               `json:"total"`
	Limit      int32               `json:"limit"`
	Offset     int32               `json:"offset"`
}

type getEvaluatorInput struct {
	OrgName     string `json:"org_name"`
	EvaluatorID string `json:"evaluator_id"`
}

type listEvaluatorLLMProvidersInput struct {
	OrgName string `json:"org_name"`
}

type createCustomEvaluatorInput struct {
	OrgName      string                        `json:"org_name"`
	Identifier   string                        `json:"identifier,omitempty"`
	DisplayName  string                        `json:"display_name"`
	Description  string                        `json:"description,omitempty"`
	Type         string                        `json:"type"`
	Level        string                        `json:"level"`
	Source       string                        `json:"source"`
	ConfigSchema []models.EvaluatorConfigParam `json:"config_schema,omitempty"`
	Tags         []string                      `json:"tags,omitempty"`
}

type updateCustomEvaluatorInput struct {
	OrgName      string                         `json:"org_name"`
	Identifier   string                         `json:"identifier"`
	DisplayName  *string                        `json:"display_name,omitempty"`
	Description  *string                        `json:"description,omitempty"`
	Source       *string                        `json:"source,omitempty"`
	ConfigSchema *[]models.EvaluatorConfigParam `json:"config_schema,omitempty"`
	Tags         *[]string                      `json:"tags,omitempty"`
}

type evaluatorDetail struct {
	Identifier   string                        `json:"identifier"`
	DisplayName  string                        `json:"display_name"`
	Description  string                        `json:"description"`
	Version      string                        `json:"version"`
	Provider     string                        `json:"provider"`
	Level        string                        `json:"level"`
	Tags         []string                      `json:"tags"`
	IsBuiltin    bool                          `json:"is_builtin"`
	ConfigSchema []models.EvaluatorConfigParam `json:"config_schema"`
	Type         string                        `json:"type,omitempty"`
	Source       string                        `json:"source,omitempty"`
}

type getEvaluatorOutput struct {
	OrgName   string          `json:"org_name"`
	Evaluator evaluatorDetail `json:"evaluator"`
}

func (t *Toolsets) registerEvaluatorTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_evaluators",
		Description: "List available evaluators (both built-in and custom) for an organization.",
		InputSchema: createSchema(map[string]any{
			"org_name": stringProperty("Optional. Organization name."),
			"limit":    intProperty(fmt.Sprintf("Optional. Max evaluators to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
			"offset":   intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
			"search":   stringProperty("Optional. Filter evaluators by a search term."),
			"provider": stringProperty("Optional. Filter by evaluator provider."),
			"source":   stringProperty("Optional. Filter by source: all, builtin, custom."),
			"tags":     arrayProperty("Optional. Filter evaluators by tags.", stringProperty("Tag value.")),
		}, nil),
	}, withToolLogging("list_evaluators", listEvaluators(t.EvaluatorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_evaluator",
		Description: "Get more details about a specific evaluator.",
		InputSchema: createSchema(map[string]any{
			"org_name":     stringProperty("Optional. Organization name."),
			"evaluator_id": stringProperty("Required. Evaluator identifier."),
		}, []string{"evaluator_id"}),
	}, withToolLogging("get_evaluator", getEvaluator(t.EvaluatorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_evaluator_llm_providers",
		Description: "List supported LLM providers for evaluator jobs (credentials and available models).",
		InputSchema: createSchema(map[string]any{
			"org_name": stringProperty("Optional. Organization name."),
		}, nil),
	}, withToolLogging("list_evaluator_llm_providers", listEvaluatorLLMProviders(t.EvaluatorToolset, t.DefaultOrg)))

	configSchemaItem := createSchema(map[string]any{
		"key":         stringProperty("Required. Config key."),
		"type":        stringProperty("Required. Value type (string, integer, float, boolean, array, enum)."),
		"description": stringProperty("Optional. Description."),
		"required":    boolProperty("Optional. Whether this field is required."),
		"default":     map[string]any{"description": "Optional default value."},
		"min":         map[string]any{"type": "number", "description": "Optional min value."},
		"max":         map[string]any{"type": "number", "description": "Optional max value."},
		"enum_values": arrayProperty("Optional. Allowed values for enum.", stringProperty("Enum value.")),
	}, []string{"key", "type"})

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "create_custom_evaluator",
		Description: "Create a custom evaluator (code or llm_judge).",
		InputSchema: createSchema(map[string]any{
			"org_name":      stringProperty("Optional. Organization name."),
			"identifier":    stringProperty("Optional. Custom evaluator identifier (slug)."),
			"display_name":  stringProperty("Required. Human-readable display name."),
			"description":   stringProperty("Optional. Description."),
			"type":          enumProperty("Required. Evaluator type.", []string{"code", "llm_judge"}),
			"level":         enumProperty("Required. Evaluation level.", []string{"trace", "agent", "llm"}),
			"source":        stringProperty("Required. Source code or prompt template."),
			"config_schema": arrayProperty("Optional. Evaluator configuration schema.", configSchemaItem),
			"tags":          arrayProperty("Optional. Tags for search and grouping.", stringProperty("Tag value.")),
		}, []string{"display_name", "type", "level", "source"}),
	}, withToolLogging("create_custom_evaluator", createCustomEvaluator(t.EvaluatorToolset, t.DefaultOrg)))

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "update_custom_evaluator",
		Description: "Update an existing custom evaluator.",
		InputSchema: createSchema(map[string]any{
			"org_name":      stringProperty("Optional. Organization name."),
			"identifier":    stringProperty("Required. Custom evaluator identifier."),
			"display_name":  stringProperty("Optional. Human-readable display name."),
			"description":   stringProperty("Optional. Description."),
			"source":        stringProperty("Optional. Updated source code or prompt template."),
			"config_schema": arrayProperty("Optional. Updated evaluator configuration schema.", configSchemaItem),
			"tags":          arrayProperty("Optional. Updated tags list.", stringProperty("Tag value.")),
		}, []string{"identifier"}),
	}, withToolLogging("update_custom_evaluator", updateCustomEvaluator(t.EvaluatorToolset, t.DefaultOrg)))
}

func listEvaluators(handler EvaluatorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listEvaluatorsInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listEvaluatorsInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		limit := utils.DefaultLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		if limit < utils.MinLimit || limit > utils.MaxLimit {
			return nil, nil, fmt.Errorf("limit must be between %d and %d", utils.MinLimit, utils.MaxLimit)
		}

		offset := utils.DefaultOffset
		if input.Offset != nil {
			offset = *input.Offset
		}
		if offset < utils.MinOffset {
			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
		}

		evaluators, total, err := handler.ListEvaluators(ctx, orgName, int32(limit), int32(offset), input.Search, input.Provider, input.Source, input.Tags)
		if err != nil {
			return nil, nil, wrapToolError("list_evaluators", err)
		}

		formatted := make([]listEvaluatorItem, 0, len(evaluators))
		for _, evaluator := range evaluators {
			if evaluator == nil {
				continue
			}
			formatted = append(formatted, listEvaluatorItem{
				Identifier:  evaluator.Identifier,
				// DisplayName: evaluator.DisplayName,
				Description: evaluator.Description,
				// Version:     evaluator.Version,
				// Provider:    evaluator.Provider,
				Level:       evaluator.Level,
				// Tags:        evaluator.Tags,
				IsBuiltin:   evaluator.IsBuiltin,
				Type:        evaluator.Type,
			})
		}

		response := listEvaluatorsOutput{
			OrgName:    orgName,
			Evaluators: formatted,
			Total:      total,
			Limit:      int32(limit),
			Offset:     int32(offset),
		}
		return handleToolResult(response, nil)
	}
}

func getEvaluator(handler EvaluatorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, getEvaluatorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input getEvaluatorInput) (*gomcp.CallToolResult, any, error) {
		if input.EvaluatorID == "" {
			return nil, nil, fmt.Errorf("evaluator_id is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		evaluator, err := handler.GetEvaluator(ctx, orgName, input.EvaluatorID)
		if err != nil {
			return nil, nil, wrapToolError("get_evaluator", err)
		}

		response := getEvaluatorOutput{
			OrgName:   orgName,
			Evaluator: formatEvaluatorDetail(evaluator),
		}
		return handleToolResult(response, nil)
	}
}

func listEvaluatorLLMProviders(handler EvaluatorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, listEvaluatorLLMProvidersInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listEvaluatorLLMProvidersInput) (*gomcp.CallToolResult, any, error) {
		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		list, err := handler.ListLLMProviders(ctx, orgName)
		if err != nil {
			return nil, nil, wrapToolError("list_evaluator_llm_providers", err)
		}

		response := spec.EvaluatorLLMProviderListResponse{
			Count: int32(len(list)),
			List:  list,
		}
		return handleToolResult(response, nil)
	}
}

func createCustomEvaluator(handler EvaluatorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, createCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input createCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
		if input.DisplayName == "" {
			return nil, nil, fmt.Errorf("display_name is required")
		}
		if input.Type == "" {
			return nil, nil, fmt.Errorf("type is required")
		}
		if input.Level == "" {
			return nil, nil, fmt.Errorf("level is required")
		}
		if input.Type != "code" && input.Type != "llm_judge" {
			return nil, nil, fmt.Errorf("type must be one of: code, llm_judge")
		}
		if input.Level != "trace" && input.Level != "agent" && input.Level != "llm" {
			return nil, nil, fmt.Errorf("level must be one of: trace, agent, llm")
		}
		if input.Source == "" {
			return nil, nil, fmt.Errorf("source is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		req := &models.CreateCustomEvaluatorRequest{
			Identifier:   input.Identifier,
			DisplayName:  input.DisplayName,
			Description:  input.Description,
			Type:         input.Type,
			Level:        input.Level,
			Source:       input.Source,
			ConfigSchema: input.ConfigSchema,
			Tags:         input.Tags,
		}

		evaluator, err := handler.CreateCustomEvaluator(ctx, orgName, req)
		if err != nil {
			return nil, nil, wrapToolError("create_custom_evaluator", err)
		}

		response := getEvaluatorOutput{
			OrgName:   orgName,
			Evaluator: formatEvaluatorDetail(evaluator),
		}
		return handleToolResult(response, nil)
	}
}

func updateCustomEvaluator(handler EvaluatorToolsetHandler, defaultOrg string) func(context.Context, *gomcp.CallToolRequest, updateCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input updateCustomEvaluatorInput) (*gomcp.CallToolResult, any, error) {
		if input.Identifier == "" {
			return nil, nil, fmt.Errorf("identifier is required")
		}

		orgName := resolveOrgName(defaultOrg, input.OrgName)
		if orgName == "" {
			return nil, nil, fmt.Errorf("org_name is required")
		}

		req := &models.UpdateCustomEvaluatorRequest{
			DisplayName:  input.DisplayName,
			Description:  input.Description,
			Source:       input.Source,
			ConfigSchema: input.ConfigSchema,
			Tags:         input.Tags,
		}

		evaluator, err := handler.UpdateCustomEvaluator(ctx, orgName, input.Identifier, req)
		if err != nil {
			return nil, nil, wrapToolError("update_custom_evaluator", err)
		}

		response := getEvaluatorOutput{
			OrgName:   orgName,
			Evaluator: formatEvaluatorDetail(evaluator),
		}
		return handleToolResult(response, nil)
	}
}

func formatEvaluatorDetail(evaluator *models.EvaluatorResponse) evaluatorDetail {
	if evaluator == nil {
		return evaluatorDetail{}
	}
	return evaluatorDetail{
		Identifier:   evaluator.Identifier,
		DisplayName:  evaluator.DisplayName,
		Description:  evaluator.Description,
		Version:      evaluator.Version,
		Provider:     evaluator.Provider,
		Level:        evaluator.Level,
		Tags:         evaluator.Tags,
		IsBuiltin:    evaluator.IsBuiltin,
		ConfigSchema: evaluator.ConfigSchema,
		Type:         evaluator.Type,
		Source:       evaluator.Source,
	}
}
