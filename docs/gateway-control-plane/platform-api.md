# platform-api: onboarding guide

**For:** engineers on the agent-manager team who need to bring agent-manager's control plane back in line with platform-api.
**Scope:** services, interfaces, data and runtime behaviour. There is no code in this guide.
**Verified against:** `platform-api/` and `common/eventhub/` at `main` (commit a6fd8e30a, VERSION `0.17.0-SNAPSHOT`). Anything not checked is marked **(unverified)**. Behaviour that lives in the gateway rather than in platform-api is marked **(gateway-side)** and was read from `gateway/gateway-controller/pkg/controlplane`.

## 0. Key points

- platform-api is the **control plane (CP)**. It holds the desired state of every API, proxy and agent and drives the **gateway-controllers**, which are the **data plane (DP)**.
- One binary serves everything on **one HTTP mux**: the public REST API (`/api/v0.9`), the gateway API (`/api/internal/v1`), the gateway WebSocket, the portal login endpoint and an optional webhook. There is no separate internal port.
- platform-api **never calls a gateway**. Each gateway opens a WebSocket and authenticates with a gateway token. platform-api pushes small notifications down that socket. The gateway then pulls content over REST and sends a `deployment.ack` back up the socket.
- Notifications go through **SQL tables (`events`, `gateway_states`)** that every replica polls. Any replica can publish an event, and the replica that holds the gateway's socket delivers it.
- Deployment state lives in `deployment_status`: `DEPLOYING → DEPLOYED | FAILED`. It is driven by acks, guarded by `performed_at`, and backstopped by a timeout job that sets `FAILED` after 60 s by default.

## 1. Purpose and where it sits

```
 Management portal     AI Workspace     CLI `ap`        API Portal (devportal)
 (portals/api-         (portals/ai-     (cli/)           ▲ publish      │ HMAC
  control-plane)        workspace)                       │ (outbound)   │ webhook
        └──── Bearer JWT ───┴──────────────┘             │              ▼
                         ┌──────────────────── platform-api ─────────────────────┐
                         │ /api/v0.9/*   /api/portal/v0.9/auth/login   /api/internal/* │
                         └──────────┬───────────────────────────▲───────────────┘
                                    │ shared SQL DB (SQLite | Postgres | SQL Server)
                WebSocket, opened by the gateway: events ↓, deployment.ack ↑
                REST pulls, initiated by the gateway: /api/internal/v1/*  (header `api-key`)
                         ┌──────────┴──────────┐
                         │ gateway-controller   │ ── xDS ──► router / policy engine
                         └─────────────────────┘
```

| Caller | Interface | Purpose |
|---|---|---|
| Management portal | Public REST API with a user JWT | Organizations, projects, gateways, APIs, deployments, keys, plans |
| AI Workspace | Public REST API with a user JWT | LLM providers and proxies, MCP proxies, agents (unverified: exact endpoints) |
| CLI `ap` | Public REST API | Scripted management |
| Portals in `file` auth mode | `POST /api/portal/v0.9/auth/login` | Swap a username and password for an RS256 JWT |
| API Portal | `POST /api/internal/v0.9/webhook/events` with an HMAC signature (off by default) | Consumer-side API keys, subscriptions and applications |
| Gateway-controllers | WebSocket plus `/api/internal/v1/*` | Receive events, pull artifacts, ack deployments, push their own artifacts |

Each gateway is registered under an organization and has a functionality type: `regular`, `ai` or `event`.

## 2. How it runs

### 2.1 Binaries and builds

- **`cmd/main.go`** is the server. It requires `-config <file.toml>`, which can be repeated; files merge in order and the last one wins.
- **`cmd/migrate`** is a one-off **v1→v2 data migration** for Postgres (Bijira migration). It does not upgrade the schema.
- **`platform/` and `pdk/`** let a wrapper module embed the server and add external plugins.
- **Build tags.** The default (OSS) build has no in-tree plugins. The `experimental` tag (`make cloud-build`) adds the **eventgateway** plugin, which provides WebSub and WebBroker APIs.

### 2.2 Configuration

- **Format:** TOML. Every key sits under `[platform_api]`.
- **No environment provider.** Values come from the file, or from explicit `{{ env "NAME" "default" }}` and `{{ file "/path" }}` tokens. `file` tokens may only read from `/etc/platform-api` and `/secrets/platform-api`.
- **Defaults:** `config/default_config.go`. **Quickstart sample:** `config/config.toml`.
- **Validation is fail-closed.** Startup refuses to proceed on an invalid database driver, a CORS `*`, an encryption key that is not 32 bytes, the removed `auth.skip_paths` key, and similar errors.

| Section | Defaults and key settings |
|---|---|
| `server.http` / `server.https` | HTTP on `9080`, **off**. HTTPS on `9243`, **on**. Certificates are required. |
| `server.timeouts` | Read-header 10 s, read 60 s, write 120 s, idle 120 s |
| `server.websocket` | `max_connections` 1000 per replica, `connection_timeout` 30 s (pong deadline), `rate_limit_per_min` 1000 connection attempts per IP |
| `database` | `sqlite3` \| `postgres`/`pgx` \| `sqlserver`/`mssql`, plus pool settings |
| `auth` | `mode`, `authorization.{enabled,mode,role_to_scope_mapping}`, `jwt`, `idp`, `file`, `claim_mappings` |
| `security` | `encryption_key` (32-byte AES key for secrets and stored credentials), `api_key.hashing_algorithms` (`sha256`) |
| `event_hub` | `poll_interval` 3 s, `cleanup_interval` 10 min, `retention_period` 1 h |
| `deployments` | Timeout check every 20 s with a 60 s timeout. `max_per_api_gateway` 20, `max_builds_per_api` 5. |
| `gateway` | Whether to verify the manifest's version and functionality type |
| `webhook`, `http_client`, `read_only` | Inbound webhook. Shared outbound client with an SSRF guard. Maintenance mode. |

