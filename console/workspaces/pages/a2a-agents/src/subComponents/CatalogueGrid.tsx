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
import { useSearchParams } from "react-router-dom";
import { Box, SearchBar, Stack, TablePagination } from "@wso2/oxygen-ui";
import { Bot, Search } from "@wso2/oxygen-ui-icons-react";
import { useListA2AAgents } from "@agent-management-platform/api-client";
import { ResourceListShell } from "@agent-management-platform/shared-component";
import type {
  A2ACatalogAgent,
  A2ACatalogEnvironment,
  A2ACatalogKindFilter,
  A2ACatalogQuery,
} from "@agent-management-platform/types";
import type { CatalogueSelection } from "../useCatalogueSelection";
import { CatalogueFilters, type CatalogueFilterValues } from "./CatalogueFilters";
import { CatalogueTile } from "./CatalogueTile";

export const PAGE_SIZE = 24;
const SEARCH_DEBOUNCE_MS = 300;

const SEARCH_PARAM = "q";
const PAGE_PARAM = "page";
const FILTER_PARAMS: Record<keyof CatalogueFilterValues, string> = {
  kind: "kind",
  environment: "environment",
  project: "fproject",
  status: "status",
};

export interface CatalogueGridProps {
  orgName: string;
  selection: CatalogueSelection | null;
  compact: boolean;
  onSelect: (agent: A2ACatalogAgent) => void;
}

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}

function readFilters(params: URLSearchParams): CatalogueFilterValues {
  return {
    kind: (params.get(FILTER_PARAMS.kind) ?? "") as A2ACatalogKindFilter | "",
    environment: params.get(FILTER_PARAMS.environment) ?? "",
    project: params.get(FILTER_PARAMS.project) ?? "",
    status: (params.get(FILTER_PARAMS.status) ?? "") as
      | A2ACatalogEnvironment["status"]
      | "",
  };
}

function buildQuery(
  q: string,
  filters: CatalogueFilterValues,
  page: number,
): A2ACatalogQuery {
  return {
    q: q || undefined,
    kind: filters.kind || undefined,
    environment: filters.environment || undefined,
    project: filters.project || undefined,
    status: filters.status || undefined,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
  };
}

const isSelected = (agent: A2ACatalogAgent, selection: CatalogueSelection | null) =>
  selection?.projectName === agent.projectName &&
  selection?.agentName === agent.agentName;

export function CatalogueGrid({ orgName, selection, compact, onSelect }: CatalogueGridProps) {
  const [searchParams, setSearchParams] = useSearchParams();
  const urlQuery = searchParams.get(SEARCH_PARAM) ?? "";
  const filters = readFilters(searchParams);
  const page = Number(searchParams.get(PAGE_PARAM) ?? 0) || 0;

  const [searchText, setSearchText] = useState(urlQuery);
  const debouncedSearch = useDebouncedValue(searchText.trim(), SEARCH_DEBOUNCE_MS);

  const updateParams = (change: (params: URLSearchParams) => void) =>
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        change(next);
        return next;
      },
      { replace: true },
    );

  useEffect(() => {
    if (debouncedSearch === urlQuery) return;
    updateParams((params) => {
      if (debouncedSearch) params.set(SEARCH_PARAM, debouncedSearch);
      else params.delete(SEARCH_PARAM);
      params.delete(PAGE_PARAM);
    });
    // Only a settled search text should write to the URL.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedSearch]);

  const { data, isLoading, error } = useListA2AAgents(
    { orgName },
    buildQuery(debouncedSearch, filters, page),
  );

  const agents = data?.agents ?? [];
  const total = data?.total ?? 0;
  const isFiltering = Object.values(filters).some(Boolean) || debouncedSearch !== "";

  const changeFilter = (key: keyof CatalogueFilterValues, value: string) =>
    updateParams((params) => {
      if (value) params.set(FILTER_PARAMS[key], value);
      else params.delete(FILTER_PARAMS[key]);
      params.delete(PAGE_PARAM);
    });

  const changePage = (nextPage: number) =>
    updateParams((params) => {
      if (nextPage > 0) params.set(PAGE_PARAM, String(nextPage));
      else params.delete(PAGE_PARAM);
    });

  const toolbar = (
    <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap alignItems="center">
      <Box flexGrow={1} minWidth={240}>
        <SearchBar
          placeholder="Search A2A agents..."
          size="small"
          fullWidth
          value={searchText}
          onChange={(event: React.ChangeEvent<HTMLInputElement>) =>
            setSearchText(event.target.value)
          }
        />
      </Box>
      <CatalogueFilters orgName={orgName} values={filters} onChange={changeFilter} />
    </Stack>
  );

  return (
    <ResourceListShell
      resourceName="a2a-agents"
      searchValue={searchText}
      onSearchChange={setSearchText}
      searchPlaceholder="Search A2A agents..."
      toolbar={toolbar}
      error={error}
      isLoading={isLoading}
      isEmpty={total === 0 && !isFiltering}
      isSearchEmpty={total === 0 && isFiltering}
      emptyState={{
        illustration: <Bot size={64} />,
        title: "No A2A agents yet",
        description: "Agents that expose an A2A card show up here once deployed.",
      }}
      searchEmptyState={{
        illustration: <Search size={64} />,
        title: "No A2A agents match your search",
        description: "Try a different keyword or clear the filters.",
      }}
    >
      <Box
        role="listbox"
        sx={{
          display: "grid",
          gap: 2,
          gridTemplateColumns: compact
            ? "1fr"
            : "repeat(auto-fill, minmax(300px, 1fr))",
        }}
      >
        {agents.map((agent) => (
          <CatalogueTile
            key={`${agent.projectName}/${agent.agentName}`}
            agent={agent}
            query={debouncedSearch}
            selected={isSelected(agent, selection)}
            compact={compact}
            onSelect={() => onSelect(agent)}
          />
        ))}
      </Box>
      <TablePagination
        component="div"
        rowsPerPageOptions={[]}
        count={total}
        rowsPerPage={PAGE_SIZE}
        page={page}
        onPageChange={(_, nextPage) => changePage(nextPage)}
      />
    </ResourceListShell>
  );
}
