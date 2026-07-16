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

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// signToken creates a throwaway HS256-signed token carrying the given
// audience claim. RejectPublisherAudience only re-parses tokens without
// verifying the signature, so an arbitrary signing key is fine here.
func signToken(t *testing.T, aud string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"aud": aud})
	signed, err := token.SignedString([]byte("k"))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}

func passThroughHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestRejectPublisherAudience_PublisherTokenRejected(t *testing.T) {
	called := false
	guard := RejectPublisherAudience()(passThroughHandler(&called))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	r.Header.Set("Authorization", "Bearer "+signToken(t, "amp-publisher-x"))
	rec := httptest.NewRecorder()

	guard.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("expected next handler not to be called for publisher audience")
	}
}

func TestRejectPublisherAudience_NormalAudiencePassesThrough(t *testing.T) {
	called := false
	guard := RejectPublisherAudience()(passThroughHandler(&called))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	r.Header.Set("Authorization", "Bearer "+signToken(t, "amp"))
	rec := httptest.NewRecorder()

	guard.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !called {
		t.Error("expected next handler to be called for non-publisher audience")
	}
}

func TestRejectPublisherAudience_MissingTokenPassesThrough(t *testing.T) {
	called := false
	guard := RejectPublisherAudience()(passThroughHandler(&called))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	rec := httptest.NewRecorder()

	guard.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !called {
		t.Error("expected next handler to be called when Authorization header is missing (JWTAuth handles this)")
	}
}

func TestRejectPublisherAudience_GarbledTokenPassesThrough(t *testing.T) {
	called := false
	guard := RejectPublisherAudience()(passThroughHandler(&called))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	r.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	rec := httptest.NewRecorder()

	guard.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !called {
		t.Error("expected next handler to be called when the token can't be parsed (JWTAuth handles this)")
	}
}
