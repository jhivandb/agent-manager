# MCP Proxy Agent-Identity Security — Design

Date: 2026-07-06 (amended 2026-07-06: role/group grants, first-class scopes,
per-env proxy security, per-agent routes removed)
Status: Approved (brainstorm concluded; ready for implementation planning)
Branch context: builds on `task/agent-id-2a` (per-agent Thunder identity provisioning)

## 1. Problem

MCP proxies today support exactly one security mode: a gateway `api-key-auth`
policy plus a per-agent API key that AMS mints and injects into the agent pod.
This branch gives every agent a Thunder identity (a `client_credentials` OAuth
client in the environment's Thunder instance), which enables a better
alternative: the agent authenticates to the MCP proxy with a JWT issued by its
environment's IdP, and the gateway authorizes individual MCP tools based on
OAuth scopes carried in that token.

This spec adds an **Agent Identity** security mode to MCP proxies:

- Platform operators define **org-global scopes** in a new catalog.
- Proxy authors bind catalog scopes (many-to-many) to the proxy's tools.
- Grants are expressed through **Thunder roles and groups**: a role carries
  scope permissions; roles are assigned to agents directly or to groups the
  agent belongs to. Agents, groups, and roles are managed in a new
  **Agent Identity** console section modeled on the existing user-management
  (identities) section.
- The dataplane gateway validates the JWT against the env-Thunder JWKS and
  enforces the per-tool scope rules.

The proxy exposure model is **one endpoint per environment**: every agent in
an environment calls the same proxy route. There are no per-agent mapping
routes. Security configuration is per environment on the proxy.

## 2. Decisions (from brainstorm Q&A, both sessions)

| Topic | Decision |
|---|---|
| Scope model | First-class org-global entities in a new AMS `mcp_scopes` table (the catalog); proxies and roles reference them by name |
| Scope authoring UI | Dedicated Scopes tab in the Agent Identity section (org-global; ignores the env picker) |
| Tool binding | Proxy Security tab picker matches catalog scopes to `capabilities.tools`; post-creation only (tool discovery happens after create) |
| Security modes | Per environment on the proxy: None / API Key / Agent Identity (mode is the only per-env part; scopes + bindings are proxy-global) |
| Grant mechanism | Thunder-native: roles → agents (direct) and roles → groups → agents; token scopes = union of both paths |
| Grant management | Agent Identity console section (env picker; Scopes / Groups / Roles tabs), based on the identities section |
| Grant storage | None in AMS — direct passthrough to the selected environment's Thunder via `EnvThunderResolver`, mirroring `identity_controller.go`'s passthrough to org Thunder. No desired-state rows, no reconciler |
| Scope→Thunder provisioning | Lazy ensure at role save: AMS ensures the `amp-mcp-scopes` resource server + referenced permissions exist in that env-Thunder, then writes the role |
| Per-agent routes | Removed (prerequisite restructure); one shared endpoint per environment per proxy |
| Proxy deployment | Environment-driven: deploy into each env with a per-env config entry, resolving the env's gateway (`resolveGatewayForEnvironment`); explicit gateway-ID selection goes away |
| API-key mode on shared endpoint | **Open — owned by the restructure work, resolved before this task starts.** Evidence: `apiKeyBroadcaster.broadcastCreate` already supports multiple named keys per artifact, so per-agent keys on a shared route look feasible |
| Gateway policies | Existing fixed-schema `mcp-auth` v1 + `mcp-authz` v1 |
| JWKS trust | Gateway-level `config.toml` key managers (already registered by `add-environment.sh` as `ThunderKeyManager`); no JWKS URLs in deployment YAML |
| Issuer selection | Always `ThunderKeyManager` in v1 (no picker) |
| Unbound tools | Authenticated-only (gateway default-permit) |
| tools/list filtering | Gateway behavior; not designed here |
| Token acquisition by agent | Out of scope (pod client-credential injection is separate work) |
| External agents | Supported identically (they claim credentials and fetch tokens via the external token URL) |

Superseded decisions from the original draft (removed machinery):

- `agent_mcp_scope_grants` table, grant reconciler, and synthetic
  `amp-agent-<project>-<agent>` roles — replaced by user-managed roles/groups.
- Scope definitions inside proxy config (`IdentitySecurity.Scopes`) — replaced
  by the org-global catalog.
- Per-agent grant endpoints and the proxy-scanning scope-catalog endpoint —
  replaced by catalog CRUD + Thunder passthrough routes.
- Per-agent mapping route policy emission and per-mapping key mint/inject —
  per-agent routes no longer exist.

## 3. Prerequisites and assumptions (delivered separately)

This spec builds on an MCP proxy restructure that is **parallel work, not
designed here**. Assumptions this spec makes about it:

1. `mcp_proxies` gains per-environment policy/security configuration; the
   per-env entry carries (at least) a security mode: None / API Key /
   Agent Identity. This spec defines the Agent Identity variant's shape and
   emission; where the per-env config lives (column/table) is the
   restructure's call.
2. One proxy endpoint per environment; `mcp_proxy_mappings` +
   `env_agent_mcp_mapping`-driven per-agent routes are removed, including the
   migration of live mappings, minted keys, and injected env vars.
3. Deployment is environment-driven. **Flag for the restructure:**
   `resolveGatewayForEnvironment` is an AI-first-preference heuristic with
   documented wrong-gateway bugs and three fallback call sites in
   `agent_configuration_service.go`; it becomes the primary deployment driver
   and needs hardening.
4. API-key mode mechanics on the shared endpoint are resolved there (per-agent
   named keys vs one shared key). This spec only requires that the per-env
   mode enum reserves the API Key value.
5. Attach remains the trigger for env-var injection: in identity mode the
   agent pod receives only the proxy `url` env var (`buildMCPEnvVars` already
   omits `apikey` when `secretRefName` is empty).

Existing machinery this design reuses (no changes needed):

- `add-environment.sh` registers `ThunderKeyManager` (env-Thunder issuer +
  internal JWKS URL) into each env gateway's
  `policy_configurations.jwtauth_v1.keymanagers` at environment creation.
- `models.SystemIdentityProviderNames` already seeds `ThunderKeyManager` as an
  undeletable system IdP.
- `clients/thundersvc` already implements everything the grant model needs:
  groups CRUD, group members with `Type: "agent"`, role CRUD, role
  assignment entries with `Type: "agent" | "group"`, `GetGroupRoles`,
  `AddRolePermissions`/`RemoveRolePermissions`, and resource servers.
- `EnvThunderResolver` resolves a ready ThunderClient per org+environment.
- `identity_controller.go` is the passthrough pattern (routes → thundersvc,
  no AMS DB state) the new agent-identity routes copy.

## 4. Architecture

```
                    Agent Identity console section (env picker)
                    Scopes tab ──────────► AMS mcp_scopes table (org-global catalog)
                    Groups/Roles tabs ───► env-Thunder (direct passthrough,
                                            EnvThunderResolver; role save
                                            lazy-ensures scope permissions)

agent pod ──client_credentials──► env-Thunder
    │        (JWT scopes = union of direct-role and group-role permissions)
    │  Bearer JWT
    ▼
dataplane gateway (per env) — ONE proxy route per environment
    ├─ mcp-auth v1: validates JWT via ThunderKeyManager (config.toml key manager)
    ├─ mcp-authz v1: per-tool requiredScopes from the proxy's global tool bindings
    ▼
upstream MCP server
```

Control plane (AMS) responsibilities:

1. Own the org-global scope catalog (`mcp_scopes` CRUD; deletion blocked while
   bound by any proxy).
2. Store tool→scope bindings on the proxy (proxy-global) and the per-env
   security mode (restructure's per-env config).
3. Emit `mcp-auth`/`mcp-authz` policies in the deployment YAML for each
   environment whose mode is Agent Identity.
4. Proxy agent-identity group/role operations to the selected env-Thunder;
   on role save, ensure the `amp-mcp-scopes` resource server and referenced
   permissions exist there first.

## 5. Data model

### 5.1 New table `mcp_scopes` (org-global catalog)

| Column | Notes |
|---|---|
| `id` | PK |
| `org_name` | unique together with `name` |
| `name` | `^[A-Za-z0-9:._\-]{1,256}$` |
| `description` | optional |
| `created_at`, `updated_at` | timestamps |

Deletion rule: rejected with 409 while any MCP proxy's tool bindings reference
the scope. Thunder roles referencing a deleted scope keep the stale permission
string — harmless (no gateway rule requires it once unbound) and warn-only in
the UI.

### 5.2 SecurityConfig extension (identity mode shape)

```go
// Per-env security entry (location owned by the restructure) selects the mode:
// none | apiKey | identity.

// Proxy-global, stored in MCPProxyConfig (JSONB configuration column):
type MCPToolScopeBinding struct {
    Tool   string   `json:"tool"`
    Scopes []string `json:"scopes"` // names from the org mcp_scopes catalog
}
```

Tool bindings are proxy-global: the same tool→scope contract applies in every
environment; only the mode varies per env.

Validation on proxy create/update:

- Every binding scope must exist in the org catalog (400 otherwise).
- Bindings whose `Tool` is absent from current `capabilities.tools` are
  accepted (upstream tool lists drift); the console flags them.
- Setting an environment's mode to Agent Identity is rejected if that env's
  gateway does not report both `mcp-auth` and `mcp-authz` via the existing MCP
  policy-availability mechanism.

## 6. Deployment YAML emission

`appendMCPIdentityAuthPolicies` beside `appendMCPAPIKeyAuthPolicy` in
`services/mcp_proxy_deployment.go`, applied per environment in the
restructure's env-driven build when that env's mode is Agent Identity:

```yaml
policies:
  - name: mcp-auth
    version: v1
    params:
      issuers:
        - ThunderKeyManager
      requiredScopes: [<union of all bound scopes>]  # metadata advertisement only
  - name: mcp-authz
    version: v1
    params:
      tools:
        - name: <tool>
          requiredScopes: [<scopes bound to this tool>]
        # one entry per tool with at least one binding; unbound tools omitted
```

- `issuers` values are key-manager *names* resolved by each gateway against
  its own `config.toml` — per-env gateways each trust their own env-Thunder
  with zero per-env YAML variance.
- `requiredScopes` on `mcp-auth` is advertised in protected-resource metadata
  but not enforced (per policy docs); enforcement is `mcp-authz`.
- Tools without bindings get no `mcp-authz` rule → gateway default-permit →
  callable by any agent with a valid JWT (decision: authenticated-only).
- If no tool has any binding, the `mcp-authz` policy and the `requiredScopes`
  param are omitted entirely — identity mode then means "any valid
  env-Thunder JWT".
- Existing policy normalize/merge machinery
  (`normalizeMCPPoliciesForDeployment`, `mergeMCPPoliciesForDeployment`)
  applies unchanged; `mcp-auth`/`mcp-authz` use the default override merge
  strategy.

## 7. Grants: Thunder roles and groups (direct management)

### 7.1 Thunder modeling

Per environment's Thunder instance, all in the default OU:

- **Resource server** `amp-mcp-scopes`: ensured lazily on role save; every
  scope referenced by the role is ensured as a permission under it before the
  role is written.
- **Roles**: user-created and user-named (no AMS-generated names). A role's
  permissions are catalog scope strings under `amp-mcp-scopes`. Assignees are
  agents (`AssignmentEntry{Type: "agent", ID: ThunderAgentID}`) and groups
  (`Type: "group"`).
- **Groups**: user-created; members are agents (`GroupMember{Type: "agent"}`).
  Roles assigned to a group apply to all member agents.

Effective token scopes for an agent = union of permissions from directly
assigned roles and roles assigned to groups the agent belongs to.

No AMS-side storage: every read/write goes straight to that env-Thunder via
`EnvThunderResolver`. Env-Thunder being down or unprovisioned surfaces as an
API error (same trade the identities section already makes for org Thunder).
Groups/roles are **not replicated across environments** — per-env definition
is deliberate (prod grants differ from dev). A new environment starts empty.

Implementation-time verification (assumptions, approved but unverified in
code) against the deployed Thunder version (`thunderid-0.45.0` per code
comments):

(a) roles are assignable to `/agents` identities;
(b) `client_credentials` tokens carry role-derived scopes in the `scope` claim;
(c) group membership flattens into the token — roles assigned to a group
    contribute their scopes to member agents' tokens. If (c) fails, groups
    are decorative and the Groups tab must not ship.

### 7.2 Agent assignment preconditions

Assigning an agent to a role or group requires its Thunder identity
(`ThunderAgentID` from the `AgentThunderClient` binding, status `COMPLETED`)
in that environment. Pickers show binding status and disable un-provisioned
agents. There is no healing loop: a failed assignment is a surfaced error the
operator retries after provisioning lands.

## 8. API surface (spec-first per add-api-resource workflow)

1. **Scope catalog CRUD**:
   - `GET /orgs/{orgName}/mcp-scopes` → list (this is the picker source for
     both the role editor and the proxy tool-binding picker)
   - `POST /orgs/{orgName}/mcp-scopes` `{name, description?}`
   - `PUT /orgs/{orgName}/mcp-scopes/{scopeName}` (description only; rename =
     delete + create, subject to the deletion rule)
   - `DELETE /orgs/{orgName}/mcp-scopes/{scopeName}` → 409 while bound by any
     proxy
2. **Agent-identity passthrough routes**, mirroring the `/identities/*` route
   shapes, controller modeled on `identity_controller.go` but resolving the
   client via `EnvThunderResolver(org, env)`:
   - `GET|POST /orgs/{orgName}/environments/{envName}/agent-identities/groups`
   - `GET|PUT|DELETE .../agent-identities/groups/{groupId}`
   - `POST .../groups/{groupId}/members/add` and `/members/remove` (agent members)
   - `GET .../groups/{groupId}/roles`
   - `GET|POST /orgs/{orgName}/environments/{envName}/agent-identities/roles`
   - `GET|PUT|DELETE .../agent-identities/roles/{roleId}` (PUT reconciles
     permissions after lazy-ensuring them under `amp-mcp-scopes`)
   - `GET .../roles/{roleId}/assignments`,
     `POST .../roles/{roleId}/assignments/add` and `/assignments/remove`
     (agent and group assignees)
   - `GET /orgs/{orgName}/environments/{envName}/agent-identities/agents` →
     agents in the org with their Thunder binding status + `ThunderAgentID`
     for that env (picker source)
3. **Proxy DTO**: extend the MCP proxy schema in
   `agent-manager-service/docs/api_v1_openapi.yaml` with `toolScopeBindings`
   (proxy-global) and the per-env identity mode value (slotting into the
   restructure's per-env security shape); regenerate spec server code, `am`
   CLI client, and console types.
4. **RBAC**: new permission entries per route in `rbac/permissions.go`,
   following the per-route-authz workflow.

## 9. Console UI

### 9.1 MCP proxy Security tab (`MCPProxySecurityTab.tsx`)

- Per-environment mode selector: **None | API Key | Agent Identity**
  (rendered within the restructure's per-env security layout).
- Tool binding table (proxy-global, shown once, not per env): one row per
  tool from `capabilities.tools`, each with a multi-select fed by
  `GET /mcp-scopes`. Unbound tools show an "authenticated only" hint.
- Warning badges for bindings referencing tools that no longer exist.
- Creation form (`AddMCPProxyForm`) unchanged — bindings are configured
  post-creation on the Security tab (tool discovery happens after create).

### 9.2 Agent Identity section (new; modeled on the identities section)

Environment picker at the top; three tabs:

- **Scopes** (org-global; unaffected by the env picker, labeled as such):
  catalog CRUD table — name, description, "in use by N proxies" indicator,
  delete disabled while bound.
- **Groups** (env-scoped): list/create/edit pages copied from
  `GroupsPage`/`GroupEditPage` patterns; members picker lists agents (with
  binding status, un-provisioned disabled); role assignment on the group.
- **Roles** (env-scoped): list/create/edit pages copied from
  `RolesPage`/`RoleEditPage` patterns; permission picker fed by the scope
  catalog; assignee picker for agents and groups.

Uses the two-file API pattern (`apis/` + `hooks/`) per add-console-api-feature.

## 10. Errors and edge cases

- **Env-Thunder outage/unprovisioned**: agent-identity routes fail with a
  surfaced error (no queue, no reconciler); the operator retries. Same
  behavior the identities section has for org Thunder today.
- **Scope deletion attempted while bound**: 409 with the binding proxies
  listed. After unbinding, deletion succeeds; Thunder roles keep the stale
  permission string (inert; warn-only).
- **Tool removed upstream**: its binding remains in config (flagged in UI);
  the emitted `mcp-authz` rule for a nonexistent tool never matches and is
  inert.
- **Role save referencing a scope deleted mid-edit**: lazy ensure re-creates
  nothing from thin air — catalog membership is validated at role save (400).
- **Agent not provisioned in env**: role/group assignment fails visibly;
  pickers pre-empt by disabling those agents.
- **New environment added later**: starts with no groups/roles (per-env by
  design); identity provisioning for the new env is covered by the existing
  `ProvisionForEnvironmentIfMissing` hook.
- **Gateway missing policies**: setting an env's mode to Agent Identity fails
  validation for gateways that don't report `mcp-auth` + `mcp-authz`.
- **Revoked/rotated agent secret**: irrelevant to grants — role/group
  membership is by Thunder agent ID, which is stable across secret rotation.
- **Mixed modes across envs**: an agent deployed to env A (identity) and
  env B (API key) gets `url`-only vars for A and `url`+`apikey` for B —
  per-env env-var branching already exists in the attach flow.

## 11. Testing

- **Unit** (per add-service-unit-test conventions: moq fakes, no build tags):
  - Scope catalog: CRUD, name validation, deletion 409 while bound.
  - Proxy validation: bindings vs catalog (400), stale-tool tolerance,
    identity-mode gateway policy-availability check.
  - YAML emission: identity policies present only for identity-mode envs,
    union `requiredScopes`, per-tool rules, no-bindings omission.
  - Agent-identity controller: passthrough mapping, lazy-ensure ordering
    (resource server + permissions before role write), env resolution errors
    surfaced, RBAC.
- **E2E** (`test/e2e/tests/mcpproxy`): identity-secured proxy lifecycle —
  create scopes, create proxy + bind tools, set env mode to Agent Identity,
  attach agent, create role (scopes) + assign agent, obtain a
  client_credentials token from env-Thunder, invoke a bound tool (200), an
  ungranted bound tool (403), no-token (401); repeat the granted-tool case
  with the role assigned via a group instead of directly.

## 12. Out of scope

- The MCP proxy restructure itself (per-env config storage, env-driven
  deployment, per-agent route removal + migration) — prerequisite, delivered
  separately (Section 3).
- API-key mode mechanics on the shared endpoint — resolved by the restructure
  owner before implementation starts.
- Token acquisition inside the agent pod (client-credential injection is
  separate, parallel work).
- `tools/list` filtering by scope (gateway policy behavior).
- Custom issuer selection on the proxy (v1 pins `ThunderKeyManager`).
- Scope rules for MCP resources/prompts/methods (tools only in v1; the
  policy schema supports the rest later).
- Cross-environment replication of groups/roles (per-env definition is
  deliberate).
- LLM provider/proxy identity security (MCP only).

## 13. Implementation notes

- Backend API work follows the `add-api-resource` skill (spec-first, codegen,
  per-route authz). Console work follows `add-console-api-feature`.
- Generators: `make am-gen-client`, `cd agent-manager-service && make codegen
  && make fmt`, `make spec`, wire if DI changes.
- New migration for `mcp_scopes` under
  `agent-manager-service/db_migrations/`.
- The agent-identity passthrough controller should share DTO mapping helpers
  with `identity_controller.go` where practical rather than duplicating them.
- Before building the Groups tab, run the Section 7.1 token verifications
  (a)–(c) against a live env-Thunder; (c) gates shipping groups.
