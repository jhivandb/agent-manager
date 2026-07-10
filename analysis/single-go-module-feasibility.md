# Feasibility: One Go Module for agent-manager-service + traces-observer-service + cli

**Date:** 2026-07-11 (evaluated at commit `949344d9`)
**Method:** empirical trial merge (build + vet + test of all three trees under a single root `go.mod`) plus a full audit of every build/CI/release touchpoint that assumes the current per-directory module layout.

## Verdict

**Feasible, low-risk, and cheaper than it looks — recommended.** A trial merge compiled the entire codebase with **zero changes to any `.go` file**: `go mod tidy`, `go build ./...`, and `go vet ./...` all pass under a single root module. Unit tests fail only in the exact same packages, for the exact same reason (missing `.env`/DB environment), as they do in the unmerged `agent-manager-service` today — the merge introduces no test regressions. Estimated effort: **1–2 days** for the merge PR including CI and Docker changes.

## Why this merge is unusually cheap

1. **Zero import churn.** The current module paths are `github.com/wso2/agent-manager/agent-manager-service`, `.../cli`, and `.../traces-observer-service` — i.e. the repo path plus the directory name. If the merged module is named `github.com/wso2/agent-manager` at the repo root and the directories keep their names, **every one of the ~1,560 existing import statements stays byte-for-byte identical**. This also means the non-import hardcodes survive untouched: `scripts/build-amctl.sh:10` (`LDFLAGS_PKG`), the AMS Dockerfile's `-X .../config.Version` ldflag, and the import path emitted by `agent-manager-service/scripts/generate-builtin-evaluators.sh:341`.
2. **No dependency conflicts.** The three go.mods share only compatible deps: `golang-jwt/jwt` is already identical (v5.3.1), and the only skews are minor-version (`oapi-codegen/runtime` 1.1.2 vs 1.4.0, `x/oauth2` 0.35 vs 0.36) which MVS resolves upward automatically. Verified: the merged module resolved `oapi-codegen/runtime v1.4.2` and AMS's v2.4.1-generated clients still compile and vet cleanly.
3. **Releases are already mono-versioned.** All release tags are `amp/vX.Y.Z` — there is no per-component version stream to reconcile. amctl is distributed via release archives + `scripts/install-amctl.sh`, not `go install`, so per-module semver tags (`cli/vX.Y.Z`) were never needed.
4. **`test/e2e` is fully independent.** It imports none of the three modules and has no `replace` directives. Leave it as a nested module (zero work, keeps ginkgo out of the main go.sum, and — usefully — a nested module is excluded from the root module's zip).
5. **No `go.work`, no vendoring (tracked), no cross-module `replace` directives** anywhere to unwind.

## What actually has to change (the work items)

| Item | What | Size |
|---|---|---|
| Root `go.mod`/`go.sum` | `module github.com/wso2/agent-manager`, go 1.25.7; delete the three per-dir go.mod/go.sum | S |
| Dockerfiles ×2 (+`.dev` variants) | Both assume `context = module dir` with `COPY go.mod go.sum ./` caching layers. Build context must become the repo root: update `agent-manager-service/Dockerfile`, `traces-observer-service/Dockerfile`, the `context`/`file` fields in the PR-check workflows, and `.github/release-config.json:5-28`. **Add a `.dockerignore`** — the repo is ~136 MB tracked (console 21 MB, documentation, samples 81 MB incl. the 83 MB SQL dump), and without one every image build uploads all of it as context. | M — the biggest single item |
| `.github/actions/setup-go` | Currently hardcodes `agent-manager-service/go.mod`/`go.sum` **while being used by traces-observer, e2e, nightly, and release jobs** — a known bug (tech-debt theme 7). A single root go.mod makes this action trivially correct with no `module-dir` input needed. The merge *fixes* this item for free. | S |
| Workflow path filters | `agent-manager-service/**`, `cli/**`, `traces-observer-service/**` filters keep working (directories don't move), but each Go workflow must also trigger on root `go.mod`/`go.sum`, since a dep bump now affects all three components. `working-directory:` settings keep working — building/testing a subdirectory of a module from within that subdirectory is fine. | S |
| oapi-codegen unification | One module ⇒ one `oapi-codegen/runtime` ⇒ one codegen CLI version. Today: root Makefile + `cli-codegen-check.yaml` pin **v2.6.0**, `agent-manager-service/Makefile:75` pins **v2.4.1**. Pin one version (v2.6.0, or better: a Go 1.24+ `tool` directive in the root go.mod so it can never skew again) and regenerate the AMS clients once — the regen diff is mechanical. | S/M |
| oapi-codegen config output paths | Two conventions today: CLI configs use repo-root-relative outputs (run from root), AMS configs use module-relative (run from module dir). Both keep working if each keeps being invoked from its current directory; just don't "helpfully" normalize invocation directories in the same PR. | – |
| Lint decision | AMS has the only `.golangci.yaml` (strict); traces-observer lints with golangci defaults; CLI uses staticcheck. Merging doesn't force unification (each workflow can keep its own `working-directory` + config), but this is the natural moment to hoist one root `.golangci.yaml` — already a theme-7 recommendation. If the strict AMS ruleset is applied to cli/ and traces-observer-service/ expect a one-time cleanup; use per-path excludes to stage it. | S–M (policy choice) |
| Root/module Makefiles | Targets that `cd` into module dirs (`cd cli && go test ./...`, wire, gen-keys, etc.) all keep working inside a single module. `go mod tidy` steps in `codegenfmt-check` now operate on the root go.mod — update paths in that target. | S |
| Local dev hygiene | Stale local `vendor/` dirs (untracked — present in at least one working copy) will make the root module error with "inconsistent vendoring"; note it in the migration PR description. | – |

## Risks and trade-offs

1. **Loss of the compile-time coupling firewall.** There are **no `internal/` directories anywhere** — today the module boundaries are the *only* thing stopping `cli` from importing gorm-laden AMS service code. In one module, everything is importable by everything. Mitigations, in increasing strength: (a) a `depguard` rule in golangci (`cli/**` may not import `agent-manager-service/**` except an allowlisted contracts package, etc.) — cheap and immediate; (b) move service code under `agent-manager-service/internal/` over time (this *does* churn imports, so do it as a follow-up, not in the merge PR). Note this "risk" is also the point of the merge — the sharing just needs to flow through deliberate packages (a shared `spec`/contracts package) rather than ad-hoc imports.
2. **Blast radius of a dependency bump.** One go.sum means any dep update re-triggers all Go CI and (with root-context Docker builds) invalidates the `go mod download` layer for both images. In exchange, the three components can never again disagree about a security patch level. Net win, but CI minutes go up slightly.
3. **Module zip weight if anyone `go install`s.** A root module's zip includes everything tracked in the repo except nested modules — ~130 MB today (samples dump + doc images). `go install github.com/wso2/agent-manager/cli/cmd/amctl@amp/v0.18.0` would work in principle but download all of it (and inject no version ldflags — it already yields `Version=dev` today, and no docs advertise it). If `go install` ever becomes a supported path, the fix is the already-recommended repo-weight cleanup (analysis/README theme 9), not a different module layout.
4. **`go test ./...` at the root runs all three suites.** CI should keep scoping (`go test ./agent-manager-service/...`) inside the existing per-component workflows; only the codegen/dep-bump paths need whole-module runs.

## What the merge buys (ties to the tech-debt reports)

- **Theme 3 (duplicated wire contracts) gets a real fix mechanism.** traces-observer-service currently hand-mirrors AMS's `spec/` models field-for-field, and the CLI hand-writes a ~130-line traces-observer client. In one module, shared contract types live in one package imported by all three, and the CLI's traceobssvc client can be generated against `traces-observer-service/docs/openapi.yaml` with types shared where appropriate — no version-tagging ceremony required.
- **Theme 7 (toolchain skew) largely evaporates for Go:** one `go` directive (1.25.0 vs 1.25.7 today), one dep set, the broken shared `setup-go` action becomes correct, and the natural moment to hoist one golangci config.
- One `dependency-validation.yml` target instead of three; Dependabot/audit surface shrinks to one go.mod.

## Alternatives considered and rejected

- **`go.work` workspace:** only helps local development; CI and external builds still can't import across modules without publishing per-module version tags or `replace` directives (which break `go install` and are toxic in a published module). Doesn't fix theme 3.
- **A fourth shared "contracts" module:** requires either per-module semver tags (a tagging discipline the repo deliberately doesn't have — everything is `amp/v*`) or commit pseudo-versions, which create a land-then-bump two-PR dance for every contract change. Strictly more ceremony than a single module for a repo that releases everything from main together.

## Suggested migration sequence (one PR, reviewable in pieces)

1. Root `go.mod` (module `github.com/wso2/agent-manager`, go 1.25.7) + `go mod tidy`; delete the three go.mod/go.sum pairs. Leave `test/e2e` as-is.
2. `.dockerignore` at root; rework both Dockerfiles (+`.dev`) to root context; update workflow `context:`/`file:` fields and `release-config.json`.
3. Simplify `.github/actions/setup-go` to root `go.mod`/`go.sum`; add root go.mod/go.sum to the three PR-check path filters; point `cli-codegen-check.yaml`'s inline setup-go at the root go.mod.
4. Pin oapi-codegen once (prefer a `tool` directive) and regenerate the AMS clients; commit the mechanical diff separately.
5. Add a depguard (or similar) rule preventing cross-component imports outside designated shared packages.
6. Follow-ups (separate PRs): shared contracts package to kill the TOS model mirror and the CLI's hand-written traceobssvc client; optional `internal/` moves; golangci unification.

## Evidence

Trial merge artifacts (tracked files only, via `git archive HEAD`): single root `go.mod` with module path `github.com/wso2/agent-manager`; `go mod tidy` clean; `go build ./...` exit 0; `go vet ./...` exit 0 (vet type-checks test files, so all tests compile); `go test ./...` fails only in the ten AMS packages whose `TestMain`/init requires `DB_HOST`/`DB_USER`/`OPEN_CHOREO_BASE_URL` env config — byte-identical failure mode to running the unmerged module without `make test`'s env setup. Resolved dep highlights: `oapi-codegen/runtime v1.4.2`, `x/oauth2 v0.36.0`, `jwt/v5 v5.3.1`.
