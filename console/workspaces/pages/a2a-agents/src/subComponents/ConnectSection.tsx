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

import { useCallback, useEffect, useState } from "react";
import { Link, generatePath, useParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Typography,
} from "@wso2/oxygen-ui";
import { useGetA2AConnect, useGetAgentCard } from "@agent-management-platform/api-client";
import { CodeBlock, monospaceInputSx } from "@agent-management-platform/shared-component";
import {
  absoluteRouteMap,
  type A2ACatalogAgent,
  type A2AConnectResponse,
  type AgentCardPathParams,
} from "@agent-management-platform/types";
import { buildConfigureTabHref, TextInput } from "@agent-management-platform/views";
import { parseA2ACard } from "../utils/card";
import { resolveConnectEndpoints, type ConnectEndpoints } from "../utils/endpoints";
import { paramsIdentity } from "../utils/paramsIdentity";
import { buildA2ASnippet, type SnippetAuth, type SnippetLanguage } from "../utils/snippets";
import { TestKeyIssuer } from "./TestKeyIssuer";

type Security = A2AConnectResponse["security"];

const API_KEY_HEADER = "X-API-Key";
const API_KEY_PLACEHOLDER = "<YOUR_API_KEY>";
const ACCESS_TOKEN_PLACEHOLDER = "<ACCESS_TOKEN>";

const SNIPPET_LANGUAGES: { value: SnippetLanguage; label: string; highlight: string }[] = [
  { value: "python", label: "Python", highlight: "python" },
  { value: "typescript", label: "TypeScript", highlight: "typescript" },
  { value: "curl", label: "curl", highlight: "bash" },
];

const agentRoutes = absoluteRouteMap.children.org.children.projects.children.agents;

function manageSecurityHref(orgId: string, agent: A2ACatalogAgent, env: string): string {
  if (agent.kind === "external") {
    return buildConfigureTabHref(orgId, agent.projectName, agent.agentName, "a2a", env);
  }
  return generatePath(agentRoutes.children.environment.children.deploy.path, {
    orgId,
    projectId: agent.projectName,
    agentId: agent.agentName,
    envId: env,
  });
}

function snippetAuth(security: Security, testKey: string | null): SnippetAuth | null {
  if (security.mode === "apiKey") {
    return {
      header: security.apiKeyHeader || API_KEY_HEADER,
      value: testKey ?? API_KEY_PLACEHOLDER,
    };
  }
  if (security.mode === "oauth") {
    const prefix = security.oauth?.authHeaderPrefix;
    return {
      header: security.oauth?.headerName || "Authorization",
      value: prefix ? `${prefix} ${ACCESS_TOKEN_PLACEHOLDER}` : ACCESS_TOKEN_PLACEHOLDER,
    };
  }
  return null;
}

const Code = ({ children }: { children: string }) => (
  <Box component="code" sx={{ fontFamily: "monospace" }}>
    {children}
  </Box>
);

function PublicationWarning({ publication }: { publication: A2AConnectResponse["publication"] }) {
  if (publication.status !== "pending" && publication.status !== "failed") return null;
  return (
    <Alert severity="warning">
      The gateway may not yet enforce these settings.
      {publication.lastError && ` ${publication.lastError}`}
    </Alert>
  );
}

function CopyableUrl({ label, url }: { label: string; url: string }) {
  return (
    <TextInput
      label={label}
      size="small"
      fullWidth
      value={url}
      copyable
      slotProps={{ input: { readOnly: true } }}
      sx={monospaceInputSx}
    />
  );
}

function EndpointList({ endpoints, declared, cardUrl }: ConnectEndpoints) {
  return (
    <Stack spacing={1}>
      {endpoints.length === 0 && (
        <Typography variant="body2" color="text.secondary">
          The card declares no endpoints yet.
        </Typography>
      )}
      <Stack spacing={1} data-testid="connect-endpoints">
        {endpoints.map((endpoint) => (
          <CopyableUrl key={`${endpoint.label} ${endpoint.url}`} {...endpoint} />
        ))}
      </Stack>
      {cardUrl && <CopyableUrl label="Agent card" url={cardUrl} />}
      {declared.length > 0 && (
        <Box>
          <Typography variant="caption" color="text.secondary">
            Declared by the card
          </Typography>
          {declared.map((iface) => (
            <Typography
              key={`${iface.protocolBinding} ${iface.url}`}
              variant="caption"
              color="text.secondary"
              component="div"
              sx={{ fontFamily: "monospace", overflowWrap: "anywhere" }}
            >
              {iface.url}
              {iface.protocolBinding && ` · ${iface.protocolBinding}`}
            </Typography>
          ))}
        </Box>
      )}
    </Stack>
  );
}

interface SecuritySummaryProps {
  connect: A2AConnectResponse;
  cardSecuritySchemes: string[];
  params: AgentCardPathParams;
  onKey: (key: string | null) => void;
}

