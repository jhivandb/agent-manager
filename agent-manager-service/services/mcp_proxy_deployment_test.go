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
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// findMCPPolicy returns a pointer to the first policy matching name, or nil.
func findMCPPolicy(policies []models.MCPPolicy, name string) *models.MCPPolicy {
	for i := range policies {
		if policies[i].Name == name {
			return &policies[i]
		}
	}
	return nil
}

// TestGenerateMCPProxyDeploymentYAML_Basic verifies a minimal proxy renders the
// expected apiVersion/kind/metadata and applies the default context and spec version.
func TestGenerateMCPProxyDeploymentYAML_Basic(t *testing.T) {
	service := &MCPProxyService{}

	proxy := &models.MCPProxy{
		Handle: "test-mcp-proxy",
		Configuration: models.MCPProxyConfig{
			Name:    "Test MCP Proxy",
			Version: "1.0.0",
			Upstream: models.UpstreamConfig{
				Main: &models.UpstreamEndpoint{URL: "https://mcp.example.com"},
			},
		},
	}

	yamlStr, err := service.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if yamlStr == "" {
		t.Fatal("expected non-empty YAML")
	}

	var deployment MCPProxyDeploymentYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &deployment); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	if deployment.ApiVersion != apiVersionMCPProxy {
		t.Errorf("expected ApiVersion %s, got %s", apiVersionMCPProxy, deployment.ApiVersion)
	}
	if deployment.Kind != kindMCPProxy {
		t.Errorf("expected Kind %s, got %s", kindMCPProxy, deployment.Kind)
	}
	if deployment.Metadata.Name != proxy.Handle {
		t.Errorf("expected metadata.name %s, got %s", proxy.Handle, deployment.Metadata.Name)
	}
	if deployment.Spec.DisplayName != proxy.Configuration.Name {
		t.Errorf("expected displayName %s, got %s", proxy.Configuration.Name, deployment.Spec.DisplayName)
	}
	if deployment.Spec.Version != proxy.Configuration.Version {
		t.Errorf("expected version %s, got %s", proxy.Configuration.Version, deployment.Spec.Version)
	}
	if deployment.Spec.Context != "/" {
		t.Errorf("expected default context '/', got %q", deployment.Spec.Context)
	}
	if deployment.Spec.SpecVersion != mcpProtocolVersion {
		t.Errorf("expected default specVersion %s, got %s", mcpProtocolVersion, deployment.Spec.SpecVersion)
	}
	if deployment.Spec.Upstream.URL != "https://mcp.example.com" {
		t.Errorf("expected upstream url https://mcp.example.com, got %s", deployment.Spec.Upstream.URL)
	}
	if len(deployment.Spec.Policies) != 0 {
		t.Errorf("expected no policies by default, got %d", len(deployment.Spec.Policies))
	}
}

// TestGenerateMCPProxyDeploymentYAML_WithContextAndVhost verifies an explicit
// context, vhost and spec version flow through to the spec.
func TestGenerateMCPProxyDeploymentYAML_WithContextAndVhost(t *testing.T) {
	service := &MCPProxyService{}

	proxy := &models.MCPProxy{
		Handle: "ctx-proxy",
		Configuration: models.MCPProxyConfig{
			Name:        "Ctx Proxy",
			Version:     "v2",
			Context:     strPtr("/custom-context"),
			Vhost:       strPtr("mcp.example.com"),
			SpecVersion: "2024-11-05",
			Upstream: models.UpstreamConfig{
				Main: &models.UpstreamEndpoint{URL: "https://mcp.example.com"},
			},
		},
	}

	yamlStr, err := service.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var deployment MCPProxyDeploymentYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &deployment); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	if deployment.Spec.Context != "/custom-context" {
		t.Errorf("expected context /custom-context, got %q", deployment.Spec.Context)
	}
	if deployment.Spec.Vhost == nil || *deployment.Spec.Vhost != "mcp.example.com" {
		t.Errorf("expected vhost mcp.example.com, got %v", deployment.Spec.Vhost)
	}
	if deployment.Spec.SpecVersion != "2024-11-05" {
		t.Errorf("expected specVersion 2024-11-05, got %s", deployment.Spec.SpecVersion)
	}
}

