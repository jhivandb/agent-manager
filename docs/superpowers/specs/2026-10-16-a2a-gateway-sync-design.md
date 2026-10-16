# A2A gateway sync: desired state instead of publication queues

Date: 2026-10-16. Status: proposed, pending leadership sign-off.

## Intent

Replace the A2A publication reconciler, the card reconciler, the publisher
and the guards that hold them together with the model the API Platform
control plane already uses with the same gateway: agent-manager records
the *desired* state of each artifact on each gateway, the gateway pulls
the authoritative set and converges, and the gateway's acknowledgement is
the only thing that ever sets the *observed* state.

The current design tracks work ("publish this agent"), so it needs a job
queue, retries, leases, supersede detection, a drift scan, a second queue
for cards, and a dozen "was the agent deleted meanwhile" sweeps. The
desired-state design tracks state, so "pending work" is simply a row whose
observed status differs from its desired status, and the gateway drains
it by syncing. There is nothing for agent-manager to drain.

Why now: the external A2A proxy feature (migration 048) is about to ship
on the queue model. Every future A2A change threads through roughly 4,000
lines of guards and two tickers. The gateway half of the replacement
already exists in the shared gateway-controller: its startup sync calls
`GET /deployments`, diffs by artifact, batch-fetches changed items, and
deletes orphans after an existence check. agent-manager answers that
endpoint with an empty list today, so the sync is a no-op by accident.

## Goals

- One table holds desired and observed state per artifact and gateway,
  for every kind the gateway serves, shaped like platform-api's
  `deployment_status`.
- The gateway can rebuild its full control-plane state from agent-manager
  on startup. A lost event costs latency, never consistency.
- Console status is truthful: "published" means the gateway acked;
  "failed" carries the gateway's reason.
- No OpenChoreo call on the external-agent path. OpenChoreo is one
  implementation of a small upstream-resolver interface, used only for
  platform agents.
- No Kubernetes assumption. The pull-based control plane is the primary
  transport; an Agent CR sink is a later option.
- The proxy and agent-card API shapes do not change.
- Delete both reconcilers, both queue tables (in two releases), the
  publisher, the drift scan, and the org-wide delete fan-out.

## Non-goals

- Changing the gateway-controller in this change. It is shared with
  api-platform and already does the pull. Two gateway-side asks are filed
  separately: deployment sync on reconnect or on a timer, and
  acknowledgements from the sync path (today only the per-event handlers
  send `deployment.ack`; `processSyncFetchBatch`, `processSyncStatusUpdates`
  and `processSyncDeletions` do not). Neither is a dependency, because
  AMS treats sync evidence as a weaker observation until they land; see
  "Observed state without an ack".
- Moving identity from name tuples to UUIDs. Cards stay keyed by names
  for now; see Deferred.
- Writing Agent CRs. The operator path on api-platform main excludes
  operator-created artifacts from control-plane sync and pushes them back
  bottom-up to an endpoint agent-manager does not implement.
- Changing how LLM providers, LLM proxies and MCP proxies are published.
  Their writers set `status_desired` alongside `status`, they appear in
  the sync list, and their acks keep today's semantics with one addition
  described under "Acks for other kinds". Nothing else.

## Anti-goals

- A second queue in disguise: no attempt counts, no leases, no
  next-attempt timestamps on the new rows.
- Any AMS-side code that marks something deployed because it sent an
  event.
- A sync list that is agents-only. The gateway treats the list as
  authoritative for every control-plane-origin artifact it holds; an
  incomplete list deletes LLM and MCP configs on the next gateway restart.

## Constraints

- Builds on top of the a2a-proxy branch (migration 048, `a2a_proxies`),
  which ships first. The `source` column 048 adds to `a2a_publications`
  dies with that table; the one on `a2a_agent_cards` stays.
- Migrations are forward-only gormigrate steps run at AMS startup. The
  schema change is additive in release one and destructive in release
  two, so a rolling upgrade with mixed replicas is safe.
