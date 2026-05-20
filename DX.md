# CLI Developer Experience Gaps

Audit of `./amctl` help messages and CLI usability as of 2026-04-29.

## 1. No `--version` / `-v` flag

`./amctl --version` returns `unknown flag: --version`. Every CLI should expose a version flag — it's the first thing users and support scripts reach for.

## 2. Root command has no Long description or examples

`amctl --help` shows only the one-liner `"Interact with Agent Manager via CLI"`. There's no onboarding guidance — no "getting started" example, no typical workflow shown. A new user has no idea what order to run things in (login first, then context org use, then agent commands).

## 3. `login --url` is required but not marked as such

`--url` is the only truly required flag on `login`, but the help shows it as a plain optional flag. Users must fail once to discover it's required. It should use `MarkFlagRequired` (like `project create --display-name` already does) so Cobra marks it `(required)` in help output.

## 4. `login --name` doesn't explain what it defaults to

The help just says `"Agent Manager instance name"`. There's no hint that it defaults to `"default"` or what the name is used for (selecting between multiple saved instances).

## 5. No `agent create` or `agent update` commands

The only agent mutations are `agent get`, `agent list`, and `agent delete`. There's no way to create or update agents from the CLI. Even a note in `amctl agent --help` ("Agents are created via the web console") would help set expectations.

## 6. No output format flag (`--output json|table|yaml`)

All output is JSON (via `render.Success` / `render.Error`). There's no `--output` / `-o` flag. For human use, raw JSON is hard to scan; for scripting, users may want different formats. At minimum the help should state that output is always JSON.

## 7. `--org` is a root persistent flag but irrelevant to many commands

`--org` is defined on root (`root.go:22`) so it appears in `--help` for every command, including ones that ignore it entirely: `login`, `context instance list`, `context instance use`, `context instance remove`, `context show`. Its description — `"Override the active organization for this command"` — is misleading on those commands. Consider moving it to the command groups that actually use it (`agent`, `project`, `context org`).

## 8. `agent list` pagination defaults are misleading

`--limit` and `--offset` show a default of `0` in help, but `0` means "not set" (the code uses `cmd.Flags().Changed()` to distinguish). The help gives no hint what happens when you omit them — the server picks defaults. `project list` and `context org list` have no pagination flags at all (both have `// TODO: paginate` comments).

## 9. Error messages use structured JSON even for user-facing validation errors

All errors go through `render.Error`, producing JSON like:

```json
{"error":{"code":"INVALID_FLAG","message":"--url is required"}}
```

Good for machine consumption, hostile for interactive use. There's no plain-text fallback. Validation errors should print the message string to stderr in human-friendly form.

## 10. Missing `Long` descriptions and `Example` fields on every command

Every command only has a `Short` string. None have `Long` or `Example`. This now covers 14+ commands across four groups (`login`, `agent`, `context`, `project`). Specific gaps:

- `agent get`/`project get`: doesn't explain what `<agent>`/`<project>` means (name? UUID?).
- `agent delete`/`project delete`/`context instance remove`: doesn't mention non-TTY behavior or that `--yes` is needed in scripts.
- `agent list`: doesn't explain pagination behavior or output shape.
- `project create`: doesn't explain what `<name>` constraints are (no slashes, no whitespace).
- `context` subcommands: no guidance on what "context" means conceptually or the instance/org model.

## 11. Cobra-generated errors get wrong error code and exit code

Errors raised by Cobra itself — missing positional args (`accepts 1 arg(s), received 0`), missing required flags (`required flag(s) "display-name" not set`), and unknown commands — all get `"code": "CLI_TRANSPORT"` and exit 1. These are user-input errors and should be `INVALID_FLAG` with exit 2, like the `SetFlagErrorFunc`-wrapped flag errors. The root cause is `render.asCLIError()` which falls back to `clierr.Transport` for any non-`CLIError` error. Cobra's arg validators and `MarkFlagRequired` return plain `error` values that bypass `SetFlagErrorFunc`.

## 12. Delete commands don't explain non-TTY behavior in help

All three delete commands (`agent delete`, `project delete`, `context instance remove`) silently require `--yes` when stdin isn't a terminal, but the help just says `"Skip confirmation prompt"`. Better: `"Skip confirmation prompt (required in non-interactive mode)"`.

## 13. No short-hand aliases for common flags

Only `--yes / -y` has a shorthand. Frequently-typed flags like `--org` and `--project` have no single-letter aliases (`-o`, `-p`). `--limit` and `--offset` also lack shorthands.

## 14. `project list` and `context org list` have no pagination

`agent list` exposes `--limit`/`--offset` flags, but `project list` and `context org list` fetch all results without any pagination support. Both have `// TODO: paginate` comments in the source. This is an inconsistency across list commands.

## 15. Inconsistent required-flag marking

`project create --display-name` correctly uses `MarkFlagRequired`, but `login --url` validates at runtime. This means `project create --help` shows `--display-name` as required while `login --help` shows `--url` as optional. Both patterns exist in the same CLI.

## 16. No `logout` command

`context instance remove` deletes an instance's config (including tokens), but there's no dedicated `logout` that clears only the auth tokens for the current instance. Users wanting to re-authenticate must remove and re-add the entire instance.

## 17. `context` tree is deep but undiscoverable

`amctl context org use <name>` is a 3-level command. Running `amctl context --help` only shows `show`, `instance`, `org` as subcommands with one-liner descriptions. Without Long descriptions or examples at any level, the instance/org context model is opaque to new users.

## 18. No `project update` command

`project` has `create`, `get`, `list`, and `delete`, but no `update`. Users can't modify a project's display-name or description after creation. If this is intentional, a note in `amctl project --help` would clarify.

---

**Summary:** The biggest gaps remain discoverability (no version, no examples, no Long descriptions) and flag documentation (required flags not consistently marked, defaults misleading). A notable correctness issue: Cobra-generated errors (missing args, required flags, unknown commands) get the wrong error code (`CLI_TRANSPORT` instead of `INVALID_FLAG`) and exit code (1 instead of 2). New command surfaces (`context`, `project`) inherit all the same documentation gaps and introduce their own: inconsistent pagination, deep-but-undocumented command trees, and missing CRUD operations.