// TestGenerateMCPProxyDeploymentYAML_StripsMCPSuffix verifies the trailing
// "/mcp" path segment is stripped from the upstream URL during deployment.
func TestGenerateMCPProxyDeploymentYAML_StripsMCPSuffix(t *testing.T) {
	service := &MCPProxyService{}

	proxy := &models.MCPProxy{
		Handle: "strip-proxy",
		Configuration: models.MCPProxyConfig{
			Name:    "Strip Proxy",
			Version: "v1",
			Upstream: models.UpstreamConfig{
				Main: &models.UpstreamEndpoint{URL: "https://host.example.com/godzilla/server/v1.0/mcp"},
			},
		},
	}

	yamlStr, err := service.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var deployment MCPProxyDeploymentYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &deployment); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	want := "https://host.example.com/godzilla/server/v1.0"
	if deployment.Spec.Upstream.URL != want {
		t.Errorf("expected upstream url %q (\"/mcp\" stripped), got %q", want, deployment.Spec.Upstream.URL)
	}
}

// TestGenerateMCPProxyDeploymentYAML_WithSecurityAPIKey verifies API key security
// is converted into an api-key-auth policy (and not surfaced as a security field).
func TestGenerateMCPProxyDeploymentYAML_WithSecurityAPIKey(t *testing.T) {
	service := &MCPProxyService{}

	proxy := &models.MCPProxy{
		Handle: "secure-proxy",
		Configuration: models.MCPProxyConfig{
			Name:    "Secure Proxy",
			Version: "v1",
			Upstream: models.UpstreamConfig{
				Main: &models.UpstreamEndpoint{URL: "https://mcp.example.com"},
			},
			Security: &models.SecurityConfig{
				Enabled: boolPtr(true),
				APIKey: &models.APIKeySecurity{
					Enabled: boolPtr(true),
					Key:     "X-API-Key",
					In:      "header",
				},
			},
		},
	}

	yamlStr, err := service.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var deployment MCPProxyDeploymentYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &deployment); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	policy := findMCPPolicy(deployment.Spec.Policies, apiKeyAuthPolicyName)
	if policy == nil {
		t.Fatalf("expected an %s policy, got policies: %+v", apiKeyAuthPolicyName, deployment.Spec.Policies)
	}
	if policy.Version != apiKeyAuthPolicyVersion {
		t.Errorf("expected policy version %s, got %s", apiKeyAuthPolicyVersion, policy.Version)
	}
	if policy.Params["key"] != "X-API-Key" {
		t.Errorf("expected param key X-API-Key, got %v", policy.Params["key"])
	}
	if policy.Params["in"] != "header" {
		t.Errorf("expected param in header, got %v", policy.Params["in"])
	}
}

