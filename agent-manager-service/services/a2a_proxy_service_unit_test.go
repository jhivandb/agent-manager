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
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/jwtassertion"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const proxyTestEnvUUID = "55555555-5555-5555-5555-555555555555"

var proxyTestKey = bytes.Repeat([]byte{7}, 32)

// proxyHarness serves an external A2A agent whose dev environment has a registered card URL and is
// still direct. Writes are wired only on the tx-bound mocks, and only by acceptsWrites; tx-bound reads
// delegate to the plain mocks.
type proxyHarness struct {
	*publisherMocks
	svc                *a2aProxyService
	txProxies          *repomocks.A2AProxyRepositoryMock
	cards, txCards     *repomocks.A2AAgentCardRepositoryMock
	configs, txConfigs *repomocks.AgentConfigRepositoryMock
	oc                 *clientmocks.OpenChoreoClientMock
	artifactUUID       uuid.UUID
}

func newProxyHarness(t *testing.T) *proxyHarness {
	m := newPublisherMocks(t)
	h := &proxyHarness{
		publisherMocks: m,
		txProxies:      &repomocks.A2AProxyRepositoryMock{},
		txCards:        &repomocks.A2AAgentCardRepositoryMock{},
		txConfigs:      &repomocks.AgentConfigRepositoryMock{},
		artifactUUID:   uuid.New(),
	}
	m.proxies.WithTxFunc = func(tx *gorm.DB) repositories.A2AProxyRepository {
		assert.Same(t, txSentinel, tx)
		return h.txProxies
	}
	m.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return nil, repositories.ErrA2AProxyNotFound
	}
	h.txProxies.GetByEnvUUIDFunc = func(ctx context.Context, ouID, projectName, agentName string, envUUID uuid.UUID) (*models.A2AProxy, error) {
		return m.proxies.GetByEnvUUIDFunc(ctx, ouID, projectName, agentName, envUUID)
	}
	h.txCards.GetFunc = func(ctx context.Context, ouID, projectName, agentName, envName string) (*models.A2AAgentCard, error) {
		return h.cards.GetFunc(ctx, ouID, projectName, agentName, envName)
	}
	m.artifacts.GetByHandleFunc = func(string, string) (*models.Artifact, error) {
		return &models.Artifact{UUID: h.artifactUUID, Kind: models.KindAgent}, nil
	}
	h.cards = &repomocks.A2AAgentCardRepositoryMock{
		GetFunc: func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
			return &models.A2AAgentCard{
				Source: models.A2AAgentCardSourceExternal, SourceURL: "https://agent.example/.well-known/agent-card.json",
			}, nil
		},
		WithTxFunc: func(tx *gorm.DB) repositories.A2AAgentCardRepository {
			assert.Same(t, txSentinel, tx)
			return h.txCards
		},
	}
	h.configs = &repomocks.AgentConfigRepositoryMock{
		WithTxFunc: func(tx *gorm.DB) repositories.AgentConfigRepository {
			assert.Same(t, txSentinel, tx)
			return h.txConfigs
		},
	}
	h.oc = &clientmocks.OpenChoreoClientMock{
		GetComponentFunc: func(context.Context, string, string, string) (*models.AgentResponse, error) {
			return externalA2AAgent(), nil
		},
		GetEnvironmentFunc: func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
			return &models.EnvironmentResponse{UUID: proxyTestEnvUUID, Name: name}, nil
		},
	}
	h.svc = NewA2AProxyService(m.tx(), m.proxies, h.cards, h.configs, m.artifacts, m.publisher(), h.oc, proxyTestKey,
		testLogger()).(*a2aProxyService)
	return h
}

// proxied makes the environment proxied; an empty secret means no upstream auth is stored.
func (h *proxyHarness) proxied(header, secret string) {
	h.proxies.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{
			EnvironmentUUID: uuid.MustParse(proxyTestEnvUUID), UpstreamAuthHeader: header, UpstreamAuthSecret: secret,
		}, nil
	}
}

