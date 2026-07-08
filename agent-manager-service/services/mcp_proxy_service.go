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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
	"github.com/wso2/agent-manager/agent-manager-service/utils/ssrf"
)

const (
	mcpJSONRPCVersion      = "2.0"
	mcpProtocolVersion     = "2025-06-18"
	mcpMethodInitialize    = "initialize"
	mcpMethodInitialized   = "notifications/initialized"
	mcpMethodToolsList     = "tools/list"
	mcpMethodPromptsList   = "prompts/list"
	mcpMethodResourcesList = "resources/list"
	mcpClientName          = "agent-manager-mcp-client"
	mcpClientVersion       = "1.0.0"
	mcpSessionHeader       = "Mcp-Session-Id"
	mcpRequestTimeout      = 10 * time.Second
	maxMCPResponseBody     = 10 << 20
)

// MCPProxyService handles MCP proxy operations.
type MCPProxyService struct {
	db                   *gorm.DB
	repo                 repositories.MCPProxyRepository
	deploymentRepo       repositories.DeploymentRepository
	gatewayRepo          repositories.GatewayRepository
	envMCPMappingRepo    repositories.EnvAgentMCPMappingRepository
	artifactRepo         repositories.ArtifactRepository
	scopeRepo            repositories.ScopeRepository
	gatewayEventsService *GatewayEventsService
	apiKeyBroadcaster    apiKeyBroadcaster
	client               *http.Client
	logger               *slog.Logger
	encryptionKey        []byte
}

// NewMCPProxyService creates a new MCP proxy service.
func NewMCPProxyService(
	db *gorm.DB,
	repo repositories.MCPProxyRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
	envMCPMappingRepo repositories.EnvAgentMCPMappingRepository,
	gatewayEventsService *GatewayEventsService,
	apiKeyRepo repositories.APIKeyRepository,
	scopeRepo repositories.ScopeRepository,
	logger *slog.Logger,
	encryptionKey []byte,
) *MCPProxyService {
	return &MCPProxyService{
		db:                   db,
		repo:                 repo,
		deploymentRepo:       deploymentRepo,
		gatewayRepo:          gatewayRepo,
		envMCPMappingRepo:    envMCPMappingRepo,
		artifactRepo:         repositories.NewArtifactRepo(db),
		scopeRepo:            scopeRepo,
		gatewayEventsService: gatewayEventsService,
		apiKeyBroadcaster: apiKeyBroadcaster{
			gatewayRepo:    gatewayRepo,
			gatewayService: gatewayEventsService,
			apiKeyRepo:     apiKeyRepo,
		},
		client:        ssrf.NewClient(mcpRequestTimeout),
		logger:        logger,
		encryptionKey: encryptionKey,
	}
}

// Create creates a new MCP proxy.
func (s *MCPProxyService) Create(ctx context.Context, orgUUID, createdBy string, req *models.MCPProxyDTO) (*models.MCPProxyDTO, error) {
	if req == nil {
		return nil, utils.ErrInvalidInput
	}

	handle := strings.TrimSpace(req.ID)
	name := strings.TrimSpace(req.Name)
	version := strings.TrimSpace(req.Version)
	if handle == "" || name == "" || version == "" {
		return nil, utils.ErrInvalidInput
	}

	if err := validateMCPEnvironments(ctx, req.Environments); err != nil {
		return nil, err
	}
	if err := s.validateMCPEnvironmentSecurity(ctx, orgUUID, req.Environments); err != nil {
		return nil, err
	}
	environments, err := s.buildMCPEnvironmentsForStorage(req.Environments, nil)
	if err != nil {
		return nil, err
	}

	exists, err := s.repo.Exists(ctx, handle, orgUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to check MCP proxy existence: %w", err)
	}
	if exists {
		return nil, utils.ErrMCPProxyExists
	}

	proxy := &models.MCPProxy{
		Description: valueOrEmpty(req.Description),
		CreatedBy:   createdBy,
		Status:      models.StatusCreated,
		Configuration: models.MCPProxyConfig{
			Name:         name,
			Version:      version,
			Context:      req.Context,
			Vhost:        req.Vhost,
			SpecVersion:  valueOrEmpty(req.McpSpecVersion),
			Environments: environments,
		},
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.repo.Create(ctx, tx, proxy, handle, name, version, orgUUID)
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, utils.ErrMCPProxyExists
		}
		return nil, fmt.Errorf("failed to create MCP proxy: %w", err)
	}

	created, err := s.repo.GetByHandle(ctx, handle, orgUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrMCPProxyNotFound
		}
		return nil, fmt.Errorf("failed to retrieve MCP proxy: %w", err)
	}

	// The MCP proxy deploys exactly one gateway artifact per configured environment,
	// immediately on creation. This is the only artifact the proxy contributes to any
	// gateway; agent configurations that reference it deploy nothing and instead reuse
	// these per-environment artifacts. Best-effort: an environment whose gateway is not
	// yet active is skipped and deploys on the next update.
	if err := s.deployMCPProxyEnvironments(ctx, created, orgUUID); err != nil {
		s.logger.Warn("Failed to deploy one or more MCP proxy environment artifacts", "proxyID", created.UUID, "error", err)
	}
	return convertModelMCPProxyToSpec(created), nil
}

