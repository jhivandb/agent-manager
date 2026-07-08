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
	"errors"
	"fmt"
	"net/http"

	"github.com/wso2/agent-manager/agent-manager-service/clients/thundersvc"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// AgentIdentityController exposes env-Thunder group/role management for agent
// identities. Every handler is a passthrough to the environment's own Thunder
// instance, resolved per request via EnvThunderResolver — AMS stores no
// group/role state of its own. Roles carry catalog scopes as their permissions.
type AgentIdentityController interface {
	// Groups
	ListGroups(w http.ResponseWriter, r *http.Request)
	CreateGroup(w http.ResponseWriter, r *http.Request)
	GetGroup(w http.ResponseWriter, r *http.Request)
	UpdateGroup(w http.ResponseWriter, r *http.Request)
	DeleteGroup(w http.ResponseWriter, r *http.Request)
	GetGroupMembers(w http.ResponseWriter, r *http.Request)
	AddGroupMembers(w http.ResponseWriter, r *http.Request)
	RemoveGroupMembers(w http.ResponseWriter, r *http.Request)
	GetGroupRoles(w http.ResponseWriter, r *http.Request)

	// Roles
	ListRoles(w http.ResponseWriter, r *http.Request)
	CreateRole(w http.ResponseWriter, r *http.Request)
	GetRole(w http.ResponseWriter, r *http.Request)
	UpdateRole(w http.ResponseWriter, r *http.Request)
	DeleteRole(w http.ResponseWriter, r *http.Request)
	GetRoleAssignments(w http.ResponseWriter, r *http.Request)
	AddRoleAssignees(w http.ResponseWriter, r *http.Request)
	RemoveRoleAssignees(w http.ResponseWriter, r *http.Request)

	// Agents picker
	ListAgents(w http.ResponseWriter, r *http.Request)
}

type agentIdentityController struct {
	resolver    thundersvc.EnvThunderResolver
	scopeRepo   repositories.ScopeRepository
	bindingRepo repositories.AgentThunderClientRepository
}

// NewAgentIdentityController creates a new agent-identity passthrough controller.
func NewAgentIdentityController(
	resolver thundersvc.EnvThunderResolver,
	scopeRepo repositories.ScopeRepository,
	bindingRepo repositories.AgentThunderClientRepository,
) AgentIdentityController {
	return &agentIdentityController{resolver: resolver, scopeRepo: scopeRepo, bindingRepo: bindingRepo}
}

// envClient resolves the env-Thunder identity client for the request's org+env,
// writing the error response itself when resolution fails (returns ok=false).
// An unprovisioned or unreachable environment surfaces 503 so callers know to
// retry once the environment's Thunder is available, rather than a generic 500.
func (c *agentIdentityController) envClient(w http.ResponseWriter, r *http.Request) (thundersvc.EnvIdentityClient, bool) {
	orgName := r.PathValue(utils.PathParamOrgName)
	envName := r.PathValue("envName")
	client, err := c.resolver.ResolveIdentity(r.Context(), orgName, envName)
	if err != nil {
		logger.GetLogger(r.Context()).Error("agent-identity: env-thunder resolve failed", "org", orgName, "env", envName, "error", err)
		utils.WriteErrorResponse(w, http.StatusServiceUnavailable,
			"The environment's identity provider is not available; retry after it is provisioned")
		return nil, false
	}
	return client, true
}

// --- Groups ---

func (c *agentIdentityController) ListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	ouID, err := client.GetDefaultOUID(ctx)
	if err != nil {
		log.Error("agent-identity ListGroups: resolve default OU failed", "error", err)
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to resolve environment identity provider OU")
		return
	}

	offset, limit := paginationParams(r)
	// Unlike the org-identity controller, env-Thunder groups are all user-created
	// and agent-scoped: there is no Administrators group to filter out here.
	groups, total, err := client.ListGroupsByOUId(ctx, ouID, offset, limit)
	if err != nil {
		log.Error("agent-identity ListGroups failed", "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, map[string]any{"groups": groups, "total": total, "offset": offset, "limit": limit})
}

func (c *agentIdentityController) CreateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)

	var body spec.AgentIdentityGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Name == "" {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "name is required")
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	ouID, err := client.GetDefaultOUID(ctx)
	if err != nil {
		log.Error("agent-identity CreateGroup: resolve default OU failed", "error", err)
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to resolve environment identity provider OU")
		return
	}

	group, err := client.CreateGroup(ctx, thundersvc.CreateGroupRequest{
		Name:        body.Name,
		OuID:        ouID,
		Description: derefString(body.Description),
	})
	if err != nil {
		log.Error("agent-identity CreateGroup failed", "name", body.Name, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to create group")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusCreated, group)
}

func (c *agentIdentityController) GetGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	groupID := r.PathValue(utils.PathParamGroupID)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	group, err := client.GetGroup(ctx, groupID)
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Group not found")
			return
		}
		log.Error("agent-identity GetGroup failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to get group")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, group)
}

