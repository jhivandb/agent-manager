// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
package services

import (
	"encoding/json"
)

type a2aCardSummary struct {
	Name        string
	Description string
	Version     string
	Skills      []string
}

// summarizeA2ACard never fails: absent or mistyped fields yield zero values.
func summarizeA2ACard(card json.RawMessage) a2aCardSummary {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(card, &fields); err != nil {
		return a2aCardSummary{}
	}
	return a2aCardSummary{
		Name:        stringField(fields["name"]),
		Description: stringField(fields["description"]),
		Version:     stringField(fields["version"]),
		Skills:      skillNames(fields["skills"]),
	}
}

func stringField(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func skillNames(raw json.RawMessage) []string {
	var skills []json.RawMessage
	if err := json.Unmarshal(raw, &skills); err != nil {
		return nil
	}
	var names []string
	for _, skill := range skills {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(skill, &fields); err != nil {
			continue
		}
		if name := stringField(fields["name"]); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// a2aCardFingerprint hashes the card without its interface URLs, which differ per environment's gateway.
func a2aCardFingerprint(card json.RawMessage) (string, error) {
	if card == nil {
		return "", nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(card, &fields); err != nil {
		return a2aCardHash(card)
	}
	var interfaces []map[string]json.RawMessage
	if err := json.Unmarshal(fields["supportedInterfaces"], &interfaces); err == nil {
		for _, iface := range interfaces {
			delete(iface, "url")
		}
		stripped, err := json.Marshal(interfaces)
		if err != nil {
			return "", err
		}
		fields["supportedInterfaces"] = stripped
	}
	withoutURLs, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return a2aCardHash(withoutURLs)
}
