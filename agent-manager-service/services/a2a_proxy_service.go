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
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const (
	a2aUpstreamHeaderMaxLen = 255
	a2aUpstreamValueMaxLen  = 8192
	// a2aProxyNoPublicationMsg reports a proxied environment with no publication queued; a refresh queues one.
	a2aProxyNoPublicationMsg = "no gateway publication is queued for this environment; refresh to publish it again"
)

// a2aUpstreamHeaderPattern is the gateway's set-headers name schema.
var a2aUpstreamHeaderPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// A2AProxyHooks is what the card service needs from the proxy service on a proxied environment.
type A2AProxyHooks interface {
	// Each decides direct vs proxied under the environment's transition lock.

	// StoreCardSource stores the URL; on a proxied environment it also parks the card and republishes.
	StoreCardSource(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string) error
	// RemoveCardSource deletes the card row; on a proxied environment it also switches to direct.
	RemoveCardSource(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error
	// RefreshCardSource requeues the card fetch; on a proxied environment it republishes, and the fetch follows.
	RefreshCardSource(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error
}

// A2AProxyService publishes external A2A agents through their environment's ingress gateway.
type A2AProxyService interface {
	A2AProxyHooks
	GetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AProxyStatus, error)
	// SetA2AProxy enables the proxy or updates its upstream auth; a nil auth clears the stored credential.
	SetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string, auth *models.A2AProxyAuthUpdate) error
	DeleteA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error
	RefreshA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error
}

type a2aProxyService struct {
	tx              TxRunner
	proxyRepo       repositories.A2AProxyRepository
	cardRepo        repositories.A2AAgentCardRepository
	agentConfigRepo repositories.AgentConfigRepository
	artifactRepo    repositories.ArtifactRepository
	publisher       *A2APublisher
	ocClient        client.OpenChoreoClient
	encryptionKey   []byte
	logger          *slog.Logger
}

// NewA2AProxyService creates an A2AProxyService.
func NewA2AProxyService(
	tx TxRunner,
	proxyRepo repositories.A2AProxyRepository,
	cardRepo repositories.A2AAgentCardRepository,
	agentConfigRepo repositories.AgentConfigRepository,
	artifactRepo repositories.ArtifactRepository,
	publisher *A2APublisher,
	ocClient client.OpenChoreoClient,
	encryptionKey []byte,
	logger *slog.Logger,
) A2AProxyService {
	return &a2aProxyService{
		tx:              tx,
		proxyRepo:       proxyRepo,
		cardRepo:        cardRepo,
		agentConfigRepo: agentConfigRepo,
		artifactRepo:    artifactRepo,
		publisher:       publisher,
		ocClient:        ocClient,
		encryptionKey:   encryptionKey,
		logger:          logger,
	}
}

// a2aProxyTarget is the agent and environment one proxy operation acts on.
type a2aProxyTarget struct {
	agent *models.AgentResponse
	env   *models.EnvironmentResponse
	key   a2aPublicationKey
}

func newA2AProxyTarget(agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) (a2aProxyTarget, error) {
	envUUID, err := uuid.Parse(env.UUID)
	if err != nil {
		return a2aProxyTarget{}, fmt.Errorf("invalid UUID for environment %s: %w", env.Name, err)
	}
	key := a2aPublicationKey{
		OUID: ouID, ProjectName: projectName, AgentName: agent.Name, EnvironmentName: env.Name, EnvironmentUUID: envUUID,
		AgentUUID: parseAgentUUID(agent.UUID),
	}
	return a2aProxyTarget{agent: agent, env: env, key: key}, nil
}

// cardRow is the environment's external card row with sourceURL registered.
func (t a2aProxyTarget) cardRow(sourceURL string) *models.A2AAgentCard {
	return &models.A2AAgentCard{
		OUID: t.key.OUID, ProjectName: t.key.ProjectName, AgentName: t.key.AgentName,
		EnvironmentName: t.key.EnvironmentName, EnvironmentUUID: t.key.EnvironmentUUID,
		Source: models.A2AAgentCardSourceExternal, SourceURL: sourceURL,
	}
}

