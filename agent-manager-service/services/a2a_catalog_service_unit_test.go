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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
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