// acceptsWrites lets every write a transition may make succeed, inside the transaction only.
func (h *proxyHarness) acceptsWrites() {
	h.txProxies.UpsertFunc = func(context.Context, *models.A2AProxy) error { return nil }
	h.txProxies.DeleteFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	h.txCards.EnqueueFunc = func(context.Context, *models.A2AAgentCard) error { return nil }
	h.txCards.ClearCardFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	h.txCards.RequeueFunc = func(context.Context, string, string, string, string) error { return nil }
	h.txCards.DeleteForAgentEnvFunc = func(context.Context, string, string, string, string) error { return nil }
	h.txConfigs.CreateIfAbsentFunc = func(context.Context, *models.AgentConfig) error { return nil }
	h.txPubs.EnqueueFunc = func(context.Context, *models.A2APublication) error { return nil }
	h.txPubs.DeleteForAgentEnvFunc = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	h.txDeployments.DeleteForArtifactFunc = func(context.Context, uuid.UUID, string) error { return nil }
	h.artifacts.CreateFunc = func(*gorm.DB, *models.Artifact) error { return nil }
}

func decryptForTest(t *testing.T, secretRef string) string {
	t.Helper()
	ciphertext, err := base64.StdEncoding.DecodeString(secretRef)
	require.NoError(t, err)
	plaintext, err := utils.DecryptBytes(ciphertext, proxyTestKey)
	require.NoError(t, err)
	return string(plaintext)
}

func devEnv() *models.EnvironmentResponse {
	return &models.EnvironmentResponse{UUID: proxyTestEnvUUID, Name: "dev"}
}

// tierOnlyCtx authorises both tiers but carries no audit recorder.
func tierOnlyCtx() context.Context {
	return jwtassertion.ContextWithTokenClaimsAndScope(context.Background(), &jwtassertion.TokenClaims{
		Scope: audit.ScopesOf([]rbac.Permission{rbac.AgentEnvNonProduction, rbac.AgentEnvProduction}),
	})
}

func TestSetA2AProxySwitchesADirectEnvironmentToProxied(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	value := "s3cret"

	require.NoError(t, h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev",
		&models.A2AProxyAuthUpdate{Header: "X-Upstream-Key", Value: &value}))

	upserted := h.txProxies.UpsertCalls()
	require.Len(t, upserted, 1)
	assert.Equal(t, "X-Upstream-Key", upserted[0].Proxy.UpstreamAuthHeader)
	assert.Equal(t, value, decryptForTest(t, upserted[0].Proxy.UpstreamAuthSecret))
	assert.Equal(t, uuid.MustParse(proxyTestEnvUUID), upserted[0].Proxy.EnvironmentUUID)

	configs := h.txConfigs.CreateIfAbsentCalls()
	require.Len(t, configs, 1)
	assert.True(t, configs[0].Config.EnableApiKeySecurity, "inbound auth defaults on")
	assert.Contains(t, configs[0].Config.CORSAllowHeaders, "A2A-Version")
	assert.Nil(t, configs[0].Config.CardCORSEnabled, "the card inherits the agent's CORS")

	require.Len(t, h.txCards.ClearCardCalls(), 1, "the card is parked")
	assert.Empty(t, h.txCards.RequeueCalls(), "and not made due before the gateway has the Agent")

	queued := h.txPubs.EnqueueCalls()
	require.Len(t, queued, 1)
	assert.Equal(t, models.A2APublicationSourceExternal, queued[0].Pub.Source)
	assert.Equal(t, h.artifactUUID, queued[0].Pub.ArtifactUUID)
}

func TestSetA2AProxyNeedsARegisteredCardURL(t *testing.T) {
	h := newProxyHarness(t)
	h.cards.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}

	err := h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", nil)

	assert.ErrorIs(t, err, utils.ErrA2AProxyNeedsCardSource)
	assert.Zero(t, h.transactions)
}

