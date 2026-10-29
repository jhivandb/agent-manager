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

package services

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

func newGatewaySyncService(deployments *repomocks.DeploymentRepositoryMock, artifacts *repomocks.ArtifactRepositoryMock, key []byte) *GatewayInternalAPIService {
	return NewGatewayInternalAPIService(nil, nil, deployments, nil, artifacts, nil, key)
}

func syncRef(kind, handle string) models.GatewayArtifactRef {
	return models.GatewayArtifactRef{ArtifactUUID: uuid.New(), ArtifactHandle: handle, ArtifactKind: kind}
}

func TestListGatewayDeploymentsReportsTheDesiredStateUnderTheGatewaysNames(t *testing.T) {
	performedAt := time.Date(2026, 10, 28, 12, 0, 0, 123456789, time.UTC)
	provider, proxy := syncRef(models.KindLLMProvider, "provider"), syncRef(models.KindLLMProxy, "my-proxy")
	mapping, agent := syncRef(models.KindMCPMapping, "mapping"), syncRef(models.KindAgent, "agent")
	restAPI := syncRef(models.KindRestAPI, "rest")
	rows := []models.GatewaySyncDeployment{
		{GatewayArtifactRef: provider, DeploymentID: uuid.New(), StatusDesired: models.DeploymentStatusDeployed, PerformedAt: performedAt},
		{GatewayArtifactRef: proxy, DeploymentID: uuid.New(), StatusDesired: models.DeploymentStatusUndeployed, PerformedAt: performedAt},
		{GatewayArtifactRef: mapping, DeploymentID: uuid.New(), StatusDesired: models.DeploymentStatusDeployed, PerformedAt: performedAt},
		{GatewayArtifactRef: agent, DeploymentID: uuid.New(), StatusDesired: models.DeploymentStatusDeployed, PerformedAt: performedAt},
		{GatewayArtifactRef: restAPI, DeploymentID: uuid.New(), StatusDesired: models.DeploymentStatusDeployed, PerformedAt: performedAt},
	}
	deployments := &repomocks.DeploymentRepositoryMock{
		ListForGatewaySyncFunc: func(_ context.Context, gatewayID, orgUUID string) ([]models.GatewaySyncDeployment, error) {
			assert.Equal(t, "gw-1", gatewayID)
			assert.Equal(t, "ou-1", orgUUID)
			return rows, nil
		},
	}

	got, err := newGatewaySyncService(deployments, nil, nil).ListGatewayDeployments(context.Background(), "ou-1", "gw-1")
	require.NoError(t, err)

	deployedAt := performedAt.Truncate(time.Millisecond)
	want := []models.ControlPlaneDeployment{
		{ArtifactID: provider.ArtifactUUID.String(), DeploymentID: rows[0].DeploymentID.String(), Kind: "LlmProvider", State: "deployed", DeployedAt: deployedAt},
		{ArtifactID: "my-proxy", DeploymentID: rows[1].DeploymentID.String(), Kind: "LlmProxy", State: "undeployed", DeployedAt: deployedAt},
		{ArtifactID: mapping.ArtifactUUID.String(), DeploymentID: rows[2].DeploymentID.String(), Kind: "Mcp", State: "deployed", DeployedAt: deployedAt},
		{ArtifactID: agent.ArtifactUUID.String(), DeploymentID: rows[3].DeploymentID.String(), Kind: "Agent", State: "deployed", DeployedAt: deployedAt},
	}
	require.Len(t, got, len(want), "a kind AMS never deploys to a gateway is left out")
	for i := range want {
		assert.NotEmpty(t, got[i].Etag)
		want[i].Etag = got[i].Etag
	}
	assert.Equal(t, want, got)
	assert.NotEqual(t, got[0].Etag, got[1].Etag, "the etag follows the deployment")
}

