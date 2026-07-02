# Agent Identity & MCP Tool Authorization — Design

**Date:** 2026-07-02
**Status:** Approved design, pending implementation plan
**Scope:** Control plane end-to-end (agent-manager-service, console, CLI). MCP first; LLM providers follow the same model in a later phase. No gateway code changes required for v1.

## Problem

Agents deployed to the dataplane consume MCP servers through per-agent `McpProxyMapping` deployments on API Platform Gateways. Access is authenticated by a per-agent API key — a bearer secret with no claims, no verifiable identity, and no way to restrict *which tools* within an MCP server an agent may use. Tool capabilities are discovered and stored (`MCPProxyCapabilities.Tools`) but never enforced: an agent bound to an MCP proxy gets every tool.

We need a first-class **agent identity** that:

1. is verifiable at the gateway (not a bare secret),
2. supports per-tool restrictions per agent,
3. does not grow the token with the number of tools/permissions (the "scope problem"),
4. allows permission changes without token reissue or agent restart,
5. extends later to LLM providers and other governed dependencies,
6. is grounded in ThunderID's actual capabilities.

## Core principle

> **The token says who you are. The per-agent gateway artifact says what you may do.**

The agent's token carries only identity claims and stays constant-size forever. Tool permissions are rendered by the control plane into the agent's own `McpProxyMapping` deployment YAML as `mcp-authz` policy rules, and re-pushed through the existing artifact mechanism whenever permissions change.

## Architecture

### Identity: ThunderID first-class agents

Thunder (deployed via `wso2-amp-thunder-extension`) supports first-class agent identities (`POST /agents`, verified against `thunder-id/thunderid` `api/agent.yaml`):

- Created in an OU, with `type`, `owner`, free-form `attributes`, and an OAuth2 inbound profile: `clientId`/`clientSecret`, `grantTypes: [client_credentials]`, per-agent token `validityPeriod`, scopes. Secret returned once.
- Client-credentials access tokens carry: `sub` = the Thunder agent ID (`granthandlers/client_credentials.go:155`), `aud`, granted scopes, and `ouId`/`ouName`/`ouHandle` (`tokenservice/utils.go:464`). Agent `attributes` do **not** flow into tokens — identity pinning therefore uses `sub`.

The control plane provisions **one Thunder agent per (agent component × environment)**, so a dev-environment token cannot be replayed against a prod mapping (different `sub`). Thunder `attributes` record `component_uid`, `environment_uid`, `project_uid`, and org for audit. This replaces the homegrown `agent_token_manager` JWTs as the agent's runtime credential; Thunder's token-exchange/OBO grants become available later for on-behalf-of-user flows.

### Credential delivery: inject client credentials

The agent pod receives the Thunder client credentials and performs the standard OAuth2 client-credentials call itself:

- Injected env vars (identity-level, once per agent, not per binding): `AMP_AGENT_CLIENT_ID`, `AMP_AGENT_CLIENT_SECRET`, `AMP_AGENT_TOKEN_URL`.
- Internal agents: secret delivered via the existing SecretReference mechanism; external agents: returned in the API response, as with API keys today.
- Client secret is stored in OpenBao; a token-refresh helper is planned for the amp SDK/instrumentation library (post-v1; v1 documents the standard call).

(Considered and deferred: CP-minted long-lived token injection. Rejected for now in favor of standard short-lived tokens with agent-side refresh.)

### Runtime flow

```
CP provisions (at first deploy to an environment):
  Thunder POST /agents → agent identity in org OU, client_credentials OAuth profile
  creds → OpenBao; env vars injected into agent

Agent pod:  client_credentials → Thunder token  {sub: <thunder-agent-id>, ou claims}
  │  Authorization: Bearer <token>
  ▼
Gateway — the agent's own McpProxyMapping deployment
  ├─ mcp-auth  : validates JWT; issuer = Thunder (registered via existing
  │              gateway_identity_providers + GatewayConfigApplier, migration 025)
  ├─ mcp-authz : rules rendered from the binding's tool allowlist (below)
  ▼
Upstream MCP server (backend-auth policy unchanged)
```

### Tool authorization: sentinel-scope encoding in mcp-authz

The gateway's existing `mcp-authz` policy evaluates per-tool rules configured **per MCP deployment** with semantics: exact-name rules before wildcard, *all matching rules must pass*, default-allow when no rule matches. Rules test `requiredScopes` and/or `requiredClaims` against the validated token.

Because each agent has its own mapping deployment, the CP renders per-agent rules:

```yaml
- name: mcp-auth
  version: v1
  params:
    issuers: [<thunder-idp-name-on-this-gateway>]
- name: mcp-authz
  version: v1
  params:
    tools:
      # identity pinning: this mapping only serves THIS agent
      - name: "*"
        requiredClaims:
          sub: "<thunder-agent-id>"
      # one unsatisfiable rule per RESTRICTED tool
      - name: <restricted-tool>
        requiredScopes: ["amp:never-issued"]
```

- **Allowed tool** → matches only the wildcard → passes via `sub`.
- **Restricted tool** → also matches its sentinel rule; no token ever carries `amp:never-issued` → 403.
- **Foreign token** (another agent, another env) → wildcard `sub` check fails → 403.
- Restricted set = `capability snapshot − allowedTools`, computed at render time.

The token needs **zero scopes**; permission changes re-render one YAML artifact through the existing push mechanism — no token reissue, no agent restart, no gateway calls to the CP on the hot path.

