# am-obs-mcp: Observability MCP Server Design

**Date:** 2026-07-15
**Status:** Approved design, pending implementation plan

## Summary

Move the six observability MCP tools (logs, metrics, traces) out of the MCP
server embedded in `agent-manager-service` and into a new MCP server —
**am-obs-mcp** — embedded in `traces-observer-service`. Observability MCP
traffic then routes through trace-observer instead of the am service.

Because trace-observer today only serves traces, it also gains logs and
metrics REST endpoints, backed by the same upstream Observer APIs the am
service uses today.

## Background

- The existing MCP server is a Go package at `agent-manager-service/mcp/`,
  mounted at `/mcp` on the am service (port 9000), built with the official
  `modelcontextprotocol/go-sdk` over Streamable HTTP.
- Of its 19 tools, 6 are observability tools (`mcp/tools/observability.go`):
  `get_runtime_logs`, `get_metrics`, `list_traces`, `get_traces`,
  `get_trace_details`, `get_span_details`.
- The 4 trace tools already call trace-observer via
  `clients/traceobserversvc`. Logs and metrics instead go through
  `AgentManagerService` → `clients/observabilitysvc` → the upstream
  Observer service (`OBSERVER_URL`).
- `traces-observer-service` is an adapter over that same upstream Observer:
  it exposes `/api/v1/traces...` endpoints, enriches spans, and
  authenticates callers with the same `KEY_MANAGER_*` JWT config as the am
  service. It has no logs or metrics endpoints.

Verified enabler: the upstream Observer scopes logs/metrics queries by
**names only** — `ComponentSearchScope{Namespace, Project, Component,
Environment}` (`clients/observabilitysvc/client.go:236-293`). The OpenChoreo
UUID lookups the am service performs are validation, not query inputs. So
trace-observer can serve logs/metrics without an OpenChoreo client.

## Decisions

1. **Extend trace-observer with logs/metrics endpoints** rather than having
   the MCP server call the upstream Observer directly. am-obs-mcp has
   exactly one backend, and console/CLI can later reuse the same endpoints.
2. **Embed the MCP server in traces-observer-service** (mounted at `/mcp`),
   mirroring how am-mcp is embedded in agent-manager-service. No new
   deployable, reuses existing JWT middleware, config, Dockerfile, Helm
   chart, and CI. Tool handlers call controllers in-process.
3. **Remove the observability toolset from am-mcp in the same change.**
   Clean cut; no duplicate tools across servers.

## Design

### 1. New REST endpoints on trace-observer

| Method & path | Purpose |
| --- | --- |
| `GET /api/v1/logs` | Runtime (component) logs |
| `GET /api/v1/metrics` | Resource metrics for a component |

Query params follow the existing trace-endpoint conventions:
`project`, `component`, `environment`, `startTime`, `endTime` (RFC3339,
required), `limit`, `sortOrder` — plus, for logs only: `searchPhrase` and
`logLevels` (comma-separated). Namespace resolution reuses
`observer.NamespaceFor` (`OBSERVER_DEFAULT_NAMESPACE`), consistent with the
single-namespace assumption in the am service's `ResolveNamespace`.

Implementation stack (same layering as traces):

- `observer/` client: new methods calling the upstream Observer's
  `POST /api/v1/logs/query` and `POST /api/v1/metrics/query` with a
  name-based `ComponentSearchScope` — the same request bodies
  `clients/observabilitysvc.GetComponentLogs`/`GetComponentMetrics` build
  today. Reuses the existing client-credentials token cache
  (`observer/auth.go`).
