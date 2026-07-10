# Technical Debt & Consistency Analysis

**Date:** 2026-07-11 (audited at commit `949344d9`)
**Method:** five parallel deep-audit passes covering the Go control plane, the React console, the amctl CLI, the Python/observability components, and cross-cutting infrastructure (CI, helm, e2e, docs). Every finding is evidence-backed with file references. Impact ordering weighs correctness/security risk first, then velocity drag, then polish.

## Contents

| Report | Scope |
|---|---|
| [agent-manager-service.md](agent-manager-service.md) | Go control plane: layering, transactions, authz, codegen, tests |
| [console.md](console.md) | React/TS Rush monorepo: tests, toolchain skew, duplication, API layer |
| [cli.md](cli.md) | amctl: flag UX, client codegen, error envelopes, test gaps |
| [python-and-observability.md](python-and-observability.md) | amp-evaluation, amp-instrumentation, provider, evaluation-job, traces-observer-service |
| [infrastructure.md](infrastructure.md) | CI workflows, Makefiles, helm charts, e2e, docs, repo hygiene, releases |

This file is the synthesis: cross-cutting themes ordered by impact, with the per-domain reports as backing detail.

---

## TLDR

This is a healthier codebase than most at this commit velocity (~1,900 commits in 4 months) — the CLI and the newer Python surfaces are genuinely well-engineered. The debt clusters into four themes, in order of impact:

1. **The security-critical code paths are the least-tested code in the repo.**
2. **The test infrastructure gives an illusion of safety — much of it never runs.**
3. **The platform's wire contracts are hand-duplicated in 3+ places with no drift protection.**
4. **Copy-paste is the default reuse mechanism** in CI workflows, helm values, and console components.

Most of the highest-impact fixes are small (S-sized); the structural ones can be done incrementally.

---

## Tier 1 — Correctness/security risk, mostly cheap to fix

### 1. The authorization enforcement layer has zero tests

The single highest-stakes finding. In agent-manager-service, all 221 routes rely on `RequirePermission`/`RequireOrgMatch` composition in `middleware/authorization.go` (191 lines) and `middleware/orgresolver.go` — **no test file exists anywhere in `middleware/`**, and no test in the repo ever invokes those functions. The route registrar has 5 variants including `HandleFuncWithValidationAndAuthzAllowRootOU` (root-OU bypass semantics, `middleware/path_params.go:68-83`) — untested. Same pattern in traces-observer-service: its JWT/JWKS middleware has zero tests. The `rbac/` role→permission map is also untested, so a wrong entry silently over- or under-grants.

A regression here is a tenant-isolation or privilege-escalation bug, and nothing would catch it before production.

**Do:** table-driven tests with fake tokens covering allow/deny/org-mismatch/root-OU-bypass per registrar variant, plus one golden-list test over `rbac.PredefinedRolePermissions`. Effort: ~2-3 days total, protecting the highest-stakes code in the repo.

### 2. The test safety net is largely an illusion — CI never runs what exists

Three independent audits converged on this:

- **Console CI never runs tests.** `amp-console-package-pr-checks.yaml` has only `lint` and `build` jobs. The 10 test files that exist (vs 573 source files) are stale scaffolds asserting template strings like `'Overview - Component Level'` that the components no longer render — they'd fail if anyone ran them. Meanwhile 17 packages carry full vitest configs with no tests.
- **The 11k-line e2e suite never runs on PRs.** `e2e.yaml` is `workflow_dispatch`-only; the only automatic runs are the nightly cron and releases. Regressions in the service, console, or charts surface up to 24h after merge, when bisection is expensive.
- **Coverage is inverted relative to risk** in the Go service: services are decently covered, but controllers sit at 6/29 files tested and `middleware/`, `rbac/`, `websocket/`, `eventhub/`, `db/` at zero. In the CLI, the untested packages are exactly the stateful ones: auth token refresh (`cmdutil/factory.go:180-258`), login/PKCE, and context linking.

**Do:** (a) add `rush test` to console CI and delete the 8 stale scaffolds (S); (b) tag a Ginkgo smoke subset that runs on PRs touching `agent-manager-service/**` or the charts (M); (c) prioritize tests for the finding-1 paths over broad coverage goals.

