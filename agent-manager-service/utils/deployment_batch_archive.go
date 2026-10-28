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
	"fmt"
	"time"
)

// DeploymentArchiveEntry is one deployment in the archive a gateway fetches in its startup sync.
type DeploymentArchiveEntry struct {
	DeploymentID string
	// FileName is the artifact YAML's name, as the single pull's ZIP names it.
	FileName string
	Content  []byte
}

// CreateDeploymentBatchTarGz writes each entry to <deploymentId>/<fileName> of a tar.gz, the layout
// the gateway's ExtractDeploymentsFromBatchZip reads: it keys each YAML by its directory.
func CreateDeploymentBatchTarGz(entries []DeploymentArchiveEntry) ([]byte, error) {
	var buf bytes.Buffer
	gzWriter, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip writer: %w", err)
	}
	tarWriter := tar.NewWriter(gzWriter)

	modTime := time.Now()
	for _, entry := range entries {
		name := entry.DeploymentID + "/" + entry.FileName
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(entry.Content)), ModTime: modTime}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("failed to write tar header for %s: %w", name, err)
		}
		if _, err := tarWriter.Write(entry.Content); err != nil {
			return nil, fmt.Errorf("failed to write tar content for %s: %w", name, err)
		}
	}

	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}
