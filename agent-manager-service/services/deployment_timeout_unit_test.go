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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/config"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

func TestDeploymentTimeoutRunOnceFailsRowsOlderThanTheTimeout(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	repo := &repomocks.DeploymentRepositoryMock{
		FailStaleTransitionalFunc: func(context.Context, time.Time) (int64, error) { return 2, nil },
	}
	svc := NewDeploymentTimeoutService(repo, config.DeploymentTimeoutConfig{Timeout: 5 * time.Minute, Interval: time.Minute}, testLogger())
	svc.now = func() time.Time { return now }

	svc.RunOnce(context.Background())

	calls := repo.FailStaleTransitionalCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, now.Add(-5*time.Minute), calls[0].OlderThan)
}

func TestDeploymentTimeoutRunOnceSurvivesARepositoryError(t *testing.T) {
	repo := &repomocks.DeploymentRepositoryMock{
		FailStaleTransitionalFunc: func(context.Context, time.Time) (int64, error) { return 0, assert.AnError },
	}
	svc := NewDeploymentTimeoutService(repo, config.DeploymentTimeoutConfig{Timeout: time.Minute, Interval: time.Minute}, testLogger())

	svc.RunOnce(context.Background())

	assert.Len(t, repo.FailStaleTransitionalCalls(), 1)
}

func TestDeploymentTimeoutStartSweepsEachIntervalUntilCancelled(t *testing.T) {
	var sweeps atomic.Int32
	repo := &repomocks.DeploymentRepositoryMock{
		FailStaleTransitionalFunc: func(context.Context, time.Time) (int64, error) {
			sweeps.Add(1)
			return 0, nil
		},
	}
	svc := NewDeploymentTimeoutService(repo, config.DeploymentTimeoutConfig{Timeout: time.Minute, Interval: 5 * time.Millisecond}, testLogger())
	ctx, cancel := context.WithCancel(context.Background())

	svc.Start(ctx)
	require.Eventually(t, func() bool { return sweeps.Load() >= 2 }, time.Second, time.Millisecond)
	cancel()
	<-svc.done
	stopped := sweeps.Load()
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, stopped, sweeps.Load(), "no sweep after cancellation")
}

func TestDeploymentTimeoutStartSweepsImmediately(t *testing.T) {
	var sweeps atomic.Int32
	repo := &repomocks.DeploymentRepositoryMock{
		FailStaleTransitionalFunc: func(context.Context, time.Time) (int64, error) {
			sweeps.Add(1)
			return 0, nil
		},
	}
	svc := NewDeploymentTimeoutService(repo, config.DeploymentTimeoutConfig{Timeout: time.Minute, Interval: time.Hour}, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)

	require.Eventually(t, func() bool { return sweeps.Load() == 1 }, time.Second, time.Millisecond,
		"a restart must not postpone timeouts by an interval")
}
