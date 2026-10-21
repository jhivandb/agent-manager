/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */


import { validateEndpointUrl } from "./url";

export interface A2ACardSkill {
  id: string;
  name: string;
  description: string;
  tags: string[];
  examples: string[];
  inputModes: string[];
  outputModes: string[];
}

export interface A2ACardInterface {
  url: string;
  protocolBinding: string;
}

/** An A2A agent card read defensively; a missing or malformed value reads as "" or []. */
export interface ParsedA2ACard {
  name: string;
  description: string;
  version: string;
  providerOrganization: string;
  /** The provider's website, only when it is an http(s) URL, so it is safe to link. */
  providerUrl: string;
  /** Interfaces with a URL; one without is unreachable. */
  interfaces: A2ACardInterface[];
  skills: A2ACardSkill[];
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

function parseSkill(raw: Json): A2ACardSkill {
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

/** Reads a third-party A2A agent card without trusting its shape. */
export function parseA2ACard(card: unknown): ParsedA2ACard {
  const source = isRecord(card) ? card : {};
  const capabilities = isRecord(source.capabilities) ? source.capabilities : {};
  const provider = isRecord(source.provider) ? source.provider : {};
  const providerUrl = asString(provider.url);

  return {
    name: asString(source.name),
    description: asString(source.description),
    version: asString(source.version),
    providerOrganization: asString(provider.organization),
    providerUrl: providerUrl && validateEndpointUrl(providerUrl) === null ? providerUrl : "",
    interfaces: asRecords(source.supportedInterfaces)
      .map((iface) => ({
        url: asString(iface.url),
        protocolBinding: asString(iface.protocolBinding),
      }))
      .filter((iface) => iface.url !== ""),
    skills: asRecords(source.skills).map(parseSkill),
    capabilities: {
      streaming: capabilities.streaming === true,
      pushNotifications: capabilities.pushNotifications === true,
      extendedAgentCard: capabilities.extendedAgentCard === true,
    },
    securitySchemes: isRecord(source.securitySchemes) ? Object.keys(source.securitySchemes) : [],
  };
}

const skillFingerprint = (skill: A2ACardSkill) =>
  JSON.stringify([skill.description, skill.tags, skill.examples]);

/** Skill names added, removed and changed going from card a to card b. */
function diffA2ASkills(a: ParsedA2ACard, b: ParsedA2ACard) {
  const before = new Map(a.skills.map((s) => [s.name, s]));
  const after = new Map(b.skills.map((s) => [s.name, s]));
  return {
    added: [...after.keys()].filter((name) => !before.has(name)),
    removed: [...before.keys()].filter((name) => !after.has(name)),
    changed: [...after.keys()].filter((name) => {
      const prior = before.get(name);
      return prior !== undefined && skillFingerprint(prior) !== skillFingerprint(after.get(name)!);
    }),
  };
}

/** The value with object keys sorted, so two encodings of one card compare equal. */
function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (!isRecord(value)) return value;
  return Object.fromEntries(
    Object.keys(value)
      .sort()
      .map((key) => [key, canonical(value[key])]),
  );
}

/** Interface URLs differ per environment's gateway, so they never count as a difference. */
const withoutInterfaceUrls = (card: Json): Json => ({
  ...card,
  supportedInterfaces: Array.isArray(card.supportedInterfaces)
    ? card.supportedInterfaces.map((iface) =>
      isRecord(iface) ? Object.fromEntries(Object.entries(iface).filter(([key]) => key !== "url")) : iface,
    )
    : card.supportedInterfaces,
});

/**
 * How card b differs from card a, on the same terms as the catalogue's "card differs" flag:
 * the top-level fields that differ, other than skills, and the skills added, removed and changed.
 */
export function diffA2ACards(a: unknown, b: unknown) {
  const before = withoutInterfaceUrls(isRecord(a) ? a : {});
  const after = withoutInterfaceUrls(isRecord(b) ? b : {});
  const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])].sort();
  return {
    fields: keys.filter(
      (key) =>
        key !== "skills" &&
        JSON.stringify(canonical(before[key])) !== JSON.stringify(canonical(after[key])),
    ),
    skills: diffA2ASkills(parseA2ACard(a), parseA2ACard(b)),
  };
}
