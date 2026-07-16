# Agent Manager Observer — Design Spec

**Date:** 2026-07-16
**Status:** Approved; revised after two adversarial reviews (2026-07-16)
**Base branch:** `upstream/main` (wso2/agent-manager). All file/line citations refer to it; the
`TRACE_OBSERVER_PUBLIC_URL` config work and the six-tool MCP observability toolset exist there.
**Scope:** Three PRs that consolidate runtime observability (logs, metrics, traces) into the
traces-observer-service, rename it to **agent-manager-observer**, and secure it.

## Background

Today observability retrieval is split across two services that both front the same upstream
**OpenChoreo Observer** (the OpenSearch/Prometheus-backed store):

- **agent-manager-service** serves build logs, runtime logs, and metrics over REST
  (`api/agent_routes.go:38,47,48` → services layer → `clients/observabilitysvc`, env
  `OBSERVER_URL`), plus **monitor-run logs**
  (`api/monitor_routes.go` → `monitor_manager.go:1025` → `GetWorkflowRunLogs`). Its MCP server
  (am-mcp) exposes six observability tools plus `get_build_logs` in the builds toolset.
- **traces-observer-service** serves traces only (`GET /api/v1/traces...`, port 9098). The
  console and CLI already call it directly — the traces migration removed agent-manager's trace
  routes entirely, which is the precedent this design follows for logs and metrics.

Discovery already exists: unauthenticated `GET /api/v1/config` on agent-manager returns
`traceObserverBaseUrl` (from `TRACE_OBSERVER_PUBLIC_URL`), and the CLI consumes it. The console
does not use it yet (static `OBS_API_BASE_URL` in `config.js`), and the config loader silently
falls back to the **internal** cluster URL when the public one is unset
(`config/config_loader.go:143`) — an internal-URL leak.

Namespace scoping today: both trace queries (observer, `controllers/controller.go:102` →
`observerClient.NamespaceFor(organization)`) and log/metric queries (agent-manager,
`agent_manager.go:4385` → `ocClient.NamespaceFor(ouID)`) resolve the upstream namespace through
a `NamespaceFor` mapping that currently returns a deployment-wide default namespace.

Known authZ gap: the observer validates JWTs but never reads claims; any valid token can query
any org by editing the `organization` query param. Additionally, any token whose audience matches
`amp-publisher-*` is accepted outright (`middleware/auth.go:38`) — this is how the evaluation
job's client-credentials token gets in.

## Goals

1. One service — **agent-manager-observer** — owns retrieval of logs, metrics, and traces.
2. Console and CLI discover it exclusively via `GET /api/v1/config`, which only ever reveals the
   public URL.
3. An observability MCP server (am-obs-mcp) runs inside the observer.
4. Org-scoped, scope-checked authorization on the observer REST API and on both MCP servers.

## Non-goals / future work

- No changes to trace/log/metric **ingestion** (stays in the upstream OpenChoreo Observer).
- No per-org namespace mapping beyond today's deployment-wide default (`NamespaceFor` stays a
  constant; a future ou→namespace mapping plugs in behind the same call).
- No resource-ownership validation in the observer (it does not verify that a project/agent
  belongs to the caller's org — stateless authZ only; parity with today's trace behavior).
- No new RBAC scope vocabulary (reuse `agent:read`; the existing `observability:*` scopes are
  dashboard-specific and untouched).
- **Publisher-token org-pinning is future work.** The evaluation job keeps calling the observer
  directly with its `amp-publisher-*` client-credentials token, and PR 3 preserves the existing
  audience carve-out for trace routes. Deriving/enforcing an org for publisher tokens (or moving
  the eval job to org-claimed tokens) is deliberately out of scope — see Risks.
- No backward compatibility for pre-rename deployments, Helm values, env vars, or CLIs — this is
  a full clean rename, accepted as breaking.

## Naming (applies across all PRs, executed in PR 1)

