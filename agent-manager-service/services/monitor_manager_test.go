// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// UNIT tests for monitorManagerService.GetMonitorRunLogs — the rewire off
// the legacy observability client onto observersvc.ObserverSvcClient
// (issue: agent-manager-observer serves build-logs itself now, so the
// namespace lookup via ocClient.NamespaceFor is gone; the observer resolves
// org->namespace on its own). No build tag, runs in the fast unit tier with
// every collaborator mocked.
package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// errUnreachable is a sentinel returned by test doubles whose call path is
// expected to be dead code (guarded by an earlier t.Fatal). Returning it
// instead of a bare nil keeps nilnil satisfied while still failing loudly if
// the call ever actually happens.
var errUnreachable = errors.New("unreachable: mock should not have been called")

func TestGetMonitorRunLogs_PassesOrgHandleAndRunNameToObserverClient(t *testing.T) {
	const ouID, orgHandle, projectName, agentName, monitorName = "org-1-uuid", "org-1-handle", "proj-1", "agent-1", "monitor-1"
	monitorID := uuid.New()
	runID := uuid.New()
	monitor := &models.Monitor{ID: monitorID, Name: monitorName, OUID: ouID, ProjectName: projectName, AgentName: agentName}
	run := &models.MonitorRun{ID: runID, MonitorID: monitorID, Name: "workflowrun-abc123"}

	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByNameFunc: func(gotOU, gotProj, gotAgent, gotName string) (*models.Monitor, error) {
			if gotOU != ouID || gotProj != projectName || gotAgent != agentName || gotName != monitorName {
				t.Errorf("GetMonitorByName got (%s,%s,%s,%s)", gotOU, gotProj, gotAgent, gotName)
			}
			return monitor, nil
		},
		GetMonitorRunByIDFunc: func(gotRunID, gotMonitorID uuid.UUID) (*models.MonitorRun, error) {
			if gotRunID != runID || gotMonitorID != monitorID {
				t.Errorf("GetMonitorRunByID got (%s,%s)", gotRunID, gotMonitorID)
			}
			return run, nil
		},
	}

	wantLogs := &models.LogsResponse{Logs: []models.LogEntry{{Log: "hello"}}, TotalCount: 1}
	var gotOrg, gotWorkflowRunName string
	observerClient := &clientmocks.ObserverSvcClientMock{
		GetWorkflowRunLogsFunc: func(_ context.Context, organization, workflowRunName string) (*models.LogsResponse, error) {
			gotOrg = organization
			gotWorkflowRunName = workflowRunName
			return wantLogs, nil
		},
	}

	s := &monitorManagerService{
		logger:         discardLogger(),
		monitorRepo:    monitorRepo,
		observerClient: observerClient,
	}

	got, err := s.GetMonitorRunLogs(context.Background(), ouID, orgHandle, projectName, agentName, monitorName, runID.String())
	if err != nil {
		t.Fatalf("GetMonitorRunLogs returned error: %v", err)
	}
	if gotOrg != orgHandle {
		t.Errorf("GetWorkflowRunLogs organization = %q, want orgHandle %q (matches every other observer consumer, not the OU UUID)", gotOrg, orgHandle)
	}
	if gotWorkflowRunName != run.Name {
		t.Errorf("GetWorkflowRunLogs workflowRunName = %q, want run.Name %q", gotWorkflowRunName, run.Name)
	}
	if got != wantLogs {
		t.Errorf("GetMonitorRunLogs returned %+v, want the observer client's response passed through", got)
	}
}

func TestGetMonitorRunLogs_MonitorNotFoundMapsToErrMonitorNotFound(t *testing.T) {
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByNameFunc: func(_, _, _, _ string) (*models.Monitor, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	observerClient := &clientmocks.ObserverSvcClientMock{
		GetWorkflowRunLogsFunc: func(_ context.Context, _, _ string) (*models.LogsResponse, error) {
			t.Fatal("GetWorkflowRunLogs should not be called when the monitor lookup fails")
			return nil, errUnreachable
		},
	}
	s := &monitorManagerService{logger: discardLogger(), monitorRepo: monitorRepo, observerClient: observerClient}

	_, err := s.GetMonitorRunLogs(context.Background(), "org", "org-handle", "proj", "agent", "monitor", uuid.New().String())
	if !errors.Is(err, utils.ErrMonitorNotFound) {
		t.Fatalf("expected ErrMonitorNotFound, got %v", err)
	}
}

func TestGetMonitorRunLogs_RunNotFoundMapsToErrMonitorRunNotFound(t *testing.T) {
	monitor := &models.Monitor{ID: uuid.New(), Name: "monitor"}
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByNameFunc: func(_, _, _, _ string) (*models.Monitor, error) {
			return monitor, nil
		},
		GetMonitorRunByIDFunc: func(_, _ uuid.UUID) (*models.MonitorRun, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	observerClient := &clientmocks.ObserverSvcClientMock{
		GetWorkflowRunLogsFunc: func(_ context.Context, _, _ string) (*models.LogsResponse, error) {
			t.Fatal("GetWorkflowRunLogs should not be called when the run lookup fails")
			return nil, errUnreachable
		},
	}
	s := &monitorManagerService{logger: discardLogger(), monitorRepo: monitorRepo, observerClient: observerClient}

	_, err := s.GetMonitorRunLogs(context.Background(), "org", "org-handle", "proj", "agent", "monitor", uuid.New().String())
	if !errors.Is(err, utils.ErrMonitorRunNotFound) {
		t.Fatalf("expected ErrMonitorRunNotFound, got %v", err)
	}
}

func TestGetMonitorRunLogs_ObserverClientErrorIsWrapped(t *testing.T) {
	monitor := &models.Monitor{ID: uuid.New(), Name: "monitor"}
	run := &models.MonitorRun{ID: uuid.New(), MonitorID: monitor.ID, Name: "workflowrun-xyz"}
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByNameFunc: func(_, _, _, _ string) (*models.Monitor, error) {
			return monitor, nil
		},
		GetMonitorRunByIDFunc: func(_, _ uuid.UUID) (*models.MonitorRun, error) {
			return run, nil
		},
	}
	sentinel := errors.New("observer unreachable")
	observerClient := &clientmocks.ObserverSvcClientMock{
		GetWorkflowRunLogsFunc: func(_ context.Context, _, _ string) (*models.LogsResponse, error) {
			return nil, sentinel
		},
	}
	s := &monitorManagerService{logger: discardLogger(), monitorRepo: monitorRepo, observerClient: observerClient}

	_, err := s.GetMonitorRunLogs(context.Background(), "org", "org-handle", "proj", "agent", "monitor", run.ID.String())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
}
