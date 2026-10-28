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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/services"
)

// syncRig is the gateway internal controller with one gateway whose api-key is token.
type syncRig struct {
	gatewayID   uuid.UUID
	ouID        string
	token       string
	deployments *repomocks.DeploymentRepositoryMock
	artifacts   *repomocks.ArtifactRepositoryMock
	ctrl        GatewayInternalController
}

func newSyncRig(t *testing.T) *syncRig {
	t.Helper()
	r := &syncRig{
		gatewayID:   uuid.New(),
		ouID:        "ou-1",
		deployments: &repomocks.DeploymentRepositoryMock{},
		artifacts:   &repomocks.ArtifactRepositoryMock{},
	}
	prefix := uuid.NewString()
	r.token = prefix + "-secret"
	salt := []byte("salt")
	sum := sha256.Sum256(append([]byte(r.token), salt...))
	gateways := &repomocks.GatewayRepositoryMock{
		GetActiveTokenByPrefixFunc: func(_ context.Context, p string) (*models.GatewayToken, error) {
			if p != prefix {
				return nil, nil //nolint:nilnil // the repository reports "no such token" this way
			}
			return &models.GatewayToken{GatewayUUID: r.gatewayID, TokenHash: hex.EncodeToString(sum[:]), Salt: hex.EncodeToString(salt)}, nil
		},
		GetByUUIDFunc: func(id string) (*models.Gateway, error) {
			return &models.Gateway{UUID: r.gatewayID, OUID: r.ouID}, nil
		},
	}
	internal := services.NewGatewayInternalAPIService(nil, nil, r.deployments, nil, r.artifacts, nil, make([]byte, 32))
	r.ctrl = NewGatewayInternalController(services.NewPlatformGatewayService(gateways, nil), internal, nil, nil)
	return r
}

