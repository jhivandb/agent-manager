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

package tests

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	dbmigrations "github.com/wso2/agent-manager/agent-manager-service/db_migrations"
)

// revertDeploymentStatusTo050 puts deployment_status back in its pre-051 shape inside tx. The old
// CHECK is NOT VALID so rows other tests committed under the new states don't block it.
func revertDeploymentStatusTo050(t *testing.T, tx *gorm.DB) {
	t.Helper()
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_deployment_status_transitional`,
		`ALTER TABLE deployment_status
			DROP CONSTRAINT IF EXISTS chk_deployment_status,
			DROP CONSTRAINT IF EXISTS chk_deployment_status_desired,
			DROP COLUMN status_desired,
			DROP COLUMN status_reason,
			DROP COLUMN performed_at,
			ALTER COLUMN status SET DEFAULT 'DEPLOYED'`,
		`ALTER TABLE deployment_status
			ADD CONSTRAINT chk_deployment_status CHECK (status IN ('DEPLOYED', 'UNDEPLOYED')) NOT VALID`,
	} {
		require.NoError(t, tx.Exec(stmt).Error)
	}
}

type lifecycleSeed struct {
	ouID                         string
	gatewayA, gatewayB, artifact uuid.UUID
	deployed, undeployed, old    uuid.UUID
	deployedAt, undeployedAt     time.Time
}

// seedDeploymentStatusAt050 writes one artifact on two gateways: DEPLOYED on A, UNDEPLOYED on B,
// and an archived deployment on A with no status row.
func seedDeploymentStatusAt050(t *testing.T, tx *gorm.DB) lifecycleSeed {
	t.Helper()
	s := lifecycleSeed{
		ouID:     "ou-mig051-" + uuid.NewString()[:8],
		gatewayA: uuid.New(), gatewayB: uuid.New(), artifact: uuid.New(),
		deployed: uuid.New(), undeployed: uuid.New(), old: uuid.New(),
		deployedAt:   time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
		undeployedAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
	}
	for _, gw := range []uuid.UUID{s.gatewayA, s.gatewayB} {
		name := "mig051-" + gw.String()[:8]
		require.NoError(t, tx.Exec(
			`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
			 VALUES (?, ?, ?, 'gw.example', 'egress', ?, TRUE)`, gw, name, name, s.ouID).Error)
	}
	require.NoError(t, tx.Exec(
		`INSERT INTO artifacts (uuid, handle, name, version, kind, ou_id)
		 VALUES (?, ?, ?, 'v1.0', 'LlmProvider', ?)`,
		s.artifact, "mig051-"+s.artifact.String(), "mig051-"+s.artifact.String()[:8], s.ouID).Error)
	for _, d := range []struct{ id, gw uuid.UUID }{{s.old, s.gatewayA}, {s.deployed, s.gatewayA}, {s.undeployed, s.gatewayB}} {
		require.NoError(t, tx.Exec(
			`INSERT INTO deployments (deployment_id, name, artifact_uuid, ou_id, gateway_uuid, content)
			 VALUES (?, 'dep', ?, ?, ?, 'kind: LlmProvider')`, d.id, s.artifact, s.ouID, d.gw).Error)
	}
	require.NoError(t, tx.Exec(
		`INSERT INTO deployment_status (artifact_uuid, ou_id, gateway_uuid, deployment_id, status, updated_at)
		 VALUES (?, ?, ?, ?, 'DEPLOYED', ?), (?, ?, ?, ?, 'UNDEPLOYED', ?)`,
		s.artifact, s.ouID, s.gatewayA, s.deployed, s.deployedAt,
		s.artifact, s.ouID, s.gatewayB, s.undeployed, s.undeployedAt).Error)
	return s
}

type migratedStatusRow struct {
	DeploymentID  uuid.UUID
	Status        string
	StatusDesired string
	StatusReason  sql.NullString
	PerformedAt   time.Time
}

func TestMigration051KeepsDeployedAndUndeployedRowsMeaning(t *testing.T) {
	tx := runtimeURLTestTx(t)
	revertDeploymentStatusTo050(t, tx)
	s := seedDeploymentStatusAt050(t, tx)

	require.NoError(t, dbmigrations.MigrateDeploymentStatusLifecycle(tx))

	var rows []migratedStatusRow
	require.NoError(t, tx.Raw(
		`SELECT deployment_id, status, status_desired, status_reason, performed_at
		   FROM deployment_status WHERE artifact_uuid = ? ORDER BY status`, s.artifact).Scan(&rows).Error)
	require.Len(t, rows, 2, "the archived deployment still has no status row")
	assert.Equal(t, migratedStatusRow{
		DeploymentID: s.deployed, Status: "DEPLOYED", StatusDesired: "DEPLOYED", PerformedAt: s.deployedAt,
	}, rows[0])
	assert.Equal(t, migratedStatusRow{
		DeploymentID: s.undeployed, Status: "UNDEPLOYED", StatusDesired: "UNDEPLOYED", PerformedAt: s.undeployedAt,
	}, rows[1])

	var deployments int64
	require.NoError(t, tx.Raw(`SELECT count(*) FROM deployments WHERE artifact_uuid = ?`, s.artifact).Scan(&deployments).Error)
	assert.EqualValues(t, 3, deployments, "no deployment history is lost")
}

func TestMigration051AcceptsTheLifecycleStatesAndRejectsOthers(t *testing.T) {
	tx := runtimeURLTestTx(t)
	revertDeploymentStatusTo050(t, tx)
	s := seedDeploymentStatusAt050(t, tx)
	require.NoError(t, dbmigrations.MigrateDeploymentStatusLifecycle(tx))

	setStatus := func(status, desired string) error {
		return tx.Exec(`UPDATE deployment_status SET status = ?, status_desired = ?
			WHERE artifact_uuid = ? AND gateway_uuid = ?`, status, desired, s.artifact, s.gatewayA).Error
	}
	for _, status := range []string{"DEPLOYING", "DEPLOYED", "UNDEPLOYING", "UNDEPLOYED", "FAILED"} {
		assert.NoError(t, setStatus(status, "DEPLOYED"), status)
	}

	for _, bad := range [][2]string{{"ARCHIVED", "DEPLOYED"}, {"DEPLOYED", "FAILED"}} {
		require.NoError(t, tx.Exec(`SAVEPOINT bad`).Error)
		assert.Error(t, tx.Exec(`UPDATE deployment_status SET status = ?, status_desired = ?
			WHERE artifact_uuid = ? AND gateway_uuid = ?`, bad[0], bad[1], s.artifact, s.gatewayA).Error, bad)
		require.NoError(t, tx.Exec(`ROLLBACK TO SAVEPOINT bad`).Error)
	}
}
