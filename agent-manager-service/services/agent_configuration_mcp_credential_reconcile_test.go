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
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
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

// TestReconcileOneMCPMappingCredential_NoChangeWritesNothing is the regression test for
// needless pod rollouts: the ReleaseBinding client stamps restartedAt on every write, so a
// converged mapping must produce no writes at all.
func TestReconcileOneMCPMappingCredential_NoChangeWritesNothing(t *testing.T) {
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				// No secret reference: nothing is provisioned, which matches OAuth mode.
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: "",
				}}, nil
			},
			UpdateAPIKeySecretReferenceFunc: func(context.Context, uuid.UUID, uuid.UUID, string) (int64, error) {
				t.Fatal("must not write secret_reference when the mapping is already converged")
				return 0, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				GetByArtifactAndNameFunc: func(string, string) (*models.StoredAPIKey, error) {
					return nil, gorm.ErrRecordNotFound
				},
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					t.Fatal("must not touch the gateway when the mapping is already converged")
					return nil, nil
				},
			},
		},
	}

	changed, err := svc.reconcileOneMCPMappingCredential(context.Background(), testCredConfig(), testCredMapping(),
		identityOnlyProxy(), "dev", "org-1", testCredEnvTemplates(), false, "dev", false)

	require.NoError(t, err)
	assert.False(t, changed)
}

// testCredEnvTemplates is the env var template pair every MCP config carries.
func testCredEnvTemplates() []EnvConfigTemplate {
	return []EnvConfigTemplate{
		{Key: "url", Name: "BOOKING_URL", IsSecret: false},
		{Key: "apikey", Name: "BOOKING_API_KEY", IsSecret: true},
	}
}

// TestReconcileOneMCPMappingCredential_SkipsEnvironmentTheProxyDoesNotCover guards the
// non-goal boundary: endpoint-removed-from-environment is the agent-config path's job.
func TestReconcileOneMCPMappingCredential_SkipsEnvironmentTheProxyDoesNotCover(t *testing.T) {
	svc := &agentConfigurationService{
		logger: slog.Default(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				t.Fatal("must not read state for an environment the proxy does not cover")
				return nil, nil
			},
		},
	}
	proxy := identityOnlyProxy()
	proxy.Endpoints[0].Environments[0].EnvironmentUUID = uuid.MustParse("99999999-9999-9999-9999-999999999999")

	changed, err := svc.reconcileOneMCPMappingCredential(context.Background(), testCredConfig(), testCredMapping(),
		proxy, "dev", "org-1", testCredEnvTemplates(), false, "dev", false)

	require.NoError(t, err)
	assert.False(t, changed)
}

// TestReconcileOneMCPMappingCredential_StoredKeyMissingCountsAsNotProvisioned covers the
// keyExists half of the gate.
func TestReconcileOneMCPMappingCredential_StoredKeyMissingCountsAsNotProvisioned(t *testing.T) {
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
					return nil, gorm.ErrRecordNotFound
				},
			},
		},
	}

	provisioned, err := svc.mcpMappingCredentialProvisioned(context.Background(), testCredConfig(), testCredMapping(), "dev")

	require.NoError(t, err)
	assert.False(t, provisioned, "a stored secret reference without its key row is not provisioned")
}

// TestReconcileOneMCPMappingCredential_ExternalAgentEnableIsSkipped documents that a
// background mint for an external agent would be unretrievable, so it is refused.
func TestReconcileOneMCPMappingCredential_ExternalAgentEnableIsSkipped(t *testing.T) {
	svc := &agentConfigurationService{
		// discardLogger keeps the expected refusal warning out of the test output.
		logger: discardLogger(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: "",
				}}, nil
			},
			UpdateAPIKeySecretReferenceFunc: func(context.Context, uuid.UUID, uuid.UUID, string) (int64, error) {
				t.Fatal("must not provision credentials for an external agent from a background reconcile")
				return 0, nil
			},
		},
	}

	changed, err := svc.reconcileOneMCPMappingCredential(context.Background(), testCredConfig(), testCredMapping(),
		apiKeyEnabledProxy(), "dev", "org-1", testCredEnvTemplates(), true, "dev", false)

	require.NoError(t, err)
	assert.False(t, changed)
}

