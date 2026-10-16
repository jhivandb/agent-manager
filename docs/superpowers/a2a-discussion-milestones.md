## Milestones

### Milestone 1: A2A agents through the gateway (shipped in #1845)

Add A2A agents to Agent Manager and expose them through the gateway, with support from the console, MCP, and CLI. The gateway serves the agent card, so the platform doesn't parse it yet. Known limitation: the platform has no visibility into whether a card is valid.

1. **Agent subtype.** Add `a2a-agent` as an internal agent subtype. Require a valid port on create and on build-parameter update.
2. **Gateway-only routing.** Route A2A agents only through the API gateway; no direct OpenChoreo endpoint.
3. **Gateway publication.** Publish each deployment to the environment's gateway as a `kind: Agent` resource (JSON-RPC and HTTP+JSON transports, inbound auth, rate limiting from the agent config). Drive it from an `a2a_publications` table used as a work queue (`FOR UPDATE SKIP LOCKED`, leased claims, retry with backoff) so publishing survives restarts and is safe across replicas.
4. **Republish triggers.** Republish on deploy, promotion, deploy-settings save, and when the reconciler detects upstream drift. Delete the agent's API artifacts in every pipeline environment on agent delete.
5. **Card serving.** The gateway serves `/.well-known/agent-card.json` with interface URLs rewritten to the gateway. Publish public-card CORS and always allow the `A2A-Version` header.
6. **Console.** A2A option in agent creation; deploy card with card CORS settings; warning when the endpoint has no inbound authentication.
7. **MCP and CLI.** Agent creation accepts the `a2a-agent` subtype.
8. **Sample.** `samples/a2a-agent` (a2a-sdk 1.1.5, auto-instrumented).

### Milestone 2: Agent cards, external agents, and catalog

#### 2a. Agent card ingestion (in progress)

1. **Card store.** New `a2a_agent_cards` table, one row per agent and environment: `source` (`platform` | `external`), `source_url`, last good `card` (JSONB), `card_hash`, `fetched_at`, `status` (`pending` | `fetched` | `failed`), `last_error`. It doubles as a work queue, with the same lease and optimistic-concurrency shape as `a2a_publications`. A re-fetch never clears the last good card, so readers keep seeing it while a redeploy settles.
2. **Fetcher.** `GET /.well-known/agent-card.json` (A2A 1.0 only, no `agent.json` fallback), 5s timeout, 1 MiB cap. Minimal validation: JSON object, non-empty `name`, non-empty `supportedInterfaces` each with a `url`, `skills` array. Unknown fields pass through. External URLs go through the SSRF-guarded client.
3. **Card reconciler.** Ticker worker that claims due rows and fetches cards with backoff (`min(5s·2^n, 60s)`, 14 attempts, then `failed` with the error stored).
4. **Triggers.** The publication reconciler queues a platform card fetch when a deployment reaches `published`, so platform cards are always fetched through the gateway and carry gateway URLs. Also manual refresh. Rows are dropped on agent delete, environment delete, and undeploy.
5. **API.** `GET …/agents/{agent}/environments/{env}/agent-card`, `POST …/agent-card/refresh`, `PUT`/`DELETE …/agent-card/source`.
6. **Console.** Agent card panel per environment on the agent overview: card contents, status, and fetch error. An agent whose card failed to fetch or validate is still listed, with the error, but can't be mapped and isn't discoverable.

#### 2b. External A2A agents

1. **Direct mode.** An external agent with subtype `a2a-agent` registers a card URL per environment. The platform stores its card; callers reach the agent at its own address.
2. **Gateway-proxied mode (per environment).** Publish the external agent through the environment's ingress gateway using the M1 publication pipeline, with the upstream and routes derived from the registered card and an optional static upstream auth header (encrypted, like LLM/MCP upstream secrets). Callers get the same gateway, inbound auth, API keys, CORS and observability as platform A2A agents, and the stored card advertises gateway URLs.

#### 2c. Agent catalog

1. List A2A agents in the catalog with their stored card (name, description, skills, interfaces) and card status.
2. Catalog search over card fields, exposed through the console, the management MCP server, and the CLI.

### Milestone 3: Mappings and runtime discovery (TBD)

#### 3a. Mappings and enforcement

Discovery and authorization are separate layers (see the discussion below). Card retrieval is a plain GET, and A2A doesn't mandate a registry, so discovery can stay dynamic. Authorization is enforced where the call is made: at the gateway.

1. **Mapping store.** `a2a_agent_mappings` table: caller agent → callee agent, per environment. One-way. Only callees with a valid stored card can be mapped.
2. **API, console, MCP and CLI** to add, remove and list a caller's mappings.
3. **Gateway enforcement.** Add a caller-allowlist policy to the callee's `kind: Agent` resource. The gateway validates the caller's AgentID token and rejects the request unless the caller is in the callee's allowlist. The token is not forwarded to the callee. Changing a mapping queues a republish of the callee through the existing publication pipeline.
4. **Network isolation.** Network policy denies all agent-to-agent traffic in the namespace except ingress from and egress to the gateway, so the gateway can't be bypassed.

#### 3b. Runtime discovery

1. **Discovery tools.** An agent-facing MCP endpoint, separate from the management MCP server, authenticated with the calling agent's AgentID. Tools: `SearchAgents` (search stored cards) and `GetAgentCard` (return one stored card). Both read from the card store, never from agents live.
2. **Scope.** Results cover A2A agents in the caller's deployment scope (project and environment), not only mapped ones. Each result says whether the caller is mapped to it. A discovered but unmapped agent can't be invoked, because the gateway rejects the call.
