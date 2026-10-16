# Gateway Controller: Onboarding Guide

**Audience:** engineers joining from the agent-manager team. agent-manager-service acts as a control plane for this same controller, so this guide focuses on what the controller expects from a control plane.
**Scope:** service and interface level. There is no code here. Paths point into `gateway/gateway-controller/` unless noted otherwise.
**Basis:** everything was checked against the code on `main` (October 2026). Anything marked *(unverified)* could not be confirmed in this repo.

---

## 1. Purpose and position

The **gateway-controller** is the brain of a gateway deployment. It holds the desired state of every artifact deployed on that gateway (APIs, LLM providers, agents, and so on). It turns that state into configuration for the **data plane** and serves it over **xDS**, Envoy's gRPC config-distribution protocol.

```
            CONTROL PLANE (one of)
   platform-api | agent-manager-service | on-prem APIM
        |  WebSocket (events down, deployment.ack up)
        |  HTTPS REST /api/internal/v1 (fetch artifacts, keys, subs; push artifacts up)
        v
 +--------------------------------------------------+
 |              gateway-controller                  |
 |  REST mgmt API :9090   admin :9092   metrics :9091|
 |  local DB (SQLite / Postgres / SQL Server)       |
 +--------------------------------------------------+
        | xDS gRPC :18000              | xDS gRPC :18001
        v                              v
   Router (Envoy)  --ext_proc-->  Policy Engine      } gateway-runtime container
   listeners/routes/clusters/     policy chains, API keys,
   secrets                        subscriptions, lazy resources
```

- **Data plane** = the `gateway-runtime` image. It contains Envoy (the "router"), the **policy engine** (an Envoy external-processing, or `ext_proc`, service that runs the policy chains) and a Python executor. Both Envoy and the policy engine are xDS clients of the controller.
- **Control plane (CP)** = whichever management service owns the artifacts. The controller does not care which product it is. It cares only that the CP speaks the WebSocket and REST contract in §4a.

**Two modes:**

| Mode | Trigger | Behaviour |
|---|---|---|
| **Standalone** | `controller.controlplane.token` is empty | The CP client never connects. Artifacts are managed only through the gateway's own REST API, the immutable-mode loader, or the Kubernetes operator. |
| **Connected** | token (and host) set | The controller opens a WebSocket to the CP, applies CP events, runs startup sync, and pushes gateway-created artifacts up to the CP. |

Connected mode has a sub-variant. If `https://<host>/internal/gateway/.well-known` returns a `gatewayPath`, the controller treats the CP as **on-prem APIM**. It then switches several behaviours off: deployment sync, secret sync, subscription sync, manifest push and the platform-API artifact push. It uses APIM bottom-up import instead. **A CP that is not APIM (for example agent-manager-service) must not answer that well-known URL with a 200 and a `gatewayPath`.**

---

## 2. How it runs

### 2.1 Entrypoint and configuration

- The binary is `cmd/controller/main.go`. It is started as `controller -config <file.toml> [-config <overlay.toml> ...]`. Files merge in order and the last value wins.
- Config is TOML only. Environment variables reach it through `{{ env "NAME" "default" }}` tokens inside the TOML. The shipped `gateway/configs/config.toml` uses names such as `APIP_GW_CONTROLLER_CONTROLPLANE_HOST`, `..._CONTROLPLANE_TOKEN`, `..._SERVER_GATEWAY_ID`, `..._STORAGE_TYPE`.
- Key config sections are `controller.server`, `controller.controlplane`, `controller.storage`, `controller.event_hub`, `controller.auth`, `controller.encryption`, `controller.policy_server`, `controller.admin_server`, `controller.metrics`, `router.*` and `immutable_gateway`.

### 2.2 Ports (defaults)

| Port | What | Notes |
|---|---|---|
| 9090 | Management REST API (`/api/management/v1`, plus deprecated unprefixed paths) | Replaced by 9093 when `server.tls.enabled` |
| 9093 | Management REST API over TLS | Off by default |
| 9092 | Admin server (`/api/admin/v1/health`, `config_dump`, `xds_sync_status`, optional pprof) | On by default. `allowed_ips` defaults to `["*"]`. Every route except health needs the `admin` role |
| 9091 | Prometheus metrics | Off by default |
| 18000 | Envoy xDS (ADS + LDS/RDS/CDS/EDS + SDS) | Plaintext by default; mTLS through `server.xds_tls` |
| 18001 | Policy-engine xDS (custom type URLs) | Plaintext by default; mTLS through `policy_server.tls` |

### 2.3 Local storage

