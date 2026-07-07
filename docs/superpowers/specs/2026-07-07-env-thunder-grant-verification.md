# Env-Thunder Grant-Model Verification (Task 2 spike)

**Date:** 2026-07-07
**Thunder instance:** live k3d cluster `k3d-openchoreo-local-setup`, env-Thunder for
org=`default` env=`default` (`amp-thunder-default-default` namespace,
`amp-thunder-default-default-service:8090`, reached via
`kubectl port-forward … 18090:8090`).
**System client:** `amp-system-client`, secret from K8s secret
`amp-thunder-default-default-system-client` key `client-secret`. Token obtained via
**HTTP Basic auth** (`client_secret_basic`) + `grant_type=client_credentials` +
`scope=system` at `POST /oauth2/token`.

## Results summary

| # | Assumption | Verdict |
|---|---|---|
| (a) | Roles assignable to **agent** identities (`{id, type:"agent"}`) | ✅ PASS |
| (b) | `client_credentials` tokens carry role-derived permissions in the `scope` claim | ✅ PASS — **with a required-request nuance** (see below) |
| (c) | Group-path role scopes flatten into member-agent tokens | ✅ PASS |
| (d) | Resource-server accepts scope-regex permission strings; roles reference them | ✅ PASS |

## Gates applied

- (b) PASS → **feature proceeds** (no stop).
- (c) PASS → **Groups are viable**: Task 8 keeps group routes with real group→role
  scope flattening; Task 15 (Groups tab, Phase 2) is **not** dropped. (Phase 2/3 are
  out of scope for the current execution, but the gate is recorded for later.)

## (b) The critical nuance — scopes must be REQUESTED and are filtered to entitlements

Thunder does **not** auto-inject an agent's granted scopes into every token. Observed:

- Agent with role granting `repo:read.all`, token request with **no** `scope` param
  → token has **no `scope` claim** (absent).
- Same agent, token request `scope=repo:read.all` → token `scope` = `"repo:read.all"`.
- **Fresh agent with NO roles**, token request `scope=repo:read.all`
  → token `scope` claim = **absent/None**. Thunder **filters** requested scopes down to
  what the agent is actually entitled to (via direct role or group-path role). An
  unentitled agent cannot obtain a scope even by requesting it.

**Format:** the `scope` claim is a **space-separated string** (e.g.
`"repo:read.all repo:write.all"`), not a JSON array.

**Implication for the runtime / E2E (Task 17) and agent SDK:** the agent's
`client_credentials` token request must include `scope=<space-separated scopes>` to
receive them in the JWT. The gateway `mcp-authz` policy then enforces per-tool
`requiredScopes` against that claim. The `jwtScopes` helper in Task 17 must split the
`scope` string on spaces. This does **not** affect Phase 1 backend code — AMS only
registers scopes/roles/grants; it never mints agent tokens.

## Exact Thunder 0.45 API shapes (authoritative for Task 7)

All requests carry `Authorization: Bearer <system-token>` and
`Content-Type: application/json`.

### Default OU
- `GET /organization-units/tree/default` → `{ "id": "<ouId>", "handle":"default", "name":"Default", ... }`
- default OU id observed: `019f3b96-294b-718e-8860-1d9663e8de6a`.

### Resource server (for `EnsureScopeResourceServer`, identifier `amp-scopes`)
- **List:** `GET /resource-servers?offset=0&limit=20`
  → `{ "totalResults", "startIndex", "count", "resourceServers":[ {id,name,description,handle,identifier,type,ouId,delimiter,isReadOnly} ], "links":[] }`.
  Match by `identifier`.
- **Create:** `POST /resource-servers`
  body `{"name":"AMP Scopes","identifier":"amp-scopes","ouId":"<ouId>"}`
  → returns full RS incl. `"id"`, `"delimiter":":"`, `"type":"CUSTOM"`. **ouId is required.**
- **List resources:** `GET /resource-servers/{rsID}/resources?offset=0&limit=20`
  → `{ "totalResults","count","resources":[ {id,name,handle,permission} ], "links":[] }`.
- **Create resource:** `POST /resource-servers/{rsID}/resources`
  body `{"name":"Repo","handle":"repo","permission":"repo"}` → `{id,name,handle,permission}`.
