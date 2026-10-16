# Policy Engine: Onboarding Guide

For engineers on the agent-manager team whose agents sit behind the WSO2 API Platform gateway. This guide stays at the level of services, interfaces and data, with no code. I checked every claim against the code on `main` (Oct 2026). Anything I could not confirm is marked **(unverified)**. The design specs under `gateway/gateway-runtime/policy-engine/specs/` are older than the code, so use them only as background.

---

## 1. Purpose and position in the request path

The **policy engine** is a Go service that decides what happens to every API request and response passing through the gateway. Envoy (the proxy, called the **Router** in this repo) does not make those decisions itself. For each request it calls out to the policy engine over Envoy's **ext_proc** protocol (External Processing: a bidirectional gRPC stream on which Envoy sends request and response headers and bodies, and the processor answers "continue", "continue with these changes", or "reply now with this response").

The gateway does not use ext_authz. ext_proc is the only external filter. The HTTP filter chain that the controller generates is fixed: ext_proc, then Lua, then the router.

```
 Client (e.g. an A2A client calling your agent)
   │ HTTP/HTTPS :8080 / :8443
   ▼
 ┌─────────────── gateway-runtime container ───────────────┐
 │  Envoy (Router)                                          │
 │   filters: ext_proc ──► lua ──► router ──────────────────┼──► Upstream (your agent / LLM / MCP server)
 │              │ gRPC over UDS                             │
 │              ▼                                           │
 │  Policy Engine (Go) ──gRPC/UDS──► Python Executor (opt.) │
 └──────────────┬───────────────────────────────────────────┘
                │ xDS (ADS gRPC)          ▲ Envoy also gets xDS
                ▼                         │ (listeners/routes/clusters)
        gateway-controller  :18001 (policy engine)   :18000 (Envoy)
```

A **policy** is a unit of request/response logic. Each policy has a name and a major version, and it is configured per API or per operation with parameters. It can inspect or change headers, bodies, path, method, query or upstream, or it can short-circuit the request with an immediate response. A **policy chain** is the ordered list of policies that applies to one route or operation.

### Policies that exist

The engine itself ships with **no** business policies. Every policy is compiled in at image build time (see §8). The default build (`gateway/build.yaml`) includes the policies below. Their source code lives in an external repo, `github.com/wso2/gateway-controllers/policies/*`, which I did not inspect. The behaviours described below come from policy names only and are **(unverified)**.

| Concern | Policies in default build |
|---|---|
| Authentication | `api-key-auth`, `jwt-auth`, `basic-auth`, `mtls-auth`, `opaque-token-auth`, `mcp-auth`, `aws-authentication` (to upstream), `backend-jwt`, `oauth2-generator` |
| Authorization / subscriptions | `mcp-authz`, `mcp-acl-list`, `subscription-validation` |
| Rate limiting / cost | `basic-ratelimit`, `advanced-ratelimit`, `token-based-ratelimit`, `llm-cost-based-ratelimit`, `mcp-ratelimit`, `llm-cost`, `azure-llm-cost` |
| CORS | `cors` |
| Guardrails | `content-length-guardrail`, `json-schema-guardrail`, `regex-guardrail`, `url-guardrail`, `word-count-guardrail`, `sentence-count-guardrail`, `pii-masking-regex`, `semantic-prompt-guard`, `aws-bedrock-guardrail`, `azure-content-safety-content-moderation`, `typesafe-jev-*` (content safety, MCP tool intent/result screening, tool filtering, model routing); Python-based: `granite-guardian-prompt-injection`, `nvidia-nemoguard-content-safety` |
| LLM / AI | `openai-to-{anthropic,azure-openai,bedrock,gemini,mistral}-transformer`, `model-round-robin`, `model-weighted-round-robin`, `llm-header-router`, `prompt-decorator`, `prompt-template`, `semantic-cache`, `semantic-tool-filtering`, `prompt-compressor` (Python) |
| MCP | `mcp-rewrite`, `mcp-spec-validation` (plus the MCP auth, authz, ACL and rate-limit policies above) |
| Transformation / routing | `set-headers`, `remove-headers`, `request-rewrite`, `host-rewrite`, `redirect`, `respond`, `dynamic-endpoint`, `json-xml-mediator`, `interceptor-service` |
| Observability | `log-message`, `analytics-header-filter` |

