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

// NewRoleReplace builds a full-replace UpdateRoleRequest from a role's current
// state. Thunder's PUT /roles/{id} is a full replace: an omitted ouId is blanked
// and omitted permissions are dropped, so both are always carried over from cur.
// A nil name/description override preserves the current value; a non-nil override
// replaces it. This keeps every full-replace call site from having to re-derive
// the preserved fields by hand.
func NewRoleReplace(cur ThunderRole, name, description *string) UpdateRoleRequest {
	req := UpdateRoleRequest{
		OuID:        cur.OuID,
		Name:        cur.Name,
		Description: cur.Description,
		Permissions: cur.Permissions,
	}
	if name != nil {
		req.Name = *name
	}
	if description != nil {
		req.Description = *description
	}
	return req
}

// NewGroupReplace builds a full-replace UpdateGroupRequest from a group's current
// state. A nil name/description override preserves the current value; a non-nil
// override replaces it.
func NewGroupReplace(cur ThunderGroup, name, description *string) UpdateGroupRequest {
	req := UpdateGroupRequest{Name: cur.Name, Description: cur.Description}
	if name != nil {
		req.Name = *name
	}
	if description != nil {
		req.Description = *description
	}
	return req
}
