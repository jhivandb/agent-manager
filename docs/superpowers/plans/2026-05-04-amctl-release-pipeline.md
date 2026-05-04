# amctl Release Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace GoReleaser with a shell script and lightweight GitHub Actions workflow for building, packaging, and releasing the amctl CLI binary.

**Architecture:** A POSIX shell script (`scripts/build-amctl.sh`) becomes the single source of truth for cross-compiling, archiving, and checksumming amctl. The GitHub Actions workflow orchestrates testing, tagging, calling the script, and creating a draft GitHub release. The Makefile delegates to the script for local dev.

**Tech Stack:** POSIX sh, GitHub Actions, `softprops/action-gh-release`, Go cross-compilation via `GOOS`/`GOARCH`.

**Spec:** `docs/superpowers/specs/2026-05-04-amctl-release-pipeline-design.md`

---

### Task 1: Create build script `scripts/build-amctl.sh`

**Files:**
- Create: `scripts/build-amctl.sh`

- [ ] **Step 1: Create the build script**

```sh
#!/bin/sh
set -eu

VERSION="dev"
COMMIT=""
DATE=""
OUTPUT_DIR="dist"
SINGLE_TARGET=false

LDFLAGS_PKG="github.com/wso2/agent-manager/cli/pkg/version"

TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64"

usage() {
    cat <<USAGE
Usage: $0 [OPTIONS]

Cross-compile, package, and checksum the amctl CLI binary.

Options:
  --version VERSION     Version string (default: dev)
  --commit SHA          Git commit (default: current HEAD short SHA)
  --date DATE           Build date (default: now in RFC3339)
  --output-dir DIR      Output directory for archives and checksums (default: dist/)
  --single-target       Build only for the current GOOS/GOARCH
  -h, --help            Show this help
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --version)    VERSION="$2"; shift 2 ;;
        --commit)     COMMIT="$2"; shift 2 ;;
        --date)       DATE="$2"; shift 2 ;;
        --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
        --single-target) SINGLE_TARGET=true; shift ;;
        -h|--help)    usage; exit 0 ;;
        *)            echo "Unknown option: $1" >&2; usage >&2; exit 1 ;;
    esac
done

REPO_ROOT="$(git rev-parse --show-toplevel)"
[ -z "$COMMIT" ] && COMMIT="$(git rev-parse --short HEAD)"
[ -z "$DATE" ] && DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [ "$SINGLE_TARGET" = true ]; then
    TARGETS="$(go env GOOS)/$(go env GOARCH)"
fi

mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

echo "==> Building amctl v${VERSION} (commit ${COMMIT}, ${DATE})"
echo "==> Targets: ${TARGETS}"

cd "${REPO_ROOT}/cli"
go mod tidy

for target in $TARGETS; do
    os="${target%/*}"
    arch="${target#*/}"
    echo "==> Compiling ${os}/${arch}..."

    staging="$(mktemp -d)"
    trap_cleanup="${trap_cleanup:-} rm -rf \"${staging}\";"
    trap "$trap_cleanup" EXIT

    bin_name="amctl"
    [ "$os" = "windows" ] && bin_name="amctl.exe"

    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -o "${staging}/${bin_name}" \
        -ldflags "-s -w \
            -X ${LDFLAGS_PKG}.Version=${VERSION} \
            -X ${LDFLAGS_PKG}.Commit=${COMMIT} \
            -X ${LDFLAGS_PKG}.Date=${DATE}" \
        ./cmd/amctl

    cp "${REPO_ROOT}/LICENSE" "${staging}/LICENSE"

    archive_base="amctl_v${VERSION}_${os}_${arch}"
    if [ "$os" = "windows" ]; then
        (cd "$staging" && zip -q "${OUTPUT_DIR}/${archive_base}.zip" "$bin_name" LICENSE)
    else
        (cd "$staging" && tar -czf "${OUTPUT_DIR}/${archive_base}.tar.gz" "$bin_name" LICENSE)
    fi

    rm -rf "$staging"
done

echo "==> Generating checksums..."
if command -v sha256sum >/dev/null 2>&1; then
    (cd "$OUTPUT_DIR" && sha256sum amctl_v* > checksums.txt)
elif command -v shasum >/dev/null 2>&1; then
    (cd "$OUTPUT_DIR" && shasum -a 256 amctl_v* > checksums.txt)
else
    echo "Error: neither sha256sum nor shasum found" >&2
    exit 1
fi

count=$(find "$OUTPUT_DIR" -maxdepth 1 -name 'amctl_v*' | wc -l | tr -d ' ')
echo "==> Done: ${count} archives + checksums.txt in ${OUTPUT_DIR}/"
```

- [ ] **Step 2: Make the script executable**

Run: `chmod +x scripts/build-amctl.sh`

- [ ] **Step 3: Test single-target local build**

Run: `scripts/build-amctl.sh --single-target --output-dir /tmp/amctl-test-build`

