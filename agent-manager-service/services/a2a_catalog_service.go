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
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const (
	a2aCatalogPipelineTTL = 30 * time.Second
	a2aAPIKeyHeader       = "X-API-Key"
)

type A2ACatalogService interface {
	ListA2AAgents(ctx context.Context, ouID string, f models.A2ACatalogFilter) (*models.A2ACatalogPage, error)
	GetA2AConnect(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AConnectInfo, error)
}

type pipelineOrder struct {
	order   []string
	expires time.Time
}

type a2aCatalogService struct {
	catalogRepo     repositories.A2ACatalogRepository
	cardService     A2AAgentCardService
	proxyService    A2AProxyService
	publisher       *A2APublisher
	agentConfigRepo repositories.AgentConfigRepository
	gatewayRepo     repositories.GatewayRepository
	ocClient        client.OpenChoreoClient
	logger          *slog.Logger

	now           func() time.Time
	pipelineMu    sync.Mutex
	pipelineCache map[string]pipelineOrder
}

func NewA2ACatalogService(
	catalogRepo repositories.A2ACatalogRepository,
	cardService A2AAgentCardService,
	proxyService A2AProxyService,
	publisher *A2APublisher,
	agentConfigRepo repositories.AgentConfigRepository,
	gatewayRepo repositories.GatewayRepository,
	ocClient client.OpenChoreoClient,
	logger *slog.Logger,
) A2ACatalogService {
	return &a2aCatalogService{
		catalogRepo:     catalogRepo,
		cardService:     cardService,
		proxyService:    proxyService,
		publisher:       publisher,
		agentConfigRepo: agentConfigRepo,
		gatewayRepo:     gatewayRepo,
		ocClient:        ocClient,
		logger:          logger,
		now:             time.Now,
		pipelineCache:   make(map[string]pipelineOrder),
	}
}

type agentKey struct {
	project string
	agent   string
}

func (s *a2aCatalogService) ListA2AAgents(ctx context.Context, ouID string, f models.A2ACatalogFilter) (*models.A2ACatalogPage, error) {
	rows, total, err := s.catalogRepo.ListAgents(ctx, ouID, f)
	if err != nil {
		return nil, fmt.Errorf("list a2a catalogue agents: %w", err)
	}
	entries := make([]models.A2ACatalogEntry, 0)
	orders := make(map[string][]string)
	for _, group := range groupByAgent(rows) {
		project := group[0].Card.ProjectName
		order, seen := orders[project]
		if !seen {
			order = s.environmentOrder(ctx, ouID, project)
			orders[project] = order
		}
		entries = append(entries, s.buildEntry(sortByEnvironmentOrder(group, order)))
	}
	return &models.A2ACatalogPage{Entries: entries, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

// a2aEnvRef names one agent in one environment.
type a2aEnvRef struct {
	ouID, project, agent, env string
	envUUID                   uuid.UUID
}

func (s *a2aCatalogService) GetA2AConnect(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AConnectInfo, error) {
	card, err := s.cardService.GetA2AAgentCard(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		return nil, err
	}
	env, err := s.ocClient.GetEnvironment(ctx, ouID, envName)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment %s: %w", envName, translateEnvironmentError(err))
	}
	ref := a2aEnvRef{ouID: ouID, project: projectName, agent: agentName, env: envName, envUUID: card.EnvironmentUUID}
	info := &models.A2AConnectInfo{Kind: card.Source, IsProduction: env.IsProduction}
	if card.Source == models.A2AAgentCardSourceExternal {
		proxied, err := s.applyProxyStatus(ctx, info, ref)
		if err != nil {
			return nil, err
		}
		if !proxied {
			return info, nil
		}
	} else if err := s.applyPlatformGateway(ctx, info, ref); err != nil {
		return nil, err
	}
	if info.Security, err = s.connectSecurity(ctx, ref); err != nil {
		return nil, err
	}
	return info, nil
}

// applyProxyStatus fills info from the environment's proxy; false means the external agent is reached directly.
func (s *a2aCatalogService) applyProxyStatus(ctx context.Context, info *models.A2AConnectInfo, ref a2aEnvRef) (bool, error) {
	status, err := s.proxyService.GetA2AProxy(ctx, ref.ouID, ref.project, ref.agent, ref.env)
	if errors.Is(err, utils.ErrEnvironmentNotProxied) {
		info.Security.Mode = models.A2ASecurityModeUnmanaged
		info.Publication.Status = models.A2AConnectPublicationNone
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info.Proxied = true
	info.GatewayURL = status.GatewayURL
	info.Publication = models.A2AConnectPublication{Status: string(status.Status), LastError: status.LastError}
	return true, nil
}

func (s *a2aCatalogService) applyPlatformGateway(ctx context.Context, info *models.A2AConnectInfo, ref a2aEnvRef) error {
	gatewayURL, err := s.publisher.GatewayURL(ref.ouID, ref.envUUID, ref.agent)
	if err != nil {
		return err
	}
	pub, err := s.publisher.Publication(ctx, ref.ouID, ref.project, ref.agent, ref.envUUID)
	status, lastError, err := a2aPublicationStatus(pub, err)
	if err != nil {
		return err
	}
	info.GatewayURL = gatewayURL
	info.Publication = models.A2AConnectPublication{Status: string(status), LastError: lastError}
	return nil
}

func (s *a2aCatalogService) connectSecurity(ctx context.Context, ref a2aEnvRef) (models.A2AConnectSecurity, error) {
	cfg, err := s.agentConfigRepo.Get(ctx, ref.ouID, ref.project, ref.agent, ref.env)
	if errors.Is(err, repositories.ErrAgentConfigNotFound) {
		return models.A2AConnectSecurity{Mode: models.A2ASecurityModeUnknown}, nil
	}
	if err != nil {
		return models.A2AConnectSecurity{}, fmt.Errorf("failed to read the agent config: %w", err)
	}
	switch {
	case cfg.EnableOAuthSecurity:
		issuers, err := s.resolveIssuers(ref.envUUID, cfg.OAuthIssuers)
		if err != nil {
			return models.A2AConnectSecurity{}, err
		}
		return models.A2AConnectSecurity{Mode: models.A2ASecurityModeOAuth, OAuth: oauthConfigFromAgentConfig(cfg), Issuers: issuers}, nil
	case cfg.EnableApiKeySecurity:
		return models.A2AConnectSecurity{Mode: models.A2ASecurityModeAPIKey, APIKeyHeader: a2aAPIKeyHeader}, nil
	default:
		return models.A2AConnectSecurity{Mode: models.A2ASecurityModeNone}, nil
	}
}

func (s *a2aCatalogService) resolveIssuers(envUUID uuid.UUID, names []string) ([]models.A2AConnectIssuer, error) {
	providers, err := s.gatewayRepo.ListIdentityProvidersByEnvironment(envUUID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to list identity providers: %w", err)
	}
	issuerByName := make(map[string]string, len(providers))
	for _, p := range providers {
		issuerByName[p.Name] = p.Issuer
	}
	issuers := make([]models.A2AConnectIssuer, 0, len(names))
	for _, name := range names {
		issuers = append(issuers, models.A2AConnectIssuer{Name: name, Issuer: issuerByName[name]})
	}
	return issuers, nil
}

func groupByAgent(rows []models.A2ACatalogRow) [][]models.A2ACatalogRow {
	var groups [][]models.A2ACatalogRow
	index := make(map[agentKey]int)
	for _, row := range rows {
		key := agentKey{project: row.Card.ProjectName, agent: row.Card.AgentName}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], row)
	}
	return groups
}

