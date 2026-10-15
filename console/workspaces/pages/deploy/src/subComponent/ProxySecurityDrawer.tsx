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

import { useRef, useState } from "react";
import { Box, Button, Form } from "@wso2/oxygen-ui";
import { ShieldCheck } from "@wso2/oxygen-ui-icons-react";
import { DrawerContent, DrawerHeader, DrawerWrapper } from "@agent-management-platform/views";
import {
  useGetAgentConfigurations,
  useUpdateAgentDeploySettings,
} from "@agent-management-platform/api-client";
import { SecurityConfigSections, type SecurityConfigHandle } from "./SecurityConfigSections";

export interface ProxySecurityDrawerProps {
  open: boolean;
  onClose: () => void;
  orgName: string;
  projName: string;
  agentName: string;
  environment: string;
}

/**
 * Inbound auth, CORS and card CORS for an external A2A agent routed through one environment's
 * gateway. Unlike EditDeployConfigDrawer it saves only deploy settings: there is no release
 * binding.
 */
export function ProxySecurityDrawer({
  open,
  onClose,
  orgName,
  projName,
  agentName,
  environment,
}: ProxySecurityDrawerProps) {
  const securityRef = useRef<SecurityConfigHandle>(null);
  const [valid, setValid] = useState(true);
  const agent = { orgName, projName, agentName };
  const { data: configurations } = useGetAgentConfigurations(agent, { environment });
  const { mutate: updateDeploySettings, isPending } = useUpdateAgentDeploySettings();

  const apply = () => {
    const security = securityRef.current;
    if (!security?.validate()) return;
    updateDeploySettings(
      { params: agent, body: { environmentName: environment, ...security.buildBody() } },
      { onSuccess: onClose },
    );
  };

  return (
    <DrawerWrapper open={open} onClose={onClose}>
      <DrawerHeader icon={<ShieldCheck size={24} />} title="Gateway security" onClose={onClose} />
      <DrawerContent>
        <Form.Stack spacing={3}>
          <SecurityConfigSections
            ref={securityRef}
            orgName={orgName}
            projName={projName}
            agentName={agentName}
            environment={environment}
            open={open}
            disabled={isPending}
            configurations={configurations}
            onValidityChange={setValid}
          />
          <Box display="flex" justifyContent="flex-end" gap={1}>
            <Button variant="outlined" onClick={onClose} disabled={isPending}>
              Cancel
            </Button>
            <Button variant="contained" onClick={apply} disabled={!valid || isPending}>
              Apply
            </Button>
          </Box>
        </Form.Stack>
      </DrawerContent>
    </DrawerWrapper>
  );
}
