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

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// Queries behind a gateway's startup sync (GET /deployments, POST /deployments/fetch-batch and
// POST /artifacts/exists on the internal API).

// gatewaySyncArtifactColumns selects a models.GatewayArtifactRef from an artifacts table aliased a.
const gatewaySyncArtifactColumns = "a.uuid AS artifact_uuid, a.handle AS artifact_handle, a.kind AS artifact_kind"

// ListForGatewaySync returns every deployment_status row of the gateway in the org.
func (r *DeploymentRepo) ListForGatewaySync(ctx context.Context, gatewayID, orgUUID string) ([]models.GatewaySyncDeployment, error) {
	var rows []models.GatewaySyncDeployment
	err := r.db.WithContext(ctx).Table("deployment_status s").
		Select(gatewaySyncArtifactColumns+", s.deployment_id, s.status_desired, s.performed_at").
		Joins("INNER JOIN artifacts a ON a.uuid = s.artifact_uuid AND a.ou_id = s.ou_id").
		Where("s.gateway_uuid = ? AND s.ou_id = ?", gatewayID, orgUUID).
		Order("s.performed_at").
		Scan(&rows).Error
	return rows, err
}

// GetContentsForGatewaySync returns the content of those deployments the gateway is meant to hold:
// its current deployment of an artifact, desired DEPLOYED. Other ids are left out.
func (r *DeploymentRepo) GetContentsForGatewaySync(ctx context.Context, gatewayID, orgUUID string, deploymentIDs []uuid.UUID) ([]models.GatewaySyncContent, error) {
	if len(deploymentIDs) == 0 {
		return nil, nil
	}
	var rows []models.GatewaySyncContent
	err := r.db.WithContext(ctx).Table("deployments d").
		Select(gatewaySyncArtifactColumns+", d.deployment_id, d.content").
		Joins("INNER JOIN deployment_status s ON s.deployment_id = d.deployment_id "+
			"AND s.artifact_uuid = d.artifact_uuid AND s.ou_id = d.ou_id AND s.gateway_uuid = d.gateway_uuid").
		Joins("INNER JOIN artifacts a ON a.uuid = d.artifact_uuid AND a.ou_id = d.ou_id").
		Where("d.gateway_uuid = ? AND d.ou_id = ? AND d.deployment_id IN ? AND s.status_desired = ?",
			gatewayID, orgUUID, deploymentIDs, models.DeploymentStatusDeployed).
		Scan(&rows).Error
	return rows, err
}

// ExistingGatewayArtifactIDs returns those of ids that name an artifact of the org the way a
// gateway does: by its UUID, or an LLM proxy by its handle.
func (r *ArtifactRepo) ExistingGatewayArtifactIDs(ctx context.Context, orgUUID string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	uuids := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if parsed, err := uuid.Parse(id); err == nil {
			uuids = append(uuids, parsed)
		}
	}
	var refs []models.GatewayArtifactRef
	query := r.db.WithContext(ctx).Table("artifacts a").
		Select(gatewaySyncArtifactColumns).
		Where("a.ou_id = ?", orgUUID)
	if len(uuids) > 0 {
		query = query.Where("a.uuid IN ? OR (a.kind = ? AND a.handle IN ?)", uuids, models.KindLLMProxy, ids)
	} else {
		query = query.Where("a.kind = ? AND a.handle IN ?", models.KindLLMProxy, ids)
	}
	if err := query.Scan(&refs).Error; err != nil {
		return nil, err
	}

	requested := make(map[string]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	existing := make([]string, 0, len(refs))
	claim := func(id string) {
		if requested[id] {
			existing = append(existing, id)
			delete(requested, id)
		}
	}
	for _, ref := range refs {
		claim(ref.ArtifactUUID.String())
		if ref.ArtifactKind == models.KindLLMProxy {
			claim(ref.ArtifactHandle)
		}
	}
	return existing, nil
}
