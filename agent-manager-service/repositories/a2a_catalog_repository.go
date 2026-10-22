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
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// A2ACatalogRepository reads the organisation's A2A agents, one entry per project and agent name.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/a2a_catalog_repository_mock.go . A2ACatalogRepository:A2ACatalogRepositoryMock
type A2ACatalogRepository interface {
	// List returns every environment row of the agents on the page, ordered by project, agent, environment name, and the number of matching agents.
	List(ctx context.Context, filter models.A2ACatalogFilter) ([]models.A2ACatalogRow, int, error)
	// ListForAgent returns the agent's rows in environment-name order, Matched false; no rows is not an error.
	ListForAgent(ctx context.Context, ouID, projectName, agentName string) ([]models.A2ACatalogRow, error)
}

type a2aCatalogRepository struct {
	db *gorm.DB
}

// NewA2ACatalogRepository creates an A2ACatalogRepository.
func NewA2ACatalogRepository(db *gorm.DB) A2ACatalogRepository {
	return &a2aCatalogRepository{db: db}
}

const a2aCatalogProxied = `EXISTS (SELECT 1 FROM a2a_proxies p
	WHERE p.ou_id = c.ou_id AND p.project_name = c.project_name
	AND p.agent_name = c.agent_name AND p.environment_uuid = c.environment_uuid)`

const a2aCatalogSearchMatch = `(c.agent_name ILIKE @pattern ESCAPE '\' OR c.card_search_text ILIKE @pattern ESCAPE '\')`

func searchPattern(query string) sql.NamedArg {
	return sql.Named("pattern", "%"+escapeLikePattern(query)+"%")
}

// rowCondition is what one card row must satisfy for its agent to be in the catalogue, with its named arguments.
func rowCondition(filter models.A2ACatalogFilter) (string, []any) {
	conds := []string{"c.ou_id = @ou"}
	args := []any{sql.Named("ou", filter.OUID)}
	addEquals := func(column, name, value string) {
		if value != "" {
			conds = append(conds, column+" = @"+name)
			args = append(args, sql.Named(name, value))
		}
	}
	addEquals("c.project_name", "project", filter.Project)
	addEquals("c.environment_name", "env", filter.Environment)
	addEquals("c.status", "status", string(filter.Status))
	switch filter.Kind {
	case models.A2ACatalogKindPlatform, models.A2ACatalogKindExternal:
		addEquals("c.source", "source", string(filter.Kind))
	case models.A2ACatalogKindProxied:
		conds = append(conds, a2aCatalogProxied)
	}
	if filter.Query != "" {
		conds = append(conds, a2aCatalogSearchMatch)
		args = append(args, searchPattern(filter.Query))
	}
	return strings.Join(conds, " AND "), args
}

type a2aCatalogAgentKey struct{ ProjectName, AgentName string }

func (r *a2aCatalogRepository) List(ctx context.Context, filter models.A2ACatalogFilter) ([]models.A2ACatalogRow, int, error) {
	where, args := rowCondition(filter)

	// The window counts the matching agents before LIMIT applies, so one scan yields the page and the total.
	pageSQL := `SELECT c.project_name, c.agent_name, COUNT(*) OVER () AS total
		FROM a2a_agent_cards c WHERE ` + where + `
		GROUP BY c.project_name, c.agent_name
		ORDER BY c.project_name, c.agent_name`
	if filter.Limit > 0 {
		pageSQL += " LIMIT @limit"
		args = append(args, sql.Named("limit", filter.Limit))
	}
	pageSQL += " OFFSET @offset"
	args = append(args, sql.Named("offset", filter.Offset))

	// Flat, not embedding a2aCatalogAgentKey: gorm skips an unexported embedded field, leaving the names empty.
	var page []struct {
		ProjectName, AgentName string
		Total                  int
	}
	if err := r.db.WithContext(ctx).Raw(pageSQL, args...).Scan(&page).Error; err != nil {
		return nil, 0, fmt.Errorf("list a2a catalogue agents: %w", err)
	}
	if len(page) == 0 {
		total, err := r.countAgents(ctx, where, args)
		return []models.A2ACatalogRow{}, total, err
	}

	agents := make([]a2aCatalogAgentKey, 0, len(page))
	for _, p := range page {
		agents = append(agents, a2aCatalogAgentKey{ProjectName: p.ProjectName, AgentName: p.AgentName})
	}
	rows, err := r.rowsOfAgents(ctx, filter.OUID, agents, filter.Query)
	if err != nil {
		return nil, 0, err
	}
	return rows, page[0].Total, nil
}

// countAgents counts the matching agents for a page past the last one, which has no row to carry the total.
func (r *a2aCatalogRepository) countAgents(ctx context.Context, where string, args []any) (int, error) {
	var total int
	countSQL := `SELECT COUNT(*) FROM (
		SELECT 1 FROM a2a_agent_cards c WHERE ` + where + `
		GROUP BY c.project_name, c.agent_name
	) m`
	if err := r.db.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("count a2a catalogue agents: %w", err)
	}
	return total, nil
}

func (r *a2aCatalogRepository) ListForAgent(ctx context.Context, ouID, projectName, agentName string) ([]models.A2ACatalogRow, error) {
	return r.rowsOfAgents(ctx, ouID, []a2aCatalogAgentKey{{ProjectName: projectName, AgentName: agentName}}, "")
}

// rowsOfAgents loads every row of the given agents; a row is Matched when it satisfies the query on its own.
func (r *a2aCatalogRepository) rowsOfAgents(ctx context.Context, ouID string, agents []a2aCatalogAgentKey, query string,
) ([]models.A2ACatalogRow, error) {
	pairs := make([][]any, 0, len(agents))
	for _, a := range agents {
		pairs = append(pairs, []any{a.ProjectName, a.AgentName})
	}
	args := []any{sql.Named("ou", ouID), sql.Named("pairs", pairs)}
	matched := "false"
	if query != "" {
		matched = "COALESCE(" + a2aCatalogSearchMatch + ", false)"
		args = append(args, searchPattern(query))
	}

	rowsSQL := `SELECT c.*, ` + a2aCatalogProxied + ` AS proxied, ` + matched + ` AS matched
	FROM a2a_agent_cards c
	WHERE c.ou_id = @ou AND (c.project_name, c.agent_name) IN @pairs
	ORDER BY c.project_name, c.agent_name, c.environment_name`
	rows := []models.A2ACatalogRow{}
	if err := r.db.WithContext(ctx).Raw(rowsSQL, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load a2a catalogue rows: %w", err)
	}
	return rows, nil
}