func (c *agentIdentityController) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	groupID := r.PathValue(utils.PathParamGroupID)

	var body spec.AgentIdentityGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	current, err := client.GetGroup(ctx, groupID)
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Group not found")
			return
		}
		log.Error("agent-identity UpdateGroup: get group failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update group")
		return
	}
	var namePtr *string
	if body.Name != "" {
		namePtr = &body.Name
	}
	// Thunder's PUT /groups/{id} is a full replace: NewGroupReplace preserves the
	// group's current name when the body omits it, applying only the given fields.
	group, err := client.UpdateGroup(ctx, groupID, thundersvc.NewGroupReplace(*current, namePtr, body.Description))
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Group not found")
			return
		}
		log.Error("agent-identity UpdateGroup failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update group")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, group)
}

func (c *agentIdentityController) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	groupID := r.PathValue(utils.PathParamGroupID)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	if err := client.DeleteGroup(ctx, groupID); err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Group not found")
			return
		}
		log.Error("agent-identity DeleteGroup failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to delete group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *agentIdentityController) GetGroupMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	groupID := r.PathValue(utils.PathParamGroupID)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	offset, limit := paginationParams(r)
	// Agent-identity group members are agents, not users, so return the raw typed
	// member entries rather than resolving user members.
	members, total, err := client.ListGroupMemberEntries(ctx, groupID, offset, limit)
	if err != nil {
		log.Error("agent-identity GetGroupMembers failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to get group members")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, map[string]any{"members": members, "total": total, "offset": offset, "limit": limit})
}

func (c *agentIdentityController) AddGroupMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	groupID := r.PathValue(utils.PathParamGroupID)

	var body spec.AgentIdentityMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(body.AgentIds) == 0 {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "agentIds must not be empty")
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	if err := client.AddGroupMemberEntries(ctx, groupID, agentMemberEntries(body.AgentIds)); err != nil {
		log.Error("agent-identity AddGroupMembers failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to add group members")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, struct{}{})
}

func (c *agentIdentityController) RemoveGroupMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	groupID := r.PathValue(utils.PathParamGroupID)

	var body spec.AgentIdentityMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(body.AgentIds) == 0 {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "agentIds must not be empty")
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	if err := client.RemoveGroupMemberEntries(ctx, groupID, agentMemberEntries(body.AgentIds)); err != nil {
		log.Error("agent-identity RemoveGroupMembers failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to remove group members")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, struct{}{})
}

func (c *agentIdentityController) GetGroupRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	groupID := r.PathValue(utils.PathParamGroupID)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	roles, err := client.GetGroupRoles(ctx, groupID)
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Group not found")
			return
		}
		log.Error("agent-identity GetGroupRoles failed", "groupID", groupID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to get group roles")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, map[string]any{"roles": roles})
}

// --- Roles ---

func (c *agentIdentityController) ListRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	ouID, err := client.GetDefaultOUID(ctx)
	if err != nil {
		log.Error("agent-identity ListRoles: resolve default OU failed", "error", err)
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to resolve environment identity provider OU")
		return
	}

	offset, limit := paginationParams(r)
	roles, total, err := client.ListRoles(ctx, ouID, offset, limit)
	if err != nil {
		log.Error("agent-identity ListRoles failed", "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to list roles")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, map[string]any{"roles": roles, "total": total, "offset": offset, "limit": limit})
}

// CreateRole registers the requested catalog scopes with the environment's
// Thunder before writing the role, so a role never references a permission the
// environment does not yet know (the lazy-ensure ordering the spike established).
func (c *agentIdentityController) CreateRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)

	var body spec.AgentIdentityRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Name == "" {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "name is required")
		return
	}
	scopes := body.Scopes
	if err := c.validateScopesInCatalog(ctx, r.PathValue(utils.PathParamOrgName), scopes); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	ouID, err := client.GetDefaultOUID(ctx)
	if err != nil {
		log.Error("agent-identity CreateRole: resolve default OU failed", "error", err)
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to resolve environment identity provider OU")
		return
	}

	var rsID string
	if len(scopes) > 0 {
		rsID, err = client.EnsureScopeResourceServer(ctx, scopes)
		if err != nil {
			log.Error("agent-identity CreateRole: ensure scope resource server failed", "error", err)
			utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to register scopes with the environment identity provider")
			return
		}
	}

	role, err := client.CreateRole(ctx, thundersvc.CreateRoleRequest{
		Name:        body.Name,
		OuID:        ouID,
		Description: derefString(body.Description),
	})
	if err != nil {
		log.Error("agent-identity CreateRole failed", "name", body.Name, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to create role")
		return
	}

	if len(scopes) > 0 {
		if err := client.AddRolePermissions(ctx, role.ID, thundersvc.RolePermissionRequest{ResourceServerID: rsID, Permissions: scopes}); err != nil {
			log.Error("agent-identity CreateRole: add role permissions failed", "roleID", role.ID, "error", err)
			utils.WriteErrorResponse(w, http.StatusInternalServerError, "Role created but scope permissions failed; edit the role to retry")
			return
		}
	}
	utils.WriteSuccessResponse(w, http.StatusCreated, role)
}

