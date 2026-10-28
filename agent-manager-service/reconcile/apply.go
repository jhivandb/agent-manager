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

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// Override is what a request deploys in place of what the kind renders.
type Override struct {
	// Name, when set, names the deployments.
	Name string
	// Content, when set, is deployed as is instead of rendering the artifact: one the caller
	// rendered from the state it holds, or the content of an earlier deployment, BaseDeploymentID.
	Content          []byte
	BaseDeploymentID *uuid.UUID
	Metadata         map[string]interface{}
}

// deploysAsIs reports whether the override's content replaces rendering.
func (o *Override) deploysAsIs() bool {
	return o != nil && (o.Content != nil || o.BaseDeploymentID != nil)
}

// Applied is an intent a request carried out in its own transaction. Its events go out with
// Publish once that transaction has committed.
type Applied struct {
	// Intent is the intent as stored.
	Intent models.DeployIntent
	// Deployments are the deployments written, one per target gateway; none for an undeploy, a
	// restore, or an intent waiting for an input.
	Deployments []*models.Deployment
	// Waiting is what the intent waits for when an input was not ready; it stays pending and the
	// worker deploys it once the input arrives.
	Waiting string

	core    *Core
	kind    Kind
	event   func(EventRef) Event
	sent    []sent
	targets []uuid.UUID
}

// Apply records intent in tx (nil: its own transaction) and carries it out there: it resolves
// the targets, renders the artifact (or deploys override's base content), writes the deployments in
// DEPLOYING and marks the intent done, or moves the artifact's rows to UNDEPLOYING for an intent
// to undeploy. A Targets or Render error is returned with nothing written, so the caller's
// transaction rolls back whole; an input that is not ready leaves the intent pending instead.
// Call Publish on the result once tx has committed.
//
// Render reads what is committed: call Apply after the edited state it renders is saved.
func (c *Core) Apply(ctx context.Context, tx *gorm.DB, intent *models.DeployIntent, override *Override) (*Applied, error) {
	kind, err := c.kind(intent.Kind)
	if err != nil {
		return nil, err
	}
	if tx != nil {
		return c.apply(ctx, tx, kind, intent, override)
	}
	var applied *Applied
	err = c.tx(ctx, func(tx *gorm.DB) error {
		applied, err = c.apply(ctx, tx, kind, intent, override)
		return err
	})
	if err != nil {
		return nil, err
	}
	return applied, nil
}

func (c *Core) apply(ctx context.Context, tx *gorm.DB, kind Kind, intent *models.DeployIntent, override *Override) (*Applied, error) {
	intents := c.intents.WithTx(tx)
	if err := intents.Upsert(ctx, intent); err != nil {
		return nil, fmt.Errorf("failed to record the deploy intent: %w", err)
	}
	applied := &Applied{Intent: *intent, core: c, kind: kind}
	if intent.Desired == models.DeploymentStatusUndeployed {
		rows, err := c.intents.StatusRows(ctx, *intent)
		if err != nil {
			return nil, fmt.Errorf("failed to read the artifact's deployments: %w", err)
		}
		applied.Intent, applied.sent, err = c.beginUndeploys(ctx, tx, *intent, rows)
		applied.event = kind.UndeployEvent
		return applied, err
	}

	targets, rendered, err := c.render(ctx, kind, *intent, override)
	if errors.Is(err, ErrInputNotReady) {
		applied.Waiting = c.describe(*intent, err)
		if err := intents.MarkWaiting(ctx, *intent, applied.Waiting, c.now().Add(c.cfg.RetryIn)); err != nil {
			return nil, fmt.Errorf("failed to record that the deploy intent waits: %w", err)
		}
		return applied, nil
	}
	if err != nil {
		return nil, err
	}
	applied.Intent, applied.Deployments, err = c.writeDeployments(ctx, tx, *intent, targets, rendered, override)
	if err != nil {
		return nil, err
	}
	applied.event, applied.targets = kind.DeployEvent, targets
	for _, d := range applied.Deployments {
		applied.sent = append(applied.sent, sent{d.GatewayUUID, d.DeploymentID.String(), *d.PerformedAt})
	}
	return applied, nil
}

// render resolves the intent's targets and renders the artifact for each, or takes override's
// content for all of them.
func (c *Core) render(ctx context.Context, kind Kind, intent models.DeployIntent, override *Override) ([]uuid.UUID, []Rendered, error) {
	targets, err := kind.Targets(ctx, intent)
	if err != nil {
		return nil, nil, err
	}
	if len(targets) == 0 {
		return nil, nil, errors.New("the artifact has no gateway to deploy to")
	}
	rendered := make([]Rendered, len(targets))
	for i, gatewayID := range targets {
		if override.deploysAsIs() {
			rendered[i] = Rendered{Content: override.Content}
		} else if rendered[i], err = kind.Render(ctx, intent, gatewayID); err != nil {
			return nil, nil, err
		}
		if override != nil && override.Name != "" {
			rendered[i].Name = override.Name
		}
	}
	return targets, rendered, nil
}

