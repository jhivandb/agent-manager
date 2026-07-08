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
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// scopeNameRe is the exact scope-name grammar (spec §5.1). Shared with the
// gateway policy layer and the console's inline validation.
var scopeNameRe = regexp.MustCompile(`^[A-Za-z0-9:._\-]{1,256}$`)

// scopeBindingPageSize bounds each page when scanning MCP proxies for scope
// bindings (the proxy repository list is paginated).
const scopeBindingPageSize = 100

// ScopeService manages the org-global scope catalog. It stores only scope names;
// grants live in each environment's Thunder instance.
type ScopeService interface {
	// List returns the org's scope catalog, name-ascending.
	List(ctx context.Context, orgName string) ([]models.Scope, error)
	// Create validates the name and rejects duplicates (utils.ErrConflict).
	Create(ctx context.Context, orgName, name, description string) (*models.Scope, error)
	// Update changes only the description; missing scope -> utils.ErrScopeNotFound.
	Update(ctx context.Context, orgName, name, description string) (*models.Scope, error)
	// Delete removes a scope; blocked with utils.ErrConflict while any MCP proxy
	// tool binding references it, utils.ErrScopeNotFound when absent.
	Delete(ctx context.Context, orgName, name string) error
	// BindingCounts returns, per scope name, how many MCP proxy environment tool
	// bindings across the org reference it.
	BindingCounts(ctx context.Context, orgName string) (map[string]int, error)
}

type scopeService struct {
	repo      repositories.ScopeRepository
	proxyRepo repositories.MCPProxyRepository
}

// NewScopeService creates a new scope catalog service.
func NewScopeService(repo repositories.ScopeRepository, proxyRepo repositories.MCPProxyRepository) ScopeService {
	return &scopeService{repo: repo, proxyRepo: proxyRepo}
}

func (s *scopeService) List(ctx context.Context, orgName string) ([]models.Scope, error) {
	return s.repo.List(ctx, orgName)
}

func (s *scopeService) Create(ctx context.Context, orgName, name, description string) (*models.Scope, error) {
	if !scopeNameRe.MatchString(name) {
		return nil, fmt.Errorf("%w: scope name must match ^[A-Za-z0-9:._\\-]{1,256}$", utils.ErrInvalidInput)
	}
	if _, err := s.repo.GetByName(ctx, orgName, name); err == nil {
		return nil, fmt.Errorf("%w: scope %q already exists", utils.ErrConflict, name)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to check scope existence: %w", err)
	}
	scope := &models.Scope{OrgName: orgName, Name: name, Description: description}
	if err := s.repo.Create(ctx, scope); err != nil {
		// A concurrent create can win the race between the GetByName check above
		// and this insert; the unique constraint (uq_scopes_org_name) then fires.
		// Map it to a conflict so the API returns 409, matching the check path.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("%w: scope %q already exists", utils.ErrConflict, name)
		}
		return nil, err
	}
	return scope, nil
}

func (s *scopeService) Update(ctx context.Context, orgName, name, description string) (*models.Scope, error) {
	if _, err := s.repo.GetByName(ctx, orgName, name); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrScopeNotFound
		}
		return nil, fmt.Errorf("failed to load scope: %w", err)
	}
	if err := s.repo.Update(ctx, &models.Scope{OrgName: orgName, Name: name, Description: description}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, utils.ErrScopeNotFound
		}
		return nil, err
	}
	updated, err := s.repo.GetByName(ctx, orgName, name)
	if err != nil {
		return nil, fmt.Errorf("failed to reload scope: %w", err)
	}
	return updated, nil
}

func (s *scopeService) Delete(ctx context.Context, orgName, name string) error {
	if _, err := s.repo.GetByName(ctx, orgName, name); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return utils.ErrScopeNotFound
		}
		return fmt.Errorf("failed to load scope: %w", err)
	}
	counts, err := s.BindingCounts(ctx, orgName)
	if err != nil {
		return err
	}
	if counts[name] > 0 {
		return fmt.Errorf("%w: scope %q is referenced by %d tool binding(s)", utils.ErrConflict, name, counts[name])
	}
	return s.repo.Delete(ctx, orgName, name)
}

func (s *scopeService) BindingCounts(ctx context.Context, orgName string) (map[string]int, error) {
	counts := make(map[string]int)
	offset := 0
	for {
		proxies, err := s.proxyRepo.List(ctx, orgName, scopeBindingPageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to list MCP proxies for scope binding counts: %w", err)
		}
		for _, proxy := range proxies {
			for _, env := range proxy.Configuration.Environments {
				for _, binding := range env.ToolScopeBindings {
					for _, scopeName := range binding.Scopes {
						counts[scopeName]++
					}
				}
			}
		}
		if len(proxies) < scopeBindingPageSize {
			break
		}
		offset += scopeBindingPageSize
	}
	return counts, nil
}
