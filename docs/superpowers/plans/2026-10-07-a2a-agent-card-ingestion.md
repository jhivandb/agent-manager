# A2A Agent Card Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store every A2A agent's public agent card per environment, kept in step with deployments, and expose it through a read/refresh/source API and a console panel.

**Architecture:** A new `a2a_agent_cards` table doubles as a work queue (same lease + optimistic-concurrency shape as `a2a_publications`). A ticker-driven card reconciler claims due rows, fetches `/.well-known/agent-card.json` (through the gateway for platform agents, SSRF-guarded for external ones) and stores the last good card. The A2A publication reconciler queues a platform card fetch whenever it publishes; external agents register a card URL per environment through a new API; the console shows the card per environment.

**Tech Stack:** Go 1.26, GORM + Postgres (pgx v5), moq mocks, google/wire, openapi-generator (`make spec`); React + TypeScript, TanStack Query, `@wso2/oxygen-ui`, vitest, pnpm workspaces.

**Spec:** `docs/superpowers/specs/2026-10-02-a2a-agent-card-ingestion-design.md` (read it before starting; the proxy spec `2026-10-07-a2a-external-agent-proxy-design.md` builds on this one and constrains `ClaimDue`).

## Global Constraints

- **Never commit anything under `docs/`.** Specs and this plan stay untracked. Stage explicit code paths only, never `git add -A` / `git add .`.
- `console/apps/web-ui/public/config.js` has unrelated local changes: never stage it.
- A2A 1.0 only: card path is exactly `/.well-known/agent-card.json`; no `agent.json` fallback.
- Fetch: `GET` with `Accept: application/json`, **5s** timeout, body cap **1 MiB** (over cap is an error, never truncation), non-2xx is an error carrying the status code.
- Validation (minimal): JSON object; non-empty `name`; non-empty `supportedInterfaces`, every entry with a non-empty `url`; `skills` present as an array. Unknown fields pass through untouched.
- External card URLs: `http(s)`, pass `ssrf.ValidateURL`, ≤ **2048** chars; fetched with `ssrf.NewClient`. Platform card URLs use a plain client.
- Card worker: tick **5s**, batch **50**, backoff `min(5s · 2^attempt, 60s)`, budget **14** attempts then `failed`.
- **Re-fetch never clears `card`.** `Enqueue` preserves `card`, `card_hash`, `fetched_at`.
- **`ClaimDue` never claims a row whose `next_attempt_at` is NULL** (unlike `a2a_publications`): the proxy spec parks rows that way.
- Card enqueue from the publication reconciler is best effort: log, never fail the publish.
- Go code comments: one terse line (house preference). Every new `.go` file carries the Apache header copied from `models/a2a_agent_card.go`.
- Go formatting gate is gofumpt: run `make fmt` before every agent-manager-service commit.
- Commit messages: imperative subject, end with the line `Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5`.

### Commands (agent-manager-service, run from `agent-manager-service/`)

The default `go` on this machine trips a bogus "compile version mismatch". Prefix every `make`/`go` command with the toolchain env, written `<go-env>` in the steps below:

```
env PATH="$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.darwin-arm64/bin:/opt/homebrew/bin:/usr/bin:/bin" GOTOOLCHAIN=local
```

| Purpose | Command |
|---|---|
| One unit test | `<go-env> make test-run RUN=<regex> PKG=./services/` |
| All unit tests | `<go-env> make test-unit` |
| Integration tests (needs the dev Postgres from `.env`, migrated with `<go-env> make dev-migrate`) | `<go-env> env ENV_FILE_PATH=$PWD/.env JWT_SIGNING_PRIVATE_KEY_PATH=$PWD/keys/private.pem JWT_SIGNING_PUBLIC_KEYS_CONFIG=$PWD/keys/public-keys-config.json go test -tags=integration -count=1 -run <regex> <pkg>` |
| Mocks (moq **v0.5.3**) | `<go-env> make mocks PKG=./repositories/...` |
| Wire DI | `<go-env> make wire` |
| OpenAPI → `spec/` (Docker) | `<go-env> make spec` |
| Format | `<go-env> make fmt` |
| CI lint | `<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./...` |
| Integration compile check | `<go-env> go build -tags=integration ./...` |

### Commands (console, run from `console/`)

| Purpose | Command |
|---|---|
| Lint / build / test one package | `pnpm --filter <pkg> run lint` · `pnpm --filter <pkg>... run build` · `pnpm --filter <pkg> run test` |
| Packages touched | `@agent-management-platform/types`, `@agent-management-platform/api-client`, `@agent-management-platform/overview`, `@agent-management-platform/add-new-agent` |

CI runs only `pnpm lint` and `pnpm build` for the console; run vitest locally anyway.

## Decisions this plan makes (the spec left them open)

1. **`envID` is the environment name**, as for the api-keys routes (`ocClient.GetEnvironment(ctx, ouID, envID)`).
2. **RBAC:** `GET` → `rbac.AgentRead`. `refresh`, `PUT/DELETE source` → `HandleFuncWithValidationAndAllAuthz(..., rbac.AgentUpdate, rbac.AgentEnvNonProduction)`, exactly what the deploy-settings save uses, plus the same in-service production-tier check (`requireEnvTier` is extracted into a package function so the new service can call it).
3. **Response shape:** `card` and `fetchedAt` are **omitted** (not `null`) when there is no card yet. Clients treat absent and null alike.
4. **`DELETE …/source` is idempotent** (204 even when no row exists).
5. **`refresh` on an external agent with no source** → 404, like `GET`.
6. **External subtype persistence (spec correction).** The spec says the external subtype "is already stored via the `openchoreo.dev/agent-sub-type` label". It is not: `buildExternalAgentComponentRequestBody` never writes that label and `convertComponentFromTyped` reads it only for internal agents, so an external `a2a-agent` reads back with an empty subtype. Task 6 writes the label for external agents **only when the subtype is `a2a-agent`** (other external subtypes stay free-form and unlabelled, so no arbitrary string is ever validated as a label value) and reads it back for every provisioning type.
7. **`ListForAgent` is deferred to M3**, its only consumer.
8. **New API types live in a new `A2AAgentCardService` + `A2AAgentCardController`** rather than the 6000-line `agentManagerService`, mirroring `AgentAPIKeyService`/`AgentAPIKeyController`.

## Review Focus

Inputs the spec implies that no test below would otherwise exercise, most likely to bite first. Each now has a test in the owning task.

1. **A card body that is valid JSON but not an object** (`[]`, `"x"`, `null`): must be rejected, not stored. → Task 2 test `TestA2ACardFetcherRejectsNonObjects`.
2. **A card whose bytes change but whose content does not** (key order / whitespace): must not count as a change. → Task 3 test `TestA2ACardHashIgnoresKeyOrderAndWhitespace`.
3. **An agent component exposing several endpoints, or one with an empty URL:** platform URL derivation must be deterministic and skip empty URLs. → Task 3 test `TestCardReconcilerPicksTheFirstNamedEndpointWithAURL`.
4. **Changing an external card URL while a fetch of the old URL is in flight:** the old result must not overwrite the new queue state. → Task 1 test `TestA2AAgentCardMarkFetchedDoesNotSwallowANewerEnqueue`.
5. **A refresh or platform re-enqueue of an external row:** must not erase the registered `source_url`. → Task 1 test `TestA2AAgentCardEnqueueKeepsSourceURLUnlessGiven`.

---

## File Structure

**agent-manager-service**

| File | Action | Responsibility |
|---|---|---|
| `db_migrations/047_create_a2a_agent_cards.go`, `migration_list.go` | exists (uncommitted) | table + index |
| `models/a2a_agent_card.go` | exists (uncommitted) | model, status/source enums |
| `repositories/a2a_agent_card_repository.go` | create | queue + store (interface, impl, moq directive) |
| `repositories/repomocks/a2a_agent_card_repository_mock.go` | generate | moq mock |
| `repositories/a2a_agent_card_repository_unit_test.go` | create | constants pin |
| `repositories/a2a_agent_card_repository_test.go` | create | integration (`//go:build integration`) |
| `services/a2a_card_fetcher.go` (+ `_unit_test.go`) | create | HTTP fetch + validation |
| `services/a2a_card_reconciler.go` (+ `_unit_test.go`) | create | queue worker, hashing, backoff |
| `services/a2a_publication_reconciler.go` (+ `_test.go`) | modify | queue a platform card fetch after publish |
| `services/agent_manager.go` | modify | card rows deleted with the agent; `requireEnvTier` extracted |
| `services/a2a_agent_deletion_unit_test.go` | modify | deletion covers card rows |
| `clients/openchoreosvc/client/components.go` (+ `component_labels_test.go`) | modify | persist + read external A2A subtype |
| `tests/create_agent_a2a_test.go` | modify | external A2A create gate audit |
| `utils/errors.go` | modify | card sentinels |
| `services/a2a_agent_card_service.go` (+ `_unit_test.go`) | create | GET/refresh/source business logic |
| `docs/api_v1_openapi.yaml`, `spec/*` | modify / generate | contract |
| `controllers/a2a_agent_card_controller.go` | create | HTTP layer |
| `api/a2a_agent_card_routes.go`, `api/app.go`, `api/audit_coverage_test.go` | create / modify | routes + authz |
| `audit/actions_domain.go`, `audit/policy.go`, `audit/actions_domain_test.go`, `docs/audit-logging.md` | modify | audit actions |
| `wiring/wire.go`, `wiring/params.go`, `wiring/wire_gen.go`, `app/app.go` | modify / generate | DI + lifecycle |

**console**

| File | Action | Responsibility |
|---|---|---|
| `workspaces/libs/types/src/api/agent-card.ts`, `api/index.ts` | create / modify | types |
| `workspaces/libs/api-client/src/apis/agent-card.ts`, `hooks/agent-card.ts`, barrels | create / modify | fetch fns + hooks |
| `workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.tsx` (+ test) | create | per-env panel |
| `workspaces/pages/overview/src/AgentOverview/AgentCardSourceForm.tsx` | create | external card URL editor |
| `workspaces/pages/overview/src/AgentOverview/EnvironmentSectionsContent.tsx` | modify | mount the panel |
| `workspaces/pages/add-new-agent/src/form/schema.ts`, `forms/ExternalAgentForm.tsx`, `utils/buildAgentPayload.ts` (+ test) | modify | external A2A subtype at create |

---

### Task 1: Card store repository

**Files:**
- Commit (already written): `agent-manager-service/db_migrations/047_create_a2a_agent_cards.go`, `agent-manager-service/db_migrations/migration_list.go`, `agent-manager-service/models/a2a_agent_card.go`
- Create: `agent-manager-service/repositories/a2a_agent_card_repository.go`
- Generate: `agent-manager-service/repositories/repomocks/a2a_agent_card_repository_mock.go`
- Test: `agent-manager-service/repositories/a2a_agent_card_repository_unit_test.go`, `agent-manager-service/repositories/a2a_agent_card_repository_test.go`

**Interfaces:**
- Consumes: `models.A2AAgentCard`, `models.A2AAgentCardStatus*`, `models.A2AAgentCardSource*` (existing file).
- Produces:
  - `var repositories.ErrA2AAgentCardSuperseded error`, `var repositories.ErrA2AAgentCardNotFound error`
  - `type repositories.A2AAgentCardRepository interface` with
    - `Enqueue(ctx context.Context, card *models.A2AAgentCard) error`
    - `ClaimDue(ctx context.Context, now time.Time, limit int) ([]models.A2AAgentCard, error)`
    - `MarkFetched(ctx context.Context, read models.A2AAgentCard, card json.RawMessage, cardHash, fetchedURL string) error`
    - `MarkAttemptFailed(ctx context.Context, read models.A2AAgentCard, lastErr string, nextAttemptAt time.Time) error`
    - `MarkFailed(ctx context.Context, read models.A2AAgentCard, lastErr string) error`
    - `Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AAgentCard, error)`
    - `DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error`
    - `DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName, environmentName string) error`
  - `func repositories.NewA2AAgentCardRepository(db *gorm.DB) A2AAgentCardRepository`
  - `repomocks.A2AAgentCardRepositoryMock`

- [ ] **Step 1: Write the constants test**

`repositories/a2a_agent_card_repository_unit_test.go` (Apache header first):

```go
package repositories

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// The due-scan and the API both filter on these exact strings.
func TestA2AAgentCardTableAndEnums(t *testing.T) {
	assert.Equal(t, "a2a_agent_cards", models.A2AAgentCard{}.TableName())
	assert.Equal(t, models.A2AAgentCardStatus("pending"), models.A2AAgentCardStatusPending)
	assert.Equal(t, models.A2AAgentCardStatus("fetched"), models.A2AAgentCardStatusFetched)
	assert.Equal(t, models.A2AAgentCardStatus("failed"), models.A2AAgentCardStatusFailed)
	assert.Equal(t, models.A2AAgentCardSource("platform"), models.A2AAgentCardSourcePlatform)
	assert.Equal(t, models.A2AAgentCardSource("external"), models.A2AAgentCardSourceExternal)
}
```

- [ ] **Step 2: Write the integration tests**

`repositories/a2a_agent_card_repository_test.go` — line 1 is `//go:build integration`, blank line, then the Apache header. Helper names must not collide with `a2a_publication_repository_test.go` (`newTestPublication`, `cleanupPublication`, `dueRowFor`, `statusOf` are taken).

