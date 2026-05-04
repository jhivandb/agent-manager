// Copyright (c) 2025, WSO2 LLC. (https://www.wso2.com).
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

package tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/jwtassertion"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/tests/apitestutils"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
	"github.com/wso2/agent-manager/agent-manager-service/wiring"
)

var (
	testDeleteOrgName     = fmt.Sprintf("test-org-%s", uuid.New().String()[:5])
	testDeleteProjName    = fmt.Sprintf("test-project-%s", uuid.New().String()[:5])
	testDeleteAgentName   = fmt.Sprintf("test-agent-%s", uuid.New().String()[:5])
	testExternalAgentName = fmt.Sprintf("test-external-%s", uuid.New().String()[:5])
	testFailingAgentName  = fmt.Sprintf("failing-agent-%s", uuid.New().String()[:5])
)

func TestDeleteAgent(t *testing.T) {
	authMiddleware := jwtassertion.NewMockMiddleware(t)

	t.Run("Deleting an internal agent should return 204", func(t *testing.T) {
		openChoreoClient := apitestutils.CreateMockOpenChoreoClient()
		secretMgmtClient := apitestutils.CreateMockSecretManagementClient()
		testClients := wiring.TestClients{
			OpenChoreoClient: openChoreoClient,
			SecretMgmtClient: secretMgmtClient,
		}

		app := apitestutils.MakeAppClientWithDeps(t, testClients, authMiddleware)

		// Send the delete request
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s", testDeleteOrgName, testDeleteProjName, testDeleteAgentName)
		req := httptest.NewRequest(http.MethodDelete, url, nil)

		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)

		// Assert response
		require.Equal(t, http.StatusNoContent, rr.Code)

		// Validate service calls
		require.Len(t, openChoreoClient.DeleteComponentCalls(), 1)

		// Validate call parameters
		deleteCall := openChoreoClient.DeleteComponentCalls()[0]
		require.Equal(t, testDeleteOrgName, deleteCall.NamespaceName)
		require.Equal(t, testDeleteProjName, deleteCall.ProjectName)
		require.Equal(t, testDeleteAgentName, deleteCall.ComponentName)
	})

	t.Run("Deleting an external agent should return 204", func(t *testing.T) {
		openChoreoClient := apitestutils.CreateMockOpenChoreoClient()
		secretMgmtClient := apitestutils.CreateMockSecretManagementClient()
		testClients := wiring.TestClients{
			OpenChoreoClient: openChoreoClient,
			SecretMgmtClient: secretMgmtClient,
		}

		app := apitestutils.MakeAppClientWithDeps(t, testClients, authMiddleware)

		// Send the delete request
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s", testDeleteOrgName, testDeleteProjName, testExternalAgentName)
		req := httptest.NewRequest(http.MethodDelete, url, nil)

		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)

		// Assert response
		require.Equal(t, http.StatusNoContent, rr.Code)

		// Validate that DeleteAgentComponent was NOT called for external agents
		require.Len(t, openChoreoClient.DeleteComponentCalls(), 1)
	})

	validationTests := []struct {
		name           string
		authMiddleware jwtassertion.Middleware
		wantStatus     int
		wantErrMsg     string
		url            string
		setupMock      func() *clientmocks.OpenChoreoClientMock
	}{
		{
			name:           "return 404 on organization not found",
			authMiddleware: authMiddleware,
			wantStatus:     404,
			wantErrMsg:     "Organization not found",
			url:            fmt.Sprintf("/api/v1/orgs/nonexistent-org/projects/%s/agents/%s", testDeleteProjName, testDeleteAgentName),
			setupMock: func() *clientmocks.OpenChoreoClientMock {
				return apitestutils.CreateMockOpenChoreoClient()
			},
		},
		{
			name:           "return 404 on project not found",
			authMiddleware: authMiddleware,
			wantStatus:     404,
			wantErrMsg:     "Project not found",
			url:            fmt.Sprintf("/api/v1/orgs/%s/projects/nonexistent-project/agents/%s", testDeleteOrgName, testDeleteAgentName),
			setupMock: func() *clientmocks.OpenChoreoClientMock {
				mock := apitestutils.CreateMockOpenChoreoClient()
				mock.DeleteComponentFunc = func(ctx context.Context, namespaceName string, projectName string, componentName string) error {
					if projectName == "nonexistent-project" {
						return utils.ErrProjectNotFound
					}
					return nil
				}
				return mock
			},
		},
		{
			name: "return 401 on missing authentication",
			authMiddleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					utils.WriteErrorResponse(w, http.StatusUnauthorized, "missing header: Authorization")
				})
			},
			wantStatus: 401,
			wantErrMsg: "missing header: Authorization",
			url:        fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s", testDeleteOrgName, testDeleteProjName, testDeleteAgentName),
			setupMock: func() *clientmocks.OpenChoreoClientMock {
				return apitestutils.CreateMockOpenChoreoClient()
			},
		},
		{
			name:           "return 500 on OpenChoreo delete failure for internal agent",
			authMiddleware: authMiddleware,
			wantStatus:     500,
			wantErrMsg:     "Failed to delete agent",
			url:            fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s", testDeleteOrgName, testDeleteProjName, testFailingAgentName),
			setupMock: func() *clientmocks.OpenChoreoClientMock {
				mock := apitestutils.CreateMockOpenChoreoClient()
				mock.DeleteComponentFunc = func(ctx context.Context, orgName string, projName string, agentName string) error {
					return fmt.Errorf("OpenChoreo service error")
				}
				return mock
			},
		},
	}

	for _, tt := range validationTests {
		t.Run(tt.name, func(t *testing.T) {
			openChoreoClient := tt.setupMock()
			secretMgmtClient := apitestutils.CreateMockSecretManagementClient()
			testClients := wiring.TestClients{
				OpenChoreoClient: openChoreoClient,
				SecretMgmtClient: secretMgmtClient,
			}

			app := apitestutils.MakeAppClientWithDeps(t, testClients, tt.authMiddleware)

			// Send the delete request
			req := httptest.NewRequest(http.MethodDelete, tt.url, nil)

			rr := httptest.NewRecorder()
			app.ServeHTTP(rr, req)

			// Assert response
			require.Equal(t, tt.wantStatus, rr.Code)

			// Check error message for error responses
			if tt.wantStatus >= 400 {
				body := rr.Body.String()
				require.Contains(t, body, tt.wantErrMsg)
			}
		})
	}
}

