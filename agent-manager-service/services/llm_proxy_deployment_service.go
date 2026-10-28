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

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const (
	apiVersionLLMProxy = "gateway.api-platform.wso2.com/v1"
	kindLLMProxy       = "LlmProxy"
)

// LLMProxyDeploymentService handles LLM proxy deployment business logic
type LLMProxyDeploymentService struct {
	deploymentRepo       repositories.DeploymentRepository
	proxyRepo            repositories.LLMProxyRepository
	providerRepo         repositories.LLMProviderRepository
	gatewayRepo          repositories.GatewayRepository
	gatewayEventsService *GatewayEventsService
	deploys              *reconcile.Core
	kind                 *LLMProxyKind
}

// NewLLMProxyDeploymentService creates a new LLM proxy deployment service
func NewLLMProxyDeploymentService(
	deploymentRepo repositories.DeploymentRepository,
	proxyRepo repositories.LLMProxyRepository,
	providerRepo repositories.LLMProviderRepository,
	gatewayRepo repositories.GatewayRepository,
	gatewayEventsService *GatewayEventsService,
	deploys *reconcile.Core,
	kind *LLMProxyKind,
) *LLMProxyDeploymentService {
	return &LLMProxyDeploymentService{
		deploymentRepo:       deploymentRepo,
		proxyRepo:            proxyRepo,
		providerRepo:         providerRepo,
		gatewayRepo:          gatewayRepo,
		gatewayEventsService: gatewayEventsService,
		deploys:              deploys,
		kind:                 kind,
	}
}