```go
//go:build integration

// <Apache header>

package repositories

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

func newTestCard(agentName string, source models.A2AAgentCardSource) *models.A2AAgentCard {
	return &models.A2AAgentCard{
		OUID:            "ou-" + uuid.New().String()[:8],
		ProjectName:     "checkout",
		AgentName:       agentName,
		EnvironmentName: "Development",
		Source:          source,
	}
}

func cleanupCard(t *testing.T, repo A2AAgentCardRepository, card *models.A2AAgentCard) {
	t.Helper()
	t.Cleanup(func() {
		_ = repo.DeleteForAgent(context.Background(), card.OUID, card.ProjectName, card.AgentName)
	})
}

// dueCardFor claims the due row for agentName, as the reconciler would.
func dueCardFor(t *testing.T, repo A2AAgentCardRepository, agentName string) models.A2AAgentCard {
	t.Helper()
	due, err := repo.ClaimDue(context.Background(), time.Now(), 100)
	require.NoError(t, err)
	for _, row := range due {
		if row.AgentName == agentName {
			return row
		}
	}
	require.FailNow(t, "no due card row", "agent %s", agentName)
	return models.A2AAgentCard{}
}

func cardRowOf(t *testing.T, card *models.A2AAgentCard) models.A2AAgentCard {
	t.Helper()
	var row models.A2AAgentCard
	require.NoError(t, db.GetDB().Where("id = ?", card.ID).First(&row).Error)
	return row
}

func claimedNames(rows []models.A2AAgentCard) []string {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.AgentName)
	}
	return names
}

const sampleCard = `{"name":"Trip Planner","supportedInterfaces":[{"url":"https://a.example/rpc"}],"skills":[]}`

// A redeploy settles over minutes; readers must keep the last good card meanwhile.
func TestA2AAgentCardEnqueuePreservesTheLastGoodCard(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("keep-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)

	require.NoError(t, repo.Enqueue(ctx, card))
	read := dueCardFor(t, repo, card.AgentName)
	require.NoError(t, repo.MarkFetched(ctx, read, json.RawMessage(sampleCard), "hash-1", "https://gw.example/a/.well-known/agent-card.json"))
	fetched := cardRowOf(t, card)
	require.NotNil(t, fetched.FetchedAt)

	again := newTestCard(card.AgentName, models.A2AAgentCardSourcePlatform)
	again.OUID = card.OUID
	require.NoError(t, repo.Enqueue(ctx, again))

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusPending, row.Status)
	assert.Equal(t, 0, row.AttemptCount, "the attempt budget is fresh")
	assert.Empty(t, row.LastError)
	assert.JSONEq(t, sampleCard, string(row.Card), "the last good card survives the re-enqueue")
	assert.Equal(t, "hash-1", row.CardHash)
	require.NotNil(t, row.FetchedAt)
	assert.WithinDuration(t, *fetched.FetchedAt, *row.FetchedAt, time.Millisecond)
	assert.Equal(t, "https://gw.example/a/.well-known/agent-card.json", row.SourceURL,
		"a platform re-enqueue keeps the diagnostic URL")
}

// A refresh or platform trigger must not erase a registered external URL.
func TestA2AAgentCardEnqueueKeepsSourceURLUnlessGiven(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("src-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	card.SourceURL = "https://agent.example/.well-known/agent-card.json"
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))

	refresh := newTestCard(card.AgentName, models.A2AAgentCardSourceExternal)
	refresh.OUID = card.OUID
	require.NoError(t, repo.Enqueue(ctx, refresh))
	assert.Equal(t, card.SourceURL, cardRowOf(t, card).SourceURL)

	moved := newTestCard(card.AgentName, models.A2AAgentCardSourceExternal)
	moved.OUID = card.OUID
	moved.SourceURL = "https://other.example/.well-known/agent-card.json"
	require.NoError(t, repo.Enqueue(ctx, moved))
	assert.Equal(t, moved.SourceURL, cardRowOf(t, card).SourceURL)
}

// A row whose retry is scheduled for later is not handed out until then.
func TestA2AAgentCardClaimDueRespectsBackoff(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("backoff-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, repo.MarkAttemptFailed(ctx, *card, "HTTP 503", time.Now().Add(time.Hour)))

	due, err := repo.ClaimDue(ctx, time.Now(), 100)
	require.NoError(t, err)
	assert.NotContains(t, claimedNames(due), card.AgentName)

	later, err := repo.ClaimDue(ctx, time.Now().Add(2*time.Hour), 100)
	require.NoError(t, err)
	assert.Contains(t, claimedNames(later), card.AgentName)
	assert.Equal(t, 1, cardRowOf(t, card).AttemptCount)
}

// The proxy spec parks rows with a NULL next_attempt_at; they must never be claimed.
func TestA2AAgentCardClaimDueSkipsAParkedRow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("parked-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, db.GetDB().Model(&models.A2AAgentCard{}).
		Where("id = ?", card.ID).Update("next_attempt_at", nil).Error)

	due, err := repo.ClaimDue(ctx, time.Now().Add(24*time.Hour), 100)
	require.NoError(t, err)
	assert.NotContains(t, claimedNames(due), card.AgentName)
}

// Replicas tick independently; two concurrent claimers never get the same row.
func TestA2AAgentCardConcurrentClaimersNeverShareARow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	prefix := "race-" + uuid.New().String()[:6] + "-"
	ours := map[string]bool{}
	for i := 0; i < 20; i++ {
		card := newTestCard(prefix+uuid.New().String()[:6], models.A2AAgentCardSourcePlatform)
		cleanupCard(t, repo, card)
		require.NoError(t, repo.Enqueue(ctx, card))
		ours[card.AgentName] = true
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := repo.ClaimDue(ctx, time.Now(), 15)
			assert.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				if ours[r.AgentName] {
					seen[r.AgentName]++
				}
			}
		}()
	}
	wg.Wait()

	for name, n := range seen {
		assert.Equal(t, 1, n, "row %s claimed twice", name)
	}
}

// A claimed row is leased: not handed out again until the lease runs out.
func TestA2AAgentCardClaimDueLeasesTheRow(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("lease-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	claimed := dueCardFor(t, repo, card.AgentName)

	again, err := repo.ClaimDue(ctx, time.Now(), 100)
	require.NoError(t, err)
	assert.NotContains(t, claimedNames(again), card.AgentName)

	afterLease, err := repo.ClaimDue(ctx, time.Now().Add(a2aAgentCardClaimLease+time.Minute), 100)
	require.NoError(t, err)
	assert.Contains(t, claimedNames(afterLease), card.AgentName)
	require.NoError(t, repo.MarkFetched(ctx, claimed, json.RawMessage(sampleCard), "h", "u"),
		"claiming is not mistaken for a re-enqueue")
}

// An unchanged hash flips the status but does not rewrite the stored card.
func TestA2AAgentCardMarkFetchedSkipsTheWriteForAnUnchangedHash(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("hash-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, repo.MarkFetched(ctx, dueCardFor(t, repo, card.AgentName), json.RawMessage(sampleCard), "same", "u"))

	again := newTestCard(card.AgentName, models.A2AAgentCardSourcePlatform)
	again.OUID = card.OUID
	require.NoError(t, repo.Enqueue(ctx, again))
	read := dueCardFor(t, repo, card.AgentName)
	require.NoError(t, repo.MarkFetched(ctx, read, json.RawMessage(`{"name":"other"}`), "same", "u"))

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusFetched, row.Status)
	assert.JSONEq(t, sampleCard, string(row.Card), "same hash, so the body was not rewritten")
}

// A fetch of the old URL must not overwrite a newer enqueue (e.g. a changed source URL).
func TestA2AAgentCardMarkFetchedDoesNotSwallowANewerEnqueue(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("race-fetch-"+uuid.New().String()[:8], models.A2AAgentCardSourceExternal)
	card.SourceURL = "https://old.example/.well-known/agent-card.json"
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	read := dueCardFor(t, repo, card.AgentName)

	moved := *card
	moved.SourceURL = "https://new.example/.well-known/agent-card.json"
	require.NoError(t, repo.Enqueue(ctx, &moved))

	require.ErrorIs(t, repo.MarkFetched(ctx, read, json.RawMessage(sampleCard), "h", read.SourceURL), ErrA2AAgentCardSuperseded)
	require.ErrorIs(t, repo.MarkAttemptFailed(ctx, read, "x", time.Now().Add(time.Hour)), ErrA2AAgentCardSuperseded)
	require.ErrorIs(t, repo.MarkFailed(ctx, read, "x"), ErrA2AAgentCardSuperseded)

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusPending, row.Status)
	assert.Equal(t, moved.SourceURL, row.SourceURL)
	assert.Nil(t, row.Card)
}

func TestA2AAgentCardMarkFailedEndsTheCycle(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	card := newTestCard("failed-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	cleanupCard(t, repo, card)
	require.NoError(t, repo.Enqueue(ctx, card))
	require.NoError(t, repo.MarkFailed(ctx, dueCardFor(t, repo, card.AgentName), "HTTP 404"))

	row := cardRowOf(t, card)
	assert.Equal(t, models.A2AAgentCardStatusFailed, row.Status)
	assert.Equal(t, "HTTP 404", row.LastError)
	assert.Nil(t, row.NextAttemptAt)
}

func TestA2AAgentCardGetAndDelete(t *testing.T) {
	repo := NewA2AAgentCardRepository(db.GetDB())
	ctx := context.Background()
	dev := newTestCard("get-"+uuid.New().String()[:8], models.A2AAgentCardSourcePlatform)
	prod := newTestCard(dev.AgentName, models.A2AAgentCardSourcePlatform)
	prod.OUID, prod.EnvironmentName = dev.OUID, "Production"
	cleanupCard(t, repo, dev)
	require.NoError(t, repo.Enqueue(ctx, dev))
	require.NoError(t, repo.Enqueue(ctx, prod))

	got, err := repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Development")
	require.NoError(t, err)
	assert.Equal(t, dev.ID, got.ID)

	_, err = repo.Get(ctx, "other-org", dev.ProjectName, dev.AgentName, "Development")
	assert.ErrorIs(t, err, ErrA2AAgentCardNotFound, "rows are org-scoped")

	require.NoError(t, repo.DeleteForAgentEnv(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Development"))
	_, err = repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Development")
	assert.ErrorIs(t, err, ErrA2AAgentCardNotFound)
	_, err = repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Production")
	require.NoError(t, err, "only that environment's row is gone")

	require.NoError(t, repo.DeleteForAgent(ctx, dev.OUID, dev.ProjectName, dev.AgentName))
	_, err = repo.Get(ctx, dev.OUID, dev.ProjectName, dev.AgentName, "Production")
	assert.ErrorIs(t, err, ErrA2AAgentCardNotFound)
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `<go-env> go build -tags=integration ./repositories/`
Expected: FAIL — `undefined: A2AAgentCardRepository`, `undefined: NewA2AAgentCardRepository`.

- [ ] **Step 4: Write the repository**

`repositories/a2a_agent_card_repository.go` (Apache header first):

```go
package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// ErrA2AAgentCardSuperseded means the row was re-enqueued after the caller read it.
var ErrA2AAgentCardSuperseded = errors.New("a2a agent card was re-enqueued since it was read")

// ErrA2AAgentCardNotFound means no card row exists for the agent and environment.
var ErrA2AAgentCardNotFound = errors.New("a2a agent card not found")

// A2AAgentCardRepository stores each A2A agent's card per environment and queues its fetches.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/a2a_agent_card_repository_mock.go . A2AAgentCardRepository:A2AAgentCardRepositoryMock
type A2AAgentCardRepository interface {
	// Enqueue upserts the row as pending with a fresh budget, keeping card, card_hash and
	// fetched_at. source_url is written on conflict only for an external row that names one.
	Enqueue(ctx context.Context, card *models.A2AAgentCard) error

	// ClaimDue leases up to limit pending rows whose next_attempt_at has passed.
	// A NULL next_attempt_at is never due: the proxy flow parks rows that way.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]models.A2AAgentCard, error)

	// The Mark* methods record an attempt on the row as ClaimDue returned it and return
	// ErrA2AAgentCardSuperseded when updated_at moved (a newer Enqueue).

	// MarkFetched rewrites card and card_hash only when cardHash differs from read.CardHash.
	MarkFetched(ctx context.Context, read models.A2AAgentCard, card json.RawMessage, cardHash, fetchedURL string) error
	MarkAttemptFailed(ctx context.Context, read models.A2AAgentCard, lastErr string, nextAttemptAt time.Time) error
	MarkFailed(ctx context.Context, read models.A2AAgentCard, lastErr string) error

	// Get returns ErrA2AAgentCardNotFound when no row exists.
	Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AAgentCard, error)
	DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error
	DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName, environmentName string) error
}

// a2aAgentCardClaimLease comfortably outlasts one 5s fetch.
const a2aAgentCardClaimLease = time.Minute

type a2aAgentCardRepository struct {
	db *gorm.DB
}

// NewA2AAgentCardRepository creates an A2AAgentCardRepository.
func NewA2AAgentCardRepository(db *gorm.DB) A2AAgentCardRepository {
	return &a2aAgentCardRepository{db: db}
}

func (r *a2aAgentCardRepository) Enqueue(ctx context.Context, card *models.A2AAgentCard) error {
	// Postgres keeps microseconds; the Mark* guards compare against this value.
	now := time.Now().Truncate(time.Microsecond)
	card.Status = models.A2AAgentCardStatusPending
	card.AttemptCount = 0
	card.LastError = ""
	card.NextAttemptAt = &now
	card.UpdatedAt = now

	updates := []string{"status", "attempt_count", "last_error", "next_attempt_at", "updated_at"}
	if card.Source == models.A2AAgentCardSourceExternal && card.SourceURL != "" {
		updates = append(updates, "source_url")
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ou_id"},
			{Name: "project_name"},
			{Name: "agent_name"},
			{Name: "environment_name"},
		},
		DoUpdates: clause.AssignmentColumns(updates),
	}).Create(card).Error
}

func (r *a2aAgentCardRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]models.A2AAgentCard, error) {
	var claimed []models.A2AAgentCard
	err := r.db.WithContext(ctx).Raw(
		`
		UPDATE a2a_agent_cards SET next_attempt_at = ?
		WHERE id IN (
			SELECT id FROM a2a_agent_cards
			WHERE status = ? AND next_attempt_at <= ?
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING *`,
		now.Add(a2aAgentCardClaimLease), models.A2AAgentCardStatusPending, now, limit,
	).Scan(&claimed).Error
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *a2aAgentCardRepository) MarkFetched(ctx context.Context, read models.A2AAgentCard, card json.RawMessage, cardHash, fetchedURL string) error {
	updates := map[string]interface{}{
		"status":          models.A2AAgentCardStatusFetched,
		"fetched_at":      time.Now(),
		"last_error":      "",
		"next_attempt_at": nil,
		"source_url":      fetchedURL,
	}
	if cardHash != read.CardHash {
		updates["card"] = card
		updates["card_hash"] = cardHash
	}
	return r.updateIfUnchanged(ctx, read, updates)
}

func (r *a2aAgentCardRepository) MarkAttemptFailed(ctx context.Context, read models.A2AAgentCard, lastErr string, nextAttemptAt time.Time) error {
	return r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"attempt_count":   gorm.Expr("attempt_count + 1"),
		"last_error":      lastErr,
		"next_attempt_at": nextAttemptAt,
	})
}

func (r *a2aAgentCardRepository) MarkFailed(ctx context.Context, read models.A2AAgentCard, lastErr string) error {
	return r.updateIfUnchanged(ctx, read, map[string]interface{}{
		"status":          models.A2AAgentCardStatusFailed,
		"attempt_count":   gorm.Expr("attempt_count + 1"),
		"last_error":      lastErr,
		"next_attempt_at": nil,
	})
}

func (r *a2aAgentCardRepository) updateIfUnchanged(ctx context.Context, read models.A2AAgentCard, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	result := r.db.WithContext(ctx).Model(&models.A2AAgentCard{}).
		Where("id = ? AND updated_at = ?", read.ID, read.UpdatedAt).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrA2AAgentCardSuperseded
	}
	return nil
}

func (r *a2aAgentCardRepository) Get(ctx context.Context, ouID, projectName, agentName, environmentName string) (*models.A2AAgentCard, error) {
	var row models.A2AAgentCard
	err := r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_name = ?",
			ouID, projectName, agentName, environmentName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrA2AAgentCardNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *a2aAgentCardRepository) DeleteForAgent(ctx context.Context, ouID, projectName, agentName string) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ?", ouID, projectName, agentName).
		Delete(&models.A2AAgentCard{}).Error
}

func (r *a2aAgentCardRepository) DeleteForAgentEnv(ctx context.Context, ouID, projectName, agentName, environmentName string) error {
	return r.db.WithContext(ctx).
		Where("ou_id = ? AND project_name = ? AND agent_name = ? AND environment_name = ?",
			ouID, projectName, agentName, environmentName).
		Delete(&models.A2AAgentCard{}).Error
}
```

- [ ] **Step 5: Generate the mock**

Run: `<go-env> make mocks PKG=./repositories/...`
Expected: creates `repositories/repomocks/a2a_agent_card_repository_mock.go`; `git status` shows no other mock changes (if it does, `moq --version` is not v0.5.3 — fix that and rerun before continuing).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `<go-env> make dev-migrate`, then the integration command with `-run 'TestA2AAgentCard' ./repositories/`, then `<go-env> make test-run RUN=TestA2AAgentCardTableAndEnums PKG=./repositories/`.
Expected: all PASS. (If the `card` insert fails with a bytea/jsonb type error, the pgx encoding assumption is wrong: stop and report rather than changing the column type.)

- [ ] **Step 7: Format, lint, commit**

```bash
<go-env> make fmt
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./repositories/... ./models/... ./db_migrations/...
git add db_migrations/047_create_a2a_agent_cards.go db_migrations/migration_list.go models/a2a_agent_card.go \
  repositories/a2a_agent_card_repository.go repositories/repomocks/a2a_agent_card_repository_mock.go \
  repositories/a2a_agent_card_repository_unit_test.go repositories/a2a_agent_card_repository_test.go
git commit -m "Add the A2A agent card store and fetch queue

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 2: Card fetcher

**Files:**
- Create: `agent-manager-service/services/a2a_card_fetcher.go`
- Test: `agent-manager-service/services/a2a_card_fetcher_unit_test.go`

**Interfaces:**
- Consumes: `ssrf.ValidateURL(ctx, url) error`, `ssrf.NewClient(timeout) *http.Client`, `utils.ErrInvalidURL`.
- Produces:
  - `const a2aAgentCardPath = "/.well-known/agent-card.json"`
  - `type A2ACardFetcher interface { Fetch(ctx context.Context, url string, guarded bool) (json.RawMessage, error) }`
  - `func NewA2ACardFetcher() A2ACardFetcher` (5s timeout); `func newA2ACardFetcher(timeout time.Duration) *a2aCardFetcher` (tests)
  - `type fakeA2ACardFetcher struct { FetchFunc func(ctx context.Context, url string, guarded bool) (json.RawMessage, error); calls []fakeFetchCall }` in the test file (used by Task 3 tests; same package).

- [ ] **Step 1: Write the failing tests**

`services/a2a_card_fetcher_unit_test.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validTestCard = `{"name":"Trip Planner","description":"d","supportedInterfaces":[{"url":"https://a.example/rpc","protocolBinding":"JSONRPC"}],"skills":[{"name":"plan"}],"x-extra":{"kept":true}}`

func cardServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestA2ACardFetcherReturnsTheCardAsServed(t *testing.T) {
	srv := cardServer(t, http.StatusOK, validTestCard)
	got, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL+a2aAgentCardPath, false)
	require.NoError(t, err)
	assert.JSONEq(t, validTestCard, string(got), "unknown fields pass through")
}

func TestA2ACardFetcherRejectsInvalidCards(t *testing.T) {
	cases := map[string]string{
		"missing name":              `{"supportedInterfaces":[{"url":"u"}],"skills":[]}`,
		"empty name":                `{"name":"","supportedInterfaces":[{"url":"u"}],"skills":[]}`,
		"missing interfaces":        `{"name":"n","skills":[]}`,
		"empty interfaces":          `{"name":"n","supportedInterfaces":[],"skills":[]}`,
		"interface without url":     `{"name":"n","supportedInterfaces":[{"protocolBinding":"JSONRPC"}],"skills":[]}`,
		"missing skills":            `{"name":"n","supportedInterfaces":[{"url":"u"}]}`,
		"null skills":               `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":null}`,
		"skills not an array":       `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":"x"}`,
		"not json":                  `<html>hi</html>`,
		"trailing garbage after obj": `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":[]} x`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := cardServer(t, http.StatusOK, body)
			_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
			require.Error(t, err)
		})
	}
}

func TestA2ACardFetcherRejectsNonObjects(t *testing.T) {
	for _, body := range []string{`[]`, `"card"`, `null`, `42`} {
		srv := cardServer(t, http.StatusOK, body)
		_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
		require.Error(t, err, body)
	}
}

func TestA2ACardFetcherReportsTheStatusCode(t *testing.T) {
	srv := cardServer(t, http.StatusNotFound, `{"name":"n"}`)
	_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestA2ACardFetcherRejectsAnOversizedBody(t *testing.T) {
	padding := strings.Repeat("a", a2aCardMaxBytes)
	body := `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":[],"pad":"` + padding + `"}`
	srv := cardServer(t, http.StatusOK, body)
	_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 MiB")
}

func TestA2ACardFetcherTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte(validTestCard))
	}))
	t.Cleanup(srv.Close)
	_, err := newA2ACardFetcher(50*time.Millisecond).Fetch(context.Background(), srv.URL, false)
	require.Error(t, err)
}

