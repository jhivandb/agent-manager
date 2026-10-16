# agent-manager-service vs platform-api as gateway control planes: divergence report and alignment plan

Date: 2026-10-16. Read-only comparison.

| Code read | Revision |
|---|---|
| agent-manager-service (AMS) | `a2a-sync-redesign` worktree, `57cacc2a7` (upstream/main + one docs commit) |
| platform-api, `common/eventhub` | api-platform `main`, `a6fd8e30a` |
| gateway-controller | api-platform `main` (HEAD), plus the tag `gateway/v2026.09.24` that AMS pins |

"Verified" means I read the code path. "Inferred" means it follows from code I read but I did not run it. Anything I did not check is marked **(unverified)**.

---

## Executive summary

- **AMS is a partial, diverged fork of platform-api's control-plane surface.** `agent-manager-service/websocket/` and `agent-manager-service/eventhub/` are ports of `platform-api/internal/websocket` and `common/eventhub`. AMS's `go.mod` has **no** dependency on any `github.com/wso2/api-platform/*` module, so the two drift independently.
- **The two sides disagree on HA.** AMS keeps one WebSocket per gateway per replica and evicts the old one on every new connect. platform-api keeps many connections and fans each event out to all of them. Two HA gateway-controllers on the same AMS replica will evict each other in a loop (the gateway reconnects with no delay after a clean close), and each eviction marks the gateway `is_active=false` while a live connection exists.
- **AMS's startup sync is a no-op by accident, and so is its orphan cleanup.** `GET /deployments` returns `[]`. `POST /artifacts/exists`, `POST /deployments/fetch-batch`, `GET /secrets*` and `POST /artifacts/import-gateway-artifacts` return 404. The gateway therefore skips every deletion and fetch, logs a secret-sync error on every connect, and retries the bottom-up push until it gives up.
- **Correction to the brief: AMS's cold-start replay is not deduplicated in practice.** `deduplicateLatestPerEntity` runs only when the poll saw zero subscribers and a subscriber then appeared within the same poll, which is a race window. Normally a gateway that connects to a replica with no cursor gets **every retained event verbatim** (up to 1h). This full replay is also the only thing that covers events lost between gateway restarts, because the gateway syncs deployments only on its first connect.
- **AMS pins gateway build `2026.09.24` (chart and image).** That tag predates three gateway changes. The gateway now acks Agents with `resourceType: "Agent"` (it was `"agentproxy"`). It bulk-syncs Agent keys from `/agents/api-keys`. And Agent and GraphQL kinds joined the key sync. AMS's ack handler accepted only `"agentproxy"` until `f265599df` (PR #2063, merged to upstream/main on 2026-10-16) added `"Agent"`. The `a2a-sync-redesign` branch is based 44 commits before that merge, so rebase it before implementing. platform-api handles gateway versions explicitly (the manifest's `version` plus `gatewaytranslator`). AMS has no version awareness at all.
- **Deployment state differs in kind, not degree.** platform-api is desired/observed (`status_desired`, transitional states, `performed_at`-guarded acks, timeout job, reason codes). AMS is a single optimistic `status` with unguarded ack updates keyed by deployment ID. The A2A redesign moves AMS most of the way, but only for Agents. LLM providers, LLM proxies and MCP proxies stay optimistic.
- **The flow goes both ways.** platform-api and `common` should take five AMS improvements:
  - the `(processed_timestamp, event_id)` cursor;
  - atomic registry mutation;
  - per-gateway rate limiting with cleanup;
  - prefix-indexed token lookup with caches;
  - "a success ack repairs a timed-out FAILED row".
- **The structural fix is to make `common/eventhub` (and later the WebSocket/dispatcher and the protocol DTOs) importable by AMS.** Today `common` is untagged and depends on `httpkit v0.0.0-local` through a `replace`, so an external module cannot consume it.

---

## 1. Gateway identity and registration

