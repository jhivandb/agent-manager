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
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type catalogHarness struct {
	svc      *a2aCatalogService
	repo     *repomocks.A2ACatalogRepositoryMock
	oc       *clientmocks.OpenChoreoClientMock
	pipeline map[string]*models.DeploymentPipelineResponse
	calls    map[string]int
	clock    time.Time
}

func newCatalogHarness(rows ...models.A2ACatalogRow) *catalogHarness {
	h := &catalogHarness{
		pipeline: map[string]*models.DeploymentPipelineResponse{},
		calls:    map[string]int{},
		clock:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h.repo = &repomocks.A2ACatalogRepositoryMock{
		ListAgentsFunc: func(context.Context, string, models.A2ACatalogFilter) ([]models.A2ACatalogRow, int64, error) {
			return rows, int64(len(rows)), nil
		},
	}
	h.oc = &clientmocks.OpenChoreoClientMock{
		GetProjectDeploymentPipelineFunc: func(_ context.Context, _, project string) (*models.DeploymentPipelineResponse, error) {
			h.calls[project]++
			return h.pipeline[project], nil
		},
	}
	h.svc = NewA2ACatalogService(h.repo, nil, nil, nil, nil, nil, h.oc, slog.Default()).(*a2aCatalogService)
	h.svc.now = func() time.Time { return h.clock }
	return h
}

func pipelineOf(envs ...string) *models.DeploymentPipelineResponse {
	var paths []models.PromotionPath
	for i := 0; i+1 < len(envs); i++ {
		paths = append(paths, models.PromotionPath{
			SourceEnvironmentRef:  envs[i],
			TargetEnvironmentRefs: []models.TargetEnvironmentRef{{Name: envs[i+1]}},
		})
	}
	return &models.DeploymentPipelineResponse{PromotionPaths: paths}
}

func catalogRow(project, agent, env, card string) models.A2ACatalogRow {
	row := models.A2ACatalogRow{Card: models.A2AAgentCard{
		ProjectName: project, AgentName: agent, EnvironmentName: env, Source: models.A2AAgentCardSourcePlatform,
		Status: models.A2AAgentCardStatusPending,
	}}
	if card != "" {
		row.Card.Card = json.RawMessage(card)
		row.Card.Status = models.A2AAgentCardStatusFetched
	}
	return row
}

func envNames(entry models.A2ACatalogEntry) []string {
	names := make([]string, 0, len(entry.Environments))
	for _, env := range entry.Environments {
		names = append(names, env.Name)
	}
	return names
}

func list(t *testing.T, h *catalogHarness) *models.A2ACatalogPage {
	t.Helper()
	page, err := h.svc.ListA2AAgents(context.Background(), "ou", models.A2ACatalogFilter{})
	require.NoError(t, err)
	return page
}

func TestListA2AAgents_OrdersByPipelineAndLeadsWithFirst(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p", "a", "prod", `{"name":"Prod Name"}`),
		catalogRow("p", "a", "dev", `{"name":"Dev Name","description":"d","version":"1","skills":[{"name":"s"}]}`),
		catalogRow("p", "a", "staging", `{"name":"Staging Name"}`),
	)
	h.pipeline["p"] = pipelineOf("dev", "staging", "prod")

	page := list(t, h)

	require.Len(t, page.Entries, 1)
	entry := page.Entries[0]
	assert.Equal(t, []string{"dev", "staging", "prod"}, envNames(entry))
	assert.Equal(t, "dev", entry.LeadEnvironment)
	assert.Equal(t, "Dev Name", entry.Name)
	assert.Equal(t, "d", entry.Description)
	assert.Equal(t, "1", entry.Version)
	assert.Equal(t, []string{"s"}, entry.Skills)
	assert.Equal(t, models.A2AAgentCardSourcePlatform, entry.Kind)
}

