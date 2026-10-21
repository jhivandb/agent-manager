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
import { diffA2ACards, parseA2ACard } from "./a2aCard";

const fullCard = {
  name: "Orders",
  description: "Handles orders",
  version: "1.2.0",
  provider: { organization: "Acme", url: "https://acme.example" },
  supportedInterfaces: [
    { url: "https://orders.example/a2a", protocolBinding: "JSONRPC" },
  ],
  capabilities: { streaming: true, pushNotifications: false, extendedAgentCard: true },
  securitySchemes: { bearer: { type: "http" }, key: { type: "apiKey" } },
  skills: [
    {
      id: "order-status",
      name: "order-status",
      description: "Look up an order",
      tags: ["orders"],
      examples: ["Where is order 7?"],
      inputModes: ["text/plain"],
      outputModes: ["application/json"],
    },
  ],
};


describe("parseA2ACard", () => {
  it("reads every field of an A2A v1 card", () => {
    expect(parseA2ACard(fullCard)).toEqual({
      name: "Orders",
      description: "Handles orders",
      version: "1.2.0",
      providerOrganization: "Acme",
      providerUrl: "https://acme.example",
      interfaces: [{ url: "https://orders.example/a2a", protocolBinding: "JSONRPC" }],
      skills: [
        {
          id: "order-status",
          name: "order-status",
          description: "Look up an order",
          tags: ["orders"],
          examples: ["Where is order 7?"],
          inputModes: ["text/plain"],
          outputModes: ["application/json"],
        },
      ],
      capabilities: { streaming: true, pushNotifications: false, extendedAgentCard: true },
      securitySchemes: ["bearer", "key"],
    });
  });

  it("falls back to empty values for malformed input", () => {
    const parsed = parseA2ACard({ skills: "x", capabilities: null });
    expect(parsed.skills).toEqual([]);
    expect(parsed.capabilities).toEqual({
      streaming: false,
      pushNotifications: false,
      extendedAgentCard: false,
    });
  });

  it("returns an empty card when there is no card", () => {
    expect(parseA2ACard(undefined).skills).toEqual([]);
  });

  it("drops a provider URL that is not safe to link", () => {
    const parsed = parseA2ACard({ provider: { organization: "Acme", url: "javascript:alert(1)" } });
    expect(parsed.providerOrganization).toBe("Acme");
    expect(parsed.providerUrl).toBe("");
  });

  it("drops interfaces without a URL", () => {
    const parsed = parseA2ACard({
      supportedInterfaces: [{ protocolBinding: "GRPC" }, { url: "https://a.example", protocolBinding: 7 }],
    });
    expect(parsed.interfaces).toEqual([{ url: "https://a.example", protocolBinding: "" }]);
  });
});

describe("diffA2ACards", () => {
  const status = { name: "order-status", description: "d", tags: [], examples: [] };
  const refund = { name: "issue-refund", description: "r", tags: [], examples: [] };

  it("reports skills only present in the second card as added", () => {
    const diff = diffA2ACards({ skills: [status] }, { skills: [status, refund] });
    expect(diff.skills).toEqual({ added: ["issue-refund"], removed: [], changed: [] });
    expect(diff.fields).toEqual([]);
  });

  it("reports removed and changed skills", () => {
    const diff = diffA2ACards(
      { skills: [status, refund] },
      { skills: [{ ...status, description: "new" }] },
    );
    expect(diff.skills).toEqual({ added: [], removed: ["issue-refund"], changed: ["order-status"] });
  });

  it("names the other fields that differ, ignoring key order", () => {
    const diff = diffA2ACards(
      { version: "1", capabilities: { streaming: true, pushNotifications: false } },
      { version: "2", capabilities: { pushNotifications: false, streaming: true } },
    );
    expect(diff.fields).toEqual(["version"]);
  });

  it("ignores interface URLs, which differ per gateway, but not their bindings", () => {
    const iface = (url: string, protocolBinding = "JSONRPC") => ({ url, protocolBinding });
    expect(
      diffA2ACards(
        { supportedInterfaces: [iface("https://dev.gw/a")] },
        { supportedInterfaces: [iface("https://prod.gw/a")] },
      ).fields,
    ).toEqual([]);
    expect(
      diffA2ACards(
        { supportedInterfaces: [iface("https://dev.gw/a")] },
        { supportedInterfaces: [iface("https://dev.gw/a", "HTTP+JSON")] },
      ).fields,
    ).toEqual(["supportedInterfaces"]);
  });
});
