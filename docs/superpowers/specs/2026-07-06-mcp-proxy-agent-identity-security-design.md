# MCP Proxy Agent-Identity Security — Design

Date: 2026-07-06
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

- Proxy authors define named scopes and bind them (many-to-many) to the
  proxy's tools.
- Platform operators grant scopes to agents per environment; grants are synced
  into env-Thunder so tokens carry them.
- The dataplane gateway validates the JWT against the env-Thunder JWKS and
  enforces the per-tool scope rules.

## 2. Decisions (from brainstorm Q&A)

| Topic | Decision |
|---|---|
| Scope model | Many-to-many, user-named scopes bound to tools |
| Security modes | Exclusive: None / API Key / Agent Identity |
| Grant management | Agent identity page, per environment, catalog picker |
| Grant storage | AMS DB is desired state; synced to Thunder (write-ahead + reconciler) |
| Thunder grant mechanism | Roles: scopes attach to a role; the role is assigned to the agent |
| Per-agent mapping routes | Kept in identity mode (policy swapped, no key minted/injected) |
| Gateway policies | Existing fixed-schema `mcp-auth` v1 + `mcp-authz` v1 |
| JWKS trust | Gateway-level `config.toml` key managers (already registered by `add-environment.sh` as `ThunderKeyManager`); no JWKS URLs in deployment YAML |
| Issuer selection | Always `ThunderKeyManager` in v1 (no picker) |
| Unbound tools | Authenticated-only (gateway default-permit) |
| tools/list filtering | Gateway behavior; not designed here |
| Token acquisition by agent | Out of scope (pod client-credential injection is separate work) |
| External agents | Supported identically (they claim credentials and fetch tokens via the external token URL) |

## 3. Architecture

```
agent pod ──client_credentials──> env-Thunder (issues JWT with role-derived scopes)
    │
    │  Bearer JWT
    ▼
dataplane gateway (per env)
    ├─ mcp-auth v1: validates JWT via ThunderKeyManager (config.toml key manager,
    │   issuer = Thunder public issuer, JWKS = internal cluster DNS)
    ├─ mcp-authz v1: per-tool requiredScopes rules from proxy config
    ▼
upstream MCP server
```

Control plane (AMS) responsibilities:

1. Store scope definitions + tool bindings in the proxy's `MCPProxyConfig`.
2. Emit `mcp-auth`/`mcp-authz` policies in the deployment YAML for the base
   route and every per-agent mapping (mappings copy the source `Security`, so
   one code path covers both).
3. Store per-agent-per-env scope grants and sync them to env-Thunder as role
   permissions + a role assignment on the agent identity.
4. Serve a scope catalog (aggregated across identity-enabled proxies) for the
   grant picker UI.

Key existing machinery this design reuses (no changes needed):

- `add-environment.sh` registers `ThunderKeyManager` (env-Thunder issuer +
  internal JWKS URL) into each env gateway's
  `policy_configurations.jwtauth_v1.keymanagers` at environment creation.
- `models.SystemIdentityProviderNames` already seeds `ThunderKeyManager` as an
  undeletable system IdP.
- `thundersvc` client already implements resource servers, roles
  (create/update/delete, `AddRolePermissions`/remove), and role assignments.
- `AgentThunderClient` write-ahead + `AgentThunderReconcilerService` establish
  the sync pattern grants will mirror.
- `RedeployMCPMappingsForSourceProxy` already propagates proxy config changes
  to all agent mappings on save.

## 4. Data model

### 4.1 SecurityConfig extension (`models/llm_provider.go` / shared)

```go
type SecurityConfig struct {
    Enabled  *bool             `json:"enabled,omitempty"`
    APIKey   *APIKeySecurity   `json:"apiKey,omitempty"`
    Identity *IdentitySecurity `json:"identity,omitempty"` // new
}

// IdentitySecurity configures JWT (agent-identity) security for an MCP proxy.
type IdentitySecurity struct {
    Enabled      *bool                 `json:"enabled,omitempty"`
    Scopes       []MCPScopeDefinition  `json:"scopes,omitempty"`
    ToolBindings []MCPToolScopeBinding `json:"toolBindings,omitempty"`
}

type MCPScopeDefinition struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
}

type MCPToolScopeBinding struct {
    Tool   string   `json:"tool"`
    Scopes []string `json:"scopes"` // names from IdentitySecurity.Scopes
}
```

Stored inside `MCPProxyConfig.Security` (JSONB `configuration` column) exactly
like `APIKey` today. No proxy-side migration.

Validation on create/update:

- `APIKey.Enabled` and `Identity.Enabled` must not both be true (400).
- Scope names: `^[A-Za-z0-9:._\-]{1,256}$`, unique within the proxy.
- Every `ToolBindings[].Scopes` entry references a defined scope (400
  otherwise).
