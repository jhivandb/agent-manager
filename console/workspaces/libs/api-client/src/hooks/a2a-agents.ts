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
import { keepPreviousData } from "@tanstack/react-query";
import { useAuthHooks } from "@agent-management-platform/auth";
import type {
  A2ACatalogAgent,
  A2AConnectResponse,
  A2ACatalogListResponse,
  A2ACatalogQuery,
  AgentCardPathParams,
} from "@agent-management-platform/types";
import { getA2ACatalogEntry, getA2AConnect, listA2AAgents } from "../apis/a2a-agents";

// Each catalogue read summarizes every card on the page, so a back-navigation reuses a recent one.
const CATALOGUE_STALE_MS = 30 * 1000;
import { useApiQuery } from "./react-query-notifications";
import { nullIfNotFound } from "../utils";

export function useListA2AAgents(params: { orgName: string }, query: A2ACatalogQuery) {
  const { getToken } = useAuthHooks();
  return useApiQuery<A2ACatalogListResponse>({
    queryKey: ["a2a-agents", params, query],
    queryFn: () => listA2AAgents(params, query, getToken),
    enabled: !!params.orgName,
    placeholderData: keepPreviousData,
    staleTime: CATALOGUE_STALE_MS,
  });
}

export function useGetA2ACatalogEntry(params: {
  orgName: string;
  projName: string;
  agentName: string;
}) {
  const { getToken } = useAuthHooks();
  // null when the agent is not in the catalogue.
  return useApiQuery<A2ACatalogAgent | null>({
    queryKey: ["a2a-catalog-entry", params],
    queryFn: () => nullIfNotFound(getA2ACatalogEntry(params, getToken)),
    enabled: !!(params.orgName && params.projName && params.agentName),
    staleTime: CATALOGUE_STALE_MS,
    retry: false,
    // The page says when the agent fails to load.
    silent: true,
  });
}

export function useGetA2AConnect(params: AgentCardPathParams) {
  const { getToken } = useAuthHooks();
  return useApiQuery<A2AConnectResponse>({
    queryKey: ["a2a-connect", params],
    queryFn: () => getA2AConnect(params, getToken),
    enabled: !!(params.orgName && params.projName && params.agentName && params.envId),
  });
}