- The gateway's sync client compares `deploymentId` and `deployedAt`
  per artifact, applies in the order providers, proxies, RestApis, MCP,
  Agents, batch-fetches 50 at a time as a tar.gz, and deletes local
  artifacts missing from the list only after `POST /artifacts/exists`
  confirms they are gone. On that endpoint failing it skips deletions.
- Gateway kind strings are fixed by the gateway: `Agent`, `Mcp`,
  `LlmProvider`, `LlmProxy`, `RestApi`. AMS artifact kinds are `Agent`,
  `McpProxy`, `LlmProvider`, `LlmProxy`, `RestAPI`; `McpProxyMapping`
  artifacts are also served to the gateway as `Mcp`.
- Desired state values the gateway parses are `deployed` and
  `undeployed`, case-insensitive. Transitional states are never sent.
- The ack payload carries `deploymentId`, `artifactId`, `resourceType`,
  `action`, `status`, `performedAt`, `errorCode`.
- The SQL-backed event hub keeps its one-hour retention. Until the
  gateway syncs on reconnect, it is the bridge for gateways that
  disconnect briefly.

## The model

### One status row per artifact and gateway

`deployment_status` already exists with `artifact_uuid`, `ou_id`,
`gateway_uuid`, `deployment_id`, `status`, `updated_at`. Three columns are
added:

| Column | Meaning |
|---|---|
| `status_desired` | `DEPLOYED` or `UNDEPLOYED`. What the gateway must converge to. |
| `status_reason` | Free text, empty on success. The gateway's error code, `TIMEOUT`, or an upstream-resolution error. |
| `performed_at` | The timestamp stamped on the event that started the current operation. Acks must match it. |

`status` gains the values `DEPLOYING`, `UNDEPLOYING`, `FAILED` and, for
agents only, `WAITING_UPSTREAM`. `DEPLOYED` and `UNDEPLOYED` keep their
meaning. `ARCHIVED` stays derived.

`deployments` gains one column, `fetched_at`, set whenever a gateway
reads the row's content (per-kind fetch endpoint or batch fetch). For
agents the row is created when desired state is set; its `content` is
empty until the upstream is known and the YAML is rendered. `metadata`
carries `upstreamUrl` and `upstreamVersion` once rendered.

`gateways` gains one column, `last_sync_at`, set whenever the gateway
calls `GET /deployments`.

`performed_at` is always truncated to milliseconds, so it compares equal
to the `performedAt` the gateway echoes in acks and stores as
`deployedAt`. An undeploy keeps the row's `deployment_id` and stamps a
fresh `performed_at`.

### The gateway is chosen when desired state is written

The status row's key includes the gateway, so the writer resolves the
environment's ingress gateway (as `resolveIngressGateway` does today) in
the request. When the environment has no ingress gateway yet:

- a user-initiated proxy or card-source request fails with the existing
  public error, 409 "no ingress gateway is mapped to this environment
  yet", and the user retries after the gateway is registered;
- a platform deploy, promote or deploy-settings save does not fail (the
  OpenChoreo side has already happened); it logs at error level and
  writes nothing, as the best-effort enqueue does today.

What closes that gap is a hook on gateway registration: when a gateway
is mapped to an environment, AMS publishes every `Agent` artifact in
that environment that has no status row on that gateway. Re-mapping an
environment to a different gateway is deferred; the hook publishes to
the new gateway but does not withdraw from the old one.

### Who writes what

Request handlers write desired state, in the same transaction as the
change that caused it:

| Request | Desired write |
|---|---|
| Deploy, promote, deploy-settings save, security save | new deployment row, status row desired `DEPLOYED`, status `WAITING_UPSTREAM` (platform) or `DEPLOYING` (external, upstream resolvable now) |
| Set proxy, refresh proxy, set card source, refresh card source | same, external |
| Delete proxy, remove card source (switch to direct) | status row desired `UNDEPLOYED`, status `UNDEPLOYING`; artifact and API keys kept |
| Undeploy agent | same as switch to direct |
| Delete agent, delete environment | status rows deleted, artifacts deleted, `agent.deleted` sent to the gateways that had rows |

