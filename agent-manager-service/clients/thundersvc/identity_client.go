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

package thundersvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
)

// IdentityClient provides user, group, and role management operations via the Thunder API.
type IdentityClient interface {
	// Users
	ListUsers(ctx context.Context, offset, limit int) ([]ThunderUser, int, error)
	ListUsersByOUId(ctx context.Context, ouID string, offset, limit int) ([]ThunderUser, int, error)
	GetUser(ctx context.Context, userID string) (*ThunderUser, error)
	CreateUser(ctx context.Context, req CreateUserRequest) (*ThunderUser, error)
	UpdateUser(ctx context.Context, userID string, req UpdateUserRequest) (*ThunderUser, error)
	DeleteUser(ctx context.Context, userID string) error
	GetUserGroups(ctx context.Context, userID string) ([]ThunderGroup, error)
	GetUserRoles(ctx context.Context, userID string) ([]ThunderRole, error)
	InviteUser(ctx context.Context, email string, ouID string) (string, error)

	// Groups
	ListGroups(ctx context.Context, ouID string, offset, limit int) ([]ThunderGroup, int, error)
	ListGroupsByOUId(ctx context.Context, ouID string, offset, limit int) ([]ThunderGroup, int, error)
	GetGroup(ctx context.Context, groupID string) (*ThunderGroup, error)
	CreateGroup(ctx context.Context, req CreateGroupRequest) (*ThunderGroup, error)
	UpdateGroup(ctx context.Context, groupID string, req UpdateGroupRequest) (*ThunderGroup, error)
	DeleteGroup(ctx context.Context, groupID string) error
	AddGroupMembers(ctx context.Context, groupID string, userIDs []string) error
	RemoveGroupMembers(ctx context.Context, groupID string, userIDs []string) error
	GetGroupMembers(ctx context.Context, groupID string, offset, limit int) ([]ThunderUser, int, error)
	GetGroupRoles(ctx context.Context, groupID string) ([]ThunderRole, error)
	// AddGroupMemberEntries adds typed members (agents, users, nested groups) to a group.
	AddGroupMemberEntries(ctx context.Context, groupID string, members []GroupMember) error
	// RemoveGroupMemberEntries removes typed members from a group.
	RemoveGroupMemberEntries(ctx context.Context, groupID string, members []GroupMember) error
	// ListGroupMemberEntries returns the raw typed member entries of a group (unlike
	// GetGroupMembers, which resolves and returns only user members).
	ListGroupMemberEntries(ctx context.Context, groupID string, offset, limit int) ([]GroupMember, int, error)

	// Roles
	ListRoles(ctx context.Context, ouID string, offset, limit int) ([]ThunderRole, int, error)
	GetRole(ctx context.Context, roleID string) (*ThunderRole, error)
	CreateRole(ctx context.Context, req CreateRoleRequest) (*ThunderRole, error)
	UpdateRole(ctx context.Context, roleID string, req UpdateRoleRequest) (*ThunderRole, error)
	DeleteRole(ctx context.Context, roleID string) error
	GetRoleAssignments(ctx context.Context, roleID string) (*RoleAssignments, error)
	AddRolePermissions(ctx context.Context, roleID string, req RolePermissionRequest) error
	RemoveRolePermissions(ctx context.Context, roleID string, req RolePermissionRequest) error
	AddRoleAssignees(ctx context.Context, roleID string, req RoleAssignmentsRequest) error
	RemoveRoleAssignees(ctx context.Context, roleID string, req RoleAssignmentsRequest) error

	// Permissions catalog
	ListAMPPermissions(ctx context.Context) ([]ThunderPermission, string, error)
	// EnsureScopeResourceServer makes sure the amp-scopes resource server exists and
	// that every given scope is registered as a permission under it, returning its ID.
	EnsureScopeResourceServer(ctx context.Context, scopes []string) (string, error)

	// Organization units
	GetOUIDByHandle(ctx context.Context, handle string) (string, error)
	ListChildOUs(ctx context.Context, parentOUID string, limit, offset int) ([]ThunderOU, int, error)
}

// NewIdentityClient creates a Thunder client for identity management operations.
// It shares the same transport and token-caching as ThunderClient.
func NewIdentityClient(baseURL, clientID, clientSecret string) IdentityClient {
	return &thunderClient{
		baseURL:      baseURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: httpClientTimeout},
	}
}

// --- Users ---

func (c *thunderClient) ListUsers(ctx context.Context, offset, limit int) ([]ThunderUser, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	url := fmt.Sprintf("%s/users?offset=%d&limit=%d", c.baseURL, offset, limit)
	body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("thunder list users: %w", err)
	}

	var wrapped thunderUserList
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, 0, fmt.Errorf("thunder list users decode: %w", err)
	}
	return wrapped.Users, wrapped.TotalResults, nil
}

func (c *thunderClient) GetUser(ctx context.Context, userID string) (*ThunderUser, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/users/"+userID, token, nil)
	if err != nil {
		return nil, fmt.Errorf("thunder get user: %w", err)
	}
	var user ThunderUser
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("thunder get user decode: %w", err)
	}
	return &user, nil
}