- `storage.type` is one of `sqlite` (default, `./data/gateway.db`), `postgres` or `sqlserver`.
- SQLite creates its own schema from an embedded file and has a small built-in migration (v5 to v6). Postgres and SQL Server schemas must be applied separately, using `pkg/storage/gateway-controller-db.{postgres,sqlserver}.sql`, which are shipped under `db-scripts/` in the distribution.
- **Every table is partitioned by `gateway_id`**, which comes from `controller.server.gateway_id` (default `platform-gateway-id`). This is a *local* partition key. It is not the gateway UUID the CP sends in `connection.ack`.

### 2.4 Startup sequence

1. Load config, initialise metrics and logger. Warn if no auth is configured.
2. In immutable mode, delete the SQLite files so every boot starts clean.
3. Open storage. Open a **second** connection pool for the EventHub, then initialise the EventHub and register this `gateway_id`.
4. Load every artifact, LLM template and API key from the DB into in-memory stores.
5. Initialise the encryption providers (AES-GCM key files) and the secrets service.
6. Load policy definitions from disk. Mark them `managedBy` wso2 or organization using `build-manifest.yaml`.
7. **Hydrate** stored MCP and LLM artifacts into their derived RestApi form. Enforce the mTLS startup invariant.
8. Build the Envoy snapshot manager, cert store and SDS secrets. Publish client CAs.
9. Wire the **transformer registry** into the Envoy translator. The ordering here matters: Envoy and the policy engine must agree on route and cluster names.
10. Build the initial Envoy snapshot, then **start the xDS server on 18000**.
11. Build the API-key, policy and subscription snapshots, then **start the policy xDS server on 18001**.
12. Load LLM templates, build validators and the shared outbound HTTP client, then the deployment services.
13. **Create and start the CP client** (a no-op without a token). A failure here does not stop startup.
14. Create the REST API, Agent and immutable services, the auth middleware, and the **EventListener**.
15. Load immutable artifacts (if enabled) and publish the lazy-resource snapshot.
16. Start the admin server, the metrics server, and finally the management REST server.
17. Print the "API Platform Gateway Started" banner once both Envoy and the policy engine have connected.

### 2.5 Deployment shapes

| Shape | Where | Notes |
|---|---|---|
| Docker Compose | `gateway/docker-compose.yaml` | Two containers: `gateway-controller` and `gateway-runtime`. The runtime finds the controller through `GATEWAY_CONTROLLER_HOST` (default `gateway-controller`) and ports 18000/18001. |
| Helm | `kubernetes/helm/gateway-helm-chart` | Separate Deployment, Service, HPA and PDB for the controller and the runtime. SQLite uses a PVC. More than one controller replica requires Postgres or SQL Server. |
| Kubernetes operator | `kubernetes/gateway-operator` | Installs gateways through Helm and reconciles CRDs (RestApi, Agent, LlmProvider/Proxy/Template, Mcp, ApiKey, Subscription, SubscriptionPlan, Secret, Certificate). It does this by calling the controller's **management REST API**. It is not a control plane in the WebSocket sense. |

### 2.6 High availability (HA)

- Multiple replicas share **one database and one `gateway_id`**.
- **EventHub** (`common/eventhub`) is a DB-backed event log made of the `events` and `gateway_states` tables. Each write bumps a version row. Every replica polls (default every 3 s), with a 120 s replay window on its first poll. Events are kept for 1 h.
- **Every replica, including the one that did the write, applies changes through its own EventListener.** A write path only persists to the DB and publishes an event. In-memory state and xDS are updated when the listener reads that event back.
- Each replica keeps its **own WebSocket** to the CP. platform-api keeps a list of connections per gateway and broadcasts each event to all of them. See §5.5 for how the replicas avoid duplicate writes and duplicate acks.

---

## 3. Internal components

