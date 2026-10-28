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
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// seedAgentDeployment writes a gateway, an agent artifact and one deployment of it.
func seedAgentDeployment(t *testing.T) (artifactUUID uuid.UUID, ouID string) {
	t.Helper()
	gdb := db.GetDB()
	ouID = "ou-" + uuid.New().String()[:8]
	gatewayID, artifactUUID := uuid.New(), uuid.New()
	name := "dep-gw-" + gatewayID.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		gatewayID, name, name, "gw.example", models.GatewayRoleIngress, ouID).Error)
	require.NoError(t, NewArtifactRepo(gdb).Create(gdb, &models.Artifact{
		UUID: artifactUUID, Handle: "proj/agent/" + artifactUUID.String(), Name: "agent-" + artifactUUID.String()[:8],
		Version: "v1.0", Kind: models.KindAgent, OUID: ouID,
	}))
	deployed := models.DeploymentStatusDeployed
	require.NoError(t, NewDeploymentRepo(gdb).CreateWithLimitEnforcement(&models.Deployment{
		Name: "agent-deployment", ArtifactUUID: artifactUUID, OUID: ouID, GatewayUUID: gatewayID,
		Content: []byte("kind: Agent\n"), Status: &deployed,
	}, 10))
	t.Cleanup(func() {
		gdb.Exec(`DELETE FROM artifacts WHERE uuid = ?`, artifactUUID)
		gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, gatewayID)
	})
	return artifactUUID, ouID
}

// A mode switch keeps the artifact (and its API keys) but no deployment of it may be served.
func TestDeploymentDeleteForArtifactInATransaction(t *testing.T) {
	repo := NewDeploymentRepo(db.GetDB())
	artifactUUID, ouID := seedAgentDeployment(t)
	gateways, err := repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	require.Len(t, gateways, 1)

	rolledBack := db.GetDB().Transaction(func(tx *gorm.DB) error {
		require.NoError(t, repo.WithTx(tx).DeleteForArtifact(context.Background(), artifactUUID, ouID))
		return errors.New("roll back")
	})
	require.Error(t, rolledBack)
	gateways, err = repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	assert.Len(t, gateways, 1, "a rolled-back delete leaves the deployment")

	require.NoError(t, repo.DeleteForArtifact(context.Background(), artifactUUID, ouID))
	gateways, err = repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	assert.Empty(t, gateways, "deployment_status went with the deployments")
}

// lifecycleFixture is one artifact deployed to one gateway, through the repository.
type lifecycleFixture struct {
	repo         DeploymentRepository
	ouID         string
	handle       string
	gatewayID    uuid.UUID
	artifactUUID uuid.UUID
	deploymentID uuid.UUID
}

// seedLifecycle writes a gateway and an LLM proxy artifact, and deploys it with the default status.
func seedLifecycle(t *testing.T) lifecycleFixture {
	t.Helper()
	gdb := db.GetDB()
	f := lifecycleFixture{
		repo: NewDeploymentRepo(gdb), ouID: "ou-" + uuid.New().String()[:8],
		gatewayID: uuid.New(), artifactUUID: uuid.New(),
	}
	f.handle = "proxy-" + f.artifactUUID.String()[:8]
	name := "lc-gw-" + f.gatewayID.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		f.gatewayID, name, name, "gw.example", models.GatewayRoleEgress, f.ouID).Error)
	require.NoError(t, NewArtifactRepo(gdb).Create(gdb, &models.Artifact{
		UUID: f.artifactUUID, Handle: f.handle, Name: f.handle, Version: "v1.0", Kind: models.KindLLMProxy, OUID: f.ouID,
	}))
	t.Cleanup(func() {
		gdb.Exec(`DELETE FROM artifacts WHERE uuid = ?`, f.artifactUUID)
		gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, f.gatewayID)
	})
	f.deploymentID = f.deploy(t)
	return f
}

