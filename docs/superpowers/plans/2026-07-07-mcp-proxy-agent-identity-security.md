# MCP Proxy Agent-Identity Security Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an Agent Identity security mode to MCP proxies: an org-global scope catalog in AMS, per-environment tool→scope bindings, `mcp-auth`/`mcp-authz` gateway policy emission, and direct-passthrough management of env-Thunder roles/groups for agent grants.

**Architecture:** AMS stores only the scope catalog (new `scopes` table) and per-environment bindings inside the existing `MCPEnvironmentConfig` JSON blocks (PR #1258 shape). Grants are Thunder-native: roles/groups live in each environment's Thunder instance, reached per request via `EnvThunderResolver` — no AMS desired-state rows, no reconciler. The per-env deployment YAML builder emits `mcp-auth`/`mcp-authz` policies when that env's `SecurityConfig` has the new `identity` variant.

**Tech Stack:** Go (net/http, GORM, wire DI, moq mocks), OpenAPI spec-first codegen, React/TypeScript console (TanStack Query, oxygen-ui, rush).

**Spec:** `docs/superpowers/specs/2026-07-06-mcp-proxy-agent-identity-security-design.md`

## Global Constraints

- **Execution precondition:** PR #1258 (`mcp-proxy-ux-revamp`) is MERGED into `upstream/main`, and `upstream/main` is merged into the working branch (continuation of `task/agent-id-2a`). Task 1 verifies this; no other task may start before Task 1 passes.
- Scope name pattern: `^[A-Za-z0-9:._\-]{1,256}$` (exact, from spec §5.1).
- Env-Thunder resource server identifier: `amp-scopes` (spec §7.1).
- JWT issuer pinned to key-manager name `ThunderKeyManager` (spec, decision table). No issuer picker in v1.
- Gateway policies emitted: `mcp-auth` version `v1` and `mcp-authz` version `v1` only.
- `SecurityConfig` variants `apiKey` and `identity` are mutually exclusive (validated, 400 on both enabled).
- Unbound tools are authenticated-only: no `mcp-authz` rule for them (gateway default-permit).
- Scope deletion blocked (409) while referenced by any MCP proxy environment's `toolScopeBindings`.
- No AMS DB storage for groups/roles — every agent-identity route is a passthrough to env-Thunder.
- Every new `.go`/`.ts`/`.tsx` file carries the Apache 2.0 header (copy from any sibling; `goheader` lint enforces it on Go, including tests).
- Go lint: `golangci-lint run --config .github/linters/.golangci.yaml ./...` must stay clean (it lints test files too — see traps in `.claude/skills/add-service-unit-test/SKILL.md`).
- Backend API work follows `.claude/skills/add-api-resource/SKILL.md`; console work follows `.claude/skills/add-console-api-feature/SKILL.md`; unit tests follow `.claude/skills/add-service-unit-test/SKILL.md`. Invoke each skill at the start of the task that cites it.
- Git/commit conventions: `am-ship` skill (invoke before first commit).
- Phase 3 (E2E) runs against a real deployment whose default environment has a provisioned env-Thunder with an externally reachable token URL — see the Phase 3 preconditions.

## Phase overview

- **Phase 0 — Preconditions:** Task 1 (preflight integration check), Task 2 (live env-Thunder grant-model spike; gates the Groups work).
- **Phase 1 — Backend:** Tasks 3–8 (scopes table → scope API → identity security model/validation → YAML emission → thundersvc client extensions → agent-identity passthrough API).
- **Phase 2 — Console:** Tasks 9–15 (types → scopes api/hooks → agent-identity api/hooks → Security tab → Agent Identity section: scaffold+Scopes, Roles, Groups).
- **Phase 3 — E2E:** Tasks 16–17 (e2e operations/framework support — including reconciling the mcpproxy e2e ops to the post-#1258 `environments` shape, which PR #1258 left untouched — then the identity-secured proxy lifecycle spec).

---

## Phase 0 — Preconditions

### Task 1: Preflight — verify the merged base and record anchor names

The plan was written against two branches (`task/agent-id-2a` and `mcp-proxy-ux-revamp`) *before* PR #1258 merged. This task verifies the union landed as expected and records the handful of names the plan could not pin down in advance.

**Files:**
- No code changes. Output: `docs/superpowers/plans/2026-07-07-preflight-notes.md` (a short findings file).

**Interfaces:**
- Produces: confirmed anchor list + three recorded names used by later tasks: `MCPProxyRepository`'s org-scoped list method (Task 4), whether `MCPProxyRequest`/`MCPProxyResponse` in the OpenAPI yaml already expose `environments` (Task 5), and the core-ui registration point for page workspaces (Task 13).

- [ ] **Step 1: Verify PR #1258 is merged and integrate main**

```bash
gh pr view 1258 --repo wso2/agent-manager --json state,mergedAt
# Expected: "state": "MERGED". If not merged: STOP — this plan cannot execute yet.
git fetch upstream
git merge upstream/main
# Resolve conflicts if any (expected hotspots: services/mcp_proxy_*.go, models/mcp_proxy.go).
```

- [ ] **Step 2: Verify every code anchor this plan builds on**

Run each; every one must produce at least one hit. A missing anchor means PR #1258 changed shape in review — stop and re-reconcile the spec section named in the comment.

```bash
cd agent-manager-service
grep -n "Environments map\[string\]MCPEnvironmentConfig" models/mcp_proxy.go        # spec §3.1
grep -n "func buildMCPProxyEnvArtifact" services/mcp_proxy_deployment.go            # §6 insertion point
grep -n "func appendMCPAPIKeyAuthPolicy" services/mcp_proxy_deployment.go           # §6 sibling
grep -n "func (s \*MCPProxyService) deployMCPProxyEnvironments" services/mcp_proxy_deployment.go
grep -n "func validateMCPEnvironments" services/mcp_proxy_service.go                # Task 5 hook
grep -n "func (s \*MCPProxyService) buildMCPEnvironmentsForStorage" services/mcp_proxy_service.go
grep -n "func defaultMCPProxySecurity" services/mcp_proxy_service.go                # Task 5 rewrite target
grep -n "func (s \*MCPProxyService) ListAvailableMCPPolicies" services/mcp_proxy_service.go
grep -n "func extractGatewayPolicyManifestItems" services/mcp_proxy_service.go      # Task 5 reuse
grep -n "ResolveIdentity\|Resolve(ctx context.Context, orgName, envName string)" clients/thundersvc/env_resolver.go
grep -n "AddRolePermissions" clients/thundersvc/identity_client.go
grep -n "func NewIdentityController" controllers/identity_controller.go
grep -n "func mcpProxyAPIKeySecurityEnabled" services/agent_configuration_service.go
cd ../console
grep -n "MCPEnvironmentConfig" workspaces/libs/types/src/api/mcp-proxies.ts
ls workspaces/pages/mcp-proxies/src/subComponents/MCPProxySecurityTab.tsx
ls workspaces/pages/identities/src/GroupsPage.tsx workspaces/pages/identities/src/RolesPage.tsx
```

- [ ] **Step 3: Record the three unpinned names**

```bash
cd agent-manager-service
# (a) MCPProxyRepository org-scoped list method (Task 4 uses it for the in-use scan):
grep -n "type MCPProxyRepository interface" -A 15 repositories/mcp_proxy_repository.go
# (b) Does the spec yaml already carry environments on the proxy DTO? (Task 5 adds it if absent — it was absent on the revamp branch at planning time):
grep -n "environments" docs/api_v1_openapi.yaml | sed -n '1,10p'
awk '/^    MCPProxyRequest:/,/^    MCPProxyResponse:/' docs/api_v1_openapi.yaml | grep -n "environments"
# (c) Console page-registration point (Task 13 mirrors the identities entry):
cd ../console && grep -rn "pages/identities\|identitiesMetadata" workspaces/apps workspaces/core-ui --include="*.ts*" -l 2>/dev/null | grep -v node_modules | grep -v dist
```

Write all three answers into `docs/superpowers/plans/2026-07-07-preflight-notes.md`.

- [ ] **Step 4: Green baseline**

```bash
cd agent-manager-service && make test-unit && go build -tags=integration ./...
```
Expected: PASS / compiles. Record any pre-existing failures in the notes file (they are not this plan's to fix, but must be known).

- [ ] **Step 5: Commit** (merge commit + notes file)

```bash
git add docs/superpowers/plans/2026-07-07-preflight-notes.md
git commit -m "docs: record agent-identity preflight anchors"
```

---

### Task 2: Spike — verify the env-Thunder grant model against a live instance

Spec §7.1 lists three approved-but-unverified assumptions, plus one this plan adds. **Results gate later tasks** — run this before writing any grant code.

**Files:**
- Create: `docs/superpowers/specs/2026-07-07-env-thunder-grant-verification.md` (findings)

**Interfaces:**
- Produces: verified Thunder API shapes for Task 7 (`EnsureScopeResourceServer` endpoints/payloads) and go/no-go flags: (b) fail → **stop the whole feature** and report; (c) fail → **drop Task 15** (Groups tab) and exclude groups from Task 8's assignment pickers and Task 14's assignee picker.

What to verify (thunderid-0.45.0):

| # | Assumption | How |
|---|---|---|
| (a) | Roles are assignable to `/agents` identities (`AssignmentEntry{Type:"agent"}`) | assign + read back |
| (b) | `client_credentials` tokens carry role-derived permissions in the `scope` claim | decode JWT |
| (c) | Roles assigned to a *group* containing the agent contribute scopes to the agent's token | group path token |
| (d) | Resource-server actions accept arbitrary permission strings matching the scope regex (e.g. `repo:read.all`), and role writes referencing them succeed | create + role write |

- [ ] **Step 1: Get access to a live env-Thunder**

Use a running local/dev environment that has at least one environment provisioned with Thunder (created via `add-environment.sh`). Retrieve the system client secret the same way `EnvThunderResolver` does — from OpenBao at `<mount>/thunder-system-clients/<org>/<env>` (key: the system client secret; client ID is the well-known `thunderSystemClientID` constant in `clients/thundersvc/`). Port-forward the env-Thunder service if needed. Obtain a system token:

```bash
curl -s -X POST "$THUNDER_URL/oauth2/token" \
  -d grant_type=client_credentials -d client_id="$SYS_ID" -d client_secret="$SYS_SECRET"
```

- [ ] **Step 2: Create the spike fixtures (agent, RS, role) and test (a), (b), (d)**

```bash
# Agent identity (or reuse an existing COMPLETED AgentThunderClient binding's IDs):
curl -s -X POST "$THUNDER_URL/agents" -H "Authorization: Bearer $TOK" \
  -d '{"name":"spike-agent","ouId":"'$DEFAULT_OU'"}'          # → note agentId, clientId, clientSecret

# (d) Resource server with a scope-shaped permission:
curl -s -X POST "$THUNDER_URL/resource-servers" -H "Authorization: Bearer $TOK" \
  -d '{"name":"AMP Scopes Spike","identifier":"amp-scopes-spike"}'   # → rsID
# Create a resource + action; verify an explicit scope-style permission string is accepted.
# Record the EXACT endpoints/fields Thunder 0.45 requires (resources vs actions, "permission" field
# or handle-derived) — Task 7's EnsureScopeResourceServer is written from this record.
curl -s -X POST "$THUNDER_URL/resource-servers/$RSID/resources" -H "Authorization: Bearer $TOK" \
  -d '{"name":"Spike","handle":"spike"}'                              # → resID
curl -s -X POST "$THUNDER_URL/resource-servers/$RSID/resources/$RESID/actions" -H "Authorization: Bearer $TOK" \
  -d '{"name":"Read All","handle":"read.all","permission":"repo:read.all"}'

# Role carrying that permission, assigned directly to the agent — tests (a):
curl -s -X POST "$THUNDER_URL/roles" -H "Authorization: Bearer $TOK" \
  -d '{"name":"spike-role","ouId":"'$DEFAULT_OU'","permissions":[{"resourceServerId":"'$RSID'","permissions":["repo:read.all"]}]}'
curl -s -X POST "$THUNDER_URL/roles/$ROLEID/assignments/add" -H "Authorization: Bearer $TOK" \
  -d '{"assignments":[{"id":"'$AGENT_ID'","type":"agent"}]}'

# (b) Agent token — decode the scope claim:
curl -s -X POST "$THUNDER_URL/oauth2/token" -d grant_type=client_credentials \
  -d client_id="$AGENT_CLIENT_ID" -d client_secret="$AGENT_CLIENT_SECRET" \
  | jq -r .access_token | cut -d. -f2 | base64 -d | jq .scope
# Expected: contains "repo:read.all"
```

- [ ] **Step 3: Test (c) — group flattening**

```bash
curl -s -X POST "$THUNDER_URL/groups" -H "Authorization: Bearer $TOK" \
  -d '{"name":"spike-group","ouId":"'$DEFAULT_OU'"}'
curl -s -X POST "$THUNDER_URL/groups/$GROUPID/members/add" -H "Authorization: Bearer $TOK" \
  -d '{"members":[{"id":"'$AGENT_ID'","type":"agent"}]}'
# Second role with a second permission, assigned to the GROUP (not the agent):
# ... create repo:write.all under the same RS, role spike-role-2, then:
curl -s -X POST "$THUNDER_URL/roles/$ROLE2ID/assignments/add" -H "Authorization: Bearer $TOK" \
  -d '{"assignments":[{"id":"'$GROUPID'","type":"group"}]}'
# Fetch the agent token again; scope must now ALSO contain "repo:write.all".
```

- [ ] **Step 4: Record findings and clean up**

Write `docs/superpowers/specs/2026-07-07-env-thunder-grant-verification.md` with: pass/fail per (a)–(d), the exact resource-server/resource/action endpoints + payloads that worked, and the token `scope` claim format (space-separated string vs array). Delete the spike fixtures (roles, group, RS, agent). Apply the gates listed under **Interfaces** above.

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/specs/2026-07-07-env-thunder-grant-verification.md
git commit -m "docs: record env-thunder grant-model verification results"
```

---

## Phase 1 — Backend

### Task 3: `scopes` table, model, repository

Invoke `add-api-resource` skill context (this task is layers 0–1 of it: migration + repository; the API lands in Task 4).

**Files:**
- Create: `agent-manager-service/db_migrations/029_create_scopes.go`
- Modify: `agent-manager-service/db_migrations/migration_list.go` (append `migration029`, bump `latestVersion` to 29)
- Create: `agent-manager-service/models/scope.go`
- Create: `agent-manager-service/repositories/scope_repository.go`

**Interfaces:**
- Produces: `models.Scope{ID uuid.UUID, OrgName, Name, Description string, CreatedAt, UpdatedAt time.Time}`; `repositories.ScopeRepository` with methods `List(ctx, orgName) ([]models.Scope, error)`, `GetByName(ctx, orgName, name) (*models.Scope, error)`, `Create(ctx, *models.Scope) error`, `Update(ctx, *models.Scope) error`, `Delete(ctx, orgName, name) error`; moq mock `repomocks.ScopeRepositoryMock`.

- [ ] **Step 1: Write the migration**

`db_migrations/029_create_scopes.go` (Apache header, `package dbmigrations`):

```go
var migration029 = migration{
	ID: 29,
	Migrate: func(db *gorm.DB) error {
		sql := `
			CREATE TABLE scopes (
				id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
				org_name    TEXT        NOT NULL,
				name        TEXT        NOT NULL,
				description TEXT        NOT NULL DEFAULT '',
				created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
				updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

				CONSTRAINT uq_scopes_org_name UNIQUE (org_name, name)
			);
			CREATE INDEX IF NOT EXISTS idx_scopes_org ON scopes (org_name);
		`
		return db.Transaction(func(tx *gorm.DB) error {
			return runSQL(tx, sql)
		})
	},
}
```

In `migration_list.go`: append `migration029,` to the list and set `const latestVersion = 29`.

- [ ] **Step 2: Write the model**

`models/scope.go`:

```go
// Scope is one entry in the org-global, resource-agnostic scope catalog.
// The same scope can gate MCP tools and (later) LLM providers; AMS stores
// only the catalog — grants live in each environment's Thunder instance.
type Scope struct {
	ID          uuid.UUID `gorm:"column:id;primaryKey;default:gen_random_uuid()" json:"id"`
	OrgName     string    `gorm:"column:org_name;not null" json:"orgName"`
	Name        string    `gorm:"column:name;not null" json:"name"`
	Description string    `gorm:"column:description;not null;default:''" json:"description"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName returns the table name for the Scope model.
func (Scope) TableName() string { return "scopes" }
```

- [ ] **Step 3: Write the repository**

`repositories/scope_repository.go` — copy the structural shape of `repositories/agent_thunder_client_repository.go` (interface + GORM impl + constructor):

```go
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/scope_repository_mock.go . ScopeRepository:ScopeRepositoryMock
type ScopeRepository interface {
	// List returns every scope in the org's catalog, name-ascending.
	List(ctx context.Context, orgName string) ([]models.Scope, error)
	// GetByName returns gorm.ErrRecordNotFound (wrapped) when absent.
	GetByName(ctx context.Context, orgName, name string) (*models.Scope, error)
	Create(ctx context.Context, scope *models.Scope) error
	Update(ctx context.Context, scope *models.Scope) error
	Delete(ctx context.Context, orgName, name string) error
}

type scopeRepository struct{ db *gorm.DB }

func NewScopeRepository(db *gorm.DB) ScopeRepository { return &scopeRepository{db: db} }

func (r *scopeRepository) List(ctx context.Context, orgName string) ([]models.Scope, error) {
	var scopes []models.Scope
	if err := r.db.WithContext(ctx).Where("org_name = ?", orgName).
		Order("name asc").Find(&scopes).Error; err != nil {
		return nil, fmt.Errorf("failed to list scopes: %w", err)
	}
	return scopes, nil
}

func (r *scopeRepository) GetByName(ctx context.Context, orgName, name string) (*models.Scope, error) {
	var scope models.Scope
	if err := r.db.WithContext(ctx).Where("org_name = ? AND name = ?", orgName, name).
		First(&scope).Error; err != nil {
		return nil, err
	}
	return &scope, nil
}

func (r *scopeRepository) Create(ctx context.Context, scope *models.Scope) error {
	if err := r.db.WithContext(ctx).Create(scope).Error; err != nil {
		return fmt.Errorf("failed to create scope: %w", err)
	}
	return nil
}

func (r *scopeRepository) Update(ctx context.Context, scope *models.Scope) error {
	if err := r.db.WithContext(ctx).Model(&models.Scope{}).
		Where("org_name = ? AND name = ?", scope.OrgName, scope.Name).
		Updates(map[string]interface{}{"description": scope.Description, "updated_at": time.Now()}).
		Error; err != nil {
		return fmt.Errorf("failed to update scope: %w", err)
	}
	return nil
}

func (r *scopeRepository) Delete(ctx context.Context, orgName, name string) error {
	res := r.db.WithContext(ctx).Where("org_name = ? AND name = ?", orgName, name).
		Delete(&models.Scope{})
	if res.Error != nil {
		return fmt.Errorf("failed to delete scope: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
```

- [ ] **Step 4: Generate the mock and verify it compiles**

```bash
cd agent-manager-service && make codegen && go build ./... && go build -tags=integration ./...
```
Expected: `repositories/repomocks/scope_repository_mock.go` generated; both builds pass.

- [ ] **Step 5: Commit**

```bash
git add db_migrations/ models/scope.go repositories/scope_repository.go repositories/repomocks/
git commit -m "feat: add org-global scopes catalog table and repository"
```

---

### Task 4: Scope catalog API (spec-first)

Invoke the `add-api-resource` skill and follow its step order exactly. This task delivers `GET/POST /orgs/{orgName}/scopes` and `PUT/DELETE /orgs/{orgName}/scopes/{scopeName}`.

**Files:**
- Modify: `agent-manager-service/docs/api_v1_openapi.yaml` (paths + schemas)
- Modify: `agent-manager-service/rbac/permissions.go`, `agent-manager-service/rbac/predefined_roles.go`
- Create: `agent-manager-service/services/scope_service.go`
- Create: `agent-manager-service/controllers/scope_controller.go`
- Create: `agent-manager-service/api/scope_routes.go`
- Modify: `agent-manager-service/api/app.go` (register), `agent-manager-service/wiring/wire.go` (+ regenerate `wire_gen.go`)
- Test: `agent-manager-service/services/scope_service_unit_test.go`

**Interfaces:**
- Consumes: `repositories.ScopeRepository` (Task 3); the `MCPProxyRepository` org-scoped list method recorded in Task 1 preflight notes.
- Produces: `services.ScopeService` interface: `List(ctx, orgName) ([]models.Scope, error)`, `Create(ctx, orgName, name, description string) (*models.Scope, error)`, `Update(ctx, orgName, name, description string) (*models.Scope, error)`, `Delete(ctx, orgName, name string) error`. Sentinel behaviors: `utils.ErrInvalidInput` (bad name), `utils.ErrConflict` (duplicate create; delete-while-bound), `gorm.ErrRecordNotFound`→`utils.ErrScopeNotFound` (add this sentinel to `utils/errors.go`). RBAC permissions `rbac.ScopeCreate/ScopeRead/ScopeUpdate/ScopeDelete`.
- Produces (for Task 5 and Task 8): `ScopeService.List` is the catalog source used to validate proxy bindings and role permissions.

- [ ] **Step 1: Spec first — add paths and schemas to `docs/api_v1_openapi.yaml`**

Under `paths:` (alphabetical near the other `/orgs/{orgName}/...` entries):

```yaml
  /orgs/{orgName}/scopes:
    get:
      summary: List scopes
      description: Lists the organization's scope catalog (org-global, resource-agnostic).
      operationId: listScopes
      tags: [Scopes]
      parameters:
        - $ref: "#/components/parameters/orgName"
      responses:
        "200":
          description: Scope list
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ScopeListResponse"
    post:
      summary: Create scope
      operationId: createScope
      tags: [Scopes]
      parameters:
        - $ref: "#/components/parameters/orgName"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/ScopeRequest"
      responses:
        "201":
          description: Scope created
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ScopeResponse"
        "409":
          description: A scope with this name already exists
  /orgs/{orgName}/scopes/{scopeName}:
    put:
      summary: Update scope description
      operationId: updateScope
      tags: [Scopes]
      parameters:
        - $ref: "#/components/parameters/orgName"
        - name: scopeName
          in: path
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/ScopeUpdateRequest"
      responses:
        "200":
          description: Scope updated
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ScopeResponse"
    delete:
      summary: Delete scope
      description: Fails with 409 while the scope is referenced by any MCP proxy environment tool binding.
      operationId: deleteScope
      tags: [Scopes]
      parameters:
        - $ref: "#/components/parameters/orgName"
        - name: scopeName
          in: path
          required: true
          schema:
            type: string
      responses:
        "204":
          description: Scope deleted
        "409":
          description: Scope is referenced by resource bindings
```

Under `components.schemas:`:

```yaml
    ScopeRequest:
      type: object
      required: [name]
      properties:
        name:
          type: string
          pattern: '^[A-Za-z0-9:._\-]{1,256}$'
          example: "repo:read.all"
        description:
          type: string
          example: Read access to all repository tools
    ScopeUpdateRequest:
      type: object
      properties:
        description:
          type: string
    ScopeResponse:
      type: object
      required: [name]
      properties:
        name:
          type: string
        description:
          type: string
        createdAt:
          type: string
          format: date-time
        updatedAt:
          type: string
          format: date-time
        bindingCount:
          type: integer
          description: Number of MCP proxy environment tool bindings referencing this scope
    ScopeListResponse:
      type: object
      required: [scopes]
      properties:
        scopes:
          type: array
          items:
            $ref: "#/components/schemas/ScopeResponse"
```

If the yaml has no reusable `orgName` parameter component, inline it the way sibling paths do (copy from `/orgs/{orgName}/mcp-proxies`).

- [ ] **Step 2: Regenerate spec code**

```bash
cd agent-manager-service && make spec
```
Expected: `spec/` rebuilt with `ScopeRequest`, `ScopeResponse`, etc. Never hand-edit `spec/`.

- [ ] **Step 3: Permissions**

`rbac/permissions.go` — new block after the existing resource blocks:

```go
// Scope catalog permissions
const (
	ScopeCreate Permission = "scope:create"
	ScopeRead   Permission = "scope:read"
	ScopeUpdate Permission = "scope:update"
	ScopeDelete Permission = "scope:delete"
)
```

`rbac/predefined_roles.go` — grant all four to the same predefined role(s) that hold `MCPServerCreate`/`MCPServerUpdate` today, and `ScopeRead` additionally to every role that holds `MCPServerRead` (open the file and mirror the existing placement).

- [ ] **Step 4: Failing unit test first**

`services/scope_service_unit_test.go` (Apache header, `package services`, **no build tag**; mocks per `add-service-unit-test`):

```go
func TestScopeService_Create_ValidatesName(t *testing.T) {
	svc := NewScopeService(&repomocks.ScopeRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{})
	_, err := svc.Create(context.Background(), "org1", "bad name with spaces", "")
	assert.ErrorIs(t, err, utils.ErrInvalidInput)
	_, err = svc.Create(context.Background(), "org1", strings.Repeat("a", 257), "")
	assert.ErrorIs(t, err, utils.ErrInvalidInput)
}

func TestScopeService_Delete_BlockedWhileBound(t *testing.T) {
	scopeRepo := &repomocks.ScopeRepositoryMock{
		GetByNameFunc: func(ctx context.Context, orgName, name string) (*models.Scope, error) {
			return &models.Scope{OrgName: orgName, Name: name}, nil
		},
	}
	bound := models.MCPProxy{Configuration: models.MCPProxyConfig{
		Environments: map[string]models.MCPEnvironmentConfig{
			"3fa85f64-5717-4562-b3fc-2c963f66afa6": {
				ToolScopeBindings: []models.MCPToolScopeBinding{{Tool: "list_repos", Scopes: []string{"repo:read.all"}}},
			},
		},
	}}
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		// Use the org-scoped list method name recorded in the Task 1 preflight notes.
		ListFunc: func(...) ([]models.MCPProxy, error) { return []models.MCPProxy{bound}, nil },
	}
	svc := NewScopeService(scopeRepo, proxyRepo)
	err := svc.Delete(context.Background(), "org1", "repo:read.all")
	assert.ErrorIs(t, err, utils.ErrConflict)
}

func TestScopeService_Delete_UnboundSucceeds(t *testing.T) {
	deleted := false
	scopeRepo := &repomocks.ScopeRepositoryMock{
		GetByNameFunc: func(ctx context.Context, orgName, name string) (*models.Scope, error) {
			return &models.Scope{OrgName: orgName, Name: name}, nil
		},
		DeleteFunc: func(ctx context.Context, orgName, name string) error { deleted = true; return nil },
	}
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		ListFunc: func(...) ([]models.MCPProxy, error) { return []models.MCPProxy{}, nil },
	}
	svc := NewScopeService(scopeRepo, proxyRepo)
	assert.NoError(t, svc.Delete(context.Background(), "org1", "repo:read.all"))
	assert.True(t, deleted)
}
```

Note: `models.MCPToolScopeBinding` does not exist until Task 5. To keep this task self-contained and compiling, **add the model in this task** (it is two structs shared by Tasks 4 and 5 — see Step 5), and Task 5 only wires it into validation/storage/flatten.

Run: `go test -run 'TestScopeService' ./services/` → expected: FAIL (NewScopeService undefined).

- [ ] **Step 5: Implement the model addition and service**

`models/mcp_proxy.go` — add (next to `MCPEnvironmentConfig`):

```go
// MCPToolScopeBinding binds catalog scopes to one MCP tool in one environment.
// Scope names reference the org-global scopes catalog by name.
type MCPToolScopeBinding struct {
	Tool   string   `json:"tool"`
	Scopes []string `json:"scopes"`
}
```

And add the field to both shapes (blueprint block and flat deployable config — same duality as `Security`):

```go
// In MCPEnvironmentConfig:
	ToolScopeBindings []MCPToolScopeBinding `json:"toolScopeBindings,omitempty"`
// In MCPProxyConfig (flat root level, populated only on flattened per-env artifacts):
	ToolScopeBindings []MCPToolScopeBinding `json:"toolScopeBindings,omitempty"`
```

`utils/errors.go` — add `ErrScopeNotFound = errors.New("scope not found")` beside its peers (and `ErrConflict` already exists at `utils/errors.go:105`).

`services/scope_service.go`:

```go
var scopeNameRe = regexp.MustCompile(`^[A-Za-z0-9:._\-]{1,256}$`)

type ScopeService interface {
	List(ctx context.Context, orgName string) ([]models.Scope, error)
	Create(ctx context.Context, orgName, name, description string) (*models.Scope, error)
	Update(ctx context.Context, orgName, name, description string) (*models.Scope, error)
	Delete(ctx context.Context, orgName, name string) error
	// BindingCounts returns scope name → number of MCP proxy environment tool
	// bindings referencing it (console "in use by N" indicator + delete guard).
	BindingCounts(ctx context.Context, orgName string) (map[string]int, error)
}

type scopeService struct {
	repo      repositories.ScopeRepository
	proxyRepo repositories.MCPProxyRepository // org-scoped list method per preflight notes
}

func NewScopeService(repo repositories.ScopeRepository, proxyRepo repositories.MCPProxyRepository) ScopeService {
	return &scopeService{repo: repo, proxyRepo: proxyRepo}
}

func (s *scopeService) Create(ctx context.Context, orgName, name, description string) (*models.Scope, error) {
	if !scopeNameRe.MatchString(name) {
		return nil, fmt.Errorf("%w: scope name must match ^[A-Za-z0-9:._\\-]{1,256}$", utils.ErrInvalidInput)
	}
	if _, err := s.repo.GetByName(ctx, orgName, name); err == nil {
		return nil, fmt.Errorf("%w: scope %q already exists", utils.ErrConflict, name)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to check scope existence: %w", err)
	}
	scope := &models.Scope{OrgName: orgName, Name: name, Description: description}
	if err := s.repo.Create(ctx, scope); err != nil {
		return nil, err
	}
	return scope, nil
}

func (s *scopeService) Delete(ctx context.Context, orgName, name string) error {
	if _, err := s.repo.GetByName(ctx, orgName, name); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return utils.ErrScopeNotFound
		}
		return fmt.Errorf("failed to load scope: %w", err)
	}
	counts, err := s.BindingCounts(ctx, orgName)
	if err != nil {
		return err
	}
	if counts[name] > 0 {
		return fmt.Errorf("%w: scope %q is referenced by %d tool binding(s)", utils.ErrConflict, name, counts[name])
	}
	return s.repo.Delete(ctx, orgName, name)
}

func (s *scopeService) BindingCounts(ctx context.Context, orgName string) (map[string]int, error) {
	proxies, err := s.proxyRepo.List(ctx, orgName) // adapt to the recorded method name
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP proxies for binding scan: %w", err)
	}
	counts := map[string]int{}
	for i := range proxies {
		for _, env := range proxies[i].Configuration.Environments {
			for _, b := range env.ToolScopeBindings {
				for _, sc := range b.Scopes {
					counts[sc]++
				}
			}
		}
	}
	return counts, nil
}
```

`List` is a straight repo passthrough; `Update` = load (map not-found → `utils.ErrScopeNotFound`), set description, `repo.Update`, return. If the recorded `MCPProxyRepository` list method takes different args (e.g. org UUID), adapt the call site here — not the interface.

- [ ] **Step 6: Run the tests**

```bash
go test -run 'TestScopeService' ./services/
```
Expected: PASS (with env vars per the unit-test skill if needed; `make test-unit` also works).

- [ ] **Step 7: Controller and routes**

`controllers/scope_controller.go` — model on the thin handlers in `controllers/identity_controller.go` (`middleware.GetResolvedOrg` is NOT needed here; org comes from the path and RBAC middleware):

```go
type ScopeController interface {
	ListScopes(w http.ResponseWriter, r *http.Request)
	CreateScope(w http.ResponseWriter, r *http.Request)
	UpdateScope(w http.ResponseWriter, r *http.Request)
	DeleteScope(w http.ResponseWriter, r *http.Request)
}

type scopeController struct{ svc services.ScopeService }

func NewScopeController(svc services.ScopeService) ScopeController { return &scopeController{svc: svc} }

func (c *scopeController) CreateScope(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgName := r.PathValue("orgName")
	var body spec.ScopeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	desc := ""
	if body.Description != nil {
		desc = *body.Description
	}
	scope, err := c.svc.Create(ctx, orgName, body.Name, desc)
	switch {
	case errors.Is(err, utils.ErrInvalidInput):
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, utils.ErrConflict):
		utils.WriteErrorResponse(w, http.StatusConflict, err.Error())
	case err != nil:
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to create scope")
	default:
		utils.WriteSuccessResponse(w, http.StatusCreated, scope)
	}
}
```

`ListScopes` returns `{scopes: [...]}` where each item carries `bindingCount` from `svc.BindingCounts` (one call, join in memory). `DeleteScope`: 204 on success, 404 for `utils.ErrScopeNotFound`, 409 for `utils.ErrConflict`. `UpdateScope`: 200/404.

`api/scope_routes.go`:

```go
func registerScopeRoutes(rr *middleware.RouteRegistrar, ctrl controllers.ScopeController) {
	rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/scopes", rbac.ScopeRead, ctrl.ListScopes)
	rr.HandleFuncWithValidationAndAuthz("POST /orgs/{orgName}/scopes", rbac.ScopeCreate, ctrl.CreateScope)
	rr.HandleFuncWithValidationAndAuthz("PUT /orgs/{orgName}/scopes/{scopeName}", rbac.ScopeUpdate, ctrl.UpdateScope)
	rr.HandleFuncWithValidationAndAuthz("DELETE /orgs/{orgName}/scopes/{scopeName}", rbac.ScopeDelete, ctrl.DeleteScope)
}
```

Register in `api/app.go` beside `registerIdentityRoutes(rr, params.IdentityController)` (add `ScopeController` to the params struct). Add wire providers for `NewScopeRepository`, `NewScopeService`, `NewScopeController` in `wiring/wire.go`, then:

```bash
make codegen && go build ./...
```

- [ ] **Step 8: Full verification and commit**

```bash
make test-unit && go build -tags=integration ./... && golangci-lint run --config .github/linters/.golangci.yaml ./...
git add docs/api_v1_openapi.yaml spec/ rbac/ services/ controllers/ api/ wiring/ models/mcp_proxy.go utils/errors.go
git commit -m "feat: add org-global scope catalog API"
```

---

### Task 5: Identity security variant, per-env bindings, proxy validation

Adds the `identity` variant to the shared `SecurityConfig`, wires `ToolScopeBindings` through storage/flatten, and validates identity-mode configuration on proxy create/update.

**Files:**
- Modify: `agent-manager-service/models/llm_provider.go` (SecurityConfig + new IdentitySecurity)
- Modify: `agent-manager-service/docs/api_v1_openapi.yaml` (SecurityConfig schema + MCPEnvironmentConfig schema; `environments` on MCPProxyRequest/Response if the Task 1 check found it absent)
- Modify: `agent-manager-service/services/mcp_proxy_service.go` (defaulting, validation, storage build)
- Modify: `agent-manager-service/services/mcp_proxy_deployment.go` (flatten)
- Modify: `agent-manager-service/wiring/wire.go` (MCPProxyService gains ScopeRepository dep)
- Test: `agent-manager-service/services/mcp_proxy_service_unit_test.go` (or the existing service test file if one covers these funcs — check `services/mcp_proxy_service_test.go` first and follow its tier)

**Interfaces:**
- Consumes: `models.MCPToolScopeBinding` (added in Task 4), `repositories.ScopeRepository` (Task 3), `resolveGatewayForEnvironment` + `extractGatewayPolicyManifestItems` (existing, PR #1258).
- Produces: `models.IdentitySecurity{Enabled *bool}`; `SecurityConfig.Identity *IdentitySecurity`; helper `isMCPIdentitySecurityEnabled(security *models.SecurityConfig) bool` (used by Task 6); validation error paths (400 `utils.ErrInvalidInput`) for: unknown binding scope, apiKey+identity both enabled, identity mode on a gateway lacking `mcp-auth`/`mcp-authz` v1.

- [ ] **Step 1: Failing tests first**

Add to the mcp proxy service unit tests (same package/tier conventions as Task 4's test):

```go
func TestDefaultMCPProxySecurity_IdentityVariantSkipsAPIKeyDefaults(t *testing.T) {
	enabled := true
	out := defaultMCPProxySecurity(&models.SecurityConfig{
		Enabled:  &enabled,
		Identity: &models.IdentitySecurity{Enabled: &enabled},
	})
	assert.Nil(t, out.APIKey, "identity mode must not default an API key on")
	assert.NotNil(t, out.Identity)
}

func TestValidateMCPEnvironments_RejectsBothVariantsEnabled(t *testing.T) {
	enabled := true
	envs := map[string]models.MCPEnvironmentConfig{
		"3fa85f64-5717-4562-b3fc-2c963f66afa6": {
			Upstream: &models.UpstreamEndpoint{URL: "https://mcp.example.com"},
			Security: &models.SecurityConfig{
				Enabled:  &enabled,
				APIKey:   &models.APIKeySecurity{Enabled: &enabled},
				Identity: &models.IdentitySecurity{Enabled: &enabled},
			},
		},
	}
	err := validateMCPEnvironments(context.Background(), envs)
	assert.ErrorIs(t, err, utils.ErrInvalidInput)
}

func TestValidateMCPEnvironmentSecurity_UnknownBindingScope(t *testing.T) {
	// service with ScopeRepositoryMock returning only {"repo:read.all"};
	// env binds "repo:write.all" -> expect utils.ErrInvalidInput mentioning the scope name.
}

func TestValidateMCPEnvironmentSecurity_IdentityNeedsGatewayPolicies(t *testing.T) {
	// gateway manifest WITHOUT mcp-authz -> identity-mode env rejected with utils.ErrInvalidInput.
	// Build the gateway mock so resolveGatewayForEnvironment finds an active gateway whose
	// Manifest lacks the policies; mirror how existing deployment tests fake gateways
	// (see mcp_proxy_deployment_test.go for the gateway fixture shape).
}
```

Run: `go test -run 'TestDefaultMCPProxySecurity_Identity|TestValidateMCPEnvironment' ./services/` → FAIL (IdentitySecurity undefined).

- [ ] **Step 2: Model + spec changes**

`models/llm_provider.go`:

```go
// SecurityConfig represents security configuration. Exactly one variant is
// active: apiKey or identity (both nil / enabled=false => no security). The
// identity variant is a shared primitive — LLM providers may adopt it later.
type SecurityConfig struct {
	Enabled  *bool             `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	APIKey   *APIKeySecurity   `json:"apiKey,omitempty" yaml:"apiKey,omitempty"`
	Identity *IdentitySecurity `json:"identity,omitempty" yaml:"identity,omitempty"`
}

// IdentitySecurity selects Agent Identity security: callers authenticate with a
// JWT from the environment's Thunder IdP. v1 pins the issuer to the gateway
// key-manager named ThunderKeyManager, so there is no issuer field yet.
type IdentitySecurity struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}
```

`docs/api_v1_openapi.yaml`:

```yaml
    # SecurityConfig gains:
        identity:
          $ref: "#/components/schemas/IdentitySecurity"
    IdentitySecurity:
      type: object
      description: Agent Identity security — callers present a JWT issued by the environment's IdP.
      properties:
        enabled:
          type: boolean
    MCPToolScopeBinding:
      type: object
      required: [tool, scopes]
      properties:
        tool:
          type: string
          example: list_repos
        scopes:
          type: array
          items:
            type: string
          example: ["repo:read.all"]
    MCPEnvironmentConfig:
      type: object
      description: Per-environment blueprint block of an MCP proxy, keyed by environment UUID.
      properties:
        artifactUuid:
          type: string
          format: uuid
          readOnly: true
        upstream:
          $ref: "#/components/schemas/UpstreamEndpoint"
        policies:
          type: array
          items:
            $ref: "#/components/schemas/MCPPolicy"
        capabilities:
          $ref: "#/components/schemas/MCPProxyCapabilities"
        security:
          $ref: "#/components/schemas/SecurityConfig"
        toolScopeBindings:
          type: array
          items:
            $ref: "#/components/schemas/MCPToolScopeBinding"
        deploymentStatus:
          type: string
          readOnly: true
```

If Task 1 found `MCPProxyRequest`/`MCPProxyResponse` lack `environments` (true on the revamp branch at planning time), add to both:

```yaml
        environments:
          type: object
          description: Per-environment configuration blocks keyed by environment UUID
          additionalProperties:
            $ref: "#/components/schemas/MCPEnvironmentConfig"
```

(If a `UpstreamEndpoint` schema name differs, reuse whatever `MCPProxyRequest.upstream` references for a single endpoint.) Then `make spec`.

- [ ] **Step 3: Implement defaulting + validation + storage/flatten**

`services/mcp_proxy_service.go` — rewrite `defaultMCPProxySecurity` (current body at the `func defaultMCPProxySecurity` anchor):

```go
func defaultMCPProxySecurity(security *models.SecurityConfig) *models.SecurityConfig {
	enabled := true
	if security == nil {
		return &models.SecurityConfig{
			Enabled: &enabled,
			APIKey:  &models.APIKeySecurity{Enabled: &enabled, Key: "X-API-Key", In: "header"},
		}
	}
	out := *security
	if out.Enabled == nil {
		out.Enabled = &enabled
	}
	if !isBoolTrue(out.Enabled) {
		return &out
	}
	if out.Identity != nil && isBoolTrue(out.Identity.Enabled) {
		// Agent Identity mode: no API-key defaulting. Mutual exclusion is
		// enforced in validateMCPEnvironments before this runs.
		identity := *out.Identity
		out.Identity = &identity
		out.APIKey = nil
		return &out
	}
	// ... keep the existing APIKey-defaulting tail unchanged ...
}
```

Extend `validateMCPEnvironments` (same function, after the upstream URL checks) with per-env structural checks:

```go
		if sec := env.Security; sec != nil && isBoolTrue(sec.Enabled) {
			apiKeyOn := sec.APIKey != nil && isBoolTrue(sec.APIKey.Enabled)
			identityOn := sec.Identity != nil && isBoolTrue(sec.Identity.Enabled)
			if apiKeyOn && identityOn {
				return fmt.Errorf("%w: environment %q: apiKey and identity security are mutually exclusive", utils.ErrInvalidInput, envID)
			}
		}
		for _, b := range env.ToolScopeBindings {
			if strings.TrimSpace(b.Tool) == "" {
				return fmt.Errorf("%w: environment %q has a tool binding with an empty tool name", utils.ErrInvalidInput, envID)
			}
			if len(b.Scopes) == 0 {
				return fmt.Errorf("%w: environment %q: tool %q binding has no scopes", utils.ErrInvalidInput, envID, b.Tool)
			}
		}
```

New method on the service (called from `Create` and `Update` right after `validateMCPEnvironments`; add `scopeRepo repositories.ScopeRepository` to the `MCPProxyService` struct + constructor + wire):

```go
// validateMCPEnvironmentSecurity enforces the cross-resource identity-mode rules:
// every bound scope exists in the org catalog, and an identity-mode environment's
// gateway advertises mcp-auth v1 + mcp-authz v1 in its policy manifest.
// Bindings to tools absent from capabilities are deliberately accepted (tool
// lists drift; the console flags them).
func (s *MCPProxyService) validateMCPEnvironmentSecurity(ctx context.Context, orgName string, environments map[string]models.MCPEnvironmentConfig) error {
	catalog, err := s.scopeRepo.List(ctx, orgName)
	if err != nil {
		return fmt.Errorf("failed to load scope catalog: %w", err)
	}
	known := make(map[string]struct{}, len(catalog))
	for _, sc := range catalog {
		known[sc.Name] = struct{}{}
	}
	for envID, env := range environments {
		for _, b := range env.ToolScopeBindings {
			for _, scName := range b.Scopes {
				if _, ok := known[scName]; !ok {
					return fmt.Errorf("%w: environment %q: tool %q references unknown scope %q", utils.ErrInvalidInput, envID, b.Tool, scName)
				}
			}
		}
		if env.Security == nil || !isBoolTrue(env.Security.Enabled) ||
			env.Security.Identity == nil || !isBoolTrue(env.Security.Identity.Enabled) {
			continue
		}
		envUUID, err := uuid.Parse(strings.TrimSpace(envID))
		if err != nil {
			continue // already rejected by validateMCPEnvironments
		}
		gateway, err := s.resolveGatewayForEnvironment(ctx, envUUID, orgName)
		if errors.Is(err, errNoActiveGatewayForEnvironment) {
			continue // no gateway yet: allowed, deploys later; policies checked when one exists
		}
		if err != nil {
			return fmt.Errorf("environment %q: resolve gateway: %w", envID, err)
		}
		if !gatewayHasMCPIdentityPolicies(gateway) {
			return fmt.Errorf("%w: environment %q: its gateway does not support mcp-auth/mcp-authz v1 policies required for Agent Identity security", utils.ErrInvalidInput, envID)
		}
	}
	return nil
}

func gatewayHasMCPIdentityPolicies(gateway *models.Gateway) bool {
	need := map[string]bool{"mcp-auth\x00v1": false, "mcp-authz\x00v1": false}
	for _, item := range extractGatewayPolicyManifestItems(gateway.Manifest) {
		key := item.Name + "\x00" + normalizePolicyVersionToMajor(item.Version)
		if _, ok := need[key]; ok {
			need[key] = true
		}
	}
	return need["mcp-auth\x00v1"] && need["mcp-authz\x00v1"]
}
```

`buildMCPEnvironmentsForStorage` — inside the per-env block construction, carry bindings:

```go
		block := models.MCPEnvironmentConfig{
			ArtifactUUID:      &artifactUUID,
			Policies:          copyMCPPolicies(incomingEnv.Policies),
			Capabilities:      copyMCPCapabilities(incomingEnv.Capabilities),
			Security:          defaultMCPProxySecurity(incomingEnv.Security),
			ToolScopeBindings: copyMCPToolScopeBindings(incomingEnv.ToolScopeBindings),
		}
```

with the helper (beside `copyMCPPolicies`):

```go
func copyMCPToolScopeBindings(bindings []models.MCPToolScopeBinding) []models.MCPToolScopeBinding {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]models.MCPToolScopeBinding, 0, len(bindings))
	for _, b := range bindings {
		scopes := make([]string, len(b.Scopes))
		copy(scopes, b.Scopes)
		out = append(out, models.MCPToolScopeBinding{Tool: b.Tool, Scopes: scopes})
	}
	return out
}
```

`services/mcp_proxy_deployment.go` — `buildMCPProxyEnvArtifact`'s `Configuration:` literal gains one line beside `Security: envCfg.Security`:

```go
			ToolScopeBindings: envCfg.ToolScopeBindings,
```

- [ ] **Step 4: Run tests**

```bash
go test -run 'TestDefaultMCPProxySecurity|TestValidateMCPEnvironment' ./services/ && make test-unit
```
Expected: PASS (including the pre-existing security-defaulting tests — if any assert the old nil-Identity shape, update them to the new struct, keeping their behavioral assertions).

- [ ] **Step 5: Commit**

```bash
git add models/ docs/api_v1_openapi.yaml spec/ services/ wiring/
git commit -m "feat: add identity security variant and per-env tool scope bindings to MCP proxies"
```

---

### Task 6: Identity policy YAML emission

**Files:**
- Modify: `agent-manager-service/services/mcp_proxy_deployment.go`
- Test: `agent-manager-service/services/mcp_proxy_deployment_test.go`

**Interfaces:**
- Consumes: `models.MCPToolScopeBinding`, `SecurityConfig.Identity` (Task 5); `buildMCPProxyDeploymentYAML` + `appendMCPAPIKeyAuthPolicy` (existing).
- Produces: `appendMCPIdentityAuthPolicies(policies []models.MCPPolicy, security *models.SecurityConfig, bindings []models.MCPToolScopeBinding) []models.MCPPolicy`.

- [ ] **Step 1: Failing tests first** (table style, matching the file's existing tests)

```go
func TestAppendMCPIdentityAuthPolicies(t *testing.T) {
	enabled := true
	identity := &models.SecurityConfig{Enabled: &enabled, Identity: &models.IdentitySecurity{Enabled: &enabled}}
	bindings := []models.MCPToolScopeBinding{
		{Tool: "list_repos", Scopes: []string{"repo:read.all"}},
		{Tool: "create_issue", Scopes: []string{"repo:write.all", "repo:read.all"}},
	}

	t.Run("disabled security emits nothing", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, nil, bindings)
		assert.Empty(t, out)
	})

	t.Run("api-key security emits nothing", func(t *testing.T) {
		sec := &models.SecurityConfig{Enabled: &enabled, APIKey: &models.APIKeySecurity{Enabled: &enabled}}
		assert.Empty(t, appendMCPIdentityAuthPolicies(nil, sec, bindings))
	})

	t.Run("identity mode emits mcp-auth with sorted scope union and per-tool mcp-authz", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, identity, bindings)
		assert.Len(t, out, 2)
		assert.Equal(t, "mcp-auth", out[0].Name)
		assert.Equal(t, []interface{}{"ThunderKeyManager"}, out[0].Params["issuers"])
		assert.Equal(t, []string{"repo:read.all", "repo:write.all"}, out[0].Params["requiredScopes"])
		assert.Equal(t, "mcp-authz", out[1].Name)
		tools := out[1].Params["tools"].([]map[string]interface{})
		assert.Len(t, tools, 2)
		assert.Equal(t, "list_repos", tools[0]["name"])
	})

	t.Run("no bindings: mcp-auth only, no requiredScopes, no mcp-authz", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, identity, nil)
		assert.Len(t, out, 1)
		assert.Equal(t, "mcp-auth", out[0].Name)
		_, hasScopes := out[0].Params["requiredScopes"]
		assert.False(t, hasScopes)
	})

	t.Run("binding with empty scopes list is skipped", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, identity, []models.MCPToolScopeBinding{{Tool: "x", Scopes: nil}})
		assert.Len(t, out, 1) // auth only — unbound tools stay authenticated-only
	})
}
```

Run: `go test -run 'TestAppendMCPIdentityAuthPolicies' ./services/` → FAIL (undefined).

- [ ] **Step 2: Implement**

In `services/mcp_proxy_deployment.go`, beside `appendMCPAPIKeyAuthPolicy`:

```go
const (
	mcpAuthPolicyName           = "mcp-auth"
	mcpAuthPolicyVersion        = "v1"
	mcpAuthzPolicyName          = "mcp-authz"
	mcpAuthzPolicyVersion       = "v1"
	mcpIdentityIssuerKeyManager = "ThunderKeyManager"
)

