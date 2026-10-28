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
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// errUpstreamNotReady means the binding has not published a ServiceURL yet. It is the expected
// condition until the first build or deploy binds, not a misconfiguration, so the intent waits
// for it without limit.
var errUpstreamNotReady = reconcile.InputNotReady("release binding has not published a service URL yet")

// a2aIntentData is the A2A kind's own data on a deploy intent.
type a2aIntentData struct {
	ProjectName     string `json:"projectName"`
	AgentName       string `json:"agentName"`
	EnvironmentName string `json:"environmentName"`
	// AgentUUID is the agent the intent was recorded for; nil when it was unknown.
	AgentUUID *uuid.UUID                  `json:"agentUuid,omitempty"`
	Source    models.A2APublicationSource `json:"source"`
}

// a2aPublication is an A2A agent's deploy intent for one environment, with its kind data decoded.
type a2aPublication struct {
	OUID            string
	ProjectName     string
	AgentName       string
	EnvironmentName string
	EnvironmentUUID uuid.UUID
	// ArtifactUUID is the per-environment models.KindAgent artifact row: the agentId the gateway
	// is told about and the key its API keys are bound to.
	ArtifactUUID uuid.UUID
	AgentUUID    *uuid.UUID
	Source       models.A2APublicationSource
}

// a2aIntent is the deploy intent publishing the agent of key from source through artifactUUID.
func a2aIntent(key a2aPublicationKey, source models.A2APublicationSource, artifactUUID uuid.UUID) (models.DeployIntent, error) {
	if source == "" {
		source = models.A2APublicationSourcePlatform
	}
	data, err := json.Marshal(a2aIntentData{
		ProjectName: key.ProjectName, AgentName: key.AgentName, EnvironmentName: key.EnvironmentName,
		AgentUUID: key.AgentUUID, Source: source,
	})
	if err != nil {
		return models.DeployIntent{}, fmt.Errorf("failed to encode the A2A intent: %w", err)
	}
	envUUID := key.EnvironmentUUID
	return models.DeployIntent{
		OUID: key.OUID, Kind: models.KindAgent, ArtifactUUID: artifactUUID, EnvironmentUUID: &envUUID,
		Desired: models.DeploymentStatusDeployed, KindData: data,
	}, nil
}

// decodeA2APublication reads an A2A agent's deploy intent.
func decodeA2APublication(intent models.DeployIntent) (a2aPublication, error) {
	var data a2aIntentData
	if err := json.Unmarshal(intent.KindData, &data); err != nil {
		return a2aPublication{}, fmt.Errorf("failed to decode A2A intent %s: %w", intent.ID, err)
	}
	if intent.EnvironmentUUID == nil {
		return a2aPublication{}, fmt.Errorf("A2A intent %s is not scoped to an environment", intent.ID)
	}
	if data.Source == "" {
		data.Source = models.A2APublicationSourcePlatform
	}
	return a2aPublication{
		OUID: intent.OUID, ProjectName: data.ProjectName, AgentName: data.AgentName,
		EnvironmentName: data.EnvironmentName, EnvironmentUUID: *intent.EnvironmentUUID,
		ArtifactUUID: intent.ArtifactUUID, AgentUUID: data.AgentUUID, Source: data.Source,
	}, nil
}

// key names the publication's agent and environment.
func (pub a2aPublication) key() a2aPublicationKey {
	return a2aPublicationKey{
		OUID: pub.OUID, ProjectName: pub.ProjectName, AgentName: pub.AgentName,
		EnvironmentName: pub.EnvironmentName, EnvironmentUUID: pub.EnvironmentUUID, AgentUUID: pub.AgentUUID,
	}
}

// A2AAgentKind publishes an A2A agent through its environment's ingress gateway as a gateway
// Agent: a platform agent in front of its release binding, or a proxied external agent in front
// of the origin its registered card names.
type A2AAgentKind struct {
	tx              TxRunner
	cardRepo        repositories.A2AAgentCardRepository
	proxyRepo       repositories.A2AProxyRepository
	deploymentRepo  repositories.DeploymentRepository
	gatewayRepo     repositories.GatewayRepository
	agentConfigRepo repositories.AgentConfigRepository
	ocClient        client.OpenChoreoClient
	fetcher         A2ACardFetcher
	encryptionKey   []byte
	logger          *slog.Logger
}

