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
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/orgctx"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// sentinelTx hands fn the shared transaction sentinel; publisherMocks.tx adds a counter and an injected failure on top.
func sentinelTx(_ context.Context, fn func(tx *gorm.DB) error) error { return fn(txSentinel) }

// testPublication is an A2A agent's publication in one environment, as a test sets it up.
type testPublication struct {
	a2aPublication
	id           uuid.UUID
	attemptCount int
}

func pendingPublication() testPublication {
	return testPublication{
		a2aPublication: a2aPublication{
			OUID:            "org-1",
			ProjectName:     "checkout",
			AgentName:       "trip-planner",
			EnvironmentName: "dev",
			EnvironmentUUID: uuid.New(),
			ArtifactUUID:    uuid.New(),
			Source:          models.A2APublicationSourcePlatform,
		},
		id: uuid.New(),
	}
}

// intent is the publication's pending deploy intent.
func (p testPublication) intent() models.DeployIntent {
	intent, err := a2aIntent(p.key(), p.Source, p.ArtifactUUID)
	if err != nil {
		panic(err)
	}
	intent.ID, intent.State, intent.AttemptCount = p.id, models.DeployIntentPending, p.attemptCount
	intent.UpdatedAt = time.Date(2026, 9, 24, 10, 0, 0, 123456000, time.UTC)
	return intent
}

// a2aKindHarness carries A2A publications out through the reconcile core over mocks, so each test can assert on
// what an attempt did rather than only on what it returned.
type a2aKindHarness struct {
	kind            *A2AAgentKind
	core            *reconcile.Core
	cfg             reconcile.Config
	intents         *repomocks.DeployIntentRepositoryMock
	gatewayRepo     *repomocks.GatewayRepositoryMock
	agentConfigRepo *repomocks.AgentConfigRepositoryMock
	hub             *recordingEventHub
	cardRepo        *repomocks.A2AAgentCardRepositoryMock
	proxyRepo       *repomocks.A2AProxyRepositoryMock
	deploymentRepo  *repomocks.DeploymentRepositoryMock
	ocClient        *clientmocks.OpenChoreoClientMock
	fetcher         *fakeA2ACardFetcher
	logs            *bytes.Buffer
	upstreamSecret  string
}

// newA2AKindHarness wires an A2A kind whose every dependency succeeds and whose binding reports serviceURL. Tests
// narrow it to the condition they are about.
func newA2AKindHarness(serviceURL string) *a2aKindHarness {
	h := &a2aKindHarness{hub: &recordingEventHub{}, fetcher: &fakeA2ACardFetcher{}, logs: &bytes.Buffer{}, cfg: reconcile.DefaultConfig()}
	h.intents = &repomocks.DeployIntentRepositoryMock{
		MarkDoneFunc: func(_ context.Context, read models.DeployIntent) (models.DeployIntent, error) {
			read.State = models.DeployIntentDone
			return read, nil
		},
		MarkPublishedFunc:     func(context.Context, models.DeployIntent) error { return nil },
		MarkAttemptFailedFunc: func(context.Context, models.DeployIntent, string, time.Time) error { return nil },
		MarkWaitingFunc:       func(context.Context, models.DeployIntent, string, time.Time) error { return nil },
		MarkFailedFunc:        func(context.Context, models.DeployIntent, string) error { return nil },
		GetFunc: func(context.Context, uuid.UUID) (*models.DeployIntent, error) {
			return &models.DeployIntent{}, nil
		},
	}
	h.intents.WithTxFunc = func(*gorm.DB) repositories.DeployIntentRepository { return h.intents }
	h.cardRepo = &repomocks.A2AAgentCardRepositoryMock{
		EnqueueFunc: func(context.Context, *models.A2AAgentCard) error { return nil },
		GetFunc: func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
			return nil, repositories.ErrA2AAgentCardNotFound
		},
	}
	h.deploymentRepo = &repomocks.DeploymentRepositoryMock{CreateWithLimitEnforcementFunc: storesDeployment}
	h.deploymentRepo.WithTxFunc = func(*gorm.DB) repositories.DeploymentRepository { return h.deploymentRepo }
	h.ocClient = &clientmocks.OpenChoreoClientMock{
		GetReleaseBindingServiceURLFunc: func(context.Context, string, string, string) (string, error) {
			return serviceURL, nil
		},
		GetComponentFunc: func(_ context.Context, _, _, name string) (*models.AgentResponse, error) {
			return &models.AgentResponse{Name: name}, nil
		},
		GetReleaseBindingRolloutFunc: func(context.Context, string, string, string) (client.ReleaseBindingRollout, error) {
			return client.ReleaseBindingRollout{ServiceURL: serviceURL, ReleaseName: "trip-planner-r2", Serving: true}, nil
		},
	}
	h.gatewayRepo = &repomocks.GatewayRepositoryMock{
		ListWithFiltersFunc: func(repositories.GatewayFilterOptions) ([]*models.Gateway, error) {
			return []*models.Gateway{{
				UUID: uuid.New(), Name: "ingress-dev", Vhost: "agents.example.com",
				GatewayFunctionalityType: models.GatewayRoleIngress,
			}}, nil
		},
	}
	h.agentConfigRepo = &repomocks.AgentConfigRepositoryMock{
		GetFunc: func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
			return &models.AgentConfig{EnableApiKeySecurity: true, CORSEnabled: true}, nil
		},
	}
	h.proxyRepo = &repomocks.A2AProxyRepositoryMock{
		LockAgentEnvFunc: func(context.Context, string, string, string, uuid.UUID) error { return nil },
	}
	// The reads happen inside the transaction; the tx-bound repositories are the mocks themselves.
	h.proxyRepo.WithTxFunc = func(*gorm.DB) repositories.A2AProxyRepository { return h.proxyRepo }
	h.cardRepo.WithTxFunc = func(*gorm.DB) repositories.A2AAgentCardRepository { return h.cardRepo }
	h.kind = NewA2AAgentKind(sentinelTx, h.cardRepo, h.proxyRepo, h.deploymentRepo, h.gatewayRepo, h.agentConfigRepo,
		h.ocClient, h.fetcher, proxyTestKey, testLogger())
	h.rebuild()
	return h
}

