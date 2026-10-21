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

import { useSearchParams } from "react-router-dom";
import type { A2ACatalogAgent } from "@agent-management-platform/types";

export interface CatalogueSelection {
  projectName: string;
  agentName: string;
  env?: string;
}

const PROJECT_PARAM = "project";
const AGENT_PARAM = "agent";
const ENV_PARAM = "env";

export function useCatalogueSelection() {
  const [searchParams, setSearchParams] = useSearchParams();
  const projectName = searchParams.get(PROJECT_PARAM);
  const agentName = searchParams.get(AGENT_PARAM);
  const env = searchParams.get(ENV_PARAM) ?? undefined;

  const selection: CatalogueSelection | null =
    projectName && agentName ? { projectName, agentName, env } : null;

  const updateParams = (change: (params: URLSearchParams) => void) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        change(next);
        return next;
      },
      { replace: true },
    );
  };

  const select = (agent: A2ACatalogAgent) =>
    updateParams((params) => {
      params.set(PROJECT_PARAM, agent.projectName);
      params.set(AGENT_PARAM, agent.agentName);
      params.set(ENV_PARAM, agent.leadEnvironment);
    });

  const selectEnv = (name: string) =>
    updateParams((params) => params.set(ENV_PARAM, name));

  const clear = () =>
    updateParams((params) => {
      params.delete(PROJECT_PARAM);
      params.delete(AGENT_PARAM);
      params.delete(ENV_PARAM);
    });

  return { selection, select, selectEnv, clear };
}
