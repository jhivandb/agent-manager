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

package api

import (
	"github.com/wso2/agent-manager/agent-manager-service/controllers"
	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
)

func registerAgentIdentityRoutes(rr *middleware.RouteRegistrar, ctrl controllers.AgentIdentityController) {
	// Groups
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/groups", rbac.AgentIdentityRead, ctrl.ListGroups)
	rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/environments/{envName}/agent-identities/groups", rbac.AgentIdentityCreate, ctrl.CreateGroup)
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/groups/{groupID}", rbac.AgentIdentityRead, ctrl.GetGroup)
	rr.HandleFuncWithValidationAndAuthz("PUT /orgs/{orgName}/environments/{envName}/agent-identities/groups/{groupID}", rbac.AgentIdentityUpdate, ctrl.UpdateGroup)
	rr.HandleFuncWithValidationAndAuthz("DELETE /orgs/{orgName}/environments/{envName}/agent-identities/groups/{groupID}", rbac.AgentIdentityDelete, ctrl.DeleteGroup)
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/groups/{groupID}/members", rbac.AgentIdentityRead, ctrl.GetGroupMembers)
	rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/environments/{envName}/agent-identities/groups/{groupID}/members/add", rbac.AgentIdentityUpdate, ctrl.AddGroupMembers)
	rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/environments/{envName}/agent-identities/groups/{groupID}/members/remove", rbac.AgentIdentityUpdate, ctrl.RemoveGroupMembers)
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/groups/{groupID}/roles", rbac.AgentIdentityRead, ctrl.GetGroupRoles)

	// Roles
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/roles", rbac.AgentIdentityRead, ctrl.ListRoles)
	rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/environments/{envName}/agent-identities/roles", rbac.AgentIdentityCreate, ctrl.CreateRole)
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/roles/{roleID}", rbac.AgentIdentityRead, ctrl.GetRole)
	rr.HandleFuncWithValidationAndAuthz("PUT /orgs/{orgName}/environments/{envName}/agent-identities/roles/{roleID}", rbac.AgentIdentityUpdate, ctrl.UpdateRole)
	rr.HandleFuncWithValidationAndAuthz("DELETE /orgs/{orgName}/environments/{envName}/agent-identities/roles/{roleID}", rbac.AgentIdentityDelete, ctrl.DeleteRole)
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/roles/{roleID}/assignments", rbac.AgentIdentityRead, ctrl.GetRoleAssignments)
	rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/environments/{envName}/agent-identities/roles/{roleID}/assignments/add", rbac.AgentIdentityUpdate, ctrl.AddRoleAssignees)
	rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/environments/{envName}/agent-identities/roles/{roleID}/assignments/remove", rbac.AgentIdentityUpdate, ctrl.RemoveRoleAssignees)

	// Agents picker
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/environments/{envName}/agent-identities/agents", rbac.AgentIdentityRead, ctrl.ListAgents)
}
