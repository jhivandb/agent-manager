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
	"errors"
	"net/http"

	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// A2AAgentCardController serves the stored A2A agent card for one
// agent-environment pair, and lets an operator re-run its fetch.
type A2AAgentCardController interface {
	GetAgentCard(w http.ResponseWriter, r *http.Request)
	RefreshAgentCard(w http.ResponseWriter, r *http.Request)
}

type a2aAgentCardController struct {
	svc services.A2AAgentCardServiceInterface
}

// NewA2AAgentCardController creates a new A2A agent card controller.
func NewA2AAgentCardController(svc services.A2AAgentCardServiceInterface) A2AAgentCardController {
	return &a2aAgentCardController{svc: svc}
}

// GetAgentCard handles GET .../environments/{envID}/agent-card
func (c *a2aAgentCardController) GetAgentCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)

	ouID := middleware.OUIDFromRequest(r)
	projName := r.PathValue(utils.PathParamProjName)
	agentName := r.PathValue(utils.PathParamAgentName)
	envID := r.PathValue(utils.PathParamEnvID)

	view, err := c.svc.GetAgentCard(ctx, ouID, projName, agentName, envID)
	if err != nil {
		switch {
		case errors.Is(err, utils.ErrEnvironmentNotFound):
			log.Warn("GetAgentCard: environment not found", "ouID", ouID, "agentName", agentName, "envID", envID)
			utils.WriteErrorResponse(w, http.StatusNotFound, "Environment not found")
		default:
			log.Error("GetAgentCard: failed to load agent card", "ouID", ouID, "agentName", agentName, "envID", envID, "error", err)
			utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to get agent card")
		}
		return
	}
	if view == nil {
		log.Warn("GetAgentCard: no publication recorded", "ouID", ouID, "agentName", agentName, "envID", envID)
		utils.WriteErrorResponse(w, http.StatusNotFound, "No agent card publication recorded for this agent-environment pair")
		return
	}

	resp := spec.A2AAgentCardResponse{
		Card:      view.Card,
		Status:    string(view.Status),
		LastError: view.LastError,
	}
	resp.FetchedAt.Set(view.FetchedAt)
	utils.WriteSuccessResponse(w, http.StatusOK, resp)
}

// RefreshAgentCard handles POST .../environments/{envID}/agent-card/refresh
func (c *a2aAgentCardController) RefreshAgentCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)

	ouID := middleware.OUIDFromRequest(r)
	projName := r.PathValue(utils.PathParamProjName)
	agentName := r.PathValue(utils.PathParamAgentName)
	envID := r.PathValue(utils.PathParamEnvID)

	err := c.svc.RefreshAgentCard(ctx, ouID, projName, agentName, envID)
	if err != nil {
		switch {
		case errors.Is(err, utils.ErrEnvironmentNotFound):
			log.Warn("RefreshAgentCard: environment not found", "ouID", ouID, "agentName", agentName, "envID", envID)
			utils.WriteErrorResponse(w, http.StatusNotFound, "Environment not found")
		case errors.Is(err, utils.ErrA2APublicationNotFound):
			log.Warn("RefreshAgentCard: no publication recorded", "ouID", ouID, "agentName", agentName, "envID", envID)
			utils.WriteErrorResponse(w, http.StatusNotFound, "No agent card publication recorded for this agent-environment pair")
		default:
			log.Error("RefreshAgentCard: failed to requeue card", "ouID", ouID, "agentName", agentName, "envID", envID, "error", err)
			utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to refresh agent card")
		}
		return
	}

	log.Info("RefreshAgentCard: card refresh requeued", "ouID", ouID, "agentName", agentName, "envID", envID)
	w.WriteHeader(http.StatusAccepted)
}
