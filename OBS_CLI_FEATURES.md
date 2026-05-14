# OBS CLI Commands Branch — Feature Report

**Branch:** `cli-obs-commands` (commits ahead of `main`)

## CLI Features (`amctl agent ...`)

Observability subcommands added under `cli/pkg/cmd/agent/`:

| Command | Purpose | Key flags |
|---|---|---|
| `agent logs <agent>` | Runtime logs for a deployed agent | `--since`, `--level` (DEBUG/INFO/WARN/ERROR), `--grep`, `--limit` (1–10000), `--sort` (asc/desc), `--env` |
| `agent metrics <agent>` | CPU & memory usage (current/request/limit) | `--since`, `--env` |
| `agent traces <agent>` | List recent traces, optionally filtered | `--since`, `--limit` (1–100), `--sort`, `--condition` (`error_status`, `high_latency`, `high_token_usage`, `tool_call_fails`, `excessive_steps`), `--max-latency`, `--max-tokens`, `--max-spans`, `--env` |
| `agent traces export <agent>` | Export traces with full span data as JSON | `--since`, `--limit`, `--sort`, `--env` |
| `agent trace <agent> <traceId>` | Span tree for one trace, or single-span detail view | `--span <id>`, `--since`, `--limit` (default 1000), `--env` |

### Supporting plumbing
- Handwritten `traceobssvc` client (`cli/pkg/clients/traceobssvc/`).
- `Factory.TraceObserver` with URL discovery via the new `GET /api/v1/config` endpoint; rewrites `host.docker.internal` → `localhost` for local dev.
- New factory helpers: `ResolveEnvironment`, `EnvScope`, `AddEnvFlag` — standardise the `--env` flag and scope formatting across obs commands.
- Client-side guard: runtime obs commands (`logs`, `metrics`, `traces`, `trace`) reject external (non-runtime-managed) agents before hitting the API.
- Validation: time-window parsing, agent-name/trace-id path-param validation, lower-cased trace IDs.

## MCP Tools (Observability)

Registered in `agent-manager-service/mcp/tools/observability.go` (handler: `ObservabilityToolset`):

| Tool | Purpose | Required input | Notable options |
|---|---|---|---|
| `get_runtime_logs` | Filter agent runtime logs | `project_name`, `agent_name` | `start_time`/`end_time` (RFC3339, ≤14d), `limit` (1–10000), `sort_order`, `log_levels[]`, `search_phrase` |
| `get_metrics` | CPU/memory/request/limit metrics | `project_name`, `agent_name` | time window ≤14d |
| `list_traces` | Trace summaries within a window | `project_name`, `agent_name` | `limit` (1–100, default 10), `sort_order`, `include_io` |
| `get_traces` | Traces with full spans, optionally filtered | `project_name`, `agent_name` | `condition` (`error_status`, `high_latency`, `high_token_usage`, `tool_call_fails`, `excessive_steps`) + `max_latency` / `max_tokens` / `max_spans` thresholds; `limit` ≤100; time window ≤30d |
| `get_trace_details` | One trace's metadata + span list | `project_name`, `agent_name`, `trace_id` | `limit` (default 1000) |
| `get_span_details` | Execution detail for one span | `project_name`, `agent_name`, `trace_id`, `span_id` | — |

### Shared behaviour
- Defaults: org from env, environment from env, 24h window if omitted.
- Trace filtering conditions implemented in `matchesCondition` (errorCount > 0, latency in ms, total token usage, tool-span error flag, span count).
- Response reducers (`extractTraceOverviews`, `extractTracesWithSpans`, `extractTraceDetails`) trim payloads before returning to the model.

## CLI ↔ MCP Parity

| Capability | CLI | MCP |
|---|---|---|
| Runtime logs | `agent logs` | `get_runtime_logs` |
| Metrics | `agent metrics` | `get_metrics` |
| Trace list (summary) | `agent traces` (no `--condition`) | `list_traces` |
| Trace list w/ filters | `agent traces --condition ...` | `get_traces` (same 5 conditions) |
| Trace export (full spans) | `agent traces export` | `get_traces` (same endpoint) |
| Single trace spans | `agent trace <id>` | `get_trace_details` |
| Single span detail | `agent trace <id> --span <id>` | `get_span_details` |
