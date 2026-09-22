"""Check what the card promises, without OpenAI: ``python smoke_test.py``.

The model is stubbed and the agent is driven in-process over ASGI, so this needs
no API key and no network. It asserts the things a caller relies on: which skill
a call reaches, what artifact comes back and under which name and media type,
how a streaming call arrives, and what a caller sees when they ask for a skill
this agent does not have.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
from types import SimpleNamespace
from typing import Any, AsyncIterator

# Before app.py's load_dotenv, so a developer's own key cannot turn the stubbed
# model into a live one.
os.environ.setdefault("OPENAI_API_KEY", "stubbed-by-smoke-test")

import httpx

import agent
from app import app

NOTES = (
    "Standup: the gateway rollout is on track. Priya will cut the release branch "
    "on Thursday. Sam is blocked on the staging certificate."
)
SUMMARY = (
    "The gateway rollout is on track for this week. Priya will cut the release "
    "branch on Thursday. Sam is blocked on the staging certificate and needs "
    "someone from platform to look at it before the branch is cut."
)
ACTION_ITEMS = {
    "action_items": [
        {"owner": "Priya", "task": "cut the release branch", "due": "Thursday"},
        {"owner": "Sam", "task": "unblock the staging certificate", "due": ""},
    ]
}

ARTIFACTS = {
    agent.SKILL_SUMMARIZE: agent.SUMMARY_ARTIFACT,
    agent.SKILL_ACTION_ITEMS: agent.ACTION_ITEMS_ARTIFACT,
}


def text_deltas(text: str, piece_chars: int = 5) -> list[str]:
    """Cut ``text`` into model-shaped deltas, most of them mid-word."""
    deltas = [word + " " for word in text.split(" ")]
    deltas[-1] = deltas[-1].rstrip()
    return [piece for delta in deltas for piece in _cut(delta, piece_chars)]


def _cut(text: str, size: int) -> list[str]:
    return [text[i : i + size] for i in range(0, len(text), size)]


class StubModel:
    """Just enough of ``AsyncOpenAI`` for the agent's two calls."""

    def __init__(self, summary: str, action_items: dict[str, Any]) -> None:
        self.summary = summary
        self.action_items = action_items
        self.chat = SimpleNamespace(completions=self)

    async def create(self, **kwargs: Any) -> Any:
        if kwargs.get("stream"):
            return self._stream()
        return _completion(json.dumps(self.action_items))

    async def _stream(self) -> AsyncIterator[Any]:
        for delta in text_deltas(self.summary):
            yield _chunk(delta)


def _chunk(content: str) -> Any:
    return SimpleNamespace(choices=[SimpleNamespace(delta=SimpleNamespace(content=content))])


def _completion(content: str) -> Any:
    return SimpleNamespace(choices=[SimpleNamespace(message=SimpleNamespace(content=content))])


failures: list[str] = []


def check(name: str, ok: bool, detail: Any = "") -> None:
    print(f"  {'ok  ' if ok else 'FAIL'} {name}")
    if not ok:
        print(f"       {detail}")
        failures.append(name)


def message(metadata: dict[str, Any] | None, notes: str = NOTES) -> dict[str, Any]:
    body: dict[str, Any] = {
        "messageId": "smoke-1",
        "role": "ROLE_USER",
        "parts": [{"text": notes}],
    }
    if metadata is not None:
        body["metadata"] = metadata
    return body


async def send(
    client: httpx.AsyncClient,
    path: str,
    message_body: dict[str, Any],
    request_metadata: dict[str, Any] | None = None,
) -> dict[str, Any]:
    params: dict[str, Any] = {"message": message_body}
    if request_metadata is not None:
        params["metadata"] = request_metadata
    if path == "/rpc":
        payload: dict[str, Any] = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "SendMessage",
            "params": params,
        }
    else:
        payload = params
    response = await client.post(
        path, json=payload, headers={"A2A-Version": "1.0", "Content-Type": "application/json"}
    )
    body = response.json()
    assert response.status_code == 200, f"{response.status_code}: {body}"
    if "error" in body:
        raise AssertionError(f"{path} returned {body['error']}")
    # JSON-RPC wraps the task in "result"; the HTTP+JSON binding returns it bare.
    result = body["result"] if "result" in body else body
    return result.get("task", result)


def artifact(task: dict[str, Any], name: str) -> dict[str, Any]:
    matches = [a for a in task.get("artifacts", []) if a.get("name") == name]
    return matches[0] if matches else {}


def artifact_text(item: dict[str, Any]) -> str:
    return "".join(part.get("text", "") for part in item.get("parts", []))


def status_text(task: dict[str, Any]) -> str:
    parts = task.get("status", {}).get("message", {}).get("parts", [])
    return " ".join(part.get("text", "") for part in parts)


async def card_skills(client: httpx.AsyncClient) -> dict[str, Any]:
    print("agent card")
    card = (await client.get("/.well-known/agent-card.json")).json()
    skills = {skill["id"]: skill for skill in card["skills"]}
    check("advertises both skills", set(skills) == set(agent.SKILL_IDS), sorted(skills))
    for skill, modes in (
        (agent.SKILL_SUMMARIZE, ["text/plain"]),
        (agent.SKILL_ACTION_ITEMS, ["application/json"]),
    ):
        check(
            f"{skill} advertises {modes[0]}",
            skills[skill]["outputModes"] == modes,
            skills[skill].get("outputModes"),
        )
    return skills


