# `amctl` CLI & MCP — Feature Report

**Branch:** `cli-obs-commands`

## 1. Full CLI Surface

Global flags (root): `--json` (JSON envelope output), `--org` (override active organization).

```
amctl
├── login                              Sign in to an instance
│       --url, --name, --client-id, --client-secret, --auth-server
├── version                            Print the amctl version
│
├── agent                              Manage agents in a project
│       persistent: --project
│   ├── list                           List agents in a project
│   │       --limit, --offset
│   ├── get <agent>                    Show details of an agent
│   ├── create <name>                  Create an agent
│   │       --display-name (req), --type, --subtype, --description,
│   │       --provisioning, --repo-url, --repo-branch, --repo-path, --repo-secret,
│   │       --build-type, --language, --language-version, --run-command,
│   │       --dockerfile, --port, --base-path, --openapi-spec,
│   │       --no-auto-instrumentation,
│   │       --env (rep), --env-secret (rep), --env-from-secret (rep),
│   │       --model-config-file
│   ├── delete <agent>                 Delete an agent (--yes/-y)
│   ├── deploy <agent>                 Deploy a built image
│   │       --build-name, --env (rep), --yes/-y
│   ├── build                          Manage agent builds
│   │   ├── create <agent>             Trigger a build (--commit)
│   │   ├── list <agent>               List builds (--limit, --offset)
│   │   ├── get <agent> <build>        Show build details
│   │   └── logs <agent> [build]       Stream build logs
│   │
│   ├── logs <agent>                   Runtime logs for a deployed agent
│   │       --since, --level (DEBUG/INFO/WARN/ERROR), --grep,
│   │       --limit (1–10000), --sort (asc/desc), --env
│   ├── metrics <agent>                CPU & memory (current / request / limit)
│   │       --since, --env
│   ├── traces <agent>                 List recent traces (with optional filter)
│   │       --since, --limit (1–100), --sort,
│   │       --condition (error_status | high_latency | high_token_usage |
│   │                   tool_call_fails | excessive_steps),
│   │       --max-latency, --max-tokens, --max-spans, --env
│   │   └── export <agent>             Export traces with full spans as JSON
│   │           --since, --limit, --sort, --env
│   └── trace <agent> <traceId>        Span tree for one trace, or single-span view
│           --span <id>, --since, --limit (default 1000), --env
│
├── project                            Manage projects in an organization
│   ├── list                           List projects
│   ├── get <project>                  Show project details
│   ├── create <name>                  Create a project
│   │       --display-name (req), --description
│   └── delete <project>               Delete a project (--yes/-y)
│
├── context                            View / manage CLI context
│   ├── show                           Show current context
│   ├── link                           Link CWD to org / project / agent
│   │       --project, --agent
│   ├── unlink                         Remove the project link for CWD
│   ├── instance                       Manage configured instances
│   │   ├── list
│   │   ├── use <name>                 Switch active instance
│   │   └── remove <name>              (--yes/-y)
│   └── org                            Manage active organization
│       ├── list
│       └── use <name>
│
└── (hidden top-level aliases)         link  →  context link
                                       unlink → context unlink
```

### Cross-cutting CLI behaviour
- **Context resolution:** every command resolves `org` / `project` / `agent` from flags → CWD-link → active context, in that order.
- **Output modes:** every command honours `--json`; tabular output via the shared `tableprinter`.
- **Shell completion:** `ValidArgsFunction` wired for agent/project/build names; `disableFileCompletion` removes file completion for non-file flags.
- **Auth:** OAuth/PKCE login (`cli/pkg/auth`) with token cache per instance.
- **Discovery:** CLI calls `GET /api/v1/config` on the agent-manager service to discover the trace-observer URL (rewrites `host.docker.internal` → `localhost` for local dev).
- **Guards:** runtime obs commands (`logs`, `metrics`, `traces`, `trace`) reject non-runtime-managed (external) agents client-side via `ValidateRuntimeManaged`.
- **Validation:** path-param sanitization (`ValidatePathParam`), duration parsing (`ParseDuration`), trace-IDs lower-cased.

### New supporting plumbing on this branch
- Handwritten `traceobssvc` client (`cli/pkg/clients/traceobssvc/`).
- `Factory.TraceObserver` with URL discovery.
- Factory helpers `ResolveEnvironment`, `EnvScope`, `AddEnvFlag` standardise the `--env` flag and scope formatting.
- Regenerated `amsvc` client adds the `getConfig` endpoint.
- `pflag` promoted to a direct dependency.

## 2. Full MCP Surface

Tools are registered in `agent-manager-service/mcp/tools/register.go` and grouped into five toolsets. Required fields are listed; org and environment fall back to env vars when omitted.

### Projects (`projects.go`)

| Tool | Purpose | Required | Notable options |
|---|---|---|---|
| `list_projects` | List projects in an organization | `org_name` | `limit`, `offset` |
| `create_project` | Create a project (logical container for agents) | `org_name`, `project_name`, `display_name` | `description` |

