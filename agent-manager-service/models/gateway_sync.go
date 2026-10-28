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

package models

import (
	"time"

	"github.com/google/uuid"
)

// Gateway kind names, as the gateway's startup sync dispatches on them (gateway-controller
// pkg/models ArtifactKind).
const (
	GatewayKindLlmProvider = "LlmProvider"
	GatewayKindLlmProxy    = "LlmProxy"
	GatewayKindMcp         = "Mcp"
	GatewayKindAgent       = "Agent"
)

// GatewayArtifactRef is an artifact as AMS stores it, which the gateway knows under its own kind
// name and id.
type GatewayArtifactRef struct {
	ArtifactUUID   uuid.UUID `gorm:"column:artifact_uuid"`
	ArtifactHandle string    `gorm:"column:artifact_handle"`
	ArtifactKind   string    `gorm:"column:artifact_kind"`
}

// GatewayKind is the gateway's name for the artifact's kind; ok is false for a kind AMS never
// deploys to a gateway.
func (r GatewayArtifactRef) GatewayKind() (kind string, ok bool) {
	switch r.ArtifactKind {
	case KindLLMProvider:
		return GatewayKindLlmProvider, true
	case KindLLMProxy:
		return GatewayKindLlmProxy, true
	case KindMCPProxy, KindMCPMapping:
		return GatewayKindMcp, true
	case KindAgent:
		return GatewayKindAgent, true
	}
	return "", false
}

// GatewayArtifactID is the id the gateway stores the artifact under, which is the id its events
// carry: an LLM proxy's handle, and the UUID of everything else.
func (r GatewayArtifactRef) GatewayArtifactID() string {
	if r.ArtifactKind == KindLLMProxy {
		return r.ArtifactHandle
	}
	return r.ArtifactUUID.String()
}

// GatewaySyncDeployment is one of a gateway's deployment_status rows, as its startup sync lists it.
type GatewaySyncDeployment struct {
	GatewayArtifactRef
	DeploymentID  uuid.UUID        `gorm:"column:deployment_id"`
	StatusDesired DeploymentStatus `gorm:"column:status_desired"`
	PerformedAt   time.Time        `gorm:"column:performed_at"`
}

// GatewaySyncContent is the stored content of a deployment the gateway fetches in its startup sync.
type GatewaySyncContent struct {
	GatewayArtifactRef
	DeploymentID uuid.UUID `gorm:"column:deployment_id"`
	Content      []byte    `gorm:"column:content"`
}

// ControlPlaneDeployment is one entry of GET /api/internal/v1/deployments, which the gateway
// diffs against what it holds (gateway-controller pkg/models ControlPlaneDeployment).
type ControlPlaneDeployment struct {
	ArtifactID   string    `json:"artifactId"`
	DeploymentID string    `json:"deploymentId"`
	Kind         string    `json:"kind"`
	State        string    `json:"state"`
	DeployedAt   time.Time `json:"deployedAt"`
	Etag         string    `json:"etag"`
}

// ControlPlaneDeploymentsResponse is the body of GET /api/internal/v1/deployments.
type ControlPlaneDeploymentsResponse struct {
	Deployments []ControlPlaneDeployment `json:"deployments"`
}

// DeploymentsBatchFetchRequest is the body of POST /api/internal/v1/deployments/fetch-batch.
type DeploymentsBatchFetchRequest struct {
	DeploymentIDs []string `json:"deploymentIds"`
}

// ArtifactsExistRequest is the body of POST /api/internal/v1/artifacts/exists.
type ArtifactsExistRequest struct {
	ArtifactIDs []string `json:"artifactIds"`
}

// ArtifactExistence answers for one id of an ArtifactsExistRequest.
type ArtifactExistence struct {
	ArtifactID string `json:"artifactId"`
	Exists     bool   `json:"exists"`
}

// ArtifactsExistResponse is the body POST /api/internal/v1/artifacts/exists answers with.
type ArtifactsExistResponse struct {
	Artifacts []ArtifactExistence `json:"artifacts"`
}