| Component (path) | What it owns |
|---|---|
| **controlplane** (`pkg/controlplane`) | The WebSocket client: connect, heartbeat, reconnect, event dispatch, `deployment.ack`, startup and reconnect sync, and the push of artifacts and the manifest to the CP. |
| **eventhub** (`common/eventhub`) | The DB-backed publish/poll bus for converging replicas. |
| **eventlistener** (`pkg/eventlistener`) | Consumes EventHub events. Reloads the entity from the DB, renders templates, updates in-memory stores and xDS snapshots. One processor per entity type. |
| **storage** (`pkg/storage`) | The `Storage` interface over SQLite/Postgres/SQL Server, plus in-memory stores: `ConfigStore` (artifacts), `APIKeyStore`, `LazyResourceStore`, `RuntimeConfigStore`. Owns the `deployed_at` staleness guard. |
| **service layer** | One deployment path per kind: `service/restapi` (RestApi), `service/agent` (Agent), `utils.LLMDeploymentService` (LlmProvider, LlmProxy, templates), `utils.MCPDeploymentService` (Mcp), and `utils.APIDeploymentService` (generic; GraphQLApi registers itself here). WebSubApi and WebBrokerApi come in through **event-gateway hooks** that are implemented in a separate event-gateway-controller binary. |
| **transform** (`pkg/transform`) | Turns a stored artifact into a `RuntimeDeployConfig` per kind (restapi, llm, agent, graphql). Both xDS paths share it. |
| **xds** (`pkg/xds`) | The Envoy translator, snapshot manager, SDS secret manager, and the gRPC server on 18000. |
| **policyxds** (`pkg/policyxds`) | Builds `PolicyChainConfig` and `RouteConfig` resources and runs the gRPC server on 18001. That server also serves the four resource types below. |
| **apikeyxds** | `APIKeyState` resources (hashed keys) built from the in-memory key store. |
| **subscriptionxds** | `SubscriptionState` resources (hashed subscription tokens and plans), read straight from the DB. |
| **lazyresourcexds** | `LazyResources`: LLM provider templates, provider-to-template mappings, client certificate authorities. |
| **secrets** + **encryption** | Encrypted secret storage (AES-GCM, versioned keys) and the target for upserts from CP secret sync. |
| **templateengine** | Renders `{{ secret "h" }}`, `{{ env "X" }}`, `default`, `required`, `lower`/`upper`/`trim`/`replace`, and `redact` inside artifact specs. |
| **immutable** | Loads artifacts from `immutable_gateway.artifacts_dir` at boot. Its middleware rejects POST/PUT/DELETE on the management API with 405. |
| **adminserver** | Health, `config_dump`, `xds_sync_status` and pprof, behind an IP allowlist plus auth with the `admin` role. |
| **workerpool** | An optional cap on concurrency for pushes from the data plane up to the control plane (DP->CP pushes; there is one pool for APIM and one for platform-API). By default it is unbounded, with one goroutine per task. |
| **certstore / clientca / gatewayidentity / certmetrics** | Upstream trust bundle, client-CA uploads for mTLS auth, gateway identity certificates, and certificate expiry metrics. |

---

## 4. Interfaces

### 4a. Northbound: the contract a control plane must implement

**Connection**
- The controller dials `wss://<host>/api/internal/v1/ws/gateways/connect`. With on-prem discovery it dials `wss://<host><gatewayPath>/ws/gateways/connect` instead. Both URLs are always TLS. `insecure_skip_verify` defaults to `true` in the shipped config.
- It authenticates with the header `api-key: <controlplane.token>`. REST calls use the same header.
- The **first message from the CP must be `connection.ack`** with `{type, gatewayId, connectionId, timestamp}`, and it must arrive within **5 s**. Otherwise the controller drops the connection and retries.
- **Heartbeat:** the CP sends WebSocket pings. The controller replies with a pong and records the time. A monitor checks every 5 s and reconnects if no ping has arrived for **35 s**. That figure is a 30 s server timeout plus 5 s grace.
- **Reconnect backoff:** exponential from `reconnect_initial` (1 s) up to `reconnect_max` (5 min), with ±25 % jitter.
- **Permanent failure:** HTTP 401, 403, 404, 409 or 422 on the dial, or on the manifest push, makes the controller call `os.Exit(1)`. The intent is that Kubernetes shows a CrashLoopBackOff instead of the controller retrying forever.
- Incoming messages are handled **one at a time on the read loop**. A slow handler, such as an artifact fetch, delays processing of the messages behind it.

**Events the controller handles** (JSON `{type, payload, timestamp, correlationId[, userId]}`)