### 2.3 Listeners and route families

HTTP and HTTPS are independent listeners. Both serve the same middleware chain and the same mux.

| Prefix | Authentication | Purpose |
|---|---|---|
| `/api/v0.9/...` | User JWT plus OpenAPI scope check | Public management API |
| `/api/portal/v0.9/auth/login` | None | File-mode login |
| `/api/internal/v1/ws/gateways/connect` | Gateway token (`api-key` header) | Gateway WebSocket |
| `/api/internal/v1/...` | Gateway token (`api-key` header) | Gateway pull and push API |
| `/api/internal/v0.9/webhook/events` | HMAC signature | API Portal webhook |
| `/health` | None | Liveness. `/metrics` is on the skip list but has no handler. |

The internal routes bypass JWT authentication and scope checks through a **built-in skip-path list that operators cannot configure**. Each internal handler authenticates the gateway token itself.

### 2.4 Authentication and authorization

| `auth.mode` | Behaviour |
|---|---|
| `internal_token` (default) | Verifies RS256 JWTs minted elsewhere with `jwt.public_key_file`. `internal_token.skip_validation` trusts tokens without checking their signature; it is off by default. |
| `file` | Local users with bcrypt hashes and roles. The login endpoint mints RS256 JWTs. Seeds one organization. There is no default user, and the mode is not meant for production. |
| `idp` | Validates against an external IDP's JWKS (Thunder, Keycloak, Asgardeo) and checks the issuer. The audience is checked only when one is configured. |

- **Organization resolution.** The token's organization claim is matched against `organizations.idp_organization_ref_uuid` first, then against `organizations.uuid`. All tenant queries use the resolved UUID.
- **Scopes.** Each operation in `resources/openapi.yaml` declares OAuth2 scopes of the form `ap:<resource>[:<sub>]:<verb>`, plus a broader `:manage` variant. The scopes are OR-ed.
- **Enforcement is deny-by-default:** a registered route with no declared scope is rejected, and startup fails if the spec and the routes disagree.
- **Role mode.** With `authorization.mode=role`, the token's roles are expanded through `role-to-scope-mapping.yaml`. The shipped roles are `ap_admin`, `ap_operator`, `ap_publisher`, `ap_subscriber` and `ap_viewer`.
- **Gateway authentication.** The token is 32 random bytes, base64url-encoded. Only its SHA-256 hash is stored, in `gateway_tokens`. A gateway may hold at most **two active tokens**, which allows rotation. The token is checked when the WebSocket connects and on every internal REST call.

### 2.5 Databases and schema

- **Engines:** SQLite (default `./data/api_platform.db`), PostgreSQL and SQL Server. Queries use `?` placeholders, rebound for each driver.
- **Schema files:** `internal/database/schema.{sqlite,postgres,sqlserver}.sql`. The plugin adds `plugins/eventgateway/schema/*`.
- **No migration framework.** The DDL is idempotent `CREATE ... IF NOT EXISTS`.
  - **SQLite:** applied automatically at startup.
  - **Postgres and SQL Server:** the operator must provision the schema before starting the server.
- **Schema changes:** shipped tables are frozen, so only additive changes are allowed (`.claude/rules/db-schema-changes.md`).

### 2.6 Background jobs (every replica runs all of them)

| Job | Cadence | Effect |
|---|---|---|
| EventHub poll | 3 s | For gateways with a **local** subscriber whose `gateway_states.version_id` changed, read new `events` and hand them to the dispatcher |
| EventHub cleanup | 10 min | Delete `events` older than 1 h, **whether or not they were delivered** |
| Deployment timeout | 20 s | Set `DEPLOYING`/`UNDEPLOYING` rows with `performed_at` older than 60 s to `FAILED`/`DEPLOYMENT_TIMEOUT`. The update is guarded, so concurrent runs are safe. |
| WebSocket heartbeat | 20 s per connection | Send a ping. Unregister the connection if no pong arrives within 30 s. |
| Startup seeding | Once | File-mode organization, plus default LLM provider templates for every organization |

### 2.7 HA and multi-replica model

- **Stateless REST.** Any replica can serve any REST call. All durable state is in the shared database.
- **Sticky sockets.** A gateway's socket is pinned to the replica that accepted it. A clustered gateway can have several sockets, possibly on different replicas.
- **No cross-replica channel.** There is no RPC between replicas, no leader election and no cache invalidation. The SQL EventHub is the only path between replicas.
- **SQLite** in practice means a single replica (unverified as a documented constraint).
- **Per-replica in-memory state.** All of the following is lost on restart:

