# OpenChoreo upstream issues uncovered by issue #769

This file captures the OpenChoreo API limitations that surfaced while fixing
[wso2/agent-manager#769](https://github.com/wso2/agent-manager/issues/769).
Agent-manager has a client-side workaround in place (see below) but the root
cause is in OpenChoreo and should be filed upstream.

## Background

`GET` and `DELETE` of `/api/v1/orgs/{org}/projects/{proj}/agents/{agent}` in
agent-manager are forwarded to OpenChoreo's `Component` resource. The bug
report showed that:

1. `GET` ignored the project scope. An agent owned by `projects/A` could be
   retrieved through `projects/B`.
2. `DELETE` accepted the same wrong-project request and would have removed the
   component (and silently returned `204` for missing names as well, but that
   half is fixed entirely in agent-manager — it was a service-layer bug).

## Root cause in OpenChoreo

OpenChoreo's component endpoints identify a component **by name within a
namespace only**. There is no project segment in the path and no project query
parameter is honoured.

Generated client signatures (from `openchoreo-api.yaml`, regenerated into
`agent-manager-service/clients/openchoreosvc/gen/client.gen.go`):

```go
// GET /orgs/{namespaceName}/components/{componentName}
GetComponent(ctx, namespaceName, componentName, ...)

// DELETE /orgs/{namespaceName}/components/{componentName}
DeleteComponent(ctx, namespaceName, componentName, ...)
```

Compare with `ListComponents`, which *does* support project filtering:

```go
// GET /orgs/{namespaceName}/components?project=…
ListComponents(ctx, namespaceName, &gen.ListComponentsParams{Project: &p})
```

Because `GetComponent` / `DeleteComponent` do not filter by project, any caller
that knows a component name within an organization can read or delete it
regardless of which project owns it. That is a horizontal-privilege-escalation
risk in any multi-project deployment that relies on these endpoints for
authorization.

## Current workaround in agent-manager

Until OpenChoreo supports project-scoped get/delete, agent-manager validates
ownership client-side:

- `clients/openchoreosvc/client/components.go::GetComponent` calls the
  upstream `GET` and then compares `Spec.Owner.ProjectName` against the
  requested project. A mismatch returns `utils.ErrNotFound` so the existence
  of a component in another project is not leaked.
- `clients/openchoreosvc/client/components.go::DeleteComponent` performs a
  pre-flight `GetComponent` (which now enforces the same check) before
  invoking the upstream `DELETE`.
- `services/agent_manager.go::GetAgent` and
  `services/agent_manager.go::DeleteAgent` repeat the project check at the
  service layer (defense-in-depth, also covers tests that swap in mock
  clients which don't go through the wrapper).

This is a TOCTOU workaround: between the validating `GET` and the upstream
`DELETE`, a concurrent move/rename could in theory make the delete operate on
a component now owned by a different project. The window is small and the
upstream API does not give us a better primitive today.

## Suggested upstream fix

Either of the following would let agent-manager drop the workaround entirely:

1. **Project-scoped paths.** Move component endpoints under the project, e.g.
   `GET /orgs/{ns}/projects/{proj}/components/{name}` and
   `DELETE /orgs/{ns}/projects/{proj}/components/{name}`. The server returns
   `404` if `name` is not owned by `proj`. This matches the existing list
   endpoint's project filtering and the URL shape clients already use.

2. **Project query parameter.** Add an optional `?project=<name>` to the
   existing `GET`/`DELETE` endpoints; the server returns `404` when the
   component is not in the named project. Less invasive than option (1) but
   requires every caller to remember to send it (whereas a path segment
   cannot be forgotten).

Either option also lets the server return `404` atomically on `DELETE`, which
removes the TOCTOU window in the workaround.

## Affected upstream artifacts

- OpenAPI spec: `openchoreo-api.yaml` — operations `GetComponent` and
  `DeleteComponent` (and any other `*Component*` operation that takes a
  bare component name without a project; e.g. `UpdateComponent`,
  `GetComponentEndpoints`, etc. should be audited for the same pattern).
- Source repo: <https://github.com/openchoreo/openchoreo> (path used to
  regenerate the client is pinned in `Makefile::gen-oc-client`).