The repo also contains two **system policies** under `gateway/system-policies/`. The controller attaches these itself, never an API author:
- `wso2_apip_sys_analytics` is added to every chain whenever the analytics or traffic-logging "collector" is enabled.
- `wso2_apip_sys_a2a` serves or rewrites A2A Agent Cards. This one matters directly to agent-manager.

---

## 2. How it runs

### Processes and packaging

One container image, `gateway-runtime`, runs up to three processes under `tini` and a bash entrypoint (`gateway/gateway-runtime/docker-entrypoint.sh`). They are not sidecars. All three share one container and talk over Unix domain sockets in `/var/run/api-platform/`.

| Process | Language | Started when | Notes |
|---|---|---|---|
| Python Executor | Python (gRPC server) | Only if the build produced Python policies (a generated `python_policy_registry.py` exists). The default build has three Python policies, so it does start. | Started first. The entrypoint waits up to 15 s for its socket. |
| Policy Engine | Go | Always | Started with `-xds-server <controller>:18001` plus `--pol.*` args. The entrypoint waits up to 10 s for its socket. |
| Envoy | C++ | Always | Bootstraps only an ADS connection to the controller on :18000. Every listener, route, cluster and the ext_proc filter itself come from the controller over xDS. |

If any of the three processes exits, the entrypoint stops the other two and the container exits. Restarting it is the orchestrator's job. On SIGTERM the entrypoint drains Envoy, but only if the Envoy admin interface is enabled. It then stops Envoy, the policy engine and the Python executor, in that order.

### Ports and sockets

| Endpoint | Default | Purpose |
|---|---|---|
| ext_proc gRPC | UDS `/var/run/api-platform/policy-engine.sock` (TCP :9001 optional) | Envoy → engine |
| Python executor gRPC | UDS `/var/run/api-platform/python-executor.sock` (TCP :9010 optional) | engine → Python |
| ALS gRPC | UDS `/var/run/api-platform/gateway-analytics.sock` | Envoy access logs → engine. Only when the collector (analytics/traffic logging) is on. |
| Admin HTTP | :9002 | `/health`, `/xds_sync_status`, `/config_dump` (off by default), pprof (off by default) |
| Admin HTTPS | :9004 | Same routes over TLS. Off by default. |
| Metrics HTTP | :9003 | Prometheus `/metrics` when `policy_engine.metrics.enabled`. The code default is off and the config template sets it on, so whether it is on in your deployment is **(unverified)**. |
| xDS client | → controller :18001 | Policy-engine config feed. Plaintext by default; mTLS is optional. |
| Envoy | :8080 HTTP, :8443 HTTPS; admin :9901 | Admin is off by default and binds to loopback. |

Two docs disagree with the code: the runtime README calls :9002 "metrics" (it is admin), and `docker-compose.yaml` calls :9004 "Health" (it is admin TLS).

### Configuration sources

1. **A TOML file** (`--pol.config`, required, repeatable, last wins). docker-compose mounts the same `config.toml` into the controller and the runtime. Values can read environment variables through `{{ env "NAME" "default" }}` tokens.
2. **Entrypoint environment variables**: controller host, xDS ports, `GOMAXPROCS` (default 2), Python worker settings, Envoy admin and xDS TLS.
3. **xDS from the controller**: all per-API runtime state (§4).
4. **File mode** (`-policy-chains-file`) for local runs without a controller.

### Resource limits and timeouts (defaults)

| Item | Default | Where set |
|---|---|---|
| ext_proc gRPC timeout and per-message timeout | 60 s / 60 s | controller `router.policy_engine.timeout_ms` / `message_timeout_ms` |
| Concurrent ext_proc streams | 10,000 | `policy_engine.server.max_concurrent_streams` |
| Decompressed body buffered per direction | 10 MiB | `policy_engine.request_body/response_body.max_decompressed_bytes`. Overflow returns 413 for a buffered request. |
| Body accepted for operation resolution | 64 KiB default; 1 MiB on Agent routes | per route, from the controller |
| xDS connect / request timeout; reconnect backoff | 10 s / 5 s; 1 s doubling to 60 s | `policy_engine.xds.*` |
| Outbound HTTP client used by policies | 30 s overall, 10 s dial/TLS/response-header; SSRF guard off by default | `policy_engine.http_client.*` |
| Python executor | 4 workers, 100 concurrent, 30 s timeout | `PYTHON_POLICY_*` env / `policy_engine.python_executor.timeout` |
| Container (example compose) | 180 MiB memory, 0.175 CPU for the whole runtime | `gateway/docker-compose.yaml` |

