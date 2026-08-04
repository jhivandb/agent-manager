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
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/secretmanagersvc"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// mcpCredentialTestIDs are the fixed identifiers every test in this file shares, so
// assertions can name them without threading values through helpers.
var (
	testCredConfigUUID   = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	testCredEnvUUID      = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	testCredMappingUUID  = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	testCredArtifactUUID = uuid.MustParse("44444444-4444-4444-4444-444444444444")
)

func testCredConfig() *models.AgentConfiguration {
	return &models.AgentConfiguration{
		UUID:        testCredConfigUUID,
		Name:        "booking",
		ProjectName: "default",
		AgentID:     "help-desk",
		OUID:        "org-1",
		TypeID:      models.AgentConfigTypeIDMCP,
	}
}

func testCredMapping() *models.EnvAgentMCPMapping {
	return &models.EnvAgentMCPMapping{
		ConfigUUID:      testCredConfigUUID,
		EnvironmentUUID: testCredEnvUUID,
		MCPProxyUUID:    testCredMappingUUID,
		ArtifactUUID:    testCredArtifactUUID,
	}
}

// testCredMappingWithProxy is testCredMapping with its source proxy preloaded, the way a
// GORM-loaded mapping normally arrives — needed whenever resolveMCPMappingAPIID must resolve
// the gateway artifact straight from mapping.MCPProxy instead of falling back to mcpProxyRepo.
func testCredMappingWithProxy(proxy *models.MCPProxy) *models.EnvAgentMCPMapping {
	mapping := testCredMapping()
	mapping.MCPProxy = proxy
	return mapping
}

// TestTryCleanupMCPMappingCredentials_RevokeFailureIsReturned proves a failed revoke
// surfaces as an error instead of being swallowed — the precondition for the gate in
// ReconcileMCPCredentialsForProxy being able to retry.
func TestTryCleanupMCPMappingCredentials_RevokeFailureIsReturned(t *testing.T) {
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: "booking-proxy-secret",
				}}, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return nil, errors.New("gateway unreachable")
				},
			},
		},
		secretClient: &clientmocks.SecretManagementClientMock{
			DeleteSecretFunc: func(context.Context, secretmanagersvc.SecretLocation, string) error {
				return nil
			},
		},
	}

	err := svc.tryCleanupMCPMappingCredentials(context.Background(), testCredConfig(), testCredMapping(), "dev", "org-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway unreachable")
}

// TestReconcileMCPMappingCredentials_DisableKeepsSecretRefWhenCleanupFails proves the gate
// cannot converge past an unfinished teardown.
func TestReconcileMCPMappingCredentials_DisableKeepsSecretRefWhenCleanupFails(t *testing.T) {
	secretRefWrites := 0
	secretMock := &clientmocks.SecretManagementClientMock{
		DeleteSecretFunc: func(context.Context, secretmanagersvc.SecretLocation, string) error {
			return nil
		},
	}
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: "booking-proxy-secret",
				}}, nil
			},
			UpdateAPIKeySecretReferenceFunc: func(context.Context, uuid.UUID, uuid.UUID, string) (int64, error) {
				secretRefWrites++
				return 1, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return nil, errors.New("gateway unreachable")
				},
			},
		},
		secretClient: secretMock,
	}

	changed, err := svc.reconcileMCPMappingCredentials(context.Background(), testCredConfig(), testCredMapping(),
		identityOnlyProxy(), "dev", "org-1", nil, false, "")

	require.Error(t, err)
	assert.False(t, changed)
	assert.Equal(t, 0, secretRefWrites, "secret_reference must survive a failed teardown so the next reconcile retries")
	// Pins this test to the disable branch: if mcpProxyAPIKeySecurityEnabled ever regressed to
	// true for identityOnlyProxy(), the enable branch would error before ever touching the
	// secret client, and this assertion would catch that the failure came from the wrong path.
	assert.Len(t, secretMock.DeleteSecretCalls(), 1)
}

// identityOnlyProxy is a source proxy whose single endpoint covers testCredEnvUUID with
// OAuth/AgentID security — i.e. api-key security is off for that environment.
func identityOnlyProxy() *models.MCPProxy {
	enabled := true
	return &models.MCPProxy{
		UUID: testCredMappingUUID,
		Endpoints: []models.MCPProxyEndpoint{{
			Handle: "booking",
			Configuration: models.MCPEndpointConfig{
				Security: &models.SecurityConfig{
					Enabled:  &enabled,
					Identity: &models.IdentitySecurity{Enabled: &enabled},
				},
			},
			Environments: []models.MCPProxyEndpointEnvironment{{
				EnvironmentUUID: testCredEnvUUID,
				ArtifactUUID:    testCredArtifactUUID,
			}},
		}},
	}
}

// apiKeyEnabledProxy is a source proxy whose single endpoint covers testCredEnvUUID with
// api-key security enabled — the mirror of identityOnlyProxy.
func apiKeyEnabledProxy() *models.MCPProxy {
	return &models.MCPProxy{
		UUID: testCredMappingUUID,
		Endpoints: []models.MCPProxyEndpoint{{
			Handle: "booking",
			Configuration: models.MCPEndpointConfig{
				Security: &models.SecurityConfig{
					Enabled: boolPtr(true),
					APIKey:  &models.APIKeySecurity{Enabled: boolPtr(true)},
				},
			},
			Environments: []models.MCPProxyEndpointEnvironment{{
				EnvironmentUUID: testCredEnvUUID,
				ArtifactUUID:    testCredArtifactUUID,
			}},
		}},
	}
}