// List retrieves MCP proxies for an organization.
func (s *MCPProxyService) List(ctx context.Context, orgUUID string, limit, offset int) (*models.MCPProxyListResponse, error) {
	proxies, err := s.repo.List(ctx, orgUUID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP proxies: %w", err)
	}

	total, err := s.repo.Count(ctx, orgUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to count MCP proxies: %w", err)
	}

	resp := &models.MCPProxyListResponse{
		Count: len(proxies),
		List:  make([]models.MCPProxyListItem, 0, len(proxies)),
		Pagination: models.PaginationInfo{
			Count:  total,
			Limit:  limit,
			Offset: offset,
		},
	}
	for _, proxy := range proxies {
		resp.List = append(resp.List, convertModelMCPProxyToListItem(proxy))
	}
	return resp, nil
}

// ListAvailableMCPPolicies returns policy versions reported by active gateways in the organization.
func (s *MCPProxyService) ListAvailableMCPPolicies(ctx context.Context, orgUUID string) (*models.MCPPolicyAvailabilityResponse, error) {
	_ = ctx
	if s.gatewayRepo == nil {
		return &models.MCPPolicyAvailabilityResponse{List: []models.MCPPolicyAvailableItem{}}, nil
	}

	active := true
	gateways, err := s.gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
		OrganizationID: orgUUID,
		Status:         &active,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list active gateways: %w", err)
	}

	var available map[string]models.MCPPolicyAvailableItem
	seenGateway := false
	for _, gateway := range gateways {
		if gateway == nil {
			continue
		}
		gatewayPolicies := map[string]models.MCPPolicyAvailableItem{}
		for _, policy := range extractGatewayPolicyManifestItems(gateway.Manifest) {
			if policy.Name == "" || policy.Version == "" {
				continue
			}
			key := policy.Name + "\x00" + policy.Version
			gatewayPolicies[key] = policy
		}
		if !seenGateway {
			available = gatewayPolicies
			seenGateway = true
			continue
		}
		for key := range available {
			if _, ok := gatewayPolicies[key]; !ok {
				delete(available, key)
			}
		}
	}

	if available == nil {
		available = map[string]models.MCPPolicyAvailableItem{}
	}
	items := make([]models.MCPPolicyAvailableItem, 0, len(available))
	for _, item := range available {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].Version < items[j].Version
		}
		return items[i].Name < items[j].Name
	})

	return &models.MCPPolicyAvailabilityResponse{
		Count: int32(len(items)),
		List:  items,
	}, nil
}

// Get retrieves an MCP proxy by handle.
func (s *MCPProxyService) Get(ctx context.Context, orgUUID, proxyID string) (*models.MCPProxyDTO, error) {
	handle := strings.TrimSpace(proxyID)
	if handle == "" {
		return nil, utils.ErrInvalidInput
	}

	proxy, err := s.repo.GetByHandle(ctx, handle, orgUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrMCPProxyNotFound
		}
		return nil, fmt.Errorf("failed to get MCP proxy: %w", err)
	}

	dto := convertModelMCPProxyToSpec(proxy)
	if dto != nil && s.deploymentRepo != nil {
		// Deployments are keyed by each environment's own artifact UUID (the proxy's UUID
		// itself is never deployed), so aggregate deployed gateways across the per-environment
		// artifacts and report a per-environment Deployed/Undeployed status.
		gatewaySet := map[string]struct{}{}
		for envID, envCfg := range proxy.Configuration.Environments {
			status := models.MCPDeploymentStatusUndeployed
			if envCfg.ArtifactUUID != nil && *envCfg.ArtifactUUID != uuid.Nil {
				gatewayIDs, err := s.deploymentRepo.GetDeployedGatewaysByProvider(*envCfg.ArtifactUUID, orgUUID)
				if err != nil {
					s.logger.Warn("Failed to list deployed gateways for MCP proxy environment",
						"proxyID", proxy.UUID, "environment", envID, "orgName", orgUUID, "error", err)
				} else if len(gatewayIDs) > 0 {
					status = models.MCPDeploymentStatusDeployed
					for _, gatewayID := range gatewayIDs {
						gatewaySet[gatewayID] = struct{}{}
					}
				}
			}
			if block, ok := dto.Environments[envID]; ok {
				block.DeploymentStatus = status
				dto.Environments[envID] = block
			}
		}
		gateways := make([]string, 0, len(gatewaySet))
		for gatewayID := range gatewaySet {
			gateways = append(gateways, gatewayID)
		}
		dto.Gateways = gateways
	}
	return dto, nil
}

