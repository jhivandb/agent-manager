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

import { useCallback, useState } from "react";
import { useParams } from "react-router-dom";
import { Box } from "@wso2/oxygen-ui";
import { PageLayout } from "@agent-management-platform/views";
import type { A2ACatalogAgent } from "@agent-management-platform/types";
import { AgentSidePanel } from "./subComponents/AgentSidePanel";
import { CatalogueGrid } from "./subComponents/CatalogueGrid";
import { useCatalogueSelection, type CatalogueSelection } from "./useCatalogueSelection";

const agentKey = (projectName: string, agentName: string) => `${projectName}/${agentName}`;

function placeholderAgent(selection: CatalogueSelection): A2ACatalogAgent {
  return {
    projectName: selection.projectName,
    agentName: selection.agentName,
    kind: "platform",
    name: selection.agentName,
    description: "",
    version: "",
    skills: [],
    leadEnvironment: selection.env ?? "",
    environments: selection.env
      ? [{ name: selection.env, status: "fetched", proxied: false, cardDiffers: false, matched: false }]
      : [],
  };
}

export function A2AAgentsOrganization() {
  const { orgId } = useParams<{ orgId: string }>();
  const { selection, select, selectEnv, clear } = useCatalogueSelection();
  const [seenAgents, setSeenAgents] = useState<Record<string, A2ACatalogAgent>>({});

  const rememberAgents = useCallback(
    (agents: A2ACatalogAgent[]) =>
      setSeenAgents((seen) => ({
        ...seen,
        ...Object.fromEntries(agents.map((a) => [agentKey(a.projectName, a.agentName), a])),
      })),
    [],
  );

  const selectedAgent = selection
    ? (seenAgents[agentKey(selection.projectName, selection.agentName)] ??
      placeholderAgent(selection))
    : null;

  const grid = (
    <CatalogueGrid
      orgName={orgId ?? ""}
      selection={selection}
      compact={selection !== null}
      onSelect={select}
      onAgentsLoaded={rememberAgents}
    />
  );

  return (
    <PageLayout title="A2A Agents" disableIcon>
      {selection && selectedAgent ? (
        <Box
          sx={{
            display: "grid",
            gap: 2,
            gridTemplateColumns: { xs: "1fr", md: "300px 1fr" },
            alignItems: "start",
          }}
        >
          <Box sx={{ minWidth: 0 }}>{grid}</Box>
          <AgentSidePanel
            agent={selectedAgent}
            env={selection.env ?? selectedAgent.leadEnvironment}
            onEnvChange={selectEnv}
            onClose={clear}
          />
        </Box>
      ) : (
        grid
      )}
    </PageLayout>
  );
}

export default A2AAgentsOrganization;
