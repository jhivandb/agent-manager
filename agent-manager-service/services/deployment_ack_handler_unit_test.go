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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// recordingAckRepo returns a repository mock that records every ApplyAck and reports it applied.
func recordingAckRepo() *repomocks.DeploymentRepositoryMock {
	return &repomocks.DeploymentRepositoryMock{
		ApplyAckFunc: func(context.Context, string, models.DeploymentAck) (bool, error) { return true, nil },
	}
}

func ackMessage(payload string) []byte {
	return []byte(`{"type":"deployment.ack","payload":` + payload + `}`)
}

func TestDeploymentAckHandlerRecordsAgentAcksUnderEitherResourceType(t *testing.T) {
	for _, resourceType := range []string{"agentproxy", "Agent"} {
		t.Run(resourceType, func(t *testing.T) {
			repo := recordingAckRepo()
			msg := ackMessage(`{"deploymentId":"d-1","artifactId":"a-1","resourceType":"` +
				resourceType + `","action":"deploy","status":"success"}`)

			assert.True(t, NewDeploymentAckHandler(repo).HandleMessage("gw-1", msg))

			calls := repo.ApplyAckCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, "gw-1", calls[0].GatewayID)
			assert.Equal(t, models.DeploymentAck{
				DeploymentID: "d-1", ArtifactID: "a-1", Action: models.DeploymentAckActionDeploy, Success: true,
			}, calls[0].Ack)
		})
	}
}

func TestDeploymentAckHandlerPassesOutcomeAndErrorCode(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          models.DeploymentAck
	}{
		{
			name:    "failed deploy keeps its error code",
			payload: `{"deploymentId":"d-1","artifactId":"p-1","resourceType":"llmprovider","action":"deploy","status":"failed","errorCode":"GATEWAY_PROCESSING_ERROR"}`,
			want: models.DeploymentAck{
				DeploymentID: "d-1", ArtifactID: "p-1", Action: models.DeploymentAckActionDeploy,
				Success: false, ErrorCode: "GATEWAY_PROCESSING_ERROR",
			},
		},
		{
			name:    "undeploy success",
			payload: `{"deploymentId":"d-2","artifactId":"x-1","resourceType":"mcpproxy","action":"undeploy","status":"success"}`,
			want:    models.DeploymentAck{DeploymentID: "d-2", ArtifactID: "x-1", Action: models.DeploymentAckActionUndeploy, Success: true},
		},
		{
			name:    "undeploy without an id answers an undeploy sent without one",
			payload: `{"deploymentId":"","artifactId":"my-proxy","resourceType":"llmproxy","action":"undeploy","status":"success"}`,
			want:    models.DeploymentAck{ArtifactID: "my-proxy", Action: models.DeploymentAckActionUndeploy, Success: true},
		},
		{
			name:    "failed undeploy",
			payload: `{"deploymentId":"d-3","artifactId":"a-1","resourceType":"Agent","action":"undeploy","status":"failed","errorCode":"DEPLOYMENT_ID_MISMATCH"}`,
			want: models.DeploymentAck{
				DeploymentID: "d-3", ArtifactID: "a-1", Action: models.DeploymentAckActionUndeploy,
				ErrorCode: "DEPLOYMENT_ID_MISMATCH",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := recordingAckRepo()

			assert.True(t, NewDeploymentAckHandler(repo).HandleMessage("gw-1", ackMessage(tc.payload)))

			calls := repo.ApplyAckCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tc.want, calls[0].Ack)
		})
	}
}

func TestDeploymentAckHandlerIgnoresAcksItCannotApply(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"deploy without an id", `{"deploymentId":"","artifactId":"a-1","resourceType":"llmprovider","action":"deploy","status":"success"}`},
		{"undeploy naming nothing", `{"deploymentId":"","artifactId":"","resourceType":"llmprovider","action":"undeploy","status":"success"}`},
		{"unsupported resource type", `{"deploymentId":"d-1","artifactId":"a-1","resourceType":"api","action":"deploy","status":"success"}`},
		{"unknown action", `{"deploymentId":"d-1","artifactId":"a-1","resourceType":"llmprovider","action":"restart","status":"success"}`},
		{"unknown outcome", `{"deploymentId":"d-1","artifactId":"a-1","resourceType":"llmprovider","action":"deploy","status":"pending"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// ApplyAckFunc is nil: applying would panic.
			repo := &repomocks.DeploymentRepositoryMock{}

			assert.True(t, NewDeploymentAckHandler(repo).HandleMessage("gw-1", ackMessage(tc.payload)))
		})
	}
}

func TestDeploymentAckHandlerSurvivesARepositoryError(t *testing.T) {
	repo := &repomocks.DeploymentRepositoryMock{
		ApplyAckFunc: func(context.Context, string, models.DeploymentAck) (bool, error) { return false, assert.AnError },
	}
	msg := ackMessage(`{"deploymentId":"d-1","artifactId":"a-1","resourceType":"llmproxy","action":"deploy","status":"success"}`)

	assert.True(t, NewDeploymentAckHandler(repo).HandleMessage("gw-1", msg))
	assert.Len(t, repo.ApplyAckCalls(), 1)
}