// appendMCPIdentityAuthPolicies emits the Agent Identity gateway policies for a
// flattened per-environment artifact: mcp-auth (JWT validation against the
// ThunderKeyManager key manager; requiredScopes is metadata advertisement only)
// and mcp-authz (per-tool requiredScopes enforcement). Tools without bindings
// get no rule — gateway default-permit means authenticated-only.
func appendMCPIdentityAuthPolicies(policies []models.MCPPolicy, security *models.SecurityConfig, bindings []models.MCPToolScopeBinding) []models.MCPPolicy {
	if security == nil || !isBoolTrue(security.Enabled) ||
		security.Identity == nil || !isBoolTrue(security.Identity.Enabled) {
		return policies
	}

	scopeSet := map[string]struct{}{}
	toolRules := make([]map[string]interface{}, 0, len(bindings))
	for _, b := range bindings {
		if strings.TrimSpace(b.Tool) == "" || len(b.Scopes) == 0 {
			continue
		}
		scopes := make([]string, 0, len(b.Scopes))
		for _, sc := range b.Scopes {
			if sc == "" {
				continue
			}
			scopes = append(scopes, sc)
			scopeSet[sc] = struct{}{}
		}
		if len(scopes) == 0 {
			continue
		}
		toolRules = append(toolRules, map[string]interface{}{"name": b.Tool, "requiredScopes": scopes})
	}

	authParams := map[string]interface{}{
		"issuers": []interface{}{mcpIdentityIssuerKeyManager},
	}
	if len(scopeSet) > 0 {
		union := make([]string, 0, len(scopeSet))
		for sc := range scopeSet {
			union = append(union, sc)
		}
		sort.Strings(union)
		authParams["requiredScopes"] = union
	}

	out := make([]models.MCPPolicy, 0, len(policies)+2)
	out = append(out, policies...)
	out = append(out, models.MCPPolicy{Name: mcpAuthPolicyName, Version: mcpAuthPolicyVersion, Params: authParams})
	if len(toolRules) > 0 {
		out = append(out, models.MCPPolicy{Name: mcpAuthzPolicyName, Version: mcpAuthzPolicyVersion, Params: map[string]interface{}{"tools": toolRules}})
	}
	return out
}
```

Wire into `buildMCPProxyDeploymentYAML`, directly after the `appendMCPAPIKeyAuthPolicy` call:

```go
	policies = appendMCPIdentityAuthPolicies(policies, proxy.Configuration.Security, proxy.Configuration.ToolScopeBindings)