| State | Effect |
|---|---|
| Connection registry (gateway → connections) | Only this replica can deliver to those sockets |
| One hub subscription per gateway, held by the dispatcher | Created on the first local connection, removed on the last |
| Hub cursor per gateway (`lastPolled`, `knownVersion`) and subscriber channels (buffer of 100) | Decides how far back events are replayed on reconnect (section 5) |
| Per-IP connection rate-limit map | Limits are enforced per replica |
| Agent-card cache (60 s positive TTL, 10 s negative TTL) | May be stale for up to 60 s per replica |
| Config, scope registry, role map, read-only list | Restart to change. All replicas must match. |

- **`gateways.is_active` is approximate.** A replica sets it to true on connect. It sets it to false when that replica has no connections left for the gateway, even if another replica still holds one. Nothing resets it after a replica crash.

### 2.8 Read-only (maintenance) mode

`read_only.enabled` blocks writes with **503** for every organization except the UUIDs in `writable_organizations`. It also drops deployment acks, skips `is_active` updates and the timeout job, and makes the manifest and import endpoints refuse. The code marks it `TEMP-READ-ONLY-MODE`: it exists for the Bijira v1→v2 migration and is meant to be deleted.

## 3. Internal structure

```
request ─► [plugin pre-chain] → CORS → authn (file|internal_token|idp) → org resolver
          → OpenAPI scope enforcer → read-only guard → [plugin post-chain] → ServeMux
                                                                             │
 handler/     decode, resolve actor, map errors  ◄──────────────────────────┘
   ▼
 service/     rules, gatewaytranslator, publish events ─► GatewayEventsService ─► EventHub (SQL)
   ▼                                                                             │
 repository/  SQL per entity + ArtifactTableRegistry (UNION across kind tables)  ▼
   ▼                                                        EventDispatcher ─► WebSocket Manager ─► gateways
 database/    *sql.DB wrapper, dialect rebind/pagination/upsert
```

All wiring is written by hand in `internal/server/server.go`. API models are generated from the OpenAPI spec (`api/generated.go`); the handlers are written by hand.

| Service group | Responsibilities |
|---|---|
| Tenancy | Organizations and projects. Identity maps an IDP `sub` to an internal user UUID (`user_idp_references`); events convert it back to the `sub`. |
| Gateways | Registration, tokens, manifest, custom-policy sync, the internal pull API (`GatewayInternalAPIService`), bottom-up import (`ArtifactImportService`) |
| APIs | REST APIs (definition, documents, thumbnails), GraphQL APIs (SDL, introspection) |
| AI | LLM provider templates (seeded per organization), LLM providers, LLM proxies (each references a provider), MCP proxies, agent proxies (A2A agent cards) |
| Deployments | One service per kind, all following the same pattern (build → deployment → status → event → ack), plus `DeploymentsByKind` and the timeout job |
| Consumers | API keys (with per-kind variants), applications, subscriptions, subscription plans. All broadcast to gateways. |
| Secrets | In-house AES-GCM vault. `{{ secret "handle" }}` placeholders are inlined at fetch time for older gateways. |
| Publishing | API Portals and publications. Pushes APIs to API Portals over HTTP. |
| Translation | `internal/gatewaytranslator` converts stored artifacts to the shape the target gateway **release** understands, at deploy and fetch time |

**Plugins.** There are two tiers. *Internal* plugins (`internal/plugin`, such as eventgateway) get full repository access. *External* plugins (`pdk`) see only public interfaces. A plugin can add or override routes, add middleware, add skip paths and scopes, and add schema.

## 4. Interfaces

### 4a. Public management REST API

- **Spec:** `platform-api/resources/openapi.yaml` (base `/api/v0.9`). The plugin adds `plugins/eventgateway/openapi.yaml`, and login is in `resources/portal-api.yaml`.
- **Resource families:**
  - Tenancy: `organizations`, `projects`, `me`
  - Gateways: `gateways` (plus `/tokens` and `/manifest`), `gateway-custom-policies`
  - APIs and AI artifacts: `rest-apis`, `graphql-apis`, `llm-provider-templates`, `llm-providers`, `llm-proxies`, `mcp-proxies`, `agent-proxies`
  - Consumers: `applications`, `subscriptions`, `subscription-plans`
  - Other: `secrets`, `api-portals`, `api-publications`
- **Shared sub-resources:** each deployable kind has the same `/{id}/builds`, `/{id}/deployments` (deploy, `/{dep}/undeploy?gatewayId=`, `/{dep}/restore`, list, get, delete) and `/{id}/api-keys`.
- **Identifiers:** public paths use **handles**, which are slugs unique per organization. The internal API uses UUIDs.

### 4b. Gateway REST API (`/api/internal/v1`)

**Contract:** `resources/gateway-internal-api.yaml`. Every call sends the `api-key` header. The organization and gateway are taken **from the token**, never from the request.

