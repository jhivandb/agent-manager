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

import { useState } from "react";
import { generatePath, useParams } from "react-router-dom";
import { Alert, Button, Chip, Stack, Typography } from "@wso2/oxygen-ui";
import { Plug } from "@wso2/oxygen-ui-icons-react";
import {
  DrawerContent,
  DrawerHeader,
  DrawerWrapper,
  PageLayout,
} from "@agent-management-platform/views";
import { normalizeVersion, parseA2ACard } from "@agent-management-platform/shared-component";
import { useGetA2ACatalogEntry, useGetAgentCard } from "@agent-management-platform/api-client";
import {
  absoluteRouteMap,
  type A2ACatalogAgent,
  type AgentCardResponse,
} from "@agent-management-platform/types";
import { useReplaceSearchParams } from "../utils/useReplaceSearchParams";
import { CardDetails } from "./CardDetails";
import { ConnectSection } from "./ConnectSection";

const ENV_PARAM = "env";

interface AgentDetailsProps {
  orgName: string;
  agent: A2ACatalogAgent;
  env: string;
  card: AgentCardResponse | null | undefined;
  isCardLoading: boolean;
  onEnvChange: (env: string) => void;
}

/** One environment's card and connect drawer; keyed by env, so no state outlives a switch. */
function AgentDetails({
  orgName,
  agent,
  env,
  card,
  isCardLoading,
  onEnvChange,
}: AgentDetailsProps) {
  const [compareEnv, setCompareEnv] = useState("");
  const [connectOpen, setConnectOpen] = useState(false);
  // Stays mounted once opened, so closing the drawer keeps an issued test key.
  const [connectMounted, setConnectMounted] = useState(false);
  const openConnect = () => {
    setConnectOpen(true);
    setConnectMounted(true);
  };

  const { data: compareCard } = useGetAgentCard(
    { orgName, projName: agent.projectName, agentName: agent.agentName, envId: compareEnv },
    { enabled: compareEnv !== "", poll: false },
  );

  return (
    <Stack spacing={2}>
      <Stack direction="row" spacing={2} alignItems="center" justifyContent="space-between">
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
        <Button
          variant="contained"
          size="small"
          startIcon={<Plug size={16} />}
          onClick={openConnect}
        >
          Connect
        </Button>
      </Stack>
      <CardDetails
        card={card}
        isLoading={isCardLoading}
        compareEnvOptions={agent.environments.map((e) => e.name).filter((n) => n !== env)}
        compareEnv={compareEnv}
        compareCard={compareCard}
        onCompareEnvChange={setCompareEnv}
      />
      <DrawerWrapper
        open={connectOpen}
        keepMounted={connectMounted}
        onClose={() => setConnectOpen(false)}
        maxWidth={700}
      >
        <DrawerHeader
          icon={<Plug size={24} />}
          title={`Connect · ${env}`}
          onClose={() => setConnectOpen(false)}
        />
        <DrawerContent>
          <ConnectSection orgName={orgName} agent={agent} env={env} card={card} />
        </DrawerContent>
      </DrawerWrapper>
    </Stack>
  );
}

export function A2AAgentView() {
  const { orgId = "", projectName = "", agentName = "" } = useParams<{
    orgId: string;
    projectName: string;
    agentName: string;
  }>();
  const [searchParams, updateParams] = useReplaceSearchParams();

  const { data, isLoading, error } = useGetA2ACatalogEntry({
    orgName: orgId,
    projName: projectName,
    agentName,
  });
  const agent = data ?? undefined;
  const env = searchParams.get(ENV_PARAM) ?? agent?.leadEnvironment ?? "";

  const { data: card, isLoading: isCardLoading } = useGetAgentCard(
    { orgName: orgId, projName: projectName, agentName, envId: env },
    { enabled: agent !== undefined },
  );
  const parsed = parseA2ACard(card?.card);
  const version = parsed.version || agent?.version;
  const kind = card?.source ?? agent?.kind;

  const changeEnv = (name: string) => updateParams((params) => params.set(ENV_PARAM, name));

  return (
    <PageLayout
      title={agent?.name || agentName}
      backHref={generatePath(absoluteRouteMap.children.org.children.a2aAgents.path, { orgId })}
      backLabel="Back to A2A Agents"
      isLoading={isLoading}
      titleTail={
        agent && (
          <Stack direction="row" spacing={1} sx={{ ml: 1 }}>
            {version && <Chip size="small" variant="outlined" label={normalizeVersion(version)} />}
            {kind && <Chip size="small" label={kind} />}
          </Stack>
        )
      }
      description={
        <Typography variant="body2" color="text.secondary">
          {projectName}
          {parsed.providerOrganization && ` · ${parsed.providerOrganization}`}
        </Typography>
      }
    >
      {error ? (
        <Alert severity="error">Failed to load the agent. Please try again.</Alert>
      ) : agent ? (
        <AgentDetails
          key={env}
          orgName={orgId}
          agent={agent}
          env={env}
          card={card}
          isCardLoading={isCardLoading}
          onEnvChange={changeEnv}
        />
      ) : (
        !isLoading && (
          <Typography color="text.secondary">
            This agent was not found in the catalogue.
          </Typography>
        )
      )}
    </PageLayout>
  );
}
