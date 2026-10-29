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
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/orgctx"
)

// Worker carries out what requests leave to the background: intents still pending (waiting for
// an input, retrying, or interrupted between commit and publish), events the gateway never acked,
// AfterLive hooks, and drift checks. Every replica runs one; claims keep them off each other's rows.
type Worker struct {
	core     *Core
	stopCh   chan struct{}
	stopOnce sync.Once
	nudge    chan struct{}
	// driftCursors is the last intent id each kind's drift scan checked.
	driftCursors map[string]uuid.UUID
}

// NewWorker creates a Worker for core.
func NewWorker(core *Core) *Worker {
	return &Worker{
		core:         core,
		stopCh:       make(chan struct{}),
		nudge:        make(chan struct{}, 1),
		driftCursors: map[string]uuid.UUID{},
	}
}

// Start runs a cycle at once and then every tick, in the background, until ctx is cancelled or
// Stop is called.
func (w *Worker) Start(ctx context.Context) error {
	go w.runLoop(ctx)
	w.core.logger.Info("Deploy intent worker started", "tick", w.core.cfg.TickInterval)
	return nil
}

// Stop stops the loop Start launched.
func (w *Worker) Stop() error {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.core.logger.Info("Deploy intent worker stopped")
	})
	return nil
}

// Nudge runs a cycle soon instead of at the next tick, e.g. once a request has recorded an intent.
// It never blocks.
func (w *Worker) Nudge() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

// ResendUnacked sends again, soon, the event of every deployment on the gateway that still awaits
// its ack, with a fresh budget of sends. The gateway's startup sync applies what it is missing but
// acks nothing, and it skips as stale an event it already holds, so only a later event can settle
// what was in flight, or timed out, while it was away.
func (w *Worker) ResendUnacked(ctx context.Context, gatewayID string) {
	rearmed, err := w.core.intents.RearmUnacked(ctx, gatewayID)
	if err != nil {
		w.core.logger.Error("Failed to rearm the unacked deployments of a starting gateway", "gatewayID", gatewayID, "error", err)
		return
	}
	if rearmed > 0 {
		w.core.logger.Info("Sending the unacked deployments of a starting gateway again", "gatewayID", gatewayID, "intents", rearmed)
		w.Nudge()
	}
}