// TestReconcileOneMCPMappingCredential_DisableRevokesAndDeInjects covers the api-key → OAuth
// transition: every piece of provisioned state is torn down and the pod variable removed.
func TestReconcileOneMCPMappingCredential_DisableRevokesAndDeInjects(t *testing.T) {
	hub := &stubEventHub{}
	var (
		deletedKeys      []string
		clearedSecretRef = "unset"
		removedEnvKeys   []string
	)

	svc := &agentConfigurationService{
		logger: discardLogger(),
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
			UpdateAPIKeySecretReferenceFunc: func(_ context.Context, _, _ uuid.UUID, secretRefName string) (int64, error) {
				clearedSecretRef = secretRefName
				return 1, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			gatewayRepo: &repomocks.GatewayRepositoryMock{
				GetByOrganizationIDFunc: func(context.Context, string) ([]*models.Gateway, error) {
					return []*models.Gateway{{UUID: uuid.New(), Name: "egress"}}, nil
				},
			},
			gatewayService: &GatewayEventsService{hub: hub},
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				GetByArtifactAndNameFunc: func(_ string, name string) (*models.StoredAPIKey, error) {
					return &models.StoredAPIKey{Name: name}, nil
				},
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return []models.StoredAPIKey{{Name: mcpMappingAPIKeyName(testCredConfig(), "dev")}}, nil
				},
				DeleteFunc: func(_ string, name string) error {
					deletedKeys = append(deletedKeys, name)
					return nil
				},
			},
		},
		secretClient: &clientmocks.SecretManagementClientMock{
			DeleteSecretFunc: func(context.Context, secretmanagersvc.SecretLocation, string) error {
				return nil
			},
		},
		ocClient: &clientmocks.OpenChoreoClientMock{
			RemoveReleaseBindingEnvVarsFunc: func(_ context.Context, _, _, _, _ string, envVarKeys []string) error {
				removedEnvKeys = append(removedEnvKeys, envVarKeys...)
				return nil
			},
			RemoveComponentEnvironmentVariablesFunc: func(context.Context, string, string, string, []string) error {
				return nil
			},
		},
	}

	changed, err := svc.reconcileOneMCPMappingCredential(context.Background(), testCredConfig(),
		testCredMappingWithProxy(identityOnlyProxy()), identityOnlyProxy(), "dev", "org-1",
		testCredEnvTemplates(), false, "dev", false)

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{mcpMappingAPIKeyName(testCredConfig(), "dev")}, deletedKeys)
	assert.Equal(t, "", clearedSecretRef, "secret_reference must be cleared after teardown succeeds")
	assert.Equal(t, []string{"BOOKING_API_KEY"}, removedEnvKeys, "only the api-key variable is removed, never the URL")
}