func (s *a2aProxyService) GetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AProxyStatus, error) {
	// Rows exist only for live proxied environments, so OpenChoreo is asked only on a miss.
	proxy, err := s.proxyRepo.Get(ctx, ouID, projectName, agentName, envName)
	if errors.Is(err, repositories.ErrA2AProxyNotFound) {
		if _, err := s.agent(ctx, ouID, projectName, agentName); err != nil {
			return nil, err
		}
		return nil, utils.ErrEnvironmentNotProxied
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	status := &models.A2AProxyStatus{UpstreamAuthHeader: proxy.UpstreamAuthHeader}
	key := a2aPublicationKey{OUID: ouID, ProjectName: projectName, AgentName: agentName, EnvironmentName: envName, EnvironmentUUID: proxy.EnvironmentUUID}
	pub, err := s.publisher.Publication(ctx, key)
	switch {
	case errors.Is(err, repositories.ErrA2APublicationNotFound):
		// Enabling writes both rows together, so a missing publication never resolves on its own.
		status.Status, status.LastError = models.A2APublicationStatusFailed, a2aProxyNoPublicationMsg
	case err != nil:
		return nil, fmt.Errorf("failed to read the A2A publication: %w", err)
	default:
		status.Status, status.LastError = pub.Status, pub.LastError
	}
	return status, nil
}

func (s *a2aProxyService) SetA2AProxy(
	ctx context.Context, ouID, projectName, agentName, envName string, auth *models.A2AProxyAuthUpdate,
) error {
	if err := validateA2AUpstreamAuth(auth); err != nil {
		return err
	}
	t, err := s.target(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		return err
	}
	var attempt *audit.Attempt
	err = s.tx(ctx, func(tx *gorm.DB) error {
		existing, err := s.lockedProxy(ctx, tx, t)
		if err != nil {
			return err
		}
		if err := s.requireCardSource(ctx, tx, t); err != nil {
			return err
		}
		proxy, authChanged, err := s.proxyRow(t, existing, auth)
		if err != nil {
			return err
		}
		// A PUT can set, replace or clear the credential the gateway sends upstream, so it is refused unrecorded.
		if attempt, err = s.beginAudit(ctx, audit.ActionAgentSetA2AProxy, t, proxy.UpstreamAuthHeader, authChanged); err != nil {
			return err
		}
		if err := s.proxyRepo.WithTx(tx).Upsert(ctx, proxy); err != nil {
			return fmt.Errorf("failed to save the A2A proxy: %w", err)
		}
		if existing == nil {
			if err := s.switchFromDirect(ctx, tx, t); err != nil {
				return err
			}
		}
		return s.enqueuePublication(ctx, tx, t)
	})
	attempt.Complete(ctx, err)
	if err != nil {
		return err
	}
	return s.dropIfAgentGone(ctx, t)
}

func (s *a2aProxyService) DeleteA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error {
	t, err := s.target(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		return err
	}
	return s.removeProxy(ctx, t, requeueDirectCardFetch, nil)
}

func (s *a2aProxyService) RefreshA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error {
	t, err := s.target(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		return err
	}
	return s.republish(ctx, t, nil)
}

func (s *a2aProxyService) StoreCardSource(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string,
) error {
	t, err := newA2AProxyTarget(agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		// Decided under the lock, so a concurrent switch to proxied cannot miss the origin check.
		proxy, err := s.lockedProxy(ctx, tx, t)
		if err != nil {
			return err
		}
		if proxy != nil {
			if err := requireProxyableCardURL(sourceURL); err != nil {
				return err
			}
			if err := s.requireSecretStaysOnOrigin(ctx, tx, t, proxy, sourceURL); err != nil {
				return err
			}
		}
		cards := s.cardRepo.WithTx(tx)
		if err := cards.Enqueue(ctx, t.cardRow(sourceURL)); err != nil {
			return fmt.Errorf("failed to store the agent card URL: %w", err)
		}
		if proxy == nil {
			return nil
		}
		// The new URL's card is shown only once the gateway fronts the new origin.
		err = cards.ClearCard(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
		if err != nil {
			return fmt.Errorf("failed to park the agent card: %w", err)
		}
		return s.enqueuePublication(ctx, tx, t)
	})
	if err != nil {
		return err
	}
	return s.dropIfAgentGone(ctx, t)
}

func (s *a2aProxyService) RemoveCardSource(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse,
) error {
	t, err := newA2AProxyTarget(agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	return s.removeProxy(ctx, t, deleteCardRow, deleteCardRow)
}

func (s *a2aProxyService) RefreshCardSource(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse,
) error {
	t, err := newA2AProxyTarget(agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	return s.republish(ctx, t, requeueCardFetch)
}

// target resolves an external A2A agent and an environment the caller may act on.
func (s *a2aProxyService) target(ctx context.Context, ouID, projectName, agentName, envName string) (a2aProxyTarget, error) {
	agent, err := s.agent(ctx, ouID, projectName, agentName)
	if err != nil {
		return a2aProxyTarget{}, err
	}
	if !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return a2aProxyTarget{}, utils.ErrAgentNotA2A
	}
	if !isExternalProvisioned(agent) {
		return a2aProxyTarget{}, utils.ErrA2AProxyAgentNotExternal
	}
	env, err := requireEnvironmentTier(ctx, s.ocClient, s.logger, ouID, envName)
	if err != nil {
		return a2aProxyTarget{}, err
	}
	return newA2AProxyTarget(agent, ouID, projectName, env)
}

func (s *a2aProxyService) agent(ctx context.Context, ouID, projectName, agentName string) (*models.AgentResponse, error) {
	agent, err := s.ocClient.GetComponent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, translateAgentError(err)
	}
	return agent, nil
}

// lockedProxy takes the environment's transition lock and reads its proxy row under it; nil while direct.
func (s *a2aProxyService) lockedProxy(ctx context.Context, tx *gorm.DB, t a2aProxyTarget) (*models.A2AProxy, error) {
	proxies := s.proxyRepo.WithTx(tx)
	if err := proxies.LockAgentEnv(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID); err != nil {
		return nil, fmt.Errorf("failed to lock the A2A proxy: %w", err)
	}
	proxy, err := proxies.GetByEnvUUID(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AProxyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return proxy, nil
}

// requireCardSource is the 409 for an environment with no registered card URL to proxy.
func (s *a2aProxyService) requireCardSource(ctx context.Context, tx *gorm.DB, t a2aProxyTarget) error {
	card, err := s.cardRepo.WithTx(tx).Get(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentName)
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardNotFound):
		return utils.ErrA2AProxyNeedsCardSource
	case err != nil:
		return fmt.Errorf("failed to read the agent card: %w", err)
	case card.Source != models.A2AAgentCardSourceExternal || card.SourceURL == "":
		return utils.ErrA2AProxyNeedsCardSource
	}
	return requireProxyableCardURL(card.SourceURL)
}

