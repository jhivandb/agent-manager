//go:build integration

// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// intentFixture is one org with an agent artifact and an ingress gateway.
type intentFixture struct {
	repo        DeployIntentRepository
	deployments DeploymentRepository
	ouID        string
	artifact    uuid.UUID
	gateway     uuid.UUID
	environment uuid.UUID
}

func seedIntentFixture(t *testing.T) intentFixture {
	t.Helper()
	gdb := db.GetDB()
	f := intentFixture{
		repo: NewDeployIntentRepository(gdb), deployments: NewDeploymentRepo(gdb),
		ouID: "ou-" + uuid.NewString()[:8], artifact: uuid.New(), gateway: uuid.New(), environment: uuid.New(),
	}
	name := "intent-gw-" + f.gateway.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		f.gateway, name, name, "gw.example", models.GatewayRoleIngress, f.ouID).Error)
	require.NoError(t, NewArtifactRepo(gdb).Create(gdb, &models.Artifact{
		UUID: f.artifact, Handle: "intent-" + f.artifact.String(), Name: "agent-" + f.artifact.String()[:8],
		Version: "v1.0", Kind: models.KindAgent, OUID: f.ouID,
	}))
	t.Cleanup(func() {
		gdb.Exec(`DELETE FROM artifacts WHERE uuid = ?`, f.artifact)
		gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, f.gateway)
	})
	return f
}

func (f intentFixture) upsert(t *testing.T, data string) models.DeployIntent {
	t.Helper()
	intent := models.DeployIntent{
		OUID: f.ouID, Kind: models.KindAgent, ArtifactUUID: f.artifact, EnvironmentUUID: &f.environment,
		Desired: models.DeploymentStatusDeployed, KindData: json.RawMessage(data),
	}
	require.NoError(t, f.repo.Upsert(context.Background(), &intent))
	return intent
}

// deploy writes a deployment of the artifact to the gateway, in status.
func (f intentFixture) deploy(t *testing.T, status models.DeploymentStatus) uuid.UUID {
	t.Helper()
	d := &models.Deployment{
		Name: "dep", ArtifactUUID: f.artifact, OUID: f.ouID, GatewayUUID: f.gateway, Content: []byte("kind: Agent\n"),
		Status: &status,
	}
	require.NoError(t, f.deployments.CreateWithLimitEnforcement(d, 10))
	return d.DeploymentID
}

func TestDeployIntentUpsertResetsTheIntentOfTheSameScope(t *testing.T) {
	ctx := context.Background()
	f := seedIntentFixture(t)
	first := f.upsert(t, `{"agentName":"a"}`)
	assert.Equal(t, models.DeployIntentPending, first.State)
	assert.NotEqual(t, uuid.Nil, first.ID)
	require.NotNil(t, first.NextAttemptAt)

	require.NoError(t, f.repo.MarkFailed(ctx, first, "gave up"))
	second := f.upsert(t, `{"agentName":"b"}`)

	assert.Equal(t, first.ID, second.ID, "one intent per artifact and environment")
	assert.Equal(t, models.DeployIntentPending, second.State)
	assert.Zero(t, second.AttemptCount)
	assert.Empty(t, second.LastError)
	assert.JSONEq(t, `{"agentName":"b"}`, string(second.KindData))

	stored, err := f.repo.Get(ctx, first.ID)
	require.NoError(t, err)
	assert.True(t, second.UpdatedAt.Equal(stored.UpdatedAt), "the returned row is the stored row")

	gatewayScoped := models.DeployIntent{
		OUID: f.ouID, Kind: models.KindAgent, ArtifactUUID: f.artifact, GatewayUUID: &f.gateway,
		Desired: models.DeploymentStatusUndeployed,
	}
	require.NoError(t, f.repo.Upsert(ctx, &gatewayScoped))
	assert.NotEqual(t, first.ID, gatewayScoped.ID, "a gateway scope is a different intent")
	byGateway, err := f.repo.GetByScope(ctx, f.ouID, f.artifact, models.GatewayScope(f.gateway))
	require.NoError(t, err)
	assert.Equal(t, gatewayScoped.ID, byGateway.ID)
	assert.JSONEq(t, `{}`, string(byGateway.KindData))

	_, err = f.repo.GetByScope(ctx, f.ouID, f.artifact, models.EnvironmentScope(uuid.New()))
	assert.ErrorIs(t, err, ErrDeployIntentNotFound)
}

