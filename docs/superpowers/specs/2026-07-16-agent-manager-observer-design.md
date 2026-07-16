# Agent Manager Observer — Design Spec

**Date:** 2026-07-16
**Status:** Approved; revised after three adversarial reviews and re-scoped to two PRs (2026-07-16).
Authorization (formerly PR 3) is deferred — see "Deferred: authorization".
**Base:** `upstream/main` @ `18851a8b` (wso2/agent-manager). All file/line citations refer to this
SHA — earlier revisions cited lines that straddled two snapshots; every citation below was
re-verified at the pin.
**Scope:** Two PRs that consolidate runtime observability (logs, metrics, traces) into the
traces-observer-service and rename it to **agent-manager-observer**. Org/scope authorization on
the observer is explicitly deferred, with its unresolved design problems documented at the end.

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
`agent_manager.go:4639,4689,4736` → `ocClient.NamespaceFor(ouID)`) resolve the upstream
namespace through a `NamespaceFor` mapping that currently returns a deployment-wide default
namespace regardless of its argument.

Known authZ gap: the observer validates JWTs (signature, issuer, audience) but never reads
claims; any valid token can query any org by editing the `organization` query param. Any token
whose audience matches `amp-publisher-*` passes the audience allowlist via a regex carve-out
(`middleware/auth.go:38`, enforced in `validateAudience` after full signature/issuer
validation) — this is how the evaluation job's client-credentials token gets in.

## Goals

1. One service — **agent-manager-observer** — owns retrieval of logs, metrics, and traces.
2. Console and CLI discover it exclusively via `GET /api/v1/config`, which only ever reveals the
   public URL.
3. An observability MCP server (am-obs-mcp) runs inside the observer.

(Formerly goal 4 — org-scoped, scope-checked authorization — is deferred; see the deferred
section for why it cannot ship as previously designed.)

## Non-goals / future work

- **No org/scope authorization on the observer.** The observer keeps its current authN-only
  posture (JWT signature/issuer/audience). The regression this implies for logs/metrics is
  called out and accepted in Risks. Design problems that any future auth work must solve are
  documented in "Deferred: authorization".
- No changes to trace/log/metric **ingestion** (stays in the upstream OpenChoreo Observer).
- No per-org namespace mapping beyond today's deployment-wide default (`NamespaceFor` stays a
  constant; a future ou→namespace mapping plugs in behind the same call).
- No resource-ownership validation in the observer (it does not verify that a project/agent
  belongs to the caller's org — parity with today's trace behavior).
- **Publisher-token org-pinning is future work.** The evaluation job keeps calling the observer
  directly with its `amp-publisher-*` client-credentials token.
- No backward compatibility for pre-rename deployments, Helm values, env vars, or CLIs — this is
  a full clean rename, accepted as breaking. **Exception:** `documentation/versioned_docs/`
  (released versions v0.11–v0.18) is excluded from the rename — those docs describe what their
  versions actually shipped. Only current `documentation/docs/` is updated.

## Naming (applies across all PRs, executed in PR 1)

