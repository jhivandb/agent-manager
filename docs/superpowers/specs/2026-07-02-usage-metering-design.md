# Usage Metering System — Design

**Date:** 2026-07-02
**Status:** Approved design, pending implementation plan

## Purpose

Meter billable usage across the agent-manager platform and publish raw usage
events to an external billing system. Agent-manager is the *producer* of usage
data only: the billing system (not under our control, API not yet finalized)
applies its own mappings and aggregation to compute cost. We keep no long-term
usage storage — only a transient staging buffer.

## Meters

| Meter | Unit | Plane of origin | Reliability class |
|---|---|---|---|
| Agent resources | CPU/memory requests × minutes pod runs | Data plane | Best-effort (bounded gaps OK) |
| Build resources | CPU/memory requests × minutes pod runs | Workflow plane (Argo) | Best-effort |
| Eval resources | CPU/memory requests × minutes pod runs | Workflow plane (Argo) | Best-effort |
| Gateway LLM invocations | Count | Data plane (gateway) | At-least-once |
| Gateway MCP invocations | Count | Data plane (gateway) | At-least-once |
| Traces ingested | Count of distinct traces | Observability plane | At-least-once |
| IDP tokens created | Count | Data plane (per-environment Thunder) | At-least-once |

Resource metering is **allocation-based**: the pod spec's resource *requests*
(summed across containers) × wall-clock minutes the pod runs. No actual-usage
sampling and no metrics pipeline.

## Constraints and context

- **Topology:** a few shared data/build planes, all operated by us, fronted by
  one control plane (`agent-manager-service`, Go + Postgres). No
  customer-hosted planes to design for.
- **Downstream:** billing system consumes raw events; its API is not finalized,
  so the egress side must be pluggable. Attribution metadata
  (org/project/environment/component) must be baked into every event.
- **Existing building blocks:** agent, build, and eval pods all carry
  `openchoreo.dev/organization|project|component|environment` labels
  (`agent-manager-service/clients/openchoreosvc/client/constants.go`). Traces
  flow through the gateway `/otel` endpoint to the observability plane
  (traces-observer → OpenSearch). Thunder has no token-creation event today.
  No metering/usage code exists anywhere in the repo.

## Rejected alternatives

- **Prometheus/kube-state-metrics pipeline:** cheapest to build, but
  scrape gaps, counter resets, and restarts silently drop counted events —
  incompatible with at-least-once for transactions/tokens, and couples billing
  correctness to observability-stack health.
- **Dedicated event bus (NATS/Kafka):** cleanest semantics and most scale
  headroom, but new stateful infra across planes for modest event volume. A
  Postgres outbox delivers the same guarantee on infrastructure we already run.

**Chosen approach:** Kubernetes-native collectors per plane + counted events
emitted at their source, all funneled to a control-plane ingestion API backed
by a transient Postgres outbox and a pluggable billing publisher.

## Canonical event model

Two event shapes cover all seven meters. Both carry:

- `eventVersion` — schema version (starts at `1`).
- `eventId` — deterministic idempotency key: `{meter}:{sourceUID}:{windowStart}`.
  Retries and replays are always safe; the billing side can dedup blind.
- `attribution` — `org`, `project`, `environment`, `component` (agentId), plus
  `plane`, `cluster`, and `resourceKind` (`agent | build | eval`) where
  applicable. Lifted from pod labels or request context at the source.

All windows align to UTC wall-clock minutes.

### 1. Resource slice event

One event per labeled pod per minute it runs:

```json
{
  "eventVersion": "1",
  "eventId": "agent_resource:pod-uid-abc:2026-07-02T09:41:00Z",
  "meter": "resource_usage",
  "resourceKind": "agent",
  "windowStart": "2026-07-02T09:41:00Z",
  "windowSeconds": 60,
  "runningSeconds": 60,
  "cpuRequestMillicores": 500,
  "memoryRequestBytes": 1073741824,
  "attribution": { "org": "…", "project": "…", "environment": "…", "component": "…" }
}
```

- `runningSeconds < 60` on partial first/last minutes. Raw seconds are
  shipped; the billing system decides rounding. A 40-second eval job produces
  exactly one slice with `runningSeconds: 40`.
- Requests come from the pod spec, summed across containers.

### 2. Counted event

Per-minute aggregated counts emitted at the source:

```json
{
  "eventVersion": "1",
  "eventId": "gateway_llm_invocation:gw-pod-xyz:2026-07-02T09:41:00Z",
  "meter": "gateway_llm_invocation",
  "windowStart": "2026-07-02T09:41:00Z",
  "count": 172,
  "attribution": { "org": "…", "project": "…", "environment": "…", "component": "…" }
}
```

`meter` ∈ `gateway_llm_invocation | gateway_mcp_invocation | trace_ingested |
idp_token_created`.

If the finalized billing API wants coarser records, roll-up (per-minute →
hourly per component) happens in the control-plane publisher as a config
option — never at the source. Sources always stay raw.

In the codebase the schema lives as versioned Go structs in a new `usage`
package in `agent-manager-service`, plus the OpenAPI definition for the
ingestion endpoint.

## Collectors — where each meter is captured

### Resource minutes: `amp-usage-collector` (data + workflow planes)

A small Go deployment, one per plane, same binary with per-plane config. It:

- Runs a shared pod informer filtered on the `openchoreo.dev/*` labels.
- Tracks open intervals in memory; every minute flushes closed slices as a
  batch to the control-plane ingestion API.
- Distinguishes builds vs evals by workflow-template labels
  (`amp-*-buildpack` / `amp-docker` vs `monitor-evaluation`); agents by the
  data-plane deployment labels.
- Attribution comes straight off pod labels.

