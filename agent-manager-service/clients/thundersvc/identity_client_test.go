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

package thundersvc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddGroupMemberEntries_SendsAgentType(t *testing.T) {
	var body GroupMembersRequest
	srv := newTestThunderServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/groups/g1/members/add", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c := NewIdentityClient(srv.URL, "sys-client", "sys-secret")
	err := c.AddGroupMemberEntries(context.Background(), "g1", []GroupMember{{ID: "a1", Type: "agent"}})

	require.NoError(t, err)
	require.Len(t, body.Members, 1)
	assert.Equal(t, "a1", body.Members[0].ID)
	assert.Equal(t, "agent", body.Members[0].Type)
}

func TestRemoveGroupMemberEntries_SendsAgentType(t *testing.T) {
	var body GroupMembersRequest
	srv := newTestThunderServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/groups/g1/members/remove", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c := NewIdentityClient(srv.URL, "sys-client", "sys-secret")
	err := c.RemoveGroupMemberEntries(context.Background(), "g1", []GroupMember{{ID: "a1", Type: "agent"}})

	require.NoError(t, err)
	require.Len(t, body.Members, 1)
	assert.Equal(t, "a1", body.Members[0].ID)
	assert.Equal(t, "agent", body.Members[0].Type)
}

func TestListGroupMemberEntries_ReturnsTypedEntries(t *testing.T) {
	srv := newTestThunderServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/groups/g1/members", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalResults": 2,
			"members": []map[string]any{
				{"id": "a1", "type": "agent"},
				{"id": "u1", "type": "user"},
			},
		})
	})
	defer srv.Close()

	c := NewIdentityClient(srv.URL, "sys-client", "sys-secret")
	members, total, err := c.ListGroupMemberEntries(context.Background(), "g1", 0, 20)

	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, members, 2)
	assert.Equal(t, GroupMember{ID: "a1", Type: "agent"}, members[0])
	assert.Equal(t, GroupMember{ID: "u1", Type: "user"}, members[1])
}

func TestEnsureScopeResourceServer_CreatesScopeAsSingleResourceWithSlashDelimiter(t *testing.T) {
	var createRSBody map[string]any
	var createResourceBody map[string]any
	rsCreated, resCreated := 0, 0

	srv := newTestThunderServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/resource-servers":
			_ = json.NewEncoder(w).Encode(map[string]any{"resourceServers": []any{}, "total": 0})
		case r.Method == http.MethodGet && r.URL.Path == "/organization-units/tree/default":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ou-1"})
		case r.Method == http.MethodPost && r.URL.Path == "/resource-servers":
			rsCreated++
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createRSBody))
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "rs-1", "identifier": scopeResourceServerIdentifier})
		case r.Method == http.MethodGet && r.URL.Path == "/resource-servers/rs-1/resources":
			_ = json.NewEncoder(w).Encode(map[string]any{"resources": []any{}, "totalResults": 0})
		case r.Method == http.MethodPost && r.URL.Path == "/resource-servers/rs-1/resources":
			resCreated++
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createResourceBody))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "res-1", "handle": createResourceBody["handle"], "permission": "repo:read.all",
			})
		default:
			// Any other request (notably an action POST) is a bug: Thunder derives the
			// permission from the resource handle, so no action must be created.
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer srv.Close()

	c := NewIdentityClient(srv.URL, "sys-client", "sys-secret")
	rsID, err := c.EnsureScopeResourceServer(context.Background(), []string{"repo:read.all"})

	require.NoError(t, err)
	assert.Equal(t, "rs-1", rsID)
	assert.Equal(t, 1, rsCreated, "resource server created exactly once")
	assert.Equal(t, 1, resCreated, "scope resource created exactly once")
	assert.Equal(t, scopeResourceServerIdentifier, createRSBody["identifier"])
	assert.Equal(t, "ou-1", createRSBody["ouId"], "resource-server creation requires the default OU id")
	assert.Equal(t, "/", createRSBody["delimiter"],
		"resource server uses '/' delimiter so scope handles never contain the delimiter")
	assert.Equal(t, "repo:read.all", createResourceBody["handle"],
		"each scope is registered as a resource whose handle is the raw scope, so the server-derived permission equals the scope")
	assert.Equal(t, "repo:read.all", createResourceBody["name"])
}

func TestEnsureScopeResourceServer_IdempotentReadsResourcePermission(t *testing.T) {
	srv := newTestThunderServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/resource-servers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resourceServers": []map[string]any{
					{"id": "rs-1", "identifier": scopeResourceServerIdentifier},
				},
				"total": 1,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/resource-servers/rs-1/resources":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resources":    []map[string]any{{"id": "res-1", "handle": "repo:read.all", "permission": "repo:read.all"}},
				"totalResults": 1,
			})
		case r.Method == http.MethodPost:
			t.Fatalf("no creation calls expected when the scope already exists, got POST %s", r.URL.Path)
		default:
			// The idempotency check must read the resource's own permission field, not
			// list per-resource actions; any other GET (e.g. an actions listing) is a bug.
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer srv.Close()

	c := NewIdentityClient(srv.URL, "sys-client", "sys-secret")
	rsID, err := c.EnsureScopeResourceServer(context.Background(), []string{"repo:read.all"})

	require.NoError(t, err)
	assert.Equal(t, "rs-1", rsID)
}

func TestEnsureScopeResourceServer_RejectsScopeExceedingHandleLimit(t *testing.T) {
	srv := newTestThunderServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("no Thunder calls expected for an over-long scope, got %s %s", r.Method, r.URL.Path)
	})
	defer srv.Close()

	longScope := strings.Repeat("a", 101)
	c := NewIdentityClient(srv.URL, "sys-client", "sys-secret")
	_, err := c.EnsureScopeResourceServer(context.Background(), []string{longScope})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "100", "error should state the 100-character Thunder handle limit")
}