| Aspect | AMS | platform-api |
|---|---|---|
| Gateway table | `gateways`: uuid, `ou_id` (was `organization_name`), name, vhost, `gateway_functionality_type` ∈ {ingress, egress, both} (mig 037), runtime URL (mig 038), `is_active`, soft delete `deleted_at` | `gateways`: uuid, `organization_uuid`, handle, **`version`**, functionality type, properties, **`manifest`**, `is_active`, audit columns; FK to organizations |
| Environment mapping | `gateway_environment_mappings`; at most one ingress gateway per environment (mig 037 asserts it) | None; artifacts map to gateways directly (`artifact_gateway_mappings`) |
| Token storage | `gateway_tokens` with `token_prefix` (UUID prefix), salted hash, partial unique index on active prefix | `gateway_tokens`: unsalted SHA-256 `token_hash`, no prefix |
| Token verification | Prefix lookup, an in-memory positive cache and a negative cache for unknown prefixes (`services/platform_gateway_service.go:541`) | Hash the token, then `GetActiveTokenByHash` and `GetByUUID` on every call, with no cache (`service/gateway.go:858`) |
| WS and internal REST auth | `api-key` header on both; no scopes; every internal call is audited (`controllers/gateway_internal_audit.go`) | `api-key` header; no scopes; unified JSON 401 body (`handler/gateway_internal.go:112`) |
| 401 body | Plain text that differs for a missing key and a bad key | JSON `InternalErrorResponse` that differs for a missing key and a bad key (documented as deliberate) |
| Manifest push | Rejects with 403 when the path `gatewayId` differs from the token's gateway; stores the policy manifest; **does not record the gateway version** | Ignores the path ID, uses the token's gateway, and **stores the gateway version** (which feeds the translator) |
| Connect rate limit | Per **gateway UUID**, after auth, default **10/min**, cleanup goroutine every 5m | Per **RemoteAddr IP**, before auth, default **1000/min**, **no map cleanup** |

## 2. WebSocket layer

| Aspect | AMS (`websocket/manager.go`, `controllers/websocket_controller.go`) | platform-api (`internal/websocket/manager.go`, `handler/websocket.go`, `service/event_dispatcher.go`) |
|---|---|---|
| Path | `/api/internal/v1/ws/gateways/connect` | same |
| Connections per gateway | **One per replica.** `Register` evicts existing connections (close 1000 "superseded by new connection") | **Many.** Appended to a slice ("clustering") |
| Registry atomicity | `registryMu` makes load, delete, count and store one unit | `LoadOrStore` followed by a separate `Store`, so concurrent registers for one gateway can lose a connection **(inferred race)** |
| Hub subscription | One per **connection**, created inside `Manager.Register` | One per **gateway per replica**, created by an `EventDispatcher` via connect/disconnect hooks and fanned out to all connections |
| Send failure | Logged and counted; the heartbeat eventually reaps the connection | `Unregister` runs immediately so the gateway reconnects |
| Heartbeat | Ping 20s, timeout 30s (`WEBSOCKET_CONNECTION_TIMEOUT`) | Ping 20s, timeout 30s |
| Max connections | 1000 | 1000 |
| `is_active` on close | Set `false` unconditionally, **even when a newer connection exists** | Set `false` only when no connections remain |
| `connection.ack` | `{type, gatewayId, connectionId, timestamp}` | identical |
| Inbound messages | `deployment.ack` only | `deployment.ack` only |
| Metrics | none | Periodic metrics log (connections, failures, events sent) |
| Read-only mode | none | TEMP read-only mode drops acks and skips `is_active` writes |

## 3. Event hub

| Aspect | AMS (`eventhub/sqlbackend.go`, mig `021`) | `common/eventhub` (platform-api schema `internal/database/schema.*.sql`) |
|---|---|---|
| Tables | `eventhub_gateway_states`, `eventhub_events` | `gateway_states`, `events` |
| Keys | `event_id` is a global PK; `gateway_id TEXT` with no FK to `gateways` | PK `(gateway_id, event_id)`; FK `gateway_states → gateways` with `ON DELETE CASCADE` |
| Timestamp type | `TIMESTAMP` (no tz) | `TIMESTAMPTZ` |
| Dialects | Postgres only | Postgres, SQLite, SQL Server |
| Publish | One transaction: ensure the state row, insert, bump `version_id`; takes a `ctx`; fails when the version update hits 0 rows | Same transaction, no `ctx` |
| Row semantics | `entity_type` = gateway event type (`agent.deployed`); `entity_id` = artifact ID (random for key revoke/update); `action` ∈ CREATE/DELETE/UPDATE/PROVISION/REVOKE | `entity_type` = constant `PLATFORM_GATEWAY_EVENT`; `entity_id` = **gateway ID**; `event_id` = correlation ID; `action` normalised to CREATE/UPDATE/DELETE |
| Poll | Pages of 200 gateway states, version compare, every **2s (hardcoded)** | Same algorithm, **3s (configured)** |
| Resume cursor | `(processed_timestamp, event_id) > (?, ?)`, strictly after | `processed_timestamp >= ?` with a single-boundary trim heuristic |
| Cold start (no cursor on this replica) | **All retained events.** The dedup branch is effectively unreachable (see summary) | **Last 120s** (`initialPollSkewWindow`) |
| Reconnect to the same replica | Cursor kept; polls with no subscriber do not advance it | Cursor kept, `knownVersion` reset on the last unsubscribe |
| Retention and cleanup | 1h; cleanup every 5m; not configurable (`wiring/wire.go:437`) | 1h; cleanup 10m; `eventhub.*` config keys |
| Config validation | Intervals must be > 0 | none in the backend |
| Ordering | Per gateway by the publisher's `time.Now()` | same |

