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

import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { Box, Chip, IconButton, Stack, Typography } from "@wso2/oxygen-ui";
import { X } from "@wso2/oxygen-ui-icons-react";
import { useGetAgentCard } from "@agent-management-platform/api-client";
import type { A2ACatalogAgent } from "@agent-management-platform/types";
import { parseA2ACard } from "../utils/card";
import { CardDetails } from "./CardDetails";

const OWN_ESCAPE_SELECTOR = "input, textarea, [role=listbox], [role=menu]";

export interface AgentSidePanelProps {
  agent: A2ACatalogAgent;
  env: string;
  onEnvChange: (env: string) => void;
  onClose: () => void;
}

export function AgentSidePanel({ agent, env, onEnvChange, onClose }: AgentSidePanelProps) {
  const { orgId } = useParams<{ orgId: string }>();
  const [compareEnv, setCompareEnv] = useState("");

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest(OWN_ESCAPE_SELECTOR)) return;
      onClose();
    };
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [onClose]);

  useEffect(() => setCompareEnv(""), [agent.projectName, agent.agentName, env]);

  const cardParams = (envId: string) => ({
    orgName: orgId,
    projName: agent.projectName,
    agentName: agent.agentName,
    envId,
  });
  const { data: card } = useGetAgentCard(cardParams(env));
  const { data: compareCard } = useGetAgentCard(cardParams(compareEnv), {
    enabled: compareEnv !== "",
    poll: false,
  });

  const parsed = parseA2ACard(card?.card);
  const kind = card?.source ?? agent.kind;
  const version = parsed.version || agent.version;
  const provider = parsed.providerOrganization;

  return (
    <Box
      data-testid="agent-side-panel"
      sx={{ border: "1px solid", borderColor: "divider", borderRadius: 1, p: 2, minWidth: 0 }}
    >
      <Stack spacing={1.5}>
        <Stack direction="row" spacing={1} alignItems="center">
          <Typography variant="h6" noWrap sx={{ flex: 1 }}>
            {agent.name || agent.agentName}
          </Typography>
          {version && <Chip size="small" variant="outlined" label={`v${version}`} />}
          <Chip size="small" label={kind} />
          <IconButton size="small" aria-label="Close details" onClick={onClose}>
            <X size={18} />
          </IconButton>
        </Stack>
        <Typography variant="caption" color="text.secondary">
          {agent.projectName}
          {provider && ` · ${provider}`}
        </Typography>
        <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
          {agent.environments.map((environment) => (
            <Chip
              key={environment.name}
              label={environment.name}
              color={environment.name === env ? "primary" : "default"}
              variant={environment.name === env ? "filled" : "outlined"}
              onClick={() => onEnvChange(environment.name)}
            />
          ))}
        </Stack>
        <Box
          sx={{
            display: "grid",
            gap: 3,
            gridTemplateColumns: { xs: "1fr", lg: "3fr 2fr" },
          }}
        >
          <CardDetails
            card={card}
            compareEnvOptions={agent.environments.map((e) => e.name).filter((n) => n !== env)}
            compareEnv={compareEnv}
            compareCard={compareCard}
            onCompareEnvChange={setCompareEnv}
          />
          <Box data-testid="connect-section-placeholder">
            <Typography variant="body2" color="text.secondary">
              Connect details are coming soon.
            </Typography>
          </Box>
        </Box>
      </Stack>
    </Box>
  );
}
