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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/thundersvc"
	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// TestAgentIdentityCreateRole_EnsuresPerProxyRSBeforePermissionWrite proves each
// proxy's resource server is ensured before any role permission is written, so a
// role never references a permission the environment's Thunder does not yet know.
func TestAgentIdentityCreateRole_EnsuresPerProxyRSBeforePermissionWrite(t *testing.T) {
	ghUUID, jiraUUID := uuid.New(), uuid.New()
	var calls []string
	addByRS := map[string][]string{}
	envClient := &clientmocks.EnvIdentityClientMock{
		GetDefaultOUIDFunc: func(_ context.Context) (string, error) { return "ou-env", nil },
		EnsureProxyResourceServerFunc: func(_ context.Context, handle, _, _ string, _ []string) (string, error) {
			calls = append(calls, "ensure:"+handle)
			return "rs-" + handle, nil
		},
		CreateRoleFunc: func(_ context.Context, req thundersvc.CreateRoleRequest) (*thundersvc.ThunderRole, error) {
			calls = append(calls, "create")
			return &thundersvc.ThunderRole{ID: "role-1", Name: req.Name}, nil
		},
		AddRolePermissionsFunc: func(_ context.Context, _ string, req thundersvc.RolePermissionRequest) error {
			calls = append(calls, "add:"+req.ResourceServerID)
			addByRS[req.ResourceServerID] = req.Permissions
			return nil
		},
		// The handler re-fetches after the permission writes so the response
		// carries the reconciled scopes rather than the empty create payload.
		GetRoleFunc: func(_ context.Context, roleID string) (*thundersvc.ThunderRole, error) {
			calls = append(calls, "get")
			return &thundersvc.ThunderRole{ID: roleID, Name: "readers", Permissions: []thundersvc.RolePermissionRequest{
				{ResourceServerID: "rs-gh-proxy", Permissions: []string{"gh-proxy:read"}},
				{ResourceServerID: "rs-jira-proxy", Permissions: []string{"jira-proxy:write"}},
			}}, nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		GetByHandleFunc: func(_ context.Context, handle, _ string) (*models.MCPProxy, error) {
			switch handle {
			case "gh-proxy":
				return &models.MCPProxy{UUID: ghUUID}, nil
			case "jira-proxy":
				return &models.MCPProxy{UUID: jiraUUID}, nil
			}
			return nil, gorm.ErrRecordNotFound
		},
	}
	scopeRepo := &repomocks.MCPProxyScopeRepositoryMock{
		GetFunc: func(_ context.Context, proxyUUID uuid.UUID, action string) (*models.MCPProxyScope, error) {
			return &models.MCPProxyScope{MCPProxyUUID: proxyUUID, Action: action}, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, proxyRepo, scopeRepo, nil)

	req := httptest.NewRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		strings.NewReader(`{"name":"readers","scopes":["gh-proxy:read","jira-proxy:write"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req = req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
	w := httptest.NewRecorder()

	ctrl.CreateRole(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	// Both resource servers are ensured before the role write and before any
	// permission add; proxies are processed in sorted-handle order.
	assert.Equal(t, []string{"ensure:gh-proxy", "ensure:jira-proxy", "create", "add:rs-gh-proxy", "add:rs-jira-proxy", "get"}, calls)
	assert.Equal(t, []string{"gh-proxy:read"}, addByRS["rs-gh-proxy"])
	assert.Equal(t, []string{"jira-proxy:write"}, addByRS["rs-jira-proxy"])
	// The response body reflects the re-fetched, reconciled permissions.
	assert.Contains(t, w.Body.String(), "gh-proxy:read")
	assert.Contains(t, w.Body.String(), "jira-proxy:write")
}

// TestAgentIdentityCreateRole_UnknownProxyHandleRejected proves a scope naming a
// proxy that does not exist is rejected with 400 before the environment's Thunder
// is contacted (the resolver's ResolveIdentityFunc is left nil, so any call panics).
func TestAgentIdentityCreateRole_UnknownProxyHandleRejected(t *testing.T) {
	resolver := &clientmocks.EnvThunderResolverMock{} // ResolveIdentityFunc nil: must not be called
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		GetByHandleFunc: func(_ context.Context, _, _ string) (*models.MCPProxy, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	scopeRepo := &repomocks.MCPProxyScopeRepositoryMock{} // GetFunc nil: must not be reached
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, proxyRepo, scopeRepo, nil)

	req := httptest.NewRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		strings.NewReader(`{"name":"readers","scopes":["ghost:read"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req = req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
	w := httptest.NewRecorder()

	ctrl.CreateRole(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "ghost:read")
}

// TestAgentIdentityCreateRole_UnknownActionRejected proves a scope whose action is
// not defined on an existing proxy is rejected with 400.
func TestAgentIdentityCreateRole_UnknownActionRejected(t *testing.T) {
	resolver := &clientmocks.EnvThunderResolverMock{} // must not be called
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		GetByHandleFunc: func(_ context.Context, _, _ string) (*models.MCPProxy, error) {
			return &models.MCPProxy{UUID: uuid.New()}, nil
		},
	}
	scopeRepo := &repomocks.MCPProxyScopeRepositoryMock{
		GetFunc: func(_ context.Context, _ uuid.UUID, _ string) (*models.MCPProxyScope, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, proxyRepo, scopeRepo, nil)

	req := httptest.NewRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		strings.NewReader(`{"name":"readers","scopes":["gh-proxy:read"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req = req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
	w := httptest.NewRecorder()

	ctrl.CreateRole(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "gh-proxy:read")
}

// TestAgentIdentityCreateRole_MalformedScopeRejected proves a scope not of the form
// <proxy-handle>:<action> is rejected with 400 before any repository or Thunder call
// (both repo funcs are left nil, so any call panics).
func TestAgentIdentityCreateRole_MalformedScopeRejected(t *testing.T) {
	resolver := &clientmocks.EnvThunderResolverMock{}
	proxyRepo := &repomocks.MCPProxyRepositoryMock{}      // GetByHandleFunc nil: must not be called
	scopeRepo := &repomocks.MCPProxyScopeRepositoryMock{} // GetFunc nil: must not be called
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, proxyRepo, scopeRepo, nil)

	req := httptest.NewRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		strings.NewReader(`{"name":"readers","scopes":["no-colon"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req = req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
	w := httptest.NewRecorder()

	ctrl.CreateRole(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "no-colon")
}

// TestAgentIdentityUpdateRole_ReconcilesAcrossResourceServers proves the role's
// permissions are diffed per resource server: newly requested scopes are added to
// their proxy's RS, dropped scopes are removed, and a proxy dropped from the role
// entirely has all its permissions removed from its RS.
func TestAgentIdentityUpdateRole_ReconcilesAcrossResourceServers(t *testing.T) {
	ghUUID, jiraUUID := uuid.New(), uuid.New()
	addByRS := map[string][]string{}
	removeByRS := map[string][]string{}
	currentPerms := []thundersvc.RolePermissionRequest{
		{ResourceServerID: "rs-gh", Permissions: []string{"gh-proxy:read", "gh-proxy:write"}},
		{ResourceServerID: "rs-old", Permissions: []string{"old-proxy:use"}},
	}
	envClient := &clientmocks.EnvIdentityClientMock{
		GetRoleFunc: func(_ context.Context, roleID string) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, OuID: "ou-env", Name: "readers", Permissions: currentPerms}, nil
		},
		UpdateRoleFunc: func(_ context.Context, roleID string, req thundersvc.UpdateRoleRequest) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, Name: req.Name}, nil
		},
		EnsureProxyResourceServerFunc: func(_ context.Context, handle, _, _ string, _ []string) (string, error) {
			switch handle {
			case "gh-proxy":
				return "rs-gh", nil
			case "jira-proxy":
				return "rs-jira", nil
			}
			return "", fmt.Errorf("unexpected handle %q", handle)
		},
		AddRolePermissionsFunc: func(_ context.Context, _ string, req thundersvc.RolePermissionRequest) error {
			addByRS[req.ResourceServerID] = req.Permissions
			return nil
		},
		RemoveRolePermissionsFunc: func(_ context.Context, _ string, req thundersvc.RolePermissionRequest) error {
			removeByRS[req.ResourceServerID] = req.Permissions
			return nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		GetByHandleFunc: func(_ context.Context, handle, _ string) (*models.MCPProxy, error) {
			switch handle {
			case "gh-proxy":
				return &models.MCPProxy{UUID: ghUUID}, nil
			case "jira-proxy":
				return &models.MCPProxy{UUID: jiraUUID}, nil
			}
			return nil, gorm.ErrRecordNotFound
		},
	}
	scopeRepo := &repomocks.MCPProxyScopeRepositoryMock{
		GetFunc: func(_ context.Context, proxyUUID uuid.UUID, action string) (*models.MCPProxyScope, error) {
			return &models.MCPProxyScope{MCPProxyUUID: proxyUUID, Action: action}, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, proxyRepo, scopeRepo, nil)

	req := httptest.NewRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/roles/role-1",
		strings.NewReader(`{"name":"readers","scopes":["gh-proxy:read","jira-proxy:track"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("roleID", "role-1")
	req = req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
	w := httptest.NewRecorder()

	ctrl.UpdateRole(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.ElementsMatch(t, []string{"jira-proxy:track"}, addByRS["rs-jira"], "new proxy's scope must be added to its RS")
	assert.ElementsMatch(t, []string{"gh-proxy:write"}, removeByRS["rs-gh"], "dropped scope must be removed from its RS")
	assert.ElementsMatch(t, []string{"old-proxy:use"}, removeByRS["rs-old"], "a proxy dropped from the role must have its permissions removed")
	assert.NotContains(t, addByRS, "rs-gh", "a scope already present must not be re-added")
}

// TestAgentIdentityUpdateRole_PreservesNameWhenOmitted proves that a role update
// omitting "name" preserves the role's current name (Thunder's PUT is a full
// replace, so an empty name would blank it). scopes is omitted (nil) and the role
// has no permissions, so the handler returns right after the metadata write.
func TestAgentIdentityUpdateRole_PreservesNameWhenOmitted(t *testing.T) {
	var captured thundersvc.UpdateRoleRequest
	envClient := &clientmocks.EnvIdentityClientMock{
		GetRoleFunc: func(_ context.Context, roleID string) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, OuID: "ou-1", Name: "readers"}, nil
		},
		UpdateRoleFunc: func(_ context.Context, roleID string, req thundersvc.UpdateRoleRequest) (*thundersvc.ThunderRole, error) {
			captured = req
			return &thundersvc.ThunderRole{ID: roleID, OuID: req.OuID, Name: req.Name}, nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)

	req := httptest.NewRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/roles/role-1",
		strings.NewReader(`{"description":"metadata only"}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("roleID", "role-1")
	w := httptest.NewRecorder()

	ctrl.UpdateRole(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "readers", captured.Name, "omitted name must preserve the current role name")
	assert.Equal(t, "ou-1", captured.OuID, "update must carry the role's ouId")
	assert.Equal(t, "metadata only", captured.Description, "provided description must be applied")
}

// TestAgentIdentityUpdateRole_OmittedScopesPreservesPermissions proves that a
// metadata-only PUT (no "scopes" field) leaves the role's existing permissions
// untouched: omitting scopes decodes to nil, which skips the reconcile entirely,
// so no resource server is written to.
func TestAgentIdentityUpdateRole_OmittedScopesPreservesPermissions(t *testing.T) {
	currentPerms := []thundersvc.RolePermissionRequest{
		{ResourceServerID: "rs-gh", Permissions: []string{"gh-proxy:read", "gh-proxy:write"}},
	}
	envClient := &clientmocks.EnvIdentityClientMock{
		GetRoleFunc: func(_ context.Context, roleID string) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, OuID: "ou-1", Name: "readers", Permissions: currentPerms}, nil
		},
		UpdateRoleFunc: func(_ context.Context, roleID string, req thundersvc.UpdateRoleRequest) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, OuID: req.OuID, Name: req.Name}, nil
		},
		AddRolePermissionsFunc: func(_ context.Context, _ string, _ thundersvc.RolePermissionRequest) error {
			t.Fatal("metadata-only update must not add permissions")
			return nil
		},
		RemoveRolePermissionsFunc: func(_ context.Context, _ string, _ thundersvc.RolePermissionRequest) error {
			t.Fatal("metadata-only update must not remove permissions")
			return nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)

	req := httptest.NewRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/roles/role-1",
		strings.NewReader(`{"description":"metadata only"}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("roleID", "role-1")
	w := httptest.NewRecorder()

	ctrl.UpdateRole(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestAgentIdentityUpdateRole_ExplicitEmptyScopesClearsPermissions proves that an
// explicit "scopes": [] clears every permission: unlike an omitted field, an empty
// slice decodes to non-nil and drives the reconcile to remove all current scopes.
func TestAgentIdentityUpdateRole_ExplicitEmptyScopesClearsPermissions(t *testing.T) {
	removeByRS := map[string][]string{}
	currentPerms := []thundersvc.RolePermissionRequest{
		{ResourceServerID: "rs-gh", Permissions: []string{"gh-proxy:read", "gh-proxy:write"}},
	}
	envClient := &clientmocks.EnvIdentityClientMock{
		GetRoleFunc: func(_ context.Context, roleID string) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, OuID: "ou-1", Name: "readers", Permissions: currentPerms}, nil
		},
		UpdateRoleFunc: func(_ context.Context, roleID string, req thundersvc.UpdateRoleRequest) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, OuID: req.OuID, Name: req.Name}, nil
		},
		AddRolePermissionsFunc: func(_ context.Context, _ string, _ thundersvc.RolePermissionRequest) error {
			t.Fatal("clearing scopes must not add permissions")
			return nil
		},
		RemoveRolePermissionsFunc: func(_ context.Context, _ string, req thundersvc.RolePermissionRequest) error {
			removeByRS[req.ResourceServerID] = req.Permissions
			return nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)

	req := httptest.NewRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/roles/role-1",
		strings.NewReader(`{"scopes":[]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("roleID", "role-1")
	req = req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
	w := httptest.NewRecorder()

	ctrl.UpdateRole(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.ElementsMatch(t, []string{"gh-proxy:read", "gh-proxy:write"}, removeByRS["rs-gh"],
		"explicit empty scopes must remove all current permissions")
}

// TestAgentIdentityUpdateGroup_PreservesNameWhenOmitted proves the group update
// fetches the current group and preserves its name when the body omits "name",
// applying only the provided description.
func TestAgentIdentityUpdateGroup_PreservesNameWhenOmitted(t *testing.T) {
	var captured thundersvc.UpdateGroupRequest
	getCalled := false
	envClient := &clientmocks.EnvIdentityClientMock{
		GetGroupFunc: func(_ context.Context, groupID string) (*thundersvc.ThunderGroup, error) {
			getCalled = true
			return &thundersvc.ThunderGroup{ID: groupID, Name: "team-a"}, nil
		},
		UpdateGroupFunc: func(_ context.Context, groupID string, req thundersvc.UpdateGroupRequest) (*thundersvc.ThunderGroup, error) {
			captured = req
			return &thundersvc.ThunderGroup{ID: groupID, Name: req.Name, Description: req.Description}, nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)

	req := httptest.NewRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/groups/grp-1",
		strings.NewReader(`{"description":"x"}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("groupID", "grp-1")
	w := httptest.NewRecorder()

	ctrl.UpdateGroup(w, req)

	assert.True(t, getCalled, "update must fetch the current group first")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "team-a", captured.Name, "omitted name must preserve the current group name")
	assert.Equal(t, "x", captured.Description, "provided description must be applied")
}

// TestAgentIdentityRoutes_EnvThunderUnavailable proves that when the environment
// has no provisioned Thunder, a passthrough handler surfaces 503 (not 500) so
// callers know to retry once the environment is provisioned.
func TestAgentIdentityRoutes_EnvThunderUnavailable(t *testing.T) {
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return nil, thundersvc.ErrThunderNotProvisioned
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)

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
		FindByOuAndEnvironmentFunc: func(_ context.Context, ouID, environmentName string) ([]models.AgentThunderClient, error) {
			assert.Equal(t, "ou-1", ouID)
			assert.Equal(t, "dev", environmentName)
			return []models.AgentThunderClient{
				{
					OUID:            "ou-1",
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
	ctrl := NewAgentIdentityController(resolver, bindingRepo, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/orgs/o1/environments/dev/agent-identities/agents", nil)
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req = req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-1"}))
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

// TestAgentIdentityGetRoleAssignments_UsesAgentSemantics proves the env
// assignments read goes through GetAgentRoleAssignments (agents + groups) and
// never the user-store GetRoleAssignments (its mock func is left nil, so any
// call panics).
func TestAgentIdentityGetRoleAssignments_UsesAgentSemantics(t *testing.T) {
	envClient := &clientmocks.EnvIdentityClientMock{
		GetAgentRoleAssignmentsFunc: func(_ context.Context, roleID string) (*thundersvc.AgentRoleAssignments, error) {
			assert.Equal(t, "r1", roleID)
			return &thundersvc.AgentRoleAssignments{
				Agents: []thundersvc.AssignmentEntry{{ID: "thunder-1", Type: "agent"}},
				Groups: []thundersvc.ThunderGroup{{ID: "g1", Name: "readers"}},
			}, nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/orgs/o1/environments/dev/agent-identities/roles/r1/assignments", nil)
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("roleID", "r1")
	w := httptest.NewRecorder()

	ctrl.GetRoleAssignments(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Agents []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"agents"`
		Groups []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"groups"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Agents, 1)
	assert.Equal(t, "thunder-1", body.Agents[0].ID)
	assert.Equal(t, "agent", body.Agents[0].Type)
	assert.Len(t, body.Groups, 1)
	assert.Equal(t, "readers", body.Groups[0].Name)
}

// adminRoleEnvClient returns an env client whose GetRole always resolves the
// native Administrator role. Every mutating func is left nil, so any write
// reaching Thunder panics the test.
func adminRoleEnvClient() *clientmocks.EnvIdentityClientMock {
	return &clientmocks.EnvIdentityClientMock{
		GetRoleFunc: func(_ context.Context, roleID string) (*thundersvc.ThunderRole, error) {
			return &thundersvc.ThunderRole{ID: roleID, OuID: "ou-env", Name: thundersvc.NativeAdministratorRoleName}, nil
		},
	}
}

func adminRoleController(envClient *clientmocks.EnvIdentityClientMock) AgentIdentityController {
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(_ context.Context, _, _, _ string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	return NewAgentIdentityController(resolver, &repomocks.AgentThunderClientRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{}, &repomocks.MCPProxyScopeRepositoryMock{}, nil)
}

// adminRoleRequest builds a request with the org/env/role path values shared by
// every native-Administrator guard test.
func adminRoleRequest(method, url, body string) *http.Request {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("roleID", "r-admin")
	return req
}

// TestAgentIdentityGetRole_NativeAdministratorHidden proves the native
// Administrator role is invisible through the agent-identity API: fetching it by
// ID returns 404, consistent with it being filtered from the role listing.
func TestAgentIdentityGetRole_NativeAdministratorHidden(t *testing.T) {
	ctrl := adminRoleController(adminRoleEnvClient())

	req := adminRoleRequest(http.MethodGet, "/orgs/o1/environments/dev/agent-identities/roles/r-admin", "")
	w := httptest.NewRecorder()

	ctrl.GetRole(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAgentIdentityUpdateRole_NativeAdministratorRejected proves the native
// Administrator role cannot be updated through the agent-identity API — an
// UpdateRole with scopes would otherwise strip its "system" permission and cut
// off the amp-system-client. UpdateRoleFunc is nil, so any write panics.
func TestAgentIdentityUpdateRole_NativeAdministratorRejected(t *testing.T) {
	ctrl := adminRoleController(adminRoleEnvClient())

	req := adminRoleRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/roles/r-admin",
		`{"name":"renamed","scopes":[]}`)
	w := httptest.NewRecorder()

	ctrl.UpdateRole(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAgentIdentityDeleteRole_NativeAdministratorRejected proves the native
// Administrator role cannot be deleted through the agent-identity API.
// DeleteRoleFunc is nil, so any delete reaching Thunder panics.
func TestAgentIdentityDeleteRole_NativeAdministratorRejected(t *testing.T) {
	ctrl := adminRoleController(adminRoleEnvClient())

	req := adminRoleRequest(http.MethodDelete, "/orgs/o1/environments/dev/agent-identities/roles/r-admin", "")
	w := httptest.NewRecorder()

	ctrl.DeleteRole(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAgentIdentityAddRoleAssignees_NativeAdministratorRejected proves agents
// and groups cannot be assigned to the native Administrator role — the path by
// which agent identities were acquiring the "system" scope.
// AddRoleAssigneesFunc is nil, so any assignment reaching Thunder panics.
func TestAgentIdentityAddRoleAssignees_NativeAdministratorRejected(t *testing.T) {
	ctrl := adminRoleController(adminRoleEnvClient())

	req := adminRoleRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles/r-admin/assignments/add",
		`{"assignments":[{"id":"thunder-1","type":"agent"}]}`)
	w := httptest.NewRecorder()

	ctrl.AddRoleAssignees(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAgentIdentityRemoveRoleAssignees_NativeAdministratorAllowed pins the
// deliberate asymmetry with AddRoleAssignees: removal from the native
// Administrator role stays open so an existing mis-assignment can be cleaned up
// through the same API — the guard must never be applied symmetrically.
func TestAgentIdentityRemoveRoleAssignees_NativeAdministratorAllowed(t *testing.T) {
	removed := false
	envClient := adminRoleEnvClient()
	envClient.RemoveRoleAssigneesFunc = func(_ context.Context, roleID string, req thundersvc.RoleAssignmentsRequest) error {
		removed = true
		assert.Equal(t, "r-admin", roleID)
		return nil
	}
	ctrl := adminRoleController(envClient)

	req := adminRoleRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles/r-admin/assignments/remove",
		`{"assignments":[{"id":"thunder-1","type":"agent"}]}`)
	w := httptest.NewRecorder()

	ctrl.RemoveRoleAssignees(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, removed, "cleanup removal must pass through to Thunder")
}

// adminGroupEnvClient returns an env client whose GetGroup always resolves the
// native Administrators group. Every other func is left nil, so any read or
// write that slips past the guard and reaches Thunder panics the test.
func adminGroupEnvClient() *clientmocks.EnvIdentityClientMock {
	return &clientmocks.EnvIdentityClientMock{
		GetGroupFunc: func(_ context.Context, groupID string) (*thundersvc.ThunderGroup, error) {
			return &thundersvc.ThunderGroup{ID: groupID, OuID: "ou-env", Name: thundersvc.NativeAdministratorsGroupName}, nil
		},
	}
}

// adminGroupRequest builds a request with the org/env/group path values shared
// by every native-Administrators guard test.
func adminGroupRequest(method, url, body string) *http.Request {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	req.SetPathValue("groupID", "g-admin")
	return req
}

// TestAgentIdentityGroupHandlers_NativeAdministratorsHidden proves the native
// Administrators group is invisible and immutable through every agent-identity
// group operation. Its members inherit the native Administrator role and with it
// Thunder's "system" scope, so AddGroupMembers in particular is a route to
// env-Thunder admin that walks around the managedRole guard.
//
// Unlike RemoveRoleAssignees on the role side, RemoveGroupMembers is guarded
// too: this is the deliberate asymmetry documented on managedGroup, matching the
// org-identity controller. Cleanup of a pre-existing mis-membership needs direct
// Thunder access.
func TestAgentIdentityGroupHandlers_NativeAdministratorsHidden(t *testing.T) {
	const base = "/orgs/o1/environments/dev/agent-identities/groups/g-admin"
	cases := []struct {
		name   string
		method string
		url    string
		body   string
		invoke func(AgentIdentityController, http.ResponseWriter, *http.Request)
	}{
		{
			"GetGroup", http.MethodGet, base, "",
			func(c AgentIdentityController, w http.ResponseWriter, r *http.Request) { c.GetGroup(w, r) },
		},
		{
			"UpdateGroup", http.MethodPut, base, `{"description":"x"}`,
			func(c AgentIdentityController, w http.ResponseWriter, r *http.Request) { c.UpdateGroup(w, r) },
		},
		{
			"DeleteGroup", http.MethodDelete, base, "",
			func(c AgentIdentityController, w http.ResponseWriter, r *http.Request) { c.DeleteGroup(w, r) },
		},
		{
			"GetGroupMembers", http.MethodGet, base + "/members", "",
			func(c AgentIdentityController, w http.ResponseWriter, r *http.Request) { c.GetGroupMembers(w, r) },
		},
		{
			"AddGroupMembers", http.MethodPost, base + "/members/add", `{"agentIds":["thunder-1"]}`,
			func(c AgentIdentityController, w http.ResponseWriter, r *http.Request) { c.AddGroupMembers(w, r) },
		},
		{
			"RemoveGroupMembers", http.MethodPost, base + "/members/remove", `{"agentIds":["thunder-1"]}`,
			func(c AgentIdentityController, w http.ResponseWriter, r *http.Request) { c.RemoveGroupMembers(w, r) },
		},
		{
			"GetGroupRoles", http.MethodGet, base + "/roles", "",
			func(c AgentIdentityController, w http.ResponseWriter, r *http.Request) { c.GetGroupRoles(w, r) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := adminRoleController(adminGroupEnvClient())
			w := httptest.NewRecorder()

			tc.invoke(ctrl, w, adminGroupRequest(tc.method, tc.url, tc.body))

			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	}
}

// TestAgentIdentityCreateGroup_ReservedNameRejected proves a second group cannot
// be created under the native Administrators name, which would shadow the hidden
// system group. CreateGroupFunc is nil, so any write reaching Thunder panics.
func TestAgentIdentityCreateGroup_ReservedNameRejected(t *testing.T) {
	ctrl := adminRoleController(&clientmocks.EnvIdentityClientMock{})

	req := adminGroupRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/groups",
		`{"name":"`+thundersvc.NativeAdministratorsGroupName+`"}`)
	w := httptest.NewRecorder()

	ctrl.CreateGroup(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAgentIdentityUpdateGroup_ReservedRenameRejected proves an ordinary group
// cannot be renamed into the native Administrators name. The guard runs before
// the group is fetched, so GetGroupFunc is nil here too.
func TestAgentIdentityUpdateGroup_ReservedRenameRejected(t *testing.T) {
	ctrl := adminRoleController(&clientmocks.EnvIdentityClientMock{})

	req := adminGroupRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/groups/g-1",
		`{"name":"`+thundersvc.NativeAdministratorsGroupName+`"}`)
	w := httptest.NewRecorder()

	ctrl.UpdateGroup(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAgentIdentityCreateRole_ReservedNameRejected proves a role cannot be
// created under the native Administrator name. CreateRoleFunc is nil, so any
// write reaching Thunder panics.
func TestAgentIdentityCreateRole_ReservedNameRejected(t *testing.T) {
	ctrl := adminRoleController(&clientmocks.EnvIdentityClientMock{})

	req := adminRoleRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		`{"name":"`+thundersvc.NativeAdministratorRoleName+`"}`)
	w := httptest.NewRecorder()

	ctrl.CreateRole(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAgentIdentityUpdateRole_ReservedRenameRejected proves an ordinary role
// cannot be renamed into the native Administrator name. managedRole already
// blocks editing the native role itself; this closes the other direction.
func TestAgentIdentityUpdateRole_ReservedRenameRejected(t *testing.T) {
	ctrl := adminRoleController(&clientmocks.EnvIdentityClientMock{})

	req := adminRoleRequest(http.MethodPut, "/orgs/o1/environments/dev/agent-identities/roles/r-1",
		`{"name":"`+thundersvc.NativeAdministratorRoleName+`"}`)
	w := httptest.NewRecorder()

	ctrl.UpdateRole(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
