# Agent Manager CLI — Proposal

## Why do we need this?

Agent Manager is primarily used through the UI. For developers building agents, there needs to be a bridge between local development and cloud deployment — this should be a CLI. It also lets a developer's agentic tools (Claude Code, Copilot, etc.) interact with Agent Manager directly.

Goals: smooth local-to-cloud deployment, plus in-cloud debugging (logs, traces, metrics) from the terminal.

## Core challenges

- Agents can't interact with the console effectively. When a cloud agent misbehaves, the developer has to hand traces and logs to their tools manually. Going through the REST API directly is context-heavy — the spec is ~11k lines.
- Dev loop is console-bound. Triggering a build, promoting to dev, fetching the invoke URL — all UI actions. Each one is a context switch.

## Existing solutions

**Railway CLI** — <https://github.com/railwayapp/cli>

- Tight local-to-cloud flow: `railway init`, `railway up`, `railway dev`, `railway logs`.
- `railway up` streams build logs until the app is live. The CLI should expose build/deploy state clearly, even while Agent Manager only offers request-response APIs.

**Google Workspace CLI** — <https://github.com/googleworkspace/cli>

- Generates its command tree from Google's discovery documents, staying in lockstep with upstream.
- `--help` at every level — how LLM agents discover capabilities.
- Defaults to JSON for responses and wraps errors in JSON for a consistent response shape.
- `--dry-run` on every command.

## Design principles

1. Agent-consumable by default. Stable commands, predictable JSON responses, and help and dry-run at every subcommand.
2. Thin over the OpenAPI spec.
3. Scope (`--instance`, `--org`, `--project`) is explicit and overridable, with context fallback. Environment is default-only in M1.
4. Progress is inspectable. Mutations return JSON for the operation they triggered; status, build, deployment, and log commands are used to inspect progress.

## Non-goals

- Local agent execution runtime (no `railway dev` analogue in v1).
- TUI/dashboards.

## User stories

Developer:

- Run `am auth login --url <cp-url>` once, all subsequent commands authenticated.
- Run `am agent create -f agent.yaml`, watch the initial build/deploy complete, then try it without opening the console.
- Run `am agent status <agent>` to check deployed state.
- Run `am agent try <agent> "hello"` after the automatic initial deployment.
- Switch control planes with `am context instance use cloud`.
- Tail filtered runtime logs with `am agent logs <agent> --since 10m`.
- Run `am agent traces <agent>` to see available traces and `am agent trace <agent> <traceId>` to get one trace.

Agentic tool:

- Discover every command and flag via `--help`.
- Pipe JSON to `am agent create -f -`, then poll `am agent status` until state is `Ready` or `Failed` to decide the next action.
- Use `--dry-run` for correctness checking before executing a request.

Operator (M2):

- Register gateways, assign environments, rotate tokens from the CLI.
- Manage LLM providers, proxies, and API keys without the console.

## Services the CLI talks to

| Service | Used for | Notes |
|---|---|---|
| agent-manager-service | auth, orgs/projects/agents, build, deploy, build logs, runtime logs, metrics, endpoints, invoke token | primary control plane |
| traces-observer-service | list traces, get single trace, export traces | URL discovered from agent-manager |
| IdP (Thunder or other)| OAuth2 flow for login | issuer URL discovered from agent-manager |

Both services share the auth token.

### Required API Dependency: Metadata Discovery

M1 requires one unauthenticated discovery call against the agent-manager URL. That response must provide auth metadata and a non-optional way to discover the traces-observer URL.

## Authentication model

Login and instance registration are a single action. An instance is its URL; the token is only valid against that URL.

- M1 supports browser-based OAuth authorization code login for interactive use.
- M1 supports OAuth client credentials for headless and automation use.
- Device code login is future work once the IdP supports it.

Instance, org, and project are CLI context values used to fill required API path parameters.

## Agent lifecycle

Four stages: create → build → deploy → observe. Create is the full definition boundary; the service performs the initial build and automatically deploys after that first build completes. Build and deploy remain explicit action primitives for later rebuild/manual deployment flows; observe is read-only and spans two services.

