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

package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const (
	apiVersionMCPProxy              = "gateway.api-platform.wso2.com/v1"
	kindMCPProxy                    = "Mcp"
	mcpSetHeadersPolicyName         = "set-headers"
	mcpRemoveHeadersPolicyName      = "remove-headers"
	mcpLogMessagePolicyName         = "log-message"
	mcpBackendAuthPolicyName        = mcpSetHeadersPolicyName
	mcpBackendAuthPolicyVersion     = "v1"
	mcpBackendAuthPolicyDisplayName = "Backend Authentication Header"
	mcpAuthPolicyName               = "mcp-auth"
	mcpAuthPolicyVersion            = "v1"
	mcpAuthzPolicyName              = "mcp-authz"
	mcpAuthzPolicyVersion           = "v1"
	mcpIdentityIssuerKeyManager     = "ThunderKeyManager"
)

type mcpPolicyMergeStrategy string

const (
	mcpPolicyMergeStrategyMerge    mcpPolicyMergeStrategy = "merge"
	mcpPolicyMergeStrategyOverride mcpPolicyMergeStrategy = "override"
)

var mcpPolicyMergeStrategies = map[string]mcpPolicyMergeStrategy{
	mcpSetHeadersPolicyName:    mcpPolicyMergeStrategyMerge,
	mcpRemoveHeadersPolicyName: mcpPolicyMergeStrategyMerge,
	mcpLogMessagePolicyName:    mcpPolicyMergeStrategyMerge,
}

