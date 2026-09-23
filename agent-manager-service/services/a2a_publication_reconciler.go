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
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

const (
	a2aReconcilerTickInterval = 30 * time.Second
	// a2aReconcilerLockID is this loop's own PostgreSQL advisory lock ID,
	// distinct from schedulerLockID and reconcilerLockID so the three background
	// loops never block each other.
	a2aReconcilerLockID  = int64(739281458)
	a2aReconcilerBatch   = 50
	a2aReconcilerRetryIn = 30 * time.Second

	// a2aPublicationAttemptBudget bounds how long an agent may fail to publish
	// an upstream before the row is called failed.
	//
	// The number is the agent startup budget (10 minutes, the point past which
	// the agent-api startup probe has already given up at least once, so nothing
	// is still starting) divided by the tick interval, with slack. Past it the
	// binding is not going to report a ServiceURL, and retrying forever would
	// only hide that from whoever has to fix it.
	a2aPublicationAttemptBudget = 30
)

// errUpstreamNotReady means the binding has not published a ServiceURL yet. It
// is the expected condition for the first few attempts after a deploy, not a
// misconfiguration.
var errUpstreamNotReady = errors.New("release binding has not published a service URL yet")

// A2APublicationReconcilerService drains the a2a_publications queue.
type A2APublicationReconcilerService interface {
	Start(ctx context.Context) error
	Stop() error
	// RunOnce drains the currently-due batch once. It is the same cycle the
	// ticker drives, exposed so a caller — a test, or an operator tool — can
	// advance the queue deterministically instead of waiting out a tick.
	RunOnce(ctx context.Context)
}

type a2aPublicationReconcilerService struct {
	pubRepo         repositories.A2APublicationRepository
	deploymentRepo  repositories.DeploymentRepository
	gatewayRepo     repositories.GatewayRepository
	agentConfigRepo repositories.AgentConfigRepository
	ocClient        client.OpenChoreoClient
	cardFetcher     A2AAgentCardFetcher
	events          *GatewayEventsService
	logger          *slog.Logger
	stopCh          chan struct{}
	stopOnce        sync.Once
}

// NewA2APublicationReconcilerService creates an A2APublicationReconcilerService.
func NewA2APublicationReconcilerService(
	pubRepo repositories.A2APublicationRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
	agentConfigRepo repositories.AgentConfigRepository,
	ocClient client.OpenChoreoClient,
	cardFetcher A2AAgentCardFetcher,
	events *GatewayEventsService,
	logger *slog.Logger,
) A2APublicationReconcilerService {
	return &a2aPublicationReconcilerService{
		pubRepo:         pubRepo,
		deploymentRepo:  deploymentRepo,
		gatewayRepo:     gatewayRepo,
		agentConfigRepo: agentConfigRepo,
		ocClient:        ocClient,
		cardFetcher:     cardFetcher,
		events:          events,
		logger:          logger,
		stopCh:          make(chan struct{}),
		stopOnce:        sync.Once{},
	}
}

func (s *a2aPublicationReconcilerService) Start(ctx context.Context) error {
	go s.runLoop(ctx)
	s.logger.Info("A2A publication reconciler started")
	return nil
}

func (s *a2aPublicationReconcilerService) Stop() error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.logger.Info("A2A publication reconciler stopped")
	})
	return nil
}

