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

package config

import (
	"strings"
	"testing"
)

func TestValidateOAuthAuthorizationServers(t *testing.T) {
	tests := []struct {
		name        string
		servers     []string
		wantErrors  int
		errContains string
	}{
		{
			name:       "empty list is allowed at config-load time",
			servers:    nil,
			wantErrors: 0,
		},
		{
			name:       "valid https URL",
			servers:    []string{"https://idp.example.com"},
			wantErrors: 0,
		},
		{
			name:       "valid http URL (dev)",
			servers:    []string{"http://thunder.amp.localhost:8080"},
			wantErrors: 0,
		},
		{
			name:       "multiple valid URLs",
			servers:    []string{"https://idp1.example.com", "https://idp2.example.com/path"},
			wantErrors: 0,
		},
		{
			name:        "non-http(s) scheme rejected",
			servers:     []string{"ftp://idp.example.com"},
			wantErrors:  1,
			errContains: "must use http or https",
		},
		{
			name:        "missing host rejected",
			servers:     []string{"https://"},
			wantErrors:  1,
			errContains: "must have a non-empty host",
		},
		{
			name:        "non-URL string rejected",
			servers:     []string{"Agent Management Platform Local"},
			wantErrors:  2, // missing scheme + missing host
			errContains: "OAUTH_AUTHORIZATION_SERVERS",
		},
		{
			name:        "one good one bad accumulates only the bad",
			servers:     []string{"https://idp.example.com", "ftp://nope.example.com"},
			wantErrors:  1,
			errContains: "ftp://nope.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{OAuthAuthorizationServers: tc.servers}
			r := &configReader{}
			validateOAuthAuthorizationServers(cfg, r)

			if len(r.errors) != tc.wantErrors {
				t.Fatalf("expected %d errors, got %d: %v", tc.wantErrors, len(r.errors), r.errors)
			}
			if tc.errContains != "" {
				found := false
				for _, e := range r.errors {
					if strings.Contains(e.Error(), tc.errContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected an error containing %q, got %v", tc.errContains, r.errors)
				}
			}
		})
	}
}

func TestValidateServerPublicURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantErrors  int
		errContains string
	}{
		{
			name:       "empty is allowed",
			url:        "",
			wantErrors: 0,
		},
		{
			name:       "valid https URL",
			url:        "https://api.example.com",
			wantErrors: 0,
		},
		{
			name:       "valid http URL with port",
			url:        "http://localhost:8080",
			wantErrors: 0,
		},
		{
			name:       "valid https URL with path",
			url:        "https://api.example.com/v1",
			wantErrors: 0,
		},
		{
			name:        "non-http(s) scheme rejected",
			url:         "ftp://api.example.com",
			wantErrors:  1,
			errContains: "must use http or https",
		},
		{
			name:        "missing host rejected",
			url:         "https://",
			wantErrors:  1,
			errContains: "must have a non-empty host",
		},
		{
			name:        "bare string rejected",
			url:         "not-a-url",
			wantErrors:  2,
			errContains: "SERVER_PUBLIC_URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{ServerPublicURL: tc.url}
			r := &configReader{}
			validateServerPublicURL(cfg, r)

			if len(r.errors) != tc.wantErrors {
				t.Fatalf("expected %d errors, got %d: %v", tc.wantErrors, len(r.errors), r.errors)
			}
			if tc.errContains != "" {
				found := false
				for _, e := range r.errors {
					if strings.Contains(e.Error(), tc.errContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected an error containing %q, got %v", tc.errContains, r.errors)
				}
			}
		})
	}
}