func (c *thunderClient) CreateUser(ctx context.Context, req CreateUserRequest) (*ThunderUser, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/users", token, req)
	if err != nil {
		return nil, fmt.Errorf("thunder create user: %w", err)
	}
	var user ThunderUser
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("thunder create user decode: %w", err)
	}
	return &user, nil
}

func (c *thunderClient) UpdateUser(ctx context.Context, userID string, req UpdateUserRequest) (*ThunderUser, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodPut, c.baseURL+"/users/"+userID, token, req)
	if err != nil {
		return nil, fmt.Errorf("thunder update user: %w", err)
	}
	var user ThunderUser
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("thunder update user decode: %w", err)
	}
	return &user, nil
}

func (c *thunderClient) DeleteUser(ctx context.Context, userID string) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodDelete, c.baseURL+"/users/"+userID, token, nil)
	if err != nil {
		return fmt.Errorf("thunder delete user: %w", err)
	}
	return nil
}

func (c *thunderClient) GetUserGroups(ctx context.Context, userID string) ([]ThunderGroup, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/users/"+userID+"/groups", token, nil)
	if err != nil {
		return nil, fmt.Errorf("thunder get user groups: %w", err)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("thunder get user groups: empty response")
	}
	if trimmed[0] == '[' {
		var groups []ThunderGroup
		if err := json.Unmarshal(body, &groups); err != nil {
			return nil, fmt.Errorf("thunder get user groups decode: %w", err)
		}
		return groups, nil
	}
	var wrapped thunderGroupList
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("thunder get user groups decode: %w", err)
	}
	return wrapped.Groups, nil
}

// --- Groups ---

// ListGroups returns groups scoped to ouID when non-empty, by fetching all pages
// from Thunder and filtering client-side (Thunder has no OU-scoped list endpoint for groups).
func (c *thunderClient) ListGroups(ctx context.Context, ouID string, offset, limit int) ([]ThunderGroup, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	if ouID == "" {
		url := fmt.Sprintf("%s/groups?offset=%d&limit=%d", c.baseURL, offset, limit)
		body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("thunder list groups: %w", err)
		}
		var wrapped thunderGroupList
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, 0, fmt.Errorf("thunder list groups decode: %w", err)
		}
		return wrapped.Groups, wrapped.TotalResults, nil
	}

	const fetchSize = 100
	var all []ThunderGroup
	fetchOffset := 0
	for {
		url := fmt.Sprintf("%s/groups?offset=%d&limit=%d", c.baseURL, fetchOffset, fetchSize)
		body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("thunder list groups: %w", err)
		}
		var wrapped thunderGroupList
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, 0, fmt.Errorf("thunder list groups decode: %w", err)
		}
		for _, g := range wrapped.Groups {
			if g.OuID == ouID {
				all = append(all, g)
			}
		}
		fetchOffset += len(wrapped.Groups)
		if fetchOffset >= wrapped.TotalResults || len(wrapped.Groups) == 0 {
			break
		}
	}
	total := len(all)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []ThunderGroup{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

// ListGroupsByOUId fetches groups directly from Thunder's OU-scoped endpoint.
// Uses /organization-units/{ouId}/groups which is more efficient than fetching all groups.
func (c *thunderClient) ListGroupsByOUId(ctx context.Context, ouID string, offset, limit int) ([]ThunderGroup, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}

	url := fmt.Sprintf("%s/organization-units/%s/groups?offset=%d&limit=%d", c.baseURL, ouID, offset, limit)
	body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("thunder list groups by ou id: %w", err)
	}

	var wrapped thunderGroupList
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, 0, fmt.Errorf("thunder list groups by ou id decode: %w", err)
	}
	return wrapped.Groups, wrapped.TotalResults, nil
}

func (c *thunderClient) GetGroup(ctx context.Context, groupID string) (*ThunderGroup, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/groups/"+groupID, token, nil)
	if err != nil {
		return nil, fmt.Errorf("thunder get group: %w", err)
	}
	var group ThunderGroup
	if err := json.Unmarshal(body, &group); err != nil {
		return nil, fmt.Errorf("thunder get group decode: %w", err)
	}
	return &group, nil
}

func (c *thunderClient) CreateGroup(ctx context.Context, req CreateGroupRequest) (*ThunderGroup, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/groups", token, req)
	if err != nil {
		return nil, fmt.Errorf("thunder create group: %w", err)
	}
	var group ThunderGroup
	if err := json.Unmarshal(body, &group); err != nil {
		return nil, fmt.Errorf("thunder create group decode: %w", err)
	}
	return &group, nil
}

func (c *thunderClient) UpdateGroup(ctx context.Context, groupID string, req UpdateGroupRequest) (*ThunderGroup, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodPut, c.baseURL+"/groups/"+groupID, token, req)
	if err != nil {
		return nil, fmt.Errorf("thunder update group: %w", err)
	}
	var group ThunderGroup
	if err := json.Unmarshal(body, &group); err != nil {
		return nil, fmt.Errorf("thunder update group decode: %w", err)
	}
	return &group, nil
}

func (c *thunderClient) DeleteGroup(ctx context.Context, groupID string) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodDelete, c.baseURL+"/groups/"+groupID, token, nil)
	if err != nil {
		return fmt.Errorf("thunder delete group: %w", err)
	}
	return nil
}