- Bindings whose `Tool` is absent from current `capabilities.tools` are
  accepted (upstream tool lists drift); the console flags them.
- Enabling identity mode is rejected for target gateways that do not report
  both `mcp-auth` and `mcp-authz` via the existing MCP policy-availability
  mechanism.

### 4.2 New table `agent_mcp_scope_grants`

Mirrors the `agent_thunder_clients` retry shape:

| Column | Notes |
|---|---|
| `id` | PK |
| `org_name`, `project_name`, `agent_name`, `environment_name` | unique together |
| `scopes` | JSONB array of scope strings (desired state) |
| `status` | `PENDING` / `SYNCED` / `FAILED` |
| `thunder_role_id` | Thunder role backing this grant set (empty until first sync) |
| `attempt_count`, `next_retry_at`, `last_error` | reconciler bookkeeping |
| `created_at`, `updated_at` | timestamps |

The row is the desired state; Thunder holds the enforced state. Scope strings
are intentionally not foreign-keyed to proxies: a grant may reference a scope
that a proxy later renames or deletes — harmless (no rule requires it), and
the UI badges unknown grants.

## 5. Deployment YAML emission

New `appendMCPIdentityAuthPolicies` beside `appendMCPAPIKeyAuthPolicy` in
`services/mcp_proxy_deployment.go`, applied in `buildMCPProxyDeploymentYAML`
when `Security.Enabled && Security.Identity.Enabled`:

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
  its own `config.toml` — per-env gateways therefore each trust their own
  env-Thunder with zero per-env YAML variance.
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
- Because `buildAgentMCPConfigProxy` copies the source proxy's `Security`
  into each mapping, mappings emit the same policies automatically.

### 5.1 Agent-config flow changes (`services/agent_configuration_service.go`)

Branch on the proxy's security mode:

- **API key mode** (existing): mint mapping key, broadcast, inject `url` +
  `apikey` env vars.
- **Identity mode** (new): skip key mint/broadcast/secret entirely; inject
  only the `url` env var (`buildMCPEnvVars` already omits `apikey` when
  `secretRefName` is empty).
- **Mode switches** on a proxy with live mappings: proxy update already
  triggers `RedeployMCPMappingsForSourceProxy`. Add: switching API key →
  identity revokes existing mapping keys (reuse
  `revokeAllMCPMappingAPIKeys`) and removes the injected `apikey` env var;
  switching identity → API key mints keys via the existing create path.

## 6. Grant sync to env-Thunder

### 6.1 Thunder modeling

Per environment's Thunder instance:

1. **Resource server**: one well-known resource server with identifier
   `amp-mcp-scopes`, created lazily on first grant sync in that env-Thunder.
   Every granted scope string is ensured as a permission under it.
2. **Role per agent per environment**: name
   `amp-agent-<project>-<agent>`, derived by a new helper in
   `thundersvc/naming.go` following its existing sanitize/truncate+hash
   conventions, in the default OU. The role's permission set is exactly the agent's granted scopes in that
   environment. The role is assigned to the agent's Thunder identity
   (`ThunderAgentID` from its `AgentThunderClient` binding) once, on first
   sync.

Grant update ⇒ reconcile the role's permissions to the desired set
(`AddRolePermissions` / remove — both exist in `thundersvc`). Empty grant set
⇒ clear the role's permissions (keep role + assignment; cheap and avoids
re-assignment races). Agent deletion ⇒ delete the role and grant rows —
extend the existing `DeleteAllBindings` flow.

Implementation-time verification (assumption, approved but unverified in
code): confirm against the deployed Thunder version (`thunderid-0.45.0` per
code comments) that (a) roles are assignable to `/agents` identities, and
(b) `client_credentials` tokens carry role-derived scopes in the `scope`
claim.

### 6.2 Sync mechanics

Mirror the `AgentThunderClient` pattern established on this branch:

- `PUT` grant endpoint upserts the row with `status=PENDING`,
  `next_retry_at=nil`, then fires a detached background attempt
  (`context.WithoutCancel`).
- A reconciler sweep (same shape as `AgentThunderReconcilerService`: claim
  via compare-and-swap, flat 3-minute backoff, 5-attempt budget → `FAILED`)
  retries PENDING rows.
- If the agent's Thunder binding in that env is not yet `COMPLETED`, the
  attempt records a transient failure and retries — grants heal once identity
  provisioning lands.
- `ErrThunderNotProvisioned` is a permanent failure, as in provisioning.

## 7. API surface (spec-first per add-api-resource workflow)

