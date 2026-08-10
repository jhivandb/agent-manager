import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    dedupe: ['react', 'react-dom', 'react-router-dom'],
    alias: [
      // More specific aliases MUST come before general ones (prefix matching order matters)
      { find: '@agent-management-platform/am-core-ui/dist/index.css', replacement: path.resolve(import.meta.dirname, '../../workspaces/core-ui/dist/index.css') },

      // Resolve core-ui and all its sub-packages to source for hot-reload
      { find: '@agent-management-platform/am-core-ui', replacement: path.resolve(import.meta.dirname, '../../workspaces/core-ui/src') },

      // Workspace libraries
      { find: '@agent-management-platform/auth', replacement: path.resolve(import.meta.dirname, '../../workspaces/libs/auth/src') },
      { find: '@agent-management-platform/api-client', replacement: path.resolve(import.meta.dirname, '../../workspaces/libs/api-client/src') },
      { find: '@agent-management-platform/shared-component', replacement: path.resolve(import.meta.dirname, '../../workspaces/libs/shared-component/src') },
      { find: '@agent-management-platform/types', replacement: path.resolve(import.meta.dirname, '../../workspaces/libs/types/src') },
      { find: '@agent-management-platform/views', replacement: path.resolve(import.meta.dirname, '../../workspaces/libs/views/src') },

      // Workspace pages
      { find: '@agent-management-platform/add-new-agent', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/add-new-agent/src') },
      { find: '@agent-management-platform/add-new-project', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/add-new-project/src') },
      { find: '@agent-management-platform/build', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/build/src') },
      { find: '@agent-management-platform/deploy', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/deploy/src') },
      { find: '@agent-management-platform/overview', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/overview/src') },
      { find: '@agent-management-platform/configure-agent', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/configure-agent/src') },
      { find: '@agent-management-platform/test', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/test/src') },
      { find: '@agent-management-platform/traces', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/traces/src') },
      { find: '@agent-management-platform/logs', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/logs/src') },
      { find: '@agent-management-platform/metrics', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/metrics/src') },
      { find: '@agent-management-platform/eval', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/eval/src') },
      { find: '@agent-management-platform/llm-providers', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/llm-providers/src') },
      { find: '@agent-management-platform/mcp-proxies', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/mcp-proxies/src') },
      { find: '@agent-management-platform/gateways', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/gateways/src') },
      { find: '@agent-management-platform/env-thunders', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/env-thunders/src') },
      { find: '@agent-management-platform/agent-security', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/agent-security/src') },
      { find: '@agent-management-platform/agent-kind', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/agent-kind/src') },
      { find: '@agent-management-platform/identities', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/identities/src') },
      { find: '@agent-management-platform/settings', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/settings/src') },
      { find: '@agent-management-platform/deployment-pipelines', replacement: path.resolve(import.meta.dirname, '../../workspaces/pages/deployment-pipelines/src') },
    ],
  },
  server: {
    port: 3000,
  },
  build: {
    // The main app chunk sits at ~5 MB; PR builds bundle the branch merged with
    // main, which pushes it just past a 5000 kB limit. Keep headroom so the Vite
    // chunk-size warning (treated as a build failure in CI) doesn't trip.
    chunkSizeWarningLimit: 6000,
  },
})
