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

export interface ParsedSkill {
  id: string;
  name: string;
  description: string;
  tags: string[];
  examples: string[];
  inputModes: string[];
  outputModes: string[];
}

export interface ParsedA2ACard {
  name: string;
  description: string;
  version: string;
  providerOrganization: string;
  interfaces: { url: string; protocolBinding: string }[];
  skills: ParsedSkill[];
  capabilities: {
    streaming: boolean;
    pushNotifications: boolean;
    extendedAgentCard: boolean;
  };
  securitySchemes: string[];
}

type Json = Record<string, unknown>;

const isRecord = (value: unknown): value is Json =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const asString = (value: unknown): string => (typeof value === "string" ? value : "");

const asStrings = (value: unknown): string[] =>
  Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];

const asRecords = (value: unknown): Json[] =>
  Array.isArray(value) ? value.filter(isRecord) : [];

function parseSkill(raw: Json): ParsedSkill {
  return {
    id: asString(raw.id),
    name: asString(raw.name),
    description: asString(raw.description),
    tags: asStrings(raw.tags),
    examples: asStrings(raw.examples),
    inputModes: asStrings(raw.inputModes),
    outputModes: asStrings(raw.outputModes),
  };
}

export function parseA2ACard(card: Json | undefined): ParsedA2ACard {
  const source = isRecord(card) ? card : {};
  const capabilities = isRecord(source.capabilities) ? source.capabilities : {};
  const provider = isRecord(source.provider) ? source.provider : {};

  return {
    name: asString(source.name),
    description: asString(source.description),
    version: asString(source.version),
    providerOrganization: asString(provider.organization),
    interfaces: asRecords(source.supportedInterfaces).map((iface) => ({
      url: asString(iface.url),
      protocolBinding: asString(iface.protocolBinding),
    })),
    skills: asRecords(source.skills).map(parseSkill),
    capabilities: {
      streaming: capabilities.streaming === true,
      pushNotifications: capabilities.pushNotifications === true,
      extendedAgentCard: capabilities.extendedAgentCard === true,
    },
    securitySchemes: isRecord(source.securitySchemes)
      ? Object.keys(source.securitySchemes)
      : [],
  };
}

const fingerprint = (skill: ParsedSkill) =>
  JSON.stringify([skill.description, skill.tags, skill.examples]);

export function diffSkills(a: ParsedA2ACard, b: ParsedA2ACard) {
  const before = new Map(a.skills.map((s) => [s.name, s]));
  const after = new Map(b.skills.map((s) => [s.name, s]));
  return {
    added: [...after.keys()].filter((name) => !before.has(name)),
    removed: [...before.keys()].filter((name) => !after.has(name)),
    changed: [...after.keys()].filter((name) => {
      const prior = before.get(name);
      return prior !== undefined && fingerprint(prior) !== fingerprint(after.get(name)!);
    }),
  };
}
