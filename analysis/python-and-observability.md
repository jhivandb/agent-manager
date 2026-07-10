# Technical-Debt Audit: Python & Observability Components

**Date:** 2026-07-11 (audited at commit `949344d9`)
**Scope:** `libs/amp-evaluation`, `libs/amp-instrumentation`, `python-instrumentation-provider`, `evaluation-job`, `traces-observer-service`, `test/instrumentation-matrix`

**Overall shape:** the newer surfaces (instrumentation-matrix, provider lock model, amp-evaluation test suite) are in notably good health. The debt concentrates in traces-observer-service (duplicated infra + legacy naming), cross-package config skew, and three divergent copies of the instrumentation bootstrap.

---

## Finding 1 — traces-observer-service re-implements agent-manager-service infrastructure and wire models by hand

**Impact: HIGH** — the same wire contract and security-sensitive middleware are maintained twice in the same repo; drift lands as silent runtime breakage between the two services.

- Hand-written span models mirror AMS's OpenAPI-generated ones field-for-field: `traces-observer-service/opensearch/types.go:44-58` (`Span` with 13 fields, `json:"traceId"..."ampAttributes"`) ↔ `agent-manager-service/spec/model_span.go:22-48`; likewise `AmpAttributes`, `LLMTokenUsage`, `TokenUsage`, `FullTrace`, `TraceOverview`, `TraceStatus`, `SpanStatus` all exist in both.
- The constant `openchoreo.dev/component-uid` is defined 3 times: `traces-observer-service/observer/convert.go:26`, inline literal at `traces-observer-service/opensearch/process.go:76`, and `agent-manager-service/clients/openchoreosvc/client/constants.go:103`.
- Parallel JWT/JWKS middleware (`traces-observer-service/middleware/auth.go` ↔ AMS `middleware/jwtassertion/auth.go` — both fetch/cache JWKS, parse RSA keys, validate publisher audience; `.env.example` even says config "must match agent-manager-service values").
- Parallel OAuth2 client-credentials providers (`observer/auth.go:37-217` ↔ AMS `clients/openchoreosvc/client/auth.go`, same `GetToken`/`InvalidateToken`/401-retry pattern), plus duplicated CORS and env-config loaders.

**Remediation: M** — extract a shared internal Go module (wire models generated from one OpenAPI spec, shared auth/CORS/config packages), or at minimum generate TOS's models from the same spec AMS uses.

## Finding 2 — traces-observer-service error handling: everything becomes HTTP 500; no panic recovery; auth path untested

**Impact: HIGH** — the Console cannot distinguish "trace not found" from a real outage, and the only untested packages are the security-critical ones.

- All controller errors map to 500 at `handlers/handlers.go:120, 183, 259, 288`; the observer client collapses upstream status into `fmt.Errorf("unexpected status %d...")` at `observer/client.go:152-153` — upstream 404/400 surface as 500.
- No panic-recovery middleware (AMS has `middleware/panic_recover.go`; TOS has none).
- Test coverage is bimodal: `opensearch/` has ~2,300 test lines (excellent), but `middleware/` (JWT, JWKS, CORS — 2 source files) has **zero** tests, and `observer/client.go`/`auth.go` (HTTP + token cache) are untested; only `convert.go` has a 65-line test.
- `handlers.go:375-377`: `WriteHeader` before `Encode`, so a mid-stream encode failure yields a 200 with a truncated body.

**Remediation: M** — typed upstream errors (sentinel for 404/400) propagated to proper status codes, a recover middleware, and unit tests for `middleware` and `observer` (JWKS parsing and token refresh are very mockable).

## Finding 3 — Legacy "OpenSearch" identity in traces-observer-service: misnamed package, dead env vars, hardcoded admin/admin

**Impact: MEDIUM** — actively misleads readers; dead credentials in the Makefile look like a security issue even though nothing reads them.