func TestDeployIntentOutcomesAreGuardedByTheReadVersion(t *testing.T) {
	ctx := context.Background()
	f := seedIntentFixture(t)
	read := f.upsert(t, `{}`)

	next := time.Now().Add(time.Minute)
	require.NoError(t, f.repo.MarkWaiting(ctx, read, "waiting for the agent's endpoint", next))
	waiting, err := f.repo.Get(ctx, read.ID)
	require.NoError(t, err)
	assert.Equal(t, "waiting for the agent's endpoint", waiting.LastError)
	assert.Zero(t, waiting.AttemptCount, "waiting is not charged")

	assert.ErrorIs(t, f.repo.MarkAttemptFailed(ctx, read, "x", next), ErrDeployIntentSuperseded,
		"the row moved on since read")

	require.NoError(t, f.repo.MarkAttemptFailed(ctx, *waiting, "unreachable", next))
	failedOnce, err := f.repo.Get(ctx, read.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, failedOnce.AttemptCount)
	assert.Equal(t, models.DeployIntentPending, failedOnce.State)

	done, err := f.repo.MarkDone(ctx, *failedOnce)
	require.NoError(t, err)
	stored, err := f.repo.Get(ctx, read.ID)
	require.NoError(t, err)
	assert.Equal(t, models.DeployIntentDone, stored.State)
	assert.Zero(t, stored.AttemptCount)
	assert.Empty(t, stored.LastError)
	assert.Nil(t, stored.NextAttemptAt)
	assert.True(t, done.UpdatedAt.Equal(stored.UpdatedAt))

	require.NoError(t, f.repo.MarkPublished(ctx, done))
	published, err := f.repo.Get(ctx, read.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, published.PublishCount)
	assert.NotNil(t, published.PublishedAt)
	assert.True(t, done.UpdatedAt.Equal(published.UpdatedAt), "publishing does not change what the intent asks for")

	require.NoError(t, f.repo.Requeue(ctx, *published))
	requeued, err := f.repo.Get(ctx, read.ID)
	require.NoError(t, err)
	assert.Equal(t, models.DeployIntentPending, requeued.State)
	assert.ErrorIs(t, f.repo.MarkPublished(ctx, done), ErrDeployIntentSuperseded)
}

func TestDeployIntentClaimDueLeasesPendingIntents(t *testing.T) {
	ctx := context.Background()
	f := seedIntentFixture(t)
	intent := f.upsert(t, `{}`)
	now := time.Now().Add(time.Second)

	claimed, err := f.repo.ClaimDue(ctx, now, 1000)
	require.NoError(t, err)
	got := findIntent(claimed, intent.ID)
	require.NotNil(t, got, "a new intent is due at once")
	assert.True(t, got.UpdatedAt.Equal(intent.UpdatedAt), "the claim leaves updated_at alone")

	again, err := f.repo.ClaimDue(ctx, now, 1000)
	require.NoError(t, err)
	assert.Nil(t, findIntent(again, intent.ID), "leased")
	later, err := f.repo.ClaimDue(ctx, now.Add(DeployIntentClaimLease+time.Second), 1000)
	require.NoError(t, err)
	assert.NotNil(t, findIntent(later, intent.ID), "claimable again once the lease runs out")
}

func findIntent(intents []models.DeployIntent, id uuid.UUID) *models.DeployIntent {
	for i := range intents {
		if intents[i].ID == id {
			return &intents[i]
		}
	}
	return nil
}

func TestDeployIntentClaimUnackedRepublishesAtMostTwiceAfterTheDelay(t *testing.T) {
	ctx := context.Background()
	f := seedIntentFixture(t)
	read := f.upsert(t, `{}`)
	f.deploy(t, models.DeploymentStatusDeploying)
	done, err := f.repo.MarkDone(ctx, read)
	require.NoError(t, err)

	claim := func(now time.Time) bool {
		claimed, err := f.repo.ClaimUnacked(ctx, now, now.Add(-30*time.Second), 1000)
		require.NoError(t, err)
		return findIntent(claimed, read.ID) != nil
	}
	now := time.Now()
	assert.False(t, claim(now), "too soon after it was handed over")
	assert.True(t, claim(now.Add(time.Minute)), "its event never went out")
	assert.False(t, claim(now.Add(time.Minute)), "sent just now")
	assert.True(t, claim(now.Add(2*time.Minute)), "sent once, still no ack")
	assert.False(t, claim(now.Add(10*time.Minute)), "sent twice: the sweeper takes it from here")

	require.NoError(t, f.repo.Requeue(ctx, done))
	requeued, err := f.repo.Get(ctx, read.ID)
	require.NoError(t, err)
	_, err = f.repo.MarkDone(ctx, *requeued)
	require.NoError(t, err)
	require.NoError(t, db.GetDB().Exec(`UPDATE deployment_status SET status = 'DEPLOYED' WHERE artifact_uuid = ?`, f.artifact).Error)
	assert.False(t, claim(time.Now().Add(time.Hour)), "acked")
}

