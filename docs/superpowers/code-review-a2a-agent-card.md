# Code review: a2a-agent-card-ingestion

Scope: `git diff main...HEAD` (20 commits) plus uncommitted working-tree changes. Effort: max.
All 10 finder angles ran. The usage cap stopped ~15 verifiers and skipped the Phase 3 gap sweep.

Verdicts: CONFIRMED unless marked PLAUSIBLE. #13 had no completed verifier.

## Verified findings (most severe first)

1. ✅ **DONE (8b07d8458)** **Pending card that nothing resolves; console polls forever.** `agent-manager-service/services/a2a_agent_card_service.go:83`
   For a platform A2A agent with no card row, GET synthesizes `pending`, and the console re-polls every 5s while pending.
   *Scenario:* upgrade an install with an already-published internal A2A agent. Its publication row is `published` and its upstream is unchanged, and migration 047 has no backfill, so no card row ever exists. Each GET returns `{status:pending, card:null}`, and every visible tab triggers OpenChoreo GetComponent+GetEnvironment every 5s indefinitely. The same applies to never-deployed environments and to failed or waiting publications.

2. ✅ **DONE (1f625b534)** **A MarkFetched error is only logged.** `agent-manager-service/services/a2a_card_reconciler.go:140`
   No attempt is charged, no last_error is written, and no backoff is set. A card that passes `validateA2ACard` but that Postgres JSONB rejects stays `pending` and is re-fetched every 5-minute lease forever.
   *Scenario:* a card containing `\u0000`, invalid UTF-8, a lone `\ud800`, or `1e200000` fails with SQLSTATE 22P05/22021/22003. After 40 claim cycles (probe): status=pending, attemptCount=0, lastError="".

3. ✅ **DONE (3acacd9cc)** **(PLAUSIBLE in part) Platform cards go stale while still marked `fetched`.** `agent-manager-service/services/a2a_publication_reconciler.go:206`
   Cards are refreshed only after a successful gateway (re)publish. A rebuild moves the binding to the new release without notifying AMS (generate-workload.yaml ~484), so the old card stays until a manual Refetch (certain). On redeploy or promote, the service URL is already non-empty, so the single fetch can still hit the old pod (PLAUSIBLE). ClaimDue selects only pending rows, and nothing re-fetches periodically.

4. ✅ **DONE (e93af4e3f)** **Card rows can outlive their agent and be inherited.** `agent-manager-service/repositories/a2a_agent_card_repository.go:85`
   SetA2ACardSource, RefreshA2AAgentCard, and publishOne→enqueueCardFetch all upsert without re-checking the agent after DeleteAgent cleared its rows. CreateAgent doesn't check for leftovers, and Enqueue's ON CONFLICT never updates `source`.
   *Scenario:* a PUT of the source races a delete, so the row is recreated. A new agent with the same name inherits the old card and URL. If the new agent is internal, it keeps `source='external'` and keeps fetching the third-party URL. In the reverse case, a user-set URL on an inherited platform row is never fetched (probe confirmed).

5. ✅ **DONE (94f015f34)** **Changing an external URL keeps the old URL's card.** `agent-manager-service/repositories/a2a_agent_card_repository.go:87`
   Only source_url is rewritten. card, card_hash and fetched_at are kept.
   *Scenario:* A is fetched. PUT B, which 404s. GET shows `{sourceUrl:B, status:pending, card:Agent A}` for about 10 min, then `failed` with Agent A's card indefinitely. Reproduced against scratch Postgres.

6. ✅ **DONE (1f625b534)** **Non-UTF-8 last_error breaks failure recording.** `agent-manager-service/services/a2a_card_reconciler.go:192`
   `cause.Error()` is stored verbatim, and a failed MarkAttemptFailed/MarkFailed is only logged.
   *Scenario:* a URL 302-redirects to `http://%FF/x`, and the DNS error text contains `\xff`. The UPDATE fails with SQLSTATE 22021, and the row loops `pending` forever. Reproduced via httpbin redirect-to.

