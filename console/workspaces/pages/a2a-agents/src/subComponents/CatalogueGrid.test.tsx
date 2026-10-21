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

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { A2ACatalogAgent } from "@agent-management-platform/types";

vi.mock("@agent-management-platform/api-client", () => ({
  useListA2AAgents: vi.fn(),
  useListEnvironments: vi.fn(() => ({ data: [] })),
  useListProjects: vi.fn(() => ({ data: { projects: [] } })),
}));
vi.mock("@agent-management-platform/shared-component", () => ({
  ResourceListShell: (props: {
    toolbar?: React.ReactNode;
    isEmpty?: boolean;
    isSearchEmpty?: boolean;
    emptyState: { title: string };
    searchEmptyState?: { title: string };
    children: React.ReactNode;
  }) => (
    <div>
      {props.toolbar}
      {props.isEmpty
        ? props.emptyState.title
        : props.isSearchEmpty
          ? props.searchEmptyState?.title
          : props.children}
    </div>
  ),
}));

import { useListA2AAgents } from "@agent-management-platform/api-client";
import { CatalogueGrid } from "./CatalogueGrid";

const agentWith = (
  projectName: string,
  environments: A2ACatalogAgent["environments"],
): A2ACatalogAgent => ({
  projectName,
  agentName: "orders",
  kind: "platform",
  name: "Orders",
  description: "Handles orders",
  version: "1",
  skills: ["refund"],
  leadEnvironment: "dev",
  environments,
});

const env = (name: string, extra = {}) => ({
  name,
  status: "fetched" as const,
  proxied: false,
  cardDiffers: false,
  matched: false,
  ...extra,
});

const agents = [
  agentWith("p1", [env("dev"), env("prod", { cardDiffers: true })]),
  agentWith("p2", [env("dev", { proxied: true })]),
];

function renderGrid(selected: { projectName: string; agentName: string } | null = null) {
  const onSelect = vi.fn();
  render(
    <MemoryRouter initialEntries={["/?q=old"]}>
      <CatalogueGrid orgName="acme" selection={selected} compact={false} onSelect={onSelect} />
    </MemoryRouter>,
  );
  return onSelect;
}

describe("CatalogueGrid", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(useListA2AAgents).mockReturnValue({
      data: { agents, total: 2, limit: 24, offset: 0 },
      isLoading: false,
    } as never);
  });
  afterEach(() => vi.useRealTimers());

  it("renders one tile per agent, even for the same agent name in two projects", () => {
    renderGrid();
    expect(screen.getAllByTestId("catalogue-tile")).toHaveLength(2);
  });

  it("flags a differing card and a proxied environment", () => {
    renderGrid();
    expect(screen.getByText("card differs")).toBeInTheDocument();
    expect(screen.getByText("proxied")).toBeInTheDocument();
  });

  it("marks only the tile with the matching project as selected", () => {
    renderGrid({ projectName: "p2", agentName: "orders" });
    const tiles = screen.getAllByTestId("catalogue-tile");
    expect(within(tiles[0]).queryByText("p1")).toBeInTheDocument();
    expect(tiles[0]).toHaveAttribute("aria-selected", "false");
    expect(tiles[1]).toHaveAttribute("aria-selected", "true");
  });

  it("selects the clicked agent", () => {
    const onSelect = renderGrid();
    fireEvent.click(screen.getAllByTestId("catalogue-tile")[1]);
    expect(onSelect).toHaveBeenCalledWith(agents[1]);
  });

  it("queries with the debounced search text from the first page", () => {
    renderGrid();
    fireEvent.change(screen.getByPlaceholderText("Search A2A agents..."), { target: { value: "refund" } });
    expect(useListA2AAgents).not.toHaveBeenCalledWith(
      { orgName: "acme" },
      expect.objectContaining({ q: "refund" }),
    );
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(useListA2AAgents).toHaveBeenLastCalledWith(
      { orgName: "acme" },
      expect.objectContaining({ q: "refund", offset: 0, limit: 24 }),
    );
  });
});
