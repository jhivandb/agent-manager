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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// cardServiceStub stands in for the card service; only SetA2ACardSource is wired.
type cardServiceStub struct {
	services.A2AAgentCardService
	SetSourceFunc func(ctx context.Context, ouID, projectName, agentName, envName, sourceURL string) error
}

func (s *cardServiceStub) SetA2ACardSource(ctx context.Context, ouID, projectName, agentName, envName, sourceURL string) error {
	return s.SetSourceFunc(ctx, ouID, projectName, agentName, envName, sourceURL)
}

func cardSourceRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut,
		"/orgs/default/projects/checkout/agents/weather/environments/dev/agent-card/source", strings.NewReader(body))
	req.SetPathValue(utils.PathParamProjName, "checkout")
	req.SetPathValue(utils.PathParamAgentName, "weather")
	req.SetPathValue(utils.PathParamEnvID, "dev")
	return req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
}

func TestSetAgentCardSourceErrorsMapToStatusCodes(t *testing.T) {
	for sentinel, code := range map[error]int{
		utils.ErrA2AProxyOriginChangeNeedsAuthReset: http.StatusConflict,
		utils.ErrInvalidURL:                         http.StatusBadRequest,
		utils.ErrAgentCardSourceNotExternal:         http.StatusBadRequest,
	} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			stub := &cardServiceStub{SetSourceFunc: func(context.Context, string, string, string, string, string) error {
				return fmt.Errorf("wrapped: %w", sentinel)
			}}
			rec := httptest.NewRecorder()
			NewA2AAgentCardController(stub).SetAgentCardSource(rec,
				cardSourceRequest(`{"url":"https://other.example/.well-known/agent-card.json"}`))
			assert.Equal(t, code, rec.Code)
		})
	}
}