// TestGenerateMCPProxyDeploymentYAML_SecurityValidation covers the validation
// branches of API key security configuration.
func TestGenerateMCPProxyDeploymentYAML_SecurityValidation(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		in         string
		expectErr  bool
		errKeyword string
	}{
		{name: "empty key", key: "", in: "header", expectErr: true, errKeyword: "key is required"},
		{name: "invalid in", key: "X-API-Key", in: "body", expectErr: true, errKeyword: "in must be 'header' or 'query'"},
		{name: "valid header", key: "X-API-Key", in: "header", expectErr: false},
		{name: "valid query", key: "apiKey", in: "query", expectErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &MCPProxyService{}
			proxy := &models.MCPProxy{
				Handle: "sec-proxy",
				Configuration: models.MCPProxyConfig{
					Name:    "Sec Proxy",
					Version: "v1",
					Upstream: models.UpstreamConfig{
						Main: &models.UpstreamEndpoint{URL: "https://mcp.example.com"},
					},
					Security: &models.SecurityConfig{
						Enabled: boolPtr(true),
						APIKey: &models.APIKeySecurity{
							Enabled: boolPtr(true),
							Key:     tt.key,
							In:      tt.in,
						},
					},
				},
			}

			_, err := service.generateMCPProxyDeploymentYAML(proxy)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if tt.errKeyword != "" && !strings.Contains(err.Error(), tt.errKeyword) {
					t.Errorf("expected error to contain %q, got %q", tt.errKeyword, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

// TestGenerateMCPProxyDeploymentYAML_WithBackendAuth verifies upstream auth is
// converted into a backend-authentication (set-headers) policy.
func TestGenerateMCPProxyDeploymentYAML_WithBackendAuth(t *testing.T) {
	service := &MCPProxyService{}

	proxy := &models.MCPProxy{
		Handle: "auth-proxy",
		Configuration: models.MCPProxyConfig{
			Name:    "Auth Proxy",
			Version: "v1",
			Upstream: models.UpstreamConfig{
				Main: &models.UpstreamEndpoint{
					URL: "https://mcp.example.com",
					Auth: &models.UpstreamAuth{
						Type:   strPtr("api-key"),
						Header: strPtr("Authorization"),
						Value:  strPtr("Bearer token123"),
					},
				},
			},
		},
	}

	yamlStr, err := service.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var deployment MCPProxyDeploymentYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &deployment); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	policy := findMCPPolicy(deployment.Spec.Policies, mcpBackendAuthPolicyName)
	if policy == nil {
		t.Fatalf("expected a %s policy, got policies: %+v", mcpBackendAuthPolicyName, deployment.Spec.Policies)
	}
	if policy.Version != mcpBackendAuthPolicyVersion {
		t.Errorf("expected policy version %s, got %s", mcpBackendAuthPolicyVersion, policy.Version)
	}

	request, ok := policy.Params["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected request param map, got %T", policy.Params["request"])
	}
	headers, ok := request["headers"].([]interface{})
	if !ok || len(headers) != 1 {
		t.Fatalf("expected one header entry, got %v", request["headers"])
	}
	header, ok := headers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected header map, got %T", headers[0])
	}
	if header["name"] != "Authorization" {
		t.Errorf("expected header name Authorization, got %v", header["name"])
	}
	if header["value"] != "Bearer token123" {
		t.Errorf("expected header value 'Bearer token123', got %v", header["value"])
	}
}

// TestGenerateMCPProxyDeploymentYAML_PrefersArtifactIdentity verifies the backing
// artifact's handle/name/version take precedence over configuration values.
func TestGenerateMCPProxyDeploymentYAML_PrefersArtifactIdentity(t *testing.T) {
	service := &MCPProxyService{}

	proxy := &models.MCPProxy{
		UUID:   uuid.New(),
		Handle: "config-handle",
		Configuration: models.MCPProxyConfig{
			Name:    "Config Name",
			Version: "config-version",
			Upstream: models.UpstreamConfig{
				Main: &models.UpstreamEndpoint{URL: "https://mcp.example.com"},
			},
		},
		Artifact: &models.Artifact{
			Handle:  "artifact-handle",
			Name:    "Artifact Name",
			Version: "artifact-version",
		},
	}

	yamlStr, err := service.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var deployment MCPProxyDeploymentYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &deployment); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	if deployment.Metadata.Name != "artifact-handle" {
		t.Errorf("expected metadata.name artifact-handle, got %s", deployment.Metadata.Name)
	}
	if deployment.Spec.DisplayName != "Artifact Name" {
		t.Errorf("expected displayName Artifact Name, got %s", deployment.Spec.DisplayName)
	}
	if deployment.Spec.Version != "artifact-version" {
		t.Errorf("expected version artifact-version, got %s", deployment.Spec.Version)
	}
}

// TestGenerateMCPProxyDeploymentYAML_PolicyVersionNormalized verifies user-defined
// policy versions are normalized to their major version.
func TestGenerateMCPProxyDeploymentYAML_PolicyVersionNormalized(t *testing.T) {
	service := &MCPProxyService{}

	proxy := &models.MCPProxy{
		Handle: "policy-proxy",
		Configuration: models.MCPProxyConfig{
			Name:    "Policy Proxy",
			Version: "v1",
			Upstream: models.UpstreamConfig{
				Main: &models.UpstreamEndpoint{URL: "https://mcp.example.com"},
			},
			Policies: []models.MCPPolicy{
				{Name: "content-length-guardrail", Version: "1.2.3"},
			},
		},
	}

	yamlStr, err := service.generateMCPProxyDeploymentYAML(proxy)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var deployment MCPProxyDeploymentYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &deployment); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	policy := findMCPPolicy(deployment.Spec.Policies, "content-length-guardrail")
	if policy == nil {
		t.Fatalf("expected content-length-guardrail policy, got %+v", deployment.Spec.Policies)
	}
	if policy.Version != "v1" {
		t.Errorf("expected normalized version v1, got %s", policy.Version)
	}
}