// requireSecretStaysOnOrigin refuses a card URL on another origin while a stored secret would be sent there.
func (s *a2aProxyService) requireSecretStaysOnOrigin(
	ctx context.Context, tx *gorm.DB, t a2aProxyTarget, proxy *models.A2AProxy, sourceURL string,
) error {
	if proxy.UpstreamAuthSecret == "" {
		return nil
	}
	card, err := s.cardRepo.WithTx(tx).Get(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentName)
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardNotFound):
		return utils.ErrA2AProxyOriginChangeNeedsAuthReset
	case err != nil:
		return fmt.Errorf("failed to read the agent card: %w", err)
	}
	registered, regErr := url.Parse(card.SourceURL)
	next, nextErr := url.Parse(sourceURL)
	if regErr != nil || nextErr != nil || !isSameOrigin(registered, next) {
		return utils.ErrA2AProxyOriginChangeNeedsAuthReset
	}
	return nil
}

// proxyRow applies the PUT's secret semantics (no auth clears, a header alone keeps, a value replaces).
func (s *a2aProxyService) proxyRow(
	t a2aProxyTarget, existing *models.A2AProxy, auth *models.A2AProxyAuthUpdate,
) (*models.A2AProxy, bool, error) {
	row := &models.A2AProxy{
		OUID: t.key.OUID, ProjectName: t.key.ProjectName, AgentName: t.key.AgentName,
		EnvironmentUUID: t.key.EnvironmentUUID, EnvironmentName: t.key.EnvironmentName,
	}
	stored := existing != nil && existing.UpstreamAuthSecret != ""
	switch {
	case auth == nil:
		return row, stored, nil
	case auth.Value == nil && !stored:
		return nil, false, fmt.Errorf("%w: a value is required, since no secret is stored", utils.ErrInvalidUpstreamAuth)
	case auth.Value == nil:
		row.UpstreamAuthHeader, row.UpstreamAuthSecret = auth.Header, existing.UpstreamAuthSecret
		return row, auth.Header != existing.UpstreamAuthHeader, nil
	}
	secret, err := encryptA2AUpstreamSecret(*auth.Value, s.encryptionKey)
	if err != nil {
		return nil, false, err
	}
	row.UpstreamAuthHeader, row.UpstreamAuthSecret = auth.Header, secret
	return row, true, nil
}