Only three things write observed state:

- The ack handler, guarded by `performed_at` and by the set of statuses
  the ack may transition from. A success ack is accepted from `FAILED` as
  well as from the transitional state, so a gateway that converges late
  repairs the row. This deliberately differs from platform-api.
- The timeout job, described under "Observed state without an ack".
- The upstream worker, which moves agent rows from `WAITING_UPSTREAM` to
  `DEPLOYING` once it has rendered content.

### Observed state without an ack

The gateway acks only events. A row the gateway converged through its
startup sync never gets an ack, and that is the normal path for every
row migration 049 creates, every row whose event fell outside the hub's
retention, and every undeploy of an artifact a fresh gateway never held.
Until the gateway acks from its sync path, AMS resolves such rows from
two pieces of evidence it does hold: the gateway's `last_sync_at`, and
the deployment's `fetched_at`.

Every minute the timeout job looks at `DEPLOYING` and `UNDEPLOYING` rows
whose `performed_at` is more than five minutes old:

| Row | Evidence | Result |
|---|---|---|
| `DEPLOYING` | gateway synced after `performed_at` and fetched this deployment after `performed_at` | `DEPLOYED`, reason `SYNCED_UNCONFIRMED` |
| `UNDEPLOYING` | gateway synced after `performed_at` | `UNDEPLOYED`, reason `SYNCED_UNCONFIRMED` |
| either | no sync since `performed_at` | `FAILED`, reason `TIMEOUT` |
| `DEPLOYING` | synced but never fetched | `FAILED`, reason `TIMEOUT` |

The reason is kept on the row so the console can show "published, not
confirmed by the gateway" if it ever wants to, and so the inference is
removable: once the gateway acks from sync, the first two rows of the
table are deleted and nothing else changes. This is the one place AMS
infers observed state, and it infers from what the gateway pulled, never
from what AMS sent.

### Acks for other kinds

The guarded transition set applies to `Agent` acks only. LLM provider,
LLM proxy and MCP proxy events stamp `performedAt` at send time and
their deploy paths write `DEPLOYED` optimistically today; a guard would
reject every one of their acks. Their acks keep today's handling, keyed
by deployment ID, with one addition: a failed deploy ack also sets
`status_desired = UNDEPLOYED`, so the next gateway sync does not
re-deploy an artifact the user watched fail. The user redeploys
explicitly. Their writers (`SetCurrent`, `CreateWithLimitEnforcement`
call sites) set `status_desired` equal to the status they write.

### The upstream resolver

```go
type UpstreamRef struct {
    OUID, ProjectName, AgentName, EnvironmentName string
    EnvironmentUUID uuid.UUID
    Source models.A2APublicationSource // platform or external
}

type Upstream struct {
    URL        string
    Version    string           // release name for platform agents, card hash for external
    Transports []A2ATransport   // nil keeps /rpc and /rest
    Auth       *models.UpstreamAuth
}

type UpstreamResolver interface {
    // Resolve returns ErrUpstreamNotReady while the runtime has no address yet.
    Resolve(ctx context.Context, ref UpstreamRef) (Upstream, error)
}
```

Two implementations. The OpenChoreo one reads the release binding's
service URL and release name. The external one reads the proxy row and
card source, fetches the origin card SSRF-guarded, and derives routes,
exactly as the reconciler does today. A third runtime later means a third
implementation and nothing else.

### The upstream worker

