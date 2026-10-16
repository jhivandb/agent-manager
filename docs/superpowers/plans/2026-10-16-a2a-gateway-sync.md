# A2A Gateway Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the A2A publication queue, card queue, publisher and reconcilers with desired-state rows on `deployment_status` that the gateway pulls and acks.

**Architecture:** Request handlers write desired state in their own transaction. One worker turns waiting agent rows into rendered deployments and sends the poke. The gateway's existing startup sync reads three new internal endpoints and converges. Acks and a timeout job are the only writers of observed state. Cards become a cache keyed to the acked deployment.

**Tech Stack:** Go 1.25, gorm + gormigrate, moq mocks (`make codegen`), wire (`make wire`), Postgres integration tests (`//go:build integration`, `make test-integration`), unit tests via `make test-run RUN=<Test> PKG=<pkg>`.

**Spec:** `docs/superpowers/specs/2026-10-16-a2a-gateway-sync-design.md`

**Baseline:** the `a2a-proxy` branch (migration 048, `a2a_proxies`, `A2APublisher`, `a2aProxyService`) merged into `main`. File references below are to that state. Where a name differs on your checkout, the spec's shapes win.

## Global Constraints

- Migrations are forward-only gormigrate steps in `agent-manager-service/db_migrations/`, registered in `migration_list.go`, SQL with `IF NOT EXISTS` where possible. This change ships migration `049` only; `050` is a separate later change.
- Gateway kind strings in the sync list: `Agent`, `Mcp`, `LlmProvider`, `LlmProxy`, `RestApi`. AMS artifact kinds map `Agent→Agent`, `McpProxy→Mcp`, `McpProxyMapping→Mcp`, `LlmProvider→LlmProvider`, `LlmProxy→LlmProxy`, `RestAPI→RestApi`.
- Sync list `state` is lower-case `deployed` or `undeployed`; rows in `WAITING_UPSTREAM` are omitted.
- Batch archive entries are `<deploymentId>/<kind>.yaml`, gzip-compressed tar, content type `application/x-tar+gzip`.
- Timeout job: interval 1 minute, stale after 5 minutes, reason `TIMEOUT`.
- Upstream worker: tick 30s, claim batch 50, drift page 50 every 5 minutes.
- Card cache: at most one fetch attempt per key per 10 seconds across replicas.
- No new attempt counters, leases or next-attempt columns anywhere.
- Commit messages: imperative, capitalised first letter, no trailing period, no prefix (see `git log --no-merges -30`).
- After changing a repository interface run `cd agent-manager-service && make codegen` and stage the regenerated mock. After changing constructors in `wiring/wire.go` run `make wire` and stage `wire_gen.go`.

## Review Focus

1. A gateway with LLM or MCP artifacts restarts after the upgrade: the sync list must include them with correct kinds, or the gateway deletes them. Pinned in Task 3 (`TestListControlPlaneDeploymentsIncludesEveryKind`).
2. A success ack arrives after the timeout job already marked the row `FAILED`: the row must become `DEPLOYED`. Pinned in Task 4 (`TestAckRepairsATimedOutRow`).
3. A stale ack for a superseded deployment arrives: it must not touch the row. Pinned in Task 4 (`TestStaleAckIsDiscarded`).
4. Two replicas tick the upstream worker at once on the same waiting row: exactly one renders and broadcasts. Pinned in Task 2 (`TestClaimWaitingUpstreamIsExclusive`).
5. A read of a card whose gateway row is not `DEPLOYED` must not fetch the origin: pinned in Task 9 (`TestCardReadDoesNotFetchBeforeTheGatewayAcks`).
6. A gateway converges a row through its startup sync, which never acks: the row must end `DEPLOYED`, not `FAILED`. Pinned in Task 4 (`TestTimeoutJobConfirmsASyncedAndFetchedDeploy`).
7. An LLM provider ack must keep working after the handler change. Pinned in Task 4 (`TestLLMProviderAckKeepsLegacyHandling`).

---

### Task 1: Migration 049 and the status model

**Files:**
- Create: `agent-manager-service/db_migrations/049_add_deployment_desired_state.go`
- Modify: `agent-manager-service/db_migrations/migration_list.go` (append `migration049`)
- Modify: `agent-manager-service/models/deployment.go:53-70`
- Modify: `agent-manager-service/models/a2a_agent_card.go`
- Test: `agent-manager-service/models/deployment_status_test.go`

**Interfaces:**
- Produces:
  ```go
  const (
      DeploymentStatusDeploying       DeploymentStatus = "DEPLOYING"
      DeploymentStatusUndeploying     DeploymentStatus = "UNDEPLOYING"
      DeploymentStatusFailed          DeploymentStatus = "FAILED"
      DeploymentStatusWaitingUpstream DeploymentStatus = "WAITING_UPSTREAM"
  )
  const (
      DeploymentReasonTimeout           = "TIMEOUT"
      DeploymentReasonSyncedUnconfirmed = "SYNCED_UNCONFIRMED"
  )
  // On Deployment: FetchedAt *time.Time `gorm:"column:fetched_at"`
  // On Gateway:    LastSyncAt *time.Time `gorm:"column:last_sync_at"`
  // On DeploymentStatusRecord:
  StatusDesired DeploymentStatus `gorm:"column:status_desired"`
  StatusReason  string           `gorm:"column:status_reason"`
  PerformedAt   time.Time        `gorm:"column:performed_at"`
  func (s DeploymentStatus) IsTransitional() bool // DEPLOYING, UNDEPLOYING, WAITING_UPSTREAM
  // On A2AAgentCard:
  ForDeploymentID       *uuid.UUID `gorm:"column:for_deployment_id;type:uuid"`
  AttemptedDeploymentID *uuid.UUID `gorm:"column:attempted_deployment_id;type:uuid"`
  AttemptedAt           *time.Time `gorm:"column:attempted_at"`
  ```

- [ ] **Step 1: Write the failing model test**

```go
func TestTransitionalStatuses(t *testing.T) {
    for _, s := range []models.DeploymentStatus{models.DeploymentStatusDeploying, models.DeploymentStatusUndeploying, models.DeploymentStatusWaitingUpstream} {
        assert.True(t, s.IsTransitional(), s)
    }
    for _, s := range []models.DeploymentStatus{models.DeploymentStatusDeployed, models.DeploymentStatusUndeployed, models.DeploymentStatusFailed} {
        assert.False(t, s.IsTransitional(), s)
    }
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd agent-manager-service && make test-run RUN=TestTransitionalStatuses PKG=./models/`
Expected: FAIL, undefined constants.

