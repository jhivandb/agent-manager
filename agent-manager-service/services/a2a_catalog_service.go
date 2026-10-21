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
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// a2aCatalogPipelineFetches bounds the concurrent pipeline reads one catalogue page makes.
const a2aCatalogPipelineFetches = 8

type A2ACatalogService interface {
	ListA2AAgents(ctx context.Context, ouID string, f models.A2ACatalogFilter) (*models.A2ACatalogPage, error)
	GetA2ACatalogEntry(ctx context.Context, ouID, projectName, agentName string) (*models.A2ACatalogEntry, error)
	GetA2AConnect(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AConnectInfo, error)
}

type a2aCatalogService struct {
	catalogRepo     repositories.A2ACatalogRepository
	cardService     A2AAgentCardService
	proxyRepo       repositories.A2AProxyRepository
	publisher       *A2APublisher
	agentConfigRepo repositories.AgentConfigRepository
	gatewayRepo     repositories.GatewayRepository
	ocClient        client.OpenChoreoClient
	logger          *slog.Logger
}

func NewA2ACatalogService(
	catalogRepo repositories.A2ACatalogRepository,
	cardService A2AAgentCardService,
	proxyRepo repositories.A2AProxyRepository,
	publisher *A2APublisher,
	agentConfigRepo repositories.AgentConfigRepository,
	gatewayRepo repositories.GatewayRepository,
	ocClient client.OpenChoreoClient,
	logger *slog.Logger,
) A2ACatalogService {
	return &a2aCatalogService{
		catalogRepo:     catalogRepo,
		cardService:     cardService,
		proxyRepo:       proxyRepo,
		publisher:       publisher,
		agentConfigRepo: agentConfigRepo,
		gatewayRepo:     gatewayRepo,
		ocClient:        ocClient,
		logger:          logger,
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
	groups := groupByAgent(rows)
	orders := s.environmentOrders(ctx, ouID, groups)
	entries := make([]models.A2ACatalogEntry, 0, len(groups))
	for _, group := range groups {
		entries = append(entries, s.buildEntry(sortByEnvironmentOrder(group, orders[group[0].Card.ProjectName])))
	}
	return &models.A2ACatalogPage{Entries: entries, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

func (s *a2aCatalogService) GetA2ACatalogEntry(ctx context.Context, ouID, projectName, agentName string) (*models.A2ACatalogEntry, error) {
	rows, err := s.catalogRepo.GetAgent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, fmt.Errorf("get a2a catalogue agent: %w", err)
	}
	if len(rows) == 0 {
		return nil, utils.ErrA2ACatalogEntryNotFound
	}
	entry := s.buildEntry(sortByEnvironmentOrder(rows, s.environmentOrder(ctx, ouID, projectName)))
	return &entry, nil
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
		if info.Proxied, err = s.isProxied(ctx, ref); err != nil {
			return nil, err
		}
		if !info.Proxied {
			info.Security.Mode = models.A2ASecurityModeUnmanaged
			info.Publication.Status = models.A2AConnectPublicationNone
			info.CardURL, info.JSONRPCURL = connectURLs("", card)
			return info, nil
		}
	}
	if err := s.applyGateway(ctx, info, ref); err != nil {
		return nil, err
	}
	info.CardURL, info.JSONRPCURL = connectURLs(info.GatewayURL, card)
	if info.Security, err = s.connectSecurity(ctx, ref); err != nil {
		return nil, err
	}
	return info, nil
}

// isProxied is false while the external agent is reached directly.
func (s *a2aCatalogService) isProxied(ctx context.Context, ref a2aEnvRef) (bool, error) {
	_, err := s.proxyRepo.GetByEnvUUID(ctx, ref.ouID, ref.project, ref.agent, ref.envUUID)
	if errors.Is(err, repositories.ErrA2AProxyNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return true, nil
}

// applyGateway fills in where the environment's gateway serves the agent and how far its publication got.
func (s *a2aCatalogService) applyGateway(ctx context.Context, info *models.A2AConnectInfo, ref a2aEnvRef) error {
	source := models.A2APublicationSourcePlatform
	if info.Kind == models.A2AAgentCardSourceExternal {
		source = models.A2APublicationSourceExternal
	}
	gateway, err := s.publisher.GatewayStatus(ctx, ref.ouID, ref.project, ref.agent, ref.envUUID, source)
	if err != nil {
		return err
	}
	info.GatewayURL = gateway.URL
	info.Publication = models.A2AConnectPublication{Status: string(gateway.Status), LastError: gateway.LastError}
	return nil
}

// connectURLs prefers the gateway's card and JSON-RPC endpoint; without one, the card's own.
// A gateway-fetched card (proxied external agent) already declares the gateway's JSON-RPC URL.
func connectURLs(gatewayURL string, card *models.A2AAgentCard) (cardURL, jsonRPCURL string) {
	declared := declaredJSONRPCURLs(card.Card)
	if gatewayURL == "" {
		if len(declared) > 0 {
			jsonRPCURL = declared[0]
		}
		return card.SourceURL, jsonRPCURL
	}
	base := strings.TrimRight(gatewayURL, "/")
	for _, link := range declared {
		if link == base || strings.HasPrefix(link, base+"/") {
			return base + a2aAgentCardPath, link
		}
	}
	return base + a2aAgentCardPath, base + a2aPathPrefixJSONRPC
}

// declaredJSONRPCURLs reads the card's JSON-RPC interfaces leniently; a malformed card declares none.
func declaredJSONRPCURLs(card json.RawMessage) []string {
	var doc struct {
		SupportedInterfaces []struct {
			URL             string `json:"url"`
			ProtocolBinding string `json:"protocolBinding"`
		} `json:"supportedInterfaces"`
	}
	if len(card) == 0 || json.Unmarshal(card, &doc) != nil {
		return nil
	}
	var urls []string
	for _, iface := range doc.SupportedInterfaces {
		if iface.ProtocolBinding == a2aTransportJSONRPC && iface.URL != "" {
			urls = append(urls, iface.URL)
		}
	}
	return urls
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
		return models.A2AConnectSecurity{Mode: models.A2ASecurityModeAPIKey, APIKeyHeader: client.APIKeyHeader}, nil
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
	fingerprints := make([]string, len(rows))
	for i, row := range rows {
		fingerprints[i] = s.fingerprint(row)
	}
	leadFingerprint := fingerprints[0]
	for i, row := range rows {
		fingerprint := fingerprints[i]
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

// environmentOrders reads each project's pipeline once, concurrently.
func (s *a2aCatalogService) environmentOrders(ctx context.Context, ouID string, groups [][]models.A2ACatalogRow) map[string][]string {
	var projects []string
	seen := make(map[string]bool)
	for _, group := range groups {
		if project := group[0].Card.ProjectName; !seen[project] {
			seen[project] = true
			projects = append(projects, project)
		}
	}
	orders := make(map[string][]string, len(projects))
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(a2aCatalogPipelineFetches)
	for _, project := range projects {
		g.Go(func() error {
			order := s.environmentOrder(ctx, ouID, project)
			mu.Lock()
			orders[project] = order
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // Never fails: an unavailable pipeline falls back to name order.
	return orders
}

// environmentOrder is nil when the pipeline is unavailable, which orders the environments by name.
func (s *a2aCatalogService) environmentOrder(ctx context.Context, ouID, project string) []string {
	pipeline, err := s.ocClient.GetProjectDeploymentPipeline(ctx, ouID, project)
	if err != nil || pipeline == nil {
		s.logger.Warn("a2a catalogue: deployment pipeline unavailable, ordering environments by name",
			"project", project, "error", err)
		return nil
	}
	return client.BuildEnvironmentOrder(pipeline.PromotionPaths)
}