// The stored card stays until the fetch after the republish replaces it.
func TestSetA2AProxyUpdatesAProxiedEnvironmentInPlace(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("X-Old", "b2xk")
	h.txCards.ClearCardFunc = nil
	h.txConfigs.CreateIfAbsentFunc = nil

	require.NoError(t, h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev",
		&models.A2AProxyAuthUpdate{Header: "X-New"}))

	upserted := h.txProxies.UpsertCalls()
	require.Len(t, upserted, 1)
	assert.Equal(t, "X-New", upserted[0].Proxy.UpstreamAuthHeader)
	assert.Equal(t, "b2xk", upserted[0].Proxy.UpstreamAuthSecret, "a header alone keeps the stored secret")
	assert.Len(t, h.txPubs.EnqueueCalls(), 1)
}

func TestSetA2AProxySecretSemantics(t *testing.T) {
	replacement, empty := "n3w", ""
	cases := []struct {
		name        string
		stored      string
		auth        *models.A2AProxyAuthUpdate
		wantHeader  string
		wantSecret  string
		wantDecrypt string
		wantErr     error
	}{
		{name: "omitting auth clears it", stored: "b2xk", auth: nil},
		{
			name: "a header alone keeps the stored secret", stored: "b2xk",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key"}, wantHeader: "X-Key", wantSecret: "b2xk",
		},
		{
			name: "a value replaces it", stored: "b2xk",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key", Value: &replacement}, wantHeader: "X-Key", wantDecrypt: "n3w",
		},
		{
			name: "an empty value is rejected", stored: "b2xk",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key", Value: &empty}, wantErr: utils.ErrInvalidUpstreamAuth,
		},
		{
			name: "a header alone needs a stored secret",
			auth: &models.A2AProxyAuthUpdate{Header: "X-Key"}, wantErr: utils.ErrInvalidUpstreamAuth,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProxyHarness(t)
			h.acceptsWrites()
			h.proxied("X-Key", tc.stored)

			err := h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", tc.auth)

			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				assert.Zero(t, h.transactions)
				return
			}
			require.NoError(t, err)
			row := h.txProxies.UpsertCalls()[0].Proxy
			assert.Equal(t, tc.wantHeader, row.UpstreamAuthHeader)
			if tc.wantDecrypt != "" {
				assert.Equal(t, tc.wantDecrypt, decryptForTest(t, row.UpstreamAuthSecret))
				return
			}
			assert.Equal(t, tc.wantSecret, row.UpstreamAuthSecret)
		})
	}
}

func TestValidateA2AUpstreamAuth(t *testing.T) {
	value := func(v string) *string { return &v }
	for _, auth := range []*models.A2AProxyAuthUpdate{
		nil,
		{Header: "X-Upstream_Key-1"},
		{Header: "X-Key", Value: value(strings.Repeat("a", 8192))},
		{Header: "Authorization", Value: value("Bearer t")},
		{Header: "X-Proxy-Key", Value: value("v")},
	} {
		assert.NoError(t, validateA2AUpstreamAuth(auth))
	}
	for name, auth := range map[string]*models.A2AProxyAuthUpdate{
		"a header with a space":  {Header: "X Key", Value: value("v")},
		"an empty header":        {Header: "", Value: value("v")},
		"Host":                   {Header: "host", Value: value("v")},
		"Content-Length":         {Header: "content-length", Value: value("v")},
		"Transfer-Encoding":      {Header: "Transfer-Encoding", Value: value("v")},
		"Connection":             {Header: "CONNECTION", Value: value("v")},
		"Upgrade":                {Header: "Upgrade", Value: value("v")},
		"TE":                     {Header: "te", Value: value("v")},
		"Keep-Alive":             {Header: "Keep-Alive", Value: value("v")},
		"Trailer":                {Header: "Trailer", Value: value("v")},
		"a Proxy-* header":       {Header: "proxy-authorization", Value: value("v")},
		"a header alone":         {Header: "Proxy-Connection"},
		"a 256-character header": {Header: strings.Repeat("h", 256), Value: value("v")},
		"an empty value":         {Header: "X-Key", Value: value("")},
		"8194 bytes":             {Header: "X-Key", Value: value(strings.Repeat("é", 4097))},
		"a single quote":         {Header: "X-Key", Value: value("it's")},
		"a carriage return":      {Header: "X-Key", Value: value("a\rb")},
		"a line feed":            {Header: "X-Key", Value: value("a\nb")},
		// The gateway renders {{ }} with secret and env functions.
		"template syntax": {Header: "X-Key", Value: value(`{{ secret "openai" }}`)},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, validateA2AUpstreamAuth(auth), utils.ErrInvalidUpstreamAuth)
		})
	}
}