// deploy creates a new deployment, which becomes the row's current one.
func (f lifecycleFixture) deploy(t *testing.T) uuid.UUID {
	t.Helper()
	d := &models.Deployment{
		Name: "dep", ArtifactUUID: f.artifactUUID, OUID: f.ouID, GatewayUUID: f.gatewayID, Content: []byte("kind: LlmProxy\n"),
	}
	require.NoError(t, f.repo.CreateWithLimitEnforcement(d, 10))
	return d.DeploymentID
}

func (f lifecycleFixture) row(t *testing.T) models.DeploymentStatusRecord {
	t.Helper()
	var rec models.DeploymentStatusRecord
	require.NoError(t, db.GetDB().
		Where("artifact_uuid = ? AND ou_id = ? AND gateway_uuid = ?", f.artifactUUID, f.ouID, f.gatewayID).
		Take(&rec).Error)
	return rec
}

func (f lifecycleFixture) setRow(t *testing.T, status, desired models.DeploymentStatus, reason *string, performedAt time.Time) {
	t.Helper()
	require.NoError(t, db.GetDB().Exec(
		`UPDATE deployment_status SET status = ?, status_desired = ?, status_reason = ?, performed_at = ?
		 WHERE artifact_uuid = ? AND gateway_uuid = ?`,
		status, desired, reason, performedAt, f.artifactUUID, f.gatewayID).Error)
}

func (f lifecycleFixture) ack(t *testing.T, ack models.DeploymentAck) bool {
	t.Helper()
	applied, err := f.repo.ApplyAck(context.Background(), f.gatewayID.String(), ack)
	require.NoError(t, err)
	return applied
}

func reasonOf(reason string) *string { return &reason }

func timeoutReason() *string { return reasonOf(models.DeploymentReasonTimeout) }

func TestDeploymentCreateStartsDeploying(t *testing.T) {
	f := seedLifecycle(t)

	rec := f.row(t)
	assert.Equal(t, models.DeploymentStatusDeploying, rec.Status)
	assert.Equal(t, models.DeploymentStatusDeployed, rec.StatusDesired)
	assert.WithinDuration(t, time.Now(), rec.PerformedAt, time.Minute)

	// A redeploy over a failed row starts afresh: the old reason goes.
	f.setRow(t, models.DeploymentStatusFailed, models.DeploymentStatusDeployed, timeoutReason(), time.Now())
	next := f.deploy(t)
	rec = f.row(t)
	assert.Equal(t, next, rec.DeploymentID)
	assert.Equal(t, models.DeploymentStatusDeploying, rec.Status)
	assert.Empty(t, rec.StatusReason)
}

func TestDeploymentPlacedAndLiveGateways(t *testing.T) {
	f := seedLifecycle(t)
	gw := f.gatewayID.String()

	for _, tc := range []struct {
		status, desired models.DeploymentStatus
		placed, live    bool
	}{
		{models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, true, false},
		{models.DeploymentStatusDeployed, models.DeploymentStatusDeployed, true, true},
		{models.DeploymentStatusFailed, models.DeploymentStatusDeployed, true, false},
		{models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, false, false},
		{models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed, false, false},
		{models.DeploymentStatusFailed, models.DeploymentStatusUndeployed, false, false},
	} {
		f.setRow(t, tc.status, tc.desired, nil, time.Now())
		placed, err := f.repo.GetDeployedGatewaysByProvider(f.artifactUUID, f.ouID)
		require.NoError(t, err)
		live, err := f.repo.GetLiveGatewaysByProvider(f.artifactUUID, f.ouID)
		require.NoError(t, err)
		assert.Equal(t, tc.placed, slices.Contains(placed, gw), "placed %s/%s", tc.status, tc.desired)
		assert.Equal(t, tc.live, slices.Contains(live, gw), "live %s/%s", tc.status, tc.desired)

		current, err := f.repo.GetCurrentByGateway(f.artifactUUID.String(), gw, f.ouID)
		require.NoError(t, err)
		assert.Equal(t, tc.placed, current != nil, "the pull serves exactly the placed deployment: %s/%s", tc.status, tc.desired)
	}
}

