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

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// a2aPublicationKey names one agent-environment gateway publication.
type a2aPublicationKey struct {
	OUID            string
	ProjectName     string
	AgentName       string
	EnvironmentName string
	EnvironmentUUID uuid.UUID
	// AgentUUID is nil when unknown; the A2A kind then checks the agent by name.
	AgentUUID *uuid.UUID
}

// a2aTeardown is an environment's gateway withdrawal, sent once its DB work has committed.
type a2aTeardown struct {
	artifactUUID uuid.UUID
	gatewayIDs   []string
	// dropsArtifact deletes the gateway's Agent with its API keys; otherwise it is undeployed and keeps them.
	dropsArtifact bool
	// undeployIDs is what each gateway's undeploy event carries; a gateway not in it gets no id.
	undeployIDs map[string]agentUndeploy
}

// TxRunner opens one database transaction and runs fn inside it, so a unit test can hand fn a sentinel.
type TxRunner func(ctx context.Context, fn func(tx *gorm.DB) error) error

// A2APublisher holds the gateway-publication steps agentManagerService and a2aProxyService share. A publication is an
// A2A agent's deploy intent for one environment, which the reconcile core carries out through A2AAgentKind.
type A2APublisher struct {
	tx             TxRunner
	intents        repositories.DeployIntentRepository
	deploys        *reconcile.Core
	worker         *reconcile.Worker
	proxyRepo      repositories.A2AProxyRepository
	cardRepo       repositories.A2AAgentCardRepository
	artifactRepo   repositories.ArtifactRepository
	deploymentRepo repositories.DeploymentRepository
	gatewayRepo    repositories.GatewayRepository
	events         *GatewayEventsService
	logger         *slog.Logger
}

// NewA2APublisher creates an A2APublisher.
func NewA2APublisher(
	tx TxRunner,
	intents repositories.DeployIntentRepository,
	deploys *reconcile.Core,
	worker *reconcile.Worker,
	proxyRepo repositories.A2AProxyRepository,
	cardRepo repositories.A2AAgentCardRepository,
	artifactRepo repositories.ArtifactRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
	events *GatewayEventsService,
	logger *slog.Logger,
) *A2APublisher {
	return &A2APublisher{
		tx:             tx,
		intents:        intents,
		deploys:        deploys,
		worker:         worker,
		proxyRepo:      proxyRepo,
		cardRepo:       cardRepo,
		artifactRepo:   artifactRepo,
		deploymentRepo: deploymentRepo,
		gatewayRepo:    gatewayRepo,
		events:         events,
		logger:         logger,
	}
}

// a2aPublicationSourceFor says where the agent's gateway upstream comes from.
func a2aPublicationSourceFor(agent *models.AgentResponse) models.A2APublicationSource {
	if isExternalProvisioned(agent) {
		return models.A2APublicationSourceExternal
	}
	return models.A2APublicationSourcePlatform
}

// Enqueue records the environment's publication as a deploy intent, due at once with a fresh attempt budget; a
// non-nil tx makes it part of the caller's transaction. Call Nudge once that has committed. A publication recorded
// for the agent under the environment's name or UUID before, e.g. for a deleted environment of the same name, is
// replaced.
func (p *A2APublisher) Enqueue(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, source models.A2APublicationSource, artifactUUID uuid.UUID,
) error {
	intent, err := a2aIntent(key, source, artifactUUID)
	if err != nil {
		return err
	}
	if err := p.dropReplacedPublications(ctx, tx, key, artifactUUID); err != nil {
		return err
	}
	if err := p.deploys.Want(ctx, tx, &intent); err != nil {
		return fmt.Errorf("failed to queue the A2A gateway publication: %w", err)
	}
	return nil
}

// dropReplacedPublications deletes the agent's publications that the one for key and artifactUUID replaces: those of
// a same-named environment, and those of this environment under another artifact.
func (p *A2APublisher) dropReplacedPublications(ctx context.Context, tx *gorm.DB, key a2aPublicationKey, artifactUUID uuid.UUID) error {
	intents := p.publications(tx)
	existing, err := intents.List(ctx, a2aAgentFilter(key.OUID, key.ProjectName, key.AgentName))
	if err != nil {
		return fmt.Errorf("failed to read the agent's A2A publications: %w", err)
	}
	for _, intent := range existing {
		pub, err := decodeA2APublication(intent)
		if err != nil {
			return err
		}
		sameEnv := pub.EnvironmentUUID == key.EnvironmentUUID
		if (sameEnv && pub.ArtifactUUID == artifactUUID) || (!sameEnv && pub.EnvironmentName != key.EnvironmentName) {
			continue
		}
		replaced := a2aEnvFilter(pub.key())
		replaced.ArtifactUUID = &pub.ArtifactUUID
		if err := intents.Delete(ctx, replaced); err != nil {
			return fmt.Errorf("failed to replace the agent's A2A publication: %w", err)
		}
	}
	return nil
}

