package mcp_handlers

import (
	"context"

	"github.com/wso2/ai-agent-management-platform/agent-manager-service/models"
	"github.com/wso2/ai-agent-management-platform/agent-manager-service/services"
)

// TraceHandler bridges MCP trace tools to the observability service layer.
type TraceHandler struct {
	observabilitySvc services.ObservabilityManagerService
}

func NewTraceHandler(observabilitySvc services.ObservabilityManagerService) *TraceHandler {
	return &TraceHandler{observabilitySvc: observabilitySvc}
}

func (h *TraceHandler) ListTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (*models.TraceOverviewResponse, error) {
	req := services.ListTracesRequest{
		OrgName:     orgName,
		ProjectName: projectName,
		AgentName:   agentName,
		Environment: environment,
		StartTime:   startTime,
		EndTime:     endTime,
		Limit:       limit,
		Offset:      offset,
		SortOrder:   sortOrder,
	}
	return h.observabilitySvc.ListTraces(ctx, req)
}

func (h *TraceHandler) ExportTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (*models.TraceExportResponse, error) {
	req := services.ListTracesRequest{
		OrgName:     orgName,
		ProjectName: projectName,
		AgentName:   agentName,
		Environment: environment,
		StartTime:   startTime,
		EndTime:     endTime,
		Limit:       limit,
		Offset:      offset,
		SortOrder:   sortOrder,
	}
	return h.observabilitySvc.ExportTraces(ctx, req)
}

func (h *TraceHandler) GetTraceDetails(ctx context.Context, orgName string, projectName string, agentName string, traceID string, environment string) (*models.TraceResponse, error) {
	req := services.TraceDetailsRequest{
		TraceID:     traceID,
		OrgName:     orgName,
		ProjectName: projectName,
		AgentName:   agentName,
		Environment: environment,
	}
	return h.observabilitySvc.GetTraceDetails(ctx, req)
}