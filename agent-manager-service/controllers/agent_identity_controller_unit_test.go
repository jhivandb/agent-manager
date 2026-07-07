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

package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/thundersvc"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// TestAgentIdentityCreateRole_LazyEnsuresScopesBeforeRoleWrite proves that the
// scope resource server is ensured before the role is written, so a role never
// exists referencing a permission the environment's Thunder does not yet know.
func TestAgentIdentityCreateRole_LazyEnsuresScopesBeforeRoleWrite(t *testing.T) {
	var calls []string
	envClient := &clientmocks.EnvIdentityClientMock{
		GetDefaultOUIDFunc: func(_ context.Context) (string, error) { return "ou-1", nil },
		EnsureScopeResourceServerFunc: func(_ context.Context, scopes []string) (string, error) {
			calls = append(calls, "ensure")
			assert.ElementsMatch(t, []string{"repo:read.all"}, scopes)
			return "rs-1", nil
		},
		CreateRoleFunc: func(_ context.Context, req thundersvc.CreateRoleRequest) (*thundersvc.ThunderRole, error) {
			calls = append(calls, "create")
			return &thundersvc.ThunderRole{ID: "role-1", Name: req.Name}, nil
		},
		AddRolePermissionsFunc: func(_ context.Context, _ string, req thundersvc.RolePermissionRequest) error {
			calls = append(calls, "perms")
			assert.Equal(t, "rs-1", req.ResourceServerID)
			assert.ElementsMatch(t, []string{"repo:read.all"}, req.Permissions)
			return nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	scopeRepo := &repomocks.ScopeRepositoryMock{
		ListFunc: func(_ context.Context, _ string) ([]models.Scope, error) {
			return []models.Scope{{Name: "repo:read.all"}}, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, scopeRepo, &repomocks.AgentThunderClientRepositoryMock{})

	req := httptest.NewRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		strings.NewReader(`{"name":"readers","scopes":["repo:read.all"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	w := httptest.NewRecorder()

	ctrl.CreateRole(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, []string{"ensure", "create", "perms"}, calls, "scope RS must be ensured before the role write")
}

// TestAgentIdentityCreateRole_UnknownScopeRejected proves an unknown scope is
// rejected with 400 before the environment's Thunder is even contacted (the
// resolver's ResolveIdentityFunc is left nil, so any call would panic).
func TestAgentIdentityCreateRole_UnknownScopeRejected(t *testing.T) {
	scopeRepo := &repomocks.ScopeRepositoryMock{
		ListFunc: func(_ context.Context, _ string) ([]models.Scope, error) {
			return []models.Scope{}, nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{} // ResolveIdentityFunc nil: must not be called
	ctrl := NewAgentIdentityController(resolver, scopeRepo, &repomocks.AgentThunderClientRepositoryMock{})

	req := httptest.NewRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		strings.NewReader(`{"name":"readers","scopes":["repo:write.all"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	w := httptest.NewRecorder()

	ctrl.CreateRole(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "repo:write.all")
}

// TestAgentIdentityUpdateRole_ReconcilesScopePermissions proves the role's scope
// permissions are diffed against the request: newly requested scopes are added and
// dropped scopes are removed, both under the amp-scopes resource server.
func TestAgentIdentityUpdateRole_ReconcilesScopePermissions(t *testing.T) {
	var added, removed []string
	currentPerms := []thundersvc.RolePermissionRequest{
		{ResourceServerID: "rs-1", Permissions: []string{"repo:read.all", "repo:write.all"}},
	}
	envClient := &clientmocks.EnvIdentityClientMock{
		GetRoleFunc: func(_ context.Context, roleID string) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{
				ID:          roleID,
				OuID:        "ou-1",
				Name:        "readers",
				Permissions: currentPerms,
			}, nil
		},
		UpdateRoleFunc: func(_ context.Context, roleID string, req thundersvc.UpdateRoleRequest) (*thundersvc.ThunderRole, error) {
			// Thunder's PUT /roles/{id} is a full replace requiring ouId; the
			// metadata update must echo the role's ouId and current permissions
			// so a name/description change never drops them (regression: ROL-1001).
			assert.Equal(t, "ou-1", req.OuID, "update must carry the role's ouId")
			assert.Equal(t, currentPerms, req.Permissions, "update must preserve current permissions")
			return &thundersvc.ThunderRole{ID: roleID, Name: req.Name}, nil
		},
		EnsureScopeResourceServerFunc: func(_ context.Context, _ []string) (string, error) {
			return "rs-1", nil
		},
		AddRolePermissionsFunc: func(_ context.Context, _ string, req thundersvc.RolePermissionRequest) error {
			added = req.Permissions
			return nil
		},
		RemoveRolePermissionsFunc: func(_ context.Context, _ string, req thundersvc.RolePermissionRequest) error {
			removed = req.Permissions
			return nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	scopeRepo := &repomocks.ScopeRepositoryMock{
		ListFunc: func(_ context.Context, _ string) ([]models.Scope, error) {
			return []models.Scope{{Name: "repo:read.all"}, {Name: "repo:write.all"}, {Name: "repo:delete.all"}}, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, scopeRepo, &repomocks.AgentThunderClientRepositoryMock{})

	req := httptest.NewRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/roles/role-1",
		strings.NewReader(`{"name":"readers","scopes":["repo:write.all","repo:delete.all"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("roleID", "role-1")
	w := httptest.NewRecorder()

	ctrl.UpdateRole(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.ElementsMatch(t, []string{"repo:delete.all"}, added, "newly requested scope must be added")
	assert.ElementsMatch(t, []string{"repo:read.all"}, removed, "dropped scope must be removed")
}

// TestAgentIdentityRoutes_EnvThunderUnavailable proves that when the environment
// has no provisioned Thunder, a passthrough handler surfaces 503 (not 500) so
// callers know to retry once the environment is provisioned.
func TestAgentIdentityRoutes_EnvThunderUnavailable(t *testing.T) {
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return nil, thundersvc.ErrThunderNotProvisioned
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.ScopeRepositoryMock{}, &repomocks.AgentThunderClientRepositoryMock{})

	req := httptest.NewRequest(http.MethodGet, "/orgs/o1/environments/dev/agent-identities/groups", nil)
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	w := httptest.NewRecorder()

	ctrl.ListGroups(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestAgentIdentityListAgents_ReturnsBindings proves the agents picker reads
// bindings straight from the repository (no env-Thunder round-trip: the
// resolver's ResolveIdentityFunc is left nil and must not be called).
func TestAgentIdentityListAgents_ReturnsBindings(t *testing.T) {
	bindingRepo := &repomocks.AgentThunderClientRepositoryMock{
		FindByOrgAndEnvironmentFunc: func(_ context.Context, orgName, environmentName string) ([]models.AgentThunderClient, error) {
			assert.Equal(t, "o1", orgName)
			assert.Equal(t, "dev", environmentName)
			return []models.AgentThunderClient{
				{
					OrgName:         "o1",
					ProjectName:     "proj",
					AgentName:       "agent-a",
					EnvironmentName: "dev",
					ThunderAgentID:  "thunder-1",
					Status:          models.AgentThunderStatusCompleted,
				},
			}, nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{} // must not be called
	ctrl := NewAgentIdentityController(resolver, &repomocks.ScopeRepositoryMock{}, bindingRepo)

	req := httptest.NewRequest(http.MethodGet, "/orgs/o1/environments/dev/agent-identities/agents", nil)
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	w := httptest.NewRecorder()

	ctrl.ListAgents(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Agents []struct {
			AgentName      string `json:"agentName"`
			ProjectName    string `json:"projectName"`
			Status         string `json:"status"`
			ThunderAgentID string `json:"thunderAgentId"`
		} `json:"agents"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Agents, 1)
	assert.Equal(t, "agent-a", body.Agents[0].AgentName)
	assert.Equal(t, "proj", body.Agents[0].ProjectName)
	assert.Equal(t, "completed", body.Agents[0].Status)
	assert.Equal(t, "thunder-1", body.Agents[0].ThunderAgentID)
}