// Update modifies an existing MCP proxy and redeploys it to active gateways. Returns
// the DTO for the response and the underlying model so the caller can cascade further
// work (e.g. redeploying agent-scoped mapping artifacts).
func (s *MCPProxyService) Update(ctx context.Context, orgUUID, proxyID string, req *models.MCPProxyDTO) (*models.MCPProxyDTO, error) {
	if req == nil {
		return nil, utils.ErrInvalidInput
	}

	handle := strings.TrimSpace(proxyID)
	if handle == "" {
		return nil, utils.ErrInvalidInput
	}
	if strings.TrimSpace(req.ID) != "" && strings.TrimSpace(req.ID) != handle {
		return nil, utils.ErrInvalidInput
	}

	name := strings.TrimSpace(req.Name)
	version := strings.TrimSpace(req.Version)
	if name == "" || version == "" {
		return nil, utils.ErrInvalidInput
	}

	// Validate the per-environment blueprint (structure + SSRF) outside the transaction
	// so network checks don't hold the row lock. Auth is encrypted inside the transaction
	// because it may need to preserve the previously stored secret when the client omits it.
	if err := validateMCPEnvironments(ctx, req.Environments); err != nil {
		return nil, err
	}
	if err := s.validateMCPEnvironmentSecurity(ctx, orgUUID, req.Environments); err != nil {
		return nil, err
	}

	// Captured inside the transaction so removed-environment artifacts can be torn down
	// from their gateways after the update commits.
	previousEnvArtifacts := map[string]uuid.UUID{}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		proxy, err := s.repo.GetByHandleForUpdate(ctx, tx, handle, orgUUID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return utils.ErrMCPProxyNotFound
			}
			return fmt.Errorf("failed to get MCP proxy before update: %w", err)
		}
		for envID, env := range proxy.Configuration.Environments {
			if env.ArtifactUUID != nil && *env.ArtifactUUID != uuid.Nil {
				previousEnvArtifacts[strings.TrimSpace(envID)] = *env.ArtifactUUID
			}
		}

		environments, err := s.buildMCPEnvironmentsForStorage(req.Environments, proxy.Configuration.Environments)
		if err != nil {
			return err
		}

		proxy.Description = valueOrEmpty(req.Description)
		proxy.Name = name
		proxy.Version = version
		proxy.Configuration = models.MCPProxyConfig{
			Name:         name,
			Version:      version,
			Context:      req.Context,
			Vhost:        req.Vhost,
			SpecVersion:  valueOrEmpty(req.McpSpecVersion),
			Environments: environments,
		}

		return s.repo.Update(ctx, tx, proxy, orgUUID)
	}); err != nil {
		if errors.Is(err, utils.ErrMCPProxyNotFound) || errors.Is(err, utils.ErrInvalidInput) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to update MCP proxy: %w", err)
	}

	updated, err := s.repo.GetByHandle(ctx, handle, orgUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrMCPProxyNotFound
		}
		return nil, fmt.Errorf("failed to retrieve MCP proxy: %w", err)
	}

	// Redeploy the per-environment artifacts so they pick up the new upstream / auth /
	// policies / context, and tear down artifacts for environments that were removed.
	if err := s.deployMCPProxyEnvironments(ctx, updated, orgUUID); err != nil {
		s.logger.Warn("Failed to redeploy one or more MCP proxy environment artifacts", "proxyID", updated.UUID, "error", err)
	}
	var removedArtifacts []uuid.UUID
	for envID, artifactUUID := range previousEnvArtifacts {
		if _, stillConfigured := updated.Configuration.Environments[envID]; !stillConfigured {
			removedArtifacts = append(removedArtifacts, artifactUUID)
		}
	}
	if len(removedArtifacts) > 0 {
		if err := s.deleteMCPProxyEnvironmentArtifacts(ctx, removedArtifacts, orgUUID); err != nil {
			s.logger.Warn("Failed to delete removed MCP proxy environment artifacts", "proxyID", updated.UUID, "error", err)
		}
	}

	// The org-level proxy owns and (re)deploys the per-environment gateway artifacts above.
	// Agents that reference this proxy read its endpoint at their own deploy time via the
	// stored DB mapping, so nothing needs to be pushed to already-deployed agents here.
	return convertModelMCPProxyToSpec(updated), nil
}

// Delete removes an MCP proxy by handle. MCP proxy mappings are deployable artifacts
// derived from an MCP proxy, so the source proxy cannot be deleted while mappings exist.
func (s *MCPProxyService) Delete(ctx context.Context, orgUUID, proxyID string) error {
	handle := strings.TrimSpace(proxyID)
	if handle == "" {
		return utils.ErrInvalidInput
	}

	proxy, err := s.repo.GetByHandle(ctx, handle, orgUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return utils.ErrMCPProxyNotFound
		}
		return fmt.Errorf("failed to get MCP proxy before delete: %w", err)
	}

	var mappings []models.EnvAgentMCPMapping
	if s.envMCPMappingRepo != nil {
		mappings, err = s.envMCPMappingRepo.ListByMCPProxy(ctx, proxy.UUID)
		if err != nil {
			return fmt.Errorf("failed to list MCP mappings before delete: %w", err)
		}
	}
	if len(mappings) > 0 {
		return utils.ErrMCPProxyHasMappings
	}

	// Tear down the per-environment gateway artifacts this proxy deployed before removing
	// the proxy row. Best-effort broadcast; the DB rows cascade-delete with the artifacts.
	var artifactUUIDs []uuid.UUID
	for _, env := range proxy.Configuration.Environments {
		if env.ArtifactUUID != nil && *env.ArtifactUUID != uuid.Nil {
			artifactUUIDs = append(artifactUUIDs, *env.ArtifactUUID)
		}
	}
	if err := s.deleteMCPProxyEnvironmentArtifacts(ctx, artifactUUIDs, orgUUID); err != nil {
		s.logger.Warn("Failed to delete MCP proxy environment artifacts during proxy delete", "proxyID", proxy.UUID, "error", err)
	}

	if err := s.repo.Delete(ctx, handle, orgUUID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return utils.ErrMCPProxyNotFound
		}
		return fmt.Errorf("failed to delete MCP proxy: %w", err)
	}

	return nil
}

