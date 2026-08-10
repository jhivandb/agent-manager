.PHONY: help install build build-webapp dev clean purge

# Default target
help:
	@echo "Console Development Commands:"
	@echo ""
	@echo "  make install       - Install all dependencies"
	@echo "  make build         - Build all projects"
	@echo "  make build-webapp  - Build core-ui and its dependencies"
	@echo "  make dev           - Start development"
	@echo "  make clean         - Clean build outputs"
	@echo "  make purge         - Remove node_modules and all caches"
	@echo ""

# Install dependencies
install:
	@echo "Installing dependencies..."
	@pnpm install

# Build all projects
build:
	@echo "Building all projects..."
	@pnpm build

build-webapp:
	@echo "Building core-ui..."
	@pnpm turbo run build --filter=@agent-management-platform/am-core-ui...

dev:
	@echo "Starting development mode (core-ui watch + web-ui dev server)..."
	@pnpm dev

# Clean build outputs
clean:
	@echo "Cleaning build outputs..."
	@find workspaces -name "dist" -type d -exec rm -rf {} + 2>/dev/null || true
	@find apps -name "dist" -type d -exec rm -rf {} + 2>/dev/null || true
	@echo "Done!"

# Remove installed dependencies and caches
purge: clean
	@echo "Purging node_modules and caches..."
	@find . -name "node_modules" -type d -prune -exec rm -rf {} + 2>/dev/null || true
	@rm -rf .turbo
	@echo "Done!"