// rebuild makes a new core over the harness's kind and mocks with h.cfg, logging to h.logs.
func (h *a2aKindHarness) rebuild() {
	h.core = reconcile.NewCore(reconcile.TxRunner(sentinelTx), h.intents, h.deploymentRepo, NewGatewayEventsService(h.hub),
		h.cfg, slog.New(slog.NewTextHandler(h.logs, nil)), h.kind)
}

// process runs one attempt at the publication's pending intent.
func (h *a2aKindHarness) process(p testPublication) {
	h.core.Process(context.Background(), p.intent())
}

func (h *a2aKindHarness) withoutIngressGateway() {
	h.gatewayRepo.ListWithFiltersFunc = func(repositories.GatewayFilterOptions) ([]*models.Gateway, error) {
		return []*models.Gateway{}, nil
	}
}

func (h *a2aKindHarness) withAgentConfig(cfg *models.AgentConfig, err error) {
	h.agentConfigRepo.GetFunc = func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
		return cfg, err
	}
}

// The whole reason publications wait: status is populated only after the binding reconciles, so an early attempt
// must wait rather than emit an Agent that routes nowhere.
func TestA2AKindWaitsWhenServiceURLIsNotReadyYet(t *testing.T) {
	h := newA2AKindHarness("")

	h.process(pendingPublication())

	assert.Empty(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(),
		"nothing is published until the binding reports an upstream")
	assert.Empty(t, h.hub.published, "and no gateway is told about it")
	waits := h.intents.MarkWaitingCalls()
	require.Len(t, waits, 1)
	assert.Equal(t, "release binding has not published a service URL yet", waits[0].Reason,
		"the status reads DEPLOYING with what it waits for")
	assert.True(t, waits[0].NextAttemptAt.After(time.Now()), "the retry is scheduled forward")
	assert.Empty(t, h.intents.MarkAttemptFailedCalls(), "waiting is not charged an attempt")
	assert.Empty(t, h.intents.MarkFailedCalls())
}

// A first source build can take far longer than the attempt budget; waiting on it must never fail the intent.
func TestA2AKindNeverGivesUpWaitingForTheFirstBinding(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	pub.attemptCount = h.cfg.AttemptBudget - 1

	h.process(pub)

	assert.Len(t, h.intents.MarkWaitingCalls(), 1)
	assert.Empty(t, h.intents.MarkFailedCalls())
}

// A retry must be due by the next tick, not land just after it and skip one.
func TestA2AKindRetryLandsOnTheNextTick(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	h.withoutIngressGateway()

	before := time.Now()
	h.process(pendingPublication())

	retries := h.intents.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.Equal(t, "no ingress gateway is mapped to this environment yet", retries[0].LastErr)
	assert.True(t, retries[0].NextAttemptAt.Before(before.Add(h.cfg.TickInterval)), "due before the next tick fires")
}

// Past the budget a real failure (here: no ingress gateway) is called failed rather than retried forever.
func TestA2AKindGivesUpPastTheAttemptBudget(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	h.withoutIngressGateway()
	exhausted := pendingPublication()
	exhausted.attemptCount = h.cfg.AttemptBudget - 1

	h.process(exhausted)

	require.Len(t, h.intents.MarkFailedCalls(), 1)
	assert.Empty(t, h.intents.MarkAttemptFailedCalls(), "the retry cycle has ended")
	assert.Empty(t, h.deploymentRepo.CreateWithLimitEnforcementCalls())
}

// The ingress-role filter belongs in the query, scoped to the org.
func TestA2AKindQueriesOnlyIngressGatewaysOfTheOrg(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	pub := pendingPublication()

	h.process(pub)

	calls := h.gatewayRepo.ListWithFiltersCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, pub.OUID, calls[0].Filters.OrganizationID)
	assert.Equal(t, models.IngressGatewayRoles, calls[0].Filters.FunctionalityTypeIn)
	require.NotNil(t, calls[0].Filters.EnvironmentID)
	assert.Equal(t, pub.EnvironmentUUID.String(), *calls[0].Filters.EnvironmentID)
}

// The happy path: a ready binding produces a DEPLOYING deployments row carrying the Agent YAML and an agent.deployed
// event keyed on the artifact UUID, carrying the row's id and performed_at.
func TestA2AKindPublishesOnceServiceURLIsAvailable(t *testing.T) {
	const upstream = "http://trip-planner.dp-default:9099"
	h := newA2AKindHarness(upstream)
	pub := pendingPublication()

	h.process(pub)

	created := h.deploymentRepo.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	assert.Equal(t, pub.ArtifactUUID, created[0].Deployment.ArtifactUUID)
	assert.Equal(t, "trip-planner-deployment", created[0].Deployment.Name)
	assert.Nil(t, created[0].Deployment.Status, "the repository starts it DEPLOYING; only the gateway's ack makes it DEPLOYED")
	var published A2AAgentDeploymentYAML
	require.NoError(t, yaml.Unmarshal(created[0].Deployment.Content, &published))
	assert.Equal(t, kindA2AAgent, published.Kind)
	assert.Equal(t, upstream, published.Spec.Upstream.URL)

	require.Len(t, h.hub.published, 1)
	evt := h.hub.published[0]
	assert.Equal(t, "agent.deployed", string(evt.EventType))
	assert.Equal(t, pub.ArtifactUUID.String(), evt.EntityID, "the gateway fetches /agents/{agentId} by the artifact UUID")
	var envelope struct {
		Payload struct {
			DeploymentID string    `json:"deploymentId"`
			PerformedAt  time.Time `json:"performedAt"`
		} `json:"payload"`
	}
	require.NoError(t, json.Unmarshal([]byte(evt.EventData), &envelope))
	assert.Equal(t, created[0].Deployment.DeploymentID.String(), envelope.Payload.DeploymentID,
		"the event points at the row the gateway will fetch")
	assert.True(t, created[0].Deployment.PerformedAt.Equal(envelope.Payload.PerformedAt))

	done := h.intents.MarkDoneCalls()
	require.Len(t, done, 1)
	assert.Equal(t, pub.intent(), done[0].Read, "the intent is marked as it was read, so a re-enqueue meanwhile is not swallowed")
	assert.Len(t, h.intents.MarkPublishedCalls(), 1)
	assert.Empty(t, h.intents.MarkAttemptFailedCalls())
	assert.Empty(t, h.cardRepo.EnqueueCalls(), "the card is fetched once the gateway acks, not on publish")
}

