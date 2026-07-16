# Agent Manager Observer Implementation Plan (PR 1 + PR 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate runtime observability (logs, metrics, traces) into a single renamed service **agent-manager-observer**, discovered by console + CLI via `GET /api/v1/config`, and (PR 2) add an observability MCP server (am-obs-mcp) inside it.

**Architecture:** PR 1 ports agent-manager's logs/metrics/build-logs retrieval into the traces-observer-service (renamed agent-manager-observer), deletes the agent-manager routes + `clients/observabilitysvc`, keeps monitor-run logs via a server-to-server call through the renamed `clients/observersvc`, and rewires console/CLI/Helm/CI/docs to the new names. PR 2 mounts an MCP server with seven tools inside the observer (calling its controllers directly) and deletes am-mcp's observability toolset.

**Tech Stack:** Go 1.25 stdlib `net/http` (observer), Go + oapi-codegen/openapi-generator + moq + wire (agent-manager-service), React/TS + TanStack Query + Rush (console), cobra CLI, Helm, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-07-16-agent-manager-observer-design.md` (authoritative for scope/decisions).

## Global Constraints

- **Base:** `upstream/main` @ `18851a8b` (already rebased onto it). All citations verified at this pin.
- **Branches:** PR 1 = branch `am-observer-pr1` (targets `main`); PR 2 = branch `am-observer-pr2` (stacked on `am-observer-pr1`). One commit per task. Follow the `am-ship` skill for commit-message/PR conventions. Push to `origin` (jhivandb fork); draft PRs against `wso2/agent-manager`.
- **No backward compatibility** for pre-rename deployments/env/Helm/CLIs — full clean rename. **Exception:** `documentation/versioned_docs/` is NEVER touched.
- **Deliberately NOT renamed:** evaluation extension's `tracesApiEndpoint` Helm key and evaluation-job's `--traces-api-endpoint` flag — only their URL **values** repoint to `amp-observer`.
- **No org/scope authorization work** — authN-only posture is kept (deferred per spec). Do not add claims parsing beyond the publisher-audience 403 on the three new REST routes.
- Naming table (spec "Naming" section) is exhaustive and mandatory: dir/module `agent-manager-observer`, binary `agent-manager-observer`, image `ghcr.io/wso2/amp-observer`, deployment/service `amp-observer`, Helm values `amObserver.*`, envs `AM_OBSERVER_URL` / `AM_OBSERVER_PUBLIC_URL` / `AM_OBSERVER_PORT` / `OPENCHOREO_OBSERVER_URL`, config field `observerBaseUrl`, clients `clients/observersvc` (server) and `cli/pkg/clients/observersvc` + `Factory.Observer` (CLI). `OBSERVER_URL`/`ObserverConfig`(old)/chart `observerURL`/`OBS_API_BASE_URL` are **deleted**.
- **Do not rename** `observerURL:` occurrences in `documentation/docs/getting-started/on-k3d.mdx:601` and `on-your-environment.mdx:855` — those are OpenChoreo dataplane config, not ours.
- Verification gates: observer dir `make fmt && make lint && make test && make build`; agent-manager-service `make fmt && make lint-fix && make test-unit && go build ./...` plus `make codegenfmt-check` after codegen tasks; CLI `cd cli && go build ./... && go test ./...`; console `rushx lint && rushx build` per touched package + `rushx test` where tests exist.
- `make spec` (agent-manager-service) needs Docker. If Docker is unavailable, STOP and surface — do not hand-edit `spec/`.
- agent-manager-service CI lint is strict (goheader license headers, exhaustruct, nilnil, errorlint) and **lints test files**. Every new Go file in either Go service needs the standard Apache-2.0 license header copied from a sibling file.

---

# PR 1 — branch `am-observer-pr1`

### Task 1: Rename traces-observer-service → agent-manager-observer (dir, module, binary, envs)

**Files:**
- Rename: `traces-observer-service/` → `agent-manager-observer/` (git mv)
- Modify: `agent-manager-observer/go.mod:1` (module path), every `.go` file importing `github.com/wso2/agent-manager/traces-observer-service/...` (~15 imports across `main.go`, `handlers/handlers.go`, `controllers/controller.go`, `controllers/controller_test.go`, `middleware/auth.go`, `observer/convert.go`)
- Modify: `agent-manager-observer/Makefile:5` (`APP_NAME=agent-manager-observer`), `Dockerfile:24,36,49`, `Dockerfile.dev:24,36,49` (`/go/bin/agent-manager-observer`), `.air.toml:7-8`, `.gitignore`
- Modify: `agent-manager-observer/config/config.go:61` (`TRACES_OBSERVER_PORT` → `AM_OBSERVER_PORT`), `config/config.go:64` (`OBSERVER_BASE_URL` → `OPENCHOREO_OBSERVER_URL`), `config/config_test.go` (all env refs), `.env.example`, `README.MD`, `AGENTS.md`
- Modify: `agent-manager-observer/docs/openapi.yaml:3` (title `Agent Manager Observer API`), `:24` (server `https://observer.example.com`), `:367` (error text mentioning `OBSERVER_BASE_URL`)
- Modify: root `Makefile:262-263` (`-C agent-manager-observer gen-instrumentation-contract`), `scripts/check-contract-drift.sh:12`

**Interfaces:**
- Produces: Go module `github.com/wso2/agent-manager/agent-manager-observer`; env vars `AM_OBSERVER_PORT` (default 9098), `OPENCHOREO_OBSERVER_URL` (upstream Observer base URL). All later observer tasks import this module path.

**Steps:**

- [ ] **Step 1: git mv + module rename**

```bash
git mv traces-observer-service agent-manager-observer
cd agent-manager-observer
# go.mod line 1:
#   module github.com/wso2/agent-manager/agent-manager-observer
grep -rl 'agent-manager/traces-observer-service' --include='*.go' . | xargs sed -i '' 's|agent-manager/traces-observer-service|agent-manager/agent-manager-observer|g'
```

- [ ] **Step 2: rename binary + env vars + docs per Files list.** In `config/config.go` change only the two env-var name strings (`TRACES_OBSERVER_PORT`→`AM_OBSERVER_PORT`, `OBSERVER_BASE_URL`→`OPENCHOREO_OBSERVER_URL`); struct/field names stay. Update `config/config_test.go` env names (6 sites: lines 29,35,64,70,84,90 + any `OBSERVER_BASE_URL` in the ~9 sites found by grep). Update `.env.example`, `README.MD` (lines 31,49,53,59,60,74,84,85,213,227,234), `AGENTS.md` (1,45,61-62,73,98), Dockerfiles, `.air.toml`, `.gitignore`, openapi title/server/error text. `Makefile:5` `APP_NAME=agent-manager-observer`.

- [ ] **Step 3: root Makefile + drift script.** `Makefile:262-263` and `scripts/check-contract-drift.sh:12` → `agent-manager-observer`.

- [ ] **Step 4: verify**

```bash
cd agent-manager-observer && make fmt && make lint && make test && make build
grep -rn 'traces-observer\|TRACES_OBSERVER\|OBSERVER_BASE_URL' agent-manager-observer/  # expect: no hits
bash scripts/check-contract-drift.sh   # from repo root; expect pass
```

- [ ] **Step 5: commit** (`git add -A && git commit` — message per am-ship, e.g. "Rename traces-observer-service to agent-manager-observer")

---

### Task 2: Observer upstream client — QueryLogs / QueryMetrics

**Files:**
- Modify: `agent-manager-observer/observer/types.go` (new request/response types)
- Modify: `agent-manager-observer/observer/client.go` (interface + 2 methods)
- Modify: `agent-manager-observer/controllers/controller_test.go` (`fakeObserverClient` gains the 2 methods)
- Test: `agent-manager-observer/observer/client_test.go` (new)

**Interfaces:**
- Consumes: existing `clientImpl.doPost` (`client.go:92`), `ComponentSearchScope` (`types.go:23-28`).
- Produces (used by Task 3):
  - `QueryLogs(ctx context.Context, req LogsQueryRequest) (*LogsQueryResponse, error)`
  - `QueryMetrics(ctx context.Context, req MetricsQueryRequest) (*ResourceMetricsTimeSeries, error)`
  - `observer.WorkflowSearchScope{Namespace string; WorkflowRunName *string}`
  - `observer.LogEntry{Timestamp *time.Time; Log *string; Level *string}` (decodes both component and workflow upstream entries)
  - `observer.LogsQueryResponse{Logs []LogEntry; Total *int; TookMs *int}`
  - `observer.MetricsTimeSeriesItem{Timestamp *time.Time; Value *float64}`, `observer.ResourceMetricsTimeSeries{CpuUsage, CpuRequests, CpuLimits, MemoryUsage, MemoryRequests, MemoryLimits *[]MetricsTimeSeriesItem}`

**Steps:**

- [ ] **Step 1: confirm upstream paths.** The spec says upstream is `POST /api/v1/logs/query` and `POST /api/v1/metrics/query`. Verify against the generated client before writing code:

```bash
grep -n 'logs/query\|metrics/query' agent-manager-service/clients/observabilitysvc/gen/client.gen.go | head
```

Use whatever exact paths appear there (expected: `/api/v1/logs/query`, `/api/v1/metrics/query`).

- [ ] **Step 2: add types to `observer/types.go`** (license header already present in file):

```go
// WorkflowSearchScope scopes a logs query to an Argo workflow run.
type WorkflowSearchScope struct {
	Namespace       string  `json:"namespace"`
	WorkflowRunName *string `json:"workflowRunName,omitempty"`
}

// LogsQueryRequest is the upstream Observer logs query. SearchScope takes either
// a ComponentSearchScope or a WorkflowSearchScope (upstream oneOf).
type LogsQueryRequest struct {
	StartTime    time.Time `json:"startTime"`
	EndTime      time.Time `json:"endTime"`
	Limit        *int      `json:"limit,omitempty"`
	LogLevels    []string  `json:"logLevels,omitempty"`
	SearchPhrase *string   `json:"searchPhrase,omitempty"`
	SortOrder    *string   `json:"sortOrder,omitempty"`
	SearchScope  any       `json:"searchScope"`
}

// LogEntry decodes both component log entries (level+log+timestamp) and
// workflow log entries (log+timestamp) from the upstream Observer.
type LogEntry struct {
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Log       *string    `json:"log,omitempty"`
	Level     *string    `json:"level,omitempty"`
}

type LogsQueryResponse struct {
	Logs   []LogEntry `json:"logs"`
	Total  *int       `json:"total,omitempty"`
	TookMs *int       `json:"tookMs,omitempty"`
}

// MetricsQueryRequest queries resource metrics for a component.
type MetricsQueryRequest struct {
	StartTime   time.Time            `json:"startTime"`
	EndTime     time.Time            `json:"endTime"`
	Metric      string               `json:"metric"` // always "resource"
	SearchScope ComponentSearchScope `json:"searchScope"`
}

type MetricsTimeSeriesItem struct {
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Value     *float64   `json:"value,omitempty"`
}

type ResourceMetricsTimeSeries struct {
	CpuUsage       *[]MetricsTimeSeriesItem `json:"cpuUsage,omitempty"`
	CpuRequests    *[]MetricsTimeSeriesItem `json:"cpuRequests,omitempty"`
	CpuLimits      *[]MetricsTimeSeriesItem `json:"cpuLimits,omitempty"`
	MemoryUsage    *[]MetricsTimeSeriesItem `json:"memoryUsage,omitempty"`
	MemoryRequests *[]MetricsTimeSeriesItem `json:"memoryRequests,omitempty"`
	MemoryLimits   *[]MetricsTimeSeriesItem `json:"memoryLimits,omitempty"`
}
```