func (c *agentIdentityController) GetRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	roleID := r.PathValue(utils.PathParamRoleID)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	role, err := client.GetRole(ctx, roleID)
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Role not found")
			return
		}
		log.Error("agent-identity GetRole failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to get role")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, role)
}

// UpdateRole updates the role metadata and reconciles its scope permissions to
// the requested set: scopes present in the request but not on the role are added
// (after ensuring the resource server), and scopes on the role but not requested
// are removed — both under the amp-scopes resource server.
func (c *agentIdentityController) UpdateRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	roleID := r.PathValue(utils.PathParamRoleID)

	var body spec.AgentIdentityRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	scopes := body.Scopes
	if err := c.validateScopesInCatalog(ctx, r.PathValue(utils.PathParamOrgName), scopes); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	current, err := client.GetRole(ctx, roleID)
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Role not found")
			return
		}
		log.Error("agent-identity UpdateRole: get role failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update role")
		return
	}

	var namePtr *string
	if body.Name != "" {
		namePtr = &body.Name
	}
	// Thunder's PUT /roles/{id} is a full replace: NewRoleReplace carries the
	// role's ouId and current permissions and preserves the name when the body
	// omits it, so a metadata change never blanks the name or drops permissions.
	// The scope reconcile below then applies the requested additions/removals.
	updated, err := client.UpdateRole(ctx, roleID, thundersvc.NewRoleReplace(*current, namePtr, body.Description))
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Role not found")
			return
		}
		log.Error("agent-identity UpdateRole failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update role")
		return
	}

	// Skip touching the resource server entirely when there are neither requested
	// scopes nor existing permissions to reconcile (a pure metadata update).
	if len(scopes) == 0 && len(current.Permissions) == 0 {
		utils.WriteSuccessResponse(w, http.StatusOK, updated)
		return
	}

	rsID, err := client.EnsureScopeResourceServer(ctx, scopes)
	if err != nil {
		log.Error("agent-identity UpdateRole: ensure scope resource server failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to register scopes with the environment identity provider")
		return
	}
	currentScopes := currentScopesForRS(current, rsID)
	additions := stringSetDifference(scopes, currentScopes)
	removals := stringSetDifference(currentScopes, scopes)

	if len(additions) > 0 {
		if err := client.AddRolePermissions(ctx, roleID, thundersvc.RolePermissionRequest{ResourceServerID: rsID, Permissions: additions}); err != nil {
			log.Error("agent-identity UpdateRole: add role permissions failed", "roleID", roleID, "error", err)
			utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update role permissions")
			return
		}
	}
	if len(removals) > 0 {
		if err := client.RemoveRolePermissions(ctx, roleID, thundersvc.RolePermissionRequest{ResourceServerID: rsID, Permissions: removals}); err != nil {
			log.Error("agent-identity UpdateRole: remove role permissions failed", "roleID", roleID, "error", err)
			utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update role permissions")
			return
		}
	}
	utils.WriteSuccessResponse(w, http.StatusOK, updated)
}

func (c *agentIdentityController) DeleteRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	roleID := r.PathValue(utils.PathParamRoleID)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	if err := client.DeleteRole(ctx, roleID); err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Role not found")
			return
		}
		log.Error("agent-identity DeleteRole failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to delete role")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *agentIdentityController) GetRoleAssignments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	roleID := r.PathValue(utils.PathParamRoleID)

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	assignments, err := client.GetRoleAssignments(ctx, roleID)
	if err != nil {
		if thundersvc.IsNotFound(err) {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Role not found")
			return
		}
		log.Error("agent-identity GetRoleAssignments failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to get role assignments")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, assignments)
}

func (c *agentIdentityController) AddRoleAssignees(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	roleID := r.PathValue(utils.PathParamRoleID)

	assignments, err := decodeAgentIdentityAssignments(r)
	if err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	if err := client.AddRoleAssignees(ctx, roleID, assignments); err != nil {
		log.Error("agent-identity AddRoleAssignees failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to add role assignees")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, struct{}{})
}