// A gateway that restarts applies what its startup sync lists without acking it, and skips as
// stale a later event no newer than what it holds. A row still waiting for an ack is listed a
// millisecond before its transition, so the row's own event, replayed or re-sent, is newer than the
// gateway's copy and gets the ack that settles the row.
func TestListGatewayDeploymentsListsARowAwaitingAnAckBeforeItsEvent(t *testing.T) {
	performedAt := time.Date(2026, 10, 28, 12, 0, 0, 123000000, time.UTC)
	row := func(status, desired models.DeploymentStatus, reason string) models.GatewaySyncDeployment {
		return models.GatewaySyncDeployment{
			GatewayArtifactRef: syncRef(models.KindLLMProvider, "provider"), DeploymentID: uuid.New(),
			Status: status, StatusDesired: desired, StatusReason: reason, PerformedAt: performedAt,
		}
	}
	rows := []models.GatewaySyncDeployment{
		row(models.DeploymentStatusDeploying, models.DeploymentStatusDeployed, ""),
		row(models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed, ""),
		row(models.DeploymentStatusFailed, models.DeploymentStatusDeployed, models.DeploymentReasonTimeout),
		row(models.DeploymentStatusFailed, models.DeploymentStatusUndeployed, models.DeploymentReasonTimeout),
		row(models.DeploymentStatusDeployed, models.DeploymentStatusDeployed, ""),
		row(models.DeploymentStatusUndeployed, models.DeploymentStatusUndeployed, ""),
		row(models.DeploymentStatusFailed, models.DeploymentStatusDeployed, "DEPLOYMENT_FAILED"),
	}
	deployments := &repomocks.DeploymentRepositoryMock{
		ListForGatewaySyncFunc: func(context.Context, string, string) ([]models.GatewaySyncDeployment, error) { return rows, nil },
	}

	got, err := newGatewaySyncService(deployments, nil, nil).ListGatewayDeployments(context.Background(), "ou-1", "gw-1")
	require.NoError(t, err)
	require.Len(t, got, len(rows))

	beforeTheEvent := performedAt.Add(-time.Millisecond)
	for i, want := range []time.Time{beforeTheEvent, beforeTheEvent, beforeTheEvent, beforeTheEvent, performedAt, performedAt, performedAt} {
		assert.Equal(t, want, got[i].DeployedAt, "row %d (%s, desired %s, %q)", i, rows[i].Status, rows[i].StatusDesired, rows[i].StatusReason)
	}
}

func TestListGatewayDeploymentsRepoError(t *testing.T) {
	repoErr := errors.New("db down")
	deployments := &repomocks.DeploymentRepositoryMock{
		ListForGatewaySyncFunc: func(context.Context, string, string) ([]models.GatewaySyncDeployment, error) { return nil, repoErr },
	}
	_, err := newGatewaySyncService(deployments, nil, nil).ListGatewayDeployments(context.Background(), "ou-1", "gw-1")
	assert.ErrorIs(t, err, repoErr)
}