| Event | Payload ID field | What the controller does | Ack? |
|---|---|---|---|
| `api.deployed` | `apiId`, `deploymentId`, `performedAt` | GET `/apis/{id}` (zip) → extract YAML → pre-fetch any referenced secrets → validate → upsert to DB with `deployed_at = performedAt` → publish EventHub event | `api` deploy success/failed. **None if stale** |
| `api.undeployed` | same | Find the local row (by UUID or `cp_artifact_id`). If `deploymentId` differs, reply failed with `DEPLOYMENT_ID_MISMATCH`. Otherwise set `desired_state=undeployed` through a guarded upsert (config, keys and policies are kept) | `api` undeploy. Success if the API is not found. **None if stale** |
| `api.deleted` | `apiId` | Delete the artifact, its API keys and its subscriptions, refresh subscription xDS, publish DELETE. If the API is unknown, clean up orphaned keys and subscriptions | No ack |
| `llmprovider.*`, `llmproxy.*` (deployed / undeployed / deleted) | `providerId` / `proxyId` | Same pattern. Fetches `/llm-providers/{id}` and `/llm-proxies/{id}` | `llmprovider` / `llmproxy` (deploy and undeploy only) |
| `mcpproxy.*` | `proxyId` | Same pattern. Fetches `/mcp-proxies/{id}` | `mcpproxy` |
| `agent.*` | **`proxyId`** (this field carries the agent ID) | Refuses an ID that is stored under a different kind. GET `/agents/{id}` must return `application/zip` holding **exactly one entry named `agent-{id}.yaml`**, and the ID must be a canonical UUID | resourceType **`Agent`**. Failure codes: `AGENT_ARTIFACT_FETCH_FAILED`, `AGENT_VALIDATION_FAILED`, `AGENT_CONFIG_RENDER_FAILED`, `AGENT_CONFLICT`, `GATEWAY_PROCESSING_ERROR`, `DEPLOYMENT_ID_MISMATCH` |
| `graphqlapi.*` | `apiId` | Same pattern. Fetches `/graphql-apis/{id}` | `graphqlapi` |
| `websub.*`, `websub.hmacsecret.*`, `webbroker.*` | n/a | Handed to event-gateway hooks. If no hooks are registered, the event is ignored | Done by the hook |
| `apikey.created` / `.updated` / `.revoked` | `apiId`, `uuid`, `name`/`keyName`, `apiKeyHashes` (a JSON string holding `{"sha256": ...}`), `expiresAt`/`expiresIn`, `issuer` | Creates, updates or revokes an "external" key (stored hash only). Revoke skips the creator check, because the CP has already authorised it | No ack |
| `subscription.created` / `.updated` / `.deleted` | `apiId`, `subscriptionId`, `subscriptionToken`, `subscriptionPlanId`, `status`, `applicationId` | Upserts or deletes the subscription. The token is hashed before storage. `apiId` is mapped to the local UUID | No ack |
| `subscriptionPlan.created` / `.updated` / `.deleted` | `planId`, `planName`, throttle fields, `status` | Upserts or deletes the plan | No ack |
| `application.updated` | `applicationId`, `applicationUuid`, `applicationName`, `applicationType`, `mappings[].apiKeyUuid` | Replaces that application's API-key mappings. Unknown key UUIDs are skipped | No ack |
| `connection.ack` (late copy) | n/a | Ignored | n/a |

**`deployment.ack` (controller to CP):** `{type:"deployment.ack", payload:{deploymentId, artifactId, resourceType, action: deploy|undeploy, status: success|failed, performedAt, errorCode?}}`.
- A success ack means **"persisted to the DB and published to EventHub"**. It does *not* mean Envoy is already serving the change. That happens a few seconds later, after the listener poll and the xDS push.
- **No ack is sent** in these cases: delete events; key, subscription, plan and application events; **stale events** (a newer `deployed_at` is already stored); anything applied through the sync path (§5); and any time `controlplane.host` is empty.

**REST calls the controller makes** (base `https://<host>/api/internal/v1`, header `api-key`)

| Call | When |
|---|---|
| `GET /apis/{id}`, `/llm-providers/{id}`, `/llm-proxies/{id}`, `/mcp-proxies/{id}`, `/agents/{id}`, `/graphql-apis/{id}` (zip) | Handling a `*.deployed` event |
| `GET /deployments` → `{deployments:[{artifactId, deploymentId, kind, state, deployedAt, etag}]}` | First connect: syncDeployments |
| `POST /deployments/fetch-batch` `{deploymentIds}` → **tar.gz**, one directory per deployment ID | syncDeployments, in batches of `sync_batch_size` (50) |
| `POST /artifacts/exists` `{artifactIds}` → the subset that exists | syncDeployments, before deleting orphans |
| `GET /secrets?includeValues=true` / `GET /secrets` / `GET /secrets/{handle}/value` | Secret sync on connect and reconnect; on-demand fetch of secrets referenced in a fetched YAML |
| `GET /subscription-plans` | Every connect |
| `GET /apis/{id}/subscriptions` | Every connect, once for each local RestApi |
| `GET /{apis,llm-providers,llm-proxies,agents,graphql-apis,websub-apis,webbroker-apis}/api-keys[?issuer=]` | Every connect: API-key bulk sync. Only `active` keys that carry a sha256 hash are kept |
| `POST /gateways/{gatewayId}/manifest` `{version, functionalityType, policies[]}` | Every connect. `gatewayId` here is the value from `connection.ack`. `wso2_apip_sys_*` policies are excluded |
| `POST /artifacts/import-gateway-artifacts` (multipart: `artifacts` zip holding `artifacts.json`, plus `total`) | Pushing gateway-created artifacts up to the CP, on each connect and after each local change |

