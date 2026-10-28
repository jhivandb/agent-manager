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
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	dbmigrations "github.com/wso2/agent-manager/agent-manager-service/db_migrations"
)

// revertDeployIntentsTo051 puts back a2a_publications, as migrations 044-048 left it, in place of
// deploy_intents, inside tx.
func revertDeployIntentsTo051(t *testing.T, tx *gorm.DB) {
	t.Helper()
	for _, stmt := range []string{
		`DROP TABLE deploy_intents`,
		`CREATE TABLE a2a_publications (
			id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			ou_id                  VARCHAR(255) NOT NULL,
			project_name           VARCHAR(255) NOT NULL,
			agent_name             VARCHAR(255) NOT NULL,
			environment_name       VARCHAR(255) NOT NULL,
			environment_uuid       UUID NOT NULL,
			artifact_uuid          UUID NOT NULL,
			status                 VARCHAR(32) NOT NULL DEFAULT 'pending',
			attempt_count          INTEGER NOT NULL DEFAULT 0,
			last_error             TEXT NOT NULL DEFAULT '',
			next_attempt_at        TIMESTAMPTZ,
			created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_upstream_url TEXT NOT NULL DEFAULT '',
			agent_uuid             UUID,
			source                 VARCHAR(32) NOT NULL DEFAULT 'platform',
			CONSTRAINT uq_a2a_publications_agent_env UNIQUE (ou_id, project_name, agent_name, environment_name),
			CONSTRAINT uq_a2a_publications_agent_env_uuid UNIQUE (ou_id, project_name, agent_name, environment_uuid)
		)`,
	} {
		require.NoError(t, tx.Exec(stmt).Error)
	}
}

type a2aPublicationSeed struct {
	agent, status, lastError, source string
	attempts                         int
	agentUUID                        *uuid.UUID
	artifact, environment            uuid.UUID
	nextAttemptAt                    *time.Time
}

type migratedIntent struct {
	Kind             string
	ArtifactUUID     uuid.UUID
	EnvironmentUUID  *uuid.UUID
	GatewayUUID      *uuid.UUID
	Desired          string
	State            string
	AttemptCount     int
	NextAttemptAt    *time.Time
	LastError        string
	PublishCount     int
	LiveDeploymentID *uuid.UUID
	KindData         json.RawMessage
}

