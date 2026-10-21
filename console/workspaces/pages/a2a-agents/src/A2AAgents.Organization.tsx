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

import { generatePath, Navigate, Route, Routes, useParams } from "react-router-dom";
import { PageLayout } from "@agent-management-platform/views";
import { absoluteRouteMap } from "@agent-management-platform/types";
import { A2AAgentView } from "./subComponents/A2AAgentView";
import { CatalogueGrid } from "./subComponents/CatalogueGrid";

export function A2AAgentsOrganization() {
  const { orgId = "" } = useParams<{ orgId: string }>();

  return (
    <Routes>
      <Route
        index
        element={
          <PageLayout title="A2A Agents" disableIcon>
            <CatalogueGrid orgName={orgId} />
          </PageLayout>
        }
      />
      <Route path="view/:projectName/:agentName" element={<A2AAgentView />} />
      <Route
        path="*"
        element={
          <Navigate
            to={generatePath(absoluteRouteMap.children.org.children.a2aAgents.path, { orgId })}
            replace
          />
        }
      />
    </Routes>
  );
}

export default A2AAgentsOrganization;