- [ ] **Step 3: extend `observer/client.go`.** Add to the `Client` interface:

```go
	QueryLogs(ctx context.Context, req LogsQueryRequest) (*LogsQueryResponse, error)
	QueryMetrics(ctx context.Context, req MetricsQueryRequest) (*ResourceMetricsTimeSeries, error)
```

Implementations (same shape as `QueryTraces`, `client.go:66`):

```go
func (c *clientImpl) QueryLogs(ctx context.Context, req LogsQueryRequest) (*LogsQueryResponse, error) {
	var result LogsQueryResponse
	if err := c.doPost(ctx, "/api/v1/logs/query", req, &result); err != nil {
		return nil, fmt.Errorf("observer.QueryLogs: %w", err)
	}
	return &result, nil
}

func (c *clientImpl) QueryMetrics(ctx context.Context, req MetricsQueryRequest) (*ResourceMetricsTimeSeries, error) {
	var result ResourceMetricsTimeSeries
	if err := c.doPost(ctx, "/api/v1/metrics/query", req, &result); err != nil {
		return nil, fmt.Errorf("observer.QueryMetrics: %w", err)
	}
	return &result, nil
}
```

(Adjust paths per Step 1 findings.)

- [ ] **Step 4: write failing test** `observer/client_test.go`: `httptest.Server` asserting method POST, path, request body round-trip (decode `LogsQueryRequest`, assert `searchScope` serialized correctly for both `ComponentSearchScope` and `WorkflowSearchScope`), canned JSON response decoded into `LogsQueryResponse`/`ResourceMetricsTimeSeries`. Construct client with `NewClient(srv.URL, NewAuthProvider("", "", ""), "default")` — with empty TokenURL `GetToken` returns an error, so instead pass a stub: check `observer/auth.go`; if AuthProvider cannot be stubbed cheaply, point TokenURL at a second httptest server returning `{"access_token":"t","expires_in":3600}`.

- [ ] **Step 5: update `fakeObserverClient`** in `controllers/controller_test.go:34-79` — add the two methods (return scripted values / zero values) so it still satisfies `observer.Client`.

- [ ] **Step 6: run** `make fmt && make lint && make test` in `agent-manager-observer/`. Expect pass.

- [ ] **Step 7: commit** ("Add upstream logs/metrics query methods to observer client")

---

### Task 3: Observer controller — wire models, conversions, GetLogs/GetBuildLogs/GetMetrics

**Files:**
- Create: `agent-manager-observer/controllers/observability.go`
- Test: `agent-manager-observer/controllers/observability_test.go`

**Interfaces:**
- Consumes: Task 2's `observer.Client.QueryLogs/QueryMetrics`, `observer.ComponentSearchScope`, `observer.WorkflowSearchScope`, `NamespaceFor`.
- Produces (used by Task 4 handlers and PR 2 MCP tools):
  - `controllers.NewObservabilityController(observerClient observer.Client) *ObservabilityController`
  - `type LogsQueryParams struct { Organization, Project, Agent, Environment string; StartTime, EndTime time.Time; SearchPhrase string; LogLevels []string; Limit *int; SortOrder string }`
  - `type MetricsQueryParams struct { Organization, Project, Agent, Environment string; StartTime, EndTime time.Time }`
  - `(*ObservabilityController) GetLogs(ctx, LogsQueryParams) (*LogsResponse, error)`
  - `(*ObservabilityController) GetBuildLogs(ctx, organization, buildName string) (*LogsResponse, error)`
  - `(*ObservabilityController) GetMetrics(ctx, MetricsQueryParams) (*MetricsResponse, error)`
  - Wire types (exact JSON parity with agent-manager's `spec.LogsResponse`/`spec.MetricsResponse`):
    - `type LogEntry struct { Timestamp time.Time \`json:"timestamp"\`; Log string \`json:"log"\`; LogLevel string \`json:"logLevel"\` }`
    - `type LogsResponse struct { Logs []LogEntry \`json:"logs"\`; TotalCount int32 \`json:"totalCount"\`; TookMs float32 \`json:"tookMs"\` }`
    - `type MetricDataPoint struct { Time string \`json:"time"\`; Value float64 \`json:"value"\` }`
    - `type MetricsResponse struct { CpuUsage, CpuRequests, CpuLimits, Memory, MemoryRequests, MemoryLimits []MetricDataPoint }` (json tags `cpuUsage`,`cpuRequests`,`cpuLimits`,`memory`,`memoryRequests`,`memoryLimits`)

**Steps:**

- [ ] **Step 1: write failing tests** in `controllers/observability_test.go` using the `fakeObserverClient` pattern. Cases: (a) GetLogs builds `LogsQueryRequest` with `ComponentSearchScope{Namespace: NamespaceFor(org), Project/Component/Environment}` and converts entries; (b) JSON-log `msg` extraction: entry log `{"time":"2026-01-01T00:00:00Z","level":"info","msg":"hello"}` with empty upstream Level → LogEntry{Log:"hello", LogLevel:"INFO"}; (c) GetBuildLogs uses `WorkflowSearchScope{WorkflowRunName}`, limit 1000, asc, ~30-day window (assert on captured request); (d) GetMetrics maps all six series and nil series → empty slices (never nil, exhaustruct-friendly).

- [ ] **Step 2: implement `controllers/observability.go`.** Port the conversion logic verbatim-in-spirit from `agent-manager-service/clients/observabilitysvc/client.go`:
  - `convertLogEntry` ports `convertComponentLogEntry` (client.go:352): copy timestamp/log/level, then if `strings.HasPrefix(strings.TrimSpace(log), "{")` json-parse and extract `msg` (string, non-empty → replaces Log), fallback `level` (uppercased) when LogLevel empty, fallback `time` (RFC3339 then `"2006-01-02T15:04:05"`) when Timestamp zero.
  - `convertTimeSeries` ports `convertTimeSeriesData` (client.go: nil → `[]MetricDataPoint{}`; format timestamps `time.RFC3339`).
  - `GetLogs`: build `observer.LogsQueryRequest{StartTime, EndTime, Limit: params.Limit, LogLevels: params.LogLevels, SearchPhrase: optional, SortOrder: optional, SearchScope: observer.ComponentSearchScope{Namespace: c.observerClient.NamespaceFor(params.Organization), Project: &params.Project, Component: &params.Agent, Environment: &params.Environment}}` → `QueryLogs` → convert (`TotalCount` from `Total` else `len(logs)`, `TookMs` from `TookMs`).
  - `GetBuildLogs`: `endTime := time.Now(); startTime := endTime.Add(-30 * 24 * time.Hour)`; limit 1000; sortOrder "asc"; `SearchScope: observer.WorkflowSearchScope{Namespace: c.observerClient.NamespaceFor(organization), WorkflowRunName: &buildName}`.
  - `GetMetrics`: `observer.MetricsQueryRequest{Metric: "resource", SearchScope: ComponentSearchScope{...}}` → `QueryMetrics` → map CpuUsage→CpuUsage, MemoryUsage→Memory, etc.

- [ ] **Step 3: run** `make fmt && make lint && make test`. Expect pass.

- [ ] **Step 4: commit** ("Add observability controller to agent-manager-observer")

---

### Task 4: Observer REST endpoints + publisher-token 403 + OpenAPI

**Files:**
- Modify: `agent-manager-observer/handlers/handlers.go` (Handler struct + 3 handlers + validation helpers)
- Create: `agent-manager-observer/middleware/publisher_guard.go`
- Modify: `agent-manager-observer/main.go:101-116` (wiring), `agent-manager-observer/docs/openapi.yaml`
- Test: `handlers/handlers_test.go` (extend + fix latent gap), `middleware/publisher_guard_test.go`

**Interfaces:**
- Consumes: Task 3 controller + params types; existing `writeError`/`writeJSON`/`parseRFC3339`/`parseLimit`/`parseSortOrder` (`handlers.go:334-382`); `validPublisherAudPattern` (`middleware/auth.go:38`); `writeAuthError` (`middleware/auth.go:53-71`).
- Produces: `GET /api/v1/logs`, `GET /api/v1/build-logs`, `GET /api/v1/metrics` (wire shapes from Task 3); `middleware.RejectPublisherAudience() func(http.Handler) http.Handler`. `handlers.NewHandler(tracing *controllers.TracingController, obs *controllers.ObservabilityController) *Handler` — **signature change**; PR 2 MCP reuses the controllers, not the handlers.

**Steps:**

- [ ] **Step 1: failing handler tests.** Extend `handlers_test.go`: `newHandler()` becomes `handlers.NewHandler(nil, nil)`. New tests for `GetLogs`/`GetBuildLogs`/`GetMetrics` covering: missing organization/project/agent/environment → 400; bad startTime/endTime → 400; invalid logLevels value → 400; limit > 10000 → 400 (logs); sortOrder invalid → 400; build-logs missing buildName → 400; non-GET method → 405. **Also fix the latent gap:** the existing trace-handler test `baseParams()` (handlers_test.go:33) sends `namespace=` instead of `organization=` so only the first branch is ever hit — change it to send `organization` and all other required params, so each missing-param test isolates its own branch.

- [ ] **Step 2: implement the three handlers** in `handlers.go`, mirroring `GetTraceOverviews` (`handlers.go:53-125`) exactly. Constants: `maxLogLimit = 10000`, `maxLogRangeDays = 14` (ported from agent-manager `utils/constants.go:94-98`). Validation for `/api/v1/logs`: organization, project, agent, environment required; startTime/endTime required RFC3339 with `endTime >= startTime`, `startTime` not in the future, and range ≤ 14 days (port `validateTimes`, `agent-manager-service/utils/utils.go:1146`); `logLevels` comma-separated, each ∈ {INFO,DEBUG,WARN,ERROR} (case-insensitive → uppercase); `limit` optional, 0 < limit ≤ 10000 (empty → nil pointer, upstream default); `sortOrder` optional asc|desc (empty → ""). `/api/v1/build-logs`: organization + buildName required, nothing else accepted. `/api/v1/metrics`: organization, project, agent, environment, startTime, endTime required (no range cap — parity with agent-manager metrics which only parses RFC3339). Handlers reject non-GET with 405. Map controller errors → 502 (`"failed to query upstream observer"`) using the existing generic-500 style but with `http.StatusBadGateway` per the spec's error-handling section.

- [ ] **Step 3: implement `middleware/publisher_guard.go`** (package `middleware`, so it can use `validPublisherAudPattern`):

```go
// RejectPublisherAudience returns middleware that rejects tokens whose audience
// matches the amp-publisher-* carve-out with 403. It runs after JWTAuth, so the
// token is already signature-verified; it is re-parsed here without verification
// only to read the audience claim.
func RejectPublisherAudience() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			tokenString := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			claims := jwt.MapClaims{}
			parser := jwt.NewParser()
			if _, _, err := parser.ParseUnverified(tokenString, claims); err == nil {
				if audiences, err := claims.GetAudience(); err == nil {
					for _, aud := range audiences {
						if validPublisherAudPattern.MatchString(strings.TrimSpace(aud)) {
							writeAuthError(w, http.StatusForbidden,
								"publisher tokens are not permitted on this endpoint")
							return
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

Test (`middleware/publisher_guard_test.go`): craft unsigned HS256 tokens via `jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"aud": "amp-publisher-x"}).SignedString([]byte("k"))`; assert 403 for publisher aud, pass-through for `aud=amp`, pass-through for missing/garbled token (JWTAuth already handles those).

- [ ] **Step 4: wire routes in `main.go`** (inside the block at lines 101-110):

```go
	obsController := controllers.NewObservabilityController(observerClient)
	handler := handlers.NewHandler(controller, obsController)
	...
	noPublisher := middleware.RejectPublisherAudience()
	apiMux.Handle("/api/v1/logs", noPublisher(http.HandlerFunc(handler.GetLogs)))
	apiMux.Handle("/api/v1/build-logs", noPublisher(http.HandlerFunc(handler.GetBuildLogs)))
	apiMux.Handle("/api/v1/metrics", noPublisher(http.HandlerFunc(handler.GetMetrics)))