- The service has **no** OpenSearch dependency at all — `go.mod` has a single direct dep (`golang-jwt/jwt/v5`); zero query-DSL construction anywhere (grep for `"_search"`, `"must"`, `"aggs"` is clean), so **no injection surface** — it proxies a typed HTTP API of the observability service.
- Yet the core package is named `opensearch/` (`process.go` is 2,304 lines of span enrichment, not queries), `Makefile` `docker-run` still exports `OPENSEARCH_ADDRESS`/`OPENSEARCH_USERNAME=admin`/`OPENSEARCH_PASSWORD=admin`/`OPENSEARCH_TRACE_INDEX="custom-otel-span-index"` which `config/config.go:58-85` no longer reads, and the CI-generated `.env` in `traces-observer-service-pr-checks.yaml` sets the same dead vars.
- Legacy unused structs: `TraceQueryParams`/`TraceByIdParams` and `TraceResponse`/`TraceDetailResponse` in `opensearch/types.go:22-41,153-167`.

**Remediation: S** — rename package (e.g. `spans` or `enrich`), delete dead env vars/structs, one PR.

## Finding 4 — Three divergent copies of the Traceloop bootstrap (`sitecustomize.py`)

**Impact: MEDIUM** — platform-injected agents and pip-installed agents get observably different behavior from what is supposed to be the same instrumentation.

- `python-instrumentation-provider/sitecustomize.py` (34 lines): no `OTEL_EXPORTER_OTLP_INSECURE`, no `AMP_AGENT_VERSION` resource attribute, no `AMP_DEBUG` logging, no idempotence guard, swallows all exceptions.
- `libs/amp-instrumentation/src/amp_instrumentation/_bootstrap/initialization.py` (145 lines): sets `OTEL_EXPORTER_OTLP_INSECURE=true`, adds `agent-manager/agent-version` resource attr from `AMP_AGENT_VERSION`, thread-safe idempotent init, `AMP_DEBUG` logging via `constants.py` — none of which the provider image gets.
- `test/instrumentation-matrix/providers/bootstrap/traceloop/sitecustomize.py:2` is a third copy that explicitly says "Mirrors python-instrumentation-provider/sitecustomize.py".
- Env-var names (`AMP_OTEL_ENDPOINT`, `AMP_AGENT_API_KEY`, `AMP_TRACE_CONTENT`, `TRACELOOP_*`) are centralized in `constants.py:23-32` but re-hardcoded as string literals in the other two copies.

**Remediation: M** — make the provider image install/vendor `amp_instrumentation._bootstrap` (it already builds from the repo), leaving one bootstrap implementation; matrix bootstrap can import the same module.

## Finding 5 — Python packaging/lint config skew and dead tool config across the three packages

**Impact: MEDIUM** — CI lint results differ per package for no reason, and part of the declared config is silently ignored.

- `libs/amp-evaluation/pyproject.toml`: dead `[tool.black]` targeting `py39/py310/py311` (below its own `requires-python = ">=3.10"`; CI runs `ruff format`, never black — black also sits unused in `dev` extras); `[tool.ruff] target-version = "py39"` also below the floor.
- `libs/amp-instrumentation/pyproject.toml`: **no** `[tool.ruff]` at all → CI (`amp-instrumentation-package-pr-checks.yaml`) lints with ruff defaults (line-length 88) while amp-evaluation and evaluation-job use 120; no mypy config either, yet CI runs mypy.
- Pytest config duplicated and conflicting in amp-evaluation: `pytest.ini` (wins) vs `[tool.pytest.ini_options]` in pyproject whose `addopts = "-v --cov=amp_evaluation ..."` is silently dead; CI re-passes `--cov` on the command line instead.
- `evaluation-job/pyproject.toml` has tool config only (no `[project]`), deps in `requirements.txt` — a third packaging style; ruff/mypy target py311 vs py39 elsewhere.
- All three PR workflows install unpinned `ruff`/`mypy`/`pytest` (`pip install ruff`), so a new ruff release can break all Python CI at once; the three workflow files are near-identical copy-paste.

**Remediation: S/M** — one shared ruff/mypy/pytest config convention (or a root `ruff.toml`), delete black and `pytest.ini`/pyproject duplication, pin CI tool versions, consider a reusable workflow.