func (c *thunderClient) AddGroupMembers(ctx context.Context, groupID string, userIDs []string) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	members := make([]GroupMember, len(userIDs))
	for i, id := range userIDs {
		members[i] = GroupMember{ID: id, Type: "user"}
	}
	_, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/groups/"+groupID+"/members/add", token, GroupMembersRequest{Members: members})
	if err != nil {
		return fmt.Errorf("thunder add group members: %w", err)
	}
	return nil
}

func (c *thunderClient) RemoveGroupMembers(ctx context.Context, groupID string, userIDs []string) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	members := make([]GroupMember, len(userIDs))
	for i, id := range userIDs {
		members[i] = GroupMember{ID: id, Type: "user"}
	}
	_, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/groups/"+groupID+"/members/remove", token, GroupMembersRequest{Members: members})
	if err != nil {
		return fmt.Errorf("thunder remove group members: %w", err)
	}
	return nil
}

func (c *thunderClient) GetGroupMembers(ctx context.Context, groupID string, offset, limit int) ([]ThunderUser, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	url := fmt.Sprintf("%s/groups/%s/members?offset=%d&limit=%d", c.baseURL, groupID, offset, limit)
	body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("thunder get group members: %w", err)
	}
	var resp thunderGroupMemberList
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("thunder get group members decode: %w", err)
	}
	users := make([]ThunderUser, 0, len(resp.Members))
	log := logger.GetLogger(ctx)
	for _, m := range resp.Members {
		if m.Type != "user" {
			continue
		}
		user, err := c.GetUser(ctx, m.ID)
		if err != nil {
			if IsNotFound(err) {
				log.Warn("skipping deleted group member", "groupID", groupID, "userID", m.ID)
				continue
			}
			return nil, 0, fmt.Errorf("thunder get group member %s: %w", m.ID, err)
		}
		users = append(users, *user)
	}
	return users, resp.TotalResults, nil
}

// AddGroupMemberEntries adds typed members (agents, users, nested groups) to a
// group. Unlike AddGroupMembers it does not assume Type "user".
func (c *thunderClient) AddGroupMemberEntries(ctx context.Context, groupID string, members []GroupMember) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/groups/"+groupID+"/members/add", token, GroupMembersRequest{Members: members})
	if err != nil {
		return fmt.Errorf("thunder add group member entries: %w", err)
	}
	return nil
}

// RemoveGroupMemberEntries removes typed members from a group. Unlike
// RemoveGroupMembers it does not assume Type "user".
func (c *thunderClient) RemoveGroupMemberEntries(ctx context.Context, groupID string, members []GroupMember) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/groups/"+groupID+"/members/remove", token, GroupMembersRequest{Members: members})
	if err != nil {
		return fmt.Errorf("thunder remove group member entries: %w", err)
	}
	return nil
}

// ListGroupMemberEntries returns the raw typed member entries of a group. Unlike
// GetGroupMembers, it does not resolve entries into full user objects, so it
// preserves non-user members (agents, nested groups).
func (c *thunderClient) ListGroupMemberEntries(ctx context.Context, groupID string, offset, limit int) ([]GroupMember, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	url := fmt.Sprintf("%s/groups/%s/members?offset=%d&limit=%d", c.baseURL, groupID, offset, limit)
	body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("thunder list group member entries: %w", err)
	}
	var resp thunderGroupMemberList
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("thunder list group member entries decode: %w", err)
	}
	return resp.Members, resp.TotalResults, nil
}

// listRoleAssignmentEntries fetches only the raw {type, id} assignment entries for a role
// without expanding each entry into a full user or group object. Use this when you only
// need to check membership rather than display full objects — it is O(1) per role instead
// of O(assignments) like GetRoleAssignments.
func (c *thunderClient) listRoleAssignmentEntries(ctx context.Context, roleID string) ([]AssignmentEntry, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/roles/"+roleID+"/assignments", token, nil)
	if err != nil {
		return nil, fmt.Errorf("thunder list role assignment entries: %w", err)
	}
	var resp thunderRoleAssignmentList
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("thunder list role assignment entries decode: %w", err)
	}
	return resp.Assignments, nil
}

func (c *thunderClient) GetGroupRoles(ctx context.Context, groupID string) ([]ThunderRole, error) {
	const pageSize = 50
	var allRoles []ThunderRole
	offset := 0
	for {
		page, total, err := c.ListRoles(ctx, "", offset, pageSize)
		if err != nil {
			return nil, fmt.Errorf("thunder get group roles (list): %w", err)
		}
		allRoles = append(allRoles, page...)
		offset += len(page)
		if offset >= total || len(page) == 0 {
			break
		}
	}

	var groupRoles []ThunderRole
	for _, role := range allRoles {
		entries, err := c.listRoleAssignmentEntries(ctx, role.ID)
		if err != nil {
			return nil, fmt.Errorf("thunder get group roles (assignments for role %s): %w", role.ID, err)
		}
		for _, e := range entries {
			if e.Type == "group" && e.ID == groupID {
				groupRoles = append(groupRoles, role)
				break
			}
		}
	}
	return groupRoles, nil
}