```

Keep the tool-rule order deterministic: iterate `bindings` in slice order (storage preserves the client's order); the test asserts that.

- [ ] **Step 3: Run tests, then the whole deployment suite**

```bash
go test -run 'TestAppendMCPIdentityAuthPolicies' ./services/ && go test ./services/ -run 'MCPProxy' && make test-unit
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add services/mcp_proxy_deployment.go services/mcp_proxy_deployment_test.go
git commit -m "feat: emit mcp-auth/mcp-authz policies for identity-secured MCP proxy environments"
```

---

### Task 7: thundersvc extensions — typed group members, scope resource-server ensure, identity resolver

**Files:**
- Modify: `agent-manager-service/clients/thundersvc/identity_client.go` (interface + methods)
- Modify: `agent-manager-service/clients/thundersvc/env_resolver.go` (resolver interface + method)
- Modify: `agent-manager-service/clients/thundersvc/identity_types.go` (only if the spike recorded new request shapes)
- Test: `agent-manager-service/clients/thundersvc/identity_client_test.go` additions (httptest, same style as `client_test.go`)
- Regenerate: `clients/clientmocks/` via `make codegen`

**Interfaces:**
- Consumes: spike findings file (Task 2) for the exact resource-server/resource/action endpoints and payloads.
- Produces (all on `IdentityClient`, implemented by `*thunderClient`):
  - `AddGroupMemberEntries(ctx context.Context, groupID string, members []GroupMember) error`
  - `RemoveGroupMemberEntries(ctx context.Context, groupID string, members []GroupMember) error`
  - `ListGroupMemberEntries(ctx context.Context, groupID string, offset, limit int) ([]GroupMember, int, error)` (raw typed entries — `GetGroupMembers` returns users only)
  - `EnsureScopeResourceServer(ctx context.Context, scopes []string) (resourceServerID string, err error)`
- Produces (resolver): `EnvIdentityClient interface { IdentityClient; GetDefaultOUID(ctx context.Context) (string, error) }` and `EnvThunderResolver.ResolveIdentity(ctx, orgName, envName) (EnvIdentityClient, error)`.
- Constant: `const scopeResourceServerIdentifier = "amp-scopes"`.

- [ ] **Step 1: Failing tests first**

Add httptest-backed tests following the existing pattern in `clients/thundersvc/client_test.go` / `identity_client_test.go` (spin an `httptest.Server`, assert method/path/body, return canned JSON):

```go
func TestAddGroupMemberEntries_SendsAgentType(t *testing.T) {
	// server asserts POST /groups/g1/members/add with body {"members":[{"id":"a1","type":"agent"}]}
}