### 4b. The controller's own management REST API

- The spec is `api/management-openapi.yaml` and the server code is generated with oapi-codegen. Resources are Kubernetes-shaped: `apiVersion: gateway.api-platform.wso2.com/v1`, `kind`, `metadata.name`, `spec`, plus a server-managed `status`. The `{id}` path parameter is `metadata.name`, the **handle**.
- **Resource families:** `rest-apis`, `graphql-apis`, `mcp-proxies`, `agents`, `llm-providers`, `llm-proxies`, `llm-provider-templates`. Each family has `api-keys` sub-resources, with `regenerate`, on REST, GraphQL, agents, LLM providers and LLM proxies. The other families are `subscriptions`, `subscription-plans`, `secrets`, `certificates` (plus `reload`) and `GET /policies`.
- **Auth:** HTTP Basic, from `controller.auth.basic.users` with bcrypt hashes, or IDP JWT (JWKS, roles claim and role mapping). Authorisation is a deny-by-default route-to-role map in `main.go` with three roles: `admin`, `developer`, `consumer`. Startup fails if basic auth is enabled with a user that has an empty username or password. If both methods are disabled, the API is open and the controller only logs a warning.

### 4c. Southbound: xDS to the data plane

| Server | Client | Resources |
|---|---|---|
| :18000 (go-control-plane ADS, node `router-node`) | Envoy | **Listeners** (HTTP 8080 and HTTPS 8443, with an ext_proc filter to the policy engine and a Lua filter), **Routes**, **Clusters** (upstreams, the policy-engine cluster, the access-log cluster), **Secrets** through SDS (listener certs, gateway identities, upstream trust) |
| :18001 (custom type URLs `api-platform.wso2.org/v1.*`) | Policy engine | `PolicyChainConfig`, `RouteConfig`, `APIKeyState`, `SubscriptionState`, `LazyResources` (plus event-gateway types) |

Both servers set message-size (16 MiB) and stream (1000) limits. When mTLS is on, each stream must pass a peer-identity allowlist check.

---

## 5. Sync and reconciliation

### 5.1 What runs when

| Step | First connect of the process | Reconnect |
|---|---|---|
| `syncSecrets` | Bulk fetch with values | Metadata only; fetches values only for secrets whose hash changed |
| `syncDeployments` | **Yes** (needs `deployment_sync_enabled` and a non-on-prem CP) | **No** |
| On-prem APIM bottom-up sync | When on-prem | When on-prem |
| Push gateway artifacts up to the CP (bottom-up push) | Yes (worker pool) | Yes |
| `syncSubscriptionPlans`, then `syncSubscriptionsForExistingAPIs` | Yes | Yes |
| `syncAPIKeysForExistingArtifacts` | Yes | Yes |
| Manifest push | Yes | Yes |

On first connect the steps run in the order shown, one after another, in a single goroutine. Order matters because subscriptions reference plans, and both reference APIs.

### 5.2 How syncDeployments diffs

The remote list from `GET /deployments` is compared against local rows that have **`origin = control_plane`**. Rows created on the gateway itself are excluded. For each remote entry:

1. **Missing locally:** fetch it, but only if the remote state is `deployed`.
2. **`deploymentId` differs:** fetch it.
3. **Same `deploymentId`, different state:** status-only update. The guarded upsert sets the new state and publishes an EventHub UPDATE.
4. **Same ID and state, but `deployedAt` differs or is null locally:** fetch it.
5. **Local CP row missing from the remote list:** orphan candidate.

**Orphans** are checked first with `POST /artifacts/exists`. An artifact that still exists on the CP but has no deployment is **kept**, so its keys and subscriptions survive. If the existence check *fails*, **no deletions happen at all** (the controller fails safe). Deletions run in reverse dependency order: agents, then MCP, REST and GraphQL, then LLM proxies, then LLM providers.

**Fetches** run in dependency order: providers, proxies, REST, MCP, agents, GraphQL. Each batch of up to 50 goes through `fetch-batch`. Each item is applied with the CP's `deployedAt` and `etag` as the correlation ID. Every step logs errors and moves on; one bad item does not abort the sync. **No acks are sent from this path.**

### 5.3 The `deployed_at` staleness guard

