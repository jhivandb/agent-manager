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

import type {
  A2AAgentCardResponse,
  GetA2AAgentCardPathParams,
  RefreshA2AAgentCardPathParams,
} from "@agent-management-platform/types";
import { encodeRequired, httpGET, httpPOST, SERVICE_BASE } from "../utils";

export async function getA2AAgentCard(
  params: GetA2AAgentCardPathParams,
  getToken?: () => Promise<string>,
): Promise<A2AAgentCardResponse> {
  const org = encodeRequired(params.orgName, "orgName");
  const proj = encodeRequired(params.projName, "projName");
  const agent = encodeRequired(params.agentName, "agentName");
  const env = encodeRequired(params.envId, "envId");
  const token = getToken ? await getToken() : undefined;

  // httpGET throws (with .status set) on a non-2xx response, including the
  // 404 that means "not an A2A agent / not deployed here" — callers read
  // that off the query's error, not this function's return value.
  const res = await httpGET(
    `${SERVICE_BASE}/orgs/${org}/projects/${proj}/agents/${agent}/environments/${env}/agent-card`,
    { token },
  );
  return res.json();
}

export async function refreshA2AAgentCard(
  params: RefreshA2AAgentCardPathParams,
  getToken?: () => Promise<string>,
): Promise<void> {
  const org = encodeRequired(params.orgName, "orgName");
  const proj = encodeRequired(params.projName, "projName");
  const agent = encodeRequired(params.agentName, "agentName");
  const env = encodeRequired(params.envId, "envId");
  const token = getToken ? await getToken() : undefined;

  // 202, no body: the fetch itself runs on the next reconciler tick.
  await httpPOST(
    `${SERVICE_BASE}/orgs/${org}/projects/${proj}/agents/${agent}/environments/${env}/agent-card/refresh`,
    {},
    { token },
  );
}