### 3. Wire contracts are hand-duplicated with no drift protection

The platform's core data contracts exist in multiple hand-maintained copies, and the one codegen gate that exists is guarding the wrong thing:

- **traces-observer-service hand-mirrors agent-manager-service's models field-for-field** (`opensearch/types.go:44-58` ↔ `spec/model_span.go:22-48` — `Span`, `AmpAttributes`, `TokenUsage`, `FullTrace`, etc.), plus parallel copies of JWT middleware, OAuth2 client-credentials providers, CORS, and config loading. Its `.env.example` literally says values "must match agent-manager-service".
- **No CI check that `make spec` output matches checked-in `spec/`** in agent-manager-service — and it has already burned the team: commit `f4b94113` ("fix code gen failure") hand-patched generated files after a spec change landed without regeneration.
- **The spec lied about build status** until 2026-07-10: it advertised a `BuildCompleted/...` enum while the server passed upstream WorkflowRun phases through verbatim, so the CLI hard-codes `"Completed"/"Succeeded"` strings (`cli/pkg/cmd/agent/deploy.go:366-375`). Commit `ab1fe84c` fixed the spec side; the CLI's hand-coded string list and stale comment remain and should now be switched to the generated enum.
- **The CLI's traces-observer client is hand-written** (`pkg/clients/traceobssvc/types.go` — ~130 lines of mirrored structs) even though `traces-observer-service/docs/openapi.yaml` exists and the oapi-codegen machinery is already in the repo.
- **The `gen_ai.*`/`traceloop.*` span-attribute vocabulary** — the platform's core observability contract — is inlined as string literals in `traces-observer-service/opensearch/process.go`, re-hardcoded in `test/instrumentation-matrix/harness/classify.py`, and only partially covered by the gen-contract drift check.
- **Three divergent copies of the instrumentation bootstrap** (`sitecustomize.py`): the platform-injected provider copy lacks the insecure-exporter flag, agent-version resource attribute, idempotence guard, and debug logging that the pip-installed `amp_instrumentation._bootstrap` has — so injected and pip-installed agents behave observably differently.

**Do:** the cheapest, highest-leverage item in this report is a **CI job that runs `make spec` + client codegen and fails on `git diff --exit-code`** (hours). Then: switch the CLI to the (now-fixed) generated build-status enum; generate the TOS models and the CLI's traceobssvc client from the existing specs; make the provider image vendor `amp_instrumentation._bootstrap`; centralize span-attribute constants feeding gen-contract.

---

## Tier 2 — Correctness risk, structural, medium effort

### 4. agent-manager-service: three transaction patterns, layering bypasses, and two 5k-line god services