// TestGenerateMCPProxyDeploymentYAML_MissingUpstreamURL verifies an empty upstream
// URL is rejected.
func TestGenerateMCPProxyDeploymentYAML_MissingUpstreamURL(t *testing.T) {
	tests := []struct {
		name  string
		proxy *models.MCPProxy
	}{
		{
			name: "nil upstream main",
			proxy: &models.MCPProxy{
				Handle:        "p",
				Configuration: models.MCPProxyConfig{Name: "p", Version: "v1"},
			},
		},
		{
			name: "empty upstream url",
			proxy: &models.MCPProxy{
				Handle: "p",
				Configuration: models.MCPProxyConfig{
					Name:     "p",
					Version:  "v1",
					Upstream: models.UpstreamConfig{Main: &models.UpstreamEndpoint{URL: "   "}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &MCPProxyService{}
			_, err := service.generateMCPProxyDeploymentYAML(tt.proxy)
			if err == nil {
				t.Fatal("expected an error for missing upstream URL, got nil")
			}
			if !strings.Contains(err.Error(), "upstream URL is required") {
				t.Errorf("expected 'upstream URL is required', got %q", err.Error())
			}
		})
	}
}

// TestAppendMCPBackendAuthPolicy covers the upstream-auth-to-policy conversion
// branches in isolation.
func TestAppendMCPBackendAuthPolicy(t *testing.T) {
	t.Run("nil auth leaves policies unchanged", func(t *testing.T) {
		out := appendMCPBackendAuthPolicy(nil, nil)
		if len(out) != 0 {
			t.Errorf("expected no policies, got %d", len(out))
		}
	})

	t.Run("header without value or secretRef is skipped", func(t *testing.T) {
		out := appendMCPBackendAuthPolicy(nil, &models.UpstreamAuth{Header: strPtr("Authorization")})
		if len(out) != 0 {
			t.Errorf("expected no policies when neither value nor secretRef is set, got %d", len(out))
		}
	})

	t.Run("secretRef takes precedence over value", func(t *testing.T) {
		out := appendMCPBackendAuthPolicy(nil, &models.UpstreamAuth{
			Header:    strPtr("Authorization"),
			Value:     strPtr("plain"),
			SecretRef: strPtr("encrypted-ref"),
		})
		if len(out) != 1 {
			t.Fatalf("expected one policy, got %d", len(out))
		}
		header := firstHeaderParam(t, out[0])
		if _, hasValue := header["value"]; hasValue {
			t.Error("expected no plaintext value when secretRef is present")
		}
		if header["secretRef"] != "encrypted-ref" {
			t.Errorf("expected secretRef encrypted-ref, got %v", header["secretRef"])
		}
	})

	t.Run("appends to existing policies", func(t *testing.T) {
		existing := []models.MCPPolicy{{Name: "log-message", Version: "v1"}}
		out := appendMCPBackendAuthPolicy(existing, &models.UpstreamAuth{
			Header: strPtr("Authorization"),
			Value:  strPtr("Bearer x"),
		})
		if len(out) != 2 {
			t.Fatalf("expected two policies, got %d", len(out))
		}
		if out[1].Name != mcpBackendAuthPolicyName {
			t.Errorf("expected appended policy %s, got %s", mcpBackendAuthPolicyName, out[1].Name)
		}
	})
}

func firstHeaderParam(t *testing.T, policy models.MCPPolicy) map[string]interface{} {
	t.Helper()
	request, ok := policy.Params["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected request param map, got %T", policy.Params["request"])
	}
	headers, ok := request["headers"].([]interface{})
	if !ok || len(headers) == 0 {
		t.Fatalf("expected header entries, got %v", request["headers"])
	}
	header, ok := headers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected header map, got %T", headers[0])
	}
	return header
}

