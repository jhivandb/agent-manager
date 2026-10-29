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
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// GatewayInternalAPIService handles internal gateway API operations
type GatewayInternalAPIService struct {
	providerRepo     repositories.LLMProviderRepository
	proxyRepo        repositories.LLMProxyRepository
	deploymentRepo   repositories.DeploymentRepository
	gatewayRepo      repositories.GatewayRepository
	artifactRepo     repositories.ArtifactRepository
	infraResourceMgr InfraResourceManager
	encryptionKey    []byte
	// afterSyncListed runs once a gateway has listed deployments of which some await its ack.
	afterSyncListed func(ctx context.Context, gatewayID string)
}

// WithSyncListed sets what runs once a starting gateway has listed its deployments and some of
// them still await its ack: the reconcile worker's ResendUnacked.
func (s *GatewayInternalAPIService) WithSyncListed(fn func(ctx context.Context, gatewayID string)) *GatewayInternalAPIService {
	s.afterSyncListed = fn
	return s
}

// DeploymentNotification represents the notification from gateway
type DeploymentNotification struct {
	ProjectIdentifier string
	Configuration     APIDeploymentYAML
}

// GatewayDeploymentResponse represents the response for gateway deployment
type GatewayDeploymentResponse struct {
	APIId        string `json:"apiId"`
	DeploymentId int    `json:"deploymentId"` // Legacy field
	Message      string `json:"message"`
	Created      bool   `json:"created"`
}

// APIDeploymentYAML represents the API deployment YAML structure
type APIDeploymentYAML struct {
	ApiVersion string             `yaml:"apiVersion"`
	Kind       string             `yaml:"kind"`
	Metadata   DeploymentMetadata `yaml:"metadata"`
	Spec       APIDeploymentSpec  `yaml:"spec"`
}

// DeploymentMetadata represents metadata in deployment
type DeploymentMetadata struct {
	Name string `yaml:"name"`
}

// APIDeploymentSpec represents the spec section
type APIDeploymentSpec struct {
	Name       string      `yaml:"name"`
	Version    string      `yaml:"version"`
	Context    string      `yaml:"context"`
	Operations []Operation `yaml:"operations"`
}

// Operation represents an API operation
type Operation struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

// NewGatewayInternalAPIService creates a new gateway internal API service
func NewGatewayInternalAPIService(
	providerRepo repositories.LLMProviderRepository,
	proxyRepo repositories.LLMProxyRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
	artifactRepo repositories.ArtifactRepository,
	infraResourceMgr InfraResourceManager,
	encryptionKey []byte,
) *GatewayInternalAPIService {
	return &GatewayInternalAPIService{
		providerRepo:     providerRepo,
		proxyRepo:        proxyRepo,
		deploymentRepo:   deploymentRepo,
		gatewayRepo:      gatewayRepo,
		artifactRepo:     artifactRepo,
		infraResourceMgr: infraResourceMgr,
		encryptionKey:    encryptionKey,
	}
}

// GetActiveMCPProxyDeploymentByGateway retrieves the currently deployed MCP artifact for the
// given UUID on this gateway. The UUID may identify a source MCP proxy (mcp_proxies) or an
// agent-scoped mapping artifact (artifacts); both live in the deployments table keyed by
// artifact_uuid, so a single lookup handles both cases.
func (s *GatewayInternalAPIService) GetActiveMCPProxyDeploymentByGateway(ctx context.Context, proxyID, ouID, gatewayID string) (map[string]string, error) {
	return s.getActiveArtifactDeploymentByGateway(ctx, proxyID, ouID, gatewayID, "MCP proxy", utils.ErrMCPProxyNotFound)
}

// GetActiveAgentDeploymentByGateway retrieves the currently deployed A2A Agent
// artifact for the given UUID on this gateway.
//
// The UUID is the per-environment models.KindAgent artifact row, which is also
// what the agent's API keys are bound to — so the gateway resolves the Agent
// and its keys through the same identity.
func (s *GatewayInternalAPIService) GetActiveAgentDeploymentByGateway(ctx context.Context, agentID, ouID, gatewayID string) (map[string]string, error) {
	return s.getActiveArtifactDeploymentByGateway(ctx, agentID, ouID, gatewayID, "Agent", utils.ErrAgentArtifactNotFound)
}