// The kind is the only place an A2A agent's gateway resource is built, so it is where A2A-Version is guaranteed and
// the card's CORS is attached.
func TestA2AKindPublishesA2AVersionAndInheritedCardCORS(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	h.withAgentConfig(&models.AgentConfig{
		CORSEnabled:      true,
		CORSAllowOrigins: []string{"https://client.example"},
		CORSAllowMethods: []string{"GET", "POST", "OPTIONS"},
		CORSAllowHeaders: []string{"Content-Type"},
	}, nil)

	h.process(pendingPublication())

	created := h.deploymentRepo.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	var published A2AAgentDeploymentYAML
	require.NoError(t, yaml.Unmarshal(created[0].Deployment.Content, &published))
	opCORS := published.Spec.A2A.OperationConfigs.Policies[0]
	assert.Equal(t, "cors", opCORS["name"])
	assert.Contains(t, opCORS["params"].(map[string]interface{})["allowedHeaders"], "A2A-Version")
	require.NotNil(t, published.Spec.A2A.AgentCard, "an inherited card follows enabled agent CORS")
	cardCORS := published.Spec.A2A.AgentCard.Public.Policies[0]["params"].(map[string]interface{})
	assert.Equal(t, []interface{}{"https://client.example"}, cardCORS["allowedOrigins"])
	assert.Equal(t, []interface{}{"GET", "OPTIONS"}, cardCORS["allowedMethods"])
}

// The worker runs off the request path, so no middleware resolves an org for it. The OpenChoreo client stamps
// X-Impersonate-Org only from the context's resolved org, and a platform that requires the header rejects the lookup.
func TestA2AKindLooksUpTheBindingAsTheIntentsOrg(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")

	h.process(pendingPublication())

	lookups := h.ocClient.GetReleaseBindingServiceURLCalls()
	require.Len(t, lookups, 1)
	org, ok := orgctx.GetResolvedOrg(lookups[0].Ctx)
	require.True(t, ok, "the lookup context carries a resolved org")
	assert.Equal(t, "org-1", org.OUID)
}

// withDeployedUpstream makes the artifact's current deployment one rendered with upstreamURL.
func (h *a2aKindHarness) withDeployedUpstream(t *testing.T, upstreamURL string) {
	t.Helper()
	content, err := generateA2AAgentDeploymentYAML(A2AAgentDeploymentInput{
		ArtifactName: "trip-planner-dev", DisplayName: "trip-planner", AgentName: "trip-planner", UpstreamURL: upstreamURL,
	})
	require.NoError(t, err)
	h.deploymentRepo.GetDeployedGatewaysByProviderFunc = func(uuid.UUID, string) ([]string, error) {
		return []string{"gw-1"}, nil
	}
	h.deploymentRepo.GetCurrentByGatewayFunc = func(string, string, string) (*models.Deployment, error) {
		return &models.Deployment{DeploymentID: uuid.New(), Content: []byte(content)}, nil
	}
}

// withCard makes the drift check find a platform card row in the given state.
func (h *a2aKindHarness) withCard(status models.A2AAgentCardStatus, releaseName string) {
	h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{Source: models.A2AAgentCardSourcePlatform, Status: status, ReleaseName: releaseName}, nil
	}
}

// A new release with a changed port moves the Service; the gateway must follow.
func TestA2AKindDriftsWhenTheUpstreamMoved(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:8080")
	h.withDeployedUpstream(t, "http://trip-planner.dp-default:9099")

	reason, drifted := h.kind.Drifted(context.Background(), pendingPublication().intent())

	assert.True(t, drifted)
	assert.Contains(t, reason, "http://trip-planner.dp-default:8080")
}

func TestA2AKindLeavesAMatchingUpstreamAlone(t *testing.T) {
	const upstream = "http://trip-planner.dp-default:9099"
	h := newA2AKindHarness(upstream)
	h.withDeployedUpstream(t, upstream)

	_, drifted := h.kind.Drifted(context.Background(), pendingPublication().intent())

	assert.False(t, drifted)
}