**Crash recovery:** on restart the informer re-lists running pods and
backfills slices from `pod.status.startTime`. Only pods that started *and*
finished entirely within the collector outage lose slices — bounded loss,
acceptable for the best-effort class.

### Gateway transactions (LLM/MCP): metered in the gateway

A hook in the gateway request path increments in-memory counters keyed by
(minute window, meter, attribution). A flusher POSTs closed windows to the
ingestion API and retries **until acked**; window-keyed idempotency makes
retries safe. Exposure: a gateway crash loses at most the in-flight minute.
Hardening knob (deferred until crash-minute loss proves material): a small
write-ahead file persisting window counters before ack.

### Traces ingested: counted in the observability plane

Counted in the trace ingestion path (the observer that indexes spans into
OpenSearch) — the one place OTLP is already parsed. "Number of traces" means
distinct `traceId`s per minute window, tracked with a bounded per-minute
seen-set. Emits standard counted events.

**Implementation-time check:** spans must carry enough attributes to attribute
to org/project/environment/component. The trace contract
(`traces-observer-service/cmd/gen-contract/contract.go`) is where that is
enforced; extend it if attribution attributes are missing.

### IDP tokens: Thunder emission hook

Thunder has no token-creation event today. **Decision: pursue an upstream
Thunder hook** that publishes a token-issuance event (HTTP post to a
configured endpoint). Fallback documented in case upstream is slow: a
log-tailing sidecar counting successful `POST /oauth2/token` responses from
Thunder's access log. Both produce the same `idp_token_created` counted event,
so swapping the stopgap for the hook touches nothing downstream. Thunder runs
per-environment in the data plane; attribution is org/environment plus client
app identity.

## Control-plane ingestion: usage module, outbox, publisher

### Ingestion endpoint

A new `usage` module inside `agent-manager-service` (not a separate service —
it inherits auth, Postgres, migrations, and deployment; event volume at
few-shared-planes scale is modest):

```
POST /api/v1/internal/usage/events    (batch of events, per-plane credentials)
```

Handler behavior:

1. Validate schema version and attribution.
2. Insert the batch into the outbox in one transaction with
   `ON CONFLICT (event_id) DO NOTHING`.
3. Return 200 only after commit.

Collectors treat any non-200 as "retry the whole batch"; dedup on `event_id`
makes that safe. Commit-before-ack gives counted meters end-to-end
at-least-once.

### Outbox table (`usage_events` — transient staging, not storage)

```sql
event_id      text PRIMARY KEY,     -- deterministic idempotency key
meter         text        NOT NULL,
window_start  timestamptz NOT NULL,
quantity      numeric     NOT NULL, -- count, or running_seconds for slices
dims          jsonb       NOT NULL, -- attribution + meter-specific fields
received_at   timestamptz NOT NULL DEFAULT now(),
published_at  timestamptz NULL,
attempts      int         NOT NULL DEFAULT 0
```

### Publisher worker

A background loop in the same service:

- Drains unpublished rows in batches ordered by `received_at`.
- Pushes through a pluggable sink interface; stamps `published_at` on success.
- Sink implementations: `http` (billing system's endpoint, once finalized),
  `log`/`file` (dev and early integration); room for `kafka` later without
  touching anything upstream.
- Exponential backoff per batch. Billing downtime just grows the outbox.
- Optional roll-up (per-minute → hourly) lives here as publisher config.

### Retention

A cleanup job deletes **published** rows older than N days (default 7).
Unpublished rows are never deleted — a long billing outage is an alert, not
data loss.

## Failure handling

Three loss surfaces, each bounded and matched to per-meter tolerance:

| Failure | Effect | Class impact |
|---|---|---|
| Collector down | Running pods backfilled from re-list; only pods born-and-died within the outage lose slices | Best-effort — acceptable |
| Source → control-plane link down | Sources hold closed windows in memory and retry until acked; loss only if the source itself dies while disconnected | Strict class — gateway WAL is the documented hardening knob |
| Control-plane → billing link down | Outbox absorbs it | Zero loss |

## Self-observability

Silent under-billing looks identical to low usage, so the pipeline itself is
instrumented. Each component exposes Prometheus metrics:

- Events emitted/acked per meter (collectors, gateway, observer, Thunder hook)
- Flush/retry failures per source
- Outbox depth and oldest-unpublished age
- Publish error rate per sink

Alerts: outbox age exceeding threshold; per-meter silence (a plane emitting
zero resource slices while labeled pods exist is a bug, not idle).

## Testing

- **Unit:** slicing math (partial minutes, restart backfill, UTC window
  alignment), idempotency-key determinism, counter window closing.
- **Integration:** collector against envtest/kind with synthetic labeled pods;
  assert exact slice output. Ingestion endpoint dedup and commit-before-ack
  semantics against a real Postgres.
- **End-to-end:** soak in a dev plane comparing collector output against
  `kubectl` ground truth over a day.
- **Contract:** billing-sink contract tests once their API is finalized; `log`
  sink used until then.

## Sequencing

Each meter is independently shippable; they share only the ingestion API.

1. `usage` module in `agent-manager-service`: schema, ingestion endpoint,
   outbox migration, publisher with `log` sink, retention job.
2. `amp-usage-collector` for agents/builds/evals (data + workflow planes).
3. Gateway LLM/MCP counters.
4. Trace counting in the observability plane (incl. trace-contract check).
5. Thunder token hook (upstream change; log-tail sidecar as stopgap if needed).
6. `http` sink when the billing API is finalized.

## Open items

- Billing system API shape — blocks only the `http` sink (item 6).
- Span attribution attributes — verify/extend the trace contract during item 4.
- Thunder upstream hook — engage the Thunder team; stopgap available.
- Auth mechanism for `POST /internal/usage/events` (per-plane credentials;
  reuse whatever plane→control-plane auth exists for gateway config sync).