## Finding 6 — evaluation-job's local `make docker-build` is broken and self-contradictory

**Impact: MEDIUM** — the documented local prod-image path cannot work; only CI (with repo-root context) builds this image.

- `evaluation-job/Dockerfile:34-40` COPYs `libs/amp-evaluation/...` — requires the **repo root** as build context (and `.github/release-config.json` correctly sets `"context": "."` for `amp-evaluation-monitor`).
- But `evaluation-job/Makefile` `docker-build` runs `docker build ... -f Dockerfile .` from `evaluation-job/` (context = evaluation-job dir → COPY fails), passes `--build-arg AMP_EVALUATION_VERSION` that **no Dockerfile declares** (no `ARG` anywhere), and its help text claims "installs amp-evaluation from PyPI" while the Dockerfile comment says "temporary until amp-evaluation is published to PyPI".
- `Dockerfile` and `Dockerfile.dev` are near-identical duplicates (same base, same COPYs at lines 21-41 in each).

**Remediation: S** — fix Makefile context to `..`, drop the phantom build-arg, collapse the two Dockerfiles or make prod actually install the released PyPI package.

## Finding 7 — GenAI span-attribute vocabulary is re-hardcoded in Go and Python with only partial drift protection

**Impact: MEDIUM** — the `gen_ai.*`/`traceloop.*` names are the platform's core data contract; two of three copies have no guard.

- `traces-observer-service/opensearch/process.go` inlines dozens of `gen_ai.*`, `traceloop.entity.*`, `crewai.*` literals with no named constants (e.g. lines 170-176, 239-251, 294-315, 560-574).
- `test/instrumentation-matrix/harness/classify.py` independently hardcodes the same vocabulary (`traceloop.span.kind` line 37, `gen_ai.system` 67, `gen_ai.usage.input_tokens` 72, tool event names 88, 95).
- Mitigation exists but covers only one edge: `cmd/gen-contract/contract.go` generates the matrix's JSON-schema contracts and PR CI runs `make check-contract-drift` — but `classify.py`'s literals and `process.go`'s inline strings are outside that check.

**Remediation: M** — centralize attribute keys as constants in one Go file (feeding gen-contract) and have classify.py consume the generated contract instead of its own literals.

## Finding 8 — Release workflows permanently mutate the documented `0.0.0-dev` version placeholder on main

**Impact: MEDIUM** — the release process contradicts its own runbook; first release after branch protection tightens will fail or leave main inconsistent.

- `python-instrumentation-provider/RELEASING.md` states the repo value is the placeholder `0.0.0-dev` ("Leave `version = "0.0.0-dev"` alone") and both pyprojects currently hold `0.0.0-dev`.
- But both `amp_evaluation_release.yaml:88-104` and `amp_instrumentation_release.yaml:79-` `sed` the real version into `pyproject.toml` and `git push origin HEAD:refs/heads/$RELEASE_BRANCH` with **no revert step** — after one release, main carries the released version, breaking the placeholder invariant (and direct pushes to a protected main will be rejected).
- Minor: `amp_instrumentation_release.yaml:82` interpolates `${{ inputs.target_version }}` directly into the shell script (workflow-input injection pattern), while the evaluation workflow uses an env var.

**Remediation: S** — set the version at build time only (e.g. `hatch-vcs`/`SETUPTOOLS_SCM` from the tag, or sed without committing), aligning both workflows.

## Finding 9 — evaluation-job executes tenant-supplied code in-process alongside platform credentials

**Impact: MEDIUM** — deliberate and commented, but the blast radius is unstated: user code shares a process with IdP client secrets and the gateway LLM key.

- `evaluation-job/main.py:392` `exec(source, namespace)  # noqa: S102` for custom code evaluators; `main.py:436` `eval(f'f"""{safe}"""', {"__builtins__": __builtins__}, variables)` for prompt templates — full builtins exposed, and the triple-quote escaping at line 435 is the only sanitization.
- The same process holds `IDP_CLIENT_SECRET` and `LLM_API_KEY` in `os.environ` (main.py:506-518), all reachable from evaluator code via `os.environ`.
- The K8s Job boundary is the real sandbox, but nothing scrubs env vars before `exec`, and the template `eval` path is reachable from a *lower*-trust input (a prompt template) than "arbitrary code evaluator".