// An unreadable or empty binding URL is not evidence of drift; redeploying would only park a working agent in the
// waiting state. Nor is a deployment AMS cannot read, or an external origin, whose routes every publish re-derives.
func TestA2AKindIgnoresAnUnknownUpstream(t *testing.T) {
	cases := map[string]func(h *a2aKindHarness){
		"empty binding URL": func(h *a2aKindHarness) {
			h.ocClient.GetReleaseBindingRolloutFunc = func(context.Context, string, string, string) (client.ReleaseBindingRollout, error) {
				return client.ReleaseBindingRollout{}, nil
			}
		},
		"unreadable binding": func(h *a2aKindHarness) {
			h.ocClient.GetReleaseBindingRolloutFunc = func(context.Context, string, string, string) (client.ReleaseBindingRollout, error) {
				return client.ReleaseBindingRollout{}, assert.AnError
			}
		},
		"no current deployment": func(h *a2aKindHarness) {
			h.deploymentRepo.GetCurrentByGatewayFunc = func(string, string, string) (*models.Deployment, error) {
				return nil, nil //nolint:nilnil // no current deployment
			}
		},
		"unparseable deployment": func(h *a2aKindHarness) {
			h.deploymentRepo.GetCurrentByGatewayFunc = func(string, string, string) (*models.Deployment, error) {
				return &models.Deployment{Content: []byte(":\tnot yaml")}, nil
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newA2AKindHarness("http://trip-planner.dp-default:8080")
			h.withDeployedUpstream(t, "http://trip-planner.dp-default:9099")
			setup(h)

			_, drifted := h.kind.Drifted(context.Background(), pendingPublication().intent())

			assert.False(t, drifted)
		})
	}
	t.Run("external", func(t *testing.T) {
		h := newA2AKindHarness("http://trip-planner.dp-default:8080")
		pub := pendingPublication()
		pub.Source = models.A2APublicationSourceExternal

		_, drifted := h.kind.Drifted(context.Background(), pub.intent())

		assert.False(t, drifted)
		assert.Empty(t, h.ocClient.GetReleaseBindingRolloutCalls())
	})
}

