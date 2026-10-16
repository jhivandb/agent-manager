# A2A External Agent Proxy (Milestone 2b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish an external A2A agent through an environment's ingress gateway (routes derived from its registered card, an optional static upstream header), so callers reach it like a platform A2A agent, and manage that from the console.

**Architecture:** A new `a2a_proxies` table marks a proxied environment and stores the encrypted upstream credential. A new `a2aProxyService` owns the lifecycle and drives M1's publication queue through an `A2APublisher` extracted from `agentManagerService`. The publication worker gains a second upstream resolver: it fetches the origin card through the SSRF-guarded client and derives the gateway transports from it. The card worker fetches a proxied card through the gateway. The card service reaches the proxy service only through a narrow `A2AProxyHooks` interface. The console adds a gateway-routing section on the Agent Card panel, a security drawer host, and API-key gates.

**Tech Stack:** Go 1.26 (nix `go` on Linux), GORM + Postgres, moq v0.5.3, google/wire v0.7.0, openapi-generator (`make spec`, Docker), oapi-codegen v2.6.0 (amctl client); React 19 + TypeScript, TanStack Query, `@wso2/oxygen-ui`, vitest, pnpm 9 workspaces.

**Spec:** `/home/lyuda/docs/superpowers/specs/2026-10-07-a2a-external-agent-proxy-design.md`. Read it before starting. Background: `/home/lyuda/docs/superpowers/specs/2026-10-02-a2a-agent-card-ingestion-design.md` (the card store this builds on), `/home/lyuda/docs/code-review-a2a-agent-card.md` (why the card repository looks the way it does), `/home/lyuda/docs/a2a-discussion-milestones.md`.

**Where:** `/home/lyuda/agent-manager-worktrees/a2a-external-agent-proxy`, branch `a2a-external-agent-proxy` (pushed to `origin`, based on PR wso2/agent-manager#1990). All paths below are relative to that worktree unless absolute.

**Revised:** 2026-10-11, after `/review-plan`. The six changes are listed under "Review fixes" below and are already folded into the tasks.

## Global Constraints

- **Never rewrite history.** The top commit "Install console deps before make setup starts the console container" is an unrelated setup fix; leave it and every earlier commit alone. New commits go on top. Never `--amend`, `--no-verify`, rebase or force-push. Push only when asked, with `git push -u origin HEAD`.
- **Commits follow `/am-ship`:** imperative, first letter capitalized, no trailing period, at most 72 characters, no `feat:`-style prefix, **no trailers** (no `Co-Authored-By`, no `Claude-Session`). Stage explicit paths only; never `git add -A` / `git add .`; never stage `.env`, `.env.test`, `.bin/` or `localdata/`.
- **Plans and specs stay out of the repo** (they live under `/home/lyuda/docs`). `agent-manager-service/docs/` (OpenAPI, audit docs) is part of the repo and is staged normally.
- Every new `.go` file, tests included, starts with the Apache header copied from lines 1–15 of `agent-manager-service/models/a2a_agent_card.go` (the `goheader` linter checks it). Integration test files put `//go:build integration` and a blank line above it. Every new `.ts`/`.tsx` file starts with the license header copied from `console/workspaces/libs/types/src/api/agent-card.ts`.
- Go comments: one terse line, at the density of the surrounding code. Run `make fmt` (gofumpt + goimports) before every Go commit.
- **Migration number is 048.** Upstream main currently ends at 046 and 047 belongs to #1990. If upstream main gains a 047 or 048 before merge, renumber in a separate commit.
- **Environment identity is the UUID** for every new key (`a2a_proxies`, `a2a_publications`). Environment names are used only to look a row up by the name in the URL path, as the card table already does.
- **Upstream auth validation** (spec § API): header matches `^[A-Za-z0-9_-]+$`, at most 255 characters, not `Host` (case-insensitive); value 1–8192 **bytes**, no `'`, CR or LF, **and no `{{`** (see "Spec corrections" item 1: added by this plan, pending your confirmation). An empty value is rejected.
- **Secret handling:** encrypt with `utils.EncryptBytes(value, encryptionKey)` and store base64 (std encoding), the format LLM/MCP secrets use. It goes into the Agent YAML only as `secretRef`. Never return it, log it, or put it in an audit record.
- **Agent YAML:** upstream auth is written as `{type: api-key, header, secretRef}`. `hostRewrite` is not written. Platform output stays byte-identical to `agent-manager-service/services/testdata/a2a_agent_golden.yaml`.
- **Publication worker:** the 30-attempt budget is unchanged; each attempt gets 30s; the batch stops one minute before the claim lease (5 min) ends; outcomes are recorded on `context.WithoutCancel`; every `last_error` goes through `sanitizeA2ALastError`; `errA2ANotProxyable` goes straight to `MarkFailed`.
- **Fetch clients:** the origin card is fetched with the guarded client (`Fetch(ctx, url, true)`); a proxied card is fetched through the gateway with the plain client (`Fetch(ctx, url, false)`).
- **Parked card row** = `status=pending`, `next_attempt_at IS NULL`. Only `Requeue` un-parks it. The publication worker never upserts an external card row.
- **RBAC:** `GET …/a2a-proxy` under `rbac.AgentRead`. `PUT`, `DELETE` and `POST …/refresh` under `rbac.AgentUpdate` + `rbac.AgentEnvNonProduction`, plus the in-service `requireEnvironmentTier` check. No new permissions.
- **Audit:** `agent:set-a2a-proxy` (PUT) and `agent:remove-a2a-proxy` (DELETE) are fail-closed: `audit.Begin` before the first write, abort on error, `Complete` after. Refresh gets a route label only.
- **Console:** pnpm (the repo's `add-console-api-feature` skill still says `rushx`; this workspace uses pnpm). Two-file API pattern (`apis/` + `hooks/`). Mutations await their invalidations inside `mutationFn`, as `hooks/agent-card.ts` does.

### Commands (agent-manager-service, run from `agent-manager-service/`)

One-time tool setup. `.bin/` is gitignored and the Makefile puts it on `PATH` for `make` targets:

```bash
GOBIN=$PWD/.bin go install github.com/matryer/moq@v0.5.3
GOBIN=$PWD/.bin go install github.com/google/wire/cmd/wire@v0.7.0
GOBIN=$PWD/.bin go install mvdan.cc/gofumpt@latest
GOBIN=$PWD/.bin go install golang.org/x/tools/cmd/goimports@latest
GOBIN=$PWD/.bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

One-time integration database. The local stack's Postgres (`agent-manager-db`, `localhost:5432`, user/password `agentmanager`) hosts a **separate** database, `agentmanager_test`. Never point the tests at `agentmanager`; the running stack uses it. Create `.env.test` (gitignored) with:

```
DB_HOST=localhost
DB_PORT=5432
DB_USER=agentmanager
DB_PASSWORD=agentmanager
DB_NAME=agentmanager_test
SERVER_PORT=8080
JWT_SIGNING_ACTIVE_KEY_ID=key-1
OPEN_CHOREO_BASE_URL=http://api.openchoreo.localhost:8080/api/v1
ENCRYPTION_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
OPENBAO_TOKEN=test-token
```

then `docker exec agent-manager-db psql -U agentmanager -c "CREATE DATABASE agentmanager_test OWNER agentmanager;"` (an "already exists" error is fine).

`<int-env>` below means `ENV_FILE_PATH=$PWD/.env.test JWT_SIGNING_PRIVATE_KEY_PATH=$PWD/keys/private.pem JWT_SIGNING_PUBLIC_KEYS_CONFIG=$PWD/keys/public-keys-config.json`.

| Purpose | Command |
|---|---|
| One unit test | `make test-run RUN=<regex> PKG=./services/` |
| All unit tests | `make test-unit` |
| Migrate the test DB | `make gen-keys && env <int-env> go run . -migrate -server=false` |
| One integration test | `env <int-env> go test -tags=integration -count=1 -run <regex> ./repositories/` |
| Whole integration suite (drops and recreates `agentmanager_test` only) | `make test-integration` (it reads `.env.test`) |
| Mocks | `make mocks PKG=./repositories/...` |
| Wire DI | `make wire` |
| OpenAPI → `spec/` (Docker) | `make spec` |
| amctl client (from the **repo root**) | `PATH="$(go env GOPATH)/bin:$PATH" make amctl-gen-client` |
| Format | `make fmt` |
| CI lint | `PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./...` |
| Integration compile check | `go build -tags=integration ./...` |

### Commands (console, run from `console/`)

| Purpose | Command |
|---|---|
| Install (once; again after adding a dependency, which updates `pnpm-lock.yaml`) | `pnpm install` |
| Lint / test one package | `pnpm --filter <pkg> run lint` · `pnpm --filter <pkg> run test` |
| Build a package and its workspace dependencies | `pnpm --filter <pkg>... run build` |
| Packages touched | `@agent-management-platform/types`, `@agent-management-platform/api-client`, `@agent-management-platform/overview`, `@agent-management-platform/deploy`, `@agent-management-platform/agent-security`, `@agent-management-platform/am-core-ui` |

CI runs only lint and build for the console. Run vitest locally anyway.

## Decisions this plan makes (the spec left them open, or the code forced them)

1. **One transaction per transition, testable without a database.** The A2A repositories and `AgentConfigRepository` and `DeploymentRepository` gain `WithTx(tx *gorm.DB) <Repo>`. The transaction itself is opened through a `services.TxRunner` (`func(ctx context.Context, fn func(tx *gorm.DB) error) error`), provided by wire from the `*gorm.DB` and injected into `A2APublisher` and `a2aProxyService`. No repository stands in for the database, and neither service holds a `*gorm.DB` (the spec listed `db`; the runner replaces it). Unit tests pass a runner that calls `fn` with a sentinel `*gorm.DB` and give each repository a separate tx-bound mock, so a write that escapes the transaction hits a nil func and panics.
2. **`ensureAgentEnvAPIArtifact` runs inside that transaction** (its `Create` takes the tx). Its concurrent-create fallback then aborts the transaction instead of recovering; the request fails and a retry succeeds. That is accepted for a rare race.
3. **`A2APublisher` is a concrete `*A2APublisher`**, provided by wire. It **replaces** `agentManagerService`'s `a2aPublicationRepo` constructor parameter, so the 20-argument constructor does not grow. Its `DeleteForAgent` removes publication **and** proxy rows; `deleteAgentAPIArtifact` and the create-time cleanup (renamed `clearInheritedA2AState`) both use it.
4. **A republish picks the source from the agent:** `external` for an external agent, `platform` otherwise. The spec did not say what source `republishA2AAgent` passes; passing `platform` for a proxied external agent would send the worker to the release binding and fail forever.
5. **`TeardownEnv(ctx, tx, key, artifactUUID) (a2aTeardown, error)` + `SendTeardown(ctx, a2aTeardown)`**, instead of the spec's `TeardownEnv(ctx, agent, envUUID)`. The DB work joins the caller's transaction and the gateway is told after commit.
6. **Extra repository methods the flows need:** publication `Get`, `ListForEnvironment` and `WithTx` (GET reads the status from the publication row; environment teardown has to find the rows); deployment `WithTx` and `DeleteForArtifact`; agent-config `WithTx` and `CreateIfAbsent`.
7. **`GET …/a2a-proxy` with no publication row** reports `failed` with a fixed message. Enabling writes both rows in one transaction, so a missing row never resolves on its own, and a `pending` would poll forever.
8. **`A2AProxyHooks` has four methods, not three:** `IsProxied`, `OnCardSourceChanged`, `OnCardSourceRemoved`, plus `OnCardRefresh`. The card refresh needs its own entry point: it must republish without clearing the stored card. On a proxied environment the card service does **not** write a new URL itself: `OnCardSourceChanged` takes the URL and writes it, parks the row and queues the publication in one transaction. A plain `Enqueue` first would leave the row due between two commits, and the card worker could fetch the old origin's card through the gateway; if the hook then failed, that card would stay stored as `fetched` under the new URL while the proxy still read `published`.
9. **Removing the card source of a proxied environment is fail-closed audited** as `agent:remove-a2a-proxy`, since it discards the stored secret. This is in addition to the card service's fail-open `agent:remove-card-source`. The spec audited only `DELETE …/a2a-proxy`.
10. **Withdraw an Agent published after its environment was torn down.** `TeardownEnv` keeps the artifact, so a publish attempt already in flight when the user switches to direct can insert a deployments row and broadcast after the teardown's delete. The worker now checks for that case when `MarkPublished` reports superseded (Review Focus 1).
11. **Environment teardown lives in `A2APublisher.TeardownEnvironment`.** `environmentService` gains one dependency (the publisher), not three.
12. **New sentinels and status codes:** `utils.ErrEnvironmentNotProxied` → 404, `utils.ErrA2AProxyNeedsCardSource` → 409, `utils.ErrA2AProxyAgentNotExternal` and `utils.ErrInvalidUpstreamAuth` → 400. `audit.ErrRecorderUnavailable` → 503, mapped once in `handleCommonErrors` (`controllers/agent_controller.go:86`): no controller maps it today, and the card-source `DELETE` on a proxied environment (Decision 9) must answer the same as `DELETE …/a2a-proxy` rather than fall through to 500.
13. **Route labels:** `agent:set-a2a-proxy`, `agent:remove-a2a-proxy`, and `agent:refresh-a2a-proxy` (label only, like `agent:refresh-card`). Both semantic actions are `ClassCredential`, with the detail schema `agentName`, `upstreamAuthHeader`, `upstreamAuthChanged`.
14. **Console layout:**
    - The gateway toggle and auth live in a new `A2AProxyFields` component.
    - It is embedded in the block `AgentCardSourceForm`, which shows only when no URL is registered. Save there sends `PUT source` and then `PUT a2a-proxy`.
    - For an environment that already has a URL, it sits in a new `EnvA2AProxySection` with its own Save, so a toggle-only or auth-only change is saveable.
    - The header's inline URL editor stays URL-only. That is why `AgentCardSourceForm`'s URL-must-change rule can stay where only the URL can change.
15. **The security drawer host lives in `@agent-management-platform/deploy`** (`ProxySecurityDrawer`, next to `SecurityConfigSections`). `@agent-management-platform/overview` gains `deploy` as a dependency. `pages/overview/package.json` already depends on `@agent-management-platform/build`, another page, though no source file imports it; the source import is what is new (core-ui lazy-loads pages). It avoids moving 900 lines into `shared-component`. Overview's `vite.config.ts` externalizes only `peerDependencies`, so the deploy package is bundled in; both packages declare `sideEffects`, so tree-shaking should keep it to the drawer, and Task 17 checks the overview `dist` size once.
16. **Shared worker helpers** move to `services/a2a_worker_shared.go`: `sanitizeA2ACardError` becomes `sanitizeA2ALastError`, and `resolveGateway` becomes `resolveIngressGateway(gatewayRepo, ouID, envUUID)`. `ListWithFilters` takes no context, so neither does the helper.

### Review fixes (2026-10-11)

Applied from `/review-plan`; each is already written into the task it names.

1. **Card URL change on a proxied environment is one transaction** (Decision 8; Tasks 9 and 10). `OnCardSourceChanged` takes the URL and runs `Enqueue` + `ClearCard` + publication enqueue inside the hook's transaction; the card service skips its own `Enqueue` on that path. `forget` also deletes the card row, since the proxy service now writes it.
2. **`audit.ErrRecorderUnavailable` → 503 lives in `handleCommonErrors`** (Decision 12; Task 13), so the card-source `DELETE` and the proxy routes agree. The OpenAPI for `DELETE …/agent-card/source` declares the 503.
3. **A `TxRunner` replaces `A2AProxyRepository.Transaction`** (Decision 1; Tasks 2, 4, 9, 12). The publisher no longer opens publication and artifact deletes through the proxy repository.
4. **`A2AProxyRepository.Upsert` uses `Select("*")`** (Task 2), like `AgentConfigRepo.Upsert`, so clearing the credential is an explicit write rather than a consequence of how GORM handles a `default:''` tag.
5. **Task 17 Step 1 is a confirmation, not a design gate.** The configurations path already tolerates an agent with no workload ("Spec corrections" 14).
6. **Decision 15 names the existing overview → build dependency** and Task 17 checks the overview bundle size.

## Spec corrections and gaps found against the code (flagged, not silently worked around)

1. **(Security, needs your decision.)** The gateway renders every string under `spec` through Go `text/template` with `secret` and `env` functions before it transforms an Agent (`gateway-controller/pkg/templateengine/spec.go:88-120`, called from `pkg/service/agent/service.go:555`). Suppose an upstream value is `{{ secret "<another handle>" }}` or `{{ env "VAR" }}`. The gateway would resolve it and send the result to an origin the tenant controls. The spec's validation does not cover this. **This plan rejects `{{` in the value** (Task 9). The same exposure may exist for MCP and LLM upstream auth values; that is out of scope here, but worth raising with the security review.
2. **Gateway `pathPrefix` rules are stricter than the spec says** (`agent_validator.go:1564-1621`): it must start with `/`, may not end in `/` (except `"/"`), and must be canonical (`path.Clean`), with no `?#{}` or whitespace. A remainder that breaks them fails the whole Agent deploy, so derivation leaves such a binding unfronted (Task 5).
3. **An omitted `pathPrefix` forwards to `{upstream path}/`, with a trailing slash** (`translator.go:429-438`). An interface URL that equals a non-root upstream path exactly (`https://a.example/team` with the card at `/team/.well-known/agent-card.json`) is therefore reached as `/team/`. The plan fronts it anyway; origins that 404 on the slash will fail at call time. Decide whether to leave such a binding unfronted instead.
4. **The card rewrite fails the whole card (HTTP 500) on any entry with a missing or non-string `protocolBinding`, even on an unfronted binding** (`system-policies/a2a/rewrite.go:494`). The spec covers only version mismatches. Derivation treats such a card as not proxyable (Task 5).
5. **The rewrite code is in api-platform** (`gateway/system-policies/a2a/`), not gateway-controllers as the spec's citations imply. The cited behaviour holds.
6. **Open point 4(c) is resolved:** `rewriteUrls` defaults to true in code (`gateway-controller/pkg/config/agent_card_defaults.go:115,173-174`); the OpenAPI prose saying "disabled" is stale. Nothing to set in the YAML.
7. **`secretRef` is unknown to the gateway.** The spec's design is still right, because AMS's internal API swaps `secretRef` for `value` before serving the YAML (`services/gateway_internal_service.go:331-363`), and Task 6 tests that swap.
8. **The 60s gateway route timeout still applies** to non-streaming HTTP+JSON routes and the card route (`translator.go:340`); only JSON-RPC and the streaming routes are uncapped. That matches the spec's chart change, which only lifts the kgateway 30s cap, but the spec's sentence reads as if all routes were uncapped.
9. **Console API-key nav:** the spec cites `navigationItems.tsx:423, 652`, but those are the internal-agent branches. External agents return early at `:215-336`, a branch that has no Credentials item at all. Task 18 adds one there.
10. **`Security.Component.tsx`'s deployment gate** is at `:63-69` (`hasActiveDeployment`), not `:46-51`.
11. **The card-repository line numbers** in the spec (`:118`, `:165`, `:167-184`, `:187-206`, `:208-228`, `:231`) are stale. At `bc19012cc`: `Enqueue` is `:85-118`, `Requeue` `:120-140`, `newSourceURLAssignments` `:147-162`, `a2aCardClaimColumns` `:164`, the NULL-never-due filter `:174`, `MarkFetched`'s `source_url` write `:194`. The behaviour the spec describes is accurate.
12. **The spec's repository list was incomplete.** A publication `Get`, a `ListForEnvironment`, a deployment delete-by-artifact and an agent-config insert-if-absent did not exist; GET status, environment teardown, `TeardownEnv` and `ensureDefaultAgentConfig` need them (Decision 6).
13. **`republishA2AAgent`'s source** (Decision 4), **refresh needing its own hook** (Decision 8), **auditing card-source removal on a proxied environment** (Decision 9) and **the disable-vs-publish race** (Decision 10) were all unspecified.
14. **The spec reuses the deploy-settings sections for external agents but not their data source.** The sections are seeded from `GET …/configurations`, which also lists workloads and release bindings (`components.go:2845-2950`). An external agent has neither. The client filters both lists by component and tolerates empty ones (`:2857-2874`, `:2896-2900`), file mounts error only on a non-200 (`:2969-2972`), and the per-environment config read returns nil when no row exists (`services/agent_manager.go:6125`), so the call should succeed. Task 17 confirms it by hand.

## Review Focus

Inputs and failure modes the spec implies that no spec-listed test exercises, most likely to bite first. Each has a test in the task named.

1. **Switching to direct while a publish of that environment is in flight.** The user expects the gateway to stop serving the agent. Without a guard, the attempt's deployments row and deploy event land after the teardown and the Agent stays live. → Task 7 `TestReconcilerWithdrawsAnAgentTornDownMidPublish`.
2. **An origin interface URL with a trailing slash or other non-canonical path** (`https://a.example/a2a/jsonrpc/`). The user expects that binding left out with a reason, and the rest of the agent still proxied, not a gateway deploy failure. → Task 5 `TestDeriveA2AProxyRoutesLeavesNonCanonicalPathsUnfronted`.
3. **A card entry without `protocolBinding`** (a hand-written card, or one older entry). The user expects an immediate `failed` naming the entry, not a published Agent whose card then returns 500 for ten minutes. → Task 5 `TestDeriveA2AProxyRoutesRequiresABindingOnEveryInterface`.
4. **An upstream value containing `{{`** (template syntax, or a token that happens to contain it). The user expects a 400, never a gateway resolving it. → Task 9 `TestValidateA2AUpstreamAuth` row "template syntax".
5. **An origin serving both bindings at its root** (`https://a.example/`, the a2a-sdk default). The user expects both fronted, with `pathPrefix` omitted on each, which the gateway allows. → Task 5 `TestDeriveA2AProxyRoutesFrontsBothBindingsAtTheRoot`.

---

## File Structure

**agent-manager-service**

| File | Action | Responsibility | Task |
|---|---|---|---|
| `db_migrations/048_create_a2a_proxies.go`, `migration_list.go` | create / modify | `a2a_proxies`; publications `source` + UUID key | 1 |
| `models/a2a_publication.go` | modify | `A2APublicationSource`, `Source` field | 1 |
| `repositories/a2a_publication_repository.go` (+ unit, integration tests, mock) | modify | UUID key, source, `Get`, `ListForEnvironment`, deletes, `WithTx`, SQL drift filter | 1 |
| `models/a2a_proxy.go` (+ `models/a2a_proxy_test.go`) | create | `A2AProxy`, `UpstreamAuth()`; later `A2AProxyAuthUpdate`, `A2AProxyStatus` | 2, 9 |
| `repositories/a2a_proxy_repository.go` (+ integration test, mock) | create | proxy rows, `WithTx` | 2 |
| `repositories/a2a_agent_card_repository.go` (+ tests, mock) | modify | `ClearCard`, `WithTx`, `MarkFetched` keeps external `source_url` | 3 |
| `repositories/agent_config_repository.go` (+ new integration test, mock) | modify | `CreateIfAbsent`, `WithTx` | 3 |
| `repositories/deployment_repository.go` (+ new integration test, mock) | modify | `DeleteForArtifact`, `WithTx` | 3 |
| `services/a2a_publisher.go` (+ unit test) | create | `TxRunner`, `Enqueue`, `ShouldPublish`, `TeardownEnv`/`SendTeardown`, `DeleteForAgent`, `Publication`, `TeardownEnvironment` | 4, 12 |
| `services/agent_manager.go` | modify | delegate to the publisher; deploy settings for proxied external agents | 4, 11 |
| `services/a2a_proxy_routes.go` (+ unit test) | create | `deriveA2AProxyRoutes`, `errA2ANotProxyable` | 5 |
| `services/a2a_agent_deployment.go` (+ test, new golden) | modify | transports + `upstream.auth`; `pathPrefix` omitempty | 6 |
| `services/gateway_internal_service_unit_test.go` | modify | `secretRef` → `value` in Agent YAML | 6 |
| `services/a2a_worker_shared.go` | create | `sanitizeA2ALastError`, `resolveIngressGateway` | 7 |
| `services/a2a_publication_reconciler.go` (+ test) | modify | `resolveUpstream`, non-retryable path, deadlines, card trigger, withdraw | 7 |
| `services/a2a_card_reconciler.go` (+ test) | modify | proxied card through the gateway | 8 |
| `services/a2a_proxy_service.go` (+ unit test) | create | proxy lifecycle, validation, audit, `A2AProxyHooks` | 9 |
| `services/a2a_agent_card_service.go` (+ test) | modify | branch to the hooks on proxied environments | 10 |
| `services/environment_service.go` (+ test) | modify | environment teardown | 12 |
| `utils/errors.go` | modify | proxy sentinels | 9 |
| `audit/actions_domain.go`, `audit/policy.go`, `audit/actions_domain_test.go`, `docs/audit-logging.md` | modify | proxy actions | 9, 13 |
| `docs/api_v1_openapi.yaml`, `spec/*` | modify / generate | `/a2a-proxy` contract | 13 |
| `controllers/a2a_proxy_controller.go` (+ unit test) | create | HTTP layer | 13 |
| `controllers/agent_controller.go`, `controllers/error_mapping_unit_test.go` | modify | 503 for an audit refusal in `handleCommonErrors` | 13 |
| `api/a2a_proxy_routes.go`, `api/app.go`, `api/audit_coverage_test.go` | create / modify | routes + authz | 13 |
| `wiring/wire.go`, `wiring/params.go`, `wiring/wire_gen.go` | modify / generate | DI | 4, 7, 8, 10, 12, 13 |
| `tests/a2a_agent_lifecycle_test.go` | modify | new reconciler constructor | 7 |
| `cli/pkg/clients/amsvc/gen/*` (repo root) | generate | amctl client | 13 |

**deployments**

| File | Action | Task |
|---|---|---|
| `deployments/helm-charts/wso2-amp-api-platform-gateway-extension/templates/gateway-kgateway-route.yaml` | modify (`30s` → `0s`) | 14 |

**console** (`console/workspaces/…`)

| File | Action | Responsibility | Task |
|---|---|---|---|
| `libs/types/src/api/a2a-proxy.ts`, `api/index.ts` | create / modify | types | 15 |
| `libs/api-client/src/apis/a2a-proxy.ts`, `hooks/a2a-proxy.ts` (+ test), barrels | create / modify | fetch fns + hooks | 15 |
| `libs/api-client/src/hooks/agent-card.ts` | modify | export the query key; `poll` option | 15 |
| `pages/overview/src/AgentOverview/a2aProxyDraft.ts` (+ test) | create | draft ↔ request logic, validation | 16 |
| `pages/overview/src/AgentOverview/A2AProxyFields.tsx` | create | toggle + header + write-only secret | 16 |
| `pages/overview/src/AgentOverview/EnvA2AProxySection.tsx` (+ test) | create | status chip, Save, confirmations | 16 |
| `pages/overview/src/AgentOverview/AgentCardSourceForm.tsx`, `EnvAgentCardSection.tsx` (+ test) | modify | two-call save; polling stop; host the section | 16 |
| `pages/deploy/src/subComponent/SecurityConfigSections.tsx` | modify | accept external A2A agents | 17 |
| `pages/deploy/src/subComponent/ProxySecurityDrawer.tsx`, `pages/deploy/src/index.ts` | create / modify | deploy-settings host for proxied envs | 17 |
| `pages/overview/package.json`, `console/pnpm-lock.yaml` | modify | depend on `deploy` | 17 |
| `pages/agent-security/src/Security.Component.tsx` | modify | API keys for proxied envs | 18 |
| `core-ui/src/Layouts/OxygenLayout/navigationItems.tsx` | modify | Credentials for external A2A agents | 18 |

---

### Task 1: Migration 048 and the publication repository

**Files:**
- Create: `agent-manager-service/db_migrations/048_create_a2a_proxies.go`
- Modify: `agent-manager-service/db_migrations/migration_list.go`, `agent-manager-service/models/a2a_publication.go`, `agent-manager-service/repositories/a2a_publication_repository.go`
- Generate: `agent-manager-service/repositories/repomocks/a2a_publication_repository_mock.go`
- Test: `agent-manager-service/repositories/a2a_publication_repository_unit_test.go`, `agent-manager-service/repositories/a2a_publication_repository_test.go`

The migration and the repository change land together: once 048 drops the name-based unique key, today's `Enqueue` (`ON CONFLICT (…, environment_name)`) fails with "no unique or exclusion constraint matching the ON CONFLICT specification".

**Interfaces:**
- Produces:
  - `type models.A2APublicationSource string`; `models.A2APublicationSourcePlatform = "platform"`, `models.A2APublicationSourceExternal = "external"`; field `A2APublication.Source A2APublicationSource`
  - `var repositories.ErrA2APublicationNotFound error`; `const repositories.A2APublicationClaimLease = 5 * time.Minute` (renamed from the unexported `a2aPublicationClaimLease`)
  - `A2APublicationRepository` gains:
    - `WithTx(tx *gorm.DB) A2APublicationRepository`
    - `Get(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (*models.A2APublication, error)`
    - `ListForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) ([]models.A2APublication, error)`
    - `DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error`
    - `DeleteForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) error`
  - `Enqueue` keys on `(ou_id, project_name, agent_name, environment_uuid)`, writes `source` (empty means `platform`), and deletes a same-named row from another environment UUID first. `FindPublished` returns `source = 'platform'` rows only.

- [ ] **Step 1: Pin the source values**

In `repositories/a2a_publication_repository_unit_test.go`, append to `TestA2APublicationTableAndStatuses`:

```go
	assert.Equal(t, models.A2APublicationSource("platform"), models.A2APublicationSourcePlatform)
	assert.Equal(t, models.A2APublicationSource("external"), models.A2APublicationSourceExternal)
```

- [ ] **Step 2: Write the failing integration tests**

In `repositories/a2a_publication_repository_test.go`:
- Rename `a2aPublicationClaimLease` to `A2APublicationClaimLease` where `TestA2APublicationClaimDueLeasesTheRow` uses it.
- Add `"errors"` and `"gorm.io/gorm"` to the imports.
- Append:

```go
func publicationRowsFor(t *testing.T, pub *models.A2APublication) []models.A2APublication {
	t.Helper()
	var rows []models.A2APublication
	require.NoError(t, db.GetDB().Where("ou_id = ? AND agent_name = ?", pub.OUID, pub.AgentName).Find(&rows).Error)
	return rows
}

// A re-created environment of the same name has a new UUID; it must not inherit the old row.
func TestA2APublicationEnqueueReplacesARowFromADeletedEnvironment(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()
	old := newTestPublication("recreated-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, old)
	require.NoError(t, repo.Enqueue(ctx, old))
	require.NoError(t, repo.MarkPublished(ctx, *old, "http://agent:9099"))

	recreated := newTestPublication(old.AgentName)
	recreated.OUID = old.OUID
	require.NoError(t, repo.Enqueue(ctx, recreated))

	rows := publicationRowsFor(t, old)
	require.Len(t, rows, 1)
	assert.Equal(t, recreated.EnvironmentUUID, rows[0].EnvironmentUUID)
	assert.Empty(t, rows[0].PublishedUpstreamURL, "nothing carries over from the deleted environment")
}

// A mode switch re-enqueues the same environment with the other source.
func TestA2APublicationEnqueueRewritesTheSource(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()
	pub := newTestPublication("source-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))
	assert.Equal(t, models.A2APublicationSourcePlatform, statusOf(t, pub).Source, "an unset source is platform")

	external := *pub
	external.Source = models.A2APublicationSourceExternal
	require.NoError(t, repo.Enqueue(ctx, &external))
	assert.Equal(t, models.A2APublicationSourceExternal, statusOf(t, pub).Source)
}

// External origins are never polled for drift; filtering after the fetch would break paging.
func TestA2APublicationFindPublishedSkipsExternalRows(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()
	platform := newTestPublication("drift-platform-" + uuid.New().String()[:8])
	external := newTestPublication("drift-external-" + uuid.New().String()[:8])
	external.Source = models.A2APublicationSourceExternal
	for _, pub := range []*models.A2APublication{platform, external} {
		cleanupPublication(t, repo, pub)
		require.NoError(t, repo.Enqueue(ctx, pub))
		require.NoError(t, repo.MarkPublished(ctx, *pub, "http://agent:9099"))
	}

	var names []string
	cursor := uuid.Nil
	for {
		page, err := repo.FindPublished(ctx, cursor, 50)
		require.NoError(t, err)
		for _, row := range page {
			names = append(names, row.AgentName)
		}
		if len(page) < 50 {
			break
		}
		cursor = page[len(page)-1].ID
	}
	assert.Contains(t, names, platform.AgentName)
	assert.NotContains(t, names, external.AgentName)
}

func TestA2APublicationGetIsByEnvironmentUUID(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()
	pub := newTestPublication("get-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)
	require.NoError(t, repo.Enqueue(ctx, pub))

	got, err := repo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	require.NoError(t, err)
	assert.Equal(t, pub.ID, got.ID)

	_, err = repo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, uuid.New())
	assert.ErrorIs(t, err, ErrA2APublicationNotFound)
}

func TestA2APublicationListsAndDeletesByEnvironment(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()
	dev := newTestPublication("env-" + uuid.New().String()[:8])
	prod := newTestPublication(dev.AgentName)
	prod.OUID, prod.EnvironmentName = dev.OUID, "Production"
	neighbour := newTestPublication("env-other-" + uuid.New().String()[:8])
	neighbour.OUID, neighbour.EnvironmentName, neighbour.EnvironmentUUID = dev.OUID, "Production", prod.EnvironmentUUID
	for _, pub := range []*models.A2APublication{dev, prod, neighbour} {
		cleanupPublication(t, repo, pub)
		require.NoError(t, repo.Enqueue(ctx, pub))
	}

	inProd, err := repo.ListForEnvironment(ctx, dev.OUID, prod.EnvironmentUUID)
	require.NoError(t, err)
	var ids []uuid.UUID
	for _, row := range inProd {
		ids = append(ids, row.ID)
	}
	assert.ElementsMatch(t, []uuid.UUID{prod.ID, neighbour.ID}, ids)

	require.NoError(t, repo.DeleteForAgentEnv(ctx, dev.OUID, dev.ProjectName, dev.AgentName, dev.EnvironmentUUID))
	_, err = repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, dev.EnvironmentUUID)
	assert.ErrorIs(t, err, ErrA2APublicationNotFound)
	_, err = repo.Get(ctx, prod.OUID, prod.ProjectName, prod.AgentName, prod.EnvironmentUUID)
	require.NoError(t, err, "only that environment's row is gone")

	require.NoError(t, repo.DeleteForEnvironment(ctx, dev.OUID, prod.EnvironmentUUID))
	inProd, err = repo.ListForEnvironment(ctx, dev.OUID, prod.EnvironmentUUID)
	require.NoError(t, err)
	assert.Empty(t, inProd)
}

// Writes through a transaction-bound repository roll back with the transaction.
func TestA2APublicationWithTxRollsBack(t *testing.T) {
	repo := NewA2APublicationRepository(db.GetDB())
	ctx := context.Background()
	pub := newTestPublication("tx-" + uuid.New().String()[:8])
	cleanupPublication(t, repo, pub)

	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		require.NoError(t, repo.WithTx(tx).Enqueue(ctx, pub))
		return errors.New("roll back")
	})
	require.Error(t, err)

	_, err = repo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	assert.ErrorIs(t, err, ErrA2APublicationNotFound)
}
```

- [ ] **Step 3: Verify they fail**

Run: `go build -tags=integration ./repositories/`
Expected: FAIL — `undefined: models.A2APublicationSourcePlatform`, `repo.WithTx undefined`, `undefined: A2APublicationClaimLease`.

- [ ] **Step 4: Write migration 048**

`db_migrations/048_create_a2a_proxies.go` (Apache header first):

```go
package dbmigrations

import (
	"gorm.io/gorm"
)

// a2a_proxies marks an external A2A agent's environment as published through
// the environment's ingress gateway: the row existing is what makes it proxied.
// It also holds the encrypted credential the gateway adds upstream.
//
// a2a_publications gains the source of the upstream it publishes, and moves to
// an environment-UUID key like a2a_agent_cards did in 047, so a re-created
// environment of the same name never inherits a publication. Every existing row
// already carries environment_uuid, so nothing needs backfilling.
var migration048 = migration{
	ID: 48,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			createProxies := `
			CREATE TABLE IF NOT EXISTS a2a_proxies (
				id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				ou_id                VARCHAR(255) NOT NULL,
				project_name         VARCHAR(255) NOT NULL,
				agent_name           VARCHAR(255) NOT NULL,
				environment_uuid     UUID NOT NULL,
				environment_name     VARCHAR(255) NOT NULL,
				upstream_auth_header VARCHAR(255) NOT NULL DEFAULT '',
				upstream_auth_secret TEXT NOT NULL DEFAULT '',
				created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),

				CONSTRAINT uq_a2a_proxies_agent_env
					UNIQUE (ou_id, project_name, agent_name, environment_uuid)
			)`
			if err := runSQL(tx, createProxies); err != nil {
				return err
			}
			addSource := `
			ALTER TABLE a2a_publications
				ADD COLUMN IF NOT EXISTS source VARCHAR(32) NOT NULL DEFAULT 'platform'`
			if err := runSQL(tx, addSource); err != nil {
				return err
			}
			dropNameKey := `
			ALTER TABLE a2a_publications DROP CONSTRAINT IF EXISTS uq_a2a_publications_agent_env`
			if err := runSQL(tx, dropNameKey); err != nil {
				return err
			}
			addUUIDKey := `
			ALTER TABLE a2a_publications ADD CONSTRAINT uq_a2a_publications_agent_env
				UNIQUE (ou_id, project_name, agent_name, environment_uuid)`
			return runSQL(tx, addUUIDKey)
		})
	},
}
```

Append `migration048,` after `migration047,` in `db_migrations/migration_list.go`.

- [ ] **Step 5: Add the source to the model**

In `models/a2a_publication.go`, after the status constants:

```go
// A2APublicationSource says where the publication's upstream comes from.
type A2APublicationSource string

const (
	// A2APublicationSourcePlatform rows publish a platform agent's release binding.
	A2APublicationSourcePlatform A2APublicationSource = "platform"
	// A2APublicationSourceExternal rows publish an external agent's origin, derived from its registered card.
	A2APublicationSourceExternal A2APublicationSource = "external"
)
```

and in `A2APublication`, after `ArtifactUUID`:

```go
	Source        A2APublicationSource `gorm:"column:source;not null;default:'platform'"`
```

(gofumpt realigns the neighbouring tags.)

- [ ] **Step 6: Update the repository**

In `repositories/a2a_publication_repository.go`:

Below `ErrA2APublicationSuperseded`:

```go
// ErrA2APublicationNotFound means no publication row exists for the agent and environment.
var ErrA2APublicationNotFound = errors.New("a2a publication not found")
```

Interface additions (keep the existing methods and comments):

```go
	// WithTx returns the repository bound to tx, so its writes commit or roll back with the caller's.
	WithTx(tx *gorm.DB) A2APublicationRepository

	// Get returns ErrA2APublicationNotFound when no row exists.
	Get(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (*models.A2APublication, error)

	// ListForEnvironment returns every agent's row in one environment.
	ListForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) ([]models.A2APublication, error)

	// DeleteForAgentEnv removes one environment's row.
	DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error

	// DeleteForEnvironment removes every agent's row in a deleted environment.
	DeleteForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) error
```

Change the `FindPublished` doc line to `// FindPublished pages through published platform rows in id order, starting after afterID. External origins are never drift-checked.` and the `Enqueue` doc to add `A same-named row from a deleted environment is replaced.`

Replace the lease constant:

```go
// A2APublicationClaimLease comfortably outlasts one publish attempt.
const A2APublicationClaimLease = 5 * time.Minute
```

and use it in `ClaimDue` (`now.Add(A2APublicationClaimLease)`).

Replace `Enqueue` and `FindPublished`, and add the new methods:

```go
func (r *a2aPublicationRepository) WithTx(tx *gorm.DB) A2APublicationRepository {
	return &a2aPublicationRepository{db: tx}
}

func (r *a2aPublicationRepository) Enqueue(ctx context.Context, pub *models.A2APublication) error {
	// Postgres keeps microseconds; matching that here keeps pub.UpdatedAt equal
	// to the stored value, which the Mark* methods compare against.
	now := time.Now().Truncate(time.Microsecond)
	pub.Status = models.A2APublicationStatusPending
	pub.AttemptCount = 0
	pub.LastError = ""
	pub.NextAttemptAt = &now
	pub.UpdatedAt = now
	if pub.Source == "" {
		pub.Source = models.A2APublicationSourcePlatform
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A same-named row from a deleted environment must not outlive its replacement.
		err := tx.Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_name = ? AND environment_uuid <> ?",
			pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName, pub.EnvironmentUUID).
			Delete(&models.A2APublication{}).Error
		if err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "ou_id"},
				{Name: "project_name"},
				{Name: "agent_name"},
				{Name: "environment_uuid"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"environment_name", "artifact_uuid", "source", "status",
				"attempt_count", "last_error", "next_attempt_at", "updated_at",
			}),
		}).Create(pub).Error
	})
}

func (r *a2aPublicationRepository) FindPublished(ctx context.Context, afterID uuid.UUID, limit int) ([]models.A2APublication, error) {
	var rows []models.A2APublication
	err := r.db.WithContext(ctx).
		Where("status = ? AND source = ? AND id > ?",
			models.A2APublicationStatusPublished, models.A2APublicationSourcePlatform, afterID).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *a2aPublicationRepository) Get(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (*models.A2APublication, error) {
	var row models.A2APublication
	err := r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_uuid = ?",
			ouID, projectName, agentName, environmentUUID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrA2APublicationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *a2aPublicationRepository) ListForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) ([]models.A2APublication, error) {
	var rows []models.A2APublication
	err := r.db.WithContext(ctx).
		Where("ou_id = ? AND environment_uuid = ?", ouID, environmentUUID).
		Order("agent_name ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *a2aPublicationRepository) DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_uuid = ?",
			ouID, projectName, agentName, environmentUUID).
		Delete(&models.A2APublication{}).Error
}

func (r *a2aPublicationRepository) DeleteForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND environment_uuid = ?", ouID, environmentUUID).
		Delete(&models.A2APublication{}).Error
}
```

`environment_uuid` is no longer in the update list: it is the conflict key now.

- [ ] **Step 7: Regenerate the mock and run the tests**

```bash
make mocks PKG=./repositories/...
git status --short repositories/repomocks/   # only a2a_publication_repository_mock.go may change
make gen-keys && env <int-env> go run . -migrate -server=false
env <int-env> go test -tags=integration -count=1 -run 'TestA2APublication' ./repositories/
make test-run RUN=TestA2APublicationTableAndStatuses PKG=./repositories/
```

Expected: all PASS, including the existing `TestA2APublication*` tests. Those re-enqueue a copy of the same row (`requeue := *pub`), so the same environment UUID still upserts in place. If another mock file changes, `moq` is not v0.5.3: fix that and rerun.

- [ ] **Step 8: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./repositories/... ./models/... ./db_migrations/...
go build ./... && go build -tags=integration ./...
git add db_migrations/048_create_a2a_proxies.go db_migrations/migration_list.go models/a2a_publication.go \
  repositories/a2a_publication_repository.go repositories/repomocks/a2a_publication_repository_mock.go \
  repositories/a2a_publication_repository_unit_test.go repositories/a2a_publication_repository_test.go
git commit -m "Add the A2A proxy table and key A2A publications on environment UUID"
```

`go build ./...` must pass at this point: nothing outside `repositories/` used `a2aPublicationClaimLease`. Check with `grep -rn a2aPublicationClaimLease .`.

---

### Task 2: A2A proxy model and repository

**Files:**
- Create: `agent-manager-service/models/a2a_proxy.go`, `agent-manager-service/models/a2a_proxy_test.go`, `agent-manager-service/repositories/a2a_proxy_repository.go`, `agent-manager-service/repositories/a2a_proxy_repository_test.go`
- Generate: `agent-manager-service/repositories/repomocks/a2a_proxy_repository_mock.go`

**Interfaces:**
- Consumes: table `a2a_proxies` (Task 1); `models.UpstreamAuth`, `models.AuthTypeAPIKey`.
- Produces:
  - `type models.A2AProxy struct { ID uuid.UUID; OUID, ProjectName, AgentName string; EnvironmentUUID uuid.UUID; EnvironmentName, UpstreamAuthHeader, UpstreamAuthSecret string; CreatedAt, UpdatedAt time.Time }`, `TableName() == "a2a_proxies"`
  - `func (p *models.A2AProxy) UpstreamAuth() *models.UpstreamAuth`: nil without a header and secret; otherwise `{Type: "api-key", Header, SecretRef}`.
  - `var repositories.ErrA2AProxyNotFound error`
  - `type repositories.A2AProxyRepository interface`:
    - `WithTx(tx *gorm.DB) A2AProxyRepository`
    - `Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AProxy, error)`
    - `GetByEnvUUID(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (*models.A2AProxy, error)`
    - `Upsert(ctx context.Context, proxy *models.A2AProxy) error`
    - `Delete(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error` (returns `ErrA2AProxyNotFound` when nothing was deleted)
    - `DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error`
    - `DeleteForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) error`
  - `func repositories.NewA2AProxyRepository(db *gorm.DB) A2AProxyRepository`; `repomocks.A2AProxyRepositoryMock`

- [ ] **Step 1: Write the model test**

`models/a2a_proxy_test.go`:

```go
package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestA2AProxyTable(t *testing.T) {
	assert.Equal(t, "a2a_proxies", A2AProxy{}.TableName())
}

// The Agent YAML carries the ciphertext as secretRef; the internal API decrypts it for the gateway.
func TestA2AProxyUpstreamAuth(t *testing.T) {
	assert.Nil(t, (&A2AProxy{}).UpstreamAuth(), "no header, no upstream auth")
	assert.Nil(t, (&A2AProxy{UpstreamAuthHeader: "X-Key"}).UpstreamAuth(), "a header without a secret is not auth")

	auth := (&A2AProxy{UpstreamAuthHeader: "X-Key", UpstreamAuthSecret: "c2VjcmV0"}).UpstreamAuth()
	require.NotNil(t, auth)
	assert.Equal(t, "api-key", *auth.Type)
	assert.Equal(t, "X-Key", *auth.Header)
	assert.Equal(t, "c2VjcmV0", *auth.SecretRef)
	assert.Nil(t, auth.Value, "the plaintext never reaches the YAML AMS stores")
}
```

- [ ] **Step 2: Write the integration tests**

`repositories/a2a_proxy_repository_test.go` (`//go:build integration`, blank line, Apache header):

```go
package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

func newTestProxy(agentName string) *models.A2AProxy {
	return &models.A2AProxy{
		OUID:               "ou-" + uuid.New().String()[:8],
		ProjectName:        "checkout",
		AgentName:          agentName,
		EnvironmentName:    "Development",
		EnvironmentUUID:    uuid.New(),
		UpstreamAuthHeader: "X-Upstream-Key",
		UpstreamAuthSecret: "c2VjcmV0",
	}
}

func cleanupProxy(t *testing.T, repo A2AProxyRepository, proxy *models.A2AProxy) {
	t.Helper()
	t.Cleanup(func() {
		_ = repo.DeleteForAgent(context.Background(), proxy.OUID, proxy.ProjectName, proxy.AgentName)
	})
}

func TestA2AProxyUpsertCreatesAndUpdates(t *testing.T) {
	repo := NewA2AProxyRepository(db.GetDB())
	ctx := context.Background()
	proxy := newTestProxy("upsert-" + uuid.New().String()[:8])
	cleanupProxy(t, repo, proxy)
	require.NoError(t, repo.Upsert(ctx, proxy))

	byName, err := repo.Get(ctx, proxy.OUID, proxy.ProjectName, proxy.AgentName, "Development")
	require.NoError(t, err)
	assert.Equal(t, "X-Upstream-Key", byName.UpstreamAuthHeader)
	byUUID, err := repo.GetByEnvUUID(ctx, proxy.OUID, proxy.ProjectName, proxy.AgentName, proxy.EnvironmentUUID)
	require.NoError(t, err)
	assert.Equal(t, byName.ID, byUUID.ID)

	cleared := *proxy
	cleared.UpstreamAuthHeader, cleared.UpstreamAuthSecret = "", ""
	require.NoError(t, repo.Upsert(ctx, &cleared))
	got, err := repo.GetByEnvUUID(ctx, proxy.OUID, proxy.ProjectName, proxy.AgentName, proxy.EnvironmentUUID)
	require.NoError(t, err)
	assert.Equal(t, byName.ID, got.ID, "the same row is updated")
	assert.Empty(t, got.UpstreamAuthHeader)
	assert.Empty(t, got.UpstreamAuthSecret, "clearing auth drops the secret")
}

// A re-created environment of the same name must not inherit the old proxy or its secret.
func TestA2AProxyUpsertReplacesARowFromADeletedEnvironment(t *testing.T) {
	repo := NewA2AProxyRepository(db.GetDB())
	ctx := context.Background()
	old := newTestProxy("recreated-" + uuid.New().String()[:8])
	cleanupProxy(t, repo, old)
	require.NoError(t, repo.Upsert(ctx, old))

	recreated := newTestProxy(old.AgentName)
	recreated.OUID, recreated.UpstreamAuthHeader, recreated.UpstreamAuthSecret = old.OUID, "", ""
	require.NoError(t, repo.Upsert(ctx, recreated))

	_, err := repo.GetByEnvUUID(ctx, old.OUID, old.ProjectName, old.AgentName, old.EnvironmentUUID)
	assert.ErrorIs(t, err, ErrA2AProxyNotFound)
	got, err := repo.Get(ctx, old.OUID, old.ProjectName, old.AgentName, "Development")
	require.NoError(t, err)
	assert.Equal(t, recreated.EnvironmentUUID, got.EnvironmentUUID)
	assert.Empty(t, got.UpstreamAuthSecret)
}

func TestA2AProxyLookupsAreOrgScopedAndReportAMissingRow(t *testing.T) {
	repo := NewA2AProxyRepository(db.GetDB())
	ctx := context.Background()
	proxy := newTestProxy("scoped-" + uuid.New().String()[:8])
	cleanupProxy(t, repo, proxy)
	require.NoError(t, repo.Upsert(ctx, proxy))

	_, err := repo.Get(ctx, "other-org", proxy.ProjectName, proxy.AgentName, "Development")
	assert.ErrorIs(t, err, ErrA2AProxyNotFound)
	_, err = repo.GetByEnvUUID(ctx, proxy.OUID, proxy.ProjectName, proxy.AgentName, uuid.New())
	assert.ErrorIs(t, err, ErrA2AProxyNotFound)
}

func TestA2AProxyDeleteReportsAMissingRow(t *testing.T) {
	repo := NewA2AProxyRepository(db.GetDB())
	ctx := context.Background()
	proxy := newTestProxy("delete-" + uuid.New().String()[:8])
	cleanupProxy(t, repo, proxy)
	require.NoError(t, repo.Upsert(ctx, proxy))

	require.NoError(t, repo.Delete(ctx, proxy.OUID, proxy.ProjectName, proxy.AgentName, proxy.EnvironmentUUID))
	assert.ErrorIs(t, repo.Delete(ctx, proxy.OUID, proxy.ProjectName, proxy.AgentName, proxy.EnvironmentUUID), ErrA2AProxyNotFound)
}

func TestA2AProxyDeletesForAgentAndEnvironment(t *testing.T) {
	repo := NewA2AProxyRepository(db.GetDB())
	ctx := context.Background()
	dev := newTestProxy("bulk-" + uuid.New().String()[:8])
	prod := newTestProxy(dev.AgentName)
	prod.OUID, prod.EnvironmentName = dev.OUID, "Production"
	cleanupProxy(t, repo, dev)
	require.NoError(t, repo.Upsert(ctx, dev))
	require.NoError(t, repo.Upsert(ctx, prod))

	require.NoError(t, repo.DeleteForEnvironment(ctx, dev.OUID, prod.EnvironmentUUID))
	_, err := repo.GetByEnvUUID(ctx, prod.OUID, prod.ProjectName, prod.AgentName, prod.EnvironmentUUID)
	assert.ErrorIs(t, err, ErrA2AProxyNotFound)
	_, err = repo.GetByEnvUUID(ctx, dev.OUID, dev.ProjectName, dev.AgentName, dev.EnvironmentUUID)
	require.NoError(t, err, "another environment's row stays")

	require.NoError(t, repo.DeleteForAgent(ctx, dev.OUID, dev.ProjectName, dev.AgentName))
	_, err = repo.GetByEnvUUID(ctx, dev.OUID, dev.ProjectName, dev.AgentName, dev.EnvironmentUUID)
	assert.ErrorIs(t, err, ErrA2AProxyNotFound)
}

func TestA2AProxyWithTxRollsBack(t *testing.T) {
	repo := NewA2AProxyRepository(db.GetDB())
	ctx := context.Background()
	proxy := newTestProxy("tx-" + uuid.New().String()[:8])
	cleanupProxy(t, repo, proxy)

	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		require.NoError(t, repo.WithTx(tx).Upsert(ctx, proxy))
		return errors.New("roll back")
	})
	require.Error(t, err)

	_, err = repo.GetByEnvUUID(ctx, proxy.OUID, proxy.ProjectName, proxy.AgentName, proxy.EnvironmentUUID)
	assert.ErrorIs(t, err, ErrA2AProxyNotFound)
}
```

- [ ] **Step 3: Verify they fail**

Run: `go vet ./models/ && go build -tags=integration ./repositories/`
Expected: FAIL — `undefined: A2AProxy`, `undefined: NewA2AProxyRepository`.

- [ ] **Step 4: Write the model**

`models/a2a_proxy.go`:

```go
package models

import (
	"time"

	"github.com/google/uuid"
)

// A2AProxy marks an external A2A agent's environment as published through the
// environment's ingress gateway; the row existing is what makes it proxied.
type A2AProxy struct {
	ID              uuid.UUID `gorm:"column:id;primaryKey;type:uuid;default:gen_random_uuid()"`
	OUID            string    `gorm:"column:ou_id;not null"`
	ProjectName     string    `gorm:"column:project_name;not null"`
	AgentName       string    `gorm:"column:agent_name;not null"`
	EnvironmentUUID uuid.UUID `gorm:"column:environment_uuid;type:uuid;not null"`
	EnvironmentName string    `gorm:"column:environment_name;not null"`
	// UpstreamAuthHeader is empty when the gateway adds no credential upstream.
	UpstreamAuthHeader string `gorm:"column:upstream_auth_header;not null;default:''"`
	// UpstreamAuthSecret is the header value as base64 AES-256-GCM ciphertext; it is never returned.
	UpstreamAuthSecret string    `gorm:"column:upstream_auth_secret;not null;default:''"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;default:NOW()"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null;default:NOW()"`
}

func (A2AProxy) TableName() string { return "a2a_proxies" }

// UpstreamAuth is the gateway's upstream credential as a secretRef, or nil when there is none.
func (p *A2AProxy) UpstreamAuth() *UpstreamAuth {
	if p.UpstreamAuthHeader == "" || p.UpstreamAuthSecret == "" {
		return nil
	}
	authType, header, secret := AuthTypeAPIKey, p.UpstreamAuthHeader, p.UpstreamAuthSecret
	return &UpstreamAuth{Type: &authType, Header: &header, SecretRef: &secret}
}
```

(`AuthTypeAPIKey` is an untyped string constant, so `authType` is a `string`.)

- [ ] **Step 5: Write the repository**

`repositories/a2a_proxy_repository.go`:

```go
package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// ErrA2AProxyNotFound means the agent's environment is not proxied.
var ErrA2AProxyNotFound = errors.New("a2a proxy not found")

// A2AProxyRepository stores which external A2A agent environments are published through their gateway.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/a2a_proxy_repository_mock.go . A2AProxyRepository:A2AProxyRepositoryMock
type A2AProxyRepository interface {
	// WithTx returns the repository bound to tx, so its writes commit or roll back with the caller's.
	WithTx(tx *gorm.DB) A2AProxyRepository

	// Get looks the row up by the environment name in the URL path; both getters return ErrA2AProxyNotFound.
	Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AProxy, error)
	GetByEnvUUID(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (*models.A2AProxy, error)

	// Upsert writes the row, replacing a same-named row from a deleted environment.
	Upsert(ctx context.Context, proxy *models.A2AProxy) error

	// Delete returns ErrA2AProxyNotFound when the environment was not proxied.
	Delete(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error
	DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error
	DeleteForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) error
}

type a2aProxyRepository struct {
	db *gorm.DB
}

// NewA2AProxyRepository creates an A2AProxyRepository.
func NewA2AProxyRepository(db *gorm.DB) A2AProxyRepository {
	return &a2aProxyRepository{db: db}
}

func (r *a2aProxyRepository) WithTx(tx *gorm.DB) A2AProxyRepository {
	return &a2aProxyRepository{db: tx}
}

func (r *a2aProxyRepository) Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AProxy, error) {
	return r.first(ctx, "ou_id = ? AND project_name = ? AND agent_name = ? AND environment_name = ?",
		ouID, projectName, agentName, environmentName)
}

func (r *a2aProxyRepository) GetByEnvUUID(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (*models.A2AProxy, error) {
	return r.first(ctx, "ou_id = ? AND project_name = ? AND agent_name = ? AND environment_uuid = ?",
		ouID, projectName, agentName, environmentUUID)
}

func (r *a2aProxyRepository) first(ctx context.Context, query string, args ...interface{}) (*models.A2AProxy, error) {
	var row models.A2AProxy
	err := r.db.WithContext(ctx).Where(query, args...).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrA2AProxyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *a2aProxyRepository) Upsert(ctx context.Context, proxy *models.A2AProxy) error {
	proxy.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A same-named row from a deleted environment must not pass its secret on.
		err := tx.Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_name = ? AND environment_uuid <> ?",
			proxy.OUID, proxy.ProjectName, proxy.AgentName, proxy.EnvironmentName, proxy.EnvironmentUUID).
			Delete(&models.A2AProxy{}).Error
		if err != nil {
			return err
		}
		// Select("*") writes an empty header and secret explicitly, as AgentConfigRepo.Upsert does for its booleans.
		return tx.Select("*").Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "ou_id"},
				{Name: "project_name"},
				{Name: "agent_name"},
				{Name: "environment_uuid"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"environment_name", "upstream_auth_header", "upstream_auth_secret", "updated_at",
			}),
		}).Create(proxy).Error
	})
}

func (r *a2aProxyRepository) Delete(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_uuid = ?",
			ouID, projectName, agentName, environmentUUID).
		Delete(&models.A2AProxy{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrA2AProxyNotFound
	}
	return nil
}

func (r *a2aProxyRepository) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ?", ouID, projectName, agentName).
		Delete(&models.A2AProxy{}).Error
}

func (r *a2aProxyRepository) DeleteForEnvironment(ctx context.Context, ouID string, environmentUUID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND environment_uuid = ?", ouID, environmentUUID).
		Delete(&models.A2AProxy{}).Error
}
```

Clearing auth works through the upsert because `excluded.upstream_auth_header` and `excluded.upstream_auth_secret` are `''` for a row with no auth. GORM writes `''` for a zero string field tagged `default:''` even without `Select("*")` (it substitutes the parsed default rather than dropping the column); `Select("*")` makes that explicit instead of a tag-parsing detail. `id` stays out of the insert while it is zero, so `gen_random_uuid()` still applies, and `created_at` keeps its `NOW()` default.

- [ ] **Step 6: Generate the mock and run the tests**

```bash
make mocks PKG=./repositories/...
make test-run RUN='TestA2AProxy' PKG=./models/
env <int-env> go test -tags=integration -count=1 -run 'TestA2AProxy' ./repositories/
```

Expected: PASS. `git status` shows only the new mock under `repomocks/`.

- [ ] **Step 7: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./repositories/... ./models/...
git add models/a2a_proxy.go models/a2a_proxy_test.go repositories/a2a_proxy_repository.go \
  repositories/a2a_proxy_repository_test.go repositories/repomocks/a2a_proxy_repository_mock.go
git commit -m "Add the A2A proxy store"
```

---

### Task 3: Card, agent-config and deployment repository additions

**Files:**
- Modify: `agent-manager-service/repositories/a2a_agent_card_repository.go`, `agent-manager-service/repositories/agent_config_repository.go`, `agent-manager-service/repositories/deployment_repository.go`
- Generate: `agent-manager-service/repositories/repomocks/{a2a_agent_card_repository,agent_config_repository,deployment_repository}_mock.go`
- Test: `agent-manager-service/repositories/a2a_agent_card_repository_test.go` (append), `agent-manager-service/repositories/agent_config_repository_test.go` (create, integration), `agent-manager-service/repositories/deployment_repository_test.go` (create, integration)

**Interfaces:**
- Produces:
  - `A2AAgentCardRepository.WithTx(tx *gorm.DB) A2AAgentCardRepository`
  - `A2AAgentCardRepository.ClearCard(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error`: empties `card`, `card_hash`, `fetched_at`, `release_name`; parks the row (`pending`, `attempt_count=0`, `last_error=''`, `next_attempt_at=NULL`); bumps `updated_at`; returns `ErrA2AAgentCardNotFound` when there is no row.
  - `MarkFetched` writes `source_url` for **platform** rows only.
  - `AgentConfigRepository.WithTx(tx *gorm.DB) AgentConfigRepository`; `AgentConfigRepository.CreateIfAbsent(ctx context.Context, config *models.AgentConfig) error` (inserts; leaves an existing row untouched)
  - `DeploymentRepository.WithTx(tx *gorm.DB) DeploymentRepository`; `DeploymentRepository.DeleteForArtifact(ctx context.Context, artifactUUID uuid.UUID, ouID string) error` (removes every deployments row of the artifact; `deployment_status` cascades)

- [ ] **Step 1: Write the card integration tests**

Append to `repositories/a2a_agent_card_repository_test.go`:

```go
// Switching modes parks the row: no card, and not due until the publish requeues it.
func TestA2AAgentCardClearCardParksTheRow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("park-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	card.SourceURL = "https://agent.example/.well-known/agent-card.json"
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, repo.MarkFetched(ctx, dueCardFor(t, repo, card.AgentName), json.RawMessage(sampleCard), "h", card.SourceURL, ""))

	require.NoError(t, repo.ClearCard(ctx, card.OUID, card.ProjectName, card.AgentName, card.EnvironmentUUID))

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusPending, row.Status)
	assert.Nil(t, row.Card)
	assert.Empty(t, row.CardHash)
	assert.Nil(t, row.FetchedAt)
	assert.Nil(t, row.NextAttemptAt)
	assert.Equal(t, 0, row.AttemptCount)
	assert.Empty(t, row.LastError)
	assert.Equal(t, card.SourceURL, row.SourceURL, "the registered URL stays")

	due, err := repo.ClaimDue(ctx, time.Now().Add(24*time.Hour), 100)
	require.NoError(t, err)
	assert.NotContains(t, claimedNames(due), card.AgentName, "a parked row waits for the publish")

	require.NoError(t, repo.Requeue(ctx, card.OUID, card.ProjectName, card.AgentName, card.EnvironmentName))
	assert.Equal(t, card.AgentName, dueCardFor(t, repo, card.AgentName).AgentName)
}

// A fetch that read the row before the switch must not write the old mode's card back.
func TestA2AAgentCardClearCardSupersedesAnInFlightFetch(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("park-race-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	card.SourceURL = "https://agent.example/.well-known/agent-card.json"
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	read := dueCardFor(t, repo, card.AgentName)

	require.NoError(t, repo.ClearCard(ctx, card.OUID, card.ProjectName, card.AgentName, card.EnvironmentUUID))

	require.ErrorIs(t, repo.MarkFetched(ctx, read, json.RawMessage(sampleCard), "h", card.SourceURL, ""), ErrA2AAgentCardSuperseded)
	assert.Nil(t, cardRowOf(t, card).Card)
}

func TestA2AAgentCardClearCardNeedsARow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	err := repo.ClearCard(context.Background(), "ou-none", "checkout", "ghost", uuid.New())
	assert.ErrorIs(t, err, ErrA2AAgentCardNotFound)
}

// A proxied card is fetched through the gateway; the registered origin URL must survive that.
func TestA2AAgentCardMarkFetchedKeepsAnExternalSourceURL(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("keep-url-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	card.SourceURL = "https://agent.example/.well-known/agent-card.json"
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))

	gatewayURL := "https://gw.example/agent/.well-known/agent-card.json"
	require.NoError(t, repo.MarkFetched(ctx, dueCardFor(t, repo, card.AgentName), json.RawMessage(sampleCard), "h", gatewayURL, ""))
	assert.Equal(t, card.SourceURL, cardRowOf(t, card).SourceURL)

	again := newTestCardLike(card)
	again.SourceURL = card.SourceURL
	require.NoError(t, repo.Enqueue(ctx, again))
	assert.JSONEq(t, sampleCard, string(cardRowOf(t, card).Card), "the same URL is not a change, so the card survives")
}
```

- [ ] **Step 2: Write the agent-config and deployment integration tests**

`repositories/agent_config_repository_test.go` (`//go:build integration`, Apache header):

```go
package repositories

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// Defaults for a newly proxied environment must never overwrite a saved config.
func TestAgentConfigCreateIfAbsentKeepsAnExistingRow(t *testing.T) {
	repo := NewAgentConfigRepo(db.GetDB())
	ctx := context.Background()
	saved := &models.AgentConfig{
		OUID: "ou-" + uuid.New().String()[:8], ProjectName: "checkout",
		AgentName: "cfg-" + uuid.New().String()[:8], EnvironmentName: "Development",
		EnableApiKeySecurity: false, CORSEnabled: false,
		OAuthHeaderName: models.DefaultOAuthHeaderName, OAuthAuthHeaderPrefix: models.DefaultOAuthAuthHeaderPrefix,
	}
	t.Cleanup(func() { _ = repo.DeleteAllByAgent(ctx, saved.OUID, saved.ProjectName, saved.AgentName) })
	require.NoError(t, repo.Upsert(ctx, saved))

	defaults := *saved
	defaults.ID = uuid.Nil
	defaults.EnableApiKeySecurity, defaults.CORSEnabled = true, true
	require.NoError(t, repo.CreateIfAbsent(ctx, &defaults))

	got, err := repo.Get(ctx, saved.OUID, saved.ProjectName, saved.AgentName, "Development")
	require.NoError(t, err)
	assert.False(t, got.EnableApiKeySecurity, "the saved config wins")
	assert.False(t, got.CORSEnabled)
}

func TestAgentConfigCreateIfAbsentInsertsDefaults(t *testing.T) {
	repo := NewAgentConfigRepo(db.GetDB())
	ctx := context.Background()
	cfg := &models.AgentConfig{
		OUID: "ou-" + uuid.New().String()[:8], ProjectName: "checkout",
		AgentName: "cfg-new-" + uuid.New().String()[:8], EnvironmentName: "Development",
		EnableApiKeySecurity: true, CORSEnabled: true, CORSAllowOrigins: []string{"*"},
		OAuthHeaderName: models.DefaultOAuthHeaderName, OAuthAuthHeaderPrefix: models.DefaultOAuthAuthHeaderPrefix,
	}
	t.Cleanup(func() { _ = repo.DeleteAllByAgent(ctx, cfg.OUID, cfg.ProjectName, cfg.AgentName) })
	require.NoError(t, repo.CreateIfAbsent(ctx, cfg))

	got, err := repo.Get(ctx, cfg.OUID, cfg.ProjectName, cfg.AgentName, "Development")
	require.NoError(t, err)
	assert.True(t, got.EnableApiKeySecurity)
	assert.Equal(t, []string{"*"}, got.CORSAllowOrigins)
	assert.Nil(t, got.CardCORSEnabled, "the card inherits the agent's CORS")
}
```

`repositories/deployment_repository_test.go` (`//go:build integration`, Apache header). It seeds a gateway the same way `tests/a2a_agent_lifecycle_test.go:seedIngressGateway` does:

```go
package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// seedAgentDeployment writes a gateway, an agent artifact and one deployment of it.
func seedAgentDeployment(t *testing.T) (artifactUUID uuid.UUID, ouID string) {
	t.Helper()
	gdb := db.GetDB()
	ouID = "ou-" + uuid.New().String()[:8]
	gatewayID, artifactUUID := uuid.New(), uuid.New()
	name := "dep-gw-" + gatewayID.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		gatewayID, name, name, "gw.example", models.GatewayRoleIngress, ouID).Error)
	require.NoError(t, NewArtifactRepo(gdb).Create(gdb, &models.Artifact{
		UUID: artifactUUID, Handle: "proj/agent/" + artifactUUID.String(), Name: "agent-" + artifactUUID.String()[:8],
		Version: "v1.0", Kind: models.KindAgent, OUID: ouID,
	}))
	deployed := models.DeploymentStatusDeployed
	require.NoError(t, NewDeploymentRepo(gdb).CreateWithLimitEnforcement(&models.Deployment{
		Name: "agent-deployment", ArtifactUUID: artifactUUID, OUID: ouID, GatewayUUID: gatewayID,
		Content: []byte("kind: Agent\n"), Status: &deployed,
	}, 10))
	t.Cleanup(func() {
		gdb.Exec(`DELETE FROM artifacts WHERE uuid = ?`, artifactUUID)
		gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, gatewayID)
	})
	return artifactUUID, ouID
}

// A mode switch keeps the artifact (and its API keys) but no deployment of it may be served.
func TestDeploymentDeleteForArtifactInATransaction(t *testing.T) {
	repo := NewDeploymentRepo(db.GetDB())
	artifactUUID, ouID := seedAgentDeployment(t)
	gateways, err := repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	require.Len(t, gateways, 1)

	rolledBack := db.GetDB().Transaction(func(tx *gorm.DB) error {
		require.NoError(t, repo.WithTx(tx).DeleteForArtifact(context.Background(), artifactUUID, ouID))
		return errors.New("roll back")
	})
	require.Error(t, rolledBack)
	gateways, err = repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	assert.Len(t, gateways, 1, "a rolled-back delete leaves the deployment")

	require.NoError(t, repo.DeleteForArtifact(context.Background(), artifactUUID, ouID))
	gateways, err = repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	assert.Empty(t, gateways, "deployment_status went with the deployments")
}
```

`CreateWithLimitEnforcement` also writes the `deployment_status` row (`repositories/deployment_repository.go:141-153`), so `GetDeployedGatewaysByProvider` sees the seeded deployment.

- [ ] **Step 3: Verify they fail**

Run: `go build -tags=integration ./repositories/`
Expected: FAIL — `repo.ClearCard undefined`, `repo.CreateIfAbsent undefined`, `repo.WithTx undefined`, `repo.DeleteForArtifact undefined`.

- [ ] **Step 4: Card repository**

In `repositories/a2a_agent_card_repository.go`, add to the interface:

```go
	// WithTx returns the repository bound to tx, so its writes commit or roll back with the caller's.
	WithTx(tx *gorm.DB) A2AAgentCardRepository

	// ClearCard drops the stored card and parks the row (pending, never due) until Requeue;
	// it returns ErrA2AAgentCardNotFound when no row exists.
	ClearCard(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error
```

Change the `MarkFetched` doc line to `// MarkFetched rewrites card and card_hash only when cardHash differs from read.CardHash, and source_url only for platform rows.`

Implementation:

```go
func (r *a2aAgentCardRepository) WithTx(tx *gorm.DB) A2AAgentCardRepository {
	return &a2aAgentCardRepository{db: tx}
}

func (r *a2aAgentCardRepository) ClearCard(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) error {
	result := r.db.WithContext(ctx).Model(&models.A2AAgentCard{}).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_uuid = ?",
			ouID, projectName, agentName, environmentUUID).
		Updates(map[string]interface{}{
			"card":            nil,
			"card_hash":       "",
			"fetched_at":      nil,
			"release_name":    "",
			"status":          models.A2AAgentCardStatusPending,
			"attempt_count":   0,
			"last_error":      "",
			"next_attempt_at": nil,
			"updated_at":      time.Now().Truncate(time.Microsecond),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrA2AAgentCardNotFound
	}
	return nil
}
```

`Enqueue` opens its own `Transaction`. When `r.db` is already a transaction, GORM turns that into a savepoint (nested transactions are on by default), so the tx-bound repository's `Enqueue` joins the caller's transaction; Task 9's `OnCardSourceChanged` relies on that.

Then in `MarkFetched`, drop `"source_url": fetchedURL,` from the map literal and add after it:

```go
	// An external row's source_url is the registered URL; a fetch through the gateway must not replace it.
	if read.Source == models.A2AAgentCardSourcePlatform {
		updates["source_url"] = fetchedURL
	}
```

- [ ] **Step 5: Agent-config repository**

In `repositories/agent_config_repository.go`, add to the interface:

```go
	// CreateIfAbsent inserts the config unless the environment already has one, which it leaves untouched.
	CreateIfAbsent(ctx context.Context, config *models.AgentConfig) error

	// WithTx returns the repository bound to tx.
	WithTx(tx *gorm.DB) AgentConfigRepository
```

Implementation:

```go
// CreateIfAbsent inserts the config unless the environment already has one.
func (r *AgentConfigRepo) CreateIfAbsent(ctx context.Context, config *models.AgentConfig) error {
	// Select("*") writes false booleans instead of their true column defaults, as Upsert does.
	return r.db.WithContext(ctx).Select("*").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "ou_id"}, {Name: "project_name"}, {Name: "agent_name"}, {Name: "environment_name"}},
		DoNothing: true,
	}).Create(config).Error
}

// WithTx returns the repository bound to tx.
func (r *AgentConfigRepo) WithTx(tx *gorm.DB) AgentConfigRepository {
	return &AgentConfigRepo{db: tx}
}
```

- [ ] **Step 6: Deployment repository**

In `repositories/deployment_repository.go`, add to the interface under the deployment artifact methods:

```go
	// DeleteForArtifact removes every deployment of the artifact; their deployment_status rows cascade.
	DeleteForArtifact(ctx context.Context, artifactUUID uuid.UUID, orgUUID string) error
	// WithTx returns the repository bound to tx.
	WithTx(tx *gorm.DB) DeploymentRepository
```

Implementation:

```go
// DeleteForArtifact removes every deployment of the artifact; their deployment_status rows cascade.
func (r *DeploymentRepo) DeleteForArtifact(ctx context.Context, artifactUUID uuid.UUID, orgUUID string) error {
	return r.db.WithContext(ctx).
		Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, orgUUID).
		Delete(&models.Deployment{}).Error
}

// WithTx returns the repository bound to tx.
func (r *DeploymentRepo) WithTx(tx *gorm.DB) DeploymentRepository {
	return &DeploymentRepo{db: tx}
}
```

- [ ] **Step 7: Regenerate mocks, run the tests**

```bash
make mocks PKG=./repositories/...
git status --short repositories/repomocks/   # only the three mocks named above
env <int-env> go test -tags=integration -count=1 -run 'TestA2AAgentCard|TestAgentConfigCreateIfAbsent|TestDeploymentDeleteForArtifact' ./repositories/
make test-unit
```

Expected: PASS. `TestA2AAgentCardEnqueuePreservesTheLastGoodCard` still passes because platform rows keep writing `source_url`.

- [ ] **Step 8: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./repositories/...
go build ./... && go build -tags=integration ./...
git add repositories/a2a_agent_card_repository.go repositories/agent_config_repository.go repositories/deployment_repository.go \
  repositories/repomocks/a2a_agent_card_repository_mock.go repositories/repomocks/agent_config_repository_mock.go \
  repositories/repomocks/deployment_repository_mock.go repositories/a2a_agent_card_repository_test.go \
  repositories/agent_config_repository_test.go repositories/deployment_repository_test.go
git commit -m "Let A2A card, agent config and deployment writes join a transaction"
```

---

### Task 4: Shared `A2APublisher`, extracted from `agentManagerService`

**Files:**
- Create: `agent-manager-service/services/a2a_publisher.go`, `agent-manager-service/services/a2a_publisher_unit_test.go`
- Modify: `agent-manager-service/services/agent_manager.go` (struct and constructor at `:82-152`; `clearInheritedA2ACards` `:2836-2845` and its call at `:1589`; `deleteAgentAPIArtifact` `:2863-2873`; `enqueueA2APublication`, `enqueueCreatedA2AAgent`, `republishA2AAgent` `:6196-6270`)
- Modify: `agent-manager-service/services/a2a_deploy_enqueue_unit_test.go`, `agent-manager-service/services/a2a_agent_deletion_unit_test.go`
- Modify: `agent-manager-service/wiring/wire.go`; generate `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: Task 1 publication repo (`WithTx`, `Get`, `DeleteForAgentEnv`, `Source`); Task 2 `A2AProxyRepository`; Task 3 `DeploymentRepository.WithTx`/`DeleteForArtifact`; existing `collectA2AAgentDeletionTargets`, `sendA2AAgentDeletion` (`services/a2a_agent_deployment.go:236`, `:293`), `isExternalProvisioned` (`services/a2a_agent_card_service.go`).
- Produces (package `services`):
  - `type a2aPublicationKey struct { OUID, ProjectName, AgentName, EnvironmentName string; EnvironmentUUID uuid.UUID }`
  - `type a2aTeardown struct { artifactUUID uuid.UUID; gatewayIDs []string }`
  - `type TxRunner func(ctx context.Context, fn func(tx *gorm.DB) error) error`: opens one database transaction; wire provides it from the `*gorm.DB` (`ProvideTxRunner`), tests pass a func that calls `fn` with a sentinel.
  - `type A2APublisher struct`; `func NewA2APublisher(tx TxRunner, pubRepo repositories.A2APublicationRepository, proxyRepo repositories.A2AProxyRepository, artifactRepo repositories.ArtifactRepository, deploymentRepo repositories.DeploymentRepository, gatewayRepo repositories.GatewayRepository, events *GatewayEventsService, logger *slog.Logger) *A2APublisher`
  - `(*A2APublisher) Enqueue(ctx context.Context, tx *gorm.DB, key a2aPublicationKey, source models.A2APublicationSource, artifactUUID uuid.UUID) error` (a nil `tx` means no transaction)
  - `(*A2APublisher) ShouldPublish(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, envUUID uuid.UUID) (bool, error)`
  - `(*A2APublisher) Publication(ctx context.Context, key a2aPublicationKey) (*models.A2APublication, error)`
  - `(*A2APublisher) TeardownEnv(ctx context.Context, tx *gorm.DB, key a2aPublicationKey, artifactUUID uuid.UUID) (a2aTeardown, error)`; `(*A2APublisher) SendTeardown(ctx context.Context, t a2aTeardown)`
  - `(*A2APublisher) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error`
  - `func a2aPublicationSourceFor(agent *models.AgentResponse) models.A2APublicationSource`
  - `NewAgentManagerService`'s 16th parameter changes from `a2aPublicationRepo repositories.A2APublicationRepository` to `a2aPublisher *A2APublisher`. The struct field is renamed the same way.
  - Test helpers in `a2a_publisher_unit_test.go`, used again by Tasks 9 and 12: `var txSentinel = &gorm.DB{}`; `type publisherMocks struct{ pubs, txPubs *repomocks.A2APublicationRepositoryMock; proxies *repomocks.A2AProxyRepositoryMock; deployments, txDeployments *repomocks.DeploymentRepositoryMock; gateways *repomocks.GatewayRepositoryMock; artifacts *repomocks.ArtifactRepositoryMock; hub *recordingEventHub; transactions int; txErr error }`; `func newPublisherMocks(t *testing.T) *publisherMocks`; `func (m *publisherMocks) tx() TxRunner` (counts into `transactions`, returns `txErr` when set, else runs `fn(txSentinel)`); `func (m *publisherMocks) publisher() *A2APublisher`; `func testA2AKey() a2aPublicationKey`; `func externalA2AAgent() *models.AgentResponse`.

- [ ] **Step 1: Write the publisher tests**

`services/a2a_publisher_unit_test.go`:

```go
package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// txSentinel stands in for an open transaction; tx-bound mocks check they were handed it.
var txSentinel = &gorm.DB{}

// publisherMocks keeps the plain and the transaction-bound mocks apart, so a write that
// escapes the transaction reaches a nil func and panics.
type publisherMocks struct {
	pubs, txPubs               *repomocks.A2APublicationRepositoryMock
	proxies                    *repomocks.A2AProxyRepositoryMock
	deployments, txDeployments *repomocks.DeploymentRepositoryMock
	gateways                   *repomocks.GatewayRepositoryMock
	artifacts                  *repomocks.ArtifactRepositoryMock
	hub                        *recordingEventHub
	// transactions counts the transactions opened; txErr, when set, makes each one fail to commit.
	transactions int
	txErr        error
}

// tx is the runner the services open transactions with; it hands fn the sentinel.
func (m *publisherMocks) tx() TxRunner {
	return func(_ context.Context, fn func(tx *gorm.DB) error) error {
		m.transactions++
		if m.txErr != nil {
			return m.txErr
		}
		return fn(txSentinel)
	}
}

func newPublisherMocks(t *testing.T) *publisherMocks {
	m := &publisherMocks{
		txPubs:        &repomocks.A2APublicationRepositoryMock{},
		proxies:       &repomocks.A2AProxyRepositoryMock{},
		txDeployments: &repomocks.DeploymentRepositoryMock{},
		gateways: &repomocks.GatewayRepositoryMock{
			ListWithFiltersFunc: func(repositories.GatewayFilterOptions) ([]*models.Gateway, error) {
				return []*models.Gateway{}, nil
			},
		},
		artifacts: &repomocks.ArtifactRepositoryMock{},
		hub:       &recordingEventHub{},
	}
	m.pubs = &repomocks.A2APublicationRepositoryMock{
		WithTxFunc: func(tx *gorm.DB) repositories.A2APublicationRepository {
			assert.Same(t, txSentinel, tx)
			return m.txPubs
		},
	}
	m.deployments = &repomocks.DeploymentRepositoryMock{
		WithTxFunc: func(tx *gorm.DB) repositories.DeploymentRepository {
			assert.Same(t, txSentinel, tx)
			return m.txDeployments
		},
		GetDeployedGatewaysByProviderFunc: func(uuid.UUID, string) ([]string, error) { return []string{"gw-1"}, nil },
	}
	return m
}

func (m *publisherMocks) publisher() *A2APublisher {
	return NewA2APublisher(m.tx(), m.pubs, m.proxies, m.artifacts, m.deployments, m.gateways, NewGatewayEventsService(m.hub), testLogger())
}

func testA2AKey() a2aPublicationKey {
	return a2aPublicationKey{
		OUID: "org-1", ProjectName: "checkout", AgentName: "weather", EnvironmentName: "dev",
		EnvironmentUUID: uuid.MustParse("55555555-5555-5555-5555-555555555555"),
	}
}

func externalA2AAgent() *models.AgentResponse {
	return &models.AgentResponse{
		UUID: "agent-uuid", Name: "weather",
		Provisioning: models.Provisioning{Type: "external"},
		Type:         models.AgentType{Type: "external-agent-api", SubType: "a2a-agent"},
	}
}

func TestA2APublisherShouldPublish(t *testing.T) {
	internal := &models.AgentResponse{Name: "weather", Provisioning: models.Provisioning{Type: "internal"},
		Type: models.AgentType{Type: "agent-api", SubType: "a2a-agent"}}
	chat := &models.AgentResponse{Name: "weather", Type: models.AgentType{Type: "agent-api", SubType: "chat-api"}}
	cases := []struct {
		name   string
		agent  *models.AgentResponse
		lookup func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error)
		want   bool
	}{
		{name: "an internal A2A agent always, without a proxy lookup", agent: internal, want: true},
		{name: "a non-A2A agent never", agent: chat, want: false},
		{
			name: "an external A2A agent in a direct environment", agent: externalA2AAgent(),
			lookup: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
				return nil, repositories.ErrA2AProxyNotFound
			},
		},
		{
			name: "an external A2A agent in a proxied environment", agent: externalA2AAgent(), want: true,
			lookup: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
				return &models.A2AProxy{}, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newPublisherMocks(t)
			m.proxies.GetByEnvUUIDFunc = tc.lookup
			got, err := m.publisher().ShouldPublish(context.Background(), tc.agent, "org-1", "checkout", testA2AKey().EnvironmentUUID)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestA2APublisherShouldPublishDoesNotMaskALookupFailure(t *testing.T) {
	m := newPublisherMocks(t)
	m.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return nil, assert.AnError
	}
	_, err := m.publisher().ShouldPublish(context.Background(), externalA2AAgent(), "org-1", "checkout", uuid.New())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestA2APublisherEnqueueJoinsTheCallersTransaction(t *testing.T) {
	m := newPublisherMocks(t)
	m.txPubs.EnqueueFunc = func(context.Context, *models.A2APublication) error { return nil }
	artifactUUID := uuid.New()

	require.NoError(t, m.publisher().Enqueue(context.Background(), txSentinel, testA2AKey(),
		models.A2APublicationSourceExternal, artifactUUID))

	queued := m.txPubs.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2APublicationSourceExternal, queued[0].Pub.Source)
	assert.Equal(t, artifactUUID, queued[0].Pub.ArtifactUUID)
	assert.Equal(t, testA2AKey().EnvironmentUUID, queued[0].Pub.EnvironmentUUID)
	assert.Equal(t, "dev", queued[0].Pub.EnvironmentName)
}

// The artifact stays so the environment's API keys survive a switch back to proxied.
func TestA2APublisherTeardownEnvKeepsTheArtifactAndWaitsToTellTheGateway(t *testing.T) {
	m := newPublisherMocks(t)
	m.txDeployments.DeleteForArtifactFunc = func(context.Context, uuid.UUID, string) error { return nil }
	m.txPubs.DeleteForAgentEnvFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	artifactUUID := uuid.New()
	publisher := m.publisher()

	teardown, err := publisher.TeardownEnv(context.Background(), txSentinel, testA2AKey(), artifactUUID)
	require.NoError(t, err)
	assert.Empty(t, m.artifacts.DeleteCalls())
	require.Len(t, m.txDeployments.DeleteForArtifactCalls(), 1)
	assert.Equal(t, artifactUUID, m.txDeployments.DeleteForArtifactCalls()[0].ArtifactUUID)
	require.Len(t, m.txPubs.DeleteForAgentEnvCalls(), 1)
	assert.Empty(t, m.hub.published, "nothing is sent before the transaction commits")

	publisher.SendTeardown(context.Background(), teardown)
	require.Len(t, m.hub.published, 1)
	assert.Equal(t, "gw-1", m.hub.published[0].GatewayID)
	assert.Equal(t, artifactUUID.String(), deletedAgentID(t, m.hub.published[0]))
}

func TestA2APublisherDeleteForAgentDropsPublicationsAndProxies(t *testing.T) {
	m := newPublisherMocks(t)
	m.pubs.DeleteForAgentFunc = func(context.Context, string, string, string) error { return nil }
	m.proxies.DeleteForAgentFunc = func(context.Context, string, string, string) error { return nil }

	require.NoError(t, m.publisher().DeleteForAgent(context.Background(), "org-1", "checkout", "weather"))
	assert.Len(t, m.pubs.DeleteForAgentCalls(), 1)
	assert.Len(t, m.proxies.DeleteForAgentCalls(), 1)
}
```

The moq call-record field names come from the interface's parameter names (`Pub`, `ArtifactUUID`, …). If a name differs, follow the generated mock.

- [ ] **Step 2: Rewrite the agentManagerService A2A tests over the publisher**

In `services/a2a_deploy_enqueue_unit_test.go`:

- Add the helper below.
- Replace each `&agentManagerService{a2aPublicationRepo: repo, logger: testLogger()}` with `a2aPublishingAgentService(repo, &repomocks.A2AProxyRepositoryMock{})`. The one in `TestRepublishA2AAgentRejectsUnparseableEnvironmentUUID` passes `&repomocks.A2APublicationRepositoryMock{}`.
- Change the two `svc.enqueueA2APublication(ctx, "org-1", "checkout", "trip-planner", "dev", envUUID, artifactUUID)` calls to `svc.enqueueA2APublication(context.Background(), a2aPublicationKey{OUID: "org-1", ProjectName: "checkout", AgentName: "trip-planner", EnvironmentName: "dev", EnvironmentUUID: envUUID}, models.A2APublicationSourcePlatform, artifactUUID)`; the second one uses `uuid.New()` for both IDs.

```go
// a2aPublishingAgentService is an agentManagerService whose only A2A collaborator is the publisher.
func a2aPublishingAgentService(pubs *repomocks.A2APublicationRepositoryMock, proxies *repomocks.A2AProxyRepositoryMock) *agentManagerService {
	publisher := NewA2APublisher(nil, pubs, proxies, nil, nil, nil, nil, testLogger())
	return NewAgentManagerService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		publisher, nil, nil, nil, testLogger()).(*agentManagerService)
}
```

Append:

```go
// A proxied external agent publishes its origin, so its row must say so or the worker reads a release binding.
func TestRepublishA2AAgentQueuesAProxiedExternalAgentAsExternal(t *testing.T) {
	var enqueued []models.A2APublication
	pubs := &repomocks.A2APublicationRepositoryMock{
		EnqueueFunc: func(_ context.Context, pub *models.A2APublication) error {
			enqueued = append(enqueued, *pub)
			return nil
		},
	}
	proxies := &repomocks.A2AProxyRepositoryMock{
		GetByEnvUUIDFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
			return &models.A2AProxy{}, nil
		},
	}
	svc := a2aPublishingAgentService(pubs, proxies)

	require.NoError(t, svc.republishA2AAgent(context.Background(), externalA2AAgent(), "org-1", "checkout", "weather", "dev", uuid.New().String(), uuid.New()))

	require.Len(t, enqueued, 1)
	assert.Equal(t, models.A2APublicationSourceExternal, enqueued[0].Source)
}

func TestRepublishA2AAgentSkipsADirectExternalAgent(t *testing.T) {
	pubs := &repomocks.A2APublicationRepositoryMock{} // EnqueueFunc is nil: queueing would panic
	proxies := &repomocks.A2AProxyRepositoryMock{
		GetByEnvUUIDFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
			return nil, repositories.ErrA2AProxyNotFound
		},
	}
	svc := a2aPublishingAgentService(pubs, proxies)

	require.NoError(t, svc.republishA2AAgent(context.Background(), externalA2AAgent(), "org-1", "checkout", "weather", "dev", uuid.New().String(), uuid.New()))
}

// A same-named agent created later must not inherit a proxy, or its secret.
func TestClearInheritedA2AStateDropsPublicationsAndProxies(t *testing.T) {
	pubs := &repomocks.A2APublicationRepositoryMock{DeleteForAgentFunc: func(context.Context, string, string, string) error { return nil }}
	proxies := &repomocks.A2AProxyRepositoryMock{DeleteForAgentFunc: func(context.Context, string, string, string) error { return nil }}
	svc := a2aPublishingAgentService(pubs, proxies)

	require.NoError(t, svc.clearInheritedA2AState(context.Background(), "org-1", "checkout", "weather"))
	assert.Len(t, pubs.DeleteForAgentCalls(), 1)
	assert.Len(t, proxies.DeleteForAgentCalls(), 1)
}
```

Add `"github.com/wso2/agent-manager/agent-manager-service/repositories"` to its imports.

In `services/a2a_agent_deletion_unit_test.go`'s fixture (`newAgentArtifactDeletionFixture`), build the service through the constructor and record proxy deletes as a step. Replace the `f.svc = &agentManagerService{…}` literal with:

```go
	proxyRepo := &repomocks.A2AProxyRepositoryMock{
		DeleteForAgentFunc: func(context.Context, string, string, string) error {
			f.steps = append(f.steps, "delete-proxies")
			return nil
		},
	}
	events := NewGatewayEventsService(f.hub)
	publisher := NewA2APublisher(nil, pubRepo, proxyRepo, artifactRepo, deploymentRepo, gatewayRepo, events, testLogger())
	f.svc = NewAgentManagerService(nil, oc, nil, nil, nil, nil, nil, nil, artifactRepo, nil, gatewayRepo, nil, nil, nil, nil,
		publisher, cardRepo, deploymentRepo, events, testLogger()).(*agentManagerService)
```

and in `TestDeleteAgentAPIArtifactBroadcastsLast` expect `[]string{"delete-queue", "delete-proxies", "delete-cards", "collect", "delete-artifact", "broadcast"}`.

- [ ] **Step 3: Verify the tests fail**

Run: `make test-run RUN='TestA2APublisher|TestRepublishA2AAgent|TestClearInheritedA2AState|TestDeleteAgentAPIArtifact|TestEnqueue' PKG=./services/`
Expected: FAIL to compile — `undefined: NewA2APublisher`, `undefined: a2aPublicationKey`.

- [ ] **Step 4: Write the publisher**

`services/a2a_publisher.go`:

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// a2aPublicationKey names one agent-environment gateway publication.
type a2aPublicationKey struct {
	OUID            string
	ProjectName     string
	AgentName       string
	EnvironmentName string
	EnvironmentUUID uuid.UUID
}

// a2aTeardown is an environment's gateway deletion, sent once its DB work has committed.
type a2aTeardown struct {
	artifactUUID uuid.UUID
	gatewayIDs   []string
}

// TxRunner opens one database transaction and runs fn inside it. Services take it instead of a
// *gorm.DB so a unit test can hand fn a sentinel and bind each repository mock to it.
type TxRunner func(ctx context.Context, fn func(tx *gorm.DB) error) error

// A2APublisher holds the gateway-publication steps agentManagerService and
// a2aProxyService share, so "does this environment publish?" has one answer.
type A2APublisher struct {
	tx             TxRunner
	pubRepo        repositories.A2APublicationRepository
	proxyRepo      repositories.A2AProxyRepository
	artifactRepo   repositories.ArtifactRepository
	deploymentRepo repositories.DeploymentRepository
	gatewayRepo    repositories.GatewayRepository
	events         *GatewayEventsService
	logger         *slog.Logger
}

// NewA2APublisher creates an A2APublisher.
func NewA2APublisher(
	tx TxRunner,
	pubRepo repositories.A2APublicationRepository,
	proxyRepo repositories.A2AProxyRepository,
	artifactRepo repositories.ArtifactRepository,
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
	events *GatewayEventsService,
	logger *slog.Logger,
) *A2APublisher {
	return &A2APublisher{
		tx:             tx,
		pubRepo:        pubRepo,
		proxyRepo:      proxyRepo,
		artifactRepo:   artifactRepo,
		deploymentRepo: deploymentRepo,
		gatewayRepo:    gatewayRepo,
		events:         events,
		logger:         logger,
	}
}

// a2aPublicationSourceFor says where the agent's gateway upstream comes from.
func a2aPublicationSourceFor(agent *models.AgentResponse) models.A2APublicationSource {
	if isExternalProvisioned(agent) {
		return models.A2APublicationSourceExternal
	}
	return models.A2APublicationSourcePlatform
}

// Enqueue queues the environment's publication; a non-nil tx makes it part of the caller's transaction.
func (p *A2APublisher) Enqueue(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, source models.A2APublicationSource, artifactUUID uuid.UUID,
) error {
	pub := &models.A2APublication{
		OUID:            key.OUID,
		ProjectName:     key.ProjectName,
		AgentName:       key.AgentName,
		EnvironmentName: key.EnvironmentName,
		EnvironmentUUID: key.EnvironmentUUID,
		ArtifactUUID:    artifactUUID,
		Source:          source,
	}
	if err := p.publications(tx).Enqueue(ctx, pub); err != nil {
		return fmt.Errorf("failed to queue the A2A gateway publication: %w", err)
	}
	return nil
}

// ShouldPublish is true for every internal A2A agent, and for an external one only while its environment is proxied.
func (p *A2APublisher) ShouldPublish(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, envUUID uuid.UUID,
) (bool, error) {
	if !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return false, nil
	}
	if !isExternalProvisioned(agent) {
		return true, nil
	}
	_, err := p.proxyRepo.GetByEnvUUID(ctx, ouID, projectName, agent.Name, envUUID)
	switch {
	case errors.Is(err, repositories.ErrA2AProxyNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return true, nil
}

// Publication returns the environment's publication row.
func (p *A2APublisher) Publication(ctx context.Context, key a2aPublicationKey) (*models.A2APublication, error) {
	return p.pubRepo.Get(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID)
}

// TeardownEnv removes one environment's publication and gateway deployments inside tx, keeping
// the artifact so its API keys survive a switch back; send the result once tx commits.
func (p *A2APublisher) TeardownEnv(
	ctx context.Context, tx *gorm.DB, key a2aPublicationKey, artifactUUID uuid.UUID,
) (a2aTeardown, error) {
	// Read first: the deployment rows name the gateways holding the Agent.
	gatewayIDs := collectA2AAgentDeletionTargets(p.deploymentRepo, p.gatewayRepo, artifactUUID, key.OUID, true, p.logger)
	if artifactUUID != uuid.Nil {
		if err := p.deploymentRepo.WithTx(tx).DeleteForArtifact(ctx, artifactUUID, key.OUID); err != nil {
			return a2aTeardown{}, fmt.Errorf("failed to delete the A2A agent's gateway deployments: %w", err)
		}
	}
	err := p.publications(tx).DeleteForAgentEnv(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID)
	if err != nil {
		return a2aTeardown{}, fmt.Errorf("failed to delete the A2A publication: %w", err)
	}
	return a2aTeardown{artifactUUID: artifactUUID, gatewayIDs: gatewayIDs}, nil
}

// SendTeardown tells each gateway to drop the torn-down Agent; best effort.
func (p *A2APublisher) SendTeardown(ctx context.Context, t a2aTeardown) {
	sendA2AAgentDeletion(ctx, p.events, t.artifactUUID, t.gatewayIDs, p.logger)
}

// DeleteForAgent drops the agent's publication and proxy rows; deleteAgentAPIArtifact tells the gateways.
func (p *A2APublisher) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error {
	var errs []error
	if err := p.pubRepo.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete A2A publications: %w", err))
	}
	if err := p.proxyRepo.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete A2A proxies: %w", err))
	}
	return errors.Join(errs...)
}

func (p *A2APublisher) publications(tx *gorm.DB) repositories.A2APublicationRepository {
	if tx == nil {
		return p.pubRepo
	}
	return p.pubRepo.WithTx(tx)
}
```

`TeardownEnv` always runs inside a transaction (its callers open one), so `deploymentRepo.WithTx(tx)` gets a non-nil tx.

- [ ] **Step 5: Delegate from agentManagerService**

In `services/agent_manager.go`:

1. Struct and constructor: replace `a2aPublicationRepo repositories.A2APublicationRepository` with `a2aPublisher *A2APublisher`, in the field list, the constructor parameter at the same position, and the assignment.

2. Replace `clearInheritedA2ACards` with the version below and rename its call site in `CreateAgent` (`:1589`):

```go
// clearInheritedA2AState drops card, publication and proxy rows a deleted same-named agent left behind.
func (s *agentManagerService) clearInheritedA2AState(ctx context.Context, ouID, projectName, agentName string) error {
	if s.a2aCardRepo != nil {
		if err := s.a2aCardRepo.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
			return fmt.Errorf("failed to clear leftover A2A agent card rows: %w", err)
		}
	}
	if s.a2aPublisher != nil {
		if err := s.a2aPublisher.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
			return fmt.Errorf("failed to clear leftover A2A publication rows: %w", err)
		}
	}
	return nil
}
```

3. In `deleteAgentAPIArtifact`, change the first bullet of the doc comment to "publication and proxy rows go first so the reconciler cannot re-publish", and replace the `s.a2aPublicationRepo` block with:

```go
	if s.a2aPublisher != nil {
		if err := s.a2aPublisher.DeleteForAgent(ctx, ouID, projectName, agentName); err != nil {
			s.logger.Warn("Failed to clear A2A publication and proxy rows for deleted agent",
				"agentName", agentName, "error", err)
		}
	}
```

4. Replace `enqueueA2APublication` (keep its long doc comment) and update `enqueueCreatedA2AAgent` and `republishA2AAgent`:

```go
func (s *agentManagerService) enqueueA2APublication(
	ctx context.Context, key a2aPublicationKey, source models.A2APublicationSource, artifactUUID uuid.UUID,
) {
	if s.a2aPublisher == nil {
		return
	}
	if err := s.a2aPublisher.Enqueue(ctx, nil, key, source, artifactUUID); err != nil {
		s.logger.Error("Failed to queue A2A agent gateway publication; the agent is deployed but will not reach its gateway until it is next deployed, promoted, or its deploy settings are saved",
			"agentName", key.AgentName, "environment", key.EnvironmentName, "error", err)
	}
}
```

In `enqueueCreatedA2AAgent`, replace the final call with:

```go
	key := a2aPublicationKey{OUID: ouID, ProjectName: projectName, AgentName: agentName, EnvironmentName: environmentName, EnvironmentUUID: envUUID}
	s.enqueueA2APublication(ctx, key, models.A2APublicationSourcePlatform, artifact.UUID)
```

Replace `republishA2AAgent`'s body (extend its doc comment with "An external agent publishes only while its environment is proxied."):

```go
	if s.a2aPublisher == nil || !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return nil
	}
	envUUID, err := uuid.Parse(environmentUUID)
	if err != nil {
		return fmt.Errorf("environment %q has an unparseable UUID %q: %w", environmentName, environmentUUID, err)
	}
	publish, err := s.a2aPublisher.ShouldPublish(ctx, agent, ouID, projectName, envUUID)
	if err != nil || !publish {
		return err
	}
	key := a2aPublicationKey{OUID: ouID, ProjectName: projectName, AgentName: agentName, EnvironmentName: environmentName, EnvironmentUUID: envUUID}
	s.enqueueA2APublication(ctx, key, a2aPublicationSourceFor(agent), artifactUUID)
	return nil
```

The subtype gate stays first, so a non-A2A agent never parses the UUID; that is today's behaviour.

- [ ] **Step 6: Wire it**

In `wiring/wire.go`:
- add `ProvideA2AProxyRepository,` after `ProvideA2AAgentCardRepository,` in `repositoryProviderSet`;
- add `ProvideTxRunner,` and `services.NewA2APublisher,` after `services.NewA2AAgentCardService,` in `serviceProviderSet`;
- add the providers next to `ProvideA2AAgentCardRepository` (add `"context"` to the imports if it is not there):

```go
func ProvideA2AProxyRepository(db *gorm.DB) repositories.A2AProxyRepository {
	return repositories.NewA2AProxyRepository(db)
}

// ProvideTxRunner opens database transactions for the services that hold no *gorm.DB.
func ProvideTxRunner(db *gorm.DB) services.TxRunner {
	return func(ctx context.Context, fn func(tx *gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}
```

Run: `make wire`
Expected: `wiring/wire_gen.go` builds an `a2APublisher` in both injectors and passes it to `NewAgentManagerService`.

- [ ] **Step 7: Run the tests**

Run: `make test-run RUN='TestA2APublisher|TestRepublishA2AAgent|TestClearInheritedA2AState|TestDeleteAgentAPIArtifact|TestEnqueue' PKG=./services/` then `make test-unit`
Expected: PASS.

- [ ] **Step 8: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./wiring/...
go build -tags=integration ./...
git add services/a2a_publisher.go services/a2a_publisher_unit_test.go services/agent_manager.go \
  services/a2a_deploy_enqueue_unit_test.go services/a2a_agent_deletion_unit_test.go wiring/wire.go wiring/wire_gen.go
git commit -m "Extract the A2A gateway publication steps into a shared publisher"
```

---

### Task 5: Derive gateway routes from an origin agent card

**Files:**
- Create: `agent-manager-service/services/a2a_proxy_routes.go`, `agent-manager-service/services/a2a_proxy_routes_unit_test.go`

**Interfaces:**
- Consumes: `A2ATransport`, `a2aTransportJSONRPC`, `a2aTransportHTTPJSON`, `a2aProtocolVersion` (`services/a2a_agent_deployment.go`); `a2aAgentCardPath` (`services/a2a_card_fetcher.go`).
- Produces:
  - `var errA2ANotProxyable error` (wrapped with the reason; the publication worker fails the row at once on it)
  - `func deriveA2AProxyRoutes(cardURL string, card json.RawMessage) (upstreamURL string, transports []A2ATransport, err error)`: transports in the order JSONRPC, HTTP+JSON; an empty `PathPrefix` means "omit".

The rules come from the spec and from the gateway source (see "Spec corrections" 2–4):
- The card URL is plain `http(s)` with no userinfo, query or fragment; its path ends in `/.well-known/agent-card.json`; the remaining base path is canonical with no trailing slash. The upstream is scheme, host and base path.
- Every `supportedInterfaces` entry carries a string `protocolBinding`. The gateway's card rewrite fails the whole card otherwise.
- A binding is fronted when all of its entries:
  - share one URL;
  - declare `protocolVersion` `"1.0"`, or none;
  - use the upstream's scheme, host (case-insensitive) and effective port, with a path at or under the upstream path.
- The remainder of that path becomes `pathPrefix`. `""` or `"/"` omits it. Otherwise it must be a canonical path with no trailing slash, `%`-escapes, braces or whitespace, and no query or fragment.
- No fronted binding is `errA2ANotProxyable`, naming each binding's reason.

- [ ] **Step 1: Write the tests**

`services/a2a_proxy_routes_unit_test.go`:

```go
package services

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const originCardURL = "https://agent.example/team/.well-known/agent-card.json"

// a2aTestCard is a minimal card offering the given interfaces.
func a2aTestCard(t *testing.T, interfaces ...map[string]any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": "Weather", "skills": []any{}, "supportedInterfaces": interfaces})
	require.NoError(t, err)
	return body
}

func a2aIface(binding, url string) map[string]any {
	return map[string]any{"protocolBinding": binding, "url": url, "protocolVersion": "1.0"}
}

func TestDeriveA2AProxyRoutesFrontsEachBindingAtItsPath(t *testing.T) {
	upstream, transports, err := deriveA2AProxyRoutes(originCardURL, a2aTestCard(t,
		a2aIface("JSONRPC", "https://agent.example/team/a2a/jsonrpc"),
		a2aIface("HTTP+JSON", "https://agent.example/team/a2a/rest"),
	))
	require.NoError(t, err)
	assert.Equal(t, "https://agent.example/team", upstream)
	assert.Equal(t, []A2ATransport{
		{ProtocolBinding: "JSONRPC", PathPrefix: "/a2a/jsonrpc"},
		{ProtocolBinding: "HTTP+JSON", PathPrefix: "/a2a/rest"},
	}, transports)
}

func TestDeriveA2AProxyRoutesTakesTheUpstreamFromTheCardURL(t *testing.T) {
	for cardURL, want := range map[string]string{
		"https://agent.example/.well-known/agent-card.json":         "https://agent.example",
		"http://agent.example:8080/a/b/.well-known/agent-card.json": "http://agent.example:8080/a/b",
	} {
		upstream, _, err := deriveA2AProxyRoutes(cardURL, a2aTestCard(t, a2aIface("JSONRPC", want+"/rpc")))
		require.NoError(t, err, cardURL)
		assert.Equal(t, want, upstream)
	}
}

// The gateway fetches the passthrough card from upstream + the well-known path, nothing else.
func TestDeriveA2AProxyRoutesRejectsACardURLTheGatewayCannotFetch(t *testing.T) {
	card := a2aTestCard(t, a2aIface("JSONRPC", "https://agent.example/rpc"))
	for _, cardURL := range []string{
		"https://agent.example/card.json",
		"https://agent.example/.well-known/agent-card.json?v=1",
		"https://agent.example//.well-known/agent-card.json",
		"ftp://agent.example/.well-known/agent-card.json",
		"https://user:pw@agent.example/.well-known/agent-card.json",
	} {
		_, _, err := deriveA2AProxyRoutes(cardURL, card)
		assert.ErrorIs(t, err, errA2ANotProxyable, cardURL)
	}
}

// Review Focus 5: the a2a-sdk default serves both bindings at the root.
func TestDeriveA2AProxyRoutesFrontsBothBindingsAtTheRoot(t *testing.T) {
	_, transports, err := deriveA2AProxyRoutes("https://agent.example/.well-known/agent-card.json", a2aTestCard(t,
		a2aIface("JSONRPC", "https://agent.example/"),
		a2aIface("HTTP+JSON", "https://agent.example"),
	))
	require.NoError(t, err)
	assert.Equal(t, []A2ATransport{{ProtocolBinding: "JSONRPC"}, {ProtocolBinding: "HTTP+JSON"}}, transports,
		"an empty remainder omits pathPrefix, and the gateway allows the same prefix on both bindings")
}

func TestDeriveA2AProxyRoutesLeavesABindingUnfronted(t *testing.T) {
	fronted := a2aIface("HTTP+JSON", "https://agent.example/team/rest")
	cases := map[string][]map[string]any{
		"its entries disagree on the URL": {
			a2aIface("JSONRPC", "https://agent.example/team/a"), a2aIface("JSONRPC", "https://agent.example/team/b"),
		},
		"another version": {{"protocolBinding": "JSONRPC", "url": "https://agent.example/team/rpc", "protocolVersion": "0.3"}},
		"a non-string version": {{"protocolBinding": "JSONRPC", "url": "https://agent.example/team/rpc", "protocolVersion": 1}},
		"another host":                    {a2aIface("JSONRPC", "https://other.example/team/rpc")},
		"another port":                    {a2aIface("JSONRPC", "https://agent.example:8443/team/rpc")},
		"another scheme":                  {a2aIface("JSONRPC", "http://agent.example/team/rpc")},
		"outside the upstream path":       {a2aIface("JSONRPC", "https://agent.example/other/rpc")},
		"a sibling of the upstream path":  {a2aIface("JSONRPC", "https://agent.example/teammate/rpc")},
	}
	for name, jsonrpc := range cases {
		t.Run(name, func(t *testing.T) {
			interfaces := append(append([]map[string]any{}, jsonrpc...), fronted)
			_, transports, err := deriveA2AProxyRoutes(originCardURL, a2aTestCard(t, interfaces...))
			require.NoError(t, err)
			assert.Equal(t, []A2ATransport{{ProtocolBinding: "HTTP+JSON", PathPrefix: "/rest"}}, transports)
		})
	}
}

// Review Focus 2: the gateway rejects these as pathPrefix and would fail the whole Agent.
func TestDeriveA2AProxyRoutesLeavesNonCanonicalPathsUnfronted(t *testing.T) {
	for _, link := range []string{
		"https://agent.example/team/a2a/jsonrpc/",
		"https://agent.example/team/a2a//jsonrpc",
		"https://agent.example/team/a2a/{id}",
		"https://agent.example/team/a2a%2Fjsonrpc",
		"https://agent.example/team/a2a%20rpc",
		"https://agent.example/team/rpc?x=1",
	} {
		_, transports, err := deriveA2AProxyRoutes(originCardURL, a2aTestCard(t,
			a2aIface("JSONRPC", link), a2aIface("HTTP+JSON", "https://agent.example/team/rest")))
		require.NoError(t, err, link)
		assert.Equal(t, []A2ATransport{{ProtocolBinding: "HTTP+JSON", PathPrefix: "/rest"}}, transports, link)
	}
}

func TestDeriveA2AProxyRoutesAcceptsUnversionedEntriesAndDefaultPorts(t *testing.T) {
	_, transports, err := deriveA2AProxyRoutes(originCardURL, a2aTestCard(t,
		map[string]any{"protocolBinding": "JSONRPC", "url": "https://AGENT.example:443/team/rpc"},
	))
	require.NoError(t, err)
	assert.Equal(t, []A2ATransport{{ProtocolBinding: "JSONRPC", PathPrefix: "/rpc"}}, transports)
}

func TestDeriveA2AProxyRoutesIgnoresGRPC(t *testing.T) {
	_, transports, err := deriveA2AProxyRoutes(originCardURL, a2aTestCard(t,
		a2aIface("GRPC", "https://grpc.example:50051"),
		a2aIface("JSONRPC", "https://agent.example/team/rpc"),
	))
	require.NoError(t, err)
	assert.Equal(t, []A2ATransport{{ProtocolBinding: "JSONRPC", PathPrefix: "/rpc"}}, transports)
}

func TestDeriveA2AProxyRoutesNeedsOneFrontedBinding(t *testing.T) {
	_, _, err := deriveA2AProxyRoutes(originCardURL, a2aTestCard(t, a2aIface("GRPC", "https://grpc.example:50051")))
	require.ErrorIs(t, err, errA2ANotProxyable)
	assert.Contains(t, err.Error(), "JSONRPC is not offered")
	assert.Contains(t, err.Error(), "HTTP+JSON is not offered")
}

// Review Focus 3: the gateway's card rewrite fails the whole card on such an entry.
func TestDeriveA2AProxyRoutesRequiresABindingOnEveryInterface(t *testing.T) {
	_, _, err := deriveA2AProxyRoutes(originCardURL, a2aTestCard(t,
		a2aIface("JSONRPC", "https://agent.example/team/rpc"),
		map[string]any{"url": "https://agent.example/team/other"},
	))
	require.ErrorIs(t, err, errA2ANotProxyable)
	assert.Contains(t, err.Error(), "supportedInterfaces[1]")
}
```

- [ ] **Step 2: Verify they fail**

Run: `make test-run RUN=TestDeriveA2AProxyRoutes PKG=./services/`
Expected: FAIL — `undefined: deriveA2AProxyRoutes`.

- [ ] **Step 3: Write the derivation**

`services/a2a_proxy_routes.go`:

```go
package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"
)

// errA2ANotProxyable means the registered card cannot be fronted by the gateway; retrying cannot help.
var errA2ANotProxyable = errors.New("the agent cannot be proxied through the gateway")

// a2aCardInterface is the part of a supportedInterfaces entry route derivation reads.
type a2aCardInterface struct {
	binding string
	url     string
	version json.RawMessage
}

// rewritesAsCurrentVersion is true when the gateway can rewrite the entry: it declares 1.0, or no version.
func (i a2aCardInterface) rewritesAsCurrentVersion() bool {
	if len(i.version) == 0 || string(i.version) == "null" {
		return true
	}
	version, ok := jsonString(i.version)
	return ok && (version == "" || version == a2aProtocolVersion)
}

// deriveA2AProxyRoutes takes the gateway upstream from the card URL and fronts each JSONRPC and
// HTTP+JSON binding the gateway can rewrite to its one endpoint for that binding. Entries on
// bindings left unfronted stay in the served card unrewritten.
func deriveA2AProxyRoutes(cardURL string, card json.RawMessage) (string, []A2ATransport, error) {
	upstream, err := a2aProxyUpstream(cardURL)
	if err != nil {
		return "", nil, err
	}
	interfaces, err := a2aCardInterfaces(card)
	if err != nil {
		return "", nil, err
	}
	var transports []A2ATransport
	var skipped []string
	for _, binding := range []string{a2aTransportJSONRPC, a2aTransportHTTPJSON} {
		transport, reason := a2aProxyTransport(upstream, binding, interfaces)
		if reason != "" {
			skipped = append(skipped, reason)
			continue
		}
		transports = append(transports, transport)
	}
	if len(transports) == 0 {
		return "", nil, fmt.Errorf("%w: no JSONRPC or HTTP+JSON interface can be fronted (%s)",
			errA2ANotProxyable, strings.Join(skipped, "; "))
	}
	return upstream.String(), transports, nil
}

// a2aProxyUpstream is the card URL without the well-known suffix, since the gateway fetches the
// passthrough card from the upstream base path plus that suffix.
func a2aProxyUpstream(cardURL string) (*url.URL, error) {
	u, err := url.Parse(cardURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("%w: the card URL must be a plain http(s) URL", errA2ANotProxyable)
	}
	if u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, a2aAgentCardPath) {
		return nil, fmt.Errorf("%w: the card URL must end in %s with no query, as the gateway fetches the card there",
			errA2ANotProxyable, a2aAgentCardPath)
	}
	base := strings.TrimSuffix(u.Path, a2aAgentCardPath)
	if base != "" && (strings.HasSuffix(base, "/") || path.Clean(base) != base || u.RawPath != "") {
		return nil, fmt.Errorf("%w: the card URL's path is not canonical", errA2ANotProxyable)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host, Path: base}, nil
}

// a2aCardInterfaces reads supportedInterfaces with exact key names, as the gateway's rewrite does.
func a2aCardInterfaces(card json.RawMessage) ([]a2aCardInterface, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(card, &doc); err != nil {
		return nil, fmt.Errorf("%w: the agent card is not a JSON object", errA2ANotProxyable)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(doc["supportedInterfaces"], &entries); err != nil {
		return nil, fmt.Errorf("%w: the agent card's supportedInterfaces is not a list", errA2ANotProxyable)
	}
	interfaces := make([]a2aCardInterface, 0, len(entries))
	for i, entry := range entries {
		// The gateway's card rewrite fails the whole card on an entry without a binding, fronted or not.
		binding, ok := jsonString(entry["protocolBinding"])
		if !ok || binding == "" {
			return nil, fmt.Errorf("%w: supportedInterfaces[%d] has no protocolBinding", errA2ANotProxyable, i)
		}
		link, _ := jsonString(entry["url"])
		interfaces = append(interfaces, a2aCardInterface{binding: binding, url: link, version: entry["protocolVersion"]})
	}
	return interfaces, nil
}

// a2aProxyTransport fronts one binding, or says why it cannot. The gateway rewrites every entry on a
// fronted binding to its one endpoint, so the entries must agree on the URL and declare 1.0 or nothing.
func a2aProxyTransport(upstream *url.URL, binding string, interfaces []a2aCardInterface) (A2ATransport, string) {
	var link string
	offered := false
	for _, iface := range interfaces {
		if iface.binding != binding {
			continue
		}
		if !iface.rewritesAsCurrentVersion() {
			return A2ATransport{}, fmt.Sprintf("%s declares protocolVersion %s, not %s", binding, iface.version, a2aProtocolVersion)
		}
		if offered && iface.url != link {
			return A2ATransport{}, binding + " interfaces disagree on the URL"
		}
		link, offered = iface.url, true
	}
	if !offered {
		return A2ATransport{}, binding + " is not offered"
	}
	prefix, ok := a2aPathPrefixUnder(upstream, link)
	if !ok {
		return A2ATransport{}, fmt.Sprintf("%s URL %q is not a plain path under %s", binding, link, upstream)
	}
	return A2ATransport{ProtocolBinding: binding, PathPrefix: prefix}, ""
}

// a2aPathPrefixUnder is link's path below the upstream's, as a gateway pathPrefix; "" omits it (the
// gateway's "/"). ok is false when link is elsewhere, or its remainder breaks the gateway's prefix rules.
func a2aPathPrefixUnder(upstream *url.URL, link string) (string, bool) {
	u, err := url.Parse(link)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", false
	}
	sameOrigin := strings.EqualFold(u.Scheme, upstream.Scheme) &&
		strings.EqualFold(u.Hostname(), upstream.Hostname()) &&
		effectivePort(u) == effectivePort(upstream)
	rest, under := strings.CutPrefix(u.Path, upstream.Path)
	if !sameOrigin || !under || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return "", false
	}
	if rest == "" || rest == "/" {
		return "", true
	}
	if path.Clean(rest) != rest || strings.ContainsAny(rest, "{}") || strings.ContainsFunc(rest, unicode.IsSpace) {
		return "", false
	}
	return rest, true
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// jsonString reads a JSON string; ok is false for anything else, absence included.
func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}
```

At `bc19012cc` the `services` package has no `jsonString` or `effectivePort`; if a later rebase brings one in, reuse it rather than redeclaring it.

- [ ] **Step 4: Run the tests**

Run: `make test-run RUN=TestDeriveA2AProxyRoutes PKG=./services/`
Expected: PASS.

- [ ] **Step 5: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/...
git add services/a2a_proxy_routes.go services/a2a_proxy_routes_unit_test.go
git commit -m "Derive A2A gateway routes from an external agent's card"
```

---

### Task 6: Agent YAML for proxied external agents

**Files:**
- Modify: `agent-manager-service/services/a2a_agent_deployment.go` (vhost comment `:69-72`, `A2AUpstream` `:90-93`, `A2ATransport` `:123-126`, `A2AAgentDeploymentInput` `:131-148`, `buildA2AAgentDeploymentYAML` `:180-222`)
- Test: `agent-manager-service/services/a2a_agent_deployment_test.go`, new golden `agent-manager-service/services/testdata/a2a_agent_proxied_golden.yaml`, `agent-manager-service/services/gateway_internal_service_unit_test.go`

**Interfaces:**
- Consumes: `models.UpstreamAuth`.
- Produces:
  - `A2AUpstream.Auth *models.UpstreamAuth` (`yaml:"auth,omitempty"`)
  - `A2ATransport.PathPrefix` gains `omitempty`
  - `A2AAgentDeploymentInput.Transports []A2ATransport` (nil keeps M1's `/rpc` and `/rest`)
  - `A2AAgentDeploymentInput.UpstreamAuth *models.UpstreamAuth`
  - Test helper `proxiedA2AInput() A2AAgentDeploymentInput` in `a2a_agent_deployment_test.go`.

- [ ] **Step 1: Write the tests**

Append to `services/a2a_agent_deployment_test.go`:

```go
// proxiedA2AInput is an external agent fronted at derived routes, with a stored upstream secret.
func proxiedA2AInput() A2AAgentDeploymentInput {
	in := a2aInput()
	in.ArtifactName = "checkout-weather-0192f4c19a7d7c3e"
	in.DisplayName, in.AgentName = "weather", "weather"
	in.UpstreamURL = "https://agent.example/team"
	in.Transports = []A2ATransport{{ProtocolBinding: "JSONRPC", PathPrefix: "/a2a/jsonrpc"}, {ProtocolBinding: "HTTP+JSON"}}
	authType, header, secret := models.AuthTypeAPIKey, "X-Upstream-Key", "c2VjcmV0"
	in.UpstreamAuth = &models.UpstreamAuth{Type: &authType, Header: &header, SecretRef: &secret}
	return in
}

func TestGenerateProxiedA2AAgentYAMLMatchesGolden(t *testing.T) {
	got, err := generateA2AAgentDeploymentYAML(proxiedA2AInput())
	require.NoError(t, err)

	goldenPath := filepath.Join("testdata", "a2a_agent_proxied_golden.yaml")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o600))
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// The gateway honours header and value only, and never sees secretRef: the internal API swaps it for value.
func TestGenerateProxiedA2AAgentYAMLCarriesTheSecretOnlyAsARef(t *testing.T) {
	got, err := generateA2AAgentDeploymentYAML(proxiedA2AInput())
	require.NoError(t, err)

	var doc struct {
		Spec struct {
			Upstream map[string]interface{} `yaml:"upstream"`
			A2A      struct {
				OperationConfigs struct {
					Transports []map[string]interface{} `yaml:"transports"`
				} `yaml:"operationConfigs"`
			} `yaml:"a2a"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(got), &doc))
	assert.Equal(t, map[string]interface{}{"type": "api-key", "header": "X-Upstream-Key", "secretRef": "c2VjcmV0"},
		doc.Spec.Upstream["auth"])
	assert.NotContains(t, got, "value:")
	assert.NotContains(t, got, "hostRewrite")

	transports := doc.Spec.A2A.OperationConfigs.Transports
	require.Len(t, transports, 2)
	assert.Equal(t, "/a2a/jsonrpc", transports[0]["pathPrefix"])
	assert.NotContains(t, transports[1], "pathPrefix", "the gateway rejects an explicit empty prefix")
}
```

Append to `services/gateway_internal_service_unit_test.go` (add `"encoding/base64"` and `"gopkg.in/yaml.v3"` to its imports):

```go
// The stored Agent YAML holds the proxy's upstream secret only as secretRef; the gateway is served the value.
func TestActiveAgentDeploymentDecryptsTheUpstreamSecret(t *testing.T) {
	key := make([]byte, 32)
	ciphertext, err := utils.EncryptBytes([]byte("s3cret"), key)
	require.NoError(t, err)
	in := proxiedA2AInput()
	secretRef := base64.StdEncoding.EncodeToString(ciphertext)
	in.UpstreamAuth.SecretRef = &secretRef
	stored, err := generateA2AAgentDeploymentYAML(in)
	require.NoError(t, err)
	repo := &repomocks.DeploymentRepositoryMock{
		GetCurrentByGatewayFunc: func(string, string, string) (*models.Deployment, error) {
			return &models.Deployment{Content: []byte(stored)}, nil
		},
	}
	svc := NewGatewayInternalAPIService(nil, nil, repo, nil, nil, key)

	got, err := svc.GetActiveAgentDeploymentByGateway(context.Background(), "artifact-1", "ou-1", "gw-1")
	require.NoError(t, err)

	var doc struct {
		Spec struct {
			Upstream struct {
				Auth map[string]interface{} `yaml:"auth"`
			} `yaml:"upstream"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(got["artifact-1"]), &doc))
	assert.Equal(t, "s3cret", doc.Spec.Upstream.Auth["value"])
	assert.Equal(t, "X-Upstream-Key", doc.Spec.Upstream.Auth["header"])
	assert.NotContains(t, doc.Spec.Upstream.Auth, "secretRef")
}
```

- [ ] **Step 2: Verify they fail**

Run: `make test-run RUN='TestGenerateProxiedA2AAgentYAML|TestActiveAgentDeploymentDecryptsTheUpstreamSecret' PKG=./services/`
Expected: FAIL to compile — `in.Transports undefined`.

- [ ] **Step 3: Extend the builder**

In `services/a2a_agent_deployment.go`:

Replace the `vhost` bullet of the `A2AAgentDeploymentYAML` comment:

```go
//   - vhost. A platform agent's clients reach the gateway through the agent's own
//     HTTPRoute (the a2a-gateway-route trait), which keeps the host they dialled
//     (<env>-<org>.<ingress host>, per agent environment); a proxied external
//     agent's arrive through the gateway's catch-all route on the gateway's own
//     host. A vhost naming either host would reject the other's requests.
```

Types:

```go
// A2AUpstream is the address the gateway dials, and for a proxied external agent the
// credential it adds to every call to it, card fetch included.
type A2AUpstream struct {
	URL  string               `yaml:"url" json:"url"`
	Auth *models.UpstreamAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// A2ATransport binds one A2A protocol to a gateway-facing path prefix. An empty prefix is
// omitted, which the gateway reads as "/"; it rejects an explicit "".
type A2ATransport struct {
	ProtocolBinding string `yaml:"protocolBinding" json:"protocolBinding"`
	PathPrefix      string `yaml:"pathPrefix,omitempty" json:"pathPrefix,omitempty"`
}
```

`A2AAgentDeploymentInput`, after `UpstreamURL` (change that field's comment to `// UpstreamURL is the release binding's service URL, or a proxied origin's base URL. Empty is an error.`):

```go
	// Transports are a proxied external agent's derived routes; nil keeps M1's /rpc and /rest.
	Transports []A2ATransport
	// UpstreamAuth is a proxied origin's credential, as a secretRef the internal API decrypts.
	UpstreamAuth *models.UpstreamAuth
```

In `buildA2AAgentDeploymentYAML`, before building `a2a`:

```go
	transports := in.Transports
	if transports == nil {
		transports = []A2ATransport{
			{ProtocolBinding: a2aTransportJSONRPC, PathPrefix: a2aPathPrefixJSONRPC},
			{ProtocolBinding: a2aTransportHTTPJSON, PathPrefix: a2aPathPrefixHTTPRPC},
		}
	}
```

Use `Transports: transports` in `A2AOperationConfigs`, and `Upstream: A2AUpstream{URL: upstreamURL, Auth: in.UpstreamAuth}`.

- [ ] **Step 4: Create the golden file and check it**

Run: `UPDATE_GOLDEN=1 make test-run RUN=TestGenerateProxiedA2AAgentYAMLMatchesGolden PKG=./services/`. Then open `services/testdata/a2a_agent_proxied_golden.yaml` and confirm:
- `spec.upstream` has `url: https://agent.example/team` and an `auth` map of `type: api-key`, `header: X-Upstream-Key`, `secretRef: c2VjcmV0`, with no `value`;
- the transports are `protocolBinding: JSONRPC` with `pathPrefix: /a2a/jsonrpc`, then a bare `protocolBinding: HTTP+JSON`;
- `context: /weather`;
- no `hostRewrite`, `vhost` or `resilience`.

Run: `git diff --exit-code services/testdata/a2a_agent_golden.yaml services/testdata/a2a_agent_card_cors_golden.yaml`
Expected: no diff. The platform output is byte-identical.

- [ ] **Step 5: Run the tests**

Run: `make test-run RUN='TestGenerateA2AAgent|TestBuildA2AAgent|TestGenerateProxiedA2AAgentYAML|TestActiveAgentDeployment|TestActiveArtifactDeployment|TestA2AAgentEnvArtifactName' PKG=./services/`
Expected: PASS. `TestGenerateA2AAgentDeploymentYAMLMatchesGolden` is the byte-identity check, and it runs without `UPDATE_GOLDEN`.

- [ ] **Step 6: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/...
git add services/a2a_agent_deployment.go services/a2a_agent_deployment_test.go \
  services/testdata/a2a_agent_proxied_golden.yaml services/gateway_internal_service_unit_test.go
git commit -m "Emit derived transports and upstream auth in the A2A Agent YAML"
```

---

### Task 7: Publication worker publishes proxied external agents

**Files:**
- Create: `agent-manager-service/services/a2a_worker_shared.go`
- Modify: `agent-manager-service/services/a2a_publication_reconciler.go`, `agent-manager-service/services/a2a_card_reconciler.go` (remove `sanitizeA2ACardError` and `a2aCardLastErrorMaxRunes` `:46-47`, `:240-247`; call the shared function)
- Modify: `agent-manager-service/services/a2a_publication_reconciler_test.go`, `agent-manager-service/services/a2a_card_reconciler_unit_test.go` (constant rename only), `agent-manager-service/tests/a2a_agent_lifecycle_test.go` (constructor call `:180-186`)
- Modify: `agent-manager-service/wiring/wire_gen.go` (via `make wire`)

**Interfaces:**
- Consumes:
  - Task 1: `A2APublication.Source`, `pubRepo.Get`, `repositories.A2APublicationClaimLease`.
  - Task 2: `A2AProxyRepository.GetByEnvUUID`, `(*models.A2AProxy).UpstreamAuth()`.
  - Task 3: `DeploymentRepository.DeleteForArtifact`.
  - Task 5: `deriveA2AProxyRoutes`, `errA2ANotProxyable`.
  - Task 6: `A2AAgentDeploymentInput.Transports`, `.UpstreamAuth`.
  - Existing: `A2ACardFetcher`, `cardRepo.Requeue`, `sendA2AAgentDeletion`.
- Produces:
  - `func sanitizeA2ALastError(msg string) string`; `const a2aLastErrorMaxRunes = 1024`
  - `func resolveIngressGateway(gatewayRepo repositories.GatewayRepository, ouID string, envUUID uuid.UUID) (*models.Gateway, error)` (Task 8 uses it)
  - `NewA2APublicationReconcilerService(pubRepo, cardRepo, proxyRepo repositories.A2AProxyRepository, deploymentRepo, gatewayRepo, agentConfigRepo, ocClient, fetcher A2ACardFetcher, events, logger)`. The new parameters go third (`proxyRepo`) and eighth (`fetcher`).

Behaviour (spec § Publication worker):
- `resolveUpstream` replaces `attemptPublish`'s first block (`:324-330`).
  - **platform:** unchanged.
  - **external:** load the proxy row (gone → `errA2ANotProxyable`) and the card row's `source_url` (gone → `errA2ANotProxyable`); fetch it guarded; run `deriveA2AProxyRoutes`; pass the stored header and ciphertext as `secretRef`.
- `errA2ANotProxyable` → `MarkFailed` with the reason, at once.
- Every `last_error` is sanitized.
- Deadlines: 30s per attempt; the batch stops at claim time + lease − 1 min; outcomes are recorded on `context.WithoutCancel`.
- After `MarkPublished`:
  - platform rows: card `Enqueue`, as today;
  - external rows: card `Requeue` (update-only); a missing row is logged and skipped.
- If `MarkPublished` returns superseded **and** the publication row no longer exists, the environment was torn down mid-attempt: delete the artifact's deployment rows and send a deletion to the gateway this attempt published to (Decision 10).

- [ ] **Step 1: Convert the harness to the constructor and add external fixtures**

In `services/a2a_publication_reconciler_test.go`:
- Add fields `proxyRepo *repomocks.A2AProxyRepositoryMock` and `fetcher *fakeA2ACardFetcher` to `a2aReconcilerHarness`.
- In `newA2AReconcilerHarness`, create `proxyRepo := &repomocks.A2AProxyRepositoryMock{}` and `fetcher := &fakeA2ACardFetcher{}` (nil funcs: platform rows must never reach them).
- Give the harness's `pubRepo` a default `GetFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) { return &models.A2APublication{}, nil }`. A superseded publish now asks whether the row still exists, and the existing `TestReconcilerQueuesNoCardForASupersededPublish` must keep seeing a plain re-enqueue.
- Store both in the returned harness.
- Replace the `svc: &a2aPublicationReconcilerService{…}` literal with:

```go
		svc: NewA2APublicationReconcilerService(pubRepo, cardRepo, proxyRepo, deploymentRepo, gatewayRepo,
			agentConfigRepo, ocClient, fetcher, NewGatewayEventsService(hub), testLogger()).(*a2aPublicationReconcilerService),
```

Add `"unicode/utf8"` and `"errors"` to the imports if they are not there yet. Then append:

```go
const originTestCardURL = "https://agent.example/.well-known/agent-card.json"

const originTestCard = `{"name":"Weather","skills":[],"supportedInterfaces":[` +
	`{"url":"https://agent.example/rpc","protocolBinding":"JSONRPC","protocolVersion":"1.0"}]}`

// withExternalOrigin makes pub an external row whose environment is proxied with a stored secret.
func (h *a2aReconcilerHarness) withExternalOrigin(pub *models.A2APublication) {
	pub.Source = models.A2APublicationSourceExternal
	h.ocClient.GetReleaseBindingServiceURLFunc = nil // an external agent has no release binding
	h.proxyRepo.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{UpstreamAuthHeader: "X-Upstream-Key", UpstreamAuthSecret: "c2VjcmV0"}, nil
	}
	h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{Source: models.A2AAgentCardSourceExternal, SourceURL: originTestCardURL}, nil
	}
	h.cardRepo.EnqueueFunc = nil // an upsert would rewrite the registered source
	h.cardRepo.RequeueFunc = func(context.Context, string, string, string, string) error { return nil }
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return json.RawMessage(originTestCard), nil
	}
}

func TestReconcilerPublishesAnExternalAgentFromItsOriginCard(t *testing.T) {
	h := newA2AReconcilerHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.fetcher.calls, 1)
	assert.Equal(t, fakeFetchCall{URL: originTestCardURL, Guarded: true}, h.fetcher.calls[0],
		"the origin is SSRF-checked again at every publish")
	created := h.deploymentRepo.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	var doc A2AAgentDeploymentYAML
	require.NoError(t, yaml.Unmarshal(created[0].Deployment.Content, &doc))
	assert.Equal(t, "https://agent.example", doc.Spec.Upstream.URL)
	assert.Equal(t, []A2ATransport{{ProtocolBinding: "JSONRPC", PathPrefix: "/rpc"}}, doc.Spec.A2A.OperationConfigs.Transports)
	require.NotNil(t, doc.Spec.Upstream.Auth)
	assert.Equal(t, "X-Upstream-Key", *doc.Spec.Upstream.Auth.Header)
	assert.Equal(t, "c2VjcmV0", *doc.Spec.Upstream.Auth.SecretRef)

	published := h.pubRepo.MarkPublishedCalls()
	require.Len(t, published, 1)
	assert.Equal(t, "https://agent.example", published[0].UpstreamURL)
	requeued := h.cardRepo.RequeueCalls()
	require.Len(t, requeued, 1, "the parked card row is made due in place")
	assert.Equal(t, pub.EnvironmentName, requeued[0].EnvironmentName)
}

func TestReconcilerFailsAnUnproxyableExternalRowAtOnce(t *testing.T) {
	h := newA2AReconcilerHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{Source: models.A2AAgentCardSourceExternal, SourceURL: "https://agent.example/card.json"}, nil
	}

	h.svc.publishOne(context.Background(), pub)

	failed := h.pubRepo.MarkFailedCalls()
	require.Len(t, failed, 1, "no attempt budget is spent on a card that can never be fronted")
	assert.Contains(t, failed[0].LastErr, "/.well-known/agent-card.json")
	assert.Empty(t, h.pubRepo.MarkAttemptFailedCalls())
	assert.Empty(t, h.deploymentRepo.CreateWithLimitEnforcementCalls())
}

func TestReconcilerFailsAnExternalRowWhoseProxyIsGone(t *testing.T) {
	h := newA2AReconcilerHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.proxyRepo.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return nil, repositories.ErrA2AProxyNotFound
	}

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.pubRepo.MarkFailedCalls(), 1)
	assert.Empty(t, h.fetcher.calls)
}

func TestReconcilerRetriesWhileTheOriginIsUnreachable(t *testing.T) {
	h := newA2AReconcilerHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("could not reach the agent card URL")
	}

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.pubRepo.MarkAttemptFailedCalls(), 1)
	assert.Empty(t, h.pubRepo.MarkFailedCalls())
}

// Review Focus 1: a switch to direct committed while this attempt was publishing.
func TestReconcilerWithdrawsAnAgentTornDownMidPublish(t *testing.T) {
	h := newA2AReconcilerHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.pubRepo.MarkPublishedFunc = func(context.Context, models.A2APublication, string) error {
		return repositories.ErrA2APublicationSuperseded
	}
	h.pubRepo.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return nil, repositories.ErrA2APublicationNotFound
	}
	h.deploymentRepo.DeleteForArtifactFunc = func(context.Context, uuid.UUID, string) error { return nil }

	h.svc.publishOne(context.Background(), pub)

	deleted := h.deploymentRepo.DeleteForArtifactCalls()
	require.Len(t, deleted, 1)
	assert.Equal(t, pub.ArtifactUUID, deleted[0].ArtifactUUID)
	require.Len(t, h.hub.published, 2, "the deploy event, then its withdrawal")
	assert.Equal(t, pub.ArtifactUUID.String(), deletedAgentID(t, h.hub.published[1]))
	assert.Empty(t, h.cardRepo.RequeueCalls())
}

// A plain re-enqueue also reports superseded; that row is published again next tick, untouched.
func TestReconcilerLeavesAReEnqueuedRowForTheNextTick(t *testing.T) {
	h := newA2AReconcilerHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.pubRepo.MarkPublishedFunc = func(context.Context, models.A2APublication, string) error {
		return repositories.ErrA2APublicationSuperseded
	}
	h.pubRepo.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return &pub, nil
	}
	// DeleteForArtifactFunc is nil: withdrawing would panic.

	h.svc.publishOne(context.Background(), pub)

	assert.Len(t, h.hub.published, 1)
}

func TestReconcilerSkipsTheCardTriggerWhenTheExternalRowIsGone(t *testing.T) {
	h := newA2AReconcilerHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.cardRepo.RequeueFunc = func(context.Context, string, string, string, string) error {
		return repositories.ErrA2AAgentCardNotFound
	}

	h.svc.publishOne(context.Background(), pub)

	require.Len(t, h.pubRepo.MarkPublishedCalls(), 1)
	assert.Empty(t, h.ocClient.GetComponentCalls(), "nothing was written, so nothing to drop")
}

func TestReconcilerStoresAValidUTF8LastError(t *testing.T) {
	h := newA2AReconcilerHarness("")
	h.ocClient.GetReleaseBindingServiceURLFunc = func(context.Context, string, string, string) (string, error) {
		return "", errors.New("lookup \xff failed")
	}

	h.svc.publishOne(context.Background(), pendingPublication())

	failed := h.pubRepo.MarkAttemptFailedCalls()
	require.Len(t, failed, 1)
	assert.True(t, utf8.ValidString(failed[0].LastErr))
}

func serviceURLBlocksUntilCancelled(h *a2aReconcilerHarness) {
	h.ocClient.GetReleaseBindingServiceURLFunc = func(ctx context.Context, _, _, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
}

func TestReconcilerBoundsEachPublishAttempt(t *testing.T) {
	h := newA2AReconcilerHarness("")
	serviceURLBlocksUntilCancelled(h)
	h.svc.attemptTimeout = 20 * time.Millisecond

	runWithin(t, 2*time.Second, func() { h.svc.publishOne(context.Background(), pendingPublication()) })

	require.Len(t, h.pubRepo.MarkAttemptFailedCalls(), 1, "a timed-out attempt is still recorded")
}

// Rows still running when the lease ends could be re-claimed and published twice.
func TestReconcilerStopsTheBatchBeforeTheLeaseEnds(t *testing.T) {
	h := newA2AReconcilerHarness("")
	serviceURLBlocksUntilCancelled(h)
	h.svc.batchBudget = 20 * time.Millisecond
	h.pubRepo.ClaimDueFunc = func(context.Context, time.Time, int) ([]models.A2APublication, error) {
		return []models.A2APublication{pendingPublication(), pendingPublication(), pendingPublication()}, nil
	}
	h.pubRepo.FindPublishedFunc = func(context.Context, uuid.UUID, int) ([]models.A2APublication, error) {
		return []models.A2APublication{}, nil
	}

	runWithin(t, 2*time.Second, func() { h.svc.RunOnce(context.Background()) })

	assert.Len(t, h.ocClient.GetReleaseBindingServiceURLCalls(), 1)
	assert.Len(t, h.pubRepo.MarkAttemptFailedCalls(), 1, "only the cut-off row is charged")
}

func TestPublicationBatchFitsInsideTheClaimLease(t *testing.T) {
	assert.Less(t, a2aPublicationBatchBudget, repositories.A2APublicationClaimLease)
}
```

`runWithin`, `fakeA2ACardFetcher`, `fakeFetchCall` and `deletedAgentID` already exist in this package's test files.

In `services/a2a_card_reconciler_unit_test.go`, rename `a2aCardLastErrorMaxRunes` to `a2aLastErrorMaxRunes` (two uses in `TestCardReconcilerCapsTheLastErrorLength`).

- [ ] **Step 2: Verify the tests fail**

Run: `make test-run RUN='TestReconciler|TestPublicationBatch|TestDriftCheck|TestCardReconciler' PKG=./services/`
Expected: FAIL to compile — wrong argument count for `NewA2APublicationReconcilerService`, `undefined: a2aLastErrorMaxRunes`, `h.svc.attemptTimeout undefined`.

- [ ] **Step 3: Move the shared helpers**

`services/a2a_worker_shared.go` (Apache header first):

```go
package services

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// a2aLastErrorMaxRunes keeps a stored last_error readable in the console.
const a2aLastErrorMaxRunes = 1024

// sanitizeA2ALastError makes msg storable in a Postgres TEXT column (valid UTF-8, no NUL) and bounded in length.
func sanitizeA2ALastError(msg string) string {
	msg = strings.ReplaceAll(strings.ToValidUTF8(msg, "�"), "\x00", "")
	if runes := []rune(msg); len(runes) > a2aLastErrorMaxRunes {
		msg = string(runes[:a2aLastErrorMaxRunes-1]) + "…"
	}
	return msg
}

// resolveIngressGateway picks the environment's gateway. An A2A agent is inbound traffic, so it
// belongs on the environment's INGRESS gateway, the slot a REST agent's api-configuration trait
// targets, not on an egress gateway, which hosts outbound LLM/MCP artifacts.
func resolveIngressGateway(gatewayRepo repositories.GatewayRepository, ouID string, envUUID uuid.UUID) (*models.Gateway, error) {
	envID := envUUID.String()
	gateways, err := gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
		OrganizationID:      ouID,
		FunctionalityTypeIn: models.IngressGatewayRoles,
		EnvironmentID:       &envID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateways for environment %s: %w", envID, err)
	}
	for _, gw := range gateways {
		if gw != nil {
			return gw, nil
		}
	}
	// The bootstrap job registers an environment's one ingress gateway, so absence is a timing condition: retry.
	return nil, fmt.Errorf("no ingress gateway is mapped to environment %s yet", envID)
}
```

In `services/a2a_card_reconciler.go`, delete `a2aCardLastErrorMaxRunes` and `sanitizeA2ACardError`, and change the call in `recordAttemptFailure` to `sanitizeA2ALastError(cause.Error())`.

- [ ] **Step 4: Rework the publication reconciler**

In `services/a2a_publication_reconciler.go`:

Constants: add after `a2aPublicationAttemptBudget`:

```go
	// a2aPublicationAttemptTimeout bounds one row's OpenChoreo reads, origin card fetch and gateway writes.
	a2aPublicationAttemptTimeout = 30 * time.Second
	// a2aPublicationBatchBudget leaves the claim lease a minute to record the last outcome.
	a2aPublicationBatchBudget = repositories.A2APublicationClaimLease - time.Minute
```

Struct: add `proxyRepo repositories.A2AProxyRepository`, `fetcher A2ACardFetcher`, and, after `driftCursor`, the two fields below. In the constructor, take `proxyRepo` as the third and `fetcher` as the eighth parameter, and initialise `attemptTimeout: a2aPublicationAttemptTimeout, batchBudget: a2aPublicationBatchBudget`.

```go
	attemptTimeout time.Duration
	batchBudget    time.Duration
```

Replace `runCycle`, `publishOne`, `enqueueCardFetch`, `recordAttemptFailure`, `recordWaiting`, `attemptPublish` and `resolveGateway`, and add the new helpers (`checkUpstreamDrift`, `refetchCardOfNewRelease`, `dropCardIfAgentGone` and `logSuperseded` are unchanged):

```go
// runCycle publishes the due batch. ClaimDue leases each row, so replicas
// running concurrently never take the same row.
func (s *a2aPublicationReconcilerService) runCycle(ctx context.Context) {
	claimedAt := time.Now()
	due, err := s.pubRepo.ClaimDue(ctx, claimedAt, a2aReconcilerBatch)
	if err != nil {
		s.logger.Error("Failed to claim due A2A publications", "error", err)
		return
	}
	s.publishBatch(ctx, claimedAt, due)
	s.checkUpstreamDrift(ctx)
}

// publishBatch stops before the claim lease runs out, so another replica never re-claims a row mid-publish.
func (s *a2aPublicationReconcilerService) publishBatch(ctx context.Context, claimedAt time.Time, due []models.A2APublication) {
	batchCtx, cancel := context.WithDeadline(ctx, claimedAt.Add(s.batchBudget))
	defer cancel()
	for i, pub := range due {
		if batchCtx.Err() != nil {
			s.logger.Info("A2A publication batch ran out of lease; leaving the rest for a later claim", "remaining", len(due)-i)
			return
		}
		s.publishOne(batchCtx, pub)
	}
}

// publishOne emits one agent-environment pair's Agent resource, or schedules a
// retry when it cannot yet.
func (s *a2aPublicationReconcilerService) publishOne(ctx context.Context, pub models.A2APublication) {
	attemptCtx, cancel := context.WithTimeout(ctx, s.attemptTimeout)
	defer cancel()
	// The outcome must be recorded even when the attempt ran out of time.
	ctx = context.WithoutCancel(ctx)
	published, err := s.attemptPublish(attemptCtx, pub)
	if err != nil {
		s.recordAttemptFailure(ctx, pub, err)
		return
	}
	err = s.pubRepo.MarkPublished(ctx, pub, published.upstreamURL)
	switch {
	case errors.Is(err, repositories.ErrA2APublicationSuperseded):
		s.logSuperseded(pub)
		s.withdrawIfTornDown(ctx, pub, published.gatewayID)
	case err != nil:
		s.logger.Error("Published A2A agent but failed to mark the queue row",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
	default:
		s.enqueueCardFetch(ctx, pub)
	}
}

// withdrawIfTornDown takes back an Agent published after its environment was torn down meanwhile:
// the teardown keeps the artifact, so this attempt's deployments row would otherwise stay served.
func (s *a2aPublicationReconcilerService) withdrawIfTornDown(ctx context.Context, pub models.A2APublication, gatewayID string) {
	_, err := s.pubRepo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	if !errors.Is(err, repositories.ErrA2APublicationNotFound) {
		return
	}
	s.logger.Info("A2A publication was torn down during the attempt; withdrawing the Agent it published",
		"agentName", pub.AgentName, "environment", pub.EnvironmentName)
	if err := s.deploymentRepo.DeleteForArtifact(ctx, pub.ArtifactUUID, pub.OUID); err != nil {
		s.logger.Error("Failed to delete the deployment of a torn-down A2A publication",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
	}
	sendA2AAgentDeletion(ctx, s.events, pub.ArtifactUUID, []string{gatewayID}, s.logger)
}

// enqueueCardFetch queues a fetch of the card the gateway now serves; best effort.
func (s *a2aPublicationReconcilerService) enqueueCardFetch(ctx context.Context, pub models.A2APublication) {
	if pub.Source == models.A2APublicationSourceExternal {
		s.requeueExternalCard(ctx, pub)
		return
	}
	card := &models.A2AAgentCard{
		OUID:            pub.OUID,
		ProjectName:     pub.ProjectName,
		AgentName:       pub.AgentName,
		EnvironmentName: pub.EnvironmentName,
		EnvironmentUUID: pub.EnvironmentUUID,
		Source:          models.A2AAgentCardSourcePlatform,
	}
	if err := s.cardRepo.Enqueue(ctx, card); err != nil {
		s.logger.Warn("Published A2A agent but failed to queue its card fetch",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
		return
	}
	s.dropCardIfAgentGone(ctx, pub)
}

// requeueExternalCard makes the parked external row due in place: an upsert would rewrite its
// source and registered URL, or recreate a row whose source was just removed.
func (s *a2aPublicationReconcilerService) requeueExternalCard(ctx context.Context, pub models.A2APublication) {
	err := s.cardRepo.Requeue(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardNotFound):
		s.logger.Info("Published A2A agent has no card row any more; skipping its card fetch",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName)
		return
	case err != nil:
		s.logger.Warn("Published A2A agent but failed to queue its card fetch",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
		return
	}
	s.dropCardIfAgentGone(ctx, pub)
}

// recordAttemptFailure waits out a not-ready binding, fails a row that can never be proxied,
// retries other failures within the budget, and gives up past it.
func (s *a2aPublicationReconcilerService) recordAttemptFailure(ctx context.Context, pub models.A2APublication, cause error) {
	if errors.Is(cause, errUpstreamNotReady) {
		s.recordWaiting(ctx, pub, cause)
		return
	}
	lastErr := sanitizeA2ALastError(cause.Error())
	if errors.Is(cause, errA2ANotProxyable) || pub.AttemptCount+1 >= a2aPublicationAttemptBudget {
		s.logger.Error("A2A agent cannot reach its gateway; giving up",
			"agentName", pub.AgentName, "environment", pub.EnvironmentName,
			"attempts", pub.AttemptCount+1, "error", cause)
		err := s.pubRepo.MarkFailed(ctx, pub, lastErr)
		switch {
		case errors.Is(err, repositories.ErrA2APublicationSuperseded):
			s.logSuperseded(pub)
		case err != nil:
			s.logger.Error("Failed to mark A2A publication failed", "error", err)
		}
		return
	}
	s.logger.Debug("A2A agent not publishable yet, will retry",
		"agentName", pub.AgentName, "environment", pub.EnvironmentName,
		"attempt", pub.AttemptCount+1, "reason", cause)
	err := s.pubRepo.MarkAttemptFailed(ctx, pub, lastErr, time.Now().Add(a2aReconcilerRetryIn))
	switch {
	case errors.Is(err, repositories.ErrA2APublicationSuperseded):
		s.logSuperseded(pub)
	case err != nil:
		s.logger.Error("Failed to schedule A2A publication retry", "error", err)
	}
}

// recordWaiting reschedules a row whose binding has no ServiceURL yet.
func (s *a2aPublicationReconcilerService) recordWaiting(ctx context.Context, pub models.A2APublication, cause error) {
	s.logger.Debug("A2A agent binding not ready yet, will check again",
		"agentName", pub.AgentName, "environment", pub.EnvironmentName)
	err := s.pubRepo.MarkWaiting(ctx, pub, sanitizeA2ALastError(cause.Error()), time.Now().Add(a2aReconcilerRetryIn))
	switch {
	case errors.Is(err, repositories.ErrA2APublicationSuperseded):
		s.logSuperseded(pub)
	case err != nil:
		s.logger.Error("Failed to schedule A2A publication retry", "error", err)
	}
}

// a2aUpstream is where the gateway forwards to; nil transports keep M1's /rpc and /rest.
type a2aUpstream struct {
	url        string
	transports []A2ATransport
	auth       *models.UpstreamAuth
}

// a2aPublishResult is where an attempt published the Agent.
type a2aPublishResult struct {
	upstreamURL string
	gatewayID   string
}

// resolveUpstream reads a platform agent's release binding, or derives a proxied external agent's from its origin card.
func (s *a2aPublicationReconcilerService) resolveUpstream(ctx context.Context, pub models.A2APublication) (a2aUpstream, error) {
	if pub.Source == models.A2APublicationSourceExternal {
		return s.resolveExternalUpstream(ctx, pub)
	}
	upstreamURL, err := s.ocClient.GetReleaseBindingServiceURL(ctx, pub.OUID, pub.AgentName, pub.EnvironmentName)
	if err != nil {
		return a2aUpstream{}, fmt.Errorf("failed to read release binding service URL: %w", err)
	}
	if upstreamURL == "" {
		return a2aUpstream{}, errUpstreamNotReady
	}
	return a2aUpstream{url: upstreamURL}, nil
}

// resolveExternalUpstream re-derives the routes from the origin's card at every publish, through the
// SSRF-guarded client: the URL was checked when saved, but its host may resolve elsewhere since.
func (s *a2aPublicationReconcilerService) resolveExternalUpstream(ctx context.Context, pub models.A2APublication) (a2aUpstream, error) {
	proxy, err := s.proxyRepo.GetByEnvUUID(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AProxyNotFound) {
		return a2aUpstream{}, fmt.Errorf("%w: the environment is no longer proxied", errA2ANotProxyable)
	}
	if err != nil {
		return a2aUpstream{}, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	card, err := s.cardRepo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
	if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
		return a2aUpstream{}, fmt.Errorf("%w: no card URL is registered for the environment", errA2ANotProxyable)
	}
	if err != nil {
		return a2aUpstream{}, fmt.Errorf("failed to read the agent card: %w", err)
	}
	body, err := s.fetcher.Fetch(ctx, card.SourceURL, true)
	if err != nil {
		return a2aUpstream{}, fmt.Errorf("failed to fetch the origin agent card: %w", err)
	}
	upstreamURL, transports, err := deriveA2AProxyRoutes(card.SourceURL, body)
	if err != nil {
		return a2aUpstream{}, err
	}
	return a2aUpstream{url: upstreamURL, transports: transports, auth: proxy.UpstreamAuth()}, nil
}

// attemptPublish emits the Agent resource and reports where it went.
func (s *a2aPublicationReconcilerService) attemptPublish(ctx context.Context, pub models.A2APublication) (a2aPublishResult, error) {
	upstream, err := s.resolveUpstream(ctx, pub)
	if err != nil {
		return a2aPublishResult{}, err
	}
	gateway, err := resolveIngressGateway(s.gatewayRepo, pub.OUID, pub.EnvironmentUUID)
	if err != nil {
		return a2aPublishResult{}, err
	}

	// The policy chain is the persisted per-environment config run through the
	// same buildPolicies a chat/custom agent gets, plus the one header every A2A
	// client must send. The public card route runs its own list.
	cfg, err := s.agentConfigRepo.Get(ctx, pub.OUID, pub.ProjectName, pub.AgentName, pub.EnvironmentName)
	if err != nil {
		return a2aPublishResult{}, fmt.Errorf("failed to load agent config: %w", err)
	}
	policies := buildPolicies(withA2AVersionHeader(resolveAPIConfig(cfg, nil, nil, nil, nil, false)))

	yamlStr, err := generateA2AAgentDeploymentYAML(A2AAgentDeploymentInput{
		ArtifactName: a2aAgentEnvArtifactName(pub.ProjectName, pub.AgentName, pub.EnvironmentUUID.String()),
		DisplayName:  pub.AgentName,
		AgentName:    pub.AgentName,
		UpstreamURL:  upstream.url,
		Transports:   upstream.transports,
		UpstreamAuth: upstream.auth,
		Policies:     policies,
		CardPolicies: buildCardPolicies(cfg.EffectiveCardCORS()),
	})
	if err != nil {
		return a2aPublishResult{}, err
	}
```

From here to the end of the function, keep the existing deployments-row and broadcast code unchanged, changing only its returns. Each `return "", fmt.Errorf(...)` becomes `return a2aPublishResult{}, fmt.Errorf(...)`, the log line uses `upstream.url`, and the final return is:

```go
	return a2aPublishResult{upstreamURL: upstream.url, gatewayID: gateway.UUID.String()}, nil
}
```

Delete `resolveGateway`; the old method's doc comment now lives on `resolveIngressGateway`.

- [ ] **Step 5: Update the integration test and wiring**

In `tests/a2a_agent_lifecycle_test.go`, replace the constructor call with:

```go
	reconciler := services.NewA2APublicationReconcilerService(
		pubRepo, cardRepo, repositories.NewA2AProxyRepository(gdb), deploymentRepo, gatewayRepo,
		repositories.NewAgentConfigRepo(gdb),
		openChoreoClient,
		services.NewA2ACardFetcher(),
		services.NewGatewayEventsService(hub),
		testLifecycleLogger(),
	)
```

Run: `make wire`
Expected: both injectors pass `a2AProxyRepository` and `a2ACardFetcher` to `NewA2APublicationReconcilerService`.

- [ ] **Step 6: Run the tests**

```bash
make test-run RUN='TestReconciler|TestPublicationBatch|TestDriftCheck|TestCardReconciler' PKG=./services/
make test-unit
go build -tags=integration ./...
env <int-env> go test -tags=integration -count=1 -run TestA2AAgentLifecycle ./tests/
```

Expected: PASS. The lifecycle test proves platform publishing is unchanged end to end. If it needs more setup than `<int-env>` gives it, run it through `make test-integration` instead.

- [ ] **Step 7: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./tests/... ./wiring/...
git add services/a2a_worker_shared.go services/a2a_publication_reconciler.go services/a2a_publication_reconciler_test.go \
  services/a2a_card_reconciler.go services/a2a_card_reconciler_unit_test.go tests/a2a_agent_lifecycle_test.go \
  wiring/wire_gen.go
git commit -m "Publish proxied external A2A agents from their origin card"
```

---

### Task 8: Card worker fetches a proxied card through the gateway

**Files:**
- Modify: `agent-manager-service/services/a2a_card_reconciler.go` (struct and constructor `:67-97`, `attemptFetch` `:178-189`)
- Test: `agent-manager-service/services/a2a_card_reconciler_unit_test.go`
- Generate: `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: Task 2 `A2AProxyRepository.GetByEnvUUID`; Task 7 `resolveIngressGateway`; `buildPublicProxyURL` (`services/agent_configuration_service.go:344`); `a2aAgentCardPath`.
- Produces: `NewA2ACardReconcilerService(cardRepo repositories.A2AAgentCardRepository, proxyRepo repositories.A2AProxyRepository, gatewayRepo repositories.GatewayRepository, fetcher A2ACardFetcher, ocClient client.OpenChoreoClient, logger *slog.Logger)`.

An external row whose environment has an `a2a_proxies` row is fetched from `{ingress gateway vhost}/{agent}/.well-known/agent-card.json` with the plain client. Without a proxy row it is fetched from `source_url`, guarded, as today. `MarkFetched` still receives the fetched URL; Task 3 made the repository keep an external row's registered `source_url`.

- [ ] **Step 1: Write the tests**

In `services/a2a_card_reconciler_unit_test.go`:
- Add `proxies *repomocks.A2AProxyRepositoryMock` and `gateways *repomocks.GatewayRepositoryMock` to `cardReconcilerHarness`.
- In `newCardReconcilerHarness`, build them as below and change the constructor call to `NewA2ACardReconcilerService(repo, proxies, gateways, fetcher, oc, testLogger())`.

```go
	proxies := &repomocks.A2AProxyRepositoryMock{
		GetByEnvUUIDFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
			return nil, repositories.ErrA2AProxyNotFound
		},
	}
	gateways := &repomocks.GatewayRepositoryMock{
		ListWithFiltersFunc: func(repositories.GatewayFilterOptions) ([]*models.Gateway, error) {
			return []*models.Gateway{{UUID: uuid.New(), Vhost: "https://gw.example/"}}, nil
		},
	}
```

Append:

```go
// Only the gateway's copy of a proxied card advertises gateway URLs.
func TestCardReconcilerFetchesAProxiedCardThroughTheGateway(t *testing.T) {
	h := newCardReconcilerHarness("")
	h.ocClient.GetReleaseBindingRolloutFunc = nil // external rows never ask OpenChoreo
	h.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{}, nil
	}
	var asked repositories.GatewayFilterOptions
	h.gateways.ListWithFiltersFunc = func(opts repositories.GatewayFilterOptions) ([]*models.Gateway, error) {
		asked = opts
		return []*models.Gateway{{UUID: uuid.New(), Vhost: "https://gw.example/"}}, nil
	}
	row := pendingCard(models.A2AAgentCardSourceExternal)
	row.SourceURL = "https://agent.example/.well-known/agent-card.json"

	h.svc.fetchOne(context.Background(), row)

	require.Len(t, h.fetcher.calls, 1)
	assert.Equal(t, fakeFetchCall{URL: "https://gw.example/trip-planner/.well-known/agent-card.json", Guarded: false},
		h.fetcher.calls[0], "a platform-derived URL, loopback in local setups")
	require.NotNil(t, asked.EnvironmentID)
	assert.Equal(t, row.EnvironmentUUID.String(), *asked.EnvironmentID)
	assert.Equal(t, models.IngressGatewayRoles, asked.FunctionalityTypeIn)
	require.Len(t, h.repo.MarkFetchedCalls(), 1)
}

func TestCardReconcilerRetriesWhenTheProxyLookupFails(t *testing.T) {
	h := newCardReconcilerHarness("")
	h.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return nil, assert.AnError
	}
	row := pendingCard(models.A2AAgentCardSourceExternal)
	row.SourceURL = "https://agent.example/.well-known/agent-card.json"

	h.svc.fetchOne(context.Background(), row)

	assert.Empty(t, h.fetcher.calls, "a failed lookup must not fall back to the origin")
	require.Len(t, h.repo.MarkAttemptFailedCalls(), 1)
}

func TestCardReconcilerRetriesUntilTheIngressGatewayIsMapped(t *testing.T) {
	h := newCardReconcilerHarness("")
	h.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{}, nil
	}
	h.gateways.ListWithFiltersFunc = func(repositories.GatewayFilterOptions) ([]*models.Gateway, error) {
		return []*models.Gateway{}, nil
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourceExternal))

	failed := h.repo.MarkAttemptFailedCalls()
	require.Len(t, failed, 1)
	assert.Contains(t, failed[0].LastErr, "no ingress gateway")
}
```

The existing `TestCardReconcilerFetchesAnExternalCardGuarded` keeps covering the direct case, because the harness's default proxy lookup is "not found".

- [ ] **Step 2: Verify they fail**

Run: `make test-run RUN=TestCardReconciler PKG=./services/`
Expected: FAIL to compile — too many arguments to `NewA2ACardReconcilerService`.

- [ ] **Step 3: Implement**

In `services/a2a_card_reconciler.go`, add `proxyRepo repositories.A2AProxyRepository` and `gatewayRepo repositories.GatewayRepository` to the struct; the constructor takes them second and third and stores them. Replace the external case of `attemptFetch`:

```go
	case models.A2AAgentCardSourceExternal:
		return s.fetchExternalCard(ctx, row)
```

and add:

```go
// fetchExternalCard fetches through the gateway while the environment is proxied, since only the
// gateway's copy advertises gateway URLs; otherwise from the registered URL, SSRF-guarded.
func (s *a2aCardReconcilerService) fetchExternalCard(ctx context.Context, row models.A2AAgentCard) (a2aCardFetch, error) {
	_, err := s.proxyRepo.GetByEnvUUID(ctx, row.OUID, row.ProjectName, row.AgentName, row.EnvironmentUUID)
	switch {
	case errors.Is(err, repositories.ErrA2AProxyNotFound):
		return s.fetchRegisteredCard(ctx, row)
	case err != nil:
		return a2aCardFetch{}, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return s.fetchCardThroughGateway(ctx, row)
}

func (s *a2aCardReconcilerService) fetchRegisteredCard(ctx context.Context, row models.A2AAgentCard) (a2aCardFetch, error) {
	body, err := s.fetcher.Fetch(ctx, row.SourceURL, true)
	return a2aCardFetch{url: row.SourceURL, body: body}, err
}

// fetchCardThroughGateway uses the plain client: the URL is platform-derived and resolves to loopback in local setups.
func (s *a2aCardReconcilerService) fetchCardThroughGateway(ctx context.Context, row models.A2AAgentCard) (a2aCardFetch, error) {
	gateway, err := resolveIngressGateway(s.gatewayRepo, row.OUID, row.EnvironmentUUID)
	if err != nil {
		return a2aCardFetch{}, err
	}
	url := strings.TrimRight(buildPublicProxyURL(gateway, nil), "/") + "/" + row.AgentName + a2aAgentCardPath
	body, err := s.fetcher.Fetch(ctx, url, false)
	if err != nil {
		// The derived URL is otherwise invisible to the user.
		return a2aCardFetch{url: url}, fmt.Errorf("%s: %w", url, err)
	}
	return a2aCardFetch{url: url, body: body}, nil
}
```

Run: `make wire`
Expected: both injectors pass `a2AProxyRepository` and `gatewayRepository` to `NewA2ACardReconcilerService`.

- [ ] **Step 4: Run the tests**

Run: `make test-run RUN=TestCardReconciler PKG=./services/` then `make test-unit`
Expected: PASS.

- [ ] **Step 5: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./wiring/...
git add services/a2a_card_reconciler.go services/a2a_card_reconciler_unit_test.go wiring/wire_gen.go
git commit -m "Fetch a proxied A2A agent's card through its gateway"
```

---

### Task 9: `a2aProxyService`

**Files:**
- Create: `agent-manager-service/services/a2a_proxy_service.go`, `agent-manager-service/services/a2a_proxy_service_unit_test.go`
- Modify: `agent-manager-service/models/a2a_proxy.go` (service types), `agent-manager-service/utils/errors.go` (sentinels), `agent-manager-service/audit/actions_domain.go` (actions)

**Interfaces:**
- Consumes:
  - Task 2: `A2AProxyRepository`.
  - Task 3: `cardRepo.ClearCard`/`WithTx`, `AgentConfigRepository.CreateIfAbsent`/`WithTx`.
  - Task 4: `*A2APublisher`, `a2aPublicationKey`, `a2aTeardown`, and the test helpers (`publisherMocks`, `txSentinel`, `externalA2AAgent`).
  - Existing: `ensureAgentEnvAPIArtifact(db, artifactRepo, ouID, project, agent, envID)`, `agentEnvAPIArtifactHandle`, `requireEnvironmentTier`, `resolveAPIConfig`, `withA2AVersionHeader`, `isExternalProvisioned`, `translateAgentError`, `utils.EncryptBytes`, `audit.Begin`.
- Produces:
  - `models.A2AProxyAuthUpdate{Header string; Value *string}` (`Value` nil keeps the stored secret); `models.A2AProxyStatus{Status models.A2APublicationStatus; LastError, UpstreamAuthHeader string}`
  - `utils.ErrEnvironmentNotProxied` (404), `utils.ErrA2AProxyAgentNotExternal` (400), `utils.ErrA2AProxyNeedsCardSource` (409), `utils.ErrInvalidUpstreamAuth` (400)
  - `audit.ActionAgentSetA2AProxy = "agent:set-a2a-proxy"`, `audit.ActionAgentRemoveA2AProxy = "agent:remove-a2a-proxy"` (both `ClassCredential`; detail schema `agentName`, `upstreamAuthHeader`, `upstreamAuthChanged`)
  - `type A2AProxyHooks interface { IsProxied(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (bool, error); OnCardSourceChanged(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string) error; OnCardSourceRemoved(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error; OnCardRefresh(same as OnCardSourceRemoved) error }`
  - `type A2AProxyService interface { A2AProxyHooks; GetA2AProxy(ctx, ouID, projectName, agentName, envName string) (*models.A2AProxyStatus, error); SetA2AProxy(ctx, ouID, projectName, agentName, envName string, auth *models.A2AProxyAuthUpdate) error; DeleteA2AProxy(ctx, ouID, projectName, agentName, envName string) error; RefreshA2AProxy(ctx, ouID, projectName, agentName, envName string) error }`
  - `func NewA2AProxyService(tx TxRunner, proxyRepo repositories.A2AProxyRepository, cardRepo repositories.A2AAgentCardRepository, agentConfigRepo repositories.AgentConfigRepository, artifactRepo repositories.ArtifactRepository, publisher *A2APublisher, ocClient client.OpenChoreoClient, encryptionKey []byte, logger *slog.Logger) A2AProxyService`
  - `func validateA2AUpstreamAuth(auth *models.A2AProxyAuthUpdate) error`
  - Test helpers: `const proxyTestEnvUUID`, `var proxyTestKey`, `type proxyHarness`, `func newProxyHarness(t) *proxyHarness`, `(h) proxied(header, secret string)`, `(h) acceptsWrites()`, `func decryptForTest(t, secretRef string) string`.

Transitions (spec § Transitions):

| Call | One transaction | After commit |
|---|---|---|
| `SetA2AProxy`, environment direct | upsert proxy row; ensure artifact; `CreateIfAbsent` default config; `ClearCard` (parks; missing row → 409); enqueue `external` publication | `dropIfAgentGone` |
| `SetA2AProxy`, already proxied | upsert proxy row; ensure artifact; enqueue publication (card kept) | `dropIfAgentGone` |
| `DeleteA2AProxy` | `TeardownEnv`; delete proxy row; `ClearCard` + `Requeue` (direct fetch) | `SendTeardown` |
| `RefreshA2AProxy`, `OnCardRefresh` | ensure artifact; enqueue publication (never `Requeue`) | `dropIfAgentGone` |
| `OnCardSourceChanged` | `Enqueue` the new URL through the tx-bound card repo; `ClearCard`; ensure artifact; enqueue publication | `dropIfAgentGone` |
| `OnCardSourceRemoved` | `TeardownEnv`; delete proxy row; delete card row | `SendTeardown` |

`SetA2AProxy`, `DeleteA2AProxy` and `OnCardSourceRemoved` open a fail-closed `audit.Begin` before their transaction.

- [ ] **Step 1: Add the service types, sentinels and audit actions**

Append to `models/a2a_proxy.go`:

```go
// A2AProxyAuthUpdate is a requested upstream credential; a nil Value keeps the stored secret.
type A2AProxyAuthUpdate struct {
	Header string
	Value  *string
}

// A2AProxyStatus is a proxied environment's gateway publication state; the secret is never part of it.
type A2AProxyStatus struct {
	Status             A2APublicationStatus
	LastError          string
	UpstreamAuthHeader string
}
```

In `utils/errors.go`, in the `var (` block after `ErrAgentCardSourceNotExternal`:

```go
	ErrEnvironmentNotProxied       = errors.New("the environment does not route this agent through its gateway")
	ErrA2AProxyAgentNotExternal    = errors.New("only external A2A agents can be routed through the gateway")
	ErrA2AProxyNeedsCardSource     = errors.New("register the agent's card URL for this environment before routing it through the gateway")
	ErrInvalidUpstreamAuth         = errors.New("invalid upstream auth")
```

In `audit/actions_domain.go`, after the card-source constants:

```go
	// A2A proxy (external A2A agents published through the ingress gateway).
	ActionAgentSetA2AProxy    Action = "agent:set-a2a-proxy"
	ActionAgentRemoveA2AProxy Action = "agent:remove-a2a-proxy"
```

and in `init()` after the card-source registrations:

```go
	// A proxy holds the credential the gateway sends upstream: setting or dropping it is a credential change.
	a2aProxyFields := map[string]FieldKind{
		"agentName":           KindName,
		"upstreamAuthHeader":  KindName,
		"upstreamAuthChanged": KindFlag,
	}
	registerCredential(ActionAgentSetA2AProxy, a2aProxyFields)
	registerCredential(ActionAgentRemoveA2AProxy, a2aProxyFields)
```

Run: `make test-run RUN='TestRegisteredActionsHaveDetailSchemas|TestCredentialActionsAreCritical' PKG=./audit/`
Expected: PASS. (`TestDomainActionsMatchRouteDerivedActions` gains its rows in Task 13, together with the routes.)

- [ ] **Step 2: Write the tests**

`services/a2a_proxy_service_unit_test.go`:

```go
package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/jwtassertion"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const proxyTestEnvUUID = "55555555-5555-5555-5555-555555555555"

var proxyTestKey = bytes.Repeat([]byte{7}, 32)

// proxyHarness serves an external A2A agent whose dev environment has a registered card URL and is
// still direct. Writes are wired only on the tx-bound mocks, and only by acceptsWrites.
type proxyHarness struct {
	*publisherMocks
	svc                *a2aProxyService
	txProxies          *repomocks.A2AProxyRepositoryMock
	cards, txCards     *repomocks.A2AAgentCardRepositoryMock
	configs, txConfigs *repomocks.AgentConfigRepositoryMock
	oc                 *clientmocks.OpenChoreoClientMock
	artifactUUID       uuid.UUID
}

func newProxyHarness(t *testing.T) *proxyHarness {
	m := newPublisherMocks(t)
	h := &proxyHarness{
		publisherMocks: m,
		txProxies:      &repomocks.A2AProxyRepositoryMock{},
		txCards:        &repomocks.A2AAgentCardRepositoryMock{},
		txConfigs:      &repomocks.AgentConfigRepositoryMock{},
		artifactUUID:   uuid.New(),
	}
	m.proxies.WithTxFunc = func(tx *gorm.DB) repositories.A2AProxyRepository {
		assert.Same(t, txSentinel, tx)
		return h.txProxies
	}
	m.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return nil, repositories.ErrA2AProxyNotFound
	}
	m.artifacts.GetByHandleFunc = func(string, string) (*models.Artifact, error) {
		return &models.Artifact{UUID: h.artifactUUID, Kind: models.KindAgent}, nil
	}
	h.cards = &repomocks.A2AAgentCardRepositoryMock{
		GetFunc: func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
			return &models.A2AAgentCard{
				Source: models.A2AAgentCardSourceExternal, SourceURL: "https://agent.example/.well-known/agent-card.json",
			}, nil
		},
		WithTxFunc: func(tx *gorm.DB) repositories.A2AAgentCardRepository {
			assert.Same(t, txSentinel, tx)
			return h.txCards
		},
	}
	h.configs = &repomocks.AgentConfigRepositoryMock{
		WithTxFunc: func(tx *gorm.DB) repositories.AgentConfigRepository {
			assert.Same(t, txSentinel, tx)
			return h.txConfigs
		},
	}
	h.oc = &clientmocks.OpenChoreoClientMock{
		GetComponentFunc: func(context.Context, string, string, string) (*models.AgentResponse, error) {
			return externalA2AAgent(), nil
		},
		GetEnvironmentFunc: func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
			return &models.EnvironmentResponse{UUID: proxyTestEnvUUID, Name: name}, nil
		},
	}
	h.svc = NewA2AProxyService(m.tx(), m.proxies, h.cards, h.configs, m.artifacts, m.publisher(), h.oc, proxyTestKey,
		testLogger()).(*a2aProxyService)
	return h
}

// proxied makes the environment proxied; an empty secret means no upstream auth is stored.
func (h *proxyHarness) proxied(header, secret string) {
	h.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{
			EnvironmentUUID: uuid.MustParse(proxyTestEnvUUID), UpstreamAuthHeader: header, UpstreamAuthSecret: secret,
		}, nil
	}
}

// acceptsWrites lets every write a transition may make succeed, inside the transaction only.
func (h *proxyHarness) acceptsWrites() {
	h.txProxies.UpsertFunc = func(context.Context, *models.A2AProxy) error { return nil }
	h.txProxies.DeleteFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	h.txCards.EnqueueFunc = func(context.Context, *models.A2AAgentCard) error { return nil }
	h.txCards.ClearCardFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	h.txCards.RequeueFunc = func(context.Context, string, string, string, string) error { return nil }
	h.txCards.DeleteForAgentEnvFunc = func(context.Context, string, string, string, string) error { return nil }
	h.txConfigs.CreateIfAbsentFunc = func(context.Context, *models.AgentConfig) error { return nil }
	h.txPubs.EnqueueFunc = func(context.Context, *models.A2APublication) error { return nil }
	h.txPubs.DeleteForAgentEnvFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	h.txDeployments.DeleteForArtifactFunc = func(context.Context, uuid.UUID, string) error { return nil }
	h.artifacts.CreateFunc = func(*gorm.DB, *models.Artifact) error { return nil }
}

func decryptForTest(t *testing.T, secretRef string) string {
	t.Helper()
	ciphertext, err := base64.StdEncoding.DecodeString(secretRef)
	require.NoError(t, err)
	plaintext, err := utils.DecryptBytes(ciphertext, proxyTestKey)
	require.NoError(t, err)
	return string(plaintext)
}

func devEnv() *models.EnvironmentResponse {
	return &models.EnvironmentResponse{UUID: proxyTestEnvUUID, Name: "dev"}
}

// tierOnlyCtx authorises both tiers but carries no audit recorder.
func tierOnlyCtx() context.Context {
	return jwtassertion.ContextWithTokenClaimsAndScope(context.Background(), &jwtassertion.TokenClaims{
		Scope: audit.ScopesOf([]rbac.Permission{rbac.AgentEnvNonProduction, rbac.AgentEnvProduction}),
	})
}

func TestSetA2AProxySwitchesADirectEnvironmentToProxied(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	value := "s3cret"

	require.NoError(t, h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev",
		&models.A2AProxyAuthUpdate{Header: "X-Upstream-Key", Value: &value}))

	upserted := h.txProxies.UpsertCalls()
	require.Len(t, upserted, 1)
	assert.Equal(t, "X-Upstream-Key", upserted[0].Proxy.UpstreamAuthHeader)
	assert.Equal(t, value, decryptForTest(t, upserted[0].Proxy.UpstreamAuthSecret))
	assert.Equal(t, uuid.MustParse(proxyTestEnvUUID), upserted[0].Proxy.EnvironmentUUID)

	configs := h.txConfigs.CreateIfAbsentCalls()
	require.Len(t, configs, 1)
	assert.True(t, configs[0].Config.EnableApiKeySecurity, "inbound auth defaults on")
	assert.Contains(t, configs[0].Config.CORSAllowHeaders, "A2A-Version")
	assert.Nil(t, configs[0].Config.CardCORSEnabled, "the card inherits the agent's CORS")

	require.Len(t, h.txCards.ClearCardCalls(), 1, "the card is parked")
	assert.Empty(t, h.txCards.RequeueCalls(), "and not made due before the gateway has the Agent")

	queued := h.txPubs.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2APublicationSourceExternal, queued[0].Pub.Source)
	assert.Equal(t, h.artifactUUID, queued[0].Pub.ArtifactUUID)
}

func TestSetA2AProxyNeedsARegisteredCardURL(t *testing.T) {
	h := newProxyHarness(t)
	h.cards.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}

	err := h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", nil)

	assert.ErrorIs(t, err, utils.ErrA2AProxyNeedsCardSource)
	assert.Zero(t, h.transactions)
}

// The stored card stays until the fetch after the republish replaces it.
func TestSetA2AProxyUpdatesAProxiedEnvironmentInPlace(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("X-Old", "b2xk")
	h.txCards.ClearCardFunc = nil
	h.txConfigs.CreateIfAbsentFunc = nil

	require.NoError(t, h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev",
		&models.A2AProxyAuthUpdate{Header: "X-New"}))

	upserted := h.txProxies.UpsertCalls()
	require.Len(t, upserted, 1)
	assert.Equal(t, "X-New", upserted[0].Proxy.UpstreamAuthHeader)
	assert.Equal(t, "b2xk", upserted[0].Proxy.UpstreamAuthSecret, "a header alone keeps the stored secret")
	assert.Len(t, h.txPubs.EnqueueCalls(), 1)
}

func TestSetA2AProxySecretSemantics(t *testing.T) {
	replacement, empty := "n3w", ""
	cases := []struct {
		name        string
		stored      string
		auth        *models.A2AProxyAuthUpdate
		wantHeader  string
		wantSecret  string
		wantDecrypt string
		wantErr     error
	}{
		{name: "omitting auth clears it", stored: "b2xk", auth: nil},
		{name: "a header alone keeps the stored secret", stored: "b2xk",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key"}, wantHeader: "X-Key", wantSecret: "b2xk"},
		{name: "a value replaces it", stored: "b2xk",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key", Value: &replacement}, wantHeader: "X-Key", wantDecrypt: "n3w"},
		{name: "an empty value is rejected", stored: "b2xk",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key", Value: &empty}, wantErr: utils.ErrInvalidUpstreamAuth},
		{name: "a header alone needs a stored secret",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key"}, wantErr: utils.ErrInvalidUpstreamAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProxyHarness(t)
			h.acceptsWrites()
			h.proxied("X-Key", tc.stored)

			err := h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", tc.auth)

			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				assert.Zero(t, h.transactions)
				return
			}
			require.NoError(t, err)
			row := h.txProxies.UpsertCalls()[0].Proxy
			assert.Equal(t, tc.wantHeader, row.UpstreamAuthHeader)
			if tc.wantDecrypt != "" {
				assert.Equal(t, tc.wantDecrypt, decryptForTest(t, row.UpstreamAuthSecret))
				return
			}
			assert.Equal(t, tc.wantSecret, row.UpstreamAuthSecret)
		})
	}
}

func TestValidateA2AUpstreamAuth(t *testing.T) {
	value := func(v string) *string { return &v }
	for _, auth := range []*models.A2AProxyAuthUpdate{
		nil,
		{Header: "X-Upstream_Key-1"},
		{Header: "X-Key", Value: value(strings.Repeat("a", 8192))},
	} {
		assert.NoError(t, validateA2AUpstreamAuth(auth))
	}
	for name, auth := range map[string]*models.A2AProxyAuthUpdate{
		"a header with a space":  {Header: "X Key", Value: value("v")},
		"an empty header":        {Header: "", Value: value("v")},
		"Host":                   {Header: "host", Value: value("v")},
		"a 256-character header": {Header: strings.Repeat("h", 256), Value: value("v")},
		"an empty value":         {Header: "X-Key", Value: value("")},
		"8194 bytes":             {Header: "X-Key", Value: value(strings.Repeat("é", 4097))},
		"a single quote":         {Header: "X-Key", Value: value("it's")},
		"a carriage return":      {Header: "X-Key", Value: value("a\rb")},
		"a line feed":            {Header: "X-Key", Value: value("a\nb")},
		// Review Focus 4: the gateway renders {{ }} with secret and env functions.
		"template syntax": {Header: "X-Key", Value: value(`{{ secret "openai" }}`)},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, validateA2AUpstreamAuth(auth), utils.ErrInvalidUpstreamAuth)
		})
	}
}

func TestSetA2AProxyRefusesWithoutAnAuditRecord(t *testing.T) {
	h := newProxyHarness(t)
	err := h.svc.SetA2AProxy(tierOnlyCtx(), "org-1", "checkout", "weather", "dev", nil)
	assert.ErrorIs(t, err, audit.ErrRecorderUnavailable)
	assert.Zero(t, h.transactions)
}

func TestDeleteA2AProxyRefusesWithoutAnAuditRecord(t *testing.T) {
	h := newProxyHarness(t)
	h.proxied("X-Key", "c2VjcmV0")
	err := h.svc.DeleteA2AProxy(tierOnlyCtx(), "org-1", "checkout", "weather", "dev")
	assert.ErrorIs(t, err, audit.ErrRecorderUnavailable)
	assert.Zero(t, h.transactions)
}

func TestA2AProxyOnADirectEnvironmentIsNotFound(t *testing.T) {
	h := newProxyHarness(t)
	h.proxies.GetFunc = func(context.Context, string, string, string, string) (*models.A2AProxy, error) {
		return nil, repositories.ErrA2AProxyNotFound
	}

	_, err := h.svc.GetA2AProxy(context.Background(), "org-1", "checkout", "weather", "dev")
	assert.ErrorIs(t, err, utils.ErrEnvironmentNotProxied)
	assert.ErrorIs(t, h.svc.DeleteA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"), utils.ErrEnvironmentNotProxied)
	assert.ErrorIs(t, h.svc.RefreshA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"), utils.ErrEnvironmentNotProxied)
	assert.Zero(t, h.transactions)
}

func TestGetA2AProxyReportsThePublicationState(t *testing.T) {
	h := newProxyHarness(t)
	h.proxies.GetFunc = func(context.Context, string, string, string, string) (*models.A2AProxy, error) {
		return &models.A2AProxy{EnvironmentUUID: uuid.MustParse(proxyTestEnvUUID), UpstreamAuthHeader: "X-Key", UpstreamAuthSecret: "c2VjcmV0"}, nil
	}
	h.pubs.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return &models.A2APublication{Status: models.A2APublicationStatusFailed, LastError: "HTTP 503"}, nil
	}
	h.oc.GetComponentFunc = nil // a stored row proves the agent, so a poll costs no OpenChoreo call

	got, err := h.svc.GetA2AProxy(context.Background(), "org-1", "checkout", "weather", "dev")

	require.NoError(t, err)
	assert.Equal(t, &models.A2AProxyStatus{
		Status: models.A2APublicationStatusFailed, LastError: "HTTP 503", UpstreamAuthHeader: "X-Key",
	}, got)
}

// Both rows are written together, so a missing publication would otherwise read as pending forever.
func TestGetA2AProxyWithoutAPublicationReportsFailed(t *testing.T) {
	h := newProxyHarness(t)
	h.proxies.GetFunc = func(context.Context, string, string, string, string) (*models.A2AProxy, error) {
		return &models.A2AProxy{EnvironmentUUID: uuid.MustParse(proxyTestEnvUUID)}, nil
	}
	h.pubs.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return nil, repositories.ErrA2APublicationNotFound
	}

	got, err := h.svc.GetA2AProxy(context.Background(), "org-1", "checkout", "weather", "dev")

	require.NoError(t, err)
	assert.Equal(t, models.A2APublicationStatusFailed, got.Status)
	assert.NotEmpty(t, got.LastError)
}

// The artifact, and the API keys bound to it, survive a switch to direct.
func TestDeleteA2AProxySwitchesOnlyThatEnvironmentToDirect(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("X-Key", "c2VjcmV0")
	// h.artifacts.DeleteFunc is nil: deleting the artifact would panic.

	require.NoError(t, h.svc.DeleteA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"))

	deployments := h.txDeployments.DeleteForArtifactCalls()
	require.Len(t, deployments, 1)
	assert.Equal(t, h.artifactUUID, deployments[0].ArtifactUUID)
	publications := h.txPubs.DeleteForAgentEnvCalls()
	require.Len(t, publications, 1)
	assert.Equal(t, uuid.MustParse(proxyTestEnvUUID), publications[0].EnvironmentUUID)
	require.Len(t, h.txProxies.DeleteCalls(), 1)
	require.Len(t, h.txCards.ClearCardCalls(), 1, "the gateway's card goes")
	require.Len(t, h.txCards.RequeueCalls(), 1, "and the origin's is fetched directly")
	require.Len(t, h.hub.published, 1, "the gateway is told once the transaction committed")
	assert.Equal(t, h.artifactUUID.String(), deletedAgentID(t, h.hub.published[0]))
}

func TestRefreshA2AProxyQueuesAPublicationNeverARequeue(t *testing.T) {
	h := newProxyHarness(t)
	h.proxied("", "")
	h.txPubs.EnqueueFunc = func(context.Context, *models.A2APublication) error { return nil }
	// No card write is wired: a Requeue would un-park the card before the gateway has the Agent.

	require.NoError(t, h.svc.RefreshA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"))

	assert.Len(t, h.txPubs.EnqueueCalls(), 1)
}

func TestSetA2AProxyRejectsAnInternalOrNonA2AAgent(t *testing.T) {
	cases := map[string]struct {
		agent *models.AgentResponse
		want  error
	}{
		"an internal A2A agent": {
			&models.AgentResponse{Name: "weather", Provisioning: models.Provisioning{Type: "internal"}, Type: models.AgentType{SubType: "a2a-agent"}},
			utils.ErrA2AProxyAgentNotExternal,
		},
		"an external non-A2A agent": {
			&models.AgentResponse{Name: "weather", Provisioning: models.Provisioning{Type: "external"}, Type: models.AgentType{SubType: "custom"}},
			utils.ErrAgentNotA2A,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newProxyHarness(t)
			h.oc.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) { return tc.agent, nil }
			assert.ErrorIs(t, h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", nil), tc.want)
		})
	}
}

func TestSetA2AProxyEnforcesTheEnvironmentTier(t *testing.T) {
	h := newProxyHarness(t)
	h.oc.GetEnvironmentFunc = func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
		return &models.EnvironmentResponse{UUID: proxyTestEnvUUID, Name: name, IsProduction: true}, nil
	}
	err := h.svc.SetA2AProxy(tierCtx(t, rbac.AgentEnvNonProduction), "org-1", "checkout", "weather", "prod", nil)
	assert.ErrorIs(t, err, utils.ErrForbidden)
	assert.Zero(t, h.transactions)
}

// The agent deletion that ran first could not see these writes, so the request removes them itself.
func TestSetA2AProxyDropsItsWritesWhenTheAgentIsDeletedMeanwhile(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.artifacts.DeleteFunc = func(*gorm.DB, string) error { return nil }
	lookups := 0
	h.oc.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) {
		lookups++
		if lookups == 1 {
			return externalA2AAgent(), nil
		}
		return nil, utils.ErrNotFound
	}

	err := h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", nil)

	assert.ErrorIs(t, err, utils.ErrAgentNotFound)
	assert.Len(t, h.artifacts.DeleteCalls(), 1)
	assert.Len(t, h.txProxies.DeleteCalls(), 1)
	assert.Len(t, h.txPubs.DeleteForAgentEnvCalls(), 1)
	assert.Len(t, h.txCards.DeleteForAgentEnvCalls(), 1, "the card row the proxy service may have written goes too")
}

// The URL write, the parking and the republish are one transaction: the row is never due in between.
func TestOnCardSourceChangedStoresTheURLParksTheCardAndRepublishes(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("", "")
	newURL := "https://other.example/.well-known/agent-card.json"

	require.NoError(t, h.svc.OnCardSourceChanged(context.Background(), externalA2AAgent(), "org-1", "checkout", devEnv(), newURL))

	written := h.txCards.EnqueueCalls()
	require.Len(t, written, 1, "the URL is written inside the transaction, never on the plain repo")
	assert.Equal(t, newURL, written[0].Card.SourceURL)
	assert.Equal(t, models.A2AAgentCardSourceExternal, written[0].Card.Source)
	assert.Equal(t, uuid.MustParse(proxyTestEnvUUID), written[0].Card.EnvironmentUUID)
	assert.Len(t, h.txCards.ClearCardCalls(), 1)
	assert.Len(t, h.txPubs.EnqueueCalls(), 1)
	assert.Empty(t, h.txCards.RequeueCalls())
	assert.Equal(t, 1, h.transactions)
}

func TestOnCardSourceRemovedTearsDownAndDeletesTheCard(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("X-Key", "c2VjcmV0")

	require.NoError(t, h.svc.OnCardSourceRemoved(auditableCtx(t), externalA2AAgent(), "org-1", "checkout", devEnv()))

	assert.Len(t, h.txCards.DeleteForAgentEnvCalls(), 1)
	assert.Empty(t, h.txCards.RequeueCalls())
	assert.Len(t, h.txProxies.DeleteCalls(), 1)
	assert.ErrorIs(t, h.svc.OnCardSourceRemoved(context.Background(), externalA2AAgent(), "org-1", "checkout", devEnv()),
		audit.ErrRecorderUnavailable, "it discards the stored secret, so it is refused unrecorded")
}

func TestIsProxied(t *testing.T) {
	h := newProxyHarness(t)
	proxied, err := h.svc.IsProxied(context.Background(), "org-1", "checkout", "weather", uuid.MustParse(proxyTestEnvUUID))
	require.NoError(t, err)
	assert.False(t, proxied)

	h.proxied("", "")
	proxied, err = h.svc.IsProxied(context.Background(), "org-1", "checkout", "weather", uuid.MustParse(proxyTestEnvUUID))
	require.NoError(t, err)
	assert.True(t, proxied)
}
```

The moq call-record field names follow the interface parameter names: `Proxy` for `Upsert`, `Config` for `CreateIfAbsent`, `Pub` for the publication `Enqueue`, `Card` for the card `Enqueue`, `EnvironmentUUID` and `ArtifactUUID` for the deletes. If moq named one differently, use the generated name.

- [ ] **Step 3: Verify the tests fail**

Run: `make test-run RUN='TestSetA2AProxy|TestValidateA2AUpstreamAuth|TestDeleteA2AProxy|TestGetA2AProxy|TestRefreshA2AProxy|TestA2AProxyOn|TestOnCardSource|TestIsProxied' PKG=./services/`
Expected: FAIL — `undefined: NewA2AProxyService`.

- [ ] **Step 4: Write the service**

`services/a2a_proxy_service.go`:

```go
package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const (
	a2aUpstreamHeaderMaxLen = 255
	a2aUpstreamValueMaxLen  = 8192
	// a2aProxyNoPublicationMsg reports a proxied environment with no publication queued; a refresh queues one.
	a2aProxyNoPublicationMsg = "no gateway publication is queued for this environment; refresh to publish it again"
)

// a2aUpstreamHeaderPattern is the gateway's set-headers name schema.
var a2aUpstreamHeaderPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// A2AProxyHooks is what the card service needs from the proxy service on a proxied environment.
type A2AProxyHooks interface {
	IsProxied(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (bool, error)
	// OnCardSourceChanged stores the new URL, parks the card and republishes from it, in one transaction.
	OnCardSourceChanged(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string) error
	// OnCardSourceRemoved switches the environment to direct and deletes its card row.
	OnCardSourceRemoved(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error
	// OnCardRefresh republishes; the card fetch follows the publish.
	OnCardRefresh(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error
}

// A2AProxyService publishes external A2A agents through their environment's ingress gateway.
type A2AProxyService interface {
	A2AProxyHooks
	GetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AProxyStatus, error)
	// SetA2AProxy enables the proxy or updates its upstream auth; a nil auth clears the stored credential.
	SetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string, auth *models.A2AProxyAuthUpdate) error
	DeleteA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error
	RefreshA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error
}

type a2aProxyService struct {
	tx              TxRunner
	proxyRepo       repositories.A2AProxyRepository
	cardRepo        repositories.A2AAgentCardRepository
	agentConfigRepo repositories.AgentConfigRepository
	artifactRepo    repositories.ArtifactRepository
	publisher       *A2APublisher
	ocClient        client.OpenChoreoClient
	encryptionKey   []byte
	logger          *slog.Logger
}

// NewA2AProxyService creates an A2AProxyService.
func NewA2AProxyService(
	tx TxRunner,
	proxyRepo repositories.A2AProxyRepository,
	cardRepo repositories.A2AAgentCardRepository,
	agentConfigRepo repositories.AgentConfigRepository,
	artifactRepo repositories.ArtifactRepository,
	publisher *A2APublisher,
	ocClient client.OpenChoreoClient,
	encryptionKey []byte,
	logger *slog.Logger,
) A2AProxyService {
	return &a2aProxyService{
		tx:              tx,
		proxyRepo:       proxyRepo,
		cardRepo:        cardRepo,
		agentConfigRepo: agentConfigRepo,
		artifactRepo:    artifactRepo,
		publisher:       publisher,
		ocClient:        ocClient,
		encryptionKey:   encryptionKey,
		logger:          logger,
	}
}

// a2aProxyTarget is the agent and environment one proxy operation acts on.
type a2aProxyTarget struct {
	agent *models.AgentResponse
	env   *models.EnvironmentResponse
	key   a2aPublicationKey
}

func newA2AProxyTarget(agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) (a2aProxyTarget, error) {
	envUUID, err := uuid.Parse(env.UUID)
	if err != nil {
		return a2aProxyTarget{}, fmt.Errorf("invalid UUID for environment %s: %w", env.Name, err)
	}
	key := a2aPublicationKey{
		OUID: ouID, ProjectName: projectName, AgentName: agent.Name, EnvironmentName: env.Name, EnvironmentUUID: envUUID,
	}
	return a2aProxyTarget{agent: agent, env: env, key: key}, nil
}

// cardRow is the environment's external card row with sourceURL registered.
func (t a2aProxyTarget) cardRow(sourceURL string) *models.A2AAgentCard {
	return &models.A2AAgentCard{
		OUID: t.key.OUID, ProjectName: t.key.ProjectName, AgentName: t.key.AgentName,
		EnvironmentName: t.key.EnvironmentName, EnvironmentUUID: t.key.EnvironmentUUID,
		Source: models.A2AAgentCardSourceExternal, SourceURL: sourceURL,
	}
}

func (s *a2aProxyService) GetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AProxyStatus, error) {
	// Rows exist only for live proxied environments, so OpenChoreo is asked only on a miss.
	proxy, err := s.proxyRepo.Get(ctx, ouID, projectName, agentName, envName)
	if errors.Is(err, repositories.ErrA2AProxyNotFound) {
		if _, err := s.agent(ctx, ouID, projectName, agentName); err != nil {
			return nil, err
		}
		return nil, utils.ErrEnvironmentNotProxied
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	status := &models.A2AProxyStatus{UpstreamAuthHeader: proxy.UpstreamAuthHeader}
	key := a2aPublicationKey{OUID: ouID, ProjectName: projectName, AgentName: agentName, EnvironmentName: envName, EnvironmentUUID: proxy.EnvironmentUUID}
	pub, err := s.publisher.Publication(ctx, key)
	switch {
	case errors.Is(err, repositories.ErrA2APublicationNotFound):
		// Enabling writes both rows together, so a missing publication never resolves on its own.
		status.Status, status.LastError = models.A2APublicationStatusFailed, a2aProxyNoPublicationMsg
	case err != nil:
		return nil, fmt.Errorf("failed to read the A2A publication: %w", err)
	default:
		status.Status, status.LastError = pub.Status, pub.LastError
	}
	return status, nil
}

func (s *a2aProxyService) SetA2AProxy(
	ctx context.Context, ouID, projectName, agentName, envName string, auth *models.A2AProxyAuthUpdate,
) error {
	if err := validateA2AUpstreamAuth(auth); err != nil {
		return err
	}
	t, err := s.target(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		return err
	}
	if err := s.requireCardSource(ctx, t); err != nil {
		return err
	}
	existing, err := s.proxyOf(ctx, t)
	if err != nil && !errors.Is(err, utils.ErrEnvironmentNotProxied) {
		return err
	}
	proxy, authChanged, err := s.proxyRow(t, existing, auth)
	if err != nil {
		return err
	}
	// A PUT can set, replace or clear the credential the gateway sends upstream, so it is refused unrecorded.
	attempt, err := audit.Begin(ctx, audit.ActionAgentSetA2AProxy, a2aProxyAuditOptions(t, proxy.UpstreamAuthHeader, authChanged)...)
	if err != nil {
		s.logger.Error("Refusing to set the A2A proxy: audit record could not be written", "agentName", agentName, "error", err)
		return err
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		if err := s.proxyRepo.WithTx(tx).Upsert(ctx, proxy); err != nil {
			return fmt.Errorf("failed to save the A2A proxy: %w", err)
		}
		if existing == nil {
			if err := s.switchFromDirect(ctx, tx, t); err != nil {
				return err
			}
		}
		return s.enqueuePublication(ctx, tx, t)
	})
	attempt.Complete(ctx, err)
	if err != nil {
		return err
	}
	return s.dropIfAgentGone(ctx, t)
}

func (s *a2aProxyService) DeleteA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error {
	t, err := s.target(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		return err
	}
	existing, err := s.proxyOf(ctx, t)
	if err != nil {
		return err
	}
	return s.removeProxy(ctx, t, existing, requeueDirectCardFetch)
}

func (s *a2aProxyService) RefreshA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) error {
	t, err := s.target(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		return err
	}
	if _, err := s.proxyOf(ctx, t); err != nil {
		return err
	}
	return s.republish(ctx, t)
}

func (s *a2aProxyService) IsProxied(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (bool, error) {
	_, err := s.proxyRepo.GetByEnvUUID(ctx, ouID, projectName, agentName, environmentUUID)
	switch {
	case errors.Is(err, repositories.ErrA2AProxyNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return true, nil
}

func (s *a2aProxyService) OnCardSourceChanged(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string,
) error {
	t, err := newA2AProxyTarget(agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		// One transaction: a row left due between the URL write and the parking could be fetched
		// through the gateway, which still fronts the old origin.
		cards := s.cardRepo.WithTx(tx)
		if err := cards.Enqueue(ctx, t.cardRow(sourceURL)); err != nil {
			return fmt.Errorf("failed to store the agent card URL: %w", err)
		}
		// The new URL's card is shown only once the gateway fronts the new origin.
		err := cards.ClearCard(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
		if err != nil {
			return fmt.Errorf("failed to park the agent card: %w", err)
		}
		return s.enqueuePublication(ctx, tx, t)
	})
	if err != nil {
		return err
	}
	return s.dropIfAgentGone(ctx, t)
}

func (s *a2aProxyService) OnCardSourceRemoved(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse,
) error {
	t, err := newA2AProxyTarget(agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	existing, err := s.proxyOf(ctx, t)
	if err != nil {
		return err
	}
	return s.removeProxy(ctx, t, existing, deleteCardRow)
}

func (s *a2aProxyService) OnCardRefresh(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse,
) error {
	t, err := newA2AProxyTarget(agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	return s.republish(ctx, t)
}

// target resolves an external A2A agent and an environment the caller may act on.
func (s *a2aProxyService) target(ctx context.Context, ouID, projectName, agentName, envName string) (a2aProxyTarget, error) {
	agent, err := s.agent(ctx, ouID, projectName, agentName)
	if err != nil {
		return a2aProxyTarget{}, err
	}
	if !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return a2aProxyTarget{}, utils.ErrAgentNotA2A
	}
	if !isExternalProvisioned(agent) {
		return a2aProxyTarget{}, utils.ErrA2AProxyAgentNotExternal
	}
	env, err := requireEnvironmentTier(ctx, s.ocClient, s.logger, ouID, envName)
	if err != nil {
		return a2aProxyTarget{}, err
	}
	return newA2AProxyTarget(agent, ouID, projectName, env)
}

func (s *a2aProxyService) agent(ctx context.Context, ouID, projectName, agentName string) (*models.AgentResponse, error) {
	agent, err := s.ocClient.GetComponent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, translateAgentError(err)
	}
	return agent, nil
}

// proxyOf is the environment's proxy row, or utils.ErrEnvironmentNotProxied while it is direct.
func (s *a2aProxyService) proxyOf(ctx context.Context, t a2aProxyTarget) (*models.A2AProxy, error) {
	proxy, err := s.proxyRepo.GetByEnvUUID(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AProxyNotFound) {
		return nil, utils.ErrEnvironmentNotProxied
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the A2A proxy: %w", err)
	}
	return proxy, nil
}

// requireCardSource is the 409 for an environment with no registered card URL to proxy.
func (s *a2aProxyService) requireCardSource(ctx context.Context, t a2aProxyTarget) error {
	card, err := s.cardRepo.Get(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentName)
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardNotFound):
		return utils.ErrA2AProxyNeedsCardSource
	case err != nil:
		return fmt.Errorf("failed to read the agent card: %w", err)
	case card.Source != models.A2AAgentCardSourceExternal || card.SourceURL == "":
		return utils.ErrA2AProxyNeedsCardSource
	}
	return nil
}

// proxyRow applies the PUT's secret semantics (no auth clears it, a header alone keeps the stored
// secret, a value replaces it) and reports whether the stored credential changed.
func (s *a2aProxyService) proxyRow(
	t a2aProxyTarget, existing *models.A2AProxy, auth *models.A2AProxyAuthUpdate,
) (*models.A2AProxy, bool, error) {
	row := &models.A2AProxy{
		OUID: t.key.OUID, ProjectName: t.key.ProjectName, AgentName: t.key.AgentName,
		EnvironmentUUID: t.key.EnvironmentUUID, EnvironmentName: t.key.EnvironmentName,
	}
	stored := existing != nil && existing.UpstreamAuthSecret != ""
	switch {
	case auth == nil:
		return row, stored, nil
	case auth.Value == nil && !stored:
		return nil, false, fmt.Errorf("%w: a value is required, since no secret is stored", utils.ErrInvalidUpstreamAuth)
	case auth.Value == nil:
		row.UpstreamAuthHeader, row.UpstreamAuthSecret = auth.Header, existing.UpstreamAuthSecret
		return row, auth.Header != existing.UpstreamAuthHeader, nil
	}
	secret, err := encryptA2AUpstreamSecret(*auth.Value, s.encryptionKey)
	if err != nil {
		return nil, false, err
	}
	row.UpstreamAuthHeader, row.UpstreamAuthSecret = auth.Header, secret
	return row, true, nil
}

// switchFromDirect gives the environment the default inbound settings and parks its card until the
// publish lands, since the gateway cannot serve a card before it has the Agent.
func (s *a2aProxyService) switchFromDirect(ctx context.Context, tx *gorm.DB, t a2aProxyTarget) error {
	if err := ensureDefaultAgentConfig(ctx, s.agentConfigRepo.WithTx(tx), t.key); err != nil {
		return err
	}
	err := s.cardRepo.WithTx(tx).ClearCard(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
		return utils.ErrA2AProxyNeedsCardSource
	}
	if err != nil {
		return fmt.Errorf("failed to park the agent card: %w", err)
	}
	return nil
}

// enqueuePublication queues the environment's external publication against its artifact, creating it if needed.
func (s *a2aProxyService) enqueuePublication(ctx context.Context, tx *gorm.DB, t a2aProxyTarget) error {
	artifact, err := ensureAgentEnvAPIArtifact(tx, s.artifactRepo, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.env.UUID)
	if err != nil {
		return fmt.Errorf("failed to ensure the agent's API artifact: %w", err)
	}
	return s.publisher.Enqueue(ctx, tx, t.key, models.A2APublicationSourceExternal, artifact.UUID)
}

// republish re-derives the routes from the origin card on the next publish; the card fetch follows it.
func (s *a2aProxyService) republish(ctx context.Context, t a2aProxyTarget) error {
	err := s.tx(ctx, func(tx *gorm.DB) error {
		return s.enqueuePublication(ctx, tx, t)
	})
	if err != nil {
		return err
	}
	return s.dropIfAgentGone(ctx, t)
}

// a2aCardStep is what happens to the card row when an environment stops being proxied.
type a2aCardStep func(ctx context.Context, cards repositories.A2AAgentCardRepository, key a2aPublicationKey) error

// requeueDirectCardFetch replaces the gateway's card, which advertised gateway URLs, with a fresh one from the origin.
func requeueDirectCardFetch(ctx context.Context, cards repositories.A2AAgentCardRepository, key a2aPublicationKey) error {
	err := cards.ClearCard(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentUUID)
	if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
		return nil // the source was removed meanwhile, so there is nothing to fetch
	}
	if err != nil {
		return fmt.Errorf("failed to clear the proxied card: %w", err)
	}
	if err := cards.Requeue(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentName); err != nil {
		return fmt.Errorf("failed to queue the direct card fetch: %w", err)
	}
	return nil
}

// deleteCardRow drops the card row together with its removed source.
func deleteCardRow(ctx context.Context, cards repositories.A2AAgentCardRepository, key a2aPublicationKey) error {
	if err := cards.DeleteForAgentEnv(ctx, key.OUID, key.ProjectName, key.AgentName, key.EnvironmentName); err != nil {
		return fmt.Errorf("failed to delete the agent card: %w", err)
	}
	return nil
}

// removeProxy switches the environment to direct; it discards the stored secret, so it is refused unrecorded.
func (s *a2aProxyService) removeProxy(ctx context.Context, t a2aProxyTarget, existing *models.A2AProxy, cardStep a2aCardStep) error {
	attempt, err := audit.Begin(ctx, audit.ActionAgentRemoveA2AProxy,
		a2aProxyAuditOptions(t, existing.UpstreamAuthHeader, existing.UpstreamAuthSecret != "")...)
	if err != nil {
		s.logger.Error("Refusing to remove the A2A proxy: audit record could not be written", "agentName", t.key.AgentName, "error", err)
		return err
	}
	teardown, err := s.switchToDirect(ctx, t, cardStep)
	attempt.Complete(ctx, err)
	if err != nil {
		return err
	}
	s.publisher.SendTeardown(ctx, teardown)
	return nil
}

// switchToDirect tears down the environment's gateway publication (keeping the artifact), deletes the
// proxy row and applies cardStep in one transaction; the teardown is sent once it commits.
func (s *a2aProxyService) switchToDirect(ctx context.Context, t a2aProxyTarget, cardStep a2aCardStep) (a2aTeardown, error) {
	artifactUUID, err := s.artifactUUID(t)
	if err != nil {
		return a2aTeardown{}, err
	}
	var teardown a2aTeardown
	err = s.tx(ctx, func(tx *gorm.DB) error {
		var err error
		if teardown, err = s.publisher.TeardownEnv(ctx, tx, t.key, artifactUUID); err != nil {
			return err
		}
		err = s.proxyRepo.WithTx(tx).Delete(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
		if errors.Is(err, repositories.ErrA2AProxyNotFound) {
			return utils.ErrEnvironmentNotProxied
		}
		if err != nil {
			return fmt.Errorf("failed to delete the A2A proxy: %w", err)
		}
		return cardStep(ctx, s.cardRepo.WithTx(tx), t.key)
	})
	return teardown, err
}

// artifactUUID is the environment's agent artifact, or uuid.Nil when it has none.
func (s *a2aProxyService) artifactUUID(t a2aProxyTarget) (uuid.UUID, error) {
	handle := agentEnvAPIArtifactHandle(t.key.ProjectName, t.key.AgentName, t.env.UUID)
	artifact, err := s.artifactRepo.GetByHandle(handle, t.key.OUID)
	if errors.Is(err, utils.ErrArtifactNotFound) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to read the agent's API artifact: %w", err)
	}
	return artifact.UUID, nil
}

// dropIfAgentGone undoes this request's writes when the agent was deleted or replaced meanwhile:
// the agent deletion that ran first could not see them.
func (s *a2aProxyService) dropIfAgentGone(ctx context.Context, t a2aProxyTarget) error {
	current, err := s.agent(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName)
	switch {
	case errors.Is(err, utils.ErrAgentNotFound):
	case err != nil:
		return err
	case current.UUID == t.agent.UUID:
		return nil
	}
	teardown, err := s.forget(ctx, t)
	if err != nil {
		s.logger.Error("Failed to drop the A2A proxy of a deleted agent",
			"agentName", t.key.AgentName, "environment", t.key.EnvironmentName, "error", err)
	}
	s.publisher.SendTeardown(ctx, teardown)
	return utils.ErrAgentNotFound
}

// forget removes the proxy row, its publication, the card row and the environment's artifact, whose deployments cascade.
func (s *a2aProxyService) forget(ctx context.Context, t a2aProxyTarget) (a2aTeardown, error) {
	artifactUUID, err := s.artifactUUID(t)
	if err != nil {
		return a2aTeardown{}, err
	}
	var teardown a2aTeardown
	err = s.tx(ctx, func(tx *gorm.DB) error {
		var err error
		if teardown, err = s.publisher.TeardownEnv(ctx, tx, t.key, artifactUUID); err != nil {
			return err
		}
		if artifactUUID != uuid.Nil {
			err := s.artifactRepo.Delete(tx, artifactUUID.String())
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed to delete the agent's API artifact: %w", err)
			}
		}
		err = s.proxyRepo.WithTx(tx).Delete(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentUUID)
		if err != nil && !errors.Is(err, repositories.ErrA2AProxyNotFound) {
			return fmt.Errorf("failed to delete the A2A proxy: %w", err)
		}
		// OnCardSourceChanged writes the card row, so this service clears it as well.
		err = s.cardRepo.WithTx(tx).DeleteForAgentEnv(ctx, t.key.OUID, t.key.ProjectName, t.key.AgentName, t.key.EnvironmentName)
		if err != nil {
			return fmt.Errorf("failed to delete the agent card: %w", err)
		}
		return nil
	})
	return teardown, err
}

func a2aProxyAuditOptions(t a2aProxyTarget, upstreamAuthHeader string, authChanged bool) []audit.Option {
	return []audit.Option{
		audit.Org(t.key.OUID),
		audit.ResourceNamed(audit.ResourceAgent, t.agent.UUID, t.agent.Name),
		audit.Project(t.key.ProjectName),
		audit.Environment(t.key.EnvironmentName),
		audit.Detail("agentName", t.agent.Name),
		audit.Detail("upstreamAuthHeader", upstreamAuthHeader),
		audit.Detail("upstreamAuthChanged", authChanged),
	}
}

// ensureDefaultAgentConfig gives a newly proxied environment the inbound defaults a platform A2A agent
// deploys with (API-key security on, CORS from the environment config, card CORS inherited). A saved config wins.
func ensureDefaultAgentConfig(ctx context.Context, configs repositories.AgentConfigRepository, key a2aPublicationKey) error {
	api := withA2AVersionHeader(resolveAPIConfig(nil, nil, nil, nil, nil, true))
	err := configs.CreateIfAbsent(ctx, &models.AgentConfig{
		OUID:                  key.OUID,
		ProjectName:           key.ProjectName,
		AgentName:             key.AgentName,
		EnvironmentName:       key.EnvironmentName,
		EnableApiKeySecurity:  api.EnableApiKeySecurity,
		CORSEnabled:           api.CORSEnabled,
		CORSAllowOrigins:      api.CORSAllowOrigins,
		CORSAllowMethods:      api.CORSAllowMethods,
		CORSAllowHeaders:      api.CORSAllowHeaders,
		CORSAllowCredentials:  api.CORSAllowCredentials,
		EnableOAuthSecurity:   api.EnableOAuthSecurity,
		OAuthIssuers:          api.OAuthIssuers,
		OAuthAudiences:        api.OAuthAudiences,
		OAuthHeaderName:       api.OAuthHeaderName,
		OAuthAuthHeaderPrefix: api.OAuthAuthHeaderPrefix,
		OAuthForwardToken:     api.OAuthForwardToken,
	})
	if err != nil {
		return fmt.Errorf("failed to write the default agent config: %w", err)
	}
	return nil
}

// validateA2AUpstreamAuth enforces what the gateway can carry safely: it pastes header and value into
// single-quoted YAML (escaping only %) and renders {{ }} as templates with secret and env functions.
func validateA2AUpstreamAuth(auth *models.A2AProxyAuthUpdate) error {
	if auth == nil {
		return nil
	}
	switch {
	case !a2aUpstreamHeaderPattern.MatchString(auth.Header):
		return fmt.Errorf("%w: header may contain only letters, digits, '-' and '_'", utils.ErrInvalidUpstreamAuth)
	case len(auth.Header) > a2aUpstreamHeaderMaxLen:
		return fmt.Errorf("%w: header must be at most %d characters", utils.ErrInvalidUpstreamAuth, a2aUpstreamHeaderMaxLen)
	case strings.EqualFold(auth.Header, "Host"):
		return fmt.Errorf("%w: the Host header cannot be set upstream", utils.ErrInvalidUpstreamAuth)
	}
	if auth.Value == nil {
		return nil
	}
	value := *auth.Value
	switch {
	case value == "":
		return fmt.Errorf("%w: value must not be empty; omit upstreamAuth to clear it", utils.ErrInvalidUpstreamAuth)
	case len(value) > a2aUpstreamValueMaxLen:
		return fmt.Errorf("%w: value must be at most %d bytes", utils.ErrInvalidUpstreamAuth, a2aUpstreamValueMaxLen)
	case strings.ContainsAny(value, "'\r\n"):
		return fmt.Errorf("%w: value must not contain quotes or line breaks", utils.ErrInvalidUpstreamAuth)
	case strings.Contains(value, "{{"):
		return fmt.Errorf("%w: value must not contain '{{'", utils.ErrInvalidUpstreamAuth)
	}
	return nil
}

func encryptA2AUpstreamSecret(value string, key []byte) (string, error) {
	ciphertext, err := utils.EncryptBytes([]byte(value), key)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt the upstream secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
```

Nothing wires `NewA2AProxyService` yet; Task 10 adds it to wire, once the card service consumes it.

- [ ] **Step 5: Run the tests**

Run: `make test-run RUN='TestSetA2AProxy|TestValidateA2AUpstreamAuth|TestDeleteA2AProxy|TestGetA2AProxy|TestRefreshA2AProxy|TestA2AProxyOn|TestOnCardSource|TestIsProxied' PKG=./services/` then `make test-unit`
Expected: PASS.

- [ ] **Step 6: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./models/... ./utils/... ./audit/...
git add services/a2a_proxy_service.go services/a2a_proxy_service_unit_test.go models/a2a_proxy.go utils/errors.go audit/actions_domain.go
git commit -m "Add the A2A proxy service for external agents"
```

---

### Task 10: Card service defers to the proxy on a proxied environment

**Files:**
- Modify: `agent-manager-service/services/a2a_agent_card_service.go` (struct and constructor `:49-62`; `RefreshA2AAgentCard` `:82-104`; `SetA2ACardSource` `:106-125`; `DeleteA2ACardSource` `:127-144`)
- Test: `agent-manager-service/services/a2a_agent_card_service_unit_test.go`
- Modify: `agent-manager-service/wiring/wire.go`; generate `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: Task 9 `A2AProxyHooks`, `NewA2AProxyService`, `utils.ErrEnvironmentNotProxied`.
- Produces:
  - `NewA2AAgentCardService(ocClient client.OpenChoreoClient, cardRepo repositories.A2AAgentCardRepository, proxyHooks A2AProxyHooks, logger *slog.Logger)`
  - `func wiring.ProvideA2AProxyHooks(svc services.A2AProxyService) services.A2AProxyHooks`
  - Test helpers: `type proxyHooksStub` (func fields `IsProxiedFunc`, `OnCardSourceChangedFunc`, `OnCardSourceRemovedFunc`, `OnCardRefreshFunc`); `func cardServiceWithHooks(provisioning, subType string, hooks A2AProxyHooks) (*a2aAgentCardService, *repomocks.A2AAgentCardRepositoryMock, *clientmocks.OpenChoreoClientMock)`; `func proxiedHooks() *proxyHooksStub`.

On a proxied environment:
- `SetA2ACardSource` validates the URL as today, then hands it to `OnCardSourceChanged` instead of calling `Enqueue`; the hook writes the URL, parks the card and republishes in one transaction, so the row is never due while the gateway still fronts the old origin.
- `DeleteA2ACardSource` calls `OnCardSourceRemoved`, which tears down and deletes the card row in its own transaction. If that reports `ErrEnvironmentNotProxied` (the proxy went meanwhile), the card row is deleted directly.
- `RefreshA2AAgentCard` calls `OnCardRefresh` **before** the `Requeue` branch.

The fail-open `agent:set-card-source` / `agent:remove-card-source` records are unchanged.

- [ ] **Step 1: Write the tests**

In `services/a2a_agent_card_service_unit_test.go`, split `cardServiceFor` so a test can pass its own hooks. Keep the existing body, change the constructor call, and add the stub:

```go
// proxyHooksStub is the hand-written stub for the in-package A2AProxyHooks.
type proxyHooksStub struct {
	IsProxiedFunc           func(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (bool, error)
	OnCardSourceChangedFunc func(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string) error
	OnCardSourceRemovedFunc func(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error
	OnCardRefreshFunc       func(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error
}

func (s *proxyHooksStub) IsProxied(ctx context.Context, ouID, projectName, agentName string, environmentUUID uuid.UUID) (bool, error) {
	return s.IsProxiedFunc(ctx, ouID, projectName, agentName, environmentUUID)
}

func (s *proxyHooksStub) OnCardSourceChanged(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string) error {
	return s.OnCardSourceChangedFunc(ctx, agent, ouID, projectName, env, sourceURL)
}

func (s *proxyHooksStub) OnCardSourceRemoved(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error {
	return s.OnCardSourceRemovedFunc(ctx, agent, ouID, projectName, env)
}

func (s *proxyHooksStub) OnCardRefresh(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error {
	return s.OnCardRefreshFunc(ctx, agent, ouID, projectName, env)
}

func directHooks() *proxyHooksStub {
	return &proxyHooksStub{
		IsProxiedFunc: func(context.Context, string, string, string, uuid.UUID) (bool, error) { return false, nil },
	}
}

func proxiedHooks() *proxyHooksStub {
	return &proxyHooksStub{
		IsProxiedFunc: func(context.Context, string, string, string, uuid.UUID) (bool, error) { return true, nil },
	}
}

func cardServiceFor(provisioning, subType string) (*a2aAgentCardService, *repomocks.A2AAgentCardRepositoryMock, *clientmocks.OpenChoreoClientMock) {
	return cardServiceWithHooks(provisioning, subType, directHooks())
}
```

Rename the existing `cardServiceFor` body to `cardServiceWithHooks(provisioning, subType string, hooks A2AProxyHooks)` and end it with `return NewA2AAgentCardService(oc, repo, hooks, testLogger()).(*a2aAgentCardService), repo, oc`.

Append:

```go
// The proxy writes the URL inside its own transaction; a plain Enqueue first would leave the row due.
func TestSetA2ACardSourceHandsAProxiedEnvironmentToTheProxy(t *testing.T) {
	hooks := proxiedHooks()
	var handed string
	hooks.OnCardSourceChangedFunc = func(_ context.Context, _ *models.AgentResponse, _, _ string, _ *models.EnvironmentResponse, sourceURL string) error {
		handed = sourceURL
		return nil
	}
	svc, repo, _ := cardServiceWithHooks("external", "a2a-agent", hooks)
	repo.EnqueueFunc = nil // a plain write would un-park the row before the gateway fronts the new origin

	require.NoError(t, svc.SetA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev", " https://93.184.215.14/.well-known/agent-card.json "))

	assert.Equal(t, "https://93.184.215.14/.well-known/agent-card.json", handed, "trimmed, as the direct path stores it")
}

func TestDeleteA2ACardSourceTearsDownAProxiedEnvironment(t *testing.T) {
	hooks := proxiedHooks()
	removed := 0
	hooks.OnCardSourceRemovedFunc = func(context.Context, *models.AgentResponse, string, string, *models.EnvironmentResponse) error {
		removed++
		return nil
	}
	svc, repo, _ := cardServiceWithHooks("external", "a2a-agent", hooks)
	repo.DeleteForAgentEnvFunc = nil // the proxy service deletes the row in its own transaction

	require.NoError(t, svc.DeleteA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	assert.Equal(t, 1, removed)
}

func TestDeleteA2ACardSourceFallsBackWhenTheProxyIsAlreadyGone(t *testing.T) {
	hooks := proxiedHooks()
	hooks.OnCardSourceRemovedFunc = func(context.Context, *models.AgentResponse, string, string, *models.EnvironmentResponse) error {
		return utils.ErrEnvironmentNotProxied
	}
	svc, repo, _ := cardServiceWithHooks("external", "a2a-agent", hooks)

	require.NoError(t, svc.DeleteA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	assert.Len(t, repo.DeleteForAgentEnvCalls(), 1)
}

// A requeue would un-park the card before the gateway has the republished Agent.
func TestRefreshA2AAgentCardRepublishesAProxiedEnvironment(t *testing.T) {
	hooks := proxiedHooks()
	refreshed := 0
	hooks.OnCardRefreshFunc = func(context.Context, *models.AgentResponse, string, string, *models.EnvironmentResponse) error {
		refreshed++
		return nil
	}
	svc, repo, _ := cardServiceWithHooks("external", "a2a-agent", hooks)
	repo.RequeueFunc = nil

	require.NoError(t, svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	assert.Equal(t, 1, refreshed)
}

func TestCardSourceRemovalDoesNotMaskAProxyLookupFailure(t *testing.T) {
	hooks := &proxyHooksStub{
		IsProxiedFunc: func(context.Context, string, string, string, uuid.UUID) (bool, error) { return false, assert.AnError },
	}
	svc, repo, _ := cardServiceWithHooks("external", "a2a-agent", hooks)

	assert.ErrorIs(t, svc.DeleteA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev"), assert.AnError)
	assert.Empty(t, repo.DeleteForAgentEnvCalls())
}
```

- [ ] **Step 2: Verify they fail**

Run: `make test-run RUN='TestSetA2ACardSource|TestDeleteA2ACardSource|TestRefreshA2AAgentCard|TestCardSourceRemoval|TestGetA2AAgentCard' PKG=./services/`
Expected: FAIL — too many arguments to `NewA2AAgentCardService`.

- [ ] **Step 3: Implement**

In `services/a2a_agent_card_service.go`, add the field `proxyHooks A2AProxyHooks`, take it as the constructor's third parameter, and store it. Then change the three methods.

`RefreshA2AAgentCard`, between the platform branch and the `Requeue`:

```go
	proxied, err := s.isProxied(ctx, agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	if proxied {
		// A requeue would un-park the card before the gateway has the republished Agent.
		return s.proxyHooks.OnCardRefresh(ctx, agent, ouID, projectName, env)
	}
```

`SetA2ACardSource`, replacing the `err = s.enqueue(...)` line:

```go
	err = s.storeSource(ctx, agent, ouID, projectName, env, sourceURL)
```

`DeleteA2ACardSource`: keep the tier check's environment (`env, err := requireEnvironmentTier(...)`), and replace the `DeleteForAgentEnv` call with `err = s.removeSource(ctx, agent, ouID, projectName, env)`. Leave the audit record after it unchanged.

New helpers:

```go
// isProxied reports whether the environment routes the agent through its gateway.
func (s *a2aAgentCardService) isProxied(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse,
) (bool, error) {
	envUUID, err := uuid.Parse(env.UUID)
	if err != nil {
		return false, fmt.Errorf("invalid UUID for environment %s: %w", env.Name, err)
	}
	return s.proxyHooks.IsProxied(ctx, ouID, projectName, agent.Name, envUUID)
}

// storeSource writes the URL directly on a direct environment. On a proxied one the proxy writes it,
// parks the card and republishes in one transaction: the gateway must front the new origin before
// its card can be fetched through it, and a row left due in between would fetch the old one.
func (s *a2aAgentCardService) storeSource(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse, sourceURL string,
) error {
	proxied, err := s.isProxied(ctx, agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	if proxied {
		return s.proxyHooks.OnCardSourceChanged(ctx, agent, ouID, projectName, env, sourceURL)
	}
	return s.enqueue(ctx, agent, ouID, projectName, env, models.A2AAgentCardSourceExternal, sourceURL)
}

// removeSource deletes the card row; on a proxied environment the proxy also switches to direct, in one transaction.
func (s *a2aAgentCardService) removeSource(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse,
) error {
	proxied, err := s.isProxied(ctx, agent, ouID, projectName, env)
	if err != nil {
		return err
	}
	if proxied {
		err := s.proxyHooks.OnCardSourceRemoved(ctx, agent, ouID, projectName, env)
		if !errors.Is(err, utils.ErrEnvironmentNotProxied) {
			return err
		}
		// The proxy was removed meanwhile; only the card row is left to delete.
	}
	if err := s.cardRepo.DeleteForAgentEnv(ctx, ouID, projectName, agent.Name, env.Name); err != nil {
		return fmt.Errorf("failed to delete agent card: %w", err)
	}
	return nil
}
```

Wiring, in `wiring/wire.go`:
- add `services.NewA2AProxyService,` and `ProvideA2AProxyHooks,` to `serviceProviderSet` after `services.NewA2APublisher,`;
- add the provider:

```go
// ProvideA2AProxyHooks gives the card service the proxy hooks only, not the whole proxy service.
func ProvideA2AProxyHooks(svc services.A2AProxyService) services.A2AProxyHooks {
	return svc
}
```

Run: `make wire`
Expected: both injectors build `a2AProxyService` (with `v`, the encryption key) and pass `a2AProxyHooks` to `NewA2AAgentCardService`. If wire reports an import cycle or an unused provider, stop and report it; do not reshape the services to satisfy it.

- [ ] **Step 4: Run the tests**

Run: `make test-run RUN='TestSetA2ACardSource|TestDeleteA2ACardSource|TestRefreshA2AAgentCard|TestCardSourceRemoval|TestGetA2AAgentCard' PKG=./services/` then `make test-unit`
Expected: PASS. The existing card tests run through `cardServiceFor`, whose hooks report "direct", so they still exercise the direct paths.

- [ ] **Step 5: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./wiring/...
go build -tags=integration ./...
git add services/a2a_agent_card_service.go services/a2a_agent_card_service_unit_test.go wiring/wire.go wiring/wire_gen.go
git commit -m "Route card source changes on a proxied environment through the proxy"
```

---

### Task 11: Deploy settings for proxied external agents

**Files:**
- Modify: `agent-manager-service/services/agent_manager.go` (`UpdateAgentDeploySettings`, `:5012-5150`)
- Create: `agent-manager-service/services/a2a_proxy_deploy_settings_unit_test.go`

**Interfaces:**
- Consumes: Task 4 `ShouldPublish`, `republishA2AAgent`; Task 9 test helpers (`externalA2AAgent`, `proxyTestEnvUUID`).
- Produces:
  - `func deploySettingsApplyTo(agent *models.AgentResponse) bool`
  - `func (s *agentManagerService) requireProxiedEnvironment(ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse) error`
  - `func (s *agentManagerService) applyReleaseBindingSettings(ctx context.Context, ouID, agentName string, req *spec.UpdateAgentDeploySettingsRequest, agent *models.AgentResponse, targetEnv *models.EnvironmentResponse, policies []map[string]interface{}, artifactUUID string, resilienceTimeoutSeconds int32, enableAutoInstrumentation bool, existingInstrumentationVersion *string) (*string, error)` (the build-and-binding block, moved out unchanged)

- [ ] **Step 1: Write the tests**

`services/a2a_proxy_deploy_settings_unit_test.go`:

```go
package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// deploySettingsService wires UpdateAgentDeploySettings for the external A2A agent in externalA2AAgent.
func deploySettingsService(configs *repomocks.AgentConfigRepositoryMock, publisher *A2APublisher) *agentManagerService {
	oc := &clientmocks.OpenChoreoClientMock{
		GetOrganizationFunc: func(_ context.Context, name string) (*models.OrganizationResponse, error) {
			return &models.OrganizationResponse{Name: name}, nil
		},
		GetComponentFunc: func(context.Context, string, string, string) (*models.AgentResponse, error) {
			return externalA2AAgent(), nil
		},
		GetEnvironmentFunc: func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
			return &models.EnvironmentResponse{Name: name, UUID: proxyTestEnvUUID}, nil
		},
		// UpdateReleaseBindingTraitConfigsFunc is nil: an external agent has no release binding.
	}
	artifacts := &repomocks.ArtifactRepositoryMock{
		GetByHandleFunc: func(handle, ouID string) (*models.Artifact, error) {
			return &models.Artifact{UUID: uuid.New(), Handle: handle, Kind: models.KindAgent, OUID: ouID}, nil
		},
	}
	return NewAgentManagerService(nil, oc, nil, nil, nil, configs, nil, nil, artifacts, nil, nil, nil, nil, nil, nil,
		publisher, nil, nil, nil, testLogger()).(*agentManagerService)
}

func savedConfigs() *repomocks.AgentConfigRepositoryMock {
	return &repomocks.AgentConfigRepositoryMock{
		GetFunc: func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
			return &models.AgentConfig{EnableApiKeySecurity: true, CORSEnabled: true}, nil
		},
		UpsertFunc: func(context.Context, *models.AgentConfig) error { return nil },
	}
}

func TestUpdateAgentDeploySettingsRepublishesAProxiedExternalAgent(t *testing.T) {
	var queued []models.A2APublication
	pubs := &repomocks.A2APublicationRepositoryMock{
		EnqueueFunc: func(_ context.Context, pub *models.A2APublication) error {
			queued = append(queued, *pub)
			return nil
		},
	}
	proxies := &repomocks.A2AProxyRepositoryMock{
		GetByEnvUUIDFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
			return &models.A2AProxy{}, nil
		},
	}
	configs := savedConfigs()
	svc := deploySettingsService(configs, NewA2APublisher(nil, pubs, proxies, nil, nil, nil, nil, testLogger()))
	apiKeys := false

	err := svc.UpdateAgentDeploySettings(tierGrantedCtx(t), "org-1", "checkout", "weather",
		&spec.UpdateAgentDeploySettingsRequest{EnvironmentName: "dev", EnableApiKeySecurity: &apiKeys})

	require.NoError(t, err)
	require.Len(t, configs.UpsertCalls(), 1)
	assert.False(t, configs.UpsertCalls()[0].Config.EnableApiKeySecurity)
	require.Len(t, queued, 1, "the gateway picks the new settings up from a republish")
	assert.Equal(t, models.A2APublicationSourceExternal, queued[0].Source)
}

func TestUpdateAgentDeploySettingsRejectsADirectExternalEnvironment(t *testing.T) {
	proxies := &repomocks.A2AProxyRepositoryMock{
		GetByEnvUUIDFunc: func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
			return nil, repositories.ErrA2AProxyNotFound
		},
	}
	configs := savedConfigs()
	svc := deploySettingsService(configs, NewA2APublisher(nil, &repomocks.A2APublicationRepositoryMock{}, proxies, nil, nil, nil, nil, testLogger()))

	err := svc.UpdateAgentDeploySettings(tierGrantedCtx(t), "org-1", "checkout", "weather",
		&spec.UpdateAgentDeploySettingsRequest{EnvironmentName: "dev"})

	require.ErrorIs(t, err, utils.ErrInvalidInput)
	assert.Contains(t, err.Error(), "gateway")
	assert.Empty(t, configs.UpsertCalls())
}
```

- [ ] **Step 2: Verify they fail**

Run: `make test-run RUN='TestUpdateAgentDeploySettings' PKG=./services/`
Expected: the two new tests FAIL. "Deploy settings only apply to API-type agents" is returned for the proxied one, and the direct one fails its "gateway" assertion. The existing `TestUpdateAgentDeploySettings_*` tests still PASS.

- [ ] **Step 3: Implement**

In `UpdateAgentDeploySettings`:

1. Replace the `agent.Type.Type != string(utils.AgentTypeAPI)` check with `if !deploySettingsApplyTo(agent) {` and keep the same error.

2. Right after `targetEnv, err := s.requireEnvTier(...)` and its error check:

```go
	external := isExternalProvisioned(agent)
	if external {
		if err := s.requireProxiedEnvironment(ctx, agent, ouID, projectName, targetEnv); err != nil {
			return err
		}
	}
```

3. Keep the `artifact, artifactErr := ensureAgentEnvAPIArtifact(...)` block. Replace everything from `isPythonBuildpack := …` through the `UpdateReleaseBindingTraitConfigs` error check with:

```go
	var existingInstrumentationVersion *string
	if existingConfig != nil {
		existingInstrumentationVersion = existingConfig.InstrumentationVersion
	}
	resolvedInstrumentationVersion := existingInstrumentationVersion
	// An external agent has no release binding: its settings reach the gateway through the republish below.
	if !external {
		resolvedInstrumentationVersion, err = s.applyReleaseBindingSettings(ctx, ouID, agentName, req, agent, targetEnv,
			policies, artifact.UUID.String(), resilienceTimeoutSeconds, tracingCfg.EnableAutoInstrumentation, existingInstrumentationVersion)
		if err != nil {
			return err
		}
	}
```

The persist block and `republishA2AAgent` call after it stay as they are. `republishA2AAgent` now goes through `ShouldPublish` and picks the `external` source (Task 4).

New helpers, placed after `UpdateAgentDeploySettings`:

```go
// deploySettingsApplyTo is true for API agents and for external A2A agents, whose proxied
// environments take inbound auth and CORS at the gateway.
func deploySettingsApplyTo(agent *models.AgentResponse) bool {
	switch agent.Type.Type {
	case string(utils.AgentTypeAPI):
		return true
	case string(utils.AgentTypeExternalAPI):
		return utils.IsA2AAgentSubType(agent.Type.SubType)
	}
	return false
}

// requireProxiedEnvironment refuses deploy settings for a direct external environment, where nothing applies them.
func (s *agentManagerService) requireProxiedEnvironment(
	ctx context.Context, agent *models.AgentResponse, ouID, projectName string, env *models.EnvironmentResponse,
) error {
	envUUID, err := uuid.Parse(env.UUID)
	if err != nil {
		return fmt.Errorf("environment %q has an unparseable UUID %q: %w", env.Name, env.UUID, err)
	}
	proxied := false
	if s.a2aPublisher != nil {
		if proxied, err = s.a2aPublisher.ShouldPublish(ctx, agent, ouID, projectName, envUUID); err != nil {
			return err
		}
	}
	if !proxied {
		return fmt.Errorf("%w: deploy settings apply only to environments that route this agent through the gateway",
			utils.ErrInvalidInput)
	}
	return nil
}

// applyReleaseBindingSettings writes the settings onto the agent's release binding (one atomic
// update, which rolls the pods) and returns the instrumentation version to persist.
func (s *agentManagerService) applyReleaseBindingSettings(
	ctx context.Context, ouID, agentName string, req *spec.UpdateAgentDeploySettingsRequest,
	agent *models.AgentResponse, targetEnv *models.EnvironmentResponse, policies []map[string]interface{},
	artifactUUID string, resilienceTimeoutSeconds int32, enableAutoInstrumentation bool, existingInstrumentationVersion *string,
) (*string, error) {
	isPythonBuildpack := agent.Build != nil && agent.Build.Buildpack != nil && agent.Build.Buildpack.Language == string(utils.LanguagePython)
	isBallerinaBuildpack := agent.Build != nil && agent.Build.Buildpack != nil && agent.Build.Buildpack.Language == string(utils.LanguageBallerina)
	// Resolve the instrumentation version: request override (validated) -> the
	// env's currently-pinned version. Its image is written as a per-env override on the OTEL trait.
	languageVersion := ""
	if agent.Build != nil && agent.Build.Buildpack != nil {
		languageVersion = agent.Build.Buildpack.LanguageVersion
	}
	resolvedInstrumentationVersion, instrumentationImage, err := s.resolveInstrumentationImageOverride(
		isPythonBuildpack, languageVersion, req.InstrumentationVersion.Get(), existingInstrumentationVersion,
	)
	if err != nil {
		return nil, err
	}
	isA2AAgent := utils.IsA2AAgentSubType(agent.Type.SubType)
	traitEnvConfigs := buildTraitEnvConfigs(agentName, policies, artifactUUID, resilienceTimeoutSeconds,
		isPythonBuildpack, isBallerinaBuildpack, enableAutoInstrumentation, instrumentationImage, !isA2AAgent)

	// Apply to the release binding (atomic: trait configs + component-type configs + restartedAt in a single update).
	settingsCTConfigs := buildComponentTypeEnvConfigs(targetEnv)
	if err := s.ocClient.UpdateReleaseBindingTraitConfigs(ctx, ouID, agentName, req.EnvironmentName, traitEnvConfigs, settingsCTConfigs); err != nil {
		s.logger.Error("Failed to update release binding deploy settings", "agentName", agentName, "environment", req.EnvironmentName, "error", err)
		return nil, fmt.Errorf("failed to update deploy settings: %w", err)
	}
	return resolvedInstrumentationVersion, nil
}
```

Before deleting the old lines, diff them against the moved code: the only differences should be the `return nil, …` forms and the `isA2AAgent` local. The outer `isA2AAgent` (used for card CORS validation) stays where it is.

- [ ] **Step 4: Run the tests**

Run: `make test-run RUN='TestUpdateAgentDeploySettings|TestEnvTier' PKG=./services/` then `make test-unit`
Expected: PASS, including the existing internal-agent deploy-settings tests.

- [ ] **Step 5: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/...
git add services/agent_manager.go services/a2a_proxy_deploy_settings_unit_test.go
git commit -m "Accept deploy settings for proxied external A2A agents"
```

---

### Task 12: Tear down A2A publications when an environment is deleted

**Files:**
- Modify: `agent-manager-service/services/a2a_publisher.go` (add `TeardownEnvironment`), `agent-manager-service/services/environment_service.go` (struct and constructor `:93-118`; `DeleteEnvironment` after the card cleanup, `:365-370`)
- Test: `agent-manager-service/services/a2a_publisher_unit_test.go`, `agent-manager-service/services/environment_service_unit_test.go`
- Generate: `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: Task 1 `ListForEnvironment`, `DeleteForEnvironment`; Task 2 `A2AProxyRepository.DeleteForEnvironment`; Task 4 `TxRunner`, `TeardownEnv`, `SendTeardown`.
- Produces:
  - `(*A2APublisher) TeardownEnvironment(ctx context.Context, ouID string, envUUID uuid.UUID) error`
  - `NewEnvironmentService(…, a2aCardRepo repositories.A2AAgentCardRepository, a2aPublisher *A2APublisher)`: the publisher is a new last parameter.

Each publication row in the deleted environment is torn down in its own transaction, and its artifact deleted with it. Then every remaining publication and proxy row of the environment is deleted, and the gateways are told. The whole step is best effort and logged, like the card cleanup next to it; the environment is already gone from OpenChoreo by then.

- [ ] **Step 1: Write the tests**

Append to `services/a2a_publisher_unit_test.go`:

```go
func TestA2APublisherTeardownEnvironmentRemovesEverythingAndTellsTheGateways(t *testing.T) {
	m := newPublisherMocks(t)
	envUUID, artifactUUID := uuid.New(), uuid.New()
	pub := models.A2APublication{
		OUID: "org-1", ProjectName: "checkout", AgentName: "weather", EnvironmentName: "dev",
		EnvironmentUUID: envUUID, ArtifactUUID: artifactUUID, Source: models.A2APublicationSourceExternal,
	}
	m.pubs.ListForEnvironmentFunc = func(context.Context, string, uuid.UUID) ([]models.A2APublication, error) {
		return []models.A2APublication{pub}, nil
	}
	m.pubs.DeleteForEnvironmentFunc = func(context.Context, string, uuid.UUID) error { return nil }
	m.proxies.DeleteForEnvironmentFunc = func(context.Context, string, uuid.UUID) error { return nil }
	m.txDeployments.DeleteForArtifactFunc = func(context.Context, uuid.UUID, string) error { return nil }
	m.txPubs.DeleteForAgentEnvFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	m.artifacts.DeleteFunc = func(tx *gorm.DB, _ string) error {
		assert.Same(t, txSentinel, tx)
		return nil
	}

	require.NoError(t, m.publisher().TeardownEnvironment(context.Background(), "org-1", envUUID))

	require.Len(t, m.artifacts.DeleteCalls(), 1, "a deleted environment needs no artifact")
	assert.Len(t, m.pubs.DeleteForEnvironmentCalls(), 1)
	assert.Len(t, m.proxies.DeleteForEnvironmentCalls(), 1)
	require.Len(t, m.hub.published, 1)
	assert.Equal(t, artifactUUID.String(), deletedAgentID(t, m.hub.published[0]))
}

// One agent's failure does not keep the environment's other rows alive.
func TestA2APublisherTeardownEnvironmentStillDropsTheRowsAfterAFailure(t *testing.T) {
	m := newPublisherMocks(t)
	m.pubs.ListForEnvironmentFunc = func(context.Context, string, uuid.UUID) ([]models.A2APublication, error) {
		return []models.A2APublication{{AgentName: "weather", ArtifactUUID: uuid.New()}}, nil
	}
	m.txErr = assert.AnError // every per-row transaction fails to commit
	m.pubs.DeleteForEnvironmentFunc = func(context.Context, string, uuid.UUID) error { return nil }
	m.proxies.DeleteForEnvironmentFunc = func(context.Context, string, uuid.UUID) error { return nil }

	err := m.publisher().TeardownEnvironment(context.Background(), "org-1", uuid.New())

	assert.ErrorIs(t, err, assert.AnError)
	assert.Len(t, m.pubs.DeleteForEnvironmentCalls(), 1)
	assert.Len(t, m.proxies.DeleteForEnvironmentCalls(), 1)
	assert.Empty(t, m.hub.published, "a teardown that did not commit is not announced")
}
```

In `services/environment_service_unit_test.go`, append `, nil` as the new last argument to every existing `NewEnvironmentService(` call. At `bc19012cc` they are at `:67`, `:92`, `:106`, `:692`, `:707`, `:719`, `:894`, `:923`, `:953`, `:1024`, `:1073`, `:1173`, `:1405` and `:1929`; the three at `:692-719` span two lines. Then append:

```go
// An external agent has no release binding to stop the delete, so its gateway Agent must be withdrawn here.
func TestEnvironmentService_DeleteEnvironmentTearsDownA2APublications(t *testing.T) {
	const org, envID = "acme", "dev"
	const envUUID = "44444444-4444-4444-4444-444444444444"
	m := newPublisherMocks(t)
	var torndown []uuid.UUID
	m.pubs.ListForEnvironmentFunc = func(_ context.Context, _ string, env uuid.UUID) ([]models.A2APublication, error) {
		torndown = append(torndown, env)
		return []models.A2APublication{}, nil
	}
	m.pubs.DeleteForEnvironmentFunc = func(context.Context, string, uuid.UUID) error { return nil }
	m.proxies.DeleteForEnvironmentFunc = func(context.Context, string, uuid.UUID) error { return nil }
	svc := NewEnvironmentService(discardLogger(), cleanedGatewayRepo(), deletingEnvOC(envUUID, nil),
		&clientmocks.ThunderProberMock{}, nil, nil, nil, nil, nil, m.publisher())

	require.NoError(t, svc.DeleteEnvironment(context.Background(), org, envID))

	assert.Equal(t, []uuid.UUID{uuid.MustParse(envUUID)}, torndown)
	assert.Len(t, m.proxies.DeleteForEnvironmentCalls(), 1)
}

func TestEnvironmentService_DeleteEnvironmentKeepsA2APublicationsWhenOCRefuses(t *testing.T) {
	m := newPublisherMocks(t) // ListForEnvironmentFunc is nil: tearing down would panic
	svc := NewEnvironmentService(discardLogger(), cleanedGatewayRepo(),
		deletingEnvOC("44444444-4444-4444-4444-444444444444", errors.New("release bindings still exist")),
		&clientmocks.ThunderProberMock{}, nil, nil, nil, nil, nil, m.publisher())

	require.Error(t, svc.DeleteEnvironment(context.Background(), "acme", "dev"))
}
```

- [ ] **Step 2: Verify they fail**

Run: `make test-run RUN='TestA2APublisherTeardownEnvironment|TestEnvironmentService_DeleteEnvironment' PKG=./services/`
Expected: FAIL to compile — `TeardownEnvironment undefined`, too many arguments to `NewEnvironmentService`.

- [ ] **Step 3: Implement**

Append to `services/a2a_publisher.go`:

```go
// TeardownEnvironment removes every A2A publication, gateway deployment, agent artifact and proxy row
// of a deleted environment, then tells the gateways. One agent's failure does not stop the rest.
func (p *A2APublisher) TeardownEnvironment(ctx context.Context, ouID string, envUUID uuid.UUID) error {
	pubs, err := p.pubRepo.ListForEnvironment(ctx, ouID, envUUID)
	if err != nil {
		return fmt.Errorf("failed to list the environment's A2A publications: %w", err)
	}
	var errs []error
	var teardowns []a2aTeardown
	for _, pub := range pubs {
		teardown, err := p.teardownDeletedEnv(ctx, pub)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		teardowns = append(teardowns, teardown)
	}
	// Rows a failed teardown left, and proxies that never published, go regardless: the environment is gone.
	if err := p.pubRepo.DeleteForEnvironment(ctx, ouID, envUUID); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete the environment's A2A publications: %w", err))
	}
	if err := p.proxyRepo.DeleteForEnvironment(ctx, ouID, envUUID); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete the environment's A2A proxies: %w", err))
	}
	for _, teardown := range teardowns {
		p.SendTeardown(ctx, teardown)
	}
	return errors.Join(errs...)
}

// teardownDeletedEnv removes one publication together with its artifact, which a deleted environment no longer needs.
func (p *A2APublisher) teardownDeletedEnv(ctx context.Context, pub models.A2APublication) (a2aTeardown, error) {
	key := a2aPublicationKey{
		OUID: pub.OUID, ProjectName: pub.ProjectName, AgentName: pub.AgentName,
		EnvironmentName: pub.EnvironmentName, EnvironmentUUID: pub.EnvironmentUUID,
	}
	var teardown a2aTeardown
	err := p.tx(ctx, func(tx *gorm.DB) error {
		var err error
		if teardown, err = p.TeardownEnv(ctx, tx, key, pub.ArtifactUUID); err != nil {
			return err
		}
		err = p.artifactRepo.Delete(tx, pub.ArtifactUUID.String())
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("failed to delete the agent's API artifact: %w", err)
		}
		return nil
	})
	if err != nil {
		return a2aTeardown{}, fmt.Errorf("agent %s: %w", pub.AgentName, err)
	}
	return teardown, nil
}
```

In `services/environment_service.go`, add `a2aPublisher *A2APublisher` as the struct's and the constructor's last field and parameter, and store it. In `DeleteEnvironment`, after the card-cleanup block:

```go
	// An external agent has no release binding to block the delete, so a proxied environment can
	// go while its gateway still serves the Agent. Best effort, like the card cleanup above.
	if s.a2aPublisher != nil {
		if err := s.a2aPublisher.TeardownEnvironment(ctx, ouID, envUUID); err != nil {
			s.logger.Error("Environment deleted but its A2A gateway publications were not fully removed",
				"ouID", ouID, "envID", envID, "envUUID", envUUID, "error", err)
		}
	}
```

Run: `make wire`
Expected: both injectors pass `a2APublisher` to `NewEnvironmentService`.

- [ ] **Step 4: Run the tests**

Run: `make test-run RUN='TestA2APublisher|TestEnvironmentService' PKG=./services/` then `make test-unit`
Expected: PASS.

- [ ] **Step 5: Format, lint, commit**

```bash
make fmt
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./wiring/...
go build -tags=integration ./...
git add services/a2a_publisher.go services/a2a_publisher_unit_test.go services/environment_service.go \
  services/environment_service_unit_test.go wiring/wire_gen.go
git commit -m "Withdraw an environment's A2A gateway publications when it is deleted"
```

---

### Task 13: `/a2a-proxy` API (OpenAPI, controller, routes, audit labels)

Follows `/add-api-resource`: spec first, then `make spec`, the controller, the routes with per-route authz, and wire. There are **no new permissions** (steps 3 and 6 of that skill do not apply): the routes reuse `rbac.AgentRead`, `rbac.AgentUpdate` and `rbac.AgentEnvNonProduction`, exactly as the card-source routes do.

**Files:**
- Modify: `agent-manager-service/docs/api_v1_openapi.yaml`; regenerate `agent-manager-service/spec/`
- Create: `agent-manager-service/controllers/a2a_proxy_controller.go`, `agent-manager-service/controllers/a2a_proxy_controller_unit_test.go`, `agent-manager-service/api/a2a_proxy_routes.go`
- Modify: `agent-manager-service/controllers/agent_controller.go`, `agent-manager-service/controllers/error_mapping_unit_test.go`, `agent-manager-service/api/app.go`, `agent-manager-service/api/audit_coverage_test.go`, `agent-manager-service/audit/policy.go`, `agent-manager-service/audit/actions_domain_test.go`, `agent-manager-service/docs/audit-logging.md`, `agent-manager-service/wiring/wire.go`, `agent-manager-service/wiring/params.go`
- Generate: `agent-manager-service/wiring/wire_gen.go`; `cli/pkg/clients/amsvc/gen/{client,types}.gen.go` (repo root)

**Interfaces:**
- Consumes: Task 9 `services.A2AProxyService`, the sentinels, `models.A2AProxyAuthUpdate`, `models.A2AProxyStatus`, the audit actions; `agentCardPath(r)` and `handleCommonErrors` (package `controllers`).
- Produces:
  - Routes:
    - `GET    /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy` → 200 `A2AProxyResponse`, 404 when direct
    - `PUT    …/a2a-proxy` with body `SetA2AProxyRequest` → 202 / 400 / 403 / 404 / 409 / 503
    - `DELETE …/a2a-proxy` → 204 / 403 / 404 / 503
    - `POST   …/a2a-proxy/refresh` → 202 / 403 / 404
  - `spec.A2AProxyResponse` (`NewA2AProxyResponse(status, lastError, upstreamAuthHeader string)`); `spec.SetA2AProxyRequest{UpstreamAuth *A2AProxyUpstreamAuth}`; `spec.A2AProxyUpstreamAuth{Header string; Value *string}`
  - `controllers.A2AProxyController`, `controllers.NewA2AProxyController(proxyService services.A2AProxyService)`; `wiring.AppParams.A2AProxyController`

- [ ] **Step 1: Write the failing audit-label rows**

In `audit/actions_domain_test.go`, append to `pairs` in `TestDomainActionsMatchRouteDerivedActions`:

```go
		{
			"PUT /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy",
			[]rbac.Permission{rbac.AgentUpdate, rbac.AgentEnvNonProduction}, ActionAgentSetA2AProxy,
		},
		{
			"DELETE /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy",
			[]rbac.Permission{rbac.AgentUpdate, rbac.AgentEnvNonProduction}, ActionAgentRemoveA2AProxy,
		},
```

and add `ActionAgentSetA2AProxy, ActionAgentRemoveA2AProxy,` to the `critical` list in the credential-severity test in the same file (the one listing `ActionGatewaySetIdentityProvider`).

Run: `make test-run RUN=TestDomainActionsMatchRouteDerivedActions PKG=./audit/`
Expected: FAIL — the derived action is `agent:update`, but `agent:set-a2a-proxy` is wanted.

- [ ] **Step 2: Add the route labels**

`audit/policy.go`, in `actionOverrides`, under the card-source lines:

```go
	"PUT /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy":          "agent:set-a2a-proxy",
	"DELETE /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy":       "agent:remove-a2a-proxy",
	"POST /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy/refresh": "agent:refresh-a2a-proxy",
```

Re-run Step 1's command → PASS. (`TestNoStaleAuditPolicyEntries` in `./api/` fails until Step 5 registers the routes; that is expected mid-task.)

- [ ] **Step 3: Add the OpenAPI paths and schemas**

In `docs/api_v1_openapi.yaml`, directly after the `…/agent-card/source` path block (it ends before `/orgs/{orgName}/projects/{projName}/agents/{agentName}/monitors:`). The paths reuse the `&agentCardPathParams` anchor defined on the agent-card path:

```yaml
  /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy:
    parameters: *agentCardPathParams
    get:
      tags:
        - A2A Proxy
      summary: Get how an external A2A agent is routed through the environment's gateway
      description: >-
        Reports the gateway publication of a proxied environment. The upstream secret is never
        returned; a non-empty upstreamAuthHeader means one is stored.
      operationId: getA2AProxy
      responses:
        "200":
          description: Proxy state
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/A2AProxyResponse"
        "401":
          description: Unauthorized
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: The environment is not proxied, or the agent or environment does not exist
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
    put:
      tags:
        - A2A Proxy
      summary: Route an external A2A agent through the environment's gateway, or change its upstream auth
      description: >-
        Publishes the agent through the environment's ingress gateway, with routes derived from its
        registered card URL. Omitting upstreamAuth clears the stored credential; upstreamAuth without
        a value keeps the stored secret; a value replaces it. External A2A agents only.
      operationId: setA2AProxy
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/SetA2AProxyRequest"
      responses:
        "202":
          description: Saved; the gateway publication is queued
        "400":
          description: Invalid upstream auth, or the agent is not an external A2A agent
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "403":
          description: Missing the environment tier permission
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: Agent or environment not found
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "409":
          description: No card URL is registered for the environment
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "503":
          description: The audit trail is unavailable; nothing was changed
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
    delete:
      tags:
        - A2A Proxy
      summary: Stop routing an external A2A agent through the environment's gateway
      description: >-
        Removes the agent from the gateway, discards the stored upstream secret and fetches the
        agent's own card again. The environment's API keys are kept.
      operationId: deleteA2AProxy
      responses:
        "204":
          description: The environment is direct again
        "403":
          description: Missing the environment tier permission
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: The environment is not proxied, or the agent or environment does not exist
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "503":
          description: The audit trail is unavailable; nothing was changed
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"

  /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy/refresh:
    parameters: *agentCardPathParams
    post:
      tags:
        - A2A Proxy
      summary: Re-derive a proxied agent's routes from its card and republish it
      operationId: refreshA2AProxy
      responses:
        "202":
          description: Republish queued; the card is fetched again once it lands
        "403":
          description: Missing the environment tier permission
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: The environment is not proxied, or the agent or environment does not exist
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
```

Also add a `"503"` response to the existing `delete:` under `…/agent-card/source` (the block just above the new paths), worded like the proxy `DELETE`'s: Decision 9 makes that route fail-closed on a proxied environment, so it can refuse the same way.

Under `components.schemas`, after `SetAgentCardSourceRequest`:

```yaml
    A2AProxyResponse:
      type: object
      description: How an external A2A agent is published through the environment's ingress gateway.
      required:
        - status
        - lastError
        - upstreamAuthHeader
      properties:
        status:
          type: string
          enum: [pending, published, failed]
          description: The gateway publication's state.
        lastError:
          type: string
          description: Why the last publish attempt failed; empty after a success.
        upstreamAuthHeader:
          type: string
          description: >-
            The header the gateway adds upstream; empty when none. A non-empty header means a
            secret is stored. The secret itself is never returned.
    SetA2AProxyRequest:
      type: object
      properties:
        upstreamAuth:
          $ref: "#/components/schemas/A2AProxyUpstreamAuth"
    A2AProxyUpstreamAuth:
      type: object
      description: A static header the gateway adds to every call to the agent, card fetch included.
      required:
        - header
      properties:
        header:
          type: string
          maxLength: 255
          pattern: "^[A-Za-z0-9_-]+$"
          description: Header name. Host is not allowed.
        value:
          type: string
          minLength: 1
          writeOnly: true
          description: >-
            Header value: at most 8192 bytes, with no quote, line break or "{{". Omit it to keep
            the stored secret.
```

`value` has no `maxLength`: the server counts bytes and JSON Schema counts characters (card review finding "the server caps URL length in bytes, while the console and OpenAPI count characters").

Run: `make spec` (Docker must be running).
Expected: new `spec/model_a2_a_proxy_response.go`, `spec/model_set_a2_a_proxy_request.go`, `spec/model_a2_a_proxy_upstream_auth.go`; the file names may differ in their underscore split. Open the response model and confirm the constructor is `NewA2AProxyResponse(status string, lastError string, upstreamAuthHeader string)`; if the argument order differs, follow it in Step 4. Confirm `A2AProxyUpstreamAuth.Value` is `*string`.

- [ ] **Step 4: Write the controller and its test**

First the shared mapping. No controller maps `audit.ErrRecorderUnavailable` today, and the card-source `DELETE` can now return it (Decision 9), so it goes in `handleCommonErrors` (`controllers/agent_controller.go:86`), as a case before `default` (add the `audit` import; `controllers/audit_helpers.go` already imports it):

```go
	case errors.Is(err, audit.ErrRecorderUnavailable):
		// A fail-closed service refused to act unrecorded; nothing was changed.
		utils.WriteErrorResponse(w, http.StatusServiceUnavailable, "The audit trail is unavailable, so the change was not made")
```

Append to `controllers/error_mapping_unit_test.go` (add `"fmt"` and the `audit` import):

```go
// A fail-closed service that refuses to act unrecorded answers 503 on every route, not only the proxy's.
func TestHandleCommonErrors_AuditRecorderUnavailable_Is503(t *testing.T) {
	rr := httptest.NewRecorder()

	handleCommonErrors(rr, fmt.Errorf("wrapped: %w", audit.ErrRecorderUnavailable), "Failed to remove the card source")

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}
```

`controllers/a2a_proxy_controller.go`:

```go
package controllers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// A2AProxyController routes an external A2A agent through an environment's gateway.
type A2AProxyController interface {
	GetA2AProxy(w http.ResponseWriter, r *http.Request)
	SetA2AProxy(w http.ResponseWriter, r *http.Request)
	DeleteA2AProxy(w http.ResponseWriter, r *http.Request)
	RefreshA2AProxy(w http.ResponseWriter, r *http.Request)
}

type a2aProxyController struct {
	proxyService services.A2AProxyService
}

// NewA2AProxyController creates an A2AProxyController.
func NewA2AProxyController(proxyService services.A2AProxyService) A2AProxyController {
	return &a2aProxyController{proxyService: proxyService}
}

func (c *a2aProxyController) GetA2AProxy(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	status, err := c.proxyService.GetA2AProxy(r.Context(), ouID, projName, agentName, envName)
	if err != nil {
		writeA2AProxyError(w, r, err, "Failed to get the A2A proxy")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK,
		spec.NewA2AProxyResponse(string(status.Status), status.LastError, status.UpstreamAuthHeader))
}

func (c *a2aProxyController) SetA2AProxy(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req spec.SetA2AProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	err := c.proxyService.SetA2AProxy(r.Context(), ouID, projName, agentName, envName, toA2AProxyAuthUpdate(req.UpstreamAuth))
	if err != nil {
		writeA2AProxyError(w, r, err, "Failed to set the A2A proxy")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (c *a2aProxyController) DeleteA2AProxy(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	if err := c.proxyService.DeleteA2AProxy(r.Context(), ouID, projName, agentName, envName); err != nil {
		writeA2AProxyError(w, r, err, "Failed to remove the A2A proxy")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *a2aProxyController) RefreshA2AProxy(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	if err := c.proxyService.RefreshA2AProxy(r.Context(), ouID, projName, agentName, envName); err != nil {
		writeA2AProxyError(w, r, err, "Failed to refresh the A2A proxy")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// toA2AProxyAuthUpdate keeps the request's three cases apart: no auth, a header alone, a header and a value.
func toA2AProxyAuthUpdate(auth *spec.A2AProxyUpstreamAuth) *models.A2AProxyAuthUpdate {
	if auth == nil {
		return nil
	}
	return &models.A2AProxyAuthUpdate{Header: auth.Header, Value: auth.Value}
}

// writeA2AProxyError maps the proxy sentinels, then defers to handleCommonErrors (which owns the
// 503 for an audit refusal, shared with the card-source routes).
func writeA2AProxyError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	logger.GetLogger(r.Context()).Warn("A2A proxy request failed", "path", r.URL.Path, "error", err)
	switch {
	case errors.Is(err, utils.ErrEnvironmentNotProxied):
		utils.WriteErrorResponse(w, http.StatusNotFound, err.Error())
	case errors.Is(err, utils.ErrA2AProxyNeedsCardSource):
		utils.WriteErrorResponse(w, http.StatusConflict, err.Error())
	case errors.Is(err, utils.ErrAgentNotA2A),
		errors.Is(err, utils.ErrA2AProxyAgentNotExternal),
		errors.Is(err, utils.ErrInvalidUpstreamAuth):
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
	default:
		handleCommonErrors(w, err, fallback)
	}
}
```

`controllers/a2a_proxy_controller_unit_test.go` (Apache header):

```go
package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// proxyServiceStub stands in for the proxy service; the hooks are nil, so calling one would panic.
type proxyServiceStub struct {
	services.A2AProxyHooks
	GetFunc func(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AProxyStatus, error)
	SetFunc func(ctx context.Context, ouID, projectName, agentName, envName string, auth *models.A2AProxyAuthUpdate) error
}

func (s *proxyServiceStub) GetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AProxyStatus, error) {
	return s.GetFunc(ctx, ouID, projectName, agentName, envName)
}

func (s *proxyServiceStub) SetA2AProxy(ctx context.Context, ouID, projectName, agentName, envName string, auth *models.A2AProxyAuthUpdate) error {
	return s.SetFunc(ctx, ouID, projectName, agentName, envName, auth)
}

func (s *proxyServiceStub) DeleteA2AProxy(context.Context, string, string, string, string) error {
	panic("not used")
}

func (s *proxyServiceStub) RefreshA2AProxy(context.Context, string, string, string, string) error {
	panic("not used")
}

func proxyRequest(method, body string) *http.Request {
	req := httptest.NewRequest(method, "/orgs/default/projects/checkout/agents/weather/environments/dev/a2a-proxy",
		strings.NewReader(body))
	req.SetPathValue(utils.PathParamProjName, "checkout")
	req.SetPathValue(utils.PathParamAgentName, "weather")
	req.SetPathValue(utils.PathParamEnvID, "dev")
	return req.WithContext(middleware.WithResolvedOrg(req.Context(), middleware.ResolvedOrg{OUID: "ou-org"}))
}

// The response has no field the secret could leak through.
func TestGetA2AProxyReturnsTheHeaderButNoSecret(t *testing.T) {
	stub := &proxyServiceStub{GetFunc: func(context.Context, string, string, string, string) (*models.A2AProxyStatus, error) {
		return &models.A2AProxyStatus{Status: models.A2APublicationStatusPublished, UpstreamAuthHeader: "X-Key"}, nil
	}}
	rec := httptest.NewRecorder()

	NewA2AProxyController(stub).GetA2AProxy(rec, proxyRequest(http.MethodGet, ""))

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]interface{}{"status": "published", "lastError": "", "upstreamAuthHeader": "X-Key"}, body)
}

func TestSetA2AProxyKeepsTheThreeAuthCasesApart(t *testing.T) {
	v := "v"
	cases := map[string]struct {
		body string
		want *models.A2AProxyAuthUpdate
	}{
		"no upstreamAuth clears it":     {`{}`, nil},
		"a header alone keeps the secret": {`{"upstreamAuth":{"header":"X-Key"}}`, &models.A2AProxyAuthUpdate{Header: "X-Key"}},
		"a value replaces it":           {`{"upstreamAuth":{"header":"X-Key","value":"v"}}`, &models.A2AProxyAuthUpdate{Header: "X-Key", Value: &v}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var got *models.A2AProxyAuthUpdate
			stub := &proxyServiceStub{SetFunc: func(_ context.Context, _, _, _, _ string, auth *models.A2AProxyAuthUpdate) error {
				got = auth
				return nil
			}}
			rec := httptest.NewRecorder()
			NewA2AProxyController(stub).SetA2AProxy(rec, proxyRequest(http.MethodPut, tc.body))
			require.Equal(t, http.StatusAccepted, rec.Code)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestA2AProxyErrorsMapToStatusCodes(t *testing.T) {
	for sentinel, code := range map[error]int{
		utils.ErrEnvironmentNotProxied:    http.StatusNotFound,
		utils.ErrA2AProxyNeedsCardSource:  http.StatusConflict,
		utils.ErrInvalidUpstreamAuth:      http.StatusBadRequest,
		utils.ErrA2AProxyAgentNotExternal: http.StatusBadRequest,
		utils.ErrAgentNotA2A:              http.StatusBadRequest,
		utils.ErrForbidden:                http.StatusForbidden,
		audit.ErrRecorderUnavailable:      http.StatusServiceUnavailable,
	} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			stub := &proxyServiceStub{SetFunc: func(context.Context, string, string, string, string, *models.A2AProxyAuthUpdate) error {
				return fmt.Errorf("wrapped: %w", sentinel)
			}}
			rec := httptest.NewRecorder()
			NewA2AProxyController(stub).SetA2AProxy(rec, proxyRequest(http.MethodPut, `{}`))
			assert.Equal(t, code, rec.Code)
		})
	}
}
```

- [ ] **Step 5: Register the routes and wire the controller**

`api/a2a_proxy_routes.go` (Apache header):

```go
package api

import (
	"github.com/wso2/agent-manager/agent-manager-service/controllers"
	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
)

const a2aProxyBase = "/orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy"

// registerA2AProxyRoutes gates writes like the deploy-settings save and the card-source routes.
func registerA2AProxyRoutes(rr *middleware.RouteRegistrar, ctrl controllers.A2AProxyController) {
	rr.HandleFuncWithValidationAndAuthz("GET "+a2aProxyBase, rbac.AgentRead, ctrl.GetA2AProxy)
	rr.HandleFuncWithValidationAndAllAuthz("PUT "+a2aProxyBase, ctrl.SetA2AProxy,
		rbac.AgentUpdate, rbac.AgentEnvNonProduction)
	rr.HandleFuncWithValidationAndAllAuthz("DELETE "+a2aProxyBase, ctrl.DeleteA2AProxy,
		rbac.AgentUpdate, rbac.AgentEnvNonProduction)
	rr.HandleFuncWithValidationAndAllAuthz("POST "+a2aProxyBase+"/refresh", ctrl.RefreshA2AProxy,
		rbac.AgentUpdate, rbac.AgentEnvNonProduction)
}
```

- `api/app.go` `registerAPIRoutes`: after `registerA2AAgentCardRoutes(rr, params.A2AAgentCardController)` add `registerA2AProxyRoutes(rr, params.A2AProxyController)`.
- `api/audit_coverage_test.go` `controllerConstructors`: after `controllers.NewA2AAgentCardController,` add `controllers.NewA2AProxyController,`.
- `wiring/wire.go` `controllerProviderSet`: after `controllers.NewA2AAgentCardController,` add `controllers.NewA2AProxyController,`.
- `wiring/params.go`: after `A2AAgentCardController controllers.A2AAgentCardController` add `A2AProxyController controllers.A2AProxyController`.
- Run `make wire`.

- [ ] **Step 6: Run the route, audit and controller guards**

Run: `make test-run RUN=. PKG=./api/`, `make test-run RUN=. PKG=./audit/`, `make test-run RUN='TestA2AProxy|TestGetA2AProxy|TestSetA2AProxy|TestHandleCommonErrors' PKG=./controllers/`.
Expected: PASS, including:
- `TestEveryMutatingRouteIsAudited` and `TestNoStaleAuditPolicyEntries`;
- `TestOrdinaryReadsAreNotAudited`: the GET returns no secret, so it is deliberately not in `sensitiveReadPaths`;
- `TestSecurityInvariantEveryRouteIsAuthorized` and `TestSecurityInvariantHandlersNeverReadOrgFromPath`;
- `TestDomainActionsMatchRouteDerivedActions` and `TestRegisteredActionsHaveDetailSchemas`.

- [ ] **Step 7: Document the audit actions**

`docs/audit-logging.md`:
- `## Semantic events` table: after the "A2A card source" row add ``| A2A proxy | `agent:set-a2a-proxy` / `agent:remove-a2a-proxy` | Fail-closed |``.
- The bullets after that table: add "- **`agent:remove-a2a-proxy` is also written when the card source of a proxied environment is removed**, since that discards the stored upstream secret too; the card route's own `agent:remove-card-source` is written alongside it."
- `## Action reference` table, in alphabetical order:
  - ``| `agent:remove-a2a-proxy` | credential | 4 critical | `agentName`, `upstreamAuthChanged`, `upstreamAuthHeader` |`` before `agent:remove-card-source`
  - ``| `agent:set-a2a-proxy` | credential | 4 critical | `agentName`, `upstreamAuthChanged`, `upstreamAuthHeader` |`` before `agent:set-card-source`

- [ ] **Step 8: Full verification, commit**

```bash
make fmt
make test-unit
go build -tags=integration ./...
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./...
git add docs/api_v1_openapi.yaml spec/ controllers/a2a_proxy_controller.go controllers/a2a_proxy_controller_unit_test.go \
  controllers/agent_controller.go controllers/error_mapping_unit_test.go \
  api/a2a_proxy_routes.go api/app.go api/audit_coverage_test.go audit/policy.go audit/actions_domain_test.go \
  docs/audit-logging.md wiring/wire.go wiring/params.go wiring/wire_gen.go
git commit -m "Add the A2A proxy API"
```

Before staging `spec/`, run `git status --short spec/` and check that only A2A-proxy files are new or changed. If the generator rewrote unrelated models, find out why (generator version, Docker image) before committing.

- [ ] **Step 9: Regenerate the amctl client**

From the repo root:

```bash
PATH="$(go env GOPATH)/bin:$PATH" make amctl-gen-client
(cd cli && go build ./... && go vet ./...)
git status --short cli/
git add cli/pkg/clients/amsvc/gen/client.gen.go cli/pkg/clients/amsvc/gen/types.gen.go
git commit -m "Regenerate the amctl client for the A2A proxy API"
```

Expected: only `client.gen.go` and `types.gen.go` change, by the new operations and types. If enum constants unrelated to this change get renamed (as `MCPEndpointEnvironmentDeploymentStatus*` did for #1990), leave them and note it for the PR description.

---

### Task 14: Lift the kgateway request timeout on the gateway's catch-all route

**Files:**
- Modify: `deployments/helm-charts/wso2-amp-api-platform-gateway-extension/templates/gateway-kgateway-route.yaml:31-32`

The chart's catch-all route (the way callers reach a proxied agent, and LLM/MCP proxies today) caps every request at 30s, which cuts A2A streams. `0s` disables the request timeout under Gateway API. The gateway's own 5m idle timeout (`route_idle_timeout_ms`, `stream_idle_timeout`) remains the liveness guard. Non-streaming HTTP+JSON A2A routes and the card route keep the gateway's 60s route timeout (`translator.go:340`), which this change does not touch.

- [ ] **Step 1: Render the current value**

Run: `helm template amp deployments/helm-charts/wso2-amp-api-platform-gateway-extension | grep -A1 'timeouts:'`
Expected: `request: 30s`.

- [ ] **Step 2: Change it**

```yaml
      timeouts:
        # A2A streams and long LLM/MCP responses outlive any fixed cap; "0s" disables the
        # request timeout, and the gateway's own 5m idle timeout stays as the liveness guard.
        request: 0s
```

- [ ] **Step 3: Verify**

```bash
helm template amp deployments/helm-charts/wso2-amp-api-platform-gateway-extension | grep -A3 'timeouts:'
helm lint deployments/helm-charts/wso2-amp-api-platform-gateway-extension
```

Expected: `request: 0s`; lint reports 0 charts failed. The chart's `values.schema.json` does not cover this field, and no generated helm reference doc mentions it.

- [ ] **Step 4: Commit**

```bash
git add deployments/helm-charts/wso2-amp-api-platform-gateway-extension/templates/gateway-kgateway-route.yaml
git commit -m "Lift the 30s request cap on the gateway's catch-all route"
```

---

### Task 15: Console types, fetch functions and hooks for `/a2a-proxy`

Follows `/add-console-api-feature`: a fetch function in `apis/`, a hook in `hooks/`, exported from both barrels, with mutations that invalidate by key prefix.

**Files:**
- Create: `console/workspaces/libs/types/src/api/a2a-proxy.ts`, `console/workspaces/libs/api-client/src/apis/a2a-proxy.ts`, `console/workspaces/libs/api-client/src/hooks/a2a-proxy.ts`, `console/workspaces/libs/api-client/src/hooks/a2a-proxy.test.ts`
- Modify: `console/workspaces/libs/types/src/api/index.ts`, `console/workspaces/libs/api-client/src/apis/index.ts`, `console/workspaces/libs/api-client/src/hooks/index.ts`, `console/workspaces/libs/api-client/src/hooks/agent-card.ts`

**Interfaces:**
- Produces (`@agent-management-platform/types`): `A2AProxyStatus`, `A2AProxyResponse { status; lastError; upstreamAuthHeader }`, `A2AProxyUpstreamAuth { header; value? }`, `SetA2AProxyRequest { upstreamAuth? }`, `A2AProxyPathParams` (= `AgentCardPathParams`).
- Produces (`@agent-management-platform/api-client`):
  - fetch functions `getA2AProxy`, `setA2AProxy`, `deleteA2AProxy`, `refreshA2AProxy`;
  - `a2aProxyQueryKey(params)` = `["a2a-proxy", orgName, projName, agentName, envId]`;
  - `a2aProxyRefetchInterval(data, error)`;
  - `invalidateA2AProxy(queryClient, params): Promise<void>` (refetches the proxy and the agent card);
  - hooks `useGetA2AProxy(params, { enabled? })`, `useSetA2AProxy()` (vars `{ params, body }`), `useDeleteA2AProxy()` (vars `params`), `useRefreshA2AProxy()` (vars `params`);
  - `agentCardQueryKey(params)` (renamed and exported from the private `agentCardKey`); `useGetAgentCard(params, { enabled?, poll? })`, where `poll: false` stops its re-poll.

- [ ] **Step 1: Write the hook tests**

`libs/api-client/src/hooks/a2a-proxy.test.ts` (license header first):

```ts
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { A2AProxyResponse, AgentCardResponse } from "@agent-management-platform/types";

vi.mock("@agent-management-platform/views", () => ({ useSnackBar: () => ({ pushSnackBar: vi.fn() }) }));
vi.mock("@agent-management-platform/auth", () => ({
  useAuthHooks: () => ({ getToken: async () => "t", isAuthenticated: true, logout: vi.fn() }),
}));
vi.mock("./telemetry", () => ({
  ConsoleAction: {}, markSessionExpired: vi.fn(), useTrack: () => ({ track: vi.fn() }),
}));
vi.mock("../apis/a2a-proxy", () => ({
  getA2AProxy: vi.fn(),
  setA2AProxy: vi.fn(async () => undefined),
  deleteA2AProxy: vi.fn(async () => undefined),
  refreshA2AProxy: vi.fn(async () => undefined),
}));
vi.mock("../apis/agent-card", () => ({
  getAgentCard: vi.fn(),
  setAgentCardSource: vi.fn(async () => undefined),
  refreshAgentCard: vi.fn(async () => undefined),
  deleteAgentCardSource: vi.fn(async () => undefined),
}));

import { getAgentCard } from "../apis/agent-card";
import { useGetAgentCard } from "./agent-card";
import { a2aProxyRefetchInterval, useSetA2AProxy } from "./a2a-proxy";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const params = { orgName: "o", projName: "p", agentName: "a", envId: "dev" };
const proxy = (status: A2AProxyResponse["status"]): A2AProxyResponse => ({ status, lastError: "", upstreamAuthHeader: "" });
const card: AgentCardResponse = { source: "external", status: "pending", sourceUrl: "https://a/.well-known/agent-card.json", lastError: "" };

let unmount: (() => void) | undefined;
afterEach(() => unmount?.());

describe("a2aProxyRefetchInterval", () => {
  it("re-polls while the gateway publish is in flight", () => {
    expect(a2aProxyRefetchInterval(proxy("pending"))).toBeGreaterThan(0);
  });

  it("stops once published, failed, or the environment is direct", () => {
    expect(a2aProxyRefetchInterval(proxy("published"))).toBe(false);
    expect(a2aProxyRefetchInterval(proxy("failed"))).toBe(false);
    expect(a2aProxyRefetchInterval(proxy("pending"), { status: 404 })).toBe(false);
  });
});

describe("useSetA2AProxy", () => {
  // A mode switch changes the card too; the form must not close on the old card.
  it("settles only after the agent card is refetched", async () => {
    vi.mocked(getAgentCard).mockResolvedValue(card);
    const result = {} as { current: ReturnType<typeof useSetA2AProxy> };
    const Probe = () => {
      useGetAgentCard(params);
      result.current = useSetA2AProxy();
      return null;
    };
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(createElement(QueryClientProvider, { client: new QueryClient() }, createElement(Probe))));
    unmount = () => act(() => root.unmount());

    let release!: () => void;
    vi.mocked(getAgentCard).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve(card); }));
    let settled = false;
    await act(async () => {
      void result.current.mutateAsync({ params, body: {} }).then(() => { settled = true; });
    });
    expect(settled).toBe(false);

    await act(async () => release());
    expect(settled).toBe(true);
  });
});
```

- [ ] **Step 2: Verify it fails**

Run (from `console/`): `pnpm install` (once per worktree), then `pnpm --filter @agent-management-platform/api-client run test -- a2a-proxy`
Expected: FAIL — `Cannot find module './a2a-proxy'`.

- [ ] **Step 3: Types**

`libs/types/src/api/a2a-proxy.ts`:

```ts
import { type AgentCardPathParams } from './agent-card';

export type A2AProxyStatus = 'pending' | 'published' | 'failed';

export interface A2AProxyResponse {
  status: A2AProxyStatus;
  lastError: string;
  /** The header the gateway adds upstream; empty when none. Non-empty means a secret is stored. */
  upstreamAuthHeader: string;
}

export interface A2AProxyUpstreamAuth {
  header: string;
  /** Omit to keep the stored secret. Never returned by the API. */
  value?: string;
}

/** Omitting upstreamAuth clears the stored credential. */
export interface SetA2AProxyRequest {
  upstreamAuth?: A2AProxyUpstreamAuth;
}

export type A2AProxyPathParams = AgentCardPathParams;
```

Add `export * from './a2a-proxy';` after the agent-card line in `libs/types/src/api/index.ts`.

- [ ] **Step 4: Fetch functions**

`libs/api-client/src/apis/a2a-proxy.ts`:

```ts
import type {
  A2AProxyPathParams,
  A2AProxyResponse,
  SetA2AProxyRequest,
} from "@agent-management-platform/types";
import { encodeRequired, httpDELETE, httpGET, httpPOST, httpPUT, SERVICE_BASE } from "../utils";

function a2aProxyPath(params: A2AProxyPathParams): string {
  const org = encodeRequired(params.orgName, "orgName");
  const proj = encodeRequired(params.projName, "projName");
  const agent = encodeRequired(params.agentName, "agentName");
  const env = encodeRequired(params.envId, "envId");
  return `${SERVICE_BASE}/orgs/${org}/projects/${proj}/agents/${agent}/environments/${env}/a2a-proxy`;
}

// A direct environment answers 404, which the http helpers throw with status 404.
export async function getA2AProxy(
  params: A2AProxyPathParams,
  getToken?: () => Promise<string>,
): Promise<A2AProxyResponse> {
  const token = getToken ? await getToken() : undefined;
  const res = await httpGET(a2aProxyPath(params), { token });
  return res.json();
}

export async function setA2AProxy(
  params: A2AProxyPathParams,
  body: SetA2AProxyRequest,
  getToken?: () => Promise<string>,
): Promise<void> {
  const token = getToken ? await getToken() : undefined;
  await httpPUT(a2aProxyPath(params), body, { token });
}

export async function deleteA2AProxy(
  params: A2AProxyPathParams,
  getToken?: () => Promise<string>,
): Promise<void> {
  const token = getToken ? await getToken() : undefined;
  await httpDELETE(a2aProxyPath(params), { token });
}

export async function refreshA2AProxy(
  params: A2AProxyPathParams,
  getToken?: () => Promise<string>,
): Promise<void> {
  const token = getToken ? await getToken() : undefined;
  await httpPOST(`${a2aProxyPath(params)}/refresh`, {}, { token });
}
```

Add `export * from './a2a-proxy';` to `libs/api-client/src/apis/index.ts`.

- [ ] **Step 5: Hooks**

In `libs/api-client/src/hooks/agent-card.ts`:
- rename `agentCardKey` to `export const agentCardQueryKey`, updating its four uses;
- give `useGetAgentCard` a `poll` option:

```ts
/** A 404 (not an A2A agent, or no fetch queued yet) is expected: it errors silently. poll: false stops the re-poll. */
export function useGetAgentCard(params: AgentCardPathParams, options: { enabled?: boolean; poll?: boolean } = {}) {
  const { getToken } = useAuthHooks();
  return useApiQuery<AgentCardResponse>({
    queryKey: agentCardQueryKey(params),
    queryFn: () => getAgentCard(params, getToken),
    enabled: (options.enabled ?? true) &&
      !!(params.orgName && params.projName && params.agentName && params.envId),
    refetchInterval: (q) => (options.poll === false ? false : agentCardRefetchInterval(q.state.data, q.state.error)),
    retry: false,
    silent: true,
  });
}
```

`libs/api-client/src/hooks/a2a-proxy.ts`:

```ts
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useAuthHooks } from "@agent-management-platform/auth";
import type {
  A2AProxyPathParams,
  A2AProxyResponse,
  SetA2AProxyRequest,
} from "@agent-management-platform/types";
import { useApiMutation, useApiQuery } from "./react-query-notifications";
import { deleteA2AProxy, getA2AProxy, refreshA2AProxy, setA2AProxy } from "../apis/a2a-proxy";
import { agentCardQueryKey } from "./agent-card";
import { POLL_INTERVAL } from "../utils";

export const a2aProxyQueryKey = (p: A2AProxyPathParams) =>
  ["a2a-proxy", p.orgName, p.projName, p.agentName, p.envId] as const;

/** A proxy change also changes the card, so both are refetched. */
export async function invalidateA2AProxy(queryClient: QueryClient, params: A2AProxyPathParams): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: a2aProxyQueryKey(params) }),
    queryClient.invalidateQueries({ queryKey: agentCardQueryKey(params) }),
  ]);
}

/** Re-poll while the gateway publish is in flight; a 404 means the environment is direct. */
export function a2aProxyRefetchInterval(
  data: A2AProxyResponse | undefined,
  error?: unknown,
): number | false {
  if ((error as { status?: number } | null)?.status === 404) return false;
  return data?.status === "pending" ? POLL_INTERVAL : false;
}

/** A 404 (the environment is direct) is expected: it errors silently. */
export function useGetA2AProxy(params: A2AProxyPathParams, options: { enabled?: boolean } = {}) {
  const { getToken } = useAuthHooks();
  return useApiQuery<A2AProxyResponse>({
    queryKey: a2aProxyQueryKey(params),
    queryFn: () => getA2AProxy(params, getToken),
    enabled: (options.enabled ?? true) &&
      !!(params.orgName && params.projName && params.agentName && params.envId),
    refetchInterval: (q) => a2aProxyRefetchInterval(q.state.data, q.state.error),
    retry: false,
    silent: true,
  });
}

export function useSetA2AProxy() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  type Vars = { params: A2AProxyPathParams; body: SetA2AProxyRequest };
  return useApiMutation<void, unknown, Vars>({
    action: { verb: "update", target: "gateway routing" },
    // In mutationFn, since useApiMutation does not await onSuccess.
    mutationFn: async ({ params, body }) => {
      await setA2AProxy(params, body, getToken);
      await invalidateA2AProxy(queryClient, params);
    },
  });
}

export function useDeleteA2AProxy() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, A2AProxyPathParams>({
    action: { verb: "remove", target: "gateway routing" },
    // A 404 keeps the old data cached, so the proxy entry is dropped rather than refetched.
    mutationFn: async (params) => {
      await deleteA2AProxy(params, getToken);
      queryClient.removeQueries({ queryKey: a2aProxyQueryKey(params) });
      await queryClient.invalidateQueries({ queryKey: agentCardQueryKey(params) });
    },
  });
}

export function useRefreshA2AProxy() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, A2AProxyPathParams>({
    action: { verb: "update", target: "gateway routing" },
    successMessage: "Gateway republish queued",
    mutationFn: async (params) => {
      await refreshA2AProxy(params, getToken);
      await invalidateA2AProxy(queryClient, params);
    },
  });
}
```

Add `export * from './a2a-proxy';` to `libs/api-client/src/hooks/index.ts`.

- [ ] **Step 6: Run the tests, lint, build**

```bash
pnpm --filter @agent-management-platform/api-client run test
pnpm --filter @agent-management-platform/api-client run lint
pnpm --filter @agent-management-platform/types run lint
pnpm --filter @agent-management-platform/api-client... run build
```

Expected: PASS, including the existing `agent-card*.test.ts`.

- [ ] **Step 7: Commit**

```bash
git add console/workspaces/libs/types/src/api/a2a-proxy.ts console/workspaces/libs/types/src/api/index.ts \
  console/workspaces/libs/api-client/src/apis/a2a-proxy.ts console/workspaces/libs/api-client/src/apis/index.ts \
  console/workspaces/libs/api-client/src/hooks/a2a-proxy.ts console/workspaces/libs/api-client/src/hooks/a2a-proxy.test.ts \
  console/workspaces/libs/api-client/src/hooks/index.ts console/workspaces/libs/api-client/src/hooks/agent-card.ts
git commit -m "Add console hooks for the A2A proxy API"
```

---

### Task 16: Gateway routing on the Agent Card panel

**Files:**
- Create: `console/workspaces/pages/overview/src/AgentOverview/a2aProxyDraft.ts` (+ `a2aProxyDraft.test.ts`), `console/workspaces/pages/overview/src/AgentOverview/A2AProxyFields.tsx`, `console/workspaces/pages/overview/src/AgentOverview/EnvA2AProxySection.tsx` (+ `EnvA2AProxySection.test.tsx`)
- Modify: `console/workspaces/pages/overview/src/AgentOverview/AgentCardSourceForm.tsx`, `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.tsx`, `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.test.tsx`

**Interfaces:**
- Consumes: Task 15 hooks and types.
- Produces:
  - `a2aProxyDraft.ts`: `type StoredSecretAction = "keep" | "replace" | "clear"`; `interface A2AProxyDraft { enabled: boolean; header: string; value: string; secret: StoredSecretAction }`; `hasStoredSecret(proxy?)`, `draftFromProxy(proxy?)`, `upstreamAuthError(draft, proxy?)`, `proxyRequestOf(draft, proxy?)`, `dropsStoredSecret(draft, proxy?)`, `draftChanged(draft, proxy?)`; constants `UPSTREAM_HEADER_MAX = 255`, `UPSTREAM_VALUE_MAX_BYTES = 8192`
  - `A2AProxyFields({ draft, onChange, hasStoredSecret, error, disabled? })`
  - `EnvA2AProxySection({ params, proxy? })`

UI behaviour (spec § Console; Decision 14):
- **No URL registered.** The block form shows the URL, a "Route through gateway" switch and, when it is on, the header and value fields. Save sends `PUT source`. Once that succeeds and the switch is on, it sends `PUT a2a-proxy`. A failure of the second call surfaces through `useApiMutation`'s own error snackbar and leaves the environment direct.
- **URL registered.** A "Gateway routing" section with the switch, the fields, a status chip, a failed alert with Retry, and its own Save, enabled on any toggle, header or secret change.
  - The secret is write-only: "A value is stored" with Replace and Clear.
  - Clearing it, emptying the header, or switching off asks for confirmation first.
- The card stops re-polling when the proxy status is `failed` (a parked card never leaves `pending` on its own). The card's "Pending" chip is then hidden; the section's alert explains why.

- [ ] **Step 1: Write the draft tests**

`pages/overview/src/AgentOverview/a2aProxyDraft.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import type { A2AProxyResponse } from "@agent-management-platform/types";
import {
  type A2AProxyDraft,
  draftChanged,
  draftFromProxy,
  dropsStoredSecret,
  proxyRequestOf,
  upstreamAuthError,
} from "./a2aProxyDraft";

const stored: A2AProxyResponse = { status: "published", lastError: "", upstreamAuthHeader: "X-Key" };
const fresh = (header: string, value: string): A2AProxyDraft => ({ enabled: true, header, value, secret: "replace" });

describe("a2aProxyDraft", () => {
  it("starts from the server state", () => {
    expect(draftFromProxy(undefined)).toEqual({ enabled: false, header: "", value: "", secret: "replace" });
    expect(draftFromProxy(stored)).toEqual({ enabled: true, header: "X-Key", value: "", secret: "keep" });
  });

  it("keeps, replaces or clears the stored secret", () => {
    expect(proxyRequestOf(draftFromProxy(stored), stored)).toEqual({ upstreamAuth: { header: "X-Key" } });
    expect(proxyRequestOf({ ...draftFromProxy(stored), secret: "replace", value: "v" }, stored))
      .toEqual({ upstreamAuth: { header: "X-Key", value: "v" } });
    expect(proxyRequestOf({ ...draftFromProxy(stored), secret: "clear" }, stored)).toEqual({});
    expect(proxyRequestOf(fresh("", ""))).toEqual({});
  });

  it("asks before a save drops a stored secret", () => {
    expect(dropsStoredSecret({ ...draftFromProxy(stored), secret: "clear" }, stored)).toBe(true);
    expect(dropsStoredSecret({ ...draftFromProxy(stored), enabled: false }, stored)).toBe(true);
    expect(dropsStoredSecret({ ...draftFromProxy(stored), header: " " }, stored)).toBe(true);
    expect(dropsStoredSecret(draftFromProxy(stored), stored)).toBe(false);
  });

  it("mirrors the server's validation", () => {
    for (const bad of [
      fresh("X Key", "v"), fresh("Host", "v"), fresh("X-Key", ""), fresh("X-Key", "it's"),
      fresh("X-Key", "a\nb"), fresh("X-Key", '{{ secret "x" }}'), fresh("X-Key", "é".repeat(4097)),
    ]) {
      expect(upstreamAuthError(bad), JSON.stringify(bad)).not.toBeNull();
    }
    expect(upstreamAuthError(fresh("X-Key", "a".repeat(8192)))).toBeNull();
    expect(upstreamAuthError(fresh("", ""))).toBeNull();
    expect(upstreamAuthError(draftFromProxy(stored), stored)).toBeNull();
  });

  it("sees toggle-only and auth-only changes", () => {
    expect(draftChanged({ ...draftFromProxy(undefined), enabled: true })).toBe(true);
    expect(draftChanged(draftFromProxy(stored), stored)).toBe(false);
    expect(draftChanged({ ...draftFromProxy(stored), header: "X-Other" }, stored)).toBe(true);
    expect(draftChanged({ ...draftFromProxy(stored), secret: "replace", value: "v" }, stored)).toBe(true);
    expect(draftChanged({ ...draftFromProxy(stored), enabled: false }, stored)).toBe(true);
  });
});
```

- [ ] **Step 2: Write the section tests**

`pages/overview/src/AgentOverview/EnvA2AProxySection.test.tsx`:

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const setProxy = vi.fn();
const removeProxy = vi.fn();
const republish = vi.fn();
const addConfirmation = vi.fn();

vi.mock("@agent-management-platform/api-client", () => ({
  useSetA2AProxy: () => ({ mutate: setProxy, isPending: false }),
  useDeleteA2AProxy: () => ({ mutate: removeProxy, isPending: false }),
  useRefreshA2AProxy: () => ({ mutate: republish, isPending: false }),
}));
vi.mock("@agent-management-platform/shared-component", () => ({
  useConfirmationDialog: () => ({ addConfirmation }),
}));

import { EnvA2AProxySection } from "./EnvA2AProxySection";

const params = { orgName: "org", projName: "proj", agentName: "agent", envId: "dev" };
const stored = { status: "published" as const, lastError: "", upstreamAuthHeader: "X-Key" };
const save = () => fireEvent.click(screen.getByRole("button", { name: "Save" }));

beforeEach(() => vi.clearAllMocks());

describe("EnvA2AProxySection", () => {
  it("saves a toggle-only change", () => {
    render(<EnvA2AProxySection params={params} />);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    fireEvent.click(screen.getByLabelText("Route through gateway"));
    save();
    expect(setProxy).toHaveBeenCalledWith({ params, body: {} });
  });

  it("keeps the stored secret when only the header changes", () => {
    render(<EnvA2AProxySection params={params} proxy={stored} />);
    expect(screen.getByText("A value is stored.")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Upstream header"), { target: { value: "X-New" } });
    save();
    expect(setProxy).toHaveBeenCalledWith({ params, body: { upstreamAuth: { header: "X-New" } } });
  });

  it("asks before clearing the stored value", () => {
    render(<EnvA2AProxySection params={params} proxy={stored} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    save();
    expect(setProxy).not.toHaveBeenCalled();
    addConfirmation.mock.calls[0][0].onConfirm();
    expect(setProxy).toHaveBeenCalledWith({ params, body: {} });
  });

  it("asks before switching to direct", () => {
    render(<EnvA2AProxySection params={params} proxy={stored} />);
    fireEvent.click(screen.getByLabelText("Route through gateway"));
    save();
    expect(removeProxy).not.toHaveBeenCalled();
    addConfirmation.mock.calls[0][0].onConfirm();
    expect(removeProxy).toHaveBeenCalledWith(params);
  });

  it("keeps Save disabled for a value the gateway cannot carry", () => {
    render(<EnvA2AProxySection params={params} />);
    fireEvent.click(screen.getByLabelText("Route through gateway"));
    fireEvent.change(screen.getByLabelText("Upstream header"), { target: { value: "X-Key" } });
    fireEvent.change(screen.getByLabelText("Header value"), { target: { value: "it's" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("shows a failed publish with its reason and offers a retry", () => {
    render(<EnvA2AProxySection params={params} proxy={{ ...stored, status: "failed", lastError: "no JSONRPC interface" }} />);
    expect(screen.getByText("Gateway publish failed")).toBeInTheDocument();
    expect(screen.getByText(/no JSONRPC interface/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(republish).toHaveBeenCalledWith(params);
  });
});
```

In `EnvAgentCardSection.test.tsx`:
- add `useGetA2AProxy: vi.fn()`, `useSetA2AProxy: vi.fn()`, `useDeleteA2AProxy: vi.fn()` and `useRefreshA2AProxy: vi.fn()` to the `vi.mock` factory and the import;
- add a `mockProxy` helper shaped like `mockCard`;
- in `beforeEach`, call `mockProxy({ isError: true, error: Object.assign(new Error("nf"), { status: 404 }) })` and give the three mutation hooks `{ mutate: vi.fn(), isPending: false }`, so existing tests see a direct environment;
- in "asks an external agent with no source for a card URL", change the expected second argument to `expect.objectContaining({ onSuccess: expect.any(Function) })`.

Then append:

```tsx
  it("saves a new URL, then routes it through the gateway", () => {
    mockAgent("a2a-agent");
    mockCard({ isError: true, error: Object.assign(new Error("nf"), { status: 404 }) });
    const setProxy = vi.fn();
    vi.mocked(useSetA2AProxy).mockReturnValue(
      { mutate: setProxy, isPending: false } as unknown as ReturnType<typeof useSetA2AProxy>);
    renderSection(true);
    fireEvent.change(screen.getByPlaceholderText(/agent-card\.json/), {
      target: { value: "https://agent.example/.well-known/agent-card.json" },
    });
    fireEvent.click(screen.getByLabelText("Route through gateway"));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(setProxy).not.toHaveBeenCalled();

    const [, options] = vi.mocked(setSource).mock.calls[0];
    options.onSuccess();
    expect(setProxy).toHaveBeenCalledWith({
      params: { orgName: "org", projName: "proj", agentName: "agent", envId: "dev" }, body: {},
    });
  });

  it("stops polling a parked card once the gateway publish failed, and says why", () => {
    mockAgent("a2a-agent");
    mockCard({ data: { source: "external", status: "pending", sourceUrl: "https://agent.example/.well-known/agent-card.json", lastError: "" } });
    mockProxy({ data: { status: "failed", lastError: "the agent cannot be proxied through the gateway", upstreamAuthHeader: "" } });
    renderSection(true);
    expect(screen.getByText("Gateway publish failed")).toBeInTheDocument();
    expect(screen.queryByText("Pending")).not.toBeInTheDocument();
    expect(vi.mocked(useGetAgentCard)).toHaveBeenLastCalledWith(
      { orgName: "org", projName: "proj", agentName: "agent", envId: "dev" }, { enabled: true, poll: false });
  });
```

- [ ] **Step 3: Verify they fail**

Run: `pnpm --filter @agent-management-platform/overview run test`
Expected: FAIL — `Cannot find module './a2aProxyDraft'` / `./EnvA2AProxySection`.

- [ ] **Step 4: Draft logic**

`pages/overview/src/AgentOverview/a2aProxyDraft.ts`:

```ts
import type { A2AProxyResponse, SetA2AProxyRequest } from "@agent-management-platform/types";

/** What a save does with a stored upstream secret. */
export type StoredSecretAction = "keep" | "replace" | "clear";

export interface A2AProxyDraft {
  enabled: boolean;
  header: string;
  value: string;
  secret: StoredSecretAction;
}

export const UPSTREAM_HEADER_MAX = 255;
export const UPSTREAM_VALUE_MAX_BYTES = 8192;
const HEADER_PATTERN = /^[A-Za-z0-9_-]+$/;

/** A non-empty header means the server stores a secret for it. */
export const hasStoredSecret = (proxy?: A2AProxyResponse) => !!proxy?.upstreamAuthHeader;

export function draftFromProxy(proxy?: A2AProxyResponse): A2AProxyDraft {
  return {
    enabled: !!proxy,
    header: proxy?.upstreamAuthHeader ?? "",
    value: "",
    secret: hasStoredSecret(proxy) ? "keep" : "replace",
  };
}

/** Mirrors the server's checks: the gateway pastes the value into single-quoted YAML and renders {{ }}. */
export function upstreamAuthError(draft: A2AProxyDraft, proxy?: A2AProxyResponse): string | null {
  const header = draft.header.trim();
  if (!draft.enabled || draft.secret === "clear" || (header === "" && draft.value === "")) return null;
  if (!HEADER_PATTERN.test(header)) return "Use only letters, digits, '-' and '_' in the header name";
  if (header.length > UPSTREAM_HEADER_MAX) return `The header name can be at most ${UPSTREAM_HEADER_MAX} characters`;
  if (header.toLowerCase() === "host") return "The Host header cannot be set";
  if (draft.secret === "keep" && hasStoredSecret(proxy)) return null;
  if (draft.value === "") return "Enter the header value";
  if (new TextEncoder().encode(draft.value).length > UPSTREAM_VALUE_MAX_BYTES) {
    return `The value can be at most ${UPSTREAM_VALUE_MAX_BYTES} bytes`;
  }
  if (/['\r\n]/.test(draft.value)) return "The value cannot contain quotes or line breaks";
  if (draft.value.includes("{{")) return "The value cannot contain '{{'";
  return null;
}

/** The PUT body: no upstreamAuth clears the stored credential, a header alone keeps its secret. */
export function proxyRequestOf(draft: A2AProxyDraft, proxy?: A2AProxyResponse): SetA2AProxyRequest {
  const header = draft.header.trim();
  if (draft.secret === "clear" || header === "") return {};
  if (draft.secret === "keep" && hasStoredSecret(proxy)) return { upstreamAuth: { header } };
  return { upstreamAuth: { header, value: draft.value } };
}

/** A save that drops the stored secret is confirmed first. */
export function dropsStoredSecret(draft: A2AProxyDraft, proxy?: A2AProxyResponse): boolean {
  return hasStoredSecret(proxy) && (!draft.enabled || draft.secret === "clear" || draft.header.trim() === "");
}

export function draftChanged(draft: A2AProxyDraft, proxy?: A2AProxyResponse): boolean {
  if (draft.enabled !== !!proxy) return true;
  if (!draft.enabled) return false;
  if (draft.header.trim() !== (proxy?.upstreamAuthHeader ?? "")) return true;
  return draft.secret === "clear" || (draft.secret === "replace" && draft.value !== "");
}
```

- [ ] **Step 5: Fields and section**

`pages/overview/src/AgentOverview/A2AProxyFields.tsx`:

```tsx
import { useId } from "react";
import { Box, Button, FormControlLabel, Stack, Switch, TextField, Typography } from "@wso2/oxygen-ui";
import { type A2AProxyDraft, UPSTREAM_HEADER_MAX } from "./a2aProxyDraft";

interface A2AProxyFieldsProps {
  draft: A2AProxyDraft;
  onChange: (draft: A2AProxyDraft) => void;
  /** True when the server stores a secret for the saved header. */
  hasStoredSecret: boolean;
  error: string | null;
  disabled?: boolean;
}

/** The route-through-gateway switch and an optional upstream header; the secret is write-only. */
export function A2AProxyFields({ draft, onChange, hasStoredSecret, error, disabled }: A2AProxyFieldsProps) {
  const id = useId();
  const set = (patch: Partial<A2AProxyDraft>) => onChange({ ...draft, ...patch });
  return (
    <Stack spacing={1.5}>
      <FormControlLabel
        control={<Switch checked={draft.enabled} disabled={disabled} onChange={(_, enabled) => set({ enabled })} />}
        label="Route through gateway"
      />
      {draft.enabled && (
        <>
          <Typography variant="caption" color="text.secondary">
            Callers reach the agent through this environment&apos;s gateway, with its API keys, CORS and
            observability. Add a header if the agent itself requires a credential.
          </Typography>
          <TextField
            id={`${id}-header`}
            size="small"
            label="Upstream header"
            placeholder="X-API-Key"
            value={draft.header}
            disabled={disabled}
            onChange={(e) => set({ header: e.target.value })}
            inputProps={{ maxLength: UPSTREAM_HEADER_MAX }}
          />
          {hasStoredSecret && draft.secret === "keep" && (
            <Box display="flex" alignItems="center" gap={1}>
              <Typography variant="body2" color="text.secondary">A value is stored.</Typography>
              <Button size="small" disabled={disabled} onClick={() => set({ secret: "replace", value: "" })}>Replace</Button>
              <Button size="small" color="error" disabled={disabled} onClick={() => set({ secret: "clear" })}>Clear</Button>
            </Box>
          )}
          {hasStoredSecret && draft.secret === "clear" && (
            <Box display="flex" alignItems="center" gap={1}>
              <Typography variant="body2" color="text.secondary">The stored value is removed when you save.</Typography>
              <Button size="small" disabled={disabled} onClick={() => set({ secret: "keep" })}>Undo</Button>
            </Box>
          )}
          {draft.secret === "replace" && (
            <TextField
              id={`${id}-value`}
              size="small"
              type="password"
              autoComplete="new-password"
              label="Header value"
              value={draft.value}
              disabled={disabled}
              onChange={(e) => set({ value: e.target.value })}
            />
          )}
          {error && <Typography variant="caption" color="error">{error}</Typography>}
        </>
      )}
    </Stack>
  );
}
```

`pages/overview/src/AgentOverview/EnvA2AProxySection.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Alert, Box, Button, Chip, Typography } from "@wso2/oxygen-ui";
import { Trash } from "@wso2/oxygen-ui-icons-react";
import { useDeleteA2AProxy, useRefreshA2AProxy, useSetA2AProxy } from "@agent-management-platform/api-client";
import { useConfirmationDialog } from "@agent-management-platform/shared-component";
import type { A2AProxyResponse, A2AProxyStatus, AgentCardPathParams } from "@agent-management-platform/types";
import { A2AProxyFields } from "./A2AProxyFields";
import {
  draftChanged,
  draftFromProxy,
  dropsStoredSecret,
  hasStoredSecret,
  proxyRequestOf,
  upstreamAuthError,
} from "./a2aProxyDraft";

const PROXY_CHIP: Record<A2AProxyStatus, { label: string; color: "warning" | "success" | "error" }> = {
  pending: { label: "Publishing to gateway", color: "warning" },
  published: { label: "Routed through gateway", color: "success" },
  failed: { label: "Gateway publish failed", color: "error" },
};

interface EnvA2AProxySectionProps {
  params: AgentCardPathParams;
  /** Undefined while the environment is direct. */
  proxy?: A2AProxyResponse;
}

/** Gateway routing for one environment of an external A2A agent that has a registered card URL. */
export function EnvA2AProxySection({ params, proxy }: EnvA2AProxySectionProps) {
  const [draft, setDraft] = useState(() => draftFromProxy(proxy));
  const { mutate: saveProxy, isPending: isSaving } = useSetA2AProxy();
  const { mutate: removeProxy, isPending: isRemoving } = useDeleteA2AProxy();
  const { mutate: republish, isPending: isRepublishing } = useRefreshA2AProxy();
  const { addConfirmation } = useConfirmationDialog();
  const proxied = !!proxy;
  const savedHeader = proxy?.upstreamAuthHeader ?? "";

  // Re-seed when the saved routing changes, not on every status poll, so typing is not overwritten.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => setDraft(draftFromProxy(proxy)), [proxied, savedHeader]);

  const error = upstreamAuthError(draft, proxy);
  const busy = isSaving || isRemoving;
  const canSave = draftChanged(draft, proxy) && !error && !busy;

  const save = () => {
    if (!canSave) return;
    if (draft.enabled && !dropsStoredSecret(draft, proxy)) {
      saveProxy({ params, body: proxyRequestOf(draft, proxy) });
      return;
    }
    addConfirmation(draft.enabled
      ? {
          analytics: { entity: "a2a-proxy", action: "clear-upstream-secret" },
          title: "Remove the stored upstream value?",
          description: "The gateway stops sending the header to the agent, and the value cannot be recovered.",
          confirmButtonText: "Remove value",
          confirmButtonColor: "error",
          confirmButtonIcon: <Trash size={16} />,
          onConfirm: () => saveProxy({ params, body: proxyRequestOf(draft, proxy) }),
        }
      : {
          analytics: { entity: "a2a-proxy", action: "disable" },
          title: "Stop routing through the gateway?",
          description: "Callers need the agent's own address again, gateway API keys and CORS stop applying, and any stored upstream value is removed.",
          confirmButtonText: "Stop routing",
          confirmButtonColor: "error",
          confirmButtonIcon: <Trash size={16} />,
          onConfirm: () => removeProxy(params),
        });
  };

  return (
    <Box sx={{ mb: 2 }}>
      <Box display="flex" alignItems="center" gap={1} sx={{ mb: 1 }}>
        <Typography variant="overline" color="text.secondary">Gateway routing</Typography>
        {proxy && (
          <Chip size="small" variant="outlined" label={PROXY_CHIP[proxy.status].label} color={PROXY_CHIP[proxy.status].color} />
        )}
      </Box>
      {proxy?.status === "failed" && (
        <Alert
          severity="error"
          sx={{ mb: 1 }}
          action={
            <Button size="small" color="inherit" disabled={isRepublishing} onClick={() => republish(params)}>Retry</Button>
          }
        >
          {proxy.lastError || "The gateway publish failed."}
        </Alert>
      )}
      <A2AProxyFields
        draft={draft}
        onChange={setDraft}
        hasStoredSecret={hasStoredSecret(proxy)}
        error={error}
        disabled={busy}
      />
      <Box display="flex" justifyContent="flex-end" sx={{ mt: 1 }}>
        <Button size="small" variant="contained" disabled={!canSave} onClick={save}>Save</Button>
      </Box>
    </Box>
  );
}
```

If `addConfirmation`'s option type rejects `analytics.action` values other than those already in use, use `"remove"` for both and keep the distinct entity. The provider falls back to defaults for missing values (`ConfirmationDialogProvider.tsx:82-84`).

- [ ] **Step 6: Wire them into the panel and the URL form**

`AgentCardSourceForm.tsx` (block mode only; the inline editor stays URL-only):

```tsx
  const [proxyDraft, setProxyDraft] = useState(() => draftFromProxy(undefined));
  const { mutate: saveProxy } = useSetA2AProxy();
  const proxyError = inline ? null : upstreamAuthError(proxyDraft);

  const trimmed = url.trim();
  const invalid = trimmed !== "" && validateEndpointUrl(trimmed) !== null;
  const canSave = !!trimmed && !invalid && trimmed !== currentUrl && !isSaving && !proxyError;
  const submit = () => {
    if (!canSave) return;
    save({ params, body: { url: trimmed } }, {
      onSuccess: () => {
        // The URL is saved even if routing fails; useApiMutation reports that failure on its own.
        if (!inline && proxyDraft.enabled) saveProxy({ params, body: proxyRequestOf(proxyDraft) });
        onDone?.();
      },
    });
  };
```

In the block form's JSX, wrap the existing URL row (`TextInput` + Save button) in its own `<Box display="flex" gap={1} alignItems="flex-start">`, make the outer `component="form"` box a plain block (`sx={{ mb: 1 }}`), and add below the row:

```tsx
      <Box sx={{ mt: 1 }}>
        <A2AProxyFields
          draft={proxyDraft}
          onChange={setProxyDraft}
          hasStoredSecret={false}
          error={proxyError}
          disabled={isSaving}
        />
      </Box>
```

Imports: `useSetA2AProxy` from `@agent-management-platform/api-client`; `A2AProxyFields` from `./A2AProxyFields`; `draftFromProxy`, `proxyRequestOf`, `upstreamAuthError` from `./a2aProxyDraft`.

`EnvAgentCardSection.tsx`:

```tsx
  const { data: proxyData, isError: proxyIsError, error: proxyError } =
    useGetA2AProxy(params, { enabled: isA2A && !!external });
  // A 404 means direct; TanStack keeps the previous data on error, so it is dropped here.
  const proxy = proxyIsError && isNotFound(proxyError) ? undefined : proxyData;
  const proxyFailed = proxy?.status === "failed";
  const { data, isLoading, isError, error } = useGetAgentCard(params, { enabled: isA2A, poll: !proxyFailed });
```

These replace the existing `useGetAgentCard` line and sit above the `if (!isA2A) return null` early return, so the hook order is stable. In the body:
- after `{showUrlForm && …}`, add `{external && sourceUrl && <EnvA2AProxySection params={params} proxy={proxy} />}`;
- change the card status chip's condition to `card.status !== "fetched" && !parkedBehindFailedPublish`, where `const parkedBehindFailedPublish = proxyFailed && card?.status === "pending";` is computed after `card`.

- [ ] **Step 7: Run tests, lint, build**

```bash
pnpm --filter @agent-management-platform/overview run test
pnpm --filter @agent-management-platform/overview run lint
pnpm --filter @agent-management-platform/overview... run build
```

Expected: PASS, including the existing panel tests.

- [ ] **Step 8: Commit**

```bash
git add console/workspaces/pages/overview/src/AgentOverview/a2aProxyDraft.ts \
  console/workspaces/pages/overview/src/AgentOverview/a2aProxyDraft.test.ts \
  console/workspaces/pages/overview/src/AgentOverview/A2AProxyFields.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvA2AProxySection.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvA2AProxySection.test.tsx \
  console/workspaces/pages/overview/src/AgentOverview/AgentCardSourceForm.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.test.tsx
git commit -m "Let the agent card panel route an external A2A agent through the gateway"
```

---

### Task 17: Inbound security settings for a proxied environment

**Files:**
- Modify: `console/workspaces/pages/deploy/src/subComponent/SecurityConfigSections.tsx` (`:127-128` and the six other `isApiAgent` uses), `console/workspaces/pages/deploy/src/subComponent/SecurityConfigSections.a2a.test.tsx`, `console/workspaces/pages/deploy/src/index.ts`
- Create: `console/workspaces/pages/deploy/src/subComponent/ProxySecurityDrawer.tsx`
- Modify: `console/workspaces/pages/overview/package.json`, `console/pnpm-lock.yaml`, `console/workspaces/pages/overview/src/AgentOverview/EnvA2AProxySection.tsx`, `console/workspaces/pages/overview/src/AgentOverview/EnvA2AProxySection.test.tsx`, `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.test.tsx`

**Interfaces:**
- Consumes: `SecurityConfigSections` and its handle; `useGetAgentConfigurations`, `useUpdateAgentDeploySettings`, `invalidateA2AProxy` (Task 15); the backend deploy-settings change (Task 11).
- Produces: `ProxySecurityDrawer({ open, onClose, orgName, projName, agentName, environment })`, exported from `@agent-management-platform/deploy`.

`SecurityConfigSections` renders and builds a body for external A2A agents too. The drawer saves only `PUT /deploy-settings`; `EditDeployConfigDrawer`'s follow-up `PUT /configurations` needs a release binding, which an external agent does not have. The section opens it only while the environment is proxied; the backend rejects a direct one anyway (Task 11).

- [ ] **Step 1: Confirm the GET the drawer depends on**

`SecurityConfigSections` is seeded from `GET …/configurations?environment=`. For an external agent that call lists workloads and release bindings (`clients/openchoreosvc/client/components.go:2845-2950`); the client filters both by component and tolerates empty lists, and the per-environment config read returns nil when no row exists ("Spec corrections" 14), so it should answer 200. With the local stack up, confirm it:

```bash
curl -s -H "Authorization: Bearer <token>" \
  "http://localhost:9000/api/v1/orgs/default/projects/<project>/agents/<external-a2a-agent>/configurations?environment=development" | jq .
```

Use a console session token from the browser's devtools. Expected: 200 with the env's `enableApiKeySecurity` / `corsConfig` fields. A failure here is a bug to report with the response body, not a design change; carry on with the task either way and note the result in the report.

- [ ] **Step 2: Write the failing tests**

In `SecurityConfigSections.a2a.test.tsx`, add a helper next to `mockAgent`, then the tests:

```tsx
const mockAgentOfType = (type: string, subType: string) =>
  vi.mocked(useGetAgent).mockReturnValue({
    data: { agentType: { type, subType } },
  } as unknown as ReturnType<typeof useGetAgent>);

  it("serves a proxied external A2A agent's inbound auth and CORS", () => {
    mockAgentOfType("external-agent-api", "a2a-agent");
    const ref = renderSections(configurations());
    expect(screen.getByText("CORS Configuration")).toBeInTheDocument();
    expect(ref.current?.buildBody()).toMatchObject({ enableApiKeySecurity: true });
  });

  it("renders nothing for an external agent that is not A2A", () => {
    mockAgentOfType("external-agent-api", "custom");
    const ref = renderSections(configurations());
    expect(screen.queryByText("CORS Configuration")).not.toBeInTheDocument();
    expect(ref.current?.buildBody()).toEqual({});
  });
```

In `EnvA2AProxySection.test.tsx` **and** `EnvAgentCardSection.test.tsx` (the panel renders the section, and the real drawer would call `api-client` hooks those tests do not mock):
- add `vi.mock("@agent-management-platform/deploy", () => ({ ProxySecurityDrawer: ({ open }: { open: boolean }) => (open ? <div>security drawer</div> : null) }));`

In `EnvA2AProxySection.test.tsx` only:
- append:

```tsx
  it("opens the gateway security settings only while proxied", () => {
    const { rerender } = render(<EnvA2AProxySection params={params} />);
    expect(screen.queryByRole("button", { name: /security settings/i })).not.toBeInTheDocument();
    rerender(<EnvA2AProxySection params={params} proxy={stored} />);
    fireEvent.click(screen.getByRole("button", { name: /security settings/i }));
    expect(screen.getByText("security drawer")).toBeInTheDocument();
  });
```

Run: `pnpm --filter @agent-management-platform/deploy run test` and `pnpm --filter @agent-management-platform/overview run test`
Expected: both new tests FAIL.

- [ ] **Step 3: Relax `SecurityConfigSections`**

Replace `const isApiAgent = agent?.agentType?.type === "agent-api";` with:

```tsx
    // A proxied external A2A agent takes inbound auth and CORS at the gateway, like an API agent.
    const hasGatewayPolicies = agent?.agentType?.type === "agent-api" ||
      (agent?.agentType?.type === "external-agent-api" && agent?.agentType?.subType === "a2a-agent");
```

Rename every remaining `isApiAgent` to `hasGatewayPolicies`: the identity-provider query, `oauthInvalid`, `timeoutInvalid`, `buildBody`, its dependency list, and the render guard. Keep `isA2AAgent` as it is (it is computed from the subtype).

- [ ] **Step 4: Add the drawer and export it**

`pages/deploy/src/subComponent/ProxySecurityDrawer.tsx`:

```tsx
import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Box, Button, Form } from "@wso2/oxygen-ui";
import { ShieldCheck } from "@wso2/oxygen-ui-icons-react";
import { DrawerContent, DrawerHeader, DrawerWrapper } from "@agent-management-platform/views";
import {
  invalidateA2AProxy,
  useGetAgentConfigurations,
  useUpdateAgentDeploySettings,
} from "@agent-management-platform/api-client";
import { SecurityConfigSections, type SecurityConfigHandle } from "./SecurityConfigSections";

export interface ProxySecurityDrawerProps {
  open: boolean;
  onClose: () => void;
  orgName: string;
  projName: string;
  agentName: string;
  environment: string;
}

/**
 * Inbound auth, CORS and card CORS for an external A2A agent routed through one environment's
 * gateway. Unlike EditDeployConfigDrawer it saves only deploy settings: there is no release binding.
 */
export function ProxySecurityDrawer({ open, onClose, orgName, projName, agentName, environment }: ProxySecurityDrawerProps) {
  const securityRef = useRef<SecurityConfigHandle>(null);
  const [valid, setValid] = useState(true);
  const queryClient = useQueryClient();
  const { data: configurations } = useGetAgentConfigurations({ orgName, projName, agentName }, { environment });
  const { mutate: updateDeploySettings, isPending } = useUpdateAgentDeploySettings();

  const apply = () => {
    const security = securityRef.current;
    if (!security?.validate()) return;
    updateDeploySettings(
      { params: { orgName, projName, agentName }, body: { environmentName: environment, ...security.buildBody() } },
      {
        onSuccess: () => {
          // The save republishes, so the gateway status goes back to pending.
          void invalidateA2AProxy(queryClient, { orgName, projName, agentName, envId: environment });
          onClose();
        },
      },
    );
  };

  return (
    <DrawerWrapper open={open} onClose={onClose}>
      <DrawerHeader icon={<ShieldCheck size={24} />} title="Gateway security" onClose={onClose} />
      <DrawerContent>
        <Form.Stack spacing={3}>
          <SecurityConfigSections
            ref={securityRef}
            orgName={orgName}
            projName={projName}
            agentName={agentName}
            environment={environment}
            open={open}
            disabled={isPending}
            configurations={configurations}
            onValidityChange={setValid}
          />
          <Box display="flex" justifyContent="flex-end" gap={1}>
            <Button variant="outlined" onClick={onClose} disabled={isPending}>Cancel</Button>
            <Button variant="contained" onClick={apply} disabled={!valid || isPending}>Apply</Button>
          </Box>
        </Form.Stack>
      </DrawerContent>
    </DrawerWrapper>
  );
}
```

If `@tanstack/react-query` is not already a dependency of `@agent-management-platform/deploy` (check `pages/deploy/package.json`), add it at the version `libs/api-client/package.json` uses.

In `pages/deploy/src/index.ts`, add:

```ts
export { ProxySecurityDrawer, type ProxySecurityDrawerProps } from './subComponent/ProxySecurityDrawer';
```

- [ ] **Step 5: Open it from the gateway section**

Before touching anything, record the overview bundle size with `pnpm --filter @agent-management-platform/overview... run build && du -sh workspaces/pages/overview/dist` (from `console/`); Step 6 compares against it.

In `pages/overview/package.json` `dependencies`, add `"@agent-management-platform/deploy": "workspace:*"`. Then run `pnpm install` from `console/` and check that `pnpm-lock.yaml` changes only by that link.

In `EnvA2AProxySection.tsx`:

1. Next to the other `useState`, add `const [securityOpen, setSecurityOpen] = useState(false);`.
2. Inside the header `<Box display="flex" …>`, after the status `Chip`, add:

```tsx
        {proxy && (
          <Button size="small" variant="text" startIcon={<ShieldCheck size={14} />} onClick={() => setSecurityOpen(true)}>
            Security settings
          </Button>
        )}
```

3. As the last child of the outer `<Box sx={{ mb: 2 }}>`, after the Save row, add:

```tsx
      {proxy && (
        <ProxySecurityDrawer
          open={securityOpen}
          onClose={() => setSecurityOpen(false)}
          orgName={params.orgName ?? ""}
          projName={params.projName ?? ""}
          agentName={params.agentName ?? ""}
          environment={params.envId ?? ""}
        />
      )}
```

Imports: `ShieldCheck` from `@wso2/oxygen-ui-icons-react`; `ProxySecurityDrawer` from `@agent-management-platform/deploy`.

- [ ] **Step 6: Run tests, lint, build**

```bash
pnpm --filter @agent-management-platform/deploy run test
pnpm --filter @agent-management-platform/deploy run lint
pnpm --filter @agent-management-platform/overview run test
pnpm --filter @agent-management-platform/overview run lint
pnpm --filter @agent-management-platform/overview... run build
pnpm --filter @agent-management-platform/am-core-ui... run build
du -sh workspaces/pages/overview/dist
```

Expected: PASS. The core-ui build proves the lazy-loaded pages still bundle with the new page-to-page dependency. Overview's `vite.config.ts` externalizes only `peerDependencies`, so the deploy package is bundled in; both packages declare `sideEffects`, so the `dist` should grow by the drawer and the security sections (tens of KB), not by the whole Deploy page. If it grew by the Deploy page's size, load the drawer with `React.lazy` in `EnvA2AProxySection.tsx` and note it in the report.

- [ ] **Step 7: Commit**

```bash
git add console/workspaces/pages/deploy/src/subComponent/SecurityConfigSections.tsx \
  console/workspaces/pages/deploy/src/subComponent/SecurityConfigSections.a2a.test.tsx \
  console/workspaces/pages/deploy/src/subComponent/ProxySecurityDrawer.tsx console/workspaces/pages/deploy/src/index.ts \
  console/workspaces/pages/overview/package.json console/pnpm-lock.yaml \
  console/workspaces/pages/overview/src/AgentOverview/EnvA2AProxySection.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvA2AProxySection.test.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.test.tsx
git commit -m "Edit gateway security for a proxied external A2A agent"
```

(Add `console/workspaces/pages/deploy/package.json` if Step 4 changed it.)

---

### Task 18: API keys for proxied external A2A agents

**Files:**
- Modify: `console/workspaces/pages/agent-security/src/Security.Component.tsx`
- Create: `console/workspaces/pages/agent-security/src/Security.Component.test.tsx`
- Modify: `console/workspaces/core-ui/src/Layouts/OxygenLayout/navigationItems.tsx` (external-agent branch, Security section `:266-287`)

The backend has no agent-type gate on API keys (`services/agent_apikey_service.go:130-150`); keys bind to the per-environment artifact that enabling the proxy creates. The console has two gates to relax:
- the Credentials page requires an active deployment (`Security.Component.tsx:63-69`);
- the external-agent navigation has no Credentials item at all ("Spec corrections" 9).

For an external A2A agent, "served" means proxied in that environment. The page then skips the deployments call, which does not apply to an external agent.

- [ ] **Step 1: Write the page tests**

`pages/agent-security/src/Security.Component.test.tsx` (license header):

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@agent-management-platform/api-client", () => ({
  useCreateAgentAPIKey: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useGetA2AProxy: vi.fn(),
  useGetAgent: vi.fn(),
  useGetAgentConfigurations: vi.fn(() => ({ data: { enableApiKeySecurity: true }, isLoading: false })),
  useListAgentDeployments: vi.fn(() => ({ data: undefined, isLoading: false })),
  useListAgentAPIKeys: vi.fn(() => ({ data: [], isLoading: false, isError: false })),
  useRevokeAgentAPIKey: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
}));
vi.mock("@agent-management-platform/shared-component", () => ({
  APIKeysManager: () => <div>api keys manager</div>,
  EnvironmentSelector: () => null,
}));
vi.mock("@agent-management-platform/views", () => ({
  PageLayout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import { useGetA2AProxy, useGetAgent, useListAgentDeployments } from "@agent-management-platform/api-client";
import { SecurityComponent } from "./Security.Component";

const renderPage = () =>
  render(
    <MemoryRouter initialEntries={["/org/o/project/p/agents/a/environment/dev/security"]}>
      <Routes>
        <Route path="/org/:orgId/project/:projectId/agents/:agentId/environment/:envId/security" element={<SecurityComponent />} />
      </Routes>
    </MemoryRouter>,
  );

const externalA2A = () =>
  vi.mocked(useGetAgent).mockReturnValue({
    data: { provisioning: { type: "external" }, agentType: { type: "external-agent-api", subType: "a2a-agent" } },
    isLoading: false,
  } as unknown as ReturnType<typeof useGetAgent>);

beforeEach(() => vi.clearAllMocks());

describe("SecurityComponent", () => {
  it("manages API keys of an external A2A agent routed through the gateway", () => {
    externalA2A();
    vi.mocked(useGetA2AProxy).mockReturnValue({
      data: { status: "published", lastError: "", upstreamAuthHeader: "" }, isLoading: false, isError: false, error: null,
    } as unknown as ReturnType<typeof useGetA2AProxy>);
    renderPage();
    expect(screen.getByText("api keys manager")).toBeInTheDocument();
    expect(vi.mocked(useListAgentDeployments)).toHaveBeenCalledWith(expect.objectContaining({ orgName: undefined }));
  });

  it("explains why a direct external A2A agent has no API keys", () => {
    externalA2A();
    vi.mocked(useGetA2AProxy).mockReturnValue({
      data: undefined, isLoading: false, isError: true, error: Object.assign(new Error("nf"), { status: 404 }),
    } as unknown as ReturnType<typeof useGetA2AProxy>);
    renderPage();
    expect(screen.getByText(/not routed through the gateway/i)).toBeInTheDocument();
  });
});
```

Run: `pnpm --filter @agent-management-platform/agent-security run test`
Expected: FAIL (the page shows "Agent is not deployed").

- [ ] **Step 2: Relax the page**

In `Security.Component.tsx`:

```tsx
  const { data: agent, isLoading: isLoadingAgent } = useGetAgent({ orgName: orgId, projName: projectId, agentName: agentId });
  const isExternalA2A = agent?.provisioning?.type === "external" && agent?.agentType?.subType === "a2a-agent";
  const { data: proxyData, isError: proxyIsError, error: proxyError, isLoading: isLoadingProxy } = useGetA2AProxy(
    { orgName: orgId, projName: projectId, agentName: agentId, envId },
    { enabled: isExternalA2A },
  );
  // A 404 means direct; the previous data may still be cached.
  const proxied = !!proxyData && !(proxyIsError && (proxyError as { status?: number } | null)?.status === 404);
```

- Pass `orgName: agent && !isExternalA2A ? orgId : undefined` to `useListAgentDeployments`, so it neither runs before the agent type is known nor runs for an external agent.
- Replace `hasActiveDeployment` with:

```tsx
  // A proxied external agent is served by the gateway, so its keys work without a deployment.
  const servedByGateway = isExternalA2A ? proxied : currentDeployment?.status === "active";
```

- Use `servedByGateway` in `shouldLoadKeys` and in the first empty-state branch. Add `isLoadingAgent || isLoadingProxy` to `isLoading`.
- For an external A2A agent that is not served, show:

```tsx
      {
        illustration: <Rocket size={48} />,
        title: "Agent is not routed through the gateway",
        description: "Turn on Route through gateway for this environment on the agent's overview to manage its API keys.",
      }
```

  For an internal agent, keep the existing "Agent is not deployed" state.
- In the "security disabled" state, an external A2A agent links to the agent overview (`absoluteRouteMap.children.org.children.projects.children.agents.path` with `{ orgId, projectId, agentId }`) with the text "Go to overview". The deploy page does not apply to it.

Import `useGetA2AProxy` and `useGetAgent` from `@agent-management-platform/api-client`.

- [ ] **Step 3: Add the navigation item**

In `navigationItems.tsx`, in the external-agent branch's "Security" section (after the "Agent ID" item, `:271-285`):

```tsx
          ...(agent?.agentType?.subType === "a2a-agent"
            ? [
                {
                  label: "Credentials",
                  type: "item" as const,
                  icon: <ShieldCheck size={20} />,
                  isActive: !!matchPath(
                    absoluteRouteMap.children.org.children.projects.children
                      .agents.children.environment.children.security.wildPath,
                    pathname,
                  ),
                  href: generatePath(
                    absoluteRouteMap.children.org.children.projects.children
                      .agents.children.environment.children.security.path,
                    { orgId, projectId, agentId, envId: defaultEnv },
                  ),
                },
              ]
            : []),
```

The item opens the default environment; the page then says per environment whether keys apply. The two internal-agent branches (`:423`, `:650`) stay as they are.

- [ ] **Step 4: Run tests, lint, build**

```bash
pnpm --filter @agent-management-platform/agent-security run test
pnpm --filter @agent-management-platform/agent-security run lint
pnpm --filter @agent-management-platform/am-core-ui run lint
pnpm --filter @agent-management-platform/am-core-ui... run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add console/workspaces/pages/agent-security/src/Security.Component.tsx \
  console/workspaces/pages/agent-security/src/Security.Component.test.tsx \
  console/workspaces/core-ui/src/Layouts/OxygenLayout/navigationItems.tsx
git commit -m "Offer API keys for external A2A agents routed through the gateway"
```

---

### Task 19: Full verification and the manual check

No code beyond what a failure here leads to. Report results; do not push or open a PR unless asked.

- [ ] **Step 1: Automated suites**

From `agent-manager-service/`:

```bash
make fmt && git diff --exit-code   # formatting already committed
make test-unit
make test-integration              # recreates agentmanager_test only
go build -tags=integration ./...
PATH=$PWD/.bin:$PATH golangci-lint run --config .github/linters/.golangci.yaml ./...
```

From `console/`: `pnpm lint` and `pnpm build`, plus `pnpm --filter <pkg> run test` for api-client, overview, deploy and agent-security.

Expected: all green. Record any failure with its output; do not paper over it.

- [ ] **Step 2: Bring the local stack up on this branch**

Rebuild and restart the service and console containers from this worktree (the repo's `make setup` flow, or the compose commands in `deployments/docker-compose.yml`). Migration 048 runs on start. Confirm it in the service log, or with:

```bash
docker exec agent-manager-db psql -U agentmanager -d agentmanager -c '\d a2a_proxies'
```

- [ ] **Step 3: Manual check**

The SSRF guard has no local override, so the origin needs a public address. Run `samples/a2a-agent` locally and expose it through a tunnel (for example `cloudflared tunnel --url http://localhost:<port>`). Set `AGENT_PUBLIC_BASE_URL` to the tunnel URL so the card's interfaces (`/rpc`, `/rest`) are on the card's host. The ingress gateway is reachable at `http://default-default.gateway.localhost:19080` (vhost in the `gateways` table).

1. Create an external agent with subtype `a2a-agent` and register `https://<tunnel>/.well-known/agent-card.json` for the development environment. The card is fetched directly; its interface URLs are the tunnel's.
2. Turn on **Route through gateway** and save. The chip goes "Publishing to gateway", then "Routed through gateway". The stored card's interface URLs become `http://default-default.gateway.localhost:19080/<agent>/rpc` and `…/rest`, while the panel header still shows the tunnel card URL (the registered URL is unchanged).
3. On **Credentials**, create an API key. Then:
   - `curl -i -X POST http://default-default.gateway.localhost:19080/<agent>/rpc -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' -H 'X-API-Key: <key>' -d '{"jsonrpc":"2.0","id":"1","method":"SendMessage","params":{"message":{"role":"ROLE_USER","messageId":"m1","parts":[{"text":"hello"}]}}}'` reaches the agent;
   - the same call without the key is rejected by the gateway (401).
4. Set an upstream header `X-Upstream-Check` with any value and save. The tunnel's request inspector (or a temporary log line in the sample, not committed) shows the header on the JSON-RPC call **and** on the gateway's own `/.well-known/agent-card.json` fetch (trigger it with `curl http://default-default.gateway.localhost:19080/<agent>/.well-known/agent-card.json`). Clear it and confirm the dialog; the header stops arriving.
5. Stream for over 30s. Use `SendStreamingMessage` against a sample whose executor sleeps 45s before its last event (a temporary local edit, not committed). The stream completes; before Task 14 it was cut at 30s.
6. Switch to direct (confirm). The gateway URL starts returning 404, the card is fetched again from the tunnel, and the API key still exists. Switch back to proxied: the same key works again.
7. Remove the card source while proxied: the gateway stops serving the agent, the card row is gone, and the audit log has both `agent:remove-card-source` and `agent:remove-a2a-proxy`.
8. Environment deletion: if a throwaway environment can be created and mapped to the ingress gateway locally, proxy an agent there, delete the environment, and confirm the gateway no longer serves it and the `a2a_proxies`, `a2a_publications` and artifact rows for it are gone. If that setup is not practical locally, say so; the unit tests in Task 12 cover the logic.

- [ ] **Step 4: Report**

Summarise for the user:
- what passed;
- anything that failed, with output;
- items for the PR description:
  - migration 048;
  - the regenerated amctl client (and any renamed enum constants);
  - the gateway issues for upstream (spec open point 4 plus "Spec corrections" 1–3);
  - the security-review flags: gateway-side SSRF on upstream connections, the `{{` template rendering;
  - the cloud-routing open point (spec open point 1).