// TestAppendMCPAPIKeyAuthPolicy covers the security-to-policy conversion branches.
func TestAppendMCPAPIKeyAuthPolicy(t *testing.T) {
	t.Run("nil security leaves policies unchanged", func(t *testing.T) {
		out, err := appendMCPAPIKeyAuthPolicy(nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 0 {
			t.Errorf("expected no policies, got %d", len(out))
		}
	})

	t.Run("disabled api key leaves policies unchanged", func(t *testing.T) {
		out, err := appendMCPAPIKeyAuthPolicy(nil, &models.SecurityConfig{
			Enabled: boolPtr(true),
			APIKey:  &models.APIKeySecurity{Enabled: boolPtr(false), Key: "X-API-Key", In: "header"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 0 {
			t.Errorf("expected no policies for disabled api key, got %d", len(out))
		}
	})

	t.Run("empty key returns error", func(t *testing.T) {
		_, err := appendMCPAPIKeyAuthPolicy(nil, &models.SecurityConfig{
			Enabled: boolPtr(true),
			APIKey:  &models.APIKeySecurity{Enabled: boolPtr(true), Key: "  ", In: "header"},
		})
		if err == nil || !strings.Contains(err.Error(), "key is required") {
			t.Errorf("expected 'key is required' error, got %v", err)
		}
	})

	t.Run("invalid in returns error", func(t *testing.T) {
		_, err := appendMCPAPIKeyAuthPolicy(nil, &models.SecurityConfig{
			Enabled: boolPtr(true),
			APIKey:  &models.APIKeySecurity{Enabled: boolPtr(true), Key: "X-API-Key", In: "cookie"},
		})
		if err == nil || !strings.Contains(err.Error(), "in must be 'header' or 'query'") {
			t.Errorf("expected 'in must be header or query' error, got %v", err)
		}
	})

	t.Run("in is lowercased", func(t *testing.T) {
		out, err := appendMCPAPIKeyAuthPolicy(nil, &models.SecurityConfig{
			Enabled: boolPtr(true),
			APIKey:  &models.APIKeySecurity{Enabled: boolPtr(true), Key: "X-API-Key", In: "HEADER"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 || out[0].Params["in"] != "header" {
			t.Errorf("expected lowercased in=header, got %+v", out)
		}
	})
}

// TestMergeMCPPoliciesForDeployment verifies the merge-vs-override behaviour for
// duplicate policy identities.
func TestMergeMCPPoliciesForDeployment(t *testing.T) {
	t.Run("set-headers policies merge their header lists", func(t *testing.T) {
		policies := []models.MCPPolicy{
			{
				Name:    mcpSetHeadersPolicyName,
				Version: "v1",
				Params: map[string]interface{}{
					"request": map[string]interface{}{
						"headers": []interface{}{map[string]interface{}{"name": "A"}},
					},
				},
			},
			{
				Name:    mcpSetHeadersPolicyName,
				Version: "v1",
				Params: map[string]interface{}{
					"request": map[string]interface{}{
						"headers": []interface{}{map[string]interface{}{"name": "B"}},
					},
				},
			},
		}

		out := mergeMCPPoliciesForDeployment(policies)
		if len(out) != 1 {
			t.Fatalf("expected merged into one policy, got %d", len(out))
		}
		request := out[0].Params["request"].(map[string]interface{})
		headers := request["headers"].([]interface{})
		if len(headers) != 2 {
			t.Errorf("expected merged header list of length 2, got %d", len(headers))
		}
	})

	t.Run("non-mergeable duplicate is overridden by the later policy", func(t *testing.T) {
		policies := []models.MCPPolicy{
			{Name: "custom-policy", Version: "v1", Params: map[string]interface{}{"max": 10}},
			{Name: "custom-policy", Version: "v1", Params: map[string]interface{}{"max": 99}},
		}

		out := mergeMCPPoliciesForDeployment(policies)
		if len(out) != 1 {
			t.Fatalf("expected one policy, got %d", len(out))
		}
		if out[0].Params["max"] != 99 {
			t.Errorf("expected later policy to override (max=99), got %v", out[0].Params["max"])
		}
	})

	t.Run("policies with distinct identities are kept separate", func(t *testing.T) {
		policies := []models.MCPPolicy{
			{Name: mcpSetHeadersPolicyName, Version: "v1"},
			{Name: mcpLogMessagePolicyName, Version: "v1"},
		}
		out := mergeMCPPoliciesForDeployment(policies)
		if len(out) != 2 {
			t.Errorf("expected two distinct policies, got %d", len(out))
		}
	})
}

// TestNormalizeMCPUpstreamURLForDeployment covers URL normalization edge cases.
func TestNormalizeMCPUpstreamURLForDeployment(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "strips trailing /mcp", in: "https://host.example.com/api/mcp", want: "https://host.example.com/api"},
		{name: "strips trailing /mcp with trailing slash", in: "https://host.example.com/api/mcp/", want: "https://host.example.com/api"},
		{name: "leaves non-mcp path untouched", in: "https://host.example.com/api/v1", want: "https://host.example.com/api/v1"},
		{name: "root mcp collapses to root", in: "https://host.example.com/mcp", want: "https://host.example.com/"},
		{name: "non-url passthrough", in: "not a url", want: "not a url"},
		{name: "trims whitespace", in: "  https://host.example.com/x  ", want: "https://host.example.com/x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeMCPUpstreamURLForDeployment(tt.in); got != tt.want {
				t.Errorf("normalizeMCPUpstreamURLForDeployment(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestAppendMCPIdentityAuthPolicies covers the Agent Identity policy emission:
// mcp-auth (with the pinned issuer and a sorted scope union) plus per-tool mcp-authz.
func TestAppendMCPIdentityAuthPolicies(t *testing.T) {
	enabled := true
	identity := &models.SecurityConfig{Enabled: &enabled, Identity: &models.IdentitySecurity{Enabled: &enabled}}
	bindings := []models.MCPToolScopeBinding{
		{Tool: "list_repos", Scopes: []string{"repo:read.all"}},
		{Tool: "create_issue", Scopes: []string{"repo:write.all", "repo:read.all"}},
	}

	t.Run("disabled security emits nothing", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, nil, bindings)
		assert.Empty(t, out)
	})

	t.Run("api-key security emits nothing", func(t *testing.T) {
		sec := &models.SecurityConfig{Enabled: &enabled, APIKey: &models.APIKeySecurity{Enabled: &enabled}}
		assert.Empty(t, appendMCPIdentityAuthPolicies(nil, sec, bindings))
	})

	t.Run("identity mode emits mcp-auth with sorted scope union and per-tool mcp-authz", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, identity, bindings)
		assert.Len(t, out, 2)
		assert.Equal(t, "mcp-auth", out[0].Name)
		assert.Equal(t, "v1", out[0].Version)
		assert.Equal(t, []interface{}{"ThunderKeyManager"}, out[0].Params["issuers"])
		assert.Equal(t, []string{"repo:read.all", "repo:write.all"}, out[0].Params["requiredScopes"])
		assert.Equal(t, "mcp-authz", out[1].Name)
		assert.Equal(t, "v1", out[1].Version)
		tools := out[1].Params["tools"].([]map[string]interface{})
		assert.Len(t, tools, 2)
		assert.Equal(t, "list_repos", tools[0]["name"])
		assert.Equal(t, "create_issue", tools[1]["name"])
	})

	t.Run("no bindings: mcp-auth only, no requiredScopes, no mcp-authz", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, identity, nil)
		assert.Len(t, out, 1)
		assert.Equal(t, "mcp-auth", out[0].Name)
		_, hasScopes := out[0].Params["requiredScopes"]
		assert.False(t, hasScopes)
	})

	t.Run("binding with empty scopes list is skipped", func(t *testing.T) {
		out := appendMCPIdentityAuthPolicies(nil, identity, []models.MCPToolScopeBinding{{Tool: "x", Scopes: nil}})
		assert.Len(t, out, 1) // auth only — unbound tools stay authenticated-only
	})

	t.Run("preserves and appends to existing policies", func(t *testing.T) {
		existing := []models.MCPPolicy{{Name: "log-message", Version: "v1"}}
		out := appendMCPIdentityAuthPolicies(existing, identity, bindings)
		assert.Len(t, out, 3)
		assert.Equal(t, "log-message", out[0].Name)
		assert.Equal(t, "mcp-auth", out[1].Name)
		assert.Equal(t, "mcp-authz", out[2].Name)
	})
}

func TestMCPProxyEnvArtifactHandleUsesFullCompactedEnvironmentUUID(t *testing.T) {
	const proxyHandle = "shared-mcp-proxy"
	envID1 := "12345678-0000-0000-0000-000000000001"
	envID2 := "12345678-ffff-ffff-ffff-ffffffffffff"

	handle1 := mcpProxyEnvArtifactHandle(proxyHandle, envID1)
	handle2 := mcpProxyEnvArtifactHandle(proxyHandle, envID2)

	if handle1 == handle2 {
		t.Fatalf("expected distinct handles for environments with the same first 8 UUID characters")
	}
	if !strings.HasSuffix(handle1, "-12345678000000000000000000000001") {
		t.Fatalf("expected full compacted UUID suffix, got %q", handle1)
	}
	if !strings.HasSuffix(handle2, "-12345678ffffffffffffffffffffffff") {
		t.Fatalf("expected full compacted UUID suffix, got %q", handle2)
	}
}
