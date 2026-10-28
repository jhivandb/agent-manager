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
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// mcpIntentData is the MCP kind's own data on a deploy intent.
type mcpIntentData struct {
	// ProxyUUID is the MCP proxy whose endpoint the artifact deploys.
	ProxyUUID uuid.UUID `json:"proxyUuid"`
	// RequestedGatewayID is the egress gateway the request chose for a new binding; an existing
	// one stays where it is placed.
	RequestedGatewayID string `json:"requestedGatewayId,omitempty"`
}

// mcpIntent is the deploy intent placing one (endpoint, environment) artifact of proxyUUID.
func mcpIntent(ouID string, proxyUUID uuid.UUID, ee *models.MCPProxyEndpointEnvironment) (models.DeployIntent, error) {
	data := mcpIntentData{ProxyUUID: proxyUUID}
	if ee.RequestedGatewayUUID != nil {
		data.RequestedGatewayID = *ee.RequestedGatewayUUID
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return models.DeployIntent{}, fmt.Errorf("failed to encode the MCP intent: %w", err)
	}
	envUUID := ee.EnvironmentUUID
	return models.DeployIntent{
		OUID: ouID, Kind: models.KindMCPProxy, ArtifactUUID: ee.ArtifactUUID, EnvironmentUUID: &envUUID,
		Desired: models.DeploymentStatusDeployed, KindData: raw,
	}, nil
}

func decodeMCPIntentData(intent models.DeployIntent) (mcpIntentData, error) {
	var data mcpIntentData
	if err := json.Unmarshal(intent.KindData, &data); err != nil {
		return mcpIntentData{}, fmt.Errorf("failed to decode MCP intent %s: %w", intent.ID, err)
	}
	return data, nil
}

// MCPProxyKind deploys an MCP proxy endpoint's artifact for one environment to that environment's
// egress gateway. Gateways store it under the artifact's UUID.
type MCPProxyKind struct {
	repo           repositories.MCPProxyRepository
	endpointRepo   repositories.MCPProxyEndpointRepository
	scopeRepo      repositories.MCPProxyScopeRepository
	deploymentRepo repositories.DeploymentRepository
	gatewayRepo    repositories.GatewayRepository
	renderer       *MCPProxyService
}

var (
	_ reconcile.Kind    = (*MCPProxyKind)(nil)
	_ reconcile.Adopter = (*MCPProxyKind)(nil)
)

// NewMCPProxyKind creates the MCP proxy kind.
func NewMCPProxyKind(
	repo repositories.MCPProxyRepository,
	endpointRepo repositories.MCPProxyEndpointRepository,
	scopeRepo repositories.MCPProxyScopeRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
) *MCPProxyKind {
	return &MCPProxyKind{
		repo: repo, endpointRepo: endpointRepo, scopeRepo: scopeRepo, deploymentRepo: deploymentRepo,
		gatewayRepo: gatewayRepo, renderer: &MCPProxyService{},
	}
}

// Name is the intents' kind for MCP proxy artifacts.
func (k *MCPProxyKind) Name() string { return models.KindMCPProxy }

// Targets is the environment's egress gateway, anchored on the one the artifact is already placed
// on so a second egress gateway never moves it.
func (k *MCPProxyKind) Targets(_ context.Context, intent models.DeployIntent) ([]uuid.UUID, error) {
	if intent.EnvironmentUUID == nil {
		return nil, reconcile.Permanent(fmt.Errorf("MCP intent %s is not scoped to an environment", intent.ID))
	}
	data, err := decodeMCPIntentData(intent)
	if err != nil {
		return nil, err
	}
	placed, err := k.deploymentRepo.GetDeployedGatewaysByProvider(intent.ArtifactUUID, intent.OUID)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing deployments: %w", err)
	}
	var requested *string
	if data.RequestedGatewayID != "" {
		requested = &data.RequestedGatewayID
	}
	gateway, err := resolveEgressGatewayForArtifact(k.gatewayRepo, intent.OUID, *intent.EnvironmentUUID, placed, requested)
	if err != nil {
		return nil, err
	}
	return []uuid.UUID{gateway.UUID}, nil
}

