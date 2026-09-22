"""The agent behind the sample: an OpenAI-backed A2A ``AgentExecutor``.

Nothing here is A2A-server plumbing. The A2A SDK's request handler calls
``execute()`` once per inbound ``SendMessage`` / ``SendStreamingMessage``, and
the executor's job is to publish task events. The events are the same either
way: the SDK renders them as a single ``Task`` for a blocking call, or as an
SSE stream for a streaming one.

Two skills are implemented, and which one runs is the caller's choice:

  * ``summarize-notes`` - streams a short summary as a text artifact.
  * ``extract-action-items`` - returns action items as a JSON data artifact.

A2A has no field for choosing a skill, so this sample reads the caller's choice
from whichever metadata field they filled in - the message's or the request's -
and falls back to the directive the card's examples open with (``Summarize:``,
``Extract action items:``). A skill id the agent does not have is rejected, with
the ids it does have: a caller who asks for action items must never silently get
a summary instead.
"""

from __future__ import annotations

import json
import logging
import os
import uuid
from typing import Any, Awaitable, Callable

from a2a.helpers import new_data_part, new_task_from_user_message, new_text_part
from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.events import EventQueue
from a2a.server.tasks import TaskUpdater
from google.protobuf.json_format import MessageToDict
from openai import AsyncOpenAI

log = logging.getLogger("a2a-agent")

SKILL_SUMMARIZE = "summarize-notes"
SKILL_ACTION_ITEMS = "extract-action-items"
SKILL_IDS = (SKILL_SUMMARIZE, SKILL_ACTION_ITEMS)

SKILL_METADATA_KEY = "skill"

# The card's examples open with the skill's own name, so a client that replays
# an example - or a person who pastes one into a generic A2A client - gets the
# skill that example advertises without knowing about the metadata key.
SKILL_OPENERS = (
    ("extract action items", SKILL_ACTION_ITEMS),
    ("summarize", SKILL_SUMMARIZE),
)

SUMMARY_ARTIFACT = "summary.txt"
ACTION_ITEMS_ARTIFACT = "action-items.json"

DEFAULT_MODEL = "gpt-4o-mini"

# A streaming summary is flushed to the task every this many characters, so a
# client sees the text arrive in pieces instead of in one lump at the end.
STREAM_CHUNK_CHARS = 120

SUMMARY_PROMPT = (
    "You summarize meeting notes. Write a short summary (at most three "
    "sentences) of the notes the user provides. Reply with the summary only."
)

ACTION_ITEMS_PROMPT = (
    "You extract action items from meeting notes. Reply with JSON only, in "
    'exactly this shape: {"action_items": [{"owner": "...", "task": "...", '
    '"due": "..."}]}. Use an empty string for anything the notes do not say. '
    "Return an empty list when the notes contain no action items."
)

_client: AsyncOpenAI | None = None


def client() -> AsyncOpenAI:
    """The OpenAI client, created on first use.

    Reads ``OPENAI_API_KEY`` from the environment, which the platform injects
    as a secret when the agent is deployed through AMP.
    """
    global _client
    if _client is None:
        if not os.getenv("OPENAI_API_KEY"):
            raise RuntimeError("OPENAI_API_KEY is required to run this sample.")
        _client = AsyncOpenAI()
    return _client


def model() -> str:
    return os.getenv("OPENAI_MODEL") or DEFAULT_MODEL


def last_space(text: str) -> int:
    """Index of the last space, tab or newline in ``text``, or -1 if there is none."""
    return max(text.rfind(char) for char in " \t\n")


async def stream_summary(notes: str, on_chunk: Callable[[str], Awaitable[None]]) -> str:
    """Summarize ``notes``, handing each piece of text to ``on_chunk`` as it
    arrives. Returns the complete summary.

    Chunks are cut at a word boundary once they pass ``STREAM_CHUNK_CHARS``, so
    a client rendering them as they arrive never gets half a word.
    """
    summary = ""
    pending = ""
    stream = await client().chat.completions.create(
        model=model(),
        messages=[
            {"role": "system", "content": SUMMARY_PROMPT},
            {"role": "user", "content": notes},
        ],
        temperature=0.2,
        stream=True,
    )
    async for chunk in stream:
        if not chunk.choices:
            continue
        delta = chunk.choices[0].delta.content or ""
        summary += delta
        pending += delta
        boundary = last_space(pending)
        if len(pending) < STREAM_CHUNK_CHARS or boundary <= 0:
            continue
        await on_chunk(pending[: boundary + 1])
        pending = pending[boundary + 1 :]
    if pending.strip():
        await on_chunk(pending.strip())
    return summary.strip()