// LLMProxyDeploymentYAML represents the deployment YAML
type LLMProxyDeploymentYAML struct {
	ApiVersion string                 `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                 `yaml:"kind" json:"kind"`
	Metadata   DeploymentMetadata     `yaml:"metadata" json:"metadata"`
	Spec       LLMProxyDeploymentSpec `yaml:"spec" json:"spec"`
}

// LLMProxyDeploymentSpec represents the spec section
type LLMProxyDeploymentSpec struct {
	DisplayName string                     `yaml:"displayName" json:"displayName"`
	Version     string                     `yaml:"version" json:"version"`
	Context     string                     `yaml:"context,omitempty" json:"context,omitempty"`
	VHost       string                     `yaml:"vhost,omitempty" json:"vhost,omitempty"`
	Provider    LLMProxyDeploymentProvider `yaml:"provider" json:"provider"`
	Resilience  *models.Resilience         `yaml:"resilience,omitempty" json:"resilience,omitempty"`
	Policies    []models.LLMPolicy         `yaml:"policies,omitempty" json:"policies,omitempty"`
	Security    *models.SecurityConfig     `yaml:"security,omitempty" json:"security,omitempty"`
}

// LLMProxyDeploymentProvider represents the provider configuration in the spec
type LLMProxyDeploymentProvider struct {
	ID   string               `yaml:"id" json:"id"`
	Auth *models.UpstreamAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// DeployLLMProxy deploys an LLM proxy to the gateway the request names.
func (s *LLMProxyDeploymentService) DeployLLMProxy(proxyID string, req *models.DeployAPIRequest, ouID string) (*models.Deployment, error) {
	return s.deployLLMProxy(context.Background(), proxyID, nil, req, ouID)
}

// DeployLLMProxyInEnvironment deploys the proxy an agent configuration or a monitor provisioned for
// one environment, to the request's gateway in it. The intent is the environment's, so a later
// redeploy finds it there.
func (s *LLMProxyDeploymentService) DeployLLMProxyInEnvironment(
	ctx context.Context, proxyID string, envUUID uuid.UUID, req *models.DeployAPIRequest, ouID string,
) (*models.Deployment, error) {
	return s.deployLLMProxy(ctx, proxyID, &envUUID, req, ouID)
}

func (s *LLMProxyDeploymentService) deployLLMProxy(
	ctx context.Context, proxyID string, envUUID *uuid.UUID, req *models.DeployAPIRequest, ouID string,
) (*models.Deployment, error) {
	slog.Info("LLMProxyDeploymentService.DeployLLMProxy: starting", "proxyID", proxyID, "ouID", ouID,
		"deploymentName", req.Name, "base", req.Base, "gatewayID", req.GatewayID)

	if req.Base == "" {
		slog.Error("LLMProxyDeploymentService.DeployLLMProxy: base is required", "proxyID", proxyID)
		return nil, utils.ErrDeploymentBaseRequired
	}
	if req.GatewayID == "" {
		slog.Error("LLMProxyDeploymentService.DeployLLMProxy: gateway ID is required", "proxyID", proxyID)
		return nil, utils.ErrDeploymentGatewayIDRequired
	}
	if req.Name == "" {
		slog.Error("LLMProxyDeploymentService.DeployLLMProxy: deployment name is required", "proxyID", proxyID)
		return nil, utils.ErrDeploymentNameRequired
	}

	// Parse UUIDs
	gatewayUUID, err := uuid.Parse(req.GatewayID)
	if err != nil {
		slog.Error("LLMProxyDeploymentService.DeployLLMProxy: invalid gateway UUID", "proxyID", proxyID, "gatewayID", req.GatewayID, "error", err)
		return nil, fmt.Errorf("invalid gateway UUID: %w", err)
	}

	// Validate gateway exists
	slog.Info("LLMProxyDeploymentService.DeployLLMProxy: validating gateway", "proxyID", proxyID, "gatewayID", req.GatewayID)
	gateway, err := s.gatewayRepo.GetByUUID(req.GatewayID)
	if err != nil {
		slog.Error("LLMProxyDeploymentService.DeployLLMProxy: failed to get gateway", "proxyID", proxyID, "gatewayID", req.GatewayID, "error", err)
		return nil, fmt.Errorf("failed to get gateway: %w", err)
	}
	if gateway == nil || gateway.OUID != ouID {
		slog.Warn("LLMProxyDeploymentService.DeployLLMProxy: gateway not found or org mismatch", "proxyID", proxyID, "gatewayID", req.GatewayID, "ouID", ouID)
		return nil, utils.ErrGatewayNotFound
	}

	// Get LLM proxy
	slog.Info("LLMProxyDeploymentService.DeployLLMProxy: getting proxy", "proxyID", proxyID, "ouID", ouID)
	proxy, err := s.proxyRepo.GetByID(proxyID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Warn("LLMProxyDeploymentService.DeployLLMProxy: proxy not found", "proxyID", proxyID, "ouID", ouID)
			return nil, utils.ErrLLMProxyNotFound
		}
		slog.Error("LLMProxyDeploymentService.DeployLLMProxy: failed to get proxy", "proxyID", proxyID, "ouID", ouID, "error", err)
		return nil, fmt.Errorf("failed to get proxy: %w", err)
	}
	if proxy == nil {
		slog.Warn("LLMProxyDeploymentService.DeployLLMProxy: proxy not found", "proxyID", proxyID, "ouID", ouID)
		return nil, utils.ErrLLMProxyNotFound
	}

	slog.Info("LLMProxyDeploymentService.DeployLLMProxy: proxy retrieved", "proxyID", proxyID, "proxyUUID", proxy.UUID)

	existing, err := s.deploymentRepo.GetDeployedGatewaysByProvider(proxy.UUID, ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing proxy deployments: %w", err)
	}
	if err := validateEgressPlacement(s.gatewayRepo, gateway, existing); err != nil {
		return nil, fmt.Errorf("%w: %w", utils.ErrInvalidInput, err)
	}

	override := &reconcile.Override{Name: req.Name, Metadata: req.Metadata}
	if req.Base != "current" {
		slog.Info("LLMProxyDeploymentService.DeployLLMProxy: using existing deployment as base", "proxyID", proxyID, "baseDeploymentID", req.Base)
		baseUUID, err := uuid.Parse(req.Base)
		if err != nil {
			slog.Error("LLMProxyDeploymentService.DeployLLMProxy: invalid base deployment ID", "proxyID", proxyID, "baseDeploymentID", req.Base, "error", err)
			return nil, fmt.Errorf("invalid base deployment ID: %w", err)
		}
		baseDeployment, err := s.deploymentRepo.GetWithContent(req.Base, proxy.UUID.String(), ouID)
		if err != nil {
			slog.Warn("LLMProxyDeploymentService.DeployLLMProxy: base deployment not found", "proxyID", proxyID, "baseDeploymentID", req.Base, "error", err)
			return nil, utils.ErrBaseDeploymentNotFound
		}
		override.Content, override.BaseDeploymentID = baseDeployment.Content, &baseUUID
	}

	scope, found, err := s.deploys.ScopeFor(ctx, models.KindLLMProxy, ouID, proxy.UUID, gatewayUUID)
	if err != nil {
		return nil, err
	}
	if !found && envUUID != nil {
		scope = models.EnvironmentScope(*envUUID)
	}
	data := proxyIntentDataFor(proxy, proxyID, req.Name)
	if scope.EnvironmentUUID != nil {
		data.GatewayID = gatewayUUID.String()
	}
	intent, err := llmIntent(models.KindLLMProxy, ouID, proxy.UUID, scope, models.DeploymentStatusDeployed, data)
	if err != nil {
		return nil, err
	}
	applied, err := s.deploys.Apply(ctx, nil, &intent, override)
	if err != nil {
		slog.Error("LLMProxyDeploymentService.DeployLLMProxy: failed to deploy", "proxyID", proxyID, "gatewayID", req.GatewayID, "error", err)
		return nil, fmt.Errorf("failed to deploy proxy: %w", err)
	}
	applied.Publish(ctx)
	deployment := applied.Deployments[0]
	slog.Info("LLMProxyDeploymentService.DeployLLMProxy: deployment created", "proxyID", proxyID,
		"deploymentID", deployment.DeploymentID, "gatewayID", req.GatewayID)
	return deployment, nil
}

// proxyIntentDataFor is the kind data of proxy's intents: proxyID is the handle callers address
// it by, which gateways store it under.
func proxyIntentDataFor(proxy *models.LLMProxy, proxyID, name string) llmIntentData {
	handle := proxy.Handle
	if handle == "" {
		handle = proxyID
	}
	return llmIntentData{Name: name, Handle: handle, Vhost: proxyVhost(proxy)}
}

// RedeployStale renders the proxy again on a gateway it is placed on, because an input outside
// its configuration changed, such as its provider's auth header. It returns the attempt's error.
func (s *LLMProxyDeploymentService) RedeployStale(ctx context.Context, proxy *models.LLMProxy, gatewayID uuid.UUID, ouID, reason string) error {
	scope, _, err := s.deploys.ScopeFor(ctx, models.KindLLMProxy, ouID, proxy.UUID, gatewayID)
	if err != nil {
		return err
	}
	return s.deploys.MarkStale(ctx, models.KindLLMProxy, ouID, proxy.UUID, scope, reason)
}

// UndeployLLMProxyDeployment undeploys a deployment
func (s *LLMProxyDeploymentService) UndeployLLMProxyDeployment(ctx context.Context, proxyID, deploymentID, gatewayID, ouID string) (*models.Deployment, error) {
	slog.Info("LLMProxyDeploymentService.UndeployLLMProxyDeployment: starting", "proxyID", proxyID,
		"deploymentID", deploymentID, "gatewayID", gatewayID, "ouID", ouID)

	// Get proxy
	slog.Info("LLMProxyDeploymentService.UndeployLLMProxyDeployment: getting proxy", "proxyID", proxyID, "ouID", ouID)
	proxy, err := s.proxyRepo.GetByIDCtx(ctx, proxyID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Warn("LLMProxyDeploymentService.UndeployLLMProxyDeployment: proxy not found", "proxyID", proxyID)
			return nil, utils.ErrLLMProxyNotFound
		}
		slog.Error("LLMProxyDeploymentService.UndeployLLMProxyDeployment: failed to get proxy", "proxyID", proxyID, "error", err)
		return nil, fmt.Errorf("failed to get proxy: %w", err)
	}
	if proxy == nil {
		slog.Warn("LLMProxyDeploymentService.UndeployLLMProxyDeployment: proxy not found", "proxyID", proxyID)
		return nil, utils.ErrLLMProxyNotFound
	}

	// Get deployment
	slog.Info("LLMProxyDeploymentService.UndeployLLMProxyDeployment: getting deployment", "proxyID", proxyID, "deploymentID", deploymentID)
	deployment, err := s.deploymentRepo.GetWithStateCtx(ctx, deploymentID, proxy.UUID.String(), ouID)
	if err != nil {
		slog.Error("LLMProxyDeploymentService.UndeployLLMProxyDeployment: failed to get deployment", "proxyID", proxyID, "deploymentID", deploymentID, "error", err)
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		slog.Warn("LLMProxyDeploymentService.UndeployLLMProxyDeployment: deployment not found", "proxyID", proxyID, "deploymentID", deploymentID)
		return nil, utils.ErrDeploymentNotFound
	}
	if deployment.GatewayUUID.String() != gatewayID {
		slog.Error("LLMProxyDeploymentService.UndeployLLMProxyDeployment: gateway ID mismatch", "proxyID", proxyID,
			"deploymentID", deploymentID, "expectedGatewayID", gatewayID, "actualGatewayID", deployment.GatewayUUID.String())
		return nil, utils.ErrGatewayIDMismatch
	}
	// Placed deployments can be undeployed, including one still DEPLOYING or one that FAILED, and
	// so can one whose undeploy FAILED.
	if !deployment.IsUndeployable() {
		slog.Warn("LLMProxyDeploymentService.UndeployLLMProxyDeployment: deployment not active", "proxyID", proxyID,
			"deploymentID", deploymentID, "status", deployment.Status)
		return nil, utils.ErrDeploymentNotActive
	}

	applied, err := s.applyAtGateway(ctx, ouID, proxy, proxyID, deployment, models.DeploymentStatusUndeployed, func(intent *models.DeployIntent) (*reconcile.Applied, error) {
		return s.deploys.Apply(ctx, nil, intent, nil)
	})
	if err != nil {
		slog.Error("LLMProxyDeploymentService.UndeployLLMProxyDeployment: failed to undeploy", "proxyID", proxyID, "deploymentID", deploymentID, "error", err)
		return nil, fmt.Errorf("failed to undeploy: %w", err)
	}
	performedAt, ok := applied.PerformedAt(deployment.GatewayUUID)
	if !ok {
		return nil, utils.ErrDeploymentNotActive
	}
	markUndeploying(deployment, performedAt)
	applied.Publish(ctx)
	slog.Info("LLMProxyDeploymentService.UndeployLLMProxyDeployment: undeploy started", "proxyID", proxyID, "deploymentID", deploymentID)
	return deployment, nil
}

// applyAtGateway records the intent placing proxy on deployment's gateway, desired, and carries it
// out with apply.
func (s *LLMProxyDeploymentService) applyAtGateway(
	ctx context.Context, ouID string, proxy *models.LLMProxy, proxyID string, deployment *models.Deployment, desired models.DeploymentStatus,
	apply func(intent *models.DeployIntent) (*reconcile.Applied, error),
) (*reconcile.Applied, error) {
	scope, _, err := s.deploys.ScopeFor(ctx, models.KindLLMProxy, ouID, proxy.UUID, deployment.GatewayUUID)
	if err != nil {
		return nil, err
	}
	data := proxyIntentDataFor(proxy, proxyID, deployment.Name)
	if scope.EnvironmentUUID != nil {
		data.GatewayID = deployment.GatewayUUID.String()
	}
	intent, err := llmIntent(models.KindLLMProxy, ouID, proxy.UUID, scope, desired, data)
	if err != nil {
		return nil, err
	}
	return apply(&intent)
}

// RestoreLLMProxyDeployment restores a previous deployment
func (s *LLMProxyDeploymentService) RestoreLLMProxyDeployment(proxyID, deploymentID, gatewayID, ouID string) (*models.Deployment, error) {
	// Get proxy
	proxy, err := s.proxyRepo.GetByID(proxyID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrLLMProxyNotFound
		}
		return nil, fmt.Errorf("failed to get proxy: %w", err)
	}
	if proxy == nil {
		return nil, utils.ErrLLMProxyNotFound
	}

	// Get target deployment
	deployment, err := s.deploymentRepo.GetWithContent(deploymentID, proxy.UUID.String(), ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, utils.ErrDeploymentNotFound
	}
	if deployment.GatewayUUID.String() != gatewayID {
		return nil, utils.ErrGatewayIDMismatch
	}

	// Check if already deployed
	currentDeploymentID, status, _, err := s.deploymentRepo.GetStatus(proxy.UUID.String(), ouID, gatewayID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment status: %w", err)
	}
	if currentDeploymentID == deploymentID && isDeployedOrDeploying(status) {
		return nil, utils.ErrDeploymentAlreadyDeployed
	}

	ctx := context.Background()
	applied, err := s.applyAtGateway(ctx, ouID, proxy, proxyID, deployment, models.DeploymentStatusDeployed, func(intent *models.DeployIntent) (*reconcile.Applied, error) {
		return s.deploys.Restore(ctx, nil, intent, deployment.GatewayUUID, deployment.DeploymentID)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to restore deployment: %w", err)
	}
	performedAt, _ := applied.PerformedAt(deployment.GatewayUUID)
	markDeploying(deployment, performedAt)
	applied.Publish(ctx)
	return deployment, nil
}

// GetLLMProxyDeployments retrieves all deployments for a proxy
// GetDeployedGatewaysByProvider returns the gateway UUIDs where the given provider artifact is currently deployed.
func (s *LLMProxyDeploymentService) GetDeployedGatewaysByProvider(providerUUID uuid.UUID, ouID string) ([]string, error) {
	return s.deploymentRepo.GetDeployedGatewaysByProvider(providerUUID, ouID)
}

func (s *LLMProxyDeploymentService) GetLLMProxyDeployments(proxyID, ouID string, gatewayID *string, status *string) ([]*models.Deployment, error) {
	// Get proxy
	proxy, err := s.proxyRepo.GetByID(proxyID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrLLMProxyNotFound
		}
		return nil, fmt.Errorf("failed to get proxy: %w", err)
	}
	if proxy == nil {
		return nil, utils.ErrLLMProxyNotFound
	}

	if status != nil && !models.IsValidDeploymentStatusFilter(*status) {
		return nil, utils.ErrInvalidDeploymentStatus
	}

	// Get deployments
	deployments, err := s.deploymentRepo.GetDeploymentsWithState(proxy.UUID.String(), ouID, gatewayID, status, maxDeploymentsPerAPI)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployments: %w", err)
	}
	if err := overlayOpenStatuses(context.Background(), s.deploys, models.KindLLMProxy, ouID, proxy.UUID, deployments...); err != nil {
		return nil, err
	}

	return deployments, nil
}

// GetLLMProxyDeployment retrieves a specific deployment
func (s *LLMProxyDeploymentService) GetLLMProxyDeployment(proxyID, deploymentID, ouID string) (*models.Deployment, error) {
	// Get proxy
	proxy, err := s.proxyRepo.GetByID(proxyID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrLLMProxyNotFound
		}
		return nil, fmt.Errorf("failed to get proxy: %w", err)
	}
	if proxy == nil {
		return nil, utils.ErrLLMProxyNotFound
	}

	// Get deployment
	deployment, err := s.deploymentRepo.GetWithState(deploymentID, proxy.UUID.String(), ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, utils.ErrDeploymentNotFound
	}
	if err := overlayOpenStatuses(context.Background(), s.deploys, models.KindLLMProxy, ouID, proxy.UUID, deployment); err != nil {
		return nil, err
	}

	return deployment, nil
}

// DeleteLLMProxyDeployment deletes a deployment
func (s *LLMProxyDeploymentService) DeleteLLMProxyDeployment(proxyID, deploymentID, ouID string) error {
	// Get proxy
	proxy, err := s.proxyRepo.GetByID(proxyID, ouID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return utils.ErrLLMProxyNotFound
		}
		return fmt.Errorf("failed to get proxy: %w", err)
	}
	if proxy == nil {
		return utils.ErrLLMProxyNotFound
	}

	// Get deployment
	deployment, err := s.deploymentRepo.GetWithState(deploymentID, proxy.UUID.String(), ouID)
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return utils.ErrDeploymentNotFound
	}
	if deployment.IsPlaced() {
		return utils.ErrDeploymentIsDeployed
	}

	// Delete deployment
	if err := s.deploymentRepo.Delete(deploymentID, proxy.UUID.String(), ouID); err != nil {
		return fmt.Errorf("failed to delete deployment: %w", err)
	}

	return nil
}

// generateLLMProxyDeploymentYAML generates deployment YAML for an LLM proxy
func (s *LLMProxyDeploymentService) generateLLMProxyDeploymentYAML(proxy *models.LLMProxy, ouID string) (string, error) {
	if proxy == nil {
		return "", errors.New("proxy is required")
	}
	if proxy.Configuration.Provider == "" {
		return "", utils.ErrInvalidInput
	}

	// Get provider to validate it exists
	provider, err := s.providerRepo.GetByUUID(proxy.Configuration.Provider, ouID)
	if err != nil {
		return "", fmt.Errorf("failed to get provider: %w", err)
	}
	if provider == nil {
		return "", utils.ErrLLMProviderNotFound
	}

	// Set default context if not provided
	contextValue := "/"
	if proxy.Configuration.Context != nil && *proxy.Configuration.Context != "" {
		contextValue = *proxy.Configuration.Context
	}

	vhostValue := ""
	if proxy.Configuration.Vhost != nil {
		vhostValue = *proxy.Configuration.Vhost
	}

	// Initialize policies slice
	policies := make([]models.LLMPolicy, 0, len(proxy.Configuration.Policies))

	// Transform security config to policy if enabled
	security := proxy.Configuration.Security
	if security != nil && isBoolTrue(security.Enabled) {
		if security.APIKey != nil && isBoolTrue(security.APIKey.Enabled) {
			key := strings.TrimSpace(security.APIKey.Key)
			if key == "" {
				return "", fmt.Errorf("invalid api key security configuration: key is required: %w", utils.ErrInvalidInput)
			}

			in := strings.ToLower(strings.TrimSpace(security.APIKey.In))
			if in != "header" && in != "query" {
				return "", fmt.Errorf("invalid api key security configuration: in must be 'header' or 'query', got %q: %w", security.APIKey.In, utils.ErrInvalidInput)
			}

			// Add API key auth as a policy
			addOrAppendPolicyPath(&policies, apiKeyAuthPolicyName, apiKeyAuthPolicyVersion, models.LLMPolicyPath{
				Path:    "/*",
				Methods: []string{"*"},
				Params: map[string]interface{}{
					"key": key,
					"in":  in,
				},
			})
		}
	}

	// Process and normalize policies
	for _, p := range proxy.Configuration.Policies {
		// Deep copy paths
		paths := make([]models.LLMPolicyPath, 0, len(p.Paths))
		for _, pp := range p.Paths {
			paths = append(paths, models.LLMPolicyPath{
				Path:    pp.Path,
				Methods: pp.Methods,
				Params:  pp.Params,
			})
		}
		// Add policy with normalized version
		policies = append(policies, models.LLMPolicy{
			Name:    p.Name,
			Version: normalizePolicyVersionToMajor(p.Version),
			Paths:   paths,
		})
	}

	// Build provider reference
	providerRef := LLMProxyDeploymentProvider{
		ID: provider.Artifact.Handle,
	}

	// Add upstream auth if configured
	if proxy.Configuration.UpstreamAuth != nil {
		providerRef.Auth = proxy.Configuration.UpstreamAuth
	}

	// Build deployment YAML
	deploymentYAML := LLMProxyDeploymentYAML{
		ApiVersion: apiVersionLLMProxy,
		Kind:       kindLLMProxy,
		Metadata: DeploymentMetadata{
			Name: proxy.Handle, // Use handle (artifact identifier) for metadata.name
		},
		Spec: LLMProxyDeploymentSpec{
			DisplayName: proxy.Configuration.Name,
			Version:     proxy.Configuration.Version,
			Context:     contextValue,
			VHost:       vhostValue,
			Provider:    providerRef,
			Resilience:  proxy.Configuration.Resilience,
			Policies:    policies,
		},
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(deploymentYAML)
	if err != nil {
		return "", fmt.Errorf("failed to marshal to YAML: %w", err)
	}

	return string(yamlBytes), nil
}

// GatewayIDsForProxyDeletion returns every gateway that could still be holding
// this proxy's config: all gateways with deployment records, unioned with
// all active gateways in the organization.
//
// The union is deliberate. A proxy whose deployment record has already been
// cleaned up would otherwise be unreachable, leaving its config stranded in the
// gateway's own store forever — the gateway treats "undeployed" as a soft state
// change that preserves the config, so nothing else ever reclaims it.
//
// Must be called BEFORE deletion removes the proxy's deployment records.
func (s *LLMProxyDeploymentService) GatewayIDsForProxyDeletion(ctx context.Context, artifactUUID uuid.UUID, ouID string) ([]string, error) {
	gatewayIDs := map[string]struct{}{}
	if s.deploymentRepo != nil {
		tracked, err := s.deploymentRepo.GetTrackedGatewaysByProviderCtx(ctx, artifactUUID, ouID)
		if err != nil {
			return nil, fmt.Errorf("get tracked deletion gateways: %w", err)
		}
		for _, id := range tracked {
			if strings.TrimSpace(id) != "" {
				gatewayIDs[id] = struct{}{}
			}
		}
	}
	if s.gatewayRepo != nil {
		active := true
		gateways, err := s.gatewayRepo.ListWithFiltersCtx(ctx, repositories.GatewayFilterOptions{OrganizationID: ouID, Status: &active})
		if err != nil {
			return nil, fmt.Errorf("get active deletion gateways: %w", err)
		}
		for _, gateway := range gateways {
			if gateway != nil {
				gatewayIDs[gateway.UUID.String()] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(gatewayIDs))
	for id := range gatewayIDs {
		out = append(out, id)
	}
	return out, nil
}

// BroadcastLLMProxyDeletion tells the given gateways to drop this proxy's config
// outright. The paired llmproxy.undeployed event only flips the gateway's desired
// state and leaves the config (and its policy chains) in the xDS snapshot, so this
// is the only event that actually reclaims it.
//
// Best-effort by design: a deletion already committed in the database must not be
// rolled back because one gateway is unreachable.
func (s *LLMProxyDeploymentService) BroadcastLLMProxyDeletion(ctx context.Context, proxyID, ouID string, gatewayIDs []string) {
	// proxyID is the proxy handle: an LlmProxy config is stored under its handle, unlike the
	// LlmProvider, Mcp and RestApi configs, which are stored under a UUID.
	intent, err := llmIntent(models.KindLLMProxy, ouID, uuid.Nil, models.GatewayScope(uuid.Nil), models.DeploymentStatusUndeployed,
		llmIntentData{Handle: proxyID})
	if err != nil {
		slog.Error("LLMProxyDeploymentService.BroadcastLLMProxyDeletion: failed to build the deletion event", "proxyID", proxyID, "error", err)
		return
	}
	publishDeletion(ctx, s.gatewayEventsService, s.kind, intent, gatewayIDs)
}
