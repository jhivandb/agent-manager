# DX Test Report — `amctl` (text mode)

**Session:** 2026-05-05 · binary: `amctl version dev` · instance: `default` · org: `default`

## Narrative

After the user re-authenticated, I switched to the `default` instance and confirmed the auth flow works. The shape of the test from there: list orgs → list projects → create a project → try to deploy an agent → cleanup.

`context org list` and `project list` both came back as headerless tab-separated rows. `project list` had four pre-existing rows including one literally called `sadas` ("sadasd"), which made it pretty clear users *do* test from the CLI in this environment but with no naming hygiene — partly because there's no `dx-test-*` convention to look for. The `--json` envelopes for both list commands were excellent: paginated structure with `limit`/`offset`/`total`, fully-typed records, scope context populated. The text output, by contrast, throws away `total`, omits headers, and gives no way to tell what each column means.

I created `dx-test-<timestamp>` as the test project. The success message was perfect: `✓ Created project dx-test-1777971791`, immediate, useful. `project get` on the new project printed the same beautiful aligned key-value block I'd seen earlier from `context show` — text output for *single-resource* views is genuinely good in this CLI; the inconsistency is concentrated in *list* views.

Then I tried duplicate creation (same name) and got `X Failed to create project` with no further detail. JSON envelope: `code: BAD_REQUEST`, message identical, `additionalData: {}`. The user has no way to know it failed because of duplicate name vs malformed input vs permission. Same for `project get nonexistent-id`: returned `status: 500, code: INTERNAL_ERROR, message: "Failed to get project"`. A nonexistent ID is a 404, not a 500, and "Failed to get project" tells the user nothing actionable.

The agent flow was the most informative segment of the test. `agent create --template` produced a JSON skeleton — schema clear, semantics opaque. I had no idea what `agentType.type`, `provisioning.type`, `inputInterface.type`, or `build.type` could legally hold. I tried a reasonable guess (`http`/`rest` for agent type, `git` for provisioning) and got `Invalid request body` with `additionalData: {}`. I went to the OpenAPI spec to find the actual enums (`provisioning.type` only accepts `internal`; `build` is a discriminated union of `buildpack`/`docker`), retried with valid values, and *still* got `Invalid request body` with no field-level detail. After that, I stopped — the entire path from "I have a template" to "I have a working request" requires reading the OpenAPI YAML or the server source. That's a major gap.

Pagination probes on `agent list`: `--limit 0` gave a clean pre-flight error with exit 2 (good — CLI-side validation works for this flag). `--limit 100` was silently accepted even though the OpenAPI spec caps it at 50 — server presumably caps, but the CLI could pre-validate.

Cleanup revealed something subtle. `project delete dx-test-1777971791` (without `-y`, in non-TTY) gave a great error: `X deletion requires --yes when stdin is not a terminal` — clear, suggests the fix. With `-y`, it printed `✓ Deleted project ...` and exited 0 immediately. But `project get` right after STILL returned the full project record. Three seconds later, the project was gone and `get` returned 1. So **delete is asynchronous but presents synchronously**. Worse: calling `delete -y` *again* on the in-flight-deleted project also returned success, so there's no visibility into what's actually committed.

One DX bright spot worth calling out: **the `clierr` envelope is genuinely good** — stable codes, scope context, populated reliably once scope resolution succeeds. The problem isn't the wire format, it's that `additionalData` is almost always empty and the messages are too coarse. If validation errors carried field-level details in `additionalData`, half the findings below would disappear.

## Findings — Grouped by Command Surface

### `agent` group

Most issues live here. The agent-create flow is the weakest part of the CLI.

| # | Severity | Command | Finding | Fix |
|---|----------|---------|---------|-----|
| 1 | **major** | `agent create -f <bad>.json` | `"Invalid request body"` with empty `additionalData` — no field-level info | Surface server validation details into `additionalData`; print field paths in text mode |
| 2 | **major** | `agent create --template` | Template shows shape but no enum hints (`agentType.type`, `provisioning.type`, `build.type` discriminator) | Embed JSONC comments with allowed values, or add `--describe` view with enums + required fields |
| 3 | minor | `agent create --template` | No hint about redirecting to file | Add footer: `# amctl agent create --template > agent.json && edit && amctl agent create -f agent.json` |
| 4 | minor | `agent list --limit 100` | Silently accepted; OpenAPI caps at 50 | Pre-validate against OpenAPI bounds |
| 5 | minor | `agent list` (empty result) | Prints nothing, exit 0 | Print `No agents in project <name>` to stderr |
| 6 | minor | `agent get` (no args) | Cobra error printed without usage block (other commands include it) | Use `cmd.SilenceUsage` consistently across the group |