### Agents (`agents.go`)

| Tool | Purpose | Required | Notable options |
|---|---|---|---|
| `list_agents` | List agents in a project (shows provisioning type) | `org_name`, `project_name` | `limit`, `offset` |
| `list_project_agent_pairs` | Cross-project agent inventory with search | `org_name` | `project_search`, `agent_search`, project/agent pagination |
| `create_external_agent` | Register an externally-hosted agent + emit token / instrumentation setup | `org_name`, `project_name`, `agent_name`, `display_name`, `language` (`python` \| `ballerina`) | `description` |
| `create_internal_agent_python` | Create platform-hosted Python agent from a git repo | `org_name`, `project_name`, `agent_name`, `display_name`, `repository_url`, `branch`, `app_path`, `interface_type` (`DEFAULT` \| `CUSTOM`), `env` | `language_version`, `run_command`, `port`, `base_path`, `openapi_path`, `enable_auto_instrumentation`, `instrumentation_version` |

### Builds (`builds.go`)

| Tool | Purpose | Required | Notable options |
|---|---|---|---|
| `list_builds` | List builds for an agent (status / image) | `org_name`, `project_name`, `agent_name` | `limit`, `offset` |
| `get_build_details` | Build metadata: steps, duration, commit | `org_name`, `project_name`, `agent_name`, `build_name` | — |
| `build_agent` | Trigger a new build | `org_name`, `project_name`, `agent_name` | `commit_id` (defaults to latest) |
| `get_build_logs` | Step-by-step build logs | `org_name`, `project_name`, `agent_name`, `build_name` | — |

### Deployments (`deployments.go`)

| Tool | Purpose | Required | Notable options |
|---|---|---|---|
| `list_deployments` | Deployments across environments | `org_name`, `project_name`, `agent_name` | — |
| `deploy_agent` | Release a built image to the lowest pipeline env | `org_name`, `project_name`, `agent_name`, `image_id` | `env[]` (key / value / is_sensitive / secret_ref), `enable_auto_instrumentation` |
| `update_deployment_state` | Redeploy or undeploy from a specific environment | `org_name`, `project_name`, `agent_name`, `environment`, `state` | — |

### Observability (`observability.go`) — added by this branch

| Tool | Purpose | Required | Notable options |
|---|---|---|---|
| `get_runtime_logs` | Filter agent runtime logs | `project_name`, `agent_name` | `start_time`/`end_time` (RFC3339, ≤14d), `limit` (1–10000), `sort_order`, `log_levels[]`, `search_phrase` |
| `get_metrics` | CPU / memory / request / limit metrics | `project_name`, `agent_name` | time window ≤14d |
| `list_traces` | Trace summaries within a window | `project_name`, `agent_name` | `limit` (1–100, default 10), `sort_order`, `include_io` |
| `get_traces` | Traces with full spans, optionally filtered | `project_name`, `agent_name` | `condition` (`error_status` \| `high_latency` \| `high_token_usage` \| `tool_call_fails` \| `excessive_steps`) + `max_latency` / `max_tokens` / `max_spans`; `limit` ≤100; window ≤30d |
| `get_trace_details` | One trace's metadata + span list | `project_name`, `agent_name`, `trace_id` | `limit` (default 1000) |
| `get_span_details` | Execution detail for one span | `project_name`, `agent_name`, `trace_id`, `span_id` | — |

### Cross-cutting MCP behaviour
- **Toolset structure:** each domain has its own handler interface in `mcp/handlers/`; tools call the handler and return `gomcp.CallToolResult`.
- **Defaults:** org & environment from env vars; obs windows default to last 24h.
- **Validation per tool:** required-field checks, `limit` bounds, RFC3339 time parsing, log-level enum, max-window limits (14d for logs/metrics, 30d for traces).
- **Internal vs external agents:** two creation paths — only Python is supported for internal today; both Python and Ballerina for external.
- **CRUD scope:** projects/agents expose create + list (no update/delete via MCP); builds are read + trigger; deployments have list / deploy / state-update.
- **Pagination:** `limit` / `offset` consistent across list tools (default 10, max 50).
- **Logging wrapper:** every tool is wrapped in `withToolLogging` for structured invocation logs.
- **Response reducers** (`extractTraceOverviews`, `extractTracesWithSpans`, `extractTraceDetails`) trim observability payloads before returning to the model.

## 3. CLI ↔ MCP Parity (observability)

| Capability | CLI | MCP |
|---|---|---|
| Runtime logs | `agent logs` | `get_runtime_logs` |
| Metrics | `agent metrics` | `get_metrics` |
| Trace list (summary) | `agent traces` (no `--condition`) | `list_traces` |
| Trace list w/ filters | `agent traces --condition ...` | `get_traces` (same 5 conditions) |
| Trace export (full spans) | `agent traces export` | `get_traces` |
| Single trace spans | `agent trace <id>` | `get_trace_details` |
| Single span detail | `agent trace <id> --span <id>` | `get_span_details` |