func TestDeployIntentClaimLiveClaimsEachAckedDeploymentOnce(t *testing.T) {
	ctx := context.Background()
	f := seedIntentFixture(t)
	read := f.upsert(t, `{}`)
	first := f.deploy(t, models.DeploymentStatusDeploying)
	_, err := f.repo.MarkDone(ctx, read)
	require.NoError(t, err)

	claim := func() *models.DeployIntent {
		claimed, err := f.repo.ClaimLive(ctx, []string{models.KindAgent}, 1000)
		require.NoError(t, err)
		return findIntent(claimed, read.ID)
	}
	assert.Nil(t, claim(), "not acked yet")
	require.NoError(t, db.GetDB().Exec(`UPDATE deployment_status SET status = 'DEPLOYED' WHERE artifact_uuid = ?`, f.artifact).Error)
	live := claim()
	require.NotNil(t, live)
	assert.Equal(t, first, *live.LiveDeploymentID)
	assert.Nil(t, claim(), "claimed once")

	second := f.deploy(t, models.DeploymentStatusDeployed)
	live = claim()
	require.NotNil(t, live, "a new deployment is claimed again")
	assert.Equal(t, second, *live.LiveDeploymentID)

	claimed, err := f.repo.ClaimLive(ctx, []string{models.KindLLMProvider}, 1000)
	require.NoError(t, err)
	assert.Nil(t, findIntent(claimed, read.ID), "only the kinds asked for")
}

func TestDeployIntentFiltersByKindDataAndEnvironment(t *testing.T) {
	ctx := context.Background()
	f := seedIntentFixture(t)
	f.upsert(t, `{"projectName":"p","agentName":"a","environmentName":"dev"}`)

	byName := DeployIntentFilter{Kind: models.KindAgent, OUID: f.ouID, KindData: map[string]any{"agentName": "a"}}
	listed, err := f.repo.List(ctx, byName)
	require.NoError(t, err)
	assert.Len(t, listed, 1)
	envs, err := f.repo.ListEnvironmentUUIDs(ctx, DeployIntentFilter{
		Kind: models.KindAgent, OUID: f.ouID, KindData: map[string]any{"environmentName": "dev"},
	})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{f.environment}, envs)

	other := DeployIntentFilter{Kind: models.KindAgent, OUID: f.ouID, KindData: map[string]any{"agentName": "b"}}
	listed, err = f.repo.List(ctx, other)
	require.NoError(t, err)
	assert.Empty(t, listed)
	otherEnv := uuid.New()
	listed, err = f.repo.List(ctx, DeployIntentFilter{Kind: models.KindAgent, OUID: f.ouID, EnvironmentUUID: &otherEnv})
	require.NoError(t, err)
	assert.Empty(t, listed)

	otherArtifact := uuid.New()
	require.NoError(t, f.repo.Delete(ctx, DeployIntentFilter{Kind: models.KindAgent, OUID: f.ouID, ArtifactUUID: &otherArtifact}))
	listed, err = f.repo.List(ctx, byName)
	require.NoError(t, err)
	assert.Len(t, listed, 1, "another artifact's intents only")

	require.NoError(t, f.repo.Delete(ctx, byName))
	listed, err = f.repo.List(ctx, DeployIntentFilter{Kind: models.KindAgent, OUID: f.ouID})
	require.NoError(t, err)
	assert.Empty(t, listed)
}

func TestDeployIntentStatusRowsAndArtifactCascade(t *testing.T) {
	ctx := context.Background()
	f := seedIntentFixture(t)
	intent := f.upsert(t, `{}`)
	deployment := f.deploy(t, models.DeploymentStatusDeploying)

	rows, err := f.repo.StatusRows(ctx, intent)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, deployment, rows[0].DeploymentID)
	otherGateway := uuid.New()
	rows, err = f.repo.StatusRows(ctx, models.DeployIntent{OUID: f.ouID, ArtifactUUID: f.artifact, GatewayUUID: &otherGateway})
	require.NoError(t, err)
	assert.Empty(t, rows, "a gateway-scoped intent reads only its gateway's row")

	require.NoError(t, db.GetDB().Transaction(func(tx *gorm.DB) error {
		return tx.Exec(`DELETE FROM artifacts WHERE uuid = ?`, f.artifact).Error
	}))
	_, err = f.repo.Get(ctx, intent.ID)
	assert.True(t, errors.Is(err, ErrDeployIntentNotFound), "an intent goes with its artifact")
}