func (c *thunderClient) GetUserRoles(ctx context.Context, userID string) ([]ThunderRole, error) {
	const pageSize = 50
	var allRoles []ThunderRole
	offset := 0
	for {
		page, total, err := c.ListRoles(ctx, "", offset, pageSize)
		if err != nil {
			return nil, fmt.Errorf("thunder get user roles (list): %w", err)
		}
		allRoles = append(allRoles, page...)
		offset += len(page)
		if offset >= total || len(page) == 0 {
			break
		}
	}

	var userRoles []ThunderRole
	for _, role := range allRoles {
		entries, err := c.listRoleAssignmentEntries(ctx, role.ID)
		if err != nil {
			return nil, fmt.Errorf("thunder get user roles (assignments for role %s): %w", role.ID, err)
		}
		for _, e := range entries {
			if e.Type == "user" && e.ID == userID {
				userRoles = append(userRoles, role)
				break
			}
		}
	}
	return userRoles, nil
}

// --- Roles ---

// ListRoles returns roles scoped to ouID when non-empty, by fetching all pages
// from Thunder and filtering client-side (Thunder has no OU-scoped list endpoint for roles).
func (c *thunderClient) ListRoles(ctx context.Context, ouID string, offset, limit int) ([]ThunderRole, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	if ouID == "" {
		url := fmt.Sprintf("%s/roles?offset=%d&limit=%d", c.baseURL, offset, limit)
		body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("thunder list roles: %w", err)
		}
		var wrapped thunderRoleList
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, 0, fmt.Errorf("thunder list roles decode: %w", err)
		}
		return wrapped.Roles, wrapped.TotalResults, nil
	}

	const fetchSize = 100
	var all []ThunderRole
	fetchOffset := 0
	for {
		url := fmt.Sprintf("%s/roles?offset=%d&limit=%d", c.baseURL, fetchOffset, fetchSize)
		body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("thunder list roles: %w", err)
		}
		var wrapped thunderRoleList
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, 0, fmt.Errorf("thunder list roles decode: %w", err)
		}
		for _, role := range wrapped.Roles {
			if role.OuID == ouID {
				all = append(all, role)
			}
		}
		fetchOffset += len(wrapped.Roles)
		if fetchOffset >= wrapped.TotalResults || len(wrapped.Roles) == 0 {
			break
		}
	}
	total := len(all)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []ThunderRole{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

func (c *thunderClient) GetRole(ctx context.Context, roleID string) (*ThunderRole, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/roles/"+roleID, token, nil)
	if err != nil {
		return nil, fmt.Errorf("thunder get role: %w", err)
	}
	var role ThunderRole
	if err := json.Unmarshal(body, &role); err != nil {
		return nil, fmt.Errorf("thunder get role decode: %w", err)
	}
	return &role, nil
}

func (c *thunderClient) CreateRole(ctx context.Context, req CreateRoleRequest) (*ThunderRole, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/roles", token, req)
	if err != nil {
		return nil, fmt.Errorf("thunder create role: %w", err)
	}
	var role ThunderRole
	if err := json.Unmarshal(body, &role); err != nil {
		return nil, fmt.Errorf("thunder create role decode: %w", err)
	}
	return &role, nil
}

func (c *thunderClient) UpdateRole(ctx context.Context, roleID string, req UpdateRoleRequest) (*ThunderRole, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodPut, c.baseURL+"/roles/"+roleID, token, req)
	if err != nil {
		return nil, fmt.Errorf("thunder update role: %w", err)
	}
	var role ThunderRole
	if err := json.Unmarshal(body, &role); err != nil {
		return nil, fmt.Errorf("thunder update role decode: %w", err)
	}
	return &role, nil
}

func (c *thunderClient) DeleteRole(ctx context.Context, roleID string) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodDelete, c.baseURL+"/roles/"+roleID, token, nil)
	if err != nil {
		return fmt.Errorf("thunder delete role: %w", err)
	}
	return nil
}

func (c *thunderClient) GetRoleAssignments(ctx context.Context, roleID string) (*RoleAssignments, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/roles/"+roleID+"/assignments", token, nil)
	if err != nil {
		return nil, fmt.Errorf("thunder get role assignments: %w", err)
	}
	var resp thunderRoleAssignmentList
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("thunder get role assignments decode: %w", err)
	}
	result := &RoleAssignments{}
	log := logger.GetLogger(ctx)
	for _, a := range resp.Assignments {
		switch a.Type {
		case "user":
			user, err := c.GetUser(ctx, a.ID)
			if err != nil {
				if IsNotFound(err) {
					log.Warn("skipping deleted role assignment user", "roleID", roleID, "userID", a.ID)
					continue
				}
				return nil, fmt.Errorf("thunder get role assignment user %s: %w", a.ID, err)
			}
			result.Users = append(result.Users, *user)
		case "group":
			group, err := c.GetGroup(ctx, a.ID)
			if err != nil {
				if IsNotFound(err) {
					log.Warn("skipping deleted role assignment group", "roleID", roleID, "groupID", a.ID)
					continue
				}
				return nil, fmt.Errorf("thunder get role assignment group %s: %w", a.ID, err)
			}
			result.Groups = append(result.Groups, *group)
		}
	}

	return result, nil
}

