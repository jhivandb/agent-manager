//go:build integration

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

package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

const (
	catalogDev  = "dev"
	catalogProd = "prod"
)

type catalogFixture struct {
	t    *testing.T
	ouID string
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	f := &catalogFixture{t: t, ouID: "ou-" + uuid.New().String()[:8]}
	t.Cleanup(func() {
		require.NoError(t, db.GetDB().Where("ou_id = ?", f.ouID).Delete(&models.A2AProxy{}).Error)
		require.NoError(t, db.GetDB().Where("ou_id = ?", f.ouID).Delete(&models.A2AAgentCard{}).Error)
	})
	return f
}

func (f *catalogFixture) addCard(project, agent, env string, source models.A2AAgentCardSource, card string) {
	f.t.Helper()
	row := &models.A2AAgentCard{
		OUID: f.ouID, ProjectName: project, AgentName: agent,
		EnvironmentName: env, EnvironmentUUID: envUUIDOf(env),
		Source: source, Status: models.A2AAgentCardStatusFetched,
	}
	if card == "" {
		row.Status = models.A2AAgentCardStatusPending
	} else {
		row.Card = json.RawMessage(card)
	}
	require.NoError(f.t, db.GetDB().Create(row).Error)
}

func (f *catalogFixture) addProxy(project, agent, env string) {
	f.t.Helper()
	require.NoError(f.t, db.GetDB().Create(&models.A2AProxy{
		OUID: f.ouID, ProjectName: project, AgentName: agent,
		EnvironmentUUID: envUUIDOf(env), EnvironmentName: env,
	}).Error)
}

func (f *catalogFixture) list(filter models.A2ACatalogFilter) ([]models.A2ACatalogRow, int64) {
	f.t.Helper()
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	rows, total, err := NewA2ACatalogRepository(db.GetDB()).ListAgents(context.Background(), f.ouID, filter)
	require.NoError(f.t, err)
	return rows, total
}

func namedCard(name string) string { return fmt.Sprintf(`{"name":%q,"skills":[]}`, name) }

func rowKeys(rows []models.A2ACatalogRow) []string {
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.Card.ProjectName+"/"+r.Card.AgentName+"/"+r.Card.EnvironmentName)
	}
	return keys
}

func TestA2ACatalogRepository_GroupsAndPaginatesByAgent(t *testing.T) {
	f := newCatalogFixture(t)
	for _, env := range []string{catalogDev, catalogProd} {
		f.addCard("p", "a", env, models.A2AAgentCardSourcePlatform, namedCard("a"))
	}
	f.addCard("p", "b", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("b"))
	f.addCard("p", "c", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("c"))

	rows, total := f.list(models.A2ACatalogFilter{Limit: 2})
	assert.Equal(t, int64(3), total)
	assert.Equal(t, []string{"p/a/dev", "p/a/prod", "p/b/dev"}, rowKeys(rows))

	rows, total = f.list(models.A2ACatalogFilter{Limit: 2, Offset: 2})
	assert.Equal(t, int64(3), total)
	assert.Equal(t, []string{"p/c/dev"}, rowKeys(rows))
}

func TestA2ACatalogRepository_CountsPastTheLastPage(t *testing.T) {
	f := newCatalogFixture(t)
	f.addCard("p", "a", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("a"))

	rows, total := f.list(models.A2ACatalogFilter{Limit: 2, Offset: 4})
	assert.Equal(t, int64(1), total)
	assert.Empty(t, rows)
}

