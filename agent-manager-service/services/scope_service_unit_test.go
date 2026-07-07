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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

func TestScopeService_Create_ValidatesName(t *testing.T) {
	svc := NewScopeService(&repomocks.ScopeRepositoryMock{}, &repomocks.MCPProxyRepositoryMock{})

	_, err := svc.Create(context.Background(), "org1", "bad name with spaces", "")
	assert.ErrorIs(t, err, utils.ErrInvalidInput)

	_, err = svc.Create(context.Background(), "org1", strings.Repeat("a", 257), "")
	assert.ErrorIs(t, err, utils.ErrInvalidInput)
}

func TestScopeService_Delete_BlockedWhileBound(t *testing.T) {
	scopeRepo := &repomocks.ScopeRepositoryMock{
		GetByNameFunc: func(_ context.Context, orgName, name string) (*models.Scope, error) {
			return &models.Scope{OrgName: orgName, Name: name}, nil
		},
	}
	bound := &models.MCPProxy{Configuration: models.MCPProxyConfig{
		Environments: map[string]models.MCPEnvironmentConfig{
			"3fa85f64-5717-4562-b3fc-2c963f66afa6": {
				ToolScopeBindings: []models.MCPToolScopeBinding{
					{Tool: "list_repos", Scopes: []string{"repo:read.all"}},
				},
			},
		},
	}}
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		ListFunc: func(_ context.Context, _ string, _, offset int) ([]*models.MCPProxy, error) {
			if offset > 0 {
				return []*models.MCPProxy{}, nil
			}
			return []*models.MCPProxy{bound}, nil
		},
	}
	svc := NewScopeService(scopeRepo, proxyRepo)

	err := svc.Delete(context.Background(), "org1", "repo:read.all")
	assert.ErrorIs(t, err, utils.ErrConflict)
}

func TestScopeService_Delete_UnboundSucceeds(t *testing.T) {
	deleted := false
	scopeRepo := &repomocks.ScopeRepositoryMock{
		GetByNameFunc: func(_ context.Context, orgName, name string) (*models.Scope, error) {
			return &models.Scope{OrgName: orgName, Name: name}, nil
		},
		DeleteFunc: func(_ context.Context, _, _ string) error { deleted = true; return nil },
	}
	proxyRepo := &repomocks.MCPProxyRepositoryMock{
		ListFunc: func(_ context.Context, _ string, _, _ int) ([]*models.MCPProxy, error) {
			return []*models.MCPProxy{}, nil
		},
	}
	svc := NewScopeService(scopeRepo, proxyRepo)

	assert.NoError(t, svc.Delete(context.Background(), "org1", "repo:read.all"))
	assert.True(t, deleted)
}

func TestScopeService_Delete_MissingScopeNotFound(t *testing.T) {
	scopeRepo := &repomocks.ScopeRepositoryMock{
		GetByNameFunc: func(_ context.Context, _, _ string) (*models.Scope, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	svc := NewScopeService(scopeRepo, &repomocks.MCPProxyRepositoryMock{})

	err := svc.Delete(context.Background(), "org1", "repo:read.all")
	assert.ErrorIs(t, err, utils.ErrScopeNotFound)
}
