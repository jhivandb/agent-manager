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

- **Node.js**: Version 18.20.3+ or 20.14.0+ (see engines in package.json)
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

### 2. Build Libraries

Build all shared libraries first:

```bash
make build-webapp
```

Or build all projects:
```bash
make build
```

### 3. Start Development Server

```bash
make dev
```

This will:
- Start all library dependencies in watch mode
- Launch the Vite dev server at `http://localhost:3000`
- Automatically rebuild dependencies when you make changes
- Hot-reload the webapp when dependencies update

Press `Ctrl+C` to stop all processes.

### 4. Environment Configuration

Copy the configuration template and customize it:

```bash
cp apps/webapp/public/config.js.template apps/webapp/public/config.js
```

Edit `apps/webapp/public/config.js` to set your API URL:

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

```bash
pnpm install                                    # was: rush install
pnpm build                                      # was: rush build
pnpm turbo run build --filter=<package-name>... # was: rush build --to <package-name>
pnpm lint                                       # was: rush lint
pnpm lint:fix                                   # eslint --fix across packages
make purge                                      # was: rush purge / rush update
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

# Preview production build
pnpm run preview
```

Note: `lint:fix` is only defined in some packages (e.g. `am-core-ui`, `api-client`, `auth`, `types`) — use `pnpm lint:fix` from the repo root to run ESLint `--fix` everywhere via Turbo.

## Project Structure Details

### Apps
- **webapp**: Main React application with Vite build system

### Libraries
- **auth**: Authentication provider and hooks
- **types**: Shared TypeScript type definitions
- **eslint-config**: Shared ESLint configuration
- **views**: Shared UI components and themes
- **api-client**: API client utilities

### Pages
- **AgentsListPage**: Example page component (use as reference)

