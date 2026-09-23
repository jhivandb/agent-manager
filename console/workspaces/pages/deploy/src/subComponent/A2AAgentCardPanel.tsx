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

import { useGetA2AAgentCard, useRefreshA2AAgentCard } from "@agent-management-platform/api-client";
import { CodeBlock } from "@agent-management-platform/shared-component";
import type { A2AAgentCardResponse } from "@agent-management-platform/types";
import { RefreshCw } from "@wso2/oxygen-ui-icons-react";
import {
  Alert,
  Button,
  Card,
  CircularProgress,
  Skeleton,
  Stack,
  Typography,
} from "@wso2/oxygen-ui";
import { formatDistanceToNow } from "date-fns";

interface A2AAgentCardPanelProps {
  orgName: string;
  projName: string;
  agentName: string;
  environment: string;
}

// Renders the same lifecycle state Task 8's API exposes, but split apart so a
// stale-but-served card ("failed", card present) never looks the same as a
// document the gateway refused outright ("rejected") — that distinction is
// the whole point: one still answers discovery requests, the other doesn't.
type CardPresentation = {
  severity?: "error" | "warning";
  heading: string;
  showCard: boolean;
  showRefresh: boolean;
  lastErrorLabel: string;
};

const DEFAULTS: Omit<CardPresentation, "heading"> = {
  showCard: false,
  showRefresh: true,
  lastErrorLabel: "Last error",
};

function presentCardState(data: A2AAgentCardResponse): CardPresentation {
  const hasCard = !!(data.card ?? null);

  switch (data.status) {
    case "pending":
      return { ...DEFAULTS, heading: "Card not fetched yet", showRefresh: false };
    case "routed":
      return {
        ...DEFAULTS,
        heading: "Waiting for the agent to serve its card",
        showRefresh: false,
      };
    case "failed":
      if (!hasCard && !data.routedAt) {
        return { ...DEFAULTS, severity: "error", heading: "Routing never succeeded" };
      }
      if (!hasCard) {
        return {
          ...DEFAULTS,
          severity: "warning",
          heading:
            "Card never arrived — the gateway is serving the agent's own card, with its URLs rewritten",
        };
      }
      return {
        ...DEFAULTS,
        severity: "warning",
        heading: "Last refresh failed — the gateway is still serving the card below",
        showCard: true,
      };
    case "rejected":
      return {
        ...DEFAULTS,
        severity: "error",
        heading: "The gateway refused this document — discovery is currently broken",
        showCard: true,
        lastErrorLabel: "Gateway error",
      };
    case "published":
    default:
      return { ...DEFAULTS, heading: "Published", showCard: true };
  }
}

export function A2AAgentCardPanel(props: A2AAgentCardPanelProps) {
  const { orgName, projName, agentName, environment } = props;
  const { data, isLoading, isError } = useGetA2AAgentCard({
    orgName,
    projName,
    agentName,
    envId: environment,
  });
  const { mutate: refresh, isPending: isRefreshing } = useRefreshA2AAgentCard();

  if (isLoading) {
    return <Skeleton variant="rounded" height={72} />;
  }

  // 404 means no publication row: not an A2A agent, or never deployed here.
  // Any other failure is surfaced by the query's own error snackbar already —
  // this panel just has nothing to add.
  if (isError || !data) {
    return null;
  }

  const card = data.card ?? null;
  const { heading, severity, showCard, showRefresh, lastErrorLabel } = presentCardState(data);

  const handleRefresh = () => {
    refresh({ orgName, projName, agentName, envId: environment });
  };

  return (
    <Card variant="outlined" sx={{ padding: 1.4 }}>
      <Stack gap={1.5}>
        <Stack direction="row" gap={1} alignItems="center" justifyContent="space-between">
          <Typography variant="h6">A2A Agent Card</Typography>
          {showRefresh && (
            <Button
              variant="text"
              size="small"
              color="inherit"
              sx={{ padding: 0.5 }}
              startIcon={isRefreshing ? <CircularProgress size={14} /> : <RefreshCw size={16} />}
              onClick={handleRefresh}
              disabled={isRefreshing}
            >
              Refresh
            </Button>
          )}
        </Stack>

        {severity ? (
          <Alert severity={severity}>{heading}</Alert>
        ) : (
          <Typography variant="body2" color="text.secondary">
            {heading}
          </Typography>
        )}

        {data.lastError && (
          <Typography variant="body2" color="text.secondary">
            {lastErrorLabel}: {data.lastError}
          </Typography>
        )}

        {data.fetchedAt && (
          <Typography variant="body2" color="text.secondary">
            Last fetched {formatDistanceToNow(new Date(data.fetchedAt), { addSuffix: true })}
          </Typography>
        )}

        {showCard && card && <CodeBlock code={JSON.stringify(card, null, 2)} language="json" />}
      </Stack>
    </Card>
  );
}

export default A2AAgentCardPanel;