// AddRolePermissions merges new permissions into the role via PUT /roles/{id}.
func (c *thunderClient) AddRolePermissions(ctx context.Context, roleID string, req RolePermissionRequest) error {
	role, err := c.GetRole(ctx, roleID)
	if err != nil {
		return fmt.Errorf("thunder add role permissions (get role): %w", err)
	}

	perms := role.Permissions
	found := false
	for i, p := range perms {
		if p.ResourceServerID == req.ResourceServerID {
			existing := make(map[string]bool, len(p.Permissions))
			for _, perm := range p.Permissions {
				existing[perm] = true
			}
			for _, newPerm := range req.Permissions {
				if !existing[newPerm] {
					perms[i].Permissions = append(perms[i].Permissions, newPerm)
				}
			}
			found = true
			break
		}
	}
	if !found {
		perms = append(perms, req)
	}

	return c.putRolePermissions(ctx, role, perms)
}

// RemoveRolePermissions removes permissions from the role via PUT /roles/{id}.
func (c *thunderClient) RemoveRolePermissions(ctx context.Context, roleID string, req RolePermissionRequest) error {
	role, err := c.GetRole(ctx, roleID)
	if err != nil {
		return fmt.Errorf("thunder remove role permissions (get role): %w", err)
	}

	toRemove := make(map[string]bool, len(req.Permissions))
	for _, p := range req.Permissions {
		toRemove[p] = true
	}

	var perms []RolePermissionRequest
	for _, p := range role.Permissions {
		if p.ResourceServerID == req.ResourceServerID {
			var remaining []string
			for _, perm := range p.Permissions {
				if !toRemove[perm] {
					remaining = append(remaining, perm)
				}
			}
			if len(remaining) > 0 {
				perms = append(perms, RolePermissionRequest{ResourceServerID: p.ResourceServerID, Permissions: remaining})
			}
		} else {
			perms = append(perms, p)
		}
	}

	return c.putRolePermissions(ctx, role, perms)
}

func (c *thunderClient) putRolePermissions(ctx context.Context, role *ThunderRole, perms []RolePermissionRequest) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	body := thunderRolePermissionsUpdateBody{
		OuID:        role.OuID,
		Name:        role.Name,
		Description: role.Description,
		Permissions: perms,
	}
	_, err = c.doRequest(ctx, http.MethodPut, c.baseURL+"/roles/"+role.ID, token, body)
	if err != nil {
		return fmt.Errorf("thunder put role permissions: %w", err)
	}
	return nil
}

func (c *thunderClient) AddRoleAssignees(ctx context.Context, roleID string, req RoleAssignmentsRequest) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/roles/"+roleID+"/assignments/add", token, req)
	if err != nil {
		return fmt.Errorf("thunder add role assignees: %w", err)
	}
	return nil
}

func (c *thunderClient) RemoveRoleAssignees(ctx context.Context, roleID string, req RoleAssignmentsRequest) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/roles/"+roleID+"/assignments/remove", token, req)
	if err != nil {
		return fmt.Errorf("thunder remove role assignees: %w", err)
	}
	return nil
}

// --- Permissions catalog ---

// ListAMPPermissions returns all permissions registered under the "amp" resource server.
// It returns the permissions as strings (e.g. "amp:agents:create") and the resource server ID.
func (c *thunderClient) ListAMPPermissions(ctx context.Context) ([]ThunderPermission, string, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, "", err
	}

	// Find the "amp" resource server.
	ampRSID, err := c.findResourceServerID(ctx, token, rbac.ResourceServer)
	if err != nil {
		return nil, "", err
	}
	if ampRSID == "" {
		// Return empty list if amp resource server not found - permissions can be managed without it
		return []ThunderPermission{}, "", nil
	}

	// Fetch all resources for the amp resource server using pagination.
	const resPageSize = 20
	var resources []ThunderResource
	resOffset := 0
	for {
		resURL := fmt.Sprintf("%s/resource-servers/%s/resources?offset=%d&limit=%d", c.baseURL, ampRSID, resOffset, resPageSize)
		resBody, err := c.doRequest(ctx, http.MethodGet, resURL, token, nil)
		if err != nil {
			return nil, "", fmt.Errorf("thunder list amp resources: %w", err)
		}
		var page thunderResourceList
		if err := json.Unmarshal(resBody, &page); err != nil {
			return nil, "", fmt.Errorf("thunder list amp resources decode: %w", err)
		}
		resources = append(resources, page.Resources...)
		resOffset += len(page.Resources)
		if resOffset >= page.TotalResults || len(page.Resources) == 0 {
			break
		}
	}

	// For each resource fetch its actions via the dedicated endpoint.
	// Actions are not embedded in the resource list response.
	var perms []ThunderPermission
	for _, res := range resources {
		actURL := fmt.Sprintf("%s/resource-servers/%s/resources/%s/actions", c.baseURL, ampRSID, res.ID)
		actBody, err := c.doRequest(ctx, http.MethodGet, actURL, token, nil)
		if err != nil {
			return nil, "", fmt.Errorf("thunder list actions for resource %s: %w", res.ID, err)
		}
		var actPage thunderActionList
		if err := json.Unmarshal(actBody, &actPage); err != nil {
			return nil, "", fmt.Errorf("thunder list actions decode for resource %s: %w", res.ID, err)
		}
		for _, action := range actPage.Actions {
			name := action.Permission
			if name == "" {
				name = res.Handle + ":" + action.Handle
			}
			perms = append(perms, ThunderPermission{
				Name:             name,
				ResourceServerID: ampRSID,
				ResourceName:     res.Name,
				ActionName:       action.Name,
			})
		}
	}

	return perms, ampRSID, nil
}