func sortByEnvironmentOrder(rows []models.A2ACatalogRow, order []string) []models.A2ACatalogRow {
	position := make(map[string]int, len(order))
	for i, name := range order {
		position[name] = i
	}
	sorted := append([]models.A2ACatalogRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Card.EnvironmentName, sorted[j].Card.EnvironmentName
		pa, inA := position[a]
		pb, inB := position[b]
		switch {
		case inA && inB:
			return pa < pb
		case inA != inB:
			return inA
		default:
			return a < b
		}
	})
	return sorted
}

func (s *a2aCatalogService) buildEntry(rows []models.A2ACatalogRow) models.A2ACatalogEntry {
	lead := rows[0]
	entry := models.A2ACatalogEntry{
		ProjectName:     lead.Card.ProjectName,
		AgentName:       lead.Card.AgentName,
		Kind:            lead.Card.Source,
		LeadEnvironment: lead.Card.EnvironmentName,
		Name:            lead.Card.AgentName,
		Environments:    make([]models.A2ACatalogEnvironment, 0, len(rows)),
	}
	if summarySource := firstWithCard(rows); summarySource != nil {
		summary := summarizeA2ACard(summarySource.Card.Card)
		entry.Description = summary.Description
		entry.Version = summary.Version
		entry.Skills = summary.Skills
		if summary.Name != "" {
			entry.Name = summary.Name
		}
	}
	leadFingerprint := s.fingerprint(lead)
	for _, row := range rows {
		fingerprint := s.fingerprint(row)
		entry.Environments = append(entry.Environments, models.A2ACatalogEnvironment{
			Name:        row.Card.EnvironmentName,
			Status:      row.Card.Status,
			FetchedAt:   row.Card.FetchedAt,
			Proxied:     row.Proxied,
			CardDiffers: leadFingerprint != "" && fingerprint != "" && fingerprint != leadFingerprint,
			Matched:     row.Matched,
		})
	}
	return entry
}

func firstWithCard(rows []models.A2ACatalogRow) *models.A2ACatalogRow {
	for i := range rows {
		if rows[i].Card.Card != nil {
			return &rows[i]
		}
	}
	return nil
}

// fingerprint is empty for a pending card and for one that cannot be hashed, so neither is ever flagged as differing.
func (s *a2aCatalogService) fingerprint(row models.A2ACatalogRow) string {
	fingerprint, err := a2aCardFingerprint(row.Card.Card)
	if err != nil {
		s.logger.Debug("a2a catalogue: card fingerprint failed",
			"project", row.Card.ProjectName, "agent", row.Card.AgentName, "environment", row.Card.EnvironmentName, "error", err)
		return ""
	}
	return fingerprint
}

func (s *a2aCatalogService) environmentOrder(ctx context.Context, ouID, project string) []string {
	key := ouID + "/" + project
	if order, ok := s.cachedOrder(key); ok {
		return order
	}
	pipeline, err := s.ocClient.GetProjectDeploymentPipeline(ctx, ouID, project)
	if err != nil || pipeline == nil {
		s.logger.Warn("a2a catalogue: deployment pipeline unavailable, ordering environments by name",
			"project", project, "error", err)
		return nil
	}
	order := client.BuildEnvironmentOrder(pipeline.PromotionPaths)
	s.pipelineMu.Lock()
	s.pipelineCache[key] = pipelineOrder{order: order, expires: s.now().Add(a2aCatalogPipelineTTL)}
	s.pipelineMu.Unlock()
	return order
}

func (s *a2aCatalogService) cachedOrder(key string) ([]string, bool) {
	s.pipelineMu.Lock()
	defer s.pipelineMu.Unlock()
	cached, ok := s.pipelineCache[key]
	if !ok || !s.now().Before(cached.expires) {
		return nil, false
	}
	return cached.order, true
}
