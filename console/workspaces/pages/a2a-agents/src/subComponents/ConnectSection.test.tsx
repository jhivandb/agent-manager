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

import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { A2ACatalogAgent, A2AConnectResponse } from "@agent-management-platform/types";

vi.mock("@agent-management-platform/api-client", () => ({
  useGetA2AConnect: vi.fn(),
  useIssueTestAgentAPIKey: vi.fn(),
}));
vi.mock("@agent-management-platform/views", () => ({
  TextInput: ({ label, value }: { label?: string; value?: string }) => (
    <input data-testid="text-input" aria-label={label} value={value} readOnly />
  ),
  buildConfigureTabHref: () => "/configure",
}));
vi.mock("@agent-management-platform/shared-component", async () => ({
  CodeBlock: ({ code }: { code: string }) => <pre data-testid="snippet">{code}</pre>,
  formatRelativeTime: () => "in 10 minutes",
  monospaceInputSx: {},
  parseA2ACard: (
    await vi.importActual<typeof import("@agent-management-platform/shared-component")>(
      "@agent-management-platform/shared-component",
    )
  ).parseA2ACard,
}));

import { useState } from "react";
import {
  useGetA2AConnect,
  useIssueTestAgentAPIKey,
} from "@agent-management-platform/api-client";
import { ConnectSection } from "./ConnectSection";

const agent: A2ACatalogAgent = {
  projectName: "p1",
  agentName: "orders",
  kind: "platform",
  name: "Orders",
  description: "",
  version: "1",
  skills: [],
  leadEnvironment: "dev",
  environments: [],
};

const originInterface = "https://origin.example/rpc";
const agentCard = {
  source: "platform",
  status: "fetched",
  sourceUrl: "https://origin.example/.well-known/agent-card.json",
  lastError: "",
  card: {
    supportedInterfaces: [{ url: originInterface, protocolBinding: "JSONRPC" }],
    securitySchemes: { bearer: { httpAuthSecurityScheme: { scheme: "Bearer" } } },
  },
};

const connect = (overrides: Partial<A2AConnectResponse> = {}): A2AConnectResponse => ({
  kind: "platform",
  proxied: false,
  isProduction: false,
  gatewayUrl: "https://gw.example/orders",
  cardUrl: "https://gw.example/orders/.well-known/agent-card.json",
  jsonRpcUrl: "https://gw.example/orders/rpc",
  security: { mode: "apiKey", apiKeyHeader: "X-API-Key" },
  publication: { status: "published", lastError: "" },
  ...overrides,
});

const issuedKey = {
  status: "success",
  apiKey: "test-key-1",
  expiresAt: "2026-10-21T10:10:00Z",
  gatewayConnected: false,
};

const issue = vi.fn();

/** Behaves like a react-query mutation: idle until called, then holds the issued key. */
function useFakeIssueMutation() {
  const [data, setData] = useState<typeof issuedKey | undefined>();
  return {
    mutate: (params: unknown) => {
      issue(params);
      setData(issuedKey);
    },
    data,
    isPending: false,
    isError: false,
    isIdle: data === undefined,
  };
}

function mockConnect(data: A2AConnectResponse) {
  vi.mocked(useGetA2AConnect).mockReturnValue({ data, isLoading: false, isError: false } as never);
}

function renderSection(env = "dev") {
  const ui = (environment: string) => (
    <MemoryRouter initialEntries={["/org/acme/a2a-agents"]}>
      <Routes>
        <Route
          path="/org/:orgId/a2a-agents"
          element={
            // Keyed as A2AAgentView mounts it: one section per environment.
            <ConnectSection
              key={environment}
              orgName="acme"
              agent={agent}
              env={environment}
              card={agentCard as never}
            />
          }
        />
      </Routes>
    </MemoryRouter>
  );
  const view = render(ui(env));
  return { rerenderWithEnv: (next: string) => view.rerender(ui(next)) };
}

const snippet = () => screen.getByTestId("snippet").textContent ?? "";