// getActiveArtifactDeploymentByGateway returns the artifact's deployed YAML with secrets resolved, or notFound.
func (s *GatewayInternalAPIService) getActiveArtifactDeploymentByGateway(
	ctx context.Context, artifactID, ouID, gatewayID, kindLabel string, notFound error,
) (map[string]string, error) {
	deployment, err := s.deploymentRepo.GetCurrentByGateway(artifactID, gatewayID, ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, notFound
	}

	resolvedYaml, err := s.resolveAllSecretsInYAML(ctx, string(deployment.Content))
	if err != nil {
		slog.Error("GatewayInternalAPIService: failed to resolve secrets in "+kindLabel+" YAML",
			"artifactID", artifactID, "error", err)
		return nil, fmt.Errorf("failed to resolve secrets: %w", err)
	}

	return map[string]string{artifactID: resolvedYaml}, nil
}

// GetActiveDeploymentByGateway retrieves the currently deployed API artifact for a specific gateway
func (s *GatewayInternalAPIService) GetActiveDeploymentByGateway(apiID, ouID, gatewayID string) (map[string]string, error) {
	// Get the active deployment for this API on this gateway
	deployment, err := s.deploymentRepo.GetCurrentByGateway(apiID, gatewayID, ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, utils.ErrDeploymentNotActive
	}

	// Deployment content is already stored as YAML
	apiYaml := string(deployment.Content)

	apiYamlMap := map[string]string{
		apiID: apiYaml,
	}
	return apiYamlMap, nil
}

// GetActiveLLMProviderDeploymentByGateway retrieves the currently deployed LLM provider artifact
func (s *GatewayInternalAPIService) GetActiveLLMProviderDeploymentByGateway(ctx context.Context, providerID, ouID, gatewayID string) (map[string]string, error) {
	provider, err := s.providerRepo.GetByUUID(providerID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrLLMProviderNotFound
		}
		return nil, fmt.Errorf("failed to get LLM provider: %w", err)
	}
	if provider == nil {
		return nil, utils.ErrLLMProviderNotFound
	}

	deployment, err := s.deploymentRepo.GetCurrentByGateway(provider.UUID.String(), gatewayID, ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, utils.ErrDeploymentNotActive
	}

	providerYaml := string(deployment.Content)

	// Resolve secret references in the YAML
	resolvedYaml, err := s.resolveSecretsForGateway(ctx, models.KindLLMProvider, providerYaml)
	if err != nil {
		slog.Error("GatewayInternalAPIService: failed to resolve secrets in provider YAML",
			"providerID", providerID, "error", err)
		return nil, fmt.Errorf("failed to resolve secrets: %w", err)
	}

	providerYamlMap := map[string]string{
		providerID: resolvedYaml,
	}
	return providerYamlMap, nil
}

// GetActiveLLMProxyDeploymentByGateway retrieves the currently deployed LLM proxy artifact
func (s *GatewayInternalAPIService) GetActiveLLMProxyDeploymentByGateway(ctx context.Context, proxyID, ouID, gatewayID string) (map[string]string, error) {
	proxy, err := s.proxyRepo.GetByID(proxyID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrLLMProxyNotFound
		}
		return nil, fmt.Errorf("failed to get LLM proxy: %w", err)
	}
	if proxy == nil {
		return nil, utils.ErrLLMProxyNotFound
	}

	deployment, err := s.deploymentRepo.GetCurrentByGateway(proxy.UUID.String(), gatewayID, ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, utils.ErrDeploymentNotActive
	}

	proxyYaml := string(deployment.Content)

	// Resolve secret references in the YAML
	resolvedYaml, err := s.resolveSecretsForGateway(ctx, models.KindLLMProxy, proxyYaml)
	if err != nil {
		slog.Error("GatewayInternalAPIService: failed to resolve secrets in proxy YAML",
			"proxyID", proxyID, "error", err)
		return nil, fmt.Errorf("failed to resolve secrets: %w", err)
	}

	proxyYamlMap := map[string]string{
		proxyID: resolvedYaml,
	}
	return proxyYamlMap, nil
}