`UpsertConfig` writes the `artifacts` row only if the stored `deployed_at` is NULL or **strictly older** than the incoming value. Otherwise it returns "not affected". Callers treat that as a stale event: they do not write, do not publish, and do not ack. Every CP deploy, undeploy and sync status update goes through this guard. On update it keeps `cp_artifact_id` and `created_at` but resets `cp_sync_status` and `cp_sync_info`.

### 5.4 Other idempotency guards

- Undeploy is refused (`DEPLOYMENT_ID_MISMATCH`) when the event's `deploymentId` is not the current one.
- Undeploy of an unknown artifact acks success.
- Delete of an unknown artifact performs orphan cleanup of keys and subscriptions.
- CP events address artifacts by CP UUID. The controller resolves that to the local row through `cp_artifact_id` for artifacts that originated on the gateway, so a CP redeploy updates the row in place.
- Plan, subscription and key bulk syncs also **delete** local rows that are missing from the CP's list.

### 5.5 HA interplay

All replicas receive the same event. All of them fetch the artifact. Only the first upsert passes the guard, because the others carry an equal `deployed_at` and the guard needs strictly newer. So exactly one replica publishes and acks. If every replica sees the event as stale, the CP sends no ack and has to rely on its own timeout.

---

## 6. Data model

All tables are keyed by `(gateway_id, …)`. Schemas live in `pkg/storage/gateway-controller-db{,.postgres,.sqlserver}.sql`.

| Table | Key columns | Meaning |
|---|---|---|
| **artifacts** | PK `(gateway_id, uuid)`. Unique `(kind, handle)` and `(kind, display_name, version)` | The shared header for every artifact. `kind` (RestApi, LlmProvider, LlmProxy, LlmProviderTemplate, Mcp, Agent, GraphQLApi, WebSubApi, WebBrokerApi). `desired_state` (deployed or undeployed). `deployment_id` (the CP's deployment). `deployed_at` (the staleness clock). `origin` (`control_plane` or `gateway_api`). `cp_sync_status` (pending, success or failed) and `cp_sync_info` (the error, or APIM `{id, revision}`) for pushes up to the CP. `cp_artifact_id` (the CP's UUID for an artifact that originated on the gateway). `data_version` |
| rest_apis, llm_providers, mcp_proxies, graphql_apis | `(gateway_id, uuid)`, FK to artifacts, cascade delete | `configuration` holds the spec as a JSON text column |
| llm_proxies | as above, plus `provider_uuid`, FK to llm_providers **RESTRICT** | A proxy cannot outlive its provider |
| agents | as above, plus `signed_public_card`, `signed_protected_card` | Agent spec plus signed agent cards |
| llm_provider_templates | `(gateway_id, uuid)`, unique handle | Templates, including the defaults shipped with the image |
| api_keys | PK `(gateway_id, api_key)`. Unique `uuid` and `(artifact_uuid, name)` | `api_key` holds a **hash** (sha256). Other columns: `masked_api_key`, `status` (active, revoked or expired), `source` (local or external), `issuer`, `expires_at`, `created_by` |
| subscription_plans | `(gateway_id, uuid)`, unique `plan_name` | Throttle limit and unit, `stop_on_quota_reach`, status |
| subscriptions | `(gateway_id, uuid)`. Unique `(api_id, subscription_token_hash)`. FK to plan | `api_id` is the local artifact UUID (no FK to artifacts). `application_id`, billing IDs, status |
| applications | `(gateway_id, application_uuid)` | Application metadata from `application.updated` |
| application_api_keys | `(gateway_id, application_uuid, api_key_id)`. FKs cascade | Maps an application to its API keys |
| secrets | `(gateway_id, handle)` | Encrypted `ciphertext`, read through `{{ secret }}` |
| certificates | `(gateway_id, uuid)`, unique name | PEM, `usage`, `role`, `match_json`, encrypted private key |
| gateway_states, events | `gateway_id`; `(gateway_id, event_id)` | EventHub version cursor and event log |
| webhook_secrets | n/a | Used only by the event-gateway extension, not by the core schema |

```
artifacts 1──1 {rest_apis | llm_providers | llm_proxies | mcp_proxies | agents | graphql_apis}
llm_providers 1──* llm_proxies            (RESTRICT)
artifacts.uuid 1──* api_keys.artifact_uuid (logical, no FK)
artifacts.uuid 1──* subscriptions.api_id   (logical, no FK)
subscription_plans 1──* subscriptions
applications *──* api_keys  via application_api_keys
gateway_states 1──* events
```

---

## 7. Key flows

