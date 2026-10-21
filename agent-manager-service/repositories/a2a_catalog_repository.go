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

// A2ACatalogRepository reads the org-wide A2A agent catalogue, one agent per (project, agent name).
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/a2a_catalog_repository_mock.go . A2ACatalogRepository:A2ACatalogRepositoryMock
type A2ACatalogRepository interface {
	// ListAgents returns every card row of the agents on the requested page, plus the number of matching agents.
	ListAgents(ctx context.Context, ouID string, f models.A2ACatalogFilter) ([]models.A2ACatalogRow, int64, error)
	// GetAgent returns every card row of one agent; none when it is not in the catalogue.
	GetAgent(ctx context.Context, ouID, projectName, agentName string) ([]models.A2ACatalogRow, error)
}

type a2aCatalogAgentKey struct{ ProjectName, AgentName string }

type A2ACatalogRepo struct {
	db *gorm.DB
}

func NewA2ACatalogRepository(db *gorm.DB) A2ACatalogRepository {
	return &A2ACatalogRepo{db: db}
}

const a2aCatalogProxyExists = `EXISTS (SELECT 1 FROM a2a_proxies p
	WHERE p.ou_id = c.ou_id AND p.project_name = c.project_name
	AND p.agent_name = c.agent_name AND p.environment_uuid = c.environment_uuid)`

// a2aCatalogSearchMatch is true for a row whose card, or any of its skills, contains @pattern.
// A NULL card yields NULL, which no WHERE clause accepts.
const a2aCatalogSearchMatch = `(
	c.card->>'name' ILIKE @pattern ESCAPE '\'
	OR c.card->>'description' ILIKE @pattern ESCAPE '\'
	OR EXISTS (
		SELECT 1 FROM jsonb_array_elements(
			CASE WHEN jsonb_typeof(c.card->'skills') = 'array' THEN c.card->'skills' ELSE '[]'::jsonb END
		) AS s
		WHERE s->>'name' ILIKE @pattern ESCAPE '\'
			OR s->>'description' ILIKE @pattern ESCAPE '\'
			OR (s->'tags')::text ILIKE @pattern ESCAPE '\'
			OR (s->'examples')::text ILIKE @pattern ESCAPE '\'
	)
)`

func (r *A2ACatalogRepo) ListAgents(ctx context.Context, ouID string, f models.A2ACatalogFilter) ([]models.A2ACatalogRow, int64, error) {
	args := []any{sql.Named("ou", ouID)}
	conditions := []string{"c.ou_id = @ou"}
	addCondition := func(condition string, arg sql.NamedArg) {
		conditions = append(conditions, condition)
		args = append(args, arg)
	}

	if f.ProjectName != "" {
		addCondition("c.project_name = @project", sql.Named("project", f.ProjectName))
	}
	if f.EnvironmentName != "" {
		addCondition("c.environment_name = @env", sql.Named("env", f.EnvironmentName))
	}
	if f.Status != "" {
		addCondition("c.status = @status", sql.Named("status", string(f.Status)))
	}
	switch f.Kind {
	case models.A2ACatalogKindFilterPlatform:
		conditions = append(conditions, "c.source = 'platform'")
	case models.A2ACatalogKindFilterExternal:
		conditions = append(conditions, "c.source = 'external'")
	case models.A2ACatalogKindFilterProxied:
		conditions = append(conditions, a2aCatalogProxyExists)
	}

	matchedExpr := "FALSE"
	if f.Query != "" {
		args = append(args, sql.Named("pattern", "%"+escapeLikePattern(f.Query)+"%"))
		conditions = append(conditions, a2aCatalogSearchMatch)
		matchedExpr = "COALESCE(" + a2aCatalogSearchMatch + ", FALSE)"
	}
	where := strings.Join(conditions, " AND ")

	// The window counts the matching agents before LIMIT applies, so one scan yields the page and the total.
	pageSQL := fmt.Sprintf(`SELECT c.project_name, c.agent_name, COUNT(*) OVER () AS total
		FROM a2a_agent_cards c WHERE %s
		GROUP BY c.project_name, c.agent_name ORDER BY c.project_name, c.agent_name`, where)
	if f.Limit > 0 {
		pageSQL += " LIMIT @limit OFFSET @offset"
		args = append(args, sql.Named("limit", f.Limit), sql.Named("offset", f.Offset))
	}
	// Flat, not embedding a2aCatalogAgentKey: gorm skips an unexported embedded field, leaving the names empty.
	var page []struct {
		ProjectName, AgentName string
		Total                  int64
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
	rows, err := r.rowsOfAgents(ctx, ouID, agents, f.Query, matchedExpr)
	if err != nil {
		return nil, 0, err
	}
	return rows, page[0].Total, nil
}

// countAgents counts the matching agents for a page past the last one, which has no row to carry the total.
func (r *A2ACatalogRepo) countAgents(ctx context.Context, where string, args []any) (int64, error) {
	var total int64
	countSQL := fmt.Sprintf(`SELECT COUNT(*) FROM (
		SELECT 1 FROM a2a_agent_cards c WHERE %s GROUP BY c.project_name, c.agent_name) agents`, where)
	if err := r.db.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("count a2a catalogue agents: %w", err)
	}
	return total, nil
}

func (r *A2ACatalogRepo) GetAgent(ctx context.Context, ouID, projectName, agentName string) ([]models.A2ACatalogRow, error) {
	return r.rowsOfAgents(ctx, ouID, []a2aCatalogAgentKey{{ProjectName: projectName, AgentName: agentName}}, "", "FALSE")
}

func (r *A2ACatalogRepo) rowsOfAgents(ctx context.Context, ouID string, agents []a2aCatalogAgentKey,
	query, matchedExpr string,
) ([]models.A2ACatalogRow, error) {
	pairs := make([][]any, 0, len(agents))
	for _, a := range agents {
		pairs = append(pairs, []any{a.ProjectName, a.AgentName})
	}
	args := []any{sql.Named("ou", ouID), sql.Named("pairs", pairs)}
	if query != "" {
		args = append(args, sql.Named("pattern", "%"+escapeLikePattern(query)+"%"))
	}

	rowsSQL := fmt.Sprintf(`SELECT c.*, %s AS proxied, %s AS matched FROM a2a_agent_cards c
		WHERE c.ou_id = @ou AND (c.project_name, c.agent_name) IN @pairs
		ORDER BY c.project_name, c.agent_name, c.environment_name`, a2aCatalogProxyExists, matchedExpr)

	var scanned []struct {
		models.A2AAgentCard
		Proxied bool
		Matched bool
	}
	if err := r.db.WithContext(ctx).Raw(rowsSQL, args...).Scan(&scanned).Error; err != nil {
		return nil, fmt.Errorf("load a2a catalogue rows: %w", err)
	}
	rows := make([]models.A2ACatalogRow, 0, len(scanned))
	for _, s := range scanned {
		rows = append(rows, models.A2ACatalogRow{Card: s.A2AAgentCard, Proxied: s.Proxied, Matched: s.Matched})
	}
	return rows, nil
}
