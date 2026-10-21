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

import {
  Box,
  Chip,
  List,
  ListItemButton,
  ListItemText,
  Stack,
} from "@wso2/oxygen-ui";
import type {
  A2ACatalogAgent,
  A2ACatalogEnvironment,
} from "@agent-management-platform/types";
import type { CatalogueSelection } from "../useCatalogueSelection";

export interface CompactAgentListProps {
  agents: A2ACatalogAgent[];
  selection: CatalogueSelection | null;
  onSelect: (agent: A2ACatalogAgent) => void;
}

const STATUS_COLOR: Record<A2ACatalogEnvironment["status"], string> = {
  fetched: "success.main",
  pending: "warning.main",
  failed: "error.main",
};

const isSelected = (agent: A2ACatalogAgent, selection: CatalogueSelection | null) =>
  selection?.projectName === agent.projectName &&
  selection?.agentName === agent.agentName;

export function CompactAgentList({ agents, selection, onSelect }: CompactAgentListProps) {
  return (
    <List disablePadding>
      {agents.map((agent) => (
        <ListItemButton
          key={`${agent.projectName}/${agent.agentName}`}
          selected={isSelected(agent, selection)}
          onClick={() => onSelect(agent)}
        >
          <ListItemText
            primary={agent.name || agent.agentName}
            secondary={agent.projectName}
            slotProps={{ primary: { noWrap: true }, secondary: { noWrap: true } }}
          />
          <Stack direction="row" spacing={0.5} alignItems="center">
            <Chip size="small" label={agent.kind} />
            {agent.environments.map((environment) => (
              <Box
                key={environment.name}
                title={`${environment.name}: ${environment.status}`}
                data-testid="environment-status-dot"
                sx={{
                  width: 8,
                  height: 8,
                  borderRadius: "50%",
                  bgcolor: STATUS_COLOR[environment.status],
                }}
              />
            ))}
          </Stack>
        </ListItemButton>
      ))}
    </List>
  );
}