- [ ] **Step 3: Add the constants, fields and `IsTransitional` in `models/deployment.go`, `FetchedAt` on `Deployment`, `LastSyncAt` on `Gateway` in `models/gateway.go`, and the three card fields in `models/a2a_agent_card.go`**

- [ ] **Step 4: Write migration 049**

The migration runs these statements in one transaction, in order:

```sql
ALTER TABLE deployment_status
    ADD COLUMN IF NOT EXISTS status_desired VARCHAR(20) NOT NULL DEFAULT 'DEPLOYED',
    ADD COLUMN IF NOT EXISTS status_reason  TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS performed_at   TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP;
UPDATE deployment_status SET status_desired = status, performed_at = COALESCE(updated_at, CURRENT_TIMESTAMP);
ALTER TABLE deployment_status DROP CONSTRAINT IF EXISTS chk_deployment_status;
ALTER TABLE deployment_status ADD CONSTRAINT chk_deployment_status
    CHECK (status IN ('DEPLOYED','UNDEPLOYED','DEPLOYING','UNDEPLOYING','FAILED','WAITING_UPSTREAM'));
ALTER TABLE deployment_status ADD CONSTRAINT chk_deployment_status_desired
    CHECK (status_desired IN ('DEPLOYED','UNDEPLOYED'));
CREATE INDEX IF NOT EXISTS idx_deployment_status_transitional
    ON deployment_status (status, performed_at) WHERE status IN ('DEPLOYING','UNDEPLOYING','WAITING_UPSTREAM');

ALTER TABLE deployments ADD COLUMN IF NOT EXISTS fetched_at TIMESTAMP;
ALTER TABLE gateways    ADD COLUMN IF NOT EXISTS last_sync_at TIMESTAMP;

ALTER TABLE a2a_agent_cards
    ADD COLUMN IF NOT EXISTS for_deployment_id       UUID,
    ADD COLUMN IF NOT EXISTS attempted_deployment_id UUID,
    ADD COLUMN IF NOT EXISTS attempted_at            TIMESTAMP;
```

Then fold `a2a_publications`, guarded by `IF EXISTS` on the table (a fresh database has it; a database that skipped 044 would not):

- published rows: `UPDATE deployment_status s SET status_desired='DEPLOYED' FROM a2a_publications p WHERE p.status='published' AND s.artifact_uuid=p.artifact_uuid AND s.ou_id=p.ou_id;`
- pending and failed rows: for each, `INSERT INTO deployments (deployment_id, name, artifact_uuid, ou_id, gateway_uuid, content, created_at)` with `gen_random_uuid()`, `agent_name || '-deployment'`, empty content, and the environment's ingress gateway resolved as `resolveIngressGateway` does (gateway rows joined through the gateway-environment mapping with an ingress role, earliest `created_at` then lowest `uuid` when several match); then `INSERT INTO deployment_status (artifact_uuid, ou_id, gateway_uuid, deployment_id, status, status_desired, status_reason, performed_at)` with status `WAITING_UPSTREAM` for pending and `FAILED` with `last_error` for failed, `ON CONFLICT (artifact_uuid, ou_id, gateway_uuid) DO UPDATE SET status=EXCLUDED.status, status_desired='DEPLOYED', status_reason=EXCLUDED.status_reason, deployment_id=EXCLUDED.deployment_id, performed_at=EXCLUDED.performed_at`.
- rows whose environment has no ingress gateway are skipped; log their count with `RAISE NOTICE`.

Write the fold as a single `DO $$ ... $$` block so it is one statement.

- [ ] **Step 5: Run the model test and the migration**

Run: `cd agent-manager-service && make test-run RUN=TestTransitionalStatuses PKG=./models/`
Expected: PASS

Run: `cd agent-manager-service && make dev-migrate` (requires the docker-compose database; ask before running) and then `psql ... -c '\d deployment_status'`
Expected: the three columns and both check constraints are present.

- [ ] **Step 6: Commit**

```bash
git add agent-manager-service/db_migrations/049_add_deployment_desired_state.go agent-manager-service/db_migrations/migration_list.go agent-manager-service/models/deployment.go agent-manager-service/models/gateway.go agent-manager-service/models/a2a_agent_card.go agent-manager-service/models/deployment_status_test.go
git commit -m "Add desired state, reason and performed-at to deployment status"
```

---

### Task 2: Deployment repository methods for desired and observed state

**Files:**
- Modify: `agent-manager-service/repositories/deployment_repository.go` (interface at lines 36-70 and implementation)
- Regenerate: `agent-manager-service/repositories/repomocks/deployment_repository_mock.go`
- Test: `agent-manager-service/repositories/deployment_desired_state_test.go` (`//go:build integration`)

