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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An agent's own card, as fetched: it advertises its own address and its own
// (absent) auth, and carries substance the platform must not touch.
func fetchedCard() map[string]any {
	return map[string]any{
		"name":            "Trip Planner",
		"description":     "Plans deterministic multi-day itineraries",
		"version":         "1.0.0",
		"protocolVersion": "1.0",
		"supportedInterfaces": []any{
			map[string]any{
				"protocolBinding": "JSONRPC",
				"protocolVersion": "1.0",
				"url":             "http://trip-planner.dp-default:9099",
				"tenant":          "acme",
			},
		},
		"securitySchemes":  map[string]any{"legacy": map[string]any{"type": "http"}},
		"capabilities":     map[string]any{"streaming": true},
		"skills":           []any{map[string]any{"id": "plan_trip", "name": "Plan a trip"}},
		"x-vendor-feature": map[string]any{"beta": true},
	}
}

func cardInput() GatewayAgentCardInput {
	return GatewayAgentCardInput{PublicBaseURL: "https://agents.example.com/trip-planner"}
}

// The emitted card is a cross-repo contract the gateway does not validate the
// content half of, so a golden file is the only thing that makes a drift
// visible in a diff rather than as clients failing to authenticate.
func assertGolden(t *testing.T, name string, got map[string]any) {
	t.Helper()
	encoded, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	encoded = append(encoded, '\n')

	goldenPath := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(goldenPath, encoded, 0o600))
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(encoded))
}

func TestBuildGatewayAgentCardAPIKeyGolden(t *testing.T) {
	in := cardInput()
	in.EnableAPIKeySecurity = true
	got, err := buildGatewayAgentCard(fetchedCard(), in)
	require.NoError(t, err)
	assertGolden(t, "a2a_card_apikey.json", got)
}

func TestBuildGatewayAgentCardJWTGolden(t *testing.T) {
	in := cardInput()
	in.EnableOAuthSecurity = true
	got, err := buildGatewayAgentCard(fetchedCard(), in)
	require.NoError(t, err)
	assertGolden(t, "a2a_card_jwt.json", got)
}

// An unauthenticated agent behind an unauthenticated gateway should say so,
// not repeat a claim it no longer owns.
func TestBuildGatewayAgentCardNoAuthGolden(t *testing.T) {
	got, err := buildGatewayAgentCard(fetchedCard(), cardInput())
	require.NoError(t, err)
	assertGolden(t, "a2a_card_noauth.json", got)

	assert.Empty(t, got["securitySchemes"])
	_, hasRequirements := got["securityRequirements"]
	assert.False(t, hasRequirements, "no requirements when nothing is enforced")
}

// Spelled out against a2aproject/A2A v1.0.1 specification/a2a.proto, because
// nothing between here and the client validates these names: the gateway does
// not inspect card content, so a wrong key is stored, served, and surfaces only
// as clients failing to authenticate.
//
// The two that are easy to get wrong: APIKeySecurityScheme.location is NOT `in`
// (proto:523 — `in` is the OpenAPI spelling A2A dropped), and StringList's field
// is `list`, NOT `values` (proto:490).
func TestBuildGatewayAgentCardSecuritySchemesMatchTheA2AModel(t *testing.T) {
	in := cardInput()
	in.EnableAPIKeySecurity = true
	got, err := buildGatewayAgentCard(fetchedCard(), in)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"apiKey": map[string]any{
			"apiKeySecurityScheme": map[string]any{
				"location": "header",
				"name":     "X-API-Key",
			},
		},
	}, got["securitySchemes"])
	assert.Equal(t, []any{
		map[string]any{"schemes": map[string]any{"apiKey": map[string]any{"list": []any{}}}},
	}, got["securityRequirements"])

	in = cardInput()
	in.EnableOAuthSecurity = true
	got, err = buildGatewayAgentCard(fetchedCard(), in)
	require.NoError(t, err)

	// httpAuthSecurityScheme{scheme: Bearer} rather than an OIDC scheme: bearer
	// is always true, where openIdConnectUrl would have to be synthesised from
	// the configured issuers and would be wrong for some IdPs.
	assert.Equal(t, map[string]any{
		"bearer": map[string]any{
			"httpAuthSecurityScheme": map[string]any{"scheme": "Bearer"},
		},
	}, got["securitySchemes"])
	assert.Equal(t, []any{
		map[string]any{"schemes": map[string]any{"bearer": map[string]any{"list": []any{}}}},
	}, got["securityRequirements"])
}

