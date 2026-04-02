package mcp_handlers

import (
	"context"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
)

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
