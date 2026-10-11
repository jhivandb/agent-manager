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

import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useAuthHooks } from "@agent-management-platform/auth";
import type {
  A2AProxyPathParams,
  A2AProxyResponse,
  SetA2AProxyRequest,
} from "@agent-management-platform/types";
import { useApiMutation, useApiQuery } from "./react-query-notifications";
import { deleteA2AProxy, getA2AProxy, refreshA2AProxy, setA2AProxy } from "../apis/a2a-proxy";
import { agentCardQueryKey } from "./agent-card";
import { pendingRefetchInterval } from "../utils";

export const a2aProxyQueryKey = (p: A2AProxyPathParams) =>
  ["a2a-proxy", p.orgName, p.projName, p.agentName, p.envId] as const;

/** A proxy change also changes the card, so both are refetched. */
export async function invalidateA2AProxy(
  queryClient: QueryClient,
  params: A2AProxyPathParams,
): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: a2aProxyQueryKey(params) }),
    queryClient.invalidateQueries({ queryKey: agentCardQueryKey(params) }),
  ]);
}

/** Binds `invalidateA2AProxy` to the app's query client, for callers that do not depend on react-query. */
export function useInvalidateA2AProxy(): (params: A2AProxyPathParams) => Promise<void> {
  const queryClient = useQueryClient();
  return (params) => invalidateA2AProxy(queryClient, params);
}

/** Re-poll while the gateway publish is in flight; a 404 means the environment is direct. */
export function a2aProxyRefetchInterval(
  data: A2AProxyResponse | undefined,
  error?: unknown,
): number | false {
  return pendingRefetchInterval(data, error);
}

/** A 404 (the environment is direct) is expected: it errors silently. */
export function useGetA2AProxy(params: A2AProxyPathParams, options: { enabled?: boolean } = {}) {
  const { getToken } = useAuthHooks();
  return useApiQuery<A2AProxyResponse>({
    queryKey: a2aProxyQueryKey(params),
    queryFn: () => getA2AProxy(params, getToken),
    enabled: (options.enabled ?? true) &&
      !!(params.orgName && params.projName && params.agentName && params.envId),
    refetchInterval: (q) => a2aProxyRefetchInterval(q.state.data, q.state.error),
    retry: false,
    silent: true,
  });
}

export function useSetA2AProxy() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  type Vars = { params: A2AProxyPathParams; body: SetA2AProxyRequest };
  return useApiMutation<void, unknown, Vars>({
    action: { verb: "update", target: "gateway routing" },
    // In mutationFn, since useApiMutation does not await onSuccess.
    mutationFn: async ({ params, body }) => {
      await setA2AProxy(params, body, getToken);
      await invalidateA2AProxy(queryClient, params);
    },
  });
}

export function useDeleteA2AProxy() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, A2AProxyPathParams>({
    action: { verb: "remove", target: "gateway routing" },
    // A 404 keeps the old data cached, so the proxy entry is dropped rather than refetched.
    mutationFn: async (params) => {
      await deleteA2AProxy(params, getToken);
      queryClient.removeQueries({ queryKey: a2aProxyQueryKey(params) });
      await queryClient.invalidateQueries({ queryKey: agentCardQueryKey(params) });
    },
  });
}

export function useRefreshA2AProxy() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, A2AProxyPathParams>({
    action: { verb: "update", target: "gateway routing" },
    successMessage: "Gateway republish queued",
    mutationFn: async (params) => {
      await refreshA2AProxy(params, getToken);
      await invalidateA2AProxy(queryClient, params);
    },
  });
}