| Today | Becomes |
|---|---|
| `traces-observer-service/` directory + Go module | `agent-manager-observer/` |
| Binary `traces-observer` | `agent-manager-observer` |
| Image `ghcr.io/wso2/amp-traces-observer` | `ghcr.io/wso2/amp-observer` |
| Deployment/Service `amp-traces-observer` | `amp-observer` (chart `wso2-amp-observability-extension` keeps its name) |
| Pod label `app.kubernetes.io/component=traces-observer` | `component=observer` (consumed by `deployments/quick-start/install-helpers.sh` selectors) |
| Extension chart values namespace `tracesObserver.*` (`values.yaml`, ~30 uses in templates, plus `--set tracesObserver...` in installers/docs) | `amObserver.*` |
| `TRACE_OBSERVER_URL` / `TRACE_OBSERVER_PUBLIC_URL` (agent-manager env) | `AM_OBSERVER_URL` / `AM_OBSERVER_PUBLIC_URL` |
| `OBSERVER_URL` + `ObserverConfig` (agent-manager, `config_loader.go:131-133`; chart `observerURL` `values.yaml:140`; compose/.env entries) | **deleted** — orphaned once `clients/observabilitysvc` (its only consumer) is removed |
| `TRACES_OBSERVER_PORT` (observer env) | `AM_OBSERVER_PORT` |
| `OBSERVER_BASE_URL` (observer → upstream env) | `OPENCHOREO_OBSERVER_URL` |
| `traceObserverBaseUrl` (`/api/v1/config` field) | `observerBaseUrl` |
| `OBS_API_BASE_URL` (console; also `console/env.example:16`) | deleted — replaced by `/api/v1/config` discovery |
| `clients/traceobserversvc` (agent-manager) | renamed `clients/observersvc`; trace methods deleted in PR 2; **retained** for workflow-run log fetches (monitor-run logs) |
| `cli/pkg/clients/traceobssvc` + `Factory.TraceObserver` (CLI) | renamed `cli/pkg/clients/observersvc` + `Factory.Observer` (gains logs/metrics/build-logs calls in PR 1) |

**Deliberately NOT renamed:** the evaluation extension's `tracesApiEndpoint` Helm value and the
evaluation job's `--traces-api-endpoint` flag (`evaluation-job/main.py:215`, AGENTS.md, workflow
template) keep their names — they describe the traces API, which survives unchanged. Only the
URL **value** repoints at the renamed service.

**CI / tooling renames (PR 1):**

| Today | Becomes |
|---|---|
| `.github/release-config.json` entry `amp-traces-observer` / dir `traces-observer-service` (publishes the image) | `amp-observer` / `agent-manager-observer` |
| `.github/workflows/traces-observer-service-pr-checks.yaml` (filename, path filters, working dirs) | renamed + repointed |
| `kubectl logs deployment/amp-traces-observer` in `nightly.yml:320`, `e2e.yaml`, `release.yml` | `deployment/amp-observer` |
| `nightly.yml:48` cleanup-images package matrix entry `amp-traces-observer` | `amp-observer` |
| `.github/workflows/instrumentation-matrix-pr.yaml` **and** `instrumentation-matrix-manual.yaml` path filters; `.github/actions/amp-dev-stack/action.yaml` | repointed |
| `deployments/quick-start/install-helpers.sh` (deployment wait + `component=traces-observer` label selector) | repointed |
| Root `Makefile` `-C traces-observer-service gen-instrumentation-contract`; `scripts/check-contract-drift.sh` | repointed |
| `test/e2e/framework/config.go` `TRACES_OBSERVER_BASE_URL`; `test/instrumentation-matrix/` (driver.py, observer.py, RUNBOOK.md, DESIGN.md) | `AM_OBSERVER_BASE_URL` + repointed |

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
- Namespace scoping reuses the observer's **existing** `NamespaceFor` (`observer/client.go:62`),
  exactly as trace queries do. No new namespace config.
- No OpenChoreo existence-validation (org/project/build) in the observer — it trusts scoping
  params. **Accepted behavior changes:** unknown org/project/agent/env returns an empty 200
  instead of agent-manager's 404, and the server-side "runtime logs are not supported for agent
  type" check disappears (the CLI keeps its client-side `ValidateRuntimeManaged` guard; the
  console loses the error).
