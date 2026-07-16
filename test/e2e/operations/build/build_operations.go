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

package build

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
)

// WaitForBuildParams holds parameters for waiting on a build to complete.
type WaitForBuildParams struct {
	OrgName     string
	ProjectName string
	AgentName   string
	Timeout     time.Duration // default: 10 minutes
}

// WaitForBuildSuccess polls the builds API until a build reaches "Completed" status.
// It first waits for a build to appear in the builds list, then polls the individual
// build until its status is "Completed".
// Returns the build name of the successful build.
func WaitForBuildSuccess(client *framework.AMPClient, params *WaitForBuildParams) string {
	timeout := params.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	basePath := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s/builds",
		params.OrgName, params.ProjectName, params.AgentName)
	scope := fmt.Sprintf("org=%s project=%s agent=%s", params.OrgName, params.ProjectName, params.AgentName)

	var lastDiag string
	framework.AttachOnFailure("build: last poll result", func() string { return lastDiag })

	// Phase 1: Wait for at least one build to appear in the list.
	var buildName string
	Eventually(func(g Gomega) {
		resp, err := client.Get(basePath)
		g.Expect(err).NotTo(HaveOccurred(), "list builds request failed (%s)", scope)
		defer resp.Body.Close()

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			lastDiag = fmt.Sprintf("%s | list builds returned %d (non-retryable)", scope, resp.StatusCode)
			StopTrying(fmt.Sprintf("list builds returned %d (%s)", resp.StatusCode, scope)).Now()
		}
		list := framework.ExpectStatusAndDecode[framework.BuildsListResponse](g, resp, http.StatusOK)
		lastDiag = fmt.Sprintf("%s | phase=list | %d build(s) so far", scope, len(list.Builds))
		g.Expect(list.Builds).NotTo(BeEmpty(), "no builds found yet for %s", scope)

		buildName = list.Builds[len(list.Builds)-1].BuildName
	}).WithTimeout(timeout).WithPolling(5 * time.Second).Should(Succeed())

	ginkgo.GinkgoWriter.Printf("Build %q appeared, waiting for completion...\n", buildName)

	// Phase 2: Poll the individual build until status = "Completed".
	Eventually(func(g Gomega) {
		buildPath := fmt.Sprintf("%s/%s", basePath, buildName)
		resp, err := client.Get(buildPath)
		g.Expect(err).NotTo(HaveOccurred(), "build check failed (%s build=%s)", scope, buildName)
		defer resp.Body.Close()

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			lastDiag = fmt.Sprintf("%s | build=%s | build check returned %d (non-retryable)", scope, buildName, resp.StatusCode)
			StopTrying(fmt.Sprintf("build check returned %d (%s build=%s)", resp.StatusCode, scope, buildName)).Now()
		}
		detail := framework.ExpectStatusAndDecode[framework.BuildDetailsResponse](g, resp, http.StatusOK)

		status := ""
		if detail.Status != nil {
			status = *detail.Status
		}

		lastDiag = fmt.Sprintf("%s | build=%s | status=%q", scope, buildName, status)
		ginkgo.GinkgoWriter.Printf("Build status: %s\n", status)

		if status == "Failed" {
			StopTrying(fmt.Sprintf("build %s failed (%s)", buildName, scope)).Now()
		}

		g.Expect(status).To(Equal("Completed"), "build %s not yet completed (status=%q) for %s", buildName, status, scope)
	}).WithTimeout(timeout).WithPolling(10 * time.Second).Should(Succeed())

	return buildName
}
