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
import type {
  A2AConnectResponse,
  A2ACatalogListResponse,
  A2ACatalogQuery,
  AgentCardPathParams,
} from "@agent-management-platform/types";
import { encodeRequired, httpGET, SERVICE_BASE } from "../utils";

function setParams(query: A2ACatalogQuery): Record<string, string> {
  const entries = Object.entries(query).filter(([, value]) => value !== undefined && value !== "");
  return Object.fromEntries(entries.map(([key, value]) => [key, String(value)]));
}

export async function listA2AAgents(
  params: { orgName: string },
  query: A2ACatalogQuery,
  getToken?: () => Promise<string>,
): Promise<A2ACatalogListResponse> {
  const org = encodeRequired(params.orgName, "orgName");
  const token = getToken ? await getToken() : undefined;
  const searchParams = setParams(query);
  const res = await httpGET(`${SERVICE_BASE}/orgs/${org}/a2a-agents`, {
    searchParams: Object.keys(searchParams).length > 0 ? searchParams : undefined,
    token,
  });
  return res.json();
}

export async function getA2AConnect(
  params: AgentCardPathParams,
  getToken?: () => Promise<string>,
): Promise<A2AConnectResponse> {
  const org = encodeRequired(params.orgName, "orgName");
  const proj = encodeRequired(params.projName, "projName");
  const agent = encodeRequired(params.agentName, "agentName");
  const env = encodeRequired(params.envId, "envId");
  const token = getToken ? await getToken() : undefined;
  const res = await httpGET(
    `${SERVICE_BASE}/orgs/${org}/projects/${proj}/agents/${agent}/environments/${env}/a2a-connect`,
    { token },
  );
  return res.json();
}
