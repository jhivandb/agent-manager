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
import { buildA2ASnippet, type SnippetLanguage } from "./snippets";

const languages: SnippetLanguage[] = ["python", "typescript", "curl"];
const cardUrl = "https://gw.example/orders/.well-known/agent-card.json";
const endpointUrl = "https://gw.example/orders/rpc";

describe("buildA2ASnippet", () => {
  it.each(languages)("%s names the card URL and the endpoint", (lang) => {
    const snippet = buildA2ASnippet(lang, { cardUrl, endpointUrl, auth: null });
    expect(snippet).toContain(cardUrl);
    expect(snippet).toContain(endpointUrl);
  });

  it.each(languages)("%s sends the auth header when one is given", (lang) => {
    const snippet = buildA2ASnippet(lang, {
      cardUrl,
      endpointUrl,
      auth: { header: "X-API-Key", value: "k1" },
    });
    expect(snippet).toContain("X-API-Key");
    expect(snippet).toContain("k1");
  });

  it.each(languages)("%s has no auth lines without auth", (lang) => {
    const snippet = buildA2ASnippet(lang, { cardUrl, endpointUrl, auth: null });
    expect(snippet).not.toContain("X-API-Key");
    expect(snippet).not.toContain("Authorization");
  });

  it.each(languages)("%s tells the user to add the card's auth when security is unmanaged", (lang) => {
    const input = { cardUrl, endpointUrl, auth: null, authUnmanaged: true };
    const snippet = buildA2ASnippet(lang, input);
    expect(snippet).toContain("does not manage this agent's security");
  });

  it.each(languages)("%s adds no unmanaged note for an open agent", (lang) => {
    const snippet = buildA2ASnippet(lang, { cardUrl, endpointUrl, auth: null });
    expect(snippet).not.toContain("does not manage");
  });

  it("curl posts a v1 JSON-RPC SendMessage to the endpoint", () => {
    const snippet = buildA2ASnippet("curl", { cardUrl, endpointUrl, auth: null });
    expect(snippet).toContain(`-X POST '${endpointUrl}'`);
    expect(snippet).toContain('"method": "SendMessage"');
    expect(snippet).toContain('"role": "ROLE_USER"');
    expect(snippet).toContain("A2A-Version: 1.0");
  });

  it("curl keeps a single quote in a value inside its shell quoting", () => {
    const snippet = buildA2ASnippet("curl", {
      cardUrl,
      endpointUrl,
      auth: { header: "X-API-Key", value: "it's" },
    });
    expect(snippet).toContain(`'X-API-Key: it'\\''s'`);
  });

  it("python and typescript escape quotes in string literals", () => {
    const auth = { header: "X-API-Key", value: 'a"b' };
    expect(buildA2ASnippet("python", { cardUrl, endpointUrl, auth })).toContain('"a\\"b"');
    expect(buildA2ASnippet("typescript", { cardUrl, endpointUrl, auth })).toContain('"a\\"b"');
  });
});
