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

import type { ParsedA2ACard } from "./card";

type CardInterface = ParsedA2ACard["interfaces"][number];

export interface ConnectEndpoint {
  label: string;
  url: string;
}

export interface ConnectEndpoints {
  /** Where to call the agent, most important first. */
  endpoints: ConnectEndpoint[];
  /** The card's own interfaces when the gateway fronts the agent; for information only. */
  declared: CardInterface[];
  cardUrl: string;
  snippetEndpoint: string;
}

const AGENT_CARD_PATH = "/.well-known/agent-card.json";
// The gateway serves a platform agent's JSON-RPC transport here (AMS a2aPathPrefixJSONRPC).
const GATEWAY_JSONRPC_PATH = "/rpc";
const JSONRPC = "JSONRPC";

const isUnder = (url: string, base: string) => url === base || url.startsWith(`${base}/`);

/**
 * A gateway-fetched card (proxied external agent) already carries the gateway's
 * JSON-RPC URL; a platform agent's card carries the agent's own address instead.
 */
function gatewayJsonRpcUrl(gatewayUrl: string, interfaces: CardInterface[]): string {
  const rewritten = interfaces.find(
    (iface) => iface.protocolBinding === JSONRPC && isUnder(iface.url, gatewayUrl),
  );
  return rewritten?.url ?? `${gatewayUrl}${GATEWAY_JSONRPC_PATH}`;
}

function viaGateway(gatewayUrl: string, interfaces: CardInterface[]): ConnectEndpoints {
  const base = gatewayUrl.replace(/\/+$/, "");
  const jsonRpcUrl = gatewayJsonRpcUrl(base, interfaces);
  return {
    endpoints: [
      { label: "Gateway endpoint", url: base },
      { label: "JSON-RPC", url: jsonRpcUrl },
    ],
    declared: interfaces,
    cardUrl: `${base}${AGENT_CARD_PATH}`,
    snippetEndpoint: jsonRpcUrl,
  };
}

function direct(cardUrl: string, interfaces: CardInterface[]): ConnectEndpoints {
  const reachable = interfaces.filter((iface) => iface.url !== "");
  const preferred = reachable.find((iface) => iface.protocolBinding === JSONRPC) ?? reachable[0];
  return {
    endpoints: reachable.map((iface) => ({
      label: iface.protocolBinding || "Endpoint",
      url: iface.url,
    })),
    declared: [],
    cardUrl,
    snippetEndpoint: preferred?.url ?? "",
  };
}

/** When the gateway fronts the agent it is the endpoint; otherwise the card's interfaces are. */
export function resolveConnectEndpoints(
  gatewayUrl: string,
  sourceCardUrl: string,
  interfaces: CardInterface[],
): ConnectEndpoints {
  return gatewayUrl ? viaGateway(gatewayUrl, interfaces) : direct(sourceCardUrl, interfaces);
}
