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
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
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

			handled := NewDeploymentAckHandler(repo, &repomocks.A2APublicationRepositoryMock{}).HandleMessage("gw-1", ackMessage(t, models.DeploymentAckPayload{
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
			handled := NewDeploymentAckHandler(repo, &repomocks.A2APublicationRepositoryMock{}).HandleMessage("gw-1", ackMessage(t, tc.ack))

			assert.True(t, handled, "the message is still a deployment.ack")
			assert.Empty(t, repo.UpdateStatusByDeploymentIDCalls())
		})
	}
}

// ackHandlerWithPublications builds the handler with both repositories, so tests
// can assert on what the rejection branch did.
func ackHandlerWithPublications(t *testing.T) (*DeploymentAckHandler, *repomocks.A2APublicationRepositoryMock) {
	t.Helper()
	deploymentRepo := &repomocks.DeploymentRepositoryMock{
		UpdateStatusByDeploymentIDFunc: func(deploymentID, gatewayUUID string, status models.DeploymentStatus) (time.Time, error) {
			return time.Now(), nil
		},
	}
	pubRepo := &repomocks.A2APublicationRepositoryMock{
		MarkCardRejectedFunc: func(ctx context.Context, deploymentID uuid.UUID, errorCode string) error { return nil },
	}
	return NewDeploymentAckHandler(deploymentRepo, pubRepo), pubRepo
}

// The gateway validates an Agent only after it fetches it, which is after the
// broadcast. Without this the row would report published for a card the gateway
// refused, and the API's whole value is honest status.
func TestDeploymentAckHandlerRecordsACardRejection(t *testing.T) {
	handler, pubRepo := ackHandlerWithPublications(t)
	deploymentID := uuid.New()

	handled := handler.HandleMessage("gw-1", ackMessage(t, models.DeploymentAckPayload{
		DeploymentID: deploymentID.String(),
		ArtifactID:   "0192f4c1-9a7d-7c3e-b4f2-1a2b3c4d5e6f",
		ResourceType: "agentproxy",
		Action:       "deploy",
		Status:       "failed",
		ErrorCode:    "INVALID_AGENT_CARD",
	}))

	assert.True(t, handled)
	calls := pubRepo.MarkCardRejectedCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, deploymentID, calls[0].DeploymentID)
	assert.Equal(t, "INVALID_AGENT_CARD", calls[0].ErrorCode)
}

// A success ack needs no handling: MarkCardPublished already recorded it.
func TestDeploymentAckHandlerIgnoresSuccessfulAgentAcks(t *testing.T) {
	handler, pubRepo := ackHandlerWithPublications(t)

	handler.HandleMessage("gw-1", ackMessage(t, models.DeploymentAckPayload{
		DeploymentID: uuid.New().String(),
		ResourceType: "agentproxy",
		Action:       "deploy",
		Status:       "success",
	}))

	assert.Empty(t, pubRepo.MarkCardRejectedCalls())
}

// The rejection branch is A2A's alone; an MCP proxy failing to deploy must not
// touch a publication row.
func TestDeploymentAckHandlerIgnoresOtherResourceTypes(t *testing.T) {
	handler, pubRepo := ackHandlerWithPublications(t)

	handler.HandleMessage("gw-1", ackMessage(t, models.DeploymentAckPayload{
		DeploymentID: uuid.New().String(),
		ResourceType: "mcpproxy",
		Action:       "deploy",
		Status:       "failed",
		ErrorCode:    "BOOM",
	}))

	assert.Empty(t, pubRepo.MarkCardRejectedCalls())
}

// A deployment ID the gateway invented, or one from a publish so old it no
// longer parses, must not reach the repository as a zero UUID — that would match
// whatever row happened to have a null-ish id.
func TestDeploymentAckHandlerIgnoresUnparseableDeploymentIDs(t *testing.T) {
	handler, pubRepo := ackHandlerWithPublications(t)

	handler.HandleMessage("gw-1", ackMessage(t, models.DeploymentAckPayload{
		DeploymentID: "not-a-uuid",
		ResourceType: "agentproxy",
		Action:       "deploy",
		Status:       "failed",
		ErrorCode:    "BOOM",
	}))

	assert.Empty(t, pubRepo.MarkCardRejectedCalls())
}
