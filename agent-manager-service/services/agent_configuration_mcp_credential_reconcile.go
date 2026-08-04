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

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
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

// MCPMappingCredentialReconciler converges agent configurations' MCP api-key credentials with
// their source proxy's security mode. Declared narrowly so MCPProxyService can hold it without
// depending on the whole AgentConfigurationService (the constructor edge runs the other way).
type MCPMappingCredentialReconciler interface {
	// ReconcileMCPCredentialsForProxy reconciles every agent configuration bound to proxyUUID.
	// Best-effort by contract: it aggregates per-config failures and returns them for logging.
	ReconcileMCPCredentialsForProxy(ctx context.Context, ouID string, proxyUUID uuid.UUID) error

	// ReconcileMCPCredentialsForAgentEnv reconciles one agent's MCP configurations in one
	// environment, and re-asserts their injected env vars. Called from deploy and promote.
	ReconcileMCPCredentialsForAgentEnv(ctx context.Context, ouID, projectName, agentName, envName string) error
}

// mcpCredentialConfigPageSize bounds the per-agent configuration listing. Truncation is logged
// rather than paged over: it is unreachable in practice and the direction is benign.
const mcpCredentialConfigPageSize = 1000

// mcpCredentialContext is the per-config, per-agent context the reconcile unit needs. Built
// once per configuration, matching how updateMCPConfig assembles the same values.
type mcpCredentialContext struct {
	envTemplates    []EnvConfigTemplate
	isExternalAgent bool
	firstEnvName    string
}

func (s *agentConfigurationService) buildMCPCredentialContext(
	ctx context.Context, ouID, projectName, agentName string, config *models.AgentConfiguration,
) (*mcpCredentialContext, error) {
	agentComp, err := s.ocClient.GetComponent(ctx, ouID, projectName, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to determine agent type for %s: %w", agentName, err)
	}
	isExternalAgent := agentComp.Provisioning.Type == string(utils.ExternalAgent)

	firstEnvName := ""
	if !isExternalAgent {
		if pipeline, pipelineErr := s.ocClient.GetProjectDeploymentPipeline(ctx, ouID, projectName); pipelineErr == nil && pipeline != nil {
			firstEnvName = client.FindFirstEnvironment(pipeline.PromotionPaths)
		}
	}

	existingVarNames, err := s.loadExistingVarNames(ctx, config.UUID)
	if err != nil {
		return nil, err
	}
	envTemplates, err := s.buildMCPMappingEnvironmentVariables(config.Name, varNamesToOverrides(existingVarNames))
	if err != nil {
		return nil, fmt.Errorf("failed to build env var templates for config %s: %w", config.Name, err)
	}

	return &mcpCredentialContext{
		envTemplates:    envTemplates,
		isExternalAgent: isExternalAgent,
		firstEnvName:    firstEnvName,
	}, nil
}

