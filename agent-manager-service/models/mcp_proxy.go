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

package models

import (
	"time"

	"github.com/google/uuid"
)

// MCPProxy represents an MCP proxy entity.
type MCPProxy struct {
	UUID          uuid.UUID      `gorm:"column:uuid;primaryKey" json:"uuid"`
	Description   string         `gorm:"column:description" json:"description,omitempty"`
	CreatedBy     string         `gorm:"column:created_by" json:"createdBy,omitempty"`
	Status        string         `gorm:"column:status" json:"status"`
	Configuration MCPProxyConfig `gorm:"column:configuration;type:jsonb;serializer:json" json:"configuration"`

	Artifact *Artifact `gorm:"foreignKey:UUID;references:UUID" json:"artifact,omitempty"`

	OrganizationName string    `gorm:"-" json:"organizationName,omitempty"`
	ID               string    `gorm:"-" json:"id,omitempty"`
	Name             string    `gorm:"-" json:"name,omitempty"`
	Handle           string    `gorm:"-" json:"handle,omitempty"`
	Version          string    `gorm:"-" json:"version,omitempty"`
	CreatedAt        time.Time `gorm:"-" json:"createdAt,omitempty"`
	UpdatedAt        time.Time `gorm:"-" json:"updatedAt,omitempty"`
}

// TableName returns the table name for the MCPProxy model.
func (MCPProxy) TableName() string {
	return "mcp_proxies"
}

// MCPProxyMapping represents an agent-scoped MCP proxy mapping deployment.
type MCPProxyMapping struct {
	UUID               uuid.UUID      `gorm:"column:uuid;primaryKey" json:"uuid"`
	SourceMCPProxyUUID uuid.UUID      `gorm:"column:source_mcp_proxy_uuid" json:"sourceMcpProxyUuid"`
	Description        string         `gorm:"column:description" json:"description,omitempty"`
	Status             string         `gorm:"column:status" json:"status"`
	Configuration      MCPProxyConfig `gorm:"column:configuration;type:jsonb;serializer:json" json:"configuration"`

	Artifact       *Artifact `gorm:"foreignKey:UUID;references:UUID" json:"artifact,omitempty"`
	SourceMCPProxy *MCPProxy `gorm:"foreignKey:SourceMCPProxyUUID;references:UUID" json:"sourceMcpProxy,omitempty"`
}

// TableName returns the table name for the MCPProxyMapping model.
func (MCPProxyMapping) TableName() string {
	return "mcp_proxy_mappings"
}

// MCPProxyConfig represents the MCP proxy configuration stored in JSON.
//
// The config carries two shapes. The source org-level MCPProxy is a blueprint: it
// populates Environments (keyed by environment UUID) and leaves the flat root-level
// Upstream/Policies/Capabilities/Security empty — it deploys nothing. An agent-scoped
// MCPProxyMapping is an actual gateway-deployable entity: it populates the flat
// root-level fields (flattened from the selected environment's blueprint block by
// buildAgentMCPConfigProxy) and leaves Environments empty. The deployment YAML builder
// reads only the flat root-level fields.
type MCPProxyConfig struct {
	Name         string                          `json:"name,omitempty"`
	Version      string                          `json:"version,omitempty"`
	Context      *string                         `json:"context,omitempty"`
	Vhost        *string                         `json:"vhost,omitempty"`
	SpecVersion  string                          `json:"specVersion,omitempty"`
	Upstream     UpstreamConfig                  `json:"upstream,omitempty"`
	Policies     []MCPPolicy                     `json:"policies,omitempty"`
	Capabilities *MCPProxyCapabilities           `json:"capabilities,omitempty"`
	Security     *SecurityConfig                 `json:"security,omitempty"`
	// ToolScopeBindings is the flat root-level copy populated only on flattened
	// per-environment deployable artifacts (mirrors the Security duality); on a
	// source MCPProxy the bindings live per-environment in Environments.
	ToolScopeBindings []MCPToolScopeBinding           `json:"toolScopeBindings,omitempty"`
	Environments      map[string]MCPEnvironmentConfig `json:"environments,omitempty"`
}

// MCPToolScopeBinding binds catalog scopes to one MCP tool in one environment.
// Scope names reference the org-global scopes catalog by name.
type MCPToolScopeBinding struct {
	Tool   string   `json:"tool"`
	Scopes []string `json:"scopes"`
}