func TestSetA2AProxyRefusesWithoutAnAuditRecord(t *testing.T) {
	h := newProxyHarness(t)
	err := h.svc.SetA2AProxy(tierOnlyCtx(), "org-1", "checkout", "weather", "dev", nil)
	assert.ErrorIs(t, err, audit.ErrRecorderUnavailable)
	assert.Zero(t, h.transactions)
}

func TestDeleteA2AProxyRefusesWithoutAnAuditRecord(t *testing.T) {
	h := newProxyHarness(t)
	h.proxied("X-Key", "c2VjcmV0")
	err := h.svc.DeleteA2AProxy(tierOnlyCtx(), "org-1", "checkout", "weather", "dev")
	assert.ErrorIs(t, err, audit.ErrRecorderUnavailable)
	assert.Zero(t, h.transactions)
}

func TestA2AProxyOnADirectEnvironmentIsNotFound(t *testing.T) {
	h := newProxyHarness(t)
	h.proxies.GetFunc = func(context.Context, string, string, string, string) (*models.A2AProxy, error) {
		return nil, repositories.ErrA2AProxyNotFound
	}

	_, err := h.svc.GetA2AProxy(context.Background(), "org-1", "checkout", "weather", "dev")
	assert.ErrorIs(t, err, utils.ErrEnvironmentNotProxied)
	assert.ErrorIs(t, h.svc.DeleteA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"), utils.ErrEnvironmentNotProxied)
	assert.ErrorIs(t, h.svc.RefreshA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"), utils.ErrEnvironmentNotProxied)
	assert.Zero(t, h.transactions)
}

func TestGetA2AProxyReportsThePublicationState(t *testing.T) {
	h := newProxyHarness(t)
	h.proxies.GetFunc = func(context.Context, string, string, string, string) (*models.A2AProxy, error) {
		return &models.A2AProxy{EnvironmentUUID: uuid.MustParse(proxyTestEnvUUID), UpstreamAuthHeader: "X-Key", UpstreamAuthSecret: "c2VjcmV0"}, nil
	}
	h.pubs.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return &models.A2APublication{Status: models.A2APublicationStatusFailed, LastError: "HTTP 503"}, nil
	}
	h.oc.GetComponentFunc = nil // a stored row proves the agent, so a poll costs no OpenChoreo call

	got, err := h.svc.GetA2AProxy(context.Background(), "org-1", "checkout", "weather", "dev")

	require.NoError(t, err)
	assert.Equal(t, &models.A2AProxyStatus{
		Status: models.A2APublicationStatusFailed, LastError: "HTTP 503", UpstreamAuthHeader: "X-Key",
	}, got)
}

// Both rows are written together, so a missing publication would otherwise read as pending forever.
func TestGetA2AProxyWithoutAPublicationReportsFailed(t *testing.T) {
	h := newProxyHarness(t)
	h.proxies.GetFunc = func(context.Context, string, string, string, string) (*models.A2AProxy, error) {
		return &models.A2AProxy{EnvironmentUUID: uuid.MustParse(proxyTestEnvUUID)}, nil
	}
	h.pubs.GetFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2APublication, error) {
		return nil, repositories.ErrA2APublicationNotFound
	}

	got, err := h.svc.GetA2AProxy(context.Background(), "org-1", "checkout", "weather", "dev")

	require.NoError(t, err)
	assert.Equal(t, models.A2APublicationStatusFailed, got.Status)
	assert.NotEmpty(t, got.LastError)
}

