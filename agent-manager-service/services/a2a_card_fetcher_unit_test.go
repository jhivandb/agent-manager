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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validTestCard = `{"name":"Trip Planner","description":"d","supportedInterfaces":[{"url":"https://a.example/rpc","protocolBinding":"JSONRPC"}],"skills":[{"name":"plan"}],"x-extra":{"kept":true}}`

func cardServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestA2ACardFetcherReturnsTheCardAsServed(t *testing.T) {
	srv := cardServer(t, http.StatusOK, validTestCard)
	got, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL+a2aAgentCardPath, false)
	require.NoError(t, err)
	assert.JSONEq(t, validTestCard, string(got), "unknown fields pass through")
}

func TestA2ACardFetcherRejectsInvalidCards(t *testing.T) {
	cases := map[string]string{
		"missing name":               `{"supportedInterfaces":[{"url":"u"}],"skills":[]}`,
		"empty name":                 `{"name":"","supportedInterfaces":[{"url":"u"}],"skills":[]}`,
		"missing interfaces":         `{"name":"n","skills":[]}`,
		"empty interfaces":           `{"name":"n","supportedInterfaces":[],"skills":[]}`,
		"interface without url":      `{"name":"n","supportedInterfaces":[{"protocolBinding":"JSONRPC"}],"skills":[]}`,
		"missing skills":             `{"name":"n","supportedInterfaces":[{"url":"u"}]}`,
		"null skills":                `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":null}`,
		"skills not an array":        `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":"x"}`,
		"not json":                   `<html>hi</html>`,
		"trailing garbage after obj": `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":[]} x`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := cardServer(t, http.StatusOK, body)
			_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
			require.Error(t, err)
		})
	}
}

func TestA2ACardFetcherRejectsNonObjects(t *testing.T) {
	for _, body := range []string{`[]`, `"card"`, `null`, `42`} {
		srv := cardServer(t, http.StatusOK, body)
		_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
		require.Error(t, err, body)
	}
}

func TestA2ACardFetcherReportsTheStatusCode(t *testing.T) {
	srv := cardServer(t, http.StatusNotFound, `{"name":"n"}`)
	_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestA2ACardFetcherRejectsAnOversizedBody(t *testing.T) {
	padding := strings.Repeat("a", a2aCardMaxBytes)
	body := `{"name":"n","supportedInterfaces":[{"url":"u"}],"skills":[],"pad":"` + padding + `"}`
	srv := cardServer(t, http.StatusOK, body)
	_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 MiB")
}

func TestA2ACardFetcherTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte(validTestCard))
	}))
	t.Cleanup(srv.Close)
	_, err := newA2ACardFetcher(50*time.Millisecond).Fetch(context.Background(), srv.URL, false)
	require.Error(t, err)
}

// External URLs are user-supplied: a loopback address is refused before any request.
func TestA2ACardFetcherGuardedModeRejectsLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(validTestCard))
	}))
	t.Cleanup(srv.Close)

	_, err := newA2ACardFetcher(time.Second).Fetch(context.Background(), srv.URL, true)
	require.Error(t, err)
	assert.Equal(t, int32(0), hits.Load())
}

type fakeFetchCall struct {
	URL     string
	Guarded bool
}

// fakeA2ACardFetcher is the hand-written stub for the in-package A2ACardFetcher.
type fakeA2ACardFetcher struct {
	FetchFunc func(ctx context.Context, url string, guarded bool) (json.RawMessage, error)
	calls     []fakeFetchCall
}

func (f *fakeA2ACardFetcher) Fetch(ctx context.Context, url string, guarded bool) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeFetchCall{URL: url, Guarded: guarded})
	return f.FetchFunc(ctx, url, guarded)
}
