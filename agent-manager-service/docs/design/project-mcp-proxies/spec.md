# Project-scoped MCP proxies in AMS, for Integration Platform (2026-10-29)

**Status:** spec agreed; ready for an implementation plan. Tracking: #2196. Handover: [`handover.md`](handover.md).

**Contract:** [`openapi.yaml`](openapi.yaml). Its shapes
come from the AMS handlers and Go models, not from `docs/api_v1_openapi.yaml`, which has drifted (e.g. the agent key list
encodes a raw `[]StoredAPIKey`, not `ListAPIKeysResponse`).

Integration Platform (IP) moves its MCP server exposure from API
Platform (APIP) to agent-manager-service (AMS), so AM agents can use IP's MCP servers as tools. Paths are relative to
`agent-manager-service/` unless noted. Facts were checked against the code on 2026-10-29; §9 lists what was verified.

## 1. Requirements (decided 2026-10-29)

- IP creates and manages **project-scoped MCP proxies** in AMS, one per (component, endpoint).
- **API keys are per environment.** No key works across environments.
- **Security:** `apiKey` (default, header `X-API-Key`) or `jwt`. `identity` is not offered to IP in phase 1.
- **jwt issuer:** the environment's ThunderID, named `ThunderKeyManager` on the gateway. It is the same issuer as IP's
  `org-idp` (IP is migrating onto it); IP sends `ThunderKeyManager`.
- **Test keys come from AMS:** at least 1 hour, up to 3 live per user.
- **AM visibility:** IP's proxies are **not** shown on AM's MCP proxy page. They appear only in an agent's
  **Configure → Tools** picker, for agents in the same project.