// The artifact, and the API keys bound to it, survive a switch to direct.
func TestDeleteA2AProxySwitchesOnlyThatEnvironmentToDirect(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("X-Key", "c2VjcmV0")
	// h.artifacts.DeleteFunc is nil: deleting the artifact would panic.

	require.NoError(t, h.svc.DeleteA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"))

	deployments := h.txDeployments.DeleteForArtifactCalls()
	require.Len(t, deployments, 1)
	assert.Equal(t, h.artifactUUID, deployments[0].ArtifactUUID)
	publications := h.txPubs.DeleteForAgentEnvCalls()
	require.Len(t, publications, 1)
	assert.Equal(t, uuid.MustParse(proxyTestEnvUUID), publications[0].EnvironmentUUID)
	require.Len(t, h.txProxies.DeleteCalls(), 1)
	require.Len(t, h.txCards.ClearCardCalls(), 1, "the gateway's card goes")
	require.Len(t, h.txCards.RequeueCalls(), 1, "and the origin's is fetched directly")
	require.Len(t, h.hub.published, 1, "the gateway is told once the transaction committed")
	assert.Equal(t, h.artifactUUID.String(), deletedAgentID(t, h.hub.published[0]))
}

func TestRefreshA2AProxyQueuesAPublicationNeverARequeue(t *testing.T) {
	h := newProxyHarness(t)
	h.proxied("", "")
	h.txPubs.EnqueueFunc = func(context.Context, *models.A2APublication) error { return nil }
	// No card write is wired: a Requeue would un-park the card before the gateway has the Agent.

	require.NoError(t, h.svc.RefreshA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev"))

	assert.Len(t, h.txPubs.EnqueueCalls(), 1)
}

func TestSetA2AProxyRejectsAnInternalOrNonA2AAgent(t *testing.T) {
	cases := map[string]struct {
		agent *models.AgentResponse
		want  error
	}{
		"an internal A2A agent": {
			&models.AgentResponse{Name: "weather", Provisioning: models.Provisioning{Type: "internal"}, Type: models.AgentType{SubType: "a2a-agent"}},
			utils.ErrA2AProxyAgentNotExternal,
		},
		"an external non-A2A agent": {
			&models.AgentResponse{Name: "weather", Provisioning: models.Provisioning{Type: "external"}, Type: models.AgentType{SubType: "custom"}},
			utils.ErrAgentNotA2A,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newProxyHarness(t)
			h.oc.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) { return tc.agent, nil }
			assert.ErrorIs(t, h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", nil), tc.want)
		})
	}
}

func TestSetA2AProxyEnforcesTheEnvironmentTier(t *testing.T) {
	h := newProxyHarness(t)
	h.oc.GetEnvironmentFunc = func(_ context.Context, _, name string) (*models.EnvironmentResponse, error) {
		return &models.EnvironmentResponse{UUID: proxyTestEnvUUID, Name: name, IsProduction: true}, nil
	}
	err := h.svc.SetA2AProxy(tierCtx(t, rbac.AgentEnvNonProduction), "org-1", "checkout", "weather", "prod", nil)
	assert.ErrorIs(t, err, utils.ErrForbidden)
	assert.Zero(t, h.transactions)
}

// The agent deletion that ran first could not see these writes, so the request removes them itself.
func TestSetA2AProxyDropsItsWritesWhenTheAgentIsDeletedMeanwhile(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.artifacts.DeleteFunc = func(*gorm.DB, string) error { return nil }
	lookups := 0
	h.oc.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) {
		lookups++
		if lookups == 1 {
			return externalA2AAgent(), nil
		}
		return nil, utils.ErrNotFound
	}

	err := h.svc.SetA2AProxy(tierGrantedCtx(t), "org-1", "checkout", "weather", "dev", nil)

	assert.ErrorIs(t, err, utils.ErrAgentNotFound)
	assert.Len(t, h.artifacts.DeleteCalls(), 1)
	assert.Len(t, h.txProxies.DeleteCalls(), 1)
	assert.Len(t, h.txPubs.DeleteForAgentEnvCalls(), 1)
	assert.Len(t, h.txCards.DeleteForAgentEnvCalls(), 1, "the card row the proxy service may have written goes too")
}