Shared weakness on both sides **(inferred)**: the cursor is a publisher wall-clock timestamp. An event whose transaction commits after a later-stamped event has already been polled, or one stamped by a replica whose clock lags, falls behind the cursor and is never delivered. A monotonic sequence cursor would fix this for both.

## 4. Event catalogue

| Event | AMS emits | platform-api emits | Gateway handles |
|---|---|---|---|
| `api.deployed/undeployed/deleted` | defined but **no callers** | yes | yes |
| `llmprovider.deployed/undeployed/deleted` | yes | yes | yes |
| `llmproxy.deployed/undeployed/deleted` | yes | yes | yes |
| `mcpproxy.deployed` / `.deleted` | yes | yes | yes |
| `mcpproxy.undeployed` | **no** | yes | yes |
| `agent.deployed` / `.deleted` | yes | yes | yes |
| `agent.undeployed` | **no** (redesign adds it) | yes | yes |
| `graphqlapi.*`, `websub.*`, `webbroker.*` | no | yes | yes |
| `websub.hmacsecret.*` | no | yes | yes |
| `apikey.created/updated/revoked` | yes | yes | yes |
| `subscription.*`, `subscriptionPlan.*` | no | yes | yes |
| `application.updated` | yes | yes (plus `.created` and `.deleted`) | `.updated` only |
| `gateway.config.updated` | no | yes | not in the gateway's switch **(unverified whether handled elsewhere)** |

Payload shapes are broadly compatible. Field-level differences (spot-checked, **completeness unverified**):

- AMS LLM provider events carry extra `gatewayId`, `organizationId` and `status`.
- The platform-api `AgentDeletionEvent` adds `apiId`.
- The AMS `APIKeyCreatedEvent` has `displayName` and `operations` but lacks `issuer` and `externalRefId`.
- The platform-api DTO adds `userId` (actor).

**Kinds served to the gateway**

- AMS: `LlmProvider`, `LlmProxy`, `Mcp` (from `McpProxy` and `McpProxyMapping`), `Agent`.
- platform-api: also `RestApi`, `GraphQLApi`, `WebSubApi` and `WebBrokerApi`, plus subscriptions, plans, secrets and HMAC secrets.

## 5. Internal REST API (`/api/internal/v1`)

| Endpoint | AMS | platform-api | Gateway calls it |
|---|---|---|---|
| `GET /deployments[?since]` | **returns `{"deployments":[]}`** | real; `state` from `status_desired`, `deployedAt` from `performed_at`, `etag` is a UUIDv7 | on first connect |
| `POST /deployments/fetch-batch` | **404** | tar.gz, rendered per gateway version, secrets inlined | in the sync |
| `POST /artifacts/exists` | **404** | yes | before orphan deletion |
| `POST /artifacts/import-gateway-artifacts` | **404** | yes | bottom-up push on each connect and on create |
| `GET /secrets`, `GET /secrets/{handle}/value` | **404** | yes | on every connect |
| `GET /llm-providers/{id}`, `/llm-proxies/{id}`, `/mcp-proxies/{id}`, `/agents/{id}` | yes (zip) | yes (zip) | after events |
| `GET /apis/{id}`, `/apis/{id}/subscriptions`, `/graphql-apis/{id}`, `/websub-apis/{id}[/secrets]`, `/webbroker-apis/{id}` | no | yes | per kind |
| `GET /llm-providers/api-keys`, `/llm-proxies/api-keys` | yes | yes | key bulk sync |
| `GET /apis/api-keys` | yes, **but it returns Agent keys** | RestApi keys | key bulk sync (as RestApi) |
| `GET /agents/api-keys` | **404** | yes | key bulk sync at HEAD only |
| `GET /graphql-apis/api-keys`, `/websub-apis/api-keys`, `/webbroker-apis/api-keys` | no | yes | key bulk sync |
| `GET /subscription-plans` | returns `[]` | real | on every connect |
| `GET /applications` | yes | **no** | **no caller in the gateway at HEAD or at the tag** |
| `POST /gateways/{id}/manifest` | yes | yes | on every connect |