- **Caller:** IP calls AMS with an **M2M token** plus `X-Impersonate-Org` (the user token lacks AMS scopes).
- **No reconciler:** IP binds an environment only once its component is deployed there.
- **A binding `PUT` re-queues a `Failed` binding** even with no input change (reconciliation design §4.4, kept outside this repo).
- **Gateway:** one gateway per environment, registered in AMS as `BOTH`, in the same OpenChoreo project as the agents
  and MCP components. Its provisioning in the cloud isn't built yet. Existing legacy AM gateways in IP orgs are not a
  concern (user's call).
- **Delete with agents bound:** `409 MCP_PROXY_IN_USE`, as on the org route. IP handles the 409 before deleting its
  MCP component (agreed with IP).
- **Base branch:** `gateway-reconciliation-v2` (the gateway reconciliation work, #2183), not `main`.

## 2. What IP does today (APIP)

- MCP servers are OpenChoreo components of type `deployment/mcp-server` / `deployment/mi-mcp-server` in an ordinary
  project.
- Each (component, endpoint) is one APIP `RestApi` with a stable hashed handle (component-endpoint plus an 8-hex sha256
  suffix, no environment), deployed per environment. One upstream serves all environments.
- Stateless and declarative: IP ensures exposure whenever it lists a component's endpoints and stores no ids. Component
  and project deletes remove the OpenChoreo resources first, then unexpose.
- Security: none / api-key / jwt; keys per API. Test keys are ordinary keys with a 1-hour `expiresAt`, 3 kept. Policies:
  CORS and rate limit.
- IP authenticates to APIP with an M2M client plus an org header.

| Concern | APIP today | AMS |
|---|---|---|
| Auth | M2M + `X-Org-Id` | M2M + `X-Impersonate-Org` (§5) |
| Org/project | IP mirrors them into APIP | None; AMS reads projects from OpenChoreo |
| API object | `/rest-apis/{handle}` with upstream, operations, policies | `/projects/{p}/mcp-proxies/{handle}` with `source{componentName, endpointName}` |
| Upstream | Caller supplies one URL | AMS resolves one per environment |
| Deploy | `POST .../deployments`, poll | `PUT .../environments/{env}`, poll `GET` for `deploymentStatus` |
| Gateway | IP picks the first regular gateway | AMS picks the environment's gateway |
| Security | Policies composed by IP | Typed `security` per environment |
| Keys | Per API, list via `/me/api-keys` | Per environment, real list route |
| Test keys | IP builds them | `POST .../api-keys/test` |

So IP writes a new client; it is not a base-URL swap.

## 3. API (summary; the OpenAPI file is normative)

Base `/api/v1/orgs/{orgName}/projects/{projName}`, header `X-Impersonate-Org: <org uuid>`, scopes `amp:mcp-server:*`.
`{projName}` is resolved through OpenChoreo (`ocClient.GetProject`), as LLM proxies do.

| Method | Path | Notes |
|---|---|---|
| PUT | `/mcp-proxies/{proxyId}` | Idempotent upsert by handle (IP's `endpointAPIHandle`). `{name, version?, description?, context?, source{componentName, endpointName}}`. 201/200. `409 HANDLE_IN_USE` if the handle belongs to another project or an org proxy. |
| GET | `/mcp-proxies[?component=&endpoint=]` | This project's proxies. |
| GET | `/mcp-proxies/{proxyId}` | Includes `environments[]`. |
| DELETE | `/mcp-proxies/{proxyId}` | 204; undeploys every environment, revokes keys. `409 MCP_PROXY_IN_USE` while agents use it. |
| PUT | `.../environments/{envName}` | `{gatewayId?, security?, policies?, upstreamAuth?}`; full replace. See below. |
| GET | `.../environments/{envName}` | `{deploymentStatus?, deploymentStatusReason?, gatewayId, publicUrl, context, ...}` |
| DELETE | `.../environments/{envName}` | Deletes the binding and its keys (MCP has no undeploy). |
| POST | `.../environments/{envName}/api-keys` | `{displayName?, expiresAt?}`; plaintext once. `400` if `expiresAt` is in the past. |
| GET | `.../environments/{envName}/api-keys` | `{keys: [APIKeyInfo]}`, masked. |
| DELETE | `.../environments/{envName}/api-keys/{keyName}` | Revoke. |
| POST | `.../environments/{envName}/api-keys/test` | `{forUser}` (the caller is IP's client, not a user). 1 h default, 3 slots per user. |

**Binding `PUT`, in order:**
1. **Upstream:** AMS resolves the in-cluster ServiceURL of `source` in that environment from the release binding (A2A
   precedent `GetReleaseBindingServiceURL`, `clients/openchoreosvc/client/deployments.go:1842-1918`, plus an
   endpoint-name variant). It skips the SSRF check only for URLs it resolved itself. Not deployed yet →
   `409 UPSTREAM_NOT_READY`, nothing stored.
2. **Gateway:** the MCP kind's `Targets` (egress-capable, anchored on the placed gateway). No gateway is not an error:
   the binding is stored, `deploymentStatus` is absent with reason `NO_GATEWAY`, and the placement pass deploys it when
   the gateway registers.
3. **jwt issuers** are validated against the identity providers on that gateway (`400 UNKNOWN_KEY_MANAGER`). With no
   gateway yet, validation happens at placement and an unknown name shows as `Failed`.
4. **Context:** the gateway context is `/<project><context>` (decided 2026-10-29,
   the project-scoped gateway paths plan, D3, kept outside this repo). It must not equal, contain or sit under another artifact's context on that
   gateway, of any kind (`409 CONTEXT_IN_USE`, nothing stored; D4). With no gateway yet, the check runs at placement and a clash
   shows as `Failed`.
5. **One transaction:** store the binding with the resolved URL, render inline, `deployment_status` DEPLOYING plus
   event. The ack turns it `Deployed` or `Failed`. IP polls `GET`. A `PUT` on a `Failed` binding re-queues it.

**Project in the gateway identity (decided 2026-10-29).** IP's `context` is relative to the project: IP sends `/weather`, and the
gateway serves `/<project>/weather`. The proxy's gateway display name is `<project> <name>`, so two projects can use the same name.
`context` reads back as IP sent it, so IP's ensure loop sees no diff; `publicUrl` carries the full path. IP never builds the URL.

**Status words** (reconciliation): `Deploying | Deployed | Failed` (`Undeploying`/`Undeployed` can't occur on project
bindings). Today `main` has only `Deployed`/`Undeployed`.

**Security shape** (`models.SecurityConfig` extended): `{enabled, apiKey{enabled, key, in}, jwt{enabled, issuers
(default [ThunderKeyManager]), audiences?}}`. `identity` → 400 on project routes.

**Org routes, changed for the picker:**
- `GET /orgs/{org}/mcp-proxies?project={p}` returns org proxies plus that project's. Without `project`, project
  proxies are excluded, so the MCP proxy page is unchanged.
- `GET /orgs/{org}/mcp-proxies/{id}` returns a project proxy read-only, with `projectName`, without the upstream URL.
- Org `PUT`/`DELETE` on a project proxy → `409 PROJECT_OWNED`.

## 4. AMS changes

1. **Migration** (052 or later; 051 is reconciliation): nullable `mcp_proxies.project_uuid`. NULL = existing org
   proxies, unchanged.
2. **Project enforced on every operation** (get, update, delete, bindings, keys, test keys). LLM proxies only enforce
   it on list/delete/keys (`controllers/llm_controller.go:1007`, `services/llm_proxy_service.go:197`); don't copy that.
3. **Visibility:** project proxies get `in_catalog=false` (`repositories/mcp_proxy_repository.go:82` puts every proxy in
   the catalog today). Agent mcp-configs may bind a project proxy only from the same project
   (`services/agent_configuration_mcp_binding.go:472`).
4. **Handles** stay org-unique (`uq_artifact_handle_ou_id`).
5. **Upstream resolution** on the binding `PUT` (§3).
6. **Per-environment keys** on the existing broadcaster (`services/apikey_broadcaster.go`); each binding is its own
   artifact. Reject a past `expiresAt` with 400: today AMS only parses it (`apikey_broadcaster.go:103-108`), and the
   gateway silently drops already-expired keys.
7. **Test keys** (shared with agent test keys, `services/agent_apikey_service.go:44-46,312-360`): TTL 1 h default,
   configurable; 3 slots per user per artifact (`testKeyName(user)` + slot), taking a free or expired slot or evicting the
   oldest, instead of rotating one row. `Purpose=test`, hidden from the list.
8. **jwt mode:** generalize `appendMCPIdentityAuthPolicies` (`services/mcp_proxy_deployment.go:537-602`), which already
   emits `mcp-auth` with `issuers: [ThunderKeyManager]`, to take issuers and audiences from the binding.
9. **Picker** (console + org routes, §3): `AddMCPToolConfigPanel.tsx` passes `?project=`; `useMCPProxySecurity` handles
   `jwt`: with `ThunderKeyManager` the agent's AgentID token is accepted if the audience matches, so treat it like
   `identity`; with any other issuer, show the proxy as not usable by agents.
10. **M2M override:** middleware on the project MCP routes, after `RequireOrgMatch`, that applies `OrgOverride` and
    rewrites the whole `ResolvedOrg` (OUID **and** handle; the header carries the UUID only, so look the handle up).
    Audit with the client id and target org.
11. **Delete:** `409 MCP_PROXY_IN_USE` if `env_agent_mcp_mapping` or `agent_mcp_config_proxy` rows reference the proxy
    (same checks as the org delete, `services/mcp_proxy_service.go:634-718`).

## 5. Outside AMS

| Where | Change | Owner |
|---|---|---|
| Platform IdP | IP's M2M client gets `ouId`/`ouHandle` token attributes (else `RequireOrgMatch` 403) and a role granting `amp:mcp-server:*` (else RBAC 403). | Cloud |
| Cloud distribution of AMS | Pick up an agent-manager version with `app.Options.OrgOverrideResolver` and install a resolver that trusts only allow-listed client ids from the root OU. | AM |
| Integration Platform | New AMS MCP client; a separate token request for `amp:*` scopes; `X-Impersonate-Org`; `ThunderKeyManager` as the jwt issuer; handle `409 MCP_PROXY_IN_USE` before deleting a component. | IP |
| Cloud gateway provisioning | One `BOTH` gateway per environment in the project, with `ThunderKeyManager` configured. Needed for real traffic, not for the API work. | Cloud |
| `wso2/gateway-controllers` | Declare `audiences` in `mcp-auth`'s policy definition. It works today only because the schema allows extra fields. | Upstream request |

No downstream change is needed in AMS for M2M: the cloud's OpenChoreo client forwards the caller's token with
`X-Impersonate-Org = ResolvedOrg.OUID`, and the platform accepts IP's client with that header.

## 6. Day-1 checks

1. A gateway reaches an MCP component in the **same project** in a cell-mode org (the same-namespace allow comes from
   OpenChoreo's component NetworkPolicy; not verified in code).
2. No project MCP path calls the cloud's secrets client, which adds `X-Impersonate-Org` only when there is no
   request token. The update path calls
   `agentIdentityInjection.ReconcileForEnvironment` (`services/mcp_proxy_service.go:616`).
3. Whether a client-credentials token from the platform IdP carries `sub` (audit and anything copied from agent keys reads it;
   `agent_apikey_controller.go:280-286` requires it).

## 7. Scope, about 12-13 engineer-days (two engineers plus cloud access, tight for one week)

| Work | Days |
|---|---|
| Migration, project checks on every route, `in_catalog=false`, same-project binding check | 1.5 |
| Proxy and binding routes, upstream resolution (`409 UPSTREAM_NOT_READY`), no-gateway path, delete 409 | 2 |
| Per-environment keys, past-`expiresAt` 400 | 0.5 |
| Test keys: 1 h, 3 slots per user (agent and MCP) | 1 |
| M2M: AMS middleware + handle lookup, cloud resolver, IdP client change | 2.5-3.5 |
| jwt: model, validation, policy, picker branch | 2 |
| Picker: org list `?project=`, read-only org GET, `PROJECT_OWNED`, console passes the project | 1.5 |
| Integration tests | 1 |

Split: Engineer A: scoping → routes → keys → M2M middleware. Engineer B: jwt → test keys → picker → tests (jwt can start
day 1 against org proxies). Cloud-access person: IdP client, cloud resolver (day 1).

Dependency: `gateway-reconciliation-v2` must land first or be the base branch (same MCP files, migration numbering).
Related: #2182 (MCP per-agent keys under the wrong artifact) touches the key path the per-environment keys reuse.

Deferred: `identity` for IP, scopes routes, `fetch-server-info`, AMS cleanup on project delete (AMS has no project table
and doesn't watch OpenChoreo; project-scoped LLM proxies already orphan), rotate, agents using custom-issuer jwt proxies.

## 8. Not needed (superseded during design)

- A reconciler for upstream readiness (IP binds only when deployed).
- Console scopes / role maps for IP users (IP uses M2M).
- An AI-only placement rule and a cell-mode network-policy change (single `BOTH` gateway in the same project).
- Keys shared across environments.

## 9. Verified facts (2026-10-29)

- **mcp-auth / audiences:** gateway 2026.09.24 pins `mcp-auth` v1.4.0, which passes params through to `jwt-auth`
  (v1.3.x), which enforces `audiences`. `issuers` entries match a key-manager name, then the token's `iss` must equal its
  issuer. IP's `org-idp` name doesn't exist on cloud gateways; the environment's ThunderID is named `ThunderKeyManager`,
  issuer per (org, env).
- **Key expiry:** the gateway rejects expired keys per request (`api-platform common/apikey/store.go:240-243`) and drops
  keys already expired at ingest.
- **Re-queue:** the first reconciliation branch re-queued on every update via `deploy_intents`; v2 drops that table, so
  the rule is written into the reconciliation design for MCP.
- **Placement:** roles are `ingress|egress|both` (migration 037); MCP accepts egress or both (`models/gateway.go:65-78`,
  `services/gateway_roles.go:71-160`), so a single `BOTH` gateway is picked with no change.
- **M2M:** IP's M2M client has no `ouId`/`ouHandle` claims and no `urn:wso2:amp` role today; the cloud installs no
  resolver; upstream override readers rewrite the OUID only (`controllers/gateway_controller.go:294-317`).
- **IP deletes** in OpenChoreo first and handles no 409 today, which is why IP agreed to handle `MCP_PROXY_IN_USE` first.
