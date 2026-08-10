#!/bin/sh
set -e

# Capture the monorepo root (set by WORKDIR in Dockerfile.dev; matches host path)
MONOREPO_ROOT="$PWD"

echo "==> Linking dependencies for container environment..."
cd "$MONOREPO_ROOT"
pnpm install --frozen-lockfile

echo "==> Generating runtime config..."
cd "$MONOREPO_ROOT/apps/web-ui"
envsubst < public/config.template.js > public/config.js

echo "==> Starting core-ui in watch mode..."
cd "$MONOREPO_ROOT/workspaces/core-ui"
pnpm run dev &
CORE_UI_PID=$!
trap 'kill $CORE_UI_PID 2>/dev/null' EXIT

# web-ui's vite config aliases core-ui's dist/index.css, so it must exist before the dev server starts
echo "==> Waiting for initial core-ui build..."
CORE_UI_CSS="$MONOREPO_ROOT/workspaces/core-ui/dist/index.css"
WAITED=0
while [ ! -f "$CORE_UI_CSS" ]; do
  if [ "$WAITED" -ge 180 ]; then
    echo "core-ui did not produce dist/index.css within 180s" >&2
    exit 1
  fi
  sleep 1
  WAITED=$((WAITED + 1))
done

echo "==> Starting web-ui dev server..."
cd "$MONOREPO_ROOT/apps/web-ui"
exec pnpm run dev --host 0.0.0.0