func extractGatewayPolicyManifestItems(value interface{}) []models.MCPPolicyAvailableItem {
	seen := map[string]struct{}{}
	items := make([]models.MCPPolicyAvailableItem, 0)
	var walk func(interface{})

	add := func(name, version string) {
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)
		if name == "" || version == "" {
			return
		}
		key := name + "\x00" + version
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, models.MCPPolicyAvailableItem{Name: name, Version: version})
	}

	stringValue := func(v interface{}) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}

	walk = func(current interface{}) {
		switch typed := current.(type) {
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		case []map[string]interface{}:
			for _, item := range typed {
				walk(item)
			}
		case map[string]interface{}:
			name := stringValue(firstMapValue(typed, "name", "policyName", "id"))
			version := stringValue(firstMapValue(typed, "version", "policyVersion"))
			if name != "" && version != "" {
				add(name, version)
			}
			if name != "" {
				if versions, ok := firstMapValue(typed, "versions", "policyVersions").([]interface{}); ok {
					for _, rawVersion := range versions {
						add(name, stringValue(rawVersion))
					}
				}
				if versions, ok := firstMapValue(typed, "versions", "policyVersions").([]string); ok {
					for _, rawVersion := range versions {
						add(name, rawVersion)
					}
				}
			}
			for _, nested := range typed {
				walk(nested)
			}
		}
	}

	walk(value)
	return items
}

