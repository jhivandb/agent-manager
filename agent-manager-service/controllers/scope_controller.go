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
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// ScopeController defines HTTP handlers for the org-global scope catalog.
type ScopeController interface {
	ListScopes(w http.ResponseWriter, r *http.Request)
	CreateScope(w http.ResponseWriter, r *http.Request)
	UpdateScope(w http.ResponseWriter, r *http.Request)
	DeleteScope(w http.ResponseWriter, r *http.Request)
}

type scopeController struct {
	svc services.ScopeService
}

// NewScopeController creates a new scope catalog controller.
func NewScopeController(svc services.ScopeService) ScopeController {
	return &scopeController{svc: svc}
}

// ListScopes returns the org's scope catalog with per-scope binding counts.
func (c *scopeController) ListScopes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	orgName := r.PathValue(utils.PathParamOrgName)

	scopes, err := c.svc.List(ctx, orgName)
	if err != nil {
		log.Error("ListScopes failed", "orgName", orgName, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to list scopes")
		return
	}
	counts, err := c.svc.BindingCounts(ctx, orgName)
	if err != nil {
		log.Error("ListScopes binding counts failed", "orgName", orgName, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to list scopes")
		return
	}

	items := make([]spec.ScopeResponse, 0, len(scopes))
	for i := range scopes {
		resp := toScopeResponse(&scopes[i])
		count := int32(counts[scopes[i].Name])
		resp.BindingCount = &count
		items = append(items, resp)
	}
	utils.WriteSuccessResponse(w, http.StatusOK, spec.ScopeListResponse{Scopes: items})
}

// CreateScope adds a new scope to the org catalog.
func (c *scopeController) CreateScope(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	orgName := r.PathValue(utils.PathParamOrgName)

	var body spec.ScopeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	description := ""
	if body.Description != nil {
		description = *body.Description
	}

	scope, err := c.svc.Create(ctx, orgName, body.Name, description)
	switch {
	case errors.Is(err, utils.ErrInvalidInput):
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, utils.ErrConflict):
		utils.WriteErrorResponse(w, http.StatusConflict, err.Error())
	case err != nil:
		log.Error("CreateScope failed", "orgName", orgName, "name", body.Name, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to create scope")
	default:
		utils.WriteSuccessResponse(w, http.StatusCreated, toScopeResponse(scope))
	}
}

// UpdateScope changes a scope's description.
func (c *scopeController) UpdateScope(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	orgName := r.PathValue(utils.PathParamOrgName)
	name := r.PathValue(utils.PathParamScopeName)

	var body spec.ScopeUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	description := ""
	if body.Description != nil {
		description = *body.Description
	}

	scope, err := c.svc.Update(ctx, orgName, name, description)
	switch {
	case errors.Is(err, utils.ErrScopeNotFound):
		utils.WriteErrorResponse(w, http.StatusNotFound, "Scope not found")
	case err != nil:
		log.Error("UpdateScope failed", "orgName", orgName, "name", name, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update scope")
	default:
		utils.WriteSuccessResponse(w, http.StatusOK, toScopeResponse(scope))
	}
}

// DeleteScope removes a scope, refusing while any MCP proxy tool binding references it.
func (c *scopeController) DeleteScope(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	orgName := r.PathValue(utils.PathParamOrgName)
	name := r.PathValue(utils.PathParamScopeName)

	err := c.svc.Delete(ctx, orgName, name)
	switch {
	case errors.Is(err, utils.ErrScopeNotFound):
		utils.WriteErrorResponse(w, http.StatusNotFound, "Scope not found")
	case errors.Is(err, utils.ErrConflict):
		utils.WriteErrorResponse(w, http.StatusConflict, err.Error())
	case err != nil:
		log.Error("DeleteScope failed", "orgName", orgName, "name", name, "error", err)
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to delete scope")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// toScopeResponse maps a stored scope to its API representation. The caller sets
// BindingCount separately where the catalog scan is available (list only).
func toScopeResponse(s *models.Scope) spec.ScopeResponse {
	id := s.ID.String()
	description := s.Description
	createdAt := s.CreatedAt
	updatedAt := s.UpdatedAt
	return spec.ScopeResponse{
		Id:          &id,
		Name:        s.Name,
		Description: &description,
		CreatedAt:   &createdAt,
		UpdatedAt:   &updatedAt,
	}
}
