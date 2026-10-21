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

import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { A2ACatalogAgent } from "@agent-management-platform/types";

vi.mock("@agent-management-platform/api-client", () => ({
  useGetA2ACatalogEntry: vi.fn(),
  useGetAgentCard: vi.fn(),
  useListEnvironments: vi.fn(),
  useGetA2AConnect: vi.fn(() => ({ data: undefined, isLoading: true })),
  useIssueTestAgentAPIKey: vi.fn(() => ({ isIdle: true })),
}));
vi.mock("@agent-management-platform/shared-component", async () => {
  const actual = await vi.importActual<typeof import("@agent-management-platform/shared-component")>(
    "@agent-management-platform/shared-component",
  );
  return {
    CodeBlock: ({ code }: { code: string }) => <pre data-testid="raw-json">{code}</pre>,
    formatRelativeTime: () => "2 minutes ago",
    monospaceInputSx: {},
    normalizeVersion: (v: string) => `v${v}`,
    parseA2ACard: actual.parseA2ACard,
    diffA2ACards: actual.diffA2ACards,
    A2ASkillItem: actual.A2ASkillItem,
  };
});
vi.mock("@agent-management-platform/views", () => ({
  PageLayout: (props: {
    title: string;
    titleTail?: React.ReactNode;
    isLoading?: boolean;
    children: React.ReactNode;
  }) => (
    <div>
      <h1>{props.title}</h1>
      {props.titleTail}
      {props.isLoading ? "Loading…" : props.children}
    </div>
  ),
  DrawerWrapper: (props: { open: boolean; keepMounted?: boolean; children: React.ReactNode }) =>
    props.open || props.keepMounted ? <div hidden={!props.open}>{props.children}</div> : null,
  DrawerHeader: (props: { title: string; onClose: () => void }) => (
    <>
      <h2>{props.title}</h2>
      <button onClick={props.onClose}>Close</button>
    </>
  ),
  DrawerContent: (props: { children: React.ReactNode }) => <div>{props.children}</div>,
  TextInput: () => null,
  buildConfigureTabHref: () => "/configure",
}));

import {
  useGetA2ACatalogEntry,
  useGetAgentCard,
  useListEnvironments,
} from "@agent-management-platform/api-client";
import { A2AAgentView } from "./A2AAgentView";

const env = (name: string) => ({
  name,
  status: "fetched" as const,
  proxied: false,
  cardDiffers: false,
  matched: false,
});

const agent: A2ACatalogAgent = {
  projectName: "p1",
  agentName: "orders",
  kind: "platform",
  name: "Orders",
  description: "Handles orders",
  version: "1.0",
  skills: ["order-status"],
  leadEnvironment: "dev",
  environments: [env("dev"), env("prod")],
};

const devCard = {
  source: "platform",
  status: "fetched",
  sourceUrl: "https://orders.example/card",
  lastError: "",
  fetchedAt: "2026-10-20T10:00:00Z",
  card: {
    name: "Orders",
    capabilities: { streaming: true },
    skills: [{ id: "s", name: "order-status", description: "Look up an order" }],
  },
};

const prodCard = {
  ...devCard,
  card: {
    skills: [
      { id: "s", name: "order-status", description: "Look up an order" },
      { id: "r", name: "issue-refund", description: "Refund" },
    ],
  },
};

const mockCards = (byEnv: Record<string, unknown>) =>
  vi.mocked(useGetAgentCard).mockImplementation(
    ((params: { envId?: string }, options?: { enabled?: boolean }) => ({
      data: options?.enabled === false ? undefined : byEnv[params.envId ?? ""],
    })) as unknown as typeof useGetAgentCard,
  );

const mockEntry = (result: object) =>
  vi.mocked(useGetA2ACatalogEntry).mockReturnValue({ isLoading: false, ...result } as never);

const renderView = (path = "/org/acme/a2a-agents/view/p1/orders") =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/org/:orgId/a2a-agents/view/:projectName/:agentName"
          element={<A2AAgentView />}
        />
      </Routes>
    </MemoryRouter>,
  );