var (
	_ reconcile.Kind           = (*A2AAgentKind)(nil)
	_ reconcile.AfterLiveHook  = (*A2AAgentKind)(nil)
	_ reconcile.DriftChecker   = (*A2AAgentKind)(nil)
	_ reconcile.ErrorDescriber = (*A2AAgentKind)(nil)
	_ reconcile.FailureHook    = (*A2AAgentKind)(nil)
)

// NewA2AAgentKind creates the A2A agent kind.
func NewA2AAgentKind(
	tx TxRunner,
	cardRepo repositories.A2AAgentCardRepository,
	proxyRepo repositories.A2AProxyRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
	agentConfigRepo repositories.AgentConfigRepository,
	ocClient client.OpenChoreoClient,
	fetcher A2ACardFetcher,
	encryptionKey []byte,
	logger *slog.Logger,
) *A2AAgentKind {
	return &A2AAgentKind{
		tx:              tx,
		cardRepo:        cardRepo,
		proxyRepo:       proxyRepo,
		deploymentRepo:  deploymentRepo,
		gatewayRepo:     gatewayRepo,
		agentConfigRepo: agentConfigRepo,
		ocClient:        ocClient,
		fetcher:         fetcher,
		encryptionKey:   encryptionKey,
		logger:          logger,
	}
}

// Name is the gateway kind A2A agents are published as.
func (k *A2AAgentKind) Name() string { return models.KindAgent }

// Targets is the environment's ingress gateway, where inbound A2A traffic belongs.
func (k *A2AAgentKind) Targets(_ context.Context, intent models.DeployIntent) ([]uuid.UUID, error) {
	if intent.EnvironmentUUID == nil {
		return nil, fmt.Errorf("A2A intent %s is not scoped to an environment", intent.ID)
	}
	gateway, err := resolveIngressGateway(k.gatewayRepo, intent.OUID, *intent.EnvironmentUUID)
	if err != nil {
		return nil, err
	}
	return []uuid.UUID{gateway.UUID}, nil
}

// Render builds the Agent resource: its policies are the environment's config as a chat or custom
// agent gets them, plus the A2A version header; the card route has its own.
func (k *A2AAgentKind) Render(ctx context.Context, intent models.DeployIntent, _ uuid.UUID) (reconcile.Rendered, error) {
	pub, err := decodeA2APublication(intent)
	if err != nil {
		return reconcile.Rendered{}, err
	}
	upstream, origin, err := k.resolveUpstream(ctx, pub)
	if err != nil {
		return reconcile.Rendered{}, err
	}
	cfg, err := k.agentConfigRepo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
	if err != nil {
		return reconcile.Rendered{}, fmt.Errorf("failed to load agent config: %w", err)
	}
	// The third-party origin is only called once every local lookup has succeeded.
	if origin != nil {
		if upstream, err = k.fetchExternalUpstream(ctx, origin); err != nil {
			return reconcile.Rendered{}, err
		}
	}
	apiCfg := withA2AVersionHeader(resolveAPIConfig(cfg, nil, nil, nil, nil, false))
	if origin != nil {
		// A third-party origin never receives the caller's token, whatever the saved config says.
		apiCfg.OAuthForwardToken = false
	}
	yamlStr, err := generateA2AAgentDeploymentYAML(A2AAgentDeploymentInput{
		ArtifactName: a2aAgentEnvArtifactName(pub.ProjectName, pub.AgentName, pub.EnvironmentUUID.String()),
		DisplayName:  pub.AgentName,
		AgentName:    pub.AgentName,
		UpstreamURL:  upstream.url,
		Transports:   upstream.transports,
		UpstreamAuth: upstream.auth,
		Policies:     buildPolicies(apiCfg),
		CardPolicies: buildCardPolicies(cfg.EffectiveCardCORS()),
	})
	if err != nil {
		return reconcile.Rendered{}, err
	}
	return reconcile.Rendered{Name: fmt.Sprintf("%s-deployment", pub.AgentName), Content: []byte(yamlStr)}, nil
}

// DeployEvent is the gateway's agent.deployed; the gateway then fetches /agents/{agentId}, keyed on
// the artifact UUID.
func (k *A2AAgentKind) DeployEvent(ref reconcile.EventRef) reconcile.Event {
	return a2aAgentDeployEvent(ref)
}

// UndeployEvent is the gateway's agent.undeployed: it stops serving the Agent but keeps it, and
// its API keys, for a redeploy. An empty deployment ID passes the gateway's deployment-ID guard.
func (k *A2AAgentKind) UndeployEvent(ref reconcile.EventRef) reconcile.Event {
	return a2aAgentUndeployEvent(ref.Intent.ArtifactUUID, ref.DeploymentID, ref.PerformedAt)
}