// The agent's own declaration is replaced, never merged: once the gateway is in
// front, what the agent says about its authentication is a claim it no longer
// owns.
func TestBuildGatewayAgentCardDropsTheAgentsOwnSchemes(t *testing.T) {
	in := cardInput()
	in.EnableAPIKeySecurity = true
	got, err := buildGatewayAgentCard(fetchedCard(), in)
	require.NoError(t, err)

	schemes, ok := got["securitySchemes"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, schemes, "legacy")
}

// The platform owns exactly three fields. Everything else — including vendor
// extensions it has never seen — is the agent's and passes through untouched.
func TestBuildGatewayAgentCardPreservesEverythingElse(t *testing.T) {
	got, err := buildGatewayAgentCard(fetchedCard(), cardInput())
	require.NoError(t, err)

	assert.Equal(t, "Trip Planner", got["name"])
	assert.Equal(t, "Plans deterministic multi-day itineraries", got["description"])
	assert.Equal(t, "1.0.0", got["version"])
	assert.Equal(t, map[string]any{"streaming": true}, got["capabilities"])
	assert.Equal(t, map[string]any{"beta": true}, got["x-vendor-feature"])
	assert.Len(t, got["skills"], 1)
}

// The fetched document must not be mutated: the caller holds it, and phase 2
// compares against the stored card to decide whether to republish.
func TestBuildGatewayAgentCardDoesNotMutateItsInput(t *testing.T) {
	fetched := fetchedCard()
	_, err := buildGatewayAgentCard(fetched, cardInput())
	require.NoError(t, err)

	interfaces, ok := fetched["supportedInterfaces"].([]any)
	require.True(t, ok)
	require.Len(t, interfaces, 1)
	assert.Equal(t, "http://trip-planner.dp-default:9099", interfaces[0].(map[string]any)["url"])
}

// supportedInterfaces is replaced wholesale, never merged. The gateway enforces
// the card-to-transport match bidirectionally, so a surviving interface from the
// agent's own card is a hard deploy-time rejection — and a surviving `tenant`
// key is rejected outright, because the gateway serves no tenant-scoped routes.
func TestBuildGatewayAgentCardReplacesInterfacesWholesale(t *testing.T) {
	got, err := buildGatewayAgentCard(fetchedCard(), cardInput())
	require.NoError(t, err)

	interfaces, ok := got["supportedInterfaces"].([]any)
	require.True(t, ok)
	require.Len(t, interfaces, 2, "exactly the two transports the platform configures")

	jsonRPC := interfaces[0].(map[string]any)
	assert.Equal(t, "JSONRPC", jsonRPC["protocolBinding"])
	assert.Equal(t, "1.0", jsonRPC["protocolVersion"])
	assert.Equal(t, "https://agents.example.com/trip-planner/rpc", jsonRPC["url"])
	assert.NotContains(t, jsonRPC, "tenant")

	httpJSON := interfaces[1].(map[string]any)
	assert.Equal(t, "HTTP+JSON", httpJSON["protocolBinding"])
	assert.Equal(t, "1.0", httpJSON["protocolVersion"])
	assert.Equal(t, "https://agents.example.com/trip-planner/rest", httpJSON["url"])
	assert.NotContains(t, httpJSON, "tenant")
}

// The gateway rejects any managed-card interface URL that is not https
// (agent_validator.go:1234), and the local gateway vhost is plain http. Without
// this every development deploy would land in the terminal rejected state.
func TestBuildGatewayAgentCardForcesHTTPS(t *testing.T) {
	in := cardInput()
	in.PublicBaseURL = "http://dev-acme.gateway.localhost:19080/trip-planner"
	got, err := buildGatewayAgentCard(fetchedCard(), in)
	require.NoError(t, err)

	interfaces := got["supportedInterfaces"].([]any)
	assert.Equal(t, "https://dev-acme.gateway.localhost:19080/trip-planner/rpc",
		interfaces[0].(map[string]any)["url"])
	assert.Equal(t, "https://dev-acme.gateway.localhost:19080/trip-planner/rest",
		interfaces[1].(map[string]any)["url"])
}

// A base URL the gateway would reject is refused here, where the reconciler can
// retry it, rather than published and rejected asynchronously.
func TestBuildGatewayAgentCardRefusesAnUnusableBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "not a url", "https://user:pw@host/x", "https://host/x?a=b"} {
		in := cardInput()
		in.PublicBaseURL = base
		_, err := buildGatewayAgentCard(fetchedCard(), in)
		assert.Error(t, err, "base %q must be refused", base)
	}
}