// switchFromDirect defaults the inbound settings and parks the card until the gateway has the Agent.
func (s *a2aProxyService) switchFromDirect(ctx context.Context, tx *gorm.DB, t a2aProxyTarget) error {
	if err := ensureDefaultAgentConfig(ctx, s.agentConfigRepo.WithTx(tx), t.key); err != nil {
		return err
	}
	err := s.cardRepo.WithTx(tx).ClearCard(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
		return utils.ErrA2AProxyNeedsCardSource
	}
	if err != nil {
		return fmt.Errorf("failed to park the agent card: %w", err)
	}
	return nil
}

// enqueuePublication queues the environment's external publication against its artifact, creating it if needed.
func (s *a2aProxyService) enqueuePublication(ctx context.Context, tx *gorm.DB, t a2aProxyTarget) error {
	artifact, err := ensureAgentEnvAPIArtifact(tx, s.artifactRepo, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.env.UUID)
	if err != nil {
		return fmt.Errorf("failed to ensure the agent's API artifact: %w", err)
	}
	return s.publisher.Enqueue(ctx, tx, t.key, models.A2APublicationSourceExternal, artifact.UUID)
}

// republish re-derives the routes from the origin card on the next publish; the card fetch follows it.
// A direct environment gets ifDirect, or utils.ErrEnvironmentNotProxied when it is nil.
func (s *a2aProxyService) republish(ctx context.Context, t a2aProxyTarget, ifDirect a2aCardStep) error {
	err := s.tx(ctx, func(tx *gorm.DB) error {
		proxy, err := s.lockedProxy(ctx, tx, t)
		if err != nil {
			return err
		}
		if proxy == nil {
			return onDirect(ctx, tx, s.cardRepo, t, ifDirect)
		}
		return s.enqueuePublication(ctx, tx, t)
	})
	if err != nil {
		return err
	}
	return s.dropIfAgentGone(ctx, t)
}

// a2aCardStep is what happens to the card row when an environment stops being proxied.
type a2aCardStep func(ctx context.Context, cards repositories.A2AAgentCardRepository, key a2aPublicationKey) error

// requeueDirectCardFetch replaces the gateway's card, which advertised gateway URLs, with a fresh one from the origin.
func requeueDirectCardFetch(ctx context.Context, cards repositories.A2AAgentCardRepository, key a2aPublicationKey) error {
	err := cards.ClearCard(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
		return nil // the source was removed meanwhile, so there is nothing to fetch
	}
	if err != nil {
		return fmt.Errorf("failed to clear the proxied card: %w", err)
	}
	if err := cards.Requeue(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentName); err != nil {
		return fmt.Errorf("failed to queue the direct card fetch: %w", err)
	}
	return nil
}

// requeueCardFetch makes the direct card row due in place; an upsert could recreate a removed row.
func requeueCardFetch(ctx context.Context, cards repositories.A2AAgentCardRepository, key a2aPublicationKey) error {
	err := cards.Requeue(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentName)
	if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
		return utils.ErrAgentCardNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to queue agent card fetch: %w", err)
	}
	return nil
}

// onDirect applies step to a direct environment's card row, or refuses with utils.ErrEnvironmentNotProxied.
func onDirect(ctx context.Context, tx *gorm.DB, cards repositories.A2AAgentCardRepository, t a2aProxyTarget, step a2aCardStep) error {
	if step == nil {
		return utils.ErrEnvironmentNotProxied
	}
	return step(ctx, cards.WithTx(tx), t.key)
}

// deleteCardRow drops the card row together with its removed source.
func deleteCardRow(ctx context.Context, cards repositories.A2AAgentCardRepository, key a2aPublicationKey) error {
	if err := cards.DeleteForAgentEnv(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentName); err != nil {
		return fmt.Errorf("failed to delete the agent card: %w", err)
	}
	return nil
}

// removeProxy switches the environment to direct; it discards the stored secret, so it is refused unrecorded.
// A direct environment gets ifDirect, or utils.ErrEnvironmentNotProxied when it is nil.
func (s *a2aProxyService) removeProxy(ctx context.Context, t a2aProxyTarget, cardStep, ifDirect a2aCardStep) error {
	var attempt *audit.Attempt
	var teardown a2aTeardown
	err := s.tx(ctx, func(tx *gorm.DB) error {
		existing, err := s.lockedProxy(ctx, tx, t)
		if err != nil {
			return err
		}
		if existing == nil {
			return onDirect(ctx, tx, s.cardRepo, t, ifDirect)
		}
		attempt, err = s.beginAudit(ctx, audit.ActionAgentRemoveA2AProxy, t, existing.UpstreamAuthHeader, existing.UpstreamAuthSecret != "")
		if err != nil {
			return err
		}
		teardown, err = s.switchToDirect(ctx, tx, t, cardStep)
		return err
	})
	attempt.Complete(ctx, err)
	if err != nil {
		return err
	}
	s.publisher.SendTeardown(ctx, teardown)
	return nil
}

