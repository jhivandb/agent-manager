# A2A Milestone 2: External A2A Agents, Gateway-Proxied Mode

**Date:** 2026-10-07 (revised 2026-10-09)
**Status:** Draft for review. Revised 2026-10-09 after a code review against the branch and the gateway source. All decisions below are agreed.
**Scope:** agent-manager-service (AMS), gateway extension Helm chart, console
**Builds on:** `2026-10-02-a2a-agent-card-ingestion-design.md` (card store, card worker, external A2A agents in direct mode; PR #1990) and Milestone 1 (PR #1845: gateway `kind: Agent` publication)
**Tracking:** wso2/agent-manager#1989, Phase 3
**Checked against:** branch `a2a-external-agent-proxy` at `bc19012cc` (card ingestion plus its review fixes); gateway `wso2/api-platform` main `9a373ef`, `wso2/gateway-controllers` main `ac54194`.

## Goal

An external A2A agent can be published through an environment's **ingress gateway**, so anyone can invoke it exactly as they invoke a platform A2A agent: same gateway, inbound auth (API key or OAuth), API keys, CORS and observability. Its stored card advertises the gateway's URLs.

Direct mode (card ingestion spec) is unchanged: the platform stores the card, and callers reach the agent at its own address. The mode is chosen per environment.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Who calls a proxied agent | Anyone, through the environment's ingress gateway | Same model as platform A2A agents, so M3 hands out one kind of endpoint. **Rejected:** an egress-only proxy for platform agents. Egress proxies are consumed through static agent-to-proxy bindings (env vars on the release binding), which cannot serve M3's runtime discovery. |
| Upstream | Derived from the registered card | One URL serves both modes. **Rejected:** a hand-entered upstream plus per-protocol paths; more to fill in and easy to get wrong. |
| Upstream credentials | One static header via the Agent kind's `spec.upstream.auth: {type: api-key, header, value}` | Confirmed in the gateway (`gateway-controller/pkg/transform/agent.go:1094`): the gateway turns it into a `set-headers` policy on every A2A operation chain **and** on the passthrough card fetch, so an origin that needs auth to serve its card still works. **Rejected:** emitting our own `set-headers` policy in `a2a.operationConfigs.policies` (MCP's approach); it never reaches the card fetch. OAuth2 client credentials deferred: the gateway accepts `type: oauth2` on Agent but silently ignores it. |
| Owner | A new `a2aProxyService`, plus a shared `a2aPublisher` helper used by it and `agentManagerService` | Proxying is a gateway-publication concern, not a card concern. Keeps the proxy lifecycle out of the 6,000-line `agent_manager.go`, and the shared helper keeps one copy of the "should this environment publish?" rule. **Rejected:** putting it in the card service (it cannot reach artifacts, agent config, the publication queue or gateway events); putting it in `agentManagerService` (has every dependency, but buries the lifecycle and inflates its 19-argument constructor). |
| Proxy storage | Own table `a2a_proxies`; a row exists ⇔ the environment is proxied | The card table is a work queue with many write paths (`Enqueue`, `Requeue`, `Mark*`, `ClearCard`, three deletes), and several card-review findings were one path clobbering a column it didn't own. A credential and a mode flag don't belong among them. **Rejected:** columns on `a2a_agent_cards` (couples removing the card source to removing the proxy and its secret). |
| Proxy API | Own resource `…/environments/{envID}/a2a-proxy` | Each controller calls the one service that owns its table; the credential-changing PUT is always fail-closed; the card API stays as in PR #1990. **Rejected:** extending `PUT …/agent-card/source` with a `proxy` block (the card controller would have to coordinate two services in one request). Cost: proxying an environment with no card URL yet takes two calls, card source then proxy; a failure between them leaves a valid direct-mode environment. |
| Pipeline | M1's publication queue and worker, with a second way to resolve the upstream | One gateway publication path: no second queue, retry loop, delivery or deletion. |
| Environment identity | Environment UUID, everywhere this spec touches | Card rows moved to UUID keys in e6f021501; publications move too (migration 048), so a re-created environment of the same name never inherits proxy state. |
| Public route | The gateway's own host, through the catch-all route the gateway chart installs on kgateway (`gateway-kgateway-route.yaml`), with its request timeout **removed** (`0s`) | External agents have no OpenChoreo route to patch: they are `autoDeploy: false` components with no workload and no release binding. LLM and MCP proxies are reached the same way. The chart's 30s cap would cut A2A streams; the gateway's own 5m idle timeout stays as the liveness guard. |
| Mode granularity | Per environment | Dev can stay direct while prod is proxied. |
| Origin polling | None. Refresh re-derives the routes | Periodic re-fetch is deferred for the milestone. |
| Card URLs | The gateway's passthrough rewrite: a card fetched through the gateway advertises gateway URLs | No `gatewayEndpoint` field is added to the card API, for platform or proxied agents. |
| Delivery order | Backend first, console after, same PR | |

