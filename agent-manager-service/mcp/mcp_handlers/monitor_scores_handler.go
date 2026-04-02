package mcp_handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/ai-agent-management-platform/agent-manager-service/models"
	"github.com/wso2/ai-agent-management-platform/agent-manager-service/repositories"
	"github.com/wso2/ai-agent-management-platform/agent-manager-service/services"
)

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