// External URLs are user-supplied: a loopback address is refused before any request.
func TestA2ACardFetcherGuardedModeRejectsLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(validTestCard))
	}))
	t.Cleanup(srv.Close)

	_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, true)
	require.Error(t, err)
	assert.Equal(t, int32(0), hits.Load())
}

type fakeFetchCall struct {
	URL     string
	Guarded bool
}

// fakeA2ACardFetcher is the hand-written stub for the in-package A2ACardFetcher.
type fakeA2ACardFetcher struct {
	FetchFunc func(ctx context.Context, url string, guarded bool) (json.RawMessage, error)
	calls     []fakeFetchCall
}

func (f *fakeA2ACardFetcher) Fetch(ctx context.Context, url string, guarded bool) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeFetchCall{URL: url, Guarded: guarded})
	return f.FetchFunc(ctx, url, guarded)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `<go-env> make test-run RUN=TestA2ACardFetcher PKG=./services/`
Expected: FAIL — `undefined: newA2ACardFetcher`, `undefined: a2aAgentCardPath`, `undefined: a2aCardMaxBytes`.

- [ ] **Step 3: Write the fetcher**

`services/a2a_card_fetcher.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/utils"
	"github.com/wso2/agent-manager/agent-manager-service/utils/ssrf"
)

const (
	// a2aAgentCardPath is the A2A 1.0 public card path; there is no agent.json fallback.
	a2aAgentCardPath    = "/.well-known/agent-card.json"
	a2aCardFetchTimeout = 5 * time.Second
	a2aCardMaxBytes     = 1 << 20
)

// A2ACardFetcher fetches and minimally validates an A2A 1.0 public agent card.
type A2ACardFetcher interface {
	// Fetch returns the card as served. guarded selects the SSRF-hardened client for user-supplied URLs.
	Fetch(ctx context.Context, url string, guarded bool) (json.RawMessage, error)
}

type a2aCardFetcher struct {
	plain   *http.Client
	guarded *http.Client
}

// NewA2ACardFetcher creates an A2ACardFetcher with the spec's 5s timeout.
func NewA2ACardFetcher() A2ACardFetcher {
	return newA2ACardFetcher(a2aCardFetchTimeout)
}

func newA2ACardFetcher(timeout time.Duration) *a2aCardFetcher {
	return &a2aCardFetcher{
		// Platform URLs resolve to loopback in local setups, which the SSRF guard rejects.
		plain:   &http.Client{Timeout: timeout},
		guarded: ssrf.NewClient(timeout),
	}
}

func (f *a2aCardFetcher) Fetch(ctx context.Context, url string, guarded bool) (json.RawMessage, error) {
	client := f.plain
	if guarded {
		if err := ssrf.ValidateURL(ctx, url); err != nil {
			return nil, fmt.Errorf("agent card URL is not allowed: %w", err)
		}
		client = f.guarded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid agent card URL: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		if guarded {
			if errors.Is(err, utils.ErrInvalidURL) {
				return nil, fmt.Errorf("agent card URL is not allowed: %w", err)
			}
			// Raw dial errors are not surfaced for user-supplied hosts.
			return nil, errors.New("could not reach the agent card URL")
		}
		return nil, fmt.Errorf("could not reach the agent card URL: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("agent card request returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, a2aCardMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read the agent card: %w", err)
	}
	if len(body) > a2aCardMaxBytes {
		return nil, errors.New("agent card exceeds 1 MiB")
	}
	if err := validateA2ACard(body); err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// a2aCardShape is the minimal A2A 1.0 card validation reads; everything else passes through.
type a2aCardShape struct {
	Name                string `json:"name"`
	SupportedInterfaces []struct {
		URL string `json:"url"`
	} `json:"supportedInterfaces"`
	Skills *[]json.RawMessage `json:"skills"`
}

func validateA2ACard(body []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return errors.New("agent card is not a JSON object")
	}
	var card a2aCardShape
	if err := json.Unmarshal(body, &card); err != nil {
		return fmt.Errorf("agent card is malformed: %w", err)
	}
	if card.Name == "" {
		return errors.New(`agent card is missing "name"`)
	}
	if len(card.SupportedInterfaces) == 0 {
		return errors.New(`agent card has no "supportedInterfaces"`)
	}
	for i, iface := range card.SupportedInterfaces {
		if iface.URL == "" {
			return fmt.Errorf(`agent card "supportedInterfaces[%d]" has no "url"`, i)
		}
	}
	if card.Skills == nil {
		return errors.New(`agent card is missing the "skills" array`)
	}
	return nil
}
```

(`json.Unmarshal` into a map rejects arrays/strings/numbers and trailing garbage; `null` yields a nil map, also rejected.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `<go-env> make test-run RUN=TestA2ACardFetcher PKG=./services/`
Expected: PASS.

- [ ] **Step 5: Format, lint, commit**

```bash
<go-env> make fmt
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./services/...
git add services/a2a_card_fetcher.go services/a2a_card_fetcher_unit_test.go
git commit -m "Add the A2A agent card fetcher

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 3: Card reconciler and its wiring

**Files:**
- Create: `agent-manager-service/services/a2a_card_reconciler.go`
- Test: `agent-manager-service/services/a2a_card_reconciler_unit_test.go`
- Modify: `agent-manager-service/wiring/wire.go`, `agent-manager-service/wiring/params.go`, `agent-manager-service/app/app.go`
- Generate: `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: Task 1 repository + mock; Task 2 `A2ACardFetcher`, `a2aAgentCardPath`, `fakeA2ACardFetcher`; `client.OpenChoreoClient.GetComponentEndpoints(ctx, ouID, projectName, componentName, environment string) (map[string]models.EndpointsResponse, error)`; `testLogger()` (in `a2a_publication_reconciler_test.go`).
- Produces:
  - `type A2ACardReconcilerService interface { Start(ctx context.Context) error; Stop() error; RunOnce(ctx context.Context) }`
  - `func NewA2ACardReconcilerService(cardRepo repositories.A2AAgentCardRepository, fetcher A2ACardFetcher, ocClient client.OpenChoreoClient, logger *slog.Logger) A2ACardReconcilerService`
  - `func a2aCardHash(card json.RawMessage) (string, error)`
  - `func a2aCardRetryIn(attempt int) time.Duration`
  - `const a2aCardAttemptBudget = 14`
  - `wiring.AppParams.A2ACardReconciler services.A2ACardReconcilerService`
  - `wiring.ProvideA2AAgentCardRepository(db *gorm.DB) repositories.A2AAgentCardRepository`

- [ ] **Step 1: Write the failing tests**

`services/a2a_card_reconciler_unit_test.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

func pendingCard(source models.A2AAgentCardSource) models.A2AAgentCard {
	return models.A2AAgentCard{
		ID:              uuid.New(),
		OUID:            "org-1",
		ProjectName:     "checkout",
		AgentName:       "trip-planner",
		EnvironmentName: "dev",
		Source:          source,
		Status:          models.A2AAgentCardStatusPending,
		UpdatedAt:       time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC),
	}
}

type cardReconcilerHarness struct {
	svc      *a2aCardReconcilerService
	repo     *repomocks.A2AAgentCardRepositoryMock
	fetcher  *fakeA2ACardFetcher
	ocClient *clientmocks.OpenChoreoClientMock
}

// newCardReconcilerHarness wires a reconciler whose fetch succeeds with validTestCard
// and whose agent exposes one endpoint at endpointURL.
func newCardReconcilerHarness(endpointURL string) *cardReconcilerHarness {
	repo := &repomocks.A2AAgentCardRepositoryMock{
		MarkFetchedFunc: func(context.Context, models.A2AAgentCard, json.RawMessage, string, string) error { return nil },
		MarkAttemptFailedFunc: func(context.Context, models.A2AAgentCard, string, time.Time) error {
			return nil
		},
		MarkFailedFunc: func(context.Context, models.A2AAgentCard, string) error { return nil },
	}
	fetcher := &fakeA2ACardFetcher{
		FetchFunc: func(context.Context, string, bool) (json.RawMessage, error) {
			return json.RawMessage(validTestCard), nil
		},
	}
	oc := &clientmocks.OpenChoreoClientMock{
		GetComponentEndpointsFunc: func(_ context.Context, _, _, _, _ string) (map[string]models.EndpointsResponse, error) {
			return map[string]models.EndpointsResponse{
				"trip-planner-endpoint": {Endpoint: models.Endpoint{Name: "trip-planner-endpoint", URL: endpointURL}},
			}, nil
		},
	}
	return &cardReconcilerHarness{
		repo: repo, fetcher: fetcher, ocClient: oc,
		svc: &a2aCardReconcilerService{cardRepo: repo, fetcher: fetcher, ocClient: oc, logger: testLogger()},
	}
}

func TestCardReconcilerFetchesAPlatformCardThroughTheEndpoint(t *testing.T) {
	h := newCardReconcilerHarness("http://dev-org.gw.example/trip-planner/")
	row := pendingCard(models.A2AAgentCardSourcePlatform)

	h.svc.fetchOne(context.Background(), row)

	require.Len(t, h.fetcher.calls, 1)
	assert.Equal(t, "http://dev-org.gw.example/trip-planner/.well-known/agent-card.json", h.fetcher.calls[0].URL)
	assert.False(t, h.fetcher.calls[0].Guarded, "platform URLs are platform-derived")

	marked := h.repo.MarkFetchedCalls()
	require.Len(t, marked, 1)
	assert.Equal(t, row, marked[0].Read)
	assert.JSONEq(t, validTestCard, string(marked[0].Card))
	wantHash, err := a2aCardHash(json.RawMessage(validTestCard))
	require.NoError(t, err)
	assert.Equal(t, wantHash, marked[0].CardHash)
	assert.Equal(t, h.fetcher.calls[0].URL, marked[0].FetchedURL)
}

func TestCardReconcilerFetchesAnExternalCardGuarded(t *testing.T) {
	h := newCardReconcilerHarness("")
	h.ocClient.GetComponentEndpointsFunc = nil // external rows never ask OpenChoreo
	row := pendingCard(models.A2AAgentCardSourceExternal)
	row.SourceURL = "https://agent.example/.well-known/agent-card.json"

	h.svc.fetchOne(context.Background(), row)

	require.Len(t, h.fetcher.calls, 1)
	assert.Equal(t, row.SourceURL, h.fetcher.calls[0].URL)
	assert.True(t, h.fetcher.calls[0].Guarded)
	require.Len(t, h.repo.MarkFetchedCalls(), 1)
}

func TestCardReconcilerPicksTheFirstNamedEndpointWithAURL(t *testing.T) {
	h := newCardReconcilerHarness("")
	h.ocClient.GetComponentEndpointsFunc = func(_ context.Context, _, _, _, _ string) (map[string]models.EndpointsResponse, error) {
		return map[string]models.EndpointsResponse{
			"c-endpoint": {Endpoint: models.Endpoint{URL: "http://c.example"}},
			"a-endpoint": {Endpoint: models.Endpoint{URL: ""}},
			"b-endpoint": {Endpoint: models.Endpoint{URL: "http://b.example"}},
		}, nil
	}

	for i := 0; i < 5; i++ {
		h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))
	}

	for _, call := range h.fetcher.calls {
		assert.Equal(t, "http://b.example/.well-known/agent-card.json", call.URL)
	}
}

func TestCardReconcilerRetriesWhenNoEndpointIsReady(t *testing.T) {
	h := newCardReconcilerHarness("")
	before := time.Now()

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	assert.Empty(t, h.fetcher.calls)
	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.WithinDuration(t, before.Add(5*time.Second), retries[0].NextAttemptAt, time.Second)
}

func TestCardReconcilerBacksOffExponentially(t *testing.T) {
	assert.Equal(t, 5*time.Second, a2aCardRetryIn(0))
	assert.Equal(t, 10*time.Second, a2aCardRetryIn(1))
	assert.Equal(t, 20*time.Second, a2aCardRetryIn(2))
	assert.Equal(t, 40*time.Second, a2aCardRetryIn(3))
	assert.Equal(t, time.Minute, a2aCardRetryIn(4))
	assert.Equal(t, time.Minute, a2aCardRetryIn(13))
	assert.Equal(t, time.Minute, a2aCardRetryIn(1000), "no overflow")
}

func TestCardReconcilerGivesUpPastTheBudget(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("agent card request returned HTTP 503")
	}
	row := pendingCard(models.A2AAgentCardSourcePlatform)
	row.AttemptCount = a2aCardAttemptBudget - 1

	h.svc.fetchOne(context.Background(), row)

	failed := h.repo.MarkFailedCalls()
	require.Len(t, failed, 1)
	assert.Contains(t, failed[0].LastErr, "503")
	assert.Empty(t, h.repo.MarkAttemptFailedCalls())
}

func TestCardReconcilerPlatformFailuresNameTheURL(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, errors.New("agent card request returned HTTP 404")
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	retries := h.repo.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.Contains(t, retries[0].LastErr, "http://gw.example/a/.well-known/agent-card.json")
}

func TestCardReconcilerTreatsSupersededAsBenign(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.repo.MarkFetchedFunc = func(context.Context, models.A2AAgentCard, json.RawMessage, string, string) error {
		return repositories.ErrA2AAgentCardSuperseded
	}

	h.svc.fetchOne(context.Background(), pendingCard(models.A2AAgentCardSourcePlatform))

	assert.Empty(t, h.repo.MarkAttemptFailedCalls(), "a superseded outcome is not a failure")
	assert.Empty(t, h.repo.MarkFailedCalls())
}

func TestCardReconcilerRunOnceDrainsTheDueBatch(t *testing.T) {
	h := newCardReconcilerHarness("http://gw.example/a")
	h.repo.ClaimDueFunc = func(_ context.Context, _ time.Time, limit int) ([]models.A2AAgentCard, error) {
		assert.Equal(t, a2aCardBatch, limit)
		return []models.A2AAgentCard{pendingCard(models.A2AAgentCardSourcePlatform), pendingCard(models.A2AAgentCardSourcePlatform)}, nil
	}

	h.svc.RunOnce(context.Background())

	assert.Len(t, h.repo.MarkFetchedCalls(), 2)
}

func TestA2ACardHashIgnoresKeyOrderAndWhitespace(t *testing.T) {
	a, err := a2aCardHash(json.RawMessage(`{"name":"n","skills":[],"n":12345678901234567890}`))
	require.NoError(t, err)
	b, err := a2aCardHash(json.RawMessage("{ \"n\": 12345678901234567890,\n \"skills\": [], \"name\": \"n\" }"))
	require.NoError(t, err)
	c, err := a2aCardHash(json.RawMessage(`{"name":"m","skills":[],"n":12345678901234567890}`))
	require.NoError(t, err)

	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	assert.Len(t, a, 64)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `<go-env> make test-run RUN='TestCardReconciler|TestA2ACardHash' PKG=./services/`
Expected: FAIL — `undefined: a2aCardReconcilerService`.

- [ ] **Step 3: Write the reconciler**

`services/a2a_card_reconciler.go`:

```go
package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

const (
	a2aCardTickInterval = 5 * time.Second
	a2aCardBatch        = 50
	a2aCardBaseBackoff  = 5 * time.Second
	a2aCardMaxBackoff   = time.Minute
	// a2aCardAttemptBudget is about 12 minutes of backoff.
	a2aCardAttemptBudget = 14
)

// errA2ACardEndpointNotReady means the agent's environment has no public endpoint URL yet.
var errA2ACardEndpointNotReady = errors.New("agent has no public endpoint in this environment yet")

// A2ACardReconcilerService drains the a2a_agent_cards fetch queue.
type A2ACardReconcilerService interface {
	Start(ctx context.Context) error
	Stop() error
	// RunOnce drains the currently-due batch once.
	RunOnce(ctx context.Context)
}

type a2aCardReconcilerService struct {
	cardRepo repositories.A2AAgentCardRepository
	fetcher  A2ACardFetcher
	ocClient client.OpenChoreoClient
	logger   *slog.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewA2ACardReconcilerService creates an A2ACardReconcilerService.
func NewA2ACardReconcilerService(
	cardRepo repositories.A2AAgentCardRepository,
	fetcher A2ACardFetcher,
	ocClient client.OpenChoreoClient,
	logger *slog.Logger,
) A2ACardReconcilerService {
	return &a2aCardReconcilerService{
		cardRepo: cardRepo,
		fetcher:  fetcher,
		ocClient: ocClient,
		logger:   logger,
		stopCh:   make(chan struct{}),
		stopOnce: sync.Once{},
	}
}

func (s *a2aCardReconcilerService) Start(ctx context.Context) error {
	go s.runLoop(ctx)
	s.logger.Info("A2A card reconciler started")
	return nil
}

func (s *a2aCardReconcilerService) Stop() error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.logger.Info("A2A card reconciler stopped")
	})
	return nil
}

func (s *a2aCardReconcilerService) runLoop(ctx context.Context) {
	ticker := time.NewTicker(a2aCardTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.RunOnce(ctx)
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *a2aCardReconcilerService) RunOnce(ctx context.Context) {
	due, err := s.cardRepo.ClaimDue(ctx, time.Now(), a2aCardBatch)
	if err != nil {
		s.logger.Error("Failed to claim due A2A agent cards", "error", err)
		return
	}
	for _, row := range due {
		s.fetchOne(ctx, row)
	}
}

// fetchOne fetches one row's card and records the outcome.
func (s *a2aCardReconcilerService) fetchOne(ctx context.Context, row models.A2AAgentCard) {
	url, body, err := s.attemptFetch(ctx, row)
	if err != nil {
		s.recordAttemptFailure(ctx, row, err)
		return
	}
	hash, err := a2aCardHash(body)
	if err != nil {
		s.recordAttemptFailure(ctx, row, err)
		return
	}
	err = s.cardRepo.MarkFetched(ctx, row, body, hash, url)
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardSuperseded):
		s.logSuperseded(row)
	case err != nil:
		s.logger.Error("Fetched A2A agent card but failed to store it",
			"agentName", row.AgentName, "environment", row.EnvironmentName, "error", err)
	}
}

