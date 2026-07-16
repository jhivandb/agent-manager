# Agent Manager Observer — Design Spec

**Date:** 2026-07-16
**Status:** Approved (brainstorm with Jhivan, 2026-07-16)
**Scope:** Three PRs that consolidate runtime observability (logs, metrics, traces) into the
traces-observer-service, rename it to **agent-manager-observer**, and secure it.

## Background

Today observability retrieval is split across two services that both front the same upstream
**OpenChoreo Observer** (the OpenSearch/Prometheus-backed store):

- **agent-manager-service** serves build logs, runtime logs, and metrics over REST
  (`api/agent_routes.go:38,47,48` → `services/agent_manager.go:4282-4423` →
  `clients/observabilitysvc`, env `OBSERVER_URL`). Its MCP server (am-mcp) also exposes six
  observability tools.
- **traces-observer-service** serves traces only (`GET /api/v1/traces...`, port 9098). The
  console and CLI already call it directly — the traces migration removed agent-manager's trace
  routes entirely, which is the precedent this design follows for logs and metrics.

Discovery already exists: unauthenticated `GET /api/v1/config` on agent-manager returns
`traceObserverBaseUrl` (from `TRACE_OBSERVER_PUBLIC_URL`), and the CLI consumes it. The console
does not use it yet (static `OBS_API_BASE_URL` in `config.js`), and the config loader silently
falls back to the **internal** cluster URL when the public one is unset
(`config/config_loader.go:141`) — an internal-URL leak.

Known authZ gap (documented in traces-observer's `AGENTS.md`): the observer validates JWTs but
never reads claims; any valid token can query any org by editing the `organization` query param.

## Goals

1. One service — **agent-manager-observer** — owns retrieval of logs, metrics, and traces.
2. Console and CLI discover it exclusively via `GET /api/v1/config`, which only ever reveals the
   public URL.
3. An observability MCP server (am-obs-mcp) runs inside the observer.
4. Org-scoped, scope-checked authorization on the observer REST API and on both MCP servers.

## Non-goals

- No changes to trace/log/metric **ingestion** (stays in the upstream OpenChoreo Observer).
- No per-org namespace mapping beyond today's deployment-wide default (`NamespaceFor` is a
  constant; the observer replicates that, and a future ou→namespace mapping plugs in later).
- No new RBAC scope vocabulary (reuse `agent:read`; finer `observability:*` read scopes can be
  added later without redesign).
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
| `clients/traceobserversvc` (agent-manager) | untouched in PR 1 (only its env vars rename), deleted in PR 2 |

## PR 1 — Move logs/metrics, rename, rewire console + CLI

### Observer: new endpoints

Same GET + query-param style as the existing traces API. Responses reuse agent-manager's exact
JSON shapes (`LogsResponse`, `MetricsResponse` from `models/infra_resources.go:58-90`) so console
changes are minimal.

