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

package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractDeploymentsFromBatch is the gateway's ExtractDeploymentsFromBatchZip
// (gateway-controller pkg/utils/api_utils.go at gateway/v2026.09.24), minus its logging: the
// archive's layout is a contract with it.
func extractDeploymentsFromBatch(data []byte) (map[string][]byte, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gzReader.Close() }()

	tarReader := tar.NewReader(gzReader)
	deployments := make(map[string][]byte)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		hasInvalidSegment := false
		for _, segment := range strings.Split(header.Name, "/") {
			if segment == ".." {
				hasInvalidSegment = true
				break
			}
		}
		if hasInvalidSegment {
			continue
		}
		cleanPath := filepath.Clean(header.Name)
		deploymentID := filepath.Base(filepath.Dir(cleanPath))
		if deploymentID == "." || deploymentID == "" {
			continue
		}
		ext := filepath.Ext(cleanPath)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		content, err := io.ReadAll(tarReader)
		if err != nil {
			continue
		}
		deployments[deploymentID] = content
	}
	return deployments, nil
}

func TestCreateDeploymentBatchTarGzIsReadByTheGateway(t *testing.T) {
	entries := []DeploymentArchiveEntry{
		{DeploymentID: "11111111-1111-1111-1111-111111111111", FileName: "llm-proxy-my-proxy.yaml", Content: []byte("kind: LlmProxy\n")},
		{DeploymentID: "22222222-2222-2222-2222-222222222222", FileName: "agent-x.yaml", Content: []byte("kind: Agent\n")},
	}

	archive, err := CreateDeploymentBatchTarGz(entries)
	require.NoError(t, err)

	got, err := extractDeploymentsFromBatch(archive)
	require.NoError(t, err)
	assert.Equal(t, map[string][]byte{
		"11111111-1111-1111-1111-111111111111": []byte("kind: LlmProxy\n"),
		"22222222-2222-2222-2222-222222222222": []byte("kind: Agent\n"),
	}, got)
}

func TestCreateDeploymentBatchTarGzEmpty(t *testing.T) {
	archive, err := CreateDeploymentBatchTarGz(nil)
	require.NoError(t, err)
	got, err := extractDeploymentsFromBatch(archive)
	require.NoError(t, err)
	assert.Empty(t, got, "an empty batch is still a valid archive")
}