The only background loop left. Every 30 seconds it claims up to 50
`WAITING_UPSTREAM` agent rows with `UPDATE ... WHERE status =
'WAITING_UPSTREAM' ... FOR UPDATE SKIP LOCKED RETURNING`, resolves each,
and on success renders the Agent YAML into the deployment row, sets
`DEPLOYING` with a fresh `performed_at`, and broadcasts `agent.deployed`.
Not-ready rows are left as they are, with `status_reason` set to the
resolver's message so the console can show it. A resolver error that is
not not-ready sets `FAILED` with the error as reason; the next desired
write retries it. There is no attempt budget: a platform agent whose
first build takes an hour simply waits an hour.

Every five minutes the same worker checks one page of 50 `DEPLOYED`
platform-agent rows against the resolver's `Version` and `URL`. A change
means a rebuild moved the upstream without AMS involvement; the worker
writes a new desired state for that row. This replaces the drift scan
with a bounded check and goes away once a runtime change notification
exists.

### The gateway sync endpoints

Three internal endpoints, gateway-key authenticated like the existing
ones, all under `/api/internal/v1`:

- `GET /deployments[?since=RFC3339]` returns every `deployment_status`
  row for the gateway, all kinds, except rows in `WAITING_UPSTREAM`.
  Each item: `artifactId`, `deploymentId`, `kind` (gateway kind string),
  `state` (`deployed` or `undeployed`, from `status_desired`),
  `deployedAt` (`performed_at`), `etag` (SHA-256 hex of deployment ID
  and `performed_at`).
- `POST /deployments/fetch-batch` takes `{"deploymentIds": [...]}` and
  returns `application/x-tar+gzip` with one entry per found deployment at
  `<deploymentId>/<kind>.yaml`, content with secret references resolved
  as `GET /agents/{id}` resolves them today. Unknown IDs and empty-content
  rows are omitted.
- `POST /artifacts/exists` takes `{"artifactIds": [...]}` and returns
  `{"artifacts": [{"artifactId", "exists"}]}` against the `artifacts`
  table for the gateway's org.

The existing per-kind fetch endpoints stay; the gateway still uses them
after an event.

### Events

The event vocabulary is unchanged: `agent.deployed`, `agent.undeployed`,
`agent.deleted`. They are sent to the gateway named on the status row,
never to every active gateway in the org. The deployed and undeployed
events carry the row's `performed_at`.

### Status as the API sees it

Proxy `GET` keeps `status`, `lastError`, `upstreamAuthHeader`:

| Row | API status |
|---|---|
| desired `DEPLOYED`, status `DEPLOYED` | `published` |
| status `WAITING_UPSTREAM`, `DEPLOYING` | `pending` |
| status `FAILED` | `failed`, `lastError` = `status_reason` |
| no proxy row | 404, as today |

Card `GET` keeps `source`, `status`, `sourceUrl`, `lastError`, `card`,
`fetchedAt`. The card table becomes a cache keyed to the deployment the
gateway acked:

| Condition | API status |
|---|---|
| cached card's `for_deployment_id` equals the row's acked deployment | `fetched` |
| gateway row not `DEPLOYED` yet, or cache behind it, or fetch in flight | `pending` |
| last fetch for the acked deployment failed | `failed`, `lastError` |

A read that finds the cache stale triggers one asynchronous fetch per
key (singleflight across the process, and at most one attempt per ten
seconds per key across replicas via `attempted_at`). The console already
polls while pending.

For platform agents that are not proxied, the card is still fetched
through the environment's gateway, so the same rule applies with the
agent's own status row. A platform agent with no status row (never
deployed, or undeployed) answers 404 as today.

An external agent that is not proxied (direct) has no status row and no
gateway artifact. Its card is fetched from the registered URL, and
`for_deployment_id` stays null:

| Condition | API status |
|---|---|
| a card is cached for the current `source_url` | `fetched` |
| no card yet, or source changed, or fetch in flight | `pending` |
| last fetch for the current source failed | `failed`, `lastError` |

Refresh on a direct environment clears the cache so the next read
fetches from the URL again. Switching a proxied environment to direct
clears the cache too, because the gateway's card advertised gateway
URLs.