func firstMapValue(values map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func (s *MCPProxyService) prepareMCPUpstreamAuthForStorage(existing, updated *models.UpstreamAuth) (*models.UpstreamAuth, error) {
	auth := preserveUpstreamAuthCredential(existing, updated)
	if auth == nil {
		return nil, nil //nolint:nilnil // A nil auth value is valid when upstream auth is omitted.
	}

	if auth.Header != nil {
		header := strings.TrimSpace(*auth.Header)
		auth.Header = &header
	}
	if auth.Value != nil && *auth.Value == "" {
		auth.Value = nil
	}
	// A newly supplied plaintext value supersedes any preserved encrypted secret,
	// keeping Value and SecretRef mutually exclusive for Validate.
	if auth.Value != nil {
		auth.SecretRef = nil
	}

	if err := auth.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", utils.ErrInvalidInput, err)
	}

	hasHeader := auth.Header != nil && *auth.Header != ""
	hasCredential := auth.Value != nil || auth.SecretRef != nil
	if !hasHeader && !hasCredential {
		return nil, nil //nolint:nilnil // Empty auth fields mean the endpoint should not store auth.
	}
	if hasHeader != hasCredential {
		return nil, fmt.Errorf("%w: authentication header and value must be provided together", utils.ErrInvalidInput)
	}

	if auth.Value != nil {
		encrypted, err := utils.EncryptBytes([]byte(*auth.Value), s.encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt upstream auth: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(encrypted)
		auth.SecretRef = &encoded
		auth.Value = nil
	}

	return auth, nil
}

// FetchServerInfo fetches server information from an MCP backend.
func (s *MCPProxyService) FetchServerInfo(ctx context.Context, req *models.MCPServerInfoFetchRequest) (*models.MCPServerInfoFetchResponse, error) {
	if req == nil || req.URL == nil || strings.TrimSpace(*req.URL) == "" {
		return nil, utils.ErrInvalidInput
	}
	if req.ProxyID != nil && strings.TrimSpace(*req.ProxyID) != "" {
		return nil, fmt.Errorf("proxyId refresh is not supported yet: %w", utils.ErrInvalidInput)
	}

	endpointURL := strings.TrimSpace(*req.URL)
	if err := ssrf.ValidateURL(ctx, endpointURL); err != nil {
		return nil, fmt.Errorf("%w: %w", utils.ErrInvalidURL, err)
	}

	headerName, headerValue := authHeader(req.Auth)
	return s.fetchMCPServerInfo(ctx, endpointURL, headerName, headerValue)
}

func authHeader(auth *models.UpstreamAuth) (string, string) {
	if auth == nil || auth.Header == nil || auth.Value == nil {
		return "", ""
	}
	return strings.TrimSpace(*auth.Header), *auth.Value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func timePtrIfNotZero(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func copyMCPPolicies(policies []models.MCPPolicy) []models.MCPPolicy {
	if len(policies) == 0 {
		return nil
	}
	out := make([]models.MCPPolicy, len(policies))
	copy(out, policies)
	return out
}

func copyMCPToolScopeBindings(bindings []models.MCPToolScopeBinding) []models.MCPToolScopeBinding {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]models.MCPToolScopeBinding, 0, len(bindings))
	for _, b := range bindings {
		scopes := make([]string, len(b.Scopes))
		copy(scopes, b.Scopes)
		out = append(out, models.MCPToolScopeBinding{Tool: b.Tool, Scopes: scopes})
	}
	return out
}

func copyMCPCapabilities(capabilities *models.MCPProxyCapabilities) *models.MCPProxyCapabilities {
	if capabilities == nil {
		return nil
	}
	return &models.MCPProxyCapabilities{
		Prompts:   capabilities.Prompts,
		Resources: capabilities.Resources,
		Tools:     capabilities.Tools,
	}
}

func sanitizeMCPUpstreamEndpointForResponse(endpoint *models.UpstreamEndpoint) *models.UpstreamEndpoint {
	if endpoint == nil {
		return nil
	}
	sanitized := *endpoint
	sanitized.Auth = sanitizeMCPUpstreamAuthForResponse(endpoint.Auth)
	return &sanitized
}

// sanitizeMCPEnvironmentsForResponse strips plaintext upstream credentials from each
// per-environment blueprint block before returning it to the client.
func sanitizeMCPEnvironmentsForResponse(environments map[string]models.MCPEnvironmentConfig) map[string]models.MCPEnvironmentConfig {
	if len(environments) == 0 {
		return nil
	}
	out := make(map[string]models.MCPEnvironmentConfig, len(environments))
	for envID, env := range environments {
		sanitized := env
		// The per-environment gateway artifact UUID is an internal identity; never expose it.
		sanitized.ArtifactUUID = nil
		sanitized.Upstream = sanitizeMCPUpstreamEndpointForResponse(env.Upstream)
		sanitized.Policies = copyMCPPolicies(env.Policies)
		sanitized.Capabilities = copyMCPCapabilities(env.Capabilities)
		out[envID] = sanitized
	}
	return out
}

func sanitizeMCPUpstreamAuthForResponse(auth *models.UpstreamAuth) *models.UpstreamAuth {
	if auth == nil {
		return nil
	}
	sanitized := *auth
	sanitized.Value = nil
	return &sanitized
}

func defaultMCPProxySecurity(security *models.SecurityConfig) *models.SecurityConfig {
	enabled := true
	if security != nil {
		out := *security
		if out.Enabled == nil {
			out.Enabled = &enabled
		}
		if !isBoolTrue(out.Enabled) {
			return &out
		}
		if out.Identity != nil && isBoolTrue(out.Identity.Enabled) {
			// Agent Identity mode: no API-key defaulting. Mutual exclusion is
			// enforced in validateMCPEnvironments before this runs.
			identity := *out.Identity
			out.Identity = &identity
			out.APIKey = nil
			return &out
		}
		if out.APIKey == nil {
			out.APIKey = &models.APIKeySecurity{}
		} else {
			apiKey := *out.APIKey
			out.APIKey = &apiKey
		}
		if out.APIKey.Enabled == nil {
			out.APIKey.Enabled = &enabled
		}
		if isBoolTrue(out.APIKey.Enabled) {
			if strings.TrimSpace(out.APIKey.Key) == "" {
				out.APIKey.Key = "X-API-Key"
			}
			if strings.TrimSpace(out.APIKey.In) == "" {
				out.APIKey.In = "header"
			}
		}
		return &out
	}
	return &models.SecurityConfig{
		Enabled: &enabled,
		APIKey: &models.APIKeySecurity{
			Enabled: &enabled,
			Key:     "X-API-Key",
			In:      "header",
		},
	}
}

func convertModelMCPProxyToSpec(proxy *models.MCPProxy) *models.MCPProxyDTO {
	if proxy == nil {
		return nil
	}
	id := proxy.Handle
	name := proxy.Name
	version := proxy.Version
	createdAt := proxy.CreatedAt
	updatedAt := proxy.UpdatedAt
	inCatalog := false
	if proxy.Artifact != nil {
		id = proxy.Artifact.Handle
		name = proxy.Artifact.Name
		version = proxy.Artifact.Version
		createdAt = proxy.Artifact.CreatedAt
		updatedAt = proxy.Artifact.UpdatedAt
		inCatalog = proxy.Artifact.InCatalog
	}
	if name == "" {
		name = proxy.Configuration.Name
	}
	if version == "" {
		version = proxy.Configuration.Version
	}
	return &models.MCPProxyDTO{
		Capabilities:   copyMCPCapabilities(proxy.Configuration.Capabilities),
		Context:        proxy.Configuration.Context,
		CreatedAt:      timePtrIfNotZero(createdAt),
		CreatedBy:      stringPtrIfNotEmpty(proxy.CreatedBy),
		Description:    stringPtrIfNotEmpty(proxy.Description),
		ID:             id,
		InCatalog:      inCatalog,
		McpSpecVersion: stringPtrIfNotEmpty(proxy.Configuration.SpecVersion),
		Name:           name,
		Environments:   sanitizeMCPEnvironmentsForResponse(proxy.Configuration.Environments),
		UpdatedAt:      timePtrIfNotZero(updatedAt),
		Version:        version,
		Vhost:          proxy.Configuration.Vhost,
	}
}

func convertModelMCPProxyToListItem(proxy *models.MCPProxy) models.MCPProxyListItem {
	status := proxy.Status
	id := proxy.Handle
	name := proxy.Name
	version := proxy.Version
	createdAt := proxy.CreatedAt
	updatedAt := proxy.UpdatedAt
	if proxy.Artifact != nil {
		id = proxy.Artifact.Handle
		name = proxy.Artifact.Name
		version = proxy.Artifact.Version
		createdAt = proxy.Artifact.CreatedAt
		updatedAt = proxy.Artifact.UpdatedAt
	}
	if name == "" {
		name = proxy.Configuration.Name
	}
	if version == "" {
		version = proxy.Configuration.Version
	}
	return models.MCPProxyListItem{
		Context:        proxy.Configuration.Context,
		CreatedAt:      timePtrIfNotZero(createdAt),
		CreatedBy:      stringPtrIfNotEmpty(proxy.CreatedBy),
		Description:    stringPtrIfNotEmpty(proxy.Description),
		ID:             stringPtrIfNotEmpty(id),
		McpSpecVersion: stringPtrIfNotEmpty(proxy.Configuration.SpecVersion),
		Name:           stringPtrIfNotEmpty(name),
		Status:         stringPtrIfNotEmpty(status),
		UpdatedAt:      timePtrIfNotZero(updatedAt),
		Version:        stringPtrIfNotEmpty(version),
	}
}

// validateMCPEnvironments enforces the per-environment blueprint structure: at least one
// block, keyed by a non-empty environment UUID, each with a valid, SSRF-safe upstream URL.
// Uniqueness is guaranteed by the map keys. It performs the network checks, so call it
// outside a DB transaction.
func validateMCPEnvironments(ctx context.Context, environments map[string]models.MCPEnvironmentConfig) error {
	if len(environments) == 0 {
		return fmt.Errorf("%w: at least one environment configuration is required", utils.ErrInvalidInput)
	}
	for envID, env := range environments {
		envUUID := strings.TrimSpace(envID)
		if envUUID == "" {
			return fmt.Errorf("%w: an environment configuration is missing an environment id", utils.ErrInvalidInput)
		}
		if _, err := uuid.Parse(envUUID); err != nil {
			return fmt.Errorf("%w: environment %q has an invalid environment id: %w", utils.ErrInvalidInput, envID, err)
		}
		// Structural security checks first (no network I/O): apiKey and identity are
		// mutually exclusive, and every tool binding must name a tool with >=1 scope.
		if sec := env.Security; sec != nil && isBoolTrue(sec.Enabled) {
			apiKeyOn := sec.APIKey != nil && isBoolTrue(sec.APIKey.Enabled)
			identityOn := sec.Identity != nil && isBoolTrue(sec.Identity.Enabled)
			if apiKeyOn && identityOn {
				return fmt.Errorf("%w: environment %q: apiKey and identity security are mutually exclusive", utils.ErrInvalidInput, envID)
			}
		}
		seenTools := make(map[string]struct{}, len(env.ToolScopeBindings))
		for _, b := range env.ToolScopeBindings {
			tool := strings.TrimSpace(b.Tool)
			if tool == "" {
				return fmt.Errorf("%w: environment %q has a tool binding with an empty tool name", utils.ErrInvalidInput, envID)
			}
			if len(b.Scopes) == 0 {
				return fmt.Errorf("%w: environment %q: tool %q binding has no scopes", utils.ErrInvalidInput, envID, b.Tool)
			}
			// A tool→scopes mapping is conceptually a map: two bindings for the same
			// tool produce ambiguous mcp-authz rules, so reject duplicates outright
			// rather than let the gateway combine them in an order we do not control.
			if _, dup := seenTools[tool]; dup {
				return fmt.Errorf("%w: environment %q has duplicate tool binding %q", utils.ErrInvalidInput, envID, tool)
			}
			seenTools[tool] = struct{}{}
		}
		if env.Upstream == nil || strings.TrimSpace(env.Upstream.URL) == "" {
			return fmt.Errorf("%w: environment %q is missing an upstream url", utils.ErrInvalidInput, envID)
		}
		if err := ssrf.ValidateURL(ctx, strings.TrimSpace(env.Upstream.URL)); err != nil {
			return fmt.Errorf("%w: environment %q upstream url: %w", utils.ErrInvalidURL, envID, err)
		}
	}
	return nil
}

// validateMCPEnvironmentSecurity enforces the cross-resource identity-mode rules:
// every bound scope exists in the org catalog, and an identity-mode environment's
// gateway advertises mcp-auth v1 + mcp-authz v1 in its policy manifest. Bindings to
// tools absent from capabilities are deliberately accepted (tool lists drift; the
// console flags them). It performs DB reads, so call it outside a transaction.
func (s *MCPProxyService) validateMCPEnvironmentSecurity(ctx context.Context, orgName string, environments map[string]models.MCPEnvironmentConfig) error {
	catalog, err := s.scopeRepo.List(ctx, orgName)
	if err != nil {
		return fmt.Errorf("failed to load scope catalog: %w", err)
	}
	known := make(map[string]struct{}, len(catalog))
	for _, sc := range catalog {
		known[sc.Name] = struct{}{}
	}
	for envID, env := range environments {
		for _, b := range env.ToolScopeBindings {
			for _, scName := range b.Scopes {
				if _, ok := known[scName]; !ok {
					return fmt.Errorf("%w: environment %q: tool %q references unknown scope %q", utils.ErrInvalidInput, envID, b.Tool, scName)
				}
			}
		}
		if env.Security == nil || !isBoolTrue(env.Security.Enabled) ||
			env.Security.Identity == nil || !isBoolTrue(env.Security.Identity.Enabled) {
			continue
		}
		envUUID, err := uuid.Parse(strings.TrimSpace(envID))
		if err != nil {
			continue // already rejected by validateMCPEnvironments
		}
		gateway, err := s.resolveGatewayForEnvironment(ctx, envUUID, orgName)
		if errors.Is(err, errNoActiveGatewayForEnvironment) {
			continue // no gateway yet: allowed, deploys later; policies checked when one exists
		}
		if err != nil {
			return fmt.Errorf("environment %q: resolve gateway: %w", envID, err)
		}
		if !gatewayHasMCPIdentityPolicies(gateway) {
			return fmt.Errorf("%w: environment %q: its gateway does not support mcp-auth/mcp-authz v1 policies required for Agent Identity security", utils.ErrInvalidInput, envID)
		}
	}
	return nil
}

// gatewayHasMCPIdentityPolicies reports whether the gateway's policy manifest
// advertises both mcp-auth v1 and mcp-authz v1 (the policies identity mode emits).
func gatewayHasMCPIdentityPolicies(gateway *models.Gateway) bool {
	need := map[string]bool{"mcp-auth\x00v1": false, "mcp-authz\x00v1": false}
	for _, item := range extractGatewayPolicyManifestItems(gateway.Manifest) {
		key := item.Name + "\x00" + normalizePolicyVersionToMajor(item.Version)
		if _, ok := need[key]; ok {
			need[key] = true
		}
	}
	return need["mcp-auth\x00v1"] && need["mcp-authz\x00v1"]
}

// buildMCPEnvironmentsForStorage normalizes incoming per-environment blueprint blocks for
// persistence: it trims the environment-UUID keys, encrypts each block's upstream auth
// (preserving a previously stored secret when the client omits the credential), and applies
// default security. Call validateMCPEnvironments first. existing may be nil (on create) and
// is matched by environment UUID so an omitted credential falls back to the stored SecretRef.
// This performs no network I/O, so it is safe to call inside a transaction.
func (s *MCPProxyService) buildMCPEnvironmentsForStorage(incoming, existing map[string]models.MCPEnvironmentConfig) (map[string]models.MCPEnvironmentConfig, error) {
	if len(incoming) == 0 {
		return map[string]models.MCPEnvironmentConfig{}, nil
	}
	existingAuthByEnv := map[string]*models.UpstreamAuth{}
	existingArtifactByEnv := map[string]uuid.UUID{}
	for envID, env := range existing {
		trimmed := strings.TrimSpace(envID)
		if env.Upstream != nil {
			existingAuthByEnv[trimmed] = env.Upstream.Auth
		}
		if env.ArtifactUUID != nil && *env.ArtifactUUID != uuid.Nil {
			existingArtifactByEnv[trimmed] = *env.ArtifactUUID
		}
	}
	out := make(map[string]models.MCPEnvironmentConfig, len(incoming))
	for rawEnvID, incomingEnv := range incoming {
		envID := strings.TrimSpace(rawEnvID)
		// The single gateway artifact deployed for this environment is identified by a
		// stable UUID: preserve the previously stored one on update, mint a new one when
		// the environment is first configured.
		artifactUUID := existingArtifactByEnv[envID]
		if artifactUUID == uuid.Nil {
			artifactUUID = uuid.New()
		}
		block := models.MCPEnvironmentConfig{
			ArtifactUUID:      &artifactUUID,
			Policies:          copyMCPPolicies(incomingEnv.Policies),
			Capabilities:      copyMCPCapabilities(incomingEnv.Capabilities),
			Security:          defaultMCPProxySecurity(incomingEnv.Security),
			ToolScopeBindings: copyMCPToolScopeBindings(incomingEnv.ToolScopeBindings),
		}
		if incomingEnv.Upstream != nil {
			endpoint := *incomingEnv.Upstream
			endpoint.URL = strings.TrimSpace(endpoint.URL)
			auth, err := s.prepareMCPUpstreamAuthForStorage(existingAuthByEnv[envID], incomingEnv.Upstream.Auth)
			if err != nil {
				return nil, err
			}
			endpoint.Auth = auth
			block.Upstream = &endpoint
		}
		out[envID] = block
	}
	return out, nil
}

func (s *MCPProxyService) fetchMCPServerInfo(ctx context.Context, endpointURL string, headerName string, headerValue string) (*models.MCPServerInfoFetchResponse, error) {
	sessionID, serverInfo, err := s.initializeMCPServer(ctx, endpointURL, headerName, headerValue)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MCP server: %w", err)
	}

	notifyReq := mcpJSONRPCRequest{
		JSONRPC: mcpJSONRPCVersion,
		Method:  mcpMethodInitialized,
	}
	if _, err := s.postJSONRPCWithSession(ctx, endpointURL, notifyReq, sessionID, headerName, headerValue); err != nil {
		return nil, fmt.Errorf("failed to send notification: %w", err)
	}

	resp := &models.MCPServerInfoFetchResponse{}
	if serverInfo != nil {
		resp.ServerInfo = &serverInfo
	}

	if tools := s.fetchTools(ctx, endpointURL, sessionID, headerName, headerValue); len(tools) > 0 {
		resp.Tools = &tools
	}
	if prompts := s.fetchPrompts(ctx, endpointURL, sessionID, headerName, headerValue); len(prompts) > 0 {
		resp.Prompts = &prompts
	}
	if resources := s.fetchResources(ctx, endpointURL, sessionID, headerName, headerValue); len(resources) > 0 {
		resp.Resources = &resources
	}

	return resp, nil
}

