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

// Deployment represents an immutable artifact deployment
// Status and UpdatedAt are populated from deployment_status table via JOIN
// If Status is nil, the deployment is ARCHIVED (not currently active or undeployed)
type Deployment struct {
	DeploymentID     uuid.UUID              `gorm:"column:deployment_id;primaryKey" json:"deploymentId"`
	Name             string                 `gorm:"column:name" json:"name"`
	ArtifactUUID     uuid.UUID              `gorm:"column:artifact_uuid" json:"artifactId"`
	OUID             string                 `gorm:"column:ou_id" json:"organizationId"`
	GatewayUUID      uuid.UUID              `gorm:"column:gateway_uuid" json:"gatewayId"`
	BaseDeploymentID *uuid.UUID             `gorm:"column:base_deployment_id" json:"baseDeploymentId,omitempty"`
	Content          []byte                 `gorm:"column:content" json:"-"`
	Metadata         map[string]interface{} `gorm:"column:metadata;type:text;serializer:json" json:"metadata,omitempty"`
	CreatedAt        time.Time              `gorm:"column:created_at" json:"createdAt"`

	// Lifecycle state fields (from deployment_status table via JOIN)
	// nil values indicate ARCHIVED state (no record in status table)
	// Read-only fields - populated via JOIN, never inserted/updated directly
	Status        *DeploymentStatus `gorm:"column:status;->" json:"status,omitempty"`
	StatusDesired *DeploymentStatus `gorm:"column:status_desired;->" json:"statusDesired,omitempty"`
	StatusReason  *string           `gorm:"column:status_reason;->" json:"statusReason,omitempty"`
	UpdatedAt     *time.Time        `gorm:"column:status_updated_at;->" json:"updatedAt,omitempty"`
	// PerformedAt is when the row's current transition began, to the millisecond. Events carry it
	// as performedAt and the gateway echoes it in its ack.
	PerformedAt *time.Time `gorm:"column:performed_at;->" json:"-"`
}

// IsPlaced reports whether this deployment is the one its gateway is meant to hold:
// desired DEPLOYED, whether it is still DEPLOYING, already DEPLOYED, or FAILED.
// Placement, URLs, keys and undeploy targets follow placed deployments.
func (d *Deployment) IsPlaced() bool {
	if d.Status == nil {
		return false
	}
	if d.StatusDesired != nil {
		return *d.StatusDesired == DeploymentStatusDeployed
	}
	return d.Status.ImpliedDesired() == DeploymentStatusDeployed
}

// IsUndeployable reports whether an undeploy may start from this deployment's row: it is placed,
// or a previous undeploy of it FAILED and is being retried.
func (d *Deployment) IsUndeployable() bool {
	if d.IsPlaced() {
		return true
	}
	return d.Status != nil && *d.Status == DeploymentStatusFailed &&
		d.StatusDesired != nil && *d.StatusDesired == DeploymentStatusUndeployed
}

// IsLive reports whether the gateway has acknowledged holding this deployment.
func (d *Deployment) IsLive() bool {
	return d.Status != nil && *d.Status == DeploymentStatusDeployed
}

// TableName returns the table name for the Deployment model
func (Deployment) TableName() string {
	return "deployments"
}

// DeploymentStatus represents the status of a deployment
// Note: ARCHIVED is a derived state (not stored in database)
type DeploymentStatus string

// DEPLOYING and UNDEPLOYING are written when AMS sends the event; the gateway's ack
// moves them to DEPLOYED, UNDEPLOYED or FAILED, and the timeout sweeper moves rows
// that never get an ack to FAILED.
const (
	DeploymentStatusDeploying   DeploymentStatus = "DEPLOYING"
	DeploymentStatusDeployed    DeploymentStatus = "DEPLOYED"
	DeploymentStatusUndeploying DeploymentStatus = "UNDEPLOYING"
	DeploymentStatusUndeployed  DeploymentStatus = "UNDEPLOYED"
	DeploymentStatusFailed      DeploymentStatus = "FAILED"
	DeploymentStatusArchived    DeploymentStatus = "ARCHIVED" // Derived state: exists in history but not in status table
)

