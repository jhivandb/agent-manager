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
	"log/slog"

	"github.com/google/uuid"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// DeploymentAckHandler processes deployment acknowledgment messages from gateways
type DeploymentAckHandler struct {
	deploymentRepo repositories.DeploymentRepository
	// pubRepo carries gateway rejections back to the A2A publication row. The
	// gateway validates an Agent after it fetches it, which is after the
	// broadcast, so this is the only path by which a refused card is ever known.
	pubRepo repositories.A2APublicationRepository
}

// NewDeploymentAckHandler creates a new deployment ack handler
func NewDeploymentAckHandler(
	deploymentRepo repositories.DeploymentRepository,
	pubRepo repositories.A2APublicationRepository,
) *DeploymentAckHandler {
	return &DeploymentAckHandler{deploymentRepo: deploymentRepo, pubRepo: pubRepo}
}

// HandleMessage parses a raw WebSocket message and processes it if it's a known event type.
// Returns true if the message was handled, false if it was unrecognized.
func (h *DeploymentAckHandler) HandleMessage(gatewayID string, data []byte) bool {
	var msg models.GatewayMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		slog.Warn("DeploymentAckHandler: failed to parse gateway message", "gatewayID", gatewayID, "error", err)
		return false
	}

	switch msg.Type {
	case "deployment.ack":
		h.handleDeploymentAck(gatewayID, msg.Payload)
		return true
	default:
		slog.Debug("DeploymentAckHandler: unhandled message type", "gatewayID", gatewayID, "type", msg.Type)
		return false
	}
}

func (h *DeploymentAckHandler) handleDeploymentAck(gatewayID string, payload json.RawMessage) {
	var ack models.DeploymentAckPayload
	if err := json.Unmarshal(payload, &ack); err != nil {
		slog.Error("DeploymentAckHandler: failed to parse deployment ack payload", "gatewayID", gatewayID, "error", err)
		return
	}

	log := slog.With(
		"gatewayID", gatewayID,
		"deploymentID", ack.DeploymentID,
		"artifactID", ack.ArtifactID,
		"resourceType", ack.ResourceType,
		"action", ack.Action,
		"status", ack.Status,
	)

	if ack.Status == "failed" {
		log.Warn("Gateway reported deployment failure", "errorCode", ack.ErrorCode)
	} else {
		log.Info("Gateway deployment ack received")
	}

	// Only process acks for deployable gateway artifacts with current deployment state.
	// An A2A Agent acks as "agentproxy", the gateway's own name for the kind.
	switch ack.ResourceType {
	case "llmprovider", "llmproxy", "mcpproxy", "agentproxy":
		// Update deployment status based on ack
		if ack.DeploymentID == "" {
			log.Warn("DeploymentAckHandler: missing deploymentID in ack, skipping status update")
			return
		}

		var status models.DeploymentStatus
		switch {
		case ack.Action == "deploy" && ack.Status == "success":
			status = models.DeploymentStatusDeployed
		case ack.Action == "undeploy" && ack.Status == "success":
			status = models.DeploymentStatusUndeployed
		case ack.Action == "deploy" && ack.Status == "failed":
			// Deploy failed — mark as undeployed so the user knows the deployment didn't succeed
			status = models.DeploymentStatusUndeployed
		case ack.Action == "undeploy" && ack.Status == "failed":
			// Undeploy failed — keep as deployed since the artifact is still running on the gateway
			status = models.DeploymentStatusDeployed
		default:
			log.Debug("DeploymentAckHandler: no status update needed for ack")
			return
		}

		if _, err := h.deploymentRepo.UpdateStatusByDeploymentID(ack.DeploymentID, gatewayID, status); err != nil {
			log.Error("DeploymentAckHandler: failed to update deployment status", "targetStatus", status, "error", err)
		} else {
			log.Info("DeploymentAckHandler: deployment status updated", "newStatus", status)
		}

		if ack.ResourceType == "agentproxy" && ack.Action == "deploy" && ack.Status == "failed" {
			h.recordA2ACardRejection(ack, log)
		}
	default:
		log.Debug("DeploymentAckHandler: skipping ack for unsupported resource type")
	}
}

// recordA2ACardRejection marks the publication row whose outstanding card
// publish the gateway just refused.
//
// Matching is on deployment ID alone: it is unique per publish, so an ack for a
// superseded publish matches no row, and a card-less first deploy left the
// column null so its rejection — a routing failure — is not mistaken for a card
// failure. The stored card is deliberately preserved: it is what an operator
// needs in order to see why.
//
// The ack path carries no context, so this uses a background one. It is a single
// short update whose caller is a WebSocket read loop with nothing to cancel it.
func (h *DeploymentAckHandler) recordA2ACardRejection(ack models.DeploymentAckPayload, log *slog.Logger) {
	deploymentID, err := uuid.Parse(ack.DeploymentID)
	if err != nil {
		log.Warn("DeploymentAckHandler: agentproxy ack carries an unparseable deployment ID, skipping card rejection")
		return
	}
	if err := h.pubRepo.MarkCardRejected(context.Background(), deploymentID, ack.ErrorCode); err != nil {
		log.Error("DeploymentAckHandler: failed to record the A2A card rejection", "error", err)
	}
}