| Endpoint | The gateway uses it to |
|---|---|
| `GET /apis/{id}`, `/llm-providers/{id}`, `/llm-proxies/{id}`, `/mcp-proxies/{id}`, `/agents/{id}`, `/graphql-apis/{id}` (`/websub-apis/{id}`, `/webbroker-apis/{id}` in the experimental build) | Fetch the current deployment of that artifact on this gateway after a `*.deployed` event. Returns a **zip** containing the YAML. 404 if there is no deployment with desired state DEPLOYED. 422 if a secret can't be inlined; the deployment is then marked `FAILED`/`SECRET_RESOLUTION_FAILED`. |
| `GET /deployments[?since=]` | Get the **startup sync list**: every CP-originated status row for this gateway, as `{artifactId, deploymentId, kind, state, deployedAt, etag}`. `state` is the **desired** state (DEPLOYED or UNDEPLOYED). Rows are ordered by kind dependency. |
| `POST /deployments/fetch-batch` | Fetch **bulk content**: a list of deployment IDs in, a tar.gz of YAMLs out (`Accept: application/x-tar+gzip`). An entry that can't be rendered is left out and marked `FAILED`. |
| `POST /artifacts/exists` | Check which local orphans still exist on the platform. The gateway keeps those, along with their keys and subscriptions. |
| `POST /artifacts/import-gateway-artifacts` | Push gateway-created artifacts up to the CP (flow 7.6) |
| `GET /secrets[?updatedAfter=&includeValues=true]`, `GET /secrets/{handle}/value` | Sync the secrets referenced by this gateway's deployments, detecting changes by hash |
| `GET /websub-apis/{id}/secrets` | Get decrypted WebSub HMAC secrets |
| `GET /{apis,llm-providers,llm-proxies,agents,graphql-apis,websub-apis,webbroker-apis}/api-keys[?issuer=]` | Run a **bulk API-key sync** for one kind: hashed keys for artifacts that have a status row on this gateway |
| `GET /apis/{id}/subscriptions` | Get a REST API's subscriptions. 403 if the API is not on this gateway. |
| `GET /subscription-plans` | Get every plan in the organization |
| `POST /gateways/{id}/manifest` | Report the gateway's version, functionality type and installed policies. The CP overwrites `gateways.version`. A mismatch returns 409 when verification is enabled. |

**There is no applications endpoint.** Applications reach gateways only through `application.updated` events.

### 4c. Gateway WebSocket: `GET /api/internal/v1/ws/gateways/connect`

**Handshake:**

1. Check the per-IP connection rate limit (429 if exceeded).
2. Look up the `api-key` token. A missing or invalid token gets the same generic 401.
3. Upgrade the connection. Any origin is accepted.
4. Register the connection. If the replica is full, the server sends `{"type":"error"}` and closes the socket.
5. Send **`connection.ack`** `{type, gatewayId, connectionId, timestamp}`.
6. Set `is_active=true` and start the read loop.

**Liveness:** the server pings every 20 s. If no pong arrives within 30 s, it closes the socket.

**Messages from the server.** Each message is the envelope `{type, payload, timestamp, correlationId, userId?}`, at most 1 MiB. Deploy and undeploy payloads are thin, `{apiId, deploymentId, performedAt}`: the gateway pulls the content itself.

| Event | Meaning and recipients |
|---|---|
| `api.deployed` / `.undeployed` / `.deleted` | Fetch and apply / remove / purge a REST API. `deleted` goes to **every gateway in the organization**. |
| `llmprovider.*`, `llmproxy.*`, `mcpproxy.*`, `agent.*`, `graphqlapi.*` (`websub.*`, `webbroker.*` experimental) | The same three actions for each kind. Restoring a deployment also sends `deployed`. |
| `websub.hmacsecret.created\|updated\|deleted` | A WebSub HMAC secret changed |
| `apikey.created\|updated\|revoked` | A key's hashes, masked form, status, expiry or issuer changed. Never carries plaintext. Sent to the gateways associated with the artifact. |
| `subscription.created\|updated\|deleted` | A subscription changed. Sent to the gateways associated with the API. |
| `subscriptionPlan.created\|updated\|deleted` | A plan changed. Sent to **every gateway in the organization**. |
| `application.updated` | An application's metadata plus its full set of key mappings (an empty set means "clear"). Sent to the gateways where the mapped artifacts are deployed. |

**Messages from the gateway.** The only one handled is **`deployment.ack`** `{deploymentId, artifactId, resourceType, action: deploy|undeploy, status: success|failed, performedAt, errorCode?}`. Other types are ignored.

### 4d. Other traffic

- **Inbound webhook** (API Portal → CP):
  - API keys: `apikey.generated`, `apikey.regenerated`, `apikey.revoked`, `apikey.application_updated`
  - Subscriptions: `subscription.created`, `.updated`, `.plan_changed`, `.token_regenerated`, `.deleted`
  - Applications: `application.created`, `.updated`, `.deleted`

  The webhook calls the same services as the REST API, so it fans out the same gateway events.
- **Outbound.** All of it goes through one SSRF-guarded client:
  - API Portal publish and plan listing, with retries and a per-portal shared key
  - IDP JWKS
  - OpenAPI fetch by URL
  - MCP server probes
  - A2A agent cards
  - GraphQL introspection
  - Thumbnails

  **There are no calls to gateways.**

## 5. Event delivery architecture

```
 any replica                         shared DB                           replica holding the gateway's socket
 Service ─► GatewayEventsService     ┌───────────────────────────┐
   (build envelope)                  │ gateway_states             │◄── poll every 3 s, paged; only gateways
   ─► EventHub.Publish ── one TX ──► │  version_id := new UUID     │    with a LOCAL subscriber; version changed?
                                     │ events (gateway_id,event_id,│──► SELECT events WHERE processed_ts >= cursor
                                     │  processed_ts, event_data)  │         ▼
                                     └───────────────────────────┘    subscriber channel (buffer 100)
                                                                           ▼
                                                                      EventDispatcher ─► WS Manager ─► each local
                                                                      connection (send error ⇒ unregister ⇒
                                                                      the gateway reconnects)
```

