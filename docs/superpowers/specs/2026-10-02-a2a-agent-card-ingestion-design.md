# A2A Milestone 2 — Agent Card Ingestion

**Date:** 2026-10-02
**Status:** Draft for review
**Scope:** agent-manager-service (AMS), console
**Builds on:** Milestone 1 (PR #1845 — `a2a-agent` subtype, gateway `kind: Agent` publication)

## Goal

Every A2A agent the platform knows about has its **public agent card stored per environment**, kept in step with deployments, so Milestone 3's discovery tools (`searchAgents`, `getAgentCard`) can read cards from the platform instead of calling agents live.

Two sources:

- **Platform agents** (`internal` + `a2a-agent`): when a deployment is published to the environment's gateway, AMS fetches the card **through the gateway** and stores it.
- **External agents** (`external` + `a2a-agent`): the user registers a card URL per environment; AMS fetches and stores it.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Who fetches | AMS (control plane), in-process | No new component to build, ship, version or authenticate. |
| How platform cards are reached | Public ingress URL of the agent's environment endpoint (`ReleaseBinding` `externalURLs`), i.e. through the gateway | Never touches pod or in-cluster addresses, so the CP/DP cluster split is respected. Same class of outbound call AMS already makes (MCP server info fetch, OIDC discovery). |
| Trigger | Publication reconciler marking a row `published`, plus external URL set and manual refresh | A card can only be served once the gateway has the Agent resource. AMS has no dataplane readiness event; availability is only a TCP-probe proxy, so retry + backoff and UI re-poll are needed regardless. |
| Queue | DB table, `FOR UPDATE SKIP LOCKED`, same shape as `a2a_publications` | Proven pattern in M1; survives restarts; safe across replicas. |
| Extended agent card | Out of scope | Needs authenticated fetch; revisit later. |
| A2A version | 1.0 only — `/.well-known/agent-card.json`; no `agent.json` fallback | Platform supports A2A 1.0 only. |
| External A2A agent shape | `provisioning.type=external`, `agentType.type=external-agent-api`, `subType=a2a-agent` | Keeps "has a card" ⇔ "is A2A", which M3's search can filter on. |

**Rejected:** a dataplane agent/operator that fetches cards and pushes them up. It would only be needed if the CP could not reach a gateway's public hostname (private/customer-hosted dataplanes). Not the case for cloud today. If it becomes a concern, the preferred evolution is the gateway pushing the card it already proxies up its existing CP connection (WebSocket / internal API) — a gateway-side change, not a new AMS component.

## Data model

New table `a2a_agent_cards` (migration 047), one row per `(ou_id, project_name, agent_name, environment_name)`:

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `ou_id`, `project_name`, `agent_name`, `environment_name` | VARCHAR(255) | Unique together (`uq_a2a_agent_cards_agent_env`) |
| `source` | VARCHAR(32) | `platform` \| `external` |
| `source_url` | TEXT | External: the registered URL. Platform: the URL last fetched (diagnostic). |
| `card` | JSONB, nullable | Last **good** card, stored as returned |
| `card_hash` | VARCHAR(64) | SHA-256 of the canonical body; skip writes when unchanged |
| `fetched_at` | TIMESTAMPTZ, nullable | Time of last successful fetch |
| `status` | VARCHAR(32) | `pending` \| `fetched` \| `failed` |
| `attempt_count` | INTEGER | Reset on enqueue |
| `last_error` | TEXT | Shown in the UI |
| `next_attempt_at` | TIMESTAMPTZ, nullable | Due-scan key |
| `created_at`, `updated_at` | TIMESTAMPTZ | `updated_at` doubles as the optimistic-concurrency token, as in `a2a_publications` |

Index `idx_a2a_agent_cards_due (status, next_attempt_at)`.

**Re-fetch never clears `card`.** A row going back to `pending` keeps the previous card and `fetched_at`, so readers (UI, M3 tools) always see the last known card while a redeploy settles. Status tells them whether it is current. The one exception is a switch between direct and proxied mode (`2026-10-07-a2a-external-agent-proxy-design.md`), which clears the card because the mode changes the URLs it advertises.

Separate table rather than columns on `a2a_publications`: publications are about gateway state and have their own 30-attempt budget; card failures must not consume it, and external agents have no publication row at all.

## Components

### `A2AAgentCardRepository` (`repositories/a2a_agent_card_repository.go`)

Mirrors `A2APublicationRepository`:

- `Enqueue(ctx, card)` — upsert on the unique key; set `status=pending`, `attempt_count=0`, `last_error=''`, `next_attempt_at=now`; **preserve** `card`, `card_hash`, `fetched_at`. For external rows also sets `source_url`.
- `ClaimDue(ctx, now, limit)` — lease pending rows whose `next_attempt_at` has passed (`UPDATE … WHERE id IN (SELECT … FOR UPDATE SKIP LOCKED) RETURNING *`). Unlike `a2a_publications`, a NULL `next_attempt_at` is **never** due: the proxy spec parks rows that way until their publish completes.
- `MarkFetched(ctx, read, card, hash, fetchedURL)`, `MarkAttemptFailed(ctx, read, err, next)`, `MarkFailed(ctx, read, err)` — guarded by `id AND updated_at = read.UpdatedAt`; mismatch returns `ErrA2AAgentCardSuperseded`.
- `Get(ctx, ouID, project, agent, env)`, `ListForAgent(ctx, ouID, project, agent)`.
- `DeleteForAgent(ctx, ouID, project, agent)`, `DeleteForAgentEnv(…)`.
- moq mock in `repositories/repomocks/`.

### `A2ACardFetcher` (`services/a2a_card_fetcher.go`)

`Fetch(ctx, url string, guarded bool) (json.RawMessage, error)`

- `GET {url}` with `Accept: application/json`, **5s** timeout, body capped at **1 MiB** (over-cap → error, not truncation).
- Non-2xx → error carrying the status code.
- Validation (A2A 1.0, minimal): valid JSON object; non-empty `name`; non-empty `supportedInterfaces` with every entry having a non-empty `url`; `skills` present as an array. Unknown fields pass through untouched.
- Client choice:
  - **External** (`guarded=true`): `ssrf.NewClient` + `ssrf.ValidateURL` up front — public IPs only, re-validated at dial and on every redirect.
  - **Platform** (`guarded=false`): plain client. The URL is platform-derived (from the ReleaseBinding), and in local/dev setups the ingress host resolves to loopback, which the SSRF guard would reject.

### `A2ACardReconcilerService` (`services/a2a_card_reconciler.go`)

Structurally a sibling of `A2APublicationReconcilerService`: `Start`/`Stop`/`RunOnce`, ticker loop, started/stopped in `app/app.go`, wired via `wiring/wire.go`.

- **Tick:** 5s; batch 50.
- **Per row:**
  1. Resolve URL — external: `source_url`; platform: first endpoint URL from `ocClient.GetComponentEndpoints(ou, project, agent, env)` + `/.well-known/agent-card.json`. No endpoint yet → retryable error.
  2. `fetcher.Fetch`.
  3. Success → hash; `MarkFetched` (if hash unchanged, still flips status and `fetched_at`, skips rewriting `card`).
  4. Failure → backoff.
- **Backoff:** `min(5s · 2^attempt, 60s)`; **budget 14 attempts** (≈ 12 min), then `MarkFailed`. A gateway that just received the Agent resource typically serves the card within the first few attempts.
- `ErrA2AAgentCardSuperseded` → log at info, leave row for next tick (same as publications).

### Triggers (who calls `Enqueue`)

| Event | Where | Row |
|---|---|---|
| Publication row reaches `published` (first publish, redeploy, promotion, deploy-settings save, upstream drift republish) | `a2aPublicationReconcilerService.publishOne`, after `MarkPublished` succeeds | `source=platform` |
| External card URL set / changed | new `SetA2ACardSource` service method | `source=external`, `source_url` |
| Manual refresh | new refresh endpoint | either source |

Enqueue from the publication reconciler is **best effort** (log on failure), matching `enqueueA2APublication`: the agent is published; a missed card row is recovered by the next deploy or a manual refresh.

### Deletion

- Agent delete: `cardRepo.DeleteForAgent` next to the existing `a2aPublicationRepo.DeleteForAgent` (`services/agent_manager.go` ~2839).
- External card source removed for an env: `DeleteForAgentEnv`.

## External A2A agents

This section covers direct mode. Gateway-proxied mode is in `2026-10-07-a2a-external-agent-proxy-design.md`.

Today `external` agents are observability-only (`external-agent-api` ComponentType, ConfigMap only), subtype unvalidated, no URL field, `GetAgentEndpoints` rejects them.

Changes:

- **Create:** external subtype stays free-form (existing callers rely on that); `a2a-agent` is the one value AMS acts on. It is already stored via the `openchoreo.dev/agent-sub-type` label, so no create-path change is needed beyond the gate audit below.
- **Card URL per environment:** set via the new endpoint below, not on create (one URL per env, envs may be added later). Must be `http(s)`, pass `ssrf.ValidateURL`, ≤ 2048 chars. Environment must exist (`ocClient.GetEnvironment`).
- **Confirm M1 gates don't misfire:** `isA2AAgent` checks in create/deploy/promote paths (trait attach, `enqueueCreatedA2AAgent`, card CORS validation) must be gated on `internal` provisioning so an external A2A agent never gets a gateway publication or route trait. Audit every `IsA2AAgentSubType` call site.

## API

Spec-first via `/add-api-resource` (OpenAPI → codegen → controller → service → RBAC). Paths follow the per-environment style of the api-keys routes.

```
GET  /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card
POST /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/refresh
PUT  /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/source   # external A2A only
DELETE …/agent-card/source                                                                          # external A2A only
```

**`GET` response** (`A2AAgentCardResponse`):

```yaml
source: platform | external
status: pending | fetched | failed
sourceUrl: string          # external URL, or last platform URL fetched
card: object | null        # last good card, as returned
fetchedAt: date-time | null
lastError: string
```

- 404 when the agent is not A2A, or no row exists yet and the agent is external with no source set.
- Platform agent with no row yet (never published) → 200 with `status: pending`, `card: null`.

**`POST …/refresh`** → 202, enqueues. **`PUT …/source`** body `{ url }` → 202, enqueues. Both reject non-A2A agents with 400; `PUT/DELETE source` reject platform agents with 400.

**RBAC:** `GET` under the agent read permission; `refresh`/`source` under the agent update/deploy permission (pick the same permission the deploy-settings save uses).

**Audit:** `PUT/DELETE source` changes where the platform fetches from — add audit actions via `/add-audit-event` (fail-open is acceptable; it is not a credential or permission change). `refresh` does not need one. Proxied mode adds upstream credentials to `PUT source`; requests that touch them are fail-closed (see `2026-10-07-a2a-external-agent-proxy-design.md`).

## Console

Two-file API pattern via `/add-console-api-feature` (`apis/` + `hooks/`, TanStack Query).

- **Agent Card panel** per environment on the agent overview (next to `EnvCapabilitiesSection`) for A2A agents:
  - Status chip (pending / fetched / failed + `lastError`), `fetchedAt`.
  - Rendered summary: name, description, version, skills (name, description, tags), interfaces.
  - Raw JSON viewer.
  - **Refresh** button.
  - Re-poll every ~5s while `status === pending`; stop on `fetched`/`failed`.
- **External A2A:** create flow offers the A2A subtype for external agents; per-environment card URL field with save → `PUT …/source`.

## Known gaps / open points

1. **`supportedInterfaces` URL rewriting: resolved (Oct 7).** Confirmed that the gateway's passthrough (A2A proxy) mode rewrites `supportedInterfaces[].url` to point at itself, so a card fetched through the gateway advertises gateway URLs and the `GET` response needs no `gatewayEndpoint`. The note in `samples/a2a-agent/README.md:329` is stale.
2. **`securitySchemes` not rewritten.** The gateway does not rewrite the card's auth settings to match the gateway's api-key/OAuth policy. To be addressed gateway-side; AMS stores the card as returned.
3. **No periodic re-fetch (decided Oct 6).** Card changes without a redeploy are picked up only by manual refresh, for both platform and external cards. Revisit only if that proves insufficient.
4. **Multiple endpoints.** If an A2A component exposes more than one endpoint, the first is used. M1 components have one.

## Testing

Unit (no build tag, moq mocks — `/add-service-unit-test`):

- Fetcher (`httptest`): valid card; missing `name` / `supportedInterfaces` / `skills`; non-2xx; over 1 MiB; timeout; non-JSON. Guarded mode rejects a loopback URL.
- Reconciler: success → `MarkFetched`; unchanged hash; retryable failure schedules `min(5·2^n, 60)s`; budget exhaustion → `MarkFailed`; superseded is benign; platform URL derivation from endpoints; no endpoint → retry.
- Publication reconciler: `MarkPublished` success enqueues a platform card row; failure to enqueue does not fail the publish.
- Service: `SetA2ACardSource` rejects non-A2A, platform agents, SSRF-blocked URLs, unknown env.
- M1 gate audit: external A2A agent create does not attach `a2a-gateway-route` or enqueue a publication.

Integration (`make test-integration`): `ClaimDue` with two concurrent claimers never returns the same row; `Enqueue` preserves `card`/`fetched_at`.

Manual (local make-setup env): deploy `samples/a2a-agent`, watch the card go `pending → fetched`; register an external A2A agent pointing at a public A2A card; refresh both.

## Out of scope (M2)

- Extended agent card.
- `searchAgents` / `getAgentCard` MCP tools (M3) — this milestone only provides the storage and read API they will use.
- A dataplane fetcher or gateway-push of cards.
- Card versioning/history beyond "last good card".