// TestReconcileMCPMappingCredentials_DisableKeepsSecretRefWhenAPIIDUnresolved proves an
// unresolved gateway artifact counts as a teardown failure. Without this, a transient
// mcpProxyRepo failure would let revokeMCPMappingAPIKey take its local-only delete branch,
// report success, and let secret_reference clear while the key stays live on the gateway.
func TestReconcileMCPMappingCredentials_DisableKeepsSecretRefWhenAPIIDUnresolved(t *testing.T) {
	secretRefWrites := 0
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: "booking-proxy-secret",
				}}, nil
			},
			UpdateAPIKeySecretReferenceFunc: func(context.Context, uuid.UUID, uuid.UUID, string) (int64, error) {
				secretRefWrites++
				return 1, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return nil, nil
				},
			},
		},
		mcpProxyRepo: &repomocks.MCPProxyRepositoryMock{
			GetByUUIDFunc: func(context.Context, string, string) (*models.MCPProxy, error) {
				return nil, errors.New("proxy lookup failed")
			},
		},
		secretClient: &clientmocks.SecretManagementClientMock{
			DeleteSecretFunc: func(context.Context, secretmanagersvc.SecretLocation, string) error {
				return nil
			},
		},
	}

	// testCredMapping has no preloaded MCPProxy, so resolveMCPMappingAPIID must fall back to
	// mcpProxyRepo.GetByUUID, which fails here — apiID stays uuid.Nil.
	changed, err := svc.reconcileMCPMappingCredentials(context.Background(), testCredConfig(), testCredMapping(),
		identityOnlyProxy(), "dev", "org-1", nil, false, "")

	require.Error(t, err)
	assert.False(t, changed)
	assert.Equal(t, 0, secretRefWrites, "an unresolved gateway artifact is not proof of teardown")
}

// TestReconcileMCPMappingCredentials_DisableKeepsSecretRefWhenSecretDeleteFails covers the
// other half of the teardown invariant: a successful revoke does not excuse a failed secret
// delete from also blocking the secret_reference clear.
func TestReconcileMCPMappingCredentials_DisableKeepsSecretRefWhenSecretDeleteFails(t *testing.T) {
	secretRefWrites := 0
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: "booking-proxy-secret",
				}}, nil
			},
			UpdateAPIKeySecretReferenceFunc: func(context.Context, uuid.UUID, uuid.UUID, string) (int64, error) {
				secretRefWrites++
				return 1, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return nil, nil
				},
			},
		},
		secretClient: &clientmocks.SecretManagementClientMock{
			DeleteSecretFunc: func(context.Context, secretmanagersvc.SecretLocation, string) error {
				return errors.New("kv unreachable")
			},
		},
	}

	changed, err := svc.reconcileMCPMappingCredentials(context.Background(), testCredConfig(),
		testCredMappingWithProxy(identityOnlyProxy()), identityOnlyProxy(), "dev", "org-1", nil, false, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "kv unreachable")
	assert.False(t, changed)
	assert.Equal(t, 0, secretRefWrites, "secret_reference must survive a failed secret delete so the next reconcile retries")
}

// TestReconcileMCPMappingCredentials_EnableAlreadyProvisionedReportsUnchanged proves that
// reconciling a mapping that already has a live key does not report a change — otherwise
// every PUT that leaves credentials untouched would still roll the agent's pods.
func TestReconcileMCPMappingCredentials_EnableAlreadyProvisionedReportsUnchanged(t *testing.T) {
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: "booking-proxy-secret",
				}}, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				GetByArtifactAndNameFunc: func(string, string) (*models.StoredAPIKey, error) {
					return &models.StoredAPIKey{Name: mcpMappingAPIKeyName(testCredConfig(), "dev")}, nil
				},
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return nil, nil
				},
			},
		},
	}

	changed, err := svc.reconcileMCPMappingCredentials(context.Background(), testCredConfig(),
		testCredMappingWithProxy(apiKeyEnabledProxy()), apiKeyEnabledProxy(), "dev", "org-1", nil, false, "")

	require.NoError(t, err)
	assert.False(t, changed, "an already-provisioned key must not report a change and roll pods")
}

// TestReconcileMCPMappingCredentials_DisableNothingProvisionedReportsUnchanged proves that
// disabling api-key security on a mapping with no provisioned key does not report a change —
// this is the case that would otherwise roll every bound agent's pods on an unrelated proxy
// edit (e.g. renaming a tool) once security was already off for that environment.
func TestReconcileMCPMappingCredentials_DisableNothingProvisionedReportsUnchanged(t *testing.T) {
	secretRefWrites := 0
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return nil, nil
			},
			UpdateAPIKeySecretReferenceFunc: func(context.Context, uuid.UUID, uuid.UUID, string) (int64, error) {
				secretRefWrites++
				return 1, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return nil, nil
				},
			},
		},
		secretClient: &clientmocks.SecretManagementClientMock{
			DeleteSecretFunc: func(context.Context, secretmanagersvc.SecretLocation, string) error {
				return nil
			},
		},
	}

	changed, err := svc.reconcileMCPMappingCredentials(context.Background(), testCredConfig(),
		testCredMappingWithProxy(identityOnlyProxy()), identityOnlyProxy(), "dev", "org-1", nil, false, "")

	require.NoError(t, err)
	assert.False(t, changed, "clearing an already-empty secret_reference is a no-op and must not roll pods")
	assert.Equal(t, 1, secretRefWrites, "the clear write still happens even though it reports unchanged")
}
