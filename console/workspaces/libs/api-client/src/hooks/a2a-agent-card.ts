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

import { useQueryClient } from "@tanstack/react-query";
import { useAuthHooks } from "@agent-management-platform/auth";
import { useApiMutation, useApiQuery } from "./react-query-notifications";
import type {
  A2AAgentCardResponse,
  GetA2AAgentCardPathParams,
  RefreshA2AAgentCardPathParams,
} from "@agent-management-platform/types";
import { getA2AAgentCard, refreshA2AAgentCard } from "../apis/a2a-agent-card";

export function useGetA2AAgentCard(
  params: GetA2AAgentCardPathParams,
  options?: { enabled?: boolean },
) {
  const { getToken } = useAuthHooks();
  return useApiQuery<A2AAgentCardResponse>({
    queryKey: ["a2a-agent-card", params.orgName, params.projName, params.agentName, params.envId],
    queryFn: () => getA2AAgentCard(params, getToken),
    enabled:
      (options?.enabled ?? true) &&
      !!(params.orgName && params.projName && params.agentName && params.envId),
    // 404 ("not an A2A agent" / "never deployed here") is expected for most
    // agents, not a failure worth a snackbar.
    silent: true,
  });
}

export function useRefreshA2AAgentCard() {
  const { getToken } = useAuthHooks();
  const queryClient = useQueryClient();
  return useApiMutation<void, unknown, RefreshA2AAgentCardPathParams>({
    action: { verb: "rerun", target: "card refresh" },
    mutationFn: (params) => refreshA2AAgentCard(params, getToken),
    onSuccess: (_data, params) => {
      queryClient.invalidateQueries({
        queryKey: ["a2a-agent-card", params.orgName, params.projName, params.agentName, params.envId],
      });
    },
  });
}
