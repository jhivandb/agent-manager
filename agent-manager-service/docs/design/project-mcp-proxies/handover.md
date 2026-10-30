# Handover: project-scoped MCP proxies for Integration Platform (2026-10-30)

**Goal.** Integration Platform (IP) publishes its MCP server components through agent-manager-service (AMS), so AM agents
can pick them in **Configure → Tools**. Tracking issue: #2196. Due ~2026-11-05.

**Read, in order:**
1. [`spec.md`](spec.md): requirements (§1), API summary (§3), AMS work list (§4), work outside AMS (§5), day-1 checks
   (§6), estimate and split (§7), verified facts (§9).
2. [`openapi.yaml`](openapi.yaml): the contract IP builds against. Shapes come from the real handlers, not
   `docs/api_v1_openapi.yaml`, which has drifted.
3. The gateway reconciliation design (#2183, branch `gateway-reconciliation-v2`): the deploy and status model this sits on.

## Where things stand

- The spec and contract are agreed. The decisions are in spec §1.
- IP has been told, and agreed to handle `409 MCP_PROXY_IN_USE` before deleting an MCP component.
- No code yet. The next step is the task-by-task implementation plan from spec §4 and §7.

## Decisions you inherit (don't reopen without the owner)

- Project-scoped proxies; per-environment bindings and keys; `apiKey` and `jwt` (`ThunderKeyManager`) only.
- IP calls with an M2M token plus `X-Impersonate-Org`.
- No reconciler: IP binds an environment only after its component is deployed; otherwise `409 UPSTREAM_NOT_READY`.
- A binding `PUT` re-queues a `Failed` binding with no input change.
- AMS owns test keys: 1 h, 3 per user, shared with agent test keys.
- Not on AM's MCP proxy page; only in the same-project Tools picker.
- One `BOTH` gateway per environment, in the same OpenChoreo project as the agents and MCP components.
- The gateway context is `/<project><context>`; `409 CONTEXT_IN_USE` on a clash.
- Delete while agents use the proxy → `409 MCP_PROXY_IN_USE`.

## Open: IP's own draft differs (2026-10-30)

IP's draft of its console-facing API publishes an endpoint to AMS as an opt-in, for agents only, and keeps API Platform as
the gateway for its other MCP clients. Raise these with the owner before planning:

1. **Keys, test keys, jwt.** IP's draft doesn't use them on the AMS proxy (agents bind with their own keys). Proposed: cut
   them from the first delivery (about 3.5 days), keep them in the spec as phase 2.
2. **IP's phase 1 upstream** is the endpoint's external route plus `/mcp`, against today's org routes. That route has no
   gateway auth. Proposed: ask IP to wait for `source`.
3. **MCP path gap.** `source` has no path, so the resolved in-cluster URL would hit the root. Add `source.path`, default
   `/mcp`.
4. **Transport.** IP refuses MI MCP servers (HTTP+SSE) because the AI gateway doesn't route SSE. Mirror that or confirm.
5. **`consoleUrl`.** IP's draft links to the proxy's page in AM's console, but project proxies are hidden there.
6. **Auth.** IP's draft forwards the user's token; the agreed model is M2M.

## Dependencies and risks

| Item | Why it matters | Owner |
|---|---|---|
| `gateway-reconciliation-v2` lands, or the work is based on it | Same MCP files and status words; owns migration 051. | AMS |
| IP's M2M client gets org claims and an `amp:mcp-server:*` role | Without it every IP call is 403. | Cloud |
| The cloud distribution picks up `OrgOverrideResolver` and installs an allow-list resolver | Without it nothing works in the cloud. | AM |
| The per-environment `BOTH` gateway in the cloud | Not built. Until then bindings stay `NO_GATEWAY`; no real traffic. | Cloud |
| `ThunderKeyManager` configured on that gateway | Without it jwt mode fails ("no matching issuers"). | Cloud |
| `audiences` isn't declared in upstream `mcp-auth` | Works only because the schema allows extra fields. Ask `wso2/gateway-controllers` to declare it. | AMS |

Day-1 checks are in spec §6.
