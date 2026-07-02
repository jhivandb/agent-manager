# Why we should ship the gateway-artifact design, not per-env Thunder

**Date:** 2026-07-02
**Context:** Two designs exist for agent identity. Design A is the approved [agent identity & MCP tool authz design](2026-07-02-agent-identity-mcp-tool-authz-design.md) (single Thunder, permissions rendered into the agent's gateway artifact). Design B is the draft "AgentID Support via Thunder ID" (one Thunder instance per data-plane × environment, scope/role/resource-server authorization). We have 10 days to implement plus 1–2 weeks of testing. This doc argues we ship A.

All ThunderID claims below are verified against `thunder-id/thunderid` main at commit `f8dd08c` — file:line references point there.

## The argument is not "Thunder can't do it"

Let's get this out of the way, because B's own risk register understates Thunder's current state. B flags R6 ("role→token-scope path unconfirmed — roles may be inert at runtime") and R4 ("OBO semantics to verify"). Both are resolved in current main:

- The client_credentials handler filters requested scopes through role-based authorization: it resolves the agent's group memberships and runs every requested scope through `authzService.EvaluateAccessBatch`, dropping unauthorized ones (`granthandlers/client_credentials.go:104–136`). Role assignments do flow into issued tokens.
- Token exchange enforces requested scopes ⊆ subject-token scopes (`granthandlers/token_exchange.go:222–243`), which is the delegation-narrowing behavior R4 wanted confirmed.

So B is buildable. The case against it is that it's the wrong shape for this problem, on this timeline, at this operational budget — and that its central authorization mechanism reintroduces the exact problem A was designed to eliminate.

## 1. The timeline argument

Ten days of implementation buys A in full: one migration, one provisioner modeled on `publisher_credential_provisioner.go`, a Thunder `/agents` client extension, policy rendering in `mcp_proxy_deployment.go`, three API endpoints, and a console tool picker. Every mechanism it touches already exists — the gateway's `mcp-auth`/`mcp-authz` policies, the artifact push path, `gateway_identity_providers`, OpenBao secret refs.

The same ten days buys B a fraction of its critical path. Before a single agent authenticates, B needs: parameterized helm deployment of the `wso2-amp-thunder-extension` chart per environment (release, namespace, PVC, issuer, HTTPRoute, ReferenceGrant), a bootstrap job seeding a per-env admin client, an `(org, env) → Thunder` registry with OpenBao-backed admin credentials, gateway key-manager rewiring per environment with readiness ordering, environment-deletion teardown, and hooks into `CreateEnvironment`. That's the *infrastructure* workstream — the resource-server/role/group provisioning, promotion flow, and console surfaces sit on top. B's own doc structures this as two parallel workstreams; we don't have parallel weeks, we have ten days.

And the testing math is worse than the implementation math. A's test surface is golden tests for YAML rendering plus one k3d integration flow (allowed tool passes, restricted tool 403s, foreign token 403s). B's test surface includes environment lifecycle (create/delete/re-create with PVC state), registry correctness (B's R5: a wrong mapping mints prod identities from the dev Thunder), partial-failure recovery across a five-step imperative provisioning sequence (B's R2), cross-plane reachability failure modes (B's R1), and per-instance backup/restore of SQLite identity state. One to two weeks of testing does not cover a new identity-server fleet.

## 2. The scope-problem argument

A's core principle is that the token says who you are and the gateway artifact says what you may do. That wasn't aesthetic — it's what makes permission changes cheap: edit the allowlist, re-render one `McpProxyMapping` YAML, push it through the existing artifact mechanism. No token reissue, no agent restart, no runtime dependency on the control plane beyond token issuance itself.

B puts permissions in the token as scopes. Even with the role→scope bridge working (verified above), that model has a structural consequence: **a permission change does nothing until the agent acquires a fresh token.** Revoking a tool from an agent with a valid one-hour token leaves that agent authorized for up to an hour — and B's R1 mitigations explicitly push the *other* way, toward "generous token TTLs + cached/refreshable tokens" to survive control-plane blips. B needs long-lived tokens for availability and short-lived tokens for revocation, simultaneously. A doesn't have this tension: its tokens carry zero scopes, so TTL is purely an availability knob, and revocation latency is one artifact push regardless.

Token size follows the same logic. Per-tool granularity in B means one scope per tool per MCP server; an agent bound to a handful of servers carries dozens of scopes, growing with every binding. A's token is constant-size forever.

## 3. The enforcement-point argument

B's outbound direction says the MCP/tool resource server "validates each call." For third-party MCP servers — the common case — that's not a thing we control. They don't know our scope grammar and won't 403 based on it. The only enforcement point we actually own is the API Platform Gateway the MCP traffic already flows through, and its `mcp-authz` policy is exactly what A programs. B, followed to its conclusion, either (a) also lands at the gateway rendering scope checks into `mcp-authz` rules — at which point it's A with extra steps and the token-staleness problem from §2 — or (b) leaves outbound tool restriction unenforced for any server that doesn't cooperate. Neither is an argument for building the fleet.

## 4. The topology argument

B rejects Thunder's OU layer and buys environment isolation with separate processes: one Thunder per (data plane × environment), each in its own namespace with its own SQLite database on its own PVC, single replica, plus an admin secret set per instance (B's own R3). But Thunder is OU-native — agents, roles, and groups are OU-scoped, and tokens carry `ouId`/`ouName`/`ouHandle` (`tokenservice/utils.go:445–465`). B pays a fleet of identity servers for tenancy the shared instance expresses in data, then inherits per-instance backup, per-instance HA ("revisit later", says the doc — for the component every token depends on), and a helm install on the environment-creation path.

Cross-environment isolation, the stated payoff, is something A already achieves: each (agent × environment) is a distinct Thunder agent, so a dev token carries a dev `sub` and fails the prod mapping's pin. Per-issuer isolation is stronger in one respect — the gateway rejects a foreign token at signature validation rather than at claim check — but both end in the same 403, and that marginal hardening is the *only* security property the entire fleet buys over A.

B's plane split makes it worse: gateways and workloads live in the data plane, the env-Thunders stay in the control plane, so *every runtime token acquisition crosses the plane boundary to a single-replica SQLite pod*. B's own doc concedes this makes the R1 mitigations "required, not optional." A has the same control-plane token issuance but against the one Thunder instance we already run, harden, and back up.

## 5. The scope-of-problem argument

The problem in front of us is: verifiable agent identity at the gateway, per-tool MCP restrictions, permission changes without redeploys. That's A's scope, end to end.

B answers a bigger question — end users, roles UX, agent-as-resource-server invoke gating, OBO — that nobody has committed to for this deadline. Those capabilities are real and some are worth building; but they're additive, and nothing in A forecloses them. A provisions the same Thunder first-class agents with the same client_credentials profile B needs; Thunder's role/scope machinery (verified working) and token exchange remain available on top of A's identities whenever inbound gating or OBO gets prioritized. If per-env identity-store isolation ever becomes a hard requirement, A's provisioner re-points at a Thunder registry — the `agent_identities` row grows an issuer column. Shipping A doesn't spend B's options; shipping B spends our deadline.

## Recommendation

Ship A. Track three follow-ups explicitly rather than letting them justify B by ambient anxiety: (1) gateway feature requests — `defaultAction: deny` and `tools/list` filtering; (2) inbound agent-invocation gating via Thunder resource servers + roles, now de-risked since the role→scope bridge is confirmed in main; (3) a periodic review of whether any compliance requirement actually demands physically separate identity stores per environment — until one does, the fleet is cost without a customer.
