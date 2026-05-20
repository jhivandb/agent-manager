# `cli/` (amctl) — Code Health Report

Generated 2026-05-18 from a 5-agent parallel analysis of `/Users/jhivan/Developer/agent-manager/cli`. Read-only audit; no code changed.

**Scope:** ~11k lines of hand-written Go + ~26k generated, single binary, Cobra-based, ~30 commands across `agent`, `project`, `context`, `skills`, `login`, `version`.

---

## Headline scores (qualitative)

| Dimension | Grade | One-line verdict |
|---|---|---|
| Architecture | A− | Clean `Factory` DI, `render`/`clierr` envelopes, generated client isolated — solid bones. |
| Consistency | B | Strong dominant patterns; ~5 small but real deviations create real UX/exit-code bugs. |
| Code reuse | C+ | Foundations exist but commands repeat ~540 lines of identical prologue/wrap/unwrap shapes. |
| Quality | B− | No god files, no panics, clean ctx threading. Mostly DRY-debt and `%v`-error-chain loss. |
| Efficiency | B+ | Light startup, no polling, shared HTTP client. Two systemic redundant-RTT patterns and zero `errgroup` use. |
| Testing | C | `agent/build/*` and `pkg/auth` (PKCE/login) have **zero** tests — critical paths. |

---

## Cross-cutting themes (where multiple agents converged)

