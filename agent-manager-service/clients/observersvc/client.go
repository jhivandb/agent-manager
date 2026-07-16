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

package observersvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	occlient "github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/clients/requests"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

//go:generate moq -rm -fmt goimports -skip-ensure -pkg clientmocks -out ../clientmocks/observer_client_fake.go . ObserverSvcClient:ObserverSvcClientMock

// ObserverSvcClient defines the interface for trace and workflow-run-log observer operations.
type ObserverSvcClient interface {
	ListTraces(ctx context.Context, params TraceListParams) (map[string]any, error)
	ExportTraces(ctx context.Context, params TraceListParams) (map[string]any, error)
	GetTrace(ctx context.Context, params TraceDetailsParams) (map[string]any, error)
	GetSpan(ctx context.Context, params SpanDetailsParams) (map[string]any, error)
	GetWorkflowRunLogs(ctx context.Context, organization, workflowRunName string) (*models.LogsResponse, error)
}

// Config contains configuration for the observer client.
type Config struct {
	BaseURL      string
	AuthProvider occlient.AuthProvider
	RetryConfig  requests.RequestRetryConfig
}

type observerSvcClient struct {
	baseURL      string
	httpClient   requests.HttpClient
	authProvider occlient.AuthProvider
}

// NewObserverClient creates a new observer client.
func NewObserverClient(cfg *Config) (ObserverSvcClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if cfg.AuthProvider == nil {
		return nil, fmt.Errorf("auth provider is required")
	}

	retryConfig := cfg.RetryConfig
	httpClient := requests.NewRetryableHTTPClient(&http.Client{}, retryConfig)

	return &observerSvcClient{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		httpClient:   httpClient,
		authProvider: cfg.AuthProvider,
	}, nil
}

func (c *observerSvcClient) ListTraces(ctx context.Context, params TraceListParams) (map[string]any, error) {
	query := map[string]string{
		"organization": params.Organization,
		"project":      params.Project,
		"agent":        params.Component,
		"environment":  params.Environment,
		"startTime":    params.StartTime,
		"endTime":      params.EndTime,
	}
	if params.Limit > 0 {
		query["limit"] = strconv.Itoa(params.Limit)
	}
	if strings.TrimSpace(params.SortOrder) != "" {
		query["sortOrder"] = params.SortOrder
	}

	return c.doGetMap(ctx, "observersvc.ListTraces", "/api/v1/traces", query)
}

func (c *observerSvcClient) ExportTraces(ctx context.Context, params TraceListParams) (map[string]any, error) {
	query := map[string]string{
		"organization": params.Organization,
		"project":      params.Project,
		"agent":        params.Component,
		"environment":  params.Environment,
		"startTime":    params.StartTime,
		"endTime":      params.EndTime,
	}
	if params.Limit > 0 {
		query["limit"] = strconv.Itoa(params.Limit)
	}
	if strings.TrimSpace(params.SortOrder) != "" {
		query["sortOrder"] = params.SortOrder
	}

	return c.doGetMap(ctx, "observersvc.ExportTraces", "/api/v1/traces/export", query)
}

func (c *observerSvcClient) GetTrace(ctx context.Context, params TraceDetailsParams) (map[string]any, error) {
	query := map[string]string{
		"organization": params.Organization,
		"startTime":    params.StartTime,
		"endTime":      params.EndTime,
	}
	if strings.TrimSpace(params.Project) != "" {
		query["project"] = params.Project
	}
	if strings.TrimSpace(params.Component) != "" {
		query["agent"] = params.Component
	}
	if strings.TrimSpace(params.Environment) != "" {
		query["environment"] = params.Environment
	}
	if strings.TrimSpace(params.SortOrder) != "" {
		query["sortOrder"] = params.SortOrder
	}
	if params.Limit > 0 {
		query["limit"] = strconv.Itoa(params.Limit)
	}

	path := "/api/v1/traces/" + url.PathEscape(params.TraceID) + "/spans"
	return c.doGetMap(ctx, "observersvc.GetTrace", path, query)
}

