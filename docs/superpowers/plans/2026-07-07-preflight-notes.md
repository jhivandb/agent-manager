# Preflight Notes — MCP Proxy Agent-Identity Security (Task 1)

Recorded during Task 1 of `2026-07-07-mcp-proxy-agent-identity-security.md`.
Verifies the merged base (PR #1258 code) and pins the anchor names / shapes the
plan builds on. **Scope of this execution: Phase 0 + Phase 1 only** (per user
direction — Phase 2 console and Phase 3 E2E are out of scope for now).

## Base state

- Working branch: `task/mcp-proxy-agent-identity`.
- PR #1258 (`mcp-proxy-ux-revamp`) is still **OPEN** upstream, but its head
  branch is merged locally into this working branch (commit `f50aaec0`
  "Merge branch 'pr-1258-head'"). The #1258 code is present and used below —
  the plan's precondition intent (have the #1258 environments-shape code) is
  satisfied even though the PR has not landed in `upstream/main`.
- Note: the plan named the continuation branch `task/agent-id-2a`; actual branch
  is `task/mcp-proxy-agent-identity` (this worktree). No functional impact.

## Backend anchors (all present ✓)

| Anchor | Location |
|---|---|
| `Environments map[string]MCPEnvironmentConfig` | `models/mcp_proxy.go:85` |
| `buildMCPProxyEnvArtifact` | `services/mcp_proxy_deployment.go:143` |
| `appendMCPAPIKeyAuthPolicy` | `services/mcp_proxy_deployment.go:496` |
| `deployMCPProxyEnvironments` | `services/mcp_proxy_deployment.go:224` |
| `validateMCPEnvironments` | `services/mcp_proxy_service.go:804` |
| `buildMCPEnvironmentsForStorage` | `services/mcp_proxy_service.go:832` |
| `defaultMCPProxySecurity` | `services/mcp_proxy_service.go:686` |
| `ListAvailableMCPPolicies` | `services/mcp_proxy_service.go:204` |
| `extractGatewayPolicyManifestItems` | `services/mcp_proxy_service.go:465` |
| `envThunderResolver.Resolve(ctx, orgName, envName)` | `clients/thundersvc/env_resolver.go:128` |
| `AddRolePermissions` (iface + impl) | `clients/thundersvc/identity_client.go:64,647` |
| `NewIdentityController` | `controllers/identity_controller.go:82` |
| `mcpProxyAPIKeySecurityEnabled` | `services/agent_configuration_service.go:333` |

## Migration version

- `db_migrations/migration_list.go`: `latestVersion = 28`, last entry `migration028`.
- **Task 3**: add `029_create_scopes.go`, append `migration029`, bump `latestVersion` → 29.

## Errors (`utils/errors.go`)

- `ErrInvalidInput` (line 97) and `ErrConflict` (line 105) exist.
- `ErrScopeNotFound` does **not** exist → **Task 4** adds it.

## CRITICAL: how the MCP proxy request maps to the model (affects Tasks 4 & 5)

- The MCP proxy controller (`controllers/mcp_proxy_controller.go`) decodes the
  request body **directly into `models.MCPProxyDTO`** (not the generated
  `spec.MCPProxyRequest`). Create: `var req models.MCPProxyDTO; json.Decode(&req)`;
  Update likewise.
- `models.MCPProxyDTO` (`models/mcp_proxy.go:136`) already has:
  - `Environments map[string]MCPEnvironmentConfig \`json:"environments,omitempty"\``
  - `Security *SecurityConfig \`json:"security,omitempty"\``
- **Consequence for Task 5:** `toolScopeBindings` (added inside
  `MCPEnvironmentConfig`) and `security.identity` (added inside `SecurityConfig`)
  flow through automatically via JSON tags — no controller/spec mapping change is
  required for them to work. The OpenAPI additions in Task 5 (adding
  `environments`, `MCPEnvironmentConfig`, `MCPToolScopeBinding`, `IdentitySecurity`
  to the spec) are **documentation-only** for this endpoint; they do not affect
  the runtime wire contract. Still worth doing for spec accuracy, but they are not
  on the functional critical path.
- OpenAPI state today (`docs/api_v1_openapi.yaml`):
  - `MCPProxyRequest` (line 13737) / `MCPProxyResponse` (13794): **no** `environments`
    field. The `environments:` at line 12860 is the **Gateway** schema's, unrelated.
  - No `MCPEnvironmentConfig` schema exists.
  - `SecurityConfig` (14366) has `enabled` + `apiKey` only. `APIKeySecurity` (14376) exists.

## CRITICAL: MCPProxyRepository shape (affects Task 4 `BindingCounts` + mocks)

The plan's Task 4 assumed an org-scoped `List(ctx, orgName) ([]models.MCPProxy, error)`.
**Actual signature** (`repositories/mcp_proxy_repository.go`):

```go
type MCPProxyRepository interface {
    List(ctx context.Context, orgUUID string, limit, offset int) ([]*models.MCPProxy, error)
    Count(ctx context.Context, orgUUID string) (int, error)
    // ...
}
```

- Returns `[]*models.MCPProxy` (pointers), takes `limit, offset` pagination.
- The `orgUUID` parameter is **misnamed** — the query filters by
  `a.organization_name = ?` (JOIN artifacts). The controller passes
  `r.PathValue("orgName")` straight through as this arg. So **`orgName` is the
  correct value to pass** — the scope service should pass its path `orgName`
  directly to `proxyRepo.List(ctx, orgName, limit, offset)`.

**Adaptations required in Task 4 (vs. the plan's snippets):**
1. `BindingCounts` must page through all proxies: call `Count(ctx, orgName)` then
   `List(ctx, orgName, count, 0)` (or loop). Do **not** assume a single unpaginated
   list. Iterate `[]*models.MCPProxy` (pointers), reading
   `proxy.Configuration.Environments[envID].ToolScopeBindings`.
2. The plan's test mock snippets use
   `ListFunc: func(...) ([]models.MCPProxy, error)` — update to the real signature
   `ListFunc: func(ctx, orgUUID string, limit, offset int) ([]*models.MCPProxy, error)`
   and add `CountFunc`.
3. `PathParamOrgName = "orgName"` (`utils/constants.go:55`).

## Env-Thunder resolver / identity client (Task 7 anchors)

- `EnvThunderResolver.Resolve(ctx, orgName, envName) (ThunderClient, error)` exists.
- `IdentityClient.AddRolePermissions(ctx, roleID, RolePermissionRequest)` exists.
- Task 7 adds `ResolveIdentity` + `EnvIdentityClient` + group-member-entry methods +
  `EnsureScopeResourceServer`; exact Thunder resource-server endpoint shapes to be
  pinned by the **Task 2 spike** against the live env before writing that code.

## Baseline build

- `go build -tags=integration ./...` → **PASS** (exit 0, clean compile) on the
  merged base. No pre-existing build failures to carry forward.