- **Three competing transaction styles** coexist: repos owning transactions internally, services opening `db.Transaction` and passing `tx`, and manual `Begin/Commit/Rollback` with hand-rolled panic recovery (`services/llm_provider_service.go:937-987`) — inconsistent even within itself (one variant defers rollback, another doesn't). There's an admitted TOCTOU at `llm_provider_service.go:957`: the existence check deliberately runs outside the transaction that performs the update. Nine services hold `*gorm.DB` directly, which is why unit-testing them requires workarounds.
- **Controllers inject repositories directly** (`gateway_internal_controller.go:51-52`, `agent_identity_controller.go:66-67`, `llm_controller.go:70`), contradicting the repo's own AGENTS.md layering rules and dodging service-layer org-scoping.
- **God functions in the highest-churn files:** `agent_configuration_service.go` is 5,342 lines with a 482-line `Update()` organized by "Phase 1/1b" comments and swallowed best-effort cleanup; `agent_manager.go` is 4,479 lines with a 330-line `DeployAgent`. The console mirrors this: `EvaluatorForm.tsx` at 1,777 lines, five files over 1,000, 39 over 500.
- **Latent multi-tenancy bug:** `NamespaceFor(_ string)` discards its argument (`clients/openchoreosvc/client/client.go:165`), and 8 call sites pass `org.Name` where 30 pass `ouID`. Everything works only because the parameter is ignored; the day namespace resolution becomes tenant-aware, 8 paths break silently. Fix the call sites now while it's a grep-driven S.

**Do:** normalize the OpenChoreo call sites immediately (S); standardize on tx-accepting repo methods composed under one `db.Transaction` helper, migrating service-by-service starting with `llm_provider_service` (M-L); adopt "no new code in god files — extract the phase you're touching" rather than a big-bang refactor.

### 5. traces-observer-service is the weakest service in the repo

Beyond the duplication in theme 3: **every controller error becomes HTTP 500** (`handlers/handlers.go:120,183,259,288`) — the console can't distinguish "trace not found" from an outage; upstream 404s are collapsed into `fmt.Errorf("unexpected status %d")`. No panic-recovery middleware (AMS has one). And the service's identity is misleading: the core package is named `opensearch/` but the service **has no OpenSearch dependency at all** — it proxies a typed HTTP API; the Makefile and CI still export dead `OPENSEARCH_USERNAME=admin`/`PASSWORD=admin` vars that nothing reads (which look like a security hole to any auditor).

**Do:** typed upstream errors mapped to proper status codes + recover middleware + tests for `middleware/` and `observer/` (M); rename the package and delete the dead env vars and legacy structs (S, one PR).

### 6. evaluation-job runs tenant code in-process with platform secrets in reach

`main.py:392` `exec()`s user-supplied evaluator code and `main.py:436` `eval()`s f-string prompt templates **with full builtins**, in a process whose `os.environ` holds `IDP_CLIENT_SECRET` and `LLM_API_KEY`. The K8s Job is the real sandbox, but nothing scrubs secrets before executing user code, and the `eval` path is reachable from a lower-trust input (a template) than "arbitrary code evaluator". Relatedly, per-handler copy-pasted API-key auth in `gateway_internal_controller.go` (the same 15-line block pasted 7 times) means one forgotten paste = an unauthenticated internal route.

**Do:** scrub secrets from the environment before `exec` (fetch tokens first), replace template `eval` with `string.Formatter` substitution, document the trust model (M); extract a gateway API-key middleware mirroring the existing publisher-auth pattern (S).

---

## Tier 3 — Velocity drag and consistency debt

### 7. Version/toolchain skew is endemic — and the enforcement mechanisms are switched off

The same story in every stack: **Node** is pinned three contradictory ways (CI `20.19.0`, `.nvmrc` `22.12.0` — which Rush's own `nodeSupportedVersionRange` rejects, rush.json allowing 18/20). **Console** builds the app with TypeScript 6/Vite 8 while all 20 page packages use TS 5.9/Vite 6 — and `ensureConsistentVersions` is commented out in `common-versions.json:52`. **Go** modules disagree (1.25.0 vs 1.25.7) and the shared `setup-go` composite action hardcodes agent-manager-service's go.mod while being used by traces-observer and e2e (wrong toolchain + guaranteed cache misses). **Python** lint targets py39 in a package requiring >=3.10, one package has no ruff config at all, and CI installs unpinned `ruff`/`mypy`. **GitHub Actions** pinning is split-brain: ~10% SHA-pinned, the rest tag-pinned. **golangci-lint**: one strict config protects one of three Go modules; the CLI uses staticcheck instead; e2e has no lint.

**Do:** these are almost all S-sized: fix `.nvmrc` + read it in CI via `node-version-file`; add `module-dir` input to setup-go; enable `ensureConsistentVersions` after one alignment pass; hoist one `.golangci.yaml`; run `pinact` once + Dependabot for actions; pin Python CI tools.

### 8. Copy-paste is the default reuse mechanism

- The **e2e job is pasted into 3 workflows** (~120 lines each) and has already drifted (pinning style, version pins re-declared per file). The **two Python package workflows are 99% identical** twins, x2 again for releases. → reusable `workflow_call` workflows.
- **Console:** identical `useValidatedForm.ts` in two packages, `CreateGitSecretModal` duplicated, `CreateButtons` triplicated, and *two parallel implementations* of the same env-var and file-mount editors living in the two shared libs simultaneously. Plus 28 near-identical per-package config stacks (~35 repeated devDependencies each).
- **CLI:** the same 8-line request/error envelope pasted ~55 times — which directly causes a real user-facing bug: hand-picked status-code variant lists mean a 400/403 JSON error from the server degrades to *"server returned 400 with no JSON body"* while the real message sits unused in `resp.Body` (`cmdutil/errors.go:80-99`). A raw-body fallback in one function fixes every command (S).
- **Go controllers:** `errors.Is` → HTTP status switch chains pasted 50/40/35 times across the three biggest controllers → one shared sentinel→status mapper.
- **Helm:** the Thunder token URL hardcoded in 4 charts; a 104-entry OAuth scope list maintained **twice in the same values.yaml** (`:208` comma-separated, `:363` space-separated); cleartext dev secrets with a "dev-only" warning on only one of six.

### 9. Onboarding and repo weight

- **Three contradictory "run it locally" paths** (AGENTS.md `make setup`+compose at `localhost:3000`; quick-start container at `console.amp.localhost:8080`; raw setup scripts) — two of which are parallel installers of the same stack with independently duplicated version pins. README and CONTRIBUTING mention none of them. `make setup` is Colima/macOS-centric with no documented Linux path — and the docker0/UFW local-networking failure mode appears nowhere in the docs.
- **Repo weight grows monotonically:** an 83.5 MB SQL dump at `samples/customer-support-agent/db_backup.sql`, 171 MB of doc images duplicated across 9 versioned-docs snapshots (one 2.9 MB PNG exists 10x), and a 3.8 MB JSON fixture committed twice. Every one of 27 workflows pays checkout time for this.
- **`documentation/docs/` has silently diverged from its own published "latest"** — 17 of 34 files differ from `version-v0.18.x` with no cut scheduled, so edits are published nowhere.
- **Console ghost packages:** `workspaces/libs/core-ui/` is a corpse package.json absent from rush.json (confusingly named like the real `am-core-ui`), and `pages/profile-settings/` is a deleted package's debris.

### 10. Release choreography and CLI UX polish

- Releases are **6 disconnected manual `workflow_dispatch` workflows**, each with free-text version inputs and no cross-check; the Python release workflows `sed` real versions over the documented `0.0.0-dev` placeholder and push to main with no revert — the first release after branch protection tightens will fail or leave main inconsistent. → derive versions from the tag at build time.
- **`--env` means opposite things between sibling commands**: environment *name* in `agent status/logs/llm set` vs `KEY=VALUE` variable in `agent deploy/create` — `agent deploy --env dev` errors instead of deploying to dev. Plus `--apikey-env` vs `--llm-api-key-env` vs `--api-key` naming drift, and unpaginated `project list`/`org list`. All S/M with deprecation aliases.
- Console **query keys are inline string literals** with mixed conventions, re-typed in page code for invalidation (`ViewMonitor.Component.tsx:333` vs `hooks/monitors.ts:179`) — a rename silently breaks cache invalidation. → per-resource key factories + a lint rule (S/M).

---

## What's healthy (calibration)

Worth protecting: the CLI's architecture (generated client + CI drift gate, uniform error envelope, stable exit codes, atomic config writes); the Go service's error-wrapping discipline (1,278 `%w` wraps, clean `go vet`, uniform slog) and its unusually good AGENTS.md; console styling and state management (zero styled-components, zero ts-ignores, 26 `any` in 101k lines); amp-evaluation's ~1:1 test-to-source ratio; the instrumentation-matrix suite (pinned versions, contract drift checks, hermetic lock model — the best-maintained area in the repo); helm chart CI with strict lint + kubeconform; and near-zero TODO/FIXME density everywhere.

## If you only did two weeks of this

1. **Authz middleware + RBAC golden tests** (days) — theme 1.
2. **`make spec` drift check in CI** (hours) — theme 3.
3. **Console: add `rush test`, delete stale scaffolds, fix `.nvmrc`** (hours) — theme 2.
4. **CLI `ErrorFromServer` raw-body fallback** (hours) — every command's errors improve.
5. **Normalize the 8 `org.Name`→`ouID` OpenChoreo call sites** (hours) — defuses the latent tenancy bug.
6. **`setup-go` module-dir input + align Go versions** (hours).
7. **Gateway API-key auth → middleware** (hours).
8. **Move the 83 MB dump to a release asset** (hours) — stops the biggest weight source.

Everything structural (transactions, god files, TOS model sharing, reusable workflows, release conductor) is better run as an incremental campaign with "fix on touch" rules than as dedicated refactoring sprints, given the repo's commit velocity.