// Render builds the Mcp resource for the endpoint bound to the environment, with the proxy's
// scopes as its tool policies.
func (k *MCPProxyKind) Render(ctx context.Context, intent models.DeployIntent, _ uuid.UUID) (reconcile.Rendered, error) {
	data, err := decodeMCPIntentData(intent)
	if err != nil {
		return reconcile.Rendered{}, err
	}
	proxy, err := k.repo.GetByUUID(ctx, data.ProxyUUID.String(), intent.OUID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return reconcile.Rendered{}, reconcile.Permanent(utils.ErrMCPProxyNotFound)
	}
	if err != nil {
		return reconcile.Rendered{}, fmt.Errorf("failed to get MCP proxy: %w", err)
	}
	endpoint, ee := boundEndpoint(proxy, intent.ArtifactUUID)
	if ee == nil {
		return reconcile.Rendered{}, reconcile.Permanent(fmt.Errorf("no endpoint of MCP proxy %s deploys artifact %s", proxy.UUID, intent.ArtifactUUID))
	}
	scopes, err := k.scopeRepo.ListByProxy(ctx, proxy.UUID)
	if err != nil {
		return reconcile.Rendered{}, fmt.Errorf("failed to load proxy scopes for emission: %w", err)
	}
	deployProxy := buildMCPProxyEnvArtifact(proxy, endpoint, ee)
	content, err := k.renderer.generateMCPProxyDeploymentYAML(deployProxy, mcpSourceHandle(proxy), scopes)
	if err != nil {
		return reconcile.Rendered{}, permanentIfInvalid(fmt.Errorf("failed to generate deployment YAML: %w", err), utils.ErrInvalidInput)
	}
	return reconcile.Rendered{Name: deploymentName(deployProxy), Content: []byte(content)}, nil
}

// boundEndpoint finds the endpoint and environment binding that deploy artifactUUID.
func boundEndpoint(proxy *models.MCPProxy, artifactUUID uuid.UUID) (*models.MCPProxyEndpoint, *models.MCPProxyEndpointEnvironment) {
	for i := range proxy.Endpoints {
		for j := range proxy.Endpoints[i].Environments {
			if proxy.Endpoints[i].Environments[j].ArtifactUUID == artifactUUID {
				return &proxy.Endpoints[i], &proxy.Endpoints[i].Environments[j]
			}
		}
	}
	return nil, nil
}

// mcpSourceHandle is the source proxy's handle, which prefixes its scope policies.
func mcpSourceHandle(proxy *models.MCPProxy) string {
	if proxy.Artifact != nil && proxy.Artifact.Handle != "" {
		return proxy.Artifact.Handle
	}
	return proxy.Handle
}

// Adopt finds the proxy whose endpoint binding deploys an artifact placed before intents.
func (k *MCPProxyKind) Adopt(ctx context.Context, intent *models.DeployIntent) error {
	ee, err := k.endpointRepo.GetEndpointEnvByArtifact(ctx, intent.ArtifactUUID)
	if err != nil {
		return fmt.Errorf("failed to find the MCP binding of artifact %s: %w", intent.ArtifactUUID, err)
	}
	adopted, err := mcpIntent(intent.OUID, ee.MCPProxyUUID, ee)
	if err != nil {
		return err
	}
	intent.KindData = adopted.KindData
	return nil
}

// DeployEvent is the gateway's mcpproxy.deployed.
func (k *MCPProxyKind) DeployEvent(ref reconcile.EventRef) reconcile.Event {
	proxyID := ref.Intent.ArtifactUUID.String()
	return reconcile.Event{
		Type: "mcpproxy.deployed", Action: "CREATE", EntityID: proxyID,
		Payload: &models.MCPProxyDeploymentEvent{ProxyID: proxyID, DeploymentID: ref.DeploymentID, PerformedAt: ref.PerformedAt},
	}
}

// UndeployEvent removes the artifact: MCP has no undeploy, so the gateway is told to delete it.
func (k *MCPProxyKind) UndeployEvent(ref reconcile.EventRef) reconcile.Event {
	return k.DeleteEvent(ref)
}

// DeleteEvent is the gateway's mcpproxy.deleted.
func (k *MCPProxyKind) DeleteEvent(ref reconcile.EventRef) reconcile.Event {
	proxyID := ref.Intent.ArtifactUUID.String()
	return reconcile.Event{
		Type: "mcpproxy.deleted", Action: "DELETE", EntityID: proxyID,
		Payload: &models.MCPProxyDeletionEvent{ProxyID: proxyID},
	}
}