**Interfaces:**
- Produces, on `DeploymentRepository`:
  ```go
  // SetDesired upserts the status row for one artifact on one gateway and points it at deploymentID.
  SetDesired(ctx context.Context, tx *gorm.DB, artifactUUID, ouID, gatewayUUID uuid.UUID, deploymentID uuid.UUID,
      desired, status models.DeploymentStatus, reason string, performedAt time.Time) error
  // UpdateObservedWithGuard sets status and reason only when performed_at matches and the current status is in from (nil means any). Returns rows affected.
  UpdateObservedWithGuard(ctx context.Context, artifactUUID, ouID, gatewayUUID uuid.UUID, status models.DeploymentStatus,
      reason string, performedAt time.Time, from []models.DeploymentStatus) (int64, error)
  // ListStaleTransitional returns DEPLOYING and UNDEPLOYING rows whose performed_at is older than before.
  ListStaleTransitional(ctx context.Context, before time.Time) ([]models.DeploymentStatusRecord, error)
  // ClaimWaitingUpstream atomically returns up to limit WAITING_UPSTREAM rows and marks them DEPLOYING with performedAt, so only one replica renders each.
  ClaimWaitingUpstream(ctx context.Context, performedAt time.Time, limit int) ([]models.DeploymentStatusRecord, error)
  // ReleaseWaitingUpstream puts a claimed row back to WAITING_UPSTREAM with reason, guarded by performedAt.
  ReleaseWaitingUpstream(ctx context.Context, rec models.DeploymentStatusRecord, reason string) error
  // ListControlPlaneDeployments lists every status row for the gateway except WAITING_UPSTREAM, joined to artifacts.kind, newest performed_at first, optionally after since.
  ListControlPlaneDeployments(ctx context.Context, ouID, gatewayUUID string, since *time.Time) ([]models.ControlPlaneDeployment, error)
  // GetContentBatch returns content and artifact kind for the given deployment IDs on the gateway, omitting unknown IDs and empty content.
  GetContentBatch(ctx context.Context, ouID, gatewayUUID string, deploymentIDs []uuid.UUID) ([]models.DeploymentContent, error)
  // UpdateContent replaces a deployment row's content and metadata.
  UpdateContent(ctx context.Context, deploymentID uuid.UUID, content []byte, metadata map[string]interface{}) error
  // ListDeployedAgentRowsPage returns DEPLOYED status rows for Agent artifacts after afterArtifact, limit rows, for the drift check.
  ListDeployedAgentRowsPage(ctx context.Context, afterArtifact uuid.UUID, limit int) ([]models.DeploymentStatusRecord, error)
  // MarkFetched stamps fetched_at on the given deployment rows.
  MarkFetched(ctx context.Context, deploymentIDs []uuid.UUID, at time.Time) error
  // ListAgentArtifactsWithoutStatus returns Agent artifacts whose handle ends in "/"+envUUID and that have no status row on gatewayUUID.
  ListAgentArtifactsWithoutStatus(ctx context.Context, ouID string, envUUID, gatewayUUID uuid.UUID) ([]models.Artifact, error)
  ```
  `GetCurrentByGateway` changes to select by `status_desired = 'DEPLOYED'` instead of `status = 'DEPLOYED'`. `SetCurrent`, `SetCurrentCtx` and `CreateWithLimitEnforcement` write `status_desired` equal to the status they write and `performed_at = now` truncated to milliseconds. `ListStaleTransitional` returns each row joined with its gateway's `last_sync_at` and its deployment's `fetched_at` (add both as fields on `DeploymentStatusRecord` with `gorm:"->"`).
- Produces, on `GatewayRepository`: `SetLastSyncAt(ctx context.Context, gatewayUUID uuid.UUID, at time.Time) error`.
- Produces, in `models/deployment.go`:
  ```go
  type ControlPlaneDeployment struct {
      ArtifactID, DeploymentID, Kind, State string
      DeployedAt time.Time
      Etag string
  }
  type DeploymentContent struct {
      DeploymentID uuid.UUID
      Kind         string // gateway kind string
      Content      []byte
  }
  func GatewayKindFor(artifactKind string) (string, bool) // the mapping in Global Constraints
  ```
- `ClaimWaitingUpstream` uses `UPDATE deployment_status SET status='DEPLOYING', performed_at=? WHERE (artifact_uuid, ou_id, gateway_uuid) IN (SELECT ... WHERE status='WAITING_UPSTREAM' ORDER BY performed_at LIMIT ? FOR UPDATE SKIP LOCKED) RETURNING *`.

- [ ] **Step 1: Write the failing integration tests**

```go
func TestSetDesiredCreatesAndRepointsTheRow(t *testing.T)        // upsert twice, second deploymentID wins, status_desired and performed_at updated
func TestUpdateObservedWithGuardRejectsAMismatchedPerformedAt(t *testing.T) // returns 0 rows, status unchanged
func TestUpdateObservedWithGuardAcceptsFromListedStatuses(t *testing.T)     // from=[DEPLOYING,FAILED] flips FAILED→DEPLOYED
func TestClaimWaitingUpstreamIsExclusive(t *testing.T)           // two concurrent claims of one row: lengths sum to 1
func TestListControlPlaneDeploymentsOmitsWaitingRows(t *testing.T) // WAITING_UPSTREAM absent, DEPLOYING present with state "deployed"
func TestGetContentBatchOmitsEmptyContent(t *testing.T)
func TestGetCurrentByGatewayServesADeployingRow(t *testing.T)    // status DEPLOYING, desired DEPLOYED → returned
func TestSetCurrentWritesDesiredAndPerformedAt(t *testing.T)     // status_desired == status, performed_at truncated to ms
func TestListStaleTransitionalCarriesSyncEvidence(t *testing.T)  // LastSyncAt and FetchedAt populated from the joins
func TestListAgentArtifactsWithoutStatusSkipsPublishedOnes(t *testing.T)
func TestGatewayKindForMapsEveryArtifactKind(t *testing.T)       // unit, table from Global Constraints, unknown→false
```

Use the fixture helpers in `repositories/a2a_agent_card_repository_test.go` as the model for creating an org, gateway and artifact.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd agent-manager-service && go test -tags=integration ./repositories/ -run 'TestSetDesired|TestUpdateObserved|TestClaimWaiting|TestListControlPlane|TestGetContentBatch' -v` (needs the test database; `make test-integration` sets it up)
Expected: FAIL, methods undefined.

- [ ] **Step 3: Add the methods to the interface and implement them**

- [ ] **Step 4: Regenerate mocks**

Run: `cd agent-manager-service && make codegen`
Expected: `repomocks/deployment_repository_mock.go` changes and `go build ./...` passes.

- [ ] **Step 5: Run the tests to verify they pass**

Run: same as Step 2.
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent-manager-service/repositories/deployment_repository.go agent-manager-service/repositories/gateway_repository.go agent-manager-service/repositories/repomocks/ agent-manager-service/repositories/deployment_desired_state_test.go agent-manager-service/models/deployment.go
git commit -m "Add desired-state and sync queries to the deployment repository"
```

---

### Task 3: Gateway sync endpoints

**Files:**
- Modify: `agent-manager-service/services/gateway_internal_service.go`
- Modify: `agent-manager-service/controllers/gateway_internal_controller.go:389-408`
- Modify: `agent-manager-service/api/gateway_internal_routes.go`
- Test: `agent-manager-service/services/gateway_sync_unit_test.go`, `agent-manager-service/controllers/gateway_sync_controller_unit_test.go`

**Interfaces:**
- Consumes: Task 2 repository methods.
- Produces, on `GatewayInternalAPIService`:
  ```go
  func (s *GatewayInternalAPIService) ListControlPlaneDeployments(ctx context.Context, ouID, gatewayID string, since *time.Time) ([]models.ControlPlaneDeployment, error)
  // BuildDeploymentArchive resolves secrets in each item's content and writes a gzip tar with entries <deploymentId>/<kind>.yaml.
  func (s *GatewayInternalAPIService) BuildDeploymentArchive(ctx context.Context, ouID, gatewayID string, deploymentIDs []uuid.UUID) ([]byte, error)
  func (s *GatewayInternalAPIService) CheckArtifactsExist(ctx context.Context, ouID string, artifactIDs []uuid.UUID) ([]models.ArtifactExistence, error)
  ```
  `models.ArtifactExistence{ArtifactID string; Exists bool}`. The service needs `repositories.ArtifactRepository` added to its constructor.
