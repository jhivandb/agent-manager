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

package rbac

const (
	RoleAdmin            = "Agent Manager Admin"
	RoleDeveloper        = "Developer"
	RoleAILead           = "AI Lead"
	RolePlatformEngineer = "Platform Engineer"
)

// PredefinedRolePermissions maps each predefined role name to its set of permissions.
// Used at bootstrap time to register role→scope bindings on the Thunder resource server.
var PredefinedRolePermissions = map[string][]Permission{
	RoleAdmin: {
		OrgView, OrgModifySettings, OrgInviteMember, OrgRemoveMember,
		OrgAssignRole, OrgManageIDP, OrgManageServiceAccount,
		ProjectCreate, ProjectRead, ProjectUpdate, ProjectDelete,
		EnvironmentCreate, EnvironmentRead, EnvironmentUpdate, EnvironmentDelete,
		GatewayCreate, GatewayRead, GatewayUpdate, GatewayDelete, GatewayTokenManage,
		DataPlaneRead, DeploymentPipelineRead, DeploymentPipelineCreate, DeploymentPipelineUpdate, DeploymentPipelineDelete,
		GitSecretCreate, GitSecretRead, GitSecretDelete,
		LLMProviderTemplateCreate, LLMProviderTemplateRead, LLMProviderTemplateUpdate, LLMProviderTemplateDelete,
		LLMProviderCreate, LLMProviderRead, LLMProviderUpdate, LLMProviderDelete,
		LLMProviderConfigureGuardrail, LLMProviderConnect, LLMProviderDeploy, LLMProviderAPIKeyManage,
		MCPServerCreate, MCPServerRead, MCPServerUpdate, MCPServerDelete,
		MCPServerConfigureGuardrail, MCPServerConnect, MCPServerAPIKeyManage,
		ScopeCreate, ScopeRead, ScopeUpdate, ScopeDelete,
		LLMProxyCreate, LLMProxyRead, LLMProxyUpdate, LLMProxyDelete, LLMProxyDeploy, LLMProxyAPIKeyManage,
		EvaluatorCreate, EvaluatorRead, EvaluatorUpdate, EvaluatorDelete,
		AgentKindCreate, AgentKindRead, AgentKindUpdate, AgentKindDelete,
		AgentCreate, AgentRead, AgentUpdate, AgentDelete, AgentBuild,
		AgentDeployNonProduction, AgentDeployProduction, AgentPromote, AgentRollback, AgentSuspend,
		AgentTokenManage, AgentAPIKeyManage,
		MonitorCreate, MonitorRead, MonitorUpdate, MonitorDelete, MonitorExecute,
		MonitorScoreRead, MonitorScorePublish,
		ObservabilityOrgDashboard, ObservabilityProjectDashboard,
		ObservabilityGuardrailMetric, ObservabilityInfraMetric,
		RoleCreate, RoleRead, RoleUpdate, RoleDelete,
		GroupCreate, GroupRead, GroupUpdate, GroupDelete,
		CatalogRead, RepositoryRead,
		ProfileRead, ProfileUpdateAttributes,
	},

	RoleDeveloper: {
		OrgView,
		ProjectCreate, ProjectRead, ProjectUpdate, ProjectDelete,
		EnvironmentRead,
		DataPlaneRead, DeploymentPipelineRead,
		GitSecretCreate, GitSecretRead, GitSecretDelete,
		LLMProviderTemplateRead,
		LLMProviderRead, LLMProviderConfigureGuardrail, LLMProviderConnect,
		MCPServerRead, MCPServerConfigureGuardrail, MCPServerConnect, MCPServerAPIKeyManage,
		ScopeRead,
		LLMProxyCreate, LLMProxyRead, LLMProxyUpdate, LLMProxyDelete,
		LLMProxyDeploy, LLMProxyAPIKeyManage,
		EvaluatorRead,
		AgentKindRead,
		AgentCreate, AgentRead, AgentUpdate, AgentDelete, AgentBuild,
		AgentDeployNonProduction, AgentTokenManage, AgentAPIKeyManage,
		MonitorCreate, MonitorRead, MonitorUpdate, MonitorDelete, MonitorExecute,
		MonitorScoreRead,
		ObservabilityProjectDashboard, ObservabilityInfraMetric,
		CatalogRead, RepositoryRead,
		ProfileRead, ProfileUpdateAttributes,
	},

	RoleAILead: {
		OrgView,
		ProjectRead,
		EnvironmentRead,
		DataPlaneRead, DeploymentPipelineRead,
		LLMProviderTemplateCreate, LLMProviderTemplateRead, LLMProviderTemplateUpdate, LLMProviderTemplateDelete,
		LLMProviderCreate, LLMProviderRead, LLMProviderUpdate, LLMProviderDelete,
		LLMProviderConfigureGuardrail, LLMProviderConnect, LLMProviderDeploy, LLMProviderAPIKeyManage,
		MCPServerCreate, MCPServerRead, MCPServerUpdate, MCPServerDelete,
		MCPServerConfigureGuardrail, MCPServerConnect, MCPServerAPIKeyManage,
		ScopeCreate, ScopeRead, ScopeUpdate, ScopeDelete,
		EvaluatorCreate, EvaluatorRead, EvaluatorUpdate, EvaluatorDelete,
		AgentKindRead,
		AgentRead, AgentBuild, AgentDeployNonProduction, AgentAPIKeyManage,
		MonitorRead, MonitorScoreRead,
		ObservabilityOrgDashboard, ObservabilityProjectDashboard, ObservabilityGuardrailMetric,
		CatalogRead,
		ProfileRead, ProfileUpdateAttributes,
	},

	RolePlatformEngineer: {
		OrgView,
		ProjectRead,
		EnvironmentCreate, EnvironmentRead, EnvironmentUpdate, EnvironmentDelete,
		GatewayCreate, GatewayRead, GatewayUpdate, GatewayDelete, GatewayTokenManage,
		DataPlaneRead, DeploymentPipelineRead, DeploymentPipelineCreate, DeploymentPipelineUpdate, DeploymentPipelineDelete,
		LLMProviderRead, LLMProviderConfigureGuardrail,
		MCPServerRead, MCPServerConfigureGuardrail,
		ScopeRead,
		AgentKindRead,
		AgentRead, AgentBuild, AgentAPIKeyManage,
		AgentDeployNonProduction, AgentDeployProduction, AgentPromote, AgentRollback, AgentSuspend,
		MonitorRead, MonitorScoreRead,
		ObservabilityOrgDashboard, ObservabilityProjectDashboard, ObservabilityInfraMetric,
		CatalogRead,
		ProfileRead, ProfileUpdateAttributes,
	},
}