func TestEnsureScopeResourceServer_CreatesWhenMissing(t *testing.T) {
	// server: GET /resource-servers -> empty list; expects POST /resource-servers with
	// identifier "amp-scopes"; then resource/action ensures for ["repo:read.all"];
	// returns the created IDs. Assert returned rsID and that each scope's
	// action/permission was created exactly once.
}

func TestEnsureScopeResourceServer_IdempotentWhenPresent(t *testing.T) {
	// server: RS exists with the permission already registered -> NO creation calls.
}
```

Run → FAIL (methods undefined).

- [ ] **Step 2: Implement the typed group-member methods**

In `identity_client.go` (beside `AddGroupMembers`; the existing user-typed methods stay untouched for the org-identity controller):

```go
// AddGroupMemberEntries adds typed members (agents, users, nested groups) to a
// group. Unlike AddGroupMembers it does not assume Type "user".
func (c *thunderClient) AddGroupMemberEntries(ctx context.Context, groupID string, members []GroupMember) error {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, http.MethodPost, c.baseURL+"/groups/"+groupID+"/members/add", token, GroupMembersRequest{Members: members})
	if err != nil {
		return fmt.Errorf("thunder add group member entries: %w", err)
	}
	return nil
}
```

`RemoveGroupMemberEntries` mirrors it against `/members/remove`. `ListGroupMemberEntries` follows the `GetGroupMembers` implementation (`identity_client.go:367`) but returns `page.Members` (`[]GroupMember`) and total directly instead of resolving users.

- [ ] **Step 3: Implement `EnsureScopeResourceServer`**

Written against the endpoints recorded by the Task 2 spike (the shape below matches `ListAMPPermissions`'s read paths — adjust field names only if the spike record differs):

```go
const scopeResourceServerIdentifier = "amp-scopes"

