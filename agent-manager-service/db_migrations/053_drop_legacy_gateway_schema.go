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

// Drop gateway schema nothing reads or writes any more: association_mappings, which was never
// written, and the organization_name columns 030 left behind as display data once ou_id took
// over every key and index on them. No Go model maps those columns, so they only ever hold ”.
var migration053 = migration{
	ID: 53,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(MigrateDropLegacyGatewaySchema)
	},
}

// MigrateDropLegacyGatewaySchema drops association_mappings, and organization_name from artifacts,
// deployments and deployment_status. It can run more than once.
func MigrateDropLegacyGatewaySchema(tx *gorm.DB) error {
	return runSQL(
		tx,
		`DROP TABLE IF EXISTS association_mappings`,
		`ALTER TABLE artifacts DROP COLUMN IF EXISTS organization_name`,
		`ALTER TABLE deployments DROP COLUMN IF EXISTS organization_name`,
		`ALTER TABLE deployment_status DROP COLUMN IF EXISTS organization_name`,
	)
}