- `GET /api/v1/logs?organization&project&agent&environment&startTime&endTime&searchPhrase&logLevels&limit&sortOrder`
  → `LogsResponse` (runtime/component logs; `logLevels` comma-separated; `limit` and `sortOrder`
  defaults/bounds ported from agent-manager's `utils.ValidateLogFilterRequest`)
- `GET /api/v1/build-logs?organization&buildName` → `LogsResponse` (workflow-scoped; keeps
  today's 30-day window and limit-1000 defaults)
- `GET /api/v1/metrics?organization&project&agent&environment&startTime&endTime`
  → `MetricsResponse` (cpu/memory usage, requests, limits time series)

Internals:

- New `QueryLogs` / `QueryMetrics` methods on the observer's hand-written upstream client
  (`observer/client.go` style), targeting the upstream Observer's logs/metrics query API with
  `ComponentSearchScope` / `WorkflowSearchScope`, ported from
  `agent-manager-service/clients/observabilitysvc/client.go` (including the JSON-log `msg`
  extraction and time-series conversion helpers).
- New config value for the workload namespace (deployment-wide constant, mirroring
  agent-manager's `NamespaceFor`), used for log/metric scopes. Trace queries keep using
  `organization` as the namespace scope, unchanged.
- No OpenChoreo existence-validation (org/project/build) in the observer — it trusts scoping
  params. PR 3 adds authZ on top.

### Agent-manager: removals and config hardening

- Delete the three REST routes, controller methods, service methods, `clients/observabilitysvc`
  (including generated client + Makefile `gen-observer-client` target), the OpenAPI paths, and
  regenerate the spec + CLI client.
- Delete the two MCP tools that depended on the removed service methods (`get_runtime_logs`,
  `get_metrics`) so the build stays green; the four trace MCP tools keep working via
  `clients/traceobserversvc`. The full toolset moves in PR 2.
- `/api/v1/config`: rename the field to `observerBaseUrl`; `AM_OBSERVER_PUBLIC_URL` defaults to
  `http://localhost:9098` and **never falls back to the internal URL** — the internal-URL leak in
  `config_loader.go:141` is removed.

### Console

- Bootstrap: fetch `GET /api/v1/config` (unauthenticated) at app init; store `observerBaseUrl`
  in global config; `httpGETObserver` consumes it. Remove `OBS_API_BASE_URL` from `config.js`,
  `config.template.js`, and the console Helm configmap.
- Repoint the logs, metrics, and build-logs API + hook files
  (`libs/api-client/src/{apis,hooks}/{runtime-logs,metrics,builds}.ts`) at the observer's new
  endpoints, following the existing traces API files as the template. Traces gain discovery for
  free (their static-config dependency disappears).

### CLI

- Rewire `amctl agent logs`, `amctl agent metrics`, `amctl agent build logs` to call the observer
  directly, using the same `/api/v1/config` discovery + bearer-token pattern as
  `amctl agent traces`. Generated amsvc types pick up the `observerBaseUrl` rename.

### Deployments

- Helm: agent-manager-service configmap (`AM_OBSERVER_URL`, `AM_OBSERVER_PUBLIC_URL`), console
  configmap (drop `OBS_API_BASE_URL`), observability-extension chart (deployment/service/image
  rename, `OPENCHOREO_OBSERVER_URL`, `AM_OBSERVER_PORT`).
- docker-compose, port-forward and setup scripts, `.env.example` files, evaluation-extension's
  `tracesApiEndpoint` value, and AGENTS.md/docs references all follow the naming table.

## PR 2 — Observability MCP (am-obs-mcp)

- New `mcp` package inside agent-manager-observer using `modelcontextprotocol/go-sdk` (same
  streamable-HTTP pattern as am-mcp: `mcp/server.go` + `RegisterRoute` on `/mcp` and `/mcp/`),
  mounted behind the observer's existing JWT middleware.
- Seven tools calling the observer's own controllers directly (no HTTP hop):
  `get_runtime_logs`, `get_build_logs`, `get_metrics`, `list_traces`, `get_traces`,
  `get_trace_details`, `get_span_details`. Tool inputs mirror the REST query params, including
  `organization` as an explicit parameter in this PR (PR 3 replaces it with token claims).
- am-mcp deletes its observability toolset (`mcp/tools/observability.go`), the observability
  handler (`mcp/handlers/observability_handler.go`), and `clients/traceobserversvc` entirely.
  Agent-manager then keeps only `AM_OBSERVER_PUBLIC_URL` (for `/api/v1/config`); the internal
  `AM_OBSERVER_URL` and its config/wiring are removed with their last consumer.
- Docs: how to point an MCP client (or an agent) at the obs MCP endpoint.

## PR 3 — AuthZ (am-observer REST + am-obs-mcp + am-mcp)

Stateless enforcement mirroring agent-manager's model — permission = scope claim in the JWT
(`jwtassertion.HasAllScopes`), org = `ouId`/`ouHandle` claims. No DB, no new dependencies.

- **Observer middleware:** extend `JWTAuth` to parse org + scope claims into the request context
  via a small claims package inside the observer (no cross-module import from
  agent-manager-service).
- **Observer REST:** the token's org claim is the source of truth. The `organization` query param
  must match it (mismatch → 403). Per-route scope check requiring `agent:read` — parity with what
  the endpoints enforced before the move.
- **am-obs-mcp:** tools derive org from claims; the explicit `organization` param from PR 2 is
  dropped (or validated against claims if kept for display). Per-tool scope checks (`agent:read`).
- **am-mcp:** per-tool scope checks added across its toolsets, using each toolset's matching
  rbac permission; org-from-claims already exists (`resolveOUID`).
- Local dev: an `RBAC_ENABLED`-equivalent toggle and `IS_LOCAL_DEV_ENV` behavior preserved in
  both services so docker-compose flows keep working.

## Error handling

- Observer REST: existing `ErrorResponse` body + status conventions (400 for missing/invalid
  params, 401 from JWT middleware, 403 for org mismatch / missing scope after PR 3, 502/504 for
  upstream Observer failures) — matching current handler behavior.
- Console: `httpGETObserver` already surfaces a clear error when discovery fails; the bootstrap
  fetch failure shows the same "observer not configured/reachable" state on observability pages
  rather than blocking the whole app.
- CLI: reuse the existing `clierr` taxonomy (e.g. `ServerInvalid` on empty `observerBaseUrl`).

## Testing

- **Observer:** unit tests per new handler/controller/client method and (PR 3) middleware —
  table-driven, `make fmt && make lint && make test` in the service dir.
- **Agent-manager:** update/remove tests for deleted routes and config
  (`api/config_handler_test.go` covers the no-internal-fallback behavior); moq-based service
  tests per repo conventions.
- **Console:** hook/API tests for the repointed files; bootstrap discovery test.
- **CLI:** command tests for the rewired `agent logs`/`metrics`/`build logs` (existing
  `traces` tests as template).
- **Per PR:** docker-compose smoke pass (console pages for logs/metrics/traces render; amctl
  commands succeed; MCP tools callable in PR 2; 403 paths verified in PR 3).

## Risks / accepted breakage

- Full clean rename breaks existing deployments (Helm value names, image, deployment name) and
  released CLIs (`traceObserverBaseUrl` field rename). Accepted — coordinated repo-wide change,
  deployments recreated rather than upgraded in place.
- Between PR 1 and PR 2, am-mcp has no logs/metrics tools (deleted in PR 1, reborn in the obs
  MCP in PR 2). Accepted explicitly during brainstorming.
- Until PR 3 lands, the observer's new logs/metrics endpoints share the existing tenant-isolation
  gap that traces already have today (valid token + arbitrary `organization` param). PR 3 is the
  fix; the gap is not widened in kind, only in surface, and the PRs land as one effort.
