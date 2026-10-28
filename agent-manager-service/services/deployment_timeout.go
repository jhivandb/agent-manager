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
	"log/slog"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/config"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// DeploymentTimeoutService fails deployments and undeployments the gateway never acknowledged.
// A gateway that is offline, or that dropped the event, leaves the row DEPLOYING or UNDEPLOYING;
// after the timeout it becomes FAILED with reason DEPLOYMENT_TIMEOUT. A late ack still settles it.
type DeploymentTimeoutService struct {
	deploymentRepo repositories.DeploymentRepository
	cfg            config.DeploymentTimeoutConfig
	logger         *slog.Logger
	now            func() time.Time
	// done is closed once the loop Start launched has stopped.
	done chan struct{}
}

// NewDeploymentTimeoutService creates a DeploymentTimeoutService.
func NewDeploymentTimeoutService(
	deploymentRepo repositories.DeploymentRepository, cfg config.DeploymentTimeoutConfig, logger *slog.Logger,
) *DeploymentTimeoutService {
	return &DeploymentTimeoutService{
		deploymentRepo: deploymentRepo,
		cfg:            cfg,
		logger:         logger,
		now:            time.Now,
		done:           make(chan struct{}),
	}
}

// Start sweeps at once, then once per interval, in the background until ctx is cancelled. Every
// replica may run it: each sweep is one guarded update, so a row is failed once.
func (s *DeploymentTimeoutService) Start(ctx context.Context) {
	s.logger.Info("Deployment timeout sweeper started", "interval", s.cfg.Interval, "timeout", s.cfg.Timeout)
	go func() {
		defer close(s.done)
		s.RunOnce(ctx)
		ticker := time.NewTicker(s.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.logger.Info("Deployment timeout sweeper stopped")
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
}

// RunOnce fails every row still waiting for an ack after the timeout.
func (s *DeploymentTimeoutService) RunOnce(ctx context.Context) {
	failed, err := s.deploymentRepo.FailStaleTransitional(ctx, s.now().Add(-s.cfg.Timeout))
	if err != nil {
		s.logger.Error("Failed to time out deployments awaiting a gateway ack", "error", err)
		return
	}
	if failed > 0 {
		s.logger.Warn("Timed out deployments the gateway never acknowledged", "count", failed, "timeout", s.cfg.Timeout)
	}
}
