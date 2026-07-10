# Technical-Debt Audit: Cross-Cutting Infrastructure

**Date:** 2026-07-11 (audited at commit `949344d9`)
**Scope:** `.github/workflows` (27 files), Makefiles, `deployments/` (helm-charts, setup, vm, quick-start), `test/e2e`, `scripts/`, `documentation/`, root-level dev experience and repo hygiene.

**What's healthy (for calibration):** Helm chart CI exists and is decent (`.github/workflows/helm-charts-pr-check.yml`: `helm lint --strict` + `helm template` + kubeconform, with change detection). The e2e suite has zero skip markers and no sleeps in specs (uses `Eventually`). PR path filters are correct and all include self-reference. Docs versioning process is documented. The findings below are against that baseline.

---

## 1. The 11k-LOC e2e suite never runs on PRs

**Impact: HIGH** — regressions in agent-manager-service/console/charts surface up to 24h later in the nightly, after merge, making bisection and revert expensive.

- `test/e2e` is 106 Go files / ~11,028 LOC (Ginkgo v2). `.github/workflows/e2e.yaml` triggers on `workflow_dispatch` only. The only automatic runs are `nightly.yml` (cron `0 0 * * *`) and `release.yml`. No PR-triggered subset, no merge-queue gate, no smoke tier. Meanwhile PR CI for the service is unit-level only (`agent-manager-service-pr-checks.yaml`).

**Remediation: M** — carve a tagged smoke subset (Ginkgo labels) that runs on PRs touching `agent-manager-service/**` or `deployments/helm-charts/**` against the k3d quick-start; keep full suite nightly.

## 2. The e2e job is copy-pasted into 3 workflows and has already drifted

**Impact: MEDIUM** — three ~110-130-line copies of the same k3d+ginkgo job; fixes land in one and not the others.

- `e2e.yaml` (108 lines), `nightly.yml:249-380`, `release.yml:444-540` all run the identical `go run .../ginkgo -v -p --timeout 45m ... ./tests/...`. Diffing the nightly vs release blocks shows real drift: release uses unpinned `actions/checkout@v6` where nightly uses SHA-pinned `@de0fac2e... # v6.0.2`, and `K3D_VERSION: v5.8.3`/`YQ_VERSION: v4.45.4` + SHA256s are re-declared in each file (job-level env in release, workflow-level in e2e.yaml/nightly).

**Remediation: M** — convert to a single reusable `workflow_call` workflow with `chart_version`/`image_tag` inputs; nightly, release, and manual dispatch all call it.

## 3. Action pinning policy is split-brain (tag pins vs SHA pins)

**Impact: MEDIUM** — supply-chain posture is inconsistent; the SHA-pinning effort is ~10% done, giving neither auditability nor simplicity.

- `actions/checkout@v6` x67 vs SHA-pinned `# v6.0.2` x7; `actions/upload-artifact@v7` x19 vs SHA-pinned x7. Third-party actions are mixed too: `dorny/test-reporter`, `peter-evans/create-pull-request`, `dataaxiom/ghcr-cleanup-action` are SHA-pinned while `softprops/action-gh-release@v2`, `docker/*@v3/v6`, `azure/setup-helm@v4` are tag-pinned.

**Remediation: S** — pick one policy (SHA + comment is the wso2 norm), run `pinact`/`frizbee` once, add a Dependabot `github-actions` ecosystem entry to keep pins fresh.

## 4. Shared `setup-go` composite hardcodes agent-manager-service's go.mod — and other modules use it

**Impact: MEDIUM** — traces-observer-service and e2e CI build with the wrong module's toolchain and cache key; cache misses every run and version skew is invisible.

- `.github/actions/setup-go/action.yaml` hardcodes `go-version-file: agent-manager-service/go.mod` and `cache-dependency-path: agent-manager-service/go.sum`. It is used 11x including 3x in `traces-observer-service-pr-checks.yaml` and in the e2e jobs (`test/e2e` is its own module). `traces-observer-service/go.mod` declares `go 1.25.0` vs `1.25.7` in the other two modules — CI silently papers over this.

**Remediation: S** — add `module-dir` input to the composite (default `agent-manager-service`), pass it at call sites; align the three `go.mod` versions.

## 5. Three Go modules, three different lint regimes