// The worker checks each intent's drift as that intent's org, and redeploys a drifted one at once.
func TestA2AKindDriftIsCheckedAsEachIntentsOrgAndRedeployed(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:8080")
	h.withDeployedUpstream(t, "http://trip-planner.dp-default:9099")
	first, second := pendingPublication(), pendingPublication()
	second.OUID = "org-2"
	h.intents.ClaimDueFunc = func(context.Context, time.Time, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ClaimUnackedFunc = func(context.Context, time.Time, time.Time, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ClaimLiveFunc = func(context.Context, []string, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ListDoneFunc = func(context.Context, string, uuid.UUID, int) ([]models.DeployIntent, error) {
		return []models.DeployIntent{first.intent(), second.intent()}, nil
	}
	h.intents.RequeueFunc = func(context.Context, models.DeployIntent) error { return nil }
	h.intents.GetFunc = func(_ context.Context, id uuid.UUID) (*models.DeployIntent, error) {
		for _, p := range []testPublication{first, second} {
			if p.id == id {
				intent := p.intent()
				return &intent, nil
			}
		}
		return nil, repositories.ErrDeployIntentNotFound
	}

	reconcile.NewWorker(h.core).RunOnce(context.Background())

	lookups := h.ocClient.GetReleaseBindingRolloutCalls()
	require.Len(t, lookups, 2)
	for i, want := range []string{"org-1", "org-2"} {
		org, ok := orgctx.GetResolvedOrg(lookups[i].Ctx)
		require.True(t, ok, "lookup %d carries a resolved org", i)
		assert.Equal(t, want, org.OUID)
	}
	assert.Len(t, h.intents.RequeueCalls(), 2)
	assert.Len(t, h.deploymentRepo.CreateWithLimitEnforcementCalls(), 2, "each drifted agent is redeployed with its new upstream")
}

// The gateway serves the card only once it holds the Agent, so its ack is the card's trigger.
func TestA2AKindQueuesAPlatformCardFetchOnceLive(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	pub := pendingPublication()

	h.kind.AfterLive(context.Background(), pub.intent(), uuid.New(), uuid.New())

	queued := h.cardRepo.EnqueueCalls()
	require.Len(t, queued, 1)
	got := queued[0].Card
	assert.Equal(t, pub.OUID, got.OUID)
	assert.Equal(t, pub.ProjectName, got.ProjectName)
	assert.Equal(t, pub.AgentName, got.AgentName)
	assert.Equal(t, pub.EnvironmentName, got.EnvironmentName)
	assert.Equal(t, pub.EnvironmentUUID, got.EnvironmentUUID)
	assert.Equal(t, models.A2AAgentCardSourcePlatform, got.Source)
}

// The worker runs AfterLive for an acked deployment.
func TestA2AKindCardIsQueuedByTheWorkerOnceTheGatewayAcks(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	pub := pendingPublication()
	live := uuid.New()
	acked := pub.intent()
	acked.LiveDeploymentID = &live
	h.intents.ClaimDueFunc = func(context.Context, time.Time, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ClaimUnackedFunc = func(context.Context, time.Time, time.Time, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ListDoneFunc = func(context.Context, string, uuid.UUID, int) ([]models.DeployIntent, error) { return nil, nil }
	h.intents.ClaimLiveFunc = func(_ context.Context, kinds []string, _ int) ([]models.DeployIntent, error) {
		assert.Equal(t, []string{models.KindAgent}, kinds)
		return []models.DeployIntent{acked}, nil
	}
	h.intents.StatusRowsFunc = func(context.Context, models.DeployIntent) ([]models.DeploymentStatusRecord, error) {
		return []models.DeploymentStatusRecord{{DeploymentID: live, GatewayUUID: uuid.New(), Status: models.DeploymentStatusDeployed}}, nil
	}

	reconcile.NewWorker(h.core).RunOnce(context.Background())

	require.Len(t, h.cardRepo.EnqueueCalls(), 1)
}

// Best effort: the agent is live; a missed card row is recovered by the next deploy or a refresh.
func TestA2AKindSurvivesAFailedCardQueue(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	h.cardRepo.EnqueueFunc = func(context.Context, *models.A2AAgentCard) error { return assert.AnError }

	h.kind.AfterLive(context.Background(), pendingPublication().intent(), uuid.New(), uuid.New())

	assert.Empty(t, h.ocClient.GetComponentCalls(), "nothing was written, so nothing to drop")
}

// A delete that clears the card rows between the ack and the enqueue must not leave an orphan.
func TestA2AKindDropsTheCardRowWhenTheAgentWasDeletedMeanwhile(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	h.ocClient.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) {
		return nil, utils.ErrNotFound
	}
	h.cardRepo.DeleteForAgentEnvFunc = func(context.Context, string, string, string, string) error { return nil }
	pub := pendingPublication()

	h.kind.AfterLive(context.Background(), pub.intent(), uuid.New(), uuid.New())

	require.Len(t, h.cardRepo.EnqueueCalls(), 1)
	deleted := h.cardRepo.DeleteForAgentEnvCalls()
	require.Len(t, deleted, 1)
	assert.Equal(t, pub.AgentName, deleted[0].AgentName)
	assert.Equal(t, pub.EnvironmentName, deleted[0].EnvironmentName)
}

func TestA2AKindCardRowAndTheAgentItWasQueuedFor(t *testing.T) {
	queuedFor := uuid.New()
	cases := []struct {
		name       string
		current    func() (*models.AgentResponse, error)
		recordedID *uuid.UUID
		dropped    bool
	}{
		{"the lookup fails", func() (*models.AgentResponse, error) { return nil, assert.AnError }, &queuedFor, false},
		{"the agent was replaced", func() (*models.AgentResponse, error) {
			return &models.AgentResponse{UUID: uuid.NewString()}, nil
		}, &queuedFor, true},
		{"the same agent", func() (*models.AgentResponse, error) {
			return &models.AgentResponse{UUID: queuedFor.String()}, nil
		}, &queuedFor, false},
		{"the agent's UUID is missing", func() (*models.AgentResponse, error) {
			return &models.AgentResponse{}, nil
		}, &queuedFor, false},
		{"an unrecorded agent that still exists", func() (*models.AgentResponse, error) {
			return &models.AgentResponse{UUID: uuid.NewString()}, nil
		}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newA2AKindHarness("http://trip-planner.dp-default:9099")
			h.ocClient.GetComponentFunc = func(context.Context, string, string, string) (*models.AgentResponse, error) {
				return tc.current()
			}
			h.cardRepo.DeleteForAgentEnvFunc = func(context.Context, string, string, string, string) error { return nil }
			pub := pendingPublication()
			pub.AgentUUID = tc.recordedID

			h.kind.AfterLive(context.Background(), pub.intent(), uuid.New(), uuid.New())

			assert.Equal(t, tc.dropped, len(h.cardRepo.DeleteForAgentEnvCalls()) == 1)
		})
	}
}

// A rebuild or redeploy moves the binding to a new release without telling AMS; the card must follow.
func TestA2AKindRefetchesACardFromAnOlderRelease(t *testing.T) {
	const upstream = "http://trip-planner.dp-default:9099"
	for _, status := range []models.A2AAgentCardStatus{models.A2AAgentCardStatusFetched, models.A2AAgentCardStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			h := newA2AKindHarness(upstream)
			h.withDeployedUpstream(t, upstream)
			h.withCard(status, "trip-planner-r1")
			pub := pendingPublication()

			_, drifted := h.kind.Drifted(context.Background(), pub.intent())

			assert.False(t, drifted)
			queued := h.cardRepo.EnqueueCalls()
			require.Len(t, queued, 1)
			assert.Equal(t, pub.AgentName, queued[0].Card.AgentName)
			assert.Equal(t, pub.EnvironmentName, queued[0].Card.EnvironmentName)
			assert.Equal(t, pub.EnvironmentUUID, queued[0].Card.EnvironmentUUID)
			assert.Equal(t, models.A2AAgentCardSourcePlatform, queued[0].Card.Source)
		})
	}
}

func TestA2AKindLeavesACardAlone(t *testing.T) {
	const upstream = "http://trip-planner.dp-default:9099"
	cases := map[string]func(h *a2aKindHarness){
		"from the serving release": func(h *a2aKindHarness) { h.withCard(models.A2AAgentCardStatusFetched, "trip-planner-r2") },
		"already being fetched":    func(h *a2aKindHarness) { h.withCard(models.A2AAgentCardStatusPending, "trip-planner-r1") },
		"with no row":              func(h *a2aKindHarness) {},
		"while the new release is still rolling out": func(h *a2aKindHarness) {
			h.withCard(models.A2AAgentCardStatusFetched, "trip-planner-r1")
			h.ocClient.GetReleaseBindingRolloutFunc = func(context.Context, string, string, string) (client.ReleaseBindingRollout, error) {
				return client.ReleaseBindingRollout{ServiceURL: upstream, ReleaseName: "trip-planner-r2"}, nil
			}
		},
		"that already failed on the serving release": func(h *a2aKindHarness) {
			h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
				return &models.A2AAgentCard{
					Source: models.A2AAgentCardSourcePlatform, Status: models.A2AAgentCardStatusFailed,
					ReleaseName: "trip-planner-r1", AttemptedReleaseName: "trip-planner-r2",
				}, nil
			}
		},
		"of an external agent": func(h *a2aKindHarness) {
			h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
				return &models.A2AAgentCard{Source: models.A2AAgentCardSourceExternal, Status: models.A2AAgentCardStatusFetched}, nil
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newA2AKindHarness(upstream)
			h.withDeployedUpstream(t, upstream)
			setup(h)

			h.kind.Drifted(context.Background(), pendingPublication().intent())

			assert.Empty(t, h.cardRepo.EnqueueCalls())
		})
	}
}

// A failed card gets one new chance per release, so a fixing redeploy still heals it.
func TestA2AKindRetriesAFailedCardOnANewRelease(t *testing.T) {
	const upstream = "http://trip-planner.dp-default:9099"
	h := newA2AKindHarness(upstream)
	h.withDeployedUpstream(t, upstream)
	h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{
			Source: models.A2AAgentCardSourcePlatform, Status: models.A2AAgentCardStatusFailed,
			AttemptedReleaseName: "trip-planner-r1",
		}, nil
	}

	h.kind.Drifted(context.Background(), pendingPublication().intent())

	require.Len(t, h.cardRepo.EnqueueCalls(), 1)
}