func TestDeploymentGetWithStateReadsDesiredAndReason(t *testing.T) {
	f := seedLifecycle(t)
	f.setRow(t, models.DeploymentStatusFailed, models.DeploymentStatusDeployed, timeoutReason(), time.Now())

	d, err := f.repo.GetWithState(f.deploymentID.String(), f.artifactUUID.String(), f.ouID)
	require.NoError(t, err)
	require.NotNil(t, d.StatusDesired)
	require.NotNil(t, d.StatusReason)
	assert.Equal(t, models.DeploymentStatusDeployed, *d.StatusDesired)
	assert.Equal(t, models.DeploymentReasonTimeout, *d.StatusReason)
	assert.True(t, d.IsPlaced())
	assert.False(t, d.IsLive())

	failed := string(models.DeploymentStatusFailed)
	listed, err := f.repo.GetDeploymentsWithState(f.artifactUUID.String(), f.ouID, nil, &failed, 10)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, models.DeploymentReasonTimeout, *listed[0].StatusReason)
}

func TestDeploymentSetCurrentRestoresAsDeploying(t *testing.T) {
	f := seedLifecycle(t)
	first := f.deploymentID
	f.deploy(t)

	_, err := f.repo.SetCurrent(f.artifactUUID.String(), f.ouID, f.gatewayID.String(), first.String(), models.DeploymentStatusDeploying)
	require.NoError(t, err)
	rec := f.row(t)
	assert.Equal(t, first, rec.DeploymentID)
	assert.Equal(t, models.DeploymentStatusDeploying, rec.Status)
	assert.Equal(t, models.DeploymentStatusDeployed, rec.StatusDesired)

	_, err = f.repo.SetCurrent(f.artifactUUID.String(), f.ouID, f.gatewayID.String(), first.String(), models.DeploymentStatusFailed)
	assert.Error(t, err, "FAILED is an outcome, not a transition to start")
}

func TestDeploymentBeginUndeploy(t *testing.T) {
	ctx := context.Background()

	t.Run("an acked deployment is undeployed by its id", func(t *testing.T) {
		f := seedLifecycle(t)
		f.setRow(t, models.DeploymentStatusDeployed, models.DeploymentStatusDeployed, nil, time.Now())

		id, err := f.repo.BeginUndeploy(ctx, f.artifactUUID.String(), f.ouID, f.gatewayID.String())
		require.NoError(t, err)
		assert.Equal(t, f.deploymentID.String(), id)
		rec := f.row(t)
		assert.Equal(t, models.DeploymentStatusUndeploying, rec.Status)
		assert.Equal(t, models.DeploymentStatusUndeployed, rec.StatusDesired)
		assert.Equal(t, f.deploymentID, rec.DeploymentID)
	})

	for _, status := range []models.DeploymentStatus{models.DeploymentStatusDeploying, models.DeploymentStatusFailed} {
		t.Run("an unacked "+string(status)+" deployment is undeployed without an id", func(t *testing.T) {
			f := seedLifecycle(t)
			f.setRow(t, status, models.DeploymentStatusDeployed, timeoutReason(), time.Now().Add(-time.Hour))

			id, err := f.repo.BeginUndeploy(ctx, f.artifactUUID.String(), f.ouID, f.gatewayID.String())
			require.NoError(t, err)
			assert.Empty(t, id)
			rec := f.row(t)
			assert.Equal(t, models.DeploymentStatusUndeploying, rec.Status)
			assert.Empty(t, rec.StatusReason)
			assert.WithinDuration(t, time.Now(), rec.PerformedAt, time.Minute, "the timeout restarts")
		})
	}

	t.Run("nothing to undeploy", func(t *testing.T) {
		f := seedLifecycle(t)
		_, err := f.repo.BeginUndeploy(ctx, f.artifactUUID.String(), f.ouID, uuid.NewString())
		assert.ErrorIs(t, err, utils.ErrDeploymentNotActive, "no row")

		for _, status := range []models.DeploymentStatus{
			models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, models.DeploymentStatusFailed,
		} {
			f.setRow(t, status, models.DeploymentStatusUndeployed, nil, time.Now())
			_, err := f.repo.BeginUndeploy(ctx, f.artifactUUID.String(), f.ouID, f.gatewayID.String())
			assert.ErrorIs(t, err, utils.ErrDeploymentNotActive, string(status))
		}
	})
}

