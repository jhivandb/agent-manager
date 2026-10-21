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

import React from "react";
import {
  Box,
  Button,
  ButtonGroup,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
} from "@wso2/oxygen-ui";
import { X as RemoveIcon } from "@wso2/oxygen-ui-icons-react";

export interface FilterBarProps {
  /** Fixed on the right of the first line: search, the Filters button. */
  actions: React.ReactNode;
  /** Toggles, chips and Clear all, laid out inline on the left. */
  children: React.ReactNode;
}

/**
 * Actions float right on the first line; children that don't fit wrap below,
 * across the full width, so the actions never move.
 */
export const FilterBar: React.FC<FilterBarProps> = ({ actions, children }) => (
  <Box
    sx={{
      display: "flow-root",
      mb: 1,
      "& > *": { display: "inline-flex", verticalAlign: "middle", mr: 1, mb: 1 },
    }}
  >
    <Stack
      direction="row"
      spacing={1}
      useFlexGap
      flexWrap="wrap"
      alignItems="center"
      sx={{ float: "right", maxWidth: "100%", minWidth: 0, "&&": { mr: 0, ml: 1 } }}
    >
      {actions}
    </Stack>
    {children}
  </Box>
);

const ALL = "all";

export interface FilterToggleProps {
  /** The group's accessible name. */
  label: string;
  /** The selected option; undefined selects All. */
  value?: string;
  options: { value: string; label: string }[];
  onChange: (value?: string) => void;
}

/** All or one option; applies straight away. */
export const FilterToggle: React.FC<FilterToggleProps> = ({ label, value, options, onChange }) => (
  <ToggleButtonGroup
    exclusive
    size="small"
    value={value ?? ALL}
    aria-label={label}
    onChange={(_, next: string | null) => {
      if (next !== null) onChange(next === ALL ? undefined : next);
    }}
    sx={{ "& .MuiToggleButton-root": { px: 1.75, textTransform: "none" } }}
  >
    <ToggleButton value={ALL}>All</ToggleButton>
    {options.map((option) => (
      <ToggleButton key={option.value} value={option.value}>
        {option.label}
      </ToggleButton>
    ))}
  </ToggleButtonGroup>
);

export interface FilterChipProps {
  name: string;
  value: string;
  /** The whole chip, for its title and the × label; defaults to "name value". */
  label?: string;
  onOpen: () => void;
  onRemove: () => void;
}

/** The filter's name and value, which open the filters panel, and an × that removes it. */
export const FilterChip: React.FC<FilterChipProps> = ({
  name,
  value,
  label = `${name} ${value}`,
  onOpen,
  onRemove,
}) => (
  <ButtonGroup
    size="small"
    variant="outlined"
    color="inherit"
    sx={{ maxWidth: 320, "& .MuiButton-root": { borderColor: "divider" } }}
  >
    <Button title={label} onClick={onOpen} sx={{ textTransform: "none", minWidth: 0 }}>
      <Box component="span" sx={{ color: "text.secondary", whiteSpace: "nowrap" }}>
        {name}
      </Box>{" "}
      <Box
        component="span"
        sx={{
          ml: 0.75,
          fontWeight: 600,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {value}
      </Box>
    </Button>
    <Button aria-label={`Remove ${label}`} onClick={onRemove} sx={{ minWidth: 0, px: 0.75 }}>
      <RemoveIcon size={14} />
    </Button>
  </ButtonGroup>
);
