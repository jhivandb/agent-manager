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

import "gorm.io/gorm"

// migration029 creates the scopes table: an org-global, resource-agnostic
// catalog of scope names. AMS stores only the catalog; grants (which agent gets
// which scope) live in each environment's own Thunder instance, not here.
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
