# Gateway sync: how control planes reach the gateway

As of 2026-10-16. Read alongside the other documents in this folder (see README.md).

A gateway that misses an event stays out of date until its controller restarts, because the full resync runs only at startup. Five changes close that gap and keep agent-manager from drifting further from api-platform.

## The pieces

```
Events are the fast path; drift is repaired only when the controller starts

┌──────────────────────────────────────────────────────────────────────────┐
│ Control plane: platform-api or agent-manager-service (AMS)               │
│ Where users create and deploy APIs, LLM and MCP proxies, and agents.     │
│ Several replicas, one database. Event hub: a table any replica writes to;│
│ the replica holding the gateway's WebSocket delivers.                    │
└──────────────────────────────────────────────────────────────────────────┘
     │ 1. event over WebSocket   ▲ 2. fetch content over REST  ▲ 3. deployment.ack
     ▼                           │                             │
┌──────────────────────────────────────────────────────────────────────────┐
│ gateway-controller: one per gateway, can run as several copies           │
│ Dials out to the control plane and keeps a local database of every       │
│ artifact the gateway should serve. Full sync (GET /deployments) runs     │
│ only at startup; after that it relies on events alone.                   │
└──────────────────────────────────────────────────────────────────────────┘
     │ xDS: routes, clusters, policy chains, API keys
     ▼
┌──────────────────────────────────────────────────────────────────────────┐
│ Data plane: Envoy router and policy engine                               │
│ Envoy routes live traffic; the policy engine runs auth, rate limits and  │
│ guardrails on each request. Both take their configuration from the       │
│ controller over xDS, Envoy's config protocol.                            │
└──────────────────────────────────────────────────────────────────────────┘
```

Three layers, each talking only to its neighbour; xDS is used only between the controller and Envoy.

- **Control plane.** The management service where users create and deploy. In api-platform it is **platform-api**; in agent-manager it is **agent-manager-service (AMS)**. Each runs as several **replicas** (identical copies behind a load balancer) over one database.
- **gateway-controller.** Runs beside each gateway. It connects out to the control plane, keeps a local database of what the gateway should serve, and turns that into Envoy configuration. The same controller code serves both products.
- **Data plane.** Envoy, which routes real traffic, and the policy engine, which applies auth, rate limits and other policies per request.

Terms used below:

- **Artifact:** one API, proxy or agent.
- **Deployment:** one version of an artifact sent to one gateway, identified by a deployment ID.
- **`performedAt` / `deployedAt`:** the timestamp the control plane stamps on a deploy; the gateway stores it as `deployedAt`.

## How a deploy reaches the gateway today

A deploy travels as a small notification over the WebSocket, and the controller then fetches the full content over REST.

1. **A user deploys.** The control plane writes the deployment to its database.
2. **It publishes an event to the event hub.** The hub is a database table (`eventhub_events`). The replica that handled the click is often not the one holding the gateway's connection, so the table acts as a shared mailbox: any replica writes, the replica holding the connection reads.
3. **The replica holding the connection polls the table** and pushes the event to the controller over the **WebSocket**. The controller opens that connection itself, so it works through firewalls and NAT. The event says "deployment X of API Y is ready"; it does not carry the content.
4. **The controller fetches the full content** from an internal REST endpoint (`GET /api/internal/v1/apis/{id}` and its siblings), saves it locally and pushes new config to Envoy.
5. **The controller sends a `deployment.ack`** over the WebSocket with success or failure. The control plane records it, and the UI shows "deployed" or "failed".

What supports those steps:

| Part | What it does |
| --- | --- |
| Event retention | Events are kept for 1 hour, then a cleanup job deletes them. |
| Replay cursor | Each replica remembers in memory how far into the table it has delivered for each gateway, so it can resume after a disconnect. |
| Heartbeats | Pings every few seconds. After 35 s of silence the controller drops the connection and reconnects with exponential backoff. |
| Status table and timeout job (platform-api only) | Each deployment row holds what the control plane wants (`status_desired`) and what the gateway has confirmed (`status`). A background job marks a row `FAILED` if no ack arrives within a few minutes. |

## The safety net: full sync

Events are the fast path; the full sync is what makes the gateway correct, and it runs only when the controller starts.

Events go missing when the gateway is offline, the connection drops mid-send, or the controller crashes while processing one. The controller's full sync (`syncDeployments` in `gateway-controller/pkg/controlplane/sync.go`) ignores events and compares state directly:

1. `GET /deployments` asks the control plane for everything this gateway should have.
2. The controller compares that list with its local database. Anything missing or at a different version is fetched; anything that should be undeployed is undeployed.
3. `POST /deployments/fetch-batch` downloads the changed artifacts, 50 at a time, as a tar.gz archive.
4. Local artifacts not on the list are deletion candidates. `POST /artifacts/exists` first confirms they are really gone, so a bad list cannot wipe a working gateway.

xDS, Envoy's config protocol, gets this property for free: it always sends the full current state, so a missed update does not matter. Here the same guarantee depends on this sync, and the controller runs it once per process start (`client.go:520-575`). On reconnect it re-syncs secrets, subscriptions and API keys, but not deployments.