// attemptFetch returns the URL it fetched and the card it got.
func (s *a2aCardReconcilerService) attemptFetch(ctx context.Context, row models.A2AAgentCard) (string, json.RawMessage, error) {
	switch row.Source {
	case models.A2AAgentCardSourceExternal:
		body, err := s.fetcher.Fetch(ctx, row.SourceURL, true)
		return row.SourceURL, body, err
	case models.A2AAgentCardSourcePlatform:
		url, err := s.platformCardURL(ctx, row)
		if err != nil {
			return "", nil, err
		}
		body, err := s.fetcher.Fetch(ctx, url, false)
		if err != nil {
			// The derived URL is otherwise invisible to the user.
			return url, nil, fmt.Errorf("%s: %w", url, err)
		}
		return url, body, nil
	default:
		return "", nil, fmt.Errorf("unknown agent card source %q", row.Source)
	}
}

// platformCardURL is the agent's public endpoint (routed through the gateway) plus the card path.
func (s *a2aCardReconcilerService) platformCardURL(ctx context.Context, row models.A2AAgentCard) (string, error) {
	endpoints, err := s.ocClient.GetComponentEndpoints(ctx, row.OUID, row.ProjectName, row.AgentName, row.EnvironmentName)
	if err != nil {
		return "", fmt.Errorf("failed to read agent endpoints: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(endpoints)) {
		if u := endpoints[name].URL; u != "" {
			return strings.TrimRight(u, "/") + a2aAgentCardPath, nil
		}
	}
	return "", errA2ACardEndpointNotReady
}

func (s *a2aCardReconcilerService) recordAttemptFailure(ctx context.Context, row models.A2AAgentCard, cause error) {
	var err error
	if row.AttemptCount+1 >= a2aCardAttemptBudget {
		s.logger.Warn("A2A agent card could not be fetched within the attempt budget",
			"agentName", row.AgentName, "environment", row.EnvironmentName, "error", cause)
		err = s.cardRepo.MarkFailed(ctx, row, cause.Error())
	} else {
		s.logger.Debug("A2A agent card not fetchable yet, will retry",
			"agentName", row.AgentName, "environment", row.EnvironmentName,
			"attempt", row.AttemptCount+1, "reason", cause)
		err = s.cardRepo.MarkAttemptFailed(ctx, row, cause.Error(), time.Now().Add(a2aCardRetryIn(row.AttemptCount)))
	}
	switch {
	case errors.Is(err, repositories.ErrA2AAgentCardSuperseded):
		s.logSuperseded(row)
	case err != nil:
		s.logger.Error("Failed to record A2A agent card attempt", "agentName", row.AgentName, "error", err)
	}
}

func (s *a2aCardReconcilerService) logSuperseded(row models.A2AAgentCard) {
	s.logger.Info("A2A agent card was re-enqueued during the attempt; leaving it for the next tick",
		"agentName", row.AgentName, "environment", row.EnvironmentName)
}

// a2aCardRetryIn is min(5s · 2^attempt, 60s).
func a2aCardRetryIn(attempt int) time.Duration {
	delay := a2aCardBaseBackoff
	for i := 0; i < attempt && delay < a2aCardMaxBackoff; i++ {
		delay *= 2
	}
	return min(delay, a2aCardMaxBackoff)
}

// a2aCardHash is SHA-256 over the card re-encoded with sorted keys, so reformatting is not a change.
func a2aCardHash(card json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(card))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("failed to hash agent card: %w", err)
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to hash agent card: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `<go-env> make test-run RUN='TestCardReconciler|TestA2ACardHash' PKG=./services/`
Expected: PASS.

- [ ] **Step 5: Wire it and start it**

`wiring/wire.go`:
- In `serviceProviderSet`, after `services.NewA2APublicationReconcilerService,` add:
  ```go
  	services.NewA2ACardFetcher,
  	services.NewA2ACardReconcilerService,
  ```
- In `repositoryProviderSet`, after `ProvideA2APublicationRepository,` add `ProvideA2AAgentCardRepository,`.
- After `func ProvideA2APublicationRepository(...)` add:
  ```go
  func ProvideA2AAgentCardRepository(db *gorm.DB) repositories.A2AAgentCardRepository {
  	return repositories.NewA2AAgentCardRepository(db)
  }
  ```

`wiring/params.go`, after `A2APublicationReconciler services.A2APublicationReconcilerService`:
```go
	A2ACardReconciler                services.A2ACardReconcilerService
```

`app/app.go`, right after the A2A publication reconciler start block:
```go
	// Started unconditionally, like the publication reconciler that feeds it.
	a2aCardReconcilerCtx, a2aCardReconcilerCancel := context.WithCancel(backgroundCtx)
	if err := dependencies.A2ACardReconciler.Start(a2aCardReconcilerCtx); err != nil {
		slog.Error("failed to start A2A card reconciler", "error", err)
		os.Exit(1)
	}
```
and in the shutdown goroutine, right after the publication reconciler's `Stop()` block:
```go
		a2aCardReconcilerCancel()
		if err := dependencies.A2ACardReconciler.Stop(); err != nil {
			slog.Error("error stopping A2A card reconciler", "error", err)
		}
```

Run: `<go-env> make wire`
Expected: `wire_gen.go` gains `a2AAgentCardRepository := ProvideA2AAgentCardRepository(db)`, `a2ACardFetcher := services.NewA2ACardFetcher()` and `A2ACardReconciler:` in both injectors. Then `<go-env> go build ./... && <go-env> go build -tags=integration ./...` → success.

- [ ] **Step 6: Format, lint, commit**

```bash
<go-env> make fmt
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./...
git add services/a2a_card_reconciler.go services/a2a_card_reconciler_unit_test.go wiring/wire.go wiring/params.go wiring/wire_gen.go app/app.go
git commit -m "Add the A2A agent card reconciler

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 4: Queue a platform card fetch when a publication lands

**Files:**
- Modify: `agent-manager-service/services/a2a_publication_reconciler.go` (struct, constructor, `publishOne`)
- Test: `agent-manager-service/services/a2a_publication_reconciler_test.go`
- Generate: `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: `repositories.A2AAgentCardRepository.Enqueue`, `repomocks.A2AAgentCardRepositoryMock`.
- Produces: `NewA2APublicationReconcilerService(pubRepo, cardRepo repositories.A2AAgentCardRepository, deploymentRepo, gatewayRepo, agentConfigRepo, ocClient, events, logger)` — `cardRepo` is the **second** parameter. `a2aReconcilerHarness` gains `cardRepo *repomocks.A2AAgentCardRepositoryMock`.

- [ ] **Step 1: Write the failing tests**

In `services/a2a_publication_reconciler_test.go`:

1. Add `cardRepo *repomocks.A2AAgentCardRepositoryMock` to `a2aReconcilerHarness`.
2. In `newA2AReconcilerHarness`, build:
   ```go
   	cardRepo := &repomocks.A2AAgentCardRepositoryMock{
   		EnqueueFunc: func(context.Context, *models.A2AAgentCard) error { return nil },
   	}
   ```
   and set `cardRepo: cardRepo` on both the harness and the `a2aPublicationReconcilerService` literal.
3. Append:

```go
// The gateway serves the card only once it has the Agent, so publishing is the card's trigger.
func TestReconcilerQueuesAPlatformCardFetchOncePublished(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")
	pub := pendingPublication()

	h.svc.publishOne(context.Background(), pub)

	queued := h.cardRepo.EnqueueCalls()
	require.Len(t, queued, 1)
	got := queued[0].Card
	assert.Equal(t, pub.OUID, got.OUID)
	assert.Equal(t, pub.ProjectName, got.ProjectName)
	assert.Equal(t, pub.AgentName, got.AgentName)
	assert.Equal(t, pub.EnvironmentName, got.EnvironmentName)
	assert.Equal(t, models.A2AAgentCardSourcePlatform, got.Source)
}

// Best effort: the agent is published; a missed card row is recovered by the next deploy or a refresh.
func TestReconcilerPublishesEvenWhenTheCardQueueFails(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")
	h.cardRepo.EnqueueFunc = func(context.Context, *models.A2AAgentCard) error { return assert.AnError }

	h.svc.publishOne(context.Background(), pendingPublication())

	assert.Len(t, h.pubRepo.MarkPublishedCalls(), 1)
	assert.Empty(t, h.pubRepo.MarkAttemptFailedCalls())
}

// A publish whose row was superseded is redone next tick; that publish queues the card.
func TestReconcilerQueuesNoCardForASupersededPublish(t *testing.T) {
	h := newA2AReconcilerHarness("http://trip-planner.dp-default:9099")
	h.pubRepo.MarkPublishedFunc = func(context.Context, models.A2APublication, string) error {
		return repositories.ErrA2APublicationSuperseded
	}

	h.svc.publishOne(context.Background(), pendingPublication())

	assert.Empty(t, h.cardRepo.EnqueueCalls())
}

func TestReconcilerQueuesNoCardWhenThePublishFails(t *testing.T) {
	h := newA2AReconcilerHarness("")

	h.svc.publishOne(context.Background(), pendingPublication())

	assert.Empty(t, h.cardRepo.EnqueueCalls())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `<go-env> make test-run RUN=TestReconciler PKG=./services/`
Expected: FAIL to compile — `unknown field cardRepo in struct literal of type a2aPublicationReconcilerService`.

- [ ] **Step 3: Implement**

In `services/a2a_publication_reconciler.go`:
- Struct: add `cardRepo repositories.A2AAgentCardRepository` after `pubRepo`.
- Constructor: add `cardRepo repositories.A2AAgentCardRepository,` as the second parameter and `cardRepo: cardRepo,` in the literal.
- Replace the `switch` at the end of `publishOne` with:
  ```go
  	err = s.pubRepo.MarkPublished(ctx, pub, upstreamURL)
  	switch {
  	case errors.Is(err, repositories.ErrA2APublicationSuperseded):
  		s.logSuperseded(pub)
  	case err != nil:
  		s.logger.Error("Published A2A agent but failed to mark the queue row",
  			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
  	default:
  		s.enqueueCardFetch(ctx, pub)
  	}
  ```
- Add after `publishOne`:
  ```go
  // enqueueCardFetch queues a fetch of the card the gateway now serves; best effort.
  func (s *a2aPublicationReconcilerService) enqueueCardFetch(ctx context.Context, pub models.A2APublication) {
  	card := &models.A2AAgentCard{
  		OUID:            pub.OUID,
  		ProjectName:     pub.ProjectName,
  		AgentName:       pub.AgentName,
  		EnvironmentName: pub.EnvironmentName,
  		Source:          models.A2AAgentCardSourcePlatform,
  	}
  	if err := s.cardRepo.Enqueue(ctx, card); err != nil {
  		s.logger.Warn("Published A2A agent but failed to queue its card fetch",
  			"agentName", pub.AgentName, "environment", pub.EnvironmentName, "error", err)
  	}
  }
  ```

Run: `<go-env> make wire` (the constructor gained a parameter; wire resolves it from `ProvideA2AAgentCardRepository`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `<go-env> make test-run RUN=TestReconciler PKG=./services/` then `<go-env> go build ./...`
Expected: PASS; build succeeds.

- [ ] **Step 5: Format, lint, commit**

```bash
<go-env> make fmt
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./wiring/...
git add services/a2a_publication_reconciler.go services/a2a_publication_reconciler_test.go wiring/wire_gen.go
git commit -m "Queue an A2A card fetch when an agent is published to its gateway

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 5: Delete card rows with the agent

**Files:**
- Modify: `agent-manager-service/services/agent_manager.go` (struct ~line 82, `NewAgentManagerService` ~line 104, `deleteAgentAPIArtifact` ~line 2838)
- Test: `agent-manager-service/services/a2a_agent_deletion_unit_test.go`
- Generate: `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: `repositories.A2AAgentCardRepository.DeleteForAgent`.
- Produces: `agentManagerService.a2aCardRepo` field; `NewAgentManagerService` gains `a2aCardRepo repositories.A2AAgentCardRepository` right after `a2aPublicationRepo`.

- [ ] **Step 1: Write the failing tests**

In `services/a2a_agent_deletion_unit_test.go`:
1. Add `cardDeletes int` to `agentArtifactDeletionFixture`.
2. In `newAgentArtifactDeletionFixture`, after `pubRepo`, add:
   ```go
   	cardRepo := &repomocks.A2AAgentCardRepositoryMock{
   		DeleteForAgentFunc: func(context.Context, string, string, string) error {
   			f.steps = append(f.steps, "delete-cards")
   			f.cardDeletes++
   			return nil
   		},
   	}
   ```
   and `a2aCardRepo: cardRepo,` in the `agentManagerService` literal.
3. In `TestDeleteAgentAPIArtifactBroadcastsLast`, change the expectation to
   `[]string{"delete-queue", "delete-cards", "collect", "delete-artifact", "broadcast"}`.
4. Append:

```go
// Card rows go with the agent, external A2A agents included, so a same-named agent starts clean.
func TestDeleteAgentAPIArtifactClearsCardRowsOnce(t *testing.T) {
	devID := uuid.New()
	f := newAgentArtifactDeletionFixture(t, map[string]uuid.UUID{"dev": devID}, nil)
	f.svc.gatewayRepo = &repomocks.GatewayRepositoryMock{}

	f.svc.deleteAgentAPIArtifact(context.Background(), "org-1", "proj", "agent", false)

	assert.Equal(t, 1, f.cardDeletes)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `<go-env> make test-run RUN=TestDeleteAgentAPIArtifact PKG=./services/`
Expected: FAIL to compile — `unknown field a2aCardRepo`.

- [ ] **Step 3: Implement**

In `services/agent_manager.go`:
- Struct: add `a2aCardRepo repositories.A2AAgentCardRepository` after `a2aPublicationRepo`.
- `NewAgentManagerService`: add `a2aCardRepo repositories.A2AAgentCardRepository,` after `a2aPublicationRepo repositories.A2APublicationRepository,` and `a2aCardRepo: a2aCardRepo,` in the literal.
- In `deleteAgentAPIArtifact`, directly after the `a2aPublicationRepo.DeleteForAgent` block:
  ```go
  	if s.a2aCardRepo != nil {
  		if cardErr := s.a2aCardRepo.DeleteForAgent(ctx, ouID, projectName, agentName); cardErr != nil {
  			s.logger.Warn("Failed to clear A2A agent card rows for deleted agent",
  				"agentName", agentName, "error", cardErr)
  		}
  	}
  ```

Run: `<go-env> make wire`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `<go-env> make test-run RUN=TestDeleteAgentAPIArtifact PKG=./services/` then `<go-env> go build ./... && <go-env> go build -tags=integration ./...`
Expected: PASS; builds succeed.

- [ ] **Step 5: Format, lint, commit**

```bash
<go-env> make fmt
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./wiring/...
git add services/agent_manager.go services/a2a_agent_deletion_unit_test.go wiring/wire_gen.go
git commit -m "Delete an agent's stored A2A cards with the agent

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 6: Persist the external A2A subtype, and audit the M1 gates

**Files:**
- Modify: `agent-manager-service/clients/openchoreosvc/client/components.go` (`buildExternalAgentComponentRequestBody` ~line 169, `convertComponentFromTyped` ~line 3058)
- Test: `agent-manager-service/clients/openchoreosvc/client/component_labels_test.go`, `agent-manager-service/tests/create_agent_a2a_test.go`

**Interfaces:**
- Produces: `GetComponent` returns `Type.SubType == "a2a-agent"` for an external A2A agent. Task 7's service relies on this.

**Gate audit result (already verified while planning — record it in the commit message):** every M1 `IsA2AAgentSubType` site is safe for external agents. Create attaches traits and enqueues publications only inside `if isInternal` (`createComponentAgent`); `DeployAgent`, `PromoteAgent` and `UpdateAgentDeploySettings` reject non-internal / non-`agent-api` agents before their A2A branches; `republishA2AAgent` is reached only from those three; `DeleteAgent` treating an external A2A agent as A2A only adds a harmless empty publication-row delete and a gateway-target lookup that finds no deployments. The one gap is the subtype not round-tripping, fixed here, and the integration test below pins the create path.

- [ ] **Step 1: Write the failing unit tests**

Append to `clients/openchoreosvc/client/component_labels_test.go`:

```go
// An external A2A agent's subtype is what the card API and M3 search filter on.
func TestBuildExternalAgentComponentRequestBody_A2ASubTypeLabel(t *testing.T) {
	req := CreateComponentRequest{
		Name:             "agent-1",
		DisplayName:      "Agent 1",
		ProvisioningType: "external",
		AgentType:        AgentTypeConfig{Type: "external-agent-api", SubType: "a2a-agent"},
	}
	body, err := buildExternalAgentComponentRequestBody("ns", "proj", req)
	require.NoError(t, err)
	labels := *body.Metadata.Labels
	assert.Equal(t, "a2a-agent", labels[string(LabelKeyAgentSubType)])
}

// Other external subtypes stay free-form and are never validated as label values.
func TestBuildExternalAgentComponentRequestBody_FreeFormSubTypeNotLabelled(t *testing.T) {
	req := CreateComponentRequest{
		Name:             "agent-1",
		DisplayName:      "Agent 1",
		ProvisioningType: "external",
		AgentType:        AgentTypeConfig{Type: "external-agent-api", SubType: "My Custom Agent!"},
	}
	body, err := buildExternalAgentComponentRequestBody("ns", "proj", req)
	require.NoError(t, err)
	_, has := (*body.Metadata.Labels)[string(LabelKeyAgentSubType)]
	assert.False(t, has)
}

func TestConvertComponentFromTyped_ExternalA2ASubType(t *testing.T) {
	labels := map[string]string{
		string(LabelKeyProvisioningType): "external",
		string(LabelKeyAgentSubType):     "a2a-agent",
	}
	comp := &gen.Component{
		Metadata: gen.ObjectMeta{Name: "agent-1", Labels: &labels},
		Spec: &gen.ComponentSpec{
			ComponentType: struct {
				Kind *gen.ComponentSpecComponentTypeKind `json:"kind,omitempty"`
				Name string                              `json:"name"`
			}{Name: "external-agent/external-agent-api"},
			Owner: struct {
				ProjectName string `json:"projectName"`
			}{ProjectName: "proj"},
		},
	}
	agent, err := convertComponentFromTyped(comp)
	require.NoError(t, err)
	assert.Equal(t, "external", agent.Provisioning.Type)
	assert.Equal(t, "a2a-agent", agent.Type.SubType)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `<go-env> make test-run RUN='TestBuildExternalAgentComponentRequestBody|TestConvertComponentFromTyped' PKG=./clients/openchoreosvc/client/`
Expected: FAIL — the label is absent and `SubType` is `""`.

- [ ] **Step 3: Implement**

`buildExternalAgentComponentRequestBody`, right after the `labels := map[string]string{...}` literal:
```go
	// Only the subtype AMS acts on is labelled; other external subtypes stay free-form.
	if utils.IsA2AAgentSubType(req.AgentType.SubType) {
		labels[string(LabelKeyAgentSubType)] = req.AgentType.SubType
	}
```

`convertComponentFromTyped`: replace
```go
	if provisioningType == string(utils.InternalAgent) {
		agentType.SubType = getLabel(comp.Metadata.Labels, string(LabelKeyAgentSubType))
	}
```
with
```go
	agentType.SubType = getLabel(comp.Metadata.Labels, string(LabelKeyAgentSubType))
```
(Confirm `utils` is already imported in `components.go`; it is used by `utils.ValidateLabelValue`.)

- [ ] **Step 4: Run them to verify they pass**

Run the Step 2 command, then the whole package: `<go-env> make test-run RUN=. PKG=./clients/openchoreosvc/client/`
Expected: PASS (the existing `TestBuildExternalAgentComponentRequestBody_UserLabels` still sees 2 labels: `custom-api` is not labelled).

- [ ] **Step 5: Write the create-path gate integration test**

Append to `tests/create_agent_a2a_test.go`:

```go
// An external A2A agent gets no gateway route trait and no publication; only its subtype is recorded.
func TestCreateExternalA2AAgentGetsNoGatewayPublication(t *testing.T) {
	authMiddleware := jwtassertion.NewMockMiddleware(t)
	openChoreoClient := apitestutils.CreateMockOpenChoreoClient()
	testClients := wiring.TestClients{
		OpenChoreoClient: openChoreoClient,
		SecretMgmtClient: apitestutils.CreateMockSecretManagementClient(),
	}
	app := apitestutils.MakeAppClientWithDeps(t, testClients, authMiddleware)
	agentName := fmt.Sprintf("ext-a2a-%s", uuid.New().String()[:5])

	reqBody := new(bytes.Buffer)
	require.NoError(t, json.NewEncoder(reqBody).Encode(map[string]interface{}{
		"name":         agentName,
		"displayName":  "External A2A",
		"provisioning": map[string]interface{}{"type": "external"},
		"agentType":    map[string]interface{}{"type": "external-agent-api", "subType": "a2a-agent"},
	}))
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents", testOrgName, testProjName), reqBody)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code, "body: %s", rr.Body.String())

	created := openChoreoClient.CreateComponentCalls()
	require.Len(t, created, 1)
	require.Equal(t, "a2a-agent", created[0].Req.AgentType.SubType)
	require.Empty(t, openChoreoClient.AttachTraitsCalls(), "no a2a-gateway-route or any other trait")

	var queued int64
	require.NoError(t, db.GetDB().Model(&models.A2APublication{}).
		Where("agent_name = ?", agentName).Count(&queued).Error)
	require.Zero(t, queued, "no gateway publication is queued")
}
```

Add `"github.com/wso2/agent-manager/agent-manager-service/db"` to the imports. Check the moq field name for `CreateComponent`'s request argument in `clients/clientmocks/openchoreo_client_fake.go` (`grep -n "CreateComponentCalls" -A 8`) and use it in place of `.Req` if it differs.

Run: the integration command with `-run 'TestCreateExternalA2AAgentGetsNoGatewayPublication|TestCreateA2AAgentOmitsAPIConfigurationTrait' ./tests/`
Expected: PASS.

- [ ] **Step 6: Format, lint, commit**

```bash
<go-env> make fmt
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./clients/... ./tests/...
git add clients/openchoreosvc/client/components.go clients/openchoreosvc/client/component_labels_test.go tests/create_agent_a2a_test.go
git commit -m "Record the a2a-agent subtype on external agents

The M1 A2A gates already skip external agents: create attaches traits and
queues publications only for internal agents, and deploy, promote and
deploy-settings reject external ones first. What was missing is the subtype
itself, which the external builder never labelled and the reader dropped.

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 7: Card service (GET / refresh / source)

**Files:**
- Modify: `agent-manager-service/utils/errors.go`, `agent-manager-service/services/agent_manager.go` (`requireEnvTier` ~line 3873), `agent-manager-service/audit/actions_domain.go`
- Create: `agent-manager-service/services/a2a_agent_card_service.go`
- Test: `agent-manager-service/services/a2a_agent_card_service_unit_test.go`

**Interfaces:**
- Consumes: Task 1 repo; `client.OpenChoreoClient.GetComponent` / `GetEnvironment`; `translateAgentError`, `translateEnvironmentError`; test helpers `tierGrantedCtx(t)`, `tierCtx(t, scopes...)` (`agent_env_tier_unit_test.go`), `testLogger()`.
- Produces:
  - `utils.ErrAgentCardNotFound`, `utils.ErrAgentNotA2A`, `utils.ErrAgentCardSourceNotExternal` (sentinels).
  - `audit.ActionAgentSetCardSource Action = "agent:set-card-source"`, `audit.ActionAgentRemoveCardSource Action = "agent:remove-card-source"`.
  - `func requireEnvironmentTier(ctx context.Context, oc client.OpenChoreoClient, logger *slog.Logger, ouID, envName string) (*models.EnvironmentResponse, error)`
  - ```go
    type A2AAgentCardService interface {
    	GetA2AAgentCard(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AAgentCard, error)
    	RefreshA2AAgentCard(ctx context.Context, ouID, projectName, agentName, envName string) error
    	SetA2ACardSource(ctx context.Context, ouID, projectName, agentName, envName, sourceURL string) error
    	DeleteA2ACardSource(ctx context.Context, ouID, projectName, agentName, envName string) error
    }
    func NewA2AAgentCardService(ocClient client.OpenChoreoClient, cardRepo repositories.A2AAgentCardRepository, logger *slog.Logger) A2AAgentCardService
    ```
  - `const a2aCardSourceURLMaxLen = 2048`

- [ ] **Step 1: Add the sentinels and audit actions**

`utils/errors.go`, in the sentinel `var (...)` block next to `ErrAgentNotFound`:
```go
	ErrAgentCardNotFound          = errors.New("agent card not found")
	ErrAgentNotA2A                = errors.New("agent is not an A2A agent")
	ErrAgentCardSourceNotExternal = errors.New("a card source can be set only for external A2A agents")
```

`audit/actions_domain.go`: in the `const` block next to `ActionAgentConfigUpdate`:
```go
	ActionAgentSetCardSource    Action = "agent:set-card-source"
	ActionAgentRemoveCardSource Action = "agent:remove-card-source"
```
and in `init()` next to the agent-config registrations:
```go
	Register(ActionAgentSetCardSource, ClassConfig, SeverityInfo)
	RegisterDetailSchema(ActionAgentSetCardSource, map[string]FieldKind{
		"agentName": KindName,
		"sourceUrl": KindURL,
	})
	Register(ActionAgentRemoveCardSource, ClassConfig, SeverityNotice)
	RegisterDetailSchema(ActionAgentRemoveCardSource, map[string]FieldKind{
		"agentName": KindName,
	})
```

- [ ] **Step 2: Extract the env-tier check**

In `services/agent_manager.go`, turn the body of `requireEnvTier` into a package function and keep the method as a wrapper, so existing callers are untouched:
```go
func (s *agentManagerService) requireEnvTier(
	ctx context.Context, ouID, envName string,
) (*models.EnvironmentResponse, error) {
	return requireEnvironmentTier(ctx, s.ocClient, s.logger, ouID, envName)
}

// requireEnvironmentTier resolves envName and refuses callers without its tier scope.
func requireEnvironmentTier(
	ctx context.Context, oc client.OpenChoreoClient, logger *slog.Logger, ouID, envName string,
) (*models.EnvironmentResponse, error) {
	// <the former method body, verbatim, with s.ocClient → oc and s.logger → logger>
}
```
Keep the long doc comment above `requireEnvTier` where it is. Run `<go-env> make test-run RUN=TestRequireEnvTier PKG=./services/` → PASS (pure refactor).

- [ ] **Step 3: Write the failing service tests**

`services/a2a_agent_card_service_unit_test.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// cardServiceFor builds the service over an agent of the given provisioning and subtype.
func cardServiceFor(provisioning, subType string) (*a2aAgentCardService, *repomocks.A2AAgentCardRepositoryMock, *clientmocks.OpenChoreoClientMock) {
	oc := &clientmocks.OpenChoreoClientMock{
		GetComponentFunc: func(_ context.Context, _, _, name string) (*models.AgentResponse, error) {
			return &models.AgentResponse{
				UUID: "agent-uuid", Name: name,
				Provisioning: models.Provisioning{Type: provisioning},
				Type:         models.AgentType{SubType: subType},
			}, nil
		},
		GetEnvironmentFunc: func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
			return &models.EnvironmentResponse{UUID: "env-uuid", Name: name}, nil
		},
	}
	repo := &repomocks.A2AAgentCardRepositoryMock{
		EnqueueFunc:           func(context.Context, *models.A2AAgentCard) error { return nil },
		DeleteForAgentEnvFunc: func(context.Context, string, string, string, string) error { return nil },
	}
	return &a2aAgentCardService{ocClient: oc, cardRepo: repo, logger: testLogger()}, repo, oc
}

func TestGetA2AAgentCardReturnsTheStoredRow(t *testing.T) {
	svc, repo, oc := cardServiceFor("internal", "a2a-agent")
	stored := &models.A2AAgentCard{Status: models.A2AAgentCardStatusFetched, Card: json.RawMessage(validTestCard)}
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) { return stored, nil }
	oc.GetEnvironmentFunc = nil // a stored row proves the environment

	got, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	require.NoError(t, err)
	assert.Same(t, stored, got)
}

func TestGetA2AAgentCardIsPendingForAnUnpublishedPlatformAgent(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}

	got, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	require.NoError(t, err)
	assert.Equal(t, models.A2AAgentCardSourcePlatform, got.Source)
	assert.Equal(t, models.A2AAgentCardStatusPending, got.Status)
	assert.Nil(t, got.Card)
}

func TestGetA2AAgentCardIsNotFoundForAnExternalAgentWithNoSource(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardNotFound)
}

