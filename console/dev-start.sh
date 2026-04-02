#!/bin/sh
set -e

LOCKFILE_PATH="/app/common/config/rush/pnpm-lock.yaml"
RUSH_TEMP_DIR="/app/common/temp"
RUSH_TEMP_HASH_FILE="${RUSH_TEMP_DIR}/.pnpm-lock.sha256"
VITE_CACHE_DIR="/app/apps/webapp/node_modules/.vite"

echo "==> Generating runtime config..."
cd /app/apps/webapp
envsubst < public/config.template.js > public/config.js

echo "==> Validating Rush temp dependencies..."
mkdir -p "${RUSH_TEMP_DIR}"
CURRENT_LOCK_HASH="$(sha256sum "${LOCKFILE_PATH}" | awk '{ print $1 }')"
STORED_LOCK_HASH=""
if [ -f "${RUSH_TEMP_HASH_FILE}" ]; then
  STORED_LOCK_HASH="$(cat "${RUSH_TEMP_HASH_FILE}")"
fi

cd /app
if [ ! -d "${RUSH_TEMP_DIR}/node_modules" ] || [ "${CURRENT_LOCK_HASH}" != "${STORED_LOCK_HASH}" ]; then
  echo "==> Refreshing Rush dependencies..."
  rush update --full
  printf '%s\n' "${CURRENT_LOCK_HASH}" > "${RUSH_TEMP_HASH_FILE}"
fi

echo "==> Clearing Vite prebundle cache..."
rm -rf "${VITE_CACHE_DIR}"

echo "==> Starting TypeScript watch mode for workspace packages..."

# Find all packages with tsconfig.lib.json and start tsc --watch for each
# Store the list first to avoid subshell issues
TSCONFIGS=$(find workspaces -name "tsconfig.lib.json" -type f 2>/dev/null)

echo "==> Waiting for initial compilation..."
sleep 5

echo "==> Starting Vite dev server..."
cd /app/apps/webapp
exec pnpm run dev --host 0.0.0.0
