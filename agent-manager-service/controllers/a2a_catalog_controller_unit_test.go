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

package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type catalogServiceStub struct {
	ListFunc    func(ctx context.Context, ouID string, filter models.A2ACatalogFilter) (*models.A2ACatalogPage, error)
	ConnectFunc func(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AConnectInfo, error)
}

func (s *catalogServiceStub) ListA2AAgents(ctx context.Context, ouID string, filter models.A2ACatalogFilter) (*models.A2ACatalogPage, error) {
	return s.ListFunc(ctx, ouID, filter)
}

func (s *catalogServiceStub) GetA2AConnect(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AConnectInfo, error) {
	return s.ConnectFunc(ctx, ouID, projectName, agentName, envName)
}

func catalogListRequest(query string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/orgs/default/a2a-agents?"+query, nil)
	return req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
}

func TestListA2AAgents_ParsesFilters(t *testing.T) {
	fetchedAt := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var gotOU string
	var gotFilter models.A2ACatalogFilter
	stub := &catalogServiceStub{ListFunc: func(_ context.Context, ouID string, filter models.A2ACatalogFilter) (*models.A2ACatalogPage, error) {
		gotOU, gotFilter = ouID, filter
		return &models.A2ACatalogPage{
			Entries: []models.A2ACatalogEntry{{
				ProjectName: "p1", AgentName: "refunds", Kind: models.A2AAgentCardSourceExternal,
				Name: "Refunds", Skills: []string{"refund"}, LeadEnvironment: "dev",
				Environments: []models.A2ACatalogEnvironment{{
					Name: "dev", Status: models.A2AAgentCardStatusFetched, FetchedAt: &fetchedAt,
					Proxied: true, CardDiffers: true, Matched: true,
				}},
			}},
			Total: 1, Limit: 24, Offset: 48,
		}, nil
	}}
	rec := httptest.NewRecorder()

	NewA2ACatalogController(stub).ListA2AAgents(rec, catalogListRequest(
		"q=%20refund%20&kind=proxied&environment=dev&project=p1&status=fetched&limit=24&offset=48"))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ou-org", gotOU)
	assert.Equal(t, models.A2ACatalogFilter{
		Query: "refund", ProjectName: "p1", EnvironmentName: "dev",
		Kind: models.A2ACatalogKindFilterProxied, Status: models.A2AAgentCardStatusFetched, Limit: 24, Offset: 48,
	}, gotFilter)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.EqualValues(t, 1, body["total"])
	assert.EqualValues(t, 24, body["limit"])
	assert.EqualValues(t, 48, body["offset"])
	agent := body["agents"].([]any)[0].(map[string]any)
	assert.Equal(t, "external", agent["kind"])
	env := agent["environments"].([]any)[0].(map[string]any)
	assert.Equal(t, true, env["cardDiffers"])
	assert.Equal(t, true, env["matched"])
	assert.Equal(t, true, env["proxied"])
	assert.Equal(t, "fetched", env["status"])
}

func TestListA2AAgents_DefaultsPagination(t *testing.T) {
	var gotFilter models.A2ACatalogFilter
	stub := &catalogServiceStub{ListFunc: func(_ context.Context, _ string, filter models.A2ACatalogFilter) (*models.A2ACatalogPage, error) {
		gotFilter = filter
		return &models.A2ACatalogPage{}, nil
	}}
	rec := httptest.NewRecorder()

	NewA2ACatalogController(stub).ListA2AAgents(rec, catalogListRequest(""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, utils.DefaultLimit, gotFilter.Limit)
	assert.Equal(t, utils.DefaultOffset, gotFilter.Offset)
	assert.JSONEq(t, `{"agents":[],"total":0,"limit":0,"offset":0}`, rec.Body.String())
}

func TestListA2AAgents_RejectsBadParams(t *testing.T) {
	cases := map[string]string{
		"limit zero":    "limit=0",
		"limit over":    "limit=101",
		"limit text":    "limit=x",
		"offset minus":  "offset=-1",
		"kind bogus":    "kind=bogus",
		"status bogus":  "status=bogus",
		"query too big": "q=" + strings.Repeat("a", 256),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			NewA2ACatalogController(&catalogServiceStub{}).ListA2AAgents(rec, catalogListRequest(query))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestGetA2AConnect_WritesResponse(t *testing.T) {
	stub := &catalogServiceStub{ConnectFunc: func(_ context.Context, ouID, projectName, agentName, envName string) (*models.A2AConnectInfo, error) {
		assert.Equal(t, []string{"ou-org", "checkout", "weather", "dev"}, []string{ouID, projectName, agentName, envName})
		return &models.A2AConnectInfo{
			Kind: models.A2AAgentCardSourceExternal, Proxied: true, IsProduction: false,
			GatewayURL: "https://gw.example.com/weather",
			Security:   models.A2AConnectSecurity{Mode: models.A2ASecurityModeAPIKey, APIKeyHeader: "X-API-Key"},
			Publication: models.A2AConnectPublication{
				Status: models.A2AConnectPublicationPublished,
			},
		}, nil
	}}
	rec := httptest.NewRecorder()

	NewA2ACatalogController(stub).GetA2AConnect(rec, agentEnvRequest(http.MethodGet, "a2a-connect", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"kind": "external", "proxied": true, "isProduction": false,
		"gatewayUrl": "https://gw.example.com/weather",
		"security": {"mode": "apiKey", "apiKeyHeader": "X-API-Key"},
		"publication": {"status": "published", "lastError": ""}
	}`, rec.Body.String())
}

func TestGetA2AConnect_WritesOAuth(t *testing.T) {
	stub := &catalogServiceStub{ConnectFunc: func(context.Context, string, string, string, string) (*models.A2AConnectInfo, error) {
		return &models.A2AConnectInfo{
			Kind: models.A2AAgentCardSourcePlatform,
			Security: models.A2AConnectSecurity{
				Mode:    models.A2ASecurityModeOAuth,
				OAuth:   &models.OAuthConfig{Audiences: []string{"aud"}, HeaderName: "Authorization", AuthHeaderPrefix: "Bearer "},
				Issuers: []models.A2AConnectIssuer{{Name: "thunder", Issuer: "https://idp.example.com"}},
			},
			Publication: models.A2AConnectPublication{Status: models.A2AConnectPublicationNone},
		}, nil
	}}
	rec := httptest.NewRecorder()

	NewA2ACatalogController(stub).GetA2AConnect(rec, agentEnvRequest(http.MethodGet, "a2a-connect", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"kind": "platform", "proxied": false, "isProduction": false, "gatewayUrl": "",
		"security": {"mode": "oauth", "oauth": {
			"issuers": [{"name": "thunder", "issuer": "https://idp.example.com"}],
			"audiences": ["aud"], "headerName": "Authorization", "authHeaderPrefix": "Bearer "}},
		"publication": {"status": "none", "lastError": ""}
	}`, rec.Body.String())
}

func TestGetA2AConnect_NotFound(t *testing.T) {
	stub := &catalogServiceStub{ConnectFunc: func(context.Context, string, string, string, string) (*models.A2AConnectInfo, error) {
		return nil, utils.ErrAgentCardNotFound
	}}
	rec := httptest.NewRecorder()

	NewA2ACatalogController(stub).GetA2AConnect(rec, agentEnvRequest(http.MethodGet, "a2a-connect", ""))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