### `project` group

Errors are the main weakness — wrong status codes, vague messages, async delete that pretends to be sync.

| # | Severity | Command | Finding | Fix |
|---|----------|---------|---------|-----|
| 7 | **major** | `project get <nonexistent>` | Returns `status: 500 INTERNAL_ERROR` instead of 404 | Map server 404 → CLI `NOT_FOUND` code; message: `project "<name>" not found in org "<org>"` |
| 8 | **major** | `project create <duplicate>` | `"Failed to create project"` — no indication the cause is a duplicate name | Map 409 → distinct code; surface conflict info in `additionalData` |
| 9 | **major** | `project delete -y` | Returns success synchronously; resource takes ~3s to actually disappear; second `delete` also returns success | Wait for deletion (with `--no-wait` to opt out) or print `Queued deletion of <name>` |
| 10 | minor | `project create --display-name (required)` | Required-ness in description string only; not enforced by cobra | `cmd.MarkFlagRequired("display-name")` for CLI-side pre-network validation |
| 11 | minor | `project list` lacks `--limit/--offset` | `agent list` has them; `project list` doesn't | Add to both, or document why only one |

### `context` group

Mostly polish. Active-state visibility and the silent-broken-auth switch are the two real issues.

| # | Severity | Command | Finding | Fix |
|---|----------|---------|---------|-----|
| 12 | minor | `context instance use <X>` when X has no valid token | Silent switch into broken state; auth error only on next API call | Warn at switch time: `"switched to <X> (no valid token — run amctl login)"` |
| 13 | minor | `context instance list` | No active-instance marker | Mark active row with `*` |
| 14 | minor | `context instance use ""` | Returns `"instance \"\" not found in config"` | Pre-validate empty input → `"instance name cannot be empty"` |
| 15 | cosmetic | `context instance list` | Multiple instances with same `url`+`org` indistinguishable | Add `last_used` or `auth_status` column |

### Cross-cutting (render / clierr / global flags / build)

These touch every command and would have the highest leverage if fixed.

| # | Severity | Surface | Finding | Fix |
|---|----------|---------|---------|-----|
| 16 | minor | `--json` envelope on pre-resolution errors | `instance: ""` even when active context exists | Populate envelope from active config before scope resolution runs |
| 17 | minor | All `list` commands (text mode) | No headers, no `total`, no active-row indicator | Render headed table; show `N of M` footer |
| 18 | minor | All single-resource text outputs (e.g., `project get`) use shortened labels (`pipeline`, `created`); JSON uses full names (`deploymentPipeline`, `createdAt`) | `pipeline` ≠ `deploymentPipeline` (loses meaning) | Keep label-shortening cosmetic only; don't drop semantic words |
| 19 | minor | All command help | No examples anywhere | Add `Example:` block per leaf command |
| 20 | cosmetic | All commands | `--org` global flag shown on `context instance use` etc. where it has no effect | Suppress globals where irrelevant |
| 21 | cosmetic | `version` | `dev (commit none, built unknown)` | Wire `-ldflags` for real build metadata |

## What Worked Well

- `clierr` envelope with stable codes and scope is excellent foundation
- `context show` / `project get` aligned key:value text output is the gold standard — apply to lists too
- `✓ Switched to instance X` / `✓ Created project X` / `✓ Deleted project X` — consistent, scannable success markers
- Delete confirmation guard for non-TTY is correct and the error message is exemplary
- Pre-flight validation on `--limit/--offset` (when present) — exit 2 with clear message
- `agent create --template` exists at all (most CLIs make you guess the schema)

## Top 3 to Fix First

1. **#1 / #2** — `agent create` validation feedback + enum-aware template. The current state makes the entire create flow unusable without reading the OpenAPI spec.
2. **#7 / #8** — Distinct error codes for `NOT_FOUND` and `ALREADY_EXISTS`. Almost free to implement; massive readability win across every command.
3. **#9** — Honest delete semantics. Either wait, or say "queued" — the current "✓ Deleted" + still-visible-resource is actively misleading.