func (s *MCPProxyService) initializeMCPServer(ctx context.Context, endpointURL string, headerName string, headerValue string) (string, map[string]interface{}, error) {
	initReq := mcpJSONRPCRequest{
		JSONRPC: mcpJSONRPCVersion,
		ID:      1,
		Method:  mcpMethodInitialize,
		Params: map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]interface{}{"roots": map[string]bool{"listChanged": true}},
			"clientInfo":      map[string]string{"name": mcpClientName, "version": mcpClientVersion},
		},
	}

	body, headers, err := s.postJSONRPC(ctx, endpointURL, initReq, "", headerName, headerValue)
	if err != nil {
		return "", nil, err
	}

	var initResult mcpInitializeResult
	if err := json.Unmarshal(body, &initResult); err != nil {
		return "", nil, fmt.Errorf("failed to parse initialize response: %w, body: %s", err, string(body))
	}
	if initResult.Error != nil {
		return "", nil, fmt.Errorf("initialize request returned an error: %s", initResult.Error.Message)
	}

	return headers.Get(mcpSessionHeader), initResult.Result.ServerInfo, nil
}

func (s *MCPProxyService) fetchTools(ctx context.Context, endpointURL string, sessionID string, headerName string, headerValue string) []map[string]interface{} {
	req := mcpJSONRPCRequest{JSONRPC: mcpJSONRPCVersion, ID: 2, Method: mcpMethodToolsList}
	body, err := s.postJSONRPCWithSession(ctx, endpointURL, req, sessionID, headerName, headerValue)
	if err != nil {
		s.logger.Warn("Failed to fetch MCP tools, continuing with available info", "error", err)
		return nil
	}
	var result mcpToolsResult
	if err := json.Unmarshal(body, &result); err != nil {
		s.logger.Warn("Failed to parse MCP tools response, continuing with available info", "error", err)
		return nil
	}
	if result.Error != nil {
		s.logger.Warn("tools/list returned an error, continuing with available info", "error", result.Error.Message)
		return nil
	}
	return result.Result.Tools
}