## Flow

For an environment switched to proxied:

1. The environment already has a registered card URL (`PUT …/agent-card/source`, card ingestion spec). `PUT …/a2a-proxy` enables the proxy. In one DB transaction, `a2aProxyService`:
   - inserts the `a2a_proxies` row;
   - ensures the environment's agent artifact;
   - inserts default agent config if none exists;
   - clears and **parks** the card row;
   - queues a publication row with `source=external`.

   It then confirms the agent still exists.
2. The publication worker claims the row and loads the `a2a_proxies` row and the registered card URL. It fetches the origin card with the SSRF-guarded fetcher and derives the upstream and routes. It then:
   - builds the `kind: Agent` (routes, upstream auth, inbound policies from agent config);
   - writes the deployments row and sends the event to the ingress gateway;
   - marks the row published and un-parks the card row.
3. The card worker fetches `{gateway host}/{agent}/.well-known/agent-card.json` and stores the gateway's card, whose interface URLs point at the gateway. It does **not** overwrite the registered origin URL.
4. Callers reach `{gateway host}/{agent}/…`. The catch-all route delivers to the gateway, which forwards to the origin with the upstream header added.

## Data model (migration 048)

Settings that face the origin (is it proxied, the upstream credential) live in `a2a_proxies`. The origin URL stays on the card row (`a2a_agent_cards.source_url`), where direct mode already keeps it. Settings that face callers stay in `agent_configs` (API key or OAuth, CORS, card CORS), as for platform A2A agents.

New table `a2a_proxies`, one row per proxied agent and environment:

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `ou_id`, `project_name`, `agent_name` | VARCHAR(255) NOT NULL | |
| `environment_uuid` | UUID NOT NULL | `UNIQUE (ou_id, project_name, agent_name, environment_uuid)` |
| `environment_name` | VARCHAR(255) NOT NULL | For lookups by the name in the URL path, as on the card row |
| `upstream_auth_header` | VARCHAR(255) NOT NULL DEFAULT '' | Empty means no upstream auth |
| `upstream_auth_secret` | TEXT NOT NULL DEFAULT '' | AES-256-GCM ciphertext, base64, under `ENCRYPTION_KEY` (`utils.EncryptBytes`), the same inline format LLM and MCP secrets use. Goes straight into the Agent YAML as `secretRef`. Never returned. |
| `created_at`, `updated_at` | TIMESTAMPTZ NOT NULL DEFAULT NOW() | |

The row existing means the environment is proxied; deleting it means direct. There is no flag to keep in sync. Repository `A2AProxyRepository` (`repositories/a2a_proxy_repository.go`) provides:

- `Get(ou, project, agent, envName)` and `GetByEnvUUID(ou, project, agent, envUUID)`;
- `Upsert(…)`, which deletes a same-named row from an earlier environment in the same transaction, as card `Enqueue` does;
- `Delete(ou, project, agent, envUUID)`, `DeleteForAgent(ou, project, agent)` and `DeleteForEnvironment(ou, envUUID)`;
- a moq mock.

`a2a_publications`:

| Change | Notes |
|---|---|
| Add `source VARCHAR(32) NOT NULL DEFAULT 'platform'` | `platform` \| `external`. Existing rows take the default. Added to `Enqueue`'s `ON CONFLICT DO UPDATE` list so a re-enqueue can change it. |
| Re-key: drop `uq_a2a_publications_agent_env (…, environment_name)`, add `UNIQUE (ou_id, project_name, agent_name, environment_uuid)` | Same move as `a2a_agent_cards` in 047. `Enqueue` gains the cards' same-transaction delete of a same-named row whose `environment_uuid` differs (`repositories/a2a_agent_card_repository.go:167-184`). Any pre-048 rows already carry `environment_uuid`, so no backfill is needed. |

The publication repository gains `DeleteForAgentEnv(ou, project, agent, envUUID)` and `DeleteForEnvironment(ou, envUUID)`, and `FindPublished` gains the `source = 'platform'` filter.

`a2a_agent_cards` gets no new columns. It gains only the repository changes below (`ClearCard`, and `MarkFetched` keeping the external `source_url`).

## Components

### Shared publication helper (`services/a2a_publisher.go`, new)

The A2A publication primitives move out of `agentManagerService` (`services/agent_manager.go:6209-6270`), which then delegates to them:

- `Enqueue(ctx, key, source, artifactUUID)`: today's `enqueueA2APublication`, plus `source`.
- `ShouldPublish(agent, envUUID) bool`: true for internal A2A agents and for external A2A agents whose environment has an `a2a_proxies` row (the helper reads `A2AProxyRepository`). `republishA2AAgent` calls it. Today it checks only the subtype (`:6255-6270`), and direct external agents are kept out only because every caller rejects them (`:1670`, `:3076`, `:4264`, `:5028`). Deploy settings stops rejecting them (below), so the check moves inside.
- `TeardownEnv(ctx, agent, envUUID)` covers one environment. It:
  - collects gateway targets for the environment's artifact (`collectA2AAgentDeletionTargets`, `services/a2a_agent_deployment.go:236`);
  - deletes that artifact's `deployments` rows (`deployment_status` cascades);
  - deletes the publication row;
  - sends `sendA2AAgentDeletion` (`:293`) after the DB work commits.

  It **keeps the artifact**, so API keys (which cascade from `artifacts`) survive a mode switch.

### `a2aProxyService` (`services/a2a_proxy_service.go`, new)

Owns the proxy lifecycle: enable/update, disable, refresh, status.

- **Dependencies:**
  - `A2AProxyRepository`
  - `cardRepo`
  - `a2aPublisher`
  - `db` and `artifactRepo` (for `ensureAgentEnvAPIArtifact`, `services/agent_api_artifact.go:34`)
  - `agentConfigRepo`
  - `ocClient` (agent lookup, `requireEnvironmentTier`)
  - the encryption key
  - `logger`
- **Atomicity:** each transition's DB writes run in one transaction. Gateway deletion events are sent after commit. After commit the service re-checks that the agent still exists and removes what it wrote if not, the same pattern as the card service's `dropIfAgentGone` (`services/a2a_agent_card_service.go:184-199`).
- **Default agent config:** a new `ensureDefaultAgentConfig(env)` writes `resolveAPIConfig(nil, …, withDefaults=true)` (`services/agent_manager.go:3596`), which turns API-key security on and sets CORS from environment config, plus the card CORS default. It writes with **insert-if-absent**, so it never overwrites a saved config. No reusable helper exists today: `persistInstrumentationConfig` (`:883-929`) is tied to the lowest environment and internal create. The publication worker treats a missing config row as a retryable error (`a2a_publication_reconciler.go:340`), so it must exist before the publication is queued.

**Transitions:**

| From → to | Actions |
|---|---|
| direct → proxied (`PUT …/a2a-proxy`, no row yet) | Insert the `a2a_proxies` row; `ensureAgentEnvAPIArtifact`; `ensureDefaultAgentConfig`; `ClearCard` (parks the row; requires a registered card URL, so the row exists); `publisher.Enqueue(source=external)`. |
| proxied → proxied (`PUT …/a2a-proxy`, auth changed) | Update the row; `publisher.Enqueue`. The card is kept and replaced by the re-fetch that follows the publish. |
| proxied, card URL changed | The card service's URL change re-enqueues a publication instead of a direct card fetch. |
| proxied → direct (`DELETE …/a2a-proxy`) | `publisher.TeardownEnv`; delete the `a2a_proxies` row; `ClearCard`; queue a direct card fetch. |
| Card source removed (`DELETE …/agent-card/source`) on a proxied environment | As proxied → direct, then delete the card row instead of fetching. The card service calls into `a2aProxyService` for this. |

**Refresh** on a proxied environment queues a publication; the card fetch follows the publish. Both `POST …/a2a-proxy/refresh` and the card's `POST …/agent-card/refresh` do this. The card refresh must branch **before** its external path, which calls `cardRepo.Requeue` (`a2a_agent_card_service.go:96-103`). `Requeue` makes the row due and would un-park it while the gateway may not have the Agent yet.

**Card service → proxy service.** Three card-service operations behave differently on a proxied environment: changing the URL, removing the source, and refresh. The card service gains one narrow dependency, an `A2AProxyHooks` interface implemented by `a2aProxyService`, with three methods:

- `IsProxied(ctx, agent, envUUID)`;
- `OnCardSourceChanged`, which republishes instead of fetching directly;
- `OnCardSourceRemoved`, which tears down as proxied → direct.

The proxy service does not depend on the card service; it uses `cardRepo` directly. Each table keeps one writing owner, apart from the card queue state the proxy parks and requeues.

### Route derivation (`services/a2a_proxy_routes.go`)

`deriveA2AProxyRoutes(cardURL string, card json.RawMessage) (upstreamURL string, transports []A2ATransport, err error)`, a pure function:

- `cardURL` must end in `/.well-known/agent-card.json`, because the gateway fetches a passthrough card from upstream base path + `/.well-known/agent-card.json` (`gateway-controller/pkg/transform/agent.go:46-54`, `:597-611`). The upstream is `cardURL` without that suffix.
- At most one transport per protocol binding (`JSONRPC`, `HTTP+JSON`). A binding is fronted only when all of its `supportedInterfaces` entries:
  - agree on one URL;
  - declare `protocolVersion` `1.0`, or none;
  - have the upstream's scheme, host and port, with a path under the upstream's path.

  The transport's `pathPrefix` is the remaining path. An empty remainder **omits** `pathPrefix`, and the gateway defaults it to `/`. An explicit `""` is rejected by the gateway (`agent_validator.go:1564-1569`), so `A2ATransport.PathPrefix` gains `omitempty` (`services/a2a_agent_deployment.go:125`). The platform golden file is unaffected, since M1 always sets a prefix.
- Why the rule is that strict: the gateway rewrites every entry on a binding it fronts to its single endpoint for that binding. If one of those entries declares another version, the whole card rewrite fails and the gateway serves the card as HTTP 500 (`system-policies/a2a/rewrite.go:422-436`, `a2a.go:703-705`). Entries without a version count as matching. A binding that breaks the rule is left unfronted.
- `errA2ANotProxyable`, wrapped with the reason, when the card URL is not at the well-known path or no binding is fronted.
- Entries on unfronted bindings (gRPC, or a binding skipped by the rule above) stay in the gateway-served card unrewritten.

The gateway removes only `spec.context` and forwards each transport's `pathPrefix` (`agent.go:515-522`, `:570-573`). These routes mirror the origin's own layout: `POST /{agent}/a2a/jsonrpc` reaches `{upstream}/a2a/jsonrpc`.

### Agent YAML (`services/a2a_agent_deployment.go`)