// TestReconcileOneMCPMappingCredential_EnableMintsAndInjects covers the OAuth → api-key
// transition, including that the injected variable references the secret rather than
// carrying a literal value.
func TestReconcileOneMCPMappingCredential_EnableMintsAndInjects(t *testing.T) {
	hub := &stubEventHub{}
	gatewayUUID := uuid.New()
	var (
		upsertedKeyNames []string
		injectedVars     []client.EnvVar
		storedSecretRef  = "unset"
	)
	// The secret reference lives in the same row the reconcile writes, so the mock reads back
	// what was stored — the injection step must see the freshly minted reference.
	currentSecretRef := ""

	svc := &agentConfigurationService{
		logger: discardLogger(),
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(context.Context, uuid.UUID, uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return []models.AgentEnvConfigVariable{{
					ConfigUUID:      testCredConfigUUID,
					EnvironmentUUID: testCredEnvUUID,
					VariableName:    "BOOKING_API_KEY",
					VariableKey:     "apikey",
					SecretReference: currentSecretRef,
				}}, nil
			},
			UpdateAPIKeySecretReferenceFunc: func(_ context.Context, _, _ uuid.UUID, secretRefName string) (int64, error) {
				storedSecretRef = secretRefName
				currentSecretRef = secretRefName
				return 1, nil
			},
		},
		gatewayRepo: &repomocks.GatewayRepositoryMock{
			EnvironmentMappingExistsFunc: func(string, string) (bool, error) {
				return true, nil
			},
			GetByUUIDFunc: func(string) (*models.Gateway, error) {
				return &models.Gateway{UUID: gatewayUUID, Name: "egress", Vhost: "gw.local", RuntimeURL: "http://gw.local"}, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			gatewayRepo: &repomocks.GatewayRepositoryMock{
				GetByOrganizationIDFunc: func(context.Context, string) ([]*models.Gateway, error) {
					return []*models.Gateway{{UUID: gatewayUUID, Name: "egress"}}, nil
				},
			},
			gatewayService: &GatewayEventsService{hub: hub},
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				GetByArtifactAndNameFunc: func(string, string) (*models.StoredAPIKey, error) {
					return nil, gorm.ErrRecordNotFound
				},
				UpsertFunc: func(key *models.StoredAPIKey) error {
					upsertedKeyNames = append(upsertedKeyNames, key.Name)
					return nil
				},
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					return nil, nil // no stale keys to revoke
				},
			},
		},
		mcpProxyService: &MCPProxyService{
			deploymentRepo: &repomocks.DeploymentRepositoryMock{
				GetDeployedGatewaysByProviderFunc: func(uuid.UUID, string) ([]string, error) {
					return []string{gatewayUUID.String()}, nil
				},
			},
		},
		aiApplicationService: &AIApplicationService{
			appRepo: &repomocks.AIApplicationRepositoryMock{
				CreateFunc: func(context.Context, *gorm.DB, *models.AIApplication) (bool, error) {
					return true, nil
				},
			},
			gatewayRepo: &repomocks.GatewayRepositoryMock{
				GetByOrganizationIDFunc: func(context.Context, string) ([]*models.Gateway, error) {
					return []*models.Gateway{{UUID: gatewayUUID, Name: "egress"}}, nil
				},
			},
			gatewayService: &GatewayEventsService{hub: hub},
			logger:         discardLogger(),
		},
		secretClient: &clientmocks.SecretManagementClientMock{
			CreateSecretFunc: func(context.Context, secretmanagersvc.SecretLocation, map[string]string) (string, error) {
				return "booking-proxy-secret", nil
			},
		},
		ocClient: &clientmocks.OpenChoreoClientMock{
			UpdateReleaseBindingEnvVarsFunc: func(_ context.Context, _, _, _, _ string, envVars []client.EnvVar) error {
				injectedVars = append(injectedVars, envVars...)
				return nil
			},
			UpdateComponentEnvVarsFunc: func(context.Context, string, string, string, []client.EnvVar) error {
				return nil
			},
		},
	}

	changed, err := svc.reconcileOneMCPMappingCredential(context.Background(), testCredConfig(),
		testCredMappingWithProxy(apiKeyEnabledProxy()), apiKeyEnabledProxy(), "dev", "org-1",
		testCredEnvTemplates(), false, "dev", false)

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{mcpMappingAPIKeyName(testCredConfig(), "dev")}, upsertedKeyNames)
	assert.NotEqual(t, "", storedSecretRef)
	assert.NotEqual(t, "unset", storedSecretRef)

	assertInjectedAPIKeyReferencesSecret(t, injectedVars)
}

// assertInjectedAPIKeyReferencesSecret pins the shape every api-key injection must have: the
// variable is present, it resolves through the stored secret, and it carries no literal value.
func assertInjectedAPIKeyReferencesSecret(t *testing.T, injectedVars []client.EnvVar) {
	t.Helper()
	var apiKeyVar *client.EnvVar
	for i := range injectedVars {
		if injectedVars[i].Key == "BOOKING_API_KEY" {
			apiKeyVar = &injectedVars[i]
		}
	}
	require.NotNil(t, apiKeyVar, "the api-key variable must be injected")
	require.NotNil(t, apiKeyVar.ValueFrom)
	require.NotNil(t, apiKeyVar.ValueFrom.SecretKeyRef)
	assert.Equal(t, "booking-proxy-secret", apiKeyVar.ValueFrom.SecretKeyRef.Name, "the injected reference must be the one just stored")
	assert.Equal(t, "", apiKeyVar.Value, "the raw key must never be injected as a literal value")
}

