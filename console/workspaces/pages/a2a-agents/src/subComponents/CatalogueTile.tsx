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

import { Link } from "react-router-dom";
import { Box, Chip, Divider, Form, Stack, Tooltip, Typography } from "@wso2/oxygen-ui";
import type {
  A2ACatalogAgent,
  A2ACatalogEnvironment,
} from "@agent-management-platform/types";

const MAX_SKILL_CHIPS = 2;

const STATUS_COLOR: Record<A2ACatalogEnvironment["status"], string> = {
  fetched: "success.main",
  pending: "warning.main",
  failed: "error.main",
};

const SMALL_CHIP_SX = { height: 20, fontSize: "0.7rem", "& .MuiChip-label": { px: 0.75 } };

export interface CatalogueTileProps {
  agent: A2ACatalogAgent;
  to: string;
  /** Outlines the tile when one of its environments matched the search. */
  emphasised: boolean;
}

function leadCardLabel(agent: A2ACatalogAgent): string | null {
  const lead = agent.environments.find((e) => e.name === agent.leadEnvironment);
  if (lead?.status === "pending") return "card pending";
  if (lead?.status === "failed") return "card failed";
  return null;
}

function EnvironmentStatus({ environment }: { environment: A2ACatalogEnvironment }) {
  return (
    <Stack direction="row" spacing={0.5} alignItems="center" sx={{ flexShrink: 0 }}>
      <Box
        data-testid="environment-status-dot"
        title={environment.status}
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          bgcolor: STATUS_COLOR[environment.status],
        }}
      />
      <Typography variant="caption" color="text.secondary">
        {environment.name}
      </Typography>
      {environment.proxied && <Chip size="small" label="proxied" sx={SMALL_CHIP_SX} />}
      {environment.cardDiffers && (
        <Chip size="small" color="warning" label="card differs" sx={SMALL_CHIP_SX} />
      )}
    </Stack>
  );
}

export function CatalogueTile({ agent, to, emphasised }: CatalogueTileProps) {
  const name = agent.name || agent.agentName;
  const cardLabel = leadCardLabel(agent);
  const extraSkills = agent.skills.length - MAX_SKILL_CHIPS;

  return (
    <Link to={to} style={{ textDecoration: "none" }}>
      <Form.CardButton
        data-testid="catalogue-tile"
        selected={emphasised}
        sx={{
          width: "100%",
          height: "100%",
          minHeight: 200,
          textAlign: "left",
          display: "flex",
          flexDirection: "column",
          justifyContent: "flex-start",
        }}
      >
        <Form.CardHeader
          sx={{
            width: "100%",
            minWidth: 0,
            overflow: "hidden",
            "& .MuiCardHeader-content": { minWidth: 0 },
          }}
          title={
            <Stack spacing={1} sx={{ minWidth: 0 }}>
              <Tooltip title={name} placement="top">
                <Typography variant="h6" noWrap>
                  {name}
                </Typography>
              </Tooltip>
              <Stack direction="row" spacing={1} alignItems="center" sx={{ opacity: 0.85 }}>
                <Chip label={agent.kind} size="small" variant="outlined" color="primary" sx={SMALL_CHIP_SX} />
                <Divider orientation="vertical" flexItem sx={{ height: 16, alignSelf: "center" }} />
                <Typography variant="caption" color="text.secondary" noWrap>
                  {agent.projectName}
                </Typography>
                {cardLabel && (
                  <Typography variant="caption" color="warning.main" noWrap>
                    {cardLabel}
                  </Typography>
                )}
              </Stack>
            </Stack>
          }
        />
        <Form.CardContent
          sx={{
            width: "100%",
            display: "flex",
            flexDirection: "column",
            justifyContent: "space-between",
            flexGrow: 1,
            gap: 1,
            // Avoids MUI's `:last-child` padding bump toggling after first hover.
            pb: 2,
            "&.MuiCardContent-root:last-child": { pb: 2 },
          }}
        >
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{
              display: "-webkit-box",
              WebkitLineClamp: 2,
              WebkitBoxOrient: "vertical",
              overflow: "hidden",
            }}
          >
            {agent.description}
          </Typography>
          <Stack spacing={1}>
            {agent.skills.length > 0 && (
              <Stack direction="row" spacing={0.75} alignItems="center" sx={{ minWidth: 0, overflow: "hidden" }}>
                {agent.skills.slice(0, MAX_SKILL_CHIPS).map((skill) => (
                  <Chip key={skill} label={skill} size="small" sx={{ flexShrink: 0 }} />
                ))}
                {extraSkills > 0 && (
                  <Tooltip title={agent.skills.join(", ")} placement="top">
                    <Typography variant="caption" color="text.secondary" noWrap>
                      {`+${extraSkills} more`}
                    </Typography>
                  </Tooltip>
                )}
              </Stack>
            )}
            <Stack direction="row" spacing={1.5} flexWrap="wrap" useFlexGap>
              {agent.environments.map((environment) => (
                <EnvironmentStatus key={environment.name} environment={environment} />
              ))}
            </Stack>
          </Stack>
        </Form.CardContent>
      </Form.CardButton>
    </Link>
  );
}