// The URL write, the parking and the republish are one transaction: the row is never due in between.
// With no stored secret the URL may move to another origin.
func TestOnCardSourceChangedStoresTheURLParksTheCardAndRepublishes(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("", "")
	newURL := "https://other.example/.well-known/agent-card.json"

	require.NoError(t, h.svc.OnCardSourceChanged(context.Background(), externalA2AAgent(), "org-1", "checkout", devEnv(), newURL))

	written := h.txCards.EnqueueCalls()
	require.Len(t, written, 1, "the URL is written inside the transaction, never on the plain repo")
	assert.Equal(t, newURL, written[0].Card.SourceURL)
	assert.Equal(t, models.A2AAgentCardSourceExternal, written[0].Card.Source)
	assert.Equal(t, uuid.MustParse(proxyTestEnvUUID), written[0].Card.EnvironmentUUID)
	assert.Len(t, h.txCards.ClearCardCalls(), 1)
	assert.Len(t, h.txPubs.EnqueueCalls(), 1)
	assert.Empty(t, h.txCards.RequeueCalls())
	assert.Equal(t, 1, h.transactions)
}

// The stored secret is sent to whatever the card URL names, so it may not follow the URL to another origin.
func TestOnCardSourceChangedRefusesToMoveAStoredSecretToAnotherOrigin(t *testing.T) {
	for name, newURL := range map[string]string{
		"another host":   "https://attacker.example/.well-known/agent-card.json",
		"another scheme": "http://agent.example/.well-known/agent-card.json",
		"another port":   "https://agent.example:8443/.well-known/agent-card.json",
	} {
		t.Run(name, func(t *testing.T) {
			h := newProxyHarness(t)
			h.proxied("X-Key", "c2VjcmV0")

			err := h.svc.OnCardSourceChanged(context.Background(), externalA2AAgent(), "org-1", "checkout", devEnv(), newURL)

			require.ErrorIs(t, err, utils.ErrA2AProxyOriginChangeNeedsAuthReset)
			assert.Len(t, h.txProxies.GetByEnvUUIDCalls(), 1, "the proxy is read inside the transaction")
			assert.Len(t, h.txCards.GetCalls(), 1, "the registered URL is read inside the transaction")
			assert.Empty(t, h.txCards.EnqueueCalls())
			assert.Empty(t, h.txCards.ClearCardCalls())
			assert.Empty(t, h.txPubs.EnqueueCalls())
			assert.Empty(t, h.artifacts.CreateCalls())
		})
	}
}

func TestOnCardSourceChangedRefusesWhenNoRegisteredURLVouchesForTheSecret(t *testing.T) {
	h := newProxyHarness(t)
	h.proxied("X-Key", "c2VjcmV0")
	h.cards.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return nil, repositories.ErrA2AAgentCardNotFound
	}

	err := h.svc.OnCardSourceChanged(context.Background(), externalA2AAgent(), "org-1", "checkout", devEnv(),
		"https://agent.example/.well-known/agent-card.json")

	require.ErrorIs(t, err, utils.ErrA2AProxyOriginChangeNeedsAuthReset)
	assert.Empty(t, h.txCards.EnqueueCalls())
}