| Step | Endpoint | Service | Required | Produces |
|---|---|---|---|---|
| Create | `POST /agents` | agent-manager | name, displayName, provisioning (repo config), agentType. Optional: build, configurations, inputInterface, modelConfig[] | Agent; service starts initial build and auto-deploys on completion |
| Build | `POST /agents/{a}/builds` | agent-manager | — (commit optional) | BuildResponse with imageId for explicit rebuilds |
| Deploy | `POST /agents/{a}/deployments` | agent-manager | imageId | Deployment |
| Inspect — agent | `GET /agents/{a}` | agent-manager | agentName | Agent |
| Inspect — model configs | `GET /agents/{a}/model-configs` / `GET /agents/{a}/model-configs/{id}` | agent-manager | agentName / configId | Model config(s) |
| Inspect — resource configs | `GET /agents/{a}/resource-configs` | agent-manager | optional environment | Resource configs |
| Inspect — runtime configurations | `GET /agents/{a}/configurations` | agent-manager | environment | Runtime configurations |
| Observe — build logs | `GET /agents/{a}/builds/{b}/build-logs` | agent-manager | buildId | Log lines |
| Observe — runtime logs | `POST /agents/{a}/runtime-logs` | agent-manager | time range / level / search filter | Log lines |
| Observe — metrics | `POST /agents/{a}/metrics` | agent-manager | time range | Resource metrics |
| Observe — list traces | `GET /api/v1/traces` | traces-observer | agent, environment, startTime, endTime | Trace list |
| Observe — single trace | `GET /api/v1/trace` | traces-observer | traceId, agent, environment | Spans |
| Observe — export traces | `GET /api/v1/traces/export` | traces-observer | agent, environment, time range | Full traces (capped) |

Four things matter:

1. Create already accepts `build`, `configurations`, `inputInterface`, and `modelConfig[]` atomically. The CLI uses that as the only v1 definition payload.
2. The initial create path does not require the user to pass an `imageId`; the service auto-deploys the first successful build.
3. For explicit rebuild/manual deploy flows, the only identifier that has to flow between mutation steps is `imageId` (build → deploy).
4. Traces are addressed through the same agent and environment context as the rest of the CLI.

### Definition payload

`CreateAgentRequest` is too large for flags. Use a manifest and pass it directly to `am agent create -f <file|->`:

```yaml
# agent.yaml
name: order-triage
displayName: Order Triage
agentType: { type: agent-api, subType: chat-api }
provisioning:
  type: internal
  repository:
    url: https://github.com/acme/order-triage
    branch: main
    appPath: /
    secretRef: acme-github-pat
build:
  type: buildpack
  buildpack: { language: python, languageVersion: "3.12" }
inputInterface: { type: HTTP, port: 8080, basePath: /v1 }
modelConfig:
  - envMappings:
      default:
        providerName: openai-gpt4
        configuration: { policies: [{ name: cache, enabled: true }] }
```

There is no `am agent apply` and no update/configure mutation surface in M1. If an agent definition changes after create, the user updates it through the UI for now or recreates the agent. This keeps M1 thin over the OpenAPI primitives and avoids a partial reconcile story across basic info, build parameters, scale configs, and model configs.

### Primitive mutations

The M1 mutation commands are explicit API primitives listed in the milestone. `createAgent` currently triggers an implicit initial build, and the service automatically deploys after that first build completes. `am agent build <agent>` is therefore an explicit rebuild command, not the default next step after create.

Manual deploy requires an explicit image ID in M1. No latest-build auto-resolution, `redeploy`, or `--build` shortcut until the build/deploy semantics are proven in real use.

`am agent delete` is destructive and irreversible. It prompts for confirmation by default; `--force` skips the prompt for automation.

### Read-only inspection

Read-only commands expose list/get surfaces for adjacent resources without adding update/configure mutations. The milestone command list is the source of truth for the exact M1 surface.

### Observe

Read-only observe commands federate agent-manager and traces-observer. Runtime logs, metrics, and build logs come from agent-manager; traces, single trace lookup, and trace export come from traces-observer.

## M1 Dependencies And Constraints

M1 is intentionally small. These are the platform constraints that define the boundary:

- Metadata discovery: `am auth login --url` needs auth metadata and the traces-observer URL discoverable from the agent-manager URL.
- Default environment only: M1 does not expose `--env`; commands that require an environment use the default environment.
- Create is the happy path: `createAgent` accepts the full definition and starts the initial build/deploy flow, so M1 does not add `ship`, `apply`, or implicit build/deploy chaining.
- No live streaming: logs, status, and traces are request-response in M1. Users inspect progress with status, build, deployment, and log commands.
- Spec/runtime parity: generated clients depend on the OpenAPI spec matching runtime responses, especially status values.

Server follow-ups, in rough priority order: metadata discovery, spec/runtime parity, streaming logs/traces, env-aware deploy, unified status, invoke endpoint.

## Scope

Every API-backed command resolves `instance`, `org`, and `project` before making a request. M1 does not expose environment selection; commands that require environment use the default environment.

## Milestones

### M1 — Core Agent Developer CLI

Goal: fastest path from login to a running, invokable agent.

```
am auth login --url <url> [--name <name>]
am auth login --url <url> --client-id <id> --client-secret <secret>
am auth logout [--instance <name>]

am context show
am context instance list | use <name> | remove <name>
am context org     use <org> | list
am context project use <proj> | list

am project list | create <project> | get <project>

am agent create -f <file|->
am agent list
am agent get    <agent>
am agent delete <agent>

am agent build <agent> [--commit <sha>]
am agent build list <agent> [--limit N] [--offset N]
am agent build get  <agent> <buildId>
am agent build logs <agent> <buildId>

am agent deploy <agent> --image <imageId>
am agent deployment list <agent>
am agent status  <agent>
am agent try     <agent> [prompt]
am agent endpoints <agent>

am agent logs    <agent> [--since <dur>] [--level …] [--grep …]
am agent metrics <agent> [--since <dur>]
am agent traces  <agent> [--since <dur>] [--limit N]
am agent trace   <agent> <traceId>
am agent traces export <agent> --since <dur>

am agent model-config list <agent> [--limit N] [--offset N]
am agent model-config get  <agent> <configId>
am agent resource-config get <agent>
am agent configurations get <agent>
```

M1 targets the default environment only. The CLI uses the default environment where required by the backing API.

Delivered:

- Instance → org → project context for scoped API calls.
- OAuth2 browser authorization-code login and client-credentials login.
- `create` as the single definition payload: full `CreateAgentRequest` from file/stdin, including build, configurations, input interface, and inline model configs.
- Explicit action primitives for non-default paths: `build`, `deploy --image`, `delete`.
- Read-only inspection for agents, builds, build logs, deployments, endpoints, model configs, resource configs, and runtime configurations.
- No update/configure mutations in M1. No `ship`, `redeploy`, latest-build deploy, or `--build` deploy shortcut.
- Mutations return JSON for the operation they triggered. Progress is inspected with status, build, deployment, and log commands.
- `status` merges `getAgent` + `listAgentDeployments`.
- Observe suite: runtime logs and resource metrics against agent-manager; build logs via `am agent build logs`; traces, single trace, and trace export against traces-observer.
- `try` composites token + endpoints + direct HTTP. Only supported for chat HTTP-JSON agents in M1; for other subtypes it exits with a pointer to `am agent endpoints` for external invocation.

### M2 — Platform and Operations CLI

M2 expands API coverage to platform operations: gateways, environments, LLM provider templates, LLM providers, LLM proxies, monitors, evaluators, agent tokens, git secrets, and catalog. Exact command names can follow the OpenAPI surface when M2 starts.

## Success metrics

- Time to first deploy on a fresh machine: < 5 minutes.
- Agent-consumability: every command exposes deterministic `--help` output, and every error is emitted as parseable JSON with a stable shape (code, message, next-step hint).

## API Discussion Points

- IdP device-code support. Is missing in Thunder; browser auth works where a browser is available, and device code would cover headless user auth. Client credentials covers automation in M1.
- Invoke protocols. v1 is HTTP-JSON only; other protocols fall back to `am agent endpoints` + external tooling.
- Polling vs. streaming. M1 uses request-response inspection commands; tailing/following logs should wait for the M2 streaming work.
- Spec drift. Generated clients need the OpenAPI spec to match runtime responses. Spec/implementation mismatches must fail CI — any implementation change lands with the corresponding spec change in the same PR.

## Rollout

1. Internal preview — trial with own agents (teams)
2. WSO2Con with M1 — must be ready by 15 May.
3. GA with M2 — full API coverage and agent integration docs.