// ListGatewayDeployments lists the gateway's deployments for its startup sync: every
// deployment_status row, named the way the gateway stores the artifact and in the state it is
// being driven to, so a row still DEPLOYING or UNDEPLOYING reads as deployed or undeployed.
//
// deployedAt is the row's performed_at. The gateway re-fetches a deployment whose deployedAt
// differs from what it holds, and from then on holds this value. The sync acks nothing, and the
// gateway skips as stale an event no newer than what it holds, so a row still awaiting its ack is
// listed a millisecond before its transition, and its event is sent again: newer than the synced
// copy, the gateway applies and acks it.
func (s *GatewayInternalAPIService) ListGatewayDeployments(ctx context.Context, ouID, gatewayID string) ([]models.ControlPlaneDeployment, error) {
	rows, err := s.deploymentRepo.ListForGatewaySync(ctx, gatewayID, ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway deployments: %w", err)
	}
	deployments := make([]models.ControlPlaneDeployment, 0, len(rows))
	awaitingAck := false
	for _, row := range rows {
		kind, ok := row.GatewayKind()
		if !ok {
			continue
		}
		// The gateway keeps deployedAt at millisecond precision.
		deployedAt := row.PerformedAt.UTC().Truncate(time.Millisecond)
		if row.AwaitsAck() {
			deployedAt = deployedAt.Add(-time.Millisecond)
			awaitingAck = true
		}
		deploymentID := row.DeploymentID.String()
		deployments = append(deployments, models.ControlPlaneDeployment{
			ArtifactID:   row.GatewayArtifactID(),
			DeploymentID: deploymentID,
			Kind:         kind,
			State:        strings.ToLower(string(row.StatusDesired)),
			DeployedAt:   deployedAt,
			Etag:         fmt.Sprintf("%s-%d", deploymentID, deployedAt.UnixMilli()),
		})
	}
	if awaitingAck && s.afterSyncListed != nil {
		s.afterSyncListed(ctx, gatewayID)
	}
	return deployments, nil
}

// GetGatewayDeploymentFiles returns the artifact YAML of each requested deployment the gateway is
// meant to hold, with secrets resolved as its single pull resolves them. Ids that name no such
// deployment are left out, and so is a deployment that can't be rendered: one bad deployment must
// not fail the gateway's whole sync.
func (s *GatewayInternalAPIService) GetGatewayDeploymentFiles(ctx context.Context, ouID, gatewayID string, deploymentIDs []string) ([]utils.DeploymentArchiveEntry, error) {
	ids := make([]uuid.UUID, 0, len(deploymentIDs))
	for _, id := range deploymentIDs {
		if parsed, err := uuid.Parse(id); err == nil {
			ids = append(ids, parsed)
		}
	}
	contents, err := s.deploymentRepo.GetContentsForGatewaySync(ctx, gatewayID, ouID, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to get gateway deployment contents: %w", err)
	}

	files := make([]utils.DeploymentArchiveEntry, 0, len(contents))
	for _, content := range contents {
		resolved, err := s.resolveSecretsForGateway(ctx, content.ArtifactKind, string(content.Content))
		if err != nil {
			slog.Warn("GatewayInternalAPIService: leaving a deployment out of the gateway's sync batch",
				"deploymentID", content.DeploymentID, "artifactUUID", content.ArtifactUUID, "gatewayID", gatewayID, "error", err)
			continue
		}
		files = append(files, utils.DeploymentArchiveEntry{
			DeploymentID: content.DeploymentID.String(),
			FileName:     gatewayArtifactFilePrefix(content.ArtifactKind) + content.GatewayArtifactID() + ".yaml",
			Content:      []byte(resolved),
		})
	}
	return files, nil
}

// gatewayArtifactFilePrefix is the file name prefix the kind's single pull gives its YAML.
func gatewayArtifactFilePrefix(artifactKind string) string {
	switch artifactKind {
	case models.KindLLMProvider:
		return "llm-provider-"
	case models.KindLLMProxy:
		return "llm-proxy-"
	case models.KindMCPProxy, models.KindMCPMapping:
		return "mcp-proxy-"
	default:
		return "agent-"
	}
}

// CheckArtifactsExist answers, for each id the gateway names an artifact by, whether AMS still has
// that artifact. The gateway keeps a local artifact missing from its deployment list when it does.
func (s *GatewayInternalAPIService) CheckArtifactsExist(ctx context.Context, ouID string, artifactIDs []string) ([]models.ArtifactExistence, error) {
	existing, err := s.artifactRepo.ExistingGatewayArtifactIDs(ctx, ouID, artifactIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to check artifact existence: %w", err)
	}
	exists := make(map[string]bool, len(existing))
	for _, id := range existing {
		exists[id] = true
	}
	answers := make([]models.ArtifactExistence, 0, len(artifactIDs))
	for _, id := range artifactIDs {
		answers = append(answers, models.ArtifactExistence{ArtifactID: id, Exists: exists[id]})
	}
	return answers, nil
}

// resolveSecretsForGateway resolves the secretRefs of an artifact of the kind into the values the
// gateway is served: an LLM provider's upstream auth, an LLM proxy's provider auth, and every
// secretRef in an MCP proxy or Agent, whose upstream auth may sit in several places.
func (s *GatewayInternalAPIService) resolveSecretsForGateway(ctx context.Context, artifactKind, yamlContent string) (string, error) {
	switch artifactKind {
	case models.KindLLMProvider:
		return s.resolveSecretsInYAML(ctx, yamlContent, "upstream.auth")
	case models.KindLLMProxy:
		return s.resolveSecretsInYAML(ctx, yamlContent, "provider.auth")
	default:
		return s.resolveAllSecretsInYAML(ctx, yamlContent)
	}
}