// Restore records intent in tx (nil: its own transaction) and points its artifact's row on
// gatewayID back at an earlier deployment, DEPLOYING. Call Publish on the result once tx has
// committed: the gateway is sent that deployment again.
func (c *Core) Restore(ctx context.Context, tx *gorm.DB, intent *models.DeployIntent, gatewayID, deploymentID uuid.UUID) (*Applied, error) {
	kind, err := c.kind(intent.Kind)
	if err != nil {
		return nil, err
	}
	restore := func(tx *gorm.DB) (*Applied, error) {
		intents := c.intents.WithTx(tx)
		if err := intents.Upsert(ctx, intent); err != nil {
			return nil, fmt.Errorf("failed to record the deploy intent: %w", err)
		}
		done, err := intents.MarkDone(ctx, *intent)
		if err != nil {
			return nil, err
		}
		performedAt, err := c.deployments.WithTx(tx).SetCurrentCtx(ctx, intent.ArtifactUUID.String(), intent.OUID,
			gatewayID.String(), deploymentID.String(), models.DeploymentStatusDeploying)
		if err != nil {
			return nil, fmt.Errorf("failed to restore deployment %s: %w", deploymentID, err)
		}
		return &Applied{
			Intent: done, core: c, kind: kind, event: kind.DeployEvent,
			sent: []sent{{gatewayID, deploymentID.String(), performedAt}},
		}, nil
	}
	if tx != nil {
		return restore(tx)
	}
	var applied *Applied
	err = c.tx(ctx, func(tx *gorm.DB) error {
		applied, err = restore(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return applied, nil
}

// Publish sends the events of what was applied; call it once the transaction has committed. A
// failure is left to the worker, which sends again what the gateway has not acked.
func (a *Applied) Publish(ctx context.Context) {
	if a == nil || len(a.sent) == 0 {
		return
	}
	a.core.publish(ctx, a.kind, a.Intent, a.event, a.sent)
	if len(a.targets) > 0 {
		a.core.withdrawIfGone(ctx, a.kind, a.Intent, a.targets)
	}
}

// PerformedAt is when the transition applied to gatewayID began, which its event carries; false
// when nothing was applied there.
func (a *Applied) PerformedAt(gatewayID uuid.UUID) (time.Time, bool) {
	for _, s := range a.sent {
		if s.gatewayID == gatewayID {
			return s.performedAt, true
		}
	}
	return time.Time{}, false
}

// ScopeFor is the scope of the kind's intent that places the artifact on gatewayID: an intent
// scoped to that gateway, or an environment intent whose deployment is there. found is false when
// no intent does, and the scope is then the gateway's own.
func (c *Core) ScopeFor(ctx context.Context, kind, ouID string, artifactUUID, gatewayID uuid.UUID) (scope models.DeployScope, found bool, err error) {
	intents, err := c.intents.List(ctx, repositories.DeployIntentFilter{Kind: kind, OUID: ouID, ArtifactUUID: &artifactUUID})
	if err != nil {
		return models.DeployScope{}, false, fmt.Errorf("failed to read the artifact's deploy intents: %w", err)
	}
	for _, intent := range intents {
		if intent.GatewayUUID != nil && *intent.GatewayUUID == gatewayID {
			return intent.Scope(), true, nil
		}
	}
	for _, intent := range intents {
		if intent.EnvironmentUUID == nil {
			continue
		}
		rows, err := c.intents.StatusRows(ctx, intent)
		if err != nil {
			return models.DeployScope{}, false, fmt.Errorf("failed to read the artifact's deployments: %w", err)
		}
		for _, row := range rows {
			if row.GatewayUUID == gatewayID {
				return intent.Scope(), true, nil
			}
		}
	}
	return models.GatewayScope(gatewayID), false, nil
}

// OpenStatuses is the status, by gateway, of the kind's intents for the artifact that are not
// done: a redeploy still retrying shows DEPLOYING, one that gave up FAILED, each with its reason.
// A done intent's deployment_status rows speak for themselves. An environment intent covers the
// gateways the artifact is placed on.
func (c *Core) OpenStatuses(ctx context.Context, kind, ouID string, artifactUUID uuid.UUID) (map[uuid.UUID]Status, error) {
	intents, err := c.intents.List(ctx, repositories.DeployIntentFilter{Kind: kind, OUID: ouID, ArtifactUUID: &artifactUUID})
	if err != nil {
		return nil, fmt.Errorf("failed to read the artifact's deploy intents: %w", err)
	}
	open := map[uuid.UUID]Status{}
	for _, intent := range intents {
		if intent.State == models.DeployIntentDone {
			continue
		}
		status, err := c.Status(ctx, intent)
		if err != nil {
			return nil, err
		}
		if intent.GatewayUUID != nil {
			open[*intent.GatewayUUID] = status
			continue
		}
		rows, err := c.intents.StatusRows(ctx, intent)
		if err != nil {
			return nil, fmt.Errorf("failed to read the artifact's deployments: %w", err)
		}
		for _, row := range rows {
			if row.StatusDesired == models.DeploymentStatusDeployed {
				open[row.GatewayUUID] = status
			}
		}
	}
	return open, nil
}