func TestListA2AAgents_FlagsDifferingCardsIgnoringURLs(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p", "a", "dev", `{"name":"n","supportedInterfaces":[{"url":"http://dev"}]}`),
		catalogRow("p", "a", "staging", `{"name":"n","supportedInterfaces":[{"url":"http://staging"}]}`),
		catalogRow("p", "a", "prod", `{"name":"n","supportedInterfaces":[{"url":"http://prod"}],"skills":[{"name":"x"}]}`),
	)
	h.pipeline["p"] = pipelineOf("dev", "staging", "prod")

	envs := list(t, h).Entries[0].Environments

	assert.False(t, envs[0].CardDiffers)
	assert.False(t, envs[1].CardDiffers)
	assert.True(t, envs[2].CardDiffers)
}

func TestListA2AAgents_PendingLeadFallsBack(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p", "a", "dev", ""),
		catalogRow("p", "a", "prod", `{"name":"Prod Name"}`),
	)
	h.pipeline["p"] = pipelineOf("dev", "prod")

	entry := list(t, h).Entries[0]

	assert.Equal(t, "Prod Name", entry.Name)
	assert.False(t, entry.Environments[0].CardDiffers)
	assert.False(t, entry.Environments[1].CardDiffers)
}

func TestListA2AAgents_AllPendingUsesAgentName(t *testing.T) {
	h := newCatalogHarness(catalogRow("p", "agent-x", "dev", ""), catalogRow("p", "agent-x", "prod", ""))
	h.pipeline["p"] = pipelineOf("dev", "prod")

	entry := list(t, h).Entries[0]

	assert.Equal(t, "agent-x", entry.Name)
	assert.Empty(t, entry.Description)
	assert.Empty(t, entry.Skills)
}

func TestListA2AAgents_UnfingerprintableCardIsNeverFlagged(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p", "a", "dev", `{"name":"n"}`),
		catalogRow("p", "a", "prod", `not json`),
	)
	h.pipeline["p"] = pipelineOf("dev", "prod")

	page := list(t, h)

	assert.False(t, page.Entries[0].Environments[1].CardDiffers)
}

func TestListA2AAgents_PipelineErrorFallsBackToNameOrder(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p", "a", "staging", `{"name":"n"}`),
		catalogRow("p", "a", "prod", `{"name":"n"}`),
		catalogRow("p", "a", "dev", `{"name":"n"}`),
	)
	h.oc.GetProjectDeploymentPipelineFunc = func(context.Context, string, string) (*models.DeploymentPipelineResponse, error) {
		return nil, errors.New("boom")
	}

	page := list(t, h)

	require.Len(t, page.Entries, 1)
	assert.Equal(t, []string{"dev", "prod", "staging"}, envNames(page.Entries[0]))
}

func TestListA2AAgents_EnvironmentsOutsidePipelineGoLastByName(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p", "a", "zeta", `{"name":"n"}`),
		catalogRow("p", "a", "alpha", `{"name":"n"}`),
		catalogRow("p", "a", "prod", `{"name":"n"}`),
		catalogRow("p", "a", "dev", `{"name":"n"}`),
	)
	h.pipeline["p"] = pipelineOf("dev", "prod")

	assert.Equal(t, []string{"dev", "prod", "alpha", "zeta"}, envNames(list(t, h).Entries[0]))
}

func TestListA2AAgents_FetchesPipelineOncePerProject(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p1", "a", "dev", `{}`),
		catalogRow("p1", "b", "dev", `{}`),
		catalogRow("p2", "c", "dev", `{}`),
	)
	h.pipeline["p1"] = pipelineOf("dev")
	h.pipeline["p2"] = pipelineOf("dev")

	page := list(t, h)

	assert.Len(t, page.Entries, 3)
	assert.Equal(t, 1, h.calls["p1"])
	assert.Equal(t, 1, h.calls["p2"])
}

func TestListA2AAgents_CachesPipelineOrderWithinTTL(t *testing.T) {
	h := newCatalogHarness(catalogRow("p", "a", "dev", `{}`))
	h.pipeline["p"] = pipelineOf("dev")

	list(t, h)
	h.clock = h.clock.Add(10 * time.Second)
	list(t, h)
	assert.Equal(t, 1, h.calls["p"])

	h.clock = h.clock.Add(a2aCatalogPipelineTTL)
	list(t, h)
	assert.Equal(t, 2, h.calls["p"])
}