func TestDeploymentApplyAck(t *testing.T) {
	deploy := func(id uuid.UUID, ok bool, code string) models.DeploymentAck {
		return models.DeploymentAck{DeploymentID: id.String(), Action: models.DeploymentAckActionDeploy, Success: ok, ErrorCode: code}
	}

	t.Run("deploy success", func(t *testing.T) {
		f := seedLifecycle(t)
		assert.True(t, f.ack(t, deploy(f.deploymentID, true, "")))
		assert.Equal(t, models.DeploymentStatusDeployed, f.row(t).Status)
	})

	t.Run("deploy failure records the error code", func(t *testing.T) {
		f := seedLifecycle(t)
		assert.True(t, f.ack(t, deploy(f.deploymentID, false, "GATEWAY_PROCESSING_ERROR")))
		rec := f.row(t)
		assert.Equal(t, models.DeploymentStatusFailed, rec.Status)
		assert.Equal(t, models.DeploymentStatusDeployed, rec.StatusDesired)
		assert.Equal(t, "GATEWAY_PROCESSING_ERROR", rec.StatusReason)
	})

	t.Run("an ack for a superseded deployment changes nothing", func(t *testing.T) {
		f := seedLifecycle(t)
		f.deploy(t)
		assert.False(t, f.ack(t, deploy(f.deploymentID, true, "")))
		assert.Equal(t, models.DeploymentStatusDeploying, f.row(t).Status)
	})

	t.Run("an ack for a row not waiting on it changes nothing", func(t *testing.T) {
		f := seedLifecycle(t)
		f.setRow(t, models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, nil, time.Now())
		assert.False(t, f.ack(t, deploy(f.deploymentID, true, "")), "a deploy ack after the undeploy began")

		f.setRow(t, models.DeploymentStatusFailed, models.DeploymentStatusDeployed, reasonOf("GATEWAY_PROCESSING_ERROR"), time.Now())
		assert.False(t, f.ack(t, deploy(f.deploymentID, true, "")), "a deploy the gateway failed stays FAILED")
	})

	t.Run("a late ack after a timeout still counts", func(t *testing.T) {
		f := seedLifecycle(t)
		f.setRow(t, models.DeploymentStatusFailed, models.DeploymentStatusDeployed, timeoutReason(), time.Now())
		assert.True(t, f.ack(t, deploy(f.deploymentID, true, "")))
		rec := f.row(t)
		assert.Equal(t, models.DeploymentStatusDeployed, rec.Status)
		assert.Empty(t, rec.StatusReason)

		f.deploy(t)
		f.setRow(t, models.DeploymentStatusFailed, models.DeploymentStatusDeployed, timeoutReason(), time.Now())
		assert.False(t, f.ack(t, deploy(f.deploymentID, true, "")), "but only for the same deployment")
	})

	t.Run("undeploy success and failure, by id", func(t *testing.T) {
		f := seedLifecycle(t)
		undeploy := models.DeploymentAck{DeploymentID: f.deploymentID.String(), Action: models.DeploymentAckActionUndeploy}
		f.setRow(t, models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, nil, time.Now())
		assert.False(t, f.ack(t, deploy(f.deploymentID, true, "")), "a deploy ack doesn't answer an undeploy")

		undeploy.Success = true
		assert.True(t, f.ack(t, undeploy))
		assert.Equal(t, models.DeploymentStatusUndeployed, f.row(t).Status)

		f.setRow(t, models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, nil, time.Now())
		undeploy.Success, undeploy.ErrorCode = false, "DEPLOYMENT_ID_MISMATCH"
		assert.True(t, f.ack(t, undeploy))
		rec := f.row(t)
		assert.Equal(t, models.DeploymentStatusFailed, rec.Status)
		assert.Equal(t, models.DeploymentStatusUndeployed, rec.StatusDesired)
		assert.Equal(t, "DEPLOYMENT_ID_MISMATCH", rec.StatusReason)
	})

	t.Run("an undeploy ack without an id matches the artifact by UUID or handle", func(t *testing.T) {
		f := seedLifecycle(t)
		for _, artifactID := range []string{f.artifactUUID.String(), f.handle} {
			f.setRow(t, models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, nil, time.Now())
			assert.True(t, f.ack(t, models.DeploymentAck{ArtifactID: artifactID, Action: models.DeploymentAckActionUndeploy, Success: true}), artifactID)
			assert.Equal(t, models.DeploymentStatusUndeployed, f.row(t).Status, artifactID)
		}

		f.setRow(t, models.DeploymentStatusFailed, models.DeploymentStatusUndeployed, timeoutReason(), time.Now())
		assert.True(t, f.ack(t, models.DeploymentAck{ArtifactID: f.handle, Action: models.DeploymentAckActionUndeploy, Success: true}),
			"late, after the undeploy timed out")

		f.setRow(t, models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, nil, time.Now())
		assert.False(t, f.ack(t, models.DeploymentAck{ArtifactID: f.handle, Action: models.DeploymentAckActionUndeploy, Success: true}),
			"not once a redeploy began")
		assert.False(t, f.ack(t, models.DeploymentAck{ArtifactID: f.handle, Action: models.DeploymentAckActionDeploy, Success: true}),
			"a deploy ack needs its id")

		other := seedLifecycle(t)
		other.setRow(t, models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, nil, time.Now())
		applied, err := f.repo.ApplyAck(context.Background(), f.gatewayID.String(),
			models.DeploymentAck{ArtifactID: other.handle, Action: models.DeploymentAckActionUndeploy, Success: true})
		require.NoError(t, err)
		assert.False(t, applied, "another gateway's artifact")
		assert.Equal(t, models.DeploymentStatusUndeploying, other.row(t).Status)
	})

	t.Run("a malformed ack is an error", func(t *testing.T) {
		f := seedLifecycle(t)
		_, err := f.repo.ApplyAck(context.Background(), f.gatewayID.String(), models.DeploymentAck{DeploymentID: "nope", Action: models.DeploymentAckActionDeploy})
		assert.Error(t, err)
		_, err = f.repo.ApplyAck(context.Background(), f.gatewayID.String(), models.DeploymentAck{DeploymentID: f.deploymentID.String(), Action: "redeploy"})
		assert.Error(t, err)
	})
}

