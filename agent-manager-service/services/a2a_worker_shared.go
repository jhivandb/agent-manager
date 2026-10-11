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
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// a2aLastErrorMaxRunes keeps a stored last_error readable in the console.
const a2aLastErrorMaxRunes = 1024

// sanitizeA2ALastError makes msg storable in a Postgres TEXT column and bounded in length.
func sanitizeA2ALastError(msg string) string {
	msg = strings.ReplaceAll(strings.ToValidUTF8(msg, "�"), "\x00", "")
	if runes := []rune(msg); len(runes) > a2aLastErrorMaxRunes {
		msg = string(runes[:a2aLastErrorMaxRunes-1]) + "…"
	}
	return msg
}

// resolveIngressGateway picks the environment's INGRESS gateway, where inbound A2A traffic belongs; egress gateways host outbound LLM/MCP artifacts.
func resolveIngressGateway(gatewayRepo repositories.GatewayRepository, ouID string, envUUID uuid.UUID) (*models.Gateway, error) {
	envID := envUUID.String()
	gateways, err := gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
		OrganizationID:      ouID,
		FunctionalityTypeIn: models.IngressGatewayRoles,
		EnvironmentID:       &envID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateways for environment %s: %w", envID, err)
	}
	for _, gw := range gateways {
		if gw != nil {
			return gw, nil
		}
	}
	// The bootstrap job registers the environment's ingress gateway, so absence is a timing condition: retry.
	return nil, fmt.Errorf("no ingress gateway is mapped to environment %s yet", envID)
}
