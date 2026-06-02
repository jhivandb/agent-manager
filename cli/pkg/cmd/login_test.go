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

package cmd

import (
	"testing"

	"github.com/wso2/agent-manager/cli/pkg/cmdutil"
)

func TestNewLoginCmdURL(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "defaults to Agent Platform",
			want: "https://production-wso2cloud.gateway.cloud.wso2.com/agent-manager-service-agent-manager-api",
		},
		{
			name: "uses explicit override",
			args: []string{"--url", "http://localhost:9000"},
			want: "http://localhost:9000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewLoginCmd(&cmdutil.Factory{})
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags() error = %v", err)
			}
			got, err := cmd.Flags().GetString("url")
			if err != nil {
				t.Fatalf("GetString(%q) error = %v", "url", err)
			}
			if got != tt.want {
				t.Errorf("url = %q, want %q", got, tt.want)
			}
		})
	}
}
