/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { type AgentPathParams } from './common';

export type A2AAgentCardStatus = 'pending' | 'routed' | 'published' | 'failed' | 'rejected';

// The stored A2A agent card and the publication state that explains its
// presence or absence. `card` is absent or null until the first successful
// fetch — read it as `card ?? null`, never `=== null`.
export interface A2AAgentCardResponse {
  card?: Record<string, unknown> | null;
  status: A2AAgentCardStatus;
  fetchedAt: string | null;
  // Null when the agent-environment pair has never been routed — distinguishes
  // "never routed" from "routed, but the card never arrived" while failed.
  routedAt: string | null;
  lastError: string;
}

export interface AgentEnvCardPathParams extends AgentPathParams {
  envId: string | undefined;
}

export type GetA2AAgentCardPathParams = AgentEnvCardPathParams;
export type RefreshA2AAgentCardPathParams = AgentEnvCardPathParams;
