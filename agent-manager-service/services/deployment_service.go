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
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// DeploymentService handles deployment business logic for artifacts to gateways
type DeploymentService struct {
	deploymentRepo repositories.DeploymentRepository
	gatewayRepo    repositories.GatewayRepository
}

// NewDeploymentService creates a new deployment service
func NewDeploymentService(
	deploymentRepo repositories.DeploymentRepository,
	gatewayRepo repositories.GatewayRepository,
) *DeploymentService {
	return &DeploymentService{
		deploymentRepo: deploymentRepo,
		gatewayRepo:    gatewayRepo,
	}
}

// isDeployedOrDeploying reports whether a status row already holds, or is on its way to holding, its deployment.
func isDeployedOrDeploying(status models.DeploymentStatus) bool {
	return status == models.DeploymentStatusDeployed || status == models.DeploymentStatusDeploying
}

// markDeploying updates a loaded deployment to the status row a deploy or restore just wrote.
func markDeploying(d *models.Deployment, performedAt time.Time) {
	status, desired := models.DeploymentStatusDeploying, models.DeploymentStatusDeployed
	d.Status, d.StatusDesired, d.StatusReason, d.UpdatedAt, d.PerformedAt = &status, &desired, nil, &performedAt, &performedAt
}

// markUndeploying updates a loaded deployment to the status row BeginUndeploy just wrote.
func markUndeploying(d *models.Deployment, performedAt time.Time) {
	status, desired := models.DeploymentStatusUndeploying, models.DeploymentStatusUndeployed
	d.Status, d.StatusDesired, d.StatusReason, d.UpdatedAt, d.PerformedAt = &status, &desired, nil, &performedAt, &performedAt
}

// overlayOpenStatuses shows, on each placed deployment, the status of an intent that has not
// handed it to its row yet: a redeploy still retrying is DEPLOYING, one that gave up FAILED, each
// with its reason.
func overlayOpenStatuses(
	ctx context.Context, deploys *reconcile.Core, kind, ouID string, artifactUUID uuid.UUID, deployments ...*models.Deployment,
) error {
	open, err := deploys.OpenStatuses(ctx, kind, ouID, artifactUUID)
	if err != nil {
		return fmt.Errorf("failed to read the deployment status: %w", err)
	}
	for _, d := range deployments {
		st, ok := open[d.GatewayUUID]
		if !ok || !d.IsPlaced() {
			continue
		}
		status, reason := st.Status, st.Reason
		d.Status, d.StatusReason = &status, &reason
	}
	return nil
}
