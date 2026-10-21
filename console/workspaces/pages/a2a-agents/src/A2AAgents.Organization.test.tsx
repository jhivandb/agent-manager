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

import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { A2ACatalogAgent } from "@agent-management-platform/types";

vi.mock("@agent-management-platform/views", () => ({
  PageLayout: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  TextInput: () => null,
  buildConfigureTabHref: () => "/configure",
}));
vi.mock("@agent-management-platform/api-client", () => ({
  useListA2AAgents: vi.fn(),
  useGetAgentCard: vi.fn(() => ({ data: undefined })),
  useGetA2AConnect: vi.fn(() => ({ data: undefined, isLoading: true })),
  useTestAgentAPIKey: vi.fn(() => ({ data: undefined })),
  useListEnvironments: vi.fn(() => ({ data: [] })),
  useListProjects: vi.fn(() => ({ data: { projects: [] } })),
}));
vi.mock("@agent-management-platform/shared-component", () => ({
  ResourceListShell: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  CodeBlock: () => null,
  formatRelativeTime: () => "now",
  monospaceInputSx: {},
}));

import { useListA2AAgents } from "@agent-management-platform/api-client";
import { A2AAgentsOrganization } from "./A2AAgents.Organization";

const externalAgent: A2ACatalogAgent = {
  projectName: "p9",
  agentName: "ext",
  kind: "external",
  name: "External Bot",
  description: "",
  version: "2",
  skills: [],
  leadEnvironment: "prod",
  environments: [
    { name: "prod", status: "fetched", proxied: false, cardDiffers: false, matched: false },
  ],
};

const listResult = (agents: A2ACatalogAgent[], isLoading = false) =>
  ({ data: isLoading ? undefined : { agents, total: agents.length }, isLoading }) as never;

const mockList = (lookup: ReturnType<typeof listResult>) =>
  vi.mocked(useListA2AAgents).mockImplementation(((
    _params: unknown,
    query?: { project?: string; limit?: number },
    options?: { enabled?: boolean },
  ) =>
    query?.limit === 100
      ? options?.enabled === false
        ? { data: undefined, isLoading: false }
        : lookup
      : { data: { agents: [], total: 0 }, isLoading: false }) as never);

const renderDeepLink = () =>
  render(
    <MemoryRouter initialEntries={["/org/acme/a2a-agents?project=p9&agent=ext"]}>
      <Routes>
        <Route path="/org/:orgId/a2a-agents" element={<A2AAgentsOrganization />} />
      </Routes>
    </MemoryRouter>,
  );

describe("A2AAgentsOrganization deep link to an unseen agent", () => {
  beforeEach(() => vi.mocked(useListA2AAgents).mockReset());

  it("shows a neutral loading state without kind or card claims", () => {
    mockList(listResult([], true));
    renderDeepLink();
    expect(screen.getByText("Loading agent…")).toBeTruthy();
    expect(screen.queryByText("platform")).toBeNull();
    expect(screen.queryByText(/no agent card is configured/i)).toBeNull();
  });

  it("resolves the real kind and lead environment from the catalogue", () => {
    mockList(listResult([externalAgent]));
    renderDeepLink();
    expect(screen.getByText("External Bot")).toBeTruthy();
    expect(screen.getByText("external")).toBeTruthy();
    expect(screen.getByRole("button", { name: "prod" })).toBeTruthy();
  });

  it("says so when the agent is not in the catalogue", () => {
    mockList(listResult([]));
    renderDeepLink();
    expect(screen.getByText(/not found in the catalogue/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Close details" })).toBeTruthy();
  });
});
