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

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// LLMProxyKind deploys LLM proxies: to the gateway a deploy request names, or, for an agent's or
// a monitor's proxy, to its environment's gateway. Gateways store a proxy under its handle.
type LLMProxyKind struct {
	proxyRepo      repositories.LLMProxyRepository
	artifactRepo   repositories.ArtifactRepository
	deploymentRepo repositories.DeploymentRepository
	gatewayRepo    repositories.GatewayRepository
	renderer       *LLMProxyDeploymentService
}

var (
	_ reconcile.Kind    = (*LLMProxyKind)(nil)
	_ reconcile.Adopter = (*LLMProxyKind)(nil)
)

// NewLLMProxyKind creates the LLM proxy kind.
func NewLLMProxyKind(
	proxyRepo repositories.LLMProxyRepository,
	providerRepo repositories.LLMProviderRepository,
	artifactRepo repositories.ArtifactRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
) *LLMProxyKind {
	return &LLMProxyKind{
		proxyRepo: proxyRepo, artifactRepo: artifactRepo, deploymentRepo: deploymentRepo, gatewayRepo: gatewayRepo,
		renderer: &LLMProxyDeploymentService{providerRepo: providerRepo},
	}
}

// Name is the gateway kind LLM proxies are deployed as.
func (k *LLMProxyKind) Name() string { return models.KindLLMProxy }

// Targets is the gateway the intent places the proxy on.
func (k *LLMProxyKind) Targets(_ context.Context, intent models.DeployIntent) ([]uuid.UUID, error) {
	return llmIntentTargets(k.gatewayRepo, k.deploymentRepo, intent)
}

// Render builds the LlmProxy resource from the proxy's current configuration.
func (k *LLMProxyKind) Render(ctx context.Context, intent models.DeployIntent, _ uuid.UUID) (reconcile.Rendered, error) {
	data, err := decodeLLMIntentData(intent)
	if err != nil {
		return reconcile.Rendered{}, err
	}
	proxy, err := k.proxyRepo.GetByIDCtx(ctx, data.Handle, intent.OUID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && proxy == nil) {
		return reconcile.Rendered{}, reconcile.Permanent(utils.ErrLLMProxyNotFound)
	}
	if err != nil {
		return reconcile.Rendered{}, fmt.Errorf("failed to get proxy: %w", err)
	}
	content, err := k.renderer.generateLLMProxyDeploymentYAML(proxy, intent.OUID)
	if err != nil {
		return reconcile.Rendered{}, permanentIfInvalid(fmt.Errorf("failed to generate deployment YAML: %w", err),
			utils.ErrInvalidInput, utils.ErrLLMProviderNotFound)
	}
	return reconcile.Rendered{Name: deploymentNameFor(data, data.Handle), Content: []byte(content)}, nil
}

// Adopt fills in the handle and vhost of a proxy deployed before intents, and keeps the name of
// its current deployment on the intent's gateway.
func (k *LLMProxyKind) Adopt(ctx context.Context, intent *models.DeployIntent) error {
	artifact, err := k.artifactRepo.GetByUUID(intent.ArtifactUUID.String(), intent.OUID)
	if err != nil {
		return fmt.Errorf("failed to read the proxy's artifact: %w", err)
	}
	data := llmIntentData{Handle: artifact.Handle}
	proxy, err := k.proxyRepo.GetByIDCtx(ctx, artifact.Handle, intent.OUID)
	if err != nil {
		return fmt.Errorf("failed to get proxy: %w", err)
	}
	data.Vhost = proxyVhost(proxy)
	if intent.GatewayUUID != nil {
		current, err := k.deploymentRepo.GetCurrentByGateway(intent.ArtifactUUID.String(), intent.GatewayUUID.String(), intent.OUID)
		if err != nil {
			return fmt.Errorf("failed to read the proxy's current deployment: %w", err)
		}
		if current != nil {
			data.Name = current.Name
		}
	}
	intent.KindData, err = data.encode()
	return err
}

func proxyVhost(proxy *models.LLMProxy) string {
	if proxy == nil || proxy.Configuration.Vhost == nil {
		return ""
	}
	return *proxy.Configuration.Vhost
}

// proxyIntentData decodes the handle and vhost every proxy event carries; an intent that cannot
// be decoded was never written by this kind.
func proxyIntentData(intent models.DeployIntent) llmIntentData {
	data, _ := decodeLLMIntentData(intent)
	return data
}

// DeployEvent is the gateway's llmproxy.deployed, keyed by the proxy's handle.
func (k *LLMProxyKind) DeployEvent(ref reconcile.EventRef) reconcile.Event {
	data := proxyIntentData(ref.Intent)
	return reconcile.Event{
		Type: "llmproxy.deployed", Action: "CREATE", EntityID: data.Handle,
		Payload: &models.LLMProxyDeploymentEvent{
			ProxyID: data.Handle, DeploymentID: ref.DeploymentID, PerformedAt: ref.PerformedAt, Vhost: data.Vhost,
			Environment: "production", GatewayID: ref.GatewayID, OrganizationID: ref.Intent.OUID,
			Status: string(models.DeploymentStatusDeployed),
		},
	}
}

// UndeployEvent is the gateway's llmproxy.undeployed: it stops serving the proxy but keeps its
// config.
func (k *LLMProxyKind) UndeployEvent(ref reconcile.EventRef) reconcile.Event {
	data := proxyIntentData(ref.Intent)
	return reconcile.Event{
		Type: "llmproxy.undeployed", Action: "DELETE", EntityID: data.Handle,
		Payload: &models.LLMProxyUndeploymentEvent{
			ProxyID: data.Handle, DeploymentID: ref.DeploymentID, PerformedAt: ref.PerformedAt, Vhost: data.Vhost,
			Environment: "production", GatewayID: ref.GatewayID, OrganizationID: ref.Intent.OUID,
		},
	}
}

// DeleteEvent is the gateway's llmproxy.deleted, the only event that drops a proxy's config.
func (k *LLMProxyKind) DeleteEvent(ref reconcile.EventRef) reconcile.Event {
	data := proxyIntentData(ref.Intent)
	return reconcile.Event{
		Type: "llmproxy.deleted", Action: "DELETE", EntityID: data.Handle,
		Payload: &models.LLMProxyDeletionEvent{ProxyID: data.Handle},
	}
}