// a2aAgentFilter selects every environment's publication of an agent.
func a2aAgentFilter(ouID, projectName, agentName string) repositories.DeployIntentFilter {
	return repositories.DeployIntentFilter{
		Kind: models.KindAgent, OUID: ouID,
		KindData: map[string]any{"projectName": projectName, "agentName": agentName},
	}
}

// a2aEnvFilter selects the agent's publication in key's environment.
func a2aEnvFilter(key a2aPublicationKey) repositories.DeployIntentFilter {
	filter := a2aAgentFilter(key.OUID, key.ProjectName, key.AgentName)
	envUUID := key.EnvironmentUUID
	filter.EnvironmentUUID = &envUUID
	return filter
}

// Nudge has the worker publish recorded publications now rather than at its next tick; call it once they have
// committed.
func (p *A2APublisher) Nudge() {
	if p.worker != nil {
		p.worker.Nudge()
	}
}

// ShouldPublish is true for every internal A2A agent, and for an external one only while its environment is
// proxied; a non-nil tx reads the proxy row inside the caller's transaction.
func (p *A2APublisher) ShouldPublish(
	ctx context.Context, tx *gorm.DB, agent *models.AgentResponse, ouID, projectName string, envUUID uuid.UUID,
) (bool, error) {
	if !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return false, nil
	}
	if !isExternalProvisioned(agent) {
		return true, nil
	}
	_, err := p.proxies(tx).GetByEnvUUID(ctx, ouID, projectName, agent.Name, envUUID)
	switch {
	case errors.Is(err, repositories.ErrA2AProxyNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return true, nil
}

// SaveAndRepublish runs save in one transaction with the republish the agent's environment is owed afterwards, if
// any. An external agent is decided under its environment's transition lock, so a concurrent switch to direct cannot
// leave an external publication behind, and a save whose republish cannot be queued is rolled back whole.
func (p *A2APublisher) SaveAndRepublish(
	ctx context.Context, agent *models.AgentResponse, key a2aPublicationKey, artifactUUID uuid.UUID, save func(tx *gorm.DB) error,
) error {
	err := p.tx(ctx, func(tx *gorm.DB) error {
		if isExternalProvisioned(agent) {
			if err := lockA2AProxyEnv(ctx, p.proxies(tx), key); err != nil {
				return err
			}
		}
		publish, err := p.ShouldPublish(ctx, tx, agent, key.OUID, key.ProjectName, key.EnvironmentUUID)
		if err != nil {
			return err
		}
		if err := save(tx); err != nil {
			return err
		}
		if !publish {
			return nil
		}
		return p.Enqueue(ctx, tx, key, a2aPublicationSourceFor(agent), artifactUUID)
	})
	if err == nil {
		p.Nudge()
	}
	return err
}

// GatewayURL is where the environment's ingress gateway serves the agent, or "" while none is mapped to it yet.
func (p *A2APublisher) GatewayURL(ouID string, envUUID uuid.UUID, agentName string) (string, error) {
	gateway, err := findIngressGateway(p.gatewayRepo, ouID, envUUID)
	if err != nil || gateway == nil {
		return "", err
	}
	return a2aGatewayURL(gateway, agentName), nil
}

// PublicationStatus is where the environment's publication stands: DEPLOYING while it waits for the agent's endpoint
// or the gateway's ack, then what the gateway acked. It returns repositories.ErrDeployIntentNotFound when the
// environment has no publication.
func (p *A2APublisher) PublicationStatus(ctx context.Context, key a2aPublicationKey) (reconcile.Status, error) {
	intents, err := p.intents.List(ctx, a2aEnvFilter(key))
	if err != nil {
		return reconcile.Status{}, fmt.Errorf("failed to read the A2A publication: %w", err)
	}
	if len(intents) == 0 {
		return reconcile.Status{}, repositories.ErrDeployIntentNotFound
	}
	return p.deploys.Status(ctx, intents[0])
}

// TeardownEnv drops one environment's publication in tx and starts undeploying the Agent from its gateways, but keeps the
// artifact and its API keys; send the result after commit.
func (p *A2APublisher) TeardownEnv(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, artifactUUID uuid.UUID,
) (a2aTeardown, error) {
	// Read first: the deployment rows name the gateways holding the Agent.
	gatewayIDs := collectA2AAgentDeletionTargets(p.deploymentRepo, p.gatewayRepo, artifactUUID, key.OUID, true, p.logger)
	undeployIDs, err := beginA2AAgentUndeploy(ctx, p.deploymentRepo.WithTx(tx), artifactUUID, key.OUID, gatewayIDs)
	if err != nil {
		return a2aTeardown{}, err
	}
	if err := p.deletePublication(ctx, tx, key); err != nil {
		return a2aTeardown{}, err
	}
	return a2aTeardown{artifactUUID: artifactUUID, gatewayIDs: gatewayIDs, undeployIDs: undeployIDs}, nil
}

// TeardownEnvAndDropArtifact is TeardownEnv that deletes the artifact (uuid.Nil: none) and its deployments instead, so
// its API keys go too.
func (p *A2APublisher) TeardownEnvAndDropArtifact(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, artifactUUID uuid.UUID,
) (a2aTeardown, error) {
	// Read first: the deployment rows name the gateways holding the Agent.
	gatewayIDs := collectA2AAgentDeletionTargets(p.deploymentRepo, p.gatewayRepo, artifactUUID, key.OUID, true, p.logger)
	if artifactUUID != uuid.Nil {
		if err := p.deploymentRepo.WithTx(tx).DeleteForArtifact(ctx, artifactUUID, key.OUID); err != nil {
			return a2aTeardown{}, fmt.Errorf("failed to delete the A2A agent's gateway deployments: %w", err)
		}
	}
	if err := p.deletePublication(ctx, tx, key); err != nil {
		return a2aTeardown{}, err
	}
	teardown := a2aTeardown{artifactUUID: artifactUUID, gatewayIDs: gatewayIDs, dropsArtifact: true}
	if artifactUUID == uuid.Nil {
		return teardown, nil
	}
	err := p.artifactRepo.Delete(tx, artifactUUID.String())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return a2aTeardown{}, fmt.Errorf("failed to delete the agent's API artifact: %w", err)
	}
	return teardown, nil
}

