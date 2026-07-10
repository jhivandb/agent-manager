# Technical-Debt Audit: `agent-manager-service`

**Date:** 2026-07-11 (audited at commit `949344d9`)

Baseline: the repo has an unusually good `AGENTS.md` that states the intended architecture (controller → service → repository via interfaces, spec-first codegen, per-route authz, ctx-first I/O). Most findings below are measured against the project's *own* stated rules. Positives worth noting up front: logging is uniformly `log/slog` (no logrus/zap mix), `go vet` is clean on the main packages, error wrapping is disciplined (1278 `%w` wraps, zero `err == gorm.ErrRecordNotFound` comparisons, zero lossy `fmt.Errorf` wraps), TODO/FIXME density is very low (8 total), and `go.mod` is small and current. The debt is concentrated in layering, transaction handling, and test coverage of the security-critical paths.

---

## 1. Controllers inject repositories directly, bypassing the service layer — HIGH

Correctness/velocity: business logic in HTTP handlers can't be unit-tested at the service tier and dodges the org-scoping conventions services enforce; directly contradicts AGENTS.md layering.

- `controllers/gateway_internal_controller.go:51-52` — `apiKeyRepo repositories.APIKeyRepository`, `aiApplicationRepo repositories.AIApplicationRepository` as controller fields.
- `controllers/agent_identity_controller.go:66-67` — `scopeRepo`, `bindingRepo` injected into the controller.
- `controllers/llm_controller.go:70` — `artifactRepo repositories.ArtifactRepository`.
- Weaker variant: `controllers/monitor_scores_controller.go:114` builds `repositories.ScoreFilters` in the handler (repo type leaking through the service seam).

**Remediation (M):** push repo access down into the owning services; controllers keep only service interfaces. Mechanical but touches wiring (`wiring/wire.go`) and mocks.

## 2. Three competing transaction patterns, with services owning `*gorm.DB` — HIGH

Correctness risk: reads happen outside the transaction that later writes (admitted TOCTOU), and rollback behavior differs per pattern.

