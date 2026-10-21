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

vi.mock("@agent-management-platform/views", () => ({ useSnackBar: () => ({ pushSnackBar: vi.fn() }) }));
vi.mock("@agent-management-platform/auth", () => ({
  useAuthHooks: () => ({ getToken: async () => "t", isAuthenticated: true, logout: vi.fn() }),
}));
vi.mock("./telemetry", () => ({
  ConsoleAction: {}, markSessionExpired: vi.fn(), useTrack: () => ({ track: vi.fn() }),
}));
vi.mock("../utils", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../utils")>()),
  httpGET: vi.fn(async () => ({ ok: true, json: async () => ({}) })),
}));

import { SERVICE_BASE, httpGET } from "../utils";
import { useGetA2AConnect, useListA2AAgents } from "./a2a-agents";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let unmount: (() => void) | undefined;
afterEach(() => {
  unmount?.();
  vi.mocked(httpGET).mockClear();
});

async function mount(useHook: () => unknown) {
  const Probe = () => {
    useHook();
    return null;
  };
  const root = createRoot(document.createElement("div"));
  await act(async () =>
    root.render(
      createElement(QueryClientProvider, { client: new QueryClient() }, createElement(Probe)),
    ),
  );
  await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
  unmount = () => act(() => root.unmount());
}

describe("useListA2AAgents", () => {
  it("sends only the set query params", async () => {
    await mount(() => useListA2AAgents({ orgName: "acme" }, { q: "refund", limit: 24, offset: 0 }));
    expect(httpGET).toHaveBeenCalledWith(
      `${SERVICE_BASE}/orgs/acme/a2a-agents`,
      expect.objectContaining({ searchParams: { q: "refund", limit: "24", offset: "0" } }),
    );
  });

  it("passes every filter through when set", async () => {
    await mount(() =>
      useListA2AAgents(
        { orgName: "acme" },
        { kind: "proxied", environment: "dev", project: "p", status: "failed" },
      ),
    );
    expect(httpGET).toHaveBeenCalledWith(
      `${SERVICE_BASE}/orgs/acme/a2a-agents`,
      expect.objectContaining({
        searchParams: { kind: "proxied", environment: "dev", project: "p", status: "failed" },
      }),
    );
  });
});

describe("useGetA2AConnect", () => {
  const params = { orgName: "acme", projName: "p 1", agentName: "a/b", envId: "dev" };

  it("requests the connect endpoint with encoded segments", async () => {
    await mount(() => useGetA2AConnect(params));
    expect(httpGET).toHaveBeenCalledWith(
      `${SERVICE_BASE}/orgs/acme/projects/p%201/agents/a%2Fb/environments/dev/a2a-connect`,
      expect.anything(),
    );
  });

  it("does not request without an environment", async () => {
    await mount(() => useGetA2AConnect({ ...params, envId: "" }));
    expect(httpGET).not.toHaveBeenCalled();
  });
});
