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

// Make deployment_status track what the gateway actually holds: a deploy or undeploy
// starts DEPLOYING or UNDEPLOYING, and the gateway's ack (or the timeout sweeper) settles
// it. Existing DEPLOYED and UNDEPLOYED rows keep their meaning.
var migration051 = migration{
	ID: 51,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(MigrateDeploymentStatusLifecycle)
	},
}

// MigrateDeploymentStatusLifecycle widens the status CHECK to the five lifecycle states and
// adds status_desired, status_reason and performed_at, back-filled from the existing rows:
// a row's desired state is the state it is in, and its transition was performed when it was
// last updated.
func MigrateDeploymentStatusLifecycle(tx *gorm.DB) error {
	return runSQL(
		tx,
		`ALTER TABLE deployment_status DROP CONSTRAINT IF EXISTS chk_deployment_status`,
		`ALTER TABLE deployment_status
			ADD COLUMN IF NOT EXISTS status_desired VARCHAR(20),
			ADD COLUMN IF NOT EXISTS status_reason TEXT,
			ADD COLUMN IF NOT EXISTS performed_at TIMESTAMP`,
		`UPDATE deployment_status SET status_desired = status WHERE status_desired IS NULL`,
		`UPDATE deployment_status SET performed_at = COALESCE(updated_at, CURRENT_TIMESTAMP) WHERE performed_at IS NULL`,
		// The old 'DEPLOYED' default would claim an ack nobody received.
		`ALTER TABLE deployment_status
			ALTER COLUMN status DROP DEFAULT,
			ALTER COLUMN status_desired SET NOT NULL,
			ALTER COLUMN performed_at SET NOT NULL,
			ALTER COLUMN performed_at SET DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE deployment_status
			ADD CONSTRAINT chk_deployment_status
				CHECK (status IN ('DEPLOYING', 'DEPLOYED', 'UNDEPLOYING', 'UNDEPLOYED', 'FAILED')),
			ADD CONSTRAINT chk_deployment_status_desired
				CHECK (status_desired IN ('DEPLOYED', 'UNDEPLOYED'))`,
		// The timeout sweeper scans only rows still waiting for an ack.
		`CREATE INDEX IF NOT EXISTS idx_deployment_status_transitional
			ON deployment_status (performed_at)
			WHERE status IN ('DEPLOYING', 'UNDEPLOYING')`,
	)
}
