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

import { useEffect, useState } from "react";
import { Box, Button, IconButton, TextField, Tooltip } from "@wso2/oxygen-ui";
import { Check, X } from "@wso2/oxygen-ui-icons-react";
import { useSetAgentCardSource } from "@agent-management-platform/api-client";
import type { AgentCardPathParams } from "@agent-management-platform/types";
import { TextInput } from "@agent-management-platform/views";

interface AgentCardSourceFormProps {
  params: AgentCardPathParams;
  currentUrl?: string;
  /** Compact header editor with tick/X; requires onDone. */
  inline?: boolean;
  /** Called after a successful save or on cancel. */
  onDone?: () => void;
}

const URL_MAX = 2048;
const INVALID_HINT = "Enter a public http(s) URL of at most 2048 characters";

/** Card URL for an external A2A agent in one environment. */
export function AgentCardSourceForm({ params, currentUrl, inline, onDone }: AgentCardSourceFormProps) {
  const [url, setUrl] = useState(currentUrl ?? "");
  useEffect(() => setUrl(currentUrl ?? ""), [currentUrl]);
  const { mutate: save, isPending: isSaving } = useSetAgentCardSource();

  const trimmed = url.trim();
  const invalid = trimmed !== "" && (!/^https?:\/\//i.test(trimmed) || trimmed.length > URL_MAX);
  const canSave = !!trimmed && !invalid && trimmed !== currentUrl && !isSaving;
  const submit = () => {
    if (canSave) save({ params, body: { url: trimmed } }, { onSuccess: onDone });
  };

  if (inline) {
    return (
      <Box display="flex" alignItems="center" gap={0.5} sx={{ flex: 1, minWidth: 0 }}>
        <TextField
          size="small"
          autoFocus
          fullWidth
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
            if (e.key === "Escape") onDone?.();
          }}
          error={invalid}
          inputProps={{ "aria-label": "Agent card URL", style: { fontFamily: "monospace" } }}
        />
        <Tooltip title={invalid ? INVALID_HINT : "Save"}>
          <span>
            <IconButton size="small" color="primary" disabled={!canSave} onClick={submit}>
              <Check size={16} />
            </IconButton>
          </span>
        </Tooltip>
        <Tooltip title="Cancel">
          <IconButton size="small" onClick={onDone}>
            <X size={16} />
          </IconButton>
        </Tooltip>
      </Box>
    );
  }

  return (
    <Box display="flex" gap={1} alignItems="flex-start" sx={{ mb: 1 }}>
      <TextInput
        label="Agent card URL"
        placeholder="https://agent.example.com/.well-known/agent-card.json"
        value={url}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => setUrl(e.target.value)}
        error={invalid}
        helperText={invalid ? INVALID_HINT : undefined}
        fullWidth
      />
      <Button variant="contained" size="small" disabled={!canSave} onClick={submit} sx={{ mt: 3 }}>
        Save
      </Button>
    </Box>
  );
}
