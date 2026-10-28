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

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
	"github.com/wso2/agent-manager/agent-manager-service/wiring"
)

// seedEgressGateway inserts an egress gateway for ouID mapped to a new environment.
func seedEgressGateway(t *testing.T, ouID string) uuid.UUID {
	t.Helper()
	gdb := db.GetDB()
	gatewayID := uuid.New()
	name := "llm-intent-gw-" + gatewayID.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		gatewayID, name, name, "llm.example.com", models.GatewayRoleEgress, ouID,
	).Error)
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateway_environment_mappings (gateway_uuid, environment_uuid) VALUES (?, ?)`, gatewayID, uuid.New(),
	).Error)
	t.Cleanup(func() {
		gdb.Exec(`DELETE FROM gateway_environment_mappings WHERE gateway_uuid = ?`, gatewayID)
		gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, gatewayID)
	})
	return gatewayID
}

// seedProvider stores an LLM provider whose configuration renders unless upstream is nil.
func seedProvider(t *testing.T, ouID string, upstream *models.UpstreamConfig) *models.LLMProvider {
	t.Helper()
	gdb := db.GetDB()
	handle := "llm-intent-prov-" + uuid.NewString()[:8]
	provider := &models.LLMProvider{
		TemplateHandle: "openai",
		Configuration:  models.LLMProviderConfig{Name: handle, Handle: handle, Version: "v1.0", Upstream: upstream},
	}
	require.NoError(t, gdb.Transaction(func(tx *gorm.DB) error {
		return repositories.NewLLMProviderRepo(gdb).Create(tx, provider, handle, handle, "v1.0", ouID)
	}))
	t.Cleanup(func() { gdb.Exec(`DELETE FROM artifacts WHERE uuid = ?`, provider.UUID) })
	return provider
}

func TestLLMProviderDeploysThroughDeployIntents(t *testing.T) {
	gdb := db.GetDB()
	ouID := "llm-intent-org-" + uuid.NewString()[:8]
	gatewayID := seedEgressGateway(t, ouID)
	deploymentRepo := repositories.NewDeploymentRepo(gdb)
	providerRepo := repositories.NewLLMProviderRepo(gdb)
	gatewayRepo := repositories.NewGatewayRepo(gdb)
	hub := &lifecycleEventHub{}
	events := services.NewGatewayEventsService(hub)
	kind := services.NewLLMProviderKind(providerRepo)
	core := reconcile.NewCore(reconcile.TxRunner(wiring.ProvideTxRunner(gdb)), repositories.NewDeployIntentRepository(gdb),
		deploymentRepo, events, reconcile.DefaultConfig(), testLifecycleLogger(), kind)
	svc := services.NewLLMProviderDeploymentService(deploymentRepo, providerRepo, nil, gatewayRepo, events, core, kind)

	intentsOf := func(t *testing.T, artifactUUID uuid.UUID) []models.DeployIntent {
		t.Helper()
		var intents []models.DeployIntent
		require.NoError(t, gdb.Where("artifact_uuid = ?", artifactUUID).Find(&intents).Error)
		return intents
	}
	statusOf := func(t *testing.T, artifactUUID uuid.UUID) models.DeploymentStatusRecord {
		t.Helper()
		var row models.DeploymentStatusRecord
		require.NoError(t, gdb.Where("artifact_uuid = ? AND gateway_uuid = ?", artifactUUID, gatewayID).First(&row).Error)
		return row
	}
	deploy := func(provider *models.LLMProvider) (*models.Deployment, error) {
		return svc.DeployLLMProvider(provider.UUID.String(), &models.DeployAPIRequest{
			Name: "intent-dep", Base: "current", GatewayID: gatewayID.String(),
		}, ouID)
	}

	t.Run("an invalid configuration commits nothing", func(t *testing.T) {
		provider := seedProvider(t, ouID, nil)

		_, err := deploy(provider)

		require.ErrorIs(t, err, utils.ErrInvalidInput)
		assert.Empty(t, intentsOf(t, provider.UUID), "the intent rolled back with the deploy")
		var count int64
		require.NoError(t, gdb.Model(&models.Deployment{}).Where("artifact_uuid = ?", provider.UUID).Count(&count).Error)
		assert.Zero(t, count)
	})

	provider := seedProvider(t, ouID, &models.UpstreamConfig{Main: &models.UpstreamEndpoint{URL: "https://api.openai.com"}})
	var first *models.Deployment

	t.Run("deploy records a done gateway intent and a DEPLOYING row", func(t *testing.T) {
		var err error
		first, err = deploy(provider)
		require.NoError(t, err)

		intents := intentsOf(t, provider.UUID)
		require.Len(t, intents, 1)
		assert.Equal(t, models.KindLLMProvider, intents[0].Kind)
		assert.Equal(t, &gatewayID, intents[0].GatewayUUID)
		assert.Equal(t, models.DeployIntentDone, intents[0].State)
		row := statusOf(t, provider.UUID)
		assert.Equal(t, models.DeploymentStatusDeploying, row.Status)
		assert.Equal(t, first.DeploymentID, row.DeploymentID)
		require.NotEmpty(t, hub.published)
		assert.Equal(t, provider.UUID.String(), hub.published[len(hub.published)-1].EntityID)
	})

	t.Run("undeploy and restore reuse the intent", func(t *testing.T) {
		_, err := svc.UndeployLLMProviderDeployment(provider.UUID.String(), first.DeploymentID.String(), gatewayID.String(), ouID)
		require.NoError(t, err)
		intents := intentsOf(t, provider.UUID)
		require.Len(t, intents, 1)
		assert.Equal(t, models.DeploymentStatusUndeployed, intents[0].Desired)
		assert.Equal(t, models.DeploymentStatusUndeploying, statusOf(t, provider.UUID).Status)

		_, err = svc.RestoreLLMProviderDeployment(provider.UUID.String(), first.DeploymentID.String(), gatewayID.String(), ouID)
		require.NoError(t, err)
		intents = intentsOf(t, provider.UUID)
		require.Len(t, intents, 1)
		assert.Equal(t, models.DeploymentStatusDeployed, intents[0].Desired)
		assert.Equal(t, models.DeployIntentDone, intents[0].State)
		row := statusOf(t, provider.UUID)
		assert.Equal(t, models.DeploymentStatusDeploying, row.Status)
		assert.Equal(t, first.DeploymentID, row.DeploymentID)
	})

	t.Run("deleting the artifact deletes its intents", func(t *testing.T) {
		require.NoError(t, providerRepo.Delete(provider.UUID.String(), ouID))
		assert.Empty(t, intentsOf(t, provider.UUID))
	})
}
