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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

func ackMessage(t *testing.T, ack models.DeploymentAckPayload) []byte {
	t.Helper()
	payload, err := json.Marshal(ack)
	require.NoError(t, err)
	msg, err := json.Marshal(models.GatewayMessage{Type: "deployment.ack", Payload: payload})
	require.NoError(t, err)
	return msg
}

// An A2A Agent acks under the gateway's own resource-type name, "agentproxy".
// Agent deployment rows are written optimistically as deployed, so a dropped ack
// would leave a failed deploy reading as healthy.
func TestDeploymentAckHandlerUpdatesAgentStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		status string
		want   models.DeploymentStatus
	}{
		{"deploy succeeded", "deploy", "success", models.DeploymentStatusDeployed},
		{"deploy failed", "deploy", "failed", models.DeploymentStatusUndeployed},
		{"undeploy succeeded", "undeploy", "success", models.DeploymentStatusUndeployed},
		{"undeploy failed", "undeploy", "failed", models.DeploymentStatusDeployed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repomocks.DeploymentRepositoryMock{
				UpdateStatusByDeploymentIDFunc: func(deploymentID, gatewayUUID string, status models.DeploymentStatus) (time.Time, error) {
					return time.Now(), nil
				},
			}

			handled := NewDeploymentAckHandler(repo).HandleMessage("gw-1", ackMessage(t, models.DeploymentAckPayload{
				DeploymentID: "dep-1",
				ArtifactID:   "0192f4c1-9a7d-7c3e-b4f2-1a2b3c4d5e6f",
				ResourceType: "agentproxy",
				Action:       tc.action,
				Status:       tc.status,
			}))

			assert.True(t, handled)
			calls := repo.UpdateStatusByDeploymentIDCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, "dep-1", calls[0].DeploymentID)
			assert.Equal(t, "gw-1", calls[0].GatewayUUID)
			assert.Equal(t, tc.want, calls[0].Status)
		})
	}
}

// A nil UpdateStatusByDeploymentIDFunc panics if called, so these assert the
// handler never reaches the repository.
func TestDeploymentAckHandlerSkipsUnusableAcks(t *testing.T) {
	for _, tc := range []struct {
		name string
		ack  models.DeploymentAckPayload
	}{
		{"unknown resource type", models.DeploymentAckPayload{
			DeploymentID: "dep-1", ResourceType: "somethingelse", Action: "deploy", Status: "success",
		}},
		{"agent ack without a deployment id", models.DeploymentAckPayload{
			ResourceType: "agentproxy", Action: "deploy", Status: "success",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repomocks.DeploymentRepositoryMock{}
			handled := NewDeploymentAckHandler(repo).HandleMessage("gw-1", ackMessage(t, tc.ack))

			assert.True(t, handled, "the message is still a deployment.ack")
			assert.Empty(t, repo.UpdateStatusByDeploymentIDCalls())
		})
	}
}
