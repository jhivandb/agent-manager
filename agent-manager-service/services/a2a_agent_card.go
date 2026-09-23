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
	"net/url"
	"strings"
)

// a2aCardURLScheme is forced rather than passed through: the gateway rejects any
// non-https interface URL, but every dev vhost is plain http.
const a2aCardURLScheme = "https"

// Proto3 JSON names for the AgentCard fields the platform rewrites or drops
// (a2aproject/A2A v1.0.1, specification/a2a.proto:361).
const (
	cardFieldSupportedInterfaces  = "supportedInterfaces"
	cardFieldSecuritySchemes      = "securitySchemes"
	cardFieldSecurityRequirements = "securityRequirements"
	cardFieldSignatures           = "signatures"
)

// The map keys naming each scheme; securityRequirements refers back to them, so
// the two must agree.
const (
	a2aCardSchemeKeyAPIKey = "apiKey"
	a2aCardSchemeKeyBearer = "bearer"
)

// GatewayAgentCardInput is everything the card builder needs, already resolved,
// so the emitted document stays a pure function for golden-testing.
type GatewayAgentCardInput struct {
	// PublicBaseURL is the agent's gateway-facing base, vhost plus context.
	PublicBaseURL string
	// Taken from resolvedCORSConfig, not buildPolicies' untyped map output, so
	// the card and the policy chain cannot disagree.
	EnableAPIKeySecurity bool
	EnableOAuthSecurity  bool
}

// buildGatewayAgentCard derives the document the gateway serves from the one the
// agent serves: it replaces exactly what stops being true once the gateway is in
// front (addresses, declared auth) and passes everything else through untouched.
func buildGatewayAgentCard(fetched map[string]any, in GatewayAgentCardInput) (map[string]any, error) {
	base, err := gatewayCardBaseURL(in.PublicBaseURL)
	if err != nil {
		return nil, err
	}

	// Shallow copy: every key the builder writes is replaced outright, so no
	// nested value the caller still holds is ever mutated.
	card := make(map[string]any, len(fetched)+2)
	for k, v := range fetched {
		card[k] = v
	}

	card[cardFieldSupportedInterfaces] = gatewayCardInterfaces(base)

	schemes, requirements := gatewayCardSecurity(in)
	card[cardFieldSecuritySchemes] = schemes
	if requirements == nil {
		delete(card, cardFieldSecurityRequirements)
	} else {
		card[cardFieldSecurityRequirements] = requirements
	}

	// The agent's JWS covered the fields just rewritten, so it could only fail verification.
	delete(card, cardFieldSignatures)

	return card, nil
}

// gatewayCardBaseURL normalises the public base to what the gateway will accept:
// absolute, https, no userinfo, no query, no fragment, no trailing slash.
func gatewayCardBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("refusing to build an agent card: no public base url")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" {
		return "", fmt.Errorf("refusing to build an agent card: %q is not an absolute url", raw)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("refusing to build an agent card: %q carries userinfo", raw)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("refusing to build an agent card: %q carries a query or fragment", raw)
	}
	parsed.Scheme = a2aCardURLScheme
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

// gatewayCardInterfaces advertises exactly the transports
// buildA2AAgentDeploymentYAML configures, from the same constants: the gateway
// enforces the card-to-transport match bidirectionally, so drift here is a
// deploy-time rejection.
//
// Keys are AgentInterface in proto3 JSON (a2a.proto:336). `tenant` is the third
// field and is deliberately never written: the gateway serves no tenant-scoped
// A2A routes and rejects a card that declares one.
func gatewayCardInterfaces(base string) []any {
	return []any{
		map[string]any{
			"protocolBinding": a2aTransportJSONRPC,
			"protocolVersion": a2aProtocolVersion,
			"url":             base + a2aPathPrefixJSONRPC,
		},
		map[string]any{
			"protocolBinding": a2aTransportHTTPJSON,
			"protocolVersion": a2aProtocolVersion,
			"url":             base + a2aPathPrefixHTTPRPC,
		},
	}
}

// gatewayCardSecurity describes the policy chain the gateway actually enforces.
// Keys are proto3 JSON for a2aproject/A2A v1.0.1 specification/a2a.proto:
// APIKeySecurityScheme's field is `location`, not the OpenAPI `in` (proto:523),
// and StringList's is `list`, not `values` (proto:490) — the gateway does not
// inspect card content, so a wrong key here is stored and served verbatim.
//
// The empty case is written out rather than left as whatever the agent
// declared: an unauthenticated agent behind an unauthenticated gateway should
// say so, not repeat a claim it no longer owns.
func gatewayCardSecurity(in GatewayAgentCardInput) (map[string]any, []any) {
	switch {
	case in.EnableAPIKeySecurity:
		return map[string]any{
			a2aCardSchemeKeyAPIKey: map[string]any{
				"apiKeySecurityScheme": map[string]any{
					"location": "header",
					"name":     apiKeyAuthHeaderName,
				},
			},
		}, a2aCardSecurityRequirement(a2aCardSchemeKeyAPIKey)
	case in.EnableOAuthSecurity:
		// "Bearer" (capitalised) matches the IANA registry and the proto's own
		// example; RFC 9110 §11.1 makes the comparison case-insensitive anyway.
		return map[string]any{
			a2aCardSchemeKeyBearer: map[string]any{
				"httpAuthSecurityScheme": map[string]any{"scheme": "Bearer"},
			},
		}, a2aCardSecurityRequirement(a2aCardSchemeKeyBearer)
	default:
		return map[string]any{}, nil
	}
}

// a2aCardSecurityRequirement requires one scheme with no scopes. The empty list
// is written explicitly rather than omitted, so it reads as "no scopes
// required" rather than "not considered".
func a2aCardSecurityRequirement(schemeKey string) []any {
	return []any{
		map[string]any{
			"schemes": map[string]any{schemeKey: map[string]any{"list": []any{}}},
		},
	}
}