**Remediation: M** — document the trust model in AGENTS.md/README explicitly, scrub secrets from `os.environ` before executing user code (tokens can be fetched first), and replace f-string `eval` with `string.Formatter`-based substitution which needs no code execution.

## Finding 10 — Dependency pins and repo metadata duplicated/stale across amp-evaluation and evaluation-job

**Impact: LOW** — lockstep-bump hazards and cosmetic staleness, cheap to fix.

- `any-llm-sdk[...]==1.16.0` (+ the identical aiohttp comment) is pinned in **three** places: `libs/amp-evaluation/pyproject.toml` (`any-llm` extra *and* `dev` extra) and `evaluation-job/requirements.txt:2-4` — instead of evaluation-job depending on `amp-evaluation[any-llm]`.
- Stale repo URLs: `libs/amp-evaluation/pyproject.toml` `Repository = "https://github.com/wso2/ai-agent-management-platform"` and both `evaluation-job/Dockerfile*` `org.opencontainers.image.source` labels point to the old repo name; canonical is `wso2/agent-manager` (which `libs/amp-instrumentation/pyproject.toml` gets right).
- Stale module docstring: `evaluation-job/main.py:26-34` shows flags `--agent-id`/`--environment-id` that no longer exist (actual: `--agent`, `--environment`, plus required `--organization`/`--project`/`--monitor-id`/`--run-id`/`--publisher-endpoint`).
- Go patch-pin skew: `traces-observer-service/go.mod` `go 1.25.0` vs `agent-manager-service` `go 1.25.7`.

**Remediation: S**.

## Finding 11 — Instrumentation matrix is healthy, but its concessions were never revalidated against traceloop 0.62.1

**Impact: LOW** — the suite is the opposite of drifting (this is the best-maintained area audited), but its findings log now lags its own matrix.

- Healthy: `matrix.yaml:5-16` pins exactly the two traceloop versions in `.github/release-config.json:32-38` (0.61.0→0.3.0, 0.62.1→0.4.0), matching `python-instrumentation-provider/locks/` (8 lock files); three CI workflows (`instrumentation-matrix-{pr,nightly,manual}.yaml`) with a gating default-cell job, contract-drift check, and cassette secret scan; last commit 2026-07-08.
- Lag: every `F-NNN` revalidation stamp in `FINDINGS.md` is 2026-05-27/06-02 against **0.61.0**; 0.62.1 was added afterwards (`a800e8de`) and F-001/F-004/F-006 concessions were never re-confirmed against it. The heavy tier (`heavy/`, ~47 KB) only runs nightly on ubuntu-latest, making it the likeliest silent-rot candidate.

**Remediation: S** — one revalidation pass of active findings against 0.62.1; add a RUNBOOK step tying "add traceloop version" to "re-stamp findings".

## Finding 12 — Positive baseline worth preserving (non-findings)

For calibration, several things this audit expected to flag are actually in good shape: **no OpenSearch injection surface exists** (Finding 3); zero TODO/FIXME across all audited Go and Python sources; test volume is strong where it matters (amp-evaluation ~11.6k src / ~11.7k test LOC across 20 test files; evaluation-job 768 src / 1,293 test LOC; TOS `opensearch` pkg ~2.3k test LOC); the provider's per-(traceloop x python) lock model with hermetic `check-locks` CI and the thorough `RELEASING.md` runbook are exemplary; per-package PR checks test Python 3.10-3.13; and the console's evaluator editor schema is generated from SDK types (`libs/amp-evaluation/src/amp_evaluation/codegen/evaluator_schema.py`, consumed by `console/workspaces/pages/eval/scripts/generate-evaluator-models.sh`) rather than duplicated.

---

**Suggested priority order:** 2 (error handling + untested auth) → 1 (shared Go module) → 4 (single bootstrap) → 6/8 (broken/contradictory release paths, S-sized) → 5/7 → the rest.