- Routes: `GET /deployments` (replace the stub), `POST /deployments/fetch-batch`, `POST /artifacts/exists`, all registered before the `{id}` routes and all authenticating with `"internal-read"`.
- Etag: `hex(sha256(deploymentID + "|" + performedAt.UTC().Format(time.RFC3339Nano)))`.
- Side effects: `ListControlPlaneDeployments` calls `gatewayRepo.SetLastSyncAt` after a successful list. `BuildDeploymentArchive` and the per-kind fetch methods (`GetActiveAgentDeploymentByGateway` and siblings) call `deploymentRepo.MarkFetched` after a successful read.
- `since` filters on `performed_at`.

- [ ] **Step 1: Write the failing service tests**

```go
func TestListControlPlaneDeploymentsIncludesEveryKind(t *testing.T)   // one row each of Agent, McpProxy, LlmProvider, LlmProxy, RestAPI → kinds Agent, Mcp, LlmProvider, LlmProxy, RestApi
func TestListControlPlaneDeploymentsLowercasesState(t *testing.T)     // desired UNDEPLOYED → "undeployed"
func TestBuildDeploymentArchiveLaysOutEntriesByDeploymentID(t *testing.T) // untar: entries "<id>/Agent.yaml", secretRef replaced by value
func TestBuildDeploymentArchiveSkipsUnknownIDs(t *testing.T)
func TestCheckArtifactsExistReportsBoth(t *testing.T)
func TestListControlPlaneDeploymentsRecordsTheSync(t *testing.T)   // SetLastSyncAt called with the gateway
func TestBuildDeploymentArchiveMarksFetched(t *testing.T)          // MarkFetched called with the served IDs only
func TestGetActiveAgentDeploymentMarksFetched(t *testing.T)
```

Controller tests: `GET /deployments` returns `{"deployments":[...]}` with the six item fields; `POST /deployments/fetch-batch` with an empty list returns 400; `POST /artifacts/exists` with more than 500 IDs returns 400; an unauthenticated call returns 401 (reuse the existing `authenticateGateway` test setup in `controllers/gateway_internal_controller_unit_test.go` if present, otherwise the HTTP test helper the other controller tests use).

- [ ] **Step 2: Run them to verify they fail**

Run: `cd agent-manager-service && make test-run RUN='TestListControlPlane|TestBuildDeploymentArchive|TestCheckArtifactsExist' PKG=./services/`
Expected: FAIL, methods undefined.

- [ ] **Step 3: Implement the three service methods, the three handlers, and the routes**

Secret resolution reuses `resolveAllSecretsInYAML`. The archive writer uses `archive/tar` and `compress/gzip` from the standard library.

- [ ] **Step 4: Run the tests and `make wire`**

Run: `cd agent-manager-service && make wire && make test-run RUN='TestListControlPlane|TestBuildDeploymentArchive|TestCheckArtifactsExist|TestGatewaySync' PKG=./...`
Expected: PASS, `wiring/wire_gen.go` updated for the new constructor argument.

- [ ] **Step 5: Commit**

```bash
git add agent-manager-service/services/gateway_internal_service.go agent-manager-service/services/gateway_sync_unit_test.go agent-manager-service/controllers/gateway_internal_controller.go agent-manager-service/controllers/gateway_sync_controller_unit_test.go agent-manager-service/api/gateway_internal_routes.go agent-manager-service/wiring/wire.go agent-manager-service/wiring/wire_gen.go
git commit -m "Serve the gateway's deployment sync, batch fetch and existence check"
```

---

### Task 4: Ack handler guard and the timeout job

**Files:**
- Modify: `agent-manager-service/services/deployment_ack_handler.go`
- Create: `agent-manager-service/services/deployment_timeout_service.go`
- Modify: `agent-manager-service/wiring/wire.go`, `agent-manager-service/app/app.go:244-260,316-325`
- Test: `agent-manager-service/services/deployment_ack_handler_unit_test.go`, `agent-manager-service/services/deployment_timeout_service_unit_test.go`

**Interfaces:**
- Consumes: `UpdateObservedWithGuard`, `ListStaleTransitional` from Task 2.
- Produces:
  ```go
  type DeploymentTimeoutService interface { Start(ctx context.Context) error; Stop() error; RunOnce(ctx context.Context) }
  func NewDeploymentTimeoutService(deploymentRepo repositories.DeploymentRepository, logger *slog.Logger) DeploymentTimeoutService
  ```
- Ack handling branches on `resourceType`:
  - `Agent` and `agentproxy` use the guard, keyed by `artifactId` + gateway; `performedAt` must match the row; the ack's `deploymentId` is logged only. Transitions (`from` sets passed to the guard):
    - deploy success → `DEPLOYED`, from `[DEPLOYING, FAILED, DEPLOYED]`
    - undeploy success → `UNDEPLOYED`, from `[UNDEPLOYING, FAILED, UNDEPLOYED]`
    - any failure → `FAILED` with `errorCode` as reason, from `[DEPLOYING, UNDEPLOYING]`
  - `llmprovider`, `llmproxy`, `mcpproxy` keep today's `UpdateStatusByDeploymentID` path unchanged, except that a failed deploy ack also sets `status_desired = UNDEPLOYED` (add that to `UpdateStatusByDeploymentID` via a new `desired *models.DeploymentStatus` argument, nil leaves it alone).
- Timeout rules, applied to rows `ListStaleTransitional` returns (older than five minutes), in this order:
  - `DEPLOYING`, gateway `LastSyncAt` after `performed_at` and deployment `FetchedAt` after `performed_at` → `DEPLOYED`, reason `SYNCED_UNCONFIRMED`
  - `UNDEPLOYING`, gateway `LastSyncAt` after `performed_at` → `UNDEPLOYED`, reason `SYNCED_UNCONFIRMED`
  - otherwise → `FAILED`, reason `TIMEOUT`
  - every write goes through `UpdateObservedWithGuard` with `from = [row.Status]`.