func (p *A2APublisher) deletePublication(ctx context.Context, tx *gorm.DB, key a2aPublicationKey) error {
	if err := p.publications(tx).Delete(ctx, a2aEnvFilter(key)); err != nil {
		return fmt.Errorf("failed to delete the A2A publication: %w", err)
	}
	return nil
}

// SendTeardown tells each gateway to stop serving the torn-down Agent; best effort.
func (p *A2APublisher) SendTeardown(ctx context.Context, t a2aTeardown) {
	sendA2AAgentWithdrawal(ctx, p.events, t.artifactUUID, t.gatewayIDs, !t.dropsArtifact, t.undeployIDs, p.logger)
}

// DeleteForAgent drops the agent's publication and proxy rows; deleteAgentAPIArtifact tells the gateways.
func (p *A2APublisher) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error {
	var errs []error
	if err := p.intents.Delete(ctx, a2aAgentFilter(ouID, projectName, agentName)); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete A2A publications: %w", err))
	}
	if err := p.proxyRepo.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete A2A proxies: %w", err))
	}
	return errors.Join(errs...)
}

// TeardownEnvironment removes a deleted environment's A2A rows, deployments and A2A agent artifacts, then tells the
// gateways; one failure does not stop the rest. Call it before the environment's card rows go: they name the agents.
func (p *A2APublisher) TeardownEnvironment(ctx context.Context, ouID string, envUUID uuid.UUID) error {
	envFilter := repositories.DeployIntentFilter{Kind: models.KindAgent, OUID: ouID, EnvironmentUUID: &envUUID}
	intents, err := p.intents.List(ctx, envFilter)
	if err != nil {
		return fmt.Errorf("failed to list the environment's A2A publications: %w", err)
	}
	var errs []error
	var teardowns []a2aTeardown
	for _, intent := range intents {
		pub, err := decodeA2APublication(intent)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		teardown, err := p.teardownDeletedEnv(ctx, pub)
		if err != nil {
			errs = append(errs, err)
		}
		teardowns = append(teardowns, teardown)
	}
	// A switch to direct drops the publication but keeps the artifact, so find the rest through the card rows.
	swept, err := p.dropLeftoverArtifacts(ctx, ouID, envUUID, teardowns)
	if err != nil {
		errs = append(errs, err)
	}
	teardowns = append(teardowns, swept...)
	// Rows a failed teardown left, and proxies that never published, go regardless: the environment is gone.
	if err := p.intents.Delete(ctx, envFilter); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete the environment's A2A publications: %w", err))
	}
	if err := p.proxyRepo.DeleteForEnvironment(ctx, ouID, envUUID); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete the environment's A2A proxies: %w", err))
	}
	for _, teardown := range teardowns {
		p.SendTeardown(ctx, teardown)
	}
	return errors.Join(errs...)
}