Expected: one archive in `/tmp/amctl-test-build/` matching your OS/arch (e.g., `amctl_vdev_darwin_arm64.tar.gz`), plus `checksums.txt`. Verify the archive contains `amctl` + `LICENSE`:

Run: `tar -tzf /tmp/amctl-test-build/amctl_vdev_$(go env GOOS)_$(go env GOARCH).tar.gz`

Expected output:
```
amctl
LICENSE
```

- [ ] **Step 4: Test cross-compile (all targets)**

Run: `scripts/build-amctl.sh --version 0.0.0-test --output-dir /tmp/amctl-test-cross`

Expected: 5 archives + `checksums.txt` in `/tmp/amctl-test-cross/`:
```
amctl_v0.0.0-test_darwin_amd64.tar.gz
amctl_v0.0.0-test_darwin_arm64.tar.gz
amctl_v0.0.0-test_linux_amd64.tar.gz
amctl_v0.0.0-test_linux_arm64.tar.gz
amctl_v0.0.0-test_windows_amd64.zip
checksums.txt
```

Verify checksums: `cd /tmp/amctl-test-cross && shasum -a 256 -c checksums.txt`

Expected: all OK.

- [ ] **Step 5: Clean up test artifacts and commit**

Run:
```bash
rm -rf /tmp/amctl-test-build /tmp/amctl-test-cross
git add scripts/build-amctl.sh
git commit -m "Add scripts/build-amctl.sh for cross-compiling amctl"
```

---

### Task 2: Rewrite the GitHub Actions workflow

**Files:**
- Modify: `.github/workflows/amctl_release.yaml`

- [ ] **Step 1: Replace the workflow file with the new version**

```yaml
name: amctl CLI Release

on:
  workflow_dispatch:
    inputs:
      branch:
        description: "Branch to release from"
        required: true
        default: "main"
        type: string
      target_version:
        description: "Target version for the release (e.g., 0.1.0)"
        required: true
        type: string

run-name: amctl Release ${{ inputs.target_version }}

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4
        with:
          ref: ${{ inputs.branch }}

      - name: Setup Go
        uses: actions/setup-go@v6.1.0
        with:
          go-version-file: cli/go.mod
          cache-dependency-path: cli/go.sum

      - name: Run tests
        run: cd cli && go test ./... -v

  release:
    name: Release
    runs-on: ubuntu-latest
    needs: test
    permissions:
      contents: write
    steps:
      - name: Checkout code
        uses: actions/checkout@v4
        with:
          ref: ${{ inputs.branch }}
          fetch-depth: 0

      - name: Setup Go
        uses: actions/setup-go@v6.1.0
        with:
          go-version-file: cli/go.mod
          cache-dependency-path: cli/go.sum

      - name: Validate and set release metadata
        env:
          TARGET_VERSION: ${{ inputs.target_version }}
        run: |
          if ! [[ "$TARGET_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
            echo "Error: target_version must be semver (e.g., 1.2.3 or 1.2.3-rc.1)"
            exit 1
          fi
          printf 'VERSION=%s\n' "$TARGET_VERSION" >> "$GITHUB_ENV"
          printf 'RELEASE_TAG=amctl/v%s\n' "$TARGET_VERSION" >> "$GITHUB_ENV"

          if [[ "$TARGET_VERSION" == *-* ]]; then
            echo "IS_PRERELEASE=true" >> "$GITHUB_ENV"
          else
            echo "IS_PRERELEASE=false" >> "$GITHUB_ENV"
          fi

      - name: Check tag does not already exist
        run: |
          if git ls-remote --tags origin "$RELEASE_TAG" | grep -q "$RELEASE_TAG"; then
            echo "Error: Tag $RELEASE_TAG already exists. Please use a different version."
            exit 1
          fi

      - name: Create and push tag
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git tag -a "$RELEASE_TAG" -m "Release amctl v${VERSION}"
          git push origin "$RELEASE_TAG"

      - name: Build release artifacts
        run: |
          scripts/build-amctl.sh \
            --version "$VERSION" \
            --commit "$(git rev-parse --short HEAD)" \
            --date "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
            --output-dir dist/

      - name: Create draft GitHub release
        uses: softprops/action-gh-release@b4309332981a82ec1c5618f44dd2e27cc8bfbfda # v3.0.0
        with:
          tag_name: ${{ env.RELEASE_TAG }}
          name: "WSO2 Agent Manager - CLI amctl v${{ env.VERSION }}"
          draft: true
          prerelease: ${{ env.IS_PRERELEASE == 'true' }}
          body: |
            ## amctl v${{ env.VERSION }}

            **Commit:** ${{ github.sha }}
            **Date:** ${{ github.event.repository.updated_at }}
            **Branch:** ${{ inputs.branch }}

            ---
            _This is a draft release. Edit this description to add release notes before publishing._
          files: dist/*
```

- [ ] **Step 2: Review the diff to confirm changes**