```

(These inherit `JWTAuth` from the `apiMux` mount at `main.go:115-116`.)

- [ ] **Step 5: update `docs/openapi.yaml`.** Add the three paths in the existing style (tags `logs`,`metrics`; operationIds `queryLogs`, `queryBuildLogs`, `queryMetrics`; bearerAuth; reuse/extend `components/parameters`; add `403 Forbidden` response for publisher tokens; schemas `LogEntry`, `LogsResponse`, `MetricDataPoint`, `MetricsResponse` matching Task 3 wire shapes). **Also fix the pre-existing mismatch:** rename the `namespace` reusable parameter to `organization` (and `component`→`agent` where the code reads `agent`) so the spec matches what the handlers actually read.

- [ ] **Step 6: run** `make fmt && make lint && make test && make build`. Expect pass.

- [ ] **Step 7: commit** ("Add logs, build-logs and metrics endpoints to agent-manager-observer")

---

### Task 5: agent-manager — observer config rename, /api/v1/config hardening, CORS

**Files:**
- Modify: `agent-manager-service/config/config.go` (delete `ObserverConfig` struct :236, rename `TraceObserverConfig` :226 → `ObserverConfig` with `URL`/`PublicURL`; field `Observer ObserverConfig` replaces both :44/:47)
- Modify: `agent-manager-service/config/config_loader.go:130-148` (delete OBSERVER_URL block; rename envs; kill fallback; IS_LOCAL_DEV_ENV default), `:347-365` (`validateTraceObserverURLs` → `validateObserverURLs`), `config/config_loader_test.go:174-228`
- Modify: `agent-manager-service/docs/api_v1_openapi.yaml:10846-10848` (`traceObserverBaseUrl` → `observerBaseUrl`), then `make spec` (regenerates `spec/model_config_response.go`)
- Modify: `agent-manager-service/api/config_handler.go`, `api/app.go:46`, `api/config_handler_test.go`
- Modify: `agent-manager-service/.env.example`, `agent-manager-service/wiring/{wire.go,wire_gen.go,params.go}` (cfg field renames), `clients/traceobserversvc/client.go` construction site (`wire.go:282` reads `cfg.TraceObserver.URL` → `cfg.Observer.URL`)

**Interfaces:**
- Produces: `config.GetConfig().Observer.URL` (env `AM_OBSERVER_URL`, default `http://localhost:9098`) and `.Observer.PublicURL` (env `AM_OBSERVER_PUBLIC_URL`, default `""`; `http://localhost:9098` only when `IS_LOCAL_DEV_ENV=true`). `GET /api/v1/config` → `{"observerBaseUrl": "..."}` (`spec.ConfigResponse.ObserverBaseUrl`), CORS-enabled, still unauthenticated. Tasks 6/8/9 depend on these names.

**Steps:**

- [ ] **Step 1: failing config tests.** Rewrite `config_loader_test.go:174-228` for: `AM_OBSERVER_URL` default `http://localhost:9098`; `AM_OBSERVER_PUBLIC_URL` unset → `""`; unset + `IS_LOCAL_DEV_ENV=true` → `http://localhost:9098`; explicit value wins; invalid URL → validation error. Delete the OBSERVER_URL test.

- [ ] **Step 2: implement loader.** Note ordering: `IS_LOCAL_DEV_ENV` is currently read at `config_loader.go:148` *after* the observer block — move the `config.IsLocalDevEnv = r.readOptionalBool("IS_LOCAL_DEV_ENV", false)` read **above** the observer block, then:

```go
	// Observer service configuration. URL is used server-side (in-cluster) for
	// monitor-run log fetches. PublicURL is handed to out-of-cluster clients
	// (console, CLI) via GET /api/v1/config. It has NO internal-URL fallback:
	// empty means "observer not configured" and clients surface that loudly.
	publicURLDefault := ""
	if config.IsLocalDevEnv {
		publicURLDefault = "http://localhost:9098"
	}
	config.Observer = ObserverConfig{
		URL:       r.readOptionalString("AM_OBSERVER_URL", "http://localhost:9098"),
		PublicURL: r.readOptionalString("AM_OBSERVER_PUBLIC_URL", publicURLDefault),
	}
```

Delete the old `ObserverConfig`/`OBSERVER_URL` block (`config_loader.go:131-133`) and the old struct (config.go:236). Rename `validateTraceObserverURLs` → `validateObserverURLs`, update env names in its error messages.

- [ ] **Step 3: OpenAPI + spec regen.** In `docs/api_v1_openapi.yaml` rename the `ConfigResponse` property and required entry `traceObserverBaseUrl` → `observerBaseUrl` (description: "Base URL for the agent-manager-observer service"). Run `make spec` (Docker required). `spec.ConfigResponse` now has `ObserverBaseUrl`.

- [ ] **Step 4: config handler + CORS.** `api/config_handler.go`:

```go
func registerConfigRoutes(mux *http.ServeMux) {
	corsWrap := middleware.CORS(config.GetConfig().CORSAllowedOrigin)
	mux.Handle("GET /api/v1/config", corsWrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := config.GetConfig()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(spec.ConfigResponse{
			ObserverBaseUrl: cfg.Observer.PublicURL,
		}); err != nil {
			logger.GetLogger(r.Context()).Error("failed to encode config response", "error", err)
		}
	})))
}
```

Check `middleware.CORS`'s handling of `OPTIONS` preflight — if the Go 1.22 mux method pattern `GET` blocks preflight, register the pattern without the method and 405 non-GET inside the handler (mirror what the CORS middleware needs). Update `api/config_handler_test.go`: field rename in assertions (lines 64-65, 85-86, 102-103), helper `withTraceObserverURL` → `withObserverPublicURL` mutating `config.GetConfig().Observer.PublicURL`, and add a CORS test (request with `Origin` header → `Access-Control-Allow-Origin` present; `OPTIONS` preflight → 2xx).

- [ ] **Step 5: chase compile errors** from the `cfg.TraceObserver` → `cfg.Observer` rename (`wiring/*`, `api/app.go`, `clients/traceobserversvc` provider, `config_loader_test.go` host strings mentioning `amp-traces-observer` → `amp-observer`). Update `.env.example` (delete `OBSERVER_URL`, rename `TRACE_OBSERVER_*`).

- [ ] **Step 6: run** `make fmt && make lint-fix && make test-unit && go build ./...` in `agent-manager-service/`. Expect pass.

- [ ] **Step 7: commit** ("Rename observer config, harden /api/v1/config, add CORS")

---

### Task 6: agent-manager — clients/observersvc rename + GetWorkflowRunLogs + monitor rewire

**Files:**
- Rename: `agent-manager-service/clients/traceobserversvc/` → `agent-manager-service/clients/observersvc/` (package `observersvc`; type `TraceObserverSvcClient` → `ObserverSvcClient`; constructor `NewTraceObserverClient` → `NewObserverClient`)
- Modify: `clients/observersvc/client.go` (new method), `clients/observersvc/types.go`
- Modify: `services/monitor_manager.go:998-1033` (+ struct field, constructor), `wiring/{wire.go:282,wire_gen.go,params.go}`, `mcp/handlers/observability_handler.go`, `mcp/setup.go`, `api/app.go` (type renames only)
- Create: moq directive on the interface → `clients/clientmocks/observer_client_fake.go` (via `make codegen`)
- Test: `services/monitor_manager_test.go` (or the existing monitor test file — update GetMonitorRunLogs tests to the new mock)

**Interfaces:**
- Consumes: Task 5's `cfg.Observer.URL`; observer endpoint `GET /api/v1/build-logs?organization&buildName` (Task 4); `occlient.AuthProvider` (unchanged).
- Produces: `observersvc.ObserverSvcClient` interface = existing four trace methods + `GetWorkflowRunLogs(ctx context.Context, organization, workflowRunName string) (*models.LogsResponse, error)`. Mock `clientmocks.ObserverSvcClientMock`. Task 7 deletes `clients/observabilitysvc` only after this lands.

**Steps:**