function SecuritySummary({ connect, cardSecuritySchemes, params, onKey }: SecuritySummaryProps) {
  const { security } = connect;
  switch (security.mode) {
    case "apiKey":
      return (
        <Stack spacing={1}>
          <Typography variant="body2">
            API key required in header <Code>{security.apiKeyHeader || API_KEY_HEADER}</Code>
          </Typography>
          {connect.isProduction ? (
            <Typography variant="body2" color="text.secondary">
              {"Keys for production are issued by the agent's owners."}
            </Typography>
          ) : (
            <TestKeyIssuer params={params} onKey={onKey} />
          )}
        </Stack>
      );
    case "oauth": {
      const oauth = security.oauth;
      return (
        <Stack spacing={0.5}>
          <Typography variant="body2">
            OAuth access token required in header <Code>{oauth?.headerName || "Authorization"}</Code>
            {oauth?.authHeaderPrefix && " with prefix "}
            {oauth?.authHeaderPrefix && <Code>{oauth.authHeaderPrefix}</Code>}
          </Typography>
          <Typography variant="body2" color="text.secondary">
            Issuers:
          </Typography>
          {(oauth?.issuers ?? []).map(({ name, issuer }) => (
            <Typography key={name} variant="body2" sx={{ pl: 1 }}>
              {`${name} — ${issuer || "not found"}`}
            </Typography>
          ))}
          <Typography variant="body2" color="text.secondary">
            {`Audiences: ${oauth?.audiences.length ? oauth.audiences.join(", ") : "any"}`}
          </Typography>
        </Stack>
      );
    }
    case "none":
      return <Typography variant="body2">No authentication required.</Typography>;
    case "unmanaged":
      return (
        <Typography variant="body2">
          {`Security isn't managed by the platform. The card declares: ${
            cardSecuritySchemes.length ? cardSecuritySchemes.join(", ") : "nothing"
          }.`}
        </Typography>
      );
    default:
      return (
        <Typography variant="body2">
          {"Security settings couldn't be determined for this environment."}
        </Typography>
      );
  }
}

function QuickStart({
  cardUrl,
  endpointUrl,
  auth,
  authUnmanaged,
}: Parameters<typeof buildA2ASnippet>[1]) {
  const [language, setLanguage] = useState<SnippetLanguage>("python");
  const highlight = SNIPPET_LANGUAGES.find((l) => l.value === language)?.highlight;
  return (
    <Stack spacing={1}>
      <ToggleButtonGroup
        size="small"
        exclusive
        value={language}
        onChange={(_, next: SnippetLanguage | null) => next && setLanguage(next)}
      >
        {SNIPPET_LANGUAGES.map(({ value, label }) => (
          <ToggleButton key={value} value={value}>
            {label}
          </ToggleButton>
        ))}
      </ToggleButtonGroup>
      <CodeBlock
        code={buildA2ASnippet(language, { cardUrl, endpointUrl, auth, authUnmanaged })}
        language={highlight}
        fieldId="a2a-connect-snippet"
      />
    </Stack>
  );
}

const SectionTitle = ({ children }: { children: string }) => (
  <Typography variant="subtitle2">{children}</Typography>
);

export interface ConnectSectionProps {
  agent: A2ACatalogAgent;
  env: string;
}

export function ConnectSection({ agent, env }: ConnectSectionProps) {
  const { orgId } = useParams<{ orgId: string }>();
  const params: AgentCardPathParams = {
    orgName: orgId,
    projName: agent.projectName,
    agentName: agent.agentName,
    envId: env,
  };
  const identity = paramsIdentity(params);
  const { data: connect, isLoading, isError } = useGetA2AConnect(params);
  const { data: card } = useGetAgentCard(params);

  const [issued, setIssued] = useState<{ identity: string; key: string | null } | null>(null);
  const testKey = issued?.identity === identity ? issued.key : null;
  const onKey = useCallback((key: string | null) => setIssued({ identity, key }), [identity]);
  useEffect(() => setIssued((prior) => (prior?.identity === identity ? prior : null)), [identity]);

  const parsedCard = parseA2ACard(card?.card);

  let body;
  if (isLoading) {
    body = <CircularProgress size={16} />;
  } else if (isError || !connect) {
    body = (
      <Typography variant="body2" color="error">
        {"Couldn't load connection details for this environment."}
      </Typography>
    );
  } else {
    const endpoints = resolveConnectEndpoints(
      connect.gatewayUrl,
      card?.sourceUrl ?? "",
      parsedCard.interfaces,
    );
    body = (
      <Stack spacing={2}>
        <PublicationWarning publication={connect.publication} />
        <Stack spacing={1}>
          <SectionTitle>Endpoint</SectionTitle>
          <EndpointList {...endpoints} />
        </Stack>
        <Stack spacing={1}>
          <SectionTitle>Security</SectionTitle>
          <SecuritySummary
            connect={connect}
            cardSecuritySchemes={parsedCard.securitySchemes}
            params={params}
            onKey={onKey}
          />
        </Stack>
        {endpoints.snippetEndpoint && (
          <Stack spacing={1}>
            <SectionTitle>Quick start</SectionTitle>
            <QuickStart
              cardUrl={endpoints.cardUrl}
              endpointUrl={endpoints.snippetEndpoint}
              auth={snippetAuth(connect.security, testKey)}
              authUnmanaged={connect.security.mode !== "none"}
            />
          </Stack>
        )}
      </Stack>
    );
  }

  return (
    <Stack spacing={1.5} data-testid="connect-section" sx={{ minWidth: 0 }}>
      <Stack direction="row" alignItems="center" justifyContent="space-between">
        <Typography variant="subtitle1">Connect</Typography>
        {orgId && (
          <Button size="small" component={Link} to={manageSecurityHref(orgId, agent, env)}>
            Manage security
          </Button>
        )}
      </Stack>
      {body}
    </Stack>
  );
}