Neither side has scopes on the internal API: a gateway token is an org-wide read credential plus a manifest write.

## 6. Deployment data model

| Aspect | AMS | platform-api |
|---|---|---|
| `deployments` | id, name, artifact, `ou_id`, gateway, base deployment, `content`, `metadata` | the same plus `build_uuid`, `data_version` (feeds the translator), `created_by` |
| `deployment_status` columns | artifact, `ou_id`, gateway, `deployment_id`, `status`, `updated_at` | plus **`status_desired`, `performed_at`, `performed_by`, `status_reason`** |
| Allowed statuses | CHECK `DEPLOYED`/`UNDEPLOYED`; `ARCHIVED` derived | `DEPLOYED`, `UNDEPLOYED`, `DEPLOYING`, `UNDEPLOYING`, `FAILED`; `ARCHIVED` derived |
| How a deploy writes status | **Optimistic `DEPLOYED`** at request time for LLM, MCP and A2A (`CreateWithLimitEnforcement`, `SetCurrent`) | `DEPLOYING` with desired `DEPLOYED` and `performed_at` truncated to ms, then broadcast (not one transaction) |
| Ack keying | `deployment_id` + gateway, **unguarded** (`UpdateStatusByDeploymentID`) | artifact + org + gateway + **`performed_at`** (`UpdateStatusWithPerformedAtGuard`) |
| Ack transitions | Deploy fail → `UNDEPLOYED`; undeploy fail → `DEPLOYED`; only for `resourceType` ∈ {llmprovider, llmproxy, mcpproxy, **agentproxy**} | Fail → `FAILED` plus a sanitised reason code, from any status; success only from the matching transitional status; `resourceType` ignored |
| Timeout | none | `DeploymentTimeoutService`: every 20s, stale after 60s, → `FAILED`/`TIMEOUT` (guarded); runs on every replica |
| Sync exclusions | n/a (empty list) | excludes artifacts with `origin = 'gateway_api'` |
| Read-only mode | none | TEMP mode: rejects internal writes, drops acks, pauses the timeout job |
| A2A | publication and card queues, two reconcilers, a publisher | ordinary `AgentProxy` deployments |

## 7. Consequences on the gateway-controller