func TestDeploymentFailStaleTransitional(t *testing.T) {
	ctx := context.Background()
	cutoff := time.Now().Add(-5 * time.Minute)

	stale, fresh, settled, undeploying := seedLifecycle(t), seedLifecycle(t), seedLifecycle(t), seedLifecycle(t)
	stale.setRow(t, models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, nil, cutoff.Add(-time.Minute))
	undeploying.setRow(t, models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, nil, cutoff.Add(-time.Minute))
	settled.setRow(t, models.DeploymentStatusDeployed, models.DeploymentStatusDeployed, nil, cutoff.Add(-time.Hour))

	failed, err := stale.repo.FailStaleTransitional(ctx, cutoff)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, failed, int64(2))

	for _, f := range []lifecycleFixture{stale, undeploying} {
		rec := f.row(t)
		assert.Equal(t, models.DeploymentStatusFailed, rec.Status)
		assert.Equal(t, models.DeploymentReasonTimeout, rec.StatusReason)
	}
	assert.Equal(t, models.DeploymentStatusUndeployed, undeploying.row(t).StatusDesired, "the desired state is kept")
	assert.Equal(t, models.DeploymentStatusDeploying, fresh.row(t).Status)
	assert.Equal(t, models.DeploymentStatusDeployed, settled.row(t).Status)
}