func (r *syncRig) request(method, path, body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("api-key", r.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestSyncRoutesRefuseAMissingOrWrongAPIKey(t *testing.T) {
	r := newSyncRig(t)
	handlers := map[string]http.HandlerFunc{
		"deployments": r.ctrl.GetDeployments,
		"fetch-batch": r.ctrl.FetchDeploymentsBatch,
		"exists":      r.ctrl.CheckArtifactsExist,
	}
	for name, handler := range handlers {
		for _, key := range []string{"", uuid.NewString() + "-wrong"} {
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
			req.Header.Set("Accept", "application/x-tar+gzip")
			if key != "" {
				req.Header.Set("api-key", key)
			}
			rec := httptest.NewRecorder()
			handler(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s with key %q", name, key)
		}
	}
	// No repository func is set: an unauthenticated request reached none.
}

func TestGetDeploymentsListsTheCallingGatewaysDeployments(t *testing.T) {
	r := newSyncRig(t)
	depID, artifact := uuid.New(), uuid.New()
	performedAt := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	r.deployments.ListForGatewaySyncFunc = func(_ context.Context, gatewayID, orgUUID string) ([]models.GatewaySyncDeployment, error) {
		assert.Equal(t, r.gatewayID.String(), gatewayID, "scoped to the authenticated gateway")
		assert.Equal(t, r.ouID, orgUUID, "and its org")
		return []models.GatewaySyncDeployment{{
			GatewayArtifactRef: models.GatewayArtifactRef{ArtifactUUID: artifact, ArtifactHandle: "my-proxy", ArtifactKind: models.KindLLMProxy},
			DeploymentID:       depID, StatusDesired: models.DeploymentStatusDeploying.ImpliedDesired(), PerformedAt: performedAt,
		}}, nil
	}

	rec := httptest.NewRecorder()
	r.ctrl.GetDeployments(rec, r.request(http.MethodGet, "/api/internal/v1/deployments", "", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body map[string][]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body["deployments"], 1)
	got := body["deployments"][0]
	assert.Equal(t, "my-proxy", got["artifactId"])
	assert.Equal(t, depID.String(), got["deploymentId"])
	assert.Equal(t, "LlmProxy", got["kind"])
	assert.Equal(t, "deployed", got["state"])
	assert.Equal(t, "2026-10-28T12:00:00Z", got["deployedAt"])
	assert.NotEmpty(t, got["etag"])
}

func TestGetDeploymentsWithNothingDeployedIsAnEmptyList(t *testing.T) {
	r := newSyncRig(t)
	r.deployments.ListForGatewaySyncFunc = func(context.Context, string, string) ([]models.GatewaySyncDeployment, error) {
		return nil, nil
	}
	rec := httptest.NewRecorder()
	r.ctrl.GetDeployments(rec, r.request(http.MethodGet, "/api/internal/v1/deployments", "", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"deployments":[]}`, rec.Body.String())
}

func TestFetchDeploymentsBatchRequiresTarGzip(t *testing.T) {
	r := newSyncRig(t)
	for _, accept := range []string{"", "application/json", "application/zip"} {
		rec := httptest.NewRecorder()
		r.ctrl.FetchDeploymentsBatch(rec, r.request(http.MethodPost, "/api/internal/v1/deployments/fetch-batch",
			`{"deploymentIds":["`+uuid.NewString()+`"]}`, map[string]string{"Accept": accept}))
		assert.Equal(t, http.StatusNotAcceptable, rec.Code, "Accept %q", accept)
	}
}

func TestFetchDeploymentsBatchRejectsABadBody(t *testing.T) {
	r := newSyncRig(t)
	for _, body := range []string{`not json`, `{}`, `{"deploymentIds":[]}`} {
		rec := httptest.NewRecorder()
		r.ctrl.FetchDeploymentsBatch(rec, r.request(http.MethodPost, "/api/internal/v1/deployments/fetch-batch",
			body, map[string]string{"Accept": "application/x-tar+gzip"}))
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
}

func TestFetchDeploymentsBatchReturnsOneDirectoryPerDeployment(t *testing.T) {
	r := newSyncRig(t)
	depID, artifact := uuid.New(), uuid.New()
	r.deployments.GetContentsForGatewaySyncFunc = func(_ context.Context, gatewayID, orgUUID string, ids []uuid.UUID) ([]models.GatewaySyncContent, error) {
		assert.Equal(t, r.gatewayID.String(), gatewayID, "scoped to the authenticated gateway")
		assert.Equal(t, r.ouID, orgUUID, "and its org")
		assert.Equal(t, []uuid.UUID{depID}, ids)
		return []models.GatewaySyncContent{{
			GatewayArtifactRef: models.GatewayArtifactRef{ArtifactUUID: artifact, ArtifactHandle: "my-proxy", ArtifactKind: models.KindLLMProxy},
			DeploymentID:       depID, Content: []byte("kind: LlmProxy\nspec: {}\n"),
		}}, nil
	}

	rec := httptest.NewRecorder()
	r.ctrl.FetchDeploymentsBatch(rec, r.request(http.MethodPost, "/api/internal/v1/deployments/fetch-batch",
		`{"deploymentIds":["`+depID.String()+`"]}`, map[string]string{"Accept": "application/x-tar+gzip"}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "application/x-tar+gzip", rec.Header().Get("Content-Type"))

	gz, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	header, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, depID.String()+"/llm-proxy-my-proxy.yaml", header.Name)
	content, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Equal(t, "kind: LlmProxy\nspec: {}\n", string(content))
	_, err = tr.Next()
	assert.ErrorIs(t, err, io.EOF)
}

func TestCheckArtifactsExistAnswersForTheGatewaysOrg(t *testing.T) {
	r := newSyncRig(t)
	r.artifacts.ExistingGatewayArtifactIDsFunc = func(_ context.Context, orgUUID string, ids []string) ([]string, error) {
		assert.Equal(t, r.ouID, orgUUID)
		return []string{"my-proxy"}, nil
	}

	rec := httptest.NewRecorder()
	r.ctrl.CheckArtifactsExist(rec, r.request(http.MethodPost, "/api/internal/v1/artifacts/exists",
		`{"artifactIds":["my-proxy","gone"]}`, map[string]string{"Content-Type": "application/json"}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"artifacts":[{"artifactId":"my-proxy","exists":true},{"artifactId":"gone","exists":false}]}`, rec.Body.String())
}

func TestCheckArtifactsExistRejectsABadBody(t *testing.T) {
	r := newSyncRig(t)
	rec := httptest.NewRecorder()
	r.ctrl.CheckArtifactsExist(rec, r.request(http.MethodPost, "/api/internal/v1/artifacts/exists", `not json`, nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCheckArtifactsExistFailsWhenTheLookupFails(t *testing.T) {
	r := newSyncRig(t)
	r.artifacts.ExistingGatewayArtifactIDsFunc = func(context.Context, string, []string) ([]string, error) {
		return nil, assert.AnError
	}
	rec := httptest.NewRecorder()
	r.ctrl.CheckArtifactsExist(rec, r.request(http.MethodPost, "/api/internal/v1/artifacts/exists", `{"artifactIds":["a"]}`, nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "the gateway then deletes no orphan")
}