func (c *observerSvcClient) GetSpan(ctx context.Context, params SpanDetailsParams) (map[string]any, error) {
	query := map[string]string{
		"organization": params.Organization,
	}
	if strings.TrimSpace(params.Project) != "" {
		query["project"] = params.Project
	}
	if strings.TrimSpace(params.Component) != "" {
		query["agent"] = params.Component
	}
	if strings.TrimSpace(params.Environment) != "" {
		query["environment"] = params.Environment
	}

	path := "/api/v1/traces/" + url.PathEscape(params.TraceID) + "/spans/" + url.PathEscape(params.SpanID)
	return c.doGetMap(ctx, "observersvc.GetSpan", path, query)
}

// GetWorkflowRunLogs fetches build/workflow-run logs from the agent-manager-observer.
// The observer resolves the upstream namespace from the organization itself.
func (c *observerSvcClient) GetWorkflowRunLogs(ctx context.Context, organization, workflowRunName string) (*models.LogsResponse, error) {
	q := url.Values{}
	q.Set("organization", organization)
	q.Set("buildName", workflowRunName)
	var out models.LogsResponse
	if err := c.doGetJSON(ctx, "/api/v1/build-logs", q, &out); err != nil {
		return nil, fmt.Errorf("observersvc.GetWorkflowRunLogs: %w", err)
	}
	return &out, nil
}

func (c *observerSvcClient) doGetMap(ctx context.Context, name, path string, query map[string]string) (map[string]any, error) {
	if c == nil {
		return nil, fmt.Errorf("observer client is nil")
	}
	url := c.baseURL + path

	result, err := c.sendGet(ctx, name, url, query)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	scanErr := result.ScanResponse(&out, http.StatusOK)
	if scanErr == nil {
		return out, nil
	}
	var httpErr *requests.HttpError
	if errors.As(scanErr, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized {
		// retry once after invalidating token
		c.authProvider.InvalidateToken()
		result, retryErr := c.sendGet(ctx, name, url, query)
		if retryErr != nil {
			return nil, retryErr
		}
		var retryOut map[string]any
		if retryErr := result.ScanResponse(&retryOut, http.StatusOK); retryErr != nil {
			return nil, retryErr
		}
		return retryOut, nil
	}

	return nil, scanErr
}

// doGetJSON is doGetMap's typed twin: same auth header handling and single
// 401-retry behavior, but decodes the response body into out instead of a
// map[string]any.
func (c *observerSvcClient) doGetJSON(ctx context.Context, path string, query url.Values, out any) error {
	if c == nil {
		return fmt.Errorf("observer client is nil")
	}
	name := "observersvc" + path
	reqURL := c.baseURL + path
	q := make(map[string]string, len(query))
	for key := range query {
		q[key] = query.Get(key)
	}

	result, err := c.sendGet(ctx, name, reqURL, q)
	if err != nil {
		return err
	}
	scanErr := result.ScanResponse(out, http.StatusOK)
	if scanErr == nil {
		return nil
	}
	var httpErr *requests.HttpError
	if errors.As(scanErr, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized {
		// retry once after invalidating token
		c.authProvider.InvalidateToken()
		retryResult, retryErr := c.sendGet(ctx, name, reqURL, q)
		if retryErr != nil {
			return retryErr
		}
		return retryResult.ScanResponse(out, http.StatusOK)
	}

	return scanErr
}

func (c *observerSvcClient) sendGet(ctx context.Context, name, url string, query map[string]string) (*requests.Result, error) {
	req := &requests.HttpRequest{
		Name:   name,
		URL:    url,
		Method: http.MethodGet,
		Query:  query,
	}

	token, err := c.authProvider.GetToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to get auth token: %w", name, err)
	}
	if strings.TrimSpace(token) != "" {
		req.SetHeader("Authorization", "Bearer "+token)
	}
	req.SetHeader("Content-Type", "application/json")

	result := requests.SendRequest(ctx, c.httpClient, req)
	if result == nil {
		return nil, fmt.Errorf("%s: request returned nil result", name)
	}
	return result, nil
}