**Known limitation (accepted for v1):** the policy is default-allow, which forces a deny-list encoding (an allowlist encoding is impossible: a wildcard-deny rule would poison allowed tools, since all matching rules must pass). If the upstream MCP server adds a new tool after rendering, it is allowed until the next render. Capability refresh is **manual only** — a "Refresh tools" button in the console (no automated polling). Longer-term: request a `defaultAction: deny` parameter from the gateway team. Secondary limitation: `mcp-authz` 403s `tools/call` but does not filter `tools/list`, so restricted tools remain visible to the agent — feature request to the gateway team, not a blocker.

## Data model

**New table `agent_identities`** (new migration):

```
uuid                PK
organization_name   TEXT
component_uuid      UUID
environment_uuid    UUID
thunder_agent_id    TEXT
client_id           TEXT
secret_ref          TEXT          -- OpenBao path
status              TEXT          -- active | revoked
created_at / updated_at
UNIQUE (component_uuid, environment_uuid)
```

**MCP binding (`agent_configurations`, type `mcp`):**

- `allowed_tools` — `null` = allow all (default, backward compatible); `[]` = deny all; list = allow exactly these.
- `auth_mode` — `apiKey | agentIdentity`. Existing bindings remain `apiKey`; new bindings default to `agentIdentity` only when the target gateway's manifest advertises `mcp-auth` + `mcp-authz`, else fall back to `apiKey` with a warning.

Capability snapshots stay where they are today (`mcp_proxies.configuration.capabilities`), updated only by explicit refresh.

## Services

- **`AgentIdentityProvisioner`** (new; modeled on `publisher_credential_provisioner.go`): `EnsureAgentIdentity(component, env)` — idempotent create-or-get; `RotateSecret`; `DeleteIdentity` on agent delete/undeploy (best-effort + later reconcile sweep). Thunder client gains `/agents` CRUD (`clients/thundersvc`).
- **Policy rendering** (`mcp_proxy_deployment.go`): for `agentIdentity` bindings, replace the API-key policy with `mcp-auth` + `mcp-authz` as above. CP verifies the Thunder issuer is registered on the target gateway at deploy time and registers it if missing (existing `gateway_identity_providers` + `GatewayConfigApplier`).
- **Env-var injection** (`agent_configuration_service.go`): `<CFG>_URL` per binding unchanged; `AMP_AGENT_CLIENT_ID/SECRET/TOKEN_URL` once per agent.

## API surface (spec-first, existing route conventions)

- `POST/PUT /orgs/{org}/projects/{proj}/agents/{agent}/mcp-configs` — request gains `allowedTools?: string[]`.
- `POST /orgs/{org}/mcp-proxies/{proxyId}/refresh-capabilities` — re-runs `FetchServerInfo`, updates the stored snapshot; backs the console button.
- `GET /orgs/{org}/projects/{proj}/agents/{agent}/identities` — list per-env identities (no secrets).
- `POST .../identities/{env}/rotate-secret` — rotate; requires new permission `amp:agent:manage-identity`.
- Allowlist editing rides on existing agent-config permissions.

## Console / CLI

- Tool picker (checkbox list from the capability snapshot) on the MCP binding form, with "Refresh tools" button.
- Allowlist entries not present in the current snapshot are kept but flagged ("unknown tool — refresh?").
- Agent identity panel: per-env identity status, rotate action.
- CLI: `allowedTools` flags on mcp-config commands; identity list/rotate commands.

## Error handling

- Thunder provisioning failure → deploy fails with actionable error; retry safe (idempotent ensure).
- Gateway lacks required policies → explicit `apiKey` fallback + warning; never a silently unenforced allowlist.
- Capability refresh failure → error surfaced, previous snapshot kept.
- Secret rotation → OpenBao + Thunder updated; internal agents pick up via SecretReference sync; already-issued tokens remain valid until expiry (documented).
- Denied tool calls → gateway 403 with reason + `WWW-Authenticate`; visible in traces.

## Testing

- Unit (moq, existing conventions): provisioner lifecycle, allowlist validation, auth-mode fallback selection.
- **Golden tests for policy rendering**: (allowlist, snapshot, identity) → exact `mcp-auth`/`mcp-authz` YAML, covering `null`/`[]`/list semantics and sub-pinning.
- Integration (k3d + thunder-extension): deploy sample agent with restricted binding; assert allowed tool succeeds, restricted tool 403s, foreign token 403s.

## Rollout

1. Migration + `AgentIdentityProvisioner` + Thunder `/agents` client.
2. Issuer auto-registration + policy rendering behind `auth_mode`.
3. API: `allowedTools`, `refresh-capabilities`, identity endpoints.
4. Console + CLI.
5. Later: LLM-provider extension (same identity, analogous authz policy), SDK token-refresh helper, gateway `defaultAction: deny` and `tools/list` filtering feature requests.

## Decisions log

| Decision | Choice | Alternatives rejected |
|---|---|---|
| Identity mechanism | Thunder first-class agents, client_credentials | Homegrown CP-signed JWT (parallel identity silo); scopes-in-token (token bloat, reissue on change) |
| Permission location | Per-agent mapping artifact (`mcp-authz` rules) | Claims in token (size + staleness); runtime CP lookup (hot-path dependency) |
| Tool-restriction encoding | Sentinel-scope deny rules + `sub` wildcard pin | Pure allowlist encoding (impossible under all-must-pass + default-allow) |
| Credential delivery | Inject client ID/secret; agent fetches tokens | CP-minted long-lived token injection (weak revocation; deferred) |
| Allowlist granularity | Per binding, uniform across envs | Per-env allowlists (more surface; add later if needed) |
| Default posture | `null` = allow all (backward compatible) | Deny by default (breaks existing bindings) |
| Capability refresh | Manual UI repoll only | Automated polling (explicitly rejected) |
