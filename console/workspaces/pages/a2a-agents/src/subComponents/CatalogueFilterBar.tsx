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

import { useId, useState } from "react";
import {
  Button,
  MenuItem,
  SearchBar,
  Select,
  Stack,
  Typography,
} from "@wso2/oxygen-ui";
import { ListFilter } from "@wso2/oxygen-ui-icons-react";
import {
  FilterBar,
  FilterChip,
  FilterPanel,
  FilterToggle,
} from "@agent-management-platform/views";
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

type AdvancedFilterKey = Exclude<keyof CatalogueFilterValues, "kind">;

export const EMPTY_FILTERS: CatalogueFilterValues = {
  kind: "",
  environment: "",
  project: "",
  status: "",
};

const FILTERS_PANEL_ID = "a2a-catalogue-filters";
const FILTERS_PANEL_WIDTH = 640;

const KIND_OPTIONS: { value: A2ACatalogKindFilter; label: string }[] = [
  { value: "platform", label: "Platform" },
  { value: "external", label: "External" },
  { value: "proxied", label: "Proxied" },
];

const STATUS_OPTIONS: { value: A2ACatalogEnvironment["status"]; label: string }[] = [
  { value: "fetched", label: "Fetched" },
  { value: "pending", label: "Pending" },
  { value: "failed", label: "Failed" },
];

interface Option {
  value: string;
  label: string;
}

interface AdvancedFilter {
  key: AdvancedFilterKey;
  name: string;
  options: Option[];
}

const optionLabel = (filter: AdvancedFilter, value: string) =>
  filter.options.find((option) => option.value === value)?.label ?? value;

function FilterSelect({
  filter,
  value,
  onChange,
}: {
  filter: AdvancedFilter;
  value: string;
  onChange: (value: string) => void;
}) {
  const labelId = useId();
  return (
    <Stack spacing={0.75} sx={{ flex: 1, minWidth: 180 }}>
      <Typography id={labelId} variant="body2" color="text.secondary">
        {filter.name}
      </Typography>
      <Select
        size="small"
        fullWidth
        displayEmpty
        value={value}
        onChange={(event) => onChange(String(event.target.value))}
        labelId={labelId}
      >
        <MenuItem value="">Any</MenuItem>
        {filter.options.map((option) => (
          <MenuItem key={option.value} value={option.value}>
            {option.label}
          </MenuItem>
        ))}
      </Select>
    </Stack>
  );
}

export interface CatalogueFilterBarProps {
  orgName: string;
  values: CatalogueFilterValues;
  onChange: (values: CatalogueFilterValues) => void;
  searchText: string;
  onSearchChange: (text: string) => void;
}

/** Kind toggle and filter chips on the left; search and Advanced Filters fixed on the right. */
export function CatalogueFilterBar({
  orgName,
  values,
  onChange,
  searchText,
  onSearchChange,
}: CatalogueFilterBarProps) {
  const { data: environments } = useListEnvironments({ orgName });
  const { data: projects } = useListProjects({ orgName });
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(values);
  const [filtersButton, setFiltersButton] = useState<HTMLButtonElement | null>(null);

  const advancedFilters: AdvancedFilter[] = [
    {
      key: "environment",
      name: "Environment",
      options: (environments ?? []).map((env) => ({
        value: env.name,
        label: env.displayName ?? env.name,
      })),
    },
    {
      key: "project",
      name: "Project",
      options: (projects?.projects ?? []).map((project) => ({
        value: project.name,
        label: project.displayName || project.name,
      })),
    },
    { key: "status", name: "Card status", options: STATUS_OPTIONS },
  ];
  const activeFilters = advancedFilters.filter((filter) => values[filter.key]);

  const openPanel = () => {
    if (open) return;
    setDraft(values);
    setOpen(true);
  };
  const remove = (key: AdvancedFilterKey) => {
    onChange({ ...values, [key]: "" });
    setDraft((d) => ({ ...d, [key]: "" }));
  };
  const clearAll = () => {
    onChange(EMPTY_FILTERS);
    setDraft(EMPTY_FILTERS);
  };
  const apply = () => {
    onChange({ ...draft, kind: values.kind });
    setOpen(false);
  };

  return (
    <>
      <FilterBar
        actions={
          <>
            <SearchBar
              size="small"
              placeholder="Search A2A agents"
              value={searchText}
              onChange={(event: React.ChangeEvent<HTMLInputElement>) =>
                onSearchChange(event.target.value)
              }
              sx={{ width: 300, maxWidth: "100%" }}
            />
            <Button
              ref={setFiltersButton}
              variant="outlined"
              color="inherit"
              startIcon={<ListFilter size={16} />}
              aria-expanded={open}
              aria-controls={FILTERS_PANEL_ID}
              onClick={() => (open ? setOpen(false) : openPanel())}
              sx={{ textTransform: "none", borderColor: "divider" }}
            >
              Advanced Filters
            </Button>
          </>
        }
      >
        <FilterToggle
          label="Kind"
          value={values.kind || undefined}
          options={KIND_OPTIONS}
          onChange={(kind) =>
            onChange({ ...values, kind: (kind ?? "") as CatalogueFilterValues["kind"] })}
        />
        {activeFilters.map((filter) => {
          const value = optionLabel(filter, values[filter.key]);
          return (
            <FilterChip
              key={filter.key}
              name={filter.name}
              value={value}
              label={`${filter.name}: ${value}`}
              onOpen={openPanel}
              onRemove={() => remove(filter.key)}
            />
          );
        })}
        {activeFilters.length + (values.kind ? 1 : 0) >= 2 && (
          <Button size="small" variant="text" onClick={clearAll}>
            Clear all
          </Button>
        )}
      </FilterBar>
      {open && filtersButton && (
        <FilterPanel
          anchorEl={filtersButton}
          id={FILTERS_PANEL_ID}
          title="Advanced Filters"
          width={FILTERS_PANEL_WIDTH}
          onReset={() => setDraft({ ...EMPTY_FILTERS, kind: draft.kind })}
          onApply={apply}
          onClose={() => setOpen(false)}
        >
          <Stack direction={{ xs: "column", sm: "row" }} spacing={2} useFlexGap>
            {advancedFilters.map((filter) => (
              <FilterSelect
                key={filter.key}
                filter={filter}
                value={draft[filter.key]}
                onChange={(value) => setDraft({ ...draft, [filter.key]: value })}
              />
            ))}
          </Stack>
        </FilterPanel>
      )}
    </>
  );
}
