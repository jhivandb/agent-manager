package mcp_handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/catalog"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
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

// MonitorHandler bridges MCP monitor tools to the monitor service layer.
type MonitorHandler struct {
	monitorSvc services.MonitorManagerService
}

func NewMonitorHandler(monitorSvc services.MonitorManagerService) *MonitorHandler {
	return &MonitorHandler{monitorSvc: monitorSvc}
}

func (h *MonitorHandler) CreateMonitor(ctx context.Context, orgName string, req *models.CreateMonitorRequest) (*models.MonitorResponse, error) {
	return h.monitorSvc.CreateMonitor(ctx, orgName, req)
}

func (h *MonitorHandler) GetMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string) (*models.MonitorResponse, error) {
	return h.monitorSvc.GetMonitor(ctx, orgName, projectName, agentName, monitorName)
}

func (h *MonitorHandler) ListMonitors(ctx context.Context, orgName, projectName, agentName string) (*models.MonitorListResponse, error) {
	return h.monitorSvc.ListMonitors(ctx, orgName, projectName, agentName)
}

func (h *MonitorHandler) UpdateMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string, req *models.UpdateMonitorRequest) (*models.MonitorResponse, error) {
	return h.monitorSvc.UpdateMonitor(ctx, orgName, projectName, agentName, monitorName, req)
}

func (h *MonitorHandler) StopMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string) (*models.MonitorResponse, error) {
	return h.monitorSvc.StopMonitor(ctx, orgName, projectName, agentName, monitorName)
}

func (h *MonitorHandler) StartMonitor(ctx context.Context, orgName, projectName, agentName, monitorName string) (*models.MonitorResponse, error) {
	return h.monitorSvc.StartMonitor(ctx, orgName, projectName, agentName, monitorName)
}

func (h *MonitorHandler) ListMonitorRuns(ctx context.Context, orgName, projectName, agentName, monitorName string, limit, offset int, includeScores bool) (*models.MonitorRunsListResponse, error) {
	return h.monitorSvc.ListMonitorRuns(ctx, orgName, projectName, agentName, monitorName, limit, offset, includeScores)
}

func (h *MonitorHandler) RerunMonitor(ctx context.Context, orgName, projectName, agentName, monitorName, runID string) (*models.MonitorRunResponse, error) {
	return h.monitorSvc.RerunMonitor(ctx, orgName, projectName, agentName, monitorName, runID)
}

func (h *MonitorHandler) GetMonitorRunLogs(ctx context.Context, orgName, projectName, agentName, monitorName, runID string) (*models.LogsResponse, error) {
	return h.monitorSvc.GetMonitorRunLogs(ctx, orgName, projectName, agentName, monitorName, runID)
}

// MonitorScoresHandler bridges MCP score tools to the score service layer.
type MonitorScoresHandler struct {
	scoresSvc *services.MonitorScoresService
}

func NewMonitorScoresHandler(scoresSvc *services.MonitorScoresService) *MonitorScoresHandler {
	return &MonitorScoresHandler{scoresSvc: scoresSvc}
}

func (h *MonitorScoresHandler) GetMonitorScores(ctx context.Context, orgName, projectName, agentName, monitorName string, startTime, endTime time.Time, evaluator string, level string) (*models.MonitorScoresResponse, error) {
	monitorID, err := h.scoresSvc.GetMonitorID(orgName, projectName, agentName, monitorName)
	if err != nil {
		return nil, err
	}
	filters := repositories.ScoreFilters{
		EvaluatorName: evaluator,
		Level:         level,
	}
	return h.scoresSvc.GetMonitorScores(monitorID, monitorName, startTime, endTime, filters)
}

func (h *MonitorScoresHandler) GetMonitorRunScores(ctx context.Context, orgName, projectName, agentName, monitorName, runID string) (*models.MonitorRunScoresResponse, error) {
	monitorID, err := h.scoresSvc.GetMonitorID(orgName, projectName, agentName, monitorName)
	if err != nil {
		return nil, err
	}
	runUUID, err := uuid.Parse(runID)
	if err != nil {
		return nil, fmt.Errorf("invalid run_id format: %w", err)
	}
	return h.scoresSvc.GetMonitorRunScores(monitorID, runUUID, monitorName)
}

func (h *MonitorScoresHandler) GetMonitorScoresTimeSeries(ctx context.Context, orgName, projectName, agentName, monitorName string, startTime, endTime time.Time, evaluators []string) (*models.BatchTimeSeriesResponse, error) {
	monitorID, err := h.scoresSvc.GetMonitorID(orgName, projectName, agentName, monitorName)
	if err != nil {
		return nil, err
	}
	return h.scoresSvc.GetEvaluatorsTimeSeries(monitorID, monitorName, evaluators, startTime, endTime)
}

func (h *MonitorScoresHandler) GetGroupedScores(ctx context.Context, orgName, projectName, agentName, monitorName string, startTime, endTime time.Time, level string) (*models.GroupedScoresResponse, error) {
	monitorID, err := h.scoresSvc.GetMonitorID(orgName, projectName, agentName, monitorName)
	if err != nil {
		return nil, err
	}
	return h.scoresSvc.GetGroupedScores(monitorID, monitorName, startTime, endTime, level)
}

func (h *MonitorScoresHandler) GetAgentTraceScores(ctx context.Context, orgName, projectName, agentName string, startTime, endTime time.Time, limit, offset int) (*models.AgentTraceScoresResponse, error) {
	return h.scoresSvc.GetAgentTraceScores(orgName, projectName, agentName, startTime, endTime, limit, offset)
}

func (h *MonitorScoresHandler) GetTraceScores(ctx context.Context, orgName, projectName, agentName, traceID string) (*models.TraceScoresResponse, error) {
	return h.scoresSvc.GetTraceScores(traceID, orgName, projectName, agentName)
}