func TestMigration052CopiesEveryA2APublication(t *testing.T) {
	tx := runtimeURLTestTx(t)
	revertDeployIntentsTo051(t, tx)

	ouID := "ou-mig052-" + uuid.NewString()[:8]
	gateway, liveDeployment := uuid.New(), uuid.New()
	nextAttempt := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	agentUUID := uuid.New()
	seeds := []a2aPublicationSeed{
		{
			agent: "waiting", status: "pending", lastError: "release binding has not published a service URL yet",
			attempts: 0, source: "platform", agentUUID: &agentUUID, nextAttemptAt: &nextAttempt,
		},
		{agent: "published", status: "published", source: "platform"},
		{
			agent: "exhausted", status: "failed", lastError: "no ingress gateway is mapped to this environment yet",
			attempts: 30, source: "external",
		},
	}
	for i := range seeds {
		seeds[i].artifact, seeds[i].environment = uuid.New(), uuid.New()
		require.NoError(t, tx.Exec(
			`INSERT INTO artifacts (uuid, handle, name, version, kind, ou_id) VALUES (?, ?, ?, 'v1.0', 'Agent', ?)`,
			seeds[i].artifact, "mig052-"+seeds[i].artifact.String(), "mig052-"+seeds[i].agent, ouID).Error)
		require.NoError(t, tx.Exec(
			`INSERT INTO a2a_publications (ou_id, project_name, agent_name, environment_name, environment_uuid,
				artifact_uuid, status, attempt_count, last_error, next_attempt_at, published_upstream_url, agent_uuid, source)
			 VALUES (?, 'proj', ?, 'dev', ?, ?, ?, ?, ?, ?, 'http://svc:8080', ?, ?)`,
			ouID, seeds[i].agent, seeds[i].environment, seeds[i].artifact, seeds[i].status, seeds[i].attempts,
			seeds[i].lastError, seeds[i].nextAttemptAt, seeds[i].agentUUID, seeds[i].source).Error)
	}
	published := seeds[1]
	require.NoError(t, tx.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, 'gw.example', 'ingress', ?, TRUE)`, gateway, "mig052-"+gateway.String()[:8], "gw", ouID).Error)
	require.NoError(t, tx.Exec(
		`INSERT INTO deployments (deployment_id, name, artifact_uuid, ou_id, gateway_uuid, content)
		 VALUES (?, 'published-deployment', ?, ?, ?, 'kind: Agent')`, liveDeployment, published.artifact, ouID, gateway).Error)
	require.NoError(t, tx.Exec(
		`INSERT INTO deployment_status (artifact_uuid, ou_id, gateway_uuid, deployment_id, status, status_desired)
		 VALUES (?, ?, ?, ?, 'DEPLOYED', 'DEPLOYED')`, published.artifact, ouID, gateway, liveDeployment).Error)
	orphanEnv := uuid.New()
	require.NoError(t, tx.Exec(
		`INSERT INTO a2a_publications (ou_id, project_name, agent_name, environment_name, environment_uuid, artifact_uuid)
		 VALUES (?, 'proj', 'orphan', 'dev', ?, ?)`, ouID, orphanEnv, uuid.New()).Error)

	require.NoError(t, dbmigrations.MigrateDeployIntents(tx))

	var a2aTables int64
	require.NoError(t, tx.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'a2a_publications'`).Scan(&a2aTables).Error)
	assert.Zero(t, a2aTables, "a2a_publications is dropped")

	var orphans int64
	require.NoError(t, tx.Raw(`SELECT count(*) FROM deploy_intents WHERE environment_uuid = ?`, orphanEnv).Scan(&orphans).Error)
	assert.Zero(t, orphans, "a row whose artifact is gone is not copied")

	got := map[string]migratedIntent{}
	for _, seed := range seeds {
		var row migratedIntent
		require.NoError(t, tx.Raw(
			`SELECT kind, artifact_uuid, environment_uuid, gateway_uuid, desired, state, attempt_count, next_attempt_at,
			        last_error, publish_count, live_deployment_id, kind_data
			   FROM deploy_intents WHERE artifact_uuid = ?`, seed.artifact).Scan(&row).Error)
		require.Equal(t, "Agent", row.Kind, seed.agent)
		assert.Equal(t, seed.environment, *row.EnvironmentUUID, seed.agent)
		assert.Nil(t, row.GatewayUUID, seed.agent)
		assert.Equal(t, "DEPLOYED", row.Desired, seed.agent)
		assert.Equal(t, seed.attempts, row.AttemptCount, seed.agent)
		assert.Equal(t, seed.lastError, row.LastError, seed.agent)
		got[seed.agent] = row
	}

	assert.Equal(t, "pending", got["waiting"].State)
	require.NotNil(t, got["waiting"].NextAttemptAt)
	assert.True(t, nextAttempt.Equal(*got["waiting"].NextAttemptAt), "a pending row keeps its schedule")
	assert.JSONEq(t, `{"projectName":"proj","agentName":"waiting","environmentName":"dev","agentUuid":"`+agentUUID.String()+`","source":"platform"}`,
		string(got["waiting"].KindData))

	assert.Equal(t, "done", got["published"].State)
	assert.Nil(t, got["published"].NextAttemptAt)
	assert.Equal(t, 1, got["published"].PublishCount, "its event already went out")
	require.NotNil(t, got["published"].LiveDeploymentID)
	assert.Equal(t, liveDeployment, *got["published"].LiveDeploymentID, "its card was already queued for that deployment")
	assert.JSONEq(t, `{"projectName":"proj","agentName":"published","environmentName":"dev","source":"platform"}`,
		string(got["published"].KindData))

	assert.Equal(t, "failed", got["exhausted"].State)
	assert.Nil(t, got["exhausted"].LiveDeploymentID)
	assert.JSONEq(t, `{"projectName":"proj","agentName":"exhausted","environmentName":"dev","source":"external"}`,
		string(got["exhausted"].KindData))
}

func TestMigration052KeyAllowsOneIntentPerArtifactAndScope(t *testing.T) {
	tx := runtimeURLTestTx(t)
	revertDeployIntentsTo051(t, tx)
	require.NoError(t, dbmigrations.MigrateDeployIntents(tx))

	ouID, artifact := "ou-mig052-"+uuid.NewString()[:8], uuid.New()
	require.NoError(t, tx.Exec(
		`INSERT INTO artifacts (uuid, handle, name, version, kind, ou_id) VALUES (?, ?, 'p', 'v1.0', 'LlmProvider', ?)`,
		artifact, "mig052-"+artifact.String(), ouID).Error)
	insert := func(env, gateway *uuid.UUID) error {
		require.NoError(t, tx.Exec(`SAVEPOINT intent`).Error)
		err := tx.Exec(`INSERT INTO deploy_intents (ou_id, kind, artifact_uuid, environment_uuid, gateway_uuid)
			VALUES (?, 'LlmProvider', ?, ?, ?)`, ouID, artifact, env, gateway).Error
		if err != nil {
			require.NoError(t, tx.Exec(`ROLLBACK TO SAVEPOINT intent`).Error)
		}
		return err
	}
	env, gw := uuid.New(), uuid.New()
	assert.Error(t, insert(nil, nil), "a scope is required")
	assert.Error(t, insert(&env, &gw), "only one scope")
	assert.NoError(t, insert(&env, nil))
	assert.Error(t, insert(&env, nil), "one intent per environment")
	assert.NoError(t, insert(nil, &gw))
	assert.Error(t, insert(nil, &gw), "one intent per gateway")
	other := uuid.New()
	assert.NoError(t, insert(&other, nil))
}