### 7.1 Startup, standalone
Steps 1–17 of §2.4. Every artifact comes from the DB (or the immutable directory), and Envoy and the policy engine get their snapshots as soon as they connect. Nothing is fetched from outside.

### 7.2 Startup, connected
Same as standalone. In addition, at step 13 the CP client starts its connection loop: try the well-known URL, dial, wait for `connection.ack`, then start the first-connect sync (§5.1), the manifest push and the heartbeat monitor. Envoy is already serving the state from the DB while the sync runs.

### 7.3 Deploy event end to end
1. CP sends `agent.deployed` (or `api.deployed`, and so on) with `{id, deploymentId, performedAt}`.
2. The read loop dispatches it. The controller resolves the local ID and checks the kind.
3. `GET /agents/{id}` returns a zip, which is checked and turned into YAML.
4. Any `{{ secret }}` handles not yet cached locally are fetched from `/secrets/{h}/value` and stored encrypted.
5. The kind service parses, renders, validates and checks for conflicts, then calls `UpsertConfig`. **Stale** stops here, with no ack.
6. The service publishes an EventHub event (`AGENT`, CREATE or UPDATE).
7. `deployment.ack` success goes back to the CP.
8. On each replica, within one poll interval (default 3 s), the EventListener reloads the row and renders it into the in-memory `ConfigStore`.
9. Two snapshots follow. The **Envoy snapshot** goes to listeners, routes and clusters on :18000. The **policy snapshot** (chain and route config) goes to the policy engine on :18001.

### 7.4 Artifact created on the gateway's REST API, pushed up to the CP
1. `POST /api/management/v1/rest-apis` passes auth and authz. The row is written with `origin=gateway_api`, `cp_sync_status=pending`, and an EventHub event is published.
2. If the controller is connected, `deployment_sync_enabled` is on and the CP is not on-prem, a task goes into the platform-API push pool.
3. That task polls the in-memory store every 500 ms, for up to 60 s, until the listener has applied the deployment.
4. It then pushes through `import-gateway-artifacts`, retrying up to 5 times with backoff from 1 s to 30 s.
5. On success it records `cp_sync_status=success` and `cp_artifact_id`. On failure it records `failed` and the error. Rows that are still pending or failed get pushed again on the next connect, because that push covers *every* gateway-origin artifact.
6. Deleting a gateway-origin artifact pushes a copy with `desired_state=undeployed`.

The CP client's own internal services are never wired as pushers, so artifacts that came from the CP are not echoed back. With an on-prem APIM CP, step 4 is an APIM import or revision undeploy instead, retried 3 times.

### 7.5 Reconnect after a network drop
1. The read fails, or the heartbeat goes 35 s without a ping. The connection is closed and the state becomes Reconnecting.
2. Backoff, then dial and `connection.ack` again.
3. Run the **reconnect set**: secrets (incremental), APIM bottom-up sync, artifact push, plans, subscriptions, keys, manifest.
4. **Deployments are not re-synced.** A deploy, undeploy or delete event the CP sent during the outage is not recovered (see §8).

### 7.6 Controller restart
The process reloads everything from the DB, so Envoy gets the last known state immediately. Then it connects. Because the process is new, the first-connect path runs, including a **full `syncDeployments`**. A restart is the only reliable way to recover deployment drift.

---

## 8. Failure behaviour and known gaps

| Situation | Behaviour |
|---|---|
| CP unreachable at boot | Serves from the DB and retries with backoff indefinitely |
| CP returns 401/403/404/409/422 on dial or manifest push | `os.Exit(1)` (by design, so the restart loop is visible) |
| Artifact fetch, parse or validation fails | `deployment.ack` failed with a code. The local state is unchanged |
| DB write fails | Ack failed (`GATEWAY_PROCESSING_ERROR`) |
| EventHub publish fails | Logged only. That replica's xDS will not update until a later event or a restart |
| Push to the CP fails | Row is marked `failed` and retried on the next connect |
| `/artifacts/exists` fails during sync | All orphan deletions are skipped |

**Known gaps**
- **No deployment sync on reconnect.** The code comment says it is "deferred; see proposal doc" *(proposal doc not located)*.
- **No periodic resync.** `controlplane.polling_interval` (15 min) is validated but nothing reads it.
- **Missed CP events are lost** unless the CP replays them on reconnect *(CP-side replay unverified)*.
- The subscription bulk sync covers **RestApi only**. Subscriptions for Agents, LLM artifacts and GraphQL APIs are not bulk-reconciled.
- Secret sync only upserts. Secrets deleted on the CP stay in the local store.
- WebSub and WebBroker kinds are not handled by `syncDeployments`. The fetch and delete switches have no case for them.
- Acks mean "persisted", not "live in Envoy". Stale duplicates get no ack, so the CP must handle the timeout itself.
- The `gatewayId` in `connection.ack` is stored and used (for the manifest URL) but never compared with local configuration.
- Security-relevant defaults: both xDS servers are plaintext unless TLS is configured. The admin server's `allowed_ips` is `["*"]` (auth still applies). CP `insecure_skip_verify` defaults to `true`.
- Events are handled serially on the WebSocket read loop. A long handler could in principle overlap the 35 s heartbeat window *(effect not tested)*.

