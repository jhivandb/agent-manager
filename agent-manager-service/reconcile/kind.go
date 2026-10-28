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

// Package reconcile carries out deploy intents: it deploys a gateway artifact to its gateways
// and keeps its deployment_status, whatever its kind. A kind supplies only what is specific to
// it, through Kind and the optional hooks next to it.
package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// ErrInputNotReady means an input the artifact is rendered from does not exist yet, such as an
// agent's endpoint before its first build binds. The intent waits for it: the wait is not charged
// to its attempts and never times out. Make one with InputNotReady.
var ErrInputNotReady = errors.New("an input of the artifact is not ready")

// ErrPermanent means retrying cannot fix the failure: the intent fails at once. Make one with
// Permanent.
var ErrPermanent = errors.New("the artifact cannot be deployed")

// InputNotReady is an ErrInputNotReady whose message, reason, says what is awaited.
func InputNotReady(reason string) error {
	return &markedError{msg: reason, mark: ErrInputNotReady}
}

// Permanent marks err as a failure retrying cannot fix, keeping its message.
func Permanent(err error) error {
	return &markedError{msg: err.Error(), mark: ErrPermanent, cause: err}
}

// markedError is an error that is also one of the package's sentinels.
type markedError struct {
	msg   string
	mark  error
	cause error
}

func (e *markedError) Error() string        { return e.msg }
func (e *markedError) Is(target error) bool { return target == e.mark }
func (e *markedError) Unwrap() error        { return e.cause }

// Rendered is one gateway's deployment of an artifact.
type Rendered struct {
	// Name is the deployment's name.
	Name string
	// Content is what the gateway pulls: the artifact's YAML, with secrets left as references.
	Content []byte
}

// Event is a gateway event in the shape the gateway expects for the kind.
type Event struct {
	// Type is the event type, e.g. "agent.deployed".
	Type string
	// Action is the event hub action: CREATE, DELETE.
	Action string
	// EntityID is the id the gateway stores the artifact under.
	EntityID string
	// Payload is the event's payload, marshalled as JSON.
	Payload any
}

// EventRef names the deployment an event is about.
type EventRef struct {
	Intent    models.DeployIntent
	GatewayID string
	// DeploymentID is the deployment's id; an undeploy carries it only when the gateway acked that
	// deployment, and is empty otherwise.
	DeploymentID string
	// PerformedAt is the status row's performed_at; the gateway echoes it in its ack.
	PerformedAt time.Time
}

// Kind is what the core needs from a gateway artifact kind.
type Kind interface {
	// Name is the kind's gateway Kind, the intents' kind: models.KindAgent, models.KindLLMProvider, ...
	Name() string
	// Targets returns the gateways the intent's artifact goes to in its scope.
	Targets(ctx context.Context, intent models.DeployIntent) ([]uuid.UUID, error)
	// Render returns the artifact's deployment for one target gateway, or an ErrInputNotReady
	// while an input it needs is missing. An error marked Permanent fails the intent at once;
	// any other is retried.
	Render(ctx context.Context, intent models.DeployIntent, gatewayID uuid.UUID) (Rendered, error)
	// DeployEvent, UndeployEvent and DeleteEvent build the kind's gateway events.
	DeployEvent(ref EventRef) Event
	UndeployEvent(ref EventRef) Event
	DeleteEvent(ref EventRef) Event
}

// AfterLiveHook is a Kind that acts once a gateway acks one of its deployments.
type AfterLiveHook interface {
	// AfterLive runs once per acked deployment, in the background; it must be best effort.
	AfterLive(ctx context.Context, intent models.DeployIntent, gatewayID, deploymentID uuid.UUID)
}

// DriftChecker is a Kind whose deployed artifacts can go stale because an input changed outside
// any request, such as an agent endpoint that moved.
type DriftChecker interface {
	// Drifted reports whether the intent's current deployment no longer matches its inputs, and why.
	Drifted(ctx context.Context, intent models.DeployIntent) (reason string, drifted bool)
}

// ErrorDescriber is a Kind that says what a failure means to a user. The core stores the text on
// the intent, where readers of the artifact see it; without one they see a generic message.
type ErrorDescriber interface {
	DescribeError(err error) string
}

// FailureHook is a Kind that acts when an intent gives up.
type FailureHook interface {
	AfterFailed(ctx context.Context, intent models.DeployIntent, lastErr string)
}

// Adopter is a Kind whose artifacts may have been deployed before it had intents. When an outside
// input changes for such an artifact, the core records an intent for it from its deployment_status
// row; Adopt fills in the kind data the kind's events and Render need.
type Adopter interface {
	Adopt(ctx context.Context, intent *models.DeployIntent) error
}

// Publisher sends a gateway event to whichever replica holds the gateway's connection.
type Publisher interface {
	PublishGatewayEvent(ctx context.Context, gatewayID string, event Event) error
}
