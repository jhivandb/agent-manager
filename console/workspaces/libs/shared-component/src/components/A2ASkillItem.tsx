/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */


import { Chip, Stack, Typography } from "@wso2/oxygen-ui";
import type { A2ACardSkill } from "../utils/a2aCard";

function ChipRow({ label, values }: { label?: string; values: string[] }) {
  if (values.length === 0) return null;
  return (
    <Stack direction="row" spacing={0.5} flexWrap="wrap" useFlexGap alignItems="center">
      {label && (
        <Typography variant="caption" color="text.secondary">
          {label}
        </Typography>
      )}
      {values.map((value, i) => (
        <Chip key={`${i}-${value}`} size="small" variant="outlined" label={value} />
      ))}
    </Stack>
  );
}

/** One skill from an A2A agent card: name, description, tags, modes and examples. */
export function A2ASkillItem({ skill }: { skill: A2ACardSkill }) {
  return (
    <Stack spacing={0.5} sx={{ p: 1.5, border: 1, borderColor: "divider", borderRadius: 1 }}>
      <Typography variant="subtitle2">{skill.name}</Typography>
      {skill.description && (
        <Typography variant="body2" color="text.secondary">
          {skill.description}
        </Typography>
      )}
      <ChipRow values={skill.tags} />
      <ChipRow label="Input" values={skill.inputModes} />
      <ChipRow label="Output" values={skill.outputModes} />
      {skill.examples.map((example, i) => (
        <Typography key={`${i}-${example}`} variant="caption" color="text.secondary">
          e.g. {example}
        </Typography>
      ))}
    </Stack>
  );
}