7. ✅ **DONE (0ab786e4f)** **The URL form treats "loading" and "error" as "no URL registered".** `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.tsx:79`
   `showUrlForm = external && !sourceUrl`. On first load (at least 1s), or after a non-404 GET error, the empty form shows and Remove is hidden. Saving from it silently overwrites a URL the user never saw.

8. ✅ **DONE (0ab786e4f)** **A 404 on refetch leaves stale data and polling.** `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.tsx:77`
   TanStack keeps the previous data on error. After the source is removed elsewhere, the panel shows the stale card and URL next to "Register this agent's card URL", with no form and no Refetch. If the stale status was pending, it keeps polling the 404 every 5s (query-core 5.90.5 probe).

9. ✅ **DONE (9fd611acf)** **4 of 6 panel tests fail at HEAD.** `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.test.tsx:113`
   Commit e4f5096cd changed the panel without updating these tests. Causes: the button text changed from /refresh/ to "Refetch", the status chip is hidden when fetched and the date chip now reads "Fetched <date>", save now takes a second `{onSuccess}` argument, and the URL form renders only when `!sourceUrl`. Console CI doesn't run vitest.
   Repro: `cd console/workspaces/pages/overview && npx vitest run src/AgentOverview/EnvAgentCardSection.test.tsx`

10. ✅ **DONE (0ab786e4f)** **SSRF/DNS error detail leaks in 400s and last_error.** `agent-manager-service/controllers/a2a_agent_card_controller.go:113`
    Messages like "resolves to a non-public IP address" and "lookup nope.internal on 10.96.0.10:53" make an internal-DNS oracle and expose the resolver address. URL validation also runs before the production-tier check. DiscoverOidc sanitizes the same class of error.

11. ✅ **DONE (0ab786e4f)** **Card validation is case-insensitive.** `agent-manager-service/services/a2a_card_fetcher.go:152`
    encoding/json matches keys case-insensitively, so `{"Name":..,"SupportedInterfaces":..,"Skills":..}` passes and is stored as `fetched`. The console reads keys case-sensitively and renders an empty card.

12. ✅ **DONE (0ab786e4f)** **No test proves tenant URLs use the guarded client.** `agent-manager-service/services/a2a_card_fetcher_unit_test.go:125`
    The only guarded-mode test is rejected by the `ssrf.ValidateURL` pre-check before a client is chosen. Swapping `client = f.guarded` for `f.plain` (a2a_card_fetcher.go:100) keeps all 1311 tests green.

13. ✅ **DONE (0ab786e4f)** **(PLAUSIBLE, unverified) A batch can outlast the 5-minute claim lease.** `agent-manager-service/services/a2a_card_reconciler.go:119`
    *Verification:* confirmed. V15(a) is wrong: `RetryableHTTPClient` applies a 30s AttemptTimeout, so nothing hangs forever. But that makes the lease worse: one OpenChoreo call can take ~2 min (4 × 30s plus backoff), each platform row made 3 list calls, and the guarded DNS pre-check ran on the un-deadlined RunOnce ctx. Fixed with a 30s per-attempt deadline and a batch deadline 1 min inside the lease.
    Up to 50 rows are processed sequentially, and the OpenChoreo endpoint lookup and the guarded DNS pre-check have no per-row deadline. 50 × 5s = 250s is already most of the 300s lease before the retried OpenChoreo calls. Another replica can then re-claim and double-fetch the tail rows.

14. ✅ **DONE (0ab786e4f)** **Refresh-vs-remove race creates an empty-URL external row.** `agent-manager-service/services/a2a_agent_card_service.go:105`
    The plain SELECT and the upsert are separate statements. A DELETE landing in between makes the upsert insert `{source:external, source_url:''}`, which sits Pending, then Failed with "parse \"\": empty url", with Remove hidden.

15. ✅ **DONE (0ab786e4f)** **Mutation resolves before the card refetch finishes.** `console/workspaces/libs/api-client/src/hooks/agent-card.ts:81`
    *Fix note:* scoped to the card hooks (the invalidation is awaited inside mutationFn). Returning the promise from the shared `useApiMutation` would change timing for all 124 console mutations; that is left as a separate decision.
    `useApiMutation` (react-query-notifications.ts:392) drops the promise returned from onSuccess. The inline editor closes while the old sourceUrl still shows, and a double click on Save sends a duplicate PUT and a second `agent:set-card-source` audit record. Returning the promise fixes it.

