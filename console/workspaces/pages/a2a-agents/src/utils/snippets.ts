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

// Written against A2A protocol 1.0, a2a-sdk 1.2 (Python) and @a2a-js/sdk 1.3.

export type SnippetLanguage = "python" | "typescript" | "curl";

export interface SnippetAuth {
  header: string;
  value: string;
}

export interface SnippetInput {
  cardUrl: string;
  endpointUrl: string;
  auth: SnippetAuth | null;
  authUnmanaged?: boolean;
}

const stringLiteral = (value: string) => JSON.stringify(value);

const shellQuote = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`;

const unmanagedAuthNote =
  "The platform does not manage this agent's security; add whatever auth its agent card declares.";

const authNoteLine = (input: SnippetInput, commentPrefix: string) =>
  input.authUnmanaged && !input.auth ? `${commentPrefix} ${unmanagedAuthNote}\n` : "";

const endpointNote = (endpointUrl: string) =>
  `The card routes JSON-RPC calls to ${stringLiteral(endpointUrl)}.`;

function pythonSnippet(input: SnippetInput): string {
  const { cardUrl, endpointUrl, auth } = input;
  const headers = auth ? `headers={${stringLiteral(auth.header)}: ${stringLiteral(auth.value)}}` : "";
  return `# pip install a2a-sdk
import asyncio
import uuid

import httpx
from a2a.client import ClientConfig, create_client
from a2a.types import Message, Part, Role, SendMessageRequest

CARD_URL = ${stringLiteral(cardUrl)}
# ${endpointNote(endpointUrl)}
${authNoteLine(input, "#")}

async def main() -> None:
    async with httpx.AsyncClient(${headers}) as http:
        client = await create_client(
            CARD_URL, ClientConfig(httpx_client=http), relative_card_path="/"
        )
        message = Message(
            message_id=str(uuid.uuid4()),
            role=Role.ROLE_USER,
            parts=[Part(text="Hello")],
        )
        async for event in client.send_message(SendMessageRequest(message=message)):
            print(event)


asyncio.run(main())
`;
}

function typescriptSnippet(input: SnippetInput): string {
  const { cardUrl, endpointUrl, auth } = input;
  const headers = auth ? `{ ${stringLiteral(auth.header)}: ${stringLiteral(auth.value)} }` : "{}";
  return `// npm install @a2a-js/sdk
import { Role, type SendMessageRequest } from "@a2a-js/sdk";
import {
  ClientFactory,
  ClientFactoryOptions,
  DefaultAgentCardResolver,
} from "@a2a-js/sdk/client";

const cardUrl = ${stringLiteral(cardUrl)};
// ${endpointNote(endpointUrl)}
${authNoteLine(input, "//")}const headers: Record<string, string> = ${headers};

const fetchWithHeaders: typeof fetch = (input, init) =>
  fetch(input, { ...init, headers: { ...Object.fromEntries(new Headers(init?.headers)), ...headers } });

const factory = new ClientFactory(
  ClientFactoryOptions.createFrom(ClientFactoryOptions.default, {
    cardResolver: new DefaultAgentCardResolver({ fetchImpl: fetchWithHeaders }),
  }),
);
const client = await factory.createFromUrl(cardUrl, "");

const request: SendMessageRequest = {
  tenant: "",
  message: {
    messageId: crypto.randomUUID(),
    contextId: "",
    taskId: "",
    role: Role.ROLE_USER,
    parts: [
      { content: { $case: "text", value: "Hello" }, metadata: undefined, filename: "", mediaType: "text/plain" },
    ],
    metadata: undefined,
    extensions: [],
    referenceTaskIds: [],
  },
  configuration: undefined,
  metadata: undefined,
};
const result = await client.sendMessage(request, { serviceParameters: headers });
console.log(JSON.stringify(result, null, 2));
`;
}

function curlSnippet(input: SnippetInput): string {
  const { cardUrl, endpointUrl, auth } = input;
  const authLine = auth ? `  -H ${shellQuote(`${auth.header}: ${auth.value}`)}` : null;
  const body = JSON.stringify(
    {
      jsonrpc: "2.0",
      id: 1,
      method: "SendMessage",
      params: {
        message: { messageId: "msg-1", role: "ROLE_USER", parts: [{ text: "Hello" }] },
      },
    },
    null,
    2,
  );
  const lines = (...parts: (string | null)[]) =>
    parts.filter((part): part is string => part !== null).join(" \\\n");
  return `${authNoteLine(input, "#")}# Fetch the agent card
${lines(`curl -s ${shellQuote(cardUrl)}`, authLine)}

# Send a message (JSON-RPC SendMessage)
${lines(
  `curl -s -X POST ${shellQuote(endpointUrl)}`,
  `  -H 'Content-Type: application/json'`,
  `  -H 'A2A-Version: 1.0'`,
  authLine,
  `  -d ${shellQuote(body)}`,
)}
`;
}

const builders: Record<SnippetLanguage, (input: SnippetInput) => string> = {
  python: pythonSnippet,
  typescript: typescriptSnippet,
  curl: curlSnippet,
};

export function buildA2ASnippet(lang: SnippetLanguage, input: SnippetInput): string {
  return builders[lang](input);
}
