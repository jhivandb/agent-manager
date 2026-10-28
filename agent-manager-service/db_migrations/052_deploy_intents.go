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

package dbmigrations

import (
	"gorm.io/gorm"
)

// deploy_intents generalises a2a_publications into the reconcile core's intent table: one row
// per artifact and scope, an environment for environment-addressed kinds or the chosen gateway
// for gateway-addressed ones, saying whether the artifact should be deployed there and how far
// that has been carried out. What only one kind needs lives in its kind_data.
var migration052 = migration{
	ID: 52,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(MigrateDeployIntents)
	},
}

// MigrateDeployIntents creates deploy_intents, copies every a2a_publications row into it and drops
// a2a_publications. A pending row stays pending, a published row is done (its deployment_status
// row already tracks it, and its card was already queued for that deployment), and a failed row
// stays failed with its last_error. The upstream a row was published with is not kept: drift is
// checked against the current deployment's content. A row whose artifact is gone is not copied:
// it could only ever fail.
func MigrateDeployIntents(tx *gorm.DB) error {
	return runSQL(
		tx,
		`CREATE TABLE IF NOT EXISTS deploy_intents (
			id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			ou_id              VARCHAR(255) NOT NULL,
			kind               VARCHAR(32) NOT NULL,
			artifact_uuid      UUID NOT NULL REFERENCES artifacts(uuid) ON DELETE CASCADE,
			environment_uuid   UUID,
			gateway_uuid       UUID,
			desired            VARCHAR(20) NOT NULL DEFAULT 'DEPLOYED',
			state              VARCHAR(16) NOT NULL DEFAULT 'pending',
			attempt_count      INTEGER NOT NULL DEFAULT 0,
			next_attempt_at    TIMESTAMPTZ,
			last_error         TEXT NOT NULL DEFAULT '',
			publish_count      INTEGER NOT NULL DEFAULT 0,
			published_at       TIMESTAMPTZ,
			live_deployment_id UUID,
			kind_data          JSONB NOT NULL DEFAULT '{}',
			created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

			CONSTRAINT chk_deploy_intents_scope
				CHECK ((environment_uuid IS NULL) <> (gateway_uuid IS NULL)),
			CONSTRAINT chk_deploy_intents_desired CHECK (desired IN ('DEPLOYED', 'UNDEPLOYED')),
			CONSTRAINT chk_deploy_intents_state CHECK (state IN ('pending', 'done', 'failed'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_deploy_intents_environment
			ON deploy_intents (ou_id, artifact_uuid, environment_uuid) WHERE environment_uuid IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_deploy_intents_gateway
			ON deploy_intents (ou_id, artifact_uuid, gateway_uuid) WHERE gateway_uuid IS NOT NULL`,
		// The worker's due-scan.
		`CREATE INDEX IF NOT EXISTS idx_deploy_intents_due ON deploy_intents (state, next_attempt_at)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_intents_environment ON deploy_intents (ou_id, environment_uuid)`,
		`INSERT INTO deploy_intents (
			ou_id, kind, artifact_uuid, environment_uuid, desired, state, attempt_count, next_attempt_at,
			last_error, publish_count, published_at, live_deployment_id, kind_data, created_at, updated_at)
		SELECT DISTINCT ON (p.ou_id, p.artifact_uuid, p.environment_uuid)
			p.ou_id, 'Agent', p.artifact_uuid, p.environment_uuid, 'DEPLOYED',
			CASE p.status WHEN 'published' THEN 'done' WHEN 'failed' THEN 'failed' ELSE 'pending' END,
			p.attempt_count,
			CASE WHEN p.status = 'pending' THEN p.next_attempt_at END,
			p.last_error,
			CASE WHEN p.status = 'published' THEN 1 ELSE 0 END,
			CASE WHEN p.status = 'published' THEN p.updated_at END,
			CASE WHEN p.status = 'published' THEN (
				SELECT s.deployment_id FROM deployment_status s
				WHERE s.artifact_uuid = p.artifact_uuid AND s.ou_id = p.ou_id AND s.status = 'DEPLOYED'
				ORDER BY s.updated_at DESC LIMIT 1)
			END,
			jsonb_strip_nulls(jsonb_build_object(
				'projectName', p.project_name,
				'agentName', p.agent_name,
				'environmentName', p.environment_name,
				'agentUuid', p.agent_uuid,
				'source', p.source)),
			p.created_at, p.updated_at
		FROM a2a_publications p
		WHERE EXISTS (SELECT 1 FROM artifacts a WHERE a.uuid = p.artifact_uuid)
		ORDER BY p.ou_id, p.artifact_uuid, p.environment_uuid, p.updated_at DESC`,
		`DROP TABLE a2a_publications`,
	)
}