- [ ] **Step 1: mechanical rename.** `git mv clients/traceobserversvc clients/observersvc`; sed package/type/constructor names across the ~27 references (`clients/observersvc/*.go`, `mcp/handlers/observability_handler.go`, `mcp/setup.go`, `wiring/{params.go,wire.go,wire_gen.go}`, `api/app.go`). Provider `ProvideTraceObserverClient` → `ProvideObserverClient`. `HTTPError.Error()` strings `"traces-observer: ..."` → `"observer: ..."`, `"traceobssvc: baseURL is required"`-style message in server client → `"observersvc: ..."`.

- [ ] **Step 2: add moq directive** above the interface (copy style from `clients/observabilitysvc/client.go`):

```go
//go:generate moq -rm -fmt goimports -skip-ensure -pkg clientmocks -out ../clientmocks/observer_client_fake.go . ObserverSvcClient:ObserverSvcClientMock
```

- [ ] **Step 3: failing service test.** Update the `GetMonitorRunLogs` unit test to inject `ObserverSvcClientMock` with `GetWorkflowRunLogsFunc: func(ctx, org, run string) (*models.LogsResponse, error)`, asserting it receives `(ouID, run.Name)` and that repo-not-found paths still map to `utils.ErrMonitorNotFound`/`ErrMonitorRunNotFound`.

- [ ] **Step 4: implement `GetWorkflowRunLogs`** in `clients/observersvc/client.go` (reuse the existing `doGetMap`/`sendGet` plumbing but decode a typed response):

```go
// GetWorkflowRunLogs fetches build/workflow-run logs from the agent-manager-observer.
// The observer resolves the upstream namespace from the organization itself.
func (c *observerSvcClient) GetWorkflowRunLogs(ctx context.Context, organization, workflowRunName string) (*models.LogsResponse, error) {
	q := url.Values{}
	q.Set("organization", organization)
	q.Set("buildName", workflowRunName)
	var out models.LogsResponse
	if err := c.doGetJSON(ctx, "/api/v1/build-logs", q, &out); err != nil {
		return nil, fmt.Errorf("observersvc.GetWorkflowRunLogs: %w", err)
	}
	return &out, nil
}
```

Add `doGetJSON` as a typed twin of the existing `doGetMap` (same auth/401-retry behavior, decodes into the target instead of `map[string]any`). The observer's wire `{logs:[{timestamp,log,logLevel}],totalCount,tookMs}` decodes into `models.LogsResponse` (json tags match; extra `models.LogEntry` fields stay zero).

- [ ] **Step 5: rewire monitor service.** In `services/monitor_manager.go`: replace the `observabilitySvcClient` field with `observerClient observersvc.ObserverSvcClient` (constructor + wiring provider args), and change line ~1025 to:

```go
	logs, err := s.observerClient.GetWorkflowRunLogs(ctx, ouID, run.Name)
```

(The `s.ocClient.NamespaceFor(ouID)` call disappears — the observer maps org→namespace itself. If `ocClient` becomes unused in this service, remove it; the compiler decides.)

- [ ] **Step 6: codegen + verify.** `make codegen` (regenerates wire + mocks), then `make fmt && make lint-fix && make test-unit && go build ./... && make codegenfmt-check`.

- [ ] **Step 7: commit** ("Rename traceobserversvc to observersvc and route monitor-run logs through the observer")

---

### Task 7: agent-manager — delete logs/metrics/build-logs routes, observabilitysvc, and 3 MCP tools

**Files:**
- Modify: `api/agent_routes.go` (delete lines 38, 47, 48)
- Modify: `controllers/` AgentController (delete `GetBuildLogs`, `GetAgentRuntimeLogs`, `GetAgentMetrics` handlers), `services/agent_manager.go` (delete methods at :4607, :4653, :4703 + interface entries + `observabilitySvcClient` field), service unit tests for those methods
- Delete: `clients/observabilitysvc/` (whole dir), `clients/clientmocks/observability_client_fake.go`
- Modify: `wiring/{wire.go,wire_gen.go,params.go}` (drop `ProvideObservabilitySvcClient`), `agent-manager-service/Makefile` (delete `gen-observer-client` target :87-95)
- Modify: `docs/api_v1_openapi.yaml` (delete the 3 paths + now-orphaned schemas `LogFilterRequest`, `MetricsFilterRequest`, `MetricsResponse`, `MetricDataPoint` — **keep `LogsResponse`/`LogEntry`**, still used by monitor-run logs) + `make spec`
- MCP: `mcp/tools/observability.go` (delete `get_runtime_logs` + `get_metrics` registrations, `runtimeLogsInput`, `getMetricsInput`, `resolveTimeWindow`, `normalizeLogLevels`, `reduceLogsResponse` if now dead), `mcp/tools/builds.go` (delete `get_build_logs` :142 + `getBuildLogs` :270 + input/output structs), `mcp/tools/types.go` (delete `BuildToolsetHandler.GetBuildLogs` :51, `ObservabilityToolsetHandler.GetRuntimeLogs` :61, `.GetMetrics` :62), `mcp/handlers/build_handler.go:46` (delete `GetBuildLogs`), `mcp/handlers/observability_handler.go` (delete `GetRuntimeLogs`/`GetMetrics`, drop `agentSvc` field + constructor arg), `mcp/setup.go` (constructor call), `mcp/tools/mock_test.go` (delete :127, :164, :172), `mcp/tools/observability_specs_test.go` (delete 2 spec entries), `mcp/tools/builds_specs_test.go` (delete `get_build_logs` entry), `mcp/README.md:62` (delete row)
- Modify: `utils/utils.go` (`ValidateLogFilterRequest` :1108, `validateTimes`, `isValidLogLevel` — delete **only if** no remaining callers), `utils/makeresults.go` (`ConvertToMetricsResponse` :540 — delete; **keep** `ConvertToLogsResponse` :522, used by monitor-run logs)

**Interfaces:**
- Consumes: Task 6 (monitor-run logs no longer uses observabilitysvc).
- Produces: agent-manager no longer serves `/build-logs`, `/runtime-logs`, `/metrics` under agents; am-mcp has 16 tools. Task 8 regenerates the CLI client afterwards.

**Steps:**

- [ ] **Step 1: delete OpenAPI paths + regen.** Remove the three path items (`.../builds/{buildName}/build-logs` GET, `.../metrics` POST, `.../runtime-logs` POST) and orphaned schemas listed above from `docs/api_v1_openapi.yaml`; run `make spec`.

- [ ] **Step 2: delete server code top-down** (routes → controllers → service methods + interface entries → `observabilitySvcClient` field/wiring → `clients/observabilitysvc/` → clientmocks fake → Makefile target). Let the compiler drive: `go build ./...` after each layer. Delete the service unit tests for the three deleted methods; update any `agentManagerService` struct literals in tests that set `observabilitySvcClient` (exhaustruct lint requires touching every constructor literal).

- [ ] **Step 3: MCP deletions** per the Files list. Then run the MCP test suite — `TestToolRegistration` and the specs tests must pass with 16 tools.

- [ ] **Step 4: orphan sweep.** `grep -rn 'ValidateLogFilterRequest\|ConvertToMetricsResponse\|observabilitysvc' agent-manager-service/` — delete confirmed orphans (and their tests), keep anything still referenced.

- [ ] **Step 5: verify** `make fmt && make lint-fix && make test-unit && go build ./... && make codegenfmt-check`.

- [ ] **Step 6: commit** ("Remove logs/metrics/build-logs routes and observability client from agent-manager")

---

### Task 8: CLI — regen client, rename observersvc, rewire logs/metrics/build-logs commands

**Files:**
- Regen: `cli/pkg/clients/amsvc/gen/{types.gen.go,client.gen.go}` via root `make amctl-gen-client`
- Rename: `cli/pkg/clients/traceobssvc/` → `cli/pkg/clients/observersvc/` (package `observersvc`)
- Modify: `cli/pkg/clients/observersvc/{client.go,types.go,client_test.go}` (3 new methods + types), `cli/pkg/cmdutil/factory.go` (`Factory.TraceObserver` → `Factory.Observer`, `discoverObserverURL`, `ObserverBaseUrl`, error copy), `cli/pkg/cmdutil/errors.go` (`TraceObserverErrorFromResponse` → `ObserverErrorFromResponse`)
- Modify: `cli/pkg/cmd/agent/traces/*.go` (+tests) — type/field renames only; `cli/pkg/cmd/agent/create/create.go` (+test) — import rename
- Modify: `cli/pkg/cmd/agent/logs.go` (+`logs_test.go`), `cli/pkg/cmd/agent/metrics.go` (+`metrics_test.go`), `cli/pkg/cmd/agent/build/logs.go` (+tests)

**Interfaces:**
- Consumes: regenerated `amsvc` `ConfigResponse.ObserverBaseUrl`; observer endpoints from Task 4.
- Produces: `observersvc.Client` methods:
  - `GetRuntimeLogs(ctx context.Context, params RuntimeLogsParams) (*LogsResponse, error)` → `GET /api/v1/logs`
  - `GetBuildLogs(ctx context.Context, params BuildLogsParams) (*LogsResponse, error)` → `GET /api/v1/build-logs`
  - `GetMetrics(ctx context.Context, params MetricsParams) (*MetricsResponse, error)` → `GET /api/v1/metrics`
  - `RuntimeLogsParams{Organization, Project, Agent, Environment, StartTime, EndTime, SearchPhrase, SortOrder string; LogLevels []string; Limit *int}`; `BuildLogsParams{Organization, BuildName string}`; `MetricsParams{Organization, Project, Agent, Environment, StartTime, EndTime string}`
  - `LogsResponse{Logs []LogEntry; TotalCount int32; TookMs float32}` / `LogEntry{Timestamp time.Time; Log string; LogLevel string}` / `MetricsResponse` mirroring Task 3 wire shapes.
  - `Factory.Observer func(ctx) (*observersvc.Client, error)` (discovery via `/api/v1/config` + bearer editor — existing pattern, `factory.go:87-132`).

**Steps:**

- [ ] **Step 1: regen amsvc client.** From repo root: `make amctl-gen-client`. `cd cli && go build ./...` — expect failures in `factory.go` (`TraceObserverBaseUrl` gone) and `logs.go`/`metrics.go`/`build/logs.go` (`FilterAgentRuntimeLogsWithResponse`/`GetAgentMetricsWithResponse`/`GetBuildLogsWithResponse` gone). These are the work list.

