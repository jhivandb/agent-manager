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

// The A2A publication row gains the agent's card and the state around it.
//
// The card lives here rather than in its own table because the row already
// means "this agent's gateway state, per environment, with retry status" — a
// second table would need its own lock, migration and ordering against this one.
//
// card_deployment_id is indexed because the deployment ack handler looks a row
// up by it on every agentproxy ack, and the table has no other access path by
// deployment ID.
//
// Nothing is backfilled, by decision. An already-published agent keeps a null
// agent_card and stays on the gateway's passthrough card — which routes
// correctly — until its next redeploy or a card refresh moves it into phase 2.
var migration045 = migration{
	ID: 45,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			addColumns := `
			ALTER TABLE a2a_publications
				ADD COLUMN IF NOT EXISTS agent_card         JSONB,
				ADD COLUMN IF NOT EXISTS card_fetched_at    TIMESTAMPTZ,
				ADD COLUMN IF NOT EXISTS routed_at          TIMESTAMPTZ,
				ADD COLUMN IF NOT EXISTS card_deployment_id UUID`
			if err := runSQL(tx, addColumns); err != nil {
				return err
			}

			createIndex := `
			CREATE INDEX IF NOT EXISTS idx_a2a_publications_card_deployment
				ON a2a_publications (card_deployment_id)`
			return runSQL(tx, createIndex)
		})
	},
}