const (
	// scopeResourceServerIdentifier is the identifier of the resource server that
	// holds the org's catalog scopes as permissions in each environment's Thunder.
	scopeResourceServerIdentifier = "amp-scopes"
	scopeResourceServerName       = "AMP Scopes"
	// scopeResourceServerDelimiter is the resource-server permission delimiter. Thunder
	// derives a resource's permission by joining ancestor handles with this delimiter;
	// with no parent and an empty resource-server handle the derived permission equals
	// the resource's own handle. We use "/" — which scope names never contain — so each
	// scope can be its own resource handle and the derived permission equals the raw
	// scope exactly (the value carried in role permissions and the agent token claim).
	scopeResourceServerDelimiter = "/"
	// scopeHandleMaxLen is Thunder's maximum resource-handle length. The scope catalog
	// allows longer names, so over-long scopes are rejected with a clear error instead
	// of letting Thunder reject the handle with an opaque 400.
	scopeHandleMaxLen = 100
)

// findResourceServerID paginates through resource servers and returns the ID of
// the one whose identifier matches, or "" if none match.
func (c *thunderClient) findResourceServerID(ctx context.Context, token, identifier string) (string, error) {
	const rsPageSize = 20
	rsOffset := 0
	for {
		rsURL := fmt.Sprintf("%s/resource-servers?offset=%d&limit=%d", c.baseURL, rsOffset, rsPageSize)
		rsBody, err := c.doRequest(ctx, http.MethodGet, rsURL, token, nil)
		if err != nil {
			return "", fmt.Errorf("thunder list resource servers: %w", err)
		}
		var page thunderResourceServerList
		if err := json.Unmarshal(rsBody, &page); err != nil {
			return "", fmt.Errorf("thunder list resource servers decode: %w", err)
		}
		for _, rs := range page.ResourceServers {
			if rs.Identifier == identifier {
				return rs.ID, nil
			}
		}
		rsOffset += len(page.ResourceServers)
		if rsOffset >= page.Total || len(page.ResourceServers) == 0 {
			return "", nil
		}
	}
}

// EnsureScopeResourceServer makes sure the amp-scopes resource server exists and
// that every given scope is registered as a permission under it, then returns the
// resource server ID. Idempotent; called lazily before role writes.
func (c *thunderClient) EnsureScopeResourceServer(ctx context.Context, scopes []string) (string, error) {
	for _, scope := range scopes {
		if len(scope) > scopeHandleMaxLen {
			return "", fmt.Errorf("scope %q exceeds the Thunder resource handle limit of %d characters", scope, scopeHandleMaxLen)
		}
	}

	token, err := c.getSystemToken(ctx)
	if err != nil {
		return "", err
	}

	rsID, err := c.findResourceServerID(ctx, token, scopeResourceServerIdentifier)
	if err != nil {
		return "", err
	}
	if rsID == "" {
		ouID, err := c.getDefaultOUID(ctx, token)
		if err != nil {
			return "", fmt.Errorf("thunder ensure scope resource server (default ou): %w", err)
		}
		body, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/resource-servers", token,
			map[string]string{
				"name":       scopeResourceServerName,
				"identifier": scopeResourceServerIdentifier,
				"ouId":       ouID,
				"delimiter":  scopeResourceServerDelimiter,
			})
		if err != nil {
			return "", fmt.Errorf("thunder create scope resource server: %w", err)
		}
		var created ThunderResourceServer
		if err := json.Unmarshal(body, &created); err != nil {
			return "", fmt.Errorf("thunder create scope resource server decode: %w", err)
		}
		rsID = created.ID
	}

	existing, err := c.listResourceServerPermissions(ctx, token, rsID)
	if err != nil {
		return "", err
	}
	for _, scope := range scopes {
		if scope == "" {
			continue
		}
		if _, ok := existing[scope]; ok {
			continue
		}
		if err := c.createScopePermission(ctx, token, rsID, scope); err != nil {
			return "", err
		}
		existing[scope] = struct{}{} // guard against duplicate scopes in the input slice
	}
	return rsID, nil
}

