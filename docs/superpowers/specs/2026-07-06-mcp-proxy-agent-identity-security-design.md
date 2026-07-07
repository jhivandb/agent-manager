# MCP Proxy Agent-Identity Security — Design

Date: 2026-07-06 (amended 2026-07-06: role/group grants, first-class scopes,
per-env proxy security, per-agent routes removed; amended 2026-07-07:
reconciled to the finalized restructure model delivered by PR #1258 —
capabilities/policies/security/tool-bindings are all per-environment, security
is expressed by a `SecurityConfig` variant rather than a stored mode enum, and
per-agent mapping rows are retained as the API-key issuance vehicle)
Status: Approved (brainstorm concluded; ready for implementation planning)
Branch context: builds on `task/agent-id-2a` (per-agent Thunder identity
provisioning); restructure prerequisites delivered by PR #1258 (`mcp-proxy-ux-revamp`)

## 1. Problem

MCP proxies today support exactly one security mode: a gateway `api-key-auth`
policy plus a per-agent API key that AMS mints and injects into the agent pod.
This branch gives every agent a Thunder identity (a `client_credentials` OAuth
client in the environment's Thunder instance), which enables a better
alternative: the agent authenticates to the MCP proxy with a JWT issued by its
environment's IdP, and the gateway authorizes individual MCP tools based on
OAuth scopes carried in that token.

This spec adds an **Agent Identity** security mode to MCP proxies:

- Platform operators define **org-global scopes** in a new catalog. Scopes are
  **resource-agnostic**: the same scope entity can authorize an LLM provider and
  an MCP tool. This spec consumes them for MCP tool bindings, but they are not
  MCP-specific and the catalog is shared platform-wide.
- Proxy authors bind catalog scopes (many-to-many) to a proxy environment's
  tools. Bindings are **per environment**: they live in each environment's
  configuration block alongside that environment's capabilities and security,
  so different environments may carry different scope contracts.
- Grants are expressed through **Thunder roles and groups**: a role carries
  scope permissions; roles are assigned to agents directly or to groups the
  agent belongs to. Agents, groups, and roles are managed in a new
  **Agent Identity** console section modeled on the existing user-management
  (identities) section.
- The dataplane gateway validates the JWT against the env-Thunder JWKS and
  enforces the per-tool scope rules.

The proxy exposure model is **one endpoint per environment**: every agent in
an environment calls the same shared proxy artifact (one gateway artifact per
configured environment). There are no per-agent gateway routes. Per-agent
mapping rows still exist, but only to mint per-agent inbound API keys against
the shared artifact in API-Key mode; Agent Identity mode mints no per-agent
keys. Security configuration is per environment on the proxy.

## 2. Decisions (from brainstorm Q&A, both sessions)

| Topic | Decision |
|---|---|
| Scope model | First-class, org-global, **resource-agnostic** entities in a new AMS `scopes` table (the catalog); the same scope can gate LLM providers and MCP tools. Proxy environments and roles reference them by name |
| Scope authoring UI | Dedicated Scopes tab in the Agent Identity section (org-global, shared catalog; ignores the env picker) |
| Tool binding | Security tab picker matches catalog scopes to the selected environment's `capabilities.tools`; stored **per environment** in that env's config block; post-creation only (tool discovery happens after create) |
| Security modes | Per environment on the proxy: None / API Key / Agent Identity. There is no stored mode enum — mode is realized by which variant of the per-env `SecurityConfig` is populated (none, `apiKey`, or a new `identity`). Capabilities, policies, security, and tool bindings are **all** per-environment |
| Grant mechanism | Thunder-native: roles → agents (direct) and roles → groups → agents; token scopes = union of both paths |
| Grant management | Agent Identity console section (env picker; Scopes / Groups / Roles tabs), based on the identities section |
| Grant storage | None in AMS — direct passthrough to the selected environment's Thunder via `EnvThunderResolver`, mirroring `identity_controller.go`'s passthrough to org Thunder. No desired-state rows, no reconciler |
| Scope→Thunder provisioning | Lazy ensure at role save: AMS ensures the `amp-scopes` resource server + referenced permissions exist in that env-Thunder, then writes the role |
| Per-agent routes | Collapsed into one shared gateway artifact per environment (`MCPEnvironmentConfig.ArtifactUUID`). The `mcp_proxy_mappings` / `env_agent_mcp_mapping` rows are **retained** and repurposed: they mint per-agent inbound API keys against the shared artifact (API-Key mode only) and drive per-agent env-var injection |
| Proxy deployment | Environment-driven: `deployMCPProxyEnvironments` deploys one artifact per configured environment, resolving the env's gateway via `resolveGatewayForEnvironment`; explicit gateway-ID selection is gone |
| API-key mode on shared endpoint | **Resolved by PR #1258**: per-agent named keys minted against the shared per-env artifact — `apiID = MCPEnvironmentConfig.ArtifactUUID` (the gateway-facing artifact), `storageUUID` = per-agent key-holder — so many agents share one gateway artifact with per-agent issuance/revocation |
| Gateway policies | Existing fixed-schema `mcp-auth` v1 + `mcp-authz` v1 |
| JWKS trust | Gateway-level `config.toml` key managers (already registered by `add-environment.sh` as `ThunderKeyManager`); no JWKS URLs in deployment YAML |
| Issuer selection | Always `ThunderKeyManager` in v1 (no picker) |
| Unbound tools | Authenticated-only (gateway default-permit) |
| tools/list filtering | Gateway behavior; not designed here |
| Token acquisition by agent | Out of scope (pod client-credential injection is separate work) |
| External agents | Supported identically (they claim credentials and fetch tokens via the external token URL) |

Superseded decisions from earlier drafts (removed machinery):

- `agent_mcp_scope_grants` table, grant reconciler, and synthetic
  `amp-agent-<project>-<agent>` roles — replaced by user-managed roles/groups.
- Scope definitions inside proxy config (`IdentitySecurity.Scopes`) — replaced
  by the org-global catalog.
- Per-agent grant endpoints and the proxy-scanning scope-catalog endpoint —
  replaced by catalog CRUD + Thunder passthrough routes.
- Per-agent mapping route policy emission and per-mapping key mint/inject —
  per-agent gateway routes no longer exist; the shared per-env artifact is the
  only route, and per-agent keys are minted against it.
- **Proxy-global scopes and tool bindings** (from the 2026-07-06 amendment) —
  superseded by per-environment bindings, because PR #1258's finalized model
  stores capabilities/policies/security per environment and leaves no
  proxy-global place for a blueprint proxy to carry them.
- **A per-env security *mode enum*** (from the 2026-07-06 amendment) —
  superseded: PR #1258 reuses the LLM-style `SecurityConfig` (`{Enabled, APIKey}`),
  so mode is expressed by the populated variant, extended here with `identity`.

## 3. The delivered restructure (PR #1258)

This spec builds on the MCP proxy restructure delivered by PR #1258
(`mcp-proxy-ux-revamp`, "Mcp proxy ux revamp"). Its shape, as built, that this
spec depends on:

1. `MCPProxyConfig` carries `Environments map[string]MCPEnvironmentConfig`
   keyed by **environment UUID**. Each `MCPEnvironmentConfig` block holds that
   environment's `Upstream` (single endpoint), `Policies`, `Capabilities`,
   `Security`, and a stable `ArtifactUUID` (the one gateway artifact deployed
   for that environment). The org-level proxy is a **blueprint** that deploys
   nothing itself; the flat root-level fields exist only for the per-env
   artifact the builder flattens out. There is **no stored security "mode"
   field** — `Security` is the shared `{Enabled, APIKey}` `SecurityConfig`.
2. **One deployed gateway artifact per environment** (`ArtifactUUID`), reachable
   at the proxy's shared context. The `mcp_proxy_mappings` /
   `env_agent_mcp_mapping` rows are **not removed** — they are retained and
   reference the shared artifact to mint per-agent inbound API keys and inject
   env vars. There is **no migration/backfill**: pre-existing proxies must be
   re-saved into the UUID-keyed `environments` shape before the new deployment
   behavior applies (breaking change owned by the restructure).
3. Deployment is environment-driven: `deployMCPProxyEnvironments` iterates the
   configured environments and resolves each env's gateway via
   `resolveGatewayForEnvironment(envUUID, orgName)`, skipping envs with no
   active gateway (`errNoActiveGatewayForEnvironment`). **Caveat this spec
   inherits:** `resolveGatewayForEnvironment` is still an AI-first heuristic
   (now env-scoped via an `EnvironmentID` filter) and is duplicated on both
   `MCPProxyService` and `agentConfigurationService`; the wrong-gateway concern
   is narrowed but not eliminated.
4. API-key mechanics on the shared endpoint are resolved (see the §2 table):
   per-agent named keys minted against the shared artifact
   (`createMCPMappingAPIKey(apiID=ArtifactUUID, storageUUID=per-agent)`).
5. Attach remains the trigger for env-var injection: in identity mode the agent
   pod receives only the proxy `url` env var (`buildMCPEnvVars` /
   `buildEmptyMCPEnvVars` omit `apikey` when the mapping has no `secretRefName`).

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
- `appendMCPAPIKeyAuthPolicy` and the per-env artifact flatten/deploy path
  (`buildMCPProxyEnvArtifact` → `generateMCPProxyDeploymentYAML`) are the
  insertion points for identity policy emission (§6).

## 4. Architecture

```
                    Agent Identity console section (env picker)
                    Scopes tab ──────────► AMS scopes table (org-global, resource-agnostic catalog)
                    Groups/Roles tabs ───► env-Thunder (direct passthrough,
                                            EnvThunderResolver; role save
                                            lazy-ensures scope permissions)

agent pod ──client_credentials──► env-Thunder
    │        (JWT scopes = union of direct-role and group-role permissions)
    │  Bearer JWT
    ▼
dataplane gateway (per env) — ONE shared proxy artifact per environment
    ├─ mcp-auth v1: validates JWT via ThunderKeyManager (config.toml key manager)
    ├─ mcp-authz v1: per-tool requiredScopes from THAT ENVIRONMENT's tool bindings
    ▼
upstream MCP server
```

Control plane (AMS) responsibilities:

1. Own the org-global, resource-agnostic scope catalog (`scopes` CRUD; deletion
   blocked while referenced by any resource binding — MCP proxy tool bindings in
   this spec, LLM-provider bindings when those land).
2. Store tool→scope bindings and the security mode **per environment** (inside
   each `MCPEnvironmentConfig`, keyed by env UUID).
3. Emit `mcp-auth`/`mcp-authz` policies into each environment's flattened
   gateway artifact when that environment's security is Agent Identity.
4. Proxy agent-identity group/role operations to the selected env-Thunder;
   on role save, ensure the `amp-scopes` resource server and referenced
   permissions exist there first.

## 5. Data model

### 5.1 New table `scopes` (org-global, resource-agnostic catalog)

The catalog is a shared, org-global platform primitive: a scope is not
MCP-specific, and the same entity can authorize LLM providers and MCP tools.
This spec implements only the MCP-tool binding + gateway-enforcement side;
other resource bindings are separate work against the same catalog.

| Column | Notes |
|---|---|
| `id` | PK |
| `org_name` | unique together with `name` |
| `name` | `^[A-Za-z0-9:._\-]{1,256}$` |
| `description` | optional |
| `created_at`, `updated_at` | timestamps |

Deletion rule: rejected with 409 while referenced by any resource binding (MCP
proxy environment tool bindings in this spec's scope; LLM-provider bindings when
those land). Thunder roles referencing a deleted scope keep the stale permission
string — harmless (no gateway rule requires it once unbound) and warn-only in
the UI.

### 5.2 Per-environment security + bindings (identity mode shape)

Security and bindings live in the per-environment blueprint block that PR #1258
introduced. `SecurityConfig` is the shared LLM-style struct; Agent Identity adds
a third, mutually-exclusive variant, and the per-env block gains the tool
bindings:

```go
// Shared SecurityConfig today (models/llm_provider.go), reused as the per-env
// MCP security block (MCPEnvironmentConfig.Security):
//   type SecurityConfig struct {
//       Enabled *bool           `json:"enabled,omitempty"`
//       APIKey  *APIKeySecurity `json:"apiKey,omitempty"`
//   }

// Agent Identity adds a third variant. Mode is implied by which of APIKey /
// Identity is populated (both nil / Enabled false => None). This is a SHARED
// security primitive: LLM providers can select the Identity variant too — it is
// not MCP-specific.
type IdentitySecurity struct {
    Enabled *bool `json:"enabled,omitempty"`
    // v1 pins the issuer to ThunderKeyManager, so no issuer field yet.
}
// SecurityConfig gains:
//   Identity *IdentitySecurity `json:"identity,omitempty"`

// Per-environment tool bindings, stored in each MCPEnvironmentConfig alongside
// that env's Capabilities and Security:
type MCPToolScopeBinding struct {
    Tool   string   `json:"tool"`
    Scopes []string `json:"scopes"` // names from the org-global scopes catalog
}
// MCPEnvironmentConfig gains:
//   ToolScopeBindings []MCPToolScopeBinding `json:"toolScopeBindings,omitempty"`
```

Tool bindings are **per-environment**: an environment binds scopes against its
own `Capabilities.Tools`. Different environments may carry different contracts
(e.g. prod stricter than dev); this matches the finalized per-env grain of
capabilities/policies/security.

Validation on proxy create/update (per environment):

- Every binding scope must exist in the org catalog (400 otherwise).
- Bindings whose `Tool` is absent from that environment's current
  `capabilities.tools` are accepted (upstream tool lists drift); the console
  flags them.
- Setting an environment's security to Agent Identity is rejected if that env's
  gateway does not report both `mcp-auth` and `mcp-authz` via the existing MCP
  policy-availability mechanism.

## 6. Deployment YAML emission

`appendMCPIdentityAuthPolicies` beside `appendMCPAPIKeyAuthPolicy` in
`services/mcp_proxy_deployment.go`, invoked while the per-environment artifact
is flattened and deployed (`buildMCPProxyEnvArtifact` →
`generateMCPProxyDeploymentYAML`) for each environment whose `Security` variant
is Agent Identity. It reads that environment's own `ToolScopeBindings`:

```yaml
policies:
  - name: mcp-auth
    version: v1
    params:
      issuers:
        - ThunderKeyManager
      requiredScopes: [<union of this env's bound scopes>]  # metadata advertisement only
  - name: mcp-authz
    version: v1
    params:
      tools:
        - name: <tool>
          requiredScopes: [<scopes bound to this tool in this env>]
        # one entry per tool with at least one binding; unbound tools omitted
```

- `issuers` values are key-manager *names* resolved by each gateway against
  its own `config.toml` — per-env gateways each trust their own env-Thunder
  with zero per-env YAML variance.
- `requiredScopes` on `mcp-auth` is advertised in protected-resource metadata
  but not enforced (per policy docs); enforcement is `mcp-authz`.
- Tools without bindings get no `mcp-authz` rule → gateway default-permit →
  callable by any agent with a valid JWT (decision: authenticated-only).
- If an environment has no tool binding, the `mcp-authz` policy and the
  `requiredScopes` param are omitted entirely — identity mode then means "any
  valid env-Thunder JWT" for that environment.
- Existing policy normalize/merge machinery
  (`normalizeMCPPoliciesForDeployment`, `mergeMCPPoliciesForDeployment`)
  applies unchanged; `mcp-auth`/`mcp-authz` use the default override merge
  strategy.

## 7. Grants: Thunder roles and groups (direct management)

### 7.1 Thunder modeling

Per environment's Thunder instance, all in the default OU:

- **Resource server** `amp-scopes`: ensured lazily on role save; every
  scope referenced by the role is ensured as a permission under it before the
  role is written.
- **Roles**: user-created and user-named (no AMS-generated names). A role's
  permissions are catalog scope strings under `amp-scopes`. Assignees are
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
   - `GET /orgs/{orgName}/scopes` → list (this is the picker source for
     both the role editor and the per-env tool-binding picker)
   - `POST /orgs/{orgName}/scopes` `{name, description?}`
   - `PUT /orgs/{orgName}/scopes/{scopeName}` (description only; rename =
     delete + create, subject to the deletion rule)
   - `DELETE /orgs/{orgName}/scopes/{scopeName}` → 409 while bound by any
     proxy environment
2. **Agent-identity passthrough routes**, mirroring the `/identities/*` route
   shapes, controller modeled on `identity_controller.go` but resolving the
   client via `EnvThunderResolver(org, env)`:
   - `GET|POST /orgs/{orgName}/environments/{envName}/agent-identities/groups`
   - `GET|PUT|DELETE .../agent-identities/groups/{groupId}`
   - `POST .../groups/{groupId}/members/add` and `/members/remove` (agent members)
   - `GET .../groups/{groupId}/roles`
   - `GET|POST /orgs/{orgName}/environments/{envName}/agent-identities/roles`
   - `GET|PUT|DELETE .../agent-identities/roles/{roleId}` (PUT reconciles
     permissions after lazy-ensuring them under `amp-scopes`)
   - `GET .../roles/{roleId}/assignments`,
     `POST .../roles/{roleId}/assignments/add` and `/assignments/remove`
     (agent and group assignees)
   - `GET /orgs/{orgName}/environments/{envName}/agent-identities/agents` →
     agents in the org with their Thunder binding status + `ThunderAgentID`
     for that env (picker source)
3. **Proxy DTO**: extend the `MCPEnvironmentConfig` schema in
   `agent-manager-service/docs/api_v1_openapi.yaml` with per-env
   `toolScopeBindings` and an `identity` variant on its `security`; the DTO
   already exposes `environments` keyed by env UUID. Regenerate spec server
   code, the `am` CLI client, and console types.
4. **RBAC**: new permission entries per route in `rbac/permissions.go`,
   following the per-route-authz workflow.

## 9. Console UI

### 9.1 MCP proxy Security tab (`MCPProxySecurityTab.tsx`)

The tab already renders per environment (driven by `selectedEnvironmentId` +
the env's `MCPEnvironmentConfig`), with `authenticationType` currently
`"apiKey" | ""`. Extend it:

- Per-environment mode selector: **None | API Key | Agent Identity**
  (`authenticationType` gains `"identity"`, mapping to the env's
  `security.identity` variant).
- Tool binding table **for the selected environment**: one row per tool from
  that env's `capabilities.tools`, each with a multi-select fed by
  `GET /scopes`. Unbound tools show an "authenticated only" hint. Shown
  only when the env's mode is Agent Identity.
- Warning badges for bindings referencing tools that no longer exist in that
  environment's capabilities.
- Creation form (`AddMCPProxyForm`) unchanged — bindings are configured
  post-creation on the Security tab (tool discovery happens after create).

### 9.2 Agent Identity section (new; modeled on the identities section)

Environment picker at the top; three tabs:

- **Scopes** (org-global; unaffected by the env picker, labeled as such):
  catalog CRUD table — name, description, "in use by N bindings" indicator
  (MCP proxy tool bindings today, other resources later), delete disabled while
  bound.
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
  (and environments) listed. After unbinding, deletion succeeds; Thunder roles
  keep the stale permission string (inert; warn-only).
- **Tool removed upstream**: its binding remains in that env's config (flagged
  in UI); the emitted `mcp-authz` rule for a nonexistent tool never matches and
  is inert.
- **Role save referencing a scope deleted mid-edit**: lazy ensure re-creates
  nothing from thin air — catalog membership is validated at role save (400).
- **Agent not provisioned in env**: role/group assignment fails visibly;
  pickers pre-empt by disabling those agents.
- **New environment added later**: starts with no groups/roles (per-env by
  design) and no binding block until configured; identity provisioning for the
  new env is covered by the existing `ProvisionForEnvironmentIfMissing` hook.
- **Gateway missing policies**: setting an env's mode to Agent Identity fails
  validation for gateways that don't report `mcp-auth` + `mcp-authz`.
- **Revoked/rotated agent secret**: irrelevant to grants — role/group
  membership is by Thunder agent ID, which is stable across secret rotation.
- **Mixed modes across envs**: an agent deployed to env A (identity) and
  env B (API key) gets `url`-only vars for A and `url`+`apikey` for B —
  per-env env-var branching already exists in the attach flow, and each env's
  `SecurityConfig` variant is independent.
- **Pre-existing proxy not yet re-saved**: without a UUID-keyed `environments`
  block there is nowhere to set identity mode or bindings; the proxy must be
  re-saved into the new shape first (no backfill — inherited from PR #1258).

## 11. Testing

- **Unit** (per add-service-unit-test conventions: moq fakes, no build tags):
  - Scope catalog: CRUD, name validation, deletion 409 while bound.
  - Proxy validation: per-env bindings vs catalog (400), stale-tool tolerance,
    identity-mode gateway policy-availability check.
  - YAML emission: identity policies present only for identity-mode envs, per
    environment's own union `requiredScopes`, per-tool rules, no-bindings
    omission.
  - Agent-identity controller: passthrough mapping, lazy-ensure ordering
    (resource server + permissions before role write), env resolution errors
    surfaced, RBAC.
- **E2E** (`test/e2e/tests/mcpproxy`): identity-secured proxy lifecycle —
  create scopes, create proxy, set an environment's mode to Agent Identity +
  bind its tools, attach agent, create role (scopes) + assign agent, obtain a
  client_credentials token from env-Thunder, invoke a bound tool (200), an
  ungranted bound tool (403), no-token (401); repeat the granted-tool case
  with the role assigned via a group instead of directly.

## 12. Out of scope

- The MCP proxy restructure itself (per-env config storage, env-driven
  deployment, per-agent route collapse) — delivered by PR #1258 (Section 3).
- API-key mode mechanics on the shared endpoint — resolved by PR #1258
  (per-agent named keys minted against the shared per-env artifact).
- Migration/backfill of pre-existing proxies into the UUID-keyed `environments`
  shape — owned by the restructure; this spec inherits the "re-save required"
  behavior.
- Token acquisition inside the agent pod (client-credential injection is
  separate, parallel work).
- `tools/list` filtering by scope (gateway policy behavior).
- Custom issuer selection on the proxy (v1 pins `ThunderKeyManager`).
- Scope rules for MCP resources/prompts/methods (tools only in v1; the
  policy schema supports the rest later).
- Cross-environment replication of groups/roles (per-env definition is
  deliberate).
- Hardening/deduplicating `resolveGatewayForEnvironment` (AI-first heuristic
  duplicated across two services) — an inherited restructure concern, not
  addressed here.
- LLM-provider scope enforcement. The scope catalog and the Identity
  `SecurityConfig` variant are shared primitives (a scope can gate LLM providers
  too, and LLM providers can select the Identity variant), but this spec
  implements only MCP tool binding + gateway enforcement; wiring scopes into
  LLM-provider authorization is separate, parallel work.

## 13. Implementation notes

- Backend API work follows the `add-api-resource` skill (spec-first, codegen,
  per-route authz). Console work follows `add-console-api-feature`.
- Generators: `make am-gen-client`, `cd agent-manager-service && make codegen
  && make fmt`, `make spec`, wire if DI changes.
- New migration for the shared `scopes` catalog table under
  `agent-manager-service/db_migrations/`. No migration is added for existing
  MCP proxies (they re-save into the `environments` shape — inherited).
- The agent-identity passthrough controller should share DTO mapping helpers
  with `identity_controller.go` where practical rather than duplicating them.
- `Identity` lives on the shared `SecurityConfig` (models/llm_provider.go) and
  is deliberately usable by both LLM providers and MCP proxies — not MCP-only;
  keep the `apiKey`/`identity` variants mutually exclusive at validation.
- Before building the Groups tab, run the Section 7.1 token verifications
  (a)–(c) against a live env-Thunder; (c) gates shipping groups.