func TestListA2AAgents_DoesNotCachePipelineFailures(t *testing.T) {
	h := newCatalogHarness(catalogRow("p", "a", "dev", `{}`))
	calls := 0
	h.oc.GetProjectDeploymentPipelineFunc = func(context.Context, string, string) (*models.DeploymentPipelineResponse, error) {
		calls++
		return nil, errors.New("boom")
	}

	list(t, h)
	list(t, h)

	assert.Equal(t, 2, calls)
}

func TestListA2AAgents_FailingPipelineIsFetchedOncePerProjectWithinARequest(t *testing.T) {
	h := newCatalogHarness(
		catalogRow("p", "a", "dev", `{}`),
		catalogRow("p", "b", "dev", `{}`),
	)
	calls := 0
	h.oc.GetProjectDeploymentPipelineFunc = func(context.Context, string, string) (*models.DeploymentPipelineResponse, error) {
		calls++
		return nil, errors.New("boom")
	}

	page := list(t, h)

	assert.Len(t, page.Entries, 2)
	assert.Equal(t, 1, calls)
}

func TestListA2AAgents_PassesThroughProxiedAndMatched(t *testing.T) {
	row := catalogRow("p", "a", "dev", `{}`)
	row.Proxied = true
	row.Matched = true
	h := newCatalogHarness(row)
	h.pipeline["p"] = pipelineOf("dev")
	h.repo.ListAgentsFunc = func(context.Context, string, models.A2ACatalogFilter) ([]models.A2ACatalogRow, int64, error) {
		return []models.A2ACatalogRow{row}, 42, nil
	}

	page, err := h.svc.ListA2AAgents(context.Background(), "ou", models.A2ACatalogFilter{Limit: 10, Offset: 20})

	require.NoError(t, err)
	assert.Equal(t, int64(42), page.Total)
	assert.Equal(t, 10, page.Limit)
	assert.Equal(t, 20, page.Offset)
	env := page.Entries[0].Environments[0]
	assert.True(t, env.Proxied)
	assert.True(t, env.Matched)
}

func TestListA2AAgents_RepoErrorIsReturnedWrapped(t *testing.T) {
	repoErr := errors.New("db down")
	h := newCatalogHarness()
	h.repo.ListAgentsFunc = func(context.Context, string, models.A2ACatalogFilter) ([]models.A2ACatalogRow, int64, error) {
		return nil, 0, repoErr
	}

	_, err := h.svc.ListA2AAgents(context.Background(), "ou", models.A2ACatalogFilter{})

	assert.ErrorIs(t, err, repoErr)
}

type fakeCardService struct {
	A2AAgentCardService
	getFunc func(ctx context.Context, ouID, project, agent, env string) (*models.A2AAgentCard, error)
}

func (f *fakeCardService) GetA2AAgentCard(ctx context.Context, ouID, project, agent, env string) (*models.A2AAgentCard, error) {
	return f.getFunc(ctx, ouID, project, agent, env)
}

type fakeProxyService struct {
	A2AProxyService
	getFunc func(ctx context.Context, ouID, project, agent, env string) (*models.A2AProxyStatus, error)
}

func (f *fakeProxyService) GetA2AProxy(ctx context.Context, ouID, project, agent, env string) (*models.A2AProxyStatus, error) {
	return f.getFunc(ctx, ouID, project, agent, env)
}

const connectEnvUUID = "55555555-5555-5555-5555-555555555555"

type connectHarness struct {
	svc         *a2aCatalogService
	card        *models.A2AAgentCard
	cardErr     error
	proxyStatus *models.A2AProxyStatus
	proxyErr    error
	env         *models.EnvironmentResponse
	envErr      error
	configs     *repomocks.AgentConfigRepositoryMock
	pubs        *repomocks.A2APublicationRepositoryMock
	gateways    *repomocks.GatewayRepositoryMock
	providers   []models.GatewayIdentityProvider
	providerErr error
}