## Verified, cut by the 15-item cap

**Low-severity correctness**
- ✅ **DONE (0ab786e4f)** External subtype is never validated, so a typo like "A2A-agent" quietly creates a non-A2A agent.
- ⏭️ **SKIPPED** (PLAUSIBLE) `pinnedDial` drops the URL's port and scheme. This only matters with TLS or a non-19080 ingress.
- ✅ **DONE (0ab786e4f)** (PLAUSIBLE, confirmed) `sameHostRedirectOnly` refuses same-host http→https upgrades.
- ✅ **DONE (0ab786e4f)** A card version that already starts with "v" renders as "vv".
- ✅ **DONE (0ab786e4f)** The server caps URL length in bytes, while the console and OpenAPI count characters.
- ✅ **DONE (0ab786e4f)** An ad-hoc URL regex enables Save for "https://", and the inputs have no maxLength. `validateEndpointUrl` and `INPUT_LIMITS.URL` already exist.

**Efficiency**
- ✅ **DONE (0ab786e4f)** One redundant ListWorkloads call per platform attempt.
- ✅ **DONE (0ab786e4f)** `RETURNING *` ships the stored card JSONB on every claim.
- ✅ **DONE (0ab786e4f, folded into #11)** `validateA2ACard` decodes the body twice.
- ✅ **DONE (0ab786e4f)** GET calls OpenChoreo GetComponent before the DB read on every poll.

**Cleanup**
- ⏭️ **SKIPPED** The `card_hash` machinery saves almost nothing, since JSONB already normalizes.
- ⏭️ **SKIPPED (deferred to M3)** `ListForAgent` is unused (deferred to M3 in the plan).
- ⏭️ **SKIPPED** The queue repository and reconciler skeleton are copied from the publication queue.
- ⏭️ **SKIPPED** The option-card selector now exists in a third copy.
- ✅ **DONE (0ab786e4f)** Guard calls are duplicated in the card JSX, which also needs an `as string` cast.

### Skip reasons
- `pinnedDial`: applies only when `A2A_CARD_FETCH_DIAL_ADDR` is set (local dev), where pinning one address regardless of the URL port is the point. TLS and scheme are handled by the transport above `DialContext`, so the scheme is not actually dropped.
- External subtype: kept free-form by design (`TestBuildExternalAgentComponentRequestBody_FreeFormSubTypeNotLabelled`), so only miscased `a2a-agent` is rejected rather than adding a whitelist.
- `card_hash`: removing it needs a migration plus repo and reconciler changes, so it's not a small cleanup.
- Queue/reconciler dedup: a shared-generic refactor across two subsystems, not contained.
- Option-card selector: the other two copies are pre-existing create-agent forms from main.

## Refuted
- The uncommitted `config.js` dropping `resource` loses no scope, because Thunder's default resource server is `urn:wso2:amp`.
- "The environment isn't checked against the project pipeline": sibling APIs use the same org-level check, and the spec asks for it.

## Other notes
- The uncommitted Go diffs are formatting only.
- The regenerated CLI client renamed 5 unused enum constants (gofumpt/oapi-codegen side effects).
- CLI build, vet, and the A2A Go unit tests pass.

## Candidates V6–V46: verified by triage 2026-10-07 (section B still unverified)

Each summary below is the finder's claim. Section A entries now carry a triage verdict, checked by reading code at HEAD 3acacd9cc (no runtime probes). Section B is still unverified.

### A. Verifier stopped before reporting (now triaged)

- ✅ **DONE (e93af4e3f)** **V6. Upsert never updates `source`.** `repositories/a2a_agent_card_repository.go:85`
  Enqueue's ON CONFLICT leaves `source` out, so a row left over from a deleted agent sends the reconciler down the wrong fetch path. For example, a deleted internal agent "planner" is re-created as external: the platform branch fails 14 times and the registered URL is never fetched. The V43 probe incidentally showed `source=external` staying in place after an internal agent's enqueue. (Overlaps finding 4.)
  - **Triage (HEAD 3acacd9cc):** ALREADY FIXED (e93af4e3f). Enqueue's ON CONFLICT now sets `source` (`repositories/a2a_agent_card_repository.go:89`), and `a2aCardSourceChanged` (:103) clears card/hash/fetched_at/release_name when the source flips.
- **V15. A sequential batch can outlast the lease.** `services/a2a_card_reconciler.go:119`, lease at `repositories/a2a_agent_card_repository.go:65`
  - (a) The OpenChoreo client is a bare `&http.Client{}` with no Timeout, so an OpenChoreo that accepts the connection and never responds can hang `RunOnce` forever.
  - (b) On 5xx, each platform row spends ~7s in retries, so 50 rows take ~12 min. Tail leases expire and another replica double-fetches them.
  - The guarded DNS pre-check runs outside the 5s timeout.
  - Another angle claims each attempt has a 30s AttemptTimeout. Which claim is right is unresolved. (Expands finding 13.)
  - **Triage (HEAD 3acacd9cc):** CONFIRMED in part (efficiency/correctness, M), within #13. (a) is REFUTED: the bare `&http.Client{}` (`clients/openchoreosvc/client/client.go:291`) is wrapped by `requests.NewRetryableHTTPClient` with a 30s per-attempt `DefaultAttemptTimeout` (`clients/requests/retry_config.go:30`, `retryable_http_client.go:115`), so nothing hangs forever. Hung OpenChoreo costs about 2 min per call (4×30s plus backoff). (b) holds: only 429/502/503/504 are retried (`client.go:278-287`), with 3.5–7s of backoff per call and 2 calls per row (`a2a_card_reconciler.go:173,194`). That is 7–14s per row, so a 50-row batch takes 6–12 min against a 5-min lease (`a2a_agent_card_repository.go:66`). `RunOnce` (:120) has no batch deadline. The DNS pre-check (`a2a_card_fetcher.go:97`) uses the reconciler ctx, outside the 5s `Client.Timeout`.
- ✅ **DONE (e6f021501)** **V21. Card rows survive environment deletion and undeploy.** `db_migrations/047_create_a2a_agent_cards.go` (unique key), `services/agent_manager.go:2849`
  Rows are keyed by env name with no environment_uuid. A re-created "staging" inherits the old card and URL, and an undeployed platform agent still shows Fetched.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (correctness, M). The unique key is `(ou_id, project_name, agent_name, environment_name)` with no env UUID (`db_migrations/047_create_a2a_agent_cards.go:53`). `DeleteEnvironment` (`services/environment_service.go:302-345`) and the undeploy path `UpdateAgentDeploymentState` (`services/agent_manager.go:5952`) never touch `a2a_agent_cards`; only agent deletion does (:2841, :2871). *Scenario:* delete and re-create "staging", and the external agent shows the old URL and card. Platform agents mostly hit this via undeploy, since OpenChoreo blocks env delete while bindings exist (`environment_service.go:339-341`).
- **V25. Every GET calls OpenChoreo GetComponent before the DB read.** `services/a2a_agent_card_service.go:62`
  That's ~125 calls per tab per pending cycle at 5s polling. Reading `cardRepo.Get` first would be cheaper.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (efficiency, S). This is a duplicate of the cut-list item "GET calls OpenChoreo GetComponent before the DB read"; track it there. `getAgent` runs at `services/a2a_agent_card_service.go:62`, the subtype gate at :66, and `cardRepo.Get` only at :69. *Caveat:* the subtype gate also stops non-A2A/stale-row reads, so read the row first and only call `getAgent` on a miss or to gate.
- **V28. Redundant DNS pre-check on guarded fetches.** `services/a2a_card_fetcher.go:97`
  The URL was already validated when it was set, and the client re-checks at dial time and on every redirect. The pre-check doubles DNS lookups with no deadline. Deleting lines 97–99 kept all 9 fetcher tests green.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (cleanup/efficiency, S). `ssrf.ValidateURL` at `services/a2a_card_fetcher.go:96-99` repeats the guarded client's dial-time check (`utils/ssrf/ssrf.go:193-200`) and redirect check (:177-182). Both map to the same message (fetcher.go:112). It adds one DNS lookup outside the 5s timeout. The "9 tests stay green" claim was not re-run.
- **V32. A redundant useEffect can overwrite typing.** `console/.../AgentCardSourceForm.tsx:43`
  It fires when sourceUrl changes while the inline editor is open, clobbering the input. The inline and block editors have also drifted apart: Enter/Escape work only inline, and the invalid hint is a Tooltip in one and helperText in the other.
  - **Triage (HEAD 3acacd9cc):** PLAUSIBLE (cleanup, minor correctness, S). The effect at `AgentCardSourceForm.tsx:43` fires only while the inline editor (`EnvAgentCardSection.tsx:96-101`) is open and `sourceUrl` changes elsewhere: another user or tab saves, and the poll clobbers the half-typed input. The block form renders only when `!sourceUrl` (:79,133). Editor drift is real: Enter/Escape work only inline (:62-65), and the hint is a Tooltip (:69) in one but helperText (:93) in the other. *Fix:* delete the effect.
- **V33. Unused exported card types.** `console/workspaces/libs/types/src/api/agent-card.ts:24`
  `AgentCard`, `AgentCardInterface` and `AgentCardSkill` have no users, and the panel reads the card as untyped JSON. They promise a shape the code ignores, and the test needs `as never`. `Record<string, unknown>` would do.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (cleanup, S). `AgentCard*` at `libs/types/src/api/agent-card.ts:24-45` are used only at :52. The panel reads untyped JSON (`EnvAgentCardSection.tsx:41-46,75-76`), and the test casts `as never` (`EnvAgentCardSection.test.tsx:164`). *Fix:* type it as `Record<string, unknown>`.
- **V38. Hand-rolled copy feedback.** `console/.../EnvAgentCardSection.tsx:63`
  This panel shows "Copied" for 1500ms, the shared component shows "Copied!" for 2000ms, and the panel's own CodeBlock uses the shared version. The shared copyable TextInput should be reused.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (cleanup, S). The panel shows "Copied" for 1500ms (`EnvAgentCardSection.tsx:63-70,191`). The shared versions show "Copied!" for 2000ms (`CodeBlock.tsx:67,88`, `SingleAPIKeyManager.tsx:103,123`), and the panel also uses CodeBlock (:228), so both behaviours sit side by side. The timer isn't cleared on unmount.
- **V39. A fetched card is never re-checked.** `repositories/a2a_agent_card_repository.go:126`
  `next_attempt_at` is NULL and ClaimDue takes only pending rows. External cards that change outside AMS go stale forever, and undeployed agents keep a Fetched card. No periodic re-fetch is a spec decision, but the undeploy case is outside it.
  - **Triage (HEAD 3acacd9cc):** PARTLY FIXED (3acacd9cc). The rest is CONFIRMED (correctness, S–M). Platform cards now re-enqueue on a new release and wait for `rollout.Serving` (`a2a_card_reconciler.go:171-190`). `ClaimDue` still takes only pending rows (`a2a_agent_card_repository.go:131`). Still open: (1) external cards are never re-fetched, which is a spec decision; (2) undeploy (`agent_manager.go:5952`) leaves a Fetched card. (2) overlaps V21.
  - **(2) DONE (e6f021501):** undeploy drops the platform card row; a user-registered external URL is kept. (1) stays open as a spec decision.
- **V40. The A2A endpoint is chosen twice with different rules.** `services/a2a_card_reconciler.go:174`
  The card uses the alphabetically first workload endpoint with an ExternalURL. The publication uses the first binding endpoint with a ServiceURL. They could diverge with more than one endpoint, but the Reuse finder says AMS creates exactly one today.
  - **Triage (HEAD 3acacd9cc):** PLAUSIBLE, latent (cleanup, S). The card uses the first ExternalURL endpoint (`a2a_card_reconciler.go:198`, via `components.go:2756-2790`). The gateway uses the first binding ServiceURL (`deployments.go:1830`, used at `a2a_publication_reconciler.go:323`). These can diverge only with more than one endpoint, and AMS creates one. *Fix:* share one selector.
- **V41. Tests build services as struct literals.** `services/a2a_agent_card_service_unit_test.go:54`, `a2a_card_reconciler_unit_test.go:79`, `a2a_agent_deletion_unit_test.go:294`, `a2a_publication_reconciler_test.go:120`
  This bypasses the NewXxx constructors, against the add-service-unit-test skill rule. Constructor wiring goes untested, and the nil `stopCh` would panic on `Stop()`. The last two files extend a pattern that already existed on main.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (cleanup, S). See `a2a_agent_card_service_unit_test.go:55`, `a2a_card_reconciler_unit_test.go:85` (no `stopCh`), `a2a_agent_deletion_unit_test.go:212` and `a2a_publication_reconciler_test.go:129`. The last two follow a pattern already on upstream/main. The `Stop()` panic (`close(nil)` at `a2a_card_reconciler.go:99`) is latent, since no test calls `Stop()`.
- **V42. Query key shape.** `console/workspaces/libs/api-client/src/hooks/agent-card.ts:36`
  The path params are spread into the key instead of the skill's `[domain, params, query]` shape. Other hooks already do the same, and prefix invalidation still works.
  - **Triage (HEAD 3acacd9cc):** REFUTED as a defect (convention drift only). The key `["agent-card", org, proj, agent, env]` (`hooks/agent-card.ts:35-36`) differs from the skill's `[domain, params, query]`, but other hooks spread their params too (`span-detail`), and invalidation and remove both use `agentCardKey` (:40,70,82).
- **V44. Spurious A2A deletion broadcast.** `clients/openchoreosvc/client/components.go:3079`
  With the internal-only guard removed, deleting an external `agent-api`/`a2a-agent` agent makes `isA2AAgent` true (`agent_manager.go:2664`), so every gateway in the org gets a delete event for an Agent none of them ever held.
  - **Triage (HEAD 3acacd9cc):** REFUTED. `isA2AAgent` is true for external agents, but the broadcast sits inside `deleteAgentAPIArtifact`'s per-env loop after `artifactRepo.GetByHandle` (`agent_manager.go:2898-2905`), which `continue`s on not-found. Only `ensureAgentEnvAPIArtifact` creates that artifact, on internal create and deploy paths (:1467/:1614), so external agents never broadcast.
- **V45. README overstates the gateway's card rewrite.** `samples/a2a-agent/README.md:328`
  It claims every `supportedInterfaces[].url` is rewritten. The branch's own design docs say unfronted and non-1.0 entries aren't, a version mismatch fails the whole rewrite, and securitySchemes aren't rewritten either.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (docs, S). `samples/a2a-agent/README.md:328-330` says "every". The branch's specs say unfronted or version-mismatched bindings stay unrewritten, a mismatch fails the whole rewrite (`docs/superpowers/specs/2026-10-07-a2a-external-agent-proxy-design.md:70,72`), and securitySchemes aren't rewritten. The claim is mostly true for this single-binding sample.
- **V46. The regenerated CLI client renamed 5 enum constants.** `cli/pkg/clients/amsvc/gen/types.gen.go:446`
  `DeploymentEndpointVisibility{Internal,Private,Public}` became `{Internal,Private,Public}`, and `Deployed`/`Undeployed` became `MCPEndpointEnvironmentDeploymentStatus*`. Out-of-tree code or cherry-picks would break. Nothing in-repo uses the old names, the CLI builds and vets clean, and oapi-codegen v2.6.0 reproduces the output.
  - **Triage (HEAD 3acacd9cc):** CONFIRMED (cleanup, S). The rename is real against upstream/main, and nothing in `cli/` outside `gen/` uses the old names. The bare `Internal`/`Private`/`Public` names are collision-prone. *Fix:* pin the names with `x-enum-varnames`, or call the rename out in the PR.

### B. Folded into a verified finding, but this point was never checked

- **A second local-dev dial workaround.** `services/a2a_card_fetcher.go:66`, `:76`
  `A2A_CARD_FETCH_DIAL_ADDR` plus `pinnedDial` duplicates the existing `IS_LOCAL_DEV_ENV` dial override in thundersvc (`naming.go:236-240`, `client.go:171-176`) and compose's `extra_hosts`. A compose setup with only `IS_LOCAL_DEV_ENV` leaves platform cards pending, then failed. (V11 checked only the port/scheme part.)
- **"No card row" is reported two ways.** `console/workspaces/libs/api-client/src/hooks/agent-card.ts:93`
  It's a synthetic 200 pending for platform agents but a 404 for external ones, and the 404 also covers non-A2A and missing agents. Only the DELETE mutation clears the cache, so every other route shows stale data. The suggested fix is a 200 with an explicit "unconfigured" status. (V13 confirmed the symptom, not this API-shape fix.)

## Triage results: ranked, with fix effort

| # | Candidate | Verdict | Effort | Fix |
|---|---|---|---|---|
| 1 | V21 + V39 remainder | CONFIRMED | M | A migration for `environment_uuid` (or another env-identity key) plus a backfill. Delete card rows in `DeleteEnvironment` and on undeploy (`agent_manager.go:5952`). Tests for both paths. | **DONE (e6f021501).** `environment_uuid` added to 047 (unreleased, 048 folded in) and made the unique key; Enqueue replaces a same-named row from a deleted env; DeleteEnvironment drops the env's rows; undeploy drops platform rows only. Reads stay by name (no OC call per poll).
| 2 | V15 (under #13) | CONFIRMED in part | M | A per-row deadline in the reconciler, plus a batch time cap or a lease extension mid-batch. Test with a slow fake OpenChoreo. | **DONE (0ab786e4f, with #13).**
| 3 | V25 (= cut-list item) | CONFIRMED | S | Read the DB first in `GetA2AAgentCard` and call `getAgent` only on a miss. Keep the A2A subtype gate on the hit path too. | **DONE (0ab786e4f).**
| 4 | V28 | CONFIRMED | S | Delete `a2a_card_fetcher.go:96-99` and re-run the fetcher tests. | **DONE (88bef7015):** pre-check removed (user-authorized); `TestA2ACardFetcherGuardedModeRejectsLoopback` now covers `127.0.0.1` and `localhost` and fails if `f.guarded` is swapped for `f.plain` (verified).
| 5 | V32 | PLAUSIBLE | S | Delete the `useEffect`. Optionally unify Enter/Escape and the hint across both editors. | **DONE (53e288881).** Editor unification not done.
| 6 | V45 | CONFIRMED | S | Qualify one README sentence. | **DONE (53e288881).**
| 7a | V33 | CONFIRMED | S | Delete 3 interfaces, use `Record<string, unknown>` and drop the `as never` cast. | **DONE (53e288881).**
| 7b | V38 | CONFIRMED | S | Use the shared copy component. | **DONE (88bef7015):** interface URLs render in the shared copyable `TextInput` ("Copied!", 2000ms).
| 7c | V41 | CONFIRMED | S | Use the constructors in the 2 new test files. The older ones follow an existing main pattern. | **DONE (53e288881):** the reconciler test already used its constructor. Go tests could not be run here (blocked), so this is unverified.
| 8 | V46 | CONFIRMED | S | Add `x-enum-varnames` and regenerate, or note the rename in the PR. | **DONE (88bef7015) for Visibility:** `x-enum-varnames` restores `DeploymentEndpointVisibility*`. **LEFT for Deployed/Undeployed:** oapi-codegen v2.6.0 prefixes them as `MCPEndpointEnvironmentDeploymentStatus*` even with `x-enum-varnames`, because they collide with the `LLMProvider{ListItem,Response}StatusDeployed` values. PR note: these two constants are renamed; nothing in-repo uses the old names.
| 9 | V40 | PLAUSIBLE (latent) | S | Share one endpoint selector. This can wait until agents can have more than one endpoint. | **LEFT (latent).**

Not worth fixing: V6 (ALREADY FIXED, e93af4e3f), V42 (REFUTED, style only), V44 (REFUTED).

Only items 1 and 2 are substantive. Items 3–9 total a few hours and fit in one or two cleanup commits.
