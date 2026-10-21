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

import { createElement, type ReactNode } from "react";
import { act, renderHook } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { A2ACatalogAgent } from "@agent-management-platform/types";
import { useCatalogueSelection } from "./useCatalogueSelection";

const agent = (projectName: string): A2ACatalogAgent => ({
  projectName,
  agentName: "orders",
  kind: "platform",
  name: "orders",
  description: "",
  version: "1",
  skills: [],
  leadEnvironment: "dev",
  environments: [],
});

function setup(initial = "/?q=x") {
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(MemoryRouter, { initialEntries: [initial] }, children);
  return renderHook(
    () => ({ catalogue: useCatalogueSelection(), location: useLocation() }),
    { wrapper },
  );
}

describe("useCatalogueSelection", () => {
  it("selects an agent with its lead environment and keeps other params", () => {
    const { result } = setup();
    act(() => result.current.catalogue.select(agent("p2")));
    expect(new URLSearchParams(result.current.location.search).toString()).toBe(
      "q=x&project=p2&agent=orders&env=dev",
    );
    expect(result.current.catalogue.selection).toEqual({
      projectName: "p2",
      agentName: "orders",
      env: "dev",
    });
  });

  it("changes the environment of the current selection", () => {
    const { result } = setup("/?project=p2&agent=orders&env=dev");
    act(() => result.current.catalogue.selectEnv("prod"));
    expect(result.current.catalogue.selection?.env).toBe("prod");
  });

  it("clears the selection but keeps other params", () => {
    const { result } = setup("/?q=x&project=p2&agent=orders&env=dev");
    act(() => result.current.catalogue.clear());
    expect(result.current.location.search).toBe("?q=x");
    expect(result.current.catalogue.selection).toBeNull();
  });

  it("has no selection unless both project and agent are present", () => {
    const { result } = setup("/?agent=orders");
    expect(result.current.catalogue.selection).toBeNull();
  });
});