// listResourceServerPermissions returns the set of permission strings already
// registered under the given resource server. Thunder derives each resource's
// permission from its handle and returns it on the resource itself, so the set is
// read directly from resource.Permission — no per-resource action listing is needed
// (and reading actions would make the idempotency check re-create on every call,
// since scopes are stored as resources with no actions).
func (c *thunderClient) listResourceServerPermissions(ctx context.Context, token, rsID string) (map[string]struct{}, error) {
	const resPageSize = 20
	perms := make(map[string]struct{})
	resOffset := 0
	for {
		resURL := fmt.Sprintf("%s/resource-servers/%s/resources?offset=%d&limit=%d", c.baseURL, rsID, resOffset, resPageSize)
		resBody, err := c.doRequest(ctx, http.MethodGet, resURL, token, nil)
		if err != nil {
			return nil, fmt.Errorf("thunder list scope resources: %w", err)
		}
		var page thunderResourceList
		if err := json.Unmarshal(resBody, &page); err != nil {
			return nil, fmt.Errorf("thunder list scope resources decode: %w", err)
		}
		for _, res := range page.Resources {
			if res.Permission != "" {
				perms[res.Permission] = struct{}{}
			}
		}
		resOffset += len(page.Resources)
		if resOffset >= page.TotalResults || len(page.Resources) == 0 {
			break
		}
	}
	return perms, nil
}

// createScopePermission registers one scope as a single resource under the scope
// resource server. Thunder ignores any client-supplied permission and derives it
// from handles: with the resource server's empty handle and "/" delimiter, and the
// raw scope as the resource handle, the derived RESOURCE.PERMISSION equals the scope
// exactly — the value that surfaces in role permissions and the agent token's scope
// claim. No action is created; the resource alone carries the permission. Callers
// must ensure the scope is within scopeHandleMaxLen before invoking this.
func (c *thunderClient) createScopePermission(ctx context.Context, token, rsID, scope string) error {
	_, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/resource-servers/"+rsID+"/resources", token,
		map[string]string{"name": scope, "handle": scope})
	if err != nil {
		return fmt.Errorf("thunder create scope resource %q: %w", scope, err)
	}
	return nil
}

// ListUsersByOUId fetches users directly from Thunder's OU-scoped endpoint.
// Uses /organization-units/{ouId}/users which is more efficient than fetching all users.
func (c *thunderClient) ListUsersByOUId(ctx context.Context, ouID string, offset, limit int) ([]ThunderUser, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}

	url := fmt.Sprintf("%s/organization-units/%s/users?offset=%d&limit=%d&include=display", c.baseURL, ouID, offset, limit)
	body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("thunder list users by ou id: %w", err)
	}

	var wrapped thunderUserList
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, 0, fmt.Errorf("thunder list users by ou id decode: %w", err)
	}
	return wrapped.Users, wrapped.TotalResults, nil
}

// InviteUser executes Thunder's USER_ONBOARDING flow for the given email address and
// returns the invite link from the final step's additionalData.
//
// The flow is adaptive: after the user-type step, Thunder may present an OU-selection
// step (multi-OU / cloud deployments) or skip straight to the invite-mode choice
// (single-OU / on-prem). The code inspects data.actions in each response to decide
// which step comes next, so it handles both topologies without separate code paths.
func (c *thunderClient) InviteUser(ctx context.Context, email string, ouID string) (string, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return "", err
	}

	// flowStep holds only what we need from each intermediate response.
	type inviteFlowStep struct {
		ExecutionID    string `json:"executionId"`
		ChallengeToken string `json:"challengeToken"`
		Data           struct {
			Actions []struct {
				Ref string `json:"ref"`
			} `json:"actions"`
		} `json:"data"`
	}
	var flowStep inviteFlowStep

	unmarshalStep := func(body []byte, label string) error {
		flowStep = inviteFlowStep{}
		if err := json.Unmarshal(body, &flowStep); err != nil {
			return fmt.Errorf("thunder invite user %s decode: %w", label, err)
		}
		return nil
	}

	hasAction := func(ref string) bool {
		for _, a := range flowStep.Data.Actions {
			if a.Ref == ref {
				return true
			}
		}
		return false
	}

	// Step 1: start the onboarding flow.
	body, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/flow/execute", token,
		map[string]any{"flowType": "USER_ONBOARDING", "verbose": true})
	if err != nil {
		return "", fmt.Errorf("thunder invite user start flow: %w", err)
	}
	if err := unmarshalStep(body, "start flow"); err != nil {
		return "", err
	}
	execID := flowStep.ExecutionID
	challengeToken := flowStep.ChallengeToken

	// Step 2: select user type.
	body, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/flow/execute", token,
		map[string]any{
			"executionId":    execID,
			"challengeToken": challengeToken,
			"inputs":         map[string]string{"userType": "engineer"},
			"verbose":        true,
			"action":         "action_usertype",
		})
	if err != nil {
		return "", fmt.Errorf("thunder invite user submit type: %w", err)
	}
	if err := unmarshalStep(body, "submit type"); err != nil {
		return "", err
	}
	challengeToken = flowStep.ChallengeToken

	// Step 3 (conditional): OU selection. Only presented in multi-OU deployments where
	// Thunder cannot infer the target org unit. Single-OU instances skip this step.
	if hasAction("action_ou_selection") {
		body, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/flow/execute", token,
			map[string]any{
				"executionId":    execID,
				"challengeToken": challengeToken,
				"inputs":         map[string]string{"ouId": ouID},
				"verbose":        true,
				"action":         "action_ou_selection",
			})
		if err != nil {
			return "", fmt.Errorf("thunder invite user submit ou: %w", err)
		}
		if err := unmarshalStep(body, "submit ou"); err != nil {
			return "", err
		}
		challengeToken = flowStep.ChallengeToken
	}

	// Step 4: select invite mode (create-user-now vs invite-user).
	body, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/flow/execute", token,
		map[string]any{
			"executionId":    execID,
			"challengeToken": challengeToken,
			"verbose":        true,
			"action":         "action_invite_user",
		})
	if err != nil {
		return "", fmt.Errorf("thunder invite user select invite mode: %w", err)
	}
	if err := unmarshalStep(body, "select invite mode"); err != nil {
		return "", err
	}
	challengeToken = flowStep.ChallengeToken

	// Step 5: submit the invitee's email address.
	// TODO: The groups input still appears in the invitee onboarding form. It must be hidden and
	// left empty — supplying any value for groups breaks user signup completion. Needs a fix to
	// suppress the groups field from the form while keeping its value empty for provisioning.
	body, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/flow/execute", token,
		map[string]any{
			"executionId":    execID,
			"challengeToken": challengeToken,
			"inputs":         map[string]string{"email": email},
			"verbose":        true,
			"action":         "action_submit_email",
		})
	if err != nil {
		return "", fmt.Errorf("thunder invite user submit email: %w", err)
	}
	if err := unmarshalStep(body, "submit email"); err != nil {
		return "", err
	}
	challengeToken = flowStep.ChallengeToken

	// Step 6: request a manually-shareable invite link.
	// action_send_email_invite produces a link that only works via email delivery;
	// action_share_manually produces a link that can be pasted directly in the browser.
	body, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/flow/execute", token,
		map[string]any{
			"executionId":    execID,
			"challengeToken": challengeToken,
			"verbose":        true,
			"action":         "action_share_manually",
		})
	if err != nil {
		return "", fmt.Errorf("thunder invite user share link: %w", err)
	}

	// Parse the final response and walk all known locations where Thunder may embed the link.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("thunder invite user share link decode: %w", err)
	}

	link := extractInviteLink(raw)
	if link == "" {
		return "", fmt.Errorf("thunder invite user: inviteLink not found in response: %s", string(body))
	}
	return link, nil
}