---

## 3. Internal components

```
             ┌──────────────────────── Policy Engine process ────────────────────────┐
 Envoy ─────►│ ext_proc server (kernel)                                               │
 ext_proc    │   ├─ route lookup: RouteConfigs[xds.route_name]                        │
             │   ├─ operation resolver (route-key | a2a | mcp) → chain key            │
             │   ├─ per-request execution context (phase state, body mode, faults)    │
             │   └─ chain executor ──► policy instances (Go, in-process)              │
             │                         └─► Python bridge ──UDS gRPC──► Python Executor│
             │ Kernel store: RouteConfigs, PolicyChains, sensitive values             │
             │ Policy registry: name:vMAJOR → factory (filled at build time)          │
             │ Shared stores (SDK singletons): API keys, subscriptions, lazy resources│
 controller◄─┤ xDS client (ADS) → handlers → kernel/stores                            │
 Envoy ALS ─►│ ALS server → analytics → publishers (Moesif, OTel, log/file/HTTP sinks)│
             │ Admin server (:9002), Metrics (:9003), OTel tracer                     │
             └────────────────────────────────────────────────────────────────────────┘
```

| Box | What it does |
|---|---|
| ext_proc server / kernel | Handles one gRPC stream per HTTP request. Binds the route, runs each phase, and turns policy actions into Envoy mutations, immediate responses and dynamic metadata. Also tells Envoy which later phases to send. |
| Operation resolver | Turns a route into a **chain key**. `route-key` (most API kinds) uses the identity key. `a2a` maps a request to a canonical A2A operation, reading the JSON-RPC `method` when needed. `mcp` is currently enrich-only: one chain per route. |
| Chain executor | Runs a chain's policies in order. Skips a policy that is disabled or whose CEL **execution condition** is false. Stops at the first immediate response. |
| Policy registry | Maps `name:vMAJOR` to a factory plus `systemParameters`. Builder-generated code fills it at startup. |
| xDS client | One ADS stream subscribing to five custom resource types. Each type is ACKed or NACKed separately. |
| Python bridge | A Go stand-in for each Python policy. It forwards each phase to the executor over a streaming RPC and manages instance init and destroy. |
| Analytics / ALS | Correlates Envoy access logs with what ext_proc captured. Publishes to Moesif, OpenTelemetry and traffic-log sinks. |
| Tracing / metrics / admin | OTel spans per phase and per policy, parented on the client's `traceparent`. Prometheus metrics per policy and route. `/health` is unhealthy until the first chain snapshot arrives, or while the Python executor is down. |

---

## 4. Interfaces

### From Envoy (ext_proc)

- **The static config always sends request headers.** Request attributes include `xds.route_name` and downstream TLS and mTLS facts (peer cert, SANs, TLS version, SNI). Response messages carry `response.code_details`, which lets the engine tell "backend returned 503" apart from "Envoy had no healthy upstream".
- **Later phases are chosen per request.** The engine's reply to the request-headers message carries a mode override. Request body can be none, BUFFERED or STREAMED. Response headers can be sent or skipped. Response body can be none, BUFFERED or STREAMED. The choice follows the chain's computed needs: if any policy declares a body mode, the body is requested. Streaming is used only if every body policy supports chunk-wise processing, and for responses only when the upstream is actually streaming (chunked or SSE). This is how token streaming from LLMs and agents stays streamed.
- **The router is configured fail-closed.** `failure_mode_allow = false` and mode override is always allowed.
- **Dynamic metadata** is shared in the `envoy.filters.http.ext_proc` namespace. It carries analytics data and the request's current upstream. When a policy redirects to a named upstream, the engine sets `x-target-upstream`. The Lua filter and the router's cluster-header routing then send the request to that cluster.

### From gateway-controller (xDS, port 18001)