// EnsureScopeResourceServer makes sure the amp-scopes resource server exists and
// that every given scope name is registered as a permission under it, then
// returns the resource server ID. Idempotent; called lazily before role writes.
func (c *thunderClient) EnsureScopeResourceServer(ctx context.Context, scopes []string) (string, error) {
	token, err := c.getSystemToken(ctx)
	if err != nil {
		return "", err
	}

	rsID, err := c.findResourceServerID(ctx, token, scopeResourceServerIdentifier)
	if err != nil {
		return "", err
	}
	if rsID == "" {
		body, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/resource-servers", token,
			map[string]string{"name": "AMP Scopes", "identifier": scopeResourceServerIdentifier})
		if err != nil {
			return "", fmt.Errorf("thunder create scope resource server: %w", err)
		}
		var created ThunderResourceServer
		if err := json.Unmarshal(body, &created); err != nil {
			return "", fmt.Errorf("thunder create scope resource server decode: %w", err)
		}
		rsID = created.ID
	}

	existing, err := c.listResourceServerPermissions(ctx, token, rsID) // set of permission strings
	if err != nil {
		return "", err
	}
	for _, scope := range scopes {
		if _, ok := existing[scope]; ok {
			continue
		}
		if err := c.createScopePermission(ctx, token, rsID, scope); err != nil {
			return "", err
		}
	}
	return rsID, nil
}
```

`findResourceServerID` = extract the RS-pagination loop already inside `ListAMPPermissions` into a shared helper (parameterized by identifier) and reuse it there — don't duplicate it. `listResourceServerPermissions` = the resources+actions read loops from `ListAMPPermissions`, returning `map[string]struct{}` of permission strings. `createScopePermission` = the resource/action creation calls exactly as the spike recorded them (single umbrella resource with explicit per-action `permission` strings, or whatever shape Thunder 0.45 accepted).

Add all four methods to the `IdentityClient` interface block.

- [ ] **Step 4: Resolver identity surface**

`env_resolver.go`:

```go
// EnvIdentityClient is the env-Thunder surface the agent-identity passthrough
// needs: full identity management plus default-OU lookup.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg clientmocks -out ../clientmocks/env_identity_client_mock.go . EnvIdentityClient:EnvIdentityClientMock
type EnvIdentityClient interface {
	IdentityClient
	GetDefaultOUID(ctx context.Context) (string, error)
}

type EnvThunderResolver interface {
	Resolve(ctx context.Context, orgName, envName string) (ThunderClient, error)
	// ResolveIdentity returns the same resolved client widened to identity
	// operations (the concrete client implements both interfaces).
	ResolveIdentity(ctx context.Context, orgName, envName string) (EnvIdentityClient, error)
}

