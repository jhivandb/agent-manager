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
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// llmIntentData is the LLM provider and proxy kinds' own data on a deploy intent.
type llmIntentData struct {
	// Name names the deployments; a request's deployment name is kept for later redeploys.
	Name string `json:"name,omitempty"`
	// GatewayID is where an environment intent places its artifact, chosen by the request that
	// recorded it; a gateway intent's scope names its gateway.
	GatewayID string `json:"gatewayId,omitempty"`
	// Handle is the proxy's handle, the id gateways store proxies under.
	Handle string `json:"handle,omitempty"`
	// Vhost is the proxy's vhost, which its events carry.
	Vhost string `json:"vhost,omitempty"`
}

func decodeLLMIntentData(intent models.DeployIntent) (llmIntentData, error) {
	var data llmIntentData
	if len(intent.KindData) == 0 {
		return data, nil
	}
	if err := json.Unmarshal(intent.KindData, &data); err != nil {
		return llmIntentData{}, fmt.Errorf("failed to decode intent %s: %w", intent.ID, err)
	}
	return data, nil
}

func (d llmIntentData) encode() (json.RawMessage, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the deploy intent: %w", err)
	}
	return raw, nil
}

// llmIntent is the deploy intent placing an LLM provider or proxy artifact in scope.
func llmIntent(kind, ouID string, artifactUUID uuid.UUID, scope models.DeployScope, desired models.DeploymentStatus, data llmIntentData) (models.DeployIntent, error) {
	raw, err := data.encode()
	if err != nil {
		return models.DeployIntent{}, err
	}
	return models.DeployIntent{
		OUID: ouID, Kind: kind, ArtifactUUID: artifactUUID,
		EnvironmentUUID: scope.EnvironmentUUID, GatewayUUID: scope.GatewayUUID,
		Desired: desired, KindData: raw,
	}, nil
}

// llmIntentTargets is the gateway an LLM provider or proxy intent places its artifact on: the one
// its scope names, the one its request chose for an environment intent, or else the gateway the
// artifact is already placed on in that environment.
func llmIntentTargets(gatewayRepo repositories.GatewayRepository, deploymentRepo repositories.DeploymentRepository, intent models.DeployIntent) ([]uuid.UUID, error) {
	if intent.GatewayUUID != nil {
		return []uuid.UUID{*intent.GatewayUUID}, nil
	}
	if intent.EnvironmentUUID == nil {
		return nil, fmt.Errorf("intent %s has no scope", intent.ID)
	}
	data, err := decodeLLMIntentData(intent)
	if err != nil {
		return nil, err
	}
	if data.GatewayID != "" {
		gatewayID, err := uuid.Parse(data.GatewayID)
		if err != nil {
			return nil, reconcile.Permanent(fmt.Errorf("intent %s names an invalid gateway: %w", intent.ID, err))
		}
		return []uuid.UUID{gatewayID}, nil
	}
	placed, err := deploymentRepo.GetDeployedGatewaysByProvider(intent.ArtifactUUID, intent.OUID)
	if err != nil {
		return nil, fmt.Errorf("failed to read where the artifact is placed: %w", err)
	}
	gateway, err := resolveEgressGatewayForArtifact(gatewayRepo, intent.OUID, *intent.EnvironmentUUID, placed, nil)
	if err != nil {
		return nil, err
	}
	return []uuid.UUID{gateway.UUID}, nil
}

// deploymentNameFor is data's deployment name, or one derived from the artifact's handle.
func deploymentNameFor(data llmIntentData, handle string) string {
	if data.Name != "" {
		return data.Name
	}
	return handle + "-deployment"
}

// permanentIfInvalid marks a render error that a retry cannot fix, keeping it matchable.
func permanentIfInvalid(err error, invalid ...error) error {
	for _, target := range invalid {
		if errors.Is(err, target) {
			return reconcile.Permanent(err)
		}
	}
	return err
}

// LLMProviderKind deploys LLM providers to the gateways their deploy requests name. Gateways store
// a provider under its UUID.
type LLMProviderKind struct {
	providerRepo repositories.LLMProviderRepository
	renderer     *LLMProviderDeploymentService
}

var _ reconcile.Kind = (*LLMProviderKind)(nil)

// NewLLMProviderKind creates the LLM provider kind.
func NewLLMProviderKind(providerRepo repositories.LLMProviderRepository) *LLMProviderKind {
	return &LLMProviderKind{providerRepo: providerRepo, renderer: &LLMProviderDeploymentService{}}
}

// Name is the gateway kind LLM providers are deployed as.
func (k *LLMProviderKind) Name() string { return models.KindLLMProvider }