async def extract_action_items(notes: str) -> dict[str, Any]:
    """Extract action items from ``notes`` as the JSON object the skill promises."""
    response = await client().chat.completions.create(
        model=model(),
        messages=[
            {"role": "system", "content": ACTION_ITEMS_PROMPT},
            {"role": "user", "content": notes},
        ],
        temperature=0,
        response_format={"type": "json_object"},
    )
    content = response.choices[0].message.content or "{}"
    parsed = json.loads(content)
    items = parsed.get("action_items")
    if not isinstance(items, list):
        raise ValueError(f"the model did not return an action_items list: {content}")
    return parsed


class UnknownSkill(ValueError):
    """The caller named a skill this agent does not have."""


def named_skill(context: RequestContext) -> str | None:
    """The skill id the caller named, from whichever metadata field carries it.

    A client can put metadata on the message (``message.metadata``) or on the
    request that wraps it (``SendMessageRequest.metadata``); the SDK hands the
    request's over as ``context.metadata``. Both spellings mean the same thing
    to a caller, so both are read here.
    """
    message = context.message
    if message is not None and message.HasField("metadata"):
        from_message = MessageToDict(message.metadata).get(SKILL_METADATA_KEY)
        if from_message:
            return from_message
    return context.metadata.get(SKILL_METADATA_KEY) or None


def requested_skill(context: RequestContext) -> str:
    """Which skill to run. Raises ``UnknownSkill`` if the caller named one this
    agent does not have.
    """
    named = named_skill(context)
    if named is not None:
        if named not in SKILL_IDS:
            raise UnknownSkill(
                f"There is no {named!r} skill. This agent offers: {', '.join(SKILL_IDS)}."
            )
        return named
    opener = context.get_user_input().strip().lower()
    for prefix, skill in SKILL_OPENERS:
        if opener.startswith(prefix):
            return skill
    return SKILL_SUMMARIZE


class NotesAgentExecutor(AgentExecutor):
    """Runs one task: read the notes, do the requested work, publish the result."""

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        # The agent owns the task: it publishes the Task before touching its
        # status, carrying the caller's message as the first history entry.
        await event_queue.enqueue_event(new_task_from_user_message(context.message))
        updater = TaskUpdater(event_queue, context.task_id, context.context_id)

        notes = context.get_user_input().strip()
        if not notes:
            await updater.reject(
                updater.new_agent_message(
                    [new_text_part("Send the meeting notes as the message text.")]
                )
            )
            return

        try:
            skill = requested_skill(context)
        except UnknownSkill as exc:
            await updater.reject(updater.new_agent_message([new_text_part(str(exc))]))
            return

        await updater.start_work()
        skills = {
            SKILL_SUMMARIZE: self._summarize,
            SKILL_ACTION_ITEMS: self._extract_action_items,
        }
        try:
            await skills[skill](updater, notes)
        except Exception as exc:  # noqa: BLE001 - report any failure on the task
            log.exception("task %s failed", context.task_id)
            await updater.failed(
                updater.new_agent_message(
                    [new_text_part(f"The agent could not finish this task: {exc}")]
                )
            )

    async def _summarize(self, updater: TaskUpdater, notes: str) -> None:
        """Publish the summary as one text artifact, built up from several chunks.

        A streaming client renders the chunks as they arrive; a blocking client
        gets the merged artifact, and both find it under the same name and the
        same ``skill`` metadata, so the two skills' results never look alike.
        """
        artifact_id = str(uuid.uuid4())
        first = True

        async def add_chunk(text: str) -> None:
            nonlocal first
            await updater.add_artifact(
                [new_text_part(text, media_type="text/plain")],
                artifact_id=artifact_id,
                name=SUMMARY_ARTIFACT,
                metadata={"skill": SKILL_SUMMARIZE},
                append=not first,
            )
            first = False

        summary = await stream_summary(notes, add_chunk)
        if first:  # the model returned nothing to stream
            await add_chunk("")
        await updater.complete(
            updater.new_agent_message(
                [
                    new_text_part(
                        f"{SUMMARY_ARTIFACT} is ready "
                        f"({SKILL_SUMMARIZE}, {len(summary)} characters)."
                    )
                ]
            )
        )
        log.info("summarized %d characters into %d", len(notes), len(summary))

    async def _extract_action_items(self, updater: TaskUpdater, notes: str) -> None:
        """Publish the action items as one JSON data artifact."""
        items = await extract_action_items(notes)
        await updater.add_artifact(
            [new_data_part(items, media_type="application/json")],
            name=ACTION_ITEMS_ARTIFACT,
            metadata={"skill": SKILL_ACTION_ITEMS},
            last_chunk=True,
        )
        count = len(items["action_items"])
        await updater.complete(
            updater.new_agent_message(
                [
                    new_text_part(
                        f"{ACTION_ITEMS_ARTIFACT} is ready "
                        f"({SKILL_ACTION_ITEMS}, {count} action item(s))."
                    )
                ]
            )
        )

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        """A cancelled task is marked cancelled; the in-flight model call is
        abandoned, since the notes it was working on are no longer wanted.
        """
        updater = TaskUpdater(event_queue, context.task_id, context.context_id)
        await updater.cancel()
