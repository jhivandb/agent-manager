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
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUserFacingA2AErrorShowsOnlyPublicMessages(t *testing.T) {
	public := newA2APublicError("agent card request returned HTTP 404", nil)
	assert.Equal(t, "agent card request returned HTTP 404", userFacingA2AError(public, "fallback"))
	assert.Equal(t, "agent card request returned HTTP 404",
		userFacingA2AError(fmt.Errorf("outer: %w", public), "fallback"), "wrapping keeps it public")
	assert.Equal(t, "fallback",
		userFacingA2AError(errors.New("failed to connect to `user=ams database=amdb`: SQLSTATE 28P01"), "fallback"))
}

func TestPrefixA2APublicErrorKeepsThePrefixOnlyWhenPublic(t *testing.T) {
	err := prefixA2APublicError("http://gw/a", newA2APublicError("agent card request returned HTTP 404", nil))
	assert.Equal(t, "http://gw/a: agent card request returned HTTP 404", userFacingA2AError(err, "fallback"))

	raw := errors.New("dial tcp 10.0.0.5:8080: connect: refused")
	wrapped := prefixA2APublicError("http://gw/a", raw)
	assert.Equal(t, "fallback", userFacingA2AError(wrapped, "fallback"))
	assert.ErrorIs(t, wrapped, raw)
}

func TestA2ALogErrorExposesTheHiddenCauseOnlyForLogs(t *testing.T) {
	err := prefixA2APublicError("http://gw/a", newA2APublicError("could not reach the agent card URL",
		errors.New("dial tcp 10.0.0.5:8080: connect: connection refused")))
	logged := a2aLogError(err)
	assert.Contains(t, logged, "could not reach the agent card URL")
	assert.Contains(t, logged, "dial tcp 10.0.0.5:8080")
	assert.NotContains(t, userFacingA2AError(err, "fallback"), "10.0.0.5")
}