// EnvironmentUUIDs lists every environment UUID with publication or proxy rows recorded under name, even when one listing fails.
func (p *A2APublisher) EnvironmentUUIDs(ctx context.Context, ouID, name string) ([]uuid.UUID, error) {
	var errs []error
	pubs, err := p.intents.ListEnvironmentUUIDs(ctx, repositories.DeployIntentFilter{
		Kind: models.KindAgent, OUID: ouID, KindData: map[string]any{"environmentName": name},
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list A2A publication environments: %w", err))
	}
	proxies, err := p.proxyRepo.ListEnvironmentUUIDs(ctx, ouID, name)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list A2A proxy environments: %w", err))
	}
	return append(pubs, proxies...), errors.Join(errs...)
}

// teardownDeletedEnv removes one publication together with its artifact, which a deleted environment no longer needs.
// On failure it still returns the teardown to send; agent deletion sweeps the artifact left behind.
func (p *A2APublisher) teardownDeletedEnv(ctx context.Context, pub a2aPublication) (a2aTeardown, error) {
	key := pub.key()
	var teardown a2aTeardown
	err := p.tx(ctx, func(tx *gorm.DB) error {
		var err error
		teardown, err = p.TeardownEnvAndDropArtifact(ctx, tx, key, pub.ArtifactUUID)
		return err
	})
	if err != nil {
		// The environment's rows go regardless, so the gateways must drop the Agent now or never.
		return a2aTeardown{
			artifactUUID:  pub.ArtifactUUID,
			gatewayIDs:    collectA2AAgentDeletionTargets(p.deploymentRepo, p.gatewayRepo, pub.ArtifactUUID, pub.OUID, true, p.logger),
			dropsArtifact: true,
		}, fmt.Errorf("agent %s: %w", pub.AgentName, err)
	}
	return teardown, nil
}

// dropLeftoverArtifacts deletes the artifacts of the environment's A2A agents that no publication teardown covered.
// The card rows name those agents; a REST or chat agent's artifact for the environment is not touched.
func (p *A2APublisher) dropLeftoverArtifacts(ctx context.Context, ouID string, envUUID uuid.UUID, done []a2aTeardown) ([]a2aTeardown, error) {
	cards, err := p.cardRepo.ListForEnvironment(ctx, ouID, envUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to list the environment's A2A agent cards: %w", err)
	}
	covered := make(map[uuid.UUID]struct{}, len(done))
	for _, t := range done {
		covered[t.artifactUUID] = struct{}{}
	}
	var errs []error
	var teardowns []a2aTeardown
	for _, card := range cards {
		artifact, err := agentEnvAPIArtifact(p.artifactRepo, ouID, card.ProjectName, card.AgentName, envUUID.String())
		if errors.Is(err, utils.ErrArtifactNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read agent %s's artifact: %w", card.AgentName, err))
			continue
		}
		if _, ok := covered[artifact.UUID]; ok {
			continue
		}
		// Read before the delete: the deployment rows cascade with the artifact.
		gatewayIDs := collectA2AAgentDeletionTargets(p.deploymentRepo, p.gatewayRepo, artifact.UUID, ouID, true, p.logger)
		err = p.tx(ctx, func(tx *gorm.DB) error { return p.artifactRepo.Delete(tx, artifact.UUID.String()) })
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			errs = append(errs, fmt.Errorf("failed to delete agent artifact %s: %w", artifact.Handle, err))
			continue
		}
		teardowns = append(teardowns, a2aTeardown{artifactUUID: artifact.UUID, gatewayIDs: gatewayIDs, dropsArtifact: true})
	}
	return teardowns, errors.Join(errs...)
}

func (p *A2APublisher) publications(tx *gorm.DB) repositories.DeployIntentRepository {
	if tx == nil {
		return p.intents
	}
	return p.intents.WithTx(tx)
}

func (p *A2APublisher) proxies(tx *gorm.DB) repositories.A2AProxyRepository {
	if tx == nil {
		return p.proxyRepo
	}
	return p.proxyRepo.WithTx(tx)
}

// lockA2AProxyEnv takes the environment's transition lock on proxies, which must be bound to the transaction that holds it.
func lockA2AProxyEnv(ctx context.Context, proxies repositories.A2AProxyRepository, key a2aPublicationKey) error {
	if err := proxies.LockAgentEnv(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID); err != nil {
		return fmt.Errorf("failed to lock the A2A proxy: %w", err)
	}
	return nil
}

// lockedA2AProxy takes the environment's transition lock and reads its proxy row under it; nil while direct.
func lockedA2AProxy(ctx context.Context, proxies repositories.A2AProxyRepository, key a2aPublicationKey) (*models.A2AProxy, error) {
	if err := lockA2AProxyEnv(ctx, proxies, key); err != nil {
		return nil, err
	}
	proxy, err := proxies.GetByEnvUUID(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AProxyNotFound) {
		return nil, nil //nolint:nilnil // No row means the environment is direct.
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return proxy, nil
}
