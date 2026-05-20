# DX Test Report — `amctl --json` (agent/scripting mode)

**Session:** 2026-05-05 · binary: `amctl version dev` · instance: `default` · org: `default`
**Lens:** evaluating from the perspective of an LLM agent or shell script consuming `--json` output.

## Narrative

`--json` mode is in much better shape than text mode in terms of structure and exit codes — but it has its own class of issues that hurt agents specifically: empty fields where data should exist, inconsistent envelope population, mixed casing conventions, JSON-escaped human strings, and a few commands that just ignore `--json` entirely.

I started with `context show --json` and `context instance list --json`. Both came back as well-formed JSON, but `instance list` had `instance: ""` in the envelope despite an active instance being set. That kind of inconsistency is exactly what trips up scripts that key off the envelope. Then `data.current_org` jumped out — snake_case in a sea of camelCase (`displayName`, `createdAt`, `orgName`). Two casing conventions in one envelope means agents have to know which endpoint they're talking to to write the right key.

Listing was uniformly good: `data.{resource_plural}` arrays with `limit`, `offset`, `total` siblings. Predictable, paginated, machine-friendly. Empty lists (`agents: []`, `total: 0`) are well-formed — a notable improvement over text mode which prints nothing at all.

Then I created `dx-test-<timestamp>` and noticed the showstopper: **the create response returns `uuid: ""` for the brand-new project**. An agent that wants to "create project, then deploy agent into it" needs the UUID and is forced into a follow-up `get` call. The create response *also* has an inconsistent timestamp format — nanosecond precision (`09:46:09.219787428Z`) on create, second precision (`09:46:09Z`) on get. Either is fine on its own; both together force agents to handle two parsers.

Errors were a mixed bag. The good: `agent get nonexistent` returned `status: 404, code: AGENT_NOT_FOUND` — properly mapped, properly typed, an agent can branch on it cleanly. The bad: `project get nonexistent` returned `status: 500, code: INTERNAL_ERROR` for the same logical scenario. Same operation, different resource, different mapping. An agent doing CRUD across both has to special-case projects.

The `additionalData` field — which is the natural place for field-level validation errors, conflict info, retry-after hints — is `{}` in every error envelope I saw. That's where the wire contract is paying dues but not getting value back. Validation errors on `agent create` say `"Invalid request body"` with empty `additionalData`. An agent has no way to course-correct without a separate roundtrip to the OpenAPI spec.

I also found a JSON-encoding gotcha: `agent list --limit 0` returned the message `"--limit must be >= 1"`. The `>=` got HTML-escaped. If you `jq -r '.error.message'`, you get the escape sequence in the rendered string. Disable HTML escaping for human-readable string fields — they're not going into a browser.

Exit codes are *mostly* fixed in `--json` mode (exit 1 on error envelope, exit 2 on `INVALID_FLAG`) — a big improvement from the earlier session where `--json` was always exit 0. But **`amctl --json login` still returns exit 0 despite an error envelope**. So the fix was applied per-command rather than centrally in `render`.