func (s *agentConfigurationService) ReconcileMCPCredentialsForProxy(ctx context.Context, ouID string, proxyUUID uuid.UUID) error {
	if s.mcpProxyRepo == nil || s.envMCPMappingRepo == nil {
		return nil
	}
	// Reload rather than trusting a caller's snapshot: two overlapping proxy updates would
	// otherwise let a stale view revoke a key the newer update just minted.
	proxy, err := s.mcpProxyRepo.GetByUUID(ctx, proxyUUID.String(), ouID)
	if err != nil {
		return fmt.Errorf("failed to load MCP proxy %s for credential reconcile: %w", proxyUUID, err)
	}

	mappings, err := s.envMCPMappingRepo.ListByMCPProxy(ctx, proxyUUID)
	if err != nil {
		return fmt.Errorf("failed to list agent bindings for MCP proxy %s: %w", proxyUUID, err)
	}
	if len(mappings) == 0 {
		return nil
	}

	envs, err := s.infraResourceManager.ListOrgEnvironments(ctx, ouID)
	if err != nil {
		return fmt.Errorf("failed to list environments for credential reconcile: %w", err)
	}
	uuidToEnvName := make(map[string]string, len(envs))
	for _, e := range envs {
		uuidToEnvName[e.UUID] = e.Name
	}

	byConfig := make(map[uuid.UUID][]models.EnvAgentMCPMapping, len(mappings))
	order := make([]uuid.UUID, 0, len(mappings))
	for _, mapping := range mappings {
		if _, seen := byConfig[mapping.ConfigUUID]; !seen {
			order = append(order, mapping.ConfigUUID)
		}
		byConfig[mapping.ConfigUUID] = append(byConfig[mapping.ConfigUUID], mapping)
	}

	var errs []error
	for _, configUUID := range order {
		if err := s.reconcileConfigMCPCredentials(ctx, ouID, configUUID, byConfig[configUUID], proxy, uuidToEnvName, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reconcileConfigMCPCredentials reconciles every given mapping of one configuration. Shared by
// both entry points so per-config context is assembled exactly once either way.
func (s *agentConfigurationService) reconcileConfigMCPCredentials(
	ctx context.Context, ouID string, configUUID uuid.UUID, mappings []models.EnvAgentMCPMapping,
	proxy *models.MCPProxy, uuidToEnvName map[string]string, assertEnvVars bool,
) error {
	config, err := s.agentConfigRepo.GetByUUID(ctx, configUUID, ouID)
	if err != nil {
		return fmt.Errorf("failed to load agent configuration %s: %w", configUUID, err)
	}
	credCtx, err := s.buildMCPCredentialContext(ctx, ouID, config.ProjectName, config.AgentID, config)
	if err != nil {
		// Logging is the only consumer of these errors, so name the config the proxy fan-out failed on.
		return fmt.Errorf("config %s of agent %s: %w", config.Name, config.AgentID, err)
	}

	var errs []error
	for i := range mappings {
		mapping := &mappings[i]
		envName := uuidToEnvName[mapping.EnvironmentUUID.String()]
		if envName == "" {
			continue // environment since deleted
		}
		sourceProxy := proxy
		if sourceProxy == nil {
			sourceProxy = mapping.MCPProxy
		}
		if sourceProxy == nil {
			continue
		}
		if _, err := s.reconcileOneMCPMappingCredential(ctx, config, mapping, sourceProxy, envName, ouID,
			credCtx.envTemplates, credCtx.isExternalAgent, credCtx.firstEnvName, assertEnvVars); err != nil {
			errs = append(errs, fmt.Errorf("config %s environment %s: %w", config.Name, envName, err))
		}
	}
	return errors.Join(errs...)
}

func (s *agentConfigurationService) ReconcileMCPCredentialsForAgentEnv(ctx context.Context, ouID, projectName, agentName, envName string) error {
	if s.agentConfigRepo == nil || s.envMCPMappingRepo == nil {
		return nil
	}
	env, err := s.ocClient.GetEnvironment(ctx, ouID, envName)
	if err != nil {
		return fmt.Errorf("failed to get environment %q: %w", envName, err)
	}
	envUUID, err := uuid.Parse(env.UUID)
	if err != nil {
		return fmt.Errorf("invalid environment UUID %q: %w", env.UUID, err)
	}
	// Keyed by the canonical form: lookups below use mapping.EnvironmentUUID.String().
	uuidToEnvName := map[string]string{envUUID.String(): envName}

	configs, err := s.agentConfigRepo.ListByAgent(ctx, ouID, projectName, agentName, mcpCredentialConfigPageSize, 0)
	if err != nil {
		return fmt.Errorf("failed to list agent configurations: %w", err)
	}
	if len(configs) == mcpCredentialConfigPageSize {
		s.logger.Warn("agent filled the configuration page; MCP credential reconcile covered only the first page",
			"agent", agentName, "environment", envName, "pageSize", mcpCredentialConfigPageSize)
	}

	var errs []error
	for i := range configs {
		config := &configs[i]
		if config.TypeID != models.AgentConfigTypeIDMCP {
			continue
		}
		mappings, err := s.envMCPMappingRepo.ListByConfig(ctx, config.UUID)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to list MCP mappings for config %s: %w", config.Name, err))
			continue
		}
		forEnv := make([]models.EnvAgentMCPMapping, 0, 1)
		for _, mapping := range mappings {
			if mapping.EnvironmentUUID == envUUID {
				forEnv = append(forEnv, mapping)
			}
		}
		if len(forEnv) == 0 {
			continue
		}
		// proxy nil: each mapping's own preloaded MCPProxy is the source of truth here.
		if err := s.reconcileConfigMCPCredentials(ctx, ouID, config.UUID, forEnv, nil, uuidToEnvName, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
