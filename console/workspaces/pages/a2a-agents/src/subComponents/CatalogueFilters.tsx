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

import { MenuItem, Select, Stack } from "@wso2/oxygen-ui";
import {
  useListEnvironments,
  useListProjects,
} from "@agent-management-platform/api-client";
import type {
  A2ACatalogEnvironment,
  A2ACatalogKindFilter,
} from "@agent-management-platform/types";

export interface CatalogueFilterValues {
  kind: A2ACatalogKindFilter | "";
  environment: string;
  project: string;
  status: A2ACatalogEnvironment["status"] | "";
}

export interface CatalogueFiltersProps {
  orgName: string;
  values: CatalogueFilterValues;
  onChange: (key: keyof CatalogueFilterValues, value: string) => void;
}

const KIND_OPTIONS: A2ACatalogKindFilter[] = ["platform", "external", "proxied"];
const STATUS_OPTIONS: A2ACatalogEnvironment["status"][] = [
  "pending",
  "fetched",
  "failed",
];

interface FilterSelectProps {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}

function FilterSelect({ label, value, options, onChange }: FilterSelectProps) {
  return (
    <Select
      size="small"
      displayEmpty
      value={value}
      onChange={(event) => onChange(String(event.target.value))}
      inputProps={{ "aria-label": label }}
      sx={{ minWidth: 140 }}
    >
      <MenuItem value="">{label}</MenuItem>
      {options.map((option) => (
        <MenuItem key={option.value} value={option.value}>
          {option.label}
        </MenuItem>
      ))}
    </Select>
  );
}

const asOptions = (values: string[]) =>
  values.map((value) => ({ value, label: value }));

export function CatalogueFilters({ orgName, values, onChange }: CatalogueFiltersProps) {
  const { data: environments } = useListEnvironments({ orgName });
  const { data: projects } = useListProjects({ orgName });

  return (
    <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
      <FilterSelect
        label="All kinds"
        value={values.kind}
        options={asOptions(KIND_OPTIONS)}
        onChange={(value) => onChange("kind", value)}
      />
      <FilterSelect
        label="All environments"
        value={values.environment}
        options={(environments ?? []).map((env) => ({
          value: env.name,
          label: env.displayName ?? env.name,
        }))}
        onChange={(value) => onChange("environment", value)}
      />
      <FilterSelect
        label="All projects"
        value={values.project}
        options={(projects?.projects ?? []).map((project) => ({
          value: project.name,
          label: project.displayName || project.name,
        }))}
        onChange={(value) => onChange("project", value)}
      />
      <FilterSelect
        label="Any status"
        value={values.status}
        options={asOptions(STATUS_OPTIONS)}
        onChange={(value) => onChange("status", value)}
      />
    </Stack>
  );
}
