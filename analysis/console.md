# Console Tech-Debt Audit — `console/`

**Date:** 2026-07-11 (audited at commit `949344d9`)
**Context:** Rush 5.157 / pnpm 9.12 monorepo, 28 projects (`rush.json`): 1 app (`apps/web-ui`), 1 aggregator (`workspaces/core-ui`), 7 libs, 20 page packages. ~573 TS/TSX source files, ~101k lines. Paths relative to `console/` unless noted.

**What's healthy (worth saying first):** styling is uniformly `sx`-based Oxygen/MUI (1,062 `sx={}` uses, zero `styled()`, only 4 CSS files, all in web-ui for swagger theming); state management is clean (no redux/zustand, 5 small contexts, TanStack Query for server state); type safety is genuinely good (26 `any` total, **zero** `ts-ignore`/`ts-expect-error`); TODO density is nearly zero (1 hit); heavy deps (swagger-ui-react, monaco) are lazy-loaded (`workspaces/libs/shared-component/src/components/SwaggerSpecViewer.tsx:21`, `workspaces/core-ui/src/pages/index.tsx` uses `lazy()` throughout). The findings below are the real debt.

---

## 1. Test suite is dead: 10 test files for 573 sources, most assert scaffold text that no longer exists, and CI never runs them

**Impact: HIGH** — zero regression safety net on a 101k-line console; the tests that exist would fail if anyone ran them, which trains developers to ignore testing entirely.

- 10 test files vs 573 source files; 8 of the 10 are identical ~96-line scaffolds (`workspaces/pages/{overview,deploy,build,test,traces,metrics,logs,add-new-agent}/src/*.test.tsx`).
- They assert placeholder strings from the original page template, e.g. `workspaces/pages/overview/src/Overview.test.tsx:29` expects `'Overview - Component Level'` — that string now exists only in `Overview.stories.tsx:51`; `Overview.Component.tsx` renders none of it and no longer takes `title`/`description` props. Same for `Deploy.test.tsx:29` (`'Deploy - Component Level'`).
- CI (`.github/workflows/amp-console-package-pr-checks.yaml`) has only `lint` and `build` jobs — no test job, so the broken tests rot silently.
- Meanwhile 17 `vitest.config.ts` + 17 `setupTests.tsx` files are carried by packages with no tests at all.

**Remediation: M** — delete the 8 stale scaffolds, add `rush test` to the PR workflow, and seed real tests in the 2-3 highest-churn packages (eval, llm-providers, deploy). The vitest plumbing already exists everywhere.

## 2. Toolchain version skew: 3 Vite majors, 2 TypeScript majors, 2 ESLint majors — and Rush consistency enforcement is switched off

**Impact: HIGH** — the app builds with a different compiler/bundler than every library it consumes; TS 6 vs 5.9 behavior differences and Vite 6 vs 8 plugin incompatibilities will surface as "works in the lib, breaks in the app" bugs, and upgrades must be done 28 times.

- `apps/web-ui/package.json`: `typescript ~6.0.2`, `vite ^8.0.10`, `eslint ^10.2.1`; all 20 page packages: `typescript 5.9.3`, `vite 6.3.5`, `eslint 9.36.0`; `workspaces/core-ui/package.json:94`: `vite 7.1.7`. Lockfile confirms all three Vite majors resolved (`common/config/rush/pnpm-lock.yaml`: vite@6.3.5, 7.1.7, 8.1.3) and both TS (5.9.3, 6.0.3).
- `common/config/rush/common-versions.json:52`: `// "ensureConsistentVersions": true,` — commented out, so Rush enforces nothing.
- `@babel/runtime-corejs3` pinned at `7.11.2` (August 2020) in every page package.
- web-ui also bypasses the shared `@agent-management-platform/eslint-config` and rolls its own flat config with different plugin majors (`apps/web-ui/eslint.config.js`).

**Remediation: M** — align on one TS/Vite/ESLint version via `preferredVersions` + enable `ensureConsistentVersions`; mostly mechanical, one painful decision (TS 6 or 5.9 everywhere).

## 3. Ghost packages in the workspace tree

**Impact: MEDIUM** — onboarding trap: two packages named "core-ui" where one is a corpse, plus a deleted page leaving debris.