// resolveSecretsInYAML parses YAML, finds auth.secretRef, decrypts it,
// and replaces secretRef with the actual plaintext value.
// authPath indicates where in the YAML structure the auth block lives
// (e.g., "upstream.auth" for providers, "provider.auth" for proxies).
func (s *GatewayInternalAPIService) resolveSecretsInYAML(ctx context.Context, yamlContent, authPath string) (string, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return "", fmt.Errorf("failed to parse YAML: %w", err)
	}
	if doc == nil {
		return "", fmt.Errorf("YAML content is empty or null")
	}

	spec, ok := doc["spec"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("YAML is missing expected \"spec\" section")
	}

	// Navigate to the auth block based on authPath
	var auth map[string]interface{}
	parts := strings.Split(authPath, ".")
	current := spec
	for i, part := range parts {
		if i == len(parts)-1 {
			if a, ok := current[part].(map[string]interface{}); ok {
				auth = a
			}
		} else {
			if next, ok := current[part].(map[string]interface{}); ok {
				current = next
			} else {
				// Distinguish: if the parent key exists but has the wrong type, the config is malformed.
				// If the parent key is absent entirely, this is a legacy record with no auth — safe to skip.
				if _, parentExists := current[part]; parentExists {
					return "", fmt.Errorf("unexpected type for YAML path segment %q: expected map", part)
				}
				return yamlContent, nil
			}
		}
	}

	if auth == nil {
		return yamlContent, nil
	}

	secretRef, ok := auth["secretRef"].(string)
	if !ok || secretRef == "" {
		return yamlContent, nil // No secretRef, return as-is
	}

	plaintext, err := s.decryptSecretRef(secretRef)
	if err != nil {
		return "", err
	}

	// Replace secretRef with decrypted value
	auth["value"] = plaintext
	delete(auth, "secretRef")

	// Re-marshal to YAML
	resolved, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to re-marshal YAML after secret resolution: %w", err)
	}

	return string(resolved), nil
}

// resolveAllSecretsInYAML walks the entire YAML and decrypts every `secretRef` field it
// finds, replacing it with `value: <plaintext>`. Use this when secrets may appear at
// multiple or unknown locations in the doc (e.g. MCP proxy YAML, where the upstream auth
// is re-expressed as headers inside a set-headers policy). For YAMLs with a single
// known secret location, prefer resolveSecretsInYAML — it's targeted and won't touch
// any field outside the specified path.
func (s *GatewayInternalAPIService) resolveAllSecretsInYAML(_ context.Context, yamlContent string) (string, error) {
	var doc interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return "", fmt.Errorf("failed to parse YAML: %w", err)
	}
	changed, err := s.decryptSecretRefsIn(doc)
	if err != nil {
		return "", err
	}
	if !changed {
		return yamlContent, nil
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to re-marshal YAML after secret resolution: %w", err)
	}
	return string(out), nil
}

// decryptSecretRefsIn recursively visits v. For every map that has a non-empty string
// `secretRef`, it decrypts the value and rewrites the map to carry `value: <plaintext>`
// instead. Returns true if any rewrite happened.
func (s *GatewayInternalAPIService) decryptSecretRefsIn(v interface{}) (bool, error) {
	switch x := v.(type) {
	case map[string]interface{}:
		changed := false
		if ref, ok := x["secretRef"].(string); ok && ref != "" {
			plaintext, err := s.decryptSecretRef(ref)
			if err != nil {
				return false, err
			}
			x["value"] = plaintext
			delete(x, "secretRef")
			changed = true
		}
		for _, child := range x {
			c, err := s.decryptSecretRefsIn(child)
			if err != nil {
				return changed, err
			}
			changed = changed || c
		}
		return changed, nil
	case []interface{}:
		changed := false
		for _, child := range x {
			c, err := s.decryptSecretRefsIn(child)
			if err != nil {
				return changed, err
			}
			changed = changed || c
		}
		return changed, nil
	default:
		return false, nil
	}
}

func (s *GatewayInternalAPIService) decryptSecretRef(secretRef string) (string, error) {
	plaintext, err := utils.DecryptSecretRef(secretRef, s.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secretRef: %w", err)
	}
	return plaintext, nil
}