// NewPerformedAt is the performed_at of a transition starting now. It has the millisecond precision
// the gateway keeps, so the value an event carries is the one an ack echoes and the row stores.
func NewPerformedAt() time.Time {
	return time.Now().Truncate(time.Millisecond)
}

// DeploymentReasonTimeout is the status_reason the sweeper records on a row that got no ack in time.
const DeploymentReasonTimeout = "DEPLOYMENT_TIMEOUT"

// AwaitsAck reports whether a deployment_status row in this status still takes a gateway ack: its
// transition is in flight, or the sweeper failed it and a late ack would still settle it.
func AwaitsAck(status DeploymentStatus, reason string) bool {
	switch status {
	case DeploymentStatusDeploying, DeploymentStatusUndeploying:
		return true
	case DeploymentStatusFailed:
		return reason == DeploymentReasonTimeout
	}
	return false
}

// IsValidDeploymentStatusFilter reports whether s is a status a deployment listing can filter on.
func IsValidDeploymentStatusFilter(s string) bool {
	switch DeploymentStatus(s) {
	case DeploymentStatusDeploying, DeploymentStatusDeployed, DeploymentStatusUndeploying,
		DeploymentStatusUndeployed, DeploymentStatusFailed, DeploymentStatusArchived:
		return true
	}
	return false
}

// ImpliedDesired is the desired state a row in this status must have, or "" for FAILED, which can follow either.
func (s DeploymentStatus) ImpliedDesired() DeploymentStatus {
	switch s {
	case DeploymentStatusDeploying, DeploymentStatusDeployed:
		return DeploymentStatusDeployed
	case DeploymentStatusUndeploying, DeploymentStatusUndeployed:
		return DeploymentStatusUndeployed
	}
	return ""
}

// DeploymentStatusRecord represents the current deployment status record
type DeploymentStatusRecord struct {
	ArtifactUUID  uuid.UUID        `gorm:"column:artifact_uuid;primaryKey" json:"artifactId"`
	OUID          string           `gorm:"column:ou_id;primaryKey" json:"organizationId"`
	GatewayUUID   uuid.UUID        `gorm:"column:gateway_uuid;primaryKey" json:"gatewayId"`
	DeploymentID  uuid.UUID        `gorm:"column:deployment_id" json:"deploymentId"`
	Status        DeploymentStatus `gorm:"column:status" json:"status"`
	StatusDesired DeploymentStatus `gorm:"column:status_desired" json:"statusDesired"`
	// StatusReason is the ack's error code, the timeout reason, or empty.
	StatusReason string `gorm:"column:status_reason" json:"statusReason,omitempty"`
	// PerformedAt is when the current transition was started; the timeout sweeper measures from it.
	PerformedAt time.Time `gorm:"column:performed_at" json:"performedAt"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// DeploymentAck is a gateway's acknowledgement of a deploy or undeploy event, reduced to what the status update needs.
type DeploymentAck struct {
	// DeploymentID is the id the event carried; empty for an undeploy sent while the gateway's version was unknown.
	DeploymentID string
	// ArtifactID is the id the gateway stores the artifact under: its UUID, or an LLM proxy's handle.
	ArtifactID string
	// Action is DeploymentAckActionDeploy or DeploymentAckActionUndeploy.
	Action    string
	Success   bool
	ErrorCode string
	// PerformedAt is the event's performedAt the gateway echoes; zero when it sent none.
	PerformedAt time.Time
}

// Deployment ack actions, as the gateway reports them.
const (
	DeploymentAckActionDeploy   = "deploy"
	DeploymentAckActionUndeploy = "undeploy"
)

// TableName returns the table name for the DeploymentStatusRecord model
func (DeploymentStatusRecord) TableName() string {
	return "deployment_status"
}