// MCPEnvironmentConfig is one per-environment blueprint block on a source MCPProxy,
// stored in MCPProxyConfig.Environments keyed by environment UUID. Upstream holds the
// single backend endpoint (URL + auth) for that environment.
//
// ArtifactUUID is the stable identity of the single gateway artifact this environment
// deploys. The MCP proxy owns and deploys exactly one artifact per configured
// environment (to that environment's gateway); it is generated when the block is first
// stored and preserved across updates. Agent MCP configurations do not deploy their own
// artifacts — they reference this shared per-environment artifact: its context becomes
// the injected proxy URL, and per-agent inbound API keys are minted against it (as the
// gateway-facing apiID) so the gateway validates them against the shared deployment.
type MCPEnvironmentConfig struct {
	ArtifactUUID *uuid.UUID            `json:"artifactUuid,omitempty"`
	Upstream     *UpstreamEndpoint     `json:"upstream,omitempty"`
	Policies     []MCPPolicy           `json:"policies,omitempty"`
	Capabilities *MCPProxyCapabilities `json:"capabilities,omitempty"`
	Security     *SecurityConfig       `json:"security,omitempty"`

	// ToolScopeBindings binds catalog scopes to this environment's MCP tools.
	// Only meaningful when Security selects the Agent Identity variant.
	ToolScopeBindings []MCPToolScopeBinding `json:"toolScopeBindings,omitempty"`

	// DeploymentStatus is a response-only indicator of whether this environment's single
	// gateway artifact is currently deployed ("Deployed") or not ("Undeployed"). It is
	// computed on read and never persisted (buildMCPEnvironmentsForStorage rebuilds blocks
	// without it).
	DeploymentStatus string `json:"deploymentStatus,omitempty"`
}

// MCP proxy per-environment deployment status values reported on read.
const (
	MCPDeploymentStatusDeployed   = "Deployed"
	MCPDeploymentStatusUndeployed = "Undeployed"
)

// MCPPolicy represents a policy attached to an MCP proxy.
type MCPPolicy struct {
	Name               string                 `json:"name" yaml:"name"`
	Version            string                 `json:"version" yaml:"version"`
	DisplayName        string                 `json:"displayName,omitempty" yaml:"displayName,omitempty"`
	ExecutionCondition *string                `json:"executionCondition,omitempty" yaml:"executionCondition,omitempty"`
	Params             map[string]interface{} `json:"params,omitempty" yaml:"params,omitempty"`
}

// MCPProxyCapabilities contains the MCP server capabilities discovered from the upstream.
type MCPProxyCapabilities struct {
	Prompts   *[]map[string]interface{} `json:"prompts,omitempty"`
	Resources *[]map[string]interface{} `json:"resources,omitempty"`
	Tools     *[]map[string]interface{} `json:"tools,omitempty"`
}

// MCPProxyDTO is the request/response body for an MCP proxy.
type MCPProxyDTO struct {
	Capabilities   *MCPProxyCapabilities           `json:"capabilities,omitempty"`
	Context        *string                         `json:"context,omitempty"`
	CreatedAt      *time.Time                      `json:"createdAt,omitempty"`
	CreatedBy      *string                         `json:"createdBy,omitempty"`
	Description    *string                         `json:"description,omitempty"`
	Gateways       []string                        `json:"gateways,omitempty"`
	ID             string                          `json:"id"`
	InCatalog      bool                            `json:"inCatalog"`
	McpSpecVersion *string                         `json:"mcpSpecVersion,omitempty"`
	Name           string                          `json:"name"`
	Policies       *[]MCPPolicy                    `json:"policies,omitempty"`
	Security       *SecurityConfig                 `json:"security,omitempty"`
	Upstream       UpstreamConfig                  `json:"upstream"`
	Environments   map[string]MCPEnvironmentConfig `json:"environments,omitempty"`
	UpdatedAt      *time.Time                      `json:"updatedAt,omitempty"`
	Version        string                          `json:"version"`
	Vhost          *string                         `json:"vhost,omitempty"`
}

// MCPPolicyAvailabilityResponse lists MCP policies reported by active gateways.
type MCPPolicyAvailabilityResponse struct {
	Count int32                    `json:"count"`
	List  []MCPPolicyAvailableItem `json:"list"`
}

// MCPPolicyAvailableItem identifies one gateway-installed policy version.
type MCPPolicyAvailableItem struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCPProxyListItem is the list representation for an MCP proxy.
type MCPProxyListItem struct {
	Context        *string    `json:"context,omitempty"`
	CreatedAt      *time.Time `json:"createdAt,omitempty"`
	CreatedBy      *string    `json:"createdBy,omitempty"`
	Description    *string    `json:"description,omitempty"`
	ID             *string    `json:"id,omitempty"`
	McpSpecVersion *string    `json:"mcpSpecVersion,omitempty"`
	Name           *string    `json:"name,omitempty"`
	Status         *string    `json:"status,omitempty"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
	Version        *string    `json:"version,omitempty"`
}

// MCPProxyListResponse is the response body for listing MCP proxies.
type MCPProxyListResponse struct {
	Count      int                `json:"count"`
	List       []MCPProxyListItem `json:"list"`
	Pagination PaginationInfo     `json:"pagination"`
}

// MCPServerInfoFetchRequest is the request body for fetching MCP server details.
type MCPServerInfoFetchRequest struct {
	Auth    *UpstreamAuth `json:"auth,omitempty"`
	ProxyID *string       `json:"proxyId,omitempty"`
	URL     *string       `json:"url,omitempty"`
}

// MCPServerInfoFetchResponse contains the MCP server metadata and capabilities.
type MCPServerInfoFetchResponse struct {
	Prompts    *[]map[string]interface{} `json:"prompts,omitempty"`
	Resources  *[]map[string]interface{} `json:"resources,omitempty"`
	ServerInfo *map[string]interface{}   `json:"serverInfo,omitempty"`
	Tools      *[]map[string]interface{} `json:"tools,omitempty"`
}