func (s *MCPProxyService) fetchPrompts(ctx context.Context, endpointURL string, sessionID string, headerName string, headerValue string) []map[string]interface{} {
	req := mcpJSONRPCRequest{JSONRPC: mcpJSONRPCVersion, ID: 3, Method: mcpMethodPromptsList}
	body, err := s.postJSONRPCWithSession(ctx, endpointURL, req, sessionID, headerName, headerValue)
	if err != nil {
		s.logger.Warn("Failed to fetch MCP prompts, continuing with available info", "error", err)
		return nil
	}
	var result mcpPromptsResult
	if err := json.Unmarshal(body, &result); err != nil {
		s.logger.Warn("Failed to parse MCP prompts response, continuing with available info", "error", err)
		return nil
	}
	if result.Error != nil {
		s.logger.Warn("prompts/list returned an error, continuing with available info", "error", result.Error.Message)
		return nil
	}
	return result.Result.Prompts
}

func (s *MCPProxyService) fetchResources(ctx context.Context, endpointURL string, sessionID string, headerName string, headerValue string) []map[string]interface{} {
	req := mcpJSONRPCRequest{JSONRPC: mcpJSONRPCVersion, ID: 4, Method: mcpMethodResourcesList}
	body, err := s.postJSONRPCWithSession(ctx, endpointURL, req, sessionID, headerName, headerValue)
	if err != nil {
		s.logger.Warn("Failed to fetch MCP resources, continuing with available info", "error", err)
		return nil
	}
	var result mcpResourcesResult
	if err := json.Unmarshal(body, &result); err != nil {
		s.logger.Warn("Failed to parse MCP resources response, continuing with available info", "error", err)
		return nil
	}
	if result.Error != nil {
		s.logger.Warn("resources/list returned an error, continuing with available info", "error", result.Error.Message)
		return nil
	}
	return result.Result.Resources
}