func newConnectHarness(source models.A2AAgentCardSource) *connectHarness {
	h := &connectHarness{
		card: &models.A2AAgentCard{Source: source, EnvironmentUUID: uuid.MustParse(connectEnvUUID)},
		env:  &models.EnvironmentResponse{Name: "dev"},
	}
	h.configs = &repomocks.AgentConfigRepositoryMock{}
	h.pubs = &repomocks.A2APublicationRepositoryMock{
		GetFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			return &models.A2APublication{Status: models.A2APublicationStatusPublished}, nil
		},
	}
	h.gateways = &repomocks.GatewayRepositoryMock{
		ListWithFiltersFunc: func(repositories.GatewayFilterOptions) ([]*models.Gateway, error) {
			return []*models.Gateway{{Vhost: "gw.example.com"}}, nil
		},
		ListIdentityProvidersByEnvironmentFunc: func(string) ([]models.GatewayIdentityProvider, error) {
			return h.providers, h.providerErr
		},
	}
	publisher := NewA2APublisher(nil, h.pubs, nil, nil, nil, nil, h.gateways, nil, slog.Default())
	cards := &fakeCardService{getFunc: func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return h.card, h.cardErr
	}}
	proxies := &fakeProxyService{getFunc: func(context.Context, string, string, string, string) (*models.A2AProxyStatus, error) {
		return h.proxyStatus, h.proxyErr
	}}
	oc := &clientmocks.OpenChoreoClientMock{
		GetEnvironmentFunc: func(context.Context, string, string) (*models.EnvironmentResponse, error) {
			return h.env, h.envErr
		},
	}
	h.svc = NewA2ACatalogService(nil, cards, proxies, publisher, h.configs, h.gateways, oc, slog.Default()).(*a2aCatalogService)
	return h
}

func (h *connectHarness) withConfig(cfg models.AgentConfig) {
	h.configs.GetFunc = func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
		return &cfg, nil
	}
}

func (h *connectHarness) connect() (*models.A2AConnectInfo, error) {
	return h.svc.GetA2AConnect(context.Background(), "ou", "p", "weather", "dev")
}

func platformGatewayURL() string {
	return a2aGatewayURL(&models.Gateway{Vhost: "gw.example.com"}, "weather")
}

func TestGetA2AConnect_PlatformAPIKey(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.withConfig(models.AgentConfig{EnableApiKeySecurity: true})

	info, err := h.connect()

	require.NoError(t, err)
	assert.Equal(t, models.A2AAgentCardSourcePlatform, info.Kind)
	assert.False(t, info.Proxied)
	assert.Equal(t, platformGatewayURL(), info.GatewayURL)
	assert.Equal(t, models.A2AConnectSecurity{Mode: models.A2ASecurityModeAPIKey, APIKeyHeader: "X-API-Key"}, info.Security)
	assert.Equal(t, models.A2AConnectPublication{Status: "published"}, info.Publication)
}

func TestGetA2AConnect_PlatformPublicationErrorIsCopied(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.withConfig(models.AgentConfig{})
	h.pubs.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return &models.A2APublication{Status: models.A2APublicationStatusFailed, LastError: "boom"}, nil
	}

	info, err := h.connect()

	require.NoError(t, err)
	assert.Equal(t, models.A2AConnectPublication{Status: "failed", LastError: "boom"}, info.Publication)
}

func TestGetA2AConnect_OAuthResolvesIssuers(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.withConfig(models.AgentConfig{EnableOAuthSecurity: true, OAuthIssuers: []string{"thunder", "gone"}})
	h.providers = []models.GatewayIdentityProvider{{Name: "thunder", Issuer: "https://idp/x"}}

	info, err := h.connect()

	require.NoError(t, err)
	assert.Equal(t, models.A2ASecurityModeOAuth, info.Security.Mode)
	assert.Equal(t, []models.A2AConnectIssuer{{Name: "thunder", Issuer: "https://idp/x"}, {Name: "gone"}}, info.Security.Issuers)
	require.NotNil(t, info.Security.OAuth)
	assert.Equal(t, "Authorization", info.Security.OAuth.HeaderName)
	assert.Equal(t, "Bearer", info.Security.OAuth.AuthHeaderPrefix)
}

func TestGetA2AConnect_NoSecurity(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.withConfig(models.AgentConfig{})

	info, err := h.connect()

	require.NoError(t, err)
	assert.Equal(t, models.A2ASecurityModeNone, info.Security.Mode)
}