// switchToDirect tears down the publication (keeping the artifact), deletes the proxy row and applies cardStep.
func (s *a2aProxyService) switchToDirect(ctx context.Context, tx *gorm.DB, t a2aProxyTarget, cardStep a2aCardStep) (a2aTeardown, error) {
	artifactUUID, err := s.artifactUUID(t)
	if err != nil {
		return a2aTeardown{}, err
	}
	teardown, err := s.publisher.TeardownEnv(ctx, tx, t.key, artifactUUID)
	if err != nil {
		return a2aTeardown{}, err
	}
	err = s.proxyRepo.WithTx(tx).Delete(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
	if err != nil {
		return a2aTeardown{}, fmt.Errorf("failed to delete the A2A proxy: %w", err)
	}
	return teardown, cardStep(ctx, s.cardRepo.WithTx(tx), t.key)
}

// artifactUUID is the environment's agent artifact, or uuid.Nil when it has none.
func (s *a2aProxyService) artifactUUID(t a2aProxyTarget) (uuid.UUID, error) {
	handle := agentEnvAPIArtifactHandle(t.key.ProjectName, t.key.AgentName, t.env.UUID)
	artifact, err := s.artifactRepo.GetByHandle(handle, t.key.OUID)
	if errors.Is(err, utils.ErrArtifactNotFound) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to read the agent's API artifact: %w", err)
	}
	return artifact.UUID, nil
}

// dropIfAgentGone undoes this request's writes when the agent was deleted or replaced meanwhile; the deletion could not see them.
func (s *a2aProxyService) dropIfAgentGone(ctx context.Context, t a2aProxyTarget) error {
	current, err := s.agent(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName)
	switch {
	case errors.Is(err, utils.ErrAgentNotFound):
	case err != nil:
		return err
	case current.UUID == t.agent.UUID:
		return nil
	}
	teardown, err := s.forget(ctx, t)
	if err != nil {
		s.logger.Error("Failed to drop the A2A proxy of a deleted agent",
			"agentName", t.key.AgentName, "environment", t.key.EnvironmentName, "error", err)
	}
	s.publisher.SendTeardown(ctx, teardown)
	return utils.ErrAgentNotFound
}

