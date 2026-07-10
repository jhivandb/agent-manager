# Technical-Debt Audit: `cli/` (amctl)

**Date:** 2026-07-11 (audited at commit `949344d9`)

**Overall verdict first:** this is an unusually disciplined CLI codebase — gh-style Factory/Options pattern, a generated API client with a CI drift gate, a documented JSON error-envelope contract (`clierr`), stable exit codes (0/1/2 in `amcmd/cmd.go:52-67`), uniform `tableprinter` usage across all 15 list/get commands, atomic config writes, and 68 test files plus a dedicated e2e harness (`test/e2e/framework/amctl/`). The findings below are real, but there is no crisis here; several are "the pattern is good but applied unevenly."

---

## 1. `--env` means two opposite things across sibling subcommands

**Impact: HIGH** — direct user-facing confusion inside the same `amctl agent` command family; muscle memory from one subcommand produces wrong behavior in another.

- `--env` = *environment name*: `cmdutil/scope.go:144` (`AddEnvFlag`, used by `agent logs`, `agent metrics`, `agent traces/*` — e.g. `cmd/agent/logs.go:124`), `cmd/agent/status.go:97`, `cmd/agent/llm/set.go:83`, `cmd/agent/mcp/set.go:83`, `llm/unset.go:92`, `mcp/unset.go:92`.
- `--env` = *KEY=VALUE environment variable*: `cmd/agent/deploy.go:220` and `cmd/agent/create/create.go:166`.

So `amctl agent status --env dev` and `amctl agent deploy --env FOO=bar` are both valid, with `--env` flipping meaning between adjacent verbs. A user typing `agent deploy --env dev` gets `invalid --env "dev": expected KEY=VALUE` (deploy.go:51) rather than deploying to dev.

**Remediation: M** — rename the KEY=VALUE flag to `--set-env`/`--env-var` (or the environment-name flag to `--environment` with `--env` as deprecated alias). Breaking flag change, so needs a deprecation window; mechanical otherwise.

## 2. Build-status contract drift between OpenAPI spec and server is codified in the CLI

**Impact: HIGH** — the CLI hard-codes strings the spec did not declare; an upstream rename in openchoreo silently breaks `amctl agent deploy` with no compile-time or CI signal.

- `cmd/agent/deploy.go:366-375` — `isBuildDeployable` accepts `"Completed"`/`"Succeeded"`, with a comment admitting: *"The OpenAPI spec advertises a BuildCompleted/BuildInProgress/BuildTriggered enum, but the server emits the upstream WorkflowRun phase verbatim (see agent-manager-service/clients/openchoreosvc/client/builds.go:677-693)."* The CI codegen gate (`.github/workflows/cli-codegen-check.yaml`) verifies generated code matches the spec — but the spec itself was wrong, so the gate protected nothing.

> **Update 2026-07-11:** commit `ab1fe84c` ("Fix build status enum to match emitted workflow phases", 2026-07-10) fixed the spec side and regenerated the CLI types. The remaining work is CLI-side cleanup: switch `isBuildDeployable` from its hand-coded string list to the generated enum and delete the now-stale comment.

**Remediation: S (reduced)** — replace `isBuildDeployable`'s string list with a switch on the generated enum type.

## 3. Hand-written traces-observer client duplicates types the service already specs

**Impact: MEDIUM** — type-drift risk with zero automated detection, in contrast to the amsvc client which is generated and CI-gated.

- `pkg/clients/traceobssvc/types.go:17-18` says outright: *"handwritten client... Types mirror the opensearch/controllers shapes used by the upstream service"* — ~130 lines of mirrored structs. Yet `traces-observer-service/docs/openapi.yaml` exists and covers exactly the four endpoints the client calls (`/api/v1/traces`, `/traces/export`, `/traces/{traceId}/spans`, `/spans/{spanId}` — client.go:146-184). No `go:generate`, no workflow analogous to `cli-codegen-check.yaml` for this client.

**Remediation: M** — generate it with oapi-codegen from the existing spec (the machinery and Makefile pattern already exist for amsvc) and extend the drift-check workflow.

## 4. Server error bodies silently dropped when the spec omits a status code

**Impact: MEDIUM** — users get "server returned 400 with no JSON body" when the server *did* return a JSON error; the real message is discarded.

- `cmdutil/errors.go:80-99` — `ErrorFromServer` with `body == nil` emits `"server returned %d with no JSON body"`. `body` is nil whenever the status code isn't among the hand-picked variants passed to `FirstNonNil` (50 call sites choose variant lists manually). Example: `GetAgentConfigurationsResp` (gen/client.gen.go:20372-20378) only has `JSON404`/`JSON500`, so a 400/403/409 JSON error from the server degrades to the generic message — even though the raw body is sitting unused in `resp.Body []byte`. Only 7 call sites ever pass `JSON401`; zero pass `JSON403`.

**Remediation: S/M** — in `ErrorFromServer`, fall back to attempting `json.Unmarshal` of the raw response body into `ErrorResponse` before giving up. One function, immediately improves every command.

## 5. ~8 lines of identical request/error boilerplate per API call, ~55 times