describe("ConnectSection", () => {
  beforeEach(() => {
    vi.mocked(useGetA2AConnect).mockReset();
    issue.mockReset();
    vi.mocked(useIssueTestAgentAPIKey).mockImplementation(useFakeIssueMutation as never);
  });

  it("offers a test key for a non-production API-key environment without issuing one", () => {
    mockConnect(connect());
    renderSection();
    expect(screen.getByText(/API key required in header/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Issue test key" })).toBeTruthy();
    expect(issue).not.toHaveBeenCalled();
    expect(screen.queryByDisplayValue("test-key-1")).toBeNull();
    expect(snippet()).toContain("<YOUR_API_KEY>");
  });

  it("issues the key on click, shows it and fills it into the snippet", () => {
    mockConnect(connect());
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Issue test key" }));
    expect(issue).toHaveBeenCalledWith({
      orgName: "acme",
      projName: "p1",
      agentName: "orders",
      envId: "dev",
    });
    expect(screen.getByDisplayValue("test-key-1")).toBeTruthy();
    expect(snippet()).toContain("test-key-1");
    expect(screen.getByText(/gateway is not connected to the control plane/)).toBeTruthy();
    expect(screen.getByText(/in 10 minutes/)).toBeTruthy();
  });

  it("issues a new key on Rotate", () => {
    mockConnect(connect());
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Issue test key" }));
    fireEvent.click(screen.getByRole("button", { name: "Rotate" }));
    expect(issue).toHaveBeenCalledTimes(2);
  });

  it("stops issuing and forgets the key when the environment changes", () => {
    mockConnect(connect());
    const { rerenderWithEnv } = renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Issue test key" }));
    rerenderWithEnv("staging");
    expect(issue).toHaveBeenCalledTimes(1);
    expect(screen.queryByDisplayValue("test-key-1")).toBeNull();
    expect(snippet()).not.toContain("test-key-1");
  });

  it("asks for a new click after coming back to an environment", () => {
    mockConnect(connect());
    const { rerenderWithEnv } = renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Issue test key" }));
    rerenderWithEnv("staging");
    rerenderWithEnv("dev");
    expect(issue).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Issue test key" })).toBeTruthy();
    expect(screen.queryByDisplayValue("test-key-1")).toBeNull();
    expect(snippet()).not.toContain("test-key-1");
  });

  it("does not bring back a production switch's forgotten key on return", () => {
    mockConnect(connect());
    const { rerenderWithEnv } = renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Issue test key" }));
    mockConnect(connect({ isProduction: true }));
    rerenderWithEnv("prod");
    mockConnect(connect());
    rerenderWithEnv("dev");
    expect(snippet()).not.toContain("test-key-1");
  });

  it("forgets the key when moving to a production environment", () => {
    mockConnect(connect());
    const { rerenderWithEnv } = renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Issue test key" }));
    mockConnect(connect({ isProduction: true }));
    rerenderWithEnv("prod");
    expect(snippet()).not.toContain("test-key-1");
    expect(snippet()).toContain("<YOUR_API_KEY>");
  });

  it("sends production users to the agent's owners for a key", () => {
    mockConnect(connect({ isProduction: true }));
    renderSection("prod");
    expect(screen.getByText(/API key required in header/)).toBeTruthy();
    expect(screen.getByText("Keys for production are issued by the agent's owners.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Issue test key" })).toBeNull();
  });

  it("describes OAuth issuers, audiences and header without a key issuer", () => {
    mockConnect(
      connect({
        security: {
          mode: "oauth",
          oauth: {
            issuers: [
              { name: "corp-idp", issuer: "https://idp/x" },
              { name: "gone-idp", issuer: "" },
            ],
            audiences: ["orders-api"],
            headerName: "Authorization",
            authHeaderPrefix: "Bearer",
          },
        },
      }),
    );
    renderSection();
    expect(screen.getByText(/https:\/\/idp\/x/)).toBeTruthy();
    expect(screen.getByText(/gone-idp — not found/)).toBeTruthy();
    expect(screen.getByText(/orders-api/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Issue test key" })).toBeNull();
    expect(snippet()).toContain("Authorization");
    expect(snippet()).toContain("Bearer <ACCESS_TOKEN>");
  });

  it("says when no authentication is required", () => {
    mockConnect(connect({ security: { mode: "none" } }));
    renderSection();
    expect(screen.getByText("No authentication required.")).toBeTruthy();
    expect(snippet()).not.toContain("X-API-Key");
  });

  it("says when security couldn't be determined", () => {
    mockConnect(connect({ security: { mode: "unknown" } }));
    renderSection();
    expect(
      screen.getByText("Security settings couldn't be determined for this environment."),
    ).toBeTruthy();
  });

  it("does not vouch for a directly reached external agent's security", () => {
    mockConnect(
      connect({
        kind: "external",
        gatewayUrl: "",
        cardUrl: "https://origin.example/.well-known/agent-card.json",
        jsonRpcUrl: originInterface,
        security: { mode: "unmanaged" },
        publication: { status: "none", lastError: "" },
      }),
    );
    renderSection();
    expect(screen.getByText(/Security isn't managed by the platform/)).toBeTruthy();
    expect(screen.getByText(/The card declares: bearer/)).toBeTruthy();
    expect(screen.queryByText("Gateway endpoint")).toBeNull();
    expect(screen.queryByLabelText("Gateway endpoint")).toBeNull();
    expect(screen.getAllByTestId("text-input")[0]).toHaveValue(originInterface);
    expect(snippet()).not.toContain("X-API-Key");
  });

  it("addresses a proxied agent at the gateway, not the card's origin", () => {
    mockConnect(
      connect({
        kind: "external",
        proxied: true,
        gatewayUrl: "https://gw/agents/x",
        cardUrl: "https://gw/agents/x/.well-known/agent-card.json",
        jsonRpcUrl: "https://gw/agents/x/rpc",
      }),
    );
    renderSection();
    const endpoints = within(screen.getByTestId("connect-endpoints"));
    expect(endpoints.getAllByTestId("text-input")[0]).toHaveValue("https://gw/agents/x");
    expect(screen.getByText("Declared by the card")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "curl" }));
    expect(snippet()).toContain("https://gw/agents/x");
    expect(snippet()).not.toContain(originInterface);
  });

  it("warns that the gateway may lag a failed publication", () => {
    mockConnect(
      connect({ publication: { status: "failed", lastError: "gateway rejected the policy" } }),
    );
    renderSection();
    expect(screen.getByText(/The gateway may not yet enforce these settings/)).toBeTruthy();
    expect(screen.getByText(/gateway rejected the policy/)).toBeTruthy();
  });
});