- `workspaces/libs/core-ui/` contains a tracked `package.json` declaring `@agent-management-platform/core-ui` with a full dependency list (near-clone of the real aggregator `@agent-management-platform/am-core-ui` at `workspaces/core-ui`), but has **no src, no other tracked files, and is absent from `rush.json`**.
- `workspaces/pages/profile-settings/` exists on disk containing only `node_modules/` and `.rush/temp/` — the package was removed from `rush.json` but the directory was never cleaned.

**Remediation: S** — `git rm workspaces/libs/core-ui/package.json`, delete the profile-settings leftovers.

## 4. God components: 39 non-generated files over 500 lines, five over 1,000

**Impact: MEDIUM** — velocity and review cost; these are the highest-churn feature areas (eval, llm-providers, deploy) so every change wades through 1,000+ line files mixing data fetching, form state, and rendering.

- `workspaces/pages/eval/src/subComponents/EvaluatorForm.tsx` — 1,777 lines, 7 useState + 7 useEffect
- `workspaces/pages/eval/src/ViewEvaluator.Organization.tsx` — 1,156 lines (also an `any` hotspot: 5)
- `workspaces/pages/configure-agent/src/AddLLMProvider.Component.tsx` — 1,118 lines
- `workspaces/pages/deploy/src/subComponent/DeployCard.tsx` — 1,103 lines
- `workspaces/pages/mcp-proxies/src/subComponents/MCPProxyRewriteTab.tsx` — 1,030 lines
- 39 files >500 lines total (excluding `eval/src/generated/evaluator-models.generated.ts`).

**Remediation: L** — don't big-bang; adopt a "no new code in >800-line files" rule and split opportunistically, starting with EvaluatorForm (its tab/section boundaries are natural seams).

## 5. Copy-paste duplication across page packages instead of promotion to shared libs

**Impact: MEDIUM** — fixes land in one copy and not the other; the monorepo has the right shared-libs structure but pages clone instead of promoting.

- `workspaces/pages/llm-providers/src/hooks/useValidatedForm.ts` and `workspaces/pages/eval/src/hooks/useValidatedForm.ts` — identical except formatting (diff shows only line-wrap differences); both wrap `useFormValidation` from views, so the wrapper belongs next to it.
- `CreateGitSecretModal.tsx` near-duplicated: `workspaces/pages/build/src/components/` (189 lines) vs `workspaces/pages/add-new-agent/src/components/` (193 lines).
- `CreateButtons.tsx` triplicated: gateways (66), add-new-project (68), add-new-agent (69 lines).
- Two parallel implementations of the same widgets in the two shared libs: `views/src/component/EnvVariableEditor/` (230 lines, 3 consumers) vs `shared-component/src/components/EnvironmentVariable.tsx` (437 lines, 10 consumers); same story for `FileMountEditor` (282) vs `FileMountSection.tsx` (154).

**Remediation: M** — promote the wrappers/modals to views or shared-component; pick one env-var/file-mount editor and migrate the 3 minority consumers.

## 6. API layer: strong two-file convention, but query keys are string literals with no factory, and keys leak into page code

**Impact: MEDIUM** — cache invalidation is correctness-critical; hand-typed keys duplicated across packages will silently stop invalidating when one side is renamed.

- The convention itself is well-followed: 26 matching `apis/` + `hooks/` pairs in `workspaces/libs/api-client/src/`, and hooks uniformly use the `useApiQuery`/`useApiMutation` wrapper (`hooks/react-query-notifications.ts`) — only that wrapper file touches raw `useQuery`.
- But keys are inline strings with mixed conventions: `["agentBuildOptions", ...]` (camelCase, `hooks/agent-build-options.ts:33`) vs `["agent-builds"]` (kebab, same file :49) vs `QUERY_KEY` constants in `hooks/catalog.ts:43`; double vs single quotes across files.
- Keys duplicated in page code: `workspaces/pages/eval/src/ViewMonitor.Component.tsx:333-335` calls `queryClient.invalidateQueries({ queryKey: ["monitor-runs"] })` / `["monitor-scores-timeseries-batch"]`, re-typing strings defined in `api-client/src/hooks/monitors.ts:179,271`.

**Remediation: S/M** — export per-resource `queryKeys` factories from api-client and forbid string-literal keys outside it (an ESLint `no-restricted-syntax` rule can enforce this).