- [ ] **Step 2: rename client package** `git mv cli/pkg/clients/traceobssvc cli/pkg/clients/observersvc`; package `observersvc`; update the ~71 references (traces commands, create command, cmdutil). Error prefixes `"traces-observer: ..."` → `"observer: ..."`, `"traceobssvc: baseURL is required"` → `"observersvc: baseURL is required"`. Factory: fields `traceObsOnce/traceObsURL/traceObsErr` → `observerOnce/observerURL/observerErr`; `discoverTraceObserverURL` → `discoverObserverURL` reading `resp.JSON200.ObserverBaseUrl`; empty → `clierr.New(clierr.ServerInvalid, "server returned empty observerBaseUrl")`. Comment in `traces/env.go:29` → "observer".

- [ ] **Step 3: failing client tests** in `observersvc/client_test.go` (httptest): `GetRuntimeLogs` sends all set query params (`organization,project,agent,environment,startTime,endTime,searchPhrase,logLevels` comma-joined, `limit`, `sortOrder`) and decodes `LogsResponse`; `GetBuildLogs` sends exactly `organization,buildName`; `GetMetrics` sends its six params; non-200 → `HTTPError`.

- [ ] **Step 4: implement the three methods + types** following `ListTraces` (`client.go:142-150`) — build `url.Values`, call `c.do(ctx, path, values, &out)`.

- [ ] **Step 5: rewire commands.** For each of `agent/logs.go`, `agent/metrics.go`, `agent/build/logs.go`: add `Observer func(ctx) (*observersvc.Client, error)` to the command's options (wired `Observer: f.Observer`, keep `Client: f.AgentManager` for agent/build lookups). `logs.go`: keep `cmdutil.ValidateRuntimeManaged` guard and env resolution as-is, then call `observer.GetRuntimeLogs` with org/project/agent/environment + the same time-range/level/search flags currently packed into `amsvc.LogFilterRequest`; keep output rendering unchanged (adapt to `observersvc.LogsResponse`). `metrics.go`: same for `GetMetrics` (keep the existing default time window logic). `build/logs.go`: keep `GetAgentBuildsWithResponse` latest-build resolution via amsvc, then `observer.GetBuildLogs({Organization: org, BuildName: buildName})`. Error mapping via `cmdutil.ObserverErrorFromResponse` where the traces commands use it.

- [ ] **Step 6: update command tests** (`logs_test.go`, `metrics_test.go`, `build` tests) using the traces command tests as the template (fake observer client over httptest / stub factory).

- [ ] **Step 7: verify** `cd cli && go build ./... && go test ./...`.

- [ ] **Step 8: commit** ("Rewire amctl logs/metrics/build-logs to the observer")

---

### Task 9: Console — runtime discovery bootstrap, remove OBS_API_BASE_URL, traces gate

**Files:**
- Create: `console/workspaces/libs/api-client/src/apis/runtime-config.ts`, `console/workspaces/libs/api-client/src/hooks/runtime-config.ts`
- Modify: `console/workspaces/libs/api-client/src/utils/utils.ts` (observer URL store + `httpGETObserver`), `apis/index.ts`, `hooks/index.ts` (barrel exports)
- Create: `console/workspaces/core-ui/src/Providers/GlobalProviders/RuntimeConfigProvider.tsx`
- Modify: `console/workspaces/core-ui/src/Providers/GlobalProviders/GlobalProviders.tsx`
- Modify: `console/workspaces/libs/types/src/config/index.ts` (delete `obsApiBaseUrl?` :24-26), `console/workspaces/libs/types/src/api/` (new `ConfigResponse` type)
- Modify: `console/workspaces/pages/traces/src/Traces.Component.tsx:289-303`
- Modify: `console/apps/web-ui/public/config.js:45`, `config.template.js:40`, `console/env.example:16` (delete `OBS_API_BASE_URL` / `obsApiBaseUrl` lines)

**Interfaces:**
- Consumes: `GET {apiBaseUrl}/api/v1/config` → `{ observerBaseUrl: string }` (Task 5), CORS-enabled, unauthenticated.
- Produces (used by Task 10): from `@agent-management-platform/api-client`: `setObserverBaseUrl(url: string | undefined): void`, `isObserverConfigured(): boolean`, `useRuntimeConfig()` (returns `useApiQuery<ConfigResponse>` result), and `httpGETObserver` now reading the module store. `RuntimeConfigProvider` gates children on the fetch.

**Steps:**

- [ ] **Step 1: api + hook (two-file pattern).** `apis/runtime-config.ts`:

```ts
import { httpGET, SERVICE_BASE } from "../utils";
import type { ConfigResponse } from "@agent-management-platform/types";

/** Unauthenticated discovery endpoint served by agent-manager. */
export async function getRuntimeConfig(): Promise<ConfigResponse> {
  const res = await httpGET(`${SERVICE_BASE}/config`, {});
  if (!res.ok) throw await res.json();
  return res.json();
}
```

`hooks/runtime-config.ts`:

```ts
import { useApiQuery } from "./react-query-notifications";
import { getRuntimeConfig } from "../apis/runtime-config";
import type { ConfigResponse } from "@agent-management-platform/types";

export function useRuntimeConfig() {
  return useApiQuery<ConfigResponse>({
    queryKey: ["runtime-config"],
    queryFn: () => getRuntimeConfig(),
    staleTime: Infinity,
    retry: 1,
  });
}
```

Type in `libs/types/src/api/` (new file `config.ts`, exported from the api barrel): `export interface ConfigResponse { observerBaseUrl: string }`.

- [ ] **Step 2: observer URL store in `utils/utils.ts`.** Replace the `globalConfig.obsApiBaseUrl` logic inside `httpGETObserver` (utils.ts:102-129):

```ts
let observerBaseUrl: string | undefined;

/** Set by the runtime-config bootstrap once GET /api/v1/config resolves. */
export function setObserverBaseUrl(url: string | undefined): void {
  observerBaseUrl = url?.trim() || undefined;
}

export function isObserverConfigured(): boolean {
  return !!observerBaseUrl;
}
```

and in `httpGETObserver`: `const obsUrl = observerBaseUrl; if (!obsUrl) { throw new Error('Observer is not configured. Set AM_OBSERVER_PUBLIC_URL on the agent-manager service.'); }`. Remove the `$OBS_API_BASE_URL` sentinel check. Export the two new functions from the utils barrel (`utils/index.ts`) and package barrel.

- [ ] **Step 3: provider.** `RuntimeConfigProvider.tsx` (placed **inside** `ClientProvider` in `GlobalProviders.tsx:47-63` — it needs the QueryClient):

```tsx
import { useEffect } from "react";
import { useRuntimeConfig, setObserverBaseUrl } from "@agent-management-platform/api-client";
import { FullPageLoader } from "@agent-management-platform/views";

export function RuntimeConfigProvider({ children }: { children: React.ReactNode }) {
  const { data, isLoading, isError } = useRuntimeConfig();

  useEffect(() => {
    setObserverBaseUrl(data?.observerBaseUrl);
  }, [data?.observerBaseUrl]);

  // Gate rendering until discovery settles so observability pages never
  // render against a transiently-empty observer URL. On error we proceed
  // unconfigured — pages show the "observer not configured" state.
  if (isLoading) return <FullPageLoader />;
  return <>{children}</>;
}
```

(Verify the actual `FullPageLoader` export path/name in `libs/views` and the correct import alias before writing; `isError` is unused — drop it if lint complains.)

- [ ] **Step 4: traces gate.** In `Traces.Component.tsx:289-303` replace the `obsUrlMissing` computation with `const obsUrlMissing = !isObserverConfigured();` (import from api-client) and change the alert copy to: `<strong>Observer not configured.</strong> Ask your platform administrator to set <code>AM_OBSERVER_PUBLIC_URL</code> on the agent-manager service.`

- [ ] **Step 5: delete static config.** Remove `obsApiBaseUrl` from `config.js`, `config.template.js`, `env.example`, and the `AppConfig` type (`libs/types/src/config/index.ts:24-26`). Grep the console workspace for leftovers: `grep -rn 'obsApiBaseUrl\|OBS_API_BASE_URL' console/` → expect no hits.

- [ ] **Step 6: verify.** `rushx lint && rushx build` in `libs/types`, `libs/api-client`, `core-ui`, `pages/traces`; `rushx test` in `pages/traces`. From `console/`: `make build`.

- [ ] **Step 7: commit** ("Console discovers the observer via /api/v1/config")

---

### Task 10: Console — rewrite logs/metrics/build-logs to observer GET calls

**Files:**
- Modify: `console/workspaces/libs/api-client/src/apis/runtime-logs.ts`, `apis/metrics.ts`, `apis/builds.ts:125-155`
- Modify: `console/workspaces/libs/api-client/src/hooks/runtime-logs.ts`, `hooks/metrics.ts` (internal call changes; keep exported signatures)
- Verify-only: `pages/logs/src/Logs.Component.tsx`, `pages/metrics/src/Metrics.Component.tsx`, `libs/shared-component/src/components/BuildLogs.tsx` (call sites — signatures preserved, no change expected)

**Interfaces:**
- Consumes: Task 9's `httpGETObserver`; observer endpoints (Task 4). Response types `LogsResponse`/`MetricsResponse`/`BuildLogEntry` in `libs/types` are **unchanged** (wire parity by design).
- Produces: `getAgentRuntimeLogs(params: ObserverRuntimeLogsParams, getToken?)`, `getAgentMetrics(params: ObserverMetricsParams, getToken?)`, `getBuildLogs(params: { orgName?: string; buildName?: string }, getToken?)` — hooks `useAgentRuntimeLogs`/`useGetAgentMetrics`/`useGetBuildLogs` keep their current exported signatures.

**Steps:**

- [ ] **Step 1: rewrite `apis/runtime-logs.ts`** following `apis/traces.ts` (its `getTraceList` is the template):

```ts
import { httpGETObserver } from "../utils";
import type { LogsResponse } from "@agent-management-platform/types";

export interface ObserverRuntimeLogsParams {
  organization: string;
  project: string;
  agent: string;
  environment: string;
  startTime: string;
  endTime: string;
  searchPhrase?: string;
  logLevels?: string[];
  limit?: number;
  sortOrder?: "asc" | "desc";
}

function assertRequired(value: string, field: string): void {
  if (!value?.trim()) throw new Error(`Missing required parameters: ${field}`);
}

export async function getAgentRuntimeLogs(
  params: ObserverRuntimeLogsParams,
  getToken?: () => Promise<string>,
): Promise<LogsResponse> {
  const { organization, project, agent, environment, startTime, endTime } = params;
  assertRequired(organization, "organization");
  assertRequired(project, "project");
  assertRequired(agent, "agent");
  assertRequired(environment, "environment");
  assertRequired(startTime, "startTime");
  assertRequired(endTime, "endTime");

  const token = getToken ? await getToken() : undefined;
  const searchParams: Record<string, string> = {
    organization, project, agent, environment, startTime, endTime,
  };
  if (params.searchPhrase) searchParams.searchPhrase = params.searchPhrase;
  if (params.logLevels?.length) searchParams.logLevels = params.logLevels.join(",");
  if (params.limit !== undefined) searchParams.limit = params.limit.toString();
  if (params.sortOrder) searchParams.sortOrder = params.sortOrder;

  const res = await httpGETObserver("/api/v1/logs", { searchParams, token });
  return res.json();
}
```