func TestGetA2AAgentCardIsNotFoundForANonA2AAgent(t *testing.T) {
	svc, _, _ := cardServiceFor("internal", "chat-api")
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardNotFound)
}

func TestGetA2AAgentCardDoesNotMaskRealErrors(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, assert.AnError
	}
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotErrorIs(t, err, utils.ErrAgentCardNotFound)
}

func TestGetA2AAgentCardMapsAMissingAgent(t *testing.T) {
	svc, _, oc := cardServiceFor("internal", "a2a-agent")
	oc.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) {
		return nil, utils.ErrNotFound
	}
	_, err := svc.GetA2AAgentCard(context.Background(), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentNotFound)
}

func TestRefreshA2AAgentCardQueuesAPlatformFetch(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	require.NoError(t, svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	queued := repo.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2AAgentCardSourcePlatform, queued[0].Card.Source)
	assert.Empty(t, queued[0].Card.SourceURL)
}

func TestRefreshA2AAgentCardKeepsTheExternalSource(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{Source: models.A2AAgentCardSourceExternal, SourceURL: "https://a.example/x"}, nil
	}
	require.NoError(t, svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	queued := repo.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2AAgentCardSourceExternal, queued[0].Card.Source)
	assert.Empty(t, queued[0].Card.SourceURL, "an empty URL leaves the stored one in place")
}

func TestRefreshA2AAgentCardNeedsAnExternalSource(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	repo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}
	err := svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardNotFound)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestRefreshA2AAgentCardRejectsANonA2AAgent(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "custom-api")
	err := svc.RefreshA2AAgentCard(tierGrantedCtx(t), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentNotA2A)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestRefreshA2AAgentCardEnforcesTheEnvironmentTier(t *testing.T) {
	svc, repo, oc := cardServiceFor("internal", "a2a-agent")
	oc.GetEnvironmentFunc = func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
		return &models.EnvironmentResponse{Name: name, IsProduction: true}, nil
	}
	err := svc.RefreshA2AAgentCard(tierCtx(t, rbac.AgentEnvNonProduction), "org", "proj", "agent", "prod")
	assert.ErrorIs(t, err, utils.ErrForbidden)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestSetA2ACardSourceQueuesTheExternalURL(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	const url = "https://93.184.215.14/.well-known/agent-card.json"

	require.NoError(t, svc.SetA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev", "  "+url+" "))

	queued := repo.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2AAgentCardSourceExternal, queued[0].Card.Source)
	assert.Equal(t, url, queued[0].Card.SourceURL)
	assert.Equal(t, "dev", queued[0].Card.EnvironmentName)
}

func TestSetA2ACardSourceRejects(t *testing.T) {
	cases := []struct {
		name, provisioning, subType, url string
		want                             error
	}{
		{"non-A2A agent", "external", "custom-api", "https://93.184.215.14/c", utils.ErrAgentNotA2A},
		{"platform agent", "internal", "a2a-agent", "https://93.184.215.14/c", utils.ErrAgentCardSourceNotExternal},
		{"loopback URL", "external", "a2a-agent", "http://127.0.0.1/c", utils.ErrInvalidURL},
		{"non-http scheme", "external", "a2a-agent", "ftp://93.184.215.14/c", utils.ErrInvalidURL},
		{"empty URL", "external", "a2a-agent", "   ", utils.ErrInvalidURL},
		{"too long", "external", "a2a-agent", "https://93.184.215.14/" + strings.Repeat("a", 2048), utils.ErrInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _ := cardServiceFor(tc.provisioning, tc.subType)
			err := svc.SetA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev", tc.url)
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, repo.EnqueueCalls())
		})
	}
}

func TestSetA2ACardSourceRejectsAnUnknownEnvironment(t *testing.T) {
	svc, repo, oc := cardServiceFor("external", "a2a-agent")
	oc.GetEnvironmentFunc = func(context.Context, string, string) (*models.EnvironmentResponse, error) {
		return nil, utils.ErrNotFound
	}
	err := svc.SetA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "nope", "https://93.184.215.14/c")
	assert.ErrorIs(t, err, utils.ErrEnvironmentNotFound)
	assert.Empty(t, repo.EnqueueCalls())
}