The engine opens one ADS (Aggregated Discovery Service) stream. All resources are **state-of-the-world** (SotW): each push carries the complete set for that type, never a delta. Each resource is a `google.protobuf.Struct` holding JSON, wrapped twice in `Any`.

| Type URL (`api-platform.wso2.org/v1.*`) | Controller producer | Content |
|---|---|---|
| `PolicyChainConfig` | `pkg/policyxds` | One resource per chain key: ordered policies, optional fault policies, API name and version, plus `sensitive_values` (resolved secret plaintexts, used to redact dumps) |
| `RouteConfig` | `pkg/policyxds` | One per Envoy route: API metadata, resolver name and config, canonical chain key, upstream info, max resolution body |
| `APIKeyState` | `pkg/apikeyxds` | Every API key of every API, hashed |
| `SubscriptionState` | `pkg/subscriptionxds` | Every ACTIVE subscription with its plan's throttle settings |
| `LazyResources` | `pkg/lazyresourcexds` | Typed blobs that policies read on demand: LLM provider templates, provider→template mappings, client-certificate-authority pools |

The engine identifies itself in xDS Node metadata: its resolution protocol version and the resolvers it supports. With that, the controller can hold back routes this runtime could not resolve.

### To external services

- **Upstreams** (your agent, LLM providers, MCP servers) are called by Envoy, not the engine. Policies only rewrite or redirect the request.
- **Policies call out themselves** through a shared outbound HTTP client: guardrail and safety APIs, JWKS and token endpoints, AWS and Azure, embedding and vector stores (e.g. Redis for `semantic-cache`). Which policy calls what is **(unverified)**.
- **Rate-limit counters** may be per-process or in a shared backend, depending on the policy **(unverified)**.
- **Moesif, an OTLP collector and HTTP log sinks** receive analytics and traffic logs when those are enabled.

---

## 5. Data model

All of this state lives in memory. It is rebuilt entirely from xDS, and the engine has no database of its own.

### Route identity

The controller names every Envoy route `METHOD|<context><version><path>|<vhost>`, with an optional `|<header-match discriminator>`. For example: `POST|/my-agent/v1/message:send|*`. Envoy passes the matched route's name to the engine as `xds.route_name`. The engine then:

1. looks up `RouteConfigs[route_name]`. If there is none, it returns a 500.
2. runs that route's prepared resolver to get the **chain key**. For REST-style routes this is the canonical chain key the controller emitted, normally equal to the route name. For Agent (A2A) routes it is the key of the canonical operation, so a JSON-RPC `SendMessage` and `POST /message:send` share one chain.
3. looks up `PolicyChains[chain key]`. If there is none, it returns a 500.

The resolver is not allowed to select a chain belonging to a different API or vhost.

### Entities

**RouteConfig** (per Envoy route)

| Field | Meaning |
|---|---|
| route_key | Envoy route name (lookup key) |
| metadata: uuid, display_name, version, api_context, vhost, kind, project_id, path, provider_name, template_handle | API identity used by policies, analytics and traces. `kind` is one of RestApi, Agent, Mcp, LlmProvider, LlmProxy, GraphQLApi, … |
| resolver_name, resolver_config, canonical_chain_key | How to derive the chain key |
| max_request_body_bytes | Acceptance ceiling for body-based resolution |
| default_upstream, default_upstream_cluster, upstream_base_path, upstream_definition_paths | Upstream info for dynamic routing and rewrite policies |

**PolicyChain** (per chain key)

| Field | Meaning |
|---|---|
| route_key | Chain key |
| policies[] | Ordered **policy instances**. Order is API-level policies first, then operation-level, then any system policies the controller injects. |
| faultPolicies[] | Run only when the request is failing (§7) |
| metadata | api_name, version |
| transport_metadata.sensitive_values | Secret values the controller resolved from `{{ secret }}` templates, which arrive as plaintext in parameters. Used only for redaction. |
| *computed at ingest* | needs request/response body, streaming support, has CEL conditions |

**PolicyInstance**

| Field | Meaning |
|---|---|
| name, version | Registry key. The version is the major only, e.g. `v1`. |
| enabled | false means always skipped |
| executionCondition | Optional CEL expression over request or response context |
| parameters | Free-form map, validated by the controller against the policy's JSON Schema. Includes `attachedTo` = `api` or `route`. |