func encryptedRef(t *testing.T, key []byte, plaintext string) string {
	t.Helper()
	ciphertext, err := utils.EncryptBytes([]byte(plaintext), key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

// Each kind's secrets are resolved where its single pull resolves them.
func TestGetGatewayDeploymentFilesResolvesSecretsPerKind(t *testing.T) {
	key := make([]byte, 32)
	ref := encryptedRef(t, key, "s3cret")
	provider, proxy := syncRef(models.KindLLMProvider, "provider"), syncRef(models.KindLLMProxy, "my-proxy")
	mcp, agent := syncRef(models.KindMCPProxy, "mcp"), syncRef(models.KindAgent, "agent")
	contents := []models.GatewaySyncContent{
		{GatewayArtifactRef: provider, DeploymentID: uuid.New(), Content: []byte("kind: LlmProvider\nspec:\n  upstream:\n    auth:\n      secretRef: " + ref + "\n")},
		{GatewayArtifactRef: proxy, DeploymentID: uuid.New(), Content: []byte("kind: LlmProxy\nspec:\n  provider:\n    auth:\n      secretRef: " + ref + "\n")},
		{GatewayArtifactRef: mcp, DeploymentID: uuid.New(), Content: []byte("kind: Mcp\nspec:\n  policies:\n    - params:\n        secretRef: " + ref + "\n")},
		{GatewayArtifactRef: agent, DeploymentID: uuid.New(), Content: []byte("kind: Agent\nspec: {}\n")},
	}
	requested := []string{
		contents[0].DeploymentID.String(), contents[1].DeploymentID.String(),
		contents[2].DeploymentID.String(), contents[3].DeploymentID.String(), "not-a-uuid",
	}
	deployments := &repomocks.DeploymentRepositoryMock{
		GetContentsForGatewaySyncFunc: func(_ context.Context, gatewayID, orgUUID string, ids []uuid.UUID) ([]models.GatewaySyncContent, error) {
			assert.Equal(t, "gw-1", gatewayID)
			assert.Equal(t, "ou-1", orgUUID)
			assert.Len(t, ids, 4, "an id that is not a UUID names no deployment")
			return contents, nil
		},
	}

	files, err := newGatewaySyncService(deployments, nil, key).GetGatewayDeploymentFiles(context.Background(), "ou-1", "gw-1", requested)
	require.NoError(t, err)
	require.Len(t, files, 4)

	byDeployment := map[string]utils.DeploymentArchiveEntry{}
	for _, f := range files {
		byDeployment[f.DeploymentID] = f
	}
	assert.Equal(t, "llm-provider-"+provider.ArtifactUUID.String()+".yaml", byDeployment[requested[0]].FileName)
	assert.Equal(t, "llm-proxy-my-proxy.yaml", byDeployment[requested[1]].FileName)
	assert.Equal(t, "mcp-proxy-"+mcp.ArtifactUUID.String()+".yaml", byDeployment[requested[2]].FileName)
	assert.Equal(t, "agent-"+agent.ArtifactUUID.String()+".yaml", byDeployment[requested[3]].FileName)

	secretAt := func(content []byte, path ...string) any {
		var doc map[string]any
		require.NoError(t, yaml.Unmarshal(content, &doc))
		var node any = doc
		for _, p := range path {
			switch n := node.(type) {
			case map[string]any:
				node = n[p]
			case []any:
				node = n[0].(map[string]any)[p]
			}
		}
		return node
	}
	assert.Equal(t, "s3cret", secretAt(byDeployment[requested[0]].Content, "spec", "upstream", "auth", "value"))
	assert.Equal(t, "s3cret", secretAt(byDeployment[requested[1]].Content, "spec", "provider", "auth", "value"))
	assert.Equal(t, "s3cret", secretAt(byDeployment[requested[2]].Content, "spec", "policies", "params", "value"))
	assert.Equal(t, "kind: Agent\nspec: {}\n", string(byDeployment[requested[3]].Content))
}

// One deployment whose secrets can't be resolved must not fail the gateway's whole sync.
func TestGetGatewayDeploymentFilesLeavesOutWhatCannotBeRendered(t *testing.T) {
	key := make([]byte, 32)
	good, bad := syncRef(models.KindAgent, "good"), syncRef(models.KindLLMProvider, "bad")
	contents := []models.GatewaySyncContent{
		{GatewayArtifactRef: good, DeploymentID: uuid.New(), Content: []byte("kind: Agent\n")},
		{GatewayArtifactRef: bad, DeploymentID: uuid.New(), Content: []byte("spec:\n  upstream:\n    auth:\n      secretRef: not-base64!\n")},
	}
	deployments := &repomocks.DeploymentRepositoryMock{
		GetContentsForGatewaySyncFunc: func(context.Context, string, string, []uuid.UUID) ([]models.GatewaySyncContent, error) {
			return contents, nil
		},
	}

	files, err := newGatewaySyncService(deployments, nil, key).GetGatewayDeploymentFiles(context.Background(), "ou-1", "gw-1",
		[]string{contents[0].DeploymentID.String(), contents[1].DeploymentID.String()})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, contents[0].DeploymentID.String(), files[0].DeploymentID)
}

func TestGetGatewayDeploymentFilesRepoError(t *testing.T) {
	repoErr := errors.New("db down")
	deployments := &repomocks.DeploymentRepositoryMock{
		GetContentsForGatewaySyncFunc: func(context.Context, string, string, []uuid.UUID) ([]models.GatewaySyncContent, error) {
			return nil, repoErr
		},
	}
	_, err := newGatewaySyncService(deployments, nil, nil).GetGatewayDeploymentFiles(context.Background(), "ou-1", "gw-1", []string{uuid.NewString()})
	assert.ErrorIs(t, err, repoErr)
}

func TestCheckArtifactsExistAnswersEveryRequestedID(t *testing.T) {
	artifacts := &repomocks.ArtifactRepositoryMock{
		ExistingGatewayArtifactIDsFunc: func(_ context.Context, orgUUID string, ids []string) ([]string, error) {
			assert.Equal(t, "ou-1", orgUUID)
			assert.Equal(t, []string{"a", "my-proxy", "gone"}, ids)
			return []string{"my-proxy", "a"}, nil
		},
	}

	got, err := newGatewaySyncService(nil, artifacts, nil).CheckArtifactsExist(context.Background(), "ou-1", []string{"a", "my-proxy", "gone"})
	require.NoError(t, err)
	assert.Equal(t, []models.ArtifactExistence{
		{ArtifactID: "a", Exists: true},
		{ArtifactID: "my-proxy", Exists: true},
		{ArtifactID: "gone", Exists: false},
	}, got)
}

// The gateway skips every orphan delete when this call fails, so a repository error must surface.
func TestCheckArtifactsExistRepoError(t *testing.T) {
	repoErr := errors.New("db down")
	artifacts := &repomocks.ArtifactRepositoryMock{
		ExistingGatewayArtifactIDsFunc: func(context.Context, string, []string) ([]string, error) { return nil, repoErr },
	}
	_, err := newGatewaySyncService(nil, artifacts, nil).CheckArtifactsExist(context.Background(), "ou-1", []string{"a"})
	assert.ErrorIs(t, err, repoErr)
}
