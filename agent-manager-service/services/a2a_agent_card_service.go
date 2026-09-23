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
	"fmt"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// A2AAgentCardServiceInterface reads and re-queues one agent-environment
// pair's A2A publication row for the agent-card API.
type A2AAgentCardServiceInterface interface {
	// GetAgentCard returns nil, nil when no row exists for the pair, which the
	// controller turns into a 404.
	GetAgentCard(ctx context.Context, ouID, projectName, agentName, envID string) (*models.A2AAgentCardView, error)
	// RefreshAgentCard returns utils.ErrA2APublicationNotFound when no row
	// exists for the pair.
	RefreshAgentCard(ctx context.Context, ouID, projectName, agentName, envID string) error
}

// A2AAgentCardService implements A2AAgentCardServiceInterface.
type A2AAgentCardService struct {
	pubRepo  repositories.A2APublicationRepository
	ocClient client.OpenChoreoClient
}

// NewA2AAgentCardService creates an A2AAgentCardService.
func NewA2AAgentCardService(
	pubRepo repositories.A2APublicationRepository,
	ocClient client.OpenChoreoClient,
) A2AAgentCardServiceInterface {
	return &A2AAgentCardService{pubRepo: pubRepo, ocClient: ocClient}
}

// resolveEnvironmentUUID mirrors agentAPIKeyService.resolveAgentAPIArtifact's
// envID lookup: envID on the wire is the environment name, not its UUID.
func (s *A2AAgentCardService) resolveEnvironmentUUID(ctx context.Context, ouID, envID string) (uuid.UUID, error) {
	environment, err := s.ocClient.GetEnvironment(ctx, ouID, envID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to get environment: %w", translateEnvironmentError(err))
	}
	envUUID, err := uuid.Parse(environment.UUID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("environment %q has an unparseable UUID %q: %w", envID, environment.UUID, err)
	}
	return envUUID, nil
}

func (s *A2AAgentCardService) GetAgentCard(
	ctx context.Context, ouID, projectName, agentName, envID string,
) (*models.A2AAgentCardView, error) {
	envUUID, err := s.resolveEnvironmentUUID(ctx, ouID, envID)
	if err != nil {
		return nil, err
	}
	pub, err := s.pubRepo.GetForAgentEnv(ctx, ouID, projectName, agentName, envUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent card: %w", err)
	}
	if pub == nil {
		return nil, nil //nolint:nilnil // absence is not an error; the caller turns it into a 404
	}
	return toAgentCardView(pub)
}

func (s *A2AAgentCardService) RefreshAgentCard(ctx context.Context, ouID, projectName, agentName, envID string) error {
	envUUID, err := s.resolveEnvironmentUUID(ctx, ouID, envID)
	if err != nil {
		return err
	}
	// Checked up front rather than trusting RequeueCard's rows-affected: the
	// repo's update-only statement matches zero rows for a redeployed agent
	// too, so it cannot tell "no such pair" from "nothing changed".
	pub, err := s.pubRepo.GetForAgentEnv(ctx, ouID, projectName, agentName, envUUID)
	if err != nil {
		return fmt.Errorf("failed to load agent card: %w", err)
	}
	if pub == nil {
		return utils.ErrA2APublicationNotFound
	}
	return s.pubRepo.RequeueCard(ctx, ouID, projectName, agentName, envUUID)
}

// toAgentCardView decodes the stored card into a free-form object for the API
// response; the repo keeps it as raw JSON so a round trip stays byte-comparable.
func toAgentCardView(pub *models.A2APublication) (*models.A2AAgentCardView, error) {
	view := &models.A2AAgentCardView{
		Status:    pub.Status,
		FetchedAt: pub.CardFetchedAt,
		LastError: pub.LastError,
	}
	if len(pub.AgentCard) > 0 {
		var card map[string]interface{}
		if err := json.Unmarshal(pub.AgentCard, &card); err != nil {
			return nil, fmt.Errorf("stored agent card is not valid JSON: %w", err)
		}
		view.Card = card
	}
	return view, nil
}