- [ ] **Step 1: Write the failing tests**

```go
func TestAckRepairsATimedOutRow(t *testing.T)        // row FAILED/TIMEOUT, success ack with matching performedAt → guard called with DEPLOYED and from containing FAILED
func TestStaleAckIsDiscarded(t *testing.T)           // guard returns 0 rows → handler logs, no second call
func TestFailedAckRecordsTheGatewayReason(t *testing.T) // errorCode "GATEWAY_PROCESSING_ERROR" becomes reason
func TestTimeoutJobFailsRowsTheGatewayNeverSynced(t *testing.T)   // LastSyncAt nil → FAILED/TIMEOUT, from=[row.Status]
func TestTimeoutJobConfirmsASyncedAndFetchedDeploy(t *testing.T)  // LastSyncAt and FetchedAt after performed_at → DEPLOYED/SYNCED_UNCONFIRMED
func TestTimeoutJobFailsASyncedButUnfetchedDeploy(t *testing.T)   // LastSyncAt after, FetchedAt nil → FAILED/TIMEOUT
func TestTimeoutJobConfirmsASyncedUndeploy(t *testing.T)          // UNDEPLOYING, LastSyncAt after → UNDEPLOYED/SYNCED_UNCONFIRMED
func TestLLMProviderAckKeepsLegacyHandling(t *testing.T)         // resourceType llmprovider → UpdateStatusByDeploymentID, guard never called
func TestLLMProviderFailedDeployAckClearsDesired(t *testing.T)   // desired argument == UNDEPLOYED
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd agent-manager-service && make test-run RUN='TestAckRepairs|TestStaleAck|TestFailedAck|TestTimeoutJob|TestLLMProviderAck' PKG=./services/`
Expected: FAIL.

- [ ] **Step 3: Rewrite `handleDeploymentAck` to branch by resource type, and implement the timeout service with the same Start/Stop/ticker shape as `a2aCardReconcilerService`**

- [ ] **Step 4: Wire it and start it in `app.go` next to the existing reconciler start, with the same cancel-on-shutdown pattern**

Run: `cd agent-manager-service && make wire`

- [ ] **Step 5: Run the tests to verify they pass**

