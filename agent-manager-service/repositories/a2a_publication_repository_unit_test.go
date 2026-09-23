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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// The reconciler's due-scan filters on this exact string; a rename that missed
// the query would silently stop every publication.
func TestA2APublicationTableAndStatuses(t *testing.T) {
	assert.Equal(t, "a2a_publications", models.A2APublication{}.TableName())
	assert.Equal(t, models.A2APublicationStatus("pending"), models.A2APublicationStatusPending)
	assert.Equal(t, models.A2APublicationStatus("published"), models.A2APublicationStatusPublished)
	assert.Equal(t, models.A2APublicationStatus("failed"), models.A2APublicationStatusFailed)
}

// The reconciler's due-scan and the ack handler both filter on these exact
// strings; a rename that missed one would silently strand rows.
func TestA2APublicationCardStatuses(t *testing.T) {
	assert.Equal(t, models.A2APublicationStatus("routed"), models.A2APublicationStatusRouted)
	assert.Equal(t, models.A2APublicationStatus("rejected"), models.A2APublicationStatusRejected)
}

// The four card columns are nullable, so every one of them is a pointer or a
// nil-able slice. A non-pointer time.Time would write a zero timestamp on the
// first deploy and make "never fetched" indistinguishable from "fetched at the
// epoch".
func TestA2APublicationCardFieldsAreNullable(t *testing.T) {
	var pub models.A2APublication
	assert.Nil(t, pub.AgentCard)
	assert.Nil(t, pub.CardFetchedAt)
	assert.Nil(t, pub.RoutedAt)
	assert.Nil(t, pub.CardDeploymentID)
}