func (s *MCPProxyService) postJSONRPCWithSession(ctx context.Context, endpointURL string, payload interface{}, sessionID string, headerName string, headerValue string) ([]byte, error) {
	body, _, err := s.postJSONRPC(ctx, endpointURL, payload, sessionID, headerName, headerValue)
	return body, err
}

func (s *MCPProxyService) postJSONRPC(ctx context.Context, endpointURL string, payload interface{}, sessionID string, headerName string, headerValue string) ([]byte, http.Header, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewBuffer(data))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	setSafeHeader(httpReq.Header, headerName, headerValue)
	if sessionID != "" {
		httpReq.Header.Set(mcpSessionHeader, sessionID)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		if errors.Is(err, utils.ErrInvalidURL) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("%w: failed to reach MCP server: %w", utils.ErrURLUnreachable, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			s.logger.Warn("Failed to close MCP server response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPResponseBody+1))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if len(body) > maxMCPResponseBody {
		return nil, nil, fmt.Errorf("%w: response body exceeds %d bytes", utils.ErrMCPResponseTooLarge, maxMCPResponseBody)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, utils.ErrMCPServerUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}
	if isMCPEventStream(resp) {
		data, err := parseMCPEventStream(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse event stream: %w, body: %s", err, string(body))
		}
		body = data
	}

	return body, resp.Header, nil
}

func setSafeHeader(headers http.Header, headerName string, headerValue string) {
	if strings.TrimSpace(headerName) == "" {
		return
	}
	switch strings.ToLower(headerName) {
	case strings.ToLower(mcpSessionHeader), "content-type", "accept":
		return
	default:
		headers.Set(headerName, headerValue)
	}
}

func parseMCPEventStream(body []byte) ([]byte, error) {
	lines := bytes.Split(body, []byte("\n"))
	var eventData bytes.Buffer
	hasData := false
	flushEvent := func() []byte {
		if !hasData {
			return nil
		}
		data := eventData.Bytes()
		eventData.Reset()
		hasData = false
		if len(data) > 0 && !bytes.Equal(data, []byte("{}")) {
			return data
		}
		return nil
	}

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if data := flushEvent(); data != nil {
				return data, nil
			}
			continue
		}
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if hasData {
			eventData.WriteByte('\n')
		}
		eventData.Write(data)
		hasData = true
	}
	if data := flushEvent(); data != nil {
		return data, nil
	}
	return nil, errors.New("no data found in event stream")
}

func isMCPEventStream(resp *http.Response) bool {
	return strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
}

type mcpJSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpInitializeResult struct {
	Result struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		ServerInfo      map[string]interface{} `json:"serverInfo"`
		Capabilities    map[string]interface{} `json:"capabilities"`
	} `json:"result"`
	Error *mcpJSONRPCError `json:"error"`
}

type mcpToolsResult struct {
	Result struct {
		Tools []map[string]interface{} `json:"tools"`
	} `json:"result"`
	Error *mcpJSONRPCError `json:"error"`
}

type mcpPromptsResult struct {
	Result struct {
		Prompts []map[string]interface{} `json:"prompts"`
	} `json:"result"`
	Error *mcpJSONRPCError `json:"error"`
}

type mcpResourcesResult struct {
	Result struct {
		Resources []map[string]interface{} `json:"resources"`
	} `json:"result"`
	Error *mcpJSONRPCError `json:"error"`
}