// Targets is the gateway the deploy request named.
func (k *LLMProviderKind) Targets(_ context.Context, intent models.DeployIntent) ([]uuid.UUID, error) {
	if intent.GatewayUUID == nil {
		return nil, reconcile.Permanent(fmt.Errorf("LLM provider intent %s is not scoped to a gateway", intent.ID))
	}
	return []uuid.UUID{*intent.GatewayUUID}, nil
}

// Render builds the LlmProvider resource from the provider's current configuration.
func (k *LLMProviderKind) Render(_ context.Context, intent models.DeployIntent, _ uuid.UUID) (reconcile.Rendered, error) {
	data, err := decodeLLMIntentData(intent)
	if err != nil {
		return reconcile.Rendered{}, err
	}
	provider, err := k.providerRepo.GetByUUID(intent.ArtifactUUID.String(), intent.OUID)
	if err != nil {
		return reconcile.Rendered{}, fmt.Errorf("failed to get provider: %w", err)
	}
	if provider == nil {
		return reconcile.Rendered{}, reconcile.Permanent(utils.ErrLLMProviderNotFound)
	}
	if provider.ModelList != "" {
		if err := json.Unmarshal([]byte(provider.ModelList), &provider.ModelProviders); err != nil {
			return reconcile.Rendered{}, reconcile.Permanent(fmt.Errorf("failed to parse model providers: %w", err))
		}
	}
	content, err := k.renderer.generateLLMProviderDeploymentYAML(provider, intent.OUID)
	if err != nil {
		return reconcile.Rendered{}, permanentIfInvalid(fmt.Errorf("failed to generate deployment YAML: %w", err),
			utils.ErrInvalidInput, utils.ErrLLMProviderTemplateNotFound)
	}
	return reconcile.Rendered{Name: deploymentNameFor(data, providerHandle(provider)), Content: []byte(content)}, nil
}

func providerHandle(provider *models.LLMProvider) string {
	if provider.Artifact != nil && provider.Artifact.Handle != "" {
		return provider.Artifact.Handle
	}
	return strings.TrimSpace(provider.Configuration.Handle)
}

// DeployEvent is the gateway's llmprovider.deployed.
func (k *LLMProviderKind) DeployEvent(ref reconcile.EventRef) reconcile.Event {
	providerID := ref.Intent.ArtifactUUID.String()
	return reconcile.Event{
		Type: "llmprovider.deployed", Action: "CREATE", EntityID: providerID,
		Payload: &models.LLMProviderDeploymentEvent{
			ProviderID: providerID, DeploymentID: ref.DeploymentID, PerformedAt: ref.PerformedAt,
			GatewayID: ref.GatewayID, OrganizationID: ref.Intent.OUID, Status: string(models.DeploymentStatusDeployed),
		},
	}
}

// UndeployEvent is the gateway's llmprovider.undeployed: it stops serving the provider but keeps
// its config.
func (k *LLMProviderKind) UndeployEvent(ref reconcile.EventRef) reconcile.Event {
	providerID := ref.Intent.ArtifactUUID.String()
	return reconcile.Event{
		Type: "llmprovider.undeployed", Action: "DELETE", EntityID: providerID,
		Payload: &models.LLMProviderUndeploymentEvent{
			ProviderID: providerID, DeploymentID: ref.DeploymentID, PerformedAt: ref.PerformedAt,
			GatewayID: ref.GatewayID, OrganizationID: ref.Intent.OUID,
		},
	}
}

// DeleteEvent is the gateway's llmprovider.deleted, the only event that drops a provider's config.
func (k *LLMProviderKind) DeleteEvent(ref reconcile.EventRef) reconcile.Event {
	providerID := ref.Intent.ArtifactUUID.String()
	return reconcile.Event{
		Type: "llmprovider.deleted", Action: "DELETE", EntityID: providerID,
		Payload: &models.LLMProviderDeletionEvent{ProviderID: providerID},
	}
}

// publishDeletion tells each gateway to drop a deleted artifact; best effort, as the deletion has
// already committed.
func publishDeletion(ctx context.Context, events *GatewayEventsService, kind reconcile.Kind, intent models.DeployIntent, gatewayIDs []string) {
	if events == nil {
		return
	}
	for _, gatewayID := range gatewayIDs {
		if strings.TrimSpace(gatewayID) == "" {
			continue
		}
		event := kind.DeleteEvent(reconcile.EventRef{Intent: intent, GatewayID: gatewayID})
		if err := events.PublishGatewayEvent(ctx, gatewayID, event); err != nil {
			slog.Warn("Failed to send a deletion event", "kind", kind.Name(), "artifactID", event.EntityID,
				"gatewayID", gatewayID, "error", err)
			continue
		}
		slog.Info("Deletion event sent", "kind", kind.Name(), "artifactID", event.EntityID, "gatewayID", gatewayID)
	}
}
