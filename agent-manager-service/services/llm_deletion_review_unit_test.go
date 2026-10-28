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
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/eventhub"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

func TestProxyDelete_RejectsWrongProjectBeforeCleanup(t *testing.T) {
	proxy := &models.LLMProxy{UUID: uuid.New(), ProjectUUID: uuid.New()}
	repo := &repomocks.LLMProxyRepositoryMock{GetByIDCtxFunc: func(context.Context, string, string) (*models.LLMProxy, error) { return proxy, nil }}
	svc := NewLLMProxyService(repo, nil, nil)
	err := svc.DeleteInProject(context.Background(), "proxy", "org", uuid.NewString(), (&LLMProxyDeploymentService{}).withTestCore())
	require.ErrorIs(t, err, utils.ErrLLMProxyNotFound)
	assert.Empty(t, repo.DeleteInProjectCalls())
}

func TestProxyDelete_PropagatesLookupErrors(t *testing.T) {
	for _, cause := range []error{errors.New("database unavailable"), context.Canceled, gorm.ErrRecordNotFound} {
		t.Run(cause.Error(), func(t *testing.T) {
			repo := &repomocks.LLMProxyRepositoryMock{GetByIDCtxFunc: func(context.Context, string, string) (*models.LLMProxy, error) { return nil, cause }}
			svc := NewLLMProxyService(repo, nil, nil)
			err := svc.Delete(context.Background(), "proxy", "org", nil)
			if errors.Is(cause, gorm.ErrRecordNotFound) {
				require.ErrorIs(t, err, utils.ErrLLMProxyNotFound)
			} else {
				require.ErrorIs(t, err, cause)
				assert.NotErrorIs(t, err, utils.ErrLLMProxyNotFound)
			}
			assert.Empty(t, repo.DeleteInProjectCalls())
		})
	}
}

func TestDeletionDiscoveryFailurePreventsDelete(t *testing.T) {
	for _, failure := range []string{"tracked", "active"} {
		t.Run(failure, func(t *testing.T) {
			cause := errors.New("lookup failed")
			deployments := &repomocks.DeploymentRepositoryMock{
				GetTrackedGatewaysByProviderCtxFunc: func(context.Context, uuid.UUID, string) ([]string, error) {
					if failure == "tracked" {
						return nil, cause
					}
					return []string{}, nil
				},
			}
			gateways := &repomocks.GatewayRepositoryMock{ListWithFiltersCtxFunc: func(context.Context, repositories.GatewayFilterOptions) ([]*models.Gateway, error) { return nil, cause }}
			proxy := &models.LLMProxy{UUID: uuid.New(), ProjectUUID: uuid.New()}
			proxies := &repomocks.LLMProxyRepositoryMock{GetByIDCtxFunc: func(context.Context, string, string) (*models.LLMProxy, error) { return proxy, nil }}
			svc := NewLLMProxyService(proxies, nil, nil)
			err := svc.Delete(context.Background(), "proxy", "org", (&LLMProxyDeploymentService{deploymentRepo: deployments, gatewayRepo: gateways}).withTestCore())
			require.ErrorIs(t, err, cause)
			assert.Empty(t, proxies.DeleteInProjectCalls())
			provider, deployment := serviceForRollback(createdProvider(), nil)
			deployment.deploymentRepo = deployments
			deployment.gatewayRepo = gateways
			err = provider.Delete(context.Background(), createdProvider().UUID.String(), "org", deployment)
			require.ErrorIs(t, err, cause)
			providerRepo := provider.providerRepo.(*repomocks.LLMProviderRepositoryMock)
			assert.Empty(t, providerRepo.DeleteCtxCalls())
			require.Len(t, providerRepo.ClearDeletingCalls(), 1)
			assert.Empty(t, deployments.GetDeployedGatewaysByProviderCalls(), "no undeploy before discovery succeeds")
		})
	}
}

type deletionContextHub struct {
	eventhub.EventHub
	publish func(context.Context, string, eventhub.Event) error
}

func (h *deletionContextHub) PublishEvent(ctx context.Context, id string, event eventhub.Event) error {
	return h.publish(ctx, id, event)
}

func TestProxyDelete_UsesScopedDeleteAndBoundedPostCommitContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy := &models.LLMProxy{UUID: uuid.New(), ProjectUUID: uuid.New()}
	gateway := uuid.NewString()
	repo := &repomocks.LLMProxyRepositoryMock{
		GetByIDCtxFunc: func(got context.Context, _, _ string) (*models.LLMProxy, error) {
			assert.Equal(t, ctx, got)
			return proxy, nil
		},
		DeleteInProjectFunc: func(got context.Context, id, org, project string) error {
			assert.Equal(t, ctx, got)
			assert.Equal(t, "proxy", id)
			assert.Equal(t, "org", org)
			assert.Equal(t, proxy.ProjectUUID.String(), project)
			cancel() // database committed, then the HTTP caller disconnected
			return nil
		},
	}
	calls := 0
	hub := &deletionContextHub{publish: func(got context.Context, id string, event eventhub.Event) error {
		calls++
		require.NoError(t, got.Err())
		deadline, ok := got.Deadline()
		require.True(t, ok)
		assert.LessOrEqual(t, time.Until(deadline), gatewayDeletionTimeout)
		assert.Equal(t, gateway, id)
		assert.Equal(t, "proxy", event.EntityID)
		assert.Equal(t, eventhub.EventType("llmproxy.deleted"), event.EventType)
		return nil
	}}
	deployments := &repomocks.DeploymentRepositoryMock{GetTrackedGatewaysByProviderCtxFunc: func(got context.Context, id uuid.UUID, org string) ([]string, error) {
		assert.Equal(t, ctx, got)
		assert.Equal(t, proxy.UUID, id)
		return []string{gateway}, nil
	}}
	svc := NewLLMProxyService(repo, nil, nil)
	err := svc.DeleteInProject(ctx, "proxy", "org", proxy.ProjectUUID.String(), (&LLMProxyDeploymentService{deploymentRepo: deployments, gatewayEventsService: NewGatewayEventsService(hub)}).withTestCore())
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}