// extractInviteLink walks common Thunder flow response shapes looking for inviteLink.
func extractInviteLink(m map[string]any) string {
	// Candidate key names Thunder might use for the invite link.
	linkKeys := []string{"inviteLink", "invite_link", "link", "invitationLink"}

	// Check a map for any of the candidate keys.
	findLink := func(src map[string]any) string {
		for _, k := range linkKeys {
			if v, ok := src[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}

	// Top-level link field.
	if link := findLink(m); link != "" {
		return link
	}

	// Top-level additionalData / additionalInfo.
	for _, adKey := range []string{"additionalData", "additionalInfo"} {
		if ad, ok := m[adKey].(map[string]any); ok {
			if link := findLink(ad); link != "" {
				return link
			}
		}
	}

	// One level of wrapping (data / output / result / response).
	for _, wrapKey := range []string{"data", "output", "result", "response"} {
		if nested, ok := m[wrapKey].(map[string]any); ok {
			if link := findLink(nested); link != "" {
				return link
			}
			for _, adKey := range []string{"additionalData", "additionalInfo"} {
				if ad, ok := nested[adKey].(map[string]any); ok {
					if link := findLink(ad); link != "" {
						return link
					}
				}
			}
		}
	}
	return ""
}

// --- HTTP helper ---

// doRequest executes an authenticated HTTP request and returns the response body.
// For DELETE responses with no content it returns nil without error.
func (c *thunderClient) doRequest(ctx context.Context, method, url, token string, payload any) ([]byte, error) {
	var reqBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{Message: string(body)}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// NotFoundError is returned when Thunder responds with 404.
type NotFoundError struct {
	Message string
}

func (e *NotFoundError) Error() string {
	return "not found: " + e.Message
}

// IsNotFound returns true if the error is a Thunder 404.
func IsNotFound(err error) bool {
	var nfe *NotFoundError
	return errors.As(err, &nfe)
}

// ListChildOUs returns the direct child OUs of the given parent OU ID.
func (c *thunderClient) ListChildOUs(ctx context.Context, parentOUID string, limit, offset int) ([]ThunderOU, int, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	url := fmt.Sprintf("%s/organization-units/%s/ous?limit=%d&offset=%d", c.baseURL, parentOUID, limit, offset)
	body, err := c.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("thunder list child ous: %w", err)
	}
	var wrapped thunderChildOUList
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, 0, fmt.Errorf("thunder list child ous decode: %w", err)
	}
	return wrapped.OrganizationUnits, wrapped.TotalResults, nil
}

// GetOUIDByHandle returns the Thunder OU ID for the given org handle by calling
// GET /organization-units/tree/{handle}. The result should be cached by callers.
func (c *thunderClient) GetOUIDByHandle(ctx context.Context, handle string) (string, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return "", err
	}
	body, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/organization-units/tree/"+handle, token, nil)
	if err != nil {
		return "", fmt.Errorf("thunder get ou by handle %q: %w", handle, err)
	}
	var ou struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ou); err != nil {
		return "", fmt.Errorf("thunder get ou by handle %q decode: %w", handle, err)
	}
	if ou.ID == "" {
		return "", fmt.Errorf("thunder ou with handle %q returned no id", handle)
	}
	return ou.ID, nil
}