| # | Consequence | Status |
|---|---|---|
| C1 | **Startup sync is a no-op.** An empty list puts every control-plane-origin config in `toDelete`. `CheckArtifactsExist` then gets a 404 and the gateway "skips deletions to avoid data loss" (`pkg/controlplane/sync.go:104-118`). Nothing is fetched. Orphans on the gateway are never removed. | Verified |
| C2 | **Secret sync fails on every connect.** `FetchPlatformSecrets` gets a 404, which is logged at error level and returned. Harmless only because AMS inlines secrets itself. | Verified (code); impact inferred |
| C3 | **Bottom-up push fails.** `PushGatewayArtifactsToControlPlane` retries the 404 with backoff, then gives up and leaves the `cp_sync_status` of gateway-origin artifacts failed or pending. Operator- or REST-created artifacts never reach AMS. | Verified (code); exact 404 classification **(unverified)** |
| C4 | **HA controllers evict each other.** Two controllers for one gateway on the **same AMS replica** evict each other. After a clean close the gateway calls `Connect()` again immediately (`connectionLoop`, no delay, retry count reset on success). The loop runs until AMS's 10/min per-gateway limit returns 429, which is not permanent, so it backs off. Each successful reconnect re-runs the reconnect sync: secrets, artifact push, plans, API keys and the manifest. On **different** AMS replicas there is no eviction, so behaviour depends on load-balancer affinity. | Code verified; runtime behaviour inferred |
| C5 | **`is_active` flaps to false** when an evicted connection's handler finishes after its replacement registered. | Verified (code) |
| C6 | **The AMS cold-start replay is a full hour.** A gateway that lands on a replica without a cursor receives every retained event, including superseded deploys. Acks for superseded deployment IDs match no row and are logged as errors. It does catch up events missed while the gateway was away, within one hour. | Verified (code) |
| C7 | **On platform-api, events are lost after a replica restart.** A controller that reconnects (no restart, so no deployment sync) to a fresh or restarted replica gets only 120s of replay. Events from a longer outage are lost until the controller restarts. | Inferred |
| C8 | **Agent acks would have been dropped after a gateway bump.** At the pinned tag the gateway sends `agentproxy`; at HEAD it sends `Agent` (`ackResourceTypeAgent = models.KindAgent`). AMS at the branch base accepts only `agentproxy`. **Fixed on upstream/main** by `f265599df` (PR #2063), which accepts both. | Verified (code at both revisions; fix verified on upstream/main) |
| C9 | **Agent key revocations are not reconciled.** At HEAD the gateway fetches `/agents/api-keys`, gets a 404 and skips reconciliation for the Agent kind. AMS serves Agent keys under `/apis/api-keys`, so they are upserted but reconciled against the empty RestApi artifact set. A key revoked while a gateway was away longer than the 1h retention stays valid on that gateway. | Inferred (`ListAPIKeysForArtifactsNotIn` semantics **unverified**) |
| C10 | **A gateway upgrade is invisible to AMS.** AMS ignores the manifest version and has no translator, so a gateway upgrade can change wire semantics with no AMS-side signal (C8 is one instance). | Verified (absence) |
| C11 | **An event can arrive before `connection.ack`.** Both control planes subscribe to the hub inside `Register`, before sending `connection.ack`. If a poll fires first, the gateway's `waitForConnectionAck` reads an event, fails the connect and retries. The risk is higher on AMS because of the cold-start backlog. | Inferred, low probability |
| C12 | **Deployment state is only as good as the last event or ack.** With optimistic `DEPLOYED` and no timeout job, AMS reports "deployed" for anything it sent, even to an offline gateway. | Verified (code) |

## 8. Gateway-controller version AMS ships

- **Pins.** `deployments/setup/env.sh:8-10` and `deployments/quick-start/install.sh:40-49` set gateway-operator `0.11.0`, chart `2026.09.24` and image `2026.09.24` for both `gateway-controller` and `gateway-runtime`. Docker-compose and the AMS Helm extension chart pin no separate controller image.
- **What the tag lacks.** `gateway/v2026.09.24` exists in api-platform. HEAD carries roughly 2.2k changed lines in `pkg/controlplane` and `pkg/utils/api_utils.go` since the tag. Commits not in the tag include:
  - Agent ack and state work (`4b98bbac8`, `b9092d1cc`);
  - agent API-key capabilities (`4c5ea2f94`);
  - GraphQL in the key bulk sync (`45e158f86`).
- **Why it matters.** STS builds are date-named and platform-api treats a date-named build as "current". AMS has no version handling, so its compatibility is "whatever `2026.09.24` does". Every future bump needs a manual protocol diff. C8 shows the next bump already breaks Agent acks unless the redesign (which accepts both strings) lands first.

## 9. What the A2A redesign fixes and what it leaves

**Fixes (for AMS):**
- real `GET /deployments` for all kinds, `POST /deployments/fetch-batch` and `POST /artifacts/exists` (closes C1);
- `status_desired`, `status_reason` and `performed_at`, a guarded ack for Agents that accepts **both** `Agent` and `agentproxy` (C8 is already fixed upstream by PR #2063);
- a timeout job that infers from sync evidence (`last_sync_at`, `fetched_at`);
- `agent.undeployed`, and events targeted at the status row's gateway instead of every gateway in the org;
- a failed LLM/MCP deploy ack sets desired `UNDEPLOYED`;
- the A2A queues and reconcilers go away.

**Deliberate divergences from platform-api (spec):**
- a success ack is accepted from `FAILED`;
- the `SYNCED_UNCONFIRMED` inference;
- timeouts of 1m/5m instead of 20s/60s;
- `etag` = SHA-256 hex instead of UUIDv7. This is cosmetic: the gateway uses `etag` only as a correlation ID (`sync.go:651`), and it re-sorts by kind itself.

**Leaves divergent:**
- WebSocket eviction and HA (C4, C5);
- the forked event hub;
- LLM provider, LLM proxy and MCP status still optimistic and unguarded;
- no `mcpproxy.undeployed`;
- no `/secrets`, `/artifacts/import-gateway-artifacts` or `/agents/api-keys`;
- hardcoded hub knobs;
- no gateway version awareness;
- no code sharing.

**A dependency the spec relies on:** "the hub's one-hour retention is the bridge" only holds because AMS replays the **full** retention on cold start. If AMS adopted `common/eventhub` with its 120s window before the gateway syncs on reconnect, that bridge would shrink to two minutes.

---

## Alignment plan

| # | Repo | Change | Why | Size | Risk | Depends on |
|---|---|---|---|---|---|---|
| **P0 (now)** | | | | | | |
| A1 | AMS | Ship the A2A redesign release one (spec and plan) | Closes C1, C8 and C12 for Agents; gives a real sync list | L | Medium: the first gateway restart after the upgrade refetches everything, and an incomplete list deletes configs | a2a-proxy (mig 048) |
| A2 | AMS | Replace singleton eviction with platform-api's multi-connection model plus a per-gateway dispatcher; keep `registryMu`; set `is_active=false` only at zero connections; `Unregister` on send failure | Closes C4 and C5; matches HA gateway-controllers | M | Medium: eviction was added for kgateway stale upstreams, so rely on heartbeat and send-failure reaping instead | none |
| A3 | AMS | Serve `GET /agents/api-keys` (Agent keys); keep `/apis/api-keys` for pre-HEAD gateways | Lets HEAD gateways reconcile revoked Agent keys (C9) | S | Low | none |
| A4 | AMS | Before any gateway bump: diff the protocol between `2026.09.24` and the target; add a CI check that greps the pinned tag's `resourceType` strings and endpoint list | Catches the next C8 | S | Low | none |
| **P1 (next)** | | | | | | |
| A5 | AMS | Extend desired/observed to LLM provider, LLM proxy and MCP: write `DEPLOYING` with ms `performed_at`, guarded acks, include them in the timeout job; emit `mcpproxy.undeployed` | Removes the second ack path the spec keeps; console truthfulness for all kinds | M | Medium: event payloads must carry `performedAt` (they do today) | A1 |
| A6 | AMS | `GET /secrets` returning an empty list with 200 (or real secrets if AMS adopts `{{ secret }}`) | Stops the error log on every connect (C2) | S | Low | none |
| A7 | AMS | `POST /artifacts/import-gateway-artifacts`: either implement it, or return a per-artifact "unsupported" result in the gateway's response shape so the gateway records one failure instead of retrying | C3; prerequisite for the operator/CR path the spec defers | M | Low (stub); High (real) | none |
| A8 | AMS | Record the gateway version from the manifest; reject or flag unsupported versions | C10; groundwork for a translator | S | Low | none |
| B1 | common | Adopt AMS's `(processed_timestamp, event_id)` cursor; make the cold-start window configurable (AMS needs "full retention" until C1 lands) | Removes the `>=` and trim heuristic; lets AMS consume `common` without losing its bridge | S | Low: query change only | none |
| B2 | platform-api | Make `Manager.Register` atomic (port `registryMu`) | Lost-connection race | S | Low | none |
| B3 | platform-api | Rate-limit per gateway after auth (keep a coarse per-IP pre-auth limit) and prune the map | IP keys conflate gateways behind one ingress; unbounded map | S | Low | none |
| B4 | platform-api | Accept a success ack from `FAILED` (same `performed_at`) | A gateway that converges after the 60s timeout leaves the row `FAILED` forever | S | Low | none |
| C1g | gateway-controller | Run `syncDeployments` on reconnect (and on a jittered timer) | Removes C7 and the need for long hub replay; makes A1 robust | M | Medium: deletion on reconnect relies on `/artifacts/exists` from every control plane | none |
| C2g | gateway-controller | Send `deployment.ack` from `processSyncFetchBatch`, `processSyncStatusUpdates` and `processSyncDeletions` | Lets both control planes drop sync-evidence inference and false `TIMEOUT` | M | Low | none |
| **P2 (structural)** | | | | | | |
| D1 | api-platform | Make `common/eventhub` an importable, tagged module (its own `go.mod`, no `httpkit` `replace`); add a table-name prefix option so AMS keeps `eventhub_*` | AMS's `go.mod` has zero api-platform deps; `common` cannot be consumed externally today | M | Low for platform-api | B1 |
| D2 | AMS | Switch to `common/eventhub` and delete `agent-manager-service/eventhub/` | Ends the fork | M | Medium: replay semantics change, so set the cold-start window to the retention period | D1, B1 |
| D3 | api-platform, then AMS | Extract the WS manager, dispatcher and transport into a shared package (for example `common/gatewayws`) with hooks for auth, rate-limit key and ack handler | Ends the second fork; A2 becomes a dependency bump | M/L | Medium | A2, D1 |
| D4 | api-platform | Publish a protocol contract package: event type constants, payload DTOs, ack DTO, `ControlPlaneDeployment`, kind strings; imported by both control planes and the gateway | Makes C8-class drift a compile error | L | Medium: touches all three components | D1 |
| D5 | api-platform | A control-plane conformance suite: the gateway's sync, ack and key flows run against any control plane URL; AMS runs it in CI against its pinned and next gateway | Continuous detection of everything in section 7 | M | Low | none |
| B5 | common / platform-api | Optional: a monotonic sequence column as the cursor | Closes the commit-order and clock-skew loss on both sides | M | Medium: additive column only (R0-frozen tables allow it) | B1 |
| B6 | platform-api | Optional: token prefix plus caches (additive `token_prefix` column, backfill on rotation) | Fewer DB hits per connect and internal call; resistance to fake-key bursts | M | Medium | none |
| C3g | gateway-controller | Treat 404 on optional endpoints (`/secrets`, `/artifacts/import-gateway-artifacts`, `/*/api-keys`) as "capability absent", or advertise capabilities in `connection.ack` | Quiet, explicit degradation against partial control planes | M | Low | none |
| C4g | AMS and platform-api | Send `connection.ack` before creating the hub subscription | C11 | S | Low | none |

### Notes

- **Fix the migration backfill in A1 before implementing it.** Migration 049 backfills `performed_at` from `updated_at`. The gateway writes an artifact only if the incoming `deployed_at` is strictly newer than the stored one (`pkg/storage/sql_store.go:733`), so wherever the backfill is older than the event timestamp the gateway stored, every restart re-downloads the artifact, is told it is stale, and writes nothing. Backfill with the migration time, truncated to milliseconds, so each gateway refetches exactly once.
- **Order inside AMS: A1, then A3 and A4, then A2.** A2 is independent of A1 but touches the same wiring, so land it after A1 to keep the redesign reviewable.
- **Do not shrink AMS's replay window** (D2 with `common`'s 120s default) until C1g is deployed on the gateway build AMS pins. Until then the full-retention replay is AMS's only catch-up path for a controller that reconnects without restarting.
- **Optimistic `DEPLOYED` and a real sync list combine dangerously.** If A5 is skipped, a failed LLM/MCP deploy that never acks stays desired `DEPLOYED` and is retried at every gateway restart. The spec's "failed ack sets desired `UNDEPLOYED`" covers only acks that arrive.
- **The spec's `SYNCED_UNCONFIRMED` inference is a stopgap that platform-api lacks.** Platform-api has the same gap: a row converged by sync after its timeout stays `FAILED`. Prefer C2g over porting the inference to platform-api; B4 is the cheap interim fix.
- **Skip the TEMP read-only mode.** It is explicitly temporary in platform-api; AMS should not adopt it.
- **The internal API has no scopes on either side.** That is acceptable for a machine credential, but the AMS audit ledger (every internal call recorded) is worth upstreaming once D3 exists.
- **Rule notes.** Per `.claude/rules/dependency-management.md`, D1 and D2 must pass `govulncheck` and `go-licenses`. The `common` module pulls `go-mssqldb` and `go-sqlite3` (cgo) into AMS's module graph, so check the transitive diff. Per `db-schema-changes.md`, B5 and B6 are additive-only on shipped platform-api tables.

## Unverified points

- Whether the gateway's `PushArtifacts` classifies a 404 as a transport error (retried) or a per-artifact error.
- The semantics of `ListAPIKeysForArtifactsNotIn` with an empty artifact set (underpins C9).
- Whether `gateway.config.updated` is handled outside `handleMessage`.
- Completeness of the payload field comparison (spot-checked eight event types).
- Whether anything outside gateway-controller (another gateway build or fork) calls AMS `GET /applications`.
- Runtime behaviour of C4, C7 and C11 (derived from code, not exercised).