Run: same as Step 2.
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent-manager-service/services/deployment_ack_handler.go agent-manager-service/services/deployment_ack_handler_unit_test.go agent-manager-service/services/deployment_timeout_service.go agent-manager-service/services/deployment_timeout_service_unit_test.go agent-manager-service/wiring/wire.go agent-manager-service/wiring/wire_gen.go agent-manager-service/app/app.go
git commit -m "Guard deployment acks by performed-at and time out stale transitions"
```

---

### Task 5: Upstream resolver

**Files:**
- Create: `agent-manager-service/services/a2a_upstream_resolver.go`
- Modify: `agent-manager-service/services/a2a_publication_reconciler.go` (move `resolveExternalOrigin`, `fetchExternalUpstream`, `originCardHeader`, `errUpstreamNotReady` out; the file is deleted in Task 8)
- Test: `agent-manager-service/services/a2a_upstream_resolver_unit_test.go`

**Interfaces:**
- Produces, exactly as in the spec:
  ```go
  type UpstreamRef struct { OUID, ProjectName, AgentName, EnvironmentName string; EnvironmentUUID uuid.UUID; Source models.A2APublicationSource }
  type Upstream struct { URL, Version string; Transports []A2ATransport; Auth *models.UpstreamAuth }
  type UpstreamResolver interface { Resolve(ctx context.Context, ref UpstreamRef) (Upstream, error) }
  var ErrUpstreamNotReady = newA2APublicError("release binding has not published a service URL yet", nil)
  var ErrUpstreamGone = errors.New("upstream is no longer configured") // proxy row or card source missing
  func NewUpstreamResolver(oc client.OpenChoreoClient, proxyRepo repositories.A2AProxyRepository, cardRepo repositories.A2AAgentCardRepository, fetcher A2ACardFetcher, encryptionKey []byte) UpstreamResolver
  ```
  The one constructor dispatches on `ref.Source`. Platform: `Version` is the binding's release name from `GetReleaseBindingRollout`; `URL` is its service URL; empty URL returns `ErrUpstreamNotReady`. External: `Version` is `a2aCardHash` of the fetched origin card; a missing proxy row or card row returns `ErrUpstreamGone`.

- [ ] **Step 1: Write the failing tests with `clientmocks.OpenChoreoClientMock` and `repomocks`**

```go
func TestPlatformResolveReturnsNotReadyWithoutServiceURL(t *testing.T)
func TestPlatformResolveCarriesTheReleaseName(t *testing.T)
func TestExternalResolveDerivesRoutesFromTheOriginCard(t *testing.T) // reuse the card fixture from a2a_proxy_routes_unit_test.go
func TestExternalResolveReportsGoneWithoutAProxyRow(t *testing.T)
func TestExternalResolveSendsTheStoredHeader(t *testing.T)           // fetcher mock sees decrypted header
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd agent-manager-service && make test-run RUN='TestPlatformResolve|TestExternalResolve' PKG=./services/`

- [ ] **Step 3: Implement by moving the existing reconciler functions behind the interface; do not change their behaviour**

- [ ] **Step 4: Run the tests to verify they pass, and run the moved code's old tests**

Run: `cd agent-manager-service && make test-run RUN='TestPlatformResolve|TestExternalResolve|TestReconciler' PKG=./services/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent-manager-service/services/a2a_upstream_resolver.go agent-manager-service/services/a2a_upstream_resolver_unit_test.go agent-manager-service/services/a2a_publication_reconciler.go
git commit -m "Put A2A upstream resolution behind one interface"
```

---

### Task 6: Desired-state service replacing the publisher

**Files:**
- Create: `agent-manager-service/services/a2a_desired_state.go`
- Test: `agent-manager-service/services/a2a_desired_state_unit_test.go`

**Interfaces:**
- Consumes: `SetDesired`, `UpdateContent` (Task 2), `UpstreamResolver` (Task 5), `GatewayEventsService`, `resolveIngressGateway`, `ensureAgentEnvAPIArtifact`, `generateA2AAgentDeploymentYAML`, `buildPolicies`, `buildCardPolicies`.
- Produces:
  ```go
  type A2ATarget struct { Ref UpstreamRef; AgentUUID *uuid.UUID; EnvironmentUUIDString string }
  type A2ADesiredState struct { /* deps */ }
  func NewA2ADesiredState(tx TxRunner, deploymentRepo repositories.DeploymentRepository, artifactRepo repositories.ArtifactRepository, gatewayRepo repositories.GatewayRepository, agentConfigRepo repositories.AgentConfigRepository, resolver UpstreamResolver, events *GatewayEventsService, logger *slog.Logger) *A2ADesiredState
  // Publish sets desired DEPLOYED. It resolves the environment's ingress gateway (ErrNoIngressGateway, the existing
  // public "no ingress gateway is mapped to this environment yet" error, when there is none), ensures the artifact,
  // creates a deployment row, and tries the resolver once:
  // ready → render, store content, status DEPLOYING, returns a poke; not ready → status WAITING_UPSTREAM, reason = resolver message.
  func (d *A2ADesiredState) Publish(ctx context.Context, tx *gorm.DB, t A2ATarget) (A2APoke, error)
  // PublishMissingForEnvironment publishes every Agent artifact in the environment that has no status row on gatewayUUID. Called when a gateway is mapped to an environment.
  func (d *A2ADesiredState) PublishMissingForEnvironment(ctx context.Context, ouID string, envUUID, gatewayUUID uuid.UUID) ([]A2APoke, error)
  // Withdraw sets desired UNDEPLOYED and status UNDEPLOYING on the existing row; keeps the artifact. No row → no-op.
  func (d *A2ADesiredState) Withdraw(ctx context.Context, tx *gorm.DB, t A2ATarget) (A2APoke, error)
  // DeleteForAgent removes status rows and artifacts for every environment of the agent and returns the deletion pokes.
  func (d *A2ADesiredState) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) ([]A2APoke, error)
  func (d *A2ADesiredState) DeleteForEnvironment(ctx context.Context, ouID string, envUUID uuid.UUID) ([]A2APoke, error)
  // Send delivers a poke after the caller's transaction committed; best effort, detached from request cancellation.
  func (d *A2ADesiredState) Send(ctx context.Context, p A2APoke)
  // Status reads the row for the API mapping in the spec.
  func (d *A2ADesiredState) Status(ctx context.Context, t A2ATarget) (*models.DeploymentStatusRecord, error)
  type A2APoke struct { GatewayID string; ArtifactUUID uuid.UUID; DeploymentID uuid.UUID; PerformedAt time.Time; Kind A2APokeKind } // deployed | undeployed | deleted | none
  ```
- Rendering: `renderA2AAgent(ctx, t, up Upstream, cfg *models.AgentConfig) (yaml string, metadata map[string]interface{}, err error)` builds `A2AAgentDeploymentInput` exactly as `attemptPublish` does today; metadata is `{"upstreamUrl": up.URL, "upstreamVersion": up.Version}`.
- `ShouldPublish` moves here unchanged from `A2APublisher`.

- [ ] **Step 1: Write the failing tests**

```go
func TestPublishRendersWhenTheUpstreamIsReady(t *testing.T)     // SetDesired called with DEPLOYING, UpdateContent called, poke kind deployed
func TestPublishWaitsWhenTheUpstreamIsNotReady(t *testing.T)    // SetDesired called with WAITING_UPSTREAM and the not-ready message, poke kind none, no UpdateContent
func TestPublishFailsOnAGoneUpstream(t *testing.T)              // status FAILED, reason from ErrUpstreamGone
func TestWithdrawKeepsTheArtifact(t *testing.T)                 // artifactRepo.Delete never called, poke kind undeployed
func TestDeleteForAgentPokesOnlyGatewaysWithRows(t *testing.T)  // two env rows on gateways A and B, a third active gateway C: pokes for A and B only
func TestSendDetachesFromRequestCancellation(t *testing.T)      // cancelled ctx still broadcasts
func TestPublishReportsAMissingIngressGateway(t *testing.T)     // gatewayRepo returns none → ErrNoIngressGateway, no writes
func TestPublishMissingForEnvironmentSkipsPublishedArtifacts(t *testing.T) // two artifacts, one with a row → one Publish
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd agent-manager-service && make test-run RUN='TestPublish|TestWithdraw|TestDeleteForAgent|TestSendDetaches' PKG=./services/`

- [ ] **Step 3: Implement**

Callers decide what `ErrNoIngressGateway` means: proxy and card-source requests return it to the user as the existing 409; platform deploy, promote and deploy-settings paths log it at error level and continue (Task 8).

The deployment row is created with `CreateWithLimitEnforcement(deployment, maxDeploymentsPerAPI+deploymentLimitBuffer)` inside the caller's transaction (`deploymentRepo.WithTx(tx)` if the repository has it; otherwise add `CreateWithLimitEnforcementTx(tx, ...)` in Task 2's style).

- [ ] **Step 4: Run the tests to verify they pass**

- [ ] **Step 5: Commit**

```bash
git add agent-manager-service/services/a2a_desired_state.go agent-manager-service/services/a2a_desired_state_unit_test.go
git commit -m "Record A2A gateway publications as desired state"
```

---

### Task 7: Upstream worker replacing the publication reconciler

**Files:**
- Create: `agent-manager-service/services/a2a_upstream_worker.go`
- Modify: `agent-manager-service/wiring/wire.go:85-88`, `agent-manager-service/app/app.go:244-260,316-325`
- Test: `agent-manager-service/services/a2a_upstream_worker_unit_test.go`

**Interfaces:**
- Consumes: `ClaimWaitingUpstream`, `ReleaseWaitingUpstream`, `ListDeployedAgentRowsPage`, `UpdateContent` (Task 2); `UpstreamResolver` (Task 5); `A2ADesiredState.Publish`/`Send` and `renderA2AAgent` (Task 6).
- Produces:
  ```go
  type A2AUpstreamWorker interface { Start(ctx context.Context) error; Stop() error; RunOnce(ctx context.Context); CheckDriftOnce(ctx context.Context) }
  func NewA2AUpstreamWorker(deploymentRepo repositories.DeploymentRepository, artifactRepo repositories.ArtifactRepository, agentConfigRepo repositories.AgentConfigRepository, desired *A2ADesiredState, resolver UpstreamResolver, logger *slog.Logger) A2AUpstreamWorker
  ```
- `RunOnce`: claim 50 rows with `performedAt = now`; for each, rebuild the `UpstreamRef` from the artifact handle (`project/agent/envUUID`) and the environment name from the status row's deployment metadata or the artifact; resolve; ready → render, `UpdateContent`, `Send` deployed poke; not ready → `ReleaseWaitingUpstream` with the message; other error → `UpdateObservedWithGuard` to `FAILED` with the message. Run rows four at a time with a 30s per-row timeout, reusing `drainWithinLease` with a 4-minute deadline.
- `CheckDriftOnce`: one page of 50 `DEPLOYED` agent rows after a cursor held on the worker; for each platform row whose resolver `URL` or `Version` differs from the deployment's metadata, call `Publish` in a fresh transaction. Runs every tenth tick.
- Context carries the org like `actingAsOrg` does today.

- [ ] **Step 1: Write the failing tests**

```go
func TestWorkerRendersAClaimedRowOnceReady(t *testing.T)
func TestWorkerReleasesANotReadyRow(t *testing.T)                // ReleaseWaitingUpstream called, no broadcast
func TestWorkerFailsARowWhoseUpstreamIsGone(t *testing.T)
func TestWorkerStopsStartingRowsAfterTheDeadline(t *testing.T)   // port of the existing lease test
func TestDriftCheckRepublishesAMovedUpstream(t *testing.T)       // metadata upstreamUrl differs → Publish called once
func TestDriftCheckIgnoresExternalRows(t *testing.T)
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd agent-manager-service && make test-run RUN='TestWorker|TestDriftCheck' PKG=./services/`

- [ ] **Step 3: Implement the worker; replace `NewA2APublicationReconcilerService` in `wire.go` and the reconciler start/stop in `app.go` with the worker**

Run: `cd agent-manager-service && make wire`

- [ ] **Step 4: Run the tests to verify they pass**

- [ ] **Step 5: Commit**

```bash
git add agent-manager-service/services/a2a_upstream_worker.go agent-manager-service/services/a2a_upstream_worker_unit_test.go agent-manager-service/wiring/wire.go agent-manager-service/wiring/wire_gen.go agent-manager-service/app/app.go
git commit -m "Resolve waiting A2A upstreams with one worker instead of a queue"
```

---

### Task 8: Rewire callers and delete the publication path

**Files:**
- Modify: `agent-manager-service/services/agent_manager.go` (`enqueueA2APublication`, `enqueueCreatedA2AAgent`, `republishA2AAgent`, `a2aRepublicationKey`, `deleteAgentAPIArtifact`, `clearInheritedA2AState`, the deploy-settings save at ~5220, the undeploy path at ~6115)
- Modify: `agent-manager-service/services/environment_service.go:440-485`
- Modify: `agent-manager-service/services/platform_gateway_service.go:1004` (`AssignGatewayToEnvironment` calls `desired.PublishMissingForEnvironment` after its transaction commits and sends the pokes; only when the gateway has an ingress role)
- Modify: `agent-manager-service/services/a2a_proxy_service.go` (every `s.publisher.*` call and `GetA2AProxy`)
- Modify: `agent-manager-service/services/a2a_agent_deployment.go` (delete `collectA2AAgentDeletionTargets`, `sendA2AAgentWithdrawal`)
- Delete: `services/a2a_publisher.go`, `services/a2a_publication_reconciler.go`, `repositories/a2a_publication_repository.go`, `repositories/repomocks/a2a_publication_repository_mock.go`, `models/a2a_publication.go` (keep `A2APublicationSource` and `A2APublicationStatus` by moving them to `models/a2a_proxy.go`), and their tests
- Modify: `agent-manager-service/wiring/wire.go` (drop `ProvideA2APublicationRepository`, `NewA2APublisher`; add `NewA2ADesiredState`, `NewUpstreamResolver`)
- Test: existing `a2a_deploy_enqueue_unit_test.go`, `a2a_agent_deletion_unit_test.go`, `a2a_proxy_service_unit_test.go`, `a2a_proxy_deploy_settings_unit_test.go` rewritten against `A2ADesiredState`

**Interfaces:**
- Consumes: Task 6.
- Call mapping:
  | Old | New |
  |---|---|
  | `a2aPublisher.Enqueue(ctx, tx, key, source, artifactUUID)` | `desired.Publish(ctx, tx, target)` then `desired.Send(ctx, poke)` after commit |
  | `a2aPublisher.TeardownEnv` + `SendTeardown` | `desired.Withdraw` + `Send` |
  | `a2aPublisher.TeardownEnvAndDropArtifact` | `desired.DeleteForEnvironment` scoped to the one artifact (add `DeleteForAgentEnv(ctx, tx, t A2ATarget) (A2APoke, error)` to Task 6 if a per-environment variant is needed) |
  | `a2aPublisher.DeleteForAgent` + `deleteAgentAPIArtifact`'s fan-out | `desired.DeleteForAgent` + `Send` each poke |
  | `a2aPublisher.TeardownEnvironment` | `desired.DeleteForEnvironment` |
  | `a2aPublisher.Publication` in `GetA2AProxy` | `desired.Status` mapped per the spec table |
  | `dropIfAgentGone`, `dropCardIfAgentGone`, `clearInheritedA2AState`, `withdrawIfTornDown` | deleted |
  | `resolveIngressGateway` inside the reconciler | `Publish` resolves it; `ErrNoIngressGateway` is a 409 on proxy and card-source routes and a logged error on platform paths |
  | (none) | gateway-environment mapping write → `PublishMissingForEnvironment` |

Add a unit test `TestMappingAGatewayPublishesUnpublishedAgents` on the platform gateway service and `TestDeployLogsAndContinuesWithoutAnIngressGateway` on the agent manager.

- [ ] **Step 1: Rewrite the four affected unit test files against the new service; run them to verify they fail**

Run: `cd agent-manager-service && make test-run RUN='TestA2A|TestDeploySettings|TestDeleteAgent' PKG=./services/`

- [ ] **Step 2: Rewire every caller per the mapping, delete the listed files, update `wire.go`**

Run: `cd agent-manager-service && make wire && go build ./... && go vet ./...`
Expected: no references to `A2APublisher`, `A2APublicationRepository` or `a2aPublicationKey` remain (`grep -rn "A2APublisher\|A2APublicationRepository\|a2aPublicationKey" --include='*.go' . | grep -v _test` is empty).

- [ ] **Step 3: Run the whole unit suite**

Run: `cd agent-manager-service && make test-unit`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add -u agent-manager-service
git commit -m "Drive A2A publications from desired state and remove the publisher"
```