Keep a deprecated-name re-export only if other files import `filterAgentRuntimeLogs` (grep; if only the hook imports it, rename cleanly).

- [ ] **Step 2: adapt `hooks/runtime-logs.ts`.** `useAgentRuntimeLogs(params, body, options)` keeps its signature; internally map `{orgName, projName, agentName}` + `body.environmentName` + computed `getTimeRange` window into `ObserverRuntimeLogsParams` for the queryFn and the `loadOlder`/`loadNewer` refetches (same time-window adjustments as today, just applied to `startTime`/`endTime` params instead of body fields).

- [ ] **Step 3: rewrite `apis/metrics.ts`** the same way (`getAgentMetrics(params: ObserverMetricsParams, getToken?)` with `{organization, project, agent, environment, startTime, endTime}`; keep the "default endTime=now / startTime=now-10s" behavior in the hook or api as today). Adapt `hooks/metrics.ts:31-60` internals; exported signature unchanged.

- [ ] **Step 4: rewrite `getBuildLogs` in `apis/builds.ts:125-155`:** swap `httpGET(...path...)` for `httpGETObserver("/api/v1/build-logs", { searchParams: { organization: orgName, buildName }, token })`; keep the `.logs ?? []` unwrap and `BuildLogEntry[]` return. `agentName`/`projName` are no longer needed for the request — keep them in `GetBuildLogsPathParams` for the hook's queryKey/enabled logic (hook unchanged).

- [ ] **Step 5: verify call sites compile untouched** (`Logs.Component.tsx:144`, `Metrics.Component.tsx:79`, `BuildLogs.tsx:78`). If a signature had to shift, update the call site in the same commit.

- [ ] **Step 6: verify.** `rushx lint && rushx build` in `libs/api-client`, `pages/logs`, `pages/metrics`, `libs/shared-component`; `rushx test` in `pages/logs`, `pages/metrics`. From `console/`: `make build`.

- [ ] **Step 7: commit** ("Console logs/metrics/build-logs call the observer directly")

---

### Task 11: Helm charts, docker-compose, installers, scripts

**Files:**
- Modify: `deployments/helm-charts/wso2-agent-manager/templates/agent-manager-service/configmap.yaml:9-14`, `templates/console/configmap.yaml:23-24`, `values.yaml:140,142,146,380`
- Modify: `deployments/helm-charts/wso2-amp-observability-extension/values.yaml` (whole `tracesObserver:` block → `amObserver:`; `name: amp-observer`; `image.repository: ghcr.io/wso2/amp-observer`; add `"amp-api-client"` to `auth.audience` — see Step 3), `templates/deployment.yaml`, `templates/service.yaml`, `templates/httproute.yaml`, `templates/secret.yaml`
- Modify: `deployments/helm-charts/wso2-amp-evaluation-extension/values.yaml:40` (host only)
- Modify: `deployments/docker-compose.yml:57-61,138`, `deployments/quick-start/install-helpers.sh:226-232`, `deployments/vm/lib-vm.sh:48,130-131`, `deployments/vm/tests/run.sh:69-71,120-124`, `deployments/setup/setup-openchoreo.sh:490-505`, `deployments/setup/port-forward.sh:74`

**Interfaces:**
- Consumes: env names from Tasks 1/5 (`AM_OBSERVER_PORT`, `OPENCHOREO_OBSERVER_URL`, `AM_OBSERVER_URL`, `AM_OBSERVER_PUBLIC_URL`).
- Produces: chart value paths `agentManagerService.config.amObserverURL` / `.amObserverPublicURL`, `amObserver.*` — consumed by Task 12 (CI) and Task 13 (docs).

**Steps:**

- [ ] **Step 1: wso2-agent-manager chart.** `configmap.yaml:9-14` becomes:

```yaml
  AM_OBSERVER_URL: {{ .Values.agentManagerService.config.amObserverURL | quote }}
  # Externally reachable observer URL handed to out-of-cluster clients
  # (console bootstrap, CLI) via /api/v1/config. No internal fallback:
  # unset means clients surface "observer not configured".
  AM_OBSERVER_PUBLIC_URL: {{ .Values.agentManagerService.config.amObserverPublicURL | default "" | quote }}
```

(the `OBSERVER_URL` line is deleted). `values.yaml`: delete `observerURL:` (:140); `traceObserverURL` → `amObserverURL: "http://amp-observer.<same-ns>.svc.cluster.local:9098"` (:142, keep existing namespace form, host renamed); `traceObserverPublicURL` → `amObserverPublicURL: ""` (:146); delete `console.config.obsApiBaseUrl` (:380). `templates/console/configmap.yaml`: delete lines 23-24 (`OBS_API_BASE_URL` + comment).

- [ ] **Step 2: observability-extension chart.** values: `tracesObserver:` → `amObserver:`, `name: amp-observer`, `image.repository: ghcr.io/wso2/amp-observer`. Templates: every `.Values.tracesObserver` → `.Values.amObserver` (deployment ~24 refs, service 5, httproute 5, secret 2); labels/selectors `app: traces-observer` → `app: amp-observer` and `app.kubernetes.io/name: traces-observer` → `amp-observer`, **plus add** `app.kubernetes.io/component: observer` to the pod template labels (install-helpers selector target); container name `observer`; dev image hardcode `amp-traces-observer:0.0.0-dev` → `amp-observer:0.0.0-dev` (deployment.yaml:38); env names `TRACES_OBSERVER_PORT` → `AM_OBSERVER_PORT` (:47), `OBSERVER_BASE_URL` → `OPENCHOREO_OBSERVER_URL` (:49). ⚠ Deployment **selector.matchLabels are immutable** on upgrade — acceptable per spec (deployments recreated, no upgrade path).

- [ ] **Step 3: observer audience allowlist for the monitor s2s token.** The monitor-run-logs call authenticates with the `amp-api-client` client-credentials token. Determine the audience Thunder stamps on it: check `deployments/helm-charts/wso2-amp-thunder-extension/values.yaml` (global `jwt.audience: "application"` at ~:124-128) vs the agent-manager chart's own allowlist (`wso2-agent-manager/values.yaml:192` lists `amp-api-client` as an audience value, implying per-client aud stamping). Add the verified value(s) to `wso2-amp-observability-extension/values.yaml:46` `auth.audience` (e.g. `"amp,amp-api-client"`). Record the finding in the commit message. This must be smoke-tested in Step 7 of the verification task — a 401 on monitor-run logs means the wrong value.

- [ ] **Step 4: evaluation extension.** `values.yaml:40`: `tracesApiEndpoint: "http://amp-observer...:9098"` (key name unchanged).

- [ ] **Step 5: compose + scripts.** `docker-compose.yml`: delete `OBSERVER_URL` (:57); `TRACE_OBSERVER_URL` → `AM_OBSERVER_URL` (:58); `TRACE_OBSERVER_PUBLIC_URL` → `AM_OBSERVER_PUBLIC_URL` (:61); delete console `OBS_API_BASE_URL` (:138); update comments. `install-helpers.sh:226-232`: deployment `amp-observer`, selector `-l app.kubernetes.io/component=observer`. `lib-vm.sh:48`: `--set console.config.obsApiBaseUrl=...` → `--set agentManagerService.config.amObserverPublicURL=https://${AMP_HOST_OBSERVER}`; `:130-131` `tracesObserver.*` → `amObserver.*`. `vm/tests/run.sh`: update the assertions (:69-71 now assert `amObserverPublicURL`; :120-124 `amObserver.*`). `setup-openchoreo.sh`: `-C .../agent-manager-observer docker-load-k3d` (:495), `--set amObserver.developmentMode=true` etc. (:503-505). `port-forward.sh:74`: `svc/amp-observer 9098:9098`.

- [ ] **Step 6: verify.** `helm template deployments/helm-charts/wso2-agent-manager >/dev/null && helm template deployments/helm-charts/wso2-amp-observability-extension >/dev/null && helm template deployments/helm-charts/wso2-amp-evaluation-extension >/dev/null` (helm lint too). `docker compose -f deployments/docker-compose.yml config >/dev/null`. `bash -n` on each touched script. Grep: `grep -rn 'tracesObserver\|TRACE_OBSERVER\|OBS_API_BASE_URL\|obsApiBaseUrl\|amp-traces-observer' deployments/` → no hits.

- [ ] **Step 7: commit** ("Rename observer across Helm charts, compose and installers")

---

### Task 12: CI workflows, e2e suite, instrumentation-matrix

**Files:**
- Rename: `.github/workflows/traces-observer-service-pr-checks.yaml` → `.github/workflows/agent-manager-observer-pr-checks.yaml` (paths :7-8, working dirs :28,43,54,98, artifact :62, `TRACES_OBSERVER_PORT` :49 → `AM_OBSERVER_PORT`, docker tag :82)
- Modify: `.github/release-config.json:14-17` (`amp-observer` / `agent-manager-observer`), `nightly.yml:48,320`, `e2e.yaml:85`, `release.yml:509`, `instrumentation-matrix-pr.yaml:13,28`, `.github/actions/amp-dev-stack/action.yaml` (comments + any `obsApiBaseUrl`/`tracesObserver` set-flags — grep it)
- Modify: `test/e2e/framework/config.go:27,44` (`TracesBaseURL` → `ObserverBaseURL`, env `AM_OBSERVER_BASE_URL`), `test/e2e/.env.example:7`, `test/e2e/framework/{client.go:116,types.go:939}` (comments)
- Modify: `test/e2e/operations/trace/{list_traces.go:58,export_traces.go:55,get_trace_spans.go:34}` (field rename only), `test/e2e/operations/agent/{metrics.go,runtime_logs.go}`, `test/e2e/operations/build/build_operations.go:108` (repoint to observer endpoints)
- Modify: `test/instrumentation-matrix/heavy/driver.py:5,58,62,138`, `heavy/observer.py:3,11`, `RUNBOOK.md:226,257,276,315,327`, `DESIGN.md` (~14 refs), `FINDINGS.md:71`

