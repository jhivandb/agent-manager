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
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const (
	apiVersionMCPProxy              = "gateway.api-platform.wso2.com/v1alpha1"
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
	SpecVersion string             `yaml:"specVersion" json:"specVersion"`
	Policies    []models.MCPPolicy `yaml:"policies,omitempty" json:"policies,omitempty"`
}

// MCPProxyUpstream represents the flat upstream shape expected by the gateway.
type MCPProxyUpstream struct {
	URL string `yaml:"url" json:"url"`
}

// deployMCPProxyToGateway deploys a single MCP artifact to one gateway. It is used by
// deployMCPProxyEnvironments for the proxy-owned per-environment artifacts and by the
// agent-configuration flow for agent-scoped mapping artifacts; callers pass the already
// flattened single-environment artifact to deploy.
func (s *MCPProxyService) deployMCPProxyToGateway(ctx context.Context, proxy *models.MCPProxy, orgName string, gateway *models.Gateway) error {
	_ = ctx
	deploymentYAML, err := s.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		return fmt.Errorf("failed to generate deployment YAML: %w", err)
	}

	deploymentID := uuid.New()
	deployed := models.DeploymentStatusDeployed
	deployment := &models.Deployment{
		DeploymentID:     deploymentID,
		Name:             deploymentName(proxy),
		ArtifactUUID:     proxy.UUID,
		OrganizationName: orgName,
		GatewayUUID:      gateway.UUID,
		Content:          []byte(deploymentYAML),
		Status:           &deployed,
	}

	hardLimit := maxDeploymentsPerAPI + deploymentLimitBuffer
	if err := s.deploymentRepo.CreateWithLimitEnforcement(deployment, hardLimit); err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	performedAt := time.Now().Truncate(time.Millisecond)
	event := &models.MCPProxyDeploymentEvent{
		ProxyID:      proxy.UUID.String(),
		DeploymentID: deployment.DeploymentID.String(),
		PerformedAt:  performedAt,
	}
	if err := s.gatewayEventsService.BroadcastMCPProxyDeploymentEvent(gateway.UUID.String(), event); err != nil {
		s.logger.Warn("Failed to broadcast MCP proxy deployment event",
			"proxyID", proxy.UUID, "deploymentID", deployment.DeploymentID, "gatewayID", gateway.UUID, "error", err)
		return fmt.Errorf("failed to broadcast MCP proxy deployment event for deployment %s: %w", deployment.DeploymentID, err)
	}
	return nil
}

// mcpProxyEnvArtifactHandle builds the stable, org-unique handle/name for the single
// gateway artifact an MCP proxy deploys for one environment. The org-level proxy handle
// is unique per org and the environment-UUID suffix distinguishes the per-environment
// artifacts, so the pair satisfies the artifacts table's UNIQUE(handle, org) constraint.
func mcpProxyEnvArtifactHandle(proxyHandle, envID string) string {
	suffix := strings.ReplaceAll(strings.TrimSpace(envID), "-", "")
	return fmt.Sprintf("%s-%s", proxyHandle, suffix)
}

