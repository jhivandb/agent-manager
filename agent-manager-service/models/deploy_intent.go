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
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// DeployIntentState is how far an intent has been carried out.
type DeployIntentState string

const (
	// DeployIntentPending needs reconciling: its artifact is to be rendered and sent to its
	// gateways, or it is waiting for an input to render it.
	DeployIntentPending DeployIntentState = "pending"
	// DeployIntentDone has been handed to deployment_status: the gateway's acks settle it there.
	DeployIntentDone DeployIntentState = "done"
	// DeployIntentFailed ran out of attempts before a deployment could be written; LastError says why.
	DeployIntentFailed DeployIntentState = "failed"
)

// DeployScope is where an intent places its artifact: an environment, whose gateway the kind
// resolves, or the gateway a user chose. Exactly one is set.
type DeployScope struct {
	EnvironmentUUID *uuid.UUID
	GatewayUUID     *uuid.UUID
}

// EnvironmentScope is the scope of an environment-addressed kind.
func EnvironmentScope(environmentUUID uuid.UUID) DeployScope {
	return DeployScope{EnvironmentUUID: &environmentUUID}
}

// GatewayScope is the scope of a gateway-addressed kind.
func GatewayScope(gatewayUUID uuid.UUID) DeployScope {
	return DeployScope{GatewayUUID: &gatewayUUID}
}

// DeployIntent is the durable record that an artifact should be deployed (or undeployed) in its
// scope. It is written with the edited state, and the reconcile worker carries it out: a request
// never waits for the gateway.
type DeployIntent struct {
	ID   uuid.UUID `gorm:"column:id;primaryKey;type:uuid;default:gen_random_uuid()"`
	OUID string    `gorm:"column:ou_id;not null"`
	// Kind is the artifact's gateway kind (KindAgent, KindLLMProvider, ...); it picks the code that
	// renders it.
	Kind            string     `gorm:"column:kind;not null"`
	ArtifactUUID    uuid.UUID  `gorm:"column:artifact_uuid;type:uuid;not null"`
	EnvironmentUUID *uuid.UUID `gorm:"column:environment_uuid;type:uuid"`
	GatewayUUID     *uuid.UUID `gorm:"column:gateway_uuid;type:uuid"`
	// Desired is DeploymentStatusDeployed or DeploymentStatusUndeployed.
	Desired       DeploymentStatus  `gorm:"column:desired;not null"`
	State         DeployIntentState `gorm:"column:state;not null"`
	AttemptCount  int               `gorm:"column:attempt_count;not null;default:0"`
	NextAttemptAt *time.Time        `gorm:"column:next_attempt_at"`
	// LastError is what a pending intent waits for or last failed on, or why a failed one gave up.
	// Readers may see it, so it never holds internal detail.
	LastError string `gorm:"column:last_error;not null;default:''"`
	// PublishCount is how many times the current deployment's event was sent; PublishedAt is the last.
	PublishCount int        `gorm:"column:publish_count;not null;default:0"`
	PublishedAt  *time.Time `gorm:"column:published_at"`
	// LiveDeploymentID is the last deployment the kind's AfterLive hook ran for.
	LiveDeploymentID *uuid.UUID `gorm:"column:live_deployment_id;type:uuid"`
	// KindData is the kind's own data about the intent; the core never reads it.
	KindData  json.RawMessage `gorm:"column:kind_data;type:jsonb;not null;default:'{}'"`
	CreatedAt time.Time       `gorm:"column:created_at;not null;default:NOW()"`
	UpdatedAt time.Time       `gorm:"column:updated_at;not null;default:NOW()"`
}

func (DeployIntent) TableName() string { return "deploy_intents" }

// Scope is the intent's environment or gateway.
func (i DeployIntent) Scope() DeployScope {
	return DeployScope{EnvironmentUUID: i.EnvironmentUUID, GatewayUUID: i.GatewayUUID}
}