| Today | Becomes |
|---|---|
| `traces-observer-service/` directory + Go module | `agent-manager-observer/` |
| Binary `traces-observer` | `agent-manager-observer` |
| Image `ghcr.io/wso2/amp-traces-observer` | `ghcr.io/wso2/amp-observer` |
| Deployment/Service `amp-traces-observer` | `amp-observer` (chart `wso2-amp-observability-extension` keeps its name) |
| `TRACE_OBSERVER_URL` / `TRACE_OBSERVER_PUBLIC_URL` (agent-manager env) | `AM_OBSERVER_URL` / `AM_OBSERVER_PUBLIC_URL` (`OBSERVER_URL` is taken by the OpenChoreo Observer) |
| `TRACES_OBSERVER_PORT` (observer env) | `AM_OBSERVER_PORT` |
| `OBSERVER_BASE_URL` (observer → upstream env) | `OPENCHOREO_OBSERVER_URL` |
| `traceObserverBaseUrl` (`/api/v1/config` field) | `observerBaseUrl` |
| `OBS_API_BASE_URL` (console) | deleted — replaced by `/api/v1/config` discovery |
| `clients/traceobserversvc` (agent-manager) | renamed `clients/observersvc`; trace methods deleted in PR 2; **retained** for workflow-run log fetches (monitor-run logs) |

**CI / tooling renames (PR 1, previously missed):**

| Today | Becomes |
|---|---|
| `.github/release-config.json` entry `amp-traces-observer` / dir `traces-observer-service` (publishes the image) | `amp-observer` / `agent-manager-observer` |
| `.github/workflows/traces-observer-service-pr-checks.yaml` (filename, path filters, working dirs) | renamed + repointed |
| `kubectl logs deployment/amp-traces-observer` in `nightly.yml`, `e2e.yaml`, `release.yml` | `deployment/amp-observer` |
| `.github/workflows/instrumentation-matrix-pr.yaml` path filter; `.github/actions/amp-dev-stack/action.yaml` | repointed |
| Root `Makefile` `-C traces-observer-service gen-instrumentation-contract`; `scripts/check-contract-drift.sh` | repointed |
| `test/e2e/framework/config.go` `TRACES_OBSERVER_BASE_URL`; `test/instrumentation-matrix/heavy/driver.py`, `RUNBOOK.md` | `AM_OBSERVER_BASE_URL` + repointed |

## PR 1 — Move logs/metrics, rename, rewire console + CLI

### Observer: new endpoints

Same GET + query-param style as the existing traces API. Responses reuse agent-manager's exact
wire shapes — `models.LogsResponse` (`models/infra_resources.go:58-77`) and the JSON-identical
metrics shape currently serialized as `spec.MetricsResponse` (via
`utils.ConvertToMetricsResponse`) — so console changes stay small.