// TestReconcileOneMCPMappingCredential_ConvergedAPIKeyModeWritesNothing is the api-key half of
// the no-write gate (TestReconcileOneMCPMappingCredential_NoChangeWritesNothing covers the OAuth
// half). With assertEnvVars off, a converged api-key mapping must not rewrite the ReleaseBinding
// either — that write stamps restartedAt, so every unrelated proxy edit would roll every bound
// agent's pods.
func TestReconcileOneMCPMappingCredential_ConvergedAPIKeyModeWritesNothing(t *testing.T) {
	svc := convergedAPIKeyModeService(t, &clientmocks.OpenChoreoClientMock{
		UpdateReleaseBindingEnvVarsFunc: func(context.Context, string, string, string, string, []client.EnvVar) error {
			t.Fatal("must not rewrite the ReleaseBinding when the mapping is already converged")
			return nil
		},
		UpdateComponentEnvVarsFunc: func(context.Context, string, string, string, []client.EnvVar) error {
			t.Fatal("must not rewrite the Component env vars when the mapping is already converged")
			return nil
		},
	})

	changed, err := svc.reconcileOneMCPMappingCredential(context.Background(), testCredConfig(),
		testCredMappingWithProxy(apiKeyEnabledProxy()), apiKeyEnabledProxy(), "dev", "org-1",
		testCredEnvTemplates(), false, "dev", false)

	require.NoError(t, err)
	assert.False(t, changed)
}

// TestReconcileOneMCPMappingCredential_AssertEnvVarsReInjectsWithoutCredentialWrites pins the
// deploy path's opt-in: a converged mapping re-injects its env vars but still writes no
// credential state and still reports unchanged.
func TestReconcileOneMCPMappingCredential_AssertEnvVarsReInjectsWithoutCredentialWrites(t *testing.T) {
	var injectedVars []client.EnvVar

	svc := convergedAPIKeyModeService(t, &clientmocks.OpenChoreoClientMock{
		UpdateReleaseBindingEnvVarsFunc: func(_ context.Context, _, _, _, _ string, envVars []client.EnvVar) error {
			injectedVars = append(injectedVars, envVars...)
			return nil
		},
		UpdateComponentEnvVarsFunc: func(context.Context, string, string, string, []client.EnvVar) error {
			return nil
		},
	})

	changed, err := svc.reconcileOneMCPMappingCredential(context.Background(), testCredConfig(),
		testCredMappingWithProxy(apiKeyEnabledProxy()), apiKeyEnabledProxy(), "dev", "org-1",
		testCredEnvTemplates(), false, "dev", true)

	require.NoError(t, err)
	assert.False(t, changed, "asserting env vars is not a credential change")
	assertInjectedAPIKeyReferencesSecret(t, injectedVars)
}

// convergedAPIKeyModeService wires a mapping that is already converged in api-key mode: the
// secret reference is stored and its key row exists, so the gate has nothing to do. Every
// credential write is fatal here; only the caller's ocClient decides whether an env var write
// is expected, which is what separates the two converged api-key tests.
func convergedAPIKeyModeService(t *testing.T, ocClient client.OpenChoreoClient) *agentConfigurationService {
	t.Helper()
	gatewayUUID := uuid.New()
	return &agentConfigurationService{
		logger: discardLogger(),
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
				t.Fatal("must not write secret_reference when the mapping is already converged")
				return 0, nil
			},
		},
		gatewayRepo: &repomocks.GatewayRepositoryMock{
			EnvironmentMappingExistsFunc: func(string, string) (bool, error) {
				return true, nil
			},
			GetByUUIDFunc: func(string) (*models.Gateway, error) {
				return &models.Gateway{UUID: gatewayUUID, Name: "egress", Vhost: "gw.local", RuntimeURL: "http://gw.local"}, nil
			},
		},
		apiKeyBroadcaster: &apiKeyBroadcaster{
			apiKeyRepo: &repomocks.APIKeyRepositoryMock{
				GetByArtifactAndNameFunc: func(_ string, name string) (*models.StoredAPIKey, error) {
					return &models.StoredAPIKey{Name: name}, nil
				},
				UpsertFunc: func(*models.StoredAPIKey) error {
					t.Fatal("must not mint a key when the mapping is already converged")
					return nil
				},
				ListByArtifactFunc: func(context.Context, string) ([]models.StoredAPIKey, error) {
					t.Fatal("must not touch the gateway when the mapping is already converged")
					return nil, nil
				},
			},
		},
		mcpProxyService: &MCPProxyService{
			deploymentRepo: &repomocks.DeploymentRepositoryMock{
				GetDeployedGatewaysByProviderFunc: func(uuid.UUID, string) ([]string, error) {
					return []string{gatewayUUID.String()}, nil
				},
			},
		},
		ocClient: ocClient,
	}
}