func TestDeleteAgentNotFound(t *testing.T) {
	authMiddleware := jwtassertion.NewMockMiddleware(t)

	t.Run("Deleting a non-existent agent should return 404", func(t *testing.T) {
		openChoreoClient := apitestutils.CreateMockOpenChoreoClient()
		// Default GetComponentFunc returns ErrAgentNotFound for names containing "nonexistent-agent".
		secretMgmtClient := apitestutils.CreateMockSecretManagementClient()
		testClients := wiring.TestClients{
			OpenChoreoClient: openChoreoClient,
			SecretMgmtClient: secretMgmtClient,
		}

		app := apitestutils.MakeAppClientWithDeps(t, testClients, authMiddleware)

		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/nonexistent-agent-%s",
			testDeleteOrgName, testDeleteProjName, uuid.New().String()[:5])
		req := httptest.NewRequest(http.MethodDelete, url, nil)

		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)

		// Idempotent semantics have been removed: a delete on a missing agent must return 404
		// so clients can distinguish a real deletion from a typo or stale name.
		require.Equal(t, http.StatusNotFound, rr.Code)
		require.Contains(t, rr.Body.String(), "Agent not found")

		// Component should never be deleted in OpenChoreo for a missing agent.
		require.Empty(t, openChoreoClient.DeleteComponentCalls())
	})

	t.Run("Second delete of the same agent should return 404", func(t *testing.T) {
		openChoreoClient := apitestutils.CreateMockOpenChoreoClient()
		secretMgmtClient := apitestutils.CreateMockSecretManagementClient()

		// Track deletion state so the mock simulates a real backend: GetComponent returns the
		// agent before the first DELETE and ErrAgentNotFound afterwards.
		var (
			mu      sync.Mutex
			deleted bool
		)
		openChoreoClient.GetComponentFunc = func(ctx context.Context, namespaceName, projectName, componentName string) (*models.AgentResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			if deleted {
				return nil, utils.ErrAgentNotFound
			}
			return &models.AgentResponse{
				UUID:        "component-uid-123",
				Name:        componentName,
				ProjectName: projectName,
				Provisioning: models.Provisioning{
					Type: "internal",
				},
			}, nil
		}
		openChoreoClient.DeleteComponentFunc = func(ctx context.Context, namespaceName, projectName, componentName string) error {
			mu.Lock()
			defer mu.Unlock()
			if deleted {
				return utils.ErrAgentNotFound
			}
			deleted = true
			return nil
		}

		testClients := wiring.TestClients{
			OpenChoreoClient: openChoreoClient,
			SecretMgmtClient: secretMgmtClient,
		}
		app := apitestutils.MakeAppClientWithDeps(t, testClients, authMiddleware)

		agentName := fmt.Sprintf("new-agent-%s", uuid.New().String()[:7])
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s", testDeleteOrgName, testDeleteProjName, agentName)

		first := httptest.NewRecorder()
		app.ServeHTTP(first, httptest.NewRequest(http.MethodDelete, url, nil))
		require.Equal(t, http.StatusNoContent, first.Code, "first delete should succeed")

		second := httptest.NewRecorder()
		app.ServeHTTP(second, httptest.NewRequest(http.MethodDelete, url, nil))
		require.Equal(t, http.StatusNotFound, second.Code, "second delete should return 404")
		require.Contains(t, second.Body.String(), "Agent not found")

		require.Len(t, openChoreoClient.DeleteComponentCalls(), 1,
			"OpenChoreo DeleteComponent should only be invoked while the agent still exists")
	})

	t.Run("Deleting an agent that lives in another project should return 404", func(t *testing.T) {
		openChoreoClient := apitestutils.CreateMockOpenChoreoClient()
		secretMgmtClient := apitestutils.CreateMockSecretManagementClient()

		// Simulate the OpenChoreo API behaviour where GET /components/{name} returns the
		// component regardless of the requested project. The agent-manager wrapper / service
		// must catch the project mismatch and refuse to delete it.
		const actualProject = "project-a"
		openChoreoClient.GetComponentFunc = func(ctx context.Context, namespaceName, projectName, componentName string) (*models.AgentResponse, error) {
			return &models.AgentResponse{
				UUID:        "component-uid-123",
				Name:        componentName,
				ProjectName: actualProject,
				Provisioning: models.Provisioning{
					Type: "internal",
				},
			}, nil
		}

		testClients := wiring.TestClients{
			OpenChoreoClient: openChoreoClient,
			SecretMgmtClient: secretMgmtClient,
		}
		app := apitestutils.MakeAppClientWithDeps(t, testClients, authMiddleware)

		agentName := fmt.Sprintf("agent-%s", uuid.New().String()[:7])
		// Delete via a different project than the one the component actually belongs to.
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/project-b/agents/%s", testDeleteOrgName, agentName)
		req := httptest.NewRequest(http.MethodDelete, url, nil)

		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
		require.Contains(t, rr.Body.String(), "Agent not found")
		require.Empty(t, openChoreoClient.DeleteComponentCalls(),
			"DeleteComponent must not be invoked when the requested project does not own the agent")
	})
}
