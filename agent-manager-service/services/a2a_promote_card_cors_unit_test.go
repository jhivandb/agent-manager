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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// TestPromoteAgent_RejectsCardCORSForNonAPIAgent pins the fix for a gap found in
// review: agentCardCorsConfig is resolved and validated inside PromoteAgent's
// `if isAPIAgent { ... }` block, so a non-API agent (which never enters that
// block) used to skip validateCardCORS entirely and promote successfully with
// the card CORS request silently ignored. PromoteAgent must instead reject it
// with utils.ErrInvalidInput, matching the global rule that agentCardCorsConfig
// only applies to A2A agents. Only ocClient.GetOrganization/GetComponent are
// wired: the guard must fire before the deployment-pipeline lookup (the next
// call PromoteAgent makes), i.e. before any side effect — a nil GetProjectDeploymentPipelineFunc
// would panic if reached, so passing without panicking is itself part of the proof.
func TestPromoteAgent_RejectsCardCORSForNonAPIAgent(t *testing.T) {
	svc := &agentManagerService{
		ocClient: &clientmocks.OpenChoreoClientMock{
			GetOrganizationFunc: func(_ context.Context, ouID string) (*models.OrganizationResponse, error) {
				return &models.OrganizationResponse{Name: ouID}, nil
			},
			GetComponentFunc: func(_ context.Context, _, _, name string) (*models.AgentResponse, error) {
				return &models.AgentResponse{
					UUID:         "agent-uuid",
					Name:         name,
					Provisioning: models.Provisioning{Type: string(utils.InternalAgent)},
					Type:         models.AgentType{Type: string(utils.AgentTypeExternalAPI)},
				}, nil
			},
		},
		logger: discardLogger(),
	}

	err := svc.PromoteAgent(context.Background(), "ou-1", "proj", "agent-1", &spec.PromoteAgentRequest{
		SourceEnvironment: "dev",
		TargetEnvironment: "staging",
		AgentCardCorsConfig: &spec.AgentCardCORSConfig{
			Enabled:     spec.PtrBool(true),
			AllowOrigin: []string{"*"},
		},
	})
	require.ErrorIs(t, err, utils.ErrInvalidInput)
}
