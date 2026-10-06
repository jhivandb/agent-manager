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

import { describe, expect, it, vi } from "vitest";
import { agentCardRefetchInterval } from "./agent-card";
import { POLL_INTERVAL } from "../utils";

// The real package drags in oxygen-ui, which cannot load under node.
vi.mock("@agent-management-platform/views", () => ({ useSnackBar: () => ({}) }));

describe("agentCardRefetchInterval", () => {
  it("polls while the card is pending", () => {
    expect(agentCardRefetchInterval({ status: "pending", source: "platform", sourceUrl: "", lastError: "" }))
      .toBe(POLL_INTERVAL);
  });

  it("stops once fetched or failed, or with no data", () => {
    expect(agentCardRefetchInterval({ status: "fetched", source: "platform", sourceUrl: "", lastError: "" })).toBe(false);
    expect(agentCardRefetchInterval({ status: "failed", source: "platform", sourceUrl: "", lastError: "x" })).toBe(false);
    expect(agentCardRefetchInterval(undefined)).toBe(false);
  });
});