func (w *Worker) runLoop(ctx context.Context) {
	ticker := time.NewTicker(w.core.cfg.TickInterval)
	defer ticker.Stop()
	for {
		w.RunOnce(ctx)
		select {
		case <-ticker.C:
		case <-w.nudge:
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// RunOnce runs one cycle: the due intents, then the unacked events, the AfterLive hooks and one
// page of drift checks. Tests and tools call it to advance the worker deterministically.
func (w *Worker) RunOnce(ctx context.Context) {
	w.processDue(ctx)
	w.republishUnacked(ctx)
	w.runAfterLive(ctx)
	w.checkDrift(ctx)
}

func (w *Worker) processDue(ctx context.Context) {
	c := w.core
	claimedAt := c.now()
	due, err := c.intents.ClaimDue(ctx, claimedAt, c.cfg.Batch)
	if err != nil {
		c.logger.Error("Failed to claim due deploy intents", "error", err)
		return
	}
	w.drainWithinLease(ctx, claimedAt.Add(c.cfg.BatchBudget), due)
}

// drainWithinLease processes each intent, Config.Concurrency at a time, and starts none once the
// batch budget is spent, so another replica never re-claims an intent mid-attempt.
func (w *Worker) drainWithinLease(ctx context.Context, deadline time.Time, due []models.DeployIntent) {
	batchCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait()
	slots := make(chan struct{}, max(w.core.cfg.Concurrency, 1))
	for i, intent := range due {
		slots <- struct{}{}
		if batchCtx.Err() != nil {
			w.core.logger.Info("Deploy intent batch ran out of lease; leaving the rest for a later claim", "remaining", len(due)-i)
			return
		}
		wg.Go(func() {
			defer func() { <-slots }()
			w.core.Process(batchCtx, intent)
		})
	}
}

// republishUnacked sends again the event of a deployment the gateway has not acked: once when it
// never went out (the publish failed, or the process died after the commit), and once more when
// the gateway still has not answered. The timeout sweeper takes it from there, and ResendUnacked
// starts over when the gateway starts.
func (w *Worker) republishUnacked(ctx context.Context) {
	c := w.core
	now := c.now()
	claimed, err := c.intents.ClaimUnacked(ctx, now, now.Add(-c.cfg.RepublishAfter), c.cfg.Batch)
	if err != nil {
		c.logger.Error("Failed to claim unacked deployments", "error", err)
		return
	}
	for _, intent := range claimed {
		kind, err := c.kind(intent.Kind)
		if err != nil {
			c.logger.Error("Unacked deployment of an unknown kind", intentAttrs(intent, "error", err)...)
			continue
		}
		rows, err := c.intents.StatusRows(ctx, intent)
		if err != nil {
			c.logger.Error("Failed to read an unacked deployment", intentAttrs(intent, "error", err)...)
			continue
		}
		for _, row := range rows {
			if row.StatusDesired != intent.Desired || !models.AwaitsAck(row.Status, row.StatusReason) {
				continue
			}
			// The gateway skips without an ack an event no newer than what it holds, and it may hold
			// this one already: it applied it but the ack was lost, or its startup sync applied it.
			performedAt := c.now().Truncate(time.Millisecond)
			restamped, err := c.deployments.RestampUnacked(ctx, row, performedAt)
			if err != nil {
				c.logger.Warn("Failed to re-time an unacked deployment", intentAttrs(intent, "gatewayID", row.GatewayUUID, "error", err)...)
				continue
			}
			if !restamped {
				continue
			}
			ref := EventRef{Intent: intent, GatewayID: row.GatewayUUID.String(), PerformedAt: performedAt}
			event := kind.UndeployEvent
			if intent.Desired == models.DeploymentStatusDeployed {
				ref.DeploymentID, event = row.DeploymentID.String(), kind.DeployEvent
			}
			// An undeploy is sent again without an id: which version the gateway holds is unknown.
			c.logger.Info("Sending an unacked deployment event again",
				intentAttrs(intent, "gatewayID", ref.GatewayID, "deploymentID", row.DeploymentID, "send", intent.PublishCount)...)
			if err := c.publisher.PublishGatewayEvent(ctx, ref.GatewayID, event(ref)); err != nil {
				c.logger.Warn("Failed to send an unacked deployment event again",
					intentAttrs(intent, "gatewayID", ref.GatewayID, "error", err)...)
			}
		}
	}
}

// runAfterLive runs the AfterLive hook of each deployment a gateway acked since the last cycle.
func (w *Worker) runAfterLive(ctx context.Context) {
	c := w.core
	var kinds []string
	for name, kind := range c.kinds {
		if _, ok := kind.(AfterLiveHook); ok {
			kinds = append(kinds, name)
		}
	}
	if len(kinds) == 0 {
		return
	}
	claimed, err := c.intents.ClaimLive(ctx, kinds, c.cfg.Batch)
	if err != nil {
		c.logger.Error("Failed to claim acked deployments", "error", err)
		return
	}
	for _, intent := range claimed {
		if intent.LiveDeploymentID == nil {
			continue
		}
		rows, err := c.intents.StatusRows(ctx, intent)
		if err != nil {
			c.logger.Error("Failed to read an acked deployment", intentAttrs(intent, "error", err)...)
			continue
		}
		for _, row := range rows {
			if row.DeploymentID != *intent.LiveDeploymentID {
				continue
			}
			hookCtx := orgctx.WithResolvedOrg(ctx, orgctx.ResolvedOrg{OUID: intent.OUID})
			c.kinds[intent.Kind].(AfterLiveHook).AfterLive(hookCtx, intent, row.GatewayUUID, row.DeploymentID)
		}
	}
}

// checkDrift checks one page of each drift-checking kind's deployed intents, resuming after it on
// the next cycle and wrapping at the end, and redeploys those whose inputs moved.
func (w *Worker) checkDrift(ctx context.Context) {
	c := w.core
	for name, kind := range c.kinds {
		checker, ok := kind.(DriftChecker)
		if !ok {
			continue
		}
		intents, err := c.intents.ListDone(ctx, name, w.driftCursors[name], c.cfg.Batch)
		if err != nil {
			c.logger.Error("Failed to list deployed intents for drift", "kind", name, "error", err)
			continue
		}
		if len(intents) < c.cfg.Batch {
			w.driftCursors[name] = uuid.Nil
		} else {
			w.driftCursors[name] = intents[len(intents)-1].ID
		}
		for _, intent := range intents {
			intentCtx := orgctx.WithResolvedOrg(ctx, orgctx.ResolvedOrg{OUID: intent.OUID})
			reason, drifted := checker.Drifted(intentCtx, intent)
			if !drifted {
				continue
			}
			// A failed attempt is recorded on the intent and retried; only log it.
			if err := c.markStale(intentCtx, intent, reason); err != nil && !errors.Is(err, ErrInputNotReady) {
				c.logger.Warn("Failed to redeploy a drifted artifact", intentAttrs(intent, "error", err)...)
			}
		}
	}
}
