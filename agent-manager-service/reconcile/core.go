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
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/orgctx"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// TxRunner opens one database transaction and runs fn inside it.
type TxRunner func(ctx context.Context, fn func(tx *gorm.DB) error) error

// Config tunes the worker.
type Config struct {
	// TickInterval is how often the worker runs a cycle.
	TickInterval time.Duration
	// RetryIn is when a failed or waiting intent is due again; just under a tick, so the next one takes it.
	RetryIn time.Duration
	// Batch caps how many intents one cycle claims for each of its scans.
	Batch int
	// AttemptBudget is how many failed attempts an intent gets before it fails.
	AttemptBudget int
	// AttemptTimeout bounds one intent's attempt.
	AttemptTimeout time.Duration
	// BatchBudget bounds one claimed batch, inside the claim lease.
	BatchBudget time.Duration
	// Concurrency caps how many intents of a batch run at once.
	Concurrency int
	// RepublishAfter is how long a deployment waits for its ack before its event is sent again.
	RepublishAfter time.Duration
	// DeploymentHardLimit is how many deployments an artifact keeps per gateway.
	DeploymentHardLimit int
}

// DefaultConfig is the worker's configuration: a tick every 30s, and about 15 minutes of failed
// attempts before an intent fails.
func DefaultConfig() Config {
	const tick = 30 * time.Second
	return Config{
		TickInterval:        tick,
		RetryIn:             tick - 5*time.Second,
		Batch:               50,
		AttemptBudget:       30,
		AttemptTimeout:      30 * time.Second,
		BatchBudget:         repositories.DeployIntentClaimLease - time.Minute,
		Concurrency:         4,
		RepublishAfter:      tick,
		DeploymentHardLimit: 25,
	}
}

// internalErrorMessage is what a reader sees of a failure the kind does not describe.
const internalErrorMessage = "deploying to the gateway failed because of an internal error; check the service logs"

// lastErrorMaxRunes keeps a stored last_error readable.
const lastErrorMaxRunes = 1024

// Core carries out deploy intents for every registered kind.
type Core struct {
	tx          TxRunner
	intents     repositories.DeployIntentRepository
	deployments repositories.DeploymentRepository
	publisher   Publisher
	kinds       map[string]Kind
	cfg         Config
	logger      *slog.Logger
	now         func() time.Time
}

// NewCore creates a Core for kinds.
func NewCore(
	tx TxRunner,
	intents repositories.DeployIntentRepository,
	deployments repositories.DeploymentRepository,
	publisher Publisher,
	cfg Config,
	logger *slog.Logger,
	kinds ...Kind,
) *Core {
	byName := make(map[string]Kind, len(kinds))
	for _, kind := range kinds {
		byName[kind.Name()] = kind
	}
	return &Core{
		tx: tx, intents: intents, deployments: deployments, publisher: publisher,
		kinds: byName, cfg: cfg, logger: logger, now: time.Now,
	}
}

// Want records intent in tx (nil: its own transaction): its artifact should be deployed (or
// undeployed) in its scope. The intent is pending and due at once; Process carries it out, and the
// worker does when nobody else does. Call it in the transaction that saves the edited state, so the
// two commit together.
func (c *Core) Want(ctx context.Context, tx *gorm.DB, intent *models.DeployIntent) error {
	if _, err := c.kind(intent.Kind); err != nil {
		return err
	}
	intents := c.intents
	if tx != nil {
		intents = intents.WithTx(tx)
	}
	if err := intents.Upsert(ctx, intent); err != nil {
		return fmt.Errorf("failed to record the deploy intent: %w", err)
	}
	return nil
}

func (c *Core) kind(name string) (Kind, error) {
	kind, ok := c.kinds[name]
	if !ok {
		return nil, fmt.Errorf("no reconcile kind %q is registered", name)
	}
	return kind, nil
}

// Reconcile carries out one pending intent as read: it renders the artifact for each target
// gateway, writes the deployments in DEPLOYING with the intent marked done in one transaction, and
// then sends their events. An undeploy intent moves each of the artifact's rows to UNDEPLOYING
// instead. The error is the attempt's, for the caller to record; Process records it.
func (c *Core) Reconcile(ctx context.Context, intent models.DeployIntent) error {
	kind, err := c.kind(intent.Kind)
	if err != nil {
		return err
	}
	if intent.Desired == models.DeploymentStatusUndeployed {
		return c.undeploy(ctx, kind, intent)
	}
	return c.deploy(ctx, kind, intent)
}

