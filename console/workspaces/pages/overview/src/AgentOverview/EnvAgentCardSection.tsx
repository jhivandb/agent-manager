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

import { useState } from "react";
import { Alert, Box, Button, Chip, CircularProgress, Typography } from "@wso2/oxygen-ui";
import { RefreshCw } from "@wso2/oxygen-ui-icons-react";
import { useGetAgent, useGetAgentCard, useRefreshAgentCard } from "@agent-management-platform/api-client";
import { CodeBlock, OverviewSectionCard } from "@agent-management-platform/shared-component";
import type { AgentCardStatus } from "@agent-management-platform/types";
import { AgentCardSourceForm } from "./AgentCardSourceForm";

interface EnvAgentCardSectionProps {
  orgId: string;
  projectId: string;
  agentId: string;
  envId: string;
  external?: boolean;
}

const STATUS_CHIP: Record<AgentCardStatus, { label: string; color: "warning" | "success" | "error" }> = {
  pending: { label: "Pending", color: "warning" },
  fetched: { label: "Fetched", color: "success" },
  failed: { label: "Failed", color: "error" },
};

const isNotFound = (error: unknown) => (error as { status?: number } | null)?.status === 404;

/** Stored A2A agent card for the selected environment. */
export function EnvAgentCardSection({
  orgId, projectId, agentId, envId, external,
}: EnvAgentCardSectionProps) {
  const params = { orgName: orgId, projName: projectId, agentName: agentId, envId };
  const { data: agent } = useGetAgent({ orgName: orgId, projName: projectId, agentName: agentId });
  const isA2A = agent?.agentType?.subType === "a2a-agent";
  const { data: card, isLoading, isError, error } = useGetAgentCard(params, { enabled: isA2A });
  const { mutate: refresh, isPending: isRefreshing } = useRefreshAgentCard();
  const [showJson, setShowJson] = useState(false);

  if (!isA2A) {
    return null;
  }
  const noSource = isError && isNotFound(error);

  return (
    <OverviewSectionCard
      title="Agent Card"
      headerAction={
        !noSource && (
          <Button
            size="small"
            variant="text"
            startIcon={isRefreshing ? <CircularProgress size={14} /> : <RefreshCw size={14} />}
            disabled={isRefreshing}
            onClick={() => refresh(params)}
          >
            Refresh
          </Button>
        )
      }
      sx={{ mb: 1.5 }}
    >
      {external && <AgentCardSourceForm params={params} currentUrl={card?.sourceUrl} />}
      {isLoading && <CircularProgress size={16} />}
      {noSource && (
        <Typography variant="body2" color="text.secondary">
          Register this agent&apos;s card URL for the environment to fetch its card.
        </Typography>
      )}
      {isError && !noSource && (
        <Typography variant="body2" color="error">Unable to load the agent card. Try again later.</Typography>
      )}
      {card && (
        <>
          <Box display="flex" alignItems="center" gap={1} sx={{ mb: 1 }}>
            <Chip
              variant="outlined"
              size="small"
              label={STATUS_CHIP[card.status].label}
              color={STATUS_CHIP[card.status].color}
            />
            {card.fetchedAt && (
              <Typography variant="caption" color="text.secondary">
                Fetched {new Date(card.fetchedAt).toLocaleString()}
              </Typography>
            )}
          </Box>
          {card.status === "failed" && card.lastError && (
            <Alert severity="error" sx={{ mb: 1 }}>Fetch failed: {card.lastError}</Alert>
          )}
          {card.card && (
            <>
              <Typography variant="subtitle2">{card.card.name}</Typography>
              {card.card.version && <Typography variant="caption">Version {card.card.version}</Typography>}
              {card.card.description && <Typography variant="body2" sx={{ mb: 1 }}>{card.card.description}</Typography>}
              <Typography variant="overline">Interfaces</Typography>
              {card.card.supportedInterfaces.map((iface) => (
                <Box key={`${iface.protocolBinding ?? ""}-${iface.url}`} display="flex" gap={1} alignItems="center">
                  {iface.protocolBinding && <Chip size="small" label={iface.protocolBinding} />}
                  <Typography variant="body2">{iface.url}</Typography>
                </Box>
              ))}
              <Typography variant="overline">Skills</Typography>
              {card.card.skills.map((skill) => (
                <Box key={skill.id ?? skill.name} sx={{ mb: 0.5 }}>
                  <Typography variant="body2" fontWeight={600}>{skill.name}</Typography>
                  {skill.description && <Typography variant="caption">{skill.description}</Typography>}
                  <Box display="flex" gap={0.5} flexWrap="wrap">
                    {(skill.tags ?? []).map((tag) => <Chip key={tag} size="small" variant="outlined" label={tag} />)}
                  </Box>
                </Box>
              ))}
              <Button size="small" variant="text" onClick={() => setShowJson((v) => !v)}>
                {showJson ? "Hide JSON" : "View JSON"}
              </Button>
              {showJson && (
                <CodeBlock code={JSON.stringify(card.card, null, 2)} language="json" fieldId="agent-card-json" analyticsId="agent-card-json" />
              )}
            </>
          )}
        </>
      )}
    </OverviewSectionCard>
  );
}