**APIKey** (store keyed by `apiId → SHA-256(key) → entry`)

| Field | Meaning |
|---|---|
| id, name | Key identity. Unique by name per API. |
| apiKey | **SHA-256 hex hash**. Plaintext never reaches the engine. |
| apiId | Owning API (keys are per API, not per operation; `operations` is always `*`) |
| applicationId, applicationName | Owning application, for subscriptions and analytics |
| status, expiresAt | Must be `active` and unexpired to pass |
| source, issuer | `local` or `external`. `issuer` lets a policy accept only keys from a given portal. |

**Subscription** (store keyed by `apiId → token hash` and `apiId → applicationId`)

| Field | Meaning |
|---|---|
| apiId, applicationId, subscriptionToken (SHA-256) | Lookup keys |
| status | Only ACTIVE subscriptions are shipped |
| planName, throttleLimitCount, throttleLimitUnit, stopOnQuotaReach | Plan quota used by `subscription-validation` / rate limiting |
| billingCustomerId, billingSubscriptionId | Pass-through billing references |

**LazyResource**: a generic `{id, resource_type, resource}` keyed by `type:id`. Current types are `LlmProviderTemplate`, `ProviderTemplateMapping` and `ClientCertificateAuthority`.

**Per-request SharedContext** is what policies share across phases: request ID, API identity, resolved operation, a free-form `Metadata` map for policy-to-policy hand-off, and **AuthContext**. AuthContext holds the authenticated and authorized flags, auth type, subject, issuer, audience, scopes, credential ID and extra claims.

---

## 6. Key flows

### 6a. A request, happy path (API key + rate limit on an Agent route)

1. The client sends `POST /my-agent/v1/message:send`. Envoy matches route `POST|/my-agent/v1/message:send|*`.
2. Envoy opens an ext_proc stream and sends request headers with `xds.route_name`.
3. The engine finds the RouteConfig and prepares the A2A resolver. For this operation, the resolver needs the body to read message IDs. The engine replies "send me the body, buffered" and runs no policy yet.
4. The body arrives. The resolver identifies `SendMessage` and binds that operation's chain.
5. The chain runs in order. `api-key-auth` hashes the presented key, finds it in the API key store, and fills AuthContext. `basic-ratelimit` checks the counter. Other policies follow, each possibly mutating headers or body.
6. The engine returns the combined mutations and the mode for the response phases. Envoy forwards the request to the agent.
7. The response headers and body flow back through any response-phase policies, streamed if every response-body policy supports it. The analytics system policy records facts. Envoy returns the response.
8. Envoy's access log goes over ALS to the engine, which publishes analytics.

### 6b. A deny

From steps 1 to 5 as above, suppose `api-key-auth` finds no key, or a hash that is not in the store, or a revoked or expired key:

1. The policy returns an **immediate response**, for example 401. The executor stops the chain, so later policies do not run.
2. The engine enters the fault flow. If the route has fault policies, they can reshape the error, for example into a JSON-RPC error for an A2A client. Then the engine sends `ImmediateResponse` to Envoy.
3. Envoy replies to the client. The upstream is never contacted.

### 6c. A config change from the controller

1. An API or Agent is deployed or updated, through the controller's REST API or a `*.deployed` WebSocket event from the control plane (platform-api). The controller stores it, publishes an event on its **EventHub** (an SQL-table event log that every controller replica polls, default every 3 s), and each replica translates the config into a RuntimeDeployConfig.
2. The controller rebuilds the full `PolicyChainConfig` and `RouteConfig` sets and pushes them on :18001. Separately, it pushes Envoy routes and clusters on :18000.
3. For each route, the engine **reuses** existing policy instances if the route's inputs are unchanged, and builds new ones otherwise. Routes missing from the snapshot are dropped. The swap is atomic: a request sees the old chain or the new one, never a mix.
4. A per-route problem, such as an unknown policy, skips only that route, and its requests get a 500. A structural decode error causes a **NACK**, and the previous version stays in place.

The engine and Envoy receive config on separate streams, so a new route might briefly answer 500 if Envoy gets the route before the engine gets its chain **(unverified)**.