func (c *agentIdentityController) RemoveRoleAssignees(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	roleID := r.PathValue(utils.PathParamRoleID)

	assignments, err := decodeAgentIdentityAssignments(r)
	if err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	client, ok := c.envClient(w, r)
	if !ok {
		return
	}
	if err := client.RemoveRoleAssignees(ctx, roleID, assignments); err != nil {
		log.Error("agent-identity RemoveRoleAssignees failed", "roleID", roleID, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to remove role assignees")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, struct{}{})
}

// --- Agents picker ---

// ListAgents returns the agent-identity bindings provisioned for this
// environment straight from AMS state (no env-Thunder round-trip). It is the
// source for the assignment picker: which agents can be added to a group or
// assigned a role.
func (c *agentIdentityController) ListAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	orgName := r.PathValue(utils.PathParamOrgName)
	envName := r.PathValue("envName")

	rows, err := c.bindingRepo.FindByOrgAndEnvironment(ctx, orgName, envName)
	if err != nil {
		log.Error("agent-identity ListAgents failed", "org", orgName, "env", envName, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to list agent identity bindings")
		return
	}
	agents := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		agents = append(agents, map[string]any{
			"agentName":      row.AgentName,
			"projectName":    row.ProjectName,
			"status":         row.Status,
			"thunderAgentId": row.ThunderAgentID,
		})
	}
	utils.WriteSuccessResponse(w, http.StatusOK, map[string]any{"agents": agents})
}

// --- helpers ---

// validateScopesInCatalog rejects any scope not present in the org-global catalog
// before the environment's Thunder is contacted, so the passthrough never
// registers a scope AMS does not recognize.
func (c *agentIdentityController) validateScopesInCatalog(ctx context.Context, orgName string, scopes []string) error {
	if len(scopes) == 0 {
		return nil
	}
	catalog, err := c.scopeRepo.List(ctx, orgName)
	if err != nil {
		// Callers surface this error's message directly to the HTTP client as a 400,
		// so the cause is logged here rather than wrapped into the returned error.
		logger.GetLogger(ctx).Error("agent-identity validateScopesInCatalog: load scope catalog failed", "orgName", orgName, "error", err)
		return errors.New("failed to load scope catalog")
	}
	known := make(map[string]struct{}, len(catalog))
	for _, sc := range catalog {
		known[sc.Name] = struct{}{}
	}
	for _, s := range scopes {
		if _, ok := known[s]; !ok {
			return fmt.Errorf("scope %q is not in the catalog", s)
		}
	}
	return nil
}

// paginationParams parses and clamps the offset/limit query parameters.
func paginationParams(r *http.Request) (offset, limit int) {
	offset = getIntQueryParam(r, "offset", 0)
	limit = getIntQueryParam(r, "limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return offset, limit
}

// agentMemberEntries maps agent IDs to typed group member entries.
func agentMemberEntries(agentIDs []string) []thundersvc.GroupMember {
	members := make([]thundersvc.GroupMember, 0, len(agentIDs))
	for _, id := range agentIDs {
		members = append(members, thundersvc.GroupMember{ID: id, Type: "agent"})
	}
	return members
}

// decodeAgentIdentityAssignments converts the request payload into the Thunder
// assignments shape, accepting only agent and group assignee types.
func decodeAgentIdentityAssignments(r *http.Request) (thundersvc.RoleAssignmentsRequest, error) {
	var body spec.AgentIdentityAssignmentsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return thundersvc.RoleAssignmentsRequest{}, errors.New("invalid request body")
	}
	if len(body.Assignments) == 0 {
		return thundersvc.RoleAssignmentsRequest{}, errors.New("assignments must not be empty")
	}
	entries := make([]thundersvc.AssignmentEntry, 0, len(body.Assignments))
	for _, a := range body.Assignments {
		if a.Type != "agent" && a.Type != "group" {
			return thundersvc.RoleAssignmentsRequest{}, fmt.Errorf("assignment type %q is not allowed (must be agent or group)", a.Type)
		}
		if a.Id == "" {
			return thundersvc.RoleAssignmentsRequest{}, errors.New("assignment id must not be empty")
		}
		entries = append(entries, thundersvc.AssignmentEntry{ID: a.Id, Type: a.Type})
	}
	return thundersvc.RoleAssignmentsRequest{Assignments: entries}, nil
}

// currentScopesForRS returns the role's permission strings under the given
// resource server, or nil if the role carries none.
func currentScopesForRS(role *thundersvc.ThunderRole, rsID string) []string {
	if role == nil {
		return nil
	}
	for _, p := range role.Permissions {
		if p.ResourceServerID == rsID {
			return p.Permissions
		}
	}
	return nil
}

// stringSetDifference returns the elements of a that are not present in b.
func stringSetDifference(a, b []string) []string {
	inB := make(map[string]struct{}, len(b))
	for _, s := range b {
		inB[s] = struct{}{}
	}
	diff := make([]string, 0)
	for _, s := range a {
		if _, ok := inB[s]; !ok {
			diff = append(diff, s)
		}
	}
	return diff
}
