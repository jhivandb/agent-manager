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
	// AgentUUID is nil when unknown; the reconciler then checks the agent by name.
	AgentUUID *uuid.UUID
}

// a2aTeardown is an environment's gateway withdrawal, sent once its DB work has committed.
type a2aTeardown struct {
	artifactUUID uuid.UUID
	gatewayIDs   []string
	// dropsArtifact deletes the gateway's Agent with its API keys; otherwise it is undeployed and keeps them.
	dropsArtifact bool
}

// TxRunner opens one database transaction and runs fn inside it, so a unit test can hand fn a sentinel.
type TxRunner func(ctx context.Context, fn func(tx *gorm.DB) error) error

// A2APublisher holds the gateway-publication steps agentManagerService and a2aProxyService share.
type A2APublisher struct {
	tx             TxRunner
	pubRepo        repositories.A2APublicationRepository
	proxyRepo      repositories.A2AProxyRepository
	artifactRepo   repositories.ArtifactRepository
	deploymentRepo repositories.DeploymentRepository
	gatewayRepo    repositories.GatewayRepository
	events         *GatewayEventsService
	logger         *slog.Logger
}

// NewA2APublisher creates an A2APublisher.
func NewA2APublisher(
	tx TxRunner,
	pubRepo repositories.A2APublicationRepository,
	proxyRepo repositories.A2AProxyRepository,
	artifactRepo repositories.ArtifactRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
	events *GatewayEventsService,
	logger *slog.Logger,
) *A2APublisher {
	return &A2APublisher{
		tx:             tx,
		pubRepo:        pubRepo,
		proxyRepo:      proxyRepo,
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

// Enqueue queues the environment's publication; a non-nil tx makes it part of the caller's transaction.
func (p *A2APublisher) Enqueue(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, source models.A2APublicationSource, artifactUUID uuid.UUID,
) error {
	pub := &models.A2APublication{
		OUID:            key.OUID,
		ProjectName:     key.ProjectName,
		AgentName:       key.AgentName,
		EnvironmentName: key.EnvironmentName,
		EnvironmentUUID: key.EnvironmentUUID,
		ArtifactUUID:    artifactUUID,
		AgentUUID:       key.AgentUUID,
		Source:          source,
	}
	if err := p.publications(tx).Enqueue(ctx, pub); err != nil {
		return fmt.Errorf("failed to queue the A2A gateway publication: %w", err)
	}
	return nil
}

// ShouldPublish is true for every internal A2A agent, and for an external one only while its environment is proxied.
func (p *A2APublisher) ShouldPublish(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, envUUID uuid.UUID,
) (bool, error) {
	if !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return false, nil
	}
	if !isExternalProvisioned(agent) {
		return true, nil
	}
	_, err := p.proxyRepo.GetByEnvUUID(ctx, ouID, projectName, agent.Name, envUUID)
	switch {
	case errors.Is(err, repositories.ErrA2AProxyNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return true, nil
}

// Publication returns the environment's publication row.
func (p *A2APublisher) Publication(ctx context.Context, key a2aPublicationKey) (*models.A2APublication, error) {
	return p.pubRepo.Get(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID)
}

// TeardownEnv removes one environment's publication and gateway deployments inside tx, keeping
// the artifact so its API keys survive a switch back; send the result once tx commits.
func (p *A2APublisher) TeardownEnv(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, artifactUUID uuid.UUID,
) (a2aTeardown, error) {
	// Read first: the deployment rows name the gateways holding the Agent.
	gatewayIDs := collectA2AAgentDeletionTargets(p.deploymentRepo, p.gatewayRepo, artifactUUID, key.OUID, true, p.logger)
	if artifactUUID != uuid.Nil {
		if err := p.deploymentRepo.WithTx(tx).DeleteForArtifact(ctx, artifactUUID, key.OUID); err != nil {
			return a2aTeardown{}, fmt.Errorf("failed to delete the A2A agent's gateway deployments: %w", err)
		}
	}
	err := p.publications(tx).DeleteForAgentEnv(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID)
	if err != nil {
		return a2aTeardown{}, fmt.Errorf("failed to delete the A2A publication: %w", err)
	}
	return a2aTeardown{artifactUUID: artifactUUID, gatewayIDs: gatewayIDs}, nil
}

// TeardownEnvAndDropArtifact is TeardownEnv for an environment that is going away: it also deletes
// the artifact, whose API keys must not outlive it. uuid.Nil means it has none.
func (p *A2APublisher) TeardownEnvAndDropArtifact(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, artifactUUID uuid.UUID,
) (a2aTeardown, error) {
	teardown, err := p.TeardownEnv(ctx, tx, key, artifactUUID)
	if err != nil {
		return a2aTeardown{}, err
	}
	teardown.dropsArtifact = true
	if artifactUUID == uuid.Nil {
		return teardown, nil
	}
	err = p.artifactRepo.Delete(tx, artifactUUID.String())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return a2aTeardown{}, fmt.Errorf("failed to delete the agent's API artifact: %w", err)
	}
	return teardown, nil
}

// SendTeardown tells each gateway to stop serving the torn-down Agent; best effort.
func (p *A2APublisher) SendTeardown(ctx context.Context, t a2aTeardown) {
	sendA2AAgentWithdrawal(ctx, p.events, t.artifactUUID, t.gatewayIDs, !t.dropsArtifact, p.logger)
}

// DeleteForAgent drops the agent's publication and proxy rows; deleteAgentAPIArtifact tells the gateways.
func (p *A2APublisher) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error {
	var errs []error
	if err := p.pubRepo.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete A2A publications: %w", err))
	}
	if err := p.proxyRepo.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete A2A proxies: %w", err))
	}
	return errors.Join(errs...)
}

// TeardownEnvironment removes every A2A publication, gateway deployment, agent artifact and proxy row
// of a deleted environment, then tells the gateways. One agent's failure does not stop the rest.
func (p *A2APublisher) TeardownEnvironment(ctx context.Context, ouID string, envUUID uuid.UUID) error {
	pubs, err := p.pubRepo.ListForEnvironment(ctx, ouID, envUUID)
	if err != nil {
		return fmt.Errorf("failed to list the environment's A2A publications: %w", err)
	}
	var errs []error
	var teardowns []a2aTeardown
	for _, pub := range pubs {
		teardown, err := p.teardownDeletedEnv(ctx, pub)
		if err != nil {
			errs = append(errs, err)
		}
		teardowns = append(teardowns, teardown)
	}
	// Rows a failed teardown left, and proxies that never published, go regardless: the environment is gone.
	if err := p.pubRepo.DeleteForEnvironment(ctx, ouID, envUUID); err != nil {
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

// teardownDeletedEnv removes one publication together with its artifact, which a deleted environment no longer needs.
// On failure it still returns the teardown to send; agent deletion sweeps the artifact left behind.
func (p *A2APublisher) teardownDeletedEnv(ctx context.Context, pub models.A2APublication) (a2aTeardown, error) {
	key := a2aPublicationKey{
		OUID: pub.OUID, ProjectName: pub.ProjectName, AgentName: pub.AgentName,
		EnvironmentName: pub.EnvironmentName, EnvironmentUUID: pub.EnvironmentUUID,
	}
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

func (p *A2APublisher) publications(tx *gorm.DB) repositories.A2APublicationRepository {
	if tx == nil {
		return p.pubRepo
	}
	return p.pubRepo.WithTx(tx)
}