- **Publisher-audience restriction (REST only):** tokens matching the `amp-publisher-*` audience
  carve-out are accepted on trace routes (the evaluation job's path) and rejected (403) on the
  new `/api/v1/logs`, `/api/v1/build-logs`, and `/api/v1/metrics` REST routes. Note: PR 2's MCP
  server deliberately does **not** replicate this restriction — see PR 2 and Risks.
- Update the observer's own API doc `docs/openapi.yaml` (today traces-only) with the three new
  endpoints and the renames.

### Agent-manager: removals, monitor-run logs, config hardening

- Delete the three agent-scoped REST routes (`build-logs`, `runtime-logs`, `metrics`), their
  controller and service methods, `clients/observabilitysvc` (generated client + Makefile
  `gen-observer-client` target), the OpenAPI paths, and regenerate the spec + CLI client. Delete
  the now-orphaned `OBSERVER_URL` env / `ObserverConfig` / chart `observerURL` / compose entries
  (naming table).
- **Monitor-run logs stay** (`GET .../monitors/{monitorName}/runs/{runId}/logs`): agent-manager
  resolves the workflow-run name from its monitors DB as today, then fetches server-to-server
  from the observer's `/api/v1/build-logs` endpoint via the renamed `clients/observersvc`
  (which gains a `GetWorkflowRunLogs`-equivalent method in this PR). The internal
  `AM_OBSERVER_URL` remains in use for this path. Implementation check: the s2s call
  authenticates via `occlient.AuthProvider` (client-credentials, `amp-api-client`); its token
  must pass the observer's audience allowlist and must NOT match the `amp-publisher-*`
  restriction on `/api/v1/build-logs` — verify what audience Thunder stamps on this token and,
  if needed, add it to the observer chart's allowlist. (No scope/org checks apply — auth is
  deferred.)
- Delete **three** MCP tools that depended on the removed service methods: `get_runtime_logs`
  and `get_metrics` (observability toolset, `mcp/tools/observability.go`) and `get_build_logs`
  (builds toolset, `mcp/tools/builds.go` + `mcp/handlers/build_handler.go`). Full collateral for
  all three: interface entries in `mcp/tools/types.go`, specs tests
  (`mcp/tools/observability_specs_test.go` and builds equivalents), `mcp/tools/mock_test.go`,
  the observability handler's `GetRuntimeLogs`/`GetMetrics` methods and its then-unused
  `agentSvc` dependency (wired in `mcp/setup.go`), and the tool table in `mcp/README.md`. All
  three tools return in PR 2 inside am-obs-mcp. The four remaining trace tools keep working via
  the (renamed) observer client.
- `/api/v1/config`: rename the field to `observerBaseUrl`. `AM_OBSERVER_PUBLIC_URL` has **no
  internal-URL fallback**: empty by default, `http://localhost:9098` only when
  `IS_LOCAL_DEV_ENV=true`. An empty value yields an empty field, which the CLI already rejects
  with a clear `ServerInvalid` error (`cli/pkg/cmdutil/factory.go:124-126`) and the console
  surfaces as "observer not configured".
- **CORS fix:** the config route is registered on the root mux and bypasses the CORS middleware
  that wraps only the `/api/v1/` subtree (`api/app.go`), so browsers cannot read it cross-origin
  today. PR 1 adds CORS handling to this route (move it under the CORS-wrapped tree or apply the
  middleware directly).

### Console

- Bootstrap: fetch `GET /api/v1/config` (unauthenticated, now CORS-enabled) at app init; store
  `observerBaseUrl` in global config; `httpGETObserver` consumes it. The bootstrap must gate
  observability pages (loading state until the config fetch resolves) so they never render
  against a transiently-empty URL. Remove `OBS_API_BASE_URL` from `config.js`,
  `config.template.js`, `env.example`, the console Helm configmap, and the `obsApiBaseUrl?`
  field in `libs/types/src/config/index.ts`.
- Rewrite the logs and metrics API + hook files
  (`libs/api-client/src/{apis,hooks}/{runtime-logs,metrics}.ts`): POST-with-body today, become
  GET + query-param calls against the observer — a request-shape rewrite following the existing
  traces API files as the template. `apis/builds.ts` build-logs is **already GET**; it only
  moves base URL and changes from path params to `{organization, buildName}` query params.
  Response types are unchanged.
- **Component call-sites change with the hook signatures:** `pages/logs/src/Logs.Component.tsx`
  (`useAgentRuntimeLogs`), `pages/metrics/src/Metrics.Component.tsx` (`useGetAgentMetrics`),
  `libs/shared-component/src/components/BuildLogs.tsx` (`useGetBuildLogs`).
- **Traces do NOT gain discovery for free:** `pages/traces/src/Traces.Component.tsx:290-298`
  hard-codes `globalConfig.obsApiBaseUrl` checks and renders a "Set `OBS_API_BASE_URL`…" error —
  rewrite it against the new bootstrap state and the "observer not configured" message.

