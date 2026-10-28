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

package models

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestGatewayArtifactRefNamesArtifactsAsTheGatewayStoresThem(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		artifactKind string
		gatewayKind  string
		gatewayID    string
	}{
		{KindLLMProvider, GatewayKindLlmProvider, id.String()},
		{KindLLMProxy, GatewayKindLlmProxy, "my-proxy"},
		{KindMCPProxy, GatewayKindMcp, id.String()},
		{KindMCPMapping, GatewayKindMcp, id.String()},
		{KindAgent, GatewayKindAgent, id.String()},
	} {
		ref := GatewayArtifactRef{ArtifactUUID: id, ArtifactHandle: "my-proxy", ArtifactKind: tc.artifactKind}
		kind, ok := ref.GatewayKind()
		assert.True(t, ok, tc.artifactKind)
		assert.Equal(t, tc.gatewayKind, kind, tc.artifactKind)
		assert.Equal(t, tc.gatewayID, ref.GatewayArtifactID(), tc.artifactKind)
	}

	for _, kind := range []string{KindRestAPI, KindWebSubAPI, "Unknown"} {
		_, ok := GatewayArtifactRef{ArtifactUUID: id, ArtifactKind: kind}.GatewayKind()
		assert.False(t, ok, "AMS deploys no %s to a gateway", kind)
	}
}