func TestGetA2AConnect_DirectExternalIsUnmanaged(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourceExternal)
	h.proxyErr = utils.ErrEnvironmentNotProxied

	info, err := h.connect()

	require.NoError(t, err)
	assert.Equal(t, models.A2AAgentCardSourceExternal, info.Kind)
	assert.False(t, info.Proxied)
	assert.Empty(t, info.GatewayURL)
	assert.Equal(t, models.A2ASecurityModeUnmanaged, info.Security.Mode)
	assert.Equal(t, models.A2AConnectPublication{Status: models.A2AConnectPublicationNone}, info.Publication)
}

func TestGetA2AConnect_ProxiedExternalUsesProxyStatus(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourceExternal)
	h.proxyStatus = &models.A2AProxyStatus{Status: models.A2APublicationStatusPublished, GatewayURL: "https://gw/agents/x"}
	h.withConfig(models.AgentConfig{EnableApiKeySecurity: true})

	info, err := h.connect()

	require.NoError(t, err)
	assert.True(t, info.Proxied)
	assert.Equal(t, "https://gw/agents/x", info.GatewayURL)
	assert.Equal(t, models.A2AConnectPublication{Status: "published"}, info.Publication)
	assert.Equal(t, models.A2ASecurityModeAPIKey, info.Security.Mode)
}

func TestGetA2AConnect_MissingConfigIsUnknown(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.configs.GetFunc = func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
		return nil, repositories.ErrAgentConfigNotFound
	}

	info, err := h.connect()

	require.NoError(t, err)
	assert.Equal(t, models.A2ASecurityModeUnknown, info.Security.Mode)
}

func TestGetA2AConnect_PlatformMissingPublicationIsFailed(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.withConfig(models.AgentConfig{})
	h.pubs.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return nil, repositories.ErrA2APublicationNotFound
	}

	info, err := h.connect()

	require.NoError(t, err)
	assert.Equal(t, models.A2AConnectPublication{Status: "failed", LastError: a2aProxyNoPublicationMsg}, info.Publication)
}

func TestGetA2AConnect_ProductionFlag(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.withConfig(models.AgentConfig{})
	h.env = &models.EnvironmentResponse{Name: "prod", IsProduction: true}

	info, err := h.connect()

	require.NoError(t, err)
	assert.True(t, info.IsProduction)
}

func TestGetA2AConnect_NoCardRowIs404(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.cardErr = utils.ErrAgentCardNotFound

	_, err := h.connect()

	require.ErrorIs(t, err, utils.ErrAgentCardNotFound)
}

func TestGetA2AConnect_UnknownEnvironmentIsTranslated(t *testing.T) {
	h := newConnectHarness(models.A2AAgentCardSourcePlatform)
	h.envErr = utils.ErrNotFound

	_, err := h.connect()

	require.ErrorIs(t, err, utils.ErrEnvironmentNotFound)
}

func TestGetA2AConnect_DoesNotMaskRealErrors(t *testing.T) {
	dbErr := errors.New("db down")
	t.Run("config read", func(t *testing.T) {
		h := newConnectHarness(models.A2AAgentCardSourcePlatform)
		h.configs.GetFunc = func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
			return nil, dbErr
		}
		info, err := h.connect()
		require.ErrorIs(t, err, dbErr)
		assert.NotErrorIs(t, err, utils.ErrAgentCardNotFound)
		assert.Nil(t, info)
	})
	t.Run("proxy read", func(t *testing.T) {
		h := newConnectHarness(models.A2AAgentCardSourceExternal)
		h.proxyErr = dbErr
		info, err := h.connect()
		require.ErrorIs(t, err, dbErr)
		assert.Nil(t, info)
	})
	t.Run("publication read", func(t *testing.T) {
		h := newConnectHarness(models.A2AAgentCardSourcePlatform)
		h.pubs.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
			return nil, dbErr
		}
		_, err := h.connect()
		require.ErrorIs(t, err, dbErr)
	})
	t.Run("identity providers", func(t *testing.T) {
		h := newConnectHarness(models.A2AAgentCardSourcePlatform)
		h.withConfig(models.AgentConfig{EnableOAuthSecurity: true, OAuthIssuers: []string{"x"}})
		h.providerErr = dbErr
		_, err := h.connect()
		require.ErrorIs(t, err, dbErr)
	})
}