**Interfaces:**
- Consumes: observer endpoints (Task 4), installer values (Task 11 — the amctl e2e ops need `AM_OBSERVER_PUBLIC_URL` set or they fail `ServerInvalid`).
- Produces: green CI on renamed paths (merge gate for PR 1).

**Steps:**

- [ ] **Step 1: CI files** per the list. In `nightly.yml:48` the cleanup-images matrix entry becomes `- amp-observer`; `:320` and `e2e.yaml:85` and `release.yml:509` → `kubectl logs deployment/amp-observer ... amp-observer.txt`. `grep -rn 'traces-observer\|amp-traces-observer\|obsApiBaseUrl\|tracesObserver' .github/` → no hits afterwards.

- [ ] **Step 2: e2e framework + ops.** `config.go`: field `ObserverBaseURL`, `envOrDefault("AM_OBSERVER_BASE_URL", "http://traces.amp.localhost:11080")` (hostname stays as deployed). Repoint the agent ops: `metrics.go` → `GET {ObserverBaseURL}/api/v1/metrics?organization&project&agent&environment&startTime&endTime`; `runtime_logs.go` → `GET {ObserverBaseURL}/api/v1/logs?...`; `build_operations.go:108` → `GET {ObserverBaseURL}/api/v1/build-logs?organization&buildName` — keep each op's existing assertion structure, changing only request construction and decoding (`LogsResponse`/`MetricsResponse` JSON shapes are unchanged). CLI-driven ops (`operations/cli/agent/*.go`) need no code change.

- [ ] **Step 3: instrumentation-matrix.** Env rename `TRACES_OBSERVER_BASE_URL` → `AM_OBSERVER_BASE_URL` in `driver.py` (default + `_env`), prose/comments in `observer.py`, RUNBOOK, DESIGN, FINDINGS.

- [ ] **Step 4: verify.** `cd test/e2e && go build ./...` (and `go vet ./...`). `python3 -m py_compile test/instrumentation-matrix/heavy/driver.py test/instrumentation-matrix/heavy/observer.py`. `grep -rn 'TRACES_OBSERVER' test/ .github/` → no hits.

- [ ] **Step 5: commit** ("Repoint CI, e2e and instrumentation-matrix at agent-manager-observer")

---

### Task 13: Docs (documentation/docs only) + AGENTS.md

**Files:**
- Modify: `documentation/docs/reference/mcp-server.mdx` (19 → 16 tools: drop `get_runtime_logs`, `get_metrics` from the observability list :57-62 — heading "(6 tools)" → "(4 tools)" — and `get_build_logs` from builds; fix the total at :13)
- Modify: `documentation/docs/getting-started/_partials/_amp-installation.mdx:99,200,204` (`console.config.obsApiBaseUrl` → `agentManagerService.config.amObserverPublicURL`; `tracesObserver.*` → `amObserver.*`; `deployment/amp-traces-observer` → `amp-observer`)
- Modify: `documentation/docs/getting-started/on-k3d.mdx:245,249`, `on-your-environment.mdx:110,116,992,1057`, `on-a-vm.mdx` (prose referencing "Traces Observer" if any) — the `OBS_API_PUBLIC_URL`/`OBS_API_PUBLIC_HOST` shell-var names may stay; what they're applied to changes per _amp-installation. **Do NOT touch** `observerURL:` at `on-k3d.mdx:601` / `on-your-environment.mdx:855` (OpenChoreo config)
- Modify: `documentation/docs/reference/cli/agent.mdx:8,26-29` (wording: logs/metrics/build-logs now served by the observer, discovered automatically)
- Modify: root `AGENTS.md:15,43,50`, `evaluation-job/AGENTS.md:26,54`, `evaluation-job/main.py:34,215` (example URLs only — flag name kept)

**Steps:**

- [ ] **Step 1: apply edits** per Files list.
- [ ] **Step 2: verify.** `grep -rn 'traces-observer\|tracesObserver\|amp-traces-observer\|obsApiBaseUrl\|OBS_API_BASE_URL' documentation/docs/ AGENTS.md evaluation-job/` → only permitted survivors: none (the `--traces-api-endpoint` flag and `tracesApiEndpoint` key contain `traces-api`, not `traces-observer`). If the docs site has a build (`cd documentation && npm run build` or similar per its README), run it.
- [ ] **Step 3: commit** ("Update docs for the observer rename and consolidation")

---

### Task 14: PR 1 verification sweep + draft PR

**Steps:**

- [ ] **Step 1: whole-repo leftover grep.** Excluding `documentation/versioned_docs` and `docs/superpowers`, expect ZERO hits for: `traces-observer`, `tracesObserver`, `TraceObserver`, `traceobssvc`, `traceobserversvc`, `TRACE_OBSERVER`, `TRACES_OBSERVER`, `OBS_API_BASE_URL`, `obsApiBaseUrl`, `traceObserverBaseUrl`, `amp-traces-observer`, `OBSERVER_BASE_URL` (bare, not `OPENCHOREO_` / `AM_`-prefixed), `OBSERVER_URL` (bare, not `AM_`-prefixed). Investigate and fix every hit.
- [ ] **Step 2: full builds.** `cd agent-manager-observer && make fmt && make lint && make test && make build`; `cd agent-manager-service && make lint && make test-unit && go build ./... && make codegenfmt-check`; `cd cli && go build ./... && go test ./...`; `cd test/e2e && go build ./...`; `cd console && make build`.
- [ ] **Step 3: docker-compose smoke** (if Docker available): bring up the stack per `deployments/docker-compose.yml` + observer `make run`; check `GET /api/v1/config` returns `observerBaseUrl`; amctl `agent logs/metrics/build logs/traces` against it; console pages render. If the environment can't run the stack, note exactly what was skipped in the PR description.
- [ ] **Step 4: push + draft PR** per am-ship (`gh pr create --draft`), PR body summarizing spec §PR 1, accepted breakage, and the audience-allowlist verification result from Task 11 Step 3.

---

# PR 2 — branch `am-observer-pr2` (stacked on `am-observer-pr1`)

### Task 15: Observer — MCP OAuth plumbing (config, well-known route, bearer challenge)