### CLI

- Rewire `amctl agent logs`, `amctl agent metrics`, `amctl agent build logs` to call the observer
  directly, using the same `/api/v1/config` discovery + bearer-token pattern as
  `amctl agent traces`. Rename `cli/pkg/clients/traceobssvc` → `observersvc` and
  `Factory.TraceObserver` → `Factory.Observer` (naming table); the renamed client gains the
  logs/metrics/build-logs calls. Generated amsvc types pick up the `observerBaseUrl` rename.

### Deployments, CI, e2e

- Helm: agent-manager-service configmap (`AM_OBSERVER_URL`, `AM_OBSERVER_PUBLIC_URL`), console
  configmap (drop `OBS_API_BASE_URL`), observability-extension chart (deployment/service/image
  rename, `tracesObserver.*` → `amObserver.*` values keys, `OPENCHOREO_OBSERVER_URL`,
  `AM_OBSERVER_PORT`).
- **Installer values (breaks PR 1's own CI if missed):** today the chart defaults
  `TRACE_OBSERVER_PUBLIC_URL` from `console.config.obsApiBaseUrl`
  (`templates/agent-manager-service/configmap.yaml:14`), and every in-repo installer sets
  `obsApiBaseUrl` (quick-start, `deployments/vm/lib-vm.sh:48`, k3d docs partials,
  `.github/actions/amp-dev-stack`). PR 1 deletes both the value and the internal fallback, so
  **each installer must explicitly set `AM_OBSERVER_PUBLIC_URL`** — otherwise console
  observability pages die and the amctl e2e ops
  (`test/e2e/operations/cli/agent/observability_operations.go`,
  `test/e2e/operations/cli/agent/build_operations.go`) fail with `ServerInvalid`.
- **Audience:** already aligned at the pin — both charts share the `amp` audience
  (`wso2-agent-manager/values.yaml:192`, `wso2-amp-observability-extension/values.yaml:46`;
  Thunder stamps `aud=amp` on console/CLI query tokens), so no allowlist change is expected for
  user tokens. The one open verification is the monitor-run-logs s2s token (see above).
- docker-compose, port-forward and setup scripts, `.env.example` files, evaluation-extension's
  `tracesApiEndpoint` **value** (name kept — naming section), and AGENTS.md references follow
  the naming table.
- **Docs (current `documentation/docs/` only; versioned_docs excluded):**
  `reference/mcp-server.mdx` tool count and list (19 → 16 tools after this PR),
  `getting-started/_partials/_amp-installation.mdx` (`--set console.config.obsApiBaseUrl` → the
  new `AM_OBSERVER_PUBLIC_URL` value, `tracesObserver.*` → `amObserver.*` set-flags,
  `deployment/amp-traces-observer` → `amp-observer`), `on-k3d.mdx` / `on-your-environment.mdx` /
  `on-a-vm.mdx` (env vars, hostnames, port-forward targets), `reference/cli/agent.mdx` (rewired
  commands).
- **E2E migration:** repoint `test/e2e/operations/agent/{metrics,runtime_logs}.go` and
  `test/e2e/operations/build/build_operations.go` at the observer's new endpoints; rename
  `TRACES_OBSERVER_BASE_URL` in `test/e2e/framework/config.go`; update the instrumentation-matrix
  suite. All `.github` workflow/tooling renames per the CI table above — PR 1 is not mergeable
  until its own CI passes on the renamed paths.

## PR 2 — Observability MCP (am-obs-mcp)

- New `mcp` package inside agent-manager-observer using `modelcontextprotocol/go-sdk` (same
  streamable-HTTP pattern as am-mcp: `mcp/setup.go`'s `RegisterRoute` mounting `/mcp` and
  `/mcp/`), mounted behind the observer's JWT middleware.
- **MCP OAuth plumbing** (required — am-mcp has it, the observer doesn't): a `WWW-Authenticate`
  bearer challenge carrying `resource_metadata` on 401s (port of
  `jwtassertion.buildBearerChallenge`), plus an RFC 9728
  `/.well-known/oauth-protected-resource` route (port of `api/well_known_routes.go`). **New
  observer config this requires** — the observer's own public URL and authorization-server
  settings — gets Helm values (extension chart) and docker-compose wiring in this PR.
- Seven tools calling the observer's own controllers directly (no HTTP hop): `get_runtime_logs`,
  `get_build_logs`, `get_metrics`, `list_traces`, `get_traces`, `get_trace_details`,
  `get_span_details`. Tool inputs mirror the REST query params — including the same validation
  (limits, sort order, required params) — with `organization` as an explicit parameter. Since
  auth is deferred, `organization` stays an explicit param indefinitely; removing it later in
  favor of token claims is a known breaking schema change for saved MCP client configs (see
  deferred section).
- **Publisher-audience tokens are NOT blocked on `/mcp`.** They pass the observer's JWT
  middleware via the existing regex carve-out, so they can call the log/metric/build-log tools
  that the REST routes deny them. Decision: allowed and documented, not rejected — the REST/MCP
  asymmetry is accepted until the deferred auth work revisits token classes (see Risks).
- am-mcp deletes its observability toolset (`mcp/tools/observability.go` — the four remaining
  trace tools) and the observability handler. `clients/observersvc` **survives** with only the
  workflow-run logs method used by monitor-run logs; its trace methods are deleted here. The
  internal `AM_OBSERVER_URL` stays (monitor-run logs is its remaining consumer).
- Docs: how to point an MCP client (or an agent) at the obs MCP endpoint; update
  `reference/mcp-server.mdx` again (16 → 12 tools / four toolsets) and document the new server's
  seven tools.

## Deferred: authorization (formerly PR 3)

Org-scoped, scope-checked authorization on the observer and both MCP servers is deferred. The
previous design (org from `ouHandle`/`ouId` claims, per-route/per-tool `agent:read` scope
checks, a claims package inside the observer, wildcard audience matching) remains the sketch,
but adversarial review found problems that must be solved before it can ship:

1. **The monitor-run-logs s2s call cannot satisfy org+scope checks.** Agent-manager calls
   `/api/v1/build-logs` with an `occlient.AuthProvider` client-credentials token
   (`amp-api-client`) that has no user, hence no `ouHandle` and no `agent:read` scope — under
   the previous PR 3 design it would 403 and monitor-run logs would break. No clean carve-out
   exists in that model: exempting `aud=amp` would disable authZ for every user token. Options
   to evaluate: a dedicated internal audience class exempt from org+scope; an m2m scope grant
   plus relaxed org requirement for that audience; or an internal-only route.
2. **Claims-derived org provides no cross-org isolation while `NamespaceFor` is a constant.**
   `NamespaceFor(_ string)` discards its argument (`observer/client.go:62`) and
   project/agent/environment come unvalidated from query params, so org A can read org B's data
   by supplying B's names regardless of where the org value comes from. Real isolation requires
   the future per-org namespace mapping (or ownership validation). Any auth PR must say plainly
   that it delivers scope enforcement and org-claim presence, not tenant isolation.
3. **Empty org claims must be rejected.** Agent-manager 403s on empty `ouId`/`ouHandle`
   (`middleware/authorization.go:83`); the observer must replicate this or blank-claim tokens
   fall through to the default namespace.
4. **The required-`organization`-param validation must be dropped in the same change** that
   stops clients sending it (`handlers/handlers.go:60,141,199` hard-400 without it today), or
   every query breaks.
5. **Removing the MCP tools' `organization` param is a breaking schema change** for saved MCP
   client configurations — needs the same accepted-breakage treatment as the rename.
6. **Publisher-token org-pinning** (org-from-audience or org-claimed m2m tokens) — tracked as a
   security backlog item; until then publisher tokens have unpinned all-org access to trace
   routes and, per the PR 2 decision, to all am-obs-mcp tools.
7. Implementation notes for whenever this lands: port agent-manager's wildcard audience matching
   (the observer's matcher is exact + publisher-regex only); the observer's package-level
   `jwksCache` globals make table-driven middleware tests order-dependent; MCP tools deriving
   org from claims requires the JWT middleware to inject parsed claims into the request context
   *before* the MCP handler runs (am-mcp's `resolveOUID` pattern); an `RBAC_ENABLED`-equivalent
   toggle and `IS_LOCAL_DEV_ENV` behavior must keep docker-compose flows working.

## Error handling

- Observer REST: existing `ErrorResponse` body + status conventions (400 for missing/invalid
  params, 401 from JWT middleware, 403 for the publisher-token restriction on the new REST
  routes, 502/504 for upstream Observer failures).
- Console: bootstrap-discovery failure (or empty `observerBaseUrl`) surfaces the existing
  "observer not configured/unreachable" state on observability pages rather than blocking the
  whole app.
- CLI: reuse the existing `clierr` taxonomy (`ServerInvalid` on empty `observerBaseUrl`).

## Testing

- **Observer:** unit tests per new handler/controller/client method — table-driven,
  `make fmt && make lint && make test` in the service dir.
- **am-obs-mcp (PR 2):** port am-mcp's tool-registration test pattern
  (`mcp/tools/registration_test.go` / `allToolSpecs`) so tool wiring regressions are caught;
  unit tests for tool input validation.
- **Agent-manager:** update/remove tests for deleted routes, monitor-run logs over the observer
  client (moq mocks), and config (`api/config_handler_test.go` covers empty-default and
  no-internal-fallback behavior).
- **Console:** hook/API tests for the rewritten files; bootstrap discovery test (including the
  gating/loading state).
- **CLI:** command tests for the rewired `agent logs`/`metrics`/`build logs` (existing `traces`
  tests as template).
- **E2E/CI:** the migrated e2e operations run in the PR's own CI (including the cli-driven
  observability/build ops, which depend on the installer values item above);
  instrumentation-matrix and contract-drift jobs pass on renamed paths.
- **Per PR:** docker-compose smoke pass (console pages for logs/metrics/traces render; amctl
  commands succeed; MCP tools callable in PR 2; PR 1's publisher-token 403 on the REST log
  routes verified).

## Risks / accepted breakage

- Full clean rename breaks existing deployments (Helm value names, image, deployment name) and
  released CLIs (`traceObserverBaseUrl` field rename). Accepted — coordinated repo-wide change,
  deployments recreated rather than upgraded in place.
- **AuthZ regression for logs/metrics/build-logs — accepted, indefinite.** Today these routes
  enforce `agent:read`, bind the path org to the token's org claim, and 404 cross-org resource
  names via existence checks. After PR 1 the observer enforces none of that: any token with an
  allowlisted audience — including tokens deliberately *not* granted `agent:read` — can read any
  org's logs, metrics, and build logs by supplying scoping params. This is a scope bypass, not
  just the tenant-isolation gap traces already have, and it persists until the deferred auth
  work ships. Accepted with eyes open; the deferred section is the checklist for closing it.
- **Publisher tokens gain log/metric access via MCP from PR 2 — accepted.** PR 1 blocks
  `amp-publisher-*` tokens on the new REST routes, but PR 2's am-obs-mcp deliberately does not
  replicate the restriction, so the widest-distributed credential class can reach the same data
  through `/mcp`. Decision: allow and document rather than reject; revisit when the deferred
  auth work defines token classes properly.
- Between PR 1 and PR 2, am-mcp has no logs/metrics/build-logs tools (three deleted in PR 1,
  reborn in am-obs-mcp in PR 2). Accepted explicitly.
- Behavior regressions accepted with the move: 404 → empty-200 for unknown scoping values;
  server-side agent-type check for runtime logs dropped.
- Existing helm installs that relied on the internal-URL fallback for `TRACE_OBSERVER_PUBLIC_URL`
  now serve an empty `observerBaseUrl` until they set the new value — a loud, detectable failure
  instead of a silently leaked internal URL. Accepted (in-repo installers are updated in PR 1;
  see the installer-values item).
