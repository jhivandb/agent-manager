# Pinned Go dev tools for this repo. `make tools` installs them into .tools/bin,
# and every make recipe sees that directory first on PATH.
# Cluster CLIs (k3d, kubectl, helm) and Docker are prerequisites you install yourself.

TOOLS_ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
TOOLS_BIN  := $(TOOLS_ROOT)/.tools/bin

export PATH := $(TOOLS_BIN):$(PATH)

# Keep the including Makefile's default goal when this file comes first.
_tools_saved_default_goal := $(.DEFAULT_GOAL)

.PHONY: tools
tools:
	GOBIN=$(TOOLS_BIN) go install github.com/google/wire/cmd/wire@v0.7.0
	GOBIN=$(TOOLS_BIN) go install github.com/matryer/moq@v0.5.3
	GOBIN=$(TOOLS_BIN) go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.6.0
	GOBIN=$(TOOLS_BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
	GOBIN=$(TOOLS_BIN) go install honnef.co/go/tools/cmd/staticcheck@2026.1
	GOBIN=$(TOOLS_BIN) go install golang.org/x/tools/cmd/goimports@v0.51.0
	GOBIN=$(TOOLS_BIN) go install mvdan.cc/gofumpt@v0.12.0
	GOBIN=$(TOOLS_BIN) go install github.com/air-verse/air@v1.67.4
	# ginkgo must match github.com/onsi/ginkgo/v2 in test/e2e/go.mod
	GOBIN=$(TOOLS_BIN) go install github.com/onsi/ginkgo/v2/ginkgo@v2.28.3
	GOBIN=$(TOOLS_BIN) go install github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen@v0.162.0

.DEFAULT_GOAL := $(_tools_saved_default_goal)
