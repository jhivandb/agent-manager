# Agent Identity & MCP Tool Authorization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** `docs/superpowers/specs/2026-07-02-agent-identity-mcp-tool-authz-design.md`

> **Amendments (2026-07-02 adversarial review):** where this plan conflicts with the spec, this plan wins. The review changed four design points: (1) identities are provisioned when the agent *reaches* an environment (deploy/promote), not eagerly at binding create; (2) Thunder agent lookup/recovery is by `component_uid`+`environment_uid` attributes, never by name; (3) `allowed_tools` is stored **per environment mapping**, not per config; (4) secret rotation triggers a pod rollout for internal agents.

**Goal:** Give each deployed agent a verifiable Thunder-backed identity (per component × environment) and enforce per-tool MCP restrictions by rendering `mcp-auth` + `mcp-authz` policies into the agent's own `McpProxyMapping` deployment.

**Architecture:** A new `AgentIdentityProvisioner` (modeled on `publisher_credential_provisioner.go`) creates Thunder agents via new `/agents` client methods, stores secrets in OpenBao via `secretmanagersvc`, and records them in a new `agent_identities` table. MCP bindings gain an `auth_mode` column on `agent_configurations` and a per-environment `allowed_tools` column on `env_agent_mcp_mapping`; `buildAgentMCPConfigProxy` materializes an `AgentAuth` block into the derived proxy config, and `buildMCPProxyDeploymentYAML` renders it as `mcp-auth`/`mcp-authz` policies instead of `api-key-auth`. Identity provisioning follows the deployment pipeline: at binding create only environments the agent is already deployed in get identities; a new `PromoteAgent` hook provisions the target env's identity, injects credentials, and re-pushes that env's mapping; unprovisioned mappings render deny-all. New API endpoints (spec-first) expose allowlists, capability refresh, and identity list/rotate; console and CLI ride on the regenerated clients.

**Tech Stack:** Go (agent-manager-service), gormigrate migrations, moq mocks, openapi-generator (`make spec`), oapi-codegen (`make amctl-gen-client`), cobra CLI, React/TS console (oxygen-ui + TanStack Query).

## Global Constraints

- All service work in `agent-manager-service/`; spec-first: edit `agent-manager-service/docs/api_v1_openapi.yaml` **before** writing handlers, then `make spec`. Follow the `add-api-resource` skill when doing Phase 3 tasks and the `add-service-unit-test` skill for all Go unit tests (CI lints test files: nilnil, goheader, exhaustruct, errorlint).
- Migrations are **up-only**, next number is **026**; never modify migrations 001–025. Register in `db_migrations/migration_list.go` and bump `latestVersion` to `26`.
- Auth-mode strings exactly: `apiKey`, `agentIdentity`. Sentinel scope exactly: `amp:never-issued`. Policy names exactly: `mcp-auth`, `mcp-authz`, version `v1`. New permission scope exactly: `amp:agent:manage-identity`.
- `allowed_tools` semantics (stored **per environment mapping** on `env_agent_mcp_mapping`): `null` = allow all (default), `[]` = deny all, list = allow exactly these. Never render an unenforced allowlist: if enforcement is impossible, fail the request — never silently degrade. An `agentIdentity` mapping whose environment has no provisioned identity yet renders **deny-all** (wildcard sentinel rule) until deploy/promote provisions it.
- Thunder agents are looked up by attributes (`component_uid` + `environment_uid`), never by name — names embed the environment name and break on rename. The name is display-only.
- New env vars injected into agent pods (identity-level, once per agent): `AMP_AGENT_CLIENT_ID`, `AMP_AGENT_CLIENT_SECRET`, `AMP_AGENT_TOKEN_URL`.
- Copy each file's license header from a sibling file (goheader lint fails otherwise).
- Commit after every task. Git conventions come from the `am-ship` skill at execution time.

## Resolved design decisions (repo-grounded)

These resolve the open points in the design doc against the actual codebase; they are binding for the tasks below.