**Files:**
- Modify: `agent-manager-observer/config/config.go` (+3 fields/envs), `config/config_test.go`
- Create: `agent-manager-observer/handlers/well_known.go` (or `main.go`-adjacent file mirroring the current route style)
- Modify: `agent-manager-observer/middleware/auth.go` (401s gain `WWW-Authenticate`), `middleware/auth_test.go` if present (else new test), `main.go` (route + config pass-through)
- Modify: `deployments/helm-charts/wso2-amp-observability-extension/{values.yaml,templates/deployment.yaml}` (new envs), `deployments/docker-compose.yml` (if the observer runs there — it doesn't today; add env docs to `agent-manager-observer/.env.example` instead)

**Interfaces:**
- Consumes: `api/well_known_routes.go` and `middleware/jwtassertion/auth.go:119` in agent-manager-service (port sources).
- Produces: envs `SERVER_PUBLIC_URL`, `OAUTH_AUTHORIZATION_SERVERS` (comma list), `OAUTH_SCOPES_SUPPORTED` (comma list) on the observer; `GET /.well-known/oauth-protected-resource`; all JWT-middleware 401s carry `WWW-Authenticate: Bearer realm="agent-manager-observer", error="invalid_token", resource_metadata="<SERVER_PUBLIC_URL>/.well-known/oauth-protected-resource"`. Task 16 mounts `/mcp` behind this middleware.

**Steps:**

- [ ] **Step 1: config.** Add to `AuthConfig` (or a new `OAuthConfig`): `ServerPublicURL` (`SERVER_PUBLIC_URL`, default ""), `AuthorizationServers []string` (`OAUTH_AUTHORIZATION_SERVERS`, `getEnvAsList`), `ScopesSupported []string` (`OAUTH_SCOPES_SUPPORTED`). No validation failure when empty (the well-known route 503s instead — parity with agent-manager). Tests via `t.Setenv`.
- [ ] **Step 2: well-known route.** Port `registerWellKnownRoutes` from `agent-manager-service/api/well_known_routes.go` verbatim (adjust config accessors; realm/log messages say agent-manager-observer). Register on the root `mux` in `main.go` (unauthenticated, before the `/api/v1/` mount).
- [ ] **Step 3: bearer challenge.** Port `buildBearerChallenge` (realm `"agent-manager-observer"`) into `middleware/auth.go`; in `JWTAuth`, thread the resource-metadata URL (from config: `ServerPublicURL + "/.well-known/oauth-protected-resource"` when set, else "") and set the header on every 401 via `writeAuthError` call sites (missing header → no error code; invalid token → `error="invalid_token"`). Table-driven middleware test asserting header content (mind the package-level `jwksCache` globals — use `IsLocalDevEnv` mode tokens to avoid JWKS).
- [ ] **Step 4: deploy wiring.** Extension chart: `amObserver.publicUrl`, `amObserver.oauth.authorizationServers`, `amObserver.oauth.scopesSupported` values → env in `templates/deployment.yaml`; sensible defaults mirroring how the agent-manager chart sets `SERVER_PUBLIC_URL`/`OAUTH_AUTHORIZATION_SERVERS` (copy its values shape). `.env.example` entries.
- [ ] **Step 5: verify** `make fmt && make lint && make test && make build`; `helm template` the extension chart.
- [ ] **Step 6: commit** ("Add OAuth protected-resource metadata and bearer challenge to the observer")

---

### Task 16: Observer — am-obs-mcp server with seven tools

**Files:**
- Modify: `agent-manager-observer/go.mod` (add `github.com/modelcontextprotocol/go-sdk` at the exact version in `agent-manager-service/go.mod`)
- Create: `agent-manager-observer/mcp/setup.go`, `agent-manager-observer/mcp/tools/{tools.go,observability.go,traces.go}` (structure mirroring am-mcp but minimal), `agent-manager-observer/mcp/tools/{setup_test.go,registration_test.go,observability_specs_test.go,traces_specs_test.go}`
- Modify: `agent-manager-observer/main.go` (mount), `agent-manager-observer/docs/openapi.yaml` (note: MCP endpoint is not OpenAPI — skip), `README.MD`/`AGENTS.md` (mention `/mcp`)

**Interfaces:**
- Consumes: `controllers.TracingController` (GetTraceOverviews/GetTraceSpans/GetSpanDetail/ExportTraces + `TraceQueryParams`), `controllers.ObservabilityController` (Task 3), `middleware.JWTAuth` (Task 15-enhanced). am-mcp sources to port: `agent-manager-service/mcp/setup.go` (RegisterRoute + streamable HTTP server), `mcp/tools/observability.go` (tool defs, `resolveTraceTimeWindow`; the PR-1-deleted `get_runtime_logs`/`get_metrics`/`get_build_logs` defs are in git history of this branch — retrieve with `git show am-observer-pr1~N` or port from the spec below), `mcp/tools/registration_test.go` + `setup_test.go` (test harness).
- Produces: streamable-HTTP MCP server at `/mcp` and `/mcp/` behind JWT (publisher tokens deliberately NOT blocked here). Seven tools: `get_runtime_logs`, `get_build_logs`, `get_metrics`, `list_traces`, `get_traces`, `get_trace_details`, `get_span_details`.

**Steps:**

- [ ] **Step 1: dependency + skeleton.** `go get github.com/modelcontextprotocol/go-sdk@$(grep modelcontextprotocol ../agent-manager-service/go.mod | awk '{print $2}')`. Port `mcp/setup.go` from am-mcp: `RegisterRoute(mux *http.ServeMux, deps Dependencies, authMiddleware func(http.Handler) http.Handler)` where `Dependencies{Tracing *controllers.TracingController; Observability *controllers.ObservabilityController}`; server name `am-obs-mcp`. Mount in `main.go`: `mcp.RegisterRoute(mux, deps, middleware.JWTAuth(cfg.Auth))` for `/mcp` and `/mcp/` (root mux, NOT under `/api/v1/`).

- [ ] **Step 2: tool inputs.** All seven take `organization` as an explicit **required** parameter (auth deferred; no claims). Input structs (jsonschema tags per am-mcp conventions):

```go
type runtimeLogsInput struct {
	Organization string   `json:"organization" jsonschema:"required"`
	Project      string   `json:"project" jsonschema:"required"`
	Agent        string   `json:"agent" jsonschema:"required"`
	Environment  string   `json:"environment" jsonschema:"required"`
	StartTime    string   `json:"start_time,omitempty"`
	EndTime      string   `json:"end_time,omitempty"`
	Limit        *int     `json:"limit,omitempty"`
	SortOrder    string   `json:"sort_order,omitempty"`
	LogLevels    []string `json:"log_levels,omitempty"`
	SearchPhrase string   `json:"search_phrase,omitempty"`
}
type buildLogsInput struct {
	Organization string `json:"organization" jsonschema:"required"`
	BuildName    string `json:"build_name" jsonschema:"required"`
}
type metricsInput struct {
	Organization string `json:"organization" jsonschema:"required"`
	Project      string `json:"project" jsonschema:"required"`
	Agent        string `json:"agent" jsonschema:"required"`
	Environment  string `json:"environment" jsonschema:"required"`
	StartTime    string `json:"start_time,omitempty"`
	EndTime      string `json:"end_time,omitempty"`
}
type listTracesInput struct {
	Organization string `json:"organization" jsonschema:"required"`
	Project      string `json:"project" jsonschema:"required"`
	Agent        string `json:"agent" jsonschema:"required"`
	Environment  string `json:"environment" jsonschema:"required"`
	StartTime    string `json:"start_time,omitempty"`
	EndTime      string `json:"end_time,omitempty"`
	Limit        *int   `json:"limit,omitempty"`
	SortOrder    string `json:"sort_order,omitempty"`
}
// get_traces reuses listTracesInput; get_trace_details adds TraceID; get_span_details adds TraceID+SpanID:
type traceDetailsInput struct {
	Organization string `json:"organization" jsonschema:"required"`
	Project      string `json:"project" jsonschema:"required"`
	Agent        string `json:"agent" jsonschema:"required"`
	Environment  string `json:"environment" jsonschema:"required"`
	TraceID      string `json:"trace_id" jsonschema:"required"`
	StartTime    string `json:"start_time,omitempty"`
	EndTime      string `json:"end_time,omitempty"`
}
type spanDetailsInput struct {
	TraceID string `json:"trace_id" jsonschema:"required"`
	SpanID  string `json:"span_id" jsonschema:"required"`
}
```

Port the tool descriptions and the time-window defaulting from am-mcp git history (`resolveTimeWindow` — 14-day cap for logs/metrics; `resolveTraceTimeWindow` — 30-day cap for traces; `normalizeLogLevels`). Validation mirrors the REST handlers (Task 4): reuse the same limits/sort/level rules — call into shared helpers if convenient, else duplicate the small checks.

- [ ] **Step 3: tool handlers** call the controllers directly (no HTTP hop): `get_runtime_logs` → `obs.GetLogs`; `get_build_logs` → `obs.GetBuildLogs`; `get_metrics` → `obs.GetMetrics`; `list_traces` → `tracing.GetTraceOverviews`; `get_traces` → `tracing.ExportTraces`; `get_trace_details` → `tracing.GetTraceSpans`; `get_span_details` → `tracing.GetSpanDetail`. Times parse RFC3339 with defaults per the resolve helpers; errors return MCP tool errors (am-mcp pattern).

- [ ] **Step 4: tests.** Port am-mcp's in-memory registration harness (`setup_test.go:39` + `registration_test.go:25` + `allToolSpecs`): assert all seven tools registered with the expected required/optional params. Input-validation unit tests: missing organization → tool error; bad RFC3339 → tool error; limit bounds; log-level normalization.

- [ ] **Step 5: verify** `make fmt && make lint && make test && make build`.

- [ ] **Step 6: commit** ("Add am-obs-mcp server with seven observability tools")

---

### Task 17: am-mcp — delete observability toolset; observersvc drops trace methods; docs

**Files:**
- Delete: `agent-manager-service/mcp/tools/observability.go`, `mcp/tools/observability_specs_test.go`, `mcp/handlers/observability_handler.go`
- Modify: `mcp/setup.go` (drop `ObservabilityToolset` from `Toolsets` + Dependencies fields `TraceObserverSvcClient`/`OpenChoreoClient` if now unused), `mcp/tools/types.go` (delete `ObservabilityToolsetHandler`), `mcp/tools/setup_test.go:114` (drop `observabilityToolSpecs()`), `mcp/tools/mock_test.go` (delete trace-tool mocks :180-210), `api/app.go:49-55` (Dependencies literal)
- Modify: `agent-manager-service/clients/observersvc/{client.go,types.go}` (delete `ListTraces`/`ExportTraces`/`GetTrace`/`GetSpan` + their param/response types + `doGetMap` if orphaned; interface keeps only `GetWorkflowRunLogs`), regen mock (`make codegen`), `wiring/*` (provider args if `ocClient`/params changed)
- Modify: `documentation/docs/reference/mcp-server.mdx` (16 → 12 tools, remove the observability toolset section)
- Create: `documentation/docs/reference/observer-mcp-server.mdx` (new page: what am-obs-mcp is, endpoint `<observer-url>/mcp`, auth = same bearer token, the seven tools with param tables, note that `organization` is explicit) + sidebar registration (find the docusaurus sidebar config and add it next to mcp-server)

**Interfaces:**
- Consumes: Task 16 (tools reborn in am-obs-mcp before am-mcp loses them).
- Produces: am-mcp = 12 tools / four toolsets; `observersvc.ObserverSvcClient` = `GetWorkflowRunLogs` only.

**Steps:**

- [ ] **Step 1: delete am-mcp observability** per Files list, compiler-driven; `TestToolRegistration` passes with 12 tools.
- [ ] **Step 2: slim observersvc.** Delete the four trace methods + types; if `mcp/` was the only consumer of anything, remove it; `make codegen` regenerates the mock; monitor tests still green.
- [ ] **Step 3: docs.** Update `mcp-server.mdx`; write `observer-mcp-server.mdx`; register in the sidebar; build docs if a build exists.
- [ ] **Step 4: verify** `make fmt && make lint-fix && make test-unit && go build ./... && make codegenfmt-check` in agent-manager-service.
- [ ] **Step 5: commit** ("Move observability MCP tools out of am-mcp; document am-obs-mcp")

---

### Task 18: PR 2 verification + draft PR

**Steps:**

- [ ] **Step 1: builds** — observer + agent-manager-service full gates (as Task 14 Step 2, minus console/CLI which PR 2 doesn't touch).
- [ ] **Step 2: grep sanity** — `grep -rn 'ObservabilityToolset\|observability_handler\|ListTraces' agent-manager-service/mcp agent-manager-service/clients/observersvc` → only expected survivors.
- [ ] **Step 3: smoke (if runnable)** — observer up locally, MCP initialize + tools/list over `/mcp` with a dev token (7 tools), one `list_traces` call.
- [ ] **Step 4: push `am-observer-pr2`, draft PR** with base `am-observer-pr1`, body noting the deliberate publisher-token/MCP asymmetry and the deferred-auth section.

---

## Self-review notes (spec-coverage checks already applied)

- Spec "Naming" table → Tasks 1, 5, 6, 8, 11, 12, 13. CI/tooling table → Tasks 1 (Makefile/drift), 12 (workflows).
- Spec "Observer: new endpoints" → Tasks 2-4 (incl. publisher 403 REST-only, openapi update, no existence-validation, empty-200 semantics).
- Spec "Agent-manager" section → Tasks 5 (config/CORS), 6 (monitor-run logs + s2s audience verification in Task 11 Step 3), 7 (deletions incl. 3 MCP tools + collateral).
- Spec "Console" → Tasks 9 (bootstrap/gating/traces message) + 10 (rewrites; call-sites verified).
- Spec "CLI" → Task 8. "Deployments, CI, e2e" → Tasks 11-12 (installer values explicitly in 11 Steps 1/5; audience check 11/3). Docs → Task 13.
- Spec "PR 2" → Tasks 15 (OAuth plumbing + new config + helm/compose), 16 (seven tools, org explicit, publisher NOT blocked), 17 (am-mcp deletions, observersvc slimming, docs 16→12).
- Known judgment calls encoded: hook signatures preserved in console Task 10 (call-sites then need no change — spec anticipated changes; preserving signatures is strictly less churn with identical behavior); monitor s2s call passes `ouID` as `organization` (observer's `NamespaceFor` discards it — same namespace as today); `metrics` REST has no 14-day cap (parity with today's behavior).
- Console testing reality: the spec asks for "hook/API tests for the rewritten files; bootstrap discovery test" — `libs/api-client` has NO test infrastructure (no test script, zero test files) and pages only have shallow render tests. Scope accordingly: Task 9 adds a component-level test for the gating behavior ONLY if `core-ui` already has a vitest setup (check `core-ui/package.json`); otherwise rely on the pages' existing render tests + `make build`, and say so in the PR description rather than bolting a new test harness onto this PR.