1. **Proxy DTO**: extend the `SecurityConfig` schema in
   `agent-manager-service/docs/api_v1_openapi.yaml` with the `identity` block;
   regenerate spec server code, `am` CLI client, and console types.
2. **Scope catalog** (backs the grant picker):
   `GET /orgs/{orgName}/mcp-scope-catalog`
   → `[{scope, description, proxies: [{id, name, tools: [string]}]}]`,
   aggregated by scanning identity-enabled MCP proxies in the org.
3. **Grants**:
   `GET|PUT /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envName}/mcp-scope-grants`
   - GET → `{scopes: [string], status, lastError?}`
   - PUT `{scopes: [string]}` → replaces the desired set, returns the row.
   - PUT validates scope-name syntax only (not catalog membership — grants may
     outlive catalog entries).
   - PUT with an empty `scopes` array clears the grant (role permissions
     emptied in Thunder); there is no separate DELETE endpoint.
4. **RBAC**: new permission entries per route in `rbac/permissions.go`,
   following the per-route-authz workflow.

## 8. Console UI

### 8.1 MCP proxy Security tab (`MCPProxySecurityTab.tsx`)

- Security type selector: **None | API Key | Agent Identity** (exclusive;
  replaces the current boolean-style API key toggle presentation).
- Agent Identity panel:
  - Scope editor: rows of name + optional description; add/remove.
  - Tool binding table: one row per tool from `capabilities.tools`, each with
    a multi-select of defined scopes. Unbound tools show an
    "authenticated only" hint.
  - Warning badges for bindings referencing tools or scopes that no longer
    exist.
- Save uses the existing proxy update flow (redeploys base route + mappings).
- Creation form (`AddMCPProxyForm`) unchanged — identity is configured
  post-creation on the Security tab (decision).

### 8.2 Agent identity page (per-environment rows)

- New scope-grants section per environment: catalog picker (grouped by proxy,
  showing which tools each scope unlocks), granted-scope chips, and a sync
  status chip (Pending / Synced / Failed with `lastError` detail).
- Uses the two-file API pattern (`apis/` + `hooks/`) per
  add-console-api-feature.

## 9. Errors and edge cases

- **Thunder outage**: grants stay PENDING with 3-minute retries; FAILED after
  the 5-attempt budget with `last_error` surfaced; editing the grant re-queues.
- **Scope renamed/deleted on a proxy**: grants keep the stale string
  (harmless — no rule requires it); the catalog stops offering it; the UI
  badges unknown grants.
- **Tool removed upstream**: its binding remains in config (flagged in UI);
  the emitted `mcp-authz` rule for a nonexistent tool never matches and is
  inert.
- **New environment added later**: grants are per-env; nothing to heal.
  Identity provisioning for the new env is covered by the existing
  `ProvisionForEnvironmentIfMissing` hook.
- **Gateway missing policies**: enabling identity mode fails validation for
  gateways that don't report `mcp-auth` + `mcp-authz`.
- **Grant before identity provisioning completes**: transient failure; the
  reconciler retries until the Thunder binding is `COMPLETED`.
- **Revoked/rotated agent secret**: irrelevant to grants — role assignment is
  by Thunder agent ID, which is stable across secret rotation.

## 10. Testing

- **Unit** (per add-service-unit-test conventions: moq fakes, no build tags):
  - YAML emission: identity policies present/absent by mode, union
    `requiredScopes`, per-tool rules, mode exclusivity validation.
  - Grant service: claim semantics, backoff schedule, attempt budget,
    binding-not-ready transient path, role permission reconciliation calls.
  - Controllers: catalog aggregation, grant GET/PUT validation, RBAC.
- **E2E** (`test/e2e/tests/mcpproxy`): identity-secured proxy lifecycle —
  create proxy with scopes + bindings, attach to agent, grant scopes, obtain a
  client_credentials token from env-Thunder, invoke a bound tool (200), an
  ungranted bound tool (403), and no-token (401).

## 11. Out of scope

- Token acquisition inside the agent pod (client-credential injection is
  separate, parallel work).
- `tools/list` filtering by scope (gateway policy behavior).
- Custom issuer selection on the proxy (v1 pins `ThunderKeyManager`).
- Scope rules for MCP resources/prompts/methods (tools only in v1; the
  policy schema supports the rest later).
- LLM provider/proxy identity security (MCP only).

## 12. Implementation notes

- Backend API work follows the `add-api-resource` skill (spec-first, codegen,
  per-route authz). Console work follows `add-console-api-feature`.
- Generators: `make am-gen-client`, `cd agent-manager-service && make codegen
  && make fmt`, `make spec`, wire if DI changes.
- New migration for `agent_mcp_scope_grants` under
  `agent-manager-service/db_migrations/`.