func TestA2ACatalogRepository_GetAgent(t *testing.T) {
	f := newCatalogFixture(t)
	for _, env := range []string{catalogDev, catalogProd} {
		f.addCard("p1", "orders", env, models.A2AAgentCardSourceExternal, namedCard("orders"))
	}
	f.addCard("p2", "orders", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("orders"))
	f.addProxy("p1", "orders", catalogProd)
	repo := NewA2ACatalogRepository(db.GetDB())

	rows, err := repo.GetAgent(context.Background(), f.ouID, "p1", "orders")
	require.NoError(t, err)
	assert.Equal(t, []string{"p1/orders/dev", "p1/orders/prod"}, rowKeys(rows))
	assert.False(t, rows[0].Proxied)
	assert.True(t, rows[1].Proxied)

	rows, err = repo.GetAgent(context.Background(), f.ouID, "p1", "missing")
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestA2ACatalogRepository_SameAgentNameInTwoProjects(t *testing.T) {
	f := newCatalogFixture(t)
	f.addCard("p1", "orders", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("orders"))
	f.addCard("p2", "orders", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("orders"))

	rows, total := f.list(models.A2ACatalogFilter{})
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)
}

func TestA2ACatalogRepository_SearchMatchesSkillsAcrossEnvironments(t *testing.T) {
	f := newCatalogFixture(t)
	f.addCard("p", "a", catalogDev, models.A2AAgentCardSourcePlatform,
		`{"name":"a","skills":[{"name":"order-status","description":"Track an order"}]}`)
	f.addCard("p", "a", catalogProd, models.A2AAgentCardSourcePlatform,
		`{"name":"a","skills":[{"name":"order-status"},{"name":"issue-refund","description":"Refund an order"}]}`)
	f.addCard("p", "other", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("other"))

	rows, total := f.list(models.A2ACatalogFilter{Query: "refund"})
	assert.Equal(t, int64(1), total)
	require.Equal(t, []string{"p/a/dev", "p/a/prod"}, rowKeys(rows))
	assert.False(t, rows[0].Matched)
	assert.True(t, rows[1].Matched)
}

func TestA2ACatalogRepository_SearchEscapesWildcards(t *testing.T) {
	f := newCatalogFixture(t)
	f.addCard("p", "ledger", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("Ledger"))
	f.addCard("p", "uptime", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("100% uptime bot"))

	rows, total := f.list(models.A2ACatalogFilter{Query: "%"})
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"p/uptime/dev"}, rowKeys(rows))

	rows, total = f.list(models.A2ACatalogFilter{Query: "_"})
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)
}

func TestA2ACatalogRepository_PendingNullCard(t *testing.T) {
	f := newCatalogFixture(t)
	f.addCard("p", "fresh", catalogDev, models.A2AAgentCardSourcePlatform, "")

	rows, total := f.list(models.A2ACatalogFilter{})
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].Matched)

	rows, total = f.list(models.A2ACatalogFilter{Query: "x"})
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)
}

func TestA2ACatalogRepository_KindAndEnvFilters(t *testing.T) {
	f := newCatalogFixture(t)
	for _, env := range []string{catalogDev, catalogProd} {
		f.addCard("p", "ext", env, models.A2AAgentCardSourceExternal, namedCard("ext"))
	}
	f.addProxy("p", "ext", catalogProd)

	rows, total := f.list(models.A2ACatalogFilter{Kind: models.A2ACatalogKindFilterProxied})
	assert.Equal(t, int64(1), total)
	require.Equal(t, []string{"p/ext/dev", "p/ext/prod"}, rowKeys(rows))
	assert.False(t, rows[0].Proxied)
	assert.True(t, rows[1].Proxied)

	_, total = f.list(models.A2ACatalogFilter{Kind: models.A2ACatalogKindFilterPlatform})
	assert.Equal(t, int64(0), total)

	rows, total = f.list(models.A2ACatalogFilter{EnvironmentName: catalogProd})
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 2)
}

func TestA2ACatalogRepository_ScopedToOrg(t *testing.T) {
	mine := newCatalogFixture(t)
	other := newCatalogFixture(t)
	mine.addCard("p", "mine", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("mine"))
	other.addCard("p", "theirs", catalogDev, models.A2AAgentCardSourcePlatform, namedCard("theirs"))

	rows, total := mine.list(models.A2ACatalogFilter{})
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"p/mine/dev"}, rowKeys(rows))
}