const originTestCardURL = "https://agent.example/.well-known/agent-card.json"

const originTestCard = `{"name":"Weather","skills":[],"supportedInterfaces":[` +
	`{"url":"https://agent.example/rpc","protocolBinding":"JSONRPC","protocolVersion":"1.0"}]}`

// withExternalOrigin makes pub an external publication whose environment is proxied with a stored secret.
func (h *a2aKindHarness) withExternalOrigin(pub *testPublication) {
	pub.Source = models.A2APublicationSourceExternal
	secret, err := utils.EncryptSecretRef("s3cret", proxyTestKey)
	if err != nil {
		panic(err)
	}
	h.upstreamSecret = secret
	h.ocClient.GetReleaseBindingServiceURLFunc = nil // an external agent has no release binding
	h.proxyRepo.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{UpstreamAuthHeader: "X-Upstream-Key", UpstreamAuthSecret: secret}, nil
	}
	h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{Source: models.A2AAgentCardSourceExternal, SourceURL: originTestCardURL}, nil
	}
	h.cardRepo.EnqueueFunc = nil // an upsert would rewrite the registered source
	h.cardRepo.RequeueFunc = func(context.Context, string, string, string, string) error { return nil }
	h.cardRepo.FailParkedFunc = func(context.Context, string, string, string, uuid.UUID, string) error { return nil }
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return json.RawMessage(originTestCard), nil
	}
}

func TestA2AKindPublishesAnExternalAgentFromItsOriginCard(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)

	h.process(pub)

	require.Len(t, h.fetcher.calls, 1)
	assert.Equal(t, fakeFetchCall{
		URL: originTestCardURL, Guarded: true,
		Header: &A2ACardFetchHeader{Name: "X-Upstream-Key", Value: "s3cret"},
	}, h.fetcher.calls[0], "the origin is SSRF-checked and sent the stored credential")
	created := h.deploymentRepo.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	var doc A2AAgentDeploymentYAML
	require.NoError(t, yaml.Unmarshal(created[0].Deployment.Content, &doc))
	assert.Equal(t, "https://agent.example", doc.Spec.Upstream.URL)
	assert.Equal(t, []A2ATransport{{ProtocolBinding: "JSONRPC", PathPrefix: "/rpc"}}, doc.Spec.A2A.OperationConfigs.Transports)
	require.NotNil(t, doc.Spec.Upstream.Auth)
	assert.Equal(t, "X-Upstream-Key", *doc.Spec.Upstream.Auth.Header)
	assert.Equal(t, h.upstreamSecret, *doc.Spec.Upstream.Auth.SecretRef)
	assert.Len(t, h.intents.MarkPublishedCalls(), 1)

	h.kind.AfterLive(context.Background(), pub.intent(), uuid.New(), uuid.New())
	requeued := h.cardRepo.RequeueCalls()
	require.Len(t, requeued, 1, "once live, the parked card row is made due in place")
	assert.Equal(t, pub.EnvironmentName, requeued[0].EnvironmentName)
}

// oauthForwardingConfig is an environment config with OAuth on and the caller's token forwarded upstream.
func oauthForwardingConfig() *models.AgentConfig {
	return &models.AgentConfig{EnableOAuthSecurity: true, OAuthIssuers: []string{"idp"}, OAuthForwardToken: true}
}

// publishedJWTAuthParams returns the params of the jwt-auth policy in the one Agent the harness published.
func publishedJWTAuthParams(t *testing.T, h *a2aKindHarness) map[string]interface{} {
	t.Helper()
	created := h.deploymentRepo.CreateWithLimitEnforcementCalls()
	require.Len(t, created, 1)
	var doc A2AAgentDeploymentYAML
	require.NoError(t, yaml.Unmarshal(created[0].Deployment.Content, &doc))
	for _, p := range doc.Spec.A2A.OperationConfigs.Policies {
		if p["name"] == client.OAuthPolicyName {
			return p["params"].(map[string]interface{})
		}
	}
	require.FailNow(t, "no jwt-auth policy was published")
	return nil
}

func TestA2AKindNeverForwardsTheCallersTokenToAnExternalOrigin(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.withAgentConfig(oauthForwardingConfig(), nil)

	h.process(pub)

	assert.Equal(t, false, publishedJWTAuthParams(t, h)["forwardToken"], "a third-party origin never sees the caller's token")
}

func TestA2AKindForwardsTheCallersTokenToAPlatformAgentWhenConfigured(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	h.withAgentConfig(oauthForwardingConfig(), nil)

	h.process(pendingPublication())

	assert.Equal(t, true, publishedJWTAuthParams(t, h)["forwardToken"])
}

func TestA2AKindFailsAnUnproxyableExternalAgentAtOnce(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.cardRepo.GetFunc = func(context.Context, string, string, string, string) (*models.A2AAgentCard, error) {
		return &models.A2AAgentCard{Source: models.A2AAgentCardSourceExternal, SourceURL: "https://agent.example/card.json"}, nil
	}

	h.process(pub)

	failed := h.intents.MarkFailedCalls()
	require.Len(t, failed, 1, "no attempt budget is spent on a card that can never be fronted")
	assert.Contains(t, failed[0].LastErr, "/.well-known/agent-card.json")
	assert.Empty(t, h.intents.MarkAttemptFailedCalls())
	assert.Empty(t, h.fetcher.calls, "a URL the gateway cannot fetch from is rejected before any network call")
	assert.Empty(t, h.deploymentRepo.CreateWithLimitEnforcementCalls())
	cardFailed := h.cardRepo.FailParkedCalls()
	require.Len(t, cardFailed, 1, "the parked card says why it will not arrive instead of staying pending")
	assert.Contains(t, cardFailed[0].LastErr, "/.well-known/agent-card.json")
	assert.Equal(t, pub.EnvironmentUUID, cardFailed[0].EnvironmentUUID)
	assert.Empty(t, h.cardRepo.RequeueCalls())
}