// forget removes the proxy row, its publication, the card row and the environment's artifact, whose deployments cascade.
func (s *a2aProxyService) forget(ctx context.Context, t a2aProxyTarget) (a2aTeardown, error) {
	var teardown a2aTeardown
	err := s.tx(ctx, func(tx *gorm.DB) error {
		if _, err := s.lockedProxy(ctx, tx, t); err != nil {
			return err
		}
		artifactUUID, err := s.artifactUUID(t)
		if err != nil {
			return err
		}
		if teardown, err = s.publisher.TeardownEnvAndDropArtifact(ctx, tx, t.key, artifactUUID); err != nil {
			return err
		}
		err = s.proxyRepo.WithTx(tx).Delete(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
		if err != nil && !errors.Is(err, repositories.ErrA2AProxyNotFound) {
			return fmt.Errorf("failed to delete the A2A proxy: %w", err)
		}
		// StoreCardSource writes the card row, so this service clears it as well.
		err = s.cardRepo.WithTx(tx).DeleteForAgentEnv(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentName)
		if err != nil {
			return fmt.Errorf("failed to delete the agent card: %w", err)
		}
		return nil
	})
	return teardown, err
}

// beginAudit opens the fail-closed intent record; a sink failure is reported as the audit outage it is.
func (s *a2aProxyService) beginAudit(
	ctx context.Context, action audit.Action, t a2aProxyTarget, upstreamAuthHeader string, authChanged bool,
) (*audit.Attempt, error) {
	attempt, err := audit.Begin(ctx, action,
		audit.Org(t.key.OUID),
		audit.ResourceNamed(audit.ResourceAgent, t.agent.UUID, t.agent.Name),
		audit.Project(t.key.ProjectName),
		audit.Environment(t.key.EnvironmentName),
		audit.Detail("agentName", t.agent.Name),
		audit.Detail("upstreamAuthHeader", upstreamAuthHeader),
		audit.Detail("upstreamAuthChanged", authChanged),
	)
	if err == nil {
		return attempt, nil
	}
	s.logger.Error("Refusing the A2A proxy change: audit record could not be written",
		"action", action, "agentName", t.agent.Name, "error", err)
	if errors.Is(err, audit.ErrRecorderUnavailable) {
		return nil, err
	}
	return nil, fmt.Errorf("%w: %w", audit.ErrRecorderUnavailable, err)
}

// ensureDefaultAgentConfig gives a newly proxied environment a platform A2A agent's inbound defaults; a saved config wins.
func ensureDefaultAgentConfig(ctx context.Context, configs repositories.AgentConfigRepository, key a2aPublicationKey) error {
	api := withA2AVersionHeader(resolveAPIConfig(nil, nil, nil, nil, nil, true))
	err := configs.CreateIfAbsent(ctx, &models.AgentConfig{
		OUID:                  key.OUID,
		ProjectName:           key.ProjectName,
		AgentName:             key.AgentName,
		EnvironmentName:       key.EnvironmentName,
		EnableApiKeySecurity:  api.EnableApiKeySecurity,
		CORSEnabled:           api.CORSEnabled,
		CORSAllowOrigins:      api.CORSAllowOrigins,
		CORSAllowMethods:      api.CORSAllowMethods,
		CORSAllowHeaders:      api.CORSAllowHeaders,
		CORSAllowCredentials:  api.CORSAllowCredentials,
		EnableOAuthSecurity:   api.EnableOAuthSecurity,
		OAuthIssuers:          api.OAuthIssuers,
		OAuthAudiences:        api.OAuthAudiences,
		OAuthHeaderName:       api.OAuthHeaderName,
		OAuthAuthHeaderPrefix: api.OAuthAuthHeaderPrefix,
		OAuthForwardToken:     api.OAuthForwardToken,
	})
	if err != nil {
		return fmt.Errorf("failed to write the default agent config: %w", err)
	}
	return nil
}

// validateA2AUpstreamAuth enforces what the gateway carries safely: it pastes into single-quoted YAML and renders {{ }}.
func validateA2AUpstreamAuth(auth *models.A2AProxyAuthUpdate) error {
	if auth == nil {
		return nil
	}
	switch {
	case !a2aUpstreamHeaderPattern.MatchString(auth.Header):
		return fmt.Errorf("%w: header may contain only letters, digits, '-' and '_'", utils.ErrInvalidUpstreamAuth)
	case len(auth.Header) > a2aUpstreamHeaderMaxLen:
		return fmt.Errorf("%w: header must be at most %d characters", utils.ErrInvalidUpstreamAuth, a2aUpstreamHeaderMaxLen)
	case isReservedUpstreamHeader(auth.Header):
		return fmt.Errorf("%w: the %s header cannot be set upstream", utils.ErrInvalidUpstreamAuth, auth.Header)
	}
	if auth.Value == nil {
		return nil
	}
	value := *auth.Value
	switch {
	case value == "":
		return fmt.Errorf("%w: value must not be empty; omit upstreamAuth to clear it", utils.ErrInvalidUpstreamAuth)
	case len(value) > a2aUpstreamValueMaxLen:
		return fmt.Errorf("%w: value must be at most %d bytes", utils.ErrInvalidUpstreamAuth, a2aUpstreamValueMaxLen)
	case strings.ContainsAny(value, "'\r\n"):
		return fmt.Errorf("%w: value must not contain quotes or line breaks", utils.ErrInvalidUpstreamAuth)
	case strings.Contains(value, "{{"):
		return fmt.Errorf("%w: value must not contain '{{'", utils.ErrInvalidUpstreamAuth)
	}
	return nil
}

// a2aReservedUpstreamHeaders (lowercase) frame or route the request, so the gateway must own them.
var a2aReservedUpstreamHeaders = []string{
	"host", "content-length", "transfer-encoding", "connection", "upgrade", "te", "keep-alive", "trailer",
}

func isReservedUpstreamHeader(header string) bool {
	name := strings.ToLower(header)
	return strings.HasPrefix(name, "proxy-") || slices.Contains(a2aReservedUpstreamHeaders, name)
}

func encryptA2AUpstreamSecret(value string, key []byte) (string, error) {
	ciphertext, err := utils.EncryptBytes([]byte(value), key)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt the upstream secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
