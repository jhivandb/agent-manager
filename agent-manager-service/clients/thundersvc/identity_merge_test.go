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

package thundersvc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRoleReplace_PreservesOUAndPermissionsWhenOverridesNil(t *testing.T) {
	cur := ThunderRole{
		OuID: "ou-1", Name: "reader", Description: "d",
		Permissions: []RolePermissionRequest{{ResourceServerID: "rs-1", Permissions: []string{"a:b"}}},
	}
	req := NewRoleReplace(cur, nil, nil)
	assert.Equal(t, "ou-1", req.OuID)
	assert.Equal(t, "reader", req.Name)
	assert.Equal(t, "d", req.Description)
	assert.Equal(t, cur.Permissions, req.Permissions)
}

func TestNewRoleReplace_AppliesOverrides(t *testing.T) {
	cur := ThunderRole{OuID: "ou-1", Name: "reader", Description: "old"}
	name, desc := "writer", "new"
	req := NewRoleReplace(cur, &name, &desc)
	assert.Equal(t, "writer", req.Name)
	assert.Equal(t, "new", req.Description)
	assert.Equal(t, "ou-1", req.OuID)
}

func TestNewGroupReplace_PreservesNameWhenNil(t *testing.T) {
	cur := ThunderGroup{Name: "team-a", Description: "d"}
	req := NewGroupReplace(cur, nil, nil)
	assert.Equal(t, "team-a", req.Name)
	assert.Equal(t, "d", req.Description)
}

func TestNewGroupReplace_AppliesOverrides(t *testing.T) {
	cur := ThunderGroup{Name: "team-a", Description: "old"}
	name := "team-b"
	req := NewGroupReplace(cur, &name, nil)
	assert.Equal(t, "team-b", req.Name)
	assert.Equal(t, "old", req.Description)
}