### Details settled here so nobody guesses

- The per-kind fetch endpoints (`GET /agents/{id}` and siblings) select
  the current deployment by `status_desired = DEPLOYED`, not by `status`,
  so a gateway can fetch content while the row is `DEPLOYING`.
- `GET /deployments?since=` filters on `performed_at`.
- The deployment list orders by kind in the gateway's apply order, then
  `performed_at` descending.
- Migration 049 picks the ingress gateway for a folded publication row by
  joining the gateway-environment mapping restricted to ingress roles and
  taking the earliest-created gateway when several match.

### Deletions

Deleting an agent removes its status rows and artifacts and sends
`agent.deleted` to the gateways that had rows. Deleting an environment
does the same for every artifact with that environment's handle suffix.
If an event is lost, the gateway's next sync does not list the artifact,
the existence check says it is gone, and the gateway deletes it. The
"agent recreated with the same name" sweeps are dropped: the new agent's
artifact gets its own UUID, and the old artifact's rows are deleted with
the old agent.

## Migration

Release one (additive, this change):

- Migration 049 adds the three status columns with defaults
  (`status_desired` backfilled from `status`, `performed_at` from
  `updated_at`, `status_reason` empty), adds `deployments.fetched_at` and
  `gateways.last_sync_at`, replaces the status check constraint, and folds
  `a2a_publications`: published rows set `status_desired = 'DEPLOYED'` on
  their existing status row; pending rows get a deployment row with empty
  content and a status row in `WAITING_UPSTREAM`; failed rows get the
  same with status `FAILED` and `status_reason = last_error`. The
  publication table is left in place, unread.
- `a2a_agent_cards` gains `for_deployment_id`, `attempted_deployment_id`
  and `attempted_at`; its queue columns are left in place, unread.
- Code stops reading or writing `a2a_publications`, the publication
  reconciler, the card reconciler and the publisher are deleted.

Release two (destructive, separate change after release one is
everywhere): drop `a2a_publications`; drop `status`, `attempt_count`,
`next_attempt_at`, `release_name`, `attempted_release_name` from
`a2a_agent_cards`.

Operational effects on upgrade:

- The deployment list becomes real for all kinds. The next restart of
  each gateway runs a full diff. Backfilled `performed_at` values will not
  match the millisecond event timestamps gateways stored, so each gateway
  refetches everything it holds once, in batches of 50. The batch-fetch
  endpoint must exist before that restart, which the ordering of this
  change guarantees.
- Nothing changes on a gateway until it restarts.
- A proxied environment mid-publish at upgrade becomes a
  `WAITING_UPSTREAM` row and completes on the worker's next tick.
- Rows the first post-upgrade gateway sync converges without an ack
  resolve to `SYNCED_UNCONFIRMED` within about six minutes of that sync.

## Deferred

- UUID-keyed identity for cards and proxies. The `agent_uuid` column
  added in 047 is empty for older rows and cannot be backfilled in SQL.
  Fill it lazily on the next write; switch keys in a later change.
- A runtime change notification replacing the five-minute version check.
- Deployment sync on gateway reconnect or on a timer, and acks from the
  sync path (gateway team). When the second lands, the
  `SYNCED_UNCONFIRMED` inference is deleted.
- Withdrawing from the old gateway when an environment is re-mapped.
- An Agent CR sink on Kubernetes, once AMS implements the bottom-up
  artifact endpoint the operator path needs.

## Left to the builder

- Exact names of the three new repository methods and the worker's
  claim query, within the shapes in the plan.
- How `McpProxyMapping` artifacts are labelled in the list: they are
  served to the gateway as `Mcp` today through the MCP proxy fetch
  endpoint, so the list says `Mcp`.
- Whether the card cache fetch runs in the request goroutine with a
  short timeout or in a detached goroutine. The spec requires only that
  the read returns promptly and that at most one fetch per key runs at
  a time.