---

## 9. Directory map: `gateway-controller/pkg`

| Dir | One line |
|---|---|
| adminserver | Admin HTTP server: health, config_dump, xds_sync_status, pprof |
| api | Generated management and admin servers, handlers, handlerkit (shared CRUD and push helpers), middleware |
| apikeyxds | API-key state manager and xDS snapshot |
| certmetrics | Periodic certificate expiry metrics |
| certstore | Upstream trust bundle and stored certificates for SDS |
| clientca | Validation of client-CA uploads for mTLS auth |
| config | TOML loading, defaults, validation; validators per kind (API, LLM, MCP, agent, policy, mTLS, upstream TLS) |
| constants | Shared constants (gateway ID default, timeouts, regexes) |
| controlplane | WebSocket client, events, sync, secret sync, push, event-gateway hooks |
| encryption | Provider manager and AES-GCM provider |
| eventlistener | EventHub consumer, one processor per entity type |
| gatewayidentity | Inspection of gateway identity certificate uploads |
| immutable | Immutable mode: SQLite reset, artifact loader, read-only middleware |
| lazyresourcexds | Lazy resource store and snapshot (templates, CAs) |
| logger, metrics, version | slog setup, Prometheus metrics and server, build info |
| models | StoredConfig, API keys, subscriptions, plans, applications, secrets, certs |
| policy, resolver | Derivation of policy chains; resolution of policy versions |
| policyxds | Policy snapshot, combined cache, gRPC server on 18001 |
| secrets | Secret service (CRUD and platform upsert) |
| service | `agent`, `restapi` deployment services |
| storage | DB backends, schema files, in-memory stores, EventHub topics |
| subscriptionxds | Subscription snapshot (reads the DB) |
| templateengine | Spec template rendering and its functions |
| testutil | Test PKI helpers |
| tlsauth | Peer-identity allowlist for xDS mTLS |
| transform | Transformers from stored artifact to RuntimeDeployConfig, per kind |
| utils | Deployment services (API, LLM, MCP, GraphQL), CP REST client (`api_utils.go`), API keys, on-prem APIM helpers, push helpers |
| workerpool | Optional bounded worker pool |
| xds | Envoy translator, snapshot, SDS, gRPC server on 18000 |

---

## 10. Glossary

| Term | Meaning |
|---|---|
| **Artifact** | Any deployable resource (RestApi, Agent, LlmProvider, and so on). It is one row in `artifacts` plus one row in its kind table |
| **Handle** | `metadata.name`. Unique per kind and used as `{id}` in the management API |
| **Origin** | `control_plane` (came from a CP event or sync) or `gateway_api` (created locally) |
| **Desired state** | `deployed` or `undeployed`. Undeploy keeps the config, keys and subscriptions |
| **deploymentId / deployed_at** | The CP's deployment identity and timestamp. `deployed_at` is the staleness clock |
| **Bottom-up sync / DP->CP push** | Pushing gateway-origin artifacts up to the control plane |
| **cp_artifact_id** | The CP's UUID for an artifact that originated on the gateway |
| **EventHub** | DB-backed event log that keeps controller replicas converged |
| **EventListener** | The per-replica consumer that applies EventHub events to memory and xDS |
| **xDS / ADS / SDS** | Envoy's discovery protocols (aggregated stream; secret discovery) |
| **Policy engine** | ext_proc service in gateway-runtime that runs the policy chains |
| **ext_proc** | Envoy's external-processing filter, used to call the policy engine |
| **Lazy resources** | Policy-engine data that is not tied to a single route: LLM templates, provider-to-template mappings, client CAs |
| **Manifest** | The list of policies and the version this gateway pushes to the CP on connect |
| **On-prem mode** | Mode entered when the CP answers the well-known URL with a `gatewayPath` (WSO2 APIM) |
| **Immutable mode** | Artifacts are loaded from disk at boot and the management API is read-only |
| **Stale event** | An event whose `deployed_at` is not newer than the stored value. Ignored and not acked |