// DeleteEvent is the gateway's agent.deleted, which drops the Agent together with its API keys.
func (k *A2AAgentKind) DeleteEvent(ref reconcile.EventRef) reconcile.Event {
	return a2aAgentDeleteEvent(ref.Intent.ArtifactUUID)
}

func a2aAgentDeployEvent(ref reconcile.EventRef) reconcile.Event {
	agentID := ref.Intent.ArtifactUUID.String()
	return reconcile.Event{
		Type: "agent.deployed", Action: "CREATE", EntityID: agentID,
		Payload: &models.AgentDeploymentEvent{AgentID: agentID, DeploymentID: ref.DeploymentID, PerformedAt: ref.PerformedAt},
	}
}

func a2aAgentUndeployEvent(artifactUUID uuid.UUID, deploymentID string, performedAt time.Time) reconcile.Event {
	agentID := artifactUUID.String()
	return reconcile.Event{
		Type: "agent.undeployed", Action: "DELETE", EntityID: agentID,
		Payload: &models.AgentUndeploymentEvent{AgentID: agentID, DeploymentID: deploymentID, PerformedAt: performedAt},
	}
}

func a2aAgentDeleteEvent(artifactUUID uuid.UUID) reconcile.Event {
	agentID := artifactUUID.String()
	return reconcile.Event{
		Type: "agent.deleted", Action: "DELETE", EntityID: agentID,
		Payload: &models.AgentDeletionEvent{AgentID: agentID},
	}
}

// DescribeError is the last_error a reader may see; unproxyable reasons come from the card itself.
func (k *A2AAgentKind) DescribeError(cause error) string {
	if errors.Is(cause, reconcile.ErrInputNotReady) {
		return sanitizeA2ALastError(cause.Error())
	}
	lastErr := userFacingA2APublishError(cause)
	warnIfInternalA2AError(k.logger, lastErr, a2aPublishInternalError, cause, "A2A publication failed internally")
	return lastErr
}

// userFacingA2APublishError is the last_error a reader may see; unproxyable reasons come from the card itself.
func userFacingA2APublishError(cause error) string {
	if errors.Is(cause, errA2ANotProxyable) {
		return sanitizeA2ALastError(cause.Error())
	}
	return userFacingA2AError(cause, a2aPublishInternalError)
}

