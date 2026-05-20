# CLI Inconsistencies

## Exit codes

Cobra arg-count errors (e.g. "accepts 1 arg(s), received 0") exit 1 with code
`CLI_TRANSPORT`, while flag validation errors (e.g. "--limit must be >= 1") exit
2 with code `INVALID_FLAG`. Both are usage errors and should use the same exit
code. `CLI_TRANSPORT` is misleading here — it implies a network problem.

## Error envelope fields

When cobra rejects args before `RunE` fires, the envelope has `"instance": ""`
and no `org`/`project` fields. When `RunE` fires but scope resolution fails
(e.g. missing `--project`), `instance` is populated but `org`/`project` are
absent. Success responses always include all three. This makes automated error
parsing inconsistent depending on where the error originates.

## `agent delete` reports success for non-existent agents

`agent delete -y nonexistent --project default` returns
`{"name": "nonexistent", "deleted": true}` even though `agent get nonexistent`
404s. Either the server is silently idempotent on DELETE and the CLI mirrors
that as a green success, or the response is unchecked. Lying about a deletion
that did not happen masks typos and stale automation. The CLI should surface
"not found" (and exit non-zero) when the target did not exist, or the server
should return 404 and the CLI should propagate it.

## `get` accepts only names, not UUIDs

`agent list` and `project list` prominently include `uuid` for each resource,
but `agent get <uuid>` returns 404 `AGENT_NOT_FOUND` and `project get <uuid>`
returns 500 `INTERNAL_ERROR`. Only the `name` resolves. Users naturally copy
UUIDs from list output and hit a confusing wall. Either accept both identifiers
in `get`/`delete`, or stop surfacing UUID as a top-level field in list output
(or document it as informational only). Note: the error shape also differs —
agent returns a clean 404 while project returns an opaque 500.

## Bogus organization errors are inconsistent and ugly

Same user mistake (typo in org name) produces two different error shapes:

- `--org bogus-org` (per-command override) -> `SERVER_RESPONSE_INVALID:
  server returned 404 with no JSON body`.
- `context org use bogus-org` -> `INTERNAL_ERROR (500): Failed to get
  organization`.

Neither is friendly and neither matches the clean `NO_PROJECT` /
`AGENT_NOT_FOUND` envelopes used elsewhere. Options: validate against
`org list` client-side before the request, normalize the server's response
to a 404 with a JSON envelope, or at least translate both into a single
`ORG_NOT_FOUND` code in the CLI.

## Unknown subcommands silently print help

`./amctl agent foo`, `./amctl project foo`, `./amctl context foo`,
`./amctl context org foo`, and `./amctl context instance foo` all print their
parent's help text with no error line and exit 0. Cobra's default behavior
is to error on unknown subcommands; something is overriding that. A typo
and an intentional `--help` are indistinguishable from the output, and
exit-code-only detection is fragile. This affects every group command in
the CLI.

## `project create` response omits the UUID

`project create` returns the new project with `"uuid": ""`, but `project
list`/`project get` show the same record with a populated UUID. Automation
that wants to act on the freshly-created project must do a follow-up
round trip. Either populate the UUID in the create response, or document
the omission and add a `--wait`-style flag.

## Same-name project creation returns 500

`project create <existing-name>` returns
`INTERNAL_ERROR (500): Failed to create project` instead of a `409
CONFLICT` style envelope (e.g. `PROJECT_ALREADY_EXISTS`). Conflicts are
expected, predictable failures and shouldn't surface as internal errors.

## Resource-not-found error codes diverge between resources

Same conceptual mistake produces different shapes:

- `agent get <uuid>` -> `404 AGENT_NOT_FOUND` (clean)
- `project get <uuid>` -> `500 INTERNAL_ERROR: Failed to get project`
- `project get <bogus-name>` -> `500 INTERNAL_ERROR`
- `agent delete -y <nonexistent>` -> `{"deleted": true}` (false success,
  filed separately)
- `project delete -y <nonexistent>` -> `500 INTERNAL_ERROR`

There should be a single `*_NOT_FOUND` 404 envelope per resource, used
consistently by `get` and `delete`.

## `project delete` reports synchronous success for an async operation

`project delete -y <name>` returns `{"deleted": true}` immediately, but
the project remains visible in `project list` for ~1-2 seconds afterward
(eventual consistency on the server side). Either the CLI should poll
until the deletion is observable, or the response should signal an
in-progress state (e.g. `"status": "Terminating"`) instead of claiming
the operation is complete.
