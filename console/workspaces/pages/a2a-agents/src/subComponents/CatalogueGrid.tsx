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
import { generatePath } from "react-router-dom";
import { Box, Skeleton, TablePagination } from "@wso2/oxygen-ui";
import { Bot, Search } from "@wso2/oxygen-ui-icons-react";
import { useListA2AAgents } from "@agent-management-platform/api-client";
import { ResourceListShell } from "@agent-management-platform/shared-component";
import { absoluteRouteMap, type A2ACatalogQuery } from "@agent-management-platform/types";
import {
  CatalogueFilterBar,
  EMPTY_FILTERS,
  type CatalogueFilterValues,
} from "./CatalogueFilterBar";
import { CatalogueTile } from "./CatalogueTile";
import { useReplaceSearchParams } from "../utils/useReplaceSearchParams";

const PAGE_SIZE = 24;
const SEARCH_DEBOUNCE_MS = 300;

const SEARCH_PARAM = "q";
const PAGE_PARAM = "page";
const FILTER_KEYS = Object.keys(EMPTY_FILTERS) as (keyof CatalogueFilterValues)[];

const GRID_SX = {
  display: "grid",
  gridTemplateColumns: {
    xs: "repeat(auto-fill, minmax(260px, 1fr))",
    md: "repeat(auto-fill, minmax(300px, 1fr))",
  },
  gap: 2,
};

export interface CatalogueGridProps {
  orgName: string;
}

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}

// The URL only ever holds values the filter bar wrote.
const readFilters = (params: URLSearchParams) =>
  Object.fromEntries(
    FILTER_KEYS.map((key) => [key, params.get(key) ?? ""]),
  ) as unknown as CatalogueFilterValues;

function buildQuery(
  q: string,
  filters: CatalogueFilterValues,
  page: number,
): A2ACatalogQuery {
  return {
    ...Object.fromEntries(FILTER_KEYS.map((key) => [key, filters[key] || undefined])),
    q: q || undefined,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
  };
}

const loadingTiles = (
  <Box sx={GRID_SX}>
    {Array.from({ length: 4 }, (_, i) => (
      <Skeleton key={i} variant="rounded" height={200} />
    ))}
  </Box>
);

export function CatalogueGrid({ orgName }: CatalogueGridProps) {
  const [searchParams, updateParams] = useReplaceSearchParams();
  const urlQuery = searchParams.get(SEARCH_PARAM) ?? "";
  const filters = readFilters(searchParams);
  const page = Number(searchParams.get(PAGE_PARAM) ?? 0) || 0;

  const [searchText, setSearchText] = useState(urlQuery);
  const debouncedSearch = useDebouncedValue(searchText.trim(), SEARCH_DEBOUNCE_MS);

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

  const changeFilters = (next: CatalogueFilterValues) =>
    updateParams((params) => {
      for (const key of FILTER_KEYS) {
        if (next[key]) params.set(key, next[key]);
        else params.delete(key);
      }
      params.delete(PAGE_PARAM);
    });

  const changePage = (nextPage: number) =>
    updateParams((params) => {
      if (nextPage > 0) params.set(PAGE_PARAM, String(nextPage));
      else params.delete(PAGE_PARAM);
    });

  return (
    <ResourceListShell
      resourceName="a2a-agents"
      searchValue={searchText}
      onSearchChange={setSearchText}
      toolbar={
        <CatalogueFilterBar
          orgName={orgName}
          values={filters}
          onChange={changeFilters}
          searchText={searchText}
          onSearchChange={setSearchText}
        />
      }
      error={error}
      isLoading={isLoading}
      loadingPlaceholder={loadingTiles}
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
      <Box sx={GRID_SX}>
        {agents.map((agent) => (
          <CatalogueTile
            key={`${agent.projectName}/${agent.agentName}`}
            agent={agent}
            to={generatePath(absoluteRouteMap.children.org.children.a2aAgents.children.view.path, {
              orgId: orgName,
              projectName: agent.projectName,
              agentName: agent.agentName,
            })}
            emphasised={debouncedSearch !== "" && agent.environments.some((e) => e.matched)}
          />
        ))}
      </Box>
      {total > PAGE_SIZE && (
        <TablePagination
          component="div"
          rowsPerPageOptions={[]}
          count={total}
          rowsPerPage={PAGE_SIZE}
          page={page}
          onPageChange={(_, nextPage) => changePage(nextPage)}
        />
      )}
    </ResourceListShell>
  );
}