---

### Task 9: Card cache replacing the card reconciler

**Files:**
- Modify: `agent-manager-service/repositories/a2a_agent_card_repository.go` (interface and implementation; regenerate mock)
- Modify: `agent-manager-service/services/a2a_agent_card_service.go`
- Delete: `agent-manager-service/services/a2a_card_reconciler.go` and its tests
- Modify: `agent-manager-service/wiring/wire.go`, `agent-manager-service/app/app.go` (drop the card reconciler start/stop)
- Test: `agent-manager-service/repositories/a2a_agent_card_cache_test.go` (integration), `agent-manager-service/services/a2a_agent_card_service_unit_test.go`

**Interfaces:**
- Produces, on `A2AAgentCardRepository` (replacing `Enqueue`, `Requeue`, `ClaimDue`, `MarkAttemptFailed`, `MarkFailed`, `MarkFetched`, `FailParked`, `ClearCard`):
  ```go
  UpsertSource(ctx context.Context, card *models.A2AAgentCard) error // source, source_url only; preserves cached card
  // TryClaimAttempt sets attempted_at=now and attempted_deployment_id when attempted_at is older than minInterval or the deployment differs; returns false otherwise.
  TryClaimAttempt(ctx context.Context, key A2ACardKey, deploymentID uuid.UUID, now time.Time, minInterval time.Duration) (bool, error)
  StoreFetched(ctx context.Context, key A2ACardKey, deploymentID uuid.UUID, card json.RawMessage, hash, sourceURL string, fetchedAt time.Time) error
  StoreFetchError(ctx context.Context, key A2ACardKey, deploymentID uuid.UUID, lastErr string) error
  ```
  `A2ACardKey{OUID, ProjectName, AgentName, EnvironmentName string}`.
