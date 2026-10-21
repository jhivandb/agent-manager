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
package models

import "time"

// A2ACatalogKindFilter is the list filter only; an entry's Kind is the existing A2AAgentCardSource.
type A2ACatalogKindFilter string

const (
	A2ACatalogKindFilterPlatform A2ACatalogKindFilter = "platform"
	A2ACatalogKindFilterExternal A2ACatalogKindFilter = "external"
	A2ACatalogKindFilterProxied  A2ACatalogKindFilter = "proxied"
)

type A2ACatalogFilter struct {
	Query           string
	ProjectName     string
	EnvironmentName string
	Kind            A2ACatalogKindFilter
	Status          A2AAgentCardStatus
	Limit           int
	Offset          int
}

type A2ACatalogRow struct {
	Card    A2AAgentCard
	Proxied bool
	Matched bool
}

type A2ACatalogEnvironment struct {
	Name        string
	Status      A2AAgentCardStatus
	FetchedAt   *time.Time
	Proxied     bool
	CardDiffers bool
	Matched     bool
}

type A2ACatalogEntry struct {
	ProjectName     string
	AgentName       string
	Kind            A2AAgentCardSource
	Name            string
	Description     string
	Version         string
	Skills          []string
	LeadEnvironment string
	Environments    []A2ACatalogEnvironment
}

type A2ACatalogPage struct {
	Entries []A2ACatalogEntry
	Total   int64
	Limit   int
	Offset  int
}

type A2ASecurityMode string

const (
	A2ASecurityModeNone      A2ASecurityMode = "none"
	A2ASecurityModeAPIKey    A2ASecurityMode = "apiKey"
	A2ASecurityModeOAuth     A2ASecurityMode = "oauth"
	A2ASecurityModeUnmanaged A2ASecurityMode = "unmanaged"
	A2ASecurityModeUnknown   A2ASecurityMode = "unknown"
)

// A2AConnectIssuer.Issuer is empty when the name no longer resolves.
type A2AConnectIssuer struct {
	Name   string
	Issuer string
}

// A2AConnectSecurity.OAuth reuses OAuthConfig; Issuers holds the resolved issuers.
type A2AConnectSecurity struct {
	Mode         A2ASecurityMode
	APIKeyHeader string
	OAuth        *OAuthConfig
	Issuers      []A2AConnectIssuer
}

const (
	A2AConnectPublicationPending   = "pending"
	A2AConnectPublicationPublished = "published"
	A2AConnectPublicationFailed    = "failed"
	A2AConnectPublicationNone      = "none"
)

type A2AConnectPublication struct {
	Status    string
	LastError string
}

type A2AConnectInfo struct {
	Kind         A2AAgentCardSource
	Proxied      bool
	IsProduction bool
	// GatewayURL is empty for a direct external agent or while no gateway is mapped.
	GatewayURL  string
	Security    A2AConnectSecurity
	Publication A2AConnectPublication
}
