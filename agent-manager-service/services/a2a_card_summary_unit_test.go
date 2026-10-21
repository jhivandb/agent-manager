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

package services

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeA2ACard_ReadsFields(t *testing.T) {
	card := json.RawMessage(`{"name":"Order Support","description":"Refunds","version":"2.1.0","skills":[{"name":"issue-refund"},{"name":"order-status"}]}`)

	assert.Equal(t, a2aCardSummary{
		Name:        "Order Support",
		Description: "Refunds",
		Version:     "2.1.0",
		Skills:      []string{"issue-refund", "order-status"},
	}, summarizeA2ACard(card))
}

func TestSummarizeA2ACard_ToleratesBadShapes(t *testing.T) {
	for name, card := range map[string]json.RawMessage{
		"nil":          nil,
		"null":         []byte("null"),
		"wrong types":  []byte(`{"name":5,"skills":"x"}`),
		"invalid json": []byte("{"),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, a2aCardSummary{}, summarizeA2ACard(card))
		})
	}

	got := summarizeA2ACard([]byte(`{"skills":[1,{"name":""},{"name":"ok"}]}`))
	assert.Equal(t, []string{"ok"}, got.Skills)
}

func TestA2ACardFingerprint_IgnoresInterfaceURLs(t *testing.T) {
	dev := json.RawMessage(`{"name":"a","supportedInterfaces":[{"url":"https://gw.dev/x","protocolBinding":"JSONRPC"}]}`)
	prod := json.RawMessage(`{"name":"a","supportedInterfaces":[{"url":"https://gw.prod/x","protocolBinding":"JSONRPC"}]}`)

	devHash, err := a2aCardFingerprint(dev)
	require.NoError(t, err)
	prodHash, err := a2aCardFingerprint(prod)
	require.NoError(t, err)

	assert.NotEmpty(t, devHash)
	assert.Equal(t, devHash, prodHash)
}

func TestA2ACardFingerprint_DetectsSkillChange(t *testing.T) {
	before := json.RawMessage(`{"name":"a","skills":[{"name":"one"}]}`)
	after := json.RawMessage(`{"name":"a","skills":[{"name":"one"},{"name":"two"}]}`)

	beforeHash, err := a2aCardFingerprint(before)
	require.NoError(t, err)
	afterHash, err := a2aCardFingerprint(after)
	require.NoError(t, err)

	assert.NotEqual(t, beforeHash, afterHash)
}

func TestA2ACardFingerprint_NilCard(t *testing.T) {
	got, err := a2aCardFingerprint(nil)

	require.NoError(t, err)
	assert.Empty(t, got)
}