**Impact: MEDIUM** — velocity tax and a copy-paste defect vector (finding 4 exists because variant lists are hand-maintained per call).

- The pattern `client.XWithResponse → clierr.Newf(clierr.Transport, "%v", err) → if JSON200 == nil → ErrorFromServer(resp.HTTPResponse, FirstNonNil(...))` repeats verbatim: 53 `clierr.Transport` wraps, 55 `ErrorFromServer` calls, 50 `FirstNonNil` calls, 329 `render.Error` sites across `pkg/cmd`. Representative: `cmd/agent/deploy.go:253-260, 293-300`, `cmd/context/link.go:108-135` (three consecutive copies), `cmd/project/list.go:72-78`. The RunE preamble (`ResolveScope → MakeScope → ResolveAgent → render.Error`) is likewise cloned in every command (e.g. deploy.go:202-216).

**Remediation: M** — a small generic helper (`func Call[T any](resp *T, err error) (*T, error)` over a `statusResponse` interface exposing `HTTPResponse`/`Body`, decoding errors from raw body per finding 4) would delete several hundred lines and eliminate the per-call variant lists.

## 6. Naming drift for the "env-var name" flags: `apikey-env` vs `llm-api-key-env`, `url-env` vs `llm-url-env`

**Impact: MEDIUM** — the same concept (which env var receives the injected URL/key) has different spellings depending on entry point; hurts discoverability and scripting.

- `cmd/agent/create/create.go:170-171` (`--llm-url-env`, `--llm-api-key-env`) vs `cmd/agent/llm/set.go:85-86` and `cmd/agent/mcp/set.go:85-86` (`--url-env`, `--apikey-env`). Note also `apikey-env` (no hyphen) vs `api-key` in `cmd/llmprovider/create.go:223` (hyphenated) — two hyphenation conventions for "API key" in one CLI.

**Remediation: S** — standardize on `--api-key-env`/`--url-env` everywhere; keep old names as hidden deprecated aliases.

## 7. Test gaps concentrated in exactly the stateful/flow-heavy code

**Impact: MEDIUM** — the untested areas are auth refresh, login, and context linking — the code paths that mutate `~/.amctl/config` and are hardest to verify manually.

- Src vs test files per package: `pkg/auth` (login.go, pkce.go — 0 tests), `pkg/amcmd` (exit-code dispatch — 0 tests), `pkg/cmd` root/login (3 src, 0 tests), `pkg/cmd/context` (link/unlink/show — 4 src, 0 tests), `pkg/cmd/agent/build` (5 src, 0 unit tests), `pkg/iostreams`, `pkg/tableprinter` (0 tests). By contrast `cmd/agent/create` has 9 test files, `llm`/`mcp` 7 each. Mitigation: e2e coverage exists for build (`test/e2e/operations/cli/agent/build_operations.go`) and login (`test/e2e/framework/amctl/login.go`), but token-refresh branching in `cmdutil/factory.go:180-258` (expiry buffer, grant-type switch, refresh persistence) has no test at any tier.

**Remediation: M** — the Factory is already function-field-injectable; table tests for `ensureFreshToken` and `runLink` are straightforward.

## 8. Org/project listings are unpaginated with no `--limit`, unlike sibling commands

**Impact: LOW** — silent truncation for orgs with many projects; inconsistent with `build list`/`traces list` which expose `--limit`/`--offset`.

- `cmd/project/list.go:71-72` (`// TODO: paginate`, `ListProjectsWithResponse(ctx, o.Org, &amsvc.ListProjectsParams{})`) and `cmd/context/org/list.go:72`. These are 2 of only 3 TODOs in the entire module (the codebase is otherwise TODO-clean).

**Remediation: S** — add the standard `--limit`/`--offset` flags (pattern already exists in `cmd/agent/build/list.go`) or loop pages.

## 9. OAuth secrets stored as plaintext YAML in `~/.amctl/config`

**Impact: LOW** — industry-common (kubeconfig does the same) and correctly permissioned, but `client_secret` + `refresh_token` in one flat file is a single-file credential exposure, and the config is also where non-secret state (links, orgs) lives, so users cat/edit it.

- `pkg/config/config.go:53-61` (`ClientSecret`, `AccessToken`, `RefreshToken` fields, YAML-serialized); mitigations present: dir 0700, file 0600, atomic temp+rename (config.go:160-190).

**Remediation: M** — optional OS keyring backend (as gh does), or at minimum split credentials from the general config file.

---

**Dependency hygiene (checked, no finding):** `cli/go.mod` is a separate module with a lean 7-entry require block, does *not* import the service module — types come via oapi-codegen from `agent-manager-service/docs/api_v1_openapi.yaml`, regenerated by `make amctl-gen-client` (Makefile:236) and enforced by `.github/workflows/cli-codegen-check.yaml`. This is the right architecture; findings 2-3 are the two places it leaks.

**God files (checked, no finding):** largest hand-written file is `cmd/api/api.go` at 507 lines (a deliberate `gh api`-style escape hatch); everything else is under 400 lines. No dead-code hotspots surfaced.