## 7. Direct fetch() outside the api-client in three page packages

**Impact: LOW-MEDIUM** — these bypass auth-token handling, snackbar error reporting, and caching that the api-client wrapper provides.

- `workspaces/pages/llm-providers/src/subComponents/LLMProviderOverviewTab.tsx:259` — raw `fetch` inside a component.
- `workspaces/pages/llm-providers/src/hooks/useOpenApiSpec.ts:52` — hand-rolled useEffect+fetch+AbortController that reimplements what `useQuery` gives for free.
- `workspaces/pages/test/src/AgentTest/Swagger.tsx:127-143` and `AgentChat.tsx:150` — arguably legitimate (proxying swagger try-it-out and chat streaming), but undocumented as sanctioned exceptions.

**Remediation: S** — move the two llm-providers cases into api-client hooks; add a comment or lint exemption marking the test-page cases as intentional.

## 8. API responses are cast, never validated — and errors are thrown as untyped JSON

**Impact: LOW-MEDIUM** — zod is a dependency of every package and used for form schemas, but network boundaries are trust-me casts; backend shape drift becomes a runtime `undefined` deep in a component instead of a clear parse error.

- The universal pattern in `workspaces/libs/api-client/src/apis/*.ts` (e.g. `apis/agents.ts`): `if (!res.ok) throw await res.json(); return res.json();` — the return is a bare `Promise<AgentResponse>` cast with zero runtime checks, and the thrown error is `any`-shaped parsed JSON that downstream `getErrorMessage` has to duck-type.

**Remediation: M** — introduce a typed `ApiError` wrapper now (S); zod-parse responses selectively on the highest-risk resources rather than everywhere.

## 9. Per-package config boilerplate tax: 28 near-identical config stacks

**Impact: MEDIUM** — adding a page means copying ~5 config files and a ~50-line devDependency block; drift is already visible (setupTests.tsx md5s differ between views and the page packages; deploy/shared-component/views carry full Storybook 8.6 dep stacks for only 9 `.stories.tsx` files in the whole repo).

- 28 `eslint.config.js`, 17 `vitest.config.ts`, 15+ `vite.config.ts`, 17 `setupTests.tsx`; every page package.json repeats ~35 identical devDependencies (prettier, 8 eslint plugins, testing-library, jsdom, postcss...). Storybook deps in 3 packages vs 9 story files total.

**Remediation: M** — extract a shared vitest/vite base config package (like the existing eslint-config); decide whether Storybook is real (expand) or vestigial (remove the deps).

## 10. `eslint-disable` as the escape hatch, concentrated where the config is weakest

**Impact: LOW** — 86 inline `eslint-disable` comments (vs 0 ts-ignores) and the shared config downgrades the rules that matter most: `@typescript-eslint/no-explicit-any: 'warn'`, `react-hooks/exhaustive-deps: 'warn'`, `no-console: 'warn'` (`workspaces/libs/eslint-config/eslint.config.js`). Warnings in CI-lint without `--max-warnings 0` are invisible. `any` hotspots align: `pages/eval/src/subComponents/EvaluatorForm.tsx` (5), `ViewEvaluator.Organization.tsx` (5), `pages/test/src/AgentTest/Swagger.tsx` (4) — mostly untyped Monaco/SwaggerUI plugin surfaces.

**Remediation: S** — add `--max-warnings 0` to the lint job, or promote exhaustive-deps/no-explicit-any to error with targeted disables where Monaco/Swagger typings genuinely fail.

## 11. Commented-out code blocks in ~12 files, including navigation and api-client hooks

**Impact: LOW** — noise, but two spots are in load-bearing shared code: `workspaces/core-ui/src/Layouts/OxygenLayout/LeftNavigation.tsx` and `workspaces/libs/api-client/src/hooks/guardrails.ts` carry commented-out implementation; `navigationItems.tsx:85` holds the repo's single TODO ("Use nav bar instead of navigate to the items") in an 850-line nav definition file.

**Remediation: S** — delete on sight; git remembers.

---

**Top 3 by leverage:** (1) wire tests into CI and kill the stale scaffolds — cheapest correctness win; (2) toolchain version alignment + `ensureConsistentVersions` — prevents an entire class of future build mysteries; (6) query-key factories — small change protecting cache correctness across all 20 page packages.
