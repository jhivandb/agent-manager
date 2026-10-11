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

import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { A2AProxyResponse, AgentCardResponse } from "@agent-management-platform/types";

vi.mock("@agent-management-platform/views", () => ({ useSnackBar: () => ({ pushSnackBar: vi.fn() }) }));
vi.mock("@agent-management-platform/auth", () => ({
  useAuthHooks: () => ({ getToken: async () => "t", isAuthenticated: true, logout: vi.fn() }),
}));
vi.mock("./telemetry", () => ({
  ConsoleAction: {}, markSessionExpired: vi.fn(), useTrack: () => ({ track: vi.fn() }),
}));
vi.mock("../apis/a2a-proxy", () => ({
  getA2AProxy: vi.fn(),
  setA2AProxy: vi.fn(async () => undefined),
  deleteA2AProxy: vi.fn(async () => undefined),
  refreshA2AProxy: vi.fn(async () => undefined),
}));
vi.mock("../apis/agent-card", () => ({
  getAgentCard: vi.fn(),
  setAgentCardSource: vi.fn(async () => undefined),
  refreshAgentCard: vi.fn(async () => undefined),
  deleteAgentCardSource: vi.fn(async () => undefined),
}));

import { getAgentCard } from "../apis/agent-card";
import { useGetAgentCard } from "./agent-card";
import { a2aProxyRefetchInterval, useSetA2AProxy } from "./a2a-proxy";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const params = { orgName: "o", projName: "p", agentName: "a", envId: "dev" };
const proxy = (status: A2AProxyResponse["status"]): A2AProxyResponse => ({ status, lastError: "", upstreamAuthHeader: "" });
const card: AgentCardResponse = { source: "external", status: "pending", sourceUrl: "https://a/.well-known/agent-card.json", lastError: "" };

let unmount: (() => void) | undefined;
afterEach(() => unmount?.());

describe("a2aProxyRefetchInterval", () => {
  it("re-polls while the gateway publish is in flight", () => {
    expect(a2aProxyRefetchInterval(proxy("pending"))).toBeGreaterThan(0);
  });

  it("stops once published, failed, or the environment is direct", () => {
    expect(a2aProxyRefetchInterval(proxy("published"))).toBe(false);
    expect(a2aProxyRefetchInterval(proxy("failed"))).toBe(false);
    expect(a2aProxyRefetchInterval(proxy("pending"), { status: 404 })).toBe(false);
  });
});

describe("useSetA2AProxy", () => {
  // A mode switch changes the card too; the form must not close on the old card.
  it("settles only after the agent card is refetched", async () => {
    vi.mocked(getAgentCard).mockResolvedValue(card);
    const result = {} as { current: ReturnType<typeof useSetA2AProxy> };
    const Probe = () => {
      useGetAgentCard(params);
      result.current = useSetA2AProxy();
      return null;
    };
    const root = createRoot(document.createElement("div"));
    const client = new QueryClient();
    await act(async () =>
      root.render(createElement(QueryClientProvider, { client }, createElement(Probe))),
    );
    unmount = () => act(() => root.unmount());

    let release!: () => void;
    vi.mocked(getAgentCard).mockImplementationOnce(
      () => new Promise((resolve) => { release = () => resolve(card); }),
    );
    let settled = false;
    await act(async () => {
      void result.current.mutateAsync({ params, body: {} }).then(() => { settled = true; });
    });
    expect(settled).toBe(false);

    await act(async () => release());
    expect(settled).toBe(true);
  });
});
