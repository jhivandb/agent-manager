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

import React from 'react';
import { Box } from '@wso2/oxygen-ui';
import { useParams } from 'react-router-dom';
import { PageLayout } from '@agent-management-platform/views';
import { a2aAgentsListPath, type A2AAgentRouteParams } from './routes';

export const A2AAgentPage: React.FC = () => {
  const { orgId = '', agentName = '' } = useParams<keyof A2AAgentRouteParams>();

  return (
    <PageLayout
      title={agentName}
      avatar={null}
      backHref={a2aAgentsListPath(orgId)}
      backLabel="Back to A2A Agents"
    >
      <Box data-testid="a2a-agent-page" />
    </PageLayout>
  );
};