### 6d. An API key is created in the control plane and becomes enforceable

1. A user creates a key in the control plane (platform-api or a developer portal). The control plane sends an `apikey.created` WebSocket event to each connected gateway-controller. The event carries the API ID, key name, a **pre-computed hash**, and the expiry. The plaintext key never reaches the gateway. Keys can also be created on the gateway's own REST API, in which case the controller generates and hashes them.
2. The controller writes the key to its database (SQLite, Postgres or SQL Server) and publishes an API-key `CREATE` event on the EventHub.
3. Every controller replica's event listener reloads the key from the database into memory, then rebuilds the **entire** `APIKeyState` resource and pushes it on :18001.
4. The engine decodes the state and atomically replaces its API key store.
5. The next request carrying that key is accepted. End-to-end latency is mostly the EventHub poll interval.

Revocation follows the same path (`apikey.revoked`). Removing the key from the next full snapshot is what makes it stop working. Subscriptions (`subscription.*` events) take the same path into `SubscriptionState`.

---

## 7. Failure behaviour

| Situation | Behaviour |
|---|---|
| Engine down or socket unavailable | Envoy fails **closed** (`failure_mode_allow=false`); the request never reaches the upstream. The container also exits when any of its processes dies. |
| Engine slow | Each exchange is bounded by the ext_proc timeouts (60 s by default). On timeout the request fails closed. |
| No RouteConfig or no chain for the route | Generic 500 with an error ID in the logs. |
| Operation resolution fails (bad JSON-RPC, unknown A2A method) | Generic error shaped for the protocol. No fallback to another chain. |
| Policy error, or CEL condition error | 500 with an error ID. Fault policies can reshape it. |
| Policy panics | I found no panic recovery around policy execution, so a panic most likely takes down the engine and the container **(unverified)**. |
| Python executor error or timeout | 500 for that phase. Exception: a failing mid-stream **request** chunk fails open and is forwarded. |
| Body over 10 MiB decompressed | 413 for a buffered request; a streaming body fails closed. |
| Controller unreachable | The engine keeps its last-applied state and reconnects with backoff. A fresh engine reports unhealthy until its first chain snapshot. |
| Upstream or Envoy-generated error | Passed through unchanged, unless `policy_engine.fault_policies.handle_upstream_faults` is on, in which case fault policies run on it too. |

---

## 8. Adding a policy (high level)

1. **Write it.** In Go, implement the SDK interface `sdk/core/policy/v1alpha2` (module `github.com/wso2/api-platform/sdk/core`). Declare a processing mode, implement only the phases you need (request headers, request body, response headers, response body, their streaming variants, and fault), and export a factory. In Python, use the `apip-sdk-core` package (`sdk-python`); the policy runs in the Python executor.
2. **Declare it.** Put a `policy-definition.yaml` next to the code. It gives the name (kebab-case), a semver version, a JSON Schema for `parameters`, and optional `systemParameters` (operator-level defaults that can reference engine config).
3. **List it** in a build file, `gateway/build.yaml`, using one of:
   - `gomodule: <module>@vN` for a published Go policy,
   - `pipPackage:` for a published Python policy,
   - `filePath:` for local source (`gateway/dev-policies/` for experiments, `gateway/sample-policies/` for examples).
4. **Build.** The **Gateway Builder** (`gateway/gateway-builder`, a build-time tool) resolves versions and records the exact resolved versions in `build-manifest.yaml`. It then generates Go registration code and compiles one policy-engine binary containing every Go policy, plus a Python bundle and a `requirements.txt`. It also exports the policy definitions, which the gateway-controller image picks up so the controller can validate parameters at deploy time.
5. **Version and ship.** The registry is keyed by **name + major version**, so one build holds one `v1` of a policy. API authors reference a policy by name plus a version, which the controller resolves to the major. A breaking change means a new major (`@v2`). Shipping a policy means shipping a new gateway-runtime and gateway-controller image; nothing is hot-loaded.

---

## 9. Directory map

