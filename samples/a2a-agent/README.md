# A2A Notes Agent - Deployment Guide

## Overview

An agent that speaks the [A2A protocol](https://a2a-protocol.org/) and is deployed
through AMP as an **A2A Agent**. Give it meeting notes and it does one of two
things, both backed by OpenAI:

| Skill | What it does | Result |
|---|---|---|
| `summarize-notes` | Condenses the notes into at most three sentences | `summary.txt`, a `text/plain` artifact, streamed as it is written |
| `extract-action-items` | Pulls out who owes what, and when | `action-items.json`, an `application/json` data artifact |

It is built on the official [`a2a-sdk`](https://github.com/a2aproject/a2a-python)
and FastAPI. The point of the sample is the protocol surface: an agent card, both
of the transports AMP publishes, the task lifecycle, and streaming.

## Choosing a skill

A2A has no field for picking one skill over another, so the sample reads the
caller's choice in this order, and the card says so:

1. a `skill` key in the **message metadata** (`message.metadata`) or in the
   **request metadata** (`SendMessageRequest.metadata`) — whichever the client
   filled in;
2. failing that, the directive the card's own examples open with: `Summarize:`
   or `Extract action items:`;
3. failing that, `summarize-notes`.

A `skill` id the agent does not have is **rejected**, with the ids it does have
in the status message. That matters more than it looks: a caller who asks for
`extract-action-items` must never be handed a summary, so the agent refuses
rather than falling back to something plausible. The completed status names the
skill that ran and the artifact it wrote, and each artifact carries
`metadata.skill`, so which skill answered is visible in the result itself.

That last point is the only way to tell, if something in the path drops the
metadata: a proxy that strips it leaves the agent nothing to route on, and the
call falls to the text directive or to a summary. The result says which skill
ran, so read the status rather than assuming the one you asked for was reached.

## What this demonstrates

An A2A agent is not called like a chat agent. There is no `/chat` endpoint, and
the Console's chat view has nothing to call here. A client resolves the agent
card, picks a transport, and sends messages; the agent answers with a task.

```text
client                                                 agent
  |  GET /.well-known/agent-card.json                     |
  |------------------------------------------------------>|  name, interfaces, skills
  |                                                       |
  |  POST /rpc          SendMessage                       |
  |  POST /rest/message:send                              |
  |------------------------------------------------------>|  Task: submitted -> working
  |                                                       |        -> completed
  |<------------------------------------------------------|  artifacts: summary.txt /
  |                                                       |             action-items.json
  |  POST /rpc          GetTask  /  CancelTask            |
  |------------------------------------------------------>|
```

What that means in code:

- **The card** (`/.well-known/agent-card.json`) declares both interfaces at
  `protocolBinding: JSONRPC` under `/rpc` and `protocolBinding: HTTP+JSON` under
  `/rest`, plus the two skills, each with the media type its result comes back in
  and how to select it. These are the paths AMP's gateway publishes an A2A agent
  under, and it rewrites the card it serves so clients dial the gateway rather
  than the agent.
- **One process serves both transports** (`app.py`), over one request handler and
  one task store, so a task sent over JSON-RPC is readable over HTTP+JSON.
- **Streaming works for free** (`SendStreamingMessage`, `/message:stream`): the
  executor publishes artifact chunks, and the SDK renders them either as an SSE
  stream or as one merged artifact, depending on how the client asked. Chunks are
  cut at word boundaries, so a client rendering them as they arrive never gets
  half a word. This is the traffic AMP's gateway deliberately leaves its route
  timeout off for.
- **The task lifecycle is explicit**: `submitted -> working -> completed`, or
  `rejected` when the message carries no notes or names a skill the agent does
  not have, and `failed` when the model call fails. Task state is readable by id
  afterwards.

## Prerequisites

- Python 3.10 to 3.13. The deployment steps below use 3.11.
- An OpenAI API key. Both skills call the model.

## Deploy it through AMP

### Step 1: Add the agent

1. Open the **Default** project.
2. Click **Add Agent** and select the **Platform-Hosted Agent** card.

### Step 2: Fill in the agent details

| Field | Value |
|---|---|
| **Display Name** | `A2A Notes Agent` |
| **Description** | `Summarizes meeting notes and extracts action items over A2A` |
| **GitHub Repository** | `https://github.com/wso2/agent-manager` |
| **Branch** | `main` |
| **App Path** | `samples/a2a-agent` |
| **Language** | `Python` |
| **Language Version** | `3.11` |
| **Start Command** | `python main.py` |
| **Port** | `9099` |
| **Enable auto instrumentation** | **Off** — see the note below |

Auto instrumentation has to be off for this agent. AMP's instrumentation init
container injects its own Python packages onto `PYTHONPATH` and ships
`protobuf` 7.x, while the A2A SDK requires `protobuf<7` and reads
`FieldDescriptor.label`, which protobuf 7 removed. With instrumentation on,
every A2A message fails with JSON-RPC `-32603` and this in the agent's logs:

```text
a2a/utils/proto_utils.py:217: AttributeError: 'google._upb._message.FieldDescriptor' object has no attribute 'label'
```

Turning it off leaves the agent on the protobuf its own build installed. The
cost is that the OpenAI calls no longer emit traces.

### Step 3: Select the agent interface

Choose **A2A Agent**. An A2A agent needs a port and nothing else: it serves its
own agent card, so there is no OpenAPI document and no base path to give it.

The gateway then publishes both A2A transports for it — JSON-RPC under
`/<agent-name>/rpc` and HTTP+JSON under `/<agent-name>/rest` — and serves the
agent's card at the well-known path.

### Step 4: Configure environment variables

| Key | Value |
|---|---|
| `OPENAI_API_KEY` | your OpenAI key (mark it **sensitive**) |

Optional, with defaults:

| Key | Default | Purpose |
|---|---|---|
| `OPENAI_MODEL` | `gpt-4o-mini` | Model used by both skills |
| `AGENT_PUBLIC_BASE_URL` | `http://localhost:9099` | The address the agent advertises in its own card. The gateway rewrites the card it serves, so leave this unset for deployed agents. |

### Step 5: Deploy

Review the configuration, click **Deploy**, and wait for the build to finish.

## Deploy it with amctl

The same deployment without the console. `amctl` needs a build that knows the
`a2a-agent` subtype — check `amctl agent create --help` says so before you start.

```bash
amctl agent create a2a-notes-agent \
  --display-name "A2A Notes Agent" \
  --subtype a2a-agent \
  --port 9099 \
  --repo-url https://github.com/wso2/agent-manager \
  --repo-branch main \
  --repo-path /samples/a2a-agent \
  --build-type buildpack \
  --language python \
  --language-version 3.11 \
  --run-command "python main.py" \
  --env-secret OPENAI_API_KEY=<your-openai-key> \
  --no-auto-instrumentation

amctl agent build create a2a-notes-agent     # takes a few minutes
amctl agent deploy a2a-notes-agent           # deploys the newest build
amctl agent status a2a-notes-agent           # waits out in-progress -> active
```

`amctl agent status` prints the agent's gateway URL. That is the base the
transports hang off:

```text
http://<gateway-host>/<agent-name>          card and context
http://<gateway-host>/<agent-name>/rpc      JSON-RPC transport
http://<gateway-host>/<agent-name>/rest     HTTP+JSON transport
```

## Call the deployed agent

Calls go to the gateway, not to the agent: `<gateway-url>/<agent-name>/rpc` for
JSON-RPC, `<gateway-url>/<agent-name>/rest/...` for HTTP+JSON. If you turned on
API key security for the agent, send its key in the `X-API-Key` header.

The card the gateway serves at the well-known path is the authority on those
URLs — it is the agent's card with its addresses rewritten to the gateway's —
so resolve it first when you are unsure how a deployment is exposed.

```bash
curl -X POST "https://<gateway-url>/<agent-name>/rpc" \
  -H "Content-Type: application/json" \
  -H "A2A-Version: 1.0" \
  -H "X-API-Key: <agent-api-key>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "SendMessage",
    "params": {
      "message": {
        "messageId": "msg-1",
        "role": "ROLE_USER",
        "parts": [{"text": "Standup: the gateway rollout is on track. Priya will cut the release branch on Thursday. Sam is blocked on the staging certificate."}]
      }
    }
  }'
```

The reply is a `Task` in a terminal state carrying the `summary.txt` artifact,
and its status message says so:

```json
"status": {"state": "TASK_STATE_COMPLETED", "message": {"parts": [
  {"text": "summary.txt is ready (summarize-notes, 214 characters)."}]}}
```

Ask for the other skill by naming it in the message metadata:

```json
{
  "message": {
    "messageId": "msg-2",
    "role": "ROLE_USER",
    "metadata": {"skill": "extract-action-items"},
    "parts": [{"text": "...the notes..."}]
  }
}
```

An unknown skill id comes back as a task in the `rejected` state, with the ids
this agent does have in the status message - not as a summary:

```json
"status": {"state": "TASK_STATE_REJECTED", "message": {"parts": [
  {"text": "There is no 'book-a-room' skill. This agent offers: summarize-notes, extract-action-items."}]}}
```

Any A2A client works too — see [Call it from an A2A client](#call-it-from-an-a2a-client).

## Run it locally

```bash
cd samples/a2a-agent
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt

export OPENAI_API_KEY="<your-openai-key>"
python main.py          # serves on http://localhost:9099
```

The agent card:

```bash
curl -s http://localhost:9099/.well-known/agent-card.json
```

Summarize notes over JSON-RPC:

```bash
curl -s -X POST http://localhost:9099/rpc \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "SendMessage",
    "params": {
      "message": {
        "messageId": "msg-1",
        "role": "ROLE_USER",
        "parts": [{"text": "Standup: Priya cuts the release branch Thursday; Sam is blocked on the staging certificate."}]
      }
    }
  }'
```

Extract action items over HTTP+JSON, choosing the skill through metadata:

```bash
curl -s -X POST http://localhost:9099/rest/message:send \
  -H "Content-Type: application/json" \
  -d '{
    "message": {
      "messageId": "msg-2",
      "role": "ROLE_USER",
      "metadata": {"skill": "extract-action-items"},
      "parts": [{"text": "Standup: Priya cuts the release branch Thursday; Sam is blocked on the staging certificate."}]
    }
  }'
```

Stream it instead, with `-N` so `curl` does not buffer:

```bash
curl -s -N -X POST http://localhost:9099/rpc \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "SendStreamingMessage",
    "params": {"message": {"messageId": "msg-3", "role": "ROLE_USER",
      "parts": [{"text": "Standup: Priya cuts the release branch Thursday."}]}}
  }'
```

Each event is a line of SSE: first the `Task`, then a `working` status update,
then the summary arriving as artifact chunks cut at word boundaries, then
`completed`.

The two examples the card carries work verbatim, because each opens with the
skill it advertises: sending `Extract action items: ...the notes...` reaches the
JSON skill with no metadata at all.

## Check it without OpenAI

```bash
python smoke_test.py
```

`smoke_test.py` stubs the model and drives the agent in-process over ASGI, so it
needs neither a key nor a network. It asserts what the card promises: which skill
each spelling of a call reaches, the artifact name and media type each skill
produces, the chunking of a streaming call, and that an unknown skill is rejected
instead of answered with a summary. No test framework to install — a non-zero
exit code means a check failed.

## Call it from an A2A client

The same protocol, without hand-written JSON. This is the `a2a-sdk` client
talking to a locally running sample — point the URL at the gateway and add an
`X-API-Key` header to call a deployed agent instead.

```python
import asyncio

from a2a.client import ClientConfig, create_client
from a2a.types.a2a_pb2 import Message, Part, Role, SendMessageRequest


async def main() -> None:
    # Resolves /.well-known/agent-card.json, then picks a transport from the card.
    # ClientConfig(supported_protocol_bindings=["HTTP+JSON"]) forces the other one.
    client = await create_client("http://localhost:9099", ClientConfig())
    message = Message(
        message_id="client-1",
        role=Role.ROLE_USER,
        parts=[Part(text="...notes...")],
    )
    message.metadata.update({"skill": "extract-action-items"})
    async for event in client.send_message(SendMessageRequest(message=message)):
        print(event)


asyncio.run(main())
```

## Notes

- **`A2A-Version: 1.0` is defaulted, not required.** A request without the header
  is read as A2A 0.3 by the SDK, which this agent does not serve. Clients that
  talk to the agent directly may omit it, and a proxy in front of the agent may
  drop it, so `app.py` stamps the version the agent serves when a request
  arrives without one.
- **`a2a-sdk` is pinned to `1.1.2`.** The sample is written against that
  generation of the SDK's routing helpers (`create_jsonrpc_routes`,
  `create_rest_routes`, `create_agent_card_routes`).
- **Tasks live in memory.** `InMemoryTaskStore` means a restart forgets task
  history. Swap in the SDK's database task store if you need tasks to survive;
  the sample is about the protocol, not about durable task storage.
- **Skill selection is a convention, not protocol.** A2A 1.0 has no field for
  choosing between an agent's skills, and no standard metadata key either, so
  this sample defines one (`skill`) and reads it from either metadata field. A
  client that does not know the convention can still select a skill by opening
  its text the way the card's examples do. If your own agent has one job, drop
  the convention and treat every message alike.
- **Push notifications are declared off** in the card, so the four
  `*PushNotificationConfig` operations answer `FAILED_PRECONDITION` rather than
  pretending to store a webhook. The agent publishes **no extended card**
  either: `extendedAgentCard` is false, and the gateway only serves an extended
  card to a caller whose policy chain authenticated the request.
- **Observability is off for this agent.** Because auto instrumentation has to be
  disabled (see Step 2), the OpenAI calls do not emit traces. Point
  `amp-instrumentation` at the exporter yourself if you want them — see the
  `manual-instrumentation-agent` sample for that path.
- **The card the gateway serves is the agent's own body.** As of this writing the
  gateway's passthrough rewrite does not touch A2A 1.0 `supportedInterfaces`
  URLs, so a card fetched through the gateway still advertises the agent's own
  address. Use the gateway paths above (`/<agent-name>/rpc`, `/rest`) when you
  wire up a client, or resolve the card and rewrite the URL yourself.

## File guide

| File | Role |
|---|---|
| `agent.py` | The agent: the OpenAI calls, the two skills, and the task events they publish |
| `app.py` | The A2A server: agent card, JSON-RPC and HTTP+JSON routes, one handler and task store behind both |
| `main.py` | Local runner (`python main.py`), and the deployed start command |
| `smoke_test.py` | Drives the agent in-process with a stubbed model: skill selection, artifacts, streaming, rejections |
| `requirements.txt` | `a2a-sdk[fastapi]`, `openai`, `uvicorn`, `python-dotenv` |
| `.env.example` | Environment variable template for local runs |