// sent is one deployment whose event goes out once its transaction commits.
type sent struct {
	gatewayID    uuid.UUID
	deploymentID string
	performedAt  time.Time
}

func (c *Core) deploy(ctx context.Context, kind Kind, intent models.DeployIntent) error {
	targets, err := kind.Targets(ctx, intent)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("the artifact has no gateway to deploy to")
	}
	rendered := make([]Rendered, len(targets))
	for i, gatewayID := range targets {
		if rendered[i], err = kind.Render(ctx, intent, gatewayID); err != nil {
			return err
		}
	}

	var done models.DeployIntent
	deployments := make([]sent, 0, len(targets))
	err = c.tx(ctx, func(tx *gorm.DB) error {
		// Marked first: the row lock it takes orders this against a teardown deleting the intent.
		if done, err = c.intents.WithTx(tx).MarkDone(ctx, intent); err != nil {
			return err
		}
		for i, gatewayID := range targets {
			deployment := &models.Deployment{
				DeploymentID: uuid.New(),
				Name:         rendered[i].Name,
				ArtifactUUID: intent.ArtifactUUID,
				OUID:         intent.OUID,
				GatewayUUID:  gatewayID,
				Content:      rendered[i].Content,
			}
			if err := c.deployments.WithTx(tx).CreateWithLimitEnforcement(deployment, c.cfg.DeploymentHardLimit); err != nil {
				return fmt.Errorf("failed to write the deployment for gateway %s: %w", gatewayID, err)
			}
			deployments = append(deployments, sent{gatewayID, deployment.DeploymentID.String(), *deployment.PerformedAt})
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.publish(ctx, kind, done, kind.DeployEvent, deployments)
	c.withdrawIfGone(ctx, kind, done, targets)
	return nil
}

func (c *Core) undeploy(ctx context.Context, kind Kind, intent models.DeployIntent) error {
	rows, err := c.intents.StatusRows(ctx, intent)
	if err != nil {
		return fmt.Errorf("failed to read the artifact's deployments: %w", err)
	}
	var done models.DeployIntent
	var undeployments []sent
	err = c.tx(ctx, func(tx *gorm.DB) error {
		if done, err = c.intents.WithTx(tx).MarkDone(ctx, intent); err != nil {
			return err
		}
		undeployments = undeployments[:0]
		for _, row := range rows {
			gatewayID := row.GatewayUUID.String()
			id, performedAt, err := c.deployments.WithTx(tx).BeginUndeploy(ctx, intent.ArtifactUUID.String(), intent.OUID, gatewayID)
			if errors.Is(err, utils.ErrDeploymentNotActive) {
				continue
			}
			if err != nil {
				return fmt.Errorf("failed to undeploy from gateway %s: %w", gatewayID, err)
			}
			undeployments = append(undeployments, sent{row.GatewayUUID, id, performedAt})
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.publish(ctx, kind, done, kind.UndeployEvent, undeployments)
	return nil
}

// publish sends each deployment's event once its transaction has committed. A failure is left to
// the worker, which sends again what the gateway has not acked; it never fails the caller.
func (c *Core) publish(ctx context.Context, kind Kind, done models.DeployIntent, event func(EventRef) Event, deployments []sent) {
	ctx = context.WithoutCancel(ctx)
	allSent := true
	for _, d := range deployments {
		ref := EventRef{Intent: done, GatewayID: d.gatewayID.String(), DeploymentID: d.deploymentID, PerformedAt: d.performedAt}
		if err := c.publisher.PublishGatewayEvent(ctx, ref.GatewayID, event(ref)); err != nil {
			allSent = false
			c.logger.Warn("Failed to send a deployment event; the worker sends it again",
				intentAttrs(done, "gatewayID", ref.GatewayID, "deploymentID", ref.DeploymentID, "error", err)...)
		}
	}
	if !allSent || len(deployments) == 0 {
		return
	}
	if err := c.intents.MarkPublished(ctx, done); err != nil && !errors.Is(err, repositories.ErrDeployIntentSuperseded) {
		c.logger.Warn("Failed to record that a deployment event was sent", intentAttrs(done, "error", err)...)
	}
}

// withdrawIfGone takes back a deployment sent after its intent was deleted, e.g. by a teardown
// that ran while the deployment's transaction waited on the intent's row.
func (c *Core) withdrawIfGone(ctx context.Context, kind Kind, done models.DeployIntent, targets []uuid.UUID) {
	_, err := c.intents.Get(ctx, done.ID)
	switch {
	case err == nil:
		return
	case !errors.Is(err, repositories.ErrDeployIntentNotFound):
		c.logger.Error("Failed to check whether a deployed intent was torn down meanwhile; its artifact may stay served",
			intentAttrs(done, "error", err)...)
		return
	}
	c.logger.Info("Deploy intent was torn down during the deploy; withdrawing what it sent", intentAttrs(done)...)
	var withdrawals []sent
	for _, gatewayID := range targets {
		id, performedAt, err := c.deployments.BeginUndeploy(ctx, done.ArtifactUUID.String(), done.OUID, gatewayID.String())
		if err != nil && !errors.Is(err, utils.ErrDeploymentNotActive) {
			c.logger.Error("Failed to undeploy the deployment of a torn-down intent",
				intentAttrs(done, "gatewayID", gatewayID, "error", err)...)
		}
		if performedAt.IsZero() {
			performedAt = models.NewPerformedAt()
		}
		withdrawals = append(withdrawals, sent{gatewayID, id, performedAt})
	}
	c.publish(ctx, kind, done, kind.UndeployEvent, withdrawals)
}

// Process runs one attempt at a pending intent as read and records its outcome: done, waiting for
// an input (not charged), failed for now and retried after Config.RetryIn, or failed for good
// once the attempt budget is spent or the failure is permanent.
func (c *Core) Process(ctx context.Context, intent models.DeployIntent) {
	ctx = orgctx.WithResolvedOrg(ctx, orgctx.ResolvedOrg{OUID: intent.OUID})
	attemptCtx, cancel := context.WithTimeout(ctx, c.cfg.AttemptTimeout)
	defer cancel()
	err := c.Reconcile(attemptCtx, intent)
	if err == nil {
		return
	}
	c.recordFailure(context.WithoutCancel(ctx), intent, err)
}

func (c *Core) recordFailure(ctx context.Context, intent models.DeployIntent, cause error) {
	if errors.Is(cause, repositories.ErrDeployIntentSuperseded) {
		c.logSuperseded(intent)
		return
	}
	lastErr := c.describe(intent, cause)
	var err error
	switch {
	case errors.Is(cause, ErrInputNotReady):
		c.logger.Debug("Deploy intent waits for an input", intentAttrs(intent, "reason", lastErr)...)
		err = c.intents.MarkWaiting(ctx, intent, lastErr, c.now().Add(c.cfg.RetryIn))
	case errors.Is(cause, ErrPermanent) || intent.AttemptCount+1 >= c.cfg.AttemptBudget:
		c.logger.Error("Deploy intent cannot be carried out; giving up",
			intentAttrs(intent, "attempts", intent.AttemptCount+1, "error", cause)...)
		if err = c.intents.MarkFailed(ctx, intent, lastErr); err == nil {
			if hook, ok := c.kinds[intent.Kind].(FailureHook); ok {
				hook.AfterFailed(ctx, intent, lastErr)
			}
		}
	default:
		c.logger.Debug("Deploy intent attempt failed; will retry",
			intentAttrs(intent, "attempt", intent.AttemptCount+1, "error", cause)...)
		err = c.intents.MarkAttemptFailed(ctx, intent, lastErr, c.now().Add(c.cfg.RetryIn))
	}
	switch {
	case errors.Is(err, repositories.ErrDeployIntentSuperseded):
		c.logSuperseded(intent)
	case err != nil:
		c.logger.Error("Failed to record a deploy intent's attempt", intentAttrs(intent, "error", err)...)
	}
}

// describe is the last_error a reader may see for cause.
func (c *Core) describe(intent models.DeployIntent, cause error) string {
	msg := internalErrorMessage
	if describer, ok := c.kinds[intent.Kind].(ErrorDescriber); ok {
		msg = describer.DescribeError(cause)
	} else if errors.Is(cause, ErrInputNotReady) {
		msg = cause.Error()
	}
	return sanitizeLastError(msg)
}

// sanitizeLastError makes msg storable in a Postgres TEXT column and bounded in length.
func sanitizeLastError(msg string) string {
	msg = strings.ReplaceAll(strings.ToValidUTF8(msg, "�"), "\x00", "")
	if runes := []rune(msg); len(runes) > lastErrorMaxRunes {
		msg = string(runes[:lastErrorMaxRunes-1]) + "…"
	}
	return msg
}

// logSuperseded notes an attempt whose intent changed while it ran; the newer intent is carried
// out on its own.
func (c *Core) logSuperseded(intent models.DeployIntent) {
	c.logger.Info("Deploy intent changed during the attempt; leaving it to the newer one", intentAttrs(intent)...)
}

// MarkStale re-renders the artifact in its scope because an input outside its edited state
// changed: it requeues the artifact's intent and processes it. An intent to undeploy is left
// alone. It returns repositories.ErrDeployIntentNotFound when the artifact has no intent there.
func (c *Core) MarkStale(ctx context.Context, kind, ouID string, artifactUUID uuid.UUID, scope models.DeployScope, reason string) error {
	intent, err := c.intents.GetByScope(ctx, ouID, artifactUUID, scope)
	if err != nil {
		return err
	}
	if intent.Kind != kind {
		return fmt.Errorf("the artifact's intent is of kind %s, not %s", intent.Kind, kind)
	}
	return c.markStale(ctx, *intent, reason)
}

func (c *Core) markStale(ctx context.Context, intent models.DeployIntent, reason string) error {
	if intent.Desired != models.DeploymentStatusDeployed {
		return nil
	}
	c.logger.Info("Redeploying a stale artifact", intentAttrs(intent, "reason", reason)...)
	err := c.intents.Requeue(ctx, intent)
	if errors.Is(err, repositories.ErrDeployIntentSuperseded) {
		return nil // it changed meanwhile, so it is being carried out anyway
	}
	if err != nil {
		return fmt.Errorf("failed to requeue the deploy intent: %w", err)
	}
	requeued, err := c.intents.Get(ctx, intent.ID)
	if err != nil {
		return fmt.Errorf("failed to read the requeued deploy intent: %w", err)
	}
	c.Process(ctx, *requeued)
	return nil
}

// Status is where an artifact's deployment in one scope stands.
type Status struct {
	Status models.DeploymentStatus
	// Reason is what a deployment waits for, or why it failed; empty otherwise.
	Reason string
}

// StatusOf is the status of the artifact's deployment in scope. It returns
// repositories.ErrDeployIntentNotFound when the artifact has no intent there.
func (c *Core) StatusOf(ctx context.Context, ouID string, artifactUUID uuid.UUID, scope models.DeployScope) (Status, error) {
	intent, err := c.intents.GetByScope(ctx, ouID, artifactUUID, scope)
	if err != nil {
		return Status{}, err
	}
	return c.Status(ctx, *intent)
}

// Status is the status of intent's deployment. An intent not yet carried out speaks for itself:
// pending is DEPLOYING (or UNDEPLOYING) with what it waits for, failed is FAILED with why. Once
// done, the newest deployment_status row of its artifact in its scope says what the gateway holds.
func (c *Core) Status(ctx context.Context, intent models.DeployIntent) (Status, error) {
	switch intent.State {
	case models.DeployIntentPending:
		status := models.DeploymentStatusDeploying
		if intent.Desired == models.DeploymentStatusUndeployed {
			status = models.DeploymentStatusUndeploying
		}
		return Status{Status: status, Reason: intent.LastError}, nil
	case models.DeployIntentFailed:
		return Status{Status: models.DeploymentStatusFailed, Reason: intent.LastError}, nil
	}
	rows, err := c.intents.StatusRows(ctx, intent)
	if err != nil {
		return Status{}, fmt.Errorf("failed to read the deployment status: %w", err)
	}
	if len(rows) == 0 {
		return Status{Status: models.DeploymentStatusUndeployed}, nil
	}
	return Status{Status: rows[0].Status, Reason: rows[0].StatusReason}, nil
}

func intentAttrs(intent models.DeployIntent, args ...any) []any {
	return append([]any{"intentID", intent.ID, "kind", intent.Kind, "artifactID", intent.ArtifactUUID, "orgID", intent.OUID}, args...)
}