**Why a SQL hub?** Writes land on arbitrary replicas, but a socket lives on exactly one. Storing events in the shared database lets every replica see them without a message broker or routing between replicas. `version_id` is a cheap per-gateway "something changed" marker, so a poll skips idle gateways without scanning `events`.

**Mechanics** (`common/eventhub/sqlbackend.go`, `topic.go`; `internal/service/event_dispatcher.go`):

- **Publish is atomic within the hub.** Inserting the event and bumping the version happen in one transaction. A missing `gateway_states` row is created first, so events queue up even before a gateway's first connection. The primary key `(gateway_id, event_id = correlationId)` makes a duplicate publish a no-op.
- **Publish is not atomic with the business write.** The `deployments` or `api_keys` row commits first, and a failed publish is only logged.
- **Subscription lifecycle.** On a replica's first connection for a gateway, the dispatcher calls `RegisterGateway`, which upserts the state row and creates an in-memory entry, then `Subscribe`. Further connections share that subscription, and the last disconnect unsubscribes.
- **Replay cursor.**
  - **First subscription on a replica** (`lastPolled`=0): replays only the last **120 s** (`initialPollSkewWindow`).
  - **Reconnect to the same replica:** the cursor (`lastPolled`) is **kept**, so the replica replays everything since the last delivered event, back to the 1 h retention limit. Comments in `pollGatewayWithState` and `EventDispatcher.deliver` say otherwise; trust the code.
  - **Equal timestamps:** the query uses `>=` on the cursor and trims a single boundary duplicate.
- **Backpressure.** If a channel is full, delivery stops at that event without advancing the cursor, and the next poll retries.
- **Guarantees.** Delivery is at-least-once within the replay window, so duplicates are possible and gateways must be idempotent. There is no per-event acknowledgement except `deployment.ack`, which updates deployment status, not the hub.
- **Latency:** up to one poll interval (3 s).

## 6. Data model

**Conventions.** Primary keys are `uuid VARCHAR(40)`. Most tables carry `organization_uuid` with `ON DELETE CASCADE`, a `handle` that is unique per organization, `data_version`, and `created_by`/`updated_by`, which hold internal user UUIDs. Definitions are stored as `configuration`/`content` blobs (YAML or JSON). **Artifacts use class-table inheritance:** a row in `artifacts(uuid, type, organization_uuid)` plus one row in the table for that kind, sharing the same UUID.

**Tenancy and identity**