A few commands silently ignore `--json` entirely:
- `--help` prints text help
- `version` prints text version
- `agent create --template` prints the raw template (which makes sense — it's *meant* to become a request body — but it's inconsistent and there's no way to detect this from the wire)

Cleanup: `project delete -y --json` returns a clean `data: {name, deleted: true}` envelope. Better than text's `"✓ Deleted project X"` (which would be hostile to parsers). But the async-vs-sync mismatch from text mode is still here, with a new wrinkle: a second delete on the in-flight-deleted project returned **500 INTERNAL_ERROR** rather than the success-then-failure pattern I saw in text mode. So the behavior is non-deterministic depending on timing — sometimes idempotent, sometimes not.

Bright spots: `code: CONFIRMATION_REQUIRED` when running `delete` without `-y` in non-tty is exactly what an agent needs to detect-and-retry. `INVALID_FLAG`, `NO_PROJECT`, `AGENT_NOT_FOUND` are all stable and brand-able. The wire contract document in `clierr.go` is real and adhered to. **The bones are good.** Most fixes here are small and high-leverage.

## Findings — Grouped by Command Surface

### Cross-cutting (envelope, render, codes, encoding) — fix these first, biggest leverage

| # | Severity | Surface | Finding | Fix |
|---|----------|---------|---------|-----|
| J1 | **major** | All error envelopes | `additionalData: {}` is always empty. Field-level validation, conflict details, retry hints — all missing | Plumb structured `additionalData` from server validation/conflict responses; add a stable schema for common cases (e.g., `{validation: [{field, code, message}]}`) |
| J2 | **major** | `amctl --json login` (and any other un-migrated command) | Returns exit 0 despite writing an error envelope | Audit: every code path that writes `JSONError` must propagate non-zero exit. Move the exit-code mapping into `render` so it can't be skipped per command |
| J3 | **major** | All `create` responses | Newly-created resource returns `uuid: ""` empty | Server must return UUID in create response; CLI must surface it. Agents need it for follow-up calls without a roundtrip |
| J4 | minor | Field naming | Mix of camelCase (`displayName`) and snake_case (`current_org`, `is_sensitive`) within the same surface | Pick one (the spec leans camelCase) and migrate the outliers; document the convention in `clierr.go` or a top-level wire-contract doc |
| J5 | minor | Error message strings | HTML-escaped: `--limit must be >= 1` instead of `>=` | Configure JSON encoder with `SetEscapeHTML(false)` for envelope output |
| J6 | minor | `error.reason` field | Sometimes string (`"Internal server error"`), sometimes `null` — values are tautological when present | Either populate meaningfully (e.g., upstream reason chain) or drop the field. Inconsistent nullability is worse than absence |
| J7 | minor | Envelope `instance` field | `""` on `context instance list --json` even when active instance exists | Populate `instance` from active config unconditionally before any command runs |
| J8 | minor | Timestamp format | `created` returns nanosecond precision (`09:46:09.219787428Z`), `get` returns second precision (`09:46:09Z`) | Standardize on RFC3339 second-precision (or nanosecond) across all endpoints |
| J9 | cosmetic | `--json` on `version`, `--help` | Silently ignored | Either honor (`{data: {version, commit, built}}`) or document that meta commands always emit text |

### `project` group

| # | Severity | Command | Finding | Fix |
|---|----------|---------|---------|-----|
| J10 | **major** | `project get <nonexistent> --json` | Returns `status: 500, code: INTERNAL_ERROR` while `agent get nonexistent` correctly returns `status: 404, code: AGENT_NOT_FOUND` | Mirror the agent mapping: add `PROJECT_NOT_FOUND` code, return 404 from server |
| J11 | **major** | `project create <duplicate> --json` | Returns `status: 500, code: INTERNAL_ERROR, reason: "Internal server error"` for duplicate | Add `PROJECT_ALREADY_EXISTS` code mapped to 409 |
| J12 | **major** | `project delete -y --json` (when called twice quickly) | Non-deterministic — sometimes returns success (idempotent), sometimes 500 INTERNAL_ERROR depending on async deletion timing | Decide: idempotent-delete (always success when resource is gone or going) OR strict (404 if gone). Document and enforce |
| J13 | minor | `project list --json` | Lacks `--limit`/`--offset` flags entirely | Add for parity with `agent list` (server presumably already paginates) |

### `agent` group

| # | Severity | Command | Finding | Fix |
|---|----------|---------|---------|-----|
| J14 | **major** | `agent create -f <bad>.json --json` | `error: { code: BAD_REQUEST, message: "Invalid request body", additionalData: {} }` — no field-level info | Cross-cuts with J1. Specifically for agent create: validation errors must include `{field, expected, got}` so an agent can self-correct without reading OpenAPI |
| J15 | **major** | `agent create --template` | Empty enum slots; no schema hints in JSON output (`agentType.type: ""`, `provisioning.type: ""`, `build: {}` discriminator missing) | Either embed a `$schema` reference in the template, or add `agent describe-schema --json` that returns enums + required fields per field. JSONC isn't valid JSON, so JSONC isn't right for this |
| J16 | minor | `agent get` (missing arg) | Returns `code: CLI_TRANSPORT, message: "accepts 1 arg(s), received 0"` — wrong code (transport ≠ argument validation) | Map cobra arg-count errors to `INVALID_ARGUMENT` or reuse `INVALID_FLAG` |
| J17 | minor | `agent list --limit 100 --json` | Silently accepted; OpenAPI caps at 50 | Pre-validate with `INVALID_FLAG` exit 2 (mirror `--limit 0` behavior) |

### `context` group

| # | Severity | Command | Finding | Fix |
|---|----------|---------|---------|-----|
| J18 | minor | `context show --json` | `data.org` and envelope `org` duplicate the same value | Drop from `data` since envelope is canonical, OR drop from envelope on this specific command |
| J19 | minor | `context instance list --json` | `data.current` field for active instance, but no `is_current: true` per row in `instances` array | Add `isCurrent: true` per row OR document `data.current` as the canonical pointer |
| J20 | cosmetic | `context instance list --json` | `current_org` is snake_case; sibling `name`/`url` are unmarked but blend with camelCase elsewhere | Migrate to `currentOrg` |

## What Worked Well (keep doing this)

- **List envelope structure** (`{limit, offset, total, <plural>: [...]}`) is uniform and parseable
- **Empty lists are well-formed** (`{agents: [], total: 0}`) — agents handle this naturally
- **Stable error codes** (`AGENT_NOT_FOUND`, `INVALID_FLAG`, `NO_PROJECT`, `CONFIRMATION_REQUIRED`) are exactly what agents brand-and-branch on
- **Exit code differentiation** (`INVALID_FLAG` → 2, others → 1) follows shell convention
- **`status: 0` for client-side errors** is a clear convention — easy to distinguish local from remote
- **Scope context on every envelope** (`instance`, `org`, `project`) means agents don't need to track ambient state separately
- **`code: CONFIRMATION_REQUIRED`** is the model — actionable, machine-detectable, retry-able

## Top 5 to Fix First (agent/scripting impact)

1. **J3** — Create responses must return `uuid`. Forces an extra roundtrip in every "create-then-use" flow today.
2. **J1** — Populate `additionalData` for validation/conflict errors. Most "vague error" complaints collapse into this single fix.
3. **J2** — Centralize exit-code mapping in `render` so no future command can ship with broken exit codes (login is the canary).
4. **J10 / J11** — Distinct `*_NOT_FOUND` and `*_ALREADY_EXISTS` codes for projects (parity with agents).
5. **J5** — `SetEscapeHTML(false)` on the JSON encoder. One-line fix; affects every error message that contains `<`, `>`, or `&`.

## Comparison vs text-mode report

Both reports surface the same root causes (vague errors, async-vs-sync delete, project not-found mapping). The text-mode report's findings about *output formatting* (no headers, no active marker, label inconsistencies) don't apply here — JSON is well-structured. The JSON-mode report's findings about *envelope consistency* (empty `instance`, `uuid: ""` on create, mixed casing, HTML escaping) don't appear in text mode — text doesn't care about machine consumption.

If you fix the cross-cutting items (J1, J2, J3, J5, J7) plus the project/agent error mapping (J10, J11, J16), both modes get materially better at once.