describe("A2AAgentView", () => {
  beforeEach(() => {
    vi.mocked(useGetAgentCard).mockReset();
    mockEntry({ data: agent });
    vi.mocked(useListEnvironments).mockReturnValue({
      data: [
        { name: "dev", displayName: "Development" },
        { name: "prod", displayName: "Production" },
      ],
    } as never);
  });

  it("reads the agent named in the URL", () => {
    mockCards({ dev: devCard });
    renderView();
    expect(useGetA2ACatalogEntry).toHaveBeenCalledWith({
      orgName: "acme",
      projName: "p1",
      agentName: "orders",
    });
    expect(screen.getByRole("heading", { name: "Orders" })).toBeTruthy();
  });

  it("stays loading without kind or card claims while the agent loads", () => {
    mockEntry({ data: undefined, isLoading: true });
    mockCards({});
    renderView();
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(screen.queryByText("platform")).toBeNull();
  });

  it("says so when the agent is not in the catalogue", () => {
    mockEntry({ data: null });
    mockCards({});
    renderView();
    expect(screen.getByText(/not found in the catalogue/i)).toBeTruthy();
  });

  it("says so when the agent fails to load", () => {
    mockEntry({ data: undefined, error: new Error("boom") });
    mockCards({});
    renderView();
    expect(screen.getByText(/Failed to load the agent/)).toBeTruthy();
  });

  it("lists skills and marks streaming as supported", () => {
    mockCards({ dev: devCard });
    renderView();
    expect(screen.getAllByText("order-status").length).toBeGreaterThan(0);
    expect(screen.getByText("✓ streaming")).toBeTruthy();
    expect(screen.getByText("✗ pushNotifications")).toBeTruthy();
  });

  it("shows a loading state, not a missing-card claim, while the card loads", () => {
    vi.mocked(useGetAgentCard).mockImplementation((() => ({
      data: undefined,
      isLoading: true,
    })) as unknown as typeof useGetAgentCard);
    renderView();
    expect(screen.getByText("Loading card…")).toBeTruthy();
    expect(screen.queryByText(/no agent card is configured/i)).toBeNull();
  });

  it("says no card is configured once the card lookup returns not found", () => {
    mockCards({ dev: null });
    renderView();
    expect(screen.getByText(/no agent card is configured/i)).toBeTruthy();
  });

  it("shows how to connect only once Connect is clicked", () => {
    mockCards({ dev: devCard });
    renderView();
    expect(screen.queryByTestId("connect-section")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect(screen.getByRole("heading", { name: "Connect · Development" })).toBeTruthy();
    expect(screen.getByTestId("connect-section")).toBeTruthy();
  });

  it("keeps the connect details, and any issued key, mounted after closing", () => {
    mockCards({ dev: devCard });
    renderView();
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    fireEvent.click(screen.getByRole("button", { name: "Close", hidden: true }));
    expect(screen.getByTestId("connect-section")).toBeTruthy();
  });

  it("opens on the environment in the URL and switches from the chips", () => {
    mockCards({ dev: devCard, prod: prodCard });
    renderView("/org/acme/a2a-agents/view/p1/orders?env=prod");
    expect(screen.getByText("issue-refund")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Development" }));
    expect(screen.queryByText("issue-refund")).toBeNull();
  });

  it("says the card is pending while it has not been fetched", () => {
    mockCards({ dev: { ...devCard, status: "pending", card: undefined, fetchedAt: undefined } });
    renderView();
    expect(screen.getByText(/card pending/i)).toBeTruthy();
  });

  it("shows the fetch error for a failed card", () => {
    mockCards({
      dev: { ...devCard, status: "failed", lastError: "connection refused", card: undefined },
    });
    renderView();
    expect(screen.getByText(/connection refused/)).toBeTruthy();
  });

  it("reveals the raw card JSON on demand", () => {
    mockCards({ dev: devCard });
    renderView();
    expect(screen.queryByTestId("raw-json")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "View raw JSON" }));
    expect(screen.getByTestId("raw-json").textContent).toContain("order-status");
  });

  it("names the other fields that differ when comparing environments", () => {
    mockCards({ dev: devCard, prod: { ...devCard, card: { ...devCard.card, version: "2" } } });
    renderView();
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "Compare with" }));
    fireEvent.click(screen.getByRole("option", { name: "Production" }));
    expect(screen.getByText("Also differs in: version")).toBeTruthy();
  });

  it("lists added skills when comparing with another environment", () => {
    mockCards({ dev: devCard, prod: prodCard });
    renderView();
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "Compare with" }));
    fireEvent.click(screen.getByRole("option", { name: "Production" }));
    expect(screen.getByText("Added: issue-refund")).toBeTruthy();
  });
});
