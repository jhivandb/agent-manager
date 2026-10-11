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

vi.mock("@agent-management-platform/api-client", () => ({
  useCreateAgentAPIKey: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useGetA2AProxy: vi.fn(),
  useGetAgent: vi.fn(),
  useGetAgentConfigurations: vi.fn(() => ({
    data: { enableApiKeySecurity: true },
    isLoading: false,
  })),
  useListAgentDeployments: vi.fn(() => ({ data: undefined, isLoading: false })),
  useListAgentAPIKeys: vi.fn(() => ({ data: [], isLoading: false, isError: false })),
  useRevokeAgentAPIKey: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
}));
vi.mock("@agent-management-platform/shared-component", () => ({
  APIKeysManager: () => <div>api keys manager</div>,
  EnvironmentSelector: () => null,
}));
vi.mock("@agent-management-platform/views", () => ({
  PageLayout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import { useGetA2AProxy, useGetAgent, useListAgentDeployments } from "@agent-management-platform/api-client";
import { SecurityComponent } from "./Security.Component";

const renderPage = () =>
  render(
    <MemoryRouter initialEntries={["/org/o/project/p/agents/a/environment/dev/security"]}>
      <Routes>
        <Route path="/org/:orgId/project/:projectId/agents/:agentId/environment/:envId/security" element={<SecurityComponent />} />
      </Routes>
    </MemoryRouter>,
  );

const externalA2A = () =>
  vi.mocked(useGetAgent).mockReturnValue({
    data: { provisioning: { type: "external" }, agentType: { type: "external-agent-api", subType: "a2a-agent" } },
    isLoading: false,
  } as unknown as ReturnType<typeof useGetAgent>);

beforeEach(() => vi.clearAllMocks());

describe("SecurityComponent", () => {
  it("manages API keys of an external A2A agent routed through the gateway", () => {
    externalA2A();
    vi.mocked(useGetA2AProxy).mockReturnValue({
      data: { status: "published", lastError: "", upstreamAuthHeader: "" }, isLoading: false, isError: false, error: null,
    } as unknown as ReturnType<typeof useGetA2AProxy>);
    renderPage();
    expect(screen.getByText("api keys manager")).toBeInTheDocument();
    expect(vi.mocked(useListAgentDeployments)).toHaveBeenCalledWith(
      expect.objectContaining({ orgName: undefined }),
    );
  });

  it("explains why a direct external A2A agent has no API keys", () => {
    externalA2A();
    vi.mocked(useGetA2AProxy).mockReturnValue({
      data: undefined, isLoading: false, isError: true, error: Object.assign(new Error("nf"), { status: 404 }),
    } as unknown as ReturnType<typeof useGetA2AProxy>);
    renderPage();
    expect(screen.getByText(/not routed through the gateway/i)).toBeInTheDocument();
  });
});