// AfterFailed reports a publish that will not happen on the external card parked for it, instead
// of leaving it pending.
func (k *A2AAgentKind) AfterFailed(ctx context.Context, intent models.DeployIntent, lastErr string) {
	pub, err := decodeA2APublication(intent)
	if err != nil || pub.Source != models.A2APublicationSourceExternal {
		return
	}
	err = k.cardRepo.FailParked(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID,
		sanitizeA2ALastError("the gateway could not publish the agent: "+lastErr))
	if err != nil {
		k.logger.Warn("Failed to report the gateway failure on the parked agent card",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
	}
}

// AfterLive queues a fetch of the card the gateway now serves; best effort.
func (k *A2AAgentKind) AfterLive(ctx context.Context, intent models.DeployIntent, _, _ uuid.UUID) {
	pub, err := decodeA2APublication(intent)
	if err != nil {
		k.logger.Error("Cannot queue the card fetch of a live A2A agent", "intentID", intent.ID, "error", err)
		return
	}
	k.enqueueCardFetch(ctx, pub)
}

// enqueueCardFetch queues a fetch of the agent's card; best effort.
func (k *A2AAgentKind) enqueueCardFetch(ctx context.Context, pub a2aPublication) {
	if pub.Source == models.A2APublicationSourceExternal {
		k.requeueExternalCard(ctx, pub)
		return
	}
	card := &models.A2AAgentCard{
		OUID:            pub.OUID,
		ProjectName:     pub.ProjectName,
		AgentName:       pub.AgentName,
		EnvironmentName: pub.EnvironmentName,
		EnvironmentUUID: pub.EnvironmentUUID,
		Source:          models.A2AAgentCardSourcePlatform,
	}
	if err := k.cardRepo.Enqueue(ctx, card); err != nil {
		k.logger.Warn("Published A2A agent but failed to queue its card fetch",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
		return
	}
	k.dropCardIfAgentGone(ctx, pub)
}

// requeueExternalCard makes the parked external row due in place; an upsert would rewrite its registered source.
func (k *A2AAgentKind) requeueExternalCard(ctx context.Context, pub a2aPublication) {
	err := k.cardRepo.Requeue(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardNotFound):
		k.logger.Info("Published A2A agent has no card row any more; skipping its card fetch",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName)
		return
	case err != nil:
		k.logger.Warn("Published A2A agent but failed to queue its card fetch",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
		return
	}
	k.dropCardIfAgentGone(ctx, pub)
}

// dropCardIfAgentGone removes a card row written after a racing DeleteAgent, even if the name was reused.
func (k *A2AAgentKind) dropCardIfAgentGone(ctx context.Context, pub a2aPublication) {
	current, err := k.ocClient.GetComponent(ctx, pub.OUID, pub.ProjectName, pub.AgentName)
	switch {
	case errors.Is(err, utils.ErrNotFound):
	case err != nil:
		return
	case pub.AgentUUID == nil || current.UUID == "" || current.UUID == pub.AgentUUID.String():
		return
	}
	if err := k.cardRepo.DeleteForAgentEnv(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName); err != nil {
		k.logger.Warn("Failed to drop the card row of a deleted A2A agent",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
	}
}

// Drifted reports a platform agent whose binding now serves a different upstream than the one its
// current deployment was rendered with, e.g. after a rebuild on a new port. One that has not
// drifted but serves a release newer than its card's gets its card fetched again. An external
// origin is never drift-checked: its routes are re-derived on every publish.
func (k *A2AAgentKind) Drifted(ctx context.Context, intent models.DeployIntent) (string, bool) {
	pub, err := decodeA2APublication(intent)
	if err != nil || pub.Source != models.A2APublicationSourcePlatform {
		return "", false
	}
	rollout, err := k.ocClient.GetReleaseBindingRollout(ctx, pub.OUID, pub.AgentName, pub.EnvironmentName)
	if err != nil || rollout.ServiceURL == "" {
		return "", false
	}
	deployed, err := k.deployedUpstream(pub)
	if err != nil {
		k.logger.Warn("Failed to read the upstream the A2A agent was deployed with",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
		return "", false
	}
	if deployed == "" || rollout.ServiceURL == deployed {
		k.refetchCardOfNewRelease(ctx, pub, rollout)
		return "", false
	}
	return fmt.Sprintf("the agent's upstream moved from %s to %s", deployed, rollout.ServiceURL), true
}

// deployedUpstream is the upstream URL in the artifact's current deployment, or "" when it has none.
func (k *A2AAgentKind) deployedUpstream(pub a2aPublication) (string, error) {
	gateways, err := k.deploymentRepo.GetDeployedGatewaysByProvider(pub.ArtifactUUID, pub.OUID)
	if err != nil || len(gateways) == 0 {
		return "", err
	}
	deployment, err := k.deploymentRepo.GetCurrentByGateway(pub.ArtifactUUID.String(), gateways[0], pub.OUID)
	if err != nil || deployment == nil {
		return "", err
	}
	var agent A2AAgentDeploymentYAML
	if err := yaml.Unmarshal(deployment.Content, &agent); err != nil {
		return "", fmt.Errorf("failed to parse deployment %s: %w", deployment.DeploymentID, err)
	}
	return agent.Spec.Upstream.URL, nil
}

// refetchCardOfNewRelease re-queues a platform card fetched from an older release than the one now serving.
func (k *A2AAgentKind) refetchCardOfNewRelease(ctx context.Context, pub a2aPublication, rollout client.ReleaseBindingRollout) {
	if !rollout.Serving || rollout.ReleaseName == "" {
		return
	}
	card, err := k.cardRepo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
	if err != nil {
		if !errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
			k.logger.Warn("Failed to read A2A agent card for release drift",
				"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
		}
		return
	}
	if card.Source != models.A2AAgentCardSourcePlatform || card.Status == models.A2AAgentCardStatusPending ||
		card.ReleaseName == rollout.ReleaseName || card.AttemptedReleaseName == rollout.ReleaseName {
		return
	}
	k.logger.Info("A2A agent serves a new release; re-fetching its card",
		"agentName", pub.AgentName, "environment", pub.EnvironmentName,
		"cardRelease", card.ReleaseName, "currentRelease", rollout.ReleaseName)
	k.enqueueCardFetch(ctx, pub)
}

// resolvedUpstream is where the gateway forwards to; nil transports keep M1's /rpc and /rest.
type resolvedUpstream struct {
	url        string
	transports []A2ATransport
	auth       *models.UpstreamAuth
}

// a2aOrigin is a proxied external agent's registered card URL and the stored credential to fetch it with.
type a2aOrigin struct {
	cardURL string
	header  *A2ACardFetchHeader
	auth    *models.UpstreamAuth
}

// resolveUpstream reads a platform agent's release binding, or returns the origin a proxied external agent's is derived from.
func (k *A2AAgentKind) resolveUpstream(ctx context.Context, pub a2aPublication) (resolvedUpstream, *a2aOrigin, error) {
	if pub.Source == models.A2APublicationSourceExternal {
		origin, err := k.resolveExternalOrigin(ctx, pub)
		return resolvedUpstream{}, origin, err
	}
	upstreamURL, err := k.ocClient.GetReleaseBindingServiceURL(ctx, pub.OUID, pub.AgentName, pub.EnvironmentName)
	if err != nil {
		return resolvedUpstream{}, nil, fmt.Errorf("failed to read release binding service URL: %w", err)
	}
	if upstreamURL == "" {
		return resolvedUpstream{}, nil, errUpstreamNotReady
	}
	return resolvedUpstream{url: upstreamURL}, nil, nil
}

// resolveExternalOrigin reads the proxy and the registered card URL, refusing one the gateway cannot front.
func (k *A2AAgentKind) resolveExternalOrigin(ctx context.Context, pub a2aPublication) (*a2aOrigin, error) {
	proxy, card, err := k.lockedProxyAndCard(ctx, pub)
	if err != nil {
		return nil, err
	}
	if _, err := a2aProxyUpstream(card.SourceURL); err != nil {
		return nil, err
	}
	header, err := k.originCardHeader(proxy)
	if err != nil {
		return nil, err
	}
	return &a2aOrigin{cardURL: card.SourceURL, header: header, auth: proxy.UpstreamAuth()}, nil
}

// lockedProxyAndCard reads both rows under the environment's transition lock, so a credential reset and a card URL
// move cannot interleave the reads and pair the old secret with the new origin.
func (k *A2AAgentKind) lockedProxyAndCard(
	ctx context.Context, pub a2aPublication,
) (proxy *models.A2AProxy, card *models.A2AAgentCard, err error) {
	key := pub.key()
	err = k.tx(ctx, func(tx *gorm.DB) error {
		proxy, err = lockedA2AProxy(ctx, k.proxyRepo.WithTx(tx), key)
		if err != nil {
			return err
		}
		if proxy == nil {
			return fmt.Errorf("%w: the environment is no longer proxied", errA2ANotProxyable)
		}
		card, err = k.cardRepo.WithTx(tx).Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
		if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
			return fmt.Errorf("%w: no card URL is registered for the environment", errA2ANotProxyable)
		}
		if err != nil {
			return fmt.Errorf("failed to read the agent card: %w", err)
		}
		return nil
	})
	return proxy, card, err
}

// fetchExternalUpstream re-derives the routes from the origin card at every publish, fetched SSRF-guarded as its host may resolve elsewhere now.
func (k *A2AAgentKind) fetchExternalUpstream(ctx context.Context, origin *a2aOrigin) (resolvedUpstream, error) {
	body, err := k.fetcher.FetchOrigin(ctx, origin.cardURL, origin.header)
	if err != nil {
		return resolvedUpstream{}, prefixA2APublicError("could not fetch the origin agent card", err)
	}
	upstreamURL, transports, err := deriveA2AProxyRoutes(origin.cardURL, body)
	if err != nil {
		return resolvedUpstream{}, err
	}
	return resolvedUpstream{url: upstreamURL, transports: transports, auth: origin.auth}, nil
}

// originCardHeader is the stored upstream credential as a fetch header; sameOriginClient refuses off-origin redirects while it is attached.
func (k *A2AAgentKind) originCardHeader(proxy *models.A2AProxy) (*A2ACardFetchHeader, error) {
	if proxy.UpstreamAuthHeader == "" || proxy.UpstreamAuthSecret == "" {
		return nil, nil //nolint:nilnil // A proxy without a stored credential fetches the card unauthenticated.
	}
	value, err := utils.DecryptSecretRef(proxy.UpstreamAuthSecret, k.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt the upstream secret: %w", err)
	}
	return &A2ACardFetchHeader{Name: proxy.UpstreamAuthHeader, Value: value}, nil
}