- **List actions:** `GET /resource-servers/{rsID}/resources/{resID}/actions`
  → `{ "actions":[ {id,name,handle,permission} ] }` (actions are NOT embedded in the resource list).
- **Create action:** `POST /resource-servers/{rsID}/resources/{resID}/actions`
  body `{"name":"Read All","handle":"read.all","permission":"repo:read.all"}` → `{id,name,handle,permission}`.
  **The `permission` field carries the exact scope string** (`repo:read.all`) and is
  what surfaces in role permissions and token scope claims. Scope-regex strings
  (`^[A-Za-z0-9:._\-]{1,256}$`) are accepted verbatim.

> **EnsureScopeResourceServer mapping:** for a scope like `repo:read.all`, register it
> under the `amp-scopes` RS. Simplest working shape observed: one resource + one action
> whose `permission` == the full scope string. Whether to model each scope as its own
> resource, or group by prefix, is an implementation choice — the role permission only
> needs the `permission` string + the RS id. Reuse the existing paginated read pattern
> in `ListAMPPermissions` (identity_client.go:753) to detect already-present scopes for
> idempotency.

### Deletion dependency (informational)
- `DELETE /resource-servers/{rsID}` returns **400 `RES-1006` "Cannot delete … has
  dependencies"** while resources exist. Order: delete each action, then the resource,
  then the RS. `EnsureScopeResourceServer` only *ensures* (create/idempotent), never
  deletes, so this is informational only.

### Roles
- **Create with inline permissions works:** `POST /roles`
  body `{"name":"...","ouId":"<ouId>","permissions":[{"resourceServerId":"<rsID>","permissions":["repo:read.all"]}]}`
  → `{id,name,ouId,ouHandle,permissions:[{resourceServerId,permissions:[…]}]}`.
  (No separate `AddRolePermissions` PUT is required at create time; the existing
  `CreateRoleRequest` is metadata-only, so either create-then-PUT or extend the create
  body — both are valid. `AddRolePermissions` PUT `/roles/{id}` also works, per existing code.)
- **Get:** `GET /roles/{id}` · **Update:** `PUT /roles/{id}` · **Delete:** `DELETE /roles/{id}` (204).
- **Assignments read:** `GET /roles/{id}/assignments`.
- **Assign:** `POST /roles/{id}/assignments/add`
  body `{"assignments":[{"id":"<agentId|groupId>","type":"agent"|"group"}]}` → 200, empty body.
  Read-back confirmed the agent assignment `{id:<agentId>, type:"agent"}`.
- **Unassign:** `POST /roles/{id}/assignments/remove` (same body shape).

### Groups
- **Create:** `POST /groups` body `{"name":"...","ouId":"<ouId>"}` → `{id,name,ouId,isReadOnly}`.
- **Get/Update/Delete:** `GET|PUT|DELETE /groups/{id}` (delete 204).
- **Add members:** `POST /groups/{id}/members/add`
  body `{"members":[{"id":"<agentId>","type":"agent"}]}` → success.
- **Remove members:** `POST /groups/{id}/members/remove` (same body shape).

### Agents (fixture creation; matches existing `agent_client.go`)
- `POST /agents` body
  `{"ouId":"<ouId>","type":"default","name":"...","inboundAuthConfig":[{"type":"oauth2","config":{"grantTypes":["client_credentials"],"tokenEndpointAuthMethod":"client_secret_basic"}}]}`
  → response includes `id` and `inboundAuthConfig[0].config.{clientId,clientSecret}`.
- Agent token: `POST /oauth2/token` Basic-auth `clientId:clientSecret`,
  `grant_type=client_credentials`, `scope=<space-separated scopes>`.

## Token claim reference (agent token)
Decoded agent JWT contained: `aud` (= RS identifier), `client_id`, `iss`
(`http://default-default.thunder.amp.localhost:8080`), `ouId`, `ouHandle`, `sub`,
`grant_type`, and `scope` (space-separated string of entitled requested scopes).

## Cleanup
All spike fixtures deleted (roles, group, resource-server + resource + actions, agents).
Verified remaining state: only pre-existing `System` RS, `Administrator` role,
`Administrators` group, and pre-existing agents.
