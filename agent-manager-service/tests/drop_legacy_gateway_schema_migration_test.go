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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	dbmigrations "github.com/wso2/agent-manager/agent-manager-service/db_migrations"
)

var legacyOrgNameTables = []string{"artifacts", "deployments", "deployment_status"}

// revertLegacyGatewaySchemaTo052 puts back what 053 drops, in the shape 003, 029 and 030 left it:
// association_mappings, and an organization_name column defaulting to ” on the gateway tables.
func revertLegacyGatewaySchemaTo052(t *testing.T, tx *gorm.DB) {
	t.Helper()
	require.NoError(t, tx.Exec(`CREATE TABLE IF NOT EXISTS association_mappings (
		id SERIAL PRIMARY KEY,
		artifact_uuid UUID NOT NULL REFERENCES artifacts(uuid) ON DELETE CASCADE,
		organization_name VARCHAR(255) NOT NULL DEFAULT '',
		resource_uuid UUID NOT NULL,
		association_type VARCHAR(20) NOT NULL,
		ou_id VARCHAR(255) NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		CONSTRAINT uq_association_artifact_resource_type_ou_id
			UNIQUE(artifact_uuid, resource_uuid, association_type, ou_id)
	)`).Error)
	for _, table := range legacyOrgNameTables {
		require.NoError(t, tx.Exec(`ALTER TABLE `+table+
			` ADD COLUMN IF NOT EXISTS organization_name VARCHAR(255) NOT NULL DEFAULT ''`).Error)
	}
}

type legacySchemaSeed struct {
	ouID                          string
	gateway, artifact, deployment uuid.UUID
}

func seedLegacyGatewaySchemaAt052(t *testing.T, tx *gorm.DB) legacySchemaSeed {
	t.Helper()
	s := legacySchemaSeed{
		ouID:    "ou-mig053-" + uuid.NewString()[:8],
		gateway: uuid.New(), artifact: uuid.New(), deployment: uuid.New(),
	}
	name := "mig053-" + s.gateway.String()[:8]
	require.NoError(t, tx.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, 'gw.example', 'egress', ?, TRUE)`, s.gateway, name, name, s.ouID).Error)
	require.NoError(t, tx.Exec(
		`INSERT INTO artifacts (uuid, handle, name, version, kind, ou_id, organization_name)
		 VALUES (?, ?, ?, 'v1.0', 'LlmProvider', ?, 'legacy-org')`,
		s.artifact, "mig053-"+s.artifact.String(), "mig053-"+s.artifact.String()[:8], s.ouID).Error)
	require.NoError(t, tx.Exec(
		`INSERT INTO deployments (deployment_id, name, artifact_uuid, ou_id, gateway_uuid, content, organization_name)
		 VALUES (?, 'dep', ?, ?, ?, 'kind: LlmProvider', 'legacy-org')`,
		s.deployment, s.artifact, s.ouID, s.gateway).Error)
	require.NoError(t, tx.Exec(
		`INSERT INTO deployment_status (artifact_uuid, ou_id, gateway_uuid, deployment_id, status, status_desired, organization_name)
		 VALUES (?, ?, ?, ?, 'DEPLOYED', 'DEPLOYED', 'legacy-org')`,
		s.artifact, s.ouID, s.gateway, s.deployment).Error)
	require.NoError(t, tx.Exec(
		`INSERT INTO association_mappings (artifact_uuid, resource_uuid, association_type, ou_id)
		 VALUES (?, ?, 'gateway', ?)`, s.artifact, s.gateway, s.ouID).Error)
	return s
}

func columnExists(t *testing.T, tx *gorm.DB, table, column string) bool {
	t.Helper()
	var n int64
	require.NoError(t, tx.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, table, column).Scan(&n).Error)
	return n > 0
}

func TestMigration053DropsAssociationMappingsAndOrganizationNameKeepingRows(t *testing.T) {
	tx := runtimeURLTestTx(t)
	revertLegacyGatewaySchemaTo052(t, tx)
	s := seedLegacyGatewaySchemaAt052(t, tx)

	require.NoError(t, dbmigrations.MigrateDropLegacyGatewaySchema(tx))

	var mappingsTable *string
	require.NoError(t, tx.Raw(`SELECT to_regclass('association_mappings')::text`).Scan(&mappingsTable).Error)
	assert.Nil(t, mappingsTable, "association_mappings is dropped")
	for _, table := range legacyOrgNameTables {
		assert.False(t, columnExists(t, tx, table, "organization_name"), table)
	}

	var status string
	require.NoError(t, tx.Raw(`SELECT s.status FROM deployment_status s
		JOIN deployments d ON d.deployment_id = s.deployment_id
		JOIN artifacts a ON a.uuid = s.artifact_uuid
		WHERE s.artifact_uuid = ? AND s.gateway_uuid = ? AND s.ou_id = ?`,
		s.artifact, s.gateway, s.ouID).Scan(&status).Error)
	assert.Equal(t, "DEPLOYED", status, "artifacts, deployments and their status rows survive")

	require.NoError(t, tx.Exec(`SAVEPOINT dup`).Error)
	assert.Error(t, tx.Exec(
		`INSERT INTO artifacts (uuid, handle, name, version, kind, ou_id) VALUES (?, ?, 'other', 'v1.0', 'LlmProvider', ?)`,
		uuid.New(), "mig053-"+s.artifact.String(), s.ouID).Error, "handles stay unique per ou_id")
	require.NoError(t, tx.Exec(`ROLLBACK TO SAVEPOINT dup`).Error)
}

func TestMigration053IsIdempotent(t *testing.T) {
	tx := runtimeURLTestTx(t)
	revertLegacyGatewaySchemaTo052(t, tx)
	seedLegacyGatewaySchemaAt052(t, tx)

	require.NoError(t, dbmigrations.MigrateDropLegacyGatewaySchema(tx))
	require.NoError(t, dbmigrations.MigrateDropLegacyGatewaySchema(tx))
}