- `controllers/`: `GetLogs`, `GetMetrics` orchestration.
- `handlers/`: param parsing/validation and response writing.
- `docs/openapi.yaml`: new paths and schemas (log entry, metrics response —
  mirroring the shapes returned by the am service's REST endpoints today).
- Routes registered in `main.go` under the existing
  RequestLogger → CORS → JWTAuth middleware chain.

### 2. Embedded MCP server (`traces-observer-service/mcp/`)

New package copying the am-mcp scaffolding:

- `mcp/server.go` — `gomcp.NewServer` + `NewStreamableHTTPHandler`;
  implementation name `agent-manager-observability`, version `0.1.0`.
- `mcp/setup.go` — deps struct (the `controllers.Controller`), route
  registration; mounted at `/mcp` and `/mcp/` behind the existing JWT
  middleware.
- `mcp/tools/` — tool definitions and schema helpers (ported from
  `agent-manager-service/mcp/tools/helpers.go` minus the OU-claim helpers,
  which don't apply — scoping is by namespace).
- `mcp/handlers/` — handlers calling controllers **in-process** (no HTTP
  self-call).

Adds `github.com/modelcontextprotocol/go-sdk` to trace-observer's `go.mod`.

**Tools (identical names and input schemas to today's am-mcp versions):**

| Tool | Backing call |
| --- | --- |
| `get_runtime_logs` | controller `GetLogs` (new) |
| `get_metrics` | controller `GetMetrics` (new) |
| `list_traces` | controller trace-overview path (existing) |
| `get_traces` | controller `ExportTraces` (existing) |
| `get_trace_details` | controller trace-spans path (existing) |
| `get_span_details` | controller span-detail path (existing) |

The trace tools' client-side response reshaping/filtering
(`extractTraceOverviews`, `extractTracesWithSpans`, `matchesCondition`,
etc. in `mcp/tools/observability.go`) moves wholesale.

### 3. MCP client auth / OAuth discovery

- `/mcp` requests are validated by trace-observer's existing JWT middleware
  (same `KEY_MANAGER_JWKS_URL` / `ISSUER` / `AUDIENCE` as the am service,
  so tokens interoperate; `IS_LOCAL_DEV_ENV` for local dev).
- New endpoint `GET /.well-known/oauth-protected-resource` (ported from
  `agent-manager-service/api/well_known_routes.go`) so MCP clients can
  discover the authorization server. Two new env vars:
  `SERVER_PUBLIC_URL` and `OAUTH_AUTHORIZATION_SERVERS`
  (comma-separated), validated at startup only as a pair (the endpoint
  returns 503 when unconfigured, matching am behavior).
- MCP clients reuse the existing `am-mcp` Thunder OAuth client (authorization
  code + PKCE, callback port 33418). No new IdP registration: the resource
  server only validates issuer/audience.

Example client registration:

```bash
claude mcp add --transport http am-obs http://localhost:9098/mcp \
  --client-id am-mcp \
  --callback-port 33418
```

### 4. Removal from agent-manager-service

- Delete `mcp/tools/observability.go`, `mcp/handlers/observability_handler.go`,
  the `ObservabilityToolsetHandler` interface and its `Toolsets` field, and
  the observability registration in `mcp/tools/register.go`.
- Drop `TraceObserverSvcClient` from `mcp/setup.go` deps and from
  `api/app.go`'s MCP wiring. Remove it from `wiring/wire.go` only if
  nothing else consumes it.
- The am REST endpoints `POST .../runtime-logs` and `POST .../metrics`
  **stay** — the console uses them. Only the MCP tools move.
- Update `agent-manager-service/mcp/README.md` (remove observability
  section, link to the new server) and add an MCP section to
  `traces-observer-service` docs (`AGENTS.md`, `README.MD`).

### 5. Config summary (new env vars, trace-observer)

| Var | Purpose | Default |
| --- | --- | --- |
| `SERVER_PUBLIC_URL` | Resource identifier in protected-resource metadata | empty (endpoint 503s) |
| `OAUTH_AUTHORIZATION_SERVERS` | Authorization servers for MCP OAuth discovery | empty (endpoint 503s) |

No other config changes: the upstream Observer connection
(`OBSERVER_BASE_URL`, `IDP_*`) and JWT validation (`KEY_MANAGER_*`) already
exist. Helm values (`wso2-amp-observability-extension`) and
`deployments/docker-compose.yml` gain the two new vars.

## Trade-offs accepted

- **No OpenChoreo validation on logs/metrics.** The am service validated
  org/project/agent/environment existence and rejected runtime logs for
  external agents with a friendly error. The new path returns empty results
  for nonexistent scopes instead. Mitigation: tool descriptions state that
  empty results may mean a wrong project/agent/environment name or an
  external (non-platform-built) agent.
- **Trace-observer's scope grows beyond traces.** The service name stays
  (renaming is out of scope); its docs will describe it as the
  observability query service.
- **Namespace is the configured default**, not per-org. This matches
  current behavior (`ResolveNamespace` returns the single org's namespace;
  `NamespaceFor` returns the configured default) but bakes the
  single-namespace assumption into one more consumer.

## Testing

- Unit tests for the new observer client methods, controller methods, and
  handlers (param validation, error mapping) in trace-observer's existing
  stdlib test style.
- Unit tests for the MCP tool handlers (table-driven, fake controller).
- am-service: remove observability MCP tests; ensure `Toolsets` wiring
  tests still pass.
- Manual verification: register the server in Claude Code against a local
  `make dev-up` environment and exercise all six tools.

## Out of scope

- Migrating the console/CLI logs and metrics calls off the am service's
  REST endpoints (they can adopt trace-observer's new endpoints later).
- Renaming traces-observer-service.
- Per-organization namespace resolution.
- Logs/metrics endpoints beyond component scope (e.g. build logs stay in
  the am service).