**Impact: MEDIUM** — the strict ruleset (`goheader`, `exhaustruct`, `errorlint` per the repo's own skill docs) only protects one of three services; code quality diverges and engineers moving between modules get inconsistent CI feedback.

- Only one golangci config exists in the repo: `agent-manager-service/.github/linters/.golangci.yaml` (used via `golangci-lint-action@v9 --config ...`). `traces-observer-service-pr-checks.yaml` runs `golangci-lint-action@v9` with **default rules** (no config file exists in that module). `amctl-pr-checks.yaml` doesn't use golangci at all — `gofmt` + `staticcheck@2026.1` + `go vet`. `test/e2e` has no lint at all. `agent-manager-service/AGENTS.md` itself warns `make lint` differs from CI.

**Remediation: M** — hoist one `.golangci.yaml` to repo root (or per-module symlinked), adopt it in cli and traces-observer CI, drop the bespoke staticcheck steps.

## 6. Console CI never runs its tests, and the Node version is contradicted three ways

**Impact: HIGH** — 10 existing test files are dead weight (never executed anywhere in CI), and a new contributor following `.nvmrc` gets a Node version Rush rejects.

- `amp-console-package-pr-checks.yaml` has exactly two jobs: `rush lint` and `rush build` — no `rush test`, yet `console/workspaces/pages/*/src/*.test.tsx` (10 files) exist. Versions: CI pins `node-version: "20.19.0"`, `console/.nvmrc` says `22.12.0`, `console/rush.json:45` allows `>=18.20.3 <19.0.0 || >=20.14.0 <21.0.0` — the `.nvmrc` version is outside Rush's supported range. Docs workflows use a fourth spelling (`node-version: 20`).

**Remediation: S** — add a `rush test` job (or wire tests into build), fix `.nvmrc` to a version inside `nodeSupportedVersionRange`, and read Node from `.nvmrc` in CI (`node-version-file`).

## 7. Twin Python workflows are 99% copy-paste, and Python version targets disagree with the code

**Impact: MEDIUM** — 4 files (~516 lines) maintained as 2 near-identical pairs; version-bump/step fixes must be applied 2-4x; lint targets a Python the packages don't support.

- `amp-evaluation-package-pr-checks.yaml` vs `amp-instrumentation-package-pr-checks.yaml` (129 lines each) differ **only** in name/paths (verified diff). Same for the two release workflows (73 diff lines, all mechanical). Version scatter: lint jobs use `3.11`, build/release jobs use `3.10`, matrix is 3.10-3.13, but `libs/amp-evaluation/pyproject.toml` sets black/ruff `target-version` to **py39** while `requires-python = ">=3.10"`; `libs/amp-instrumentation` has no ruff/black config at all; `evaluation-job` targets py311.

**Remediation: M** — one reusable `python-package-checks.yaml` (`workflow_call` with `package-dir` input); align `target-version` with `requires-python` and give amp-instrumentation a lint config.

## 8. 83.5 MB SQL dump and ~171 MB of duplicated doc images checked into the working tree

**Impact: HIGH** — clone/checkout weight and CI checkout time for every workflow (27 of them) pay for sample data and image copies; this grows monotonically with each doc version cut.

- `samples/customer-support-agent/db_backup.sql` = 83,530,791 bytes (added in PR #21, referenced by the sample README). `documentation/versioned_docs/` = 171 MB across 9 snapshots (v0.11.x-v0.18.x + cloud), each carrying a full copy of `img/evaluation/*.png` — e.g. `custom-eval-list.png` (2.9 MB) exists **10x**. Also `libs/amp-evaluation/tests/fixtures/sample_traces.json` and `libs/amp-evaluation/samples/data/sample_traces.json` are identical 3.8 MB copies. `git count-objects`: 62.9 MiB pack.

**Remediation: M** — move the dump to a release asset/LFS with a download step in the sample README; move shared doc images to Docusaurus `static/img/` (not copied per version); dedupe the JSON fixture. Note history rewrite is optional — stopping the growth is the win.

## 9. `docs/` has silently diverged from its own "latest" published version

**Impact: MEDIUM** — users on the default (latest) doc version read stale content for half the pages; the contributing guide exists in two hand-maintained copies.

- 17 of 34 files differ between `documentation/docs/` and `documentation/versioned_docs/version-v0.18.x/` (`getting-started/on-your-environment.mdx` alone: 91 changed lines), yet `docs/_constants.md` still declares `latestVersion: 'v0.18.x'` — the edits are published nowhere until the next cut. `documentation/docs/contributing/contributing.mdx` is a verbatim copy of root `CONTRIBUTING.md` (87 identical lines). Also: the known local docker0/UFW/k3d networking failure mode appears **nowhere** in docs (only cloud-VM firewall notes in `on-a-vm.mdx`).

**Remediation: S** — add a scheduled or PR check that flags `docs/` vs latest-version drift older than N days; make `contributing.mdx` import/include the root file; add a local-networking troubleshooting section to `on-k3d.mdx`.

## 10. Three competing "run it locally" paths with contradictory ports, and none in README/CONTRIBUTING

**Impact: HIGH** — onboarding requires oral tradition; the three paths disagree on mechanism (compose vs quick-start container vs raw Helm) and console URL (`localhost:3000` vs `console.amp.localhost:8080`).

- `README.md` has zero local-dev instructions (links hosted quick start only); `CONTRIBUTING.md` is purely issue-process. `AGENTS.md:52-64` says `make setup` (Colima+k3d) then `make dev-up` (compose), console at `localhost:3000`. `documentation/docs/getting-started/quick-start.mdx` says run the `amp-quick-start` container → `install.sh`, console at `console.amp.localhost:8080`. `deployments/setup/setup-openchoreo.sh` and `deployments/quick-start/install.sh` are **parallel installers of the same stack** with independently duplicated version pins (`OPENCHOREO_VERSION="1.1.1"`, `GATEWAY_OPERATOR_VERSION="0.7.0"`, observability chart versions — defined in both `deployments/setup/env.sh` and `quick-start/install.sh`). `make setup` runs Colima (macOS-centric) with no Linux branch documented.

**Remediation: M** — pick one canonical contributor path, put a 10-line pointer in README/CONTRIBUTING, extract shared version pins to a single `versions.env` sourced by both installers, document the Linux (native docker) path.

## 11. Helm values hygiene: duplicated config blobs and unmarked cleartext dev secrets

**Impact: MEDIUM** — copy-paste values drift breaks environments at upgrade time; unmarked default secrets invite production reuse.

- The Thunder token URL `http://amp-thunder-extension-service.amp-thunder.svc.cluster.local:8090/oauth2/token` is hardcoded in 4 charts' values.yaml (`wso2-agent-manager:215`, evaluation-extension:50, observability-extension:32, gateway-extension:27). A ~104-entry `oauthScopesSupported` scope list is maintained **twice in the same file** (`wso2-agent-manager/values.yaml:208` comma-separated, `:363` space-separated). Cleartext dev credentials sit in committed values (`password: "agentmanager"` :21, OpenBao `token: "root"` :250, client secrets at `wso2-amp-thunder-extension/values.yaml:298,310,322,337,349,416`) with a LOCAL-DEVELOPMENT-ONLY warning on only one of them. Buildpacks images use `:latest` (`wso2-amp-platform-resources-extension/values.yaml:80-83`). Subchart `thunder/Chart.yaml` says 0.21.0 while the parent pins image tag 0.45.0.

**Remediation: M** — promote shared endpoints/scopes to `global.*` consumed via helpers; generate the scope list once; add "dev-only, must override" comments + a template `required`/warning for secret defaults; pin the buildpacks tags.

## 12. Release machinery is 6 disconnected manual workflows plus duplicated codegen/tool pins

**Impact: MEDIUM** — every release is a multi-dispatch human choreography (platform, docs, core-ui npm, 2 Python packages, instrumentation images) with no shared versioning; tool-version pins drift between Makefiles.

- Manual `workflow_dispatch` release workflows: `release.yml` (630 lines, lockstep `target_version` for service+console images, amctl, charts — charts sit at placeholder `version: 0.0.0-dev` until release-time rewrite), `docs-release.yml`, `publish-core-ui-npm.yml`, `amp_evaluation_release.yaml`, `amp_instrumentation_release.yaml`, `python_instrumentation_image_release.yaml` — each takes its own free-text version input with no cross-check. Supporting drift: root `Makefile` pins `oapi-codegen v2.6.0` while `agent-manager-service/Makefile` pins `v2.4.1`; target-name inconsistency across Makefiles (`test-coverage` vs `test-cover`; `console/Makefile` has no `lint`/`test` target at all); no `.editorconfig` anywhere.

**Remediation: L** — a release "conductor" doc or meta-workflow that derives component versions from one input and dispatches the rest; S-sized quick wins inside it: unify the oapi-codegen pin and standardize Makefile target names (`lint`, `test`, `fmt` everywhere).

---

**Suggested priority order (cost/benefit):** #6 and #4 (S-sized, immediate correctness), #3 (S, mechanical), #8 (stops monotonic growth), #1/#2 together (one reusable e2e workflow + PR smoke tier), then #10 (onboarding), with #5, #7, #9, #11, #12 as backlog.
