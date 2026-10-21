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

import { useEffect } from "react";
import { Alert, Button, CircularProgress, Stack, Typography } from "@wso2/oxygen-ui";
import { useIssueTestAgentAPIKey } from "@agent-management-platform/api-client";
import { formatRelativeTime, monospaceInputSx } from "@agent-management-platform/shared-component";
import type { AgentCardPathParams } from "@agent-management-platform/types";
import { TextInput } from "@agent-management-platform/views";

export interface TestKeyIssuerProps {
  params: AgentCardPathParams;
  onKey: (key: string | null) => void;
}

/** Issuing rotates the caller's test key, so it happens only on a click. */
export function TestKeyIssuer({ params, onKey }: TestKeyIssuerProps) {
  const { mutate: issue, data, isPending, isError, isIdle } = useIssueTestAgentAPIKey();
  const apiKey = data?.apiKey ?? null;

  useEffect(() => onKey(apiKey), [apiKey, onKey]);

  if (isIdle) {
    return (
      <Button variant="outlined" size="small" onClick={() => issue(params)}>
        Issue test key
      </Button>
    );
  }

  return (
    <Stack spacing={1}>
      {isPending && <CircularProgress size={16} />}
      {isError && (
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
            )}.`}
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
        <Button size="small" onClick={() => issue(params)} disabled={isPending}>
          {apiKey ? "Rotate" : "Retry"}
        </Button>
      </Stack>
    </Stack>
  );
}
