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
import {
  Box,
  Button,
  Chip,
  FormControl,
  InputLabel,
  MenuItem,
  Select,
  Stack,
  Typography,
} from "@wso2/oxygen-ui";
import { CodeBlock, formatRelativeTime } from "@agent-management-platform/shared-component";
import type { AgentCardResponse } from "@agent-management-platform/types";
import { diffSkills, parseA2ACard, type ParsedSkill } from "../utils/card";

export interface CardDetailsProps {
  card: AgentCardResponse | null | undefined;
  isLoading?: boolean;
  compareEnvOptions: string[];
  compareEnv: string;
  compareCard: AgentCardResponse | null | undefined;
  onCompareEnvChange: (env: string) => void;
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <Typography variant="subtitle2" color="text.secondary">
      {children}
    </Typography>
  );
}

function ChipRow({ label, values }: { label?: string; values: string[] }) {
  if (values.length === 0) return null;
  return (
    <Stack direction="row" spacing={0.5} flexWrap="wrap" useFlexGap alignItems="center">
      {label && (
        <Typography variant="caption" color="text.secondary">
          {label}
        </Typography>
      )}
      {values.map((value) => (
        <Chip key={value} size="small" variant="outlined" label={value} />
      ))}
    </Stack>
  );
}

function SkillItem({ skill }: { skill: ParsedSkill }) {
  return (
    <Stack spacing={0.5} sx={{ p: 1.5, border: "1px solid", borderColor: "divider", borderRadius: 1 }}>
      <Typography variant="subtitle2">{skill.name}</Typography>
      {skill.description && (
        <Typography variant="body2" color="text.secondary">
          {skill.description}
        </Typography>
      )}
      <ChipRow values={skill.tags} />
      <ChipRow label="Input" values={skill.inputModes} />
      <ChipRow label="Output" values={skill.outputModes} />
      {skill.examples.map((example) => (
        <Typography key={example} variant="caption" color="text.secondary">
          e.g. {example}
        </Typography>
      ))}
    </Stack>
  );
}

function CardStatus({ card }: { card: AgentCardResponse }) {
  if (card.status === "fetched") {
    return (
      <Typography variant="caption" color="text.secondary">
        Fetched {formatRelativeTime(card.fetchedAt)}
      </Typography>
    );
  }
  return (
    <Stack spacing={0.5}>
      <Typography variant="body2" color="warning.main">
        {card.status === "pending" ? "Card pending" : "Card fetch failed"}
      </Typography>
      {card.lastError && (
        <Typography variant="caption" color="error.main">
          {card.lastError}
        </Typography>
      )}
    </Stack>
  );
}

type MaybeCard = AgentCardResponse | null | undefined;

function SkillDiff({ base, other }: { base: MaybeCard; other: MaybeCard }) {
  if (!other?.card) {
    return (
      <Typography variant="caption" color="text.secondary">
        No card available to compare.
      </Typography>
    );
  }
  const { added, removed, changed } = diffSkills(
    parseA2ACard(base?.card),
    parseA2ACard(other.card),
  );
  const lines = [
    ...added.map((name) => `Added: ${name}`),
    ...removed.map((name) => `Removed: ${name}`),
    ...changed.map((name) => `Changed: ${name}`),
  ];
  if (lines.length === 0) {
    return (
      <Typography variant="caption" color="text.secondary">
        Skills are identical.
      </Typography>
    );
  }
  return (
    <Stack>
      {lines.map((line) => (
        <Typography key={line} variant="body2">
          {line}
        </Typography>
      ))}
    </Stack>
  );
}

export function CardDetails({
  card,
  isLoading = false,
  compareEnvOptions,
  compareEnv,
  compareCard,
  onCompareEnvChange,
}: CardDetailsProps) {
  const [showRaw, setShowRaw] = useState(false);

  if (isLoading && card === undefined) {
    return (
      <Typography variant="body2" color="text.secondary">
        Loading card…
      </Typography>
    );
  }

  if (!card) {
    return (
      <Typography variant="body2" color="text.secondary">
        No agent card is configured for this environment.
      </Typography>
    );
  }

  const parsed = parseA2ACard(card.card);
  const capabilityEntries = Object.entries(parsed.capabilities);

  return (
    <Stack spacing={2}>
      <CardStatus card={card} />
      {parsed.description && <Typography variant="body2">{parsed.description}</Typography>}

      <SectionTitle>Skills</SectionTitle>
      {parsed.skills.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          No skills declared.
        </Typography>
      ) : (
        <Stack spacing={1}>
          {parsed.skills.map((skill) => (
            <SkillItem key={skill.id || skill.name} skill={skill} />
          ))}
        </Stack>
      )}

      <SectionTitle>Capabilities</SectionTitle>
      <Stack>
        {capabilityEntries.map(([name, supported]) => (
          <Typography key={name} variant="body2">
            {`${supported ? "✓" : "✗"} ${name}`}
          </Typography>
        ))}
      </Stack>

      {compareEnvOptions.length > 0 && (
        <Stack spacing={1}>
          <FormControl size="small" sx={{ maxWidth: 240 }}>
            <InputLabel id="compare-env-label">Compare with</InputLabel>
            <Select
              labelId="compare-env-label"
              label="Compare with"
              value={compareEnv}
              onChange={(event) => onCompareEnvChange(String(event.target.value))}
            >
              <MenuItem value="">None</MenuItem>
              {compareEnvOptions.map((name) => (
                <MenuItem key={name} value={name}>
                  {name}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          {compareEnv && <SkillDiff base={card} other={compareCard} />}
        </Stack>
      )}

      {card.card && (
        <Box>
          <Button size="small" onClick={() => setShowRaw((shown) => !shown)}>
            {showRaw ? "Hide raw JSON" : "View raw JSON"}
          </Button>
          {showRaw && (
            <CodeBlock
              code={JSON.stringify(card.card, null, 2)}
              language="json"
              fieldId="a2a-card-json"
            />
          )}
        </Box>
      )}
    </Stack>
  );
}