- Card service gains an `A2ADesiredState` dependency and an in-process `singleflight.Group`. `GetA2AAgentCard` derives the response status per the spec table and, when stale and the gateway row is `DEPLOYED`, launches one fetch through `singleflight` in a detached goroutine with a 30s timeout. The fetch URL for a platform or proxied agent is the gateway card URL built as `fetchCardThroughGateway` builds it today; for a direct external agent it is the registered source URL.
- `RefreshA2AAgentCard` clears `for_deployment_id` so the next read refetches; on a proxied environment it also calls `desired.Publish`.
- Direct external agents (no proxy row, so no status row) are the third branch: `for_deployment_id` stays nil, the fetch goes to `source_url` through `FetchOrigin`, and the status rule is fetched when a card is cached for the current `source_url`, pending while none or a fetch is due, failed when `attempted_at` is newer than `fetched_at` and `last_error` is set. `TryClaimAttempt` takes `uuid.Nil` as the deployment for this branch. Switching proxied to direct, and setting a new source URL, clear the cached card.

- [ ] **Step 1: Write the failing tests**

```go
// integration
func TestTryClaimAttemptRateLimitsPerKey(t *testing.T)            // second claim within 10s returns false; after the interval true
func TestStoreFetchedRecordsTheDeployment(t *testing.T)
// unit
func TestCardReadDoesNotFetchBeforeTheGatewayAcks(t *testing.T)   // status row DEPLOYING → response pending, fetcher never called
func TestCardReadFetchesOnceWhenStale(t *testing.T)               // two concurrent reads → fetcher called once (singleflight)
func TestCardReadReportsFetchedWhenCacheMatchesTheAck(t *testing.T)
func TestCardReadReportsFailedForTheAckedDeployment(t *testing.T) // attempted_deployment_id == acked, last_error set → failed
func TestRefreshInvalidatesAndRepublishesWhenProxied(t *testing.T)
func TestDirectExternalCardFetchesFromTheRegisteredURL(t *testing.T)   // no status row → FetchOrigin called with source_url, for_deployment_id nil
func TestDirectExternalCardReportsFailedAfterABadFetch(t *testing.T)
func TestSwitchingToDirectClearsTheGatewayCard(t *testing.T)
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd agent-manager-service && make test-run RUN='TestCardRead|TestRefreshInvalidates' PKG=./services/`

- [ ] **Step 3: Implement the repository methods, regenerate the mock, rewrite the card service, delete the reconciler, update wiring and app startup**

Run: `cd agent-manager-service && make codegen && make wire`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd agent-manager-service && make test-run RUN='TestCardRead|TestRefreshInvalidates|TestA2AAgentCard' PKG=./services/ && go test -tags=integration ./repositories/ -run 'TestTryClaimAttempt|TestStoreFetched'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add -u agent-manager-service
git commit -m "Serve A2A agent cards from a cache keyed to the acked deployment"
```

---

### Task 10: Full verification and cleanup

**Files:**
- Modify: anything `make lint` flags; `agent-manager-service/docs/database-er.md` (document the three new columns and the card columns)

- [ ] **Step 1: Confirm nothing references the removed code**

Run: `cd agent-manager-service && grep -rn "A2ACardReconciler\|A2APublicationReconciler\|a2a_publications" --include='*.go' . | grep -v db_migrations`
Expected: empty.

- [ ] **Step 2: Lint, format, unit and integration tests**

Run: `cd agent-manager-service && make fmt && make lint && make test-unit && make test-integration`
Expected: all PASS. `make test-integration` needs the test database; ask before running.

- [ ] **Step 3: Exercise the sync against a real gateway**

Run the local environment (`deployments/docker-compose.yml` or the k3d setup), restart the gateway-controller, and check its log for `Deployment sync completed` with `to_fetch` equal to the number of deployed artifacts on first restart and `0` on a second restart. Deploy an A2A agent and confirm the proxy status moves `pending → published` only after the gateway's ack log line.

- [ ] **Step 4: Update `docs/database-er.md` and commit**

```bash
git add -u agent-manager-service
git commit -m "Document the deployment status columns and card cache"
```

---

### Task 11 (separate change, after release one is deployed everywhere): Migration 050

**Files:**
- Create: `agent-manager-service/db_migrations/050_drop_a2a_publication_queue.go`
- Modify: `agent-manager-service/db_migrations/migration_list.go`, `agent-manager-service/models/a2a_agent_card.go` (drop the queue fields)

```sql
DROP TABLE IF EXISTS a2a_publications;
ALTER TABLE a2a_agent_cards
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS release_name,
    DROP COLUMN IF EXISTS attempted_release_name;
DROP INDEX IF EXISTS idx_a2a_agent_cards_due;
```

Commit as `Drop the A2A publication queue and card queue columns`. Not part of this plan's execution.
