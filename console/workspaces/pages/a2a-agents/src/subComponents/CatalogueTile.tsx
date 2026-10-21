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

import { Box, Card, Chip, Stack, Typography } from "@wso2/oxygen-ui";
import type {
  A2ACatalogAgent,
  A2ACatalogEnvironment,
} from "@agent-management-platform/types";

const MAX_SKILL_CHIPS = 4;

export interface CatalogueTileProps {
  agent: A2ACatalogAgent;
  query: string;
  selected: boolean;
  compact: boolean;
  onSelect: () => void;
}

const STATUS_COLOR: Record<A2ACatalogEnvironment["status"], string> = {
  fetched: "success.main",
  pending: "warning.main",
  failed: "error.main",
};

function leadCardLabel(agent: A2ACatalogAgent): string | null {
  const lead = agent.environments.find((e) => e.name === agent.leadEnvironment);
  if (lead?.status === "pending") return "card pending";
  if (lead?.status === "failed") return "card failed";
  return null;
}

function EnvironmentChip({ environment }: { environment: A2ACatalogEnvironment }) {
  return (
    <Stack direction="row" spacing={0.5} alignItems="center">
      <Box
        data-testid="environment-status-dot"
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          bgcolor: STATUS_COLOR[environment.status],
        }}
      />
      <Chip size="small" variant="outlined" label={environment.name} />
      {environment.proxied && <Chip size="small" label="proxied" />}
      {environment.cardDiffers && (
        <Chip size="small" color="warning" label="card differs" />
      )}
    </Stack>
  );
}

export function CatalogueTile({
  agent,
  query,
  selected,
  compact,
  onSelect,
}: CatalogueTileProps) {
  const emphasised = query !== "" && agent.environments.some((e) => e.matched);
  const cardLabel = leadCardLabel(agent);

  return (
    <Card
      data-testid="catalogue-tile"
      role="option"
      aria-selected={selected}
      tabIndex={0}
      onClick={onSelect}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onSelect();
        }
      }}
      sx={{
        p: 2,
        cursor: "pointer",
        border: "1px solid",
        borderColor: selected || emphasised ? "primary.main" : "divider",
        bgcolor: selected ? "action.selected" : "background.paper",
      }}
    >
      <Stack spacing={1}>
        <Stack direction="row" spacing={1} alignItems="center">
          <Typography variant="subtitle1" fontWeight={600} noWrap sx={{ flex: 1 }}>
            {agent.name || agent.agentName}
          </Typography>
          <Chip size="small" label={agent.kind} />
        </Stack>
        <Typography variant="caption" color="text.secondary" noWrap>
          {agent.projectName}
        </Typography>
        {cardLabel && (
          <Typography variant="caption" color="warning.main">
            {cardLabel}
          </Typography>
        )}
        {!compact && (
          <>
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{
                display: "-webkit-box",
                WebkitLineClamp: 2,
                WebkitBoxOrient: "vertical",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {agent.description}
            </Typography>
            <Stack direction="row" spacing={0.5} flexWrap="wrap" useFlexGap>
              {agent.skills.slice(0, MAX_SKILL_CHIPS).map((skill) => (
                <Chip key={skill} size="small" variant="outlined" label={skill} />
              ))}
            </Stack>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              {agent.environments.map((environment) => (
                <EnvironmentChip key={environment.name} environment={environment} />
              ))}
            </Stack>
          </>
        )}
      </Stack>
    </Card>
  );
}