func TestValidateObserverURLs(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		publicURL   string
		wantErrors  int
		errContains string
	}{
		{
			name:       "valid in-cluster and public URLs",
			url:        "http://amp-observer.openchoreo-observability-plane.svc.cluster.local:9098",
			publicURL:  "https://observer.example.com",
			wantErrors: 0,
		},
		{
			name:       "valid http URL with port",
			url:        "http://localhost:9098",
			publicURL:  "http://localhost:9098",
			wantErrors: 0,
		},
		{
			name:        "malformed in-cluster URL rejected",
			url:         "not-a-url",
			publicURL:   "https://observer.example.com",
			wantErrors:  2, // missing scheme + missing host
			errContains: "AM_OBSERVER_URL",
		},
		{
			name:        "malformed public URL rejected",
			url:         "http://localhost:9098",
			publicURL:   "not-a-url",
			wantErrors:  2, // missing scheme + missing host
			errContains: "AM_OBSERVER_PUBLIC_URL",
		},
		{
			name:        "non-http(s) scheme rejected",
			url:         "ftp://observer.example.com",
			publicURL:   "https://observer.example.com",
			wantErrors:  1,
			errContains: "must use http or https",
		},
		{
			name:        "missing host rejected",
			url:         "http://localhost:9098",
			publicURL:   "https://",
			wantErrors:  1,
			errContains: "must have a non-empty host",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Observer: ObserverConfig{URL: tc.url, PublicURL: tc.publicURL}}
			r := &configReader{}
			validateObserverURLs(cfg, r)

			if len(r.errors) != tc.wantErrors {
				t.Fatalf("expected %d errors, got %d: %v", tc.wantErrors, len(r.errors), r.errors)
			}
			if tc.errContains != "" {
				found := false
				for _, e := range r.errors {
					if strings.Contains(e.Error(), tc.errContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected an error containing %q, got %v", tc.errContains, r.errors)
				}
			}
		})
	}
}

func TestLoadEnvs_ObserverConfig(t *testing.T) {
	requiredEnv := map[string]string{
		"OPEN_CHOREO_BASE_URL": "http://localhost/api/v1",
		"DB_HOST":              "localhost",
		"DB_USER":              "unit",
		"DB_PASSWORD":          "unit",
		"DB_NAME":              "unit",
	}

	setEnv := func(t *testing.T, vars map[string]string) {
		t.Helper()
		for k, v := range vars {
			t.Setenv(k, v)
		}
	}

	t.Run("defaults: URL set, PublicURL empty", func(t *testing.T) {
		setEnv(t, requiredEnv)
		loadEnvs()

		if got := config.Observer.URL; got != "http://localhost:9098" {
			t.Errorf("Observer.URL = %q, want %q", got, "http://localhost:9098")
		}
		if got := config.Observer.PublicURL; got != "" {
			t.Errorf("Observer.PublicURL = %q, want empty", got)
		}
	})

	t.Run("IS_LOCAL_DEV_ENV=true defaults PublicURL to localhost", func(t *testing.T) {
		setEnv(t, requiredEnv)
		t.Setenv("IS_LOCAL_DEV_ENV", "true")
		loadEnvs()

		if got := config.Observer.PublicURL; got != "http://localhost:9098" {
			t.Errorf("Observer.PublicURL = %q, want %q", got, "http://localhost:9098")
		}
	})

	t.Run("explicit AM_OBSERVER_PUBLIC_URL wins over IS_LOCAL_DEV_ENV default", func(t *testing.T) {
		setEnv(t, requiredEnv)
		t.Setenv("IS_LOCAL_DEV_ENV", "true")
		t.Setenv("AM_OBSERVER_PUBLIC_URL", "https://observer.example.com")
		loadEnvs()

		if got := config.Observer.PublicURL; got != "https://observer.example.com" {
			t.Errorf("Observer.PublicURL = %q, want %q", got, "https://observer.example.com")
		}
	})

	t.Run("explicit AM_OBSERVER_URL wins over default", func(t *testing.T) {
		setEnv(t, requiredEnv)
		t.Setenv("AM_OBSERVER_URL", "http://observer.internal:9099")
		loadEnvs()

		if got := config.Observer.URL; got != "http://observer.internal:9099" {
			t.Errorf("Observer.URL = %q, want %q", got, "http://observer.internal:9099")
		}
	})
}