- Pattern A — repos own transactions internally: `repositories/gateway_repository.go:223`, `repositories/llm_provider_repository.go:193`, `repositories/mcp_proxy_repository.go:200`, etc.
- Pattern B — services open `db.Transaction` and pass `tx` into repos: `services/mcp_proxy_service.go:160,378,555`, `services/agent_configuration_service.go:2870,2885`, `services/monitor_manager.go:559`.
- Pattern C — manual `Begin/Commit/Rollback` with hand-rolled panic recovery: `services/llm_provider_service.go:937-987` (includes `panic(r)` re-panic at line 949), `services/agent_thunder_reconciler.go:107-135`, `services/monitor_scheduler.go:114-141` (this one uses `defer tx.Rollback()`, the other doesn't — inconsistent even within pattern C).
- Smoking gun at `services/llm_provider_service.go:957`: *"We use the non-transactional repo here since GetByUUID doesn't support tx parameter. This is acceptable as the critical update happens within the transaction"* — the existence check runs outside the tx that performs the update.
- 9 services hold `*gorm.DB` or call `db.GetDB()` directly (`agent_manager.go`, `agent_configuration_service.go`, `mcp_proxy_service.go`, `llm_provider_service.go`, `monitor_manager.go`, `agent_api_artifact.go`, `agent_thunder_reconciler.go`, `monitor_scheduler.go`, `agent_thunder_provisioning_service.go`), which is why unit-testing them requires the "test the branches before the boundary" workaround AGENTS.md documents.

**Remediation (M-L):** standardize on one pattern — repos expose tx-accepting methods, services compose them under a single `db.Transaction` helper (or a UnitOfWork seam so services stop holding `*gorm.DB`). Do it service-by-service, starting with `llm_provider_service.go`.

## 3. The authz enforcement middleware has zero tests — HIGH

Security/correctness: 221 routes rely on `RequirePermission`/`RequireOrgMatch` composition; a regression there is a tenant-isolation or privilege-escalation bug.

- `middleware/authorization.go` (191 lines) and `middleware/orgresolver.go` (98 lines): no test files exist in `middleware/` at all; the only auth test is `middleware/jwtassertion/auth_test.go`. Grep for `RequirePermission|RequireOrgMatch` across all test files: 0 hits.
- The route-registration compositor `middleware/path_params.go:45-107` (5 registrar variants, incl. `HandleFuncWithValidationAndAuthzAllowRootOU` with root-OU bypass semantics at lines 68-83) is likewise untested.

**Remediation (S):** table-driven unit tests with fake tokens covering allow/deny/org-mismatch/root-OU-bypass per registrar variant. A day or two of work protecting the highest-stakes code in the repo.

## 4. God-functions and god-services in the two core services — HIGH

Velocity/onboarding: the two files everyone must touch are 5.3k and 4.5k lines with multi-phase functions mixing DB tx, remote API calls, and best-effort cleanup.

- `services/agent_configuration_service.go` — 5342 lines. `Update()` at line 2800 is **482 lines** with named phases ("Phase 1", "Phase 1b"…), inline tx blocks, OpenChoreo CR mutation, and best-effort cleanup where failures are logged and swallowed (lines 2952, 2956, 2980). Also `createLLMConfig` 318 lines (line 882), `updateMCPConfig` 286 lines (line 2114), `createMCPConfig` 251 lines (line 1202), `deleteLLMConfig` 240 lines (line 3323).
- `services/agent_manager.go` — 4479 lines. `DeployAgent` **330 lines** (line 2370), `createComponentAgent` 290 lines (line 1034), `PromoteAgent` 263 lines (line 3221).
- `services/monitor_manager.go` — `UpdateMonitor` 271 lines (line 422), `CreateMonitor` 231 lines (line 107).

**Remediation (L):** don't big-bang refactor; adopt a rule that any new phase becomes a named private method with its own unit test, and extract the existing "Phase 1b" style blocks opportunistically when touched. Splitting `agent_configuration_service.go` by config type (LLM/MCP) is a natural seam.

## 5. Generated code fixed by hand instead of regenerated; no drift check in CI — MEDIUM

Correctness/velocity: AGENTS.md says `spec/` is "regenerated wholesale — do not edit", yet history shows hand-patching; the next `make spec` silently reverts such fixes.

- Commit `f4b94113` ("fix code gen failure") hand-edits `agent-manager-service/spec/model_mcp_proxy_request.go` (and `cli/pkg/clients/amsvc/gen/types.gen.go`) to match a YAML description change (`docs/api_v1_openapi.yaml:15003`) that a prior commit made without running `make spec`.
- Commit `11f6e0aa` also touched `spec/` without touching `docs/api_v1_openapi.yaml`.
- Only comment text drifted this time, but the process failed twice in the last 40 spec-touching commits, and nothing in CI verifies `make spec` output matches the checked-in `spec/`.

**Remediation (S):** add a CI job that runs `make spec` (and the CLI client codegen) and fails on `git diff --exit-code`. Eliminates the class of bug.

## 6. Two parallel "agent config" subsystems with near-identical names — MEDIUM

Onboarding cost/correctness: `AgentConfig` (table `agent_configs`, migration 007) and `AgentConfiguration` (table `agent_configurations`, migration 008) coexist with different conventions and separate repos/mocks.

- `repositories/agent_config_repository.go:35-40` — `AgentConfigRepository`: no `context.Context`, no tx params, its own local sentinel `ErrAgentConfigNotFound` (line 30) instead of `utils` sentinels; used only by `services/agent_manager.go` (lines 81, 501, 731, 2182…).
- `repositories/agent_configuration_repository.go:34-40` — `AgentConfigurationRepository`: ctx-first, tx-accepting, `utils` sentinels; used by `agent_configuration_service.go`.
- Nobody reading `s.agentConfigRepo` vs `s.agentConfigurationRepo` can tell which subsystem they're in without opening the model.

**Remediation (M):** if `agent_configs` is legacy, document it as such at the interface declaration and rename the type (e.g. `AgentDeploySettingsRepository` — it stores OAuth/API-key deploy settings); if not legacy, migrate it to the ctx/tx/sentinel conventions. Renaming alone is a half-day with mocks regen.

## 7. Per-handler copy-pasted API-key auth in the internal gateway API — MEDIUM

Security consistency: unlike the other 221 routes (authz at registration), the internal gateway routes authenticate inside each handler with a duplicated block — one forgotten paste = unauthenticated route.

- `controllers/gateway_internal_controller.go` (500 lines): the `r.Header.Get("api-key")` → `c.gatewayService.VerifyToken(apiKey)` → 401 block is repeated **7 times** (first instance lines 79-93).
- `services/platform_gateway_service.go:446` — `VerifyToken(plainToken string)` takes no `context.Context` despite doing DB lookups, violating the ctx-first rule.

**Remediation (S):** extract a `GatewayAPIKeyAuthMiddleware` (mirroring the existing `jwtassertion.PublisherClientAuthMiddleware()` pattern in `api/monitor_publisher_routes.go:30`) that stashes the resolved gateway in context; add ctx to `VerifyToken`.

## 8. `context.Context` propagation is inconsistent, including deliberate discards — MEDIUM

Correctness (cancellation/timeouts) and observability (request-scoped logger/correlation IDs are lost).

- `services/llm_provider_service.go`: 6 of 12 exported methods take no ctx (e.g. `UpdateCatalogStatus(providerID, ouID string, inCatalog bool)` at line 926).
- Explicit discards: `_ = ctx` at `services/mcp_proxy_deployment.go:95,301,338,379`, `services/mcp_proxy_service.go:220`, `services/agent_configuration_service.go:3937` — methods accept ctx then run GORM calls without `WithContext`.
- Related logging split: 16 service files use injected `s.logger`, 10 use package-level `slog.*` directly (so correlation-ID-enriched logging via `logger.GetLogger(ctx)` — the controllers' pattern — is bypassed).

**Remediation (M):** mechanical sweep — thread ctx through `LLMProviderService` and the `_ = ctx` sites, and pick one logger idiom for services (`logger.GetLogger(ctx)` is the only one that keeps correlation IDs).

## 9. OpenChoreo client: wrong identifier passed at 8 call sites; `projectName` accepted but ignored — MEDIUM

Latent multi-tenancy bug: everything works today only because namespace resolution ignores its argument.

- `clients/openchoreosvc/client/client.go:165-167` — `NamespaceFor(_ string)` discards its parameter and returns the single configured default namespace.
- `GetComponent(ctx, ouID, projectName, componentName)` (`clients/openchoreosvc/client/components.go:544`) is called with `org.Name` instead of `ouID` at 8 sites and `ouID` at 30 (e.g. `services/agent_manager.go:2377` passes `org.Name`, line 2386 passes `ouID` to a sibling call). The day `NamespaceFor` becomes tenant-aware, 8 call paths break silently.
- `projectName` is unused inside `GetComponent` (lines 544-565): components are fetched by name within the shared namespace with no project-scope validation, so service-layer "does this agent belong to this project" checks are only as strong as component-name uniqueness.

**Remediation (S):** normalize all call sites to `ouID` now (grep-driven, low risk), and either use or drop the dead `projectName` parameter — if OpenChoreo can filter by project, wire it; if not, document the invariant at the interface.

## 10. Test coverage is inverted relative to risk: controllers 6/29 files, middleware/rbac/models 0 — MEDIUM

Correctness/velocity: services are decently covered (28 test files, real assertions — e.g. `services/agent_apikey_service_unit_test.go` has 28 assert/require calls; `tests/monitor_test.go` has 188), but the HTTP boundary is nearly untested.

- `controllers/`: 29 source files, 6 test files. The largest, `controllers/identity_controller.go` (1498 lines, ~25 handlers proxying Thunder identity ops), has a test file but the pattern-heavy error-mapping switches across controllers are mostly unexercised.
- Zero tests: `middleware/` (see finding 3), `rbac/` (role→permission map — a wrong entry silently over/under-grants), `websocket/`, `eventhub/`, `db/`.
- Error-mapping duplication compounds this: `errors.Is(err, utils.Err...)` switch chains appear 50 times in `controllers/llm_controller.go`, 40 in `agent_controller.go`, 35 in `agent_configuration_controller.go` — copy-pasted mapping that a shared `utils`-sentinel→HTTP-status helper (extending `controllers/helpers.go`, which currently has exactly one function) would collapse and make testable once.

**Remediation (M):** (a) one table-driven test asserting every `rbac.PredefinedRolePermissions` entry against a golden list; (b) shared error-mapper helper + tests; (c) handler tests for the identity controller's happy/4xx paths.

## 11. Swallowed request-body decode errors in API-key rotation — LOW

Correctness: malformed JSON is silently treated as an empty body, so a caller's `displayName`/`expiresAt` is dropped without any error.

- `controllers/llm_proxy_apikey_controller.go:194` and `controllers/llm_provider_apikey_controller.go:189` — `_ = json.NewDecoder(r.Body).Decode(&specReq)` with comment "Body is optional for rotation; ignore decode errors on empty body". `io.EOF` (empty body) is conflated with genuine syntax errors.

**Remediation (S):** `if err := Decode(...); err != nil && !errors.Is(err, io.EOF) { 400 }`.

## 12. Migrations define no rollbacks; panics in library code — LOW

Operational risk, contained but worth a decision.

- 0 of 34 gormigrate migrations in `db_migrations/` define a `Rollback` func, so `gormigrate.RollbackLast` is unusable and a bad deploy requires manual SQL. Index hygiene is otherwise good — migration `030_swap_org_constraints_to_ou_id.go:44-97` added `ou_id` indexes across all tenant tables when the tenancy column changed.
- Panics reachable from non-startup code: `clients/thundersvc/naming.go:70,149,356` panic on invalid org/env/agent slugs — safe only as long as every caller pre-validates; `services/agent_thunder_provisioning_service.go:157,161` panic inside what appears to be a lazily-initialized path. There is a `middleware/panic_recover.go`, so these become opaque 500s rather than crashes, but they violate the repo's own error-mapping rules.

**Remediation (S):** adopt "new migrations must include Rollback" (backfilling old ones is optional); convert the naming panics to returned errors (call sites already handle errors).

---

**Highest-leverage order:** #3 (authz tests, days), #5 (codegen CI check, hours), #7 (auth middleware extraction, hours), then #2 and #1 as an incremental campaign, with #4 handled by a "shrink on touch" rule rather than a rewrite.
