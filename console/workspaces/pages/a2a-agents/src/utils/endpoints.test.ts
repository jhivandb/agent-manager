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

import { describe, expect, it } from "vitest";
import { resolveConnectEndpoints } from "./endpoints";

const gatewayUrl = "https://gw.example/orders";
const viaGateway = {
  gatewayUrl,
  cardUrl: `${gatewayUrl}/.well-known/agent-card.json`,
  jsonRpcUrl: `${gatewayUrl}/rpc`,
};
const direct = (jsonRpcUrl: string) => ({
  gatewayUrl: "",
  cardUrl: "https://origin.example/.well-known/agent-card.json",
  jsonRpcUrl,
});

describe("resolveConnectEndpoints", () => {
  it("puts the gateway first and keeps the card's own interfaces for information", () => {
    const interfaces = [{ url: "http://localhost:9099/rpc", protocolBinding: "JSONRPC" }];
    const resolved = resolveConnectEndpoints(viaGateway, interfaces);
    expect(resolved.endpoints).toEqual([
      { label: "Gateway endpoint", url: gatewayUrl },
      { label: "JSON-RPC", url: `${gatewayUrl}/rpc` },
    ]);
    expect(resolved.declared).toEqual(interfaces);
    expect(resolved.cardUrl).toBe(viaGateway.cardUrl);
    expect(resolved.snippetEndpoint).toBe(`${gatewayUrl}/rpc`);
  });

  it("lists the card's interfaces as the endpoints of a directly reached agent", () => {
    const interfaces = [
      { url: "https://origin.example/rest", protocolBinding: "HTTP+JSON" },
      { url: "https://origin.example/rpc", protocolBinding: "JSONRPC" },
    ];
    const resolved = resolveConnectEndpoints(direct("https://origin.example/rpc"), interfaces);
    expect(resolved.endpoints).toEqual([
      { label: "HTTP+JSON", url: "https://origin.example/rest" },
      { label: "JSONRPC", url: "https://origin.example/rpc" },
    ]);
    expect(resolved.declared).toEqual([]);
    expect(resolved.cardUrl).toBe("https://origin.example/.well-known/agent-card.json");
    expect(resolved.snippetEndpoint).toBe("https://origin.example/rpc");
  });

  it("falls back to the first interface when the card offers no JSON-RPC", () => {
    const interfaces = [{ url: "https://origin.example/rest", protocolBinding: "HTTP+JSON" }];
    expect(resolveConnectEndpoints(direct(""), interfaces).snippetEndpoint)
      .toBe("https://origin.example/rest");
  });

  it("has no snippet endpoint when a direct agent's card declares none", () => {
    const resolved = resolveConnectEndpoints(direct(""), []);
    expect(resolved.endpoints).toEqual([]);
    expect(resolved.snippetEndpoint).toBe("");
  });
});