1. **Boilerplate around every API call.** ~25 sites repeat `if err != nil { Transport } ; if resp.JSON200 == nil { ErrorFromServer(FirstNonNil(...)) }`. Both reuse and quality agents flagged this. A `cmdutil.UnwrapResponse` + `cmdutil.TransportError` would delete ~100 lines and stop leaking `amsvc` types into every command.
2. **Identical RunE prologue in 10+ commands.** `ResolveScope → ResolveAgent → MakeScope → set Options fields` is ~20 lines copy-pasted. Reuse, quality, and consistency agents all hit this. A `Factory.ScopeFor(spec)` resolver removes ~150 lines.
3. **Three KEY=VALUE env parsers that disagree.** `agent create` rejects `1foo=bar`; `agent deploy` accepts it. **Real bug**, not style. Files: `agent/deploy.go:46`, `agent/create/request.go:194`, `agent/create/validation.go:164`.
4. **`%v`-wrapped errors lose the chain.** ~23 sites use `clierr.Newf(clierr.Transport, "%v", err)`, breaking `errors.Is(ctx.DeadlineExceeded)` downstream.
5. **Redundant pre-flight GETs.** Every `agent logs/metrics/build/deploy` does a `ValidateBuildable` `GET /agents/{name}` just to read `Provisioning.Type` — an extra RTT on the most common commands. Traces commands add another `preflightEnv` GET. Either let the server return a typed 422 or cache within the command.
6. **Two `formatDuration` functions** with same name, different signatures, different formats (`agent/build/get.go:146` vs `agent/traces/helpers.go:24`). Same for three date formats (`2006-01-02`, RFC3339, `2006-01-02 15:04:05` — the last isn't ISO).
7. **"human" appears in user-facing text** (user-flagged preference). `agent/create/create.go:108` flag help `"Human-readable name (required)"` and `clierr/clierr.go:27` doc comment.
8. **Exit-code inconsistency for "build name required".** `build get` returns `clierr.New` → exit 1; equivalent in `traces/trace.go` uses `FlagErrorf` → exit 2. Same class of mistake, different exit codes.
9. **Flag `--env` is overloaded** for both "environment name" (logs/metrics/traces) and "KEY=VALUE" (deploy/create). Disjoint today, will collide the moment `deploy` takes an environment selector.
10. **Zero `errgroup` anywhere.** `context link` does 3 independent GETs sequentially; `deploy` does up to 5. Easy 1–2 RTT wins.

---

## What's actually good (don't break these)

- One `Factory`, one HTTP client, one shared 30s timeout — auth, token refresh, and trace observer all reuse it.
- No `init()` functions, no `panic` in non-main, single `os.Exit` at `cmd/amctl/main.go:26` — clean process boundaries.
- No polling, no `time.Sleep` — fire-and-forget design throughout.
- Context propagated cleanly — only `pkce.go` uses `context.Background()`, and intentionally.
- Stable error-code wire contract in `clierr` with 23 codes — well-designed for the JSON envelope.
- `AGENTS.md` is precise and matches the code — packages obey the "no `cmdutil` import from leaves" rule.

---

## Top 10 fixes to queue

| # | Fix | Source | Impact |
|---|---|---|---|
| 1 | Unify env-key validation across create/deploy | reuse/quality | **bug** |
| 2 | `UnwrapResponse` + `TransportError` helpers | reuse/quality | ~100 LoC, isolates oapi-codegen |
| 3 | `Factory.ScopeFor(spec)` to kill RunE prologue duplication | reuse/quality | ~150 LoC |
| 4 | Drop `ValidateBuildable` preflight (server returns typed 422) | efficiency | RTT on every agent command |
| 5 | Fix `build get` exit code (use `FlagErrorf`) | consistency | UX consistency |
| 6 | Rename deploy/create `--env` to `--env-var` or `--set-env` | consistency | future-proofing |
| 7 | Switch `%v` error wrapping to `%w` everywhere | quality | breaks silently today |
| 8 | Backfill tests for `pkg/cmd/agent/build/*` and `pkg/auth/{login,pkce}` | consistency | critical-path coverage |
| 9 | `errgroup` parallelize `context link` (3 GETs) and `agent deploy` (independent fetches) | efficiency | UX latency |
| 10 | Cache tab-completion results on disk (~30s TTL) | efficiency | most-visible latency sink |

---

# Detailed findings

## 1. Structure map

### Directory layout
- **`cmd/amctl/`** — single entry point (`main.go`, 27 lines)
- **`pkg/`** — organized by function: `amcmd`, `cmd` (all commands), `cmdutil` (DI), `clients` (OpenAPI), `auth`, `config`, `render`, `clierr`, `iostreams`, utilities

### Command hierarchy
Root `amctl` branches into 6 groups:
- **agent** (9 subcommands: list, get, delete, deploy, create, logs, metrics, build with 4 nested)
- **project** (4: list, get, create, delete)
- **context** (show, link, unlink, instance [3], org [2])
- **skills** (install, remove, list)
- **login** / **version**

### Architecture pillars
1. `cmdutil.Factory` — single DI container managing Config, IOStreams, HTTP clients, OAuth token refresh
2. `render` package — JSON/text output dispatch with stable Scope and error codes
3. `clierr` — 23 stable error codes for the JSON wire contract
4. `clients/amsvc/gen` — auto-generated OpenAPI client (~26k lines)
5. Commands pattern: Options struct with factory deps → RunE resolves scope/flags → testable `runX(ctx, opts)`

### Code volume
~5.4k CLI command code + 1.0k cmdutil + 26.5k generated clients = ~35k total lines.

---

## 2. Consistency audit

### Command structure
**Dominant:** `NewXxxCmd(f *Factory)` → `XxxOptions` struct → flags via `pflag` direct (no viper) → `RunE` resolves scope → calls free `runXxx(ctx, opts)`.

**Deviations:**
- `pkg/cmd/version.go:32` uses `Run:` not `RunE` (only one).
- `pkg/cmd/login.go:69-83` omits `Args:`.
- `pkg/cmd/agent/traces/traces.go` is both a subcommand and a parent group — `agent traces` lists, `agent traces export` is a child. Asymmetric with other groups.
- `agent trace` (singular) is registered under `agent/` while `agent traces` (plural) is the same package — two surface entry points.
- Two scope-construction orderings: some commands build scope first then check error (good), others return empty `render.Scope{}` on resolve failure (drops org/instance from JSON error envelope). Affected: `logs.go:74`, `metrics.go:67`, `traces/{trace,export,traces}.go`.

### Flag conventions
**Dominant:** kebab-case, no env-var fallback, no short flags except `-y`, required-ness enforced by hand-rolled validation.

**Deviations:**
- Only one `cmd.MarkFlagRequired` in the codebase (`traces/export.go:102`).
- "Required" affordance has three styles: cobra-marked, post-parse error, or just `(required)` in help text.
- Build name is `--build-name` flag in deploy but positional in `build get`.
- `--sort` (CLI) maps to `sortOrder` (API) — naming drift.
- `--env` overloaded between environment name and KEY=VALUE — see cross-cutting #9.

### Error handling
**Dominant:** Commands return errors; `render.Error(io, scope, err)` dispatches to JSON or text; transport failures wrap as `clierr.Newf(clierr.Transport, "%v", err)`; server responses use `cmdutil.ErrorFromServer` + `cmdutil.FirstNonNil`. No `panic`, no `log.Fatal`.

**Deviations:**
- Three wrap styles: `clierr.Newf(..., "%v", err)` (lossy), `fmt.Errorf("...: %w", err)` (chains), `fmt.Errorf("%v", err)` (lossy).
- `build get` returns `clierr.New(clierr.InvalidFlag, ...)` for missing build name → exit 1; `traces/trace.go` uses `FlagErrorf` → exit 2. Behavioral bug.
- `deploy.go:51` returns bare `fmt.Errorf` from `parseEnvFlag`; caller wraps with `FlagErrorf("%v", err)` losing chain.

### Output formatting
**Dominant:** `--json` is persistent root flag; text uses `tableprinter` for lists and `fmt.Fprintf` blocks for "get" details; colors via `iostreams/color.go` (hand-rolled ANSI).

**Deviations:**
- No shared key-value printer — three `get` commands (`agent/get.go:104`, `project/get.go:99`, `build/get.go:127`) each hand-align with different column widths.
- "human" appears in user-facing text and doc comments (see cross-cutting #7).
- `instance/list.go:88` shows a "*" current-item indicator only when TTY — unique pattern.
- No spinners/progress bars anywhere.

### Logging
There is **no logger**. Every message is `fmt.Fprintf(io.Out|io.ErrOut, ...)`. No `slog`, `log`, `logrus`, or `zap`. `pkg/cmd/login.go` hand-writes `"warning: ..."` prefixes four times.

### HTTP / API client usage
**Dominant:** single `http.Client{Timeout: 30s}` per Factory; both service clients reuse it; auth + token refresh centralized.

**Deviations:**
- `pkg/clients/discovery.go:50` creates its own `http.Client{Timeout: 10s}` instead of reusing the factory's.
- Two slightly different option APIs: `WithRequestEditorFn` (amsvc) vs `WithRequestEditor` (traceobssvc).
- `traceObserver` URL is `sync.Once`-cached; nothing else is.

### Config / credentials
Single file `~/.amctl/config` (YAML), atomic save via tempfile + rename. Strict decoding (`KnownFields(true)`) — forward-compat additions break older binaries.

**Deviations:**
- Mutation is half via methods (`cfg.LinkProject`), half via direct map writes (`login.go:133`, `instance/use.go:79`).
- `Current()` returns `*Instance` from a value-typed map — pointer cannot mutate the stored value; several call sites do the read-modify-write dance.

### Naming
**Dominant:** lowercase single-word packages, lowercase files, `XxxOptions` structs, `NewXxxCmd` constructors, `runXxx` runners.

**Deviations:**
- `pkg/cmd/context/` shadows stdlib `context` — imported as `amcontext` in `root.go`.
- Snake_case files only in `cmdutil`: `validate_buildable.go`, `org_override.go`, `project_override.go`.
- Two lowercase exported-looking structs: `loginData` (`login.go:40`), `listData` (`skills/list.go:43`).
- `boolPtrLocal` (`deploy.go:169`) — "Local" suffix implies a global; there isn't one.

### Testing
Plain `testing` only, no testify. Test files use local `newTestIO` + `decodeEnvelope` helpers.

**Coverage gaps:**
- `pkg/cmd/agent/build/*` — **0 tests** (critical deploy path)
- `pkg/cmd/context/{link,unlink,show}` — 0 tests
- `pkg/auth/{login,pkce}` — 0 tests (OAuth/PKCE flow unverified)
- `pkg/cmd/login.go` — 0 tests
- `pkg/clients/discovery.go` — 0 tests

### Context propagation
**Dominant:** every `RunE` calls `cmd.Context()` and threads it through; only `pkce.go:135` uses `context.Background()`, intentionally for server shutdown.

**Deviations:** `instance/{use,list,remove}.go` and `context/{show,unlink}.go` drop ctx because they're purely filesystem-bound. Consistent within subgroup but inconsistent with the rest.

### Top 5 inconsistencies worth fixing
1. Build name as flag vs positional, and `FlagError` vs `clierr.New` mismatch causing different exit codes.
2. `--env` overloaded for both environment name and KEY=VALUE.
3. Scope-construction ordering drops instance/org from error envelopes.
4. Three styles of "required flag" enforcement.
5. Untested `agent/build/*` and `pkg/auth/*` packages.

---

## 3. Code reuse audit

### High-impact duplications

**`clierr.Newf(clierr.Transport, "%v", err)`** — 23 call sites across the codebase, all transport errors wrapped identically. Helper: `cmdutil.TransportError(err)`.

**`if resp.JSON200 == nil { ErrorFromServer(FirstNonNil(...)) }`** — ~25 call sites. Helper: `cmdutil.UnwrapResponse(httpResp, want, body, errs...)`. Today, every command depends on the oapi-codegen `*amsvc.ClientWithResponses` pointer-fork-per-status-code shape.

**RunE prologue chain** (`ResolveOrgProject → ResolveAgent → MakeScope → set Options`) — 10+ commands repeat ~20 lines each. Helper: `Factory.ScopeFor(spec)` returning a populated context struct.

**Latest build lookup** — 3 places: `deploy.go:368`, `build/logs.go:132`, plus inline in `deploy.go:346`. Helper: `cmdutil.LatestBuild(ctx, client, scope) BuildResponse`.

**KEY=VALUE env parsing** — three implementations that disagree on key validation:
- `agent/deploy.go:46` — `parseEnvFlag` rejects empty keys only
- `agent/create/request.go:194` — `splitEnv` no validation
- `agent/create/validation.go:164` — `parseEnvKey` requires regex `[A-Za-z_][A-Za-z0-9_]*`

Result: `--env 1foo=bar` is accepted by `deploy`, rejected by `create`. **Real bug.**

**Config-load + "no instance" preamble** — 9 call sites repeating the same 6-line block. Helper: `Factory.CurrentConfig() (*Config, Scope, error)`.

### Medium-impact

- **Confirm-with-TTY-fallback** for destructive ops (3 copies: `agent/delete.go:97`, `project/delete.go:89`, `instance/remove.go:83`).
- **`limit/offset` flag plumbing** (2 near-identical 25-line blocks in `agent/list.go:52` and `build/list.go:55`).
- **`ValidArgsFunction` single-arg completer boilerplate** (15+ sites with the same 5-line block).
- **"Success message" boilerplate**: `cs := o.IO.StderrColorScheme(); fmt.Fprintf(o.IO.ErrOut, "%s ... %s\n", cs.SuccessIcon(), name)` — 12+ copies.

### Cross-package helpers that should be in `cmdutil`

| Helper | Currently in | Should be in |
|---|---|---|
| `formatDuration` (nanos int64) | `agent/traces/helpers.go:24` | `cmdutil` |
| `formatDuration` (time.Duration) | `agent/build/get.go:146` | `cmdutil` |
| `timeAgo` | `agent/traces/helpers.go:36` | `cmdutil` |
| `truncate` | `agent/traces/helpers.go:54` | `cmdutil` |
| `parseTimeOrZero` | `agent/traces/list.go:68` | `cmdutil` (preferably surface `time.Time` from `ResolveSinceWindow` directly) |
| Three env parsers | three files | `cmdutil` (unified) |

### Date format drift
Five sites format dates ad-hoc with three different formats:
- `"2006-01-02"` — `agent/list.go:123`, `org/list.go:89`, `project/list.go:89`
- `"2006-01-02T15:04:05Z07:00"` — `agent/get.go:119`, `project/get.go:104`, `build/get.go:131`
- `"2006-01-02 15:04:05"` — `build/list.go:157` (not even ISO)

### Top 10 reuse opportunities (ranked)

| # | Opportunity | Severity | Sites | Est. LOC removed |
|---|---|---|---|---|
| 1 | `UnwrapResponse` helper for oapi-codegen response | HIGH | ~25 | ~75 |
| 2 | `TransportError(err)` helper | HIGH | ~23 | ~23 |
| 3 | `Factory.ScopeFor(spec)` resolver | HIGH | ~10 | ~150 |
| 4 | `LatestBuild()` helper | HIGH | 3 | ~30 |
| 5 | Unified `ParseEnvSlice` (+ bug fix) | HIGH | 3 | ~40 |
| 6 | `ConfirmDeletion` helper | MED | 3 | ~25 |
| 7 | `AddPaginationFlags` helper | MED | 2 | ~50 |
| 8 | `SingleArgCompleter` helper | MED | ~15 | ~75 |
| 9 | `PrintSuccess` helper | MED | ~12 | ~25 |
| 10 | `Factory.CurrentConfig()` collapse | HIGH | ~9 | ~45 |

**Total reduction:** ~540 lines of duplicated code, plus one bug fix and one consistency win.

---

## 4. Code quality audit

### Redundant state
- **`limitSet`/`offsetSet` booleans** snapshot from `cmd.Flags().Changed(...)` in PreRunE just to be read in RunE that already has `cmd` — `agent/list.go:52`, `build/list.go:55`, `agent/create/create.go:62` (`PortSet`).
- **`userCacheDir = os.UserCacheDir`** mutable package-level var as test seam in `completion.go:38`.
- **`Scope` field on every Options struct** — derivable from already-stored `Org/Proj/AgentName/Env`.

### Parameter sprawl
- **Factory callable `MakeScope func(string, string, string, string)`** — 4 unnamed positional strings in logs/metrics/traces/trace/export. Use a typed `ScopeParams` struct.
- **Per-command Options structs** all duplicate the same 5–6 fields (`IO, Client, ResolveScope, MakeScope, ResolveAgent, ResolveEnv`). Factory subset interface would eliminate ~50 lines per command.
- **`resolveDeployableBuild(ctx, client, org, proj, agent, buildName)`** — 5 positional strings; a `scope` value type would help.

### Copy-paste with slight variation
- Three `Complete*` functions in `completion.go` with the same 15-line shape.
- 10+ commands share the same RunE prologue verbatim.
- 30+ sites repeat the "error or JSON200" boilerplate.
- Two `formatDuration` functions with same name, different signatures.

### Leaky abstractions
- **`ErrorFromServer` requires `amsvc` import in every caller** + manual `FirstNonNil(resp.JSON404, resp.JSON500)`.
- **`*amsvc.ClientWithResponses` pierces every Options struct** — codegen change = 100+ file refactor. A thin domain interface in `pkg/clients/amsvc` would isolate it.

### Stringly-typed code
- **`isBuildDeployable(status string)`** in `deploy.go:400` compares raw `"Completed"` / `"Succeeded"` strings. Need a typed const set.
- **`GrantType` raw strings** in `auth/login.go:84,142` and `factory.go:188-193` — typo silently picks default branch (wrong refresh path).
- **`provisioningExternal = "external"`** const exists only to print "not supported" — delete it.
- **`--sort` flag** accepts arbitrary string; typo yields 400 not a flag error.

### Nested conditionals
- **`mergeEnv` switch-case** in `deploy.go:81-117` — two-level boolean logic flattened into a switch.
- **`validateInternal`** in `agent/create/validation.go:56-145` — 90 lines, three levels of nesting. Convert to a rule table.
- **`runDeploy`** is 90+ lines with six sequential do-X-error-Y blocks plus a 3-deep prompt sub-block.

### Unnecessary comments / dead markers
- **`// wired in Task 5` / `// wired in Task 6`** in `deploy.go:184-185` — internal task tracking leaked into source.
- **`boolPtrLocal`** in `deploy.go:169` — "Local" suffix implies a shared helper that doesn't exist.
- **TODOs without owner/date**: `org/list.go:72`, `project/list.go:71`, `browser/browser.go:17`.

### Error handling smells
- **`fmt.Errorf("%v", err)` losing Unwrap chain** — 20+ sites including `agent/deploy.go:269,283,309,322`, `agent/get.go:92`, `context/link.go:110,118,131`, `cmd/login.go:107,112,122`.
- **Silent ignored errors** — `cfg, _ := f.Config()` ×4 in `scope.go:31,65,122,125` causes misleading "no organization" downstream messages.

### Concurrency
- PKCE callback server goroutine is clean (`pkce.go:122`).
- `traceObsOnce` doesn't propagate ctx cancellation — first caller's ctx wins for everyone.

### God files
None over 500 lines. Borderline:
- `pkg/cmd/agent/deploy.go` — 416 lines (parsing + merging + rendering + HTTP)
- `pkg/skills/skills.go` — 361 lines (install + list + remove + frontmatter + fs helpers)

### Top 10 quality issues
1. Duplicated `Complete*` functions (HIGH).
2. Duplicated RunE prologue across 10+ commands (HIGH).
3. Duplicated `err != nil / resp.JSON200 == nil` boilerplate (HIGH).
4. Generated `*amsvc.ClientWithResponses` exposed throughout (HIGH).
5. "human" / "Human-readable" in code and help text (HIGH per user preference).
6. `// wired in Task N` leftover comments (HIGH).
7. Stringly-typed build status & grant type (MED).
8. `%v`-wrapped errors lose Unwrap chain (MED).
9. `loadModelConfig` parsed twice (MED).
10. Two `formatDuration` with different signatures (MED).

---

## 5. Efficiency audit

### Startup
**Largely fine.** No `init()` functions outside generated code. `cmd/amctl/main.go:25-27` is a one-liner. `Factory.New` uses lazy closures. Verified: no network calls during cobra wiring.

**Minor:**
- `disableFileCompletion` walks the entire cobra subtree at startup even for `--help`/`--version`.
- `config.Load` is eager in `amcmd.Main:41` even when the command needs no config — defer behind `f.Config` closure.

### Unnecessary work
- **`Factory.linkedProject()` calls `os.Getwd()` ×3 per command** because `ResolveOrgProject`, `ResolveAgent`, and `ResolveEnvironment` each independently walk the directory.
- **`Validate*` preflight GET** — every `agent logs/metrics/build/deploy` calls `GET /agents/{name}` just to read `Provisioning.Type`. `deploy` makes up to 5 sequential round-trips; `build logs` makes 3.
- **`preflightEnv` GET** on every trace command (`traces.go:72`, `export.go:94`, `trace.go:98`) — extra RTT.
- **`amctl link`** runs 3 sequential GETs that are independent.
- **`loadModelConfig` double-parses** YAML→`interface{}`→JSON→struct (yaml.v3 doesn't honor json tags — tracked in MEMORY).

### Missed concurrency
- `context link` (3 GETs) — independent, could use `errgroup`.
- `agent deploy` (build resolve + pipeline + configurations) — at least 2 of these are independent.
- **No `errgroup` anywhere in the codebase.**

### N+1 API calls
- None in read paths. Closest: completion functions make full list calls per TAB press (medium UX cost).

### Polling
- **No polling exists.** `grep -rn "Sleep|time.Tick|time.NewTicker"` returns nothing. Build/deploy are fire-and-forget; user runs `agent get` to check status. No `--wait` UX either way.

### HTTP client setup
- Single shared `http.Client{Timeout: 30s}` in `Factory` — good, connection pool preserved.
- **`clients/discovery.go:50`** creates its own client with a 10s timeout — undocumented divergence.
- **OAuth2 token sources** bypass the factory client.
- No `Transport` tuning, but defaults are fine for single-host CLI.

### Memory
- **`walkTarball` in `skills/remote.go:93`** reads every tar entry into `[]byte`, stuffs them into a `map[string]map[string][]byte`, then re-materializes as `fstest.MapFS`. Entire archive held twice in memory briefly.
- `io.ReadAll(tr)` per entry is unbounded — no size cap; potential DoS surface.
- Otherwise, `defer Close()` discipline is good across HTTP responses and files.

### Existence checks (TOCTOU)
- Minor: `skills/skills.go` has a few `Stat`-before-act sequences. All low-risk (heuristics for "is this a skill dir").
- Write paths use atomic tempfile + rename. Good.

### Spinner / progress overhead
None. No spinners imported anywhere.

### Top 10 efficiency wins
1. Drop `Validate*` preflight GET (saves RTT on every agent command).
2. Drop `preflightEnv` GET on trace commands.
3. Disk cache for tab-completion (~30s TTL) — most user-visible latency sink.
4. Parallelize `context link`'s 3 GETs with `errgroup`.
5. Parallelize `agent deploy`'s independent fetches.
6. Stream skills tarball directly to disk instead of through `fstest.MapFS`.
7. Memoize `linkedProject` / `os.Getwd` on the Factory.
8. Add server-side "latest build logs" endpoint to collapse 3 RTTs into 1.
9. Reuse `f.HTTPClient()` in `Discover` and oauth2 token sources.
10. Defer `disableFileCompletion` tree walk and lazy-load config.

---

## Acceptance criteria for follow-up

This report is analysis only. To act on it without breaking anything:

- Items 5, 6, 7 from "Top 10 fixes" are mechanical and safe.
- Items 1, 2, 3 are larger refactors — write tests first (item 8 is a prerequisite).
- Items 4, 9 may require server changes; coordinate.
- Item 10 (completion cache) needs a cache invalidation story (org/project switch should clear it).

Per-agent raw findings cited file:line throughout. If any item needs deeper drilldown, the relevant cli/ subtree is identified by path.