## How agent-manager differs from platform-api

Agent-manager's gateway code is a copy of platform-api's that has drifted; each copy now has something the other lacks.

| Area | platform-api | agent-manager (AMS) |
| --- | --- | --- |
| Event hub code | Shared module `common/eventhub` | Own fork in `agent-manager-service/eventhub/` |
| Replay when a replica first sees a gateway | Last 120 s only | Every retained event (up to 1 hour), replayed verbatim |
| Connections per gateway | Several; each event goes to all of them | One; a new connection evicts the old |
| `GET /deployments` | Real list of what the gateway should run | Always an empty list |
| `fetch-batch`, `artifacts/exists`, secrets, artifact import | Served | Not served |
| Deployment status | Desired and confirmed state, acks checked against `performed_at`, timeout job | One status column, acks unchecked, no timeout job; A2A uses its own queues and reconcilers |

What that means in practice:

- **Startup sync does nothing in AMS.** The list is empty, so recovery depends entirely on events and the 1-hour hub replay.
- **Calls to endpoints AMS does not serve** (secrets, artifact push) should fail on every connect and show up in gateway logs (not checked at runtime).
- **The one-connection rule may break redundant gateways.** The controller supports several copies under one gateway identity; under AMS's rule they would evict each other in a loop (read from code, not tested).
- **The A2A sync redesign** moves AMS onto platform-api's status model and serves the sync endpoints. The event hub and connection handling stay different.

## The suggestions

| # | Change | Where | Fixes | Size |
| --- | --- | --- | --- | --- |
| 1 | Run the full sync on reconnect and on a jittered timer | gateway-controller (shared) | Drift that lasts until restart | Small |
| 2 | Backfill `performed_at` with the migration time | AMS redesign spec | A re-download loop on every restart | One line |
| 3 | Copy AMS's cold-start replay into the shared hub | `common/eventhub` | 120 s replay window in platform-api | Small |
| 4 | Decide one vs several connections per gateway | AMS | Redundant controllers evicting each other | Medium |
| 5 | Share the code instead of keeping forks | Both repos | Fixes landing in one copy only | Large |

### 1. Full sync on reconnect and on a timer

- **Problem.** After a reconnect, missed deploys, undeploys and deletes come back only if the hub still holds them: up to 1 hour in AMS, 120 s in platform-api when the gateway lands on another replica. Beyond that the gateway serves stale routes until the controller restarts.
- **Change.** Call the existing `syncDeployments` in the reconnect branch of `client.go`, as first connect already does, and on a timer with random jitter so gateways do not hit the control plane together.
- **Benefit.** Drift is fixed within one sync interval, however the event was lost. It also gives the AMS redesign a regular "gateway synced" signal, so rows can be confirmed without waiting for a restart.

### 2. Backfill `performed_at` with the migration time

- **Problem.** Migration 049 fills `performed_at` from `updated_at`. The controller refuses to overwrite an artifact with an older timestamp (`storage/sql_store.go:733`). Where the backfill is older than what the gateway stored, the sync re-downloads the artifact, is told it is stale, and repeats on every restart.
- **Change.** Use the migration time, truncated to milliseconds, for every backfilled row.
- **Benefit.** Each gateway re-downloads its artifacts exactly once after the upgrade, as the spec already promises.

### 3. AMS's replay in the shared hub

- **Problem.** A gateway that reconnects to a different platform-api replica, or after a platform-api restart, gets only the last 120 s of events. Behind a load balancer or after a rolling deploy, that is the normal case.
- **Change.** On a replica's first contact with a gateway, replay every event still retained (up to 1 hour), as AMS's fork does. Its code also tries to keep only the newest event per artifact, but that branch runs only in a narrow race window, so in practice the replay is verbatim; keep it that way until change 1 lands, because replay is AMS's only catch-up path today.
- **Benefit.** A one-hour recovery window wherever the gateway lands. Replay restores missed changes quickly; the sync in change 1 catches what replay cannot.

### 4. One or several connections per gateway

- **Problem.** The controller supports several copies under one gateway identity for redundancy. AMS keeps one connection per gateway, so the copies would keep evicting each other and only one would receive events at a time.
- **Change.** Adopt platform-api's model (one hub subscription per gateway per replica, sent to every connection), or document that redundant controllers are unsupported with AMS.
- **Benefit.** Redundant gateways either work, or the limit is known before production finds it.

### 5. Share the code instead of keeping forks

- **Problem.** AMS has the better replay; platform-api has the sync endpoints and the status model. Every fix has to be found and applied twice.
- **Change.** Make AMS import `common/eventhub` instead of its fork, then do the same for the WebSocket manager and the internal endpoint contract, which the controller already treats as one contract.
- **Benefit.** One implementation, fixed once, and the two products cannot silently diverge again.

## Priority

1. **Change 1** fixes "out of sync until restart" for both products.
2. **Change 2** is a one-line edit to the A2A sync spec, best made before the plan is implemented.
3. **Changes 3 and 4** are smaller correctness fixes, one in each repo.
4. **Change 5** stops this kind of drift from recurring.
