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

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// reconcileOneMCPMappingCredential brings one (agent config, environment) mapping's api-key
// credential state in line with its source proxy's security mode for that environment.
//
// The gate matters as much as the work: UpdateReleaseBindingEnvVars and
// RemoveReleaseBindingEnvVars rewrite the binding and stamp restartedAt even when the content
// is unchanged, so reconciling unconditionally would roll every bound agent's pods whenever
// anyone edited an unrelated part of the proxy.
//
// assertEnvVars re-injects the desired env vars even when the credential state did not change.
// The deploy path sets it — deploy rolls the pod anyway, so the write is free there, and it is
// the only thing that repairs a previously failed (best-effort) env var write. The proxy-update
// path must leave it false.
func (s *agentConfigurationService) reconcileOneMCPMappingCredential(
	ctx context.Context,
	config *models.AgentConfiguration,
	mapping *models.EnvAgentMCPMapping,
	proxy *models.MCPProxy,
	envName, ouID string,
	envTemplates []EnvConfigTemplate,
	isExternalAgent bool,
	firstEnvName string,
	assertEnvVars bool,
) (bool, error) {
	envID := mapping.EnvironmentUUID.String()
	if endpoint, _ := resolveMCPEndpointForEnv(proxy, envID); endpoint == nil {
		// The proxy has no endpoint bound to this environment. Tearing the credential down
		// here would collide with the unconfigured-environment teardown the agent-config path
		// owns, so leave it alone.
		return false, nil
	}

	desired := mcpProxyAPIKeySecurityEnabled(proxy, envID)
	current, err := s.mcpMappingCredentialProvisioned(ctx, config, mapping, envName)
	if err != nil {
		return false, err
	}

	if desired == current {
		if assertEnvVars && desired && !isExternalAgent {
			if injectErr := s.injectMCPMappingEnvVars(ctx, config, mapping, proxy, envName, ouID, envTemplates, firstEnvName); injectErr != nil {
				s.logger.Warn("failed to assert MCP mapping env vars", "environment", envName, "err", injectErr)
			}
		}
		return false, nil
	}

	if desired && isExternalAgent {
		// An external agent receives its raw key only in a create/rotate response, so a
		// background mint would produce a credential nobody can retrieve.
		s.logger.Warn("MCP proxy switched to api-key security for an external agent; rotate the config API key to issue a retrievable key",
			"agent", config.AgentID, "config", config.Name, "environment", envName)
		return false, nil
	}

	changed, err := s.reconcileMCPMappingCredentials(ctx, config, mapping, proxy, envName, ouID, envTemplates, isExternalAgent, firstEnvName)
	if err != nil {
		return false, err
	}
	if desired && !isExternalAgent {
		if injectErr := s.injectMCPMappingEnvVars(ctx, config, mapping, proxy, envName, ouID, envTemplates, firstEnvName); injectErr != nil {
			s.logger.Warn("failed to inject MCP mapping env vars after security switch", "environment", envName, "err", injectErr)
		}
	}
	return changed, nil
}

// mcpMappingCredentialProvisioned reports whether an api-key credential is currently
// provisioned for this mapping. Both halves are needed: ensureMCPMappingCredentials treats
// "secret reference stored AND key row present" as provisioned, and states exist where they
// disagree (an external-agent key revocation deletes the key row and leaves the reference).
//
// The key name derives from config.Name, so after a config rename the lookup misses the key
// stored under the old name, this reads as not-provisioned, and one re-mint happens on the
// next reconcile. revokeStaleMCPMappingAPIKeys then clears the orphan.
func (s *agentConfigurationService) mcpMappingCredentialProvisioned(
	ctx context.Context, config *models.AgentConfiguration, mapping *models.EnvAgentMCPMapping, envName string,
) (bool, error) {
	secretRef, err := s.loadSecretRefForConfigEnv(ctx, config.UUID, mapping.EnvironmentUUID)
	if err != nil {
		return false, fmt.Errorf("failed to load MCP SecretReference for %s: %w", envName, err)
	}
	if secretRef == "" {
		return false, nil
	}
	keyExists, err := s.mcpMappingAPIKeyExists(mapping.ArtifactUUID, mcpMappingAPIKeyName(config, envName))
	if err != nil {
		return false, fmt.Errorf("failed to inspect MCP API key for %s: %w", envName, err)
	}
	return keyExists, nil
}
