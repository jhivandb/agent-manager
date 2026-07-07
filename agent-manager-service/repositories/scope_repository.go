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
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// ScopeRepository persists the org-global scope catalog. Grants (which agent
// gets which scope) are not stored here — they live in each environment's
// Thunder instance.
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/scope_repository_mock.go . ScopeRepository:ScopeRepositoryMock
type ScopeRepository interface {
	// List returns every scope in the org's catalog, name-ascending.
	List(ctx context.Context, orgName string) ([]models.Scope, error)
	// GetByName returns gorm.ErrRecordNotFound (wrapped) when absent.
	GetByName(ctx context.Context, orgName, name string) (*models.Scope, error)
	Create(ctx context.Context, scope *models.Scope) error
	Update(ctx context.Context, scope *models.Scope) error
	Delete(ctx context.Context, orgName, name string) error
}

type scopeRepository struct{ db *gorm.DB }

// NewScopeRepository creates a new scope catalog repository.
func NewScopeRepository(db *gorm.DB) ScopeRepository { return &scopeRepository{db: db} }

func (r *scopeRepository) List(ctx context.Context, orgName string) ([]models.Scope, error) {
	var scopes []models.Scope
	if err := r.db.WithContext(ctx).Where("org_name = ?", orgName).
		Order("name ASC").Find(&scopes).Error; err != nil {
		return nil, fmt.Errorf("failed to list scopes: %w", err)
	}
	return scopes, nil
}

func (r *scopeRepository) GetByName(ctx context.Context, orgName, name string) (*models.Scope, error) {
	var scope models.Scope
	if err := r.db.WithContext(ctx).Where("org_name = ? AND name = ?", orgName, name).
		First(&scope).Error; err != nil {
		return nil, err
	}
	return &scope, nil
}

func (r *scopeRepository) Create(ctx context.Context, scope *models.Scope) error {
	if err := r.db.WithContext(ctx).Create(scope).Error; err != nil {
		return fmt.Errorf("failed to create scope: %w", err)
	}
	return nil
}

func (r *scopeRepository) Update(ctx context.Context, scope *models.Scope) error {
	if err := r.db.WithContext(ctx).Model(&models.Scope{}).
		Where("org_name = ? AND name = ?", scope.OrgName, scope.Name).
		Updates(map[string]any{"description": scope.Description, "updated_at": time.Now()}).
		Error; err != nil {
		return fmt.Errorf("failed to update scope: %w", err)
	}
	return nil
}

func (r *scopeRepository) Delete(ctx context.Context, orgName, name string) error {
	res := r.db.WithContext(ctx).Where("org_name = ? AND name = ?", orgName, name).
		Delete(&models.Scope{})
	if res.Error != nil {
		return fmt.Errorf("failed to delete scope: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