- `A2AAgentDeploymentInput` gains `Transports []A2ATransport` (nil keeps M1's `/rpc` and `/rest`) and `UpstreamAuth *models.UpstreamAuth`.
- `A2AUpstream` gains `Auth *models.UpstreamAuth` (`yaml:"auth,omitempty"`), written as `{type: api-key, header, secretRef}`. `models.UpstreamAuth` (`models/upstream.go:48-53`) uses pointer fields. The internal API's generic walk (`decryptSecretRefsIn`, `services/gateway_internal_service.go:331-363`, reached from `:134` for `GET /agents/{id}`) replaces any `secretRef` with `value`, so the worker never decrypts.
- The gateway honours only `header` + `value` for Agent: `type` is checked only for `none`, and `policyName`/`policyParams` are ignored (`agent.go:1094-1113`). It also skips auth when either is empty. Always emit `type: api-key` with both.
- `hostRewrite` is not written; the gateway defaults to `auto` (`agent.go:233-234`).
- Platform output stays byte-identical (`services/testdata/a2a_agent_golden.yaml`). The builder's comment about platform agents being reached through their own HTTPRoute host (`:69-72`) gains the proxied case.

### Publication worker (`services/a2a_publication_reconciler.go`)

- **Upstream resolution:** `attemptPublish` resolves the upstream through a new step, `resolveUpstream(ctx, pub)`, which replaces `:324-330` and returns the upstream URL, transports and upstream auth:
  - **`platform`:** unchanged. `GetReleaseBindingServiceURL`, `errUpstreamNotReady` while empty, M1 transports, no upstream auth.
  - **`external`:** load the `a2a_proxies` row (`errA2ANotProxyable` if it is gone) and the card row's `source_url`. Fetch it with the guarded fetcher, run `deriveA2AProxyRoutes`, and pass the stored header and ciphertext through as `secretRef`.
- **After resolution:** everything is M1 as-is: `resolveGateway` (ingress), `agentConfigRepo.Get` and `buildPolicies`, the deployments row, the event, and `MarkPublished` with the derived upstream as `published_upstream_url` (diagnostic only).
- **New non-retryable path:** `errA2ANotProxyable` goes straight to `MarkFailed` with its reason from `recordAttemptFailure` (`:279`), without spending the 30-attempt budget. Today `MarkFailed` is reached only at budget exhaustion (`:284`).
- **Error text:** every `last_error` written by the worker goes through the card worker's `sanitizeA2ACardError` (`a2a_card_reconciler.go:211-247`), moved to a shared file. It is now shown to users as the proxy status, so it must not carry non-UTF-8 or DNS detail (card review findings #6 and #10).
- **Deadlines:** port the card worker's 30s per-attempt deadline and its batch deadline of one minute inside the lease. Record outcomes with `context.WithoutCancel` (`a2a_card_reconciler.go:41-44`, `:128-176`). Today the publication worker has neither (`:144-155`), and the origin fetch adds up to 5s per external row.
- **Card trigger after `MarkPublished`:** today `enqueueCardFetch` hard-codes `Source: platform` (`:240-247`). Card `Enqueue` now rewrites `source`, and on a source change clears the card and takes the new `source_url` (`repositories/a2a_agent_card_repository.go:165`, `:208-228`). For an external row it would destroy the registered URL. So:
  - `platform` rows: `Enqueue` as today.
  - `external` rows: the card repo's update-only `Requeue` (`:187-206`), which makes the parked row due without touching `source`, `source_url` or the card. If the row is gone (the source was removed concurrently), log and skip. Never upsert, so no empty-URL row is created (card review finding #14).
- **Drift scan:** `FindPublished` filters `source = 'platform'` **in SQL** (`repositories/a2a_publication_repository.go:163-174`). Filtering after the fetch would break its cursor paging. External origins are never polled.
- **OpenChoreo calls for external rows:** only `GetComponent` from `dropCardIfAgentGone` (`:258-267`), which works for external agents.
- **New dependencies** (`wiring/wire.go`): `A2ACardFetcher` (already a provider, `wire_gen.go:181`) and `A2AProxyRepository`. `cardRepo` is already injected (`:68`).

### Card worker (`services/a2a_card_reconciler.go`)

- **URL for a proxied external row:** `buildPublicProxyURL` (`services/agent_configuration_service.go:344`) for the environment's ingress gateway and context `/{agent}`, plus the well-known path. It uses the plain client, as for platform rows, since the URL is platform-derived and resolves to loopback in local setups (`A2A_CARD_FETCH_DIAL_ADDR` pins it there in compose).
- **Gateway lookup:** the reconciler has no gateway lookup today (`:67-97`). It gains `GatewayRepository`, and `resolveGateway` (`a2a_publication_reconciler.go:395-414`) moves to a shared `resolveIngressGateway(ctx, gatewayRepo, ouID, envUUID)` used by both workers.
- **Knowing the row is proxied:** `ClaimDue` uses an explicit column list (`a2aCardClaimColumns`, `repositories/a2a_agent_card_repository.go:231`). The card row carries no proxy flag, so for an external row the worker looks up `A2AProxyRepository.GetByEnvUUID`: found means fetch through the gateway, not found means fetch `source_url` directly.
- **`source_url`:** `MarkFetched` writes `source_url = fetchedURL` unconditionally (`repositories/a2a_agent_card_repository.go:194`). For external rows it must keep the registered origin URL: write `source_url` for platform rows only. Otherwise the next `Enqueue` comparison sees a changed URL and clears the card.

### Card repository (`repositories/a2a_agent_card_repository.go`)

- `ClearCard(ctx, ouID, project, agent, envUUID)` empties `card`, `card_hash`, `fetched_at` and `release_name`, and **parks** the row: `status=pending`, `attempt_count=0`, `last_error=''`, `next_attempt_at=NULL`. It bumps `updated_at`, so an in-flight `Mark*` hits `ErrA2AAgentCardSuperseded` rather than writing over the cleared card. `ClaimDue` never claims a NULL `next_attempt_at` (`:118`), so a parked row waits for the post-publish `Requeue`.
  - Left `fetched`, the row would read as fetched with no card.
  - Left due, the card worker would fetch from the gateway before the Agent exists there, spending its ~10-minute budget against the publication's ~15-minute one.
- `MarkFetched` gains the platform-only `source_url` write above.
- moq mocks regenerated.

### Deploy settings (`UpdateAgentDeploySettings`, `services/agent_manager.go:5012`)

- Accept `external-agent-api` agents with the `a2a-agent` subtype when the environment is proxied. Reject a direct external environment with a 400 saying deploy settings apply to proxied environments only. Today the method rejects every non-`agent-api` agent (`:5028`).
- For external agents:
  - skip the build and instrumentation steps (`:5083-5102`) and `UpdateReleaseBindingTraitConfigs` (`:5106`), since no release binding exists;
  - persist the agent config, then `republishA2AAgent`, now gated by `publisher.ShouldPublish`.
- Config resolution here runs with `withDefaults=false` (`:5043`). That is safe because enabling the proxy already wrote defaults.

### Deletion

- **Agent deletion:**
  - `deleteAgentAPIArtifact` already runs for every agent (`services/agent_manager.go:2734`, `:2766`; defined `:2863`), clearing publication rows, card rows, per-environment artifacts and gateway resources.
  - It also calls `a2aProxyRepo.DeleteForAgent`.
  - `clearInheritedA2ACards` (`:2836-2845`) gains `a2aProxyRepo.DeleteForAgent` at create, so a same-named new agent inherits nothing.
- **Environment deletion (`DeleteEnvironment`, `services/environment_service.go:~345-375`):**
  - **Today:** it drops card rows (`DeleteForEnvironment`, `:367`) but leaves A2A publication rows, agent artifacts and the gateway `kind: Agent` behind. Platform agents are mostly covered, because OpenChoreo refuses to delete an environment while release bindings exist. External agents have no binding, so a proxied environment is deletable with a live gateway Agent.
  - **Add:** for each publication row in the environment, run `publisher.TeardownEnv`, then delete the artifact. Then `a2aPublicationRepo.DeleteForEnvironment(envUUID)` and `a2aProxyRepo.DeleteForEnvironment(envUUID)`.
- **Undeploy:** does not apply to external agents (no binding; `UpdateAgentDeploymentState` rejects them, `:5968`). A proxy is torn down only by switching to direct, removing the card source, deleting the environment, or deleting the agent.

## API

Spec-first via `/add-api-resource`: a new controller (`controllers/a2a_proxy_controller.go`) and routes file (`api/a2a_proxy_routes.go`), served by `a2aProxyService`. The card API is unchanged from PR #1990.

```
GET    /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/a2a-proxy
PUT    …/environments/{envID}/a2a-proxy
DELETE …/environments/{envID}/a2a-proxy
POST   …/environments/{envID}/a2a-proxy/refresh
```

| Operation | Behaviour | Responses |
|---|---|---|
| `GET` | Proxy status (body below) | 200; 404 when the environment is direct (no row) |
| `PUT` | Enable, or update upstream auth. Body: `{upstreamAuth?: {header, value?}}` | 202; 400 not an external A2A agent or invalid auth; 409 no card URL registered for the environment |
| `DELETE` | Switch to direct | 204; 404 already direct |
| `POST …/refresh` | Re-derive routes from the origin card and republish | 202; 404 not proxied |

`GET` response (`A2AProxyResponse`):

```yaml
status: pending | published | failed   # from the publication row
lastError: string
upstreamAuthHeader: string             # empty when no upstream auth; the secret is never returned
```

Proxying an environment that has no card URL yet takes two calls: `PUT …/agent-card/source`, then `PUT …/a2a-proxy`. If the second fails, the environment is a valid direct-mode environment and the user retries; the console reports the second call's error on its own.

Request semantics:

- **Secret semantics** (LLM-provider precedent, `controllers/llm_controller.go:641-662`; deliberately the opposite of MCP, which keeps the secret when auth is omitted):
  - `upstreamAuth` omitted: upstream auth is **cleared**. The console asks for a yes/no confirmation before sending a clear.
  - `upstreamAuth` without `value`: the stored secret is kept. Rejected when no secret is stored yet.
  - `upstreamAuth` with `value`: the value is encrypted and replaces the stored secret.
  - An empty `value` is rejected. The gateway treats an empty value as "no auth" (`agent.go:1103`), so it would be an ambiguous way to clear.
- **Validation:**
  - `header` matches `^[A-Za-z0-9_-]+$` (the gateway's `set-headers` schema), is at most 255 characters, and is not `Host` (case-insensitive).
  - `value` is 1–8192 bytes and contains no `'`, CR or LF. The gateway pastes header and value into a single-quoted YAML template and escapes only `%` (`gateway-controller/pkg/constants/constants.go:176-180`, `pkg/utils/commonutils.go:36`), so a quote or newline breaks or alters its config.
  - No header-validation helper exists in AMS today, so these are new.
- A stored secret is implied by a non-empty `upstreamAuthHeader`, matching the console's existing `hasStoredCredential` pattern.
- The deploy-settings endpoint accepts proxied external A2A agents for auth, CORS and card CORS.
- **RBAC:** `GET` under agent read. `PUT`, `DELETE` and refresh use `rbac.AgentUpdate + rbac.AgentEnvNonProduction`, the same as deploy settings and the card source routes (`api/a2a_agent_card_routes.go:28-36`, `api/agent_routes.go:65-67`).
- **Audit** (`/add-audit-event`), new actions in `audit/policy.go` and `audit/actions_domain.go`:
  - `agent:set-a2a-proxy` (`PUT`) is always **fail-closed**: `audit.Begin`/`Complete` in the service (precedent `services/git_secret_service.go:79,154`). A PUT can set, replace or clear a credential.
  - `agent:remove-a2a-proxy` (`DELETE`) is also fail-closed, since it discards the stored secret.
  - Refresh needs no action (like card refresh).
  - Detail fields (`agentName`, `upstreamAuthHeader`, `upstreamAuthChanged`) are registered with `RegisterDetailSchema`, since undeclared fields are dropped (`audit/schema.go:71`). The secret value never appears.

## Gateway extension chart

`deployments/helm-charts/wso2-amp-api-platform-gateway-extension/templates/gateway-kgateway-route.yaml:31-32`: change `timeouts.request` from `30s` to `0s` (disabled under Gateway API). The gateway applies no route timeout to JSON-RPC or streaming A2A routes unless `resilience.timeout` is set. Its 5m idle timeout (`route_idle_timeout_ms`, `stream_idle_timeout`) remains the liveness guard (`gateway/configs/config-template.toml:440-466`, `agent.go:1053-1084`). This also uncaps LLM and MCP proxy traffic, which uses the same catch-all route.

## Console

Built after the backend, in the same PR, via `/add-console-api-feature`. On an external A2A agent's Agent Card panel, per environment:

- New `apis/` + `hooks/` pair for `…/a2a-proxy` (get, put, delete, refresh), keyed per agent and environment. Proxy mutations invalidate both the proxy query and the agent card query.
- A **Route through gateway** toggle with the upstream header and secret. The secret is write-only: the field shows that a value is stored and offers Replace and Clear; Clear (and saving with auth removed) asks for yes/no confirmation.
- Turning the toggle on calls `PUT …/a2a-proxy`, and off calls `DELETE`. When the URL is new too, Save sends `PUT …/agent-card/source` first, then the proxy `PUT`, and reports a failure of the second call on its own (the environment is left direct).
- `AgentCardSourceForm`'s `canSave` currently requires the URL to change (`AgentCardSourceForm.tsx:46`); a toggle-only or auth-only change must be saveable.
- A proxy status chip from `GET …/a2a-proxy`: pending, published, or failed with `lastError`.
- **Polling:** while a proxied environment's publish is in flight, the card stays `pending` (parked). The panel's re-poll (`hooks/agent-card.ts`, `agentCardRefetchInterval`) also stops when the proxy status is `failed`, since a parked card never leaves `pending` on its own; the panel shows the proxy's `lastError` instead.
- Interface URLs already render with copy buttons (`EnvAgentCardSection.tsx:182-197`). Once proxied they are the gateway endpoints; no new work.
- **Inbound auth, CORS and card CORS** for a proxied environment reuse the deploy-settings sections, not the drawer:
  - `AgentCardCorsSection` is presentational and reusable as is.
  - `SecurityConfigSections` renders and builds a body only for `agentType.type === "agent-api"` (`SecurityConfigSections.tsx:127, :259, :286`); relax this for proxied external A2A agents.
  - `EditDeployConfigDrawer` chains a `PUT /configurations` against the release binding (`:225-260`), which external agents don't have, so the sections need a new host on the external agent's environment view.
- **API keys:** the backend has no agent-type gate (`services/agent_apikey_service.go:130-150`). The console has two to relax for proxied environments:
  - `Security.Component.tsx:46-51` requires an active deployment;
  - the Credentials nav item is `agent-api` only (`navigationItems.tsx:423, 652`).

## Failures

| Condition | Result |
|---|---|
| Origin unreachable, or its card invalid, during a publish | M1's retry budget (30 attempts, about 15 minutes), then `failed`; the sanitized reason shows as the proxy's `lastError`. |
| Card URL not at the well-known path, or no usable JSON-RPC or HTTP+JSON 1.0 interface | `failed` at once, with the reason. |
| Published, but the card fetch through the gateway fails | The card ingestion spec's card status and retries, unchanged. |
| Origin card declares a mismatched version on a fronted binding after publishing | The gateway serves the card as HTTP 500; the card worker's retries, then `failed`. Refresh re-derives the routes. |
| No ingress gateway mapped to the environment | M1's existing retry (a timing condition at bootstrap). |

## Security

- **Upstream URLs are SSRF-checked twice:** with `ssrf.ValidateURL` when the card URL is saved (`services/a2a_agent_card_service.go:242-256`), and again at every publish, since the worker fetches the origin card through the SSRF-guarded client. The publish check is stronger than the MCP precedent, which checks only on save (`services/mcp_proxy_service.go:1189`).
- **The gateway's own runtime connections are not guarded by AMS,** as with MCP upstreams today: a DNS change after publishing is not caught. Flag for the security review, since anyone can reach this upstream through the ingress gateway.
- **Inbound auth defaults on** (API key security), as for platform agents.
- **The upstream secret** is encrypted at rest, never returned, and decrypted only into the YAML AMS serves to the authenticated gateway. It is validated against the gateway's YAML-quoting weakness (above).

## Testing

Unit, with moq mocks and constructors (`/add-service-unit-test`):

- **Route derivation:**
  - the upstream is taken from the card URL; a non-well-known card URL is rejected;
  - a binding whose entries disagree on URL, declare another version, or sit on another host, port or path is left unfronted;
  - unversioned entries are accepted; gRPC entries are ignored;
  - an empty remainder omits `pathPrefix`;
  - nothing fronted is `errA2ANotProxyable`.
- **YAML:** external transports and `upstream.auth {type: api-key, header, secretRef}` are emitted; an omitted `pathPrefix` has no key; platform output is byte-identical to the golden file.
- **Publication worker:**
  - external rows make no release-binding calls;
  - `errA2ANotProxyable` marks the row failed at once;
  - the drift scan's SQL returns platform rows only;
  - the card trigger requeues external rows without touching `source`/`source_url`, and skips when the row is gone;
  - errors are sanitized;
  - the batch deadline stops before the lease.
- **Card service:** URL change, source removal and refresh on a proxied environment go through `A2AProxyHooks`, never `Requeue` or a direct fetch.
- **Card worker:** an external row with an `a2a_proxies` row fetches the gateway URL, without one fetches `source_url`; `MarkFetched` keeps an external row's `source_url`.
- **`a2aProxyService`:**
  - `PUT` with no registered card URL is 409; `GET`/`DELETE`/refresh on a direct environment are 404;
  - each transition in the table above;
  - a mode switch tears down only that environment's gateway resources and deployment rows, keeps the artifact, clears the card and queues the right fetch;
  - a switch to proxied leaves the card row parked rather than due;
  - refresh on proxied queues a publication, never `Requeue`;
  - secret semantics: omit clears, keep, replace, empty rejected;
  - header and value validation;
  - the fail-closed audit aborts the change on audit failure.
- **Deploy settings:** a proxied external agent skips the release binding and republishes; a direct external agent gets a 400.
- **`ShouldPublish`:** direct external environments are refused; proxied and internal ones are allowed.
- **Environment deletion:** tears down proxied state.
- **Secret handling:** the secret never appears in an API response; the internal API's secret walk decrypts `upstream.auth.secretRef` in an Agent YAML.

Integration:

- `a2a_proxies` `Upsert` replaces a same-named row from a deleted environment, and its `DeleteForEnvironment` removes it;
- the publication `Enqueue` on the UUID key replaces a same-named row from a deleted environment;
- `FindPublished` returns platform rows only;
- `ClearCard` parks the row and `ClaimDue` skips it until the next `Requeue`;
- `DeleteForEnvironment` on publications.

Manual (local make-setup env): the SSRF guard has no local override, so the external agent needs a public address; a tunnel to a locally run `samples/a2a-agent` works.

1. Proxy it, and call it through `{gateway host}/{agent}` with an API key.
2. Check the stored card shows gateway URLs while the registered URL is unchanged.
3. Set an upstream header and check it reaches the agent, including on the card fetch.
4. Stream for over 30s.
5. Switch modes both ways, delete the environment.

## Out of scope

- OAuth2 client-credentials upstream auth. The gateway accepts `oauth2` on Agent but ignores it today; add it when the gateway applies it.
- Egress-side proxying for platform agents.
- Proxying gRPC-only agents, or agents whose interfaces live on another host than their card.
- Protocol translation. The gateway forwards each request unchanged to the same binding on the upstream (its A2A resolver only classifies the operation for policy), so a proxied agent offers exactly the bindings the origin serves.
- Managed-mode cards (gateway-stored content) for agents whose card is not at the well-known path.
- Periodic origin polling.

## Open points

1. **Cloud routing.** Confirm cloud data planes install the gateway chart's catch-all route for ingress gateways. The chart in this repo renders it unconditionally, for split topologies too (`add-environment.sh:697-713`). No cloud chart is in the repo.
2. **Gateway-side SSRF.** The gateway's runtime upstream connections are unguarded, as with MCP upstreams. Security review.
3. **Context collisions.** A proxied agent's context is `/{agentName}` on the ingress gateway, as for platform A2A agents. A clash between agents of the same name in one environment fails the gateway deploy (the gateway validates route collisions). M1 has the same property; external agents make a clash more likely.
4. **Gateway issues to report upstream** (wso2/api-platform):
   - (a) the Agent upstream auth template escapes only `%` inside single-quoted YAML;
   - (b) `type: oauth2` and `policyParams` are accepted on Agent and silently ignored;
   - (c) the schema contradicts itself on the passthrough card's `rewriteUrls` default (`management-openapi.yaml:4866-4869` vs `:4997-5024`). M1 relies on the rewrite happening; confirm the effective default, or set it explicitly in the Agent YAML.
