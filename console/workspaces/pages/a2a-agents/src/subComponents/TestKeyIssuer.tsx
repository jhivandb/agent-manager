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
import { Alert, Button, CircularProgress, Stack, Typography } from "@wso2/oxygen-ui";
import { useTestAgentAPIKey } from "@agent-management-platform/api-client";
import { formatRelativeTime, monospaceInputSx } from "@agent-management-platform/shared-component";
import type { AgentCardPathParams } from "@agent-management-platform/types";
import { TextInput } from "@agent-management-platform/views";
import { paramsIdentity } from "../utils/paramsIdentity";

export interface TestKeyIssuerProps {
  params: AgentCardPathParams;
  onKey: (key: string | null) => void;
}

/**
 * Every run of the test-key query rotates the caller's key, so it runs only
 * after a click for these exact params. Keying the request to the params keeps
 * the first render after a switch disabled; dropping it afterwards makes coming
 * back to the same params ask for a new click.
 */
export function TestKeyIssuer({ params, onKey }: TestKeyIssuerProps) {
  const identity = paramsIdentity(params);
  const [requestedFor, setRequestedFor] = useState<string | null>(null);
  const requested = requestedFor === identity;
  useEffect(() => setRequestedFor((prior) => (prior === identity ? prior : null)), [identity]);

  const { data, isFetching, isError, refetch } = useTestAgentAPIKey(params, {
    enabled: requested,
  });
  const apiKey = requested ? (data?.apiKey ?? null) : null;

  useEffect(() => onKey(apiKey), [apiKey, onKey]);

  // A cached key may be minutes from expiry while the 9-minute refresh only
  // starts now, so a click always issues; with nothing cached, enabling does.
  const issue = () => {
    setRequestedFor(identity);
    if (data) void refetch();
  };

  if (!requested) {
    return (
      <Button variant="outlined" size="small" onClick={issue}>
        Issue test key
      </Button>
    );
  }

  return (
    <Stack spacing={1}>
      {isFetching && <CircularProgress size={16} />}
      {isError && !apiKey && (
        <Typography variant="body2" color="error">
          {"Couldn't issue a test key. Try again."}
        </Typography>
      )}
      {apiKey && (
        <>
          <TextInput
            label="Test API key"
            size="small"
            fullWidth
            value={apiKey}
            copyable
            copyTooltipText="Copy API key"
            slotProps={{ input: { readOnly: true } }}
            sx={monospaceInputSx}
          />
          <Typography variant="caption" color="text.secondary">
            {`Your personal test key for this environment. It expires ${formatRelativeTime(
              data?.expiresAt,
            )}; while this panel stays open a new key replaces it before then.`}
          </Typography>
        </>
      )}
      {apiKey && data?.gatewayConnected === false && (
        <Alert severity="warning">
          The gateway is not connected to the control plane right now. The API key has been
          stored but will only work once the gateway reconnects.
        </Alert>
      )}
      <Stack direction="row">
        <Button size="small" onClick={() => refetch()} disabled={isFetching}>
          {apiKey ? "Rotate" : "Retry"}
        </Button>
      </Stack>
    </Stack>
  );
}