func TestDeleteA2ACardSourceRemovesThatEnvironmentsRow(t *testing.T) {
	svc, repo, _ := cardServiceFor("external", "a2a-agent")
	require.NoError(t, svc.DeleteA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev"))
	deleted := repo.DeleteForAgentEnvCalls()
	require.Len(t, deleted, 1)
	assert.Equal(t, "dev", deleted[0].EnvironmentName)
}

func TestDeleteA2ACardSourceRejectsAPlatformAgent(t *testing.T) {
	svc, repo, _ := cardServiceFor("internal", "a2a-agent")
	err := svc.DeleteA2ACardSource(tierGrantedCtx(t), "org", "proj", "agent", "dev")
	assert.ErrorIs(t, err, utils.ErrAgentCardSourceNotExternal)
	assert.Empty(t, repo.DeleteForAgentEnvCalls())
}
```

Add `"strings"` and `"github.com/wso2/agent-manager/agent-manager-service/rbac"` to the imports. (`93.184.215.14` is a public literal IP, so `ssrf.ValidateURL` passes without DNS.)

- [ ] **Step 4: Run them to verify they fail**

Run: `<go-env> make test-run RUN='A2AAgentCard|A2ACardSource' PKG=./services/`
Expected: FAIL — `undefined: a2aAgentCardService`.

- [ ] **Step 5: Write the service**

`services/a2a_agent_card_service.go`:

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
	"github.com/wso2/agent-manager/agent-manager-service/utils/ssrf"
)

const a2aCardSourceURLMaxLen = 2048

// A2AAgentCardService reads and manages stored A2A agent cards.
type A2AAgentCardService interface {
	GetA2AAgentCard(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AAgentCard, error)
	RefreshA2AAgentCard(ctx context.Context, ouID, projectName, agentName, envName string) error
	SetA2ACardSource(ctx context.Context, ouID, projectName, agentName, envName, sourceURL string) error
	DeleteA2ACardSource(ctx context.Context, ouID, projectName, agentName, envName string) error
}

type a2aAgentCardService struct {
	ocClient client.OpenChoreoClient
	cardRepo repositories.A2AAgentCardRepository
	logger   *slog.Logger
}

// NewA2AAgentCardService creates an A2AAgentCardService.
func NewA2AAgentCardService(
	ocClient client.OpenChoreoClient,
	cardRepo repositories.A2AAgentCardRepository,
	logger *slog.Logger,
) A2AAgentCardService {
	return &a2aAgentCardService{ocClient: ocClient, cardRepo: cardRepo, logger: logger}
}

func (s *a2aAgentCardService) GetA2AAgentCard(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AAgentCard, error) {
	agent, err := s.getAgent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, err
	}
	if !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return nil, utils.ErrAgentCardNotFound
	}
	row, err := s.cardRepo.Get(ctx, ouID, projectName, agentName, envName)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
		return nil, fmt.Errorf("failed to read agent card: %w", err)
	}
	if isExternalAgent(agent) {
		return nil, utils.ErrAgentCardNotFound
	}
	// Never published here yet; confirm the environment before reporting pending.
	if _, err := s.ocClient.GetEnvironment(ctx, ouID, envName); err != nil {
		return nil, translateEnvironmentError(err)
	}
	return &models.A2AAgentCard{
		OUID:            ouID,
		ProjectName:     projectName,
		AgentName:       agentName,
		EnvironmentName: envName,
		Source:          models.A2AAgentCardSourcePlatform,
		Status:          models.A2AAgentCardStatusPending,
	}, nil
}

func (s *a2aAgentCardService) RefreshA2AAgentCard(ctx context.Context, ouID, projectName, agentName, envName string) error {
	agent, err := s.getA2AAgent(ctx, ouID, projectName, agentName)
	if err != nil {
		return err
	}
	if _, err := requireEnvironmentTier(ctx, s.ocClient, s.logger, ouID, envName); err != nil {
		return err
	}
	source := models.A2AAgentCardSourcePlatform
	if isExternalAgent(agent) {
		if _, err := s.cardRepo.Get(ctx, ouID, projectName, agentName, envName); err != nil {
			if errors.Is(err, repositories.ErrA2AAgentCardNotFound) {
				return utils.ErrAgentCardNotFound
			}
			return fmt.Errorf("failed to read agent card: %w", err)
		}
		source = models.A2AAgentCardSourceExternal
	}
	return s.enqueue(ctx, ouID, projectName, agentName, envName, source, "")
}

func (s *a2aAgentCardService) SetA2ACardSource(ctx context.Context, ouID, projectName, agentName, envName, sourceURL string) error {
	agent, err := s.getExternalA2AAgent(ctx, ouID, projectName, agentName)
	if err != nil {
		return err
	}
	sourceURL = strings.TrimSpace(sourceURL)
	if err := validateCardSourceURL(ctx, sourceURL); err != nil {
		return err
	}
	if _, err := requireEnvironmentTier(ctx, s.ocClient, s.logger, ouID, envName); err != nil {
		return err
	}
	err = s.enqueue(ctx, ouID, projectName, agentName, envName, models.A2AAgentCardSourceExternal, sourceURL)
	audit.Record(ctx, audit.ActionAgentSetCardSource,
		audit.Org(ouID),
		audit.ResourceNamed(audit.ResourceAgent, agent.UUID, agentName),
		audit.Project(projectName),
		audit.Environment(envName),
		audit.Detail("agentName", agentName),
		audit.Detail("sourceUrl", sourceURL),
		audit.Result(err),
	)
	return err
}

func (s *a2aAgentCardService) DeleteA2ACardSource(ctx context.Context, ouID, projectName, agentName, envName string) error {
	agent, err := s.getExternalA2AAgent(ctx, ouID, projectName, agentName)
	if err != nil {
		return err
	}
	if _, err := requireEnvironmentTier(ctx, s.ocClient, s.logger, ouID, envName); err != nil {
		return err
	}
	err = s.cardRepo.DeleteForAgentEnv(ctx, ouID, projectName, agentName, envName)
	if err != nil {
		err = fmt.Errorf("failed to delete agent card: %w", err)
	}
	audit.Record(ctx, audit.ActionAgentRemoveCardSource,
		audit.Org(ouID),
		audit.ResourceNamed(audit.ResourceAgent, agent.UUID, agentName),
		audit.Project(projectName),
		audit.Environment(envName),
		audit.Detail("agentName", agentName),
		audit.Result(err),
	)
	return err
}

func (s *a2aAgentCardService) enqueue(ctx context.Context, ouID, projectName, agentName, envName string, source models.A2AAgentCardSource, sourceURL string) error {
	card := &models.A2AAgentCard{
		OUID:            ouID,
		ProjectName:     projectName,
		AgentName:       agentName,
		EnvironmentName: envName,
		Source:          source,
		SourceURL:       sourceURL,
	}
	if err := s.cardRepo.Enqueue(ctx, card); err != nil {
		return fmt.Errorf("failed to queue agent card fetch: %w", err)
	}
	return nil
}

func (s *a2aAgentCardService) getAgent(ctx context.Context, ouID, projectName, agentName string) (*models.AgentResponse, error) {
	agent, err := s.ocClient.GetComponent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, translateAgentError(err)
	}
	return agent, nil
}

func (s *a2aAgentCardService) getA2AAgent(ctx context.Context, ouID, projectName, agentName string) (*models.AgentResponse, error) {
	agent, err := s.getAgent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, err
	}
	if !utils.IsA2AAgentSubType(agent.Type.SubType) {
		return nil, utils.ErrAgentNotA2A
	}
	return agent, nil
}

func (s *a2aAgentCardService) getExternalA2AAgent(ctx context.Context, ouID, projectName, agentName string) (*models.AgentResponse, error) {
	agent, err := s.getA2AAgent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, err
	}
	if !isExternalAgent(agent) {
		return nil, utils.ErrAgentCardSourceNotExternal
	}
	return agent, nil
}

func isExternalAgent(agent *models.AgentResponse) bool {
	return agent.Provisioning.Type == string(utils.ExternalAgent)
}

// validateCardSourceURL wraps every rejection in utils.ErrInvalidURL.
func validateCardSourceURL(ctx context.Context, sourceURL string) error {
	if sourceURL == "" {
		return fmt.Errorf("%w: url is required", utils.ErrInvalidURL)
	}
	if len(sourceURL) > a2aCardSourceURLMaxLen {
		return fmt.Errorf("%w: url must be at most %d characters", utils.ErrInvalidURL, a2aCardSourceURLMaxLen)
	}
	if err := ssrf.ValidateURL(ctx, sourceURL); err != nil {
		return fmt.Errorf("%w: %w", utils.ErrInvalidURL, err)
	}
	return nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `<go-env> make test-run RUN='A2AAgentCard|A2ACardSource|TestRequireEnvTier' PKG=./services/` and `<go-env> make test-run RUN=. PKG=./audit/`
Expected: PASS (the audit package's `TestRegisteredActionsHaveDetailSchemas` covers the new actions).

- [ ] **Step 7: Format, lint, commit**

```bash
<go-env> make fmt
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./services/... ./utils/... ./audit/...
git add utils/errors.go audit/actions_domain.go services/agent_manager.go services/a2a_agent_card_service.go services/a2a_agent_card_service_unit_test.go
git commit -m "Add the A2A agent card service

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 8: Card API (OpenAPI, controller, routes, audit labels)

**Files:**
- Modify: `agent-manager-service/docs/api_v1_openapi.yaml`; regenerate `agent-manager-service/spec/`
- Create: `agent-manager-service/controllers/a2a_agent_card_controller.go`, `agent-manager-service/api/a2a_agent_card_routes.go`
- Modify: `agent-manager-service/api/app.go`, `agent-manager-service/api/audit_coverage_test.go`, `agent-manager-service/audit/policy.go`, `agent-manager-service/audit/actions_domain_test.go`, `agent-manager-service/docs/audit-logging.md`, `agent-manager-service/wiring/wire.go`, `agent-manager-service/wiring/params.go`
- Generate: `agent-manager-service/wiring/wire_gen.go`

**Interfaces:**
- Consumes: Task 7 `A2AAgentCardService`, sentinels, audit actions; `handleCommonErrors(w, err, fallbackMsg)` (controllers package); `middleware.OUIDFromRequest`; `utils.PathParamProjName/PathParamAgentName/PathParamEnvID`.
- Produces: routes
  - `GET    /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card` → 200 `AgentCardResponse`
  - `POST   …/agent-card/refresh` → 202
  - `PUT    …/agent-card/source` body `SetAgentCardSourceRequest {url}` → 202
  - `DELETE …/agent-card/source` → 204
  - `spec.AgentCardResponse` (`NewAgentCardResponse(source, status, sourceUrl, lastError string)`, `SetCard(map[string]interface{})`, `SetFetchedAt(time.Time)`), `spec.SetAgentCardSourceRequest{Url string}`.
  - `controllers.A2AAgentCardController`, `controllers.NewA2AAgentCardController(svc services.A2AAgentCardService)`; `wiring.AppParams.A2AAgentCardController`.

- [ ] **Step 1: Write the failing audit-label rows**

In `audit/actions_domain_test.go`, append to `pairs` in `TestDomainActionsMatchRouteDerivedActions`:
```go
		{
			"PUT /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/source",
			[]rbac.Permission{rbac.AgentUpdate, rbac.AgentEnvNonProduction}, ActionAgentSetCardSource,
		},
		{
			"DELETE /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/source",
			[]rbac.Permission{rbac.AgentUpdate, rbac.AgentEnvNonProduction}, ActionAgentRemoveCardSource,
		},
```
Run: `<go-env> make test-run RUN=TestDomainActionsMatchRouteDerivedActions PKG=./audit/`
Expected: FAIL — derived `agent:update`, want `agent:set-card-source`.

- [ ] **Step 2: Add the overrides**

`audit/policy.go`, in `actionOverrides` under the "Agent sub-resource updates" group:
```go
	"POST /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/refresh": "agent:refresh-card",
	"PUT /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/source":   "agent:set-card-source",
	"DELETE /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/source": "agent:remove-card-source",
```
Re-run Step 1's command → PASS. (`TestNoStaleAuditPolicyEntries` will fail until Step 6 registers the routes; that is expected mid-task.)

- [ ] **Step 3: Add the OpenAPI paths and schemas**

In `docs/api_v1_openapi.yaml`, directly after the `.../environments/{envID}/api-keys/{keyName}` path block:

```yaml
  /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card:
    parameters: &agentCardPathParams
      - name: orgName
        in: path
        required: true
        description: Organization name/handle
        schema:
          type: string
      - name: projName
        in: path
        required: true
        description: Project name
        schema:
          type: string
      - name: agentName
        in: path
        required: true
        description: Agent name
        schema:
          type: string
      - name: envID
        in: path
        required: true
        description: Environment name
        schema:
          type: string
    get:
      tags:
        - Agent Card
      summary: Get an A2A agent's stored agent card for an environment
      description: Returns the last good public agent card the platform fetched, and the state of the current fetch. A platform agent that was never published to the environment reports `pending` with no card.
      operationId: getAgentCard
      responses:
        "200":
          description: Stored agent card and fetch state
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/AgentCardResponse"
        "401":
          description: Unauthorized
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: Agent, environment or card not found, or the agent is not an A2A agent
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"

  /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/refresh:
    parameters: *agentCardPathParams
    post:
      tags:
        - Agent Card
      summary: Re-fetch an A2A agent's card
      description: Queues a fetch of the agent card. The stored card is kept until the new fetch succeeds.
      operationId: refreshAgentCard
      responses:
        "202":
          description: Fetch queued
        "400":
          description: The agent is not an A2A agent
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "403":
          description: Missing the environment tier permission
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: Agent or environment not found, or an external agent has no card source
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"

  /orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card/source:
    parameters: *agentCardPathParams
    put:
      tags:
        - Agent Card
      summary: Register an external A2A agent's card URL for an environment
      description: Sets the URL the platform fetches the card from and queues a fetch. External A2A agents only.
      operationId: setAgentCardSource
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/SetAgentCardSourceRequest"
      responses:
        "202":
          description: Source saved and fetch queued
        "400":
          description: Invalid URL, or the agent is not an external A2A agent
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "403":
          description: Missing the environment tier permission
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: Agent or environment not found
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
    delete:
      tags:
        - Agent Card
      summary: Remove an external A2A agent's card source for an environment
      description: Deletes the stored card and its source. Idempotent. External A2A agents only.
      operationId: deleteAgentCardSource
      responses:
        "204":
          description: Source removed
        "400":
          description: The agent is not an external A2A agent
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "403":
          description: Missing the environment tier permission
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "404":
          description: Agent or environment not found
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
        "500":
          description: Internal server error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorResponse"
```

If the repo's spec tooling (`npx openapi-format` in `gen_client.sh`) rejects YAML anchors, spell the four parameters out under each of the three paths instead.

Under `components.schemas`, next to `AgentCardCORSConfig`:

```yaml
    AgentCardResponse:
      type: object
      description: An A2A agent's stored public agent card for one environment, and the state of its fetch.
      required:
        - source
        - status
        - sourceUrl
        - lastError
      properties:
        source:
          type: string
          enum: [platform, external]
          description: platform cards are fetched through the environment's gateway; external cards from a registered URL.
        status:
          type: string
          enum: [pending, fetched, failed]
          description: pending while a fetch is due or retrying; failed once the retry budget ran out.
        sourceUrl:
          type: string
          description: The registered URL (external) or the URL last fetched (platform).
        lastError:
          type: string
          description: Why the last attempt failed; empty after a success.
        card:
          type: object
          additionalProperties: true
          description: The last good card, as returned by the agent. Absent until the first successful fetch.
        fetchedAt:
          type: string
          format: date-time
          description: Time of the last successful fetch. Absent until then.
    SetAgentCardSourceRequest:
      type: object
      required:
        - url
      properties:
        url:
          type: string
          maxLength: 2048
          description: Public http(s) URL of the agent card, normally ending in /.well-known/agent-card.json.
```

Run: `<go-env> make spec` (Docker must be running).
Expected: new `spec/model_agent_card_response.go` and `spec/model_set_agent_card_source_request.go`. Open the first and confirm `func NewAgentCardResponse(source string, status string, sourceUrl string, lastError string) *AgentCardResponse`, `SetCard(v map[string]interface{})` and `SetFetchedAt(v time.Time)`; if argument order differs, follow the generated signature in Step 4.

- [ ] **Step 4: Write the controller**

`controllers/a2a_agent_card_controller.go`:

```go
package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// A2AAgentCardController serves an A2A agent's stored card per environment.
type A2AAgentCardController interface {
	GetAgentCard(w http.ResponseWriter, r *http.Request)
	RefreshAgentCard(w http.ResponseWriter, r *http.Request)
	SetAgentCardSource(w http.ResponseWriter, r *http.Request)
	DeleteAgentCardSource(w http.ResponseWriter, r *http.Request)
}

type a2aAgentCardController struct {
	cardService services.A2AAgentCardService
}

// NewA2AAgentCardController creates an A2AAgentCardController.
func NewA2AAgentCardController(cardService services.A2AAgentCardService) A2AAgentCardController {
	return &a2aAgentCardController{cardService: cardService}
}

func (c *a2aAgentCardController) GetAgentCard(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	card, err := c.cardService.GetA2AAgentCard(r.Context(), ouID, projName, agentName, envName)
	if err != nil {
		writeAgentCardError(w, r, err, "Failed to get agent card")
		return
	}
	resp, err := toAgentCardResponse(card)
	if err != nil {
		writeAgentCardError(w, r, err, "Failed to get agent card")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, resp)
}

func (c *a2aAgentCardController) RefreshAgentCard(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	if err := c.cardService.RefreshA2AAgentCard(r.Context(), ouID, projName, agentName, envName); err != nil {
		writeAgentCardError(w, r, err, "Failed to refresh agent card")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (c *a2aAgentCardController) SetAgentCardSource(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	var req spec.SetAgentCardSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := c.cardService.SetA2ACardSource(r.Context(), ouID, projName, agentName, envName, req.Url); err != nil {
		writeAgentCardError(w, r, err, "Failed to set agent card source")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (c *a2aAgentCardController) DeleteAgentCardSource(w http.ResponseWriter, r *http.Request) {
	ouID, projName, agentName, envName := agentCardPath(r)
	if err := c.cardService.DeleteA2ACardSource(r.Context(), ouID, projName, agentName, envName); err != nil {
		writeAgentCardError(w, r, err, "Failed to delete agent card source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func agentCardPath(r *http.Request) (string, string, string, string) {
	return middleware.OUIDFromRequest(r),
		r.PathValue(utils.PathParamProjName),
		r.PathValue(utils.PathParamAgentName),
		r.PathValue(utils.PathParamEnvID)
}

// writeAgentCardError maps the card sentinels, then defers to handleCommonErrors.
func writeAgentCardError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	logger.GetLogger(r.Context()).Warn("Agent card request failed", "path", r.URL.Path, "error", err)
	switch {
	case errors.Is(err, utils.ErrAgentCardNotFound):
		utils.WriteErrorResponse(w, http.StatusNotFound, "Agent card not found")
	case errors.Is(err, utils.ErrAgentNotA2A),
		errors.Is(err, utils.ErrAgentCardSourceNotExternal),
		errors.Is(err, utils.ErrInvalidURL):
		utils.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
	default:
		handleCommonErrors(w, err, fallback)
	}
}

func toAgentCardResponse(card *models.A2AAgentCard) (*spec.AgentCardResponse, error) {
	resp := spec.NewAgentCardResponse(string(card.Source), string(card.Status), card.SourceURL, card.LastError)
	if len(card.Card) > 0 {
		dec := json.NewDecoder(bytes.NewReader(card.Card))
		dec.UseNumber()
		var body map[string]interface{}
		if err := dec.Decode(&body); err != nil {
			return nil, err
		}
		resp.SetCard(body)
	}
	if card.FetchedAt != nil {
		resp.SetFetchedAt(*card.FetchedAt)
	}
	return resp, nil
}
```

- [ ] **Step 5: Register the routes and wire the controller**

`api/a2a_agent_card_routes.go`:

```go
package api

import (
	"github.com/wso2/agent-manager/agent-manager-service/controllers"
	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
)

const agentCardBase = "/orgs/{orgName}/projects/{projName}/agents/{agentName}/environments/{envID}/agent-card"

// registerA2AAgentCardRoutes gates writes like the deploy-settings save.
func registerA2AAgentCardRoutes(rr *middleware.RouteRegistrar, ctrl controllers.A2AAgentCardController) {
	rr.HandleFuncWithValidationAndAuthz("GET "+agentCardBase, rbac.AgentRead, ctrl.GetAgentCard)
	rr.HandleFuncWithValidationAndAllAuthz("POST "+agentCardBase+"/refresh", ctrl.RefreshAgentCard,
		rbac.AgentUpdate, rbac.AgentEnvNonProduction)
	rr.HandleFuncWithValidationAndAllAuthz("PUT "+agentCardBase+"/source", ctrl.SetAgentCardSource,
		rbac.AgentUpdate, rbac.AgentEnvNonProduction)
	rr.HandleFuncWithValidationAndAllAuthz("DELETE "+agentCardBase+"/source", ctrl.DeleteAgentCardSource,
		rbac.AgentUpdate, rbac.AgentEnvNonProduction)
}
```

- `api/app.go` `registerAPIRoutes`: after `RegisterAgentAPIKeyRoutes(rr, params.AgentAPIKeyController)` add `registerA2AAgentCardRoutes(rr, params.A2AAgentCardController)`.
- `api/audit_coverage_test.go` `controllerConstructors`: after `controllers.NewAgentAPIKeyController,` add `controllers.NewA2AAgentCardController,`.
- `wiring/wire.go`: `controllerProviderSet` after `controllers.NewAgentAPIKeyController,` add `controllers.NewA2AAgentCardController,`; `serviceProviderSet` after `services.NewA2ACardReconcilerService,` add `services.NewA2AAgentCardService,`.
- `wiring/params.go`: after `AgentAPIKeyController controllers.AgentAPIKeyController` add `A2AAgentCardController controllers.A2AAgentCardController`.
- Run `<go-env> make wire`.

- [ ] **Step 6: Run the route, audit and authz guards**

Run: `<go-env> make test-run RUN=. PKG=./api/` and `<go-env> make test-run RUN=. PKG=./audit/`
Expected: PASS — including `TestEveryMutatingRouteIsAudited`, `TestNoStaleAuditPolicyEntries`, `TestOrdinaryReadsAreNotAudited` (the GET is deliberately not in `sensitiveReadPaths`: a public card), `TestSecurityInvariantEveryRouteIsAuthorized`, `TestSecurityInvariantHandlersNeverReadOrgFromPath`.

- [ ] **Step 7: Document the audit actions**

`docs/audit-logging.md`:
- `## Semantic events` table: add the row ``| Agent card source | `agent:set-card-source` / `agent:remove-card-source` | Fail-open |``.
- `## Action reference` table: add
  ``| `agent:set-card-source` | config | 1 info | `agentName`, `sourceUrl` |`` and
  ``| `agent:remove-card-source` | config | 2 notice | `agentName` |``.

- [ ] **Step 8: Full verification, commit**

```bash
<go-env> make fmt
<go-env> make test-unit
<go-env> go build -tags=integration ./...
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./...
git add docs/api_v1_openapi.yaml spec/ controllers/a2a_agent_card_controller.go api/a2a_agent_card_routes.go api/app.go \
  api/audit_coverage_test.go audit/policy.go audit/actions_domain_test.go docs/audit-logging.md \
  wiring/wire.go wiring/params.go wiring/wire_gen.go
git commit -m "Add the A2A agent card API

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```
(`agent-manager-service/docs/` is the service's API/audit docs, not the untracked `docs/superpowers/` at the repo root — staging it is correct.)

Expected: unit suite green; lint clean.

---

### Task 9: Console types, fetch functions and hooks

**Files:**
- Create: `console/workspaces/libs/types/src/api/agent-card.ts`, `console/workspaces/libs/api-client/src/apis/agent-card.ts`, `console/workspaces/libs/api-client/src/hooks/agent-card.ts`
- Modify: `console/workspaces/libs/types/src/api/index.ts`, `console/workspaces/libs/api-client/src/apis/index.ts`, `console/workspaces/libs/api-client/src/hooks/index.ts`
- Test: `console/workspaces/libs/api-client/src/hooks/agent-card.test.ts`

**Interfaces:**
- Produces (exported from `@agent-management-platform/types`): `AgentCardSource`, `AgentCardStatus`, `AgentCardInterface`, `AgentCardSkill`, `AgentCard`, `AgentCardResponse`, `AgentCardPathParams`, `SetAgentCardSourceRequest`.
- Produces (from `@agent-management-platform/api-client`): `getAgentCard`, `refreshAgentCard`, `setAgentCardSource`, `deleteAgentCardSource`, `agentCardRefetchInterval(data)`, `useGetAgentCard(params)`, `useRefreshAgentCard()`, `useSetAgentCardSource()`, `useDeleteAgentCardSource()`. Query key: `["agent-card", orgName, projName, agentName, envId]`.

- [ ] **Step 1: Types**

`libs/types/src/api/agent-card.ts` (copy the license header from `agent-api-keys.ts`):

```ts
import { type AgentPathParams } from './common';

export type AgentCardSource = 'platform' | 'external';
export type AgentCardStatus = 'pending' | 'fetched' | 'failed';

export interface AgentCardInterface {
  url: string;
  protocolBinding?: string;
  protocolVersion?: string;
}

export interface AgentCardSkill {
  id?: string;
  name: string;
  description?: string;
  tags?: string[];
}

/** An A2A 1.0 public agent card; unknown fields pass through. */
export interface AgentCard {
  name: string;
  description?: string;
  version?: string;
  supportedInterfaces: AgentCardInterface[];
  skills: AgentCardSkill[];
  [key: string]: unknown;
}

export interface AgentCardResponse {
  source: AgentCardSource;
  status: AgentCardStatus;
  sourceUrl: string;
  lastError: string;
  card?: AgentCard;
  fetchedAt?: string;
}

export interface AgentCardPathParams extends AgentPathParams {
  envId: string | undefined;
}

export interface SetAgentCardSourceRequest {
  url: string;
}
```

Add `export * from './agent-card';` to `libs/types/src/api/index.ts` (alphabetical, after `agent-api-keys`).

- [ ] **Step 2: Write the failing test for the poll rule**

`libs/api-client/src/hooks/agent-card.test.ts` (license header first):

```ts
import { describe, expect, it } from "vitest";
import { agentCardRefetchInterval } from "./agent-card";
import { POLL_INTERVAL } from "../utils";

describe("agentCardRefetchInterval", () => {
  it("polls while the card is pending", () => {
    expect(agentCardRefetchInterval({ status: "pending", source: "platform", sourceUrl: "", lastError: "" }))
      .toBe(POLL_INTERVAL);
  });

  it("stops once fetched or failed, or with no data", () => {
    expect(agentCardRefetchInterval({ status: "fetched", source: "platform", sourceUrl: "", lastError: "" })).toBe(false);
    expect(agentCardRefetchInterval({ status: "failed", source: "platform", sourceUrl: "", lastError: "x" })).toBe(false);
    expect(agentCardRefetchInterval(undefined)).toBe(false);
  });
});
```

Run: `pnpm --filter @agent-management-platform/api-client run test`
Expected: FAIL — cannot resolve `./agent-card`.

- [ ] **Step 3: Fetch functions**

`libs/api-client/src/apis/agent-card.ts`:

```ts
import type {
  AgentCardPathParams,
  AgentCardResponse,
  SetAgentCardSourceRequest,
} from "@agent-management-platform/types";
import { encodeRequired, httpDELETE, httpGET, httpPOST, httpPUT, SERVICE_BASE } from "../utils";

function agentCardPath(params: AgentCardPathParams): string {
  const org = encodeRequired(params.orgName, "orgName");
  const proj = encodeRequired(params.projName, "projName");
  const agent = encodeRequired(params.agentName, "agentName");
  const env = encodeRequired(params.envId, "envId");
  return `${SERVICE_BASE}/orgs/${org}/projects/${proj}/agents/${agent}/environments/${env}/agent-card`;
}

export async function getAgentCard(
  params: AgentCardPathParams,
  getToken?: () => Promise<string>,
): Promise<AgentCardResponse> {
  const token = getToken ? await getToken() : undefined;
  const res = await httpGET(agentCardPath(params), { token });
  if (!res.ok) throw await res.json();
  return res.json();
}

// 202/204 carry no body, so these resolve to void.
export async function refreshAgentCard(
  params: AgentCardPathParams,
  getToken?: () => Promise<string>,
): Promise<void> {
  const token = getToken ? await getToken() : undefined;
  await httpPOST(`${agentCardPath(params)}/refresh`, {}, { token });
}

export async function setAgentCardSource(
  params: AgentCardPathParams,
  body: SetAgentCardSourceRequest,
  getToken?: () => Promise<string>,
): Promise<void> {
  const token = getToken ? await getToken() : undefined;
  await httpPUT(`${agentCardPath(params)}/source`, body, { token });
}

export async function deleteAgentCardSource(
  params: AgentCardPathParams,
  getToken?: () => Promise<string>,
): Promise<void> {
  const token = getToken ? await getToken() : undefined;
  await httpDELETE(`${agentCardPath(params)}/source`, { token });
}
```

(The write helpers throw on non-OK themselves, so no `res.ok` check is needed for them — same as relying on `httpGET`'s own throw.)

- [ ] **Step 4: Hooks**

`libs/api-client/src/hooks/agent-card.ts`:

```ts
import { useQueryClient } from "@tanstack/react-query";
import { useAuthHooks } from "@agent-management-platform/auth";
import type {
  AgentCardPathParams,
  AgentCardResponse,
  SetAgentCardSourceRequest,
} from "@agent-management-platform/types";
import { useApiMutation, useApiQuery } from "./react-query-notifications";
import {
  deleteAgentCardSource,
  getAgentCard,
  refreshAgentCard,
  setAgentCardSource,
} from "../apis/agent-card";
import { POLL_INTERVAL } from "../utils";

const agentCardKey = (p: AgentCardPathParams) =>
  ["agent-card", p.orgName, p.projName, p.agentName, p.envId] as const;

/** Re-poll while a fetch is pending; stop on fetched or failed. */
export function agentCardRefetchInterval(data: AgentCardResponse | undefined): number | false {
  return data?.status === "pending" ? POLL_INTERVAL : false;
}

export function useGetAgentCard(params: AgentCardPathParams, options: { enabled?: boolean } = {}) {
  const { getToken } = useAuthHooks();
  return useApiQuery<AgentCardResponse>({
    queryKey: agentCardKey(params),
    queryFn: () => getAgentCard(params, getToken),
    enabled: (options.enabled ?? true) &&
      !!(params.orgName && params.projName && params.agentName && params.envId),
    refetchInterval: (q) => agentCardRefetchInterval(q.state.data),
    retry: false,
  });
}

export function useRefreshAgentCard() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, AgentCardPathParams>({
    action: { verb: "update", target: "agent card" },
    successMessage: "Agent card refresh queued",
    mutationFn: (params) => refreshAgentCard(params, getToken),
    onSuccess: (_d, params) => queryClient.invalidateQueries({ queryKey: agentCardKey(params) }),
  });
}

export function useSetAgentCardSource() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, { params: AgentCardPathParams; body: SetAgentCardSourceRequest }>({
    action: { verb: "update", target: "agent card source" },
    mutationFn: ({ params, body }) => setAgentCardSource(params, body, getToken),
    onSuccess: (_d, { params }) => queryClient.invalidateQueries({ queryKey: agentCardKey(params) }),
  });
}

export function useDeleteAgentCardSource() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, AgentCardPathParams>({
    action: { verb: "remove", target: "agent card source" },
    mutationFn: (params) => deleteAgentCardSource(params, getToken),
    onSuccess: (_d, params) => queryClient.invalidateQueries({ queryKey: agentCardKey(params) }),
  });
}
```

Add `export * from './agent-card';` to `apis/index.ts` and `hooks/index.ts` (alphabetical, after `agent-api-keys`).

- [ ] **Step 5: Run the test, lint and build**

```bash
pnpm --filter @agent-management-platform/api-client run test
pnpm --filter @agent-management-platform/types run lint
pnpm --filter @agent-management-platform/api-client run lint
pnpm --filter @agent-management-platform/api-client... run build
```
Expected: test PASS; lint and build clean.

- [ ] **Step 6: Commit**

```bash
git add console/workspaces/libs/types/src/api/agent-card.ts console/workspaces/libs/types/src/api/index.ts \
  console/workspaces/libs/api-client/src/apis/agent-card.ts console/workspaces/libs/api-client/src/apis/index.ts \
  console/workspaces/libs/api-client/src/hooks/agent-card.ts console/workspaces/libs/api-client/src/hooks/index.ts \
  console/workspaces/libs/api-client/src/hooks/agent-card.test.ts
git commit -m "Add console API hooks for A2A agent cards

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 10: Agent Card panel on the agent overview

**Files:**
- Create: `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.tsx`, `console/workspaces/pages/overview/src/AgentOverview/AgentCardSourceForm.tsx`
- Modify: `console/workspaces/pages/overview/src/AgentOverview/EnvironmentSectionsContent.tsx`
- Test: `console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.test.tsx`

**Interfaces:**
- Consumes: Task 9 hooks/types; `useGetAgent` (`@agent-management-platform/api-client`); `OverviewSectionCard`, `CodeBlock` (`@agent-management-platform/shared-component`); `TextInput` (`@agent-management-platform/views`).
- Produces: `EnvAgentCardSection({ orgId, projectId, agentId, envId, external? })`, `AgentCardSourceForm({ params, currentUrl })`.

- [ ] **Step 1: Write the failing component test**

`EnvAgentCardSection.test.tsx` (license header first):

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@agent-management-platform/api-client", () => ({
  useGetAgent: vi.fn(),
  useGetAgentCard: vi.fn(),
  useRefreshAgentCard: vi.fn(),
  useSetAgentCardSource: vi.fn(),
  useDeleteAgentCardSource: vi.fn(),
}));

import {
  useDeleteAgentCardSource,
  useGetAgent,
  useGetAgentCard,
  useRefreshAgentCard,
  useSetAgentCardSource,
} from "@agent-management-platform/api-client";
import { EnvAgentCardSection } from "./EnvAgentCardSection";

const refresh = vi.fn();
const setSource = vi.fn();

const mockAgent = (subType: string) =>
  vi.mocked(useGetAgent).mockReturnValue({
    data: { agentType: { type: "agent-api", subType } },
  } as unknown as ReturnType<typeof useGetAgent>);

const mockCard = (value: Partial<ReturnType<typeof useGetAgentCard>>) =>
  vi.mocked(useGetAgentCard).mockReturnValue({
    data: undefined, isLoading: false, isError: false, error: null, ...value,
  } as unknown as ReturnType<typeof useGetAgentCard>);

const renderSection = (external = false) =>
  render(
    <MemoryRouter>
      <EnvAgentCardSection orgId="org" projectId="proj" agentId="agent" envId="dev" external={external} />
    </MemoryRouter>,
  );

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useRefreshAgentCard).mockReturnValue(
    { mutate: refresh, isPending: false } as unknown as ReturnType<typeof useRefreshAgentCard>);
  vi.mocked(useSetAgentCardSource).mockReturnValue(
    { mutate: setSource, isPending: false } as unknown as ReturnType<typeof useSetAgentCardSource>);
  vi.mocked(useDeleteAgentCardSource).mockReturnValue(
    { mutate: vi.fn(), isPending: false } as unknown as ReturnType<typeof useDeleteAgentCardSource>);
});

describe("EnvAgentCardSection", () => {
  it("renders nothing for a non-A2A agent", () => {
    mockAgent("chat-api");
    mockCard({});
    const { container } = renderSection();
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the fetched card's summary and skills", () => {
    mockAgent("a2a-agent");
    mockCard({
      data: {
        source: "platform", status: "fetched", sourceUrl: "https://gw/a/.well-known/agent-card.json", lastError: "",
        fetchedAt: "2026-10-07T10:00:00Z",
        card: {
          name: "Trip Planner", description: "Plans trips", version: "1.2.0",
          supportedInterfaces: [{ url: "https://gw/a/rpc", protocolBinding: "JSONRPC" }],
          skills: [{ name: "plan", description: "Plan a trip", tags: ["travel"] }],
        },
      },
    });
    renderSection();
    expect(screen.getByText("Trip Planner")).toBeInTheDocument();
    expect(screen.getByText("Fetched")).toBeInTheDocument();
    expect(screen.getByText("plan")).toBeInTheDocument();
    expect(screen.getByText("https://gw/a/rpc")).toBeInTheDocument();
  });

  it("shows the last error when the fetch failed", () => {
    mockAgent("a2a-agent");
    mockCard({ data: { source: "platform", status: "failed", sourceUrl: "", lastError: "HTTP 404" } });
    renderSection();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.getByText(/HTTP 404/)).toBeInTheDocument();
  });

  it("refreshes on demand", () => {
    mockAgent("a2a-agent");
    mockCard({ data: { source: "platform", status: "fetched", sourceUrl: "", lastError: "" } });
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: /refresh/i }));
    expect(refresh).toHaveBeenCalledWith({ orgName: "org", projName: "proj", agentName: "agent", envId: "dev" });
  });

  it("asks an external agent with no source for a card URL", () => {
    mockAgent("a2a-agent");
    mockCard({ isError: true, error: Object.assign(new Error("nf"), { status: 404 }) });
    renderSection(true);
    fireEvent.change(screen.getByPlaceholderText(/agent-card\.json/), {
      target: { value: "https://agent.example/.well-known/agent-card.json" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(setSource).toHaveBeenCalledWith(
      {
        params: { orgName: "org", projName: "proj", agentName: "agent", envId: "dev" },
        body: { url: "https://agent.example/.well-known/agent-card.json" },
      },
    );
  });
});
```

Run: `cd console/workspaces/pages/overview && pnpm exec vitest run src/AgentOverview/EnvAgentCardSection.test.tsx`
Expected: FAIL — cannot resolve `./EnvAgentCardSection`.

- [ ] **Step 2: Write the source form**

`AgentCardSourceForm.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Box, Button } from "@wso2/oxygen-ui";
import { useDeleteAgentCardSource, useSetAgentCardSource } from "@agent-management-platform/api-client";
import type { AgentCardPathParams } from "@agent-management-platform/types";
import { TextInput } from "@agent-management-platform/views";

interface AgentCardSourceFormProps {
  params: AgentCardPathParams;
  currentUrl?: string;
}

const URL_MAX = 2048;

/** Card URL for an external A2A agent in one environment. */
export function AgentCardSourceForm({ params, currentUrl }: AgentCardSourceFormProps) {
  const [url, setUrl] = useState(currentUrl ?? "");
  useEffect(() => setUrl(currentUrl ?? ""), [currentUrl]);
  const { mutate: save, isPending: isSaving } = useSetAgentCardSource();
  const { mutate: remove, isPending: isRemoving } = useDeleteAgentCardSource();

  const trimmed = url.trim();
  const invalid = trimmed !== "" && (!/^https?:\/\//i.test(trimmed) || trimmed.length > URL_MAX);

  return (
    <Box display="flex" gap={1} alignItems="flex-start" sx={{ mb: 1 }}>
      <TextInput
        label="Agent card URL"
        placeholder="https://agent.example.com/.well-known/agent-card.json"
        value={url}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => setUrl(e.target.value)}
        error={invalid}
        helperText={invalid ? "Enter a public http(s) URL of at most 2048 characters" : undefined}
        fullWidth
      />
      <Button
        variant="contained"
        size="small"
        disabled={!trimmed || invalid || trimmed === currentUrl || isSaving}
        onClick={() => save({ params, body: { url: trimmed } })}
        sx={{ mt: 3 }}
      >
        Save
      </Button>
      {currentUrl && (
        <Button
          variant="text"
          size="small"
          color="error"
          disabled={isRemoving}
          onClick={() => remove(params)}
          sx={{ mt: 3 }}
        >
          Remove
        </Button>
      )}
    </Box>
  );
}
```

(If `TextInput`'s `onChange`/`error`/`helperText` props differ from MUI `TextField`'s, check `libs/views/src/component/FormElements/TextInput.tsx` and match its prop names.)

- [ ] **Step 3: Write the panel**

`EnvAgentCardSection.tsx`:

```tsx
import { useState } from "react";
import { Alert, Box, Button, Chip, CircularProgress, Typography } from "@wso2/oxygen-ui";
import { RefreshCw } from "@wso2/oxygen-ui-icons-react";
import { useGetAgent, useGetAgentCard, useRefreshAgentCard } from "@agent-management-platform/api-client";
import { CodeBlock, OverviewSectionCard } from "@agent-management-platform/shared-component";
import type { AgentCardStatus } from "@agent-management-platform/types";
import { AgentCardSourceForm } from "./AgentCardSourceForm";

interface EnvAgentCardSectionProps {
  orgId: string;
  projectId: string;
  agentId: string;
  envId: string;
  external?: boolean;
}

const STATUS_CHIP: Record<AgentCardStatus, { label: string; color: "warning" | "success" | "error" }> = {
  pending: { label: "Pending", color: "warning" },
  fetched: { label: "Fetched", color: "success" },
  failed: { label: "Failed", color: "error" },
};

const isNotFound = (error: unknown) => (error as { status?: number } | null)?.status === 404;

/** Stored A2A agent card for the selected environment. */
export function EnvAgentCardSection({ orgId, projectId, agentId, envId, external }: EnvAgentCardSectionProps) {
  const params = { orgName: orgId, projName: projectId, agentName: agentId, envId };
  // Real orgId, unlike EnvCapabilitiesSection, so external agents' subtype is visible.
  const { data: agent } = useGetAgent({ orgName: orgId, projName: projectId, agentName: agentId });
  const isA2A = agent?.agentType?.subType === "a2a-agent";
  const { data: card, isLoading, isError, error } = useGetAgentCard(params, { enabled: isA2A });
  const { mutate: refresh, isPending: isRefreshing } = useRefreshAgentCard();
  const [showJson, setShowJson] = useState(false);

  if (!isA2A) {
    return null;
  }
  const noSource = isError && isNotFound(error);

  return (
    <OverviewSectionCard
      title="Agent Card"
      headerAction={
        !noSource && (
          <Button
            size="small"
            variant="text"
            startIcon={isRefreshing ? <CircularProgress size={14} /> : <RefreshCw size={14} />}
            disabled={isRefreshing}
            onClick={() => refresh(params)}
          >
            Refresh
          </Button>
        )
      }
      sx={{ mb: 1.5 }}
    >
      {external && <AgentCardSourceForm params={params} currentUrl={card?.sourceUrl} />}
      {isLoading && <CircularProgress size={16} />}
      {noSource && (
        <Typography variant="body2" color="text.secondary">
          Register this agent's card URL for the environment to fetch its card.
        </Typography>
      )}
      {isError && !noSource && (
        <Typography variant="body2" color="error">Unable to load the agent card. Try again later.</Typography>
      )}
      {card && (
        <>
          <Box display="flex" alignItems="center" gap={1} sx={{ mb: 1 }}>
            <Chip variant="outlined" size="small" label={STATUS_CHIP[card.status].label} color={STATUS_CHIP[card.status].color} />
            {card.fetchedAt && (
              <Typography variant="caption" color="text.secondary">
                Fetched {new Date(card.fetchedAt).toLocaleString()}
              </Typography>
            )}
          </Box>
          {card.status === "failed" && card.lastError && (
            <Alert severity="error" sx={{ mb: 1 }}>Fetch failed: {card.lastError}</Alert>
          )}
          {card.card && (
            <>
              <Typography variant="subtitle2">{card.card.name}</Typography>
              {card.card.version && <Typography variant="caption">Version {card.card.version}</Typography>}
              {card.card.description && <Typography variant="body2" sx={{ mb: 1 }}>{card.card.description}</Typography>}
              <Typography variant="overline">Interfaces</Typography>
              {card.card.supportedInterfaces.map((iface) => (
                <Box key={`${iface.protocolBinding ?? ""}-${iface.url}`} display="flex" gap={1} alignItems="center">
                  {iface.protocolBinding && <Chip size="small" label={iface.protocolBinding} />}
                  <Typography variant="body2">{iface.url}</Typography>
                </Box>
              ))}
              <Typography variant="overline">Skills</Typography>
              {card.card.skills.map((skill) => (
                <Box key={skill.id ?? skill.name} sx={{ mb: 0.5 }}>
                  <Typography variant="body2" fontWeight={600}>{skill.name}</Typography>
                  {skill.description && <Typography variant="caption">{skill.description}</Typography>}
                  <Box display="flex" gap={0.5} flexWrap="wrap">
                    {(skill.tags ?? []).map((tag) => <Chip key={tag} size="small" variant="outlined" label={tag} />)}
                  </Box>
                </Box>
              ))}
              <Button size="small" variant="text" onClick={() => setShowJson((v) => !v)}>
                {showJson ? "Hide JSON" : "View JSON"}
              </Button>
              {showJson && (
                <CodeBlock code={JSON.stringify(card.card, null, 2)} language="json" analyticsId="agent-card-json" />
              )}
            </>
          )}
        </>
      )}
    </OverviewSectionCard>
  );
}
```

`EnvironmentSectionsContent.tsx`: import `EnvAgentCardSection` and render it right after `<EnvCapabilitiesSection … />`:
```tsx
            <EnvAgentCardSection orgId={orgId} projectId={projectId} agentId={agentId} envId={envId} external={external} />
```

- [ ] **Step 4: Run the test to verify it passes, then lint and build**

```bash
cd console/workspaces/pages/overview && pnpm exec vitest run src/AgentOverview/EnvAgentCardSection.test.tsx
cd console && pnpm --filter @agent-management-platform/overview run test
pnpm --filter @agent-management-platform/overview run lint
pnpm --filter @agent-management-platform/overview... run build
```
Expected: PASS (whole package, including `EnvCapabilitiesSection.a2a.test.tsx`); lint and build clean.

- [ ] **Step 5: Commit**

```bash
git add console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvAgentCardSection.test.tsx \
  console/workspaces/pages/overview/src/AgentOverview/AgentCardSourceForm.tsx \
  console/workspaces/pages/overview/src/AgentOverview/EnvironmentSectionsContent.tsx
git commit -m "Show A2A agent cards per environment on the agent overview

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 11: Offer the A2A subtype when connecting an external agent

**Files:**
- Modify: `console/workspaces/pages/add-new-agent/src/form/schema.ts` (`connectAgentSchema`), `console/workspaces/pages/add-new-agent/src/forms/ExternalAgentForm.tsx`, `console/workspaces/pages/add-new-agent/src/utils/buildAgentPayload.ts` (external return ~line 280)
- Test: `console/workspaces/pages/add-new-agent/src/utils/buildAgentPayload.external-a2a.test.ts`

**Interfaces:**
- Produces: `ConnectAgentFormValues.externalSubType?: "custom-api" | "a2a-agent"`; the external payload sends `agentType.subType = externalSubType ?? "custom-api"`.

- [ ] **Step 1: Write the failing payload test**

`buildAgentPayload.external-a2a.test.ts` (license header first; copy the `params` constant from `buildAgentPayload.a2a.test.ts`):

```ts
import { describe, expect, it } from "vitest";
import { buildAgentCreationPayload } from "./buildAgentPayload";
import type { AddAgentFormValues } from "../form/schema";

const params = { orgName: "org", projName: "proj" };

const externalForm = (over: Record<string, unknown> = {}) =>
  ({ deploymentType: "existing", name: "ext", displayName: "Ext", description: "", ...over } as unknown as AddAgentFormValues);

describe("buildAgentCreationPayload for an external agent", () => {
  it("keeps custom-api as the default subtype", () => {
    const { body } = buildAgentCreationPayload(externalForm(), params);
    expect(body.agentType).toEqual({ type: "external-agent-api", subType: "custom-api" });
  });

  it("sends a2a-agent when chosen", () => {
    const { body } = buildAgentCreationPayload(externalForm({ externalSubType: "a2a-agent" }), params);
    expect(body.agentType).toEqual({ type: "external-agent-api", subType: "a2a-agent" });
    expect(body.provisioning).toEqual({ type: "external" });
  });
});
```

Run: `cd console/workspaces/pages/add-new-agent && pnpm exec vitest run src/utils/buildAgentPayload.external-a2a.test.ts`
Expected: FAIL on the second case (`custom-api` received).

- [ ] **Step 2: Implement**

`form/schema.ts`:
```ts
export const connectAgentSchema = z.object({
  ...baseAgentFields,
  deploymentType: z.literal('existing').optional(),
  externalSubType: z.enum(['custom-api', 'a2a-agent']).optional(),
});
```

`utils/buildAgentPayload.ts`, external return: replace `subType: "custom-api",` with
```ts
        subType: ("externalSubType" in data && data.externalSubType) || "custom-api",
```

`forms/ExternalAgentForm.tsx`: add `Box`, `Divider` to the `@wso2/oxygen-ui` import and `import { CheckCircle, Circle } from "@wso2/oxygen-ui-icons-react";`, then add a second section after the "Agent Details" `</Form.Section>`:
```tsx
      <Form.Section>
        <Form.Subheader>Agent Type</Form.Subheader>
        <Form.Stack spacing={2}>
          <Box display="flex" flexDirection="row" gap={1}>
            {([
              { value: "custom-api", label: "Agent", description: "Any externally hosted agent. Observability only." },
              { value: "a2a-agent", label: "A2A Agent", description: "Speaks A2A 1.0. Register its agent card URL per environment after creating it." },
            ] as const).map((option) => {
              const selected = (formData.externalSubType ?? "custom-api") === option.value;
              return (
                <Form.CardButton
                  key={option.value}
                  onClick={() => handleFieldChange("externalSubType", option.value)}
                  selected={selected}
                  sx={{ maxWidth: 500, flexGrow: 1 }}
                >
                  <Form.CardContent sx={{ height: "100%" }}>
                    <Box display="flex" flexDirection="row" alignItems="center" height="100%" gap={1}>
                      <Box>{selected ? <CheckCircle size={16} /> : <Circle size={16} />}</Box>
                      <Divider orientation="vertical" flexItem />
                      <Box>
                        <Typography variant="h6">{option.label}</Typography>
                        <Typography variant="caption" color="text.secondary">{option.description}</Typography>
                      </Box>
                    </Box>
                  </Form.CardContent>
                </Form.CardButton>
              );
            })}
          </Box>
        </Form.Stack>
      </Form.Section>
```
(Mirror the exact inner markup of `components/InputInterface.tsx`'s card button — read lines 165-215 there and match its `Typography` variants.)

- [ ] **Step 3: Run tests, lint, build**

```bash
cd console/workspaces/pages/add-new-agent && pnpm exec vitest run src/utils/
cd console && pnpm --filter @agent-management-platform/add-new-agent run lint
pnpm --filter @agent-management-platform/add-new-agent... run build
```
Expected: PASS (new and existing payload tests); lint and build clean.

- [ ] **Step 4: Commit**

```bash
git add console/workspaces/pages/add-new-agent/src/form/schema.ts \
  console/workspaces/pages/add-new-agent/src/forms/ExternalAgentForm.tsx \
  console/workspaces/pages/add-new-agent/src/utils/buildAgentPayload.ts \
  console/workspaces/pages/add-new-agent/src/utils/buildAgentPayload.external-a2a.test.ts
git commit -m "Let an external agent be connected as an A2A agent

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```

---

### Task 12: Manual verification and the stale README note

**Files:**
- Modify: `samples/a2a-agent/README.md` (the "card the gateway serves is the agent's own body" bullet, ~line 328)

- [ ] **Step 1: Fix the stale note**

Replace the bullet starting `- **The card the gateway serves is the agent's own body.**` (through `rewrite the URL yourself.`) with:
```markdown
- **The card the gateway serves advertises the gateway.** The gateway's passthrough
  rewrite points every `supportedInterfaces[].url` at itself, so resolve the card
  through the gateway and use the URLs it returns.
```

- [ ] **Step 2: Run the full backend and console checks**

```bash
cd agent-manager-service && <go-env> make test-unit && <go-env> go build -tags=integration ./...
<go-env> golangci-lint run --config .github/linters/.golangci.yaml ./...
cd ../console && pnpm lint && pnpm build
```
Expected: all green.

- [ ] **Step 3: Platform agent, local make-setup env**

Bring the AMP layer up with the branch's AMS (`make reset-amp` if it's stale; see the repo's local-env notes). Deploy `samples/a2a-agent` per its README. Then:
- `GET …/environments/<env>/agent-card` shows `status: pending`, then `fetched` within ~30s of the publication reaching `published`, with `sourceUrl` ending in `/<agent>/.well-known/agent-card.json` and the card's interface URLs pointing at the gateway.
- `POST …/agent-card/refresh` → 202; status goes `pending` → `fetched`; `card` stays present throughout.
- The console overview shows the Agent Card panel with status, summary, skills and JSON.

- [ ] **Step 4: External agent**

Connect an external agent as "A2A Agent" in the console. In its overview, register a public A2A card URL for one environment (any public A2A 1.0 card, or a tunnel to a locally run `samples/a2a-agent`). Confirm `fetched` and the summary. Register `http://localhost/...` and confirm a 400 naming the SSRF rejection. Remove the source and confirm the panel returns to the URL prompt.

- [ ] **Step 5: Delete**

Delete both agents and confirm `SELECT count(*) FROM a2a_agent_cards WHERE agent_name IN ('<platform>', '<external>')` is 0.

- [ ] **Step 6: Commit**

```bash
git add samples/a2a-agent/README.md
git commit -m "Correct the A2A sample's note on gateway card rewriting

Claude-Session: https://claude.ai/code/session_01AQZvWVZD8QvWXx2FCc3SZ5"
```