func (r *envThunderResolver) ResolveIdentity(ctx context.Context, orgName, envName string) (EnvIdentityClient, error) {
	c, err := r.Resolve(ctx, orgName, envName)
	if err != nil {
		return nil, err
	}
	ic, ok := c.(EnvIdentityClient)
	if !ok {
		return nil, fmt.Errorf("resolved thunder client for %s/%s does not support identity operations", orgName, envName)
	}
	return ic, nil
}
```

- [ ] **Step 5: Regenerate mocks, run tests**

```bash
make codegen   # regenerates clientmocks (EnvThunderResolverMock gains ResolveIdentityFunc; IdentityClient mocks gain the new methods)
go test ./clients/thundersvc/... && make test-unit && go build -tags=integration ./...
```
Expected: PASS. Fix any existing fake that hand-implements `EnvThunderResolver` (`clients/clientmocks/env_thunder_resolver_fake.go` is moq-generated — regen covers it).

- [ ] **Step 6: Commit**

```bash
git add clients/
git commit -m "feat: add typed group members, scope resource-server ensure, and identity resolver to thundersvc"
```

---

### Task 8: Agent-identity passthrough API (groups, roles, agents picker)

Invoke `add-api-resource`. Routes mirror `/identities/*` shapes under `/orgs/{orgName}/environments/{envName}/agent-identities/*`, controller modeled on `controllers/identity_controller.go` but resolving the client per request via `EnvThunderResolver.ResolveIdentity`.

**Files:**
- Modify: `agent-manager-service/docs/api_v1_openapi.yaml`
- Modify: `agent-manager-service/rbac/permissions.go`, `rbac/predefined_roles.go`
- Modify: `agent-manager-service/repositories/agent_thunder_client_repository.go` (one new method)
- Create: `agent-manager-service/controllers/agent_identity_controller.go`
- Create: `agent-manager-service/api/agent_identity_routes.go`
- Modify: `agent-manager-service/api/app.go`, `agent-manager-service/wiring/wire.go`
- Test: `agent-manager-service/controllers/agent_identity_controller_unit_test.go`

**Interfaces:**
- Consumes: `EnvThunderResolver.ResolveIdentity` + `EnvIdentityClient` + `EnsureScopeResourceServer` + `AddGroupMemberEntries`/`RemoveGroupMemberEntries`/`ListGroupMemberEntries` (Task 7); `repositories.ScopeRepository` (Task 3); `thundersvc` types (`ThunderGroup`, `ThunderRole`, `GroupMember`, `AssignmentEntry`, `RolePermissionRequest`).
- Produces: routes below; `AgentThunderClientRepository.FindByOrgAndEnvironment(ctx, orgName, environmentName string) ([]models.AgentThunderClient, error)`; RBAC perms `AgentIdentityRead/Create/Update/Delete`.

Routes (all under `/orgs/{orgName}/environments/{envName}/agent-identities`):

| Route | Handler | Perm |
|---|---|---|
| `GET .../groups` | ListGroups | AgentIdentityRead |
| `POST .../groups` | CreateGroup | AgentIdentityCreate |
| `GET .../groups/{groupID}` | GetGroup | AgentIdentityRead |
| `PUT .../groups/{groupID}` | UpdateGroup | AgentIdentityUpdate |
| `DELETE .../groups/{groupID}` | DeleteGroup | AgentIdentityDelete |
| `GET .../groups/{groupID}/members` | GetGroupMembers | AgentIdentityRead |
| `POST .../groups/{groupID}/members/add` | AddGroupMembers | AgentIdentityUpdate |
| `POST .../groups/{groupID}/members/remove` | RemoveGroupMembers | AgentIdentityUpdate |
| `GET .../groups/{groupID}/roles` | GetGroupRoles | AgentIdentityRead |
| `GET .../roles` | ListRoles | AgentIdentityRead |
| `POST .../roles` | CreateRole | AgentIdentityCreate |
| `GET .../roles/{roleID}` | GetRole | AgentIdentityRead |
| `PUT .../roles/{roleID}` | UpdateRole | AgentIdentityUpdate |
| `DELETE .../roles/{roleID}` | DeleteRole | AgentIdentityDelete |
| `GET .../roles/{roleID}/assignments` | GetRoleAssignments | AgentIdentityRead |
| `POST .../roles/{roleID}/assignments/add` | AddRoleAssignees | AgentIdentityUpdate |
| `POST .../roles/{roleID}/assignments/remove` | RemoveRoleAssignees | AgentIdentityUpdate |
| `GET .../agents` | ListAgents | AgentIdentityRead |

- [ ] **Step 1: Spec first**

Add the paths above to `docs/api_v1_openapi.yaml` with an `envName` path parameter alongside `orgName`. Request/response schemas:

```yaml
    AgentIdentityGroupRequest:
      type: object
      required: [name]
      properties:
        name: { type: string }
        description: { type: string }
    AgentIdentityMembersRequest:
      type: object
      required: [agentIds]
      properties:
        agentIds:
          type: array
          items: { type: string }
          description: Thunder agent IDs (from the agents picker)
    AgentIdentityRoleRequest:
      type: object
      required: [name]
      properties:
        name: { type: string }
        description: { type: string }
        scopes:
          type: array
          items: { type: string }
          description: Catalog scope names carried as the role's permissions
    AgentIdentityAssignmentsRequest:
      type: object
      required: [assignments]
      properties:
        assignments:
          type: array
          items:
            type: object
            required: [id, type]
            properties:
              id: { type: string }
              type: { type: string, enum: [agent, group] }
    AgentIdentityAgentResponse:
      type: object
      required: [agentName, projectName, status]
      properties:
        agentName: { type: string }
        projectName: { type: string }
        status:
          type: string
          description: Thunder binding status (pending/in_progress/completed/failed)
        thunderAgentId: { type: string }
    AgentIdentityAgentListResponse:
      type: object
      required: [agents]
      properties:
        agents:
          type: array
          items:
            $ref: "#/components/schemas/AgentIdentityAgentResponse"
```

Group/role/assignment *responses* pass through the thundersvc JSON shapes (like the identities endpoints do — no spec response schemas needed beyond a generic object if that is the identities precedent; mirror it). Then `make spec`.

- [ ] **Step 2: Permissions**

```go
// Agent identity (env-Thunder agent groups/roles) permissions
const (
	AgentIdentityRead   Permission = "agent-identity:read"
	AgentIdentityCreate Permission = "agent-identity:create"
	AgentIdentityUpdate Permission = "agent-identity:update"
	AgentIdentityDelete Permission = "agent-identity:delete"
)
```

Grant in `predefined_roles.go` to the same roles that hold the org-identity `GroupCreate`/`RoleCreate` family.

- [ ] **Step 3: Repository method**

`repositories/agent_thunder_client_repository.go` — add to the interface and impl:

```go
	// FindByOrgAndEnvironment returns every binding row for the org+environment,
	// the source for the agent-identity assignment picker (name, status, ThunderAgentID).
	FindByOrgAndEnvironment(ctx context.Context, orgName, environmentName string) ([]models.AgentThunderClient, error)
```

```go
func (r *agentThunderClientRepository) FindByOrgAndEnvironment(ctx context.Context, orgName, environmentName string) ([]models.AgentThunderClient, error) {
	var rows []models.AgentThunderClient
	if err := r.db.WithContext(ctx).
		Where("org_name = ? AND environment_name = ?", orgName, environmentName).
		Order("project_name asc, agent_name asc").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list agent thunder bindings for %s/%s: %w", orgName, environmentName, err)
	}
	return rows, nil
}
```

`make codegen` regenerates the repo mock.

- [ ] **Step 4: Failing controller tests first**

`controllers/agent_identity_controller_unit_test.go` — the two behaviors unique to this controller (the rest is passthrough identical to the already-shipped identity controller):

```go
func TestAgentIdentityCreateRole_LazyEnsuresScopesBeforeRoleWrite(t *testing.T) {
	var calls []string
	envClient := &clientmocks.EnvIdentityClientMock{ // generated in Task 7's make codegen; if moq did not
		// generate one (no //go:generate for it), add the directive above EnvIdentityClient and regen.
		GetDefaultOUIDFunc: func(ctx context.Context) (string, error) { return "ou-1", nil },
		EnsureScopeResourceServerFunc: func(ctx context.Context, scopes []string) (string, error) {
			calls = append(calls, "ensure")
			assert.ElementsMatch(t, []string{"repo:read.all"}, scopes)
			return "rs-1", nil
		},
		CreateRoleFunc: func(ctx context.Context, req thundersvc.CreateRoleRequest) (*thundersvc.ThunderRole, error) {
			calls = append(calls, "create")
			return &thundersvc.ThunderRole{ID: "role-1", Name: req.Name}, nil
		},
		AddRolePermissionsFunc: func(ctx context.Context, roleID string, req thundersvc.RolePermissionRequest) error {
			calls = append(calls, "perms")
			assert.Equal(t, "rs-1", req.ResourceServerID)
			return nil
		},
	}
	resolver := &clientmocks.EnvThunderResolverMock{
		ResolveIdentityFunc: func(ctx context.Context, orgName, envName string) (thundersvc.EnvIdentityClient, error) {
			return envClient, nil
		},
	}
	scopeRepo := &repomocks.ScopeRepositoryMock{
		ListFunc: func(ctx context.Context, orgName string) ([]models.Scope, error) {
			return []models.Scope{{Name: "repo:read.all"}}, nil
		},
	}
	ctrl := NewAgentIdentityController(resolver, scopeRepo, &repomocks.AgentThunderClientRepositoryMock{})
	req := httptest.NewRequest(http.MethodPost, "/orgs/o1/environments/dev/agent-identities/roles",
		strings.NewReader(`{"name":"readers","scopes":["repo:read.all"]}`))
	req.SetPathValue("orgName", "o1")
	req.SetPathValue("envName", "dev")
	w := httptest.NewRecorder()
	ctrl.CreateRole(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, []string{"ensure", "create", "perms"}, calls) // ensure strictly precedes the role write
}

func TestAgentIdentityCreateRole_UnknownScopeRejected(t *testing.T) {
	// scopeRepo returns empty catalog; same request -> 400, resolver must not be called
	// (leave ResolveIdentityFunc nil so an unexpected call panics — the moq idiom).
}

func TestAgentIdentityRoutes_EnvThunderUnavailableSurfaces502(t *testing.T) {
	// ResolveIdentityFunc returns thundersvc.ErrThunderNotProvisioned -> handler responds
	// 503 with a "environment identity provider is not available" message.
}
```

Run → FAIL (NewAgentIdentityController undefined).

- [ ] **Step 5: Implement the controller**

`controllers/agent_identity_controller.go`. Constructor + shared plumbing:

```go
type AgentIdentityController interface {
	ListGroups(w http.ResponseWriter, r *http.Request)
	CreateGroup(w http.ResponseWriter, r *http.Request)
	GetGroup(w http.ResponseWriter, r *http.Request)
	UpdateGroup(w http.ResponseWriter, r *http.Request)
	DeleteGroup(w http.ResponseWriter, r *http.Request)
	GetGroupMembers(w http.ResponseWriter, r *http.Request)
	AddGroupMembers(w http.ResponseWriter, r *http.Request)
	RemoveGroupMembers(w http.ResponseWriter, r *http.Request)
	GetGroupRoles(w http.ResponseWriter, r *http.Request)
	ListRoles(w http.ResponseWriter, r *http.Request)
	CreateRole(w http.ResponseWriter, r *http.Request)
	GetRole(w http.ResponseWriter, r *http.Request)
	UpdateRole(w http.ResponseWriter, r *http.Request)
	DeleteRole(w http.ResponseWriter, r *http.Request)
	GetRoleAssignments(w http.ResponseWriter, r *http.Request)
	AddRoleAssignees(w http.ResponseWriter, r *http.Request)
	RemoveRoleAssignees(w http.ResponseWriter, r *http.Request)
	ListAgents(w http.ResponseWriter, r *http.Request)
}

type agentIdentityController struct {
	resolver    thundersvc.EnvThunderResolver
	scopeRepo   repositories.ScopeRepository
	bindingRepo repositories.AgentThunderClientRepository
}

func NewAgentIdentityController(resolver thundersvc.EnvThunderResolver, scopeRepo repositories.ScopeRepository, bindingRepo repositories.AgentThunderClientRepository) AgentIdentityController {
	return &agentIdentityController{resolver: resolver, scopeRepo: scopeRepo, bindingRepo: bindingRepo}
}

// envClient resolves the env-Thunder identity client for the request's org+env,
// writing the error response itself when resolution fails (returns ok=false).
func (c *agentIdentityController) envClient(w http.ResponseWriter, r *http.Request) (thundersvc.EnvIdentityClient, string, bool) {
	orgName := r.PathValue("orgName")
	envName := r.PathValue("envName")
	client, err := c.resolver.ResolveIdentity(r.Context(), orgName, envName)
	if err != nil {
		logger.GetLogger(r.Context()).Error("env-thunder resolve failed", "org", orgName, "env", envName, "error", err)
		utils.WriteErrorResponse(w, http.StatusServiceUnavailable,
			"The environment's identity provider is not available; retry after it is provisioned")
		return nil, "", false
	}
	return client, orgName, true
}
```

The novel handler — `CreateRole` (lazy-ensure order is the contract Task 4/2 established):

```go
func (c *agentIdentityController) CreateRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body spec.AgentIdentityRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Name == "" {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "name is required")
		return
	}
	scopes := derefStringSlice(body.Scopes)
	if err := c.validateScopesInCatalog(ctx, r.PathValue("orgName"), scopes); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	client, _, ok := c.envClient(w, r)
	if !ok {
		return
	}
	ouID, err := client.GetDefaultOUID(ctx)
	if err != nil {
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to resolve environment identity provider OU")
		return
	}
	var rsID string
	if len(scopes) > 0 {
		rsID, err = client.EnsureScopeResourceServer(ctx, scopes)
		if err != nil {
			utils.WriteErrorResponse(w, http.StatusBadGateway, "Failed to register scopes with the environment identity provider")
			return
		}
	}
	role, err := client.CreateRole(ctx, thundersvc.CreateRoleRequest{Name: body.Name, OuID: ouID, Description: deref(body.Description)})
	if err != nil {
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to create role")
		return
	}
	if len(scopes) > 0 {
		if err := client.AddRolePermissions(ctx, role.ID, thundersvc.RolePermissionRequest{ResourceServerID: rsID, Permissions: scopes}); err != nil {
			utils.WriteErrorResponse(w, http.StatusInternalServerError, "Role created but scope permissions failed; edit the role to retry")
			return
		}
	}
	utils.WriteSuccessResponse(w, http.StatusCreated, role)
}

// deref / derefStringSlice: nil-safe pointer unwrap helpers for spec types.
// Check controllers/ for existing equivalents first (several controllers define
// them); reuse instead of redeclaring if present.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefStringSlice(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}

func (c *agentIdentityController) validateScopesInCatalog(ctx context.Context, orgName string, scopes []string) error {
	if len(scopes) == 0 {
		return nil
	}
	catalog, err := c.scopeRepo.List(ctx, orgName)
	if err != nil {
		return fmt.Errorf("failed to load scope catalog")
	}
	known := make(map[string]struct{}, len(catalog))
	for _, sc := range catalog {
		known[sc.Name] = struct{}{}
	}
	for _, s := range scopes {
		if _, ok := known[s]; !ok {
			return fmt.Errorf("scope %q is not in the catalog", s)
		}
	}
	return nil
}
```

`UpdateRole` (PUT): decode the same body; validate scopes; `GetRole` → update name/description via `UpdateRole`; diff current permission strings under the `amp-scopes` RS vs requested: `EnsureScopeResourceServer(requested)` first, then `AddRolePermissions` for additions and `RemoveRolePermissions` for removals.

`ListAgents`:

```go
func (c *agentIdentityController) ListAgents(w http.ResponseWriter, r *http.Request) {
	rows, err := c.bindingRepo.FindByOrgAndEnvironment(r.Context(), r.PathValue("orgName"), r.PathValue("envName"))
	if err != nil {
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to list agent identity bindings")
		return
	}
	agents := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		agents = append(agents, map[string]any{
			"agentName":      row.AgentName,
			"projectName":    row.ProjectName,
			"status":         row.Status,
			"thunderAgentId": row.ThunderAgentID,
		})
	}
	utils.WriteSuccessResponse(w, http.StatusOK, map[string]any{"agents": agents})
}
```

Every remaining handler is a 1:1 copy of its namesake in `controllers/identity_controller.go` with three mechanical changes: (1) obtain the client from `c.envClient(w, r)` instead of `c.client`; (2) OU comes from `client.GetDefaultOUID(ctx)` where the original used `resolvedOrg.OUID`; (3) member/assignee bodies decode `spec.AgentIdentityMembersRequest` / `spec.AgentIdentityAssignmentsRequest` and call `AddGroupMemberEntries`/`RemoveGroupMemberEntries` with `GroupMember{ID: id, Type: "agent"}` and `AddRoleAssignees`/`RemoveRoleAssignees` with the given typed entries (allow only `agent`/`group` types; 400 otherwise). Do **not** copy the Administrators-group filtering from `ListGroups` — env-Thunder groups are user-created; list them plainly with `ListGroupsByOUId(ctx, ouID, offset, limit)`.

If the Task 2 spike concluded (c) fails, still ship the group routes (they are generic passthrough) but the console tasks skip the Groups tab; no backend change.

- [ ] **Step 6: Routes, wiring, tests**

`api/agent_identity_routes.go` — mirror `identity_routes.go` exactly with the table in this task's header (`registerAgentIdentityRoutes(rr, ctrl)`), register in `api/app.go`, add the controller to the params struct + wire providers. Then:

```bash
make codegen && go test -run 'TestAgentIdentity' ./controllers/ && make test-unit && go build -tags=integration ./... && golangci-lint run --config .github/linters/.golangci.yaml ./...
```
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add docs/api_v1_openapi.yaml spec/ rbac/ repositories/ controllers/ api/ wiring/
git commit -m "feat: add agent-identity passthrough API for env-thunder groups and roles"
```

---

## Phase 2 — Console

Every task in this phase: invoke the `add-console-api-feature` skill first and keep to its two-file API pattern, TanStack conventions, and `@wso2/oxygen-ui` imports. Verification for each task: `rushx lint && rushx build` inside each touched package, and `make build` from `console/` at the end of the phase.

### Task 9: Console types

**Files:**
- Modify: `console/workspaces/libs/types/src/api/mcp-proxies.ts`
- Create: `console/workspaces/libs/types/src/api/agent-identities.ts`
- Modify: the `types` package barrel (`console/workspaces/libs/types/src/index.ts` or `src/api/index.ts` — match how `mcp-proxies.ts` is exported)

**Interfaces:**
- Produces the TS contracts every later task imports: `MCPToolScopeBinding`, `IdentitySecurity`, extended `MCPEnvironmentConfig`/`SecurityConfig`, `Scope`, `ScopeListResponse`, `AgentIdentityGroup`, `AgentIdentityRole`, `AgentIdentityAgent`, request bodies.

- [ ] **Step 1: Extend the MCP proxy types**

In `mcp-proxies.ts` (the `MCPEnvironmentConfig` interface found in preflight):

```ts
export interface MCPToolScopeBinding {
  tool: string;
  scopes: string[];
}

export interface IdentitySecurity {
  enabled?: boolean;
}

// SecurityConfig (wherever it is declared — this file or a shared security types file) gains:
//   identity?: IdentitySecurity;
// MCPEnvironmentConfig gains:
//   toolScopeBindings?: MCPToolScopeBinding[];
```

- [ ] **Step 2: New `agent-identities.ts` types**

```ts
export interface Scope {
  name: string;
  description?: string;
  createdAt?: string;
  updatedAt?: string;
  bindingCount?: number;
}

export interface ScopeListResponse {
  scopes: Scope[];
}

export interface AgentIdentityGroup {
  id: string;
  name: string;
  description?: string;
}

export interface AgentIdentityRolePermission {
  resourceServerId: string;
  permissions: string[];
}

export interface AgentIdentityRole {
  id: string;
  name: string;
  description?: string;
  permissions?: AgentIdentityRolePermission[];
}

export type AgentIdentityBindingStatus = "pending" | "in_progress" | "completed" | "failed";

export interface AgentIdentityAgent {
  agentName: string;
  projectName: string;
  status: AgentIdentityBindingStatus;
  thunderAgentId?: string;
}

export interface AgentIdentityAssignment {
  id: string;
  type: "agent" | "group";
}

export interface AgentIdentityGroupMember {
  id: string;
  type: string;
}
```

Export from the barrel. Run `rushx build` in the types package → PASS.

- [ ] **Step 3: Commit**

```bash
git add console/workspaces/libs/types/
git commit -m "feat(console): add scope catalog and agent-identity types"
```

---

### Task 10: api-client — scopes

**Files:**
- Create: `console/workspaces/libs/api-client/src/apis/scopes.ts`
- Create: `console/workspaces/libs/api-client/src/hooks/scopes.ts`
- Modify: `console/workspaces/libs/api-client/src/apis/index.ts`, `.../hooks/index.ts`

**Interfaces:**
- Produces hooks: `useListScopes(orgName)`, `useCreateScope(orgName)`, `useUpdateScope(orgName)`, `useDeleteScope(orgName)`. Query key: `["scopes"]` collection prefix.

- [ ] **Step 1: Fetch functions** (`apis/scopes.ts`; header + shape copied from `apis/identities.ts`)

```ts
import { httpDELETE, httpGET, httpPOST, httpPUT, SERVICE_BASE } from "../utils";
import type { Scope, ScopeListResponse } from "@agent-management-platform/types";

const scopesBase = (orgName: string) => `${SERVICE_BASE}/orgs/${orgName}/scopes`;

export interface ScopePathParams {
  orgName?: string;
}

export async function listScopes(
  params: ScopePathParams,
  _query?: undefined,
  getToken?: () => Promise<string>,
): Promise<ScopeListResponse> {
  const { orgName = "default" } = params;
  const token = getToken ? await getToken() : undefined;
  const res = await httpGET(scopesBase(orgName), { token });
  if (!res.ok) throw await res.json();
  return res.json();
}

export async function createScope(
  params: ScopePathParams,
  body: { name: string; description?: string },
  getToken?: () => Promise<string>,
): Promise<Scope> {
  const { orgName = "default" } = params;
  const token = getToken ? await getToken() : undefined;
  const res = await httpPOST(scopesBase(orgName), { body, token });
  if (!res.ok) throw await res.json();
  return res.json();
}

export async function updateScope(
  params: ScopePathParams & { scopeName: string },
  body: { description?: string },
  getToken?: () => Promise<string>,
): Promise<Scope> {
  const { orgName = "default", scopeName } = params;
  const token = getToken ? await getToken() : undefined;
  const res = await httpPUT(`${scopesBase(orgName)}/${encodeURIComponent(scopeName)}`, { body, token });
  if (!res.ok) throw await res.json();
  return res.json();
}

export async function deleteScope(
  params: ScopePathParams & { scopeName: string },
  _body?: undefined,
  getToken?: () => Promise<string>,
): Promise<void> {
  const { orgName = "default", scopeName } = params;
  const token = getToken ? await getToken() : undefined;
  const res = await httpDELETE(`${scopesBase(orgName)}/${encodeURIComponent(scopeName)}`, { token });
  if (!res.ok) throw await res.json();
}
```

(Match the exact `httpPOST`/`httpPUT` option signatures used in `apis/identities.ts` — if bodies are passed positionally there, do the same.)

- [ ] **Step 2: Hooks** (`hooks/scopes.ts`)

```ts
import { useQueryClient } from "@tanstack/react-query";
import { useApiMutation, useApiQuery } from "./react-query-notifications";
import { useAuthHooks } from "../auth";           // match the import path used by hooks/identities.ts
import { createScope, deleteScope, listScopes, updateScope } from "../apis/scopes";

export function useListScopes(orgName: string) {
  const { getToken } = useAuthHooks();
  return useApiQuery({
    queryKey: ["scopes", { orgName }],
    queryFn: () => listScopes({ orgName }, undefined, getToken),
  });
}

export function useCreateScope(orgName: string) {
  const { getToken } = useAuthHooks();
  const qc = useQueryClient();
  return useApiMutation({
    mutationFn: (body: { name: string; description?: string }) =>
      createScope({ orgName }, body, getToken),
    action: { verb: "create", target: "Scope" },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["scopes"] }),
  });
}
```

`useUpdateScope` / `useDeleteScope` follow the same mutation shape (`verb: "update" | "delete"`, invalidate `["scopes"]`). Copy the precise `useApiQuery`/`useApiMutation` option names from an existing hook in `hooks/identities.ts` — the wrapper's option spelling wins over this sketch.

- [ ] **Step 3: Barrels, lint, build, commit**

```bash
cd console/workspaces/libs/api-client && rushx lint && rushx build
git add console/workspaces/libs/api-client/
git commit -m "feat(console): add scope catalog api-client functions and hooks"
```

---

### Task 11: api-client — agent identities

**Files:**
- Create: `console/workspaces/libs/api-client/src/apis/agent-identities.ts`
- Create: `console/workspaces/libs/api-client/src/hooks/agent-identities.ts`
- Modify: both barrels

**Interfaces:**
- Produces hooks (all keyed `["agent-identities", <sub>, {orgName, envName}, ...]`):
  `useAgentIdentityGroups`, `useAgentIdentityGroup`, `useCreateAgentIdentityGroup`, `useUpdateAgentIdentityGroup`, `useDeleteAgentIdentityGroup`, `useAgentIdentityGroupMembers`, `useAddAgentIdentityGroupMembers`, `useRemoveAgentIdentityGroupMembers`, `useAgentIdentityGroupRoles`,
  `useAgentIdentityRoles`, `useAgentIdentityRole`, `useCreateAgentIdentityRole`, `useUpdateAgentIdentityRole`, `useDeleteAgentIdentityRole`, `useAgentIdentityRoleAssignments`, `useAddAgentIdentityRoleAssignees`, `useRemoveAgentIdentityRoleAssignees`,
  `useAgentIdentityAgents`.

- [ ] **Step 1: Fetch functions**

Base helper and one representative of each verb (the rest are mechanical clones over the Task 8 route table):

```ts
const aiBase = (orgName: string, envName: string) =>
  `${SERVICE_BASE}/orgs/${orgName}/environments/${envName}/agent-identities`;

export interface AgentIdentityPathParams {
  orgName?: string;
  envName: string;
}

export async function listAgentIdentityGroups(
  params: AgentIdentityPathParams,
  query?: { offset?: number; limit?: number },
  getToken?: () => Promise<string>,
): Promise<{ groups: AgentIdentityGroup[]; total: number }> {
  const { orgName = "default", envName } = params;
  const token = getToken ? await getToken() : undefined;
  const search = query
    ? { offset: String(query.offset ?? 0), limit: String(query.limit ?? 20) }
    : undefined;
  const res = await httpGET(`${aiBase(orgName, envName)}/groups`, { searchParams: search, token });
  if (!res.ok) throw await res.json();
  return res.json();
}

export async function createAgentIdentityRole(
  params: AgentIdentityPathParams,
  body: { name: string; description?: string; scopes?: string[] },
  getToken?: () => Promise<string>,
): Promise<AgentIdentityRole> {
  const { orgName = "default", envName } = params;
  const token = getToken ? await getToken() : undefined;
  const res = await httpPOST(`${aiBase(orgName, envName)}/roles`, { body, token });
  if (!res.ok) throw await res.json();
  return res.json();
}

export async function listAgentIdentityAgents(
  params: AgentIdentityPathParams,
  _query?: undefined,
  getToken?: () => Promise<string>,
): Promise<{ agents: AgentIdentityAgent[] }> {
  const { orgName = "default", envName } = params;
  const token = getToken ? await getToken() : undefined;
  const res = await httpGET(`${aiBase(orgName, envName)}/agents`, { token });
  if (!res.ok) throw await res.json();
  return res.json();
}
```

Full function list (one per Task 8 route): `listAgentIdentityGroups`, `createAgentIdentityGroup`, `getAgentIdentityGroup`, `updateAgentIdentityGroup`, `deleteAgentIdentityGroup`, `getAgentIdentityGroupMembers`, `addAgentIdentityGroupMembers` (body `{agentIds: string[]}`), `removeAgentIdentityGroupMembers`, `getAgentIdentityGroupRoles`, `listAgentIdentityRoles`, `createAgentIdentityRole`, `getAgentIdentityRole`, `updateAgentIdentityRole`, `deleteAgentIdentityRole`, `getAgentIdentityRoleAssignments`, `addAgentIdentityRoleAssignees` (body `{assignments: AgentIdentityAssignment[]}`), `removeAgentIdentityRoleAssignees`, `listAgentIdentityAgents`.

- [ ] **Step 2: Hooks**

Same wrapper pattern as Task 10. Query keys: collections `["agent-identities", "groups", { orgName, envName }]`, details append the id. Every mutation invalidates its collection prefix, e.g. `qc.invalidateQueries({ queryKey: ["agent-identities", "roles"] })` in role mutations' `onSuccess`. Mutations set `action` (e.g. `{ verb: "create", target: "Role" }`).

- [ ] **Step 3: Barrels, lint, build, commit**

```bash
cd console/workspaces/libs/api-client && rushx lint && rushx build
git add console/workspaces/libs/api-client/
git commit -m "feat(console): add agent-identity api-client functions and hooks"
```

---

### Task 12: MCP proxy Security tab — Agent Identity mode + tool bindings

**Files:**
- Modify: `console/workspaces/pages/mcp-proxies/src/subComponents/MCPProxySecurityTab.tsx`
- Create: `console/workspaces/pages/mcp-proxies/src/subComponents/MCPToolScopeBindingsTable.tsx`

**Interfaces:**
- Consumes: `useListScopes` (Task 10), extended `MCPEnvironmentConfig` types (Task 9). The tab's existing props (`config`, `selectedEnvironmentId`, `onUpdate`) stay — `onUpdate` already patches the selected environment's block, so bindings ride the same call.
- Produces: the tab saves `{ security: {...}, toolScopeBindings: [...] }` per environment.

- [ ] **Step 1: Bindings table component**

`MCPToolScopeBindingsTable.tsx` — controlled component:

```tsx
export type MCPToolScopeBindingsTableProps = {
  tools: string[];                       // names from the env's capabilities.tools
  bindings: Record<string, string[]>;    // tool -> scope names (draft state)
  scopeOptions: string[];                // catalog names from useListScopes
  staleTools: string[];                  // bound tools missing from capabilities
  disabled?: boolean;
  onChange: (tool: string, scopes: string[]) => void;
};

export function MCPToolScopeBindingsTable({
  tools, bindings, scopeOptions, staleTools, disabled = false, onChange,
}: MCPToolScopeBindingsTableProps) {
  const rows = [...tools, ...staleTools];
  return (
    <Table size="small">
      <TableHead>
        <TableRow>
          <TableCell>Tool</TableCell>
          <TableCell>Required scopes</TableCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {rows.map((tool) => {
          const scopes = bindings[tool] ?? [];
          const stale = staleTools.includes(tool);
          return (
            <TableRow key={tool}>
              <TableCell>
                <Stack direction="row" spacing={1} alignItems="center">
                  <Typography variant="body2" sx={{ fontFamily: "monospace" }}>{tool}</Typography>
                  {stale && <Chip size="small" color="warning" label="not in current tools" />}
                </Stack>
              </TableCell>
              <TableCell>
                <Select
                  multiple
                  size="small"
                  fullWidth
                  disabled={disabled}
                  value={scopes}
                  onChange={(e) => onChange(tool, e.target.value as string[])}
                  renderValue={(sel) =>
                    (sel as string[]).length === 0
                      ? "Authenticated only"
                      : (sel as string[]).join(", ")
                  }
                  displayEmpty
                >
                  {scopeOptions.map((s) => (
                    <MenuItem key={s} value={s}>{s}</MenuItem>
                  ))}
                </Select>
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}
```

(Imports from `@wso2/oxygen-ui`; add the Apache header.)

- [ ] **Step 2: Extend the tab**

Exact changes to `MCPProxySecurityTab.tsx`:

1. Auth type union: `type AuthType = "" | "apiKey" | "identity";` — replace both `useState<"apiKey" | "">` and the `Select` casts. Add `<MenuItem value="identity">Agent Identity</MenuItem>` to the method `Select`.
2. Saved-state derivation — add beside `isAPIKeySecurityEnabled`:

```tsx
function isIdentitySecurityEnabled(config: MCPEnvironmentConfig): boolean {
  return (
    config.security?.enabled !== false &&
    !!config.security?.identity &&
    config.security.identity.enabled !== false
  );
}

function bindingsToRecord(bindings?: MCPToolScopeBinding[]): Record<string, string[]> {
  return Object.fromEntries((bindings ?? []).map((b) => [b.tool, b.scopes]));
}
```

3. New state: `const [bindings, setBindings] = useState<Record<string, string[]>>({});` — seeded in the existing `useEffect` (and `handleDiscard`) with `setBindings(bindingsToRecord(config.toolScopeBindings));` and `setAuthenticationType(hasIdentity ? "identity" : hasApiKey ? "apiKey" : "")`.
4. `isDirty` gains: saved type comparison covers `"identity"`, plus `JSON.stringify(bindings) !== JSON.stringify(bindingsToRecord(config.toolScopeBindings))`.
5. Derived rows:

```tsx
const toolNames = useMemo(
  () =>
    (config?.capabilities?.tools ?? [])
      .map((t) => (typeof t.name === "string" ? t.name : ""))
      .filter(Boolean),
  [config],
);
const staleTools = useMemo(
  () => Object.keys(bindings).filter((t) => bindings[t].length > 0 && !toolNames.includes(t)),
  [bindings, toolNames],
);
const { data: scopesData } = useListScopes(orgName); // orgName: same source the page's other hooks use (route params)
```

6. Identity branch UI, rendered when `authenticationType === "identity"`:

```tsx
{authenticationType === "identity" && (
  <Stack spacing={2}>
    <Alert severity="info">
      Agents authenticate with a JWT from this environment&apos;s identity provider.
      Tools without scopes are callable by any authenticated agent.
    </Alert>
    <MCPToolScopeBindingsTable
      tools={toolNames}
      bindings={bindings}
      scopeOptions={(scopesData?.scopes ?? []).map((s) => s.name)}
      staleTools={staleTools}
      disabled={isDisabled}
      onChange={(tool, scopes) => setBindings((prev) => ({ ...prev, [tool]: scopes }))}
    />
  </Stack>
)}
```

7. `handleSave` builds the payload per mode (API-key validation branch unchanged):

```tsx
const toolScopeBindings = Object.entries(bindings)
  .filter(([, scopes]) => scopes.length > 0)
  .map(([tool, scopes]) => ({ tool, scopes }));

await onUpdate({
  security:
    authenticationType === "identity"
      ? { enabled: true, identity: { enabled: true } }
      : {
          enabled: config.security?.enabled ?? true,
          apiKey: {
            enabled: authenticationType === "apiKey",
            key: authenticationType === "apiKey" ? nextKey : "",
            in: nextIn,
          },
        },
  toolScopeBindings: authenticationType === "identity" ? toolScopeBindings : (config.toolScopeBindings ?? []),
});
```

Backend rejections (unknown scope, gateway missing policies) surface through the existing failure path (`setStatus` error) — append the server message when the thrown error carries one.

- [ ] **Step 3: Verify and commit**

```bash
cd console/workspaces/pages/mcp-proxies && rushx lint && rushx build
```
Manual check (if a dev stack is running): `make dev`, open an MCP proxy → Security tab → select Agent Identity on an env with discovered tools → bind scopes → Save → re-open (state persists), and `GET /orgs/{org}/mcp-proxies/{id}` shows the env's `security.identity` + `toolScopeBindings`.

```bash
git add console/workspaces/pages/mcp-proxies/
git commit -m "feat(console): agent identity mode and tool scope bindings on MCP proxy security tab"
```

---

### Task 13: Agent Identity section — scaffold, env picker, Scopes tab

**Files:**
- Create: `console/workspaces/pages/agent-identities/` (new rush package copied from `console/workspaces/pages/identities/` scaffolding: `package.json`, `tsconfig.json`, lint config, `src/`)
- Create: `src/index.ts`, `src/AgentIdentities.Organization.tsx`, `src/ScopesTab.tsx`
- Modify: `console/rush.json` (register the project — copy the identities entry, adjust name/path)
- Modify: the core-ui registration point recorded in Task 1 preflight notes (add the page exactly the way `pages/identities` is added: dependency in that package's `package.json` + metaData wiring)

**Interfaces:**
- Consumes: `useListScopes`/`useCreateScope`/`useUpdateScope`/`useDeleteScope` (Task 10); the environment-list hook used by the MCP proxy detail page's env picker (grep `workspaces/pages/mcp-proxies/src` for its environment source and reuse it).
- Produces: routed page at `/agent-identities` with tabs Scopes | Roles | Groups; `EnvPickerContext` (selected env name) consumed by Tasks 14–15.

- [ ] **Step 1: Scaffold the package**

Copy `package.json`/`tsconfig.json`/eslint config from `pages/identities`, rename to `@agent-management-platform/page-agent-identities` (match the identities package's actual naming convention), register in `rush.json`, run `rush update`.

- [ ] **Step 2: Entry + shell with env picker**

`src/index.ts`:

```ts
import { ShieldCheck } from "@wso2/oxygen-ui-icons-react"; // pick an existing icon if this one is absent
import type { PageMetadata } from "@agent-management-platform/types";
import { AgentIdentitiesOrganization } from "./AgentIdentities.Organization";

export const metaData: PageMetadata = {
  title: "Agent Identity",
  description: "Manage agent scopes, roles, and groups per environment",
  icon: ShieldCheck,
  path: "/agent-identities",
  component: AgentIdentitiesOrganization,
  levels: {
    organization: AgentIdentitiesOrganization,
  },
};

export { AgentIdentitiesOrganization };
export default AgentIdentitiesOrganization;
```

`src/AgentIdentities.Organization.tsx` — mirror `Identities.Organization.tsx`'s tab shell, plus an environment `Select` populated from the environment hook; hold `selectedEnv` in state (default: first environment). Tabs: **Scopes** (always enabled — org-global; render the caption "Scopes are shared across all environments" and do not filter by env), **Roles**, **Groups** (env-scoped; disabled with a tooltip until an environment is selected). If the Task 2 spike failed (c), omit the Groups tab entirely.

- [ ] **Step 3: Scopes tab**

`src/ScopesTab.tsx` — a table page with create dialog, modeled on the list/CRUD structure of `UsersPage.tsx`:

- Columns: Name (monospace), Description, In use (`bindingCount` → `Chip label={`${n} bindings`}`; 0 → "—"), Actions.
- Create dialog: name (`TextField`, validate `^[A-Za-z0-9:._\-]{1,256}$` inline, helper text on mismatch), description; submit via `useCreateScope`.
- Edit: description only (name immutable — rename is delete+create per spec).
- Delete: `IconButton` disabled when `bindingCount > 0` with tooltip "Unbind this scope from all MCP proxy tools first"; confirm dialog; `useDeleteScope`. A 409 from a race still surfaces via the mutation snackbar.

- [ ] **Step 4: Register, verify, commit**

Wire into core-ui per the preflight-recorded registration point. Then:

```bash
cd console && rush update && (cd workspaces/pages/agent-identities && rushx lint && rushx build) && make build
```
Manual: `make dev` → sidebar shows "Agent Identity" → Scopes tab lists/creates/deletes scopes.

```bash
git add console/rush.json console/workspaces/pages/agent-identities/ <core-ui registration files>
git commit -m "feat(console): agent identity section with org-global scopes tab"
```

---

### Task 14: Agent Identity section — Roles tab

**Files:**
- Create: `console/workspaces/pages/agent-identities/src/RolesTab.tsx`, `src/RoleCreatePage.tsx`, `src/RoleEditPage.tsx` (copied and adapted from `pages/identities/src/RolesPage.tsx`, `RoleCreatePage.tsx`, `RoleEditPage.tsx`)

**Interfaces:**
- Consumes: `useAgentIdentityRoles`/`useCreateAgentIdentityRole`/`useUpdateAgentIdentityRole`/`useDeleteAgentIdentityRole`/`useAgentIdentityRoleAssignments`/`useAddAgentIdentityRoleAssignees`/`useRemoveAgentIdentityRoleAssignees`/`useAgentIdentityAgents`/`useAgentIdentityGroups` (Task 11), `useListScopes` (Task 10), `selectedEnv` from the Task 13 shell.

- [ ] **Step 1: Adapt the three pages**

Copy each identities source file, then apply these exact substitutions:

1. Hook swap: every `useListRoles`/`useCreateRole`/... (org identities) → the `useAgentIdentity*` equivalent, passing `{ orgName, envName: selectedEnv }`.
2. **Permission picker** (in create/edit): replace the `useListAMPPermissions`-style source with `useListScopes(orgName)`; options are `scopes.map((s) => s.name)`; selected values go to the request body as `scopes: string[]` (the backend maps them under `amp-scopes` — no resource-server concept in the UI).
3. **Assignee picker** (edit page): two option groups —
   - Agents from `useAgentIdentityAgents`: label `${projectName}/${agentName}`, value `thunderAgentId`, `disabled: status !== "completed"` with tooltip "Agent identity not provisioned in this environment" (spec §7.2 — no healing loop; the operator retries after provisioning); submit as `{id: thunderAgentId, type: "agent"}`.
   - Groups from `useAgentIdentityGroups`: submit as `{id, type: "group"}`. Omit this group entirely if the spike failed (c).
4. Remove any org-identities-only affordances that don't apply (user assignees, Administrators special-casing).
5. Route the list → create/edit pages within the tab using the same relative-route mechanics the identities pages use.

- [ ] **Step 2: Verify and commit**

```bash
cd console/workspaces/pages/agent-identities && rushx lint && rushx build
```
Manual: create a role with two catalog scopes, assign a provisioned agent, reload — role shows scopes and assignment; an unprovisioned agent renders disabled.

```bash
git add console/workspaces/pages/agent-identities/
git commit -m "feat(console): roles tab for agent identity grants"
```

---

### Task 15: Agent Identity section — Groups tab (GATED on spike (c))

**Skip this task entirely if Task 2 recorded that group-role scopes do not flatten into member-agent tokens.** Groups would be decorative; the spec forbids shipping them in that case.

**Files:**
- Create: `console/workspaces/pages/agent-identities/src/GroupsTab.tsx`, `src/GroupCreatePage.tsx`, `src/GroupEditPage.tsx` (copied and adapted from the identities equivalents)

**Interfaces:**
- Consumes: `useAgentIdentityGroups`/`useCreateAgentIdentityGroup`/`useUpdateAgentIdentityGroup`/`useDeleteAgentIdentityGroup`/`useAgentIdentityGroupMembers`/`useAddAgentIdentityGroupMembers`/`useRemoveAgentIdentityGroupMembers`/`useAgentIdentityGroupRoles`/`useAgentIdentityAgents` (Task 11), `selectedEnv` (Task 13).

- [ ] **Step 1: Adapt the three pages**

Same substitution recipe as Task 14: hook swap with `{orgName, envName: selectedEnv}`; the **members picker** lists agents from `useAgentIdentityAgents` (value `thunderAgentId`, disabled unless `status === "completed"`, submit as `{agentIds: [...]}`); the group's roles panel reads `useAgentIdentityGroupRoles`. Drop user-member affordances and Administrators filtering.

- [ ] **Step 2: Verify and commit**

```bash
cd console/workspaces/pages/agent-identities && rushx lint && rushx build && cd ../../.. && make build
```
Manual: create group, add a provisioned agent, assign a role (from Roles tab) to the group, group edit shows the role.

```bash
git add console/workspaces/pages/agent-identities/
git commit -m "feat(console): groups tab for agent identity grants"
```

---

## Phase 3 — E2E

Black-box lifecycle coverage per spec §11, in the existing Ginkgo HTTP stack (`test/e2e`; conventions in `test/e2e/AGENTS.md`). **Environment preconditions** (verify before starting; they are deployment properties, not test setup):

- The target deployment's default environment has a provisioned env-Thunder (created via `add-environment.sh`, which also registers `ThunderKeyManager` in the env gateway's `config.toml`).
- The env-Thunder external route is reachable from the test runner: the environments API returns a `tokenUrl` per environment (`ThunderExternalTokenURL(org, env)` → `https://<org>-<env>.thunder.<base>/oauth2/token` — see `services/environment_service.go` where it is populated).
- An active AI gateway exists for the default env (same `gateway.WaitForActiveGatewayForEnv` precondition the existing invocation spec uses).

**Inherited gap this phase must absorb:** PR #1258 did not touch `test/e2e` — `framework.CreateMCPProxyRequest` and the mcpproxy operations still speak the pre-restructure flat shape (`Gateways: []string{...}`). Task 16 reconciles them to the UUID-keyed `environments` shape. If a restructure follow-up already did this by execution time, skip the overlapping steps and keep the additions.

### Task 16: E2E operations and framework support

**Files:**
- Modify: `test/e2e/framework/` request/response types (the file where `CreateMCPProxyRequest` / `SecurityConfig` live — locate with `grep -rn "CreateMCPProxyRequest" test/e2e/framework/`)
- Create: `test/e2e/operations/scope/scope.go`
- Create: `test/e2e/operations/agentidentity/agentidentity.go`
- Modify: `test/e2e/operations/mcpproxy/` (environments-shape create/update)
- Create: `test/e2e/framework/mcp_session.go` (MCP handshake + tools/call helper)

**Interfaces:**
- Consumes: Phase 1 API surface (scopes CRUD, agent-identities routes, per-env proxy DTO); existing `framework.AMPClient` request helpers (copy the call pattern from `operations/mcpproxy/`).
- Produces (used by Task 17):
  - `framework.MCPEnvironmentConfig{Upstream *UpstreamEndpoint, Security *SecurityConfig, ToolScopeBindings []MCPToolScopeBinding, ...}` + `Environments map[string]MCPEnvironmentConfig` on `CreateMCPProxyRequest`; `SecurityConfig` gains `Identity *IdentitySecurity`; new `MCPToolScopeBinding{Tool string; Scopes []string}`.
  - `scope.CreateScope(g, client, org, name, description)` / `scope.DeleteScope(...)`.
  - `agentidentity.CreateRole(g, client, org, env, name, scopes []string) RoleResponse`
  - `agentidentity.AssignRole(g, client, org, env, roleID string, assignments []Assignment)` (`Assignment{ID, Type string}`)
  - `agentidentity.CreateGroup(g, client, org, env, name) GroupResponse` and `agentidentity.AddGroupMembers(g, client, org, env, groupID string, agentIDs []string)`
  - `agentidentity.ListAgents(g, client, org, env) []AgentBinding` (`AgentBinding{AgentName, ProjectName, Status, ThunderAgentID string}`)
  - `framework.MCPSession` helper: `OpenMCPSession(g, httpClient, url string, headers http.Header) *MCPSession` (runs `initialize`, captures the `Mcp-Session-Id` response header, sends `notifications/initialized`) with method `CallTool(g, name string, args map[string]any) (*http.Response, []byte)`.

- [ ] **Step 1: Reconcile the mcpproxy framework types to the environments shape**

Mirror the Task 5 backend DTO exactly (env-UUID-keyed map; per-env upstream/security/bindings). Update `operations/mcpproxy` create/update wrappers and the two existing specs (`mcp_proxy_test.go`, `mcp_proxy_invocation_test.go`) to build one `environments` block for the default env instead of `Gateways`. The default env's UUID comes from the environments operation (`operations/environment` — it wraps `GET /orgs/{orgName}/environments`; use the entry matching `Cfg.DefaultEnv` and read its UUID and `tokenUrl`).

Run the existing suite to prove the reconciliation holds:

```bash
cd test/e2e && go test ./tests/mcpproxy/ -v
```
Expected: existing specs PASS against a running deployment (README.md documents the quick-start + `.env` config).

- [ ] **Step 2: Scope and agent-identity operations**

Copy the structure of an existing operations package (e.g. `operations/mcpproxy`): each function takes `(g Gomega, client *framework.AMPClient, ...)`, performs the HTTP call via the client, asserts status, returns the decoded response. Routes are the Task 4 / Task 8 tables. Keep response structs local to the operations package (match the passthrough JSON: role `{id, name, permissions: [{resourceServerId, permissions}]}`, group `{id, name}`, agents `{agents: [{agentName, projectName, status, thunderAgentId}]}`).

- [ ] **Step 3: MCP session helper**

`framework/mcp_session.go` — the existing invocation spec stops at `initialize`; per-tool authz needs a real session:

```go
// OpenMCPSession performs the MCP handshake against url: initialize (expects
// 200), captures the Mcp-Session-Id response header, and sends
// notifications/initialized. All requests carry the given headers (e.g.
// Authorization). CallTool then issues tools/call for one tool and returns the
// raw response for status/body assertions (403s arrive as HTTP responses from
// the gateway policy, not MCP errors).
```

Implementation notes: JSON-RPC bodies as in `mcp_proxy_invocation_test.go`'s `mcpInitializeBody`; `tools/call` params: `{"name": <tool>, "arguments": <args>}`; propagate `Mcp-Session-Id` on every post-initialize request; `Accept: application/json, text/event-stream`; a 401/403 during `initialize` must be returned to the caller, not asserted away (the negative cases depend on it).

- [ ] **Step 4: Commit**

```bash
git add test/e2e/
git commit -m "test(e2e): environments-shape mcp proxy ops, scope/agent-identity ops, mcp session helper"
```

---

### Task 17: Identity-secured proxy lifecycle spec

**Files:**
- Create: `test/e2e/tests/mcpproxy/mcp_proxy_identity_invocation_test.go`

**Interfaces:**
- Consumes: everything Task 16 produces; `agentops.CreateAgent` + `framework.NewExternalAgentRequest` + `configuration.CreateAgentMCPConfig` (existing, as used by `mcp_proxy_invocation_test.go`); the agent credentials claim route `DELETE /orgs/{org}/projects/{proj}/agents/{agent}/identities/secrets?environment=<env>` (external agents, one-time claim — returns clientId/clientSecret).

Spec skeleton (`Ordered`, labels `"mcp-proxy", "agent-identity"`); scenario table per spec §11:

| It | Asserts |
|---|---|
| running AI gateway + env-Thunder | `gateway.WaitForActiveGatewayForEnv`; environments API returns non-empty `tokenUrl` for the default env |
| create scopes | `e2e.echo.call`, `e2e.add.call` via `scope.CreateScope` |
| create identity-mode proxy | one `environments` block for the default env UUID: upstream `framework.TestMCPServerURL`, `security: {enabled: true, identity: {enabled: true}}`, `toolScopeBindings: [{tool: "echo", scopes: ["e2e.echo.call"]}, {tool: "add", scopes: ["e2e.add.call"]}]` (the mcp-everything server exposes `echo` and `add`; verify tool names against its `tools/list` once and adjust) |
| create external agent + attach | as the existing invocation spec, but assert the env mapping's `Configuration.URL` is set and `Configuration.AuthInfo` is **nil** — identity mode injects no API key |
| wait for identity provisioning + claim credentials | poll `agentidentity.ListAgents` until this agent's binding `status == "completed"` (timeout ~3 min — reconciler cadence); then claim the secret via the DELETE route; record `thunderAgentId`, `clientId`, `clientSecret` |
| token without grants | `client_credentials` POST to the env's `tokenUrl`; expect 200 and a JWT whose `scope` claim lacks the e2e scopes |
| **401**: no token | `OpenMCPSession` with no Authorization header → initialize rejected 401 (Eventually, past gateway route sync — copy the 404-tolerant retry pattern from the existing spec) |
| **403**: authenticated, ungranted bound tool | session with Bearer token (no grants yet... if `initialize` itself passes with any valid JWT — it should, only tools carry scope rules) → `CallTool("echo", {"message":"hi"})` → 403 |
| grant via direct role | `agentidentity.CreateRole(..., "e2e-echo-role", ["e2e.echo.call"])`; `AssignRole` with `{ID: thunderAgentID, Type: "agent"}` |
| **200**: granted bound tool | fresh token (scopes now include `e2e.echo.call` — assert on the JWT before calling); `CallTool("echo", ...)` → 200. Still **403** for `add` (bound, ungranted) |
| **200**: unbound tool | `CallTool` on an unbound tool (e.g. `longRunningOperation`) with the same token → 200 (authenticated-only default-permit) |
| group-path grant (**skip if spike (c) failed**) | `CreateGroup` + `AddGroupMembers([thunderAgentID])`; role `e2e-add-role` with `e2e.add.call` assigned `{Type: "group"}`; fresh token → `CallTool("add", {"a":1,"b":2})` → 200 |
| cleanup | `AfterAll`: delete proxy, agent config, agent, roles, group, scopes (scope deletion must now succeed — proxy deleted first; asserting the 409-then-204 order here doubles as the deletion-rule e2e) |

- [ ] **Step 1: Write the spec** per the table (each row an `It`, shared state in `Ordered` vars like the existing spec; every gateway-facing assertion wrapped in `Eventually(...).WithTimeout(2*time.Minute).WithPolling(5*time.Second)`).

JWT scope decode helper (local to the spec):

```go
func jwtScopes(g Gomega, accessToken string) []string {
	parts := strings.Split(accessToken, ".")
	g.Expect(parts).To(HaveLen(3))
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	g.Expect(err).NotTo(HaveOccurred())
	var claims struct {
		Scope string `json:"scope"`
	}
	g.Expect(json.Unmarshal(payload, &claims)).To(Succeed())
	return strings.Fields(claims.Scope)
}
```

(If the Task 2 spike recorded the claim as an array, adjust — the spike findings file states the format.)

- [ ] **Step 2: Run**

```bash
cd test/e2e && go test ./tests/mcpproxy/ -run TestMCPProxy -v
```
Expected: full suite PASS, including both the api-key and identity invocation specs.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/tests/mcpproxy/
git commit -m "test(e2e): identity-secured MCP proxy lifecycle with per-tool scope enforcement"
```

## Post-plan checklist (run after Phase 3)

- [ ] `cd agent-manager-service && make test-unit && go build -tags=integration ./... && golangci-lint run --config .github/linters/.golangci.yaml ./...`
- [ ] `cd console && make build`
- [ ] `cd test/e2e && go test ./tests/mcpproxy/ -v` against a running deployment (both invocation specs green).
- [ ] Re-read spec §10 (errors/edge cases) against the implementation; each row must be traceable to code, a test, or an existing behavior.
- [ ] Invoke `superpowers:requesting-code-review` before merging.