- `GET /api/v1/logs?organization&project&agent&environment&startTime&endTime&searchPhrase&logLevels&limit&sortOrder`
  → `LogsResponse` (runtime/component logs; `logLevels` comma-separated; `limit` and `sortOrder`
  defaults/bounds ported from agent-manager's `utils.ValidateLogFilterRequest`)
- `GET /api/v1/build-logs?organization&buildName` → `LogsResponse` (workflow-scoped; keeps
  today's 30-day window and limit-1000 defaults). `buildName` is an Argo workflow-run name —
  agent build runs and monitor evaluation runs both resolve here.
- `GET /api/v1/metrics?organization&project&agent&environment&startTime&endTime`
  → `MetricsResponse` (cpu/memory usage, requests, limits time series)

Internals:

- New `QueryLogs` / `QueryMetrics` methods on the observer's hand-written upstream client
  (`observer/client.go` style), targeting the upstream Observer's `POST /api/v1/logs/query` and
  `POST /api/v1/metrics/query` with `ComponentSearchScope` / `WorkflowSearchScope`, ported from
  `agent-manager-service/clients/observabilitysvc/client.go` (including the JSON-log `msg`
  extraction and time-series conversion helpers). Verified: the UUIDs agent-manager resolves
  today are never sent upstream — scopes carry names only, so query params suffice.
- Namespace scoping reuses the observer's **existing** `NamespaceFor` (`observer/client.go`),
  exactly as trace queries do. No new namespace config.
- No OpenChoreo existence-validation (org/project/build) in the observer — it trusts scoping
  params. **Accepted behavior changes:** unknown org/project/agent/env returns an empty 200
  instead of agent-manager's 404, and the server-side "runtime logs are not supported for agent
  type" check disappears (the CLI keeps its client-side `ValidateRuntimeManaged` guard; the
  console loses the error).
- **Publisher-audience restriction:** tokens matching the `amp-publisher-*` audience carve-out
  are accepted on trace routes only (the evaluation job's path) and rejected (403) on the new
  `/api/v1/logs`, `/api/v1/build-logs`, and `/api/v1/metrics` routes. This prevents widely
  distributed agent-workload credentials from gaining log/metric access they never had.

### Agent-manager: removals, monitor-run logs, config hardening

- Delete the three agent-scoped REST routes (`build-logs`, `runtime-logs`, `metrics`), their
  controller and service methods, `clients/observabilitysvc` (generated client + Makefile
  `gen-observer-client` target), the OpenAPI paths, and regenerate the spec + CLI client.
- **Monitor-run logs stay** (`GET .../monitors/{monitorName}/runs/{runId}/logs`): agent-manager
  resolves the workflow-run name from its monitors DB as today, then fetches server-to-server
  from the observer's `/api/v1/build-logs` endpoint via the renamed `clients/observersvc`
  (which gains a `GetWorkflowRunLogs`-equivalent method in this PR). The internal
  `AM_OBSERVER_URL` remains in use for this path. Implementation check: agent-manager's
  service token (via `occlient.AuthProvider`) must pass the observer's audience allowlist and
  must NOT match the `amp-publisher-*` restriction on `/api/v1/build-logs`, or monitor-run logs
  break — verify and, if needed, add its audience to the observer chart's allowlist.
- Delete **three** MCP tools that depended on the removed service methods: `get_runtime_logs`
  and `get_metrics` (observability toolset, `mcp/tools/observability.go`) and `get_build_logs`
  (builds toolset, `mcp/tools/builds.go` + `mcp/handlers/build_handler.go` + the interface entry
  in `mcp/tools/types.go` + specs tests). All three return in PR 2 inside am-obs-mcp. The four
  remaining trace tools keep working via the observer client.
- `/api/v1/config`: rename the field to `observerBaseUrl`. `AM_OBSERVER_PUBLIC_URL` has **no
  internal-URL fallback**: empty by default, `http://localhost:9098` only when
  `IS_LOCAL_DEV_ENV=true`. An empty value yields an empty field, which the CLI already rejects
  with a clear `ServerInvalid` error and the console surfaces as "observer not configured".
- **CORS fix:** the config route is registered on the root mux and bypasses the CORS middleware
  that wraps only the `/api/v1/` subtree (`api/app.go`), so browsers cannot read it cross-origin
  today. PR 1 adds CORS handling to this route (move it under the CORS-wrapped tree or apply the
  middleware directly).

### Console

- Bootstrap: fetch `GET /api/v1/config` (unauthenticated, now CORS-enabled) at app init; store
  `observerBaseUrl` in global config; `httpGETObserver` consumes it. Remove `OBS_API_BASE_URL`
  from `config.js`, `config.template.js`, and the console Helm configmap.
- Rewrite the logs, metrics, and build-logs API + hook files
  (`libs/api-client/src/{apis,hooks}/{runtime-logs,metrics,builds}.ts`): these are POST-with-body
  calls today and become GET + query-param calls against the observer — a request-shape rewrite
  following the existing traces API files as the template, not a URL swap. Response types are
  unchanged. Traces gain discovery for free (their static-config dependency disappears).

### CLI

- Rewire `amctl agent logs`, `amctl agent metrics`, `amctl agent build logs` to call the observer
  directly, using the same `/api/v1/config` discovery + bearer-token pattern as
  `amctl agent traces`. Generated amsvc types pick up the `observerBaseUrl` rename.

### Deployments, CI, e2e

- Helm: agent-manager-service configmap (`AM_OBSERVER_URL`, `AM_OBSERVER_PUBLIC_URL`), console
  configmap (drop `OBS_API_BASE_URL`), observability-extension chart (deployment/service/image
  rename, `OPENCHOREO_OBSERVER_URL`, `AM_OBSERVER_PORT`).
- **Audience alignment:** the observer chart's `KEY_MANAGER_AUDIENCE` default gains `amctl` (and
  `am-mcp`/am-obs-mcp's audience in PR 2) to match the agent-manager chart
  (`wso2-agent-manager/values.yaml:149` vs `wso2-amp-observability-extension/values.yaml:32`) —
  without this, CLI tokens 401 at the observer in a default install.
- docker-compose, port-forward and setup scripts, `.env.example` files, evaluation-extension's
  `tracesApiEndpoint`, and AGENTS.md/docs references follow the naming table.
- **E2E migration:** repoint `test/e2e/operations/agent/{metrics,runtime_logs}.go` and
  `test/e2e/operations/build/build_operations.go` at the observer's new endpoints; rename
  `TRACES_OBSERVER_BASE_URL` in `test/e2e/framework/config.go`; update the instrumentation-matrix
  suite. All `.github` workflow/tooling renames per the CI table above — PR 1 is not mergeable
  until its own CI passes on the renamed paths.

## PR 2 — Observability MCP (am-obs-mcp)

- New `mcp` package inside agent-manager-observer using `modelcontextprotocol/go-sdk` (same
  streamable-HTTP pattern as am-mcp: `mcp/server.go` + `RegisterRoute` on `/mcp` and `/mcp/`),
  mounted behind the observer's JWT middleware.
- **MCP OAuth plumbing** (required — am-mcp has it, the observer doesn't): a `WWW-Authenticate`
  bearer challenge carrying `resource_metadata` on 401s (port of
  `jwtassertion.buildBearerChallenge`), plus an RFC 9728
  `/.well-known/oauth-protected-resource` route (port of `api/well_known_routes.go`), with the
  observer's public URL + authorization-server config to drive them. Without this, spec-compliant
  MCP clients cannot discover how to authenticate.
- Seven tools calling the observer's own controllers directly (no HTTP hop): `get_runtime_logs`,
  `get_build_logs`, `get_metrics`, `list_traces`, `get_traces`, `get_trace_details`,
  `get_span_details`. Tool inputs mirror the REST query params, including `organization` as an
  explicit parameter in this PR (PR 3 replaces it with token claims).
- am-mcp deletes its observability toolset (`mcp/tools/observability.go` — the four remaining
  trace tools) and the observability handler. `clients/observersvc` **survives** with only the
  workflow-run logs method used by monitor-run logs; its trace methods are deleted here. The
  internal `AM_OBSERVER_URL` stays (monitor-run logs is its remaining consumer).
- Docs: how to point an MCP client (or an agent) at the obs MCP endpoint.

## PR 3 — AuthZ (am-observer REST + am-obs-mcp + am-mcp)

Stateless enforcement mirroring agent-manager's model — permission = scope claim in the JWT
(`jwtassertion.HasAllScopes`), org = `ouId`/`ouHandle` claims. No DB, no new dependencies, no
resource-ownership validation.

- **Observer middleware:** extend `JWTAuth` to parse org + scope claims into the request context
  via a small claims package inside the observer (no cross-module import from
  agent-manager-service). Port agent-manager's wildcard audience matching
  (`jwtassertion/auth.go`) while at it — the observer's matcher is exact-only today.
- **Observer REST:** the token's org claim (`ouHandle`) is the source of truth. The
  client-supplied `organization` query param is **removed from console/CLI requests and ignored
  by the observer** — the observer derives the org from the token and resolves the upstream
  namespace via its existing `NamespaceFor(org)`. (Note: the console's current param value is
  `orgData?.namespace` — a deployment-wide constant, not an org identifier — which is why
  param-vs-claim matching cannot work; derivation replaces it.) Per-route scope check requiring
  `agent:read`, parity with what the endpoints enforced before the move.
- **Publisher-audience tokens** (evaluation job): the existing `amp-publisher-*` carve-out is
  preserved on trace routes, bypassing the org-claim requirement — these tokens carry no org
  claims and the eval job must keep working. They remain rejected on logs/metrics/build-logs
  routes (from PR 1). Org-pinning for this token class is explicitly future work.
- **am-obs-mcp:** tools derive org from claims; the explicit `organization` param from PR 2 is
  removed. Per-tool scope checks (`agent:read`).
- **am-mcp:** per-tool scope checks added across its toolsets, using each toolset's matching
  rbac permission; org-from-claims already exists (`resolveOUID`).
- Local dev: an `RBAC_ENABLED`-equivalent toggle and `IS_LOCAL_DEV_ENV` behavior preserved in
  both services so docker-compose flows keep working (docker-compose console tokens already
  carry the full scope list).

## Error handling

- Observer REST: existing `ErrorResponse` body + status conventions (400 for missing/invalid
  params, 401 from JWT middleware, 403 for publisher-token route restrictions in PR 1 and
  org/scope failures in PR 3, 502/504 for upstream Observer failures).
- Console: bootstrap-discovery failure (or empty `observerBaseUrl`) surfaces the existing
  "observer not configured/unreachable" state on observability pages rather than blocking the
  whole app.
- CLI: reuse the existing `clierr` taxonomy (`ServerInvalid` on empty `observerBaseUrl`).

## Testing

- **Observer:** unit tests per new handler/controller/client method and (PR 3) middleware —
  table-driven, `make fmt && make lint && make test` in the service dir.
- **Agent-manager:** update/remove tests for deleted routes, monitor-run logs over the observer
  client (moq mocks), and config (`api/config_handler_test.go` covers empty-default and
  no-internal-fallback behavior).
- **Console:** hook/API tests for the rewritten files; bootstrap discovery test.
- **CLI:** command tests for the rewired `agent logs`/`metrics`/`build logs` (existing `traces`
  tests as template).
- **E2E/CI:** the migrated e2e operations run in the PR's own CI; instrumentation-matrix and
  contract-drift jobs pass on renamed paths.
- **Per PR:** docker-compose smoke pass (console pages for logs/metrics/traces render; amctl
  commands succeed; MCP tools callable in PR 2; 403 paths verified in PR 1 — publisher tokens on
  log routes — and PR 3 — wrong-org/missing-scope).

## Risks / accepted breakage

- Full clean rename breaks existing deployments (Helm value names, image, deployment name) and
  released CLIs (`traceObserverBaseUrl` field rename). Accepted — coordinated repo-wide change,
  deployments recreated rather than upgraded in place.
- Between PR 1 and PR 2, am-mcp has no logs/metrics/build-logs tools (three deleted in PR 1,
  reborn in am-obs-mcp in PR 2). Accepted explicitly.
- Until PR 3 lands, the observer's new logs/metrics endpoints share the tenant-isolation gap
  traces have today for **user-class tokens** (valid token + arbitrary scoping params). The
  publisher-token class — the widest-distributed credentials — is blocked from the new routes
  from PR 1 onward, so the gap is not widened for them.
- **Persisting after PR 3:** publisher-audience tokens retain unpinned (all-org) access to trace
  routes — the eval job depends on it. Closing this (org-from-audience or org-claimed m2m
  tokens) is future work and should be tracked as a security backlog item.
- Behavior regressions accepted with the move: 404 → empty-200 for unknown scoping values;
  server-side agent-type check for runtime logs dropped.
- Existing helm installs that relied on the internal-URL fallback for `TRACE_OBSERVER_PUBLIC_URL`
  now serve an empty `observerBaseUrl` until they set the new value — a loud, detectable failure
  instead of a silently leaked internal URL. Accepted.
