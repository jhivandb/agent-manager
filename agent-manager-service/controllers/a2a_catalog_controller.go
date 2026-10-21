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
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const maxCatalogQueryRunes = 255

// A2ACatalogController serves the org-wide A2A agent catalogue and the per-environment connect details.
type A2ACatalogController interface {
	ListA2AAgents(w http.ResponseWriter, r *http.Request)
	GetA2AConnect(w http.ResponseWriter, r *http.Request)
}

type a2aCatalogController struct {
	catalogService services.A2ACatalogService
}

// NewA2ACatalogController creates an A2ACatalogController.
func NewA2ACatalogController(catalogService services.A2ACatalogService) A2ACatalogController {
	return &a2aCatalogController{catalogService: catalogService}
}

func (c *a2aCatalogController) ListA2AAgents(w http.ResponseWriter, r *http.Request) {
	filter, problem := parseA2ACatalogFilter(r)
	if problem != "" {
		utils.WriteErrorResponse(w, http.StatusBadRequest, problem)
		return
	}
	page, err := c.catalogService.ListA2AAgents(r.Context(), middleware.OUIDFromRequest(r), filter)
	if err != nil {
		handleCommonErrors(w, err, "Failed to list A2A agents")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, toA2ACatalogListResponse(page))
}

func (c *a2aCatalogController) GetA2AConnect(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	info, err := c.catalogService.GetA2AConnect(r.Context(), ouID, projName, agentName, envName)
	if err != nil {
		writeAgentCardError(w, r, err, "Failed to get the A2A connect details")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, toA2AConnectResponse(info))
}

// parseA2ACatalogFilter returns a non-empty problem when a query parameter is invalid.
func parseA2ACatalogFilter(r *http.Request) (models.A2ACatalogFilter, string) {
	query := r.URL.Query()
	var filter models.A2ACatalogFilter

	limit, offset := utils.DefaultLimit, utils.DefaultOffset
	var err error
	if raw := query.Get("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil || limit < utils.MinLimit || limit > utils.MaxLimit {
			return filter, "Invalid limit parameter: must be between " +
				strconv.Itoa(utils.MinLimit) + " and " + strconv.Itoa(utils.MaxLimit)
		}
	}
	if raw := query.Get("offset"); raw != "" {
		if offset, err = strconv.Atoi(raw); err != nil || offset < utils.MinOffset {
			return filter, "Invalid offset parameter: must be " + strconv.Itoa(utils.MinOffset) + " or greater"
		}
	}

	text := strings.TrimSpace(query.Get("q"))
	if utf8.RuneCountInString(text) > maxCatalogQueryRunes {
		return filter, "Invalid q parameter: must be at most " + strconv.Itoa(maxCatalogQueryRunes) + " characters"
	}

	kind := models.A2ACatalogKindFilter(query.Get("kind"))
	switch kind {
	case "", models.A2ACatalogKindFilterPlatform, models.A2ACatalogKindFilterExternal, models.A2ACatalogKindFilterProxied:
	default:
		return filter, "Invalid kind parameter: must be platform, external or proxied"
	}

	status := models.A2AAgentCardStatus(query.Get("status"))
	switch status {
	case "", models.A2AAgentCardStatusPending, models.A2AAgentCardStatusFetched, models.A2AAgentCardStatusFailed:
	default:
		return filter, "Invalid status parameter: must be pending, fetched or failed"
	}

	filter = models.A2ACatalogFilter{
		Query:           text,
		ProjectName:     query.Get("project"),
		EnvironmentName: query.Get("environment"),
		Kind:            kind,
		Status:          status,
		Limit:           limit,
		Offset:          offset,
	}
	return filter, ""
}

func toA2ACatalogListResponse(page *models.A2ACatalogPage) *spec.A2ACatalogListResponse {
	agents := make([]spec.A2ACatalogAgent, 0, len(page.Entries))
	for _, entry := range page.Entries {
		environments := make([]spec.A2ACatalogEnvironment, 0, len(entry.Environments))
		for _, env := range entry.Environments {
			wire := spec.NewA2ACatalogEnvironment(env.Name, string(env.Status), env.Proxied, env.CardDiffers, env.Matched)
			wire.FetchedAt = env.FetchedAt
			environments = append(environments, *wire)
		}
		skills := entry.Skills
		if skills == nil {
			skills = []string{}
		}
		agents = append(agents, *spec.NewA2ACatalogAgent(entry.ProjectName, entry.AgentName, string(entry.Kind),
			entry.Name, entry.Description, entry.Version, skills, entry.LeadEnvironment, environments))
	}
	return spec.NewA2ACatalogListResponse(agents, int32(page.Total), int32(page.Limit), int32(page.Offset)) //nolint:gosec // bounded by the page limit
}

func toA2AConnectResponse(info *models.A2AConnectInfo) *spec.A2AConnectResponse {
	security := spec.NewA2AConnectSecurity(string(info.Security.Mode))
	if info.Security.APIKeyHeader != "" {
		security.SetApiKeyHeader(info.Security.APIKeyHeader)
	}
	if info.Security.Mode == models.A2ASecurityModeOAuth && info.Security.OAuth != nil {
		security.SetOauth(toA2AConnectOAuth(info.Security.OAuth, info.Security.Issuers))
	}
	publication := spec.NewA2AConnectPublication(info.Publication.Status, info.Publication.LastError)
	return spec.NewA2AConnectResponse(string(info.Kind), info.Proxied, info.IsProduction, info.GatewayURL, *security, *publication)
}

func toA2AConnectOAuth(oauth *models.OAuthConfig, issuers []models.A2AConnectIssuer) spec.A2AConnectOAuth {
	wireIssuers := make([]spec.A2AConnectIssuer, 0, len(issuers))
	for _, issuer := range issuers {
		wireIssuers = append(wireIssuers, *spec.NewA2AConnectIssuer(issuer.Name, issuer.Issuer))
	}
	audiences := oauth.Audiences
	if audiences == nil {
		audiences = []string{}
	}
	return *spec.NewA2AConnectOAuth(wireIssuers, audiences, oauth.HeaderName, oauth.AuthHeaderPrefix)
}
