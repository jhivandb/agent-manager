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

package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// syncFixture is one org with two gateways and one artifact of every kind AMS deploys, plus a
// second org with the same proxy handle.
type syncFixture struct {
	ouID, otherOUID     string
	gatewayID, otherGW  uuid.UUID
	foreignGW           uuid.UUID
	provider, proxy     uuid.UUID
	mcp, mapping, agent uuid.UUID
	foreignProxy        uuid.UUID
	proxyHandle         string
}

func seedGateway(t *testing.T, id uuid.UUID, ouID string) {
	t.Helper()
	gdb := db.GetDB()
	name := "sync-gw-" + id.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		id, name, name, "gw.example", models.GatewayRoleBoth, ouID).Error)
	t.Cleanup(func() { gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, id) })
}

func seedArtifact(t *testing.T, id uuid.UUID, ouID, kind, handle string) {
	t.Helper()
	gdb := db.GetDB()
	require.NoError(t, NewArtifactRepo(gdb).Create(gdb, &models.Artifact{
		UUID: id, Handle: handle, Name: handle, Version: "v1.0", Kind: kind, OUID: ouID,
	}))
	t.Cleanup(func() { gdb.Exec(`DELETE FROM artifacts WHERE uuid = ?`, id) })
}

func seedSync(t *testing.T) syncFixture {
	t.Helper()
	f := syncFixture{
		ouID: "ou-" + uuid.New().String()[:8], otherOUID: "ou-" + uuid.New().String()[:8],
		gatewayID: uuid.New(), otherGW: uuid.New(), foreignGW: uuid.New(),
		provider: uuid.New(), proxy: uuid.New(), mcp: uuid.New(), mapping: uuid.New(), agent: uuid.New(),
		foreignProxy: uuid.New(),
	}
	f.proxyHandle = "proxy-" + f.proxy.String()[:8]
	seedGateway(t, f.gatewayID, f.ouID)
	seedGateway(t, f.otherGW, f.ouID)
	seedGateway(t, f.foreignGW, f.otherOUID)
	seedArtifact(t, f.provider, f.ouID, models.KindLLMProvider, "provider-"+f.provider.String()[:8])
	seedArtifact(t, f.proxy, f.ouID, models.KindLLMProxy, f.proxyHandle)
	seedArtifact(t, f.mcp, f.ouID, models.KindMCPProxy, "mcp-"+f.mcp.String()[:8])
	seedArtifact(t, f.mapping, f.ouID, models.KindMCPMapping, "mapping-"+f.mapping.String()[:8])
	seedArtifact(t, f.agent, f.ouID, models.KindAgent, "agent-"+f.agent.String()[:8])
	seedArtifact(t, f.foreignProxy, f.otherOUID, models.KindLLMProxy, f.proxyHandle)
	return f
}

func (f syncFixture) deploy(t *testing.T, artifact, gateway uuid.UUID, ouID, content string) uuid.UUID {
	t.Helper()
	d := &models.Deployment{
		Name: "dep", ArtifactUUID: artifact, OUID: ouID, GatewayUUID: gateway, Content: []byte(content),
	}
	require.NoError(t, NewDeploymentRepo(db.GetDB()).CreateWithLimitEnforcement(d, 10))
	return d.DeploymentID
}

func setSyncRow(t *testing.T, artifact, gateway uuid.UUID, status, desired models.DeploymentStatus) {
	t.Helper()
	require.NoError(t, db.GetDB().Exec(
		`UPDATE deployment_status SET status = ?, status_desired = ? WHERE artifact_uuid = ? AND gateway_uuid = ?`,
		status, desired, artifact, gateway).Error)
}