| Path | What's there |
|---|---|
| `gateway/gateway-runtime/` (Dockerfile, `docker-entrypoint.sh`, `health-check.sh`) | Runtime image build, process supervision, container probe |
| `gateway/gateway-runtime/router/config/` | Envoy bootstrap (ADS only) |
| `gateway/gateway-runtime/api/proto/`, `python-executor/` | Engine ↔ Python gRPC contract and the Python executor |
| `.../policy-engine/cmd/policy-engine/` | Process wiring: config, registry, xDS client, servers |
| `.../policy-engine/internal/kernel/` | ext_proc server, per-request execution, body modes, fault flow |
| `.../policy-engine/internal/executor/`, `resolver/`, `registry/` | Chain execution, operation resolvers, policy registry |
| `.../policy-engine/internal/xdsclient/`, `pkg/apikey/` | ADS client and handlers; API key state decoding |
| `.../policy-engine/internal/pythonbridge/` | Go proxy for Python policies |
| `.../policy-engine/internal/analytics/`, `admin/`, `metrics/`, `tracing/`, `config/` | Analytics/ALS, admin API, Prometheus, OTel, config defaults |
| `.../policy-engine/specs/` | Original design specs (dated) |
| `gateway/system-policies/` | `analytics` and `a2a` system policies |
| `gateway/build.yaml`, `gateway/build-manifest.yaml` | Policy list and resolved-version lock |
| `gateway/gateway-builder/` | Build-time policy compiler and code generator |
| `gateway/gateway-controller/pkg/policyxds/` | Policy-engine xDS server (:18001); chain and route snapshots |
| `gateway/gateway-controller/pkg/{apikeyxds,subscriptionxds,lazyresourcexds}/` | API key, subscription and lazy-resource snapshots |
| `gateway/gateway-controller/pkg/xds/translator.go`, `pkg/transform/` | Envoy config (ext_proc filter, route names); API/Agent/MCP/LLM → chains and routes |
| `sdk/core/policy/v1alpha2/`, `sdk/core/policyengine/` | Go policy SDK; shared wire types and stores |
| `sdk-python/` | Python policy SDK (`apip-sdk-core`) |
| `common/apikey/` | API key store and SHA-256 hashing |

---

## 10. Glossary

| Term | Meaning |
|---|---|
| ADS | Aggregated Discovery Service: one gRPC stream that carries several xDS resource types |
| ALS | Envoy Access Log Service: a gRPC stream of access-log entries, here sent to the engine for analytics |
| AuthContext | The structured result of authentication that policies leave for later policies |
| CEL | Common Expression Language, used for a policy's optional execution condition |
| Chain key | Key of a policy chain. Usually equal to the route name. For A2A, it is per operation. |
| Collector | The analytics/traffic-logging capture pipeline. It turns on implicitly when a consumer is enabled. |
| EventHub | The controller's database-backed event log that keeps controller replicas in sync |
| ext_proc | Envoy External Processing filter and protocol |
| Fault policies | Per-route policies that run only on a failing request, to shape the error |
| Gateway Builder | Build-time tool that compiles chosen policies into the engine binary |
| Immediate response | A policy action that answers the client directly and stops the chain |
| Lazy resource | A typed config blob pushed over xDS that policies look up on demand |
| NACK / ACK | The xDS client's rejection or acceptance of a pushed version |
| Policy definition | `policy-definition.yaml`: a policy's name, version and parameter schema |
| Policy instance | One configured use of a policy in a chain (name, version, params, condition) |
| RouteConfig | Per-route metadata and resolver settings pushed to the engine |
| RuntimeDeployConfig | The controller's internal compiled form of a deployed API (routes, chains, upstreams) |
| Sterile response | A generic error body with no internal details, plus an error ID for log correlation |
| System policy | A policy the controller injects (analytics, A2A), not authored by users |
| SotW | State of the World: every xDS push contains the full set for its type |
| UDS | Unix domain socket |
| vhost | Virtual host. Part of the route identity and the chain-key partition. |

---

### Points marked unverified
- What individual policies do: header names, JWKS fetching, rate-limit storage. Their source lives in `github.com/wso2/gateway-controllers`, which I did not inspect.
- Whether a rate limit is local to each engine instance or shared across instances.
- Whether metrics on :9003 are enabled in a given deployment.
- Possible brief 500 during a deploy, because Envoy and the engine are updated on separate streams.
- Effect of a panicking policy, inferred from the absence of panic recovery.