// The proxy service clears a credential and moves a card URL under the environment's lock, in separate
// transactions; reading the two rows under that same lock keeps an attempt from pairing the old secret
// with the new origin.
func TestA2AKindReadsTheProxyAndTheCardUnderTheEnvironmentLock(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	var steps []string
	h.proxyRepo.LockAgentEnvFunc = func(_ context.Context, _, _, _ string, envUUID uuid.UUID) error {
		assert.Equal(t, pub.EnvironmentUUID, envUUID)
		steps = append(steps, "lock")
		return nil
	}
	h.proxyRepo.WithTxFunc = func(tx *gorm.DB) repositories.A2AProxyRepository {
		assert.Same(t, txSentinel, tx)
		steps = append(steps, "proxies-in-tx")
		return h.proxyRepo
	}
	h.cardRepo.WithTxFunc = func(tx *gorm.DB) repositories.A2AAgentCardRepository {
		assert.Same(t, txSentinel, tx)
		steps = append(steps, "cards-in-tx")
		return h.cardRepo
	}

	h.process(pub)

	assert.Equal(t, []string{"proxies-in-tx", "lock", "cards-in-tx"}, steps)
	require.Len(t, h.fetcher.calls, 1, "the fetch happens after the transaction released the lock")
}

func TestA2AKindRetriesWhenTheEnvironmentLockCannotBeTaken(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.proxyRepo.LockAgentEnvFunc = func(context.Context, string, string, string, uuid.UUID) error { return assert.AnError }

	h.process(pub)

	assert.Empty(t, h.fetcher.calls)
	require.Len(t, h.intents.MarkAttemptFailedCalls(), 1)
}

func TestA2AKindFailsAnExternalAgentWhoseProxyIsGone(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.proxyRepo.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return nil, repositories.ErrA2AProxyNotFound
	}

	h.process(pub)

	require.Len(t, h.intents.MarkFailedCalls(), 1)
	assert.Empty(t, h.fetcher.calls)
}

// The third-party origin is not called for a publish a local lookup already rules out.
func TestA2AKindFetchesTheOriginOnlyAfterTheLocalLookups(t *testing.T) {
	t.Run("no ingress gateway", func(t *testing.T) {
		h := newA2AKindHarness("")
		pub := pendingPublication()
		h.withExternalOrigin(&pub)
		h.withoutIngressGateway()

		h.process(pub)

		assert.Empty(t, h.fetcher.calls)
		retries := h.intents.MarkAttemptFailedCalls()
		require.Len(t, retries, 1)
		assert.Contains(t, retries[0].LastErr, "no ingress gateway")
	})
	t.Run("unreadable agent config", func(t *testing.T) {
		h := newA2AKindHarness("")
		pub := pendingPublication()
		h.withExternalOrigin(&pub)
		h.withAgentConfig(nil, assert.AnError)

		h.process(pub)

		assert.Empty(t, h.fetcher.calls)
		require.Len(t, h.intents.MarkAttemptFailedCalls(), 1)
	})
}

func TestA2AKindRetriesWhileTheOriginIsUnreachable(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, newA2APublicError("could not reach the agent card URL", nil)
	}

	h.process(pub)

	require.Len(t, h.intents.MarkAttemptFailedCalls(), 1)
	assert.Empty(t, h.intents.MarkFailedCalls())
}

// A switch to direct committed while this attempt was publishing.
func TestA2AKindWithdrawsAnAgentTornDownMidPublish(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.intents.GetFunc = func(context.Context, uuid.UUID) (*models.DeployIntent, error) {
		return nil, repositories.ErrDeployIntentNotFound
	}
	h.deploymentRepo.BeginUndeployFunc = func(context.Context, string, string, string) (string, time.Time, error) {
		return "", models.NewPerformedAt(), nil
	}

	h.process(pub)

	undeploys := h.deploymentRepo.BeginUndeployCalls()
	require.Len(t, undeploys, 1, "the deployment just written is undeployed, so its ack has a row to settle")
	assert.Equal(t, pub.ArtifactUUID.String(), undeploys[0].ArtifactUUID)
	require.Len(t, h.hub.published, 2, "the deploy event, then its withdrawal")
	assert.Equal(t, pub.ArtifactUUID.String(), undeployedAgentID(t, h.hub.published[1]), "the artifact and its keys may still be wanted")
	assert.Empty(t, undeployedAgentDeploymentID(t, h.hub.published[1]), "it was never acked")
}

// An intent changed meanwhile is carried out on its own; this attempt writes and sends nothing.
func TestA2AKindLeavesAnIntentChangedMeanwhileForTheNextAttempt(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	h.intents.MarkDoneFunc = func(context.Context, models.DeployIntent) (models.DeployIntent, error) {
		return models.DeployIntent{}, repositories.ErrDeployIntentSuperseded
	}

	h.process(pendingPublication())

	assert.Empty(t, h.deploymentRepo.CreateWithLimitEnforcementCalls())
	assert.Empty(t, h.hub.published)
	assert.Empty(t, h.intents.MarkAttemptFailedCalls())
}

func TestA2AKindLogsWhenItCannotTellIfAPublishedIntentWasTornDown(t *testing.T) {
	h := newA2AKindHarness("http://trip-planner.dp-default:9099")
	pub := pendingPublication()
	h.intents.GetFunc = func(context.Context, uuid.UUID) (*models.DeployIntent, error) {
		return nil, errors.New("connection reset")
	}
	// BeginUndeployFunc is nil: withdrawing would panic.

	h.process(pub)

	assert.Contains(t, h.logs.String(), "level=ERROR")
	assert.Contains(t, h.logs.String(), pub.ArtifactUUID.String())
	assert.Contains(t, h.logs.String(), "connection reset")
	assert.Len(t, h.hub.published, 1, "nothing is withdrawn")
}