1. **Storage of `allowed_tools`/`auth_mode`:** `agent_configurations` has *no* JSONB config column (config is relational). Migration 026 adds `auth_mode TEXT NOT NULL DEFAULT 'apiKey'` to `agent_configurations` (uniform per binding — a binding is either key-based or identity-based everywhere) and `allowed_tools JSONB NULL` to `env_agent_mcp_mapping` (`models/agent_configuration.go:94-112`) — **per environment**, because the console binds a possibly different proxy per environment (`AddMCPServer.Component.tsx:78-81` `serverByEnv`) and an allowlist only makes sense against that environment's tool snapshot.
2. **Render seam:** per-agent mappings are rendered by deriving a `models.MCPProxy` in `buildAgentMCPConfigProxy` (`services/agent_configuration_service.go:3769`) and passing it through the shared `buildMCPProxyDeploymentYAML` (`services/mcp_proxy_deployment.go:300`). We add `AgentAuth *MCPAgentAuthConfig` to `models.MCPProxyConfig` (JSONB, persisted on the mapping) and branch in the builder.
3. **No issuer auto-registration.** Thunder is already seeded on gateways as the **system** identity provider `"ThunderKeyManager"` (`models.SystemIdentityProviderNames`, `models/agent.go:81`), written by gateway bootstrap. `UpsertIdentityProvider` only handles custom IdPs and the OSS `GatewayConfigApplier` is nil. v1 therefore only *verifies* preconditions per gateway: (a) the gateway manifest advertises `mcp-auth` and `mcp-authz` (walk `models.Gateway.Manifest` like `extractGatewayPolicyManifestItems`, `services/mcp_proxy_service.go:474`), and (b) a `gateway_identity_providers` row named `ThunderKeyManager` exists for that gateway. If either fails: default requests fall back to `apiKey` with a warning; explicit `agentIdentity`/`allowedTools` requests fail 400.
4. **Thunder `/agents` contract** (pinned against `thunder-id/thunderid` `api/agent.yaml`, fetched 2026-07-02):
   - `POST /agents` requires `ouId`, `type`, `name`. `type` must be a registered agent-type schema; Thunder restricts agent types to a **single `default` schema per deployment** (bootstrap-provisioned). We use `type: "default"`.
   - OAuth profile: `inboundAuthConfig: [{type: "oauth2", config: {grantTypes: ["client_credentials"], tokenEndpointAuthMethod: "client_secret_basic", token: {accessToken: {validityPeriod: <seconds>}}}}]`. `clientId` auto-generated when omitted. `clientSecret` is returned **only** in POST/PUT responses (`AgentCompleteResponse.inboundAuthConfig[].config.clientSecret`), never on GET.
   - **No regenerate-secret endpoint for agents.** Rotation = `GET /agents/{id}` then `PUT /agents/{id}` full replacement with a CP-generated `clientSecret` (crypto/rand, 32 bytes, base64url) supplied in `inboundAuthConfig[].config.clientSecret`.
   - **Lookup is attribute-based.** Idempotency/conflict recovery finds existing Thunder agents by `attributes.component_uid` + `attributes.environment_uid` (filter grammar if Thunder's `filterParam` supports attribute paths; otherwise paginated list + client-side attribute match, mirroring `findApp` in `client.go:395`). Never by name: the name embeds the environment name and silently breaks on env rename, creating duplicates.
   - `attributes` are validated against the `default` agent-type schema (required fields enforced). The provisioner therefore has an `ensureAgentType` step: `GET /agent-types`; if none exists, `POST /agent-types` with `name: "default"`, root `ouId`, and a schema declaring our audit fields (`component_uid`, `environment_uid`, `project_uid`, `organization`) as optional strings. If a schema exists with required fields we don't supply, `PUT /agent-types/{id}` to make them optional (this Thunder instance is owned by the AMP deployment). **Implementation-time check:** inspect what `wso2-amp-thunder-extension` bootstraps and adjust.
   - OU resolution: existing `GetOUIDByHandle(ctx, handle)` (`clients/thundersvc/identity_client.go:1146`), handle = org name.
5. **Thunder client shape:** new interface `ThunderAgentClient` in a new file `clients/thundersvc/agent_client.go`, implemented on the existing `thunderClient` struct via the shared `doRequest` helper. This adds the **first moq mock in `thundersvc`** — follow `clients/secretmanagersvc/client.go:246` convention (`-out ../clientmocks/... -pkg clientmocks`).
6. **Identity lifecycle follows the deployment pipeline** (agents start in the lowest environment and are promoted upward; environments can be added and removed):
   - **Binding create/update (`createMCPConfig`/`updateMCPConfig`), internal agents:** `EnsureIdentity` only for environments where the agent is *currently deployed* — a ReleaseBinding exists (new exported OC-client check wrapping `findReleaseBindingForEnv`, `clients/openchoreosvc/client/deployments.go:289`) — or the pipeline's first environment (its env vars bootstrap via `UpdateComponentEnvVars`, so they survive first deploy). Other mapped environments get **no** identity and their mappings render deny-all (fail closed; nothing is running there anyway). Rationale: `UpdateReleaseBindingEnvVars` is warn-only against a missing ReleaseBinding (`agent_configuration_service.go:1054-1056`), so eager provisioning would strand credentials that never reach the pod.
   - **Promotion (`PromoteAgent`, `agent_manager.go:2818`):** new hook (Task 7b) — for each `agentIdentity`-mode MCP config with a mapping for the target env: `EnsureIdentity`, append `AMP_AGENT_*` env vars to the promote overrides (the existing `tgtSystemEnvVars` mechanism at `agent_manager.go:2884-2890` is LLM-only and rebuilds from variable rows that won't carry these — the hook injects directly), then re-derive + re-push the target env's mapping so the deny-all placeholder is replaced with the real `sub`-pinned rule.
   - **External agents:** provisioned eagerly for all mapped environments at binding create — the platform never deploys/promotes them, and binding-create is the only moment credentials can be returned one-time.
   - **Env mapping removed / environment deleted:** removing an env mapping from a binding revokes that env's identity iff no other `agentIdentity`-mode config of the same component still maps it (`DeleteIdentity`: Thunder delete + secret delete + row status `revoked`). `environmentService.DeleteEnvironment` (`environment_service.go:224`) gains a best-effort `DeleteAllForEnvironment(envUUID)` next to the existing gateway-mapping cleanup (`:276`). Agent delete: `deleteAgentLLMConfigurations` (`services/agent_manager.go:2096`) gains `provisioner.DeleteAllForComponent`.
   - **Rotation restarts the pod.** `AMP_AGENT_CLIENT_SECRET` is an env var from a SecretKeyRef — captured at container start; after rotation the running pod holds a dead secret and its next token request 401s. Rotate (internal agents) therefore triggers a rollout via a new exported OC-client method wrapping `setRestartedAt` (`clients/openchoreosvc/client/deployments.go:319`, the same mechanism `UpdateReleaseBindingTraitConfigs` uses). External agents get response/UX copy telling the operator to update their runtime's credentials.
7. **"Golden tests" → table-driven YAML tests.** The repo has no golden-file infra; the convention is `yaml.Unmarshal` + struct assertions (`services/mcp_proxy_deployment_test.go`). We extend that file.
8. **`refresh-capabilities`** implements the `proxyId` refresh that `FetchServerInfo` explicitly does not support yet (`services/mcp_proxy_service.go:597`): re-runs the MCP handshake against the stored upstream, persists the snapshot, then re-pushes the proxy and all derived mappings (restricted set changes with the snapshot). Permission: `rbac.MCPServerUpdate` (matches `PUT /mcp-proxies/{proxyId}`).
9. **Token URL** delivered to agents = `cfg.IDP.TokenURL` (`config/config.go:176`), which is the in-cluster thunder-extension token endpoint (see `Makefile:280`).
10. **RBAC:** new `AgentManageIdentity Permission = "agent:manage-identity"` in the agent block of `rbac/permissions.go:131-145`; grant to `RoleAdmin` in `rbac/predefined_roles.go`. Identity list uses existing `rbac.AgentRead`; allowlist editing rides existing `rbac.AgentUpdate`.
11. **Console hosts:** tool picker in `configure-agent/src/AddMCPServer.Component.tsx` (+ read-only in `ViewMCPServer.Component.tsx`), modeled on the selection logic of `mcp-proxies/src/subComponents/MCPProxyRewriteTab.tsx`. The picker is **per environment tab** (state keyed by env name, fed by that env's selected proxy snapshot) since `allowed_tools` is per env mapping. "Refresh tools" uses the new refresh-capabilities endpoint (not `fetch-server-info`, which requires re-supplying auth). Four UX rules from the review:
    - **Warnings/effective mode survive navigation.** `AddMCPServer`'s `onSuccess` navigates away immediately (`AddMCPServer.Component.tsx:224-239`), so `warnings`, effective `authMode`, and external-agent identity credentials travel via the same navigation-state mechanism as `authInfoByEnv` and render on `ViewMCPServer`.
    - **External agents see their one-time client secret in the create flow** (threaded through navigation state) — the Security page cannot be their only path.
    - **Identity panel is NOT gated on `hasActiveDeployment`/`securityEnabled`.** `Security.Component.tsx:305-323` early-returns "Agent is not deployed" and `:347-351` suppresses content when API-key security is off; the identity section must render outside both gates (external agents have no deployments at all). The page is retitled **"Security"** with two labeled sections: "Inbound API Keys" (existing content, keeps its gates) and "Agent Identity" (outbound credential the agent uses to call MCP servers).
    - **The env-var-names table is mode-aware.** The Add form gains an "Authentication" select (`Auto` (default) / `API key` / `Agent identity`) mapped to the request `authMode`; the `apikey` row (`AddMCPServer.Component.tsx:497-551`) is hidden for `Agent identity`, annotated "only applies if the binding resolves to API-key auth" for `Auto`, and fixed read-only `AMP_AGENT_*` rows are shown for `Agent identity`/`Auto`.
12. **CLI:** flags go on `agent mcp set` (`cli/pkg/cmd/agent/mcp/set.go`); `--allowed-tools` requires `--env` (allowlist is per environment; read-merge-write updates only that env's mapping). New `agent identity` command group mirrors the `mcp` package layout; `rotate-secret` prints whether a restart was triggered.
13. **Rollout mechanism:** rotation and promote-time mapping refresh reuse the ReleaseBinding `restartedAt` pattern — expose `setRestartedAt` (`clients/openchoreosvc/client/deployments.go:319`) as an exported `RestartComponent(ctx, orgName, componentName, envName) error`, and `findReleaseBindingForEnv` (`:289`) as `HasReleaseBinding(ctx, orgName, componentName, envName) (bool, error)` for the deployed-in-env gate.

---

# Phase 1 — Identity foundation (service)

### Task 1: Migration 026 + models

**Files:**
- Create: `agent-manager-service/db_migrations/026_add_agent_identities.go`
- Modify: `agent-manager-service/db_migrations/migration_list.go` (append `migration026`, bump `latestVersion` to 26)
- Create: `agent-manager-service/models/agent_identity.go`
- Modify: `agent-manager-service/models/agent_configuration.go` (add `AuthMode` to `AgentConfiguration`, `AllowedTools` to `EnvAgentMCPMapping`)
- Modify: `agent-manager-service/models/constants.go` (auth-mode constants)

**Interfaces:**
- Produces: `models.AgentIdentity` (table `agent_identities`), `models.AgentConfiguration.AuthMode string`, `models.EnvAgentMCPMapping.AllowedTools *[]string`, constants `models.MCPAuthModeAPIKey = "apiKey"`, `models.MCPAuthModeAgentIdentity = "agentIdentity"`, `models.AgentIdentityStatusActive = "active"`, `models.AgentIdentityStatusRevoked = "revoked"`.

- [ ] **Step 1: Write the migration.** Open `025_add_agent_oauth_config.go` first and copy its header + structure exactly. Then:

```go
// 026_add_agent_identities.go (body of the Migrate func)
var migration026 = migration{
	ID: 26,
	Migrate: func(tx *gorm.DB) error {
		if err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS agent_identities (
				uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				organization_name TEXT NOT NULL,
				component_uuid UUID NOT NULL,
				environment_uuid UUID NOT NULL,
				thunder_agent_id TEXT NOT NULL,
				client_id TEXT NOT NULL,
				secret_ref TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CONSTRAINT uq_agent_identities_component_env UNIQUE (component_uuid, environment_uuid)
			)`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_identities_org
			ON agent_identities (organization_name)`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
			ALTER TABLE agent_configurations
				ADD COLUMN IF NOT EXISTS auth_mode TEXT NOT NULL DEFAULT 'apiKey'`).Error; err != nil {
			return err
		}
		return tx.Exec(`
			ALTER TABLE env_agent_mcp_mapping
				ADD COLUMN IF NOT EXISTS allowed_tools JSONB`).Error
	},
}
```

In `migration_list.go`: append `migration026` to the slice, change `const latestVersion = 25` → `26`.

- [ ] **Step 2: Write the model.**

```go
// models/agent_identity.go
// AgentIdentity records the Thunder-backed identity provisioned for one
// (agent component × environment). The client secret itself lives in the
// secret store; SecretRef is the SecretReference CR name.
type AgentIdentity struct {
	UUID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"uuid"`
	OrganizationName string    `gorm:"not null" json:"organizationName"`
	ComponentUUID    uuid.UUID `gorm:"type:uuid;not null" json:"componentUuid"`
	EnvironmentUUID  uuid.UUID `gorm:"type:uuid;not null" json:"environmentUuid"`
	ThunderAgentID   string    `gorm:"not null" json:"thunderAgentId"`
	ClientID         string    `gorm:"not null" json:"clientId"`
	SecretRef        string    `gorm:"not null;default:''" json:"-"`
	Status           string    `gorm:"not null;default:'active'" json:"status"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func (AgentIdentity) TableName() string { return "agent_identities" }
```

Constants in `models/constants.go` (place near `AgentConfigTypeMCP`, line ~22):

```go
// MCP binding auth modes
const (
	MCPAuthModeAPIKey        = "apiKey"
	MCPAuthModeAgentIdentity = "agentIdentity"
)

// Agent identity statuses
const (
	AgentIdentityStatusActive  = "active"
	AgentIdentityStatusRevoked = "revoked"
)
```

Field on `AgentConfiguration` (`models/agent_configuration.go`, inside the struct at lines 53-73):

```go
	// AuthMode: models.MCPAuthModeAPIKey | models.MCPAuthModeAgentIdentity. Uniform per binding.
	AuthMode string `gorm:"column:auth_mode;not null;default:apiKey" json:"authMode,omitempty"`
```

Field on `EnvAgentMCPMapping` (`models/agent_configuration.go`, inside the struct at lines 94-107):

```go
	// AllowedTools: nil = allow all tools (default); empty = deny all; list = allow exactly
	// these. Per environment — each env mapping may point at a different proxy/tool set.
	AllowedTools *[]string `gorm:"column:allowed_tools;type:jsonb;serializer:json" json:"allowedTools,omitempty"`
```

- [ ] **Step 3: Build and run migrations against the local DB.**

Run: `cd agent-manager-service && go build ./...` — expect clean.
Run the service's migration path the way the repo does in dev (check `make` targets / README; if a local Postgres from `deployments/docker-compose.yml` is up, start the service once and confirm log line for migration 26, and `\d agent_identities` shows the table).

- [ ] **Step 4: Commit.** `git add ... && git commit -m "feat: add agent_identities table and MCP binding auth columns (migration 026)"`

---

### Task 2: AgentIdentityRepository

**Files:**
- Create: `agent-manager-service/repositories/agent_identity_repository.go`
- Create (generated): `agent-manager-service/repositories/repomocks/agent_identity_repository_mock.go`
- Test: `agent-manager-service/repositories/agent_identity_repository_test.go` (only if sibling repositories have DB-less tests — check `agent_configuration_repository` for precedent; if repo tests require a live DB and none exist for siblings, skip the test file)

**Interfaces:**
- Consumes: `models.AgentIdentity` (Task 1).
- Produces:

```go
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/agent_identity_repository_mock.go . AgentIdentityRepository:AgentIdentityRepositoryMock
type AgentIdentityRepository interface {
	GetByComponentEnv(componentUUID, environmentUUID uuid.UUID) (*models.AgentIdentity, error)
	ListByComponent(componentUUID uuid.UUID) ([]models.AgentIdentity, error)
	ListByEnvironment(environmentUUID uuid.UUID) ([]models.AgentIdentity, error)
	Upsert(identity *models.AgentIdentity) error
	// MarkRevoked flips status to models.AgentIdentityStatusRevoked (row kept for audit).
	MarkRevoked(componentUUID, environmentUUID uuid.UUID) error
	DeleteAllByComponent(componentUUID uuid.UUID) error
}
```

(Match the exact receiver/constructor/ctx conventions of `repositories/org_publisher_credential_repository.go` — if that file threads `context.Context`, do the same here.)

- [ ] **Step 1: Write the repository.** Constructor `NewAgentIdentityRepository(db *gorm.DB) AgentIdentityRepository`. `GetByComponentEnv` returns `gorm.ErrRecordNotFound` untranslated (callers use `errors.Is`). `Upsert` uses `clause.OnConflict{Columns: component_uuid+environment_uuid, DoUpdates: ...}` or the Get-then-Save pattern — copy whichever `org_publisher_credential_repository.Upsert` uses.
- [ ] **Step 2: Generate the mock.** Run: `cd agent-manager-service && make codegen` (runs `go generate ./...`). Expect `repomocks/agent_identity_repository_mock.go` created.
- [ ] **Step 3: Build + lint.** Run: `go build ./... && make lint` (or the repo's lint target). Expect clean.
- [ ] **Step 4: Commit.** `git commit -m "feat: add agent identity repository"`

---

### Task 3: Thunder `/agents` client

**Files:**
- Create: `agent-manager-service/clients/thundersvc/agent_client.go`
- Create: `agent-manager-service/clients/thundersvc/agent_types.go` (DTOs)
- Create (generated): `agent-manager-service/clients/clientmocks/thunder_agent_client_fake.go`
- Test: `agent-manager-service/clients/thundersvc/agent_client_test.go` (httptest-based; check how/if `client.go` paths are tested — if no client tests exist, still add httptest coverage for create/update/delete happy paths and 404 mapping, using `httptest.NewServer`)

**Interfaces:**
- Produces (package `thundersvc`):

```go
//go:generate moq -rm -fmt goimports -out ../clientmocks/thunder_agent_client_fake.go -pkg clientmocks . ThunderAgentClient

// ThunderAgentClient manages Thunder first-class agent identities (/agents).
type ThunderAgentClient interface {
	EnsureAgentType(ctx context.Context, rootOUID string) error
	CreateAgent(ctx context.Context, req CreateThunderAgentRequest) (*ThunderAgentComplete, error)
	GetAgent(ctx context.Context, id string) (*ThunderAgent, error)
	// FindAgentByAttributes locates an agent by component_uid + environment_uid
	// attributes. Returns nil, nil when absent. NEVER match by name — names embed
	// the environment name and break on rename.
	FindAgentByAttributes(ctx context.Context, componentUID, environmentUID string) (*ThunderAgent, error)
	UpdateAgent(ctx context.Context, id string, req UpdateThunderAgentRequest) (*ThunderAgentComplete, error)
	DeleteAgent(ctx context.Context, id string) error
	GetOUIDByHandle(ctx context.Context, handle string) (string, error) // already on thunderClient; re-expose
}

func NewThunderAgentClient(baseURL, clientID, clientSecret string) ThunderAgentClient
```

DTOs in `agent_types.go` (shapes pinned to thunder `api/agent.yaml`):

```go
type ThunderAgentOAuthConfig struct {
	ClientID                string                    `json:"clientId,omitempty"`
	ClientSecret            string                    `json:"clientSecret,omitempty"` // write-only on update; returned on create/update
	GrantTypes              []string                  `json:"grantTypes,omitempty"`
	TokenEndpointAuthMethod string                    `json:"tokenEndpointAuthMethod,omitempty"`
	Token                   *ThunderAgentTokenConfig  `json:"token,omitempty"`
}

type ThunderAgentTokenConfig struct {
	AccessToken *ThunderAccessTokenConfig `json:"accessToken,omitempty"`
}

type ThunderAccessTokenConfig struct {
	ValidityPeriod int `json:"validityPeriod,omitempty"` // seconds
}

type ThunderAgentInboundAuth struct {
	Type   string                   `json:"type"` // "oauth2"
	Config *ThunderAgentOAuthConfig `json:"config,omitempty"`
}

type CreateThunderAgentRequest struct {
	OUID              string                    `json:"ouId"`
	Type              string                    `json:"type"` // "default"
	Name              string                    `json:"name"`
	Description       string                    `json:"description,omitempty"`
	Attributes        map[string]any            `json:"attributes,omitempty"`
	InboundAuthConfig []ThunderAgentInboundAuth `json:"inboundAuthConfig,omitempty"`
}

type UpdateThunderAgentRequest struct { // PUT is full-replacement; mirror Create
	OUID              string                    `json:"ouId,omitempty"`
	Type              string                    `json:"type"`
	Name              string                    `json:"name"`
	Description       string                    `json:"description,omitempty"`
	Attributes        map[string]any            `json:"attributes,omitempty"`
	InboundAuthConfig []ThunderAgentInboundAuth `json:"inboundAuthConfig,omitempty"`
}

type ThunderAgent struct { // GET/list shape (no secret)
	ID                string                    `json:"id"`
	OUID              string                    `json:"ouId"`
	Type              string                    `json:"type"`
	Name              string                    `json:"name"`
	Description       string                    `json:"description,omitempty"`
	ClientID          string                    `json:"clientId,omitempty"`
	Attributes        map[string]any            `json:"attributes,omitempty"`
	InboundAuthConfig []ThunderAgentInboundAuth `json:"inboundAuthConfig,omitempty"`
}

type ThunderAgentComplete = ThunderAgent // POST/PUT response carries clientSecret inside InboundAuthConfig[].Config
```

- [ ] **Step 1: Write a failing httptest test** for `CreateAgent` (server asserts method POST, path `/agents`, bearer token present; returns a canned `AgentCompleteResponse` with `clientSecret`) and `FindAgentByAttributes` (try `GET /agents?filter=attributes.component_uid+eq+"<uid>"` — check the spec's `filterParam` syntax in the downloaded copy; if attribute paths aren't filterable, implement as paginated list + client-side match on both attributes, mirroring `findApp` in `client.go:395`; assert a name-mismatched-but-attribute-matched agent IS found — the rename-safety property).

Run: `go test ./clients/thundersvc/ -run TestAgentClient -v` — expect FAIL (undefined symbols).

- [ ] **Step 2: Implement.** All methods on the existing `*thunderClient` struct via `getSystemToken` + `doRequest` (the `identity_client.go` style — it already maps 404 → `*NotFoundError`). `EnsureAgentType`: `GET /agent-types`; if the list is non-empty, verify the `default` schema's required fields are limited to ones we supply, else `PUT /agent-types/{id}` relaxing them; if empty, `POST /agent-types` with:

```json
{"name": "default", "ouId": "<rootOUID>", "allowSelfRegistration": false,
 "systemAttributes": {"display": "name"},
 "schema": {
   "component_uid":   {"type": "string", "required": false},
   "environment_uid": {"type": "string", "required": false},
   "project_uid":     {"type": "string", "required": false},
   "organization":    {"type": "string", "required": false}}}
```

`FindAgentByAttributes` returns `(nil, nil)` when no match. `DeleteAgent` treats 404 as success (`IsNotFound`).

- [ ] **Step 3: Run tests.** `go test ./clients/thundersvc/ -v` — expect PASS.
- [ ] **Step 4: Generate mock + build.** `make codegen && go build ./...` — expect `clientmocks/thunder_agent_client_fake.go` created.
- [ ] **Step 5: Commit.** `git commit -m "feat: add Thunder /agents client with agent-type bootstrap"`

---

### Task 4: AgentIdentityProvisioner + wiring

**Files:**
- Create: `agent-manager-service/services/agent_identity_provisioner.go`
- Test: `agent-manager-service/services/agent_identity_provisioner_unit_test.go`
- Modify: `agent-manager-service/wiring/wire.go` (new provider), regenerate `wiring/wire_gen.go` via `make codegen`

**Interfaces:**
- Consumes: `ThunderAgentClient` (Task 3), `AgentIdentityRepository` (Task 2), `secretmanagersvc.SecretManagementClient`, `config.Config`.
- Produces (package `services`):

```go
// AgentIdentityInfo is returned by Ensure/Rotate. ClientSecret is non-empty
// only when the identity was just created or rotated (one-time reveal).
type AgentIdentityInfo struct {
	Identity      *models.AgentIdentity
	ClientSecret  string
	SecretRefName string // SecretReference CR name for pod SecretKeyRef injection
	TokenURL      string
}

type EnsureAgentIdentityRequest struct {
	OrgName, ProjectName, AgentName, EnvironmentName string
	ComponentUUID, EnvironmentUUID, ProjectUUID      uuid.UUID
}

type AgentIdentityProvisioner interface {
	Enabled() bool // false when Thunder is not configured
	EnsureIdentity(ctx context.Context, req EnsureAgentIdentityRequest) (*AgentIdentityInfo, error)
	RotateSecret(ctx context.Context, orgName string, componentUUID, environmentUUID uuid.UUID) (*AgentIdentityInfo, error)
	ListIdentities(ctx context.Context, componentUUID uuid.UUID) ([]models.AgentIdentity, error)
	// DeleteIdentity revokes ONE (component, environment) identity: Thunder agent
	// deleted (404 tolerated), secret deleted, row marked revoked. Used when an env
	// mapping is removed and no other agentIdentity-mode config still maps that env.
	DeleteIdentity(ctx context.Context, orgName string, componentUUID, environmentUUID uuid.UUID) error
	// DeleteAllForEnvironment revokes every identity in an environment (environment
	// deletion). Best-effort per row: Thunder/secret failures logged, not fatal.
	DeleteAllForEnvironment(ctx context.Context, environmentUUID uuid.UUID) error
	DeleteAllForComponent(ctx context.Context, orgName string, componentUUID uuid.UUID) error
}

func NewAgentIdentityProvisioner(cfg config.Config, logger *slog.Logger,
	agentClient thundersvc.ThunderAgentClient, // nil in non-Thunder mode
	secretClient secretmanagersvc.SecretManagementClient,
	repo repositories.AgentIdentityRepository) AgentIdentityProvisioner
```

Follow `publisher_credential_provisioner.go` patterns exactly: dual impl (`disabledAgentIdentityProvisioner` returning `Enabled() == false` and a sentinel `ErrAgentIdentityNotEnabled` from every method, chosen when `cfg.Thunder.BaseURL == ""`), `singleflight.Group` keyed `"ensure:"+componentUUID+":"+environmentUUID`, `fmt.Errorf("...: %w", err)` wrapping.

- [ ] **Step 1: Write failing unit tests** (`add-service-unit-test` skill: no build tags, moq mocks). Cover:
  - `EnsureIdentity` idempotency: repo hit → returns existing, zero Thunder calls.
  - Fresh provision: OU lookup → `EnsureAgentType` → `CreateAgent` → `CreateSecret` → `Upsert`; assert the Thunder request has `Type == "default"`, `GrantTypes == ["client_credentials"]`, attributes carrying the four audit fields; returned info has non-empty `ClientSecret` and `SecretRefName`.
  - Partial-failure recovery: repo miss + `CreateAgent` conflict → `FindAgentByAttributes(component_uid, environment_uid)` → `UpdateAgent` (secret reset) → proceeds. Include a case where the recovered Thunder agent has a *different name* than the generator would produce (post-rename) — recovery still succeeds.
  - `RotateSecret`: `GetAgent` → `UpdateAgent` with new CP-generated secret → `PatchSecret`; returns new secret.
  - `DeleteIdentity`: Thunder delete + secret delete + `MarkRevoked`; Thunder 404 tolerated.
  - `DeleteAllForEnvironment`: iterates `ListByEnvironment`; per-row Thunder failure logged, remaining rows still processed.
  - `DeleteAllForComponent`: Thunder delete failure is logged, not fatal; secret + row still removed.
  - Disabled mode: every method returns `ErrAgentIdentityNotEnabled`.

Run: `go test ./services/ -run TestAgentIdentityProvisioner -v` — expect FAIL.

- [ ] **Step 2: Implement.** Key details:
  - Thunder agent name: `fmt.Sprintf("amp-agent-%s-%s-%s", req.AgentName, req.EnvironmentName, req.ComponentUUID.String()[:8])` — **display-only**; uniqueness and idempotency come from `FindAgentByAttributes` recovery on `component_uid`+`environment_uid`, never from the name.
  - Secret storage: `secretmanagersvc.SecretLocation{OrgName, ProjectName, AgentName, EnvironmentName, EntityName: "agent-identity"}`; data map keys `"client-id"` and `"client-secret"`; `CreateSecret` returns the `SecretRefName` stored in `agent_identities.secret_ref`.
  - Secret generation for rotate: `crypto/rand` 32 bytes → `base64.RawURLEncoding`.
  - `TokenURL` = `cfg.IDP.TokenURL`.
  - Attributes: `{"component_uid": ..., "environment_uid": ..., "project_uid": ..., "organization": req.OrgName}`.
- [ ] **Step 3: Run tests.** `go test ./services/ -run TestAgentIdentityProvisioner -v` — expect PASS.
- [ ] **Step 4: Wire.** Add `ProvideAgentIdentityProvisioner` to `wiring/wire.go` (mirror `ProvidePublisherProvisioner`, `wire.go:297-301`): construct `thundersvc.NewThunderAgentClient(cfg.Thunder.BaseURL, cfg.Thunder.ClientID, cfg.Thunder.ClientSecret)` when BaseURL non-empty, else pass nil. Run `make codegen`, then `go build ./...`.
- [ ] **Step 5: Commit.** `git commit -m "feat: add AgentIdentityProvisioner backed by Thunder agents"`

---

# Phase 2 — Policy rendering & binding flow

### Task 5: `AgentAuth` config + mcp-auth/mcp-authz rendering

**Files:**
- Modify: `agent-manager-service/models/mcp_proxy.go` (add `AgentAuth` to `MCPProxyConfig` at line ~75, new struct)
- Modify: `agent-manager-service/services/mcp_proxy_deployment.go` (constants; new append funcs; branch in `buildMCPProxyDeploymentYAML` at lines 320-325)
- Test: `agent-manager-service/services/mcp_proxy_deployment_test.go`

**Interfaces:**
- Produces:

```go
// models/mcp_proxy.go
// MCPAgentAuthConfig switches a per-agent MCP mapping from API-key auth to
// agent-identity (JWT) auth with per-tool authorization.
// ThunderAgentSub == "" means the environment's identity is not provisioned yet
// (agent hasn't reached this environment in the pipeline): render DENY-ALL —
// a single wildcard rule requiring the sentinel scope. Fail closed, never open.
type MCPAgentAuthConfig struct {
	IssuerName      string    `json:"issuerName"`               // gateway IdP name, e.g. "ThunderKeyManager"
	ThunderAgentSub string    `json:"thunderAgentSub"`          // token sub claim pinned by the wildcard rule; "" = deny-all
	AllowedTools    *[]string `json:"allowedTools,omitempty"`   // nil = allow all; [] = deny all
}
// on MCPProxyConfig:
	AgentAuth *MCPAgentAuthConfig `json:"agentAuth,omitempty"`
```

```go
// services/mcp_proxy_deployment.go
const (
	mcpAuthPolicyName     = "mcp-auth"
	mcpAuthzPolicyName    = "mcp-authz"
	mcpAgentPolicyVersion = "v1"
	mcpAuthzSentinelScope = "amp:never-issued"
)
func appendMCPAgentAuthPolicies(policies []models.MCPPolicy, cfg *models.MCPAgentAuthConfig, caps *models.MCPProxyCapabilities) []models.MCPPolicy
func restrictedMCPTools(allowed *[]string, caps *models.MCPProxyCapabilities) []string
```

- [ ] **Step 1: Write failing table-driven tests** in `mcp_proxy_deployment_test.go` (reuse `findMCPPolicy` helper, line 30). Cases for `TestGenerateMCPProxyDeploymentYAML_AgentIdentity`:
  1. `AgentAuth` set, `AllowedTools == nil` → YAML has `mcp-auth` (params.issuers `["ThunderKeyManager"]`) and `mcp-authz` with exactly one rule: `{name: "*", requiredClaims: {sub: "<id>"}}`; **no** `api-key-auth` policy even though `Security.APIKey.Enabled` is true on the same proxy.
  2. `AllowedTools = ["get_weather"]`, snapshot tools `[get_weather, delete_db, send_mail]` → wildcard rule + sentinel rules for `delete_db` and `send_mail` (sorted), each `requiredScopes: ["amp:never-issued"]`; no rule for `get_weather`.
  3. `AllowedTools = []` (empty, non-nil) → sentinel rule for every snapshot tool.
  4. `AllowedTools` contains a tool absent from the snapshot ("unknown tool") → no sentinel for it, no error (kept-but-unenforceable until refresh, per design).
  5. `AgentAuth == nil` → existing behavior untouched (api-key path — assert existing test still passes).
  6. Snapshot nil/empty with non-nil `AllowedTools` → only the wildcard rule (nothing to restrict).
  7. `ThunderAgentSub == ""` (identity not provisioned — env not reached yet) → `mcp-auth` present, `mcp-authz` has exactly one rule `{name: "*", requiredScopes: ["amp:never-issued"]}` (deny-all), regardless of `AllowedTools`.

Run: `go test ./services/ -run TestGenerateMCPProxyDeploymentYAML_AgentIdentity -v` — expect FAIL.

- [ ] **Step 2: Implement.**

```go
func restrictedMCPTools(allowed *[]string, caps *models.MCPProxyCapabilities) []string {
	if allowed == nil || caps == nil || caps.Tools == nil {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(*allowed))
	for _, t := range *allowed {
		allowedSet[t] = struct{}{}
	}
	var restricted []string
	for _, tool := range *caps.Tools {
		name, _ := tool["name"].(string)
		if name == "" {
			continue
		}
		if _, ok := allowedSet[name]; !ok {
			restricted = append(restricted, name)
		}
	}
	sort.Strings(restricted)
	return restricted
}

func appendMCPAgentAuthPolicies(policies []models.MCPPolicy, cfg *models.MCPAgentAuthConfig, caps *models.MCPProxyCapabilities) []models.MCPPolicy {
	if cfg == nil {
		return policies
	}
	policies = append(policies, models.MCPPolicy{
		Name:    mcpAuthPolicyName,
		Version: mcpAgentPolicyVersion,
		Params:  map[string]interface{}{"issuers": []string{cfg.IssuerName}},
	})
	var rules []map[string]interface{}
	if cfg.ThunderAgentSub == "" {
		// Identity not provisioned yet (environment not reached in the pipeline):
		// deny everything until deploy/promote provisions it and re-pushes.
		rules = []map[string]interface{}{{
			"name":           "*",
			"requiredScopes": []string{mcpAuthzSentinelScope},
		}}
	} else {
		rules = []map[string]interface{}{{
			"name":           "*",
			"requiredClaims": map[string]interface{}{"sub": cfg.ThunderAgentSub},
		}}
		for _, tool := range restrictedMCPTools(cfg.AllowedTools, caps) {
			rules = append(rules, map[string]interface{}{
				"name":           tool,
				"requiredScopes": []string{mcpAuthzSentinelScope},
			})
		}
	}
	policies = append(policies, models.MCPPolicy{
		Name:    mcpAuthzPolicyName,
		Version: mcpAgentPolicyVersion,
		Params:  map[string]interface{}{"tools": rules},
	})
	return policies
}
```

Branch in `buildMCPProxyDeploymentYAML` (replacing the unconditional api-key call at line ~320):

```go
	var policies []models.MCPPolicy
	var err error
	if proxy.Configuration != nil && proxy.Configuration.AgentAuth != nil {
		policies = appendMCPAgentAuthPolicies(proxy.Configuration.Policies,
			proxy.Configuration.AgentAuth, proxy.Configuration.Capabilities)
	} else {
		policies, err = appendMCPAPIKeyAuthPolicy(proxy.Configuration.Policies, proxy.Configuration.Security)
		if err != nil { return nil, err }
	}
```

(Adapt to the file's actual nil-handling of `Configuration` — read lines 300-330 first. Exact `mcp-auth`/`mcp-authz` param key names — `issuers`, `tools`, `requiredScopes`, `requiredClaims` — come from the design doc's gateway-team verification; before merging, double-check them against a gateway's pushed policy manifest in `gateways.manifest` on any live environment.)

- [ ] **Step 3: Run all deployment tests.** `go test ./services/ -run TestGenerateMCPProxyDeploymentYAML -v` and `go test ./services/ -run TestAppendMCP -v` — expect PASS including pre-existing cases.
- [ ] **Step 4: Commit.** `git commit -m "feat: render mcp-auth/mcp-authz policies for agent-identity MCP mappings"`

---

### Task 6: Auth-mode resolution (gateway support check)

**Files:**
- Modify: `agent-manager-service/services/agent_configuration_service.go` (new helper + service deps)
- Test: `agent-manager-service/services/agent_configuration_service_unit_test.go` (or the file where existing unit tests for this service live — find with `ls agent-manager-service/services/*agent_configuration*test*`)

**Interfaces:**
- Consumes: `AgentIdentityProvisioner.Enabled()` (Task 4); `models.Gateway.Manifest`; gateway IdP lookup (`platformGatewayService.ListIdentityProvidersByGateway`, `services/platform_gateway_service.go:848` — inject the service or its repo, matching how `agentConfigurationService` already reaches gateway data).
- Produces:

```go
// resolveMCPAuthMode decides the effective auth mode for an MCP binding.
// requestedMode "" means "not specified" (default selection applies).
// allowedToolsRequested is true when ANY env mapping in the request carries a
// non-nil allowedTools (allowlists are per environment; the mode is per binding).
// Returns the effective mode plus human-readable warnings for the API response.
func (s *agentConfigurationService) resolveMCPAuthMode(ctx context.Context,
	requestedMode string, allowedToolsRequested bool,
	gateways []models.Gateway) (mode string, warnings []string, err error)

// gatewaySupportsAgentIdentity reports whether ONE gateway can enforce
// agent-identity auth: manifest advertises mcp-auth AND mcp-authz, and the
// Thunder system IdP is registered on it.
func (s *agentConfigurationService) gatewaySupportsAgentIdentity(ctx context.Context, gw models.Gateway) (bool, string)
```

Decision table (all gateways in the binding's deployment set must support it):

| requested | provisioner enabled + all gateways support | result |
|---|---|---|
| `""` | yes | `agentIdentity` |
| `""` | no | `apiKey` + warning (also error if `allowedToolsRequested`) |
| `apiKey` | — | `apiKey`; error if `allowedToolsRequested` (unenforceable) |
| `agentIdentity` | yes | `agentIdentity` |
| `agentIdentity` | no | 400 error naming the failing gateway + reason |

- [ ] **Step 1: Write failing tests** covering every row above plus: manifest advertises only `mcp-auth` (not `mcp-authz`) → unsupported with reason string containing `mcp-authz`; ThunderKeyManager IdP row missing → unsupported with reason naming the IdP. Use `repomocks`/`clientmocks` and a hand-built `models.Gateway{Manifest: ...}` — copy a real manifest shape from `extractGatewayPolicyManifestItems`'s test or from `mcp_proxy_service.go:474` reading logic.

Run: `go test ./services/ -run TestResolveMCPAuthMode -v` — expect FAIL.

- [ ] **Step 2: Implement.** Reuse the manifest-walking logic: extract policy name/version pairs the way `extractGatewayPolicyManifestItems` does (refactor it to a shared helper if it's method-bound). IdP check: list identity providers for the gateway and look for `models.SystemIdentityProviderNames` membership (i.e. `ThunderKeyManager`).
- [ ] **Step 3: Run tests.** Expect PASS.
- [ ] **Step 4: Commit.** `git commit -m "feat: resolve MCP binding auth mode from gateway capabilities"`

---

### Task 7: Binding flow integration (create/update/delete + env vars + external response)

This is the largest task; it stitches Tasks 1-6 into `createMCPConfig`/`updateMCPConfig`.

**Files:**
- Modify: `agent-manager-service/clients/openchoreosvc/client/deployments.go`: new exported `HasReleaseBinding(ctx context.Context, orgName, componentName, envName string) (bool, error)` wrapping `findReleaseBindingForEnv` (line 289; the client's not-found error → `false, nil`); add to the client interface (grep `PromoteComponent(` under `clients/openchoreosvc/` for the interface file) + `make codegen` for mocks.
- Modify: `agent-manager-service/services/agent_configuration_service.go`:
  - struct + `NewAgentConfigurationService` (lines 90-110, 372-392): add `agentIdentityProvisioner AgentIdentityProvisioner` dep; rewire in `wiring/wire.go` + `make codegen`.
  - `createMCPConfig` (lines 842-1069), `updateMCPConfig`, `buildAgentMCPConfigProxy` (lines 3769-3807), `buildMCPEnvVars` (lines 229-250) area.
- Modify: `agent-manager-service/models/agent_configuration_dto.go`: add `AuthMode string` to `CreateAgentModelConfigRequest`/`UpdateAgentModelConfigRequest`/`AgentModelConfigResponse`; add `AllowedTools *[]string` to the **per-env mapping** request/response structs (the ones keyed by env name in `EnvMappings`); add `AgentIdentityCredentials *AgentIdentityCredentialInfo` (fields `ClientID`, `ClientSecret`, `TokenURL`) next to `LLMProxyInfo` for external-agent responses.
- Modify: `agent-manager-service/services/agent_manager.go` `deleteAgentLLMConfigurations` (line 2096): add identity teardown.
- Test: `agent-manager-service/services/agent_configuration_service_unit_test.go` (extend), `agent-manager-service/services/mcp_proxy_deployment_test.go` (mapping derivation case)

**Interfaces:**
- Consumes: everything from Tasks 1-6.
- Produces: `buildAgentMCPConfigProxy` gains an `agentAuth *models.MCPAgentAuthConfig` parameter (or reads it from the config row + identity repo — match how the function currently receives data; it must work from **both** call paths: config create/update AND `RedeployMCPMappingsForSourceProxy` at line 3825).

- [ ] **Step 1: Write failing tests** for the new behavior:
  - `createMCPConfig` with effective mode `agentIdentity`, internal agent deployed only in the first env, binding maps first + second env: `EnsureIdentity` called **once** (first env only — the deployed-env gate via `ocClient.HasReleaseBinding` / first-env rule from decision 6); first env's derived mapping proxy has `Configuration.AgentAuth` populated (`IssuerName == "ThunderKeyManager"`, `ThunderAgentSub == identity.ThunderAgentID`, `AllowedTools` from **that env's mapping row**); second env's mapping has `AgentAuth` with `ThunderAgentSub == ""` (deny-all placeholder); **no** API key is minted (no `createMCPMappingAPIKey` broadcast, no `EnsureAndBind` API-key binding — check which of those remain necessary: `EnsureAndBind` may still be needed for gateway app binding; read lines 964-1051 and decide, encode the decision in the test).
  - Internal agent env vars (provisioned env only): `UpdateReleaseBindingEnvVars` receives `<PREFIX>_URL` (unchanged) plus `AMP_AGENT_CLIENT_ID` (plain value), `AMP_AGENT_TOKEN_URL` (plain), `AMP_AGENT_CLIENT_SECRET` (`ValueFrom.SecretKeyRef{Name: identityInfo.SecretRefName, Key: "client-secret"}`). No `<PREFIX>_API_KEY` template is created for `agentIdentity` bindings (adjust `buildMCPMappingEnvironmentVariables` so the `apikey` template is only emitted for `apiKey` mode). No identity env vars are pushed for the unprovisioned env.
  - External agent: `EnsureIdentity` called for **every** mapped env (eager — the platform never promotes external agents); response contains per-env `AgentIdentityCredentials{ClientID, ClientSecret (one-time), TokenURL}` and no API key.
  - `updateMCPConfig` changing an env mapping's `AllowedTools`: persists on that mapping row and re-pushes that env's mapping (assert `deployMCPProxyToGateway`/`deployMCPMapping` called).
  - `updateMCPConfig` removing an env mapping: `DeleteIdentity` called for that env when no other `agentIdentity`-mode config of the component maps it; NOT called when another config still does.
  - Mode `apiKey` (default fallback): existing behavior byte-identical (regression cases already exist — they must stay green).
  - Agent delete: `deleteAgentLLMConfigurations` calls `DeleteAllForComponent` (best-effort: error logged, not returned).

Run: `go test ./services/ -run TestCreateMCPConfig -v` (and the file's existing test names) — expect FAIL on new cases.

- [ ] **Step 2: Implement**, in this order:
  1. DTO fields + `convertCreateAgentModelConfigRequest`/`convertUpdateAgentModelConfigRequest`/`convertAgentModelConfigResponse` passthrough (`controllers/agent_configuration_controller.go:585+`) — note controller compiles only after Task 8's `make spec`; keep model-side converters ready and gate controller edits to Task 8 if the generated spec structs don't exist yet.
  2. In `createMCPConfig`: after resolving the source proxy + gateways, call `resolveMCPAuthMode`; persist `AuthMode` on the `AgentConfiguration` row and each env's `AllowedTools` on its `EnvAgentMCPMapping` row; branch per mode around the existing API-key mint/secret/env-var block (lines 964-1062). Provisioning gate (internal agents): `EnsureIdentity` only when `ocClient.HasReleaseBinding(env)` is true or env is the pipeline's first (`client.FindFirstEnvironment` is already computed at line ~905); external agents: always.
  3. `buildAgentMCPConfigProxy`: set `derived.Configuration.AgentAuth` when the config row's `AuthMode == models.MCPAuthModeAgentIdentity`, with `AllowedTools` from the mapping row and `ThunderAgentSub` from `agentIdentityRepo.GetByComponentEnv` (the mapping's env) — `""` when no active identity row exists (deny-all placeholder, Task 5 case 7). Since `RedeployMCPMappingsForSourceProxy` re-derives from the config + mapping rows, allowlist changes, capability refreshes, and promote-time provisioning all flow through automatically.
  4. New env-var builder:

```go
func buildAgentIdentityEnvVars(info *AgentIdentityInfo) []client.EnvVar {
	return []client.EnvVar{
		{Key: "AMP_AGENT_CLIENT_ID", Value: info.Identity.ClientID},
		{Key: "AMP_AGENT_TOKEN_URL", Value: info.TokenURL},
		{Key: "AMP_AGENT_CLIENT_SECRET", ValueFrom: &client.EnvVarSource{
			SecretKeyRef: &client.SecretKeySelector{Name: info.SecretRefName, Key: "client-secret"},
		}},
	}
}
```

(Match the exact `client.EnvVar` field names used in `buildMCPEnvVars`, lines 229-250.) Inject alongside the URL var via the existing `UpdateReleaseBindingEnvVars`/`UpdateComponentEnvVars` calls (lines 1052-1062).
  5. In `updateMCPConfig`'s env-mapping-removal path: before deleting the mapping row, query whether any other config of the same component with `AuthMode == agentIdentity` has a mapping for that env (`envMCPMappingRepo` join on `agent_configurations`); if not, `provisioner.DeleteIdentity(ctx, orgName, componentUUID, envUUID)` (best-effort: warn-log on failure, don't block the update).
  6. `deleteAgentLLMConfigurations`: after `aiApplicationService.DeleteAllByAgent` (line 2114), `if err := s.agentIdentityProvisioner.DeleteAllForComponent(ctx, orgName, componentUUID); err != nil { s.logger.Warn(...) }`.

- [ ] **Step 3: Run the full service test package.** `go test ./services/ -v` — expect PASS, zero regressions.
- [ ] **Step 4: Commit.** `git commit -m "feat: wire agent identity auth mode through MCP binding lifecycle"`

---

### Task 7b: Promotion & environment lifecycle hooks

Identities follow the pipeline: this task provisions on promote, revokes on environment delete, and adds the `RestartComponent` OC-client plumbing that Task 10's rotate endpoint consumes (`HasReleaseBinding` was added in Task 7).

**Files:**
- Modify: `agent-manager-service/clients/openchoreosvc/client/deployments.go` (exported `RestartComponent` wrapping `setRestartedAt`, line 319) + the client interface file where `PromoteComponent` etc. are declared (grep `PromoteComponent(` under `clients/openchoreosvc/`), regenerate the OC client mock via `make codegen`
- Modify: `agent-manager-service/services/agent_configuration_service.go` (new method `EnsureMCPIdentitiesForEnvironment` on the service + its interface at lines 60-80)
- Modify: `agent-manager-service/services/agent_manager.go` `PromoteAgent` (after the `tgtSystemEnvVars` block, lines 2884-2890)
- Modify: `agent-manager-service/services/environment_service.go` `DeleteEnvironment` (line 224; provisioner dep added via `wiring/wire.go` + `make codegen`)
- Test: `agent-manager-service/services/agent_configuration_service_unit_test.go`, the agent-manager unit test file (find with `ls agent-manager-service/services/*agent_manager*test*`)

**Interfaces:**
- Consumes: `AgentIdentityProvisioner.EnsureIdentity`/`.DeleteAllForEnvironment` (Task 4), `buildAgentIdentityEnvVars` (Task 7), `buildAgentMCPConfigProxy` (Task 7).
- Produces:

```go
// clients/openchoreosvc/client (exported wrapper over setRestartedAt):
RestartComponent(ctx context.Context, orgName, componentName, envName string) error

// services/agent_configuration_service.go:
// EnsureMCPIdentitiesForEnvironment provisions the agent's identity for envName
// if any agentIdentity-mode MCP config maps it, re-pushes those mappings (replacing
// the deny-all placeholder with the sub-pinned rule), and returns the AMP_AGENT_*
// env vars to inject into the target ReleaseBinding. Returns (nil, nil) when the
// agent has no agentIdentity-mode MCP config for envName.
EnsureMCPIdentitiesForEnvironment(ctx context.Context, orgName, projectName, agentName, envName string) ([]client.EnvVar, error)
```

- [ ] **Step 1: Write failing tests.**
  - `EnsureMCPIdentitiesForEnvironment`: agent with one `agentIdentity` config mapping the env → `EnsureIdentity` called once, each of that env's mappings re-deployed (`deployMCPProxyToGateway` seen with `AgentAuth.ThunderAgentSub == identity.ThunderAgentID`), returns the three `AMP_AGENT_*` vars. Agent with only `apiKey` configs → `(nil, nil)`, zero provisioner calls. Two `agentIdentity` configs mapping the same env → `EnsureIdentity` once (identity is per component×env), both configs' mappings re-pushed.
  - `PromoteAgent`: identity env vars appear in the `envOverrides` passed to `ocClient.PromoteComponent` (both `useConfigFromSourceEnv` branches); `EnsureMCPIdentitiesForEnvironment` failure aborts the promotion with a wrapped error (an agent promoted without credentials is broken — fail loudly, not warn).
  - `DeleteEnvironment`: `provisioner.DeleteAllForEnvironment(envUUID)` called after gateway-mapping cleanup; its failure is warn-logged, not returned.

Run: `go test ./services/ -run 'TestEnsureMCPIdentitiesForEnvironment|TestPromoteAgent|TestDeleteEnvironment' -v` — expect FAIL.

- [ ] **Step 2: Implement the OC client wrapper.** `RestartComponent` is `setRestartedAt` verbatim. Add it to the client interface; run `make codegen` for mocks.
- [ ] **Step 3: Implement `EnsureMCPIdentitiesForEnvironment`.** List the agent's MCP configs (`agentConfigRepo` by agent + type MCP) filtered to `AuthMode == agentIdentity` with a mapping for the env; short-circuit `(nil, nil)` if none; `EnsureIdentity` (singleflight in the provisioner dedupes); for each matching mapping re-derive via `buildAgentMCPConfigProxy` and `deployMCPProxyToGateway` (same call shape as `RedeployMCPMappingsForSourceProxy`, line 3825); return `buildAgentIdentityEnvVars(info)`.
- [ ] **Step 4: Hook `PromoteAgent`.** Immediately after the `tgtSystemEnvVars` append (`envOverrides = append(envOverrides, tgtSystemEnvVars...)`):

```go
	identityVars, err := s.agentConfigurationService.EnsureMCPIdentitiesForEnvironment(ctx, orgName, projectName, agentName, req.TargetEnvironment)
	if err != nil {
		return fmt.Errorf("failed to provision agent identity for target environment %q: %w", req.TargetEnvironment, err)
	}
	envOverrides = append(envOverrides, identityVars...)
```

- [ ] **Step 5: Hook `DeleteEnvironment`.** After `gatewayRepo.DeleteEnvironmentMappingsByEnvironmentID` (`environment_service.go:276`): `if err := s.agentIdentityProvisioner.DeleteAllForEnvironment(ctx, envUUID); err != nil { s.logger.Warn("failed to revoke agent identities for deleted environment", "env", envID, "err", err) }`. Wire the provisioner into `environmentService` via `wiring/wire.go` + `make codegen`.
- [ ] **Step 6: Run tests.** `go test ./services/ -v` — expect PASS, zero regressions.
- [ ] **Step 7: Commit.** `git commit -m "feat: provision agent identities on promotion, revoke on environment delete"`

---

# Phase 3 — API surface (spec-first; use the `add-api-resource` skill for each task)

### Task 8: OpenAPI spec + codegen for all new surface

**Files:**
- Modify: `agent-manager-service/docs/api_v1_openapi.yaml`
- Generated: `agent-manager-service/spec/**` via `make spec`
- Modify: `agent-manager-service/controllers/agent_configuration_controller.go` (converter passthrough for the new fields, deferred from Task 7)

**Spec edits (all at once so one `make spec` covers Tasks 8-10):**
1. `CreateAgentModelConfigRequest` (line ~13756), `UpdateAgentModelConfigRequest` (~13844), `AgentModelConfigResponse` (~13878): add

```yaml
        authMode:
          type: string
          enum: [apiKey, agentIdentity]
          description: Authentication mode for the MCP binding (uniform across environments). Defaults to agentIdentity when the target gateways support enforcement, else apiKey. The response carries the EFFECTIVE mode.
```

On the **per-env mapping** schema inside `envMappings` (find the exact schema by following the three schemas' `envMappings` refs — the same object that carries `proxyId`/`LLMProxyInfo`), add:

```yaml
        allowedTools:
          type: array
          items:
            type: string
          nullable: true
          description: MCP tools the agent may call in THIS environment. Omitted/null = all tools; empty = none. Only valid when authMode is agentIdentity.
```

Also add to the response schema a `warnings: {type: array, items: {type: string}}` field, and an `agentIdentity` object (`clientId`, `clientSecret`, `tokenUrl` — returned once, external agents only) on the same per-env mapping object.

2. New path `POST /orgs/{orgName}/mcp-proxies/{proxyId}/refresh-capabilities` → 200 returns the existing MCP proxy response schema (same one `PUT /mcp-proxies/{proxyId}` returns).
3. New paths:

```yaml
  /orgs/{orgName}/projects/{projectName}/agents/{agentName}/identities:
    get: # operationId: listAgentIdentities → AgentIdentityListResponse
  /orgs/{orgName}/projects/{projectName}/agents/{agentName}/identities/{environmentUuid}/rotate-secret:
    post: # operationId: rotateAgentIdentitySecret → AgentIdentityRotateResponse
```

Schemas:

```yaml
    AgentIdentityResponse:
      type: object
      properties:
        environmentUuid: {type: string, format: uuid}
        thunderAgentId: {type: string}
        clientId: {type: string}
        status: {type: string, enum: [active, revoked]}
        createdAt: {type: string, format: date-time}
        updatedAt: {type: string, format: date-time}
    AgentIdentityListResponse:
      type: object
      properties:
        identities:
          type: array
          items: {$ref: '#/components/schemas/AgentIdentityResponse'}
    AgentIdentityRotateResponse:
      type: object
      properties:
        clientId: {type: string}
        clientSecret: {type: string, description: Returned once; store securely.}
        tokenUrl: {type: string}
        restarted: {type: boolean, description: True when a pod rollout was triggered so the running agent picks up the new secret (internal agents). False for external agents — update your runtime's credentials.}
```

(Match surrounding spec conventions exactly — copy an adjacent path block for parameter definitions, error responses, and security.)

- [ ] **Step 1: Edit the spec** as above.
- [ ] **Step 2: Regenerate.** Run: `cd agent-manager-service && make spec` — expect regenerated `spec/` compiles: `go build ./...`.
- [ ] **Step 3: Wire the new DTO fields** through `controllers/agent_configuration_controller.go` converters (lines 585+): `AllowedTools`/`AuthMode` both directions, `Warnings` + `AgentIdentity` on responses. Run `go test ./controllers/... ./services/...` — expect PASS.
- [ ] **Step 4: Commit.** `git commit -m "feat: extend API spec with allowedTools, refresh-capabilities, and agent identity endpoints"`

---

### Task 9: refresh-capabilities endpoint

**Files:**
- Modify: `agent-manager-service/services/mcp_proxy_service.go` (new method on the service + interface)
- Modify: `agent-manager-service/controllers/mcp_proxy_controller.go` (new handler `RefreshCapabilities`)
- Modify: `agent-manager-service/api/mcp_proxy_routes.go` (new route)
- Test: extend the mcp proxy service unit test file

**Interfaces:**
- Produces: `RefreshCapabilities(ctx context.Context, orgUUID uuid.UUID, proxyID string) (*models.MCPProxy, error)` on `MCPProxyService`.

- [ ] **Step 1: Write failing tests:** happy path (fetch succeeds → capabilities replaced → proxy persisted → redeploy triggered including `RedeployMCPMappingsForSourceProxy`); fetch failure → error returned AND stored snapshot untouched (assert no repo update call).
- [ ] **Step 2: Implement.** Load the proxy; build a `models.MCPServerInfoFetchRequest` from `proxy.Configuration.Upstream` (URL + auth — read how `UpdateMCPProxy` handles upstream auth secrets and reuse the decrypt path); call the existing `fetchMCPServerInfo` (line 836); on success, set `Configuration.Capabilities` via `copyMCPCapabilities` (line 662), persist, then mirror `UpdateMCPProxy`'s redeploy sequence (`redeployMCPProxyToCurrentGateways` + the source-proxy → mapping redeploy hook — find the exact call `UpdateMCPProxy` uses to reach `RedeployMCPMappingsForSourceProxy`).
- [ ] **Step 3: Route + handler.** `rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/mcp-proxies/{proxyId}/refresh-capabilities", rbac.MCPServerUpdate, ctrl.RefreshCapabilities)` in `api/mcp_proxy_routes.go`; handler decodes path params and returns the converted proxy response like `GetMCPProxy` does.
- [ ] **Step 4: Run tests + build.** `go test ./services/ ./controllers/ -v` — expect PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat: add MCP proxy refresh-capabilities endpoint"`

---

### Task 10: Agent identity endpoints + RBAC

**Files:**
- Modify: `agent-manager-service/rbac/permissions.go` (line ~145: `AgentManageIdentity Permission = "agent:manage-identity"`)
- Modify: `agent-manager-service/rbac/predefined_roles.go` (add to `RoleAdmin`'s slice)
- Create: `agent-manager-service/controllers/agent_identity_controller.go`
- Modify: `agent-manager-service/api/agent_routes.go` (or a new `agent_identity_routes.go` if route files are per-resource — follow the pattern of `agent_config_routes.go`)
- Modify: `agent-manager-service/wiring/wire.go` + `make codegen`
- Test: `agent-manager-service/controllers/agent_identity_controller_test.go` (if controller tests are conventional — check siblings; otherwise service-level tests suffice since the provisioner is already tested)

**Interfaces:**
- Consumes: `AgentIdentityProvisioner.ListIdentities` / `.RotateSecret` (Task 4); generated `spec.AgentIdentityListResponse` / `spec.AgentIdentityRotateResponse` (Task 8).
- Routes:

```go
rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/projects/{projectName}/agents/{agentName}/identities", rbac.AgentRead, ctrl.ListAgentIdentities)
rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/projects/{projectName}/agents/{agentName}/identities/{environmentUuid}/rotate-secret", rbac.AgentManageIdentity, ctrl.RotateAgentIdentitySecret)
```

- [ ] **Step 1: Add permission + role grant.** Also check whether Thunder role bootstrap needs a version bump / re-sync when permissions change (grep for how `PredefinedRolePermissions` reaches Thunder; if there's a bootstrap version constant, bump it).
- [ ] **Step 2: Controller.** Resolve the agent's component UUID the way `agent_configuration_controller.go` does (org/project/agent path params → component lookup), call the provisioner, map `models.AgentIdentity` → `spec.AgentIdentityResponse` (never expose `SecretRef`). Rotate returns 200 with the one-time secret, then — **internal agents only** — triggers `ocClient.RestartComponent(orgName, agentName, envName)` (Task 7b; resolve env name from the path's `environmentUuid`) so the running pod picks up the new secret; set `restarted: true` on success, `false` + warn-log on failure or for external agents. Provisioner-disabled (`ErrAgentIdentityNotEnabled`) → 409 or 400 with a clear message (match how other services surface "feature not configured" — grep `ErrNotThunderMode` handling).
- [ ] **Step 3: Wire + routes + build.** `make codegen && go build ./... && go test ./... ` (unit tier) — expect PASS.
- [ ] **Step 4: Commit.** `git commit -m "feat: add agent identity list and rotate-secret endpoints"`

---

# Phase 4 — CLI

### Task 11: `--allowed-tools` / `--auth-mode` on `agent mcp set`

**Files:**
- Regenerate: `cli/pkg/clients/amsvc/gen/{types.gen.go,client.gen.go}` via `make amctl-gen-client` (repo root)
- Modify: `cli/pkg/cmd/agent/mcp/set.go` (+ `get.go`, `list.go` display)
- Test: `cli/pkg/cmd/agent/mcp/set_test.go`

- [ ] **Step 1: Regenerate the client.** Run at repo root: `make amctl-gen-client`. Expect `AuthMode` on the generated request/response types and `AllowedTools` on the per-env mapping types.
- [ ] **Step 2: Write failing test** in `set_test.go`: invoking `set --name foo --env dev --allowed-tools get_weather,list_events` produces a request whose `EnvMappings["dev"].AllowedTools` is `["get_weather","list_events"]` and leaves other envs' mappings untouched (read-merge-write); `--allowed-tools` without `--env` when the binding maps more than one environment → usage error naming the mapped envs; omitting the flag leaves the mapping's allowlist nil (existing tests show the httptest-backed command harness — copy it).
- [ ] **Step 3: Implement.** On `SetOptions`: `AllowedTools []string`, `AllowedToolsSet bool` (from `cmd.Flags().Changed("allowed-tools")` so "flag absent" → nil ≠ "empty list" → deny-all), `Env string`, `AuthMode string`. Flags:

```go
cmd.Flags().StringSliceVar(&opts.AllowedTools, "allowed-tools", nil, "MCP tools the agent may call in the environment given by --env (comma-separated). Omit for all tools; pass empty string for none")
cmd.Flags().StringVar(&opts.Env, "env", "", "Environment the --allowed-tools flag applies to (required with --allowed-tools when the binding maps multiple environments)")
cmd.Flags().StringVar(&opts.AuthMode, "auth-mode", "", "Binding auth mode: apiKey or agentIdentity (default: auto)")
```

Thread into the create/update request structs in `runSet`: `AuthMode` top-level; `AllowedTools` onto the `--env` mapping (defaulting to the sole mapped env when only one exists), preserving all other envs' mappings on read-merge-write. In `get.go`, print `authMode` and a per-environment allowlist column; in JSON mode they ride the payload automatically.
- [ ] **Step 4: Run tests.** `cd cli && go test ./pkg/cmd/agent/mcp/... -v` — expect PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat(cli): allowed-tools and auth-mode flags on agent mcp set"`

---

### Task 12: `agent identity` command group

**Files:**
- Create: `cli/pkg/cmd/agent/identity/identity.go`, `list.go`, `rotate_secret.go`
- Modify: `cli/pkg/cmd/agent/agent.go` (`cmd.AddCommand(identity.NewIdentityCmd(f))`)
- Test: `cli/pkg/cmd/agent/identity/list_test.go`, `rotate_secret_test.go`

**Interfaces:**
- Consumes: generated `ListAgentIdentitiesWithResponse`, `RotateAgentIdentitySecretWithResponse` (from Task 11's regen — verify the operationIds produced these names; adjust if the generator named them differently).

- [ ] **Step 1: Write failing tests** (httptest harness copied from `mcp/list_test.go`): `identity list` renders a table with columns `environment`, `client id`, `status`; `identity rotate-secret --env <uuid>` prints the one-time secret to stdout in JSON mode and a `SuccessIcon` + secret in text mode with a "store this now" warning to stderr, plus — from the response's `restarted` field — either "agent restarted in <env> to pick up the new secret" or "update the credentials configured in your agent's runtime" for external agents.
- [ ] **Step 2: Implement.** `identity.go` mirrors `mcp.go` (`NewIdentityCmd(f *cmdutil.Factory)`); subcommands copy the resolve/scope boilerplate from `mcp/list.go:63-76` (`f.ResolveOrgProject`, `f.AgentScope`, `f.ResolveAgent`, `cmdutil.ValidatePathParam`). Table via `tableprinter.New(o.IO, "environment", "client id", "status")`; JSON via `render.JSONSuccess`. Required flag `--env` on rotate (`MarkFlagRequired`).
- [ ] **Step 3: Run tests.** `cd cli && go test ./pkg/cmd/agent/... -v` — expect PASS.
- [ ] **Step 4: Commit.** `git commit -m "feat(cli): agent identity list and rotate-secret commands"`

---

# Phase 5 — Console (use the `add-console-api-feature` skill for each task)

### Task 13: api-client + types for identities, refresh, allowlist

**Files:**
- Modify: `console/workspaces/libs/types/src/api/mcp-proxies.ts` (nothing needed for capabilities — exists) and the agent-mcp-config request/response types (find them: `grep -r "CreateAgentModelConfigRequest\|envMappings" console/workspaces/libs/types/src/`) — add `authMode?: 'apiKey' | 'agentIdentity'` + `warnings?: string[]` at the config level, and `allowedTools?: string[] | null` + `agentIdentity?: {clientId: string; clientSecret?: string; tokenUrl: string}` on the **per-env mapping** entry type (the object keyed by env name in `envMappings`).
- Create: `console/workspaces/libs/types/src/api/agent-identities.ts`:

```ts
export interface AgentIdentity {
  environmentUuid: string;
  thunderAgentId: string;
  clientId: string;
  status: 'active' | 'revoked';
  createdAt: string;
  updatedAt: string;
}
export interface AgentIdentityListResponse { identities?: AgentIdentity[]; }
export interface AgentIdentityRotateResponse { clientId: string; clientSecret: string; tokenUrl: string; restarted?: boolean; }
```

- Create: `console/workspaces/libs/api-client/src/apis/agent-identities.ts` — `listAgentIdentities(params, getToken)`, `rotateAgentIdentitySecret(params, getToken)` (params: org/project/agent [+ environmentUuid for rotate]); copy the URL-building/`httpGET`/`httpPOST`/`encodeRequired`/throw-on-`!res.ok` shape from `apis/agent-mcp-configs.ts`.
- Create: `console/workspaces/libs/api-client/src/hooks/agent-identities.ts` — `QUERY_KEY = "agent-identities"`; `useListAgentIdentities(params)` (`useApiQuery`, `enabled` guard); `useRotateAgentIdentitySecret()` (`useApiMutation`, `action: {verb: "rotate", target: "agent identity secret"}`, `onSuccess` invalidates `[QUERY_KEY]`).
- Modify: `console/workspaces/libs/api-client/src/apis/mcp-proxies.ts` + `hooks/mcp-proxies.ts` — add `refreshMCPProxyCapabilities(params, getToken)` → `POST {SERVICE_BASE}/orgs/{org}/mcp-proxies/{proxyId}/refresh-capabilities` and `useRefreshMCPProxyCapabilities()` (mutation; `onSuccess` invalidates the mcp-proxies query key so the fresh snapshot re-renders).
- Modify: the package's barrel exports (`index.ts` files) — mirror how `agent-mcp-configs` is exported.

- [ ] **Step 1: Implement all files above.**
- [ ] **Step 2: Typecheck/build the workspace.** Run the console's build/lint the way the repo does (check `console/README` / rush commands, e.g. `rush build -t @agent-management-platform/api-client`). Expect clean.
- [ ] **Step 3: Commit.** `git commit -m "feat(console): api-client for agent identities, capability refresh, allowedTools"`

---

### Task 14: Tool picker + auth mode + Refresh tools on the MCP binding form

**Files:**
- Create: `console/workspaces/pages/configure-agent/src/Configure/subComponents/ToolPicker.tsx`
- Modify: `console/workspaces/pages/configure-agent/src/AddMCPServer.Component.tsx` (per-env picker inside the env tabs; auth-mode select; mode-aware env-var table; nav-state handoff)
- Modify: `console/workspaces/pages/configure-agent/src/ViewMCPServer.Component.tsx` (effective mode + warnings + one-time credentials from navigation state; per-env allowlist display + edit path via `useUpdateAgentMCPConfig`)

**Interfaces:**
- Produces `ToolPicker` props (one instance per environment tab — allowlists are per env):

```ts
interface ToolPickerProps {
  tools: Record<string, unknown>[] | undefined;   // the CURRENT env's proxy.capabilities.tools snapshot
  value: string[] | null;                          // null = all tools allowed (this env)
  onChange: (next: string[] | null) => void;
  onRefresh: () => void;                           // fires useRefreshMCPProxyCapabilities for this env's proxy
  refreshing: boolean;
}
```

Behavior: a "Restrict tools" oxygen `Checkbox` master toggle (unchecked → `value = null`, all-tools banner; checked → per-tool `FormControlLabel`+`Checkbox` list from the snapshot, initialized to all-checked when toggling on). Entries in `value` missing from the snapshot render as a `Chip` flagged "unknown tool — refresh?", kept in the list (design: keep-but-flag). "Refresh tools" = outlined `Button` with refresh icon calling `onRefresh`, disabled while `refreshing`. Follow the checkbox idiom from `add-new-agent/src/forms/InternalAgentForm.tsx` and selection state from `mcp-proxies/src/subComponents/MCPProxyRewriteTab.tsx` (`buildDefaultsFromCapabilities`).

- [ ] **Step 1: Build `ToolPicker`** (plain useState, no form lib — matches the page).
- [ ] **Step 2: Wire into `AddMCPServer.Component.tsx`:**
  - Render the picker **inside the environment tab content**, below the selected-server card: state `allowedToolsByEnv: Record<string, string[] | null>` keyed like `serverByEnv` (`AddMCPServer.Component.tsx:78-81`), fed by that env's selected proxy snapshot. Changing an env's server resets that env's entry to `null`. Payload: `envMappings[env.name].allowedTools` (omit when `null`).
  - Add an "Authentication" `Select` (options `Auto — platform decides` (default) / `API key` / `Agent identity`) mapped to the top-level `authMode` ( `Auto` → omit).
  - Make the env-var-names table (`:497-551`) mode-aware: `Agent identity` → hide the `apikey` row and append read-only rows for `AMP_AGENT_CLIENT_ID`, `AMP_AGENT_CLIENT_SECRET`, `AMP_AGENT_TOKEN_URL` (fixed names, not editable, description "injected by the platform"); `Auto` → keep `apikey` with its description suffixed "(only injected if the binding resolves to API-key auth)" and show the `AMP_AGENT_*` rows with the mirror caveat; `API key` → unchanged.
  - **Navigation-state handoff:** the `onSuccess` handler navigates immediately (`:224-239`), so extend the existing `state` object with `warnings: data.warnings`, `effectiveAuthMode: data.authMode`, and `identityByEnv` (per-env `agentIdentity` credentials from `data.envMappings`, external agents) alongside `authInfoByEnv`. Do NOT render warnings in this component — it unmounts.
- [ ] **Step 3: Wire into `ViewMCPServer.Component.tsx`:** on mount, read navigation state: render `warnings` as a dismissible warning `Alert` (the apiKey-fallback message), and `identityByEnv` as a one-time credential reveal per env (clientId / clientSecret / tokenUrl with copy buttons and "you will only see this once" — clone the `NewKeyBanner` idiom from `agent-security/src/Security.Component.tsx:160-194`). Always display the persisted effective `authMode` as a `Chip` ("API key" / "Agent identity") and the per-env allowlist (or "All tools") in each env's section; edit → `useUpdateAgentMCPConfig` sending only that env's `allowedTools`.
- [ ] **Step 4: Verify in the running console** (local env per `deployments/`; the `run` skill or existing dev setup): create a binding with different allowlists in two envs, confirm `envMappings.<env>.allowedTools` in the network tab; confirm the fallback warning and (for an external agent) the one-time credentials render on the view page after the redirect; refresh tools and confirm the POST returns updated capabilities.
- [ ] **Step 5: Commit.** `git commit -m "feat(console): per-env MCP tool picker, auth mode select, capability refresh"`

---

### Task 15: Security page restructure + agent identity panel

**Files:**
- Modify: `console/workspaces/pages/agent-security/src/Security.Component.tsx` (retitle, two sections, un-gate the identity section)

**Interfaces:**
- Consumes: `useListAgentIdentities`, `useRotateAgentIdentitySecret` (Task 13).

- [ ] **Step 1: Restructure the page.** Retitle `PageLayout` from "API Keys" to **"Security"**. Split into two labeled sections:
  - **"Inbound API Keys"** — the existing content verbatim, with its existing gates, but scoped: the `hasActiveDeployment` early return (`Security.Component.tsx:305-323`) and the `securityEnabled` info alert (`:347-351`) become section-level states inside this section, NOT page-level returns. Section subtitle: "Keys callers use to invoke this agent."
  - **"Agent Identity"** — new section, rendered **unconditionally** (no deployment or api-key-security gate; external agents have no deployments and must still reach it). Section subtitle: "The credential this agent uses to call MCP servers."
- [ ] **Step 2: Implement the identity panel.** For the current `envId` (page is already per-env): find the matching identity in `useListAgentIdentities` data; show `clientId`, status `Chip` (active → success, matching the API-key chips), created/updated. "Rotate secret" `Button` → confirm `Dialog` (clone `CreateAPIKeyDialog`'s structure, `:57-158`) whose body warns per agent type: internal → "The agent will be restarted in this environment to pick up the new secret."; external → "Update the credentials configured in your agent's runtime after rotating." On success show the one-time `clientSecret` + `tokenUrl` in the `NewKeyBanner`-style reveal (`:160-194`), plus a confirmation line driven by the response's `restarted` field. Empty state: "No identity provisioned for this environment yet — identities are created when the agent is deployed or promoted here with an agent-identity MCP binding."
- [ ] **Step 3: Verify in the running console** against a locally-provisioned identity (from Task 16's flow, or mock the API if backend isn't running). Confirm: identity section renders for an undeployed env and for an external agent; rotate shows the secret once and reports the restart.
- [ ] **Step 4: Commit.** `git commit -m "feat(console): security page with inbound keys and agent identity sections"`

---

# Phase 6 — End-to-end verification

### Task 16: Integration verification (local k3d + thunder-extension)

No new product code; this validates the whole chain. Use the `verify` skill mindset: drive the real flow.

- [ ] **Step 1:** Bring up the local environment with the thunder extension (see memory notes `local-env-container-networking.md` + `amctl-sample-deploy-config.md` for local quirks; `deployments/` + Makefile targets).
- [ ] **Step 2:** Deploy a sample agent (`samples/`, via `amctl` per the `am-ops` skill) into the **lowest environment only** of a two-env pipeline, and bind an MCP proxy mapping **both** environments with `--auth-mode agentIdentity --env <lowest-env> --allowed-tools <one-tool>`.
- [ ] **Step 3:** Assert, in order:
  1. Exactly **one** `agent_identities` row exists (lowest env); Thunder has that agent (find by `component_uid`/`environment_uid` attributes); OpenBao secret present. **No** identity exists for the higher env.
  2. Agent pod env (lowest env) has `AMP_AGENT_CLIENT_ID/TOKEN_URL` and mounted `AMP_AGENT_CLIENT_SECRET`.
  3. Deployed mapping YAML (from `deployments` table `content`): lowest env contains `mcp-auth` + `mcp-authz` with the wildcard sub rule and one sentinel rule per restricted tool; **higher env contains the deny-all wildcard sentinel rule** (identity not provisioned yet). **This is also the moment to confirm the gateway's actual `mcp-auth`/`mcp-authz` param names accept our rendering** — if the gateway rejects the artifact or ignores rules, fix the param names in Task 5 and its tests.
  4. With a client-credentials token from the injected creds: allowed tool call succeeds; restricted tool call → 403; token from a different agent/env → 403 on everything; any call against the higher env's mapping → 403 (deny-all).
  5. **Promote the agent to the higher env** (`amctl` or console). Assert: a second `agent_identities` row + Thunder agent + secret appear for the target env; the target pod has all three `AMP_AGENT_*` vars; the target mapping YAML now carries the sub-pinned wildcard rule (deny-all replaced); a token from the target env's creds works there and is rejected by the lowest env's mapping.
  6. `POST .../refresh-capabilities` after adding a tool upstream → snapshot updated, mapping re-pushed, new tool restricted after re-render (since it's not in the allowlist).
  7. `amctl agent identity list` / `rotate-secret` round-trip: response has `restarted: true`, the pod actually restarts (watch `kubectl get pods`), the restarted pod's next token request succeeds with the new secret; old tokens keep working until expiry.
  8. Remove the higher env's mapping from the binding (`amctl agent mcp set` read-merge-write): that env's identity row flips to `revoked` and the Thunder agent is gone; the lowest env is untouched.
- [ ] **Step 4:** Run the full repo verification: service `make test` + lint, `cli` tests, console build. Fix anything red.
- [ ] **Step 5: Commit** any fixes; then follow `superpowers:finishing-a-development-branch`.

---

## Self-review notes (spec → plan coverage)

- Spec §Data model → Task 1 (amended: `allowed_tools` per env mapping); §Services provisioner → Tasks 2-4 (amended: attribute-based lookup, `DeleteIdentity`/`DeleteAllForEnvironment`); §policy rendering → Task 5 (amended: deny-all placeholder for unprovisioned envs); issuer verification (amended: no auto-registration) → Task 6; §credential delivery + env-var injection → Task 7 (amended: deployment-gated provisioning) + Task 7b (promotion hook, environment-delete revocation, restart plumbing); §API surface → Tasks 8-10 (amended: rotate triggers restart); §Console/CLI → Tasks 11-15 (amended: per-env allowlists, nav-state warnings/credentials, Security page restructure); §Testing unit/rendering/integration → embedded per task + Task 16; §Error handling rows → Task 4 (provision failure/idempotency), Task 6 (gateway-lacks-policies fallback), Task 7b (promote fails loudly when identity provisioning fails), Task 9 (refresh failure keeps snapshot), Tasks 4+10 (rotation semantics), Task 16.4 (denied-call 403).
- Deliberately out of scope (per spec): LLM-provider extension, SDK token-refresh helper, gateway `defaultAction: deny` + `tools/list` filtering feature requests, automated capability polling.
- Known open items flagged inline: gateway `mcp-auth`/`mcp-authz` param names (verify against a live gateway manifest — Task 5 note, Task 16.3), thunder-extension's bootstrapped agent-type schema (Task 3), Thunder `filter` grammar for attribute-based find (Task 3 — fall back to list + client-side match if unsupported).
