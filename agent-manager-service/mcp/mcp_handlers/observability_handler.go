package mcp_handlers

import (
	"context"
	"fmt"

	occlient "github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	traceobserversvc "github.com/wso2/agent-manager/agent-manager-service/clients/traceobserversvc"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

// ObserverHandler bridges MCP observer tools (logs/metrics) to the agent manager service layer.
type ObserverHandler struct {
	agentSvc services.AgentManagerService
}

func NewObserverHandler(agentSvc services.AgentManagerService) *ObserverHandler {
	return &ObserverHandler{agentSvc: agentSvc}
}

func (h *ObserverHandler) GetRuntimeLogs(ctx context.Context, orgName string, projectName string, agentName string, payload spec.LogFilterRequest) (*models.LogsResponse, error) {
	return h.agentSvc.GetAgentRuntimeLogs(ctx, orgName, projectName, agentName, payload)
}

// RuntimeLogHandler bridges MCP runtime log tools to the agent manager service layer.
type RuntimeLogHandler struct {
	agentSvc services.AgentManagerService
}

func NewRuntimeLogHandler(agentSvc services.AgentManagerService) *RuntimeLogHandler {
	return &RuntimeLogHandler{agentSvc: agentSvc}
}

func (h *RuntimeLogHandler) GetRuntimeLogs(ctx context.Context, orgName string, projectName string, agentName string, payload spec.LogFilterRequest) (*models.LogsResponse, error) {
	return h.agentSvc.GetAgentRuntimeLogs(ctx, orgName, projectName, agentName, payload)
}

// TraceHandler bridges MCP trace tools to the trace-observer service.
type TraceHandler struct {
	ocClient    occlient.OpenChoreoClient
	traceClient traceobserversvc.TraceObserverClient
}

func NewTraceHandler(ocClient occlient.OpenChoreoClient, traceClient traceobserversvc.TraceObserverClient) *TraceHandler {
	return &TraceHandler{ocClient: ocClient, traceClient: traceClient}
}

func (h *TraceHandler) ListTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (map[string]any, error) {
	componentUid, environmentUid, err := h.resolveComponentEnvironment(ctx, orgName, projectName, agentName, environment)
	if err != nil {
		return nil, err
	}

	params := traceobserversvc.TraceListParams{
		ComponentUid:   componentUid,
		EnvironmentUid: environmentUid,
		StartTime:      startTime,
		EndTime:        endTime,
		Limit:          limit,
		Offset:         offset,
		SortOrder:      sortOrder,
	}

	return h.traceClient.ListTraces(ctx, params)
}

func (h *TraceHandler) ExportTraces(ctx context.Context, orgName string, projectName string, agentName string, environment string, startTime string, endTime string, sortOrder string, limit int, offset int) (map[string]any, error) {
	componentUid, environmentUid, err := h.resolveComponentEnvironment(ctx, orgName, projectName, agentName, environment)
	if err != nil {
		return nil, err
	}

	params := traceobserversvc.TraceListParams{
		ComponentUid:   componentUid,
		EnvironmentUid: environmentUid,
		StartTime:      startTime,
		EndTime:        endTime,
		Limit:          limit,
		Offset:         offset,
		SortOrder:      sortOrder,
	}

	return h.traceClient.ExportTraces(ctx, params)
}

func (h *TraceHandler) GetTraceDetails(ctx context.Context, orgName string, projectName string, agentName string, traceID string, environment string) (map[string]any, error) {
	componentUid, environmentUid, err := h.resolveComponentEnvironment(ctx, orgName, projectName, agentName, environment)
	if err != nil {
		return nil, err
	}

	params := traceobserversvc.TraceDetailsParams{
		TraceID:        traceID,
		ComponentUid:   componentUid,
		EnvironmentUid: environmentUid,
	}

	return h.traceClient.GetTrace(ctx, params)
}

func (h *TraceHandler) resolveComponentEnvironment(ctx context.Context, orgName, projectName, agentName, environment string) (string, string, error) {
	if h == nil || h.ocClient == nil {
		return "", "", fmt.Errorf("openchoreo client is not configured")
	}
	if h.traceClient == nil {
		return "", "", fmt.Errorf("trace observer client is not configured")
	}

	// Validate org and project exist
	if _, err := h.ocClient.GetOrganization(ctx, orgName); err != nil {
		return "", "", err
	}
	if _, err := h.ocClient.GetProject(ctx, orgName, projectName); err != nil {
		return "", "", err
	}

	// Resolve component UID
	agent, err := h.ocClient.GetComponent(ctx, orgName, projectName, agentName)
	if err != nil {
		return "", "", err
	}

	// Resolve environment UID
	env, err := h.ocClient.GetEnvironment(ctx, orgName, environment)
	if err != nil {
		return "", "", err
	}

	return agent.UUID, env.UUID, nil
}