func TestA2AKindSkipsTheCardTriggerWhenTheExternalRowIsGone(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.cardRepo.RequeueFunc = func(context.Context, string, string, string, string) error {
		return repositories.ErrA2AAgentCardNotFound
	}

	h.kind.AfterLive(context.Background(), pub.intent(), uuid.New(), uuid.New())

	assert.Empty(t, h.ocClient.GetComponentCalls(), "nothing was written, so nothing to drop")
}

func TestA2AKindStoresAValidUTF8LastError(t *testing.T) {
	h := newA2AKindHarness("")
	h.ocClient.GetReleaseBindingServiceURLFunc = func(context.Context, string, string, string) (string, error) {
		return "", errors.New("lookup \xff failed")
	}

	h.process(pendingPublication())

	failed := h.intents.MarkAttemptFailedCalls()
	require.Len(t, failed, 1)
	assert.True(t, utf8.ValidString(failed[0].LastErr))
}

func TestA2AKindBoundsEachPublishAttempt(t *testing.T) {
	h := newA2AKindHarness("")
	h.ocClient.GetReleaseBindingServiceURLFunc = func(ctx context.Context, _, _, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	h.cfg.AttemptTimeout = 20 * time.Millisecond
	h.rebuild()

	runWithin(t, 2*time.Second, func() { h.process(pendingPublication()) })

	require.Len(t, h.intents.MarkAttemptFailedCalls(), 1, "a timed-out attempt is still recorded")
}

func TestA2AKindHidesInternalErrorsFromLastError(t *testing.T) {
	h := newA2AKindHarness("http://svc.dev:8080")
	h.deploymentRepo.CreateWithLimitEnforcementFunc = func(*models.Deployment, int) error {
		return errors.New("failed to connect to `user=ams database=amdb host=10.0.0.5`: SQLSTATE 28P01")
	}

	h.process(pendingPublication())

	retries := h.intents.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.Equal(t, a2aPublishInternalError, retries[0].LastErr)
}

func TestA2AKindNamesTheOriginInAFetchFailure(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.fetcher.FetchFunc = func(context.Context, string, bool) (json.RawMessage, error) {
		return nil, newA2APublicError("agent card request returned HTTP 503", nil)
	}

	h.process(pub)

	retries := h.intents.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.Equal(t, "could not fetch the origin agent card: agent card request returned HTTP 503", retries[0].LastErr)
}

func TestA2AKindFetchesAnUnauthenticatedOriginWithoutAHeader(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.proxyRepo.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{}, nil
	}

	h.process(pub)

	require.Len(t, h.fetcher.calls, 1)
	assert.Nil(t, h.fetcher.calls[0].Header)
}

func TestA2AKindRetriesWhenTheUpstreamSecretCannotBeDecrypted(t *testing.T) {
	h := newA2AKindHarness("")
	pub := pendingPublication()
	h.withExternalOrigin(&pub)
	h.proxyRepo.GetByEnvUUIDFunc = func(context.Context, string, string, string, uuid.UUID) (*models.A2AProxy, error) {
		return &models.A2AProxy{UpstreamAuthHeader: "X-Upstream-Key", UpstreamAuthSecret: "bm90LWNpcGhlcnRleHQ="}, nil
	}

	h.process(pub)

	assert.Empty(t, h.fetcher.calls, "no fetch without the credential the origin needs")
	retries := h.intents.MarkAttemptFailedCalls()
	require.Len(t, retries, 1)
	assert.Equal(t, a2aPublishInternalError, retries[0].LastErr)
}

// The kind's events keep the gateway's wire shapes (pkg/controlplane/events.go).
func TestA2AKindEventsKeepTheGatewaysShapes(t *testing.T) {
	pub := pendingPublication()
	performedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	ref := reconcile.EventRef{Intent: pub.intent(), GatewayID: "gw-1", DeploymentID: "dep-1", PerformedAt: performedAt}
	kind := &A2AAgentKind{}
	agentID := pub.ArtifactUUID.String()

	assert.Equal(t, reconcile.Event{
		Type: "agent.deployed", Action: "CREATE", EntityID: agentID,
		Payload: &models.AgentDeploymentEvent{AgentID: agentID, DeploymentID: "dep-1", PerformedAt: performedAt},
	}, kind.DeployEvent(ref))
	assert.Equal(t, reconcile.Event{
		Type: "agent.undeployed", Action: "DELETE", EntityID: agentID,
		Payload: &models.AgentUndeploymentEvent{AgentID: agentID, DeploymentID: "dep-1", PerformedAt: performedAt},
	}, kind.UndeployEvent(ref))
	assert.Equal(t, reconcile.Event{
		Type: "agent.deleted", Action: "DELETE", EntityID: agentID,
		Payload: &models.AgentDeletionEvent{AgentID: agentID},
	}, kind.DeleteEvent(ref))
}

func TestA2AIntentRoundTripsItsKindData(t *testing.T) {
	pub := pendingPublication()
	agentUUID := uuid.New()
	pub.AgentUUID = &agentUUID
	pub.Source = models.A2APublicationSourceExternal

	intent := pub.intent()
	assert.Equal(t, models.KindAgent, intent.Kind)
	assert.Equal(t, models.DeploymentStatusDeployed, intent.Desired)
	assert.JSONEq(t, `{"projectName":"checkout","agentName":"trip-planner","environmentName":"dev","agentUuid":"`+
		agentUUID.String()+`","source":"external"}`, string(intent.KindData))
	decoded, err := decodeA2APublication(intent)
	require.NoError(t, err)
	assert.Equal(t, pub.a2aPublication, decoded)

	intent.KindData = json.RawMessage(`{"projectName":"checkout","agentName":"trip-planner","environmentName":"dev"}`)
	decoded, err = decodeA2APublication(intent)
	require.NoError(t, err)
	assert.Equal(t, models.A2APublicationSourcePlatform, decoded.Source, "a missing source is a platform agent")
}