func TestDeploymentListForGatewaySync(t *testing.T) {
	ctx := context.Background()
	f := seedSync(t)
	repo := NewDeploymentRepo(db.GetDB())

	f.deploy(t, f.provider, f.gatewayID, f.ouID, "old provider")
	providerDep := f.deploy(t, f.provider, f.gatewayID, f.ouID, "provider")
	proxyDep := f.deploy(t, f.proxy, f.gatewayID, f.ouID, "proxy")
	mcpDep := f.deploy(t, f.mcp, f.gatewayID, f.ouID, "mcp")
	mappingDep := f.deploy(t, f.mapping, f.gatewayID, f.ouID, "mapping")
	agentDep := f.deploy(t, f.agent, f.gatewayID, f.ouID, "agent")
	f.deploy(t, f.proxy, f.otherGW, f.ouID, "proxy on another gateway")
	f.deploy(t, f.foreignProxy, f.foreignGW, f.otherOUID, "another org's proxy")

	setSyncRow(t, f.provider, f.gatewayID, models.DeploymentStatusDeployed, models.DeploymentStatusDeployed)
	setSyncRow(t, f.proxy, f.gatewayID, models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed)
	setSyncRow(t, f.mcp, f.gatewayID, models.DeploymentStatusFailed, models.DeploymentStatusDeployed)
	require.NoError(t, db.GetDB().Exec(`UPDATE deployment_status SET status_reason = ? WHERE artifact_uuid = ?`,
		models.DeploymentReasonTimeout, f.mcp).Error)
	setSyncRow(t, f.mapping, f.gatewayID, models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed)
	// The agent stays DEPLOYING, desired DEPLOYED.

	rows, err := repo.ListForGatewaySync(ctx, f.gatewayID.String(), f.ouID)
	require.NoError(t, err)

	type entry struct {
		deploymentID uuid.UUID
		kind, id     string
		desired      models.DeploymentStatus
	}
	got := map[uuid.UUID]entry{}
	awaitingAck := map[uuid.UUID]bool{}
	for _, r := range rows {
		kind, ok := r.GatewayKind()
		require.True(t, ok, r.ArtifactKind)
		assert.WithinDuration(t, time.Now(), r.PerformedAt, time.Minute)
		got[r.ArtifactUUID] = entry{r.DeploymentID, kind, r.GatewayArtifactID(), r.StatusDesired}
		awaitingAck[r.ArtifactUUID] = r.AwaitsAck()
	}
	assert.Equal(t, map[uuid.UUID]bool{f.provider: false, f.proxy: true, f.mcp: true, f.mapping: false, f.agent: true},
		awaitingAck, "rows in flight, and rows the sweeper timed out, still await an ack")
	assert.Equal(t, map[uuid.UUID]entry{
		f.provider: {providerDep, models.GatewayKindLlmProvider, f.provider.String(), models.DeploymentStatusDeployed},
		f.proxy:    {proxyDep, models.GatewayKindLlmProxy, f.proxyHandle, models.DeploymentStatusUndeployed},
		f.mcp:      {mcpDep, models.GatewayKindMcp, f.mcp.String(), models.DeploymentStatusDeployed},
		f.mapping:  {mappingDep, models.GatewayKindMcp, f.mapping.String(), models.DeploymentStatusUndeployed},
		f.agent:    {agentDep, models.GatewayKindAgent, f.agent.String(), models.DeploymentStatusDeployed},
	}, got, "every status row of this gateway, with its current deployment and desired state")

	rows, err = repo.ListForGatewaySync(ctx, f.gatewayID.String(), f.otherOUID)
	require.NoError(t, err)
	assert.Empty(t, rows, "scoped to the gateway's org")
}

func TestDeploymentGetContentsForGatewaySync(t *testing.T) {
	ctx := context.Background()
	f := seedSync(t)
	repo := NewDeploymentRepo(db.GetDB())

	archived := f.deploy(t, f.provider, f.gatewayID, f.ouID, "old provider")
	provider := f.deploy(t, f.provider, f.gatewayID, f.ouID, "provider")
	proxy := f.deploy(t, f.proxy, f.gatewayID, f.ouID, "proxy")
	undeployed := f.deploy(t, f.mcp, f.gatewayID, f.ouID, "mcp")
	setSyncRow(t, f.mcp, f.gatewayID, models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed)
	elsewhere := f.deploy(t, f.agent, f.otherGW, f.ouID, "agent on another gateway")
	foreign := f.deploy(t, f.foreignProxy, f.foreignGW, f.otherOUID, "another org's proxy")

	got, err := repo.GetContentsForGatewaySync(ctx, f.gatewayID.String(), f.ouID,
		[]uuid.UUID{archived, provider, proxy, undeployed, elsewhere, foreign, uuid.New()})
	require.NoError(t, err)

	contents := map[uuid.UUID]string{}
	for _, c := range got {
		contents[c.DeploymentID] = string(c.Content)
		if c.DeploymentID == proxy {
			assert.Equal(t, f.proxyHandle, c.GatewayArtifactID())
		}
	}
	assert.Equal(t, map[uuid.UUID]string{provider: "provider", proxy: "proxy"}, contents,
		"only the gateway's own deployments that it is meant to hold")

	got, err = repo.GetContentsForGatewaySync(ctx, f.gatewayID.String(), f.ouID, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestArtifactExistingGatewayArtifactIDs(t *testing.T) {
	ctx := context.Background()
	f := seedSync(t)
	repo := NewArtifactRepo(db.GetDB())

	missing := uuid.NewString()
	got, err := repo.ExistingGatewayArtifactIDs(ctx, f.ouID, []string{
		f.provider.String(), f.proxyHandle, f.mcp.String(), f.agent.String(), missing, "no-such-handle",
		"mcp-" + f.mcp.String()[:8], // only an LLM proxy is named by its handle
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{f.provider.String(), f.proxyHandle, f.mcp.String(), f.agent.String()}, got)

	got, err = repo.ExistingGatewayArtifactIDs(ctx, f.otherOUID, []string{f.provider.String(), f.proxyHandle})
	require.NoError(t, err)
	assert.Equal(t, []string{f.proxyHandle}, got, "scoped to the org: only its own proxy of that handle")

	got, err = repo.ExistingGatewayArtifactIDs(ctx, f.ouID, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}
