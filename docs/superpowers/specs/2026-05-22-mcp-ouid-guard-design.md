# MCP OuId Guard — Design Spec

**Date:** 2026-05-22
**Branch:** `mcp-ouid-guard`
**Author:** Jhivan (with Claude)
**Status:** Draft — awaiting user review

## Problem

The `/mcp` endpoint (`agent-manager-service/api/app.go:49`) is wrapped only by `JWTAuthMiddleware`, which validates signature, issuer, and audience but does not enforce that the organization a caller acts on matches the organization their token was issued for. Every MCP tool takes an `org_name` JSON argument and resolves it via `tools/helpers.go:37`, where an empty value falls back to the literal string `"default"`.

The result: any holder of a valid token in the MCP audience can call any tool against any `org_name` they choose, and an omitted argument silently drops them into the `default` OU regardless of what their token actually represents. Two handlers (`mcp/handlers/agent_handler.go:55`, `services/agent_manager.go:528`) already require `OuId` on the claims for unrelated downstream reasons, but that is an incidental coupling rather than a security boundary.

This spec defines the minimum guard needed to close that gap for the **interactive (authorization-code) flow**. The `client_credentials` flow that PR #892 introduces is out of scope here and will be addressed separately.

## Decisions

Three decisions were locked during brainstorming:

1. **Compare on `ouHandle`, not `ouId`.** `TokenClaims` already has an `OuHandle` field (`middleware/jwtassertion/auth.go:44`) but Thunder's `userAttributes` list in `deployments/helm-charts/wso2-amp-thunder-extension/values.yaml` does not emit it. Adding it makes the claim populated and lets the guard do a direct string compare against `input.OrgName` with no UUID-to-handle translation.
2. **Auto-fill on empty input; reject on mismatch.** When `input.OrgName` is empty, the guard fills it from `claims.OuHandle`. When it is non-empty and matches, the call proceeds. When it is non-empty and does not match, the call is rejected. This preserves ergonomics (AI assistants do not need to know the user's org) while making cross-org calls structurally impossible.
3. **Enforce via a generic handler wrapper at registration time.** A `withOrgGuard[I orgScoped, O any]` wrapper sits next to the existing `withToolLogging` wrapper. Each tool input that has an `OrgName` field implements a two-method `orgScoped` interface. This gives compile-time enforcement (new tools cannot accidentally bypass the guard) and zero changes inside tool handler bodies.

## Architecture

Three concrete changes:

1. **Helm config.** Add `"ouHandle"` to the `userAttributes` list under `amctlClient`, `amMcpClient`, and `ampConsoleClient` in `deployments/helm-charts/wso2-amp-thunder-extension/values.yaml` (currently lines 257, 283, 309 on `upstream/main`). Once rolled out, Thunder mints tokens carrying both `ouId` and `ouHandle`.
2. **New guard module.** A new file `agent-manager-service/mcp/tools/org_guard.go` containing the `orgScoped` interface, the `withOrgGuard` wrapper, and a `requireOuHandle(ctx) (string, error)` helper. Companion test file `org_guard_test.go`.
3. **Per-input-struct methods.** Each tool input struct in `agent-manager-service/mcp/tools/` that has an `OrgName` field gets a pointer-receiver `GetOrgName() string` and `SetOrgName(string)` pair. Estimated ~25 receivers across `agents.go`, `builds.go`, `deployments.go`, `observability.go`, and `projects.go`.

Tool registration call sites change from:

```go
withToolLogging("list_builds", listBuilds(t.BuildToolset))
```

to:

```go
withToolLogging("list_builds", withOrgGuard("list_builds", listBuilds(t.BuildToolset)))
```

Nothing inside any tool handler body changes. The existing `resolveOrgName(input.OrgName)` calls become redundant (the guard has already filled or validated the field by then) and are deleted in the same PR, along with the `resolveOrgName` helper itself.

## Components

### `orgScoped` interface

```go
type orgScoped interface {
    GetOrgName() string
    SetOrgName(string)
}
```

### `requireOuHandle`

```go
func requireOuHandle(ctx context.Context) (string, error) {
    claims := jwtassertion.GetTokenClaims(ctx)
    if claims == nil || strings.TrimSpace(claims.OuHandle) == "" {
        return "", fmt.Errorf(
            "organization identity missing from caller token; re-authenticate to obtain a token with the ouHandle claim",
        )
    }
    return claims.OuHandle, nil
}
```

### `withOrgGuard`

```go
func withOrgGuard[I orgScoped, O any](
    toolName string,
    next func(context.Context, *gomcp.CallToolRequest, I) (*gomcp.CallToolResult, O, error),
) func(context.Context, *gomcp.CallToolRequest, I) (*gomcp.CallToolResult, O, error) {
    return func(ctx context.Context, req *gomcp.CallToolRequest, input I) (*gomcp.CallToolResult, O, error) {
        var zero O
        ouHandle, err := requireOuHandle(ctx)
        if err != nil {
            return nil, zero, wrapToolError(toolName, err)
        }
        argOrg := strings.TrimSpace(input.GetOrgName())
        switch {
        case argOrg == "":
            input.SetOrgName(ouHandle)
        case argOrg != ouHandle:
            return nil, zero, wrapToolError(toolName, fmt.Errorf(
                "org_name %q does not match caller's organization %q",
                argOrg, ouHandle,
            ))
        }
        return next(ctx, req, input)
    }
}
```

### Per-input methods

Mechanical, one pair per struct that has `OrgName`:

```go
func (i *listBuildsInput) GetOrgName() string  { return i.OrgName }
func (i *listBuildsInput) SetOrgName(v string) { i.OrgName = v }
```

### Pointer-receiver note

`SetOrgName` mutates the original input, so the interface must be satisfied by the pointer type. Concretely: methods are defined on `*listBuildsInput`, and the registered handler signature becomes `func(ctx, req, input *listBuildsInput) (...)` instead of taking the value. The `gomcp.AddTool` generic accepts pointer types as the input parameter — to be confirmed by a small spike in the implementation plan, since it determines whether each handler needs a one-line signature change or whether the SDK requires a different pattern (e.g., method on value receiver that returns a modified copy).

## Data flow

```
HTTP request → JWTAuthMiddleware (parses claims, stores in ctx)
              ↓
            /mcp handler (gomcp.NewStreamableHTTPHandler)
              ↓
            registered tool closure:
              withToolLogging("list_builds",
                withOrgGuard("list_builds",
                  listBuilds(handler)))
              ↓
            withToolLogging        : log entry/exit
              ↓
            withOrgGuard           :
              1. requireOuHandle(ctx)              → ouHandle or reject
              2. argOrg := input.GetOrgName()
              3. branch:
                 - argOrg == ""        → input.SetOrgName(ouHandle)
                 - argOrg == ouHandle  → pass
                 - argOrg != ouHandle  → reject (no downstream call)
              4. next(ctx, req, input)
              ↓
            listBuilds handler      : runs unchanged, input.OrgName is now valid and non-empty
```

## Error handling

Two failure cases, both returned as MCP tool errors via the existing `wrapToolError(toolName, err)` envelope. The HTTP layer stays 200 OK; the JSON-RPC response carries the error — same shape as today's `"organization identity missing"` rejection in `agent_handler.go:55`.

| Trigger | Returned error |
|---|---|
| `claims == nil` or `claims.OuHandle == ""` (or whitespace-only) | `organization identity missing from caller token; re-authenticate to obtain a token with the ouHandle claim` |
| `input.OrgName` non-empty and `!= claims.OuHandle` | `org_name %q does not match caller's organization %q` (caller's own OuHandle interpolated) |

One INFO log line per rejection with `toolName`, `caller_sub`, and the rejection reason. No success-path log line (already covered by `withToolLogging`).

The mismatch message exposes the caller's own `OuHandle`, which is their own claim and not a secret, so no information leakage.

## Testing

### Unit tests for the wrapper

New file `agent-manager-service/mcp/tools/org_guard_test.go`. Table-driven, no MCP server dependency:

- nil claims → reject
- claims with empty `OuHandle` → reject
- claims with whitespace-only `OuHandle` → reject
- empty `input.OrgName` + valid claims → auto-fill, `next` receives filled input
- whitespace-only `input.OrgName` → treated as empty (auto-fill)
- matching `input.OrgName == claims.OuHandle` → pass through
- mismatched `input.OrgName != claims.OuHandle` → reject
- `next` returns an error → wrapper propagates unchanged
- `next` returns a result → wrapper passes it through

### Existing tool tests

`builds_specs_test.go`, `deployments_specs_test.go`, `observability_specs_test.go`, etc. need a small change: each test's context must carry mock claims with the expected `OuHandle`. A new helper `withMockClaims(ctx, ouHandle)` in `tools/test_helpers.go` keeps that a one-liner. Existing tests that already pass `testOrgName` into the input keep doing so and set `ouHandle = testOrgName`.

### Integration tests

`mcp/integration_test.go` already spins up an MCP client/server with `NewMockMiddleware`. Add three cases:

1. Happy path: matching org → tool executes.
2. Mismatched org → JSON-RPC error.
3. Token missing `OuHandle` → JSON-RPC error.

### No test for the Helm change

Config-only. Verified post-deploy by decoding a token and checking the `ouHandle` claim is present.

## Rollout

Two phases. Each phase is independently safe to ship.

### Phase 1 — Thunder emits `ouHandle`

Ship only the `values.yaml` change. Existing tokens still lack the claim until refresh. MCP behavior is unchanged. This phase is risk-free and can land on its own.

### Phase 2 — Guard enforces

Ship the wrapper, the per-struct methods, the registration-site changes, and the removal of `resolveOrgName`. Any token issued before Phase 1's rollout completes gets rejected at the guard with the clear "re-authenticate" message. Tokens issued after Phase 1 keep working.

### Why no audit-only window

The brainstorm scope is the bare-minimum guard. An audit-only window means a feature flag, a deprecation metric, and code to remove later — three things to maintain. The error message is actionable (re-auth), the `am-mcp` token TTL is short (1h on `upstream/main`), and the worst-case burden is one re-auth prompt per session.

### Operational

The Phase 1 → Phase 2 gap can be hours, not days. The two phases can ship in separate PRs or a single PR — they do not need to be atomic.

## Out of scope

- **`client_credentials` flow (PR #892).** The guard as designed rejects any token without `OuHandle`. Client-credentials tokens will fail at `/mcp` until that flow is properly designed — that is the intended behavior here.
- **`am-mcp` → `am-mcp-user` rename and the new audience list** (also PR #892). Independent.
- **Project- or agent-level authorization.** Only org binding is enforced. Downstream tools still trust the `project_name` / `agent_name` they are given.
- **Non-MCP API routes.** `/api/v1/...` routes have the same gap but live behind different controllers. Separate PR.
- **`OuId`-based comparison or lookups.** The guard uses `OuHandle` because it matches the tool args directly. `OuId` remains available in claims for code that needs the UUID downstream (e.g., `agent_handler.GenerateToken`).

## Files touched (estimated)

| File | Change |
|---|---|
| `deployments/helm-charts/wso2-amp-thunder-extension/values.yaml` | Add `"ouHandle"` to three `userAttributes` lists |
| `agent-manager-service/mcp/tools/org_guard.go` | New file: interface, wrapper, helper |
| `agent-manager-service/mcp/tools/org_guard_test.go` | New file: table-driven wrapper tests |
| `agent-manager-service/mcp/tools/test_helpers.go` | Add `withMockClaims` |
| `agent-manager-service/mcp/tools/agents.go` | Add per-struct methods; wrap registrations |
| `agent-manager-service/mcp/tools/builds.go` | Add per-struct methods; wrap registrations |
| `agent-manager-service/mcp/tools/deployments.go` | Add per-struct methods; wrap registrations |
| `agent-manager-service/mcp/tools/projects.go` | Add per-struct methods; wrap registrations |
| `agent-manager-service/mcp/tools/observability.go` | Add per-struct methods; wrap registrations |
| `agent-manager-service/mcp/tools/helpers.go` | Delete `resolveOrgName` |
| `agent-manager-service/mcp/tools/*_specs_test.go` | Inject mock claims via helper |
| `agent-manager-service/mcp/integration_test.go` | Add three new cases |
