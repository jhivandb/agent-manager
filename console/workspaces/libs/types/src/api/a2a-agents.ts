/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */
import type { ListQuery, PaginationMeta } from './common';

export type A2ACatalogKind = 'platform' | 'external';
export type A2ACatalogKindFilter = A2ACatalogKind | 'proxied';

export interface A2ACatalogEnvironment {
  name: string;
  status: 'pending' | 'fetched' | 'failed';
  fetchedAt?: string;
  proxied: boolean;
  cardDiffers: boolean;
  matched: boolean;
}

export interface A2ACatalogAgent {
  projectName: string;
  agentName: string;
  kind: A2ACatalogKind;
  name: string;
  description: string;
  version: string;
  skills: string[];
  leadEnvironment: string;
  environments: A2ACatalogEnvironment[];
}

export interface A2ACatalogListResponse extends PaginationMeta {
  agents: A2ACatalogAgent[];
}

export interface A2ACatalogQuery extends ListQuery {
  q?: string;
  kind?: A2ACatalogKindFilter;
  environment?: string;
  project?: string;
  status?: A2ACatalogEnvironment['status'];
}

export type A2ASecurityMode = 'none' | 'apiKey' | 'oauth' | 'unmanaged' | 'unknown';

export interface A2AConnectOAuth {
  issuers: { name: string; issuer: string }[];
  audiences: string[];
  headerName: string;
  authHeaderPrefix: string;
}

export interface A2AConnectResponse {
  kind: A2ACatalogKind;
  proxied: boolean;
  isProduction: boolean;
  gatewayUrl: string;
  /** The gateway's when it fronts the agent, otherwise the card's own; empty when unknown. */
  cardUrl: string;
  jsonRpcUrl: string;
  security: {
    mode: A2ASecurityMode;
    /** Set when mode is apiKey. */
    apiKeyHeader?: string;
    oauth?: A2AConnectOAuth;
  };
  publication: {
    status: 'pending' | 'published' | 'failed' | 'none';
    lastError: string;
  };
}