func (s *a2aPublicationReconcilerService) runLoop(ctx context.Context) {
	ticker := time.NewTicker(a2aReconcilerTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runCycle(ctx)
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// RunOnce drains the currently-due batch once.
func (s *a2aPublicationReconcilerService) RunOnce(ctx context.Context) {
	s.runCycle(ctx)
}

// runCycle claims the due batch under an advisory lock so only one replica
// scans at a time, then releases it before the slow OpenChoreo and event-hub
// calls — mirroring agentThunderReconcilerService.runCycle.
func (s *a2aPublicationReconcilerService) runCycle(ctx context.Context) {
	tx := db.GetDB().WithContext(ctx).Begin()
	if tx.Error != nil {
		s.logger.Error("Failed to begin transaction for A2A publication advisory lock", "error", tx.Error)
		return
	}

	var locked bool
	if err := tx.Raw("SELECT pg_try_advisory_xact_lock(?)", a2aReconcilerLockID).Scan(&locked).Error; err != nil {
		s.logger.Error("Failed to try A2A publication advisory lock", "error", err)
		tx.Rollback()
		return
	}
	if !locked {
		tx.Rollback()
		return
	}

	due, err := s.pubRepo.FindDue(ctx, time.Now(), a2aReconcilerBatch)
	if err != nil {
		s.logger.Error("Failed to query due A2A publications", "error", err)
		tx.Rollback()
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.logger.Error("Failed to commit A2A publication advisory lock transaction", "error", err)
		return
	}

	for _, pub := range due {
		s.publishOne(ctx, pub)
	}
}

// publishOne advances one row by one phase, or schedules a retry when it cannot.
//
// The two phases run sequentially and share the retry columns, which reset at
// the boundary: routing and card-fetching are different failures to survive.
func (s *a2aPublicationReconcilerService) publishOne(ctx context.Context, pub models.A2APublication) {
	if pub.Status == models.A2APublicationStatusRouted {
		s.publishCard(ctx, pub)
		return
	}
	s.publishRoute(ctx, pub)
}

// publishRoute is phase 1: get the agent routable. It emits the best card it
// has — the stored one on a redeploy, none on a first deploy, where the
// gateway's passthrough default rewrites the proxied card's URLs and only the
// agent's own (usually absent) security declarations are stale.
func (s *a2aPublicationReconcilerService) publishRoute(ctx context.Context, pub models.A2APublication) {
	pc, err := s.resolvePublishContext(ctx, pub)
	if err != nil {
		s.recordAttemptFailure(ctx, pub, err)
		return
	}

	stored, err := storedAgentCard(pub)
	if err != nil {
		// A card that no longer parses is not worth failing a deploy over;
		// route without it and let phase 2 replace it.
		s.logger.Warn("Stored A2A agent card is unreadable, routing without it",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
		stored = nil
	}

	if err := s.attemptPublish(ctx, pub, pc, stored); err != nil {
		s.recordAttemptFailure(ctx, pub, err)
		return
	}
	if err := s.pubRepo.MarkRouted(ctx, pub.ID, time.Now()); err != nil {
		s.logger.Error("Routed A2A agent but failed to mark the queue row",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
	}
}

// publishCard is phase 2: fetch the agent's card, rewrite what the gateway owns,
// and republish as managed.
func (s *a2aPublicationReconcilerService) publishCard(ctx context.Context, pub models.A2APublication) {
	pc, err := s.resolvePublishContext(ctx, pub)
	if err != nil {
		s.recordAttemptFailure(ctx, pub, err)
		return
	}

	fetched, err := s.cardFetcher.Fetch(ctx, pc.upstreamURL)
	if err != nil {
		s.recordAttemptFailure(ctx, pub, err)
		return
	}

	contextPath := "/" + pub.AgentName
	card, err := buildGatewayAgentCard(fetched, GatewayAgentCardInput{
		PublicBaseURL:        buildPublicProxyURL(pc.gateway, &contextPath),
		EnableAPIKeySecurity: pc.apiConfig.EnableApiKeySecurity,
		EnableOAuthSecurity:  pc.apiConfig.EnableOAuthSecurity,
	})
	if err != nil {
		s.recordAttemptFailure(ctx, pub, err)
		return
	}

	encoded, err := json.Marshal(card)
	if err != nil {
		s.recordAttemptFailure(ctx, pub, fmt.Errorf("failed to encode the agent card: %w", err))
		return
	}

	// An unchanged card is the common case on a redeploy, and republishing it
	// would cost a second gateway apply for a document the gateway already has.
	// Skip only when an accepted publish is known: a set ID and a clean last attempt.
	unchanged := sameAgentCard(pub.AgentCard, encoded) && pub.CardDeploymentID != nil && pub.LastError == ""
	if !unchanged {
		if err := s.attemptPublish(ctx, pub, pc, card); err != nil {
			s.recordAttemptFailure(ctx, pub, err)
			return
		}
	}

	if err := s.pubRepo.MarkCardPublished(ctx, pub.ID, encoded, time.Now()); err != nil {
		s.logger.Error("Published the A2A agent card but failed to mark the queue row",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
	}
}

// storedAgentCard decodes the card a previous cycle published, if any.
func storedAgentCard(pub models.A2APublication) (map[string]any, error) {
	if len(pub.AgentCard) == 0 {
		return nil, nil //nolint:nilnil // no card is the ordinary first-deploy state
	}
	var card map[string]any
	if err := json.Unmarshal(pub.AgentCard, &card); err != nil {
		return nil, fmt.Errorf("failed to decode the stored agent card: %w", err)
	}
	return card, nil
}

// sameAgentCard compares semantically rather than byte-wise: jsonb does not
// preserve key order or whitespace, so the stored bytes are never the bytes that
// were written.
func sameAgentCard(stored, built []byte) bool {
	if len(stored) == 0 {
		return false
	}
	var was, is any
	if json.Unmarshal(stored, &was) != nil || json.Unmarshal(built, &is) != nil {
		return false
	}
	return reflect.DeepEqual(was, is)
}

// recordAttemptFailure retries within the budget and gives up past it.
func (s *a2aPublicationReconcilerService) recordAttemptFailure(ctx context.Context, pub models.A2APublication, cause error) {
	if pub.AttemptCount+1 >= a2aPublicationAttemptBudget {
		s.logger.Error("A2A agent never reached its gateway within the attempt budget",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName,
			"attempts", pub.AttemptCount+1, "error", cause)
		if err := s.pubRepo.MarkFailed(ctx, pub.ID, cause.Error()); err != nil {
			s.logger.Error("Failed to mark A2A publication failed", "error", err)
		}
		return
	}
	s.logger.Debug("A2A agent not publishable yet, will retry",
		"agentName", pub.AgentName, "environment", pub.EnvironmentName,
		"attempt", pub.AttemptCount+1, "reason", cause)
	if err := s.pubRepo.MarkAttemptFailed(ctx, pub.ID, cause.Error(), time.Now().Add(a2aReconcilerRetryIn)); err != nil {
		s.logger.Error("Failed to schedule A2A publication retry", "error", err)
	}
}

// a2aPublishContext is everything a publish needs from the outside world,
// resolved once so phase 2 does not repeat phase 1's lookups.
type a2aPublishContext struct {
	upstreamURL string
	gateway     *models.Gateway
	apiConfig   resolvedCORSConfig
}

func (s *a2aPublicationReconcilerService) resolvePublishContext(
	ctx context.Context, pub models.A2APublication,
) (a2aPublishContext, error) {
	upstreamURL, err := s.ocClient.GetReleaseBindingServiceURL(ctx, pub.OUID, pub.AgentName, pub.EnvironmentName)
	if err != nil {
		return a2aPublishContext{}, fmt.Errorf("failed to read release binding service URL: %w", err)
	}
	if upstreamURL == "" {
		return a2aPublishContext{}, errUpstreamNotReady
	}

	gateway, err := s.resolveGateway(pub)
	if err != nil {
		return a2aPublishContext{}, err
	}

	// The policy chain is exactly what a chat/custom agent gets: the persisted
	// per-environment config, run through the same resolveAPIConfig. The card's
	// security declarations are derived from this same struct, so the document
	// and the chain enforcing it cannot disagree.
	cfg, err := s.agentConfigRepo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
	if err != nil {
		return a2aPublishContext{}, fmt.Errorf("failed to load agent config: %w", err)
	}

	return a2aPublishContext{
		upstreamURL: upstreamURL,
		gateway:     gateway,
		apiConfig:   resolveAPIConfig(cfg, nil, nil, nil, nil, false),
	}, nil
}

// attemptPublish writes one Agent resource and tells the gateway about it.
//
// When it carries a card, the publication row records the deployment ID BEFORE
// the broadcast. The gateway validates after it fetches, which is after the
// broadcast, and an ack that arrives before the row knows what it is waiting on
// is dropped silently — reinstating the exact blindness the ack feedback
// removes. Moving the broadcast earlier is a correctness bug, not a reordering.
func (s *a2aPublicationReconcilerService) attemptPublish(
	ctx context.Context, pub models.A2APublication, pc a2aPublishContext, card map[string]any,
) error {
	yamlStr, err := generateA2AAgentDeploymentYAML(A2AAgentDeploymentInput{
		ArtifactName: a2aAgentEnvArtifactName(pub.ProjectName, pub.AgentName, pub.EnvironmentUUID.String()),
		DisplayName:  pub.AgentName,
		AgentName:    pub.AgentName,
		Vhost:        pc.gateway.Vhost,
		UpstreamURL:  pc.upstreamURL,
		Policies:     buildPolicies(pc.apiConfig),
		AgentCard:    card,
	})
	if err != nil {
		return err
	}

	deploymentID := uuid.New()
	deployed := models.DeploymentStatusDeployed
	deployment := &models.Deployment{
		DeploymentID: deploymentID,
		Name:         fmt.Sprintf("%s-deployment", pub.AgentName),
		ArtifactUUID: pub.ArtifactUUID,
		OUID:         pub.OUID,
		GatewayUUID:  pc.gateway.UUID,
		Content:      []byte(yamlStr),
		Status:       &deployed,
	}
	// The deployments row is not optional bookkeeping: GET /agents/{agentId}
	// serves the Agent YAML back out of it when the gateway fetches after the
	// event, so an event without a row is an event the gateway cannot act on.
	if err := s.deploymentRepo.CreateWithLimitEnforcement(deployment, maxDeploymentsPerAPI+deploymentLimitBuffer); err != nil {
		return fmt.Errorf("failed to create A2A agent deployment row: %w", err)
	}

	if len(card) > 0 {
		if err := s.pubRepo.SetCardDeploymentID(ctx, pub.ID, deploymentID); err != nil {
			return fmt.Errorf("failed to record the card deployment id: %w", err)
		}
	}

	event := &models.AgentDeploymentEvent{
		ProxyID:      pub.ArtifactUUID.String(),
		DeploymentID: deploymentID.String(),
		PerformedAt:  time.Now().Truncate(time.Millisecond),
	}
	if err := s.events.BroadcastAgentDeploymentEvent(pc.gateway.UUID.String(), event); err != nil {
		return fmt.Errorf("failed to broadcast agent deployment event: %w", err)
	}

	s.logger.Info("Published A2A agent to gateway",
		"agentName", pub.AgentName, "environment", pub.EnvironmentName,
		"artifactID", pub.ArtifactUUID, "gateway", pc.gateway.Name,
		"upstream", pc.upstreamURL, "managedCard", len(card) > 0)
	return nil
}

// resolveGateway picks the environment's gateway. An A2A agent is inbound
// traffic, so it belongs on the environment's INGRESS gateway — the same slot a
// REST agent's api-configuration trait targets via the apiGatewayName
// convention — not on an egress gateway, which hosts outbound LLM/MCP artifacts.
func (s *a2aPublicationReconcilerService) resolveGateway(pub models.A2APublication) (*models.Gateway, error) {
	envID := pub.EnvironmentUUID.String()
	gateways, err := s.gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
		OrganizationID: pub.OUID,
		EnvironmentID:  &envID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateways for environment %s: %w", envID, err)
	}
	for _, gw := range gateways {
		if gw != nil && gw.IsIngressCapable() {
			return gw, nil
		}
	}
	// An environment has at most one ingress gateway, and it is registered by the
	// bootstrap job, so absence is a timing condition rather than a
	// misconfiguration — retry.
	return nil, fmt.Errorf("no ingress gateway is mapped to environment %s yet", envID)
}