// buildMCPProxyEnvArtifact flattens one per-environment blueprint block into the flat,
// single-environment MCPProxy that the deployment YAML builder consumes. Unlike the
// agent-scoped mapping (buildAgentMCPConfigProxy) this is the proxy's OWN artifact: the
// context is the proxy's base context — shared by every agent that references it — and
// the artifact identity is the stable per-environment ArtifactUUID owned by the proxy.
func buildMCPProxyEnvArtifact(source *models.MCPProxy, envID string, envCfg models.MCPEnvironmentConfig) *models.MCPProxy {
	proxyHandle := source.Handle
	if source.Artifact != nil && source.Artifact.Handle != "" {
		proxyHandle = source.Artifact.Handle
	}
	handle := mcpProxyEnvArtifactHandle(proxyHandle, envID)

	version := source.Version
	if source.Artifact != nil && source.Artifact.Version != "" {
		version = source.Artifact.Version
	}
	if version == "" {
		version = source.Configuration.Version
	}

	var upstream models.UpstreamConfig
	if envCfg.Upstream != nil {
		endpoint := *envCfg.Upstream
		upstream.Main = &endpoint
	}

	artifactUUID := uuid.Nil
	if envCfg.ArtifactUUID != nil {
		artifactUUID = *envCfg.ArtifactUUID
	}

	return &models.MCPProxy{
		UUID:        artifactUUID,
		Description: source.Description,
		Status:      source.Status,
		Configuration: models.MCPProxyConfig{
			Name:              handle,
			Version:           version,
			Context:           source.Configuration.Context,
			Vhost:             source.Configuration.Vhost,
			SpecVersion:       source.Configuration.SpecVersion,
			Upstream:          upstream,
			Policies:          envCfg.Policies,
			Capabilities:      envCfg.Capabilities,
			Security:          envCfg.Security,
			ToolScopeBindings: envCfg.ToolScopeBindings,
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
func (s *MCPProxyService) ensureMCPProxyEnvArtifactRow(deployProxy *models.MCPProxy, orgName string) error {
	if s.artifactRepo == nil {
		return nil
	}
	if _, err := s.artifactRepo.GetByHandle(deployProxy.Handle, orgName); err == nil {
		return nil
	} else if !errors.Is(err, utils.ErrArtifactNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to check MCP proxy environment artifact: %w", err)
	}
	now := time.Now()
	return s.db.Transaction(func(tx *gorm.DB) error {
		return s.artifactRepo.Create(tx, &models.Artifact{
			UUID:             deployProxy.UUID,
			Handle:           deployProxy.Handle,
			Name:             deployProxy.Name,
			Version:          deployProxy.Version,
			Kind:             models.KindMCPMapping,
			OrganizationName: orgName,
			CreatedAt:        now,
			UpdatedAt:        now,
		})
	})
}

// deployMCPProxyEnvironments deploys (or refreshes) the single gateway artifact for every
// configured environment of the proxy. An environment with no active gateway is skipped
// (it deploys on the next update once a gateway exists), mirroring the agent-config flow.
// Best-effort: per-environment failures are aggregated and returned.
func (s *MCPProxyService) deployMCPProxyEnvironments(ctx context.Context, proxy *models.MCPProxy, orgName string) error {
	var errs []error
	for envID, envCfg := range proxy.Configuration.Environments {
		envUUID, err := uuid.Parse(strings.TrimSpace(envID))
		if err != nil {
			errs = append(errs, fmt.Errorf("environment %q: invalid id: %w", envID, err))
			continue
		}
		if envCfg.ArtifactUUID == nil || *envCfg.ArtifactUUID == uuid.Nil {
			errs = append(errs, fmt.Errorf("environment %q: missing artifact id", envID))
			continue
		}
		gateway, err := s.resolveGatewayForEnvironment(ctx, envUUID, orgName)
		if errors.Is(err, errNoActiveGatewayForEnvironment) {
			s.logger.Info("Skipping MCP proxy environment deploy; no active gateway",
				"proxyUUID", proxy.UUID, "environment", envID)
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("environment %q: resolve gateway: %w", envID, err))
			continue
		}
		deployProxy := buildMCPProxyEnvArtifact(proxy, envID, envCfg)
		if err := s.ensureMCPProxyEnvArtifactRow(deployProxy, orgName); err != nil {
			errs = append(errs, fmt.Errorf("environment %q: %w", envID, err))
			continue
		}
		if err := s.deployMCPProxyToGateway(ctx, deployProxy, orgName, gateway); err != nil {
			errs = append(errs, fmt.Errorf("environment %q: deploy: %w", envID, err))
			continue
		}
	}
	return errors.Join(errs...)
}

// deleteMCPProxyEnvironmentArtifacts broadcast-deletes the given per-environment gateway
// artifacts and removes their artifacts rows (cascading to deployments / deployment_status
// via the FK). Used when environments are removed from a proxy and when the proxy itself
// is deleted. Best-effort: per-artifact failures are aggregated.
func (s *MCPProxyService) deleteMCPProxyEnvironmentArtifacts(ctx context.Context, artifactUUIDs []uuid.UUID, orgName string) error {
	var errs []error
	for _, artifactUUID := range artifactUUIDs {
		if artifactUUID == uuid.Nil {
			continue
		}
		s.BroadcastMCPArtifactDeletion(ctx, artifactUUID, orgName)
		if s.artifactRepo == nil {
			continue
		}
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("artifact_uuid = ? AND organization_name = ?", artifactUUID, orgName).
				Delete(&models.DeploymentStatusRecord{}).Error; err != nil {
				return err
			}
			if err := tx.Where("artifact_uuid = ? AND organization_name = ?", artifactUUID, orgName).
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

// resolveGatewayForEnvironment selects the environment's gateway with AI-first preference,
// returning errNoActiveGatewayForEnvironment when the environment has no active gateway.
func (s *MCPProxyService) resolveGatewayForEnvironment(ctx context.Context, envUUID uuid.UUID, orgName string) (*models.Gateway, error) {
	_ = ctx
	envIDStr := envUUID.String()
	aiType := "ai"
	activeStatus := true

	gateways, err := s.gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
		OrganizationID:    orgName,
		FunctionalityType: &aiType,
		Status:            &activeStatus,
		EnvironmentID:     &envIDStr,
		Limit:             1,
	})
	if err == nil && len(gateways) > 0 {
		return gateways[0], nil
	}

	gateways, err = s.gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
		OrganizationID: orgName,
		Status:         &activeStatus,
		EnvironmentID:  &envIDStr,
		Limit:          1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find gateway: %w", err)
	}
	if len(gateways) == 0 {
		return nil, errNoActiveGatewayForEnvironment
	}
	return gateways[0], nil
}

func (s *MCPProxyService) BroadcastMCPArtifactDeletion(ctx context.Context, artifactUUID uuid.UUID, orgName string) {
	proxy := &models.MCPProxy{UUID: artifactUUID}
	s.broadcastMCPProxyDeletion(ctx, proxy, s.gatewayIDsForDeletion(ctx, proxy, orgName))
}

func (s *MCPProxyService) gatewayIDsForDeletion(ctx context.Context, proxy *models.MCPProxy, orgName string) []string {
	_ = ctx
	if proxy == nil {
		return nil
	}
	gatewayIDs := map[string]struct{}{}
	if s.deploymentRepo != nil {
		deployedGatewayIDs, err := s.deploymentRepo.GetDeployedGatewaysByProvider(proxy.UUID, orgName)
		if err != nil {
			s.logger.Warn("Failed to get deployed gateways for MCP proxy deletion", "proxyID", proxy.UUID, "orgName", orgName, "error", err)
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
			OrganizationID: orgName,
			Status:         &active,
		})
		if err != nil {
			s.logger.Warn("Failed to get active gateways for MCP proxy deletion", "proxyID", proxy.UUID, "orgName", orgName, "error", err)
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
	_ = ctx
	if proxy == nil || s.gatewayEventsService == nil || len(gatewayIDs) == 0 {
		return
	}
	event := &models.MCPProxyDeletionEvent{
		ProxyID: proxy.UUID.String(),
	}
	for _, gatewayID := range gatewayIDs {
		if strings.TrimSpace(gatewayID) == "" {
			continue
		}
		if err := s.gatewayEventsService.BroadcastMCPProxyDeletionEvent(gatewayID, event); err != nil {
			s.logger.Warn("Failed to broadcast MCP proxy deletion event", "proxyID", proxy.UUID, "gatewayID", gatewayID, "error", err)
		} else {
			s.logger.Info("MCP proxy deletion event sent", "proxyID", proxy.UUID, "gatewayID", gatewayID)
		}
	}
}

func (s *MCPProxyService) generateMCPProxyDeploymentYAML(proxy *models.MCPProxy) (string, error) {
	deployment, err := s.buildMCPProxyDeploymentYAML(proxy)
	if err != nil {
		return "", err
	}
	yamlBytes, err := yaml.Marshal(deployment)
	if err != nil {
		return "", fmt.Errorf("failed to marshal MCP proxy deployment YAML: %w", err)
	}
	return string(yamlBytes), nil
}

func (s *MCPProxyService) buildMCPProxyDeploymentYAML(proxy *models.MCPProxy) (*MCPProxyDeploymentYAML, error) {
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
		upstream.URL = normalizeMCPUpstreamURLForDeployment(proxy.Configuration.Upstream.Main.URL)
		upstreamAuth = proxy.Configuration.Upstream.Main.Auth
	}
	if strings.TrimSpace(upstream.URL) == "" {
		return nil, fmt.Errorf("upstream URL is required")
	}
	policies, err := appendMCPAPIKeyAuthPolicy(proxy.Configuration.Policies, proxy.Configuration.Security)
	if err != nil {
		return nil, err
	}
	policies = appendMCPIdentityAuthPolicies(policies, proxy.Configuration.Security, proxy.Configuration.ToolScopeBindings)
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
// ThunderKeyManager key manager; requiredScopes is metadata advertisement only)
// and mcp-authz (per-tool requiredScopes enforcement). Tools without bindings get
// no rule — gateway default-permit means authenticated-only. Tool-rule order follows
// the bindings slice (storage preserves the client's order).
func appendMCPIdentityAuthPolicies(policies []models.MCPPolicy, security *models.SecurityConfig, bindings []models.MCPToolScopeBinding) []models.MCPPolicy {
	if security == nil || !isBoolTrue(security.Enabled) ||
		security.Identity == nil || !isBoolTrue(security.Identity.Enabled) {
		return policies
	}

	scopeSet := map[string]struct{}{}
	toolRules := make([]map[string]interface{}, 0, len(bindings))
	for _, b := range bindings {
		if strings.TrimSpace(b.Tool) == "" || len(b.Scopes) == 0 {
			continue
		}
		scopes := make([]string, 0, len(b.Scopes))
		for _, sc := range b.Scopes {
			if sc == "" {
				continue
			}
			scopes = append(scopes, sc)
			scopeSet[sc] = struct{}{}
		}
		if len(scopes) == 0 {
			continue
		}
		toolRules = append(toolRules, map[string]interface{}{"name": b.Tool, "requiredScopes": scopes})
	}

	authParams := map[string]interface{}{
		"issuers": []interface{}{mcpIdentityIssuerKeyManager},
	}
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

func normalizeMCPUpstreamURLForDeployment(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return trimmed
	}

	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		return trimmed
	}
	segments := strings.Split(path, "/")
	if len(segments) == 0 || segments[len(segments)-1] != "mcp" {
		return trimmed
	}

	segments = segments[:len(segments)-1]
	parsed.Path = strings.Join(segments, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.RawPath = ""
	return parsed.String()
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