func TestOnCardSourceChangedKeepsTheSecretOnTheSameOrigin(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("X-Key", "c2VjcmV0")
	newURL := "https://AGENT.example:443/v2/.well-known/agent-card.json"

	require.NoError(t, h.svc.OnCardSourceChanged(context.Background(), externalA2AAgent(), "org-1", "checkout", devEnv(), newURL))

	written := h.txCards.EnqueueCalls()
	require.Len(t, written, 1)
	assert.Equal(t, newURL, written[0].Card.SourceURL)
	assert.Len(t, h.txPubs.EnqueueCalls(), 1)
}

func TestOnCardSourceRemovedTearsDownAndDeletesTheCard(t *testing.T) {
	h := newProxyHarness(t)
	h.acceptsWrites()
	h.proxied("X-Key", "c2VjcmV0")

	require.NoError(t, h.svc.OnCardSourceRemoved(auditableCtx(t), externalA2AAgent(), "org-1", "checkout", devEnv()))

	assert.Len(t, h.txCards.DeleteForAgentEnvCalls(), 1)
	assert.Empty(t, h.txCards.RequeueCalls())
	assert.Len(t, h.txProxies.DeleteCalls(), 1)
	assert.ErrorIs(t, h.svc.OnCardSourceRemoved(context.Background(), externalA2AAgent(), "org-1", "checkout", devEnv()),
		audit.ErrRecorderUnavailable, "it discards the stored secret, so it is refused unrecorded")
}

func TestIsProxied(t *testing.T) {
	h := newProxyHarness(t)
	proxied, err := h.svc.IsProxied(context.Background(), "org-1", "checkout", "weather", uuid.MustParse(proxyTestEnvUUID))
	require.NoError(t, err)
	assert.False(t, proxied)

	h.proxied("", "")
	proxied, err = h.svc.IsProxied(context.Background(), "org-1", "checkout", "weather", uuid.MustParse(proxyTestEnvUUID))
	require.NoError(t, err)
	assert.True(t, proxied)
}

// failingRecorder is an audit sink outage: RecordSync fails with an error that does not name the recorder.
type failingRecorder struct{ audit.Recorder }

func (failingRecorder) RecordSync(context.Context, audit.Event) error { return assert.AnError }

func sinkOutageCtx() context.Context {
	return jwtassertion.ContextWithTokenClaimsAndScope(
		audit.WithRecorder(context.Background(), failingRecorder{audit.NewNoopRecorder()}),
		&jwtassertion.TokenClaims{Scope: audit.ScopesOf([]rbac.Permission{rbac.AgentEnvNonProduction, rbac.AgentEnvProduction})},
	)
}

// A real sink failure is the documented 503, not a 500.
func TestA2AProxyWritesRefuseWhenTheAuditSinkFails(t *testing.T) {
	calls := map[string]func(h *proxyHarness) error{
		"set": func(h *proxyHarness) error {
			return h.svc.SetA2AProxy(sinkOutageCtx(), "org-1", "checkout", "weather", "dev", nil)
		},
		"delete": func(h *proxyHarness) error {
			h.proxied("X-Key", "c2VjcmV0")
			return h.svc.DeleteA2AProxy(sinkOutageCtx(), "org-1", "checkout", "weather", "dev")
		},
		"card source removed": func(h *proxyHarness) error {
			h.proxied("X-Key", "c2VjcmV0")
			return h.svc.OnCardSourceRemoved(sinkOutageCtx(), externalA2AAgent(), "org-1", "checkout", devEnv())
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			h := newProxyHarness(t)

			err := call(h)

			assert.ErrorIs(t, err, audit.ErrRecorderUnavailable)
			assert.ErrorIs(t, err, assert.AnError)
			assert.Zero(t, h.transactions, "nothing is written")
		})
	}
}
