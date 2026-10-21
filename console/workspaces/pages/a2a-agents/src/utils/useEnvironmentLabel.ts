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

import { useListEnvironments } from "@agent-management-platform/api-client";

/** Labels an environment with its display name, or its name when it has none. */
export function useEnvironmentLabel(orgName: string): (name: string) => string {
  const { data: environments } = useListEnvironments({ orgName });
  const labels = new Map(
    (environments ?? []).map((env) => [env.name, env.displayName || env.name]),
  );
  return (name) => labels.get(name) ?? name;
}
