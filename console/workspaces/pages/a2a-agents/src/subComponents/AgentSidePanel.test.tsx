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
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { A2ACatalogAgent } from "@agent-management-platform/types";

vi.mock("@agent-management-platform/api-client", () => ({
  useGetAgentCard: vi.fn(),
}));
vi.mock("@agent-management-platform/shared-component", () => ({
  CodeBlock: ({ code }: { code: string }) => <pre data-testid="raw-json">{code}</pre>,
  formatRelativeTime: () => "2 minutes ago",
}));

import { useGetAgentCard } from "@agent-management-platform/api-client";
import { AgentSidePanel } from "./AgentSidePanel";

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

const renderPanel = (props: Partial<Parameters<typeof AgentSidePanel>[0]> = {}) => {
  const handlers = { onEnvChange: vi.fn(), onClose: vi.fn() };
  render(
    <MemoryRouter initialEntries={["/org/acme/a2a-agents"]}>
      <AgentSidePanel agent={agent} env="dev" {...handlers} {...props} />
    </MemoryRouter>,
  );
  return handlers;
};

describe("AgentSidePanel", () => {
  beforeEach(() => {
    vi.mocked(useGetAgentCard).mockReset();
  });

  it("lists skills and marks streaming as supported", () => {
    mockCards({ dev: devCard });
    renderPanel();
    expect(screen.getAllByText("order-status").length).toBeGreaterThan(0);
    expect(screen.getByText("✓ streaming")).toBeTruthy();
    expect(screen.getByText("✗ pushNotifications")).toBeTruthy();
  });

  it("switches environment from the chips", () => {
    mockCards({ dev: devCard });
    const { onEnvChange } = renderPanel();
    fireEvent.click(screen.getByRole("button", { name: "prod" }));
    expect(onEnvChange).toHaveBeenCalledWith("prod");
  });

  it("closes on Escape and on the close button", () => {
    mockCards({ dev: devCard });
    const { onClose } = renderPanel();
    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.click(screen.getByRole("button", { name: "Close details" }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("says the card is pending while it has not been fetched", () => {
    mockCards({ dev: { ...devCard, status: "pending", card: undefined, fetchedAt: undefined } });
    renderPanel();
    expect(screen.getByText(/card pending/i)).toBeTruthy();
  });

  it("shows the fetch error for a failed card", () => {
    mockCards({
      dev: { ...devCard, status: "failed", lastError: "connection refused", card: undefined },
    });
    renderPanel();
    expect(screen.getByText(/connection refused/)).toBeTruthy();
  });

  it("reveals the raw card JSON on demand", () => {
    mockCards({ dev: devCard });
    renderPanel();
    expect(screen.queryByTestId("raw-json")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "View raw JSON" }));
    expect(screen.getByTestId("raw-json").textContent).toContain("order-status");
  });

  it("lists added skills when comparing with another environment", () => {
    mockCards({ dev: devCard, prod: prodCard });
    renderPanel();
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "Compare with" }));
    fireEvent.click(screen.getByRole("option", { name: "prod" }));
    expect(screen.getByText("Added: issue-refund")).toBeTruthy();
  });
});