Run: `git diff .github/workflows/amctl_release.yaml`

Verify:
- No GoReleaser step remains
- No `v${VERSION}` tag creation (only `amctl/v${VERSION}`)
- `softprops/action-gh-release` is SHA-pinned
- Draft release with placeholder body

- [ ] **Step 3: Commit**

Run:
```bash
git add .github/workflows/amctl_release.yaml
git commit -m "Rewrite amctl release workflow: replace GoReleaser with build script"
```

---

### Task 3: Update Makefile targets and help text

**Files:**
- Modify: `Makefile:1` (`.PHONY` line)
- Modify: `Makefile:42-45` (help text)
- Modify: `Makefile:217-225` (amctl build targets)

- [ ] **Step 1: Update the help text**

Replace lines 42-45:
```
	@echo "amctl CLI:"
	@echo "  make amctl-build             - Build amctl for current platform (requires goreleaser)"
	@echo "  make amctl-release-dry-run   - Cross-compile all targets without publishing"
	@echo "  make amctl-test              - Run amctl tests"
```

With:
```
	@echo "amctl CLI:"
	@echo "  make amctl-build             - Build amctl for current platform"
	@echo "  make amctl-release-dry-run   - Cross-compile all targets without publishing"
	@echo "  make amctl-test              - Run amctl tests"
```

- [ ] **Step 2: Update the build targets**

Replace lines 217-225:
```makefile
# amctl CLI build targets (requires: go install github.com/goreleaser/goreleaser/v2@latest)
amctl-build:
	goreleaser build --clean --snapshot --single-target

amctl-release-dry-run:
	goreleaser release --clean --snapshot --skip=publish

amctl-test:
	cd cli && go test ./... -v
```

With:
```makefile
# amctl CLI build targets
amctl-build:
	scripts/build-amctl.sh --single-target

amctl-release-dry-run:
	scripts/build-amctl.sh --output-dir dist/

amctl-test:
	cd cli && go test ./... -v
```

- [ ] **Step 3: Verify Makefile targets work**

Run: `make amctl-build`

Expected: single-target build completes, one archive in `dist/`.

Run: `make amctl-test`

Expected: CLI tests pass.

- [ ] **Step 4: Clean up and commit**

Run:
```bash
rm -rf dist/
git add Makefile
git commit -m "Update Makefile amctl targets: use build script instead of goreleaser"
```

---

### Task 4: Update install script archive naming

**Files:**
- Modify: `scripts/install-amctl.sh:74`

- [ ] **Step 1: Update the archive name format**

In `scripts/install-amctl.sh`, replace:
```sh
    ARCHIVE="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
```

With:
```sh
    ARCHIVE="${BINARY}_v${VERSION}_${OS}_${ARCH}.tar.gz"
```

- [ ] **Step 2: Commit**

Run:
```bash
git add scripts/install-amctl.sh
git commit -m "Update install-amctl.sh: archive naming now includes v prefix"
```

---

### Task 5: Delete `.goreleaser.yaml`

**Files:**
- Delete: `.goreleaser.yaml`

- [ ] **Step 1: Delete the file**

Run: `git rm .goreleaser.yaml`

- [ ] **Step 2: Verify nothing else references goreleaser**

Run: `grep -ri goreleaser Makefile .github/ scripts/ 2>/dev/null || echo "No references found"`

Expected: "No references found"

- [ ] **Step 3: Commit**

Run:
```bash
git commit -m "Delete .goreleaser.yaml: no longer needed"
```

---

### Task 6: End-to-end validation

- [ ] **Step 1: Run a full dry-run release**

Run: `make amctl-release-dry-run`

Expected: 5 archives + `checksums.txt` in `dist/`. Verify:

Run: `ls -la dist/`

Expected output (sizes will vary):
```
amctl_vdev_darwin_amd64.tar.gz
amctl_vdev_darwin_arm64.tar.gz
amctl_vdev_linux_amd64.tar.gz
amctl_vdev_linux_arm64.tar.gz
amctl_vdev_windows_amd64.zip
checksums.txt
```

- [ ] **Step 2: Verify checksum integrity**

Run: `cd dist && shasum -a 256 -c checksums.txt && cd ..`

Expected: all OK.

- [ ] **Step 3: Verify a linux binary has correct version info**

Run: `tar -xzf dist/amctl_vdev_linux_amd64.tar.gz -C /tmp amctl && file /tmp/amctl`

Expected: `ELF 64-bit LSB executable, x86-64` (confirms cross-compile worked).

- [ ] **Step 4: Verify archive contents include LICENSE**

Run: `tar -tzf dist/amctl_vdev_darwin_arm64.tar.gz`

Expected:
```
amctl
LICENSE
```

- [ ] **Step 5: Clean up**

Run: `rm -rf dist/ /tmp/amctl`

- [ ] **Step 6: Run tests one final time**

Run: `make amctl-test`

Expected: all tests pass.