// MCPProxyDeploymentYAML represents the deployment YAML consumed by the gateway.
type MCPProxyDeploymentYAML struct {
	ApiVersion string                 `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                 `yaml:"kind" json:"kind"`
	Metadata   DeploymentMetadata     `yaml:"metadata" json:"metadata"`
	Spec       MCPProxyDeploymentSpec `yaml:"spec" json:"spec"`
}

// MCPProxyDeploymentSpec represents the deployment spec section.
type MCPProxyDeploymentSpec struct {
	DisplayName string             `yaml:"displayName" json:"displayName"`
	Version     string             `yaml:"version" json:"version"`
	Context     string             `yaml:"context" json:"context"`
	Vhost       *string            `yaml:"vhost" json:"vhost"`
	Upstream    MCPProxyUpstream   `yaml:"upstream" json:"upstream"`
	Resilience  *models.Resilience `yaml:"resilience,omitempty" json:"resilience,omitempty"`
	SpecVersion string             `yaml:"specVersion" json:"specVersion"`
	Policies    []models.MCPPolicy `yaml:"policies,omitempty" json:"policies,omitempty"`
}

// MCPProxyUpstream represents the flat upstream shape expected by the gateway.
type MCPProxyUpstream struct {
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	Ref string `yaml:"ref,omitempty" json:"ref,omitempty"`
}

// mcpProxyEnvArtifactHandle builds the stable, org-unique handle/name for the single
// gateway artifact an MCP proxy endpoint deploys for one environment. The org-level proxy
// handle is unique per org; the endpoint handle (unique within the proxy) and the
// environment-UUID suffix distinguish the per-(endpoint,environment) artifacts, so the
// triple satisfies the artifacts table's UNIQUE(handle, org) constraint.
func mcpProxyEnvArtifactHandle(proxyHandle, endpointHandle, envID string) string {
	suffix := strings.ReplaceAll(strings.TrimSpace(envID), "-", "")
	return fmt.Sprintf("%s-%s-%s", proxyHandle, strings.TrimSpace(endpointHandle), suffix)
}

// buildMCPProxyEnvArtifact flattens one endpoint's config into the flat, single-environment
// MCPProxy that the deployment YAML builder consumes. Unlike the agent-scoped mapping
// (buildAgentMCPConfigProxy) this is the proxy's OWN artifact: the context/version/vhost
// inherit from the parent proxy's shared metadata — shared by every agent that references
// it — and the artifact identity is the stable (endpoint, environment) ArtifactUUID.
func buildMCPProxyEnvArtifact(source *models.MCPProxy, endpoint *models.MCPProxyEndpoint, ee *models.MCPProxyEndpointEnvironment) *models.MCPProxy {
	proxyHandle := source.Handle
	if source.Artifact != nil && source.Artifact.Handle != "" {
		proxyHandle = source.Artifact.Handle
	}
	handle := mcpProxyEnvArtifactHandle(proxyHandle, endpoint.Handle, ee.EnvironmentUUID.String())

	version := source.Version
	if source.Artifact != nil && source.Artifact.Version != "" {
		version = source.Artifact.Version
	}
	if version == "" {
		version = source.Configuration.Version
	}

	cfg := endpoint.Configuration
	var upstream models.UpstreamConfig
	if cfg.Upstream != nil {
		endpointCopy := *cfg.Upstream
		upstream.Main = &endpointCopy
	}

	return &models.MCPProxy{
		UUID:        ee.ArtifactUUID,
		Description: source.Description,
		Status:      source.Status,
		Configuration: models.MCPProxyConfig{
			Name:         handle,
			Version:      version,
			Context:      source.Configuration.Context,
			Vhost:        source.Configuration.Vhost,
			SpecVersion:  source.Configuration.SpecVersion,
			Upstream:     upstream,
			Policies:     cfg.Policies,
			Capabilities: cfg.Capabilities,
			Security:     cfg.Security,
		},
		OrganizationName: source.OrganizationName,
		ID:               handle,
		Name:             handle,
		Handle:           handle,
		Version:          version,
	}
}

// ensureMCPProxyEnvArtifactRow creates the artifacts row backing a per-environment
// gateway artifact when it does not already exist. The deployments and deployment_status
// foreign keys require the row to be present before a deployment can be recorded. It is a
// no-op when the row already exists (the ArtifactUUID is stable across proxy updates).
func (s *MCPProxyService) ensureMCPProxyEnvArtifactRow(deployProxy *models.MCPProxy, ouID string) error {
	if s.artifactRepo == nil {
		return nil
	}
	// The artifacts row is keyed by its stable UUID (deployProxy.UUID == the join row's
	// ArtifactUUID). Check existence by UUID, not handle: a preserved artifact whose
	// environment remapped to a different endpoint carries a new handle but the same UUID,
	// so a handle lookup would miss and re-create would hit a primary-key collision.
	if _, err := s.artifactRepo.GetByUUID(deployProxy.UUID.String(), ouID); err == nil {
		return nil
	} else if !errors.Is(err, utils.ErrArtifactNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to check MCP proxy environment artifact: %w", err)
	}
	now := time.Now()
	return s.db.Transaction(func(tx *gorm.DB) error {
		return s.artifactRepo.Create(tx, &models.Artifact{
			UUID:      deployProxy.UUID,
			Handle:    deployProxy.Handle,
			Name:      deployProxy.Name,
			Version:   deployProxy.Version,
			Kind:      models.KindMCPMapping,
			OUID:      ouID,
			CreatedAt: now,
			UpdatedAt: now,
		})
	})
}

// deployMCPProxyEndpoints deploys (or refreshes) the single gateway artifact for every
// (endpoint, environment) binding of the proxy. An environment with no gateway is skipped (it
// deploys on the next update once a gateway exists), mirroring the agent-config flow.
// Best-effort: per-(endpoint,environment) failures are aggregated and returned.
func (s *MCPProxyService) deployMCPProxyEndpoints(ctx context.Context, proxy *models.MCPProxy, ouID string) error {
	return s.eachMCPBinding(proxy, func(endpoint *models.MCPProxyEndpoint, ee *models.MCPProxyEndpointEnvironment) error {
		return s.deployMCPBinding(ctx, proxy, endpoint, ee, ouID)
	})
}

// eachMCPBinding runs fn for every (endpoint, environment) binding of the proxy and joins the
// failures, each named by its binding.
func (s *MCPProxyService) eachMCPBinding(
	proxy *models.MCPProxy, fn func(endpoint *models.MCPProxyEndpoint, ee *models.MCPProxyEndpointEnvironment) error,
) error {
	var errs []error
	for i := range proxy.Endpoints {
		endpoint := &proxy.Endpoints[i]
		for j := range endpoint.Environments {
			ee := &endpoint.Environments[j]
			if ee.ArtifactUUID == uuid.Nil {
				errs = append(errs, fmt.Errorf("endpoint %q environment %q: missing artifact id", endpoint.Handle, ee.EnvironmentUUID))
				continue
			}
			if err := fn(endpoint, ee); err != nil {
				errs = append(errs, fmt.Errorf("endpoint %q environment %q: %w", endpoint.Handle, ee.EnvironmentUUID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// deployMCPBinding records the binding's intent and carries it out in one transaction: its
// egress gateway is resolved, anchored on the one the artifact is already placed on, and the
// artifact rendered from the proxy's current state. Its event goes out after commit. Scopes are
// per proxy: every binding emits the same scope->tool policies, prefixed with the source proxy's
// handle, never the per-environment artifact's.
func (s *MCPProxyService) deployMCPBinding(
	ctx context.Context, proxy *models.MCPProxy, endpoint *models.MCPProxyEndpoint, ee *models.MCPProxyEndpointEnvironment, ouID string,
) error {
	deployProxy := buildMCPProxyEnvArtifact(proxy, endpoint, ee)
	if err := s.ensureMCPProxyEnvArtifactRow(deployProxy, ouID); err != nil {
		return err
	}
	scopes, err := s.mcpProxyScopeRepo.ListByProxy(ctx, proxy.UUID)
	if err != nil {
		return fmt.Errorf("failed to load proxy scopes for emission: %w", err)
	}
	content, err := s.generateMCPProxyDeploymentYAML(deployProxy, mcpSourceHandle(proxy), scopes)
	if err != nil {
		return fmt.Errorf("failed to generate deployment YAML: %w", err)
	}
	intent, err := mcpIntent(ouID, proxy.UUID, ee)
	if err != nil {
		return err
	}
	// Rendered from the proxy in hand, as the kind would from the stored one.
	applied, err := s.deploys.Apply(ctx, nil, &intent, &reconcile.Override{Name: deploymentName(deployProxy), Content: []byte(content)})
	if errors.Is(err, errNoGatewayForEnvironment) {
		s.logger.Info("Skipping MCP proxy endpoint deploy; no gateway mapped to environment",
			"proxyUUID", proxy.UUID, "endpoint", endpoint.Handle, "environment", ee.EnvironmentUUID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	applied.Publish(ctx)
	return nil
}

// RedeployMCPProxy re-renders the proxy's gateway artifacts for every bound (endpoint,
// environment), because the proxy's scopes changed and its mcp-authz policies with them. A
// binding never deployed, e.g. for want of a gateway then, is deployed now.
func (s *MCPProxyService) RedeployMCPProxy(ctx context.Context, proxy *models.MCPProxy, ouID string) error {
	return s.eachMCPBinding(proxy, func(endpoint *models.MCPProxyEndpoint, ee *models.MCPProxyEndpointEnvironment) error {
		err := s.deploys.MarkStale(ctx, models.KindMCPProxy, ouID, ee.ArtifactUUID,
			models.EnvironmentScope(ee.EnvironmentUUID), "the proxy's scopes changed")
		if errors.Is(err, repositories.ErrDeployIntentNotFound) {
			return s.deployMCPBinding(ctx, proxy, endpoint, ee, ouID)
		}
		return err
	})
}

// deleteMCPProxyEnvironmentArtifacts broadcast-deletes the given per-environment gateway
// artifacts and removes their artifacts rows (cascading to deployments / deployment_status
// via the FK). Used when environments are removed from a proxy and when the proxy itself
// is deleted. Best-effort: per-artifact failures are aggregated.
func (s *MCPProxyService) deleteMCPProxyEnvironmentArtifacts(ctx context.Context, artifactUUIDs []uuid.UUID, ouID string) error {
	var errs []error
	for _, artifactUUID := range artifactUUIDs {
		if artifactUUID == uuid.Nil {
			continue
		}
		s.BroadcastMCPArtifactDeletion(ctx, artifactUUID, ouID)
		if s.artifactRepo == nil {
			continue
		}
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, ouID).
				Delete(&models.DeploymentStatusRecord{}).Error; err != nil {
				return err
			}
			if err := tx.Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, ouID).
				Delete(&models.Deployment{}).Error; err != nil {
				return err
			}
			if err := s.artifactRepo.Delete(tx, artifactUUID.String()); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("artifact %s: %w", artifactUUID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *MCPProxyService) BroadcastMCPArtifactDeletion(ctx context.Context, artifactUUID uuid.UUID, ouID string) {
	proxy := &models.MCPProxy{UUID: artifactUUID}
	s.broadcastMCPProxyDeletion(ctx, proxy, s.gatewayIDsForDeletion(ctx, proxy, ouID))
}

func (s *MCPProxyService) gatewayIDsForDeletion(ctx context.Context, proxy *models.MCPProxy, ouID string) []string {
	_ = ctx
	if proxy == nil {
		return nil
	}
	gatewayIDs := map[string]struct{}{}
	if s.deploymentRepo != nil {
		deployedGatewayIDs, err := s.deploymentRepo.GetDeployedGatewaysByProvider(proxy.UUID, ouID)
		if err != nil {
			s.logger.Warn("Failed to get deployed gateways for MCP proxy deletion", "proxyID", proxy.UUID, "ouID", ouID, "error", err)
		}
		for _, gatewayID := range deployedGatewayIDs {
			if strings.TrimSpace(gatewayID) != "" {
				gatewayIDs[gatewayID] = struct{}{}
			}
		}
	}

	if s.gatewayRepo != nil {
		active := true
		gateways, err := s.gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
			OrganizationID: ouID,
			Status:         &active,
		})
		if err != nil {
			s.logger.Warn("Failed to get active gateways for MCP proxy deletion", "proxyID", proxy.UUID, "ouID", ouID, "error", err)
		}
		for _, gateway := range gateways {
			if gateway != nil {
				gatewayIDs[gateway.UUID.String()] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(gatewayIDs))
	for gatewayID := range gatewayIDs {
		out = append(out, gatewayID)
	}
	return out
}

func (s *MCPProxyService) broadcastMCPProxyDeletion(ctx context.Context, proxy *models.MCPProxy, gatewayIDs []string) {
	if proxy == nil {
		return
	}
	publishDeletion(ctx, s.gatewayEventsService, s.kind, models.DeployIntent{ArtifactUUID: proxy.UUID}, gatewayIDs)
}

func (s *MCPProxyService) generateMCPProxyDeploymentYAML(proxy *models.MCPProxy, proxyHandle string, scopes []models.MCPProxyScope) (string, error) {
	deployment, err := s.buildMCPProxyDeploymentYAML(proxy, proxyHandle, scopes)
	if err != nil {
		return "", err
	}
	yamlBytes, err := yaml.Marshal(deployment)
	if err != nil {
		return "", fmt.Errorf("failed to marshal MCP proxy deployment YAML: %w", err)
	}
	return string(yamlBytes), nil
}

func (s *MCPProxyService) buildMCPProxyDeploymentYAML(proxy *models.MCPProxy, proxyHandle string, scopes []models.MCPProxyScope) (*MCPProxyDeploymentYAML, error) {
	contextValue := "/"
	if proxy.Configuration.Context != nil && strings.TrimSpace(*proxy.Configuration.Context) != "" {
		contextValue = *proxy.Configuration.Context
	}

	specVersion := proxy.Configuration.SpecVersion
	if strings.TrimSpace(specVersion) == "" {
		specVersion = mcpProtocolVersion
	}

	upstream := MCPProxyUpstream{}
	var upstreamAuth *models.UpstreamAuth
	if proxy.Configuration.Upstream.Main != nil {
		upstream.URL = strings.TrimSpace(proxy.Configuration.Upstream.Main.URL)
		upstream.Ref = strings.TrimSpace(proxy.Configuration.Upstream.Main.Ref)
		upstreamAuth = proxy.Configuration.Upstream.Main.Auth
	}
	if upstream.URL == "" && upstream.Ref == "" {
		return nil, fmt.Errorf("upstream URL or ref is required")
	}
	policies, err := appendMCPAPIKeyAuthPolicy(proxy.Configuration.Policies, proxy.Configuration.Security)
	if err != nil {
		return nil, err
	}
	policies = appendMCPIdentityAuthPolicies(policies, proxy.Configuration.Security, proxyHandle, scopes)
	policies = appendMCPBackendAuthPolicy(policies, upstreamAuth)
	policies = mergeMCPPoliciesForDeployment(normalizeMCPPoliciesForDeployment(policies))
	handle := proxy.Handle
	displayName := proxy.Name
	version := proxy.Version
	if proxy.Artifact != nil {
		handle = proxy.Artifact.Handle
		displayName = proxy.Artifact.Name
		version = proxy.Artifact.Version
	}
	if displayName == "" {
		displayName = proxy.Configuration.Name
	}
	if version == "" {
		version = proxy.Configuration.Version
	}
	if handle == "" {
		handle = proxy.UUID.String()
	}

	return &MCPProxyDeploymentYAML{
		ApiVersion: apiVersionMCPProxy,
		Kind:       kindMCPProxy,
		Metadata:   DeploymentMetadata{Name: handle},
		Spec: MCPProxyDeploymentSpec{
			DisplayName: displayName,
			Version:     version,
			Context:     contextValue,
			Vhost:       proxy.Configuration.Vhost,
			Upstream:    upstream,
			Resilience:  proxy.Configuration.Resilience,
			SpecVersion: specVersion,
			Policies:    policies,
		},
	}, nil
}

func appendMCPBackendAuthPolicy(policies []models.MCPPolicy, auth *models.UpstreamAuth) []models.MCPPolicy {
	if auth == nil || auth.Header == nil || strings.TrimSpace(*auth.Header) == "" {
		return policies
	}

	header := strings.TrimSpace(*auth.Header)
	headerParam := map[string]interface{}{"name": header}
	switch {
	case auth.SecretRef != nil && *auth.SecretRef != "":
		headerParam["secretRef"] = *auth.SecretRef
	case auth.Value != nil && *auth.Value != "":
		headerParam["value"] = *auth.Value
	default:
		return policies
	}

	out := make([]models.MCPPolicy, 0, len(policies)+1)
	out = append(out, policies...)
	out = append(out, models.MCPPolicy{
		Name:        mcpBackendAuthPolicyName,
		Version:     mcpBackendAuthPolicyVersion,
		DisplayName: mcpBackendAuthPolicyDisplayName,
		Params: map[string]interface{}{
			"request": map[string]interface{}{
				"headers": []interface{}{headerParam},
			},
		},
	})
	return out
}

func appendMCPAPIKeyAuthPolicy(policies []models.MCPPolicy, security *models.SecurityConfig) ([]models.MCPPolicy, error) {
	if security == nil || !isBoolTrue(security.Enabled) {
		return policies, nil
	}
	if security.APIKey == nil || !isBoolTrue(security.APIKey.Enabled) {
		return policies, nil
	}

	key := strings.TrimSpace(security.APIKey.Key)
	if key == "" {
		return nil, fmt.Errorf("invalid api key security configuration: key is required")
	}

	in := strings.ToLower(strings.TrimSpace(security.APIKey.In))
	if in != "header" && in != "query" {
		return nil, fmt.Errorf("invalid api key security configuration: in must be 'header' or 'query', got %q", security.APIKey.In)
	}

	out := make([]models.MCPPolicy, 0, len(policies)+1)
	out = append(out, policies...)
	out = append(out, models.MCPPolicy{
		Name:    apiKeyAuthPolicyName,
		Version: apiKeyAuthPolicyVersion,
		Params: map[string]interface{}{
			"key": key,
			"in":  in,
		},
	})
	return out, nil
}

// appendMCPIdentityAuthPolicies emits the Agent Identity gateway policies for a
// flattened per-environment artifact: mcp-auth (JWT validation against the
// ThunderKeyManager key manager) and mcp-authz (per-tool requiredScopes
// enforcement). mcp-auth's requiredScopes is advertisement only — it becomes
// scopes_supported in the protected resource metadata and the scope hint in the
// WWW-Authenticate challenge, so clients learn what to request; mcp-auth strips
// it before delegating to jwt-auth (wso2/gateway-controllers commit 7045133,
// mcp-auth v1.1.1). Tools with no covering scope get no mcp-authz rule — gateway
// default-permit means authenticated-only.
func appendMCPIdentityAuthPolicies(policies []models.MCPPolicy, security *models.SecurityConfig, proxyHandle string, scopes []models.MCPProxyScope) []models.MCPPolicy {
	if !mcpIdentityEnabled(security) {
		return policies
	}

	// Invert scope->tools into one mcp-authz rule per tool. ONE rule per tool is
	// load-bearing: the gateway ANDs multiple matching rules together but ORs the
	// scopes inside a rule (checkScopes in wso2/gateway-controllers
	// policies/mcp-authz/mcp-authz.go:515 passes on the FIRST matching token
	// scope). A tool covered by several scopes must therefore carry them all in
	// a single rule's requiredScopes. NOTE: that policy's definition YAML
	// *describes* all-of semantics; the shipped code is any-of. Re-verify on any
	// gateway policy version bump.
	toolScopes := map[string][]string{}
	scopeSet := map[string]struct{}{}
	for _, sc := range scopes {
		str := sc.ScopeString(proxyHandle)
		scopeSet[str] = struct{}{}
		for _, tool := range sc.Tools {
			toolScopes[tool] = append(toolScopes[tool], str)
		}
	}
	toolNames := make([]string, 0, len(toolScopes))
	for tool := range toolScopes {
		toolNames = append(toolNames, tool)
	}
	sort.Strings(toolNames)
	toolRules := make([]map[string]interface{}, 0, len(toolNames))
	for _, tool := range toolNames {
		reqs := toolScopes[tool]
		sort.Strings(reqs)
		toolRules = append(toolRules, map[string]interface{}{"name": tool, "requiredScopes": reqs})
	}

	authParams := map[string]interface{}{
		"issuers": []interface{}{mcpIdentityIssuerKeyManager},
	}
	// Every scope of the proxy, including ones covering no tool: the union is what
	// a client should ask for, not what any single call enforces.
	if len(scopeSet) > 0 {
		union := make([]string, 0, len(scopeSet))
		for sc := range scopeSet {
			union = append(union, sc)
		}
		sort.Strings(union)
		authParams["requiredScopes"] = union
	}

	out := make([]models.MCPPolicy, 0, len(policies)+2)
	out = append(out, policies...)
	out = append(out, models.MCPPolicy{Name: mcpAuthPolicyName, Version: mcpAuthPolicyVersion, Params: authParams})
	if len(toolRules) > 0 {
		out = append(out, models.MCPPolicy{Name: mcpAuthzPolicyName, Version: mcpAuthzPolicyVersion, Params: map[string]interface{}{"tools": toolRules}})
	}
	return out
}

// mcpIdentityEnabled reports whether identity security is enabled for the given
// endpoint security config. Shared by gateway emission and Thunder scope
// cleanup so the identity gate can't drift between the two.
func mcpIdentityEnabled(security *models.SecurityConfig) bool {
	return security != nil && isBoolTrue(security.Enabled) &&
		security.Identity != nil && isBoolTrue(security.Identity.Enabled)
}

func normalizeMCPPoliciesForDeployment(policies []models.MCPPolicy) []models.MCPPolicy {
	if len(policies) == 0 {
		return nil
	}

	out := make([]models.MCPPolicy, 0, len(policies))
	for _, policy := range policies {
		out = append(out, models.MCPPolicy{
			Name:               policy.Name,
			Version:            normalizePolicyVersionToMajor(policy.Version),
			DisplayName:        policy.DisplayName,
			ExecutionCondition: policy.ExecutionCondition,
			Params:             policy.Params,
		})
	}
	return out
}

func mergeMCPPoliciesForDeployment(policies []models.MCPPolicy) []models.MCPPolicy {
	if len(policies) < 2 {
		return policies
	}

	out := make([]models.MCPPolicy, 0, len(policies))
	policyIndexes := map[string]int{}
	for _, policy := range policies {
		key := mcpPolicyIdentityKey(policy)
		existingIndex, ok := policyIndexes[key]
		if !ok {
			policyIndexes[key] = len(out)
			out = append(out, policy)
			continue
		}

		switch mcpPolicyMergeStrategyFor(policy.Name) {
		case mcpPolicyMergeStrategyMerge:
			out[existingIndex] = mergeMCPPolicyForDeployment(out[existingIndex], policy)
		default:
			out[existingIndex] = policy
		}
	}
	return out
}

func mcpPolicyIdentityKey(policy models.MCPPolicy) string {
	return strings.TrimSpace(policy.Name) + "\x00" + strings.TrimSpace(policy.Version)
}

func mcpPolicyMergeStrategyFor(policyName string) mcpPolicyMergeStrategy {
	if strategy, ok := mcpPolicyMergeStrategies[strings.TrimSpace(policyName)]; ok {
		return strategy
	}
	return mcpPolicyMergeStrategyOverride
}

func mergeMCPPolicyForDeployment(base, next models.MCPPolicy) models.MCPPolicy {
	merged := next
	merged.Params = mergeMCPPolicyParams(base.Params, next.Params)
	return merged
}

func mergeMCPPolicyParams(base, next map[string]interface{}) map[string]interface{} {
	if len(base) == 0 {
		return next
	}
	if len(next) == 0 {
		return base
	}

	out := cloneStringInterfaceMap(base)
	for key, nextValue := range next {
		baseValue, exists := out[key]
		if !exists {
			out[key] = nextValue
			continue
		}
		out[key] = mergeMCPPolicyParamValue(baseValue, nextValue)
	}
	return out
}

func mergeMCPPolicyParamValue(base, next interface{}) interface{} {
	baseMap, baseMapOK := base.(map[string]interface{})
	nextMap, nextMapOK := next.(map[string]interface{})
	if baseMapOK && nextMapOK {
		return mergeMCPPolicyParams(baseMap, nextMap)
	}

	baseBool, baseBoolOK := base.(bool)
	nextBool, nextBoolOK := next.(bool)
	if baseBoolOK && nextBoolOK {
		return baseBool || nextBool
	}

	if merged, ok := mergeStringSlices(base, next); ok {
		return merged
	}

	if merged, ok := mergeInterfaceSlices(base, next); ok {
		return merged
	}

	return next
}

func mergeStringSlices(base, next interface{}) (interface{}, bool) {
	baseStrings, baseOK := base.([]string)
	nextStrings, nextOK := next.([]string)
	if !baseOK || !nextOK {
		return nil, false
	}

	out := make([]string, 0, len(baseStrings)+len(nextStrings))
	seen := map[string]struct{}{}
	for _, value := range append(baseStrings, nextStrings...) {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, true
}

func mergeInterfaceSlices(base, next interface{}) (interface{}, bool) {
	baseItems, baseOK := base.([]interface{})
	nextItems, nextOK := next.([]interface{})
	if !baseOK || !nextOK {
		return nil, false
	}

	out := make([]interface{}, 0, len(baseItems)+len(nextItems))
	out = append(out, baseItems...)
	for _, item := range nextItems {
		if stringValue, ok := item.(string); ok && containsStringInterface(out, stringValue) {
			continue
		}
		out = append(out, item)
	}
	return out, true
}

func containsStringInterface(items []interface{}, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func cloneStringInterfaceMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func deploymentName(proxy *models.MCPProxy) string {
	if strings.TrimSpace(proxy.Handle) != "" {
		return fmt.Sprintf("%s-deployment", proxy.Handle)
	}
	if proxy.Artifact != nil && strings.TrimSpace(proxy.Artifact.Handle) != "" {
		return fmt.Sprintf("%s-deployment", proxy.Artifact.Handle)
	}
	return fmt.Sprintf("%s-deployment", proxy.UUID.String())
}
