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

import type { ParsedA2ACard } from "@agent-management-platform/shared-component";
import type { A2AConnectResponse } from "@agent-management-platform/types";

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

/** When the gateway fronts the agent it is the endpoint; otherwise the card's interfaces are. */
export function resolveConnectEndpoints(
  connect: Pick<A2AConnectResponse, "gatewayUrl" | "cardUrl" | "jsonRpcUrl">,
  interfaces: CardInterface[],
): ConnectEndpoints {
  if (connect.gatewayUrl) {
    return {
      endpoints: [
        { label: "Gateway endpoint", url: connect.gatewayUrl },
        { label: "JSON-RPC", url: connect.jsonRpcUrl },
      ],
      declared: interfaces,
      cardUrl: connect.cardUrl,
      snippetEndpoint: connect.jsonRpcUrl,
    };
  }
  return {
    endpoints: interfaces.map((iface) => ({
      label: iface.protocolBinding || "Endpoint",
      url: iface.url,
    })),
    declared: [],
    cardUrl: connect.cardUrl,
    snippetEndpoint: connect.jsonRpcUrl || (interfaces[0]?.url ?? ""),
  };
}