async def check_card_examples(client: httpx.AsyncClient, skills: dict[str, Any]) -> None:
    print("the card's own examples")
    for skill_id, skill in skills.items():
        for example in skill["examples"]:
            task = await send(client, "/rpc", message(None, notes=example))
            check(
                f"an example of {skill_id} reaches {skill_id}",
                [a["name"] for a in task.get("artifacts", [])] == [ARTIFACTS[skill_id]],
                {"example": example[:60], "artifacts": [a["name"] for a in task.get("artifacts", [])]},
            )


async def check_summarize(client: httpx.AsyncClient) -> None:
    print("summarize-notes: no skill named")
    task = await send(client, "/rpc", message(None))
    summary = artifact(task, agent.SUMMARY_ARTIFACT)
    check("task completed", task["status"]["state"] == "TASK_STATE_COMPLETED", task["status"])
    check(
        "one summary.txt artifact",
        [a["name"] for a in task.get("artifacts", [])] == [agent.SUMMARY_ARTIFACT],
        [a["name"] for a in task.get("artifacts", [])],
    )
    check(
        "carries the model's summary as text/plain",
        artifact_text(summary) == SUMMARY
        and {part.get("mediaType") for part in summary.get("parts", [])} == {"text/plain"},
        summary.get("parts"),
    )
    check(
        "artifact is labelled with its skill",
        summary.get("metadata", {}).get("skill") == agent.SKILL_SUMMARIZE,
        summary.get("metadata"),
    )
    check(
        "status names the artifact and the skill",
        agent.SUMMARY_ARTIFACT in status_text(task)
        and agent.SKILL_SUMMARIZE in status_text(task),
        status_text(task),
    )


async def check_action_items(client: httpx.AsyncClient) -> None:
    print("extract-action-items: named in metadata")
    cases = {
        "message metadata": ("/rpc", message({"skill": agent.SKILL_ACTION_ITEMS}), None),
        "request metadata": ("/rpc", message(None), {"skill": agent.SKILL_ACTION_ITEMS}),
        "http+json, message metadata": (
            "/rest/message:send",
            message({"skill": agent.SKILL_ACTION_ITEMS}),
            None,
        ),
    }
    for label, (path, body, request_metadata) in cases.items():
        task = await send(client, path, body, request_metadata)
        items = artifact(task, agent.ACTION_ITEMS_ARTIFACT)
        part = (items.get("parts") or [{}])[0]
        check(
            f"{label}: action-items.json holds the JSON data part",
            task["status"]["state"] == "TASK_STATE_COMPLETED"
            and part.get("data") == ACTION_ITEMS
            and part.get("mediaType") == "application/json",
            {"state": task["status"]["state"], "part": part},
        )
        check(
            f"{label}: no summary artifact",
            [a["name"] for a in task.get("artifacts", [])] == [agent.ACTION_ITEMS_ARTIFACT],
            [a["name"] for a in task.get("artifacts", [])],
        )
        check(
            f"{label}: status names the artifact and the skill",
            agent.ACTION_ITEMS_ARTIFACT in status_text(task)
            and agent.SKILL_ACTION_ITEMS in status_text(task),
            status_text(task),
        )


async def check_bad_input(client: httpx.AsyncClient) -> None:
    print("input the agent cannot honour")
    task = await send(client, "/rpc", message({"skill": "book-a-room"}))
    check(
        "unknown skill is rejected, not summarized",
        task["status"]["state"] == "TASK_STATE_REJECTED"
        and not task.get("artifacts")
        and "book-a-room" in status_text(task)
        and all(skill in status_text(task) for skill in agent.SKILL_IDS),
        {"state": task["status"]["state"], "status": status_text(task)},
    )
    task = await send(client, "/rpc", message(None, notes="   "))
    check(
        "notes-free message is rejected",
        task["status"]["state"] == "TASK_STATE_REJECTED",
        task["status"],
    )


async def check_streaming(client: httpx.AsyncClient) -> None:
    print("streaming: SendStreamingMessage")
    payload = {
        "jsonrpc": "2.0",
        "id": 2,
        "method": "SendStreamingMessage",
        "params": {"message": message(None)},
    }
    chunks: list[str] = []
    async with client.stream(
        "POST", "/rpc", json=payload, headers={"A2A-Version": "1.0"}
    ) as response:
        async for line in response.aiter_lines():
            if not line.startswith("data:"):
                continue
            event = json.loads(line[len("data:") :].strip())["result"]
            update = event.get("artifactUpdate", {}).get("artifact", {})
            if update.get("name") == agent.SUMMARY_ARTIFACT:
                chunks += [p.get("text", "") for p in update.get("parts", [])]
    check("text arrives in more than one chunk", len(chunks) > 1, chunks)
    check("the chunks reassemble into the summary", "".join(chunks).strip() == SUMMARY, chunks)
    check(
        "chunks arrive in roughly threshold-sized pieces",
        all(len(chunk) >= agent.STREAM_CHUNK_CHARS // 2 for chunk in chunks[:-1]),
        [len(chunk) for chunk in chunks],
    )
    check(
        "no chunk splits a word",
        all(chunk.endswith((" ", "\n")) for chunk in chunks[:-1])
        and all(not chunk.startswith(" ") for chunk in chunks),
        chunks,
    )


async def main() -> None:
    agent.client = lambda: StubModel(SUMMARY, ACTION_ITEMS)  # type: ignore[assignment]
    transport = httpx.ASGITransport(app=app)
    async with httpx.AsyncClient(transport=transport, base_url="http://agent") as client:
        skills = await card_skills(client)
        await check_card_examples(client, skills)
        await check_summarize(client)
        await check_action_items(client)
        await check_bad_input(client)
        await check_streaming(client)

    if failures:
        print(f"\n{len(failures)} check(s) failed: {', '.join(failures)}")
        sys.exit(1)
    print("\nall checks passed")


if __name__ == "__main__":
    asyncio.run(main())