| Table | Key columns and meaning |
|---|---|
| `organizations` | `uuid`, `handle`, `region`, `idp_organization_ref_uuid` (what the token's organization claim is matched against) |
| `projects` | `uuid`, `organization_uuid`, `handle` |
| `user_idp_references`, `user_organization_mappings` | Internal user UUID ↔ IDP `sub`; which organizations a user has acted in |
| `audit` | `action`, `resource_uuid`, `resource_type`, `performed_by`, `performed_at` |

**Gateways**

| Table | Key columns and meaning |
|---|---|
| `gateways` | `uuid`, `organization_uuid`, `handle`, `version` (overwritten by the manifest), `gateway_functionality_type`, `properties`, `manifest`, `is_active`, `is_critical` |
| `gateway_tokens` | `gateway_uuid`, `token_hash` (SHA-256), `status` (active/revoked), `revoked_at` |
| `gateway_endpoints` | Advertised URLs (informational) |
| `gateway_custom_policies`, `gateway_custom_policy_usages` | Policies synced from manifests, and which artifacts use them |
| `artifact_gateway_mappings` | PK `(org, artifact, gateway)`. Written on the first deploy. It decides where key and subscription events go. |

**Artifacts, by kind** (`origin` is `control_plane` or `gateway_api`; `gateway_api` rows are read-only in the CP)

| Table | `artifacts.type` | Notes |
|---|---|---|
| `rest_apis` | `RestApi` | `project_uuid`, `version`, `lifecycle_status`, `configuration` |
| `graphql_apis` | `GraphQLApi` | `project_uuid`, `configuration` |
| `llm_provider_templates` | (not an artifact) | `group_id`, `version`, `is_latest`, `managed_by` |
| `llm_providers` | `LlmProvider` | `template_uuid`, `model_list`. Organization-level, with no project. |
| `llm_proxies` | `LlmProxy` | `project_uuid`, `provider_uuid` |
| `mcp_proxies` | `Mcp` | `project_uuid` (nullable) |
| `agent_proxies` | `AgentProxy` (gateway calls it `Agent`) | `project_uuid`, `protocol` |
| `websub_apis`, `webbroker_apis`, `websub_api_hmac_secrets` | `WebSubApi`, `WebBrokerApi` | eventgateway plugin only |

**Builds and deployments**

| Table | Key columns and meaning |
|---|---|
| `builds` | `uuid`, `build_id` (readable), `artifact_uuid`, `content`, `data_version`. An immutable snapshot, not tied to any gateway. |
| `deployments` | `uuid`, `artifact_uuid`, `gateway_uuid`, `build_uuid`, `base_deployment_uuid`, `content` (**already translated** for that gateway's release), `metadata`. Immutable; the history is capped per (artifact, gateway). |
| `deployment_status` | PK `(org, artifact, gateway)`. Columns: `deployment_uuid` (the current pointer), `status` (DEPLOYING, DEPLOYED, UNDEPLOYING, UNDEPLOYED, FAILED), `status_desired` (DEPLOYED or UNDEPLOYED), `performed_at` (the concurrency token, in ms), `status_reason` (a code of at most 50 characters). A deployment that no status row points to is reported as **ARCHIVED**. |

**Consumers**

| Table | Key columns and meaning |
|---|---|
| `api_keys` | `artifact_uuid`, `handle`, `masked_api_key`, `api_key_hashes` (JSON keyed by algorithm), `status`, `expires_at`, `issuer`, `allowed_targets` (gateway names or `ALL`), `created_by`. Scoped to an **artifact**, not a gateway. Only the creator, or a holder of `ap:api_key:all:manage`, may manage a key. |
| `applications`, `application_api_key_mappings`, `application_artifact_mappings` | Consumer applications, the keys they own and the APIs they use |
| `subscription_plans`, `subscription_plan_limits`, `artifact_subscription_plans` | Organization-wide throttling plans, their limits, and the plans each API offers |
| `subscriptions` | `artifact_uuid`, `application_id`, `subscriber_id`, `subscription_token` (encrypted) and its hash, `subscription_plan_uuid`, `status` |

**Secrets:** `secrets` (`ciphertext` AES-GCM, `hash`, `status`), `secret_scopes` (org, project or artifact) and `artifact_secret_refs` (which artifact, and which gateway, references which secret handle; this controls what a gateway may read).
**Portal publishing:** `api_portals` (each with an encrypted `internal_auth_key`), `api_publications`, `api_publication_contents`, `api_documents`, and their doc and plan mapping tables.
**EventHub:** `gateway_states(gateway_id PK→gateways, version_id, updated_at)` and `events(PK gateway_id+event_id, processed_timestamp, originated_timestamp, entity_type, action CREATE|UPDATE|DELETE, entity_id, event_data = the envelope)`.

```
organizations ─┬─< projects ─┬─< rest_apis | graphql_apis | llm_proxies | mcp_proxies | agent_proxies
               │             └─< applications ─┬─< application_api_key_mappings >── api_keys
               │                               └─< application_artifact_mappings >── artifacts
               ├─< artifacts ══1:1══ <kind row>
               │     ├─< builds ◄──────────── deployments.build_uuid
               │     ├─< deployments >── gateways
               │     ├─< deployment_status (org+artifact+gateway) ──► deployments (current)
               │     ├─< artifact_gateway_mappings >── gateways
               │     ├─< api_keys
               │     ├─< subscriptions >── subscription_plans ─< subscription_plan_limits
               │     └─< artifact_secret_refs ··· secrets ─< secret_scopes
               ├─< gateways ─┬─< gateway_tokens, gateway_endpoints
               │             └─1 gateway_states ─< events
               ├─< llm_provider_templates ─< llm_providers ─< llm_proxies
               └─< subscription_plans, secrets, gateway_custom_policies, api_portals, audit
```

## 7. Key flows

### 7.1 Gateway registration and connection

1. An admin calls `POST /api/v0.9/gateways` with a handle, name, endpoints, functionality type and optional version. This creates the rows with `is_active=0`. **No token is issued.**
2. The admin calls `POST /gateways/{handle}/tokens`. platform-api stores the token's hash and returns the plaintext **once**. To rotate, create a second token, then `DELETE .../tokens/{id}` the old one.
3. The gateway-controller starts with the host and the token, then connects to the WebSocket (4c). On a replica's first connection for the gateway, the dispatcher subscribes, which replays the last 120 s.
4. The gateway posts its manifest. platform-api checks major.minor and the functionality type, stores the policies and **overwrites `gateways.version`**. All later translation targets that version.

### 7.2 Deploying an API (DEPLOYING → DEPLOYED, or FAILED)

1. The client calls `POST /rest-apis/{handle}/deployments` with `{name, gatewayId (the handle), base: current|build, buildId?, metadata?}`.
2. The service checks that:
   - the gateway belongs to the organization,
   - the API exists,
   - the API's kind is supported by the gateway's release,
   - the API's `origin=control_plane`.
3. It resolves a build. `current` renders a new `builds` row. `build` loads the named one.
4. It applies the overrides (`endpointUrl`, `vhostMain`, `vhostSandbox`), translates the artifact to the gateway's version and serializes it to YAML.
5. It inserts the `deployments` row (enforcing limits) and ensures an `artifact_gateway_mappings` row exists.
6. It sets `deployment_status` to `DEPLOYING`, desired `DEPLOYED`, `performed_at=now`.
7. It publishes `api.deployed`. It also backfills existing keys as `apikey.created` events, skipping expired keys and keys whose `allowed_targets` exclude this gateway.
8. The HTTP response returns `DEPLOYING`.
9. The replica that holds the socket delivers the event within about 3 s.
10. The gateway calls `GET /api/internal/v1/apis/{uuid}` and receives a zip of the YAML, with secrets inlined for older gateways.
11. The gateway applies it and sends `deployment.ack`.
12. `success` + `deploy` triggers `UPDATE … DEPLOYED WHERE status='DEPLOYING' AND performed_at=ack.performedAt`. `failed` sets `FAILED` with the sanitized `errorCode`, from any status, provided `performed_at` matches. Acks that don't match are discarded.
13. **With no ack,** the timeout job sets `FAILED`/`DEPLOYMENT_TIMEOUT` 60–80 s after the deploy.

### 7.3 Undeploy, restore and delete

- **Undeploy:** the deployment must be the current one and be DEPLOYED or DEPLOYING. The service sets `UNDEPLOYING`/desired `UNDEPLOYED` with a new `performed_at` and publishes `api.undeployed`. An ack moves the row to `UNDEPLOYED`; no ack leads to `FAILED`.
- **Restore:** points the status row back at an older deployment (`DEPLOYING`) and publishes `*.deployed`.
- **Delete a deployment:** allowed only when it is not DEPLOYED or DEPLOYING. Sends no event.
- **Delete an API:** a CP-originated API can be deleted **while deployed**. FK cascades remove its deployments, status rows, keys and subscriptions. `api.deleted` then goes to **every gateway in the organization**. A DP-originated API must be undeployed everywhere first.
- **Delete a gateway:** removes its deployments, status rows, mappings, tokens and hub rows. Nothing is sent to the gateway. Neither this nor revoking a token closes an already-open socket.

### 7.4 Gateway startup sync, from the platform side

The sequence is driven by the gateway **(gateway-side)**:

1. Secrets: bulk, or incremental by hash.
2. `GET /deployments`.
3. Diff the list against local state. Before deleting local orphans, call `POST /artifacts/exists` and keep those that still exist.
4. `POST /deployments/fetch-batch`.
5. `POST /artifacts/import-gateway-artifacts`.
6. `GET /subscription-plans`, then `GET /apis/{id}/subscriptions` for each API, then `GET /<kind>/api-keys` for each kind.
7. `POST /gateways/{id}/manifest`.

**(gateway-side)** Steps 2–4 run only on the **first** connection of a controller process. Reconnects redo secrets, push, plans, subscriptions and keys, but not deployments. The startup sync sends **no** `deployment.ack`.

### 7.5 API key creation broadcast

1. The client calls `POST /rest-apis/{handle}/api-keys` (or the LLM, MCP, agent or GraphQL variant), or the API Portal sends a webhook.
2. The service resolves the artifact by handle and kind. The targets are its `artifact_gateway_mappings` rows; an empty list is valid.
3. It uses the supplied key or generates one (32 random bytes, hex), hashes it with each configured algorithm and stores it with `status=active`, the masked value, `expires_at`, `issuer` and `allowed_targets=ALL`.
4. For each target gateway it publishes `apikey.created` with the hashes and the masked key, never the plaintext. `userId` is the actor's IDP `sub`.
5. The response returns the plaintext only if platform-api generated it.
6. **Recovery.** A gateway that missed the event picks up the key on its next key sync. A gateway associated later receives it through the deploy-time backfill.

### 7.6 Bottom-up import (gateway-originated artifacts)

1. The gateway posts multipart data. The `artifacts` part is a zip of `artifacts.json`, a list of `{dpid, configuration{kind,metadata,spec}, status, createdAt, updatedAt, deployedAt, properties.deploymentRevision}`. The `total` part is a count.
2. The server sorts the items by kind dependency and imports each one independently; one failure doesn't stop the others.
3. For each item:
   1. `status=undeployed` is handled as an undeploy.
   2. For project-scoped kinds, resolve the project from a metadata annotation.
   3. Match an existing artifact **by handle** within the organization. A kind mismatch is an error.
   4. The CP mints **its own** UUID; the `dpid` is used only for tracing.
   5. Metadata follows last-write-wins on `deployedAt`. The artifact is stored with `origin=gateway_api`.
   6. Write a `deployments` row with the mapped status, unless `deploymentRevision` is unchanged, and ensure the gateway mapping.
4. The response maps each `dpid` to its result, with counts. It is 200 unless the request itself is malformed.
5. These artifacts are excluded from `GET /deployments`.

## 8. Failure and recovery behaviour

| Situation | Behaviour |
|---|---|
| Gateway offline when an event is published | The event waits in `events` for up to 1 h, then is deleted. A pending deploy becomes `FAILED` after 60–80 s. |
| Reconnect to the **same** replica | Replays everything since the last delivered event, within the 1 h retention |
| Reconnect to a **different or restarted** replica | No cursor exists there, so only the last **120 s** are replayed. Keys, plans, subscriptions and secrets are resynced on reconnect. Deployments are resynced only when the controller restarts **(gateway-side)**, so a deploy event missed during a long disconnect can be lost until then. |
| Replica crash | Its sockets drop and gateways reconnect elsewhere (see above). Other replicas keep running the timeout job. `is_active` can stay `true` for a gateway that never returns. |
| Ack never arrives | `FAILED`/`DEPLOYMENT_TIMEOUT`. A late `success` ack is **discarded**, because it requires `DEPLOYING`. `status_desired` stays `DEPLOYED`, so the next startup sync still ships the artifact, but the status stays `FAILED` until someone redeploys. A timed-out undeploy behaves the same way. |
| Stale or duplicate ack | Ignored, because `performed_at` must match the current row |
| Publish fails | Logged only, and the business row stays committed. A deploy then times out. A key or subscription is recovered at the gateway's next sync. |
| Socket send error | The connection is unregistered at once, and the gateway reconnects |
| Slow gateway (channel buffer full) | Delivery pauses with the cursor unchanged and is retried on the next poll |
| Secret can't be resolved at fetch | 422 for a single fetch, or the entry is omitted from a batch. The deployment becomes `FAILED`/`SECRET_RESOLUTION_FAILED`, and the desired state is kept. |
| Token revoked or gateway deleted while connected | The open socket stays up. Every REST call and the next reconnect return 401. |
| Read-only mode | Writes return 503. Acks are dropped and timeouts skipped, so statuses freeze. |

## 9. Directory map (`platform-api/`)

| Path | Contents |
|---|---|
| `cmd/` | Server entrypoint and the `migrate/` v1→v2 data migrator |
| `config/` | Config structs, defaults, loader and validation, TLS parsing, read-only mode, sample TOML |
| `api/` | Generated OpenAPI models plus hand-written additions |
| `internal/server/` | Wiring, middleware chain, plugin loading, listeners, shared outbound client |
| `internal/middleware/` | Authentication, claims, organization resolver, scope registry and enforcer, role map, read-only guard |
| `internal/handler/` | HTTP handlers, including `gateway_internal.go` and `websocket.go` |
| `internal/service/` | Business logic, per-kind deployment services, `gateway_events.go`, `event_dispatcher.go`, the timeout job, import |
| `internal/repository/`, `internal/model/`, `internal/dto/` | SQL access, including the artifact-table registry; models; event payloads and DTOs |
| `internal/database/` | Connection, dialect helpers, schema files for each engine |
| `internal/websocket/` | Connection manager, connections, transport |
| `internal/gatewaytranslator/` | Translation to each gateway release (`kinds/`, `gwversion/`, `translate/`, `secretinline/`) |
| `internal/webhook/`, `internal/vault/`, `internal/apperror/` | API Portal webhook receiver; AES-GCM vault; error catalog |
| `internal/migration/`, `internal/integration/` | v1→v2 migrator logic; integration tests across database engines |
| `internal/{utils,constants,logger,pagination,client,plugin,builtins,agentproto,router}` | Helpers, the plugin contract, built-in plugin selection, the A2A protocol, the route recorder |
| `plugins/eventgateway/` | Experimental WebSub and WebBroker plugin (handlers, services, repositories, schema, spec) |
| `pdk/`, `platform/` | External plugin SDK; embeddable server facade |
| `resources/` | `openapi.yaml`, `gateway-internal-api.yaml`, `portal-api.yaml`, `role-to-scope-mapping.yaml`, LLM templates |
| `spec/`, `it/`, `scripts/` | Design and PRD notes (possibly out of date); database containers for integration tests; local development setup |

Shared modules: `common/eventhub` (the hub), `common/authenticators` (IDP JWT middleware), `common/models`, `common/configinterpolate`, and `httpkit/` (CORS, middleware chaining, HTTP client).

## 10. Glossary

| Term | Meaning |
|---|---|
| CP / DP | Control plane (platform-api) / data plane (gateway-controller plus router) |
| Artifact, kind | Anything deployable or keyable / its type string (`RestApi`, `LlmProvider`, `LlmProxy`, `Mcp`, `AgentProxy`, `GraphQLApi`, `WebSubApi`, `WebBrokerApi`) |
| Handle | A slug, unique per organization, used in public paths |
| Origin | `control_plane` (authored in the CP) or `gateway_api` (imported from a gateway, read-only in the CP) |
| Build | An immutable definition snapshot, not tied to a gateway |
| Deployment | An immutable record of content sent to one gateway, already translated for its release |
| `status_desired` | The terminal target of an in-flight operation. It is what gateways sync against. |
| `performed_at` | The deploy or undeploy timestamp echoed back in acks; an optimistic concurrency token |
| ARCHIVED | A derived state: a deployment that no status row points to |
| Gateway token | The opaque `api-key` credential. Only its hash is stored, and at most two can be active. |
| Manifest | A gateway's self-report: version, functionality type and policies |
| Gateway version vs data version | The gateway's release vs the shape in which an artifact is stored. The translator bridges the two. |
| EventHub | The SQL outbox (`events`) and change marker (`gateway_states`), polled by every replica |
| EventDispatcher | Bridges the hub to the WebSocket connections on the same replica |
| Replay window | The 120 s of history replayed when a replica first subscribes for a gateway |
| `connection.ack` / `deployment.ack` | The server's greeting after the handshake / the gateway's result for a deploy or undeploy |
| Bottom-up import | A gateway pushing its own artifacts up to the CP |
| Skip paths | The built-in list of path prefixes that bypass JWT authentication and scope checks |
| Secret placeholder | `{{ secret "handle" }}`, resolved by the gateway or inlined by the CP for older gateways |

## Unverified items and notes

- Which endpoints the AI Workspace calls was not traced in the portal code.
- How the OSS build (without the eventgateway plugin) answers `/api/internal/v1/websub-apis/*` and `/webbroker-apis/*` was not checked.
- That SQLite limits a deployment to one replica is inferred, not documented.
- Statements marked **(gateway-side)** come from `gateway/gateway-controller` at the same commit.
- There is no `kb/` directory at the repository root.
- The `spec/` documents were not compared line by line against the code.
- Two code comments, in `eventhub.pollGatewayWithState` and `EventDispatcher.deliver`, misdescribe reconnect replay (section 5).
