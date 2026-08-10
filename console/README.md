# Agent Manager Console

React/TypeScript web application for the Agent Manager platform, built as a pnpm + Turborepo monorepo.

## Tech Stack

- **React 19** - UI framework
- **TypeScript** - Type safety
- **Vite** - Build tool and dev server
- **Turborepo** - Monorepo task orchestration
- **pnpm** - Package manager

## Prerequisites

Before you begin, ensure you have the following installed:

- **Node.js**: Version 20.19.0+ or 22.12.0+ (see engines in package.json)
- **pnpm**: Package manager, pinned via `packageManager` in package.json and activated through Corepack

### Installing pnpm

Enable Corepack (ships with Node.js):

```bash
corepack enable
```

Verify installation:
```bash
pnpm --version
```

Corepack activates the pinned pnpm 9.12.3 automatically based on the `packageManager` field in `console/package.json`.

## Getting Started

### 1. Install Dependencies

From the `console/` directory, install all dependencies for the monorepo:

```bash
cd console
make install
```

This command will:
- Install all dependencies for all projects in the monorepo via pnpm
- Create symlinks between local packages

### 2. Start Development Server

```bash
make dev
```

This will:
- Start all library dependencies in watch mode
- Launch the Vite dev server at `http://localhost:3000`
- Automatically rebuild dependencies when you make changes
- Hot-reload the webapp when dependencies update

Press `Ctrl+C` to stop all processes. No separate build step is needed for development —
Vite resolves `@agent-management-platform/*` imports straight to each package's `src/`.
Run `make build` only to produce production output.

### 3. Environment Configuration

Copy the configuration template and customize it:

```bash
cp apps/web-ui/public/config.template.js apps/web-ui/public/config.js
```

Edit `apps/web-ui/public/config.js` to set your API URL:

```javascript
window.APP_CONFIG = {
  API_URL: 'http://localhost:8080'
};
```

## Available Commands

### Make Commands (Recommended)

```bash
# Start development mode with hot-reload
make dev

# Install dependencies
make install

# Build all projects
make build

# Clean build outputs
make clean

# Remove node_modules and all caches
make purge

# Show all available commands
make help
```

### pnpm / Turbo Commands

The `make` targets wrap these. Reach for them directly when you need a filter:

```bash
pnpm build                                      # all packages
pnpm build:core-ui                              # core-ui and its dependencies
pnpm turbo run build --filter=<package-name>... # any package and its dependencies
pnpm lint                                       # eslint across packages
pnpm lint:fix                                   # eslint --fix across packages
```

### Project-Specific Commands

Navigate to any project directory and use `pnpm run`:

```bash
cd apps/web-ui

# Start development server
pnpm run dev

# Build for production
pnpm run build

# Run linting
pnpm run lint

# Fix linting issues
pnpm run lint:fix

# Preview production build
pnpm run preview
```

## Project Structure Details

See [`AGENTS.md`](AGENTS.md) for the package map — every workspace, its package name, and what it holds.

