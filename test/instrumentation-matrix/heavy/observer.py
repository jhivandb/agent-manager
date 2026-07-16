"""Trace-observer query helpers for the heavy-tier driver.

After an agent is invoked, its spans land in agent-manager-observer. We
fetch them in three steps, mirroring the Go e2e suite
(`test/e2e/operations/trace/`):

  1. list traces for the agent      GET /api/v1/traces?organization=&project=&agent=&environment=&startTime=&endTime=
  2. list span summaries per trace  GET /api/v1/traces/{traceId}/spans?...
  3. fetch each span's detail        GET /api/v1/traces/{traceId}/spans/{spanId}

Step 3 returns `opensearch.Span` (agent-manager-observer/opensearch/types.go),
which carries `name`, `kind`, `attributes` (raw gen_ai.* map), `resource`,
and the trace/span/parent ids — exactly the shape the emission-tier
ContractValidator + classify_span already consume, so no separate heavy
contract is needed.

FIRST-RUN-TUNABLE: the step-2 summaries endpoint takes `namespace` /
`component` query params in the e2e client, whose mapping from
(org, project, agent) isn't certain without a live observer. We send the
organization/project/agent names *and* best-effort namespace/component so
the call has the widest chance of resolving; confirm + tighten on the first
real heavy run. The observer is authenticated (Bearer token), same as AMP.
"""
from __future__ import annotations

import time
from datetime import datetime, timedelta, timezone

import requests

from heavy.amp_client import AmpClient, DeployedAgent

_HTTP_TIMEOUT_S = 30
_POLL_S = 10
# Spans index incrementally: a chat turn's chain/embedding spans land before
# the llm completion span. Don't return on the first non-empty fetch — wait
# until the span count holds steady across this many consecutive polls so
# late-arriving spans aren't missed.
_SETTLE_POLLS = 2


def _rfc3339(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def poll_traces(
    client: AmpClient,
    deployed: DeployedAgent,
    observer_base_url: str,
    timeout_s: int = 120,
) -> list[dict]:
    """Block until the observer has spans for the deployed agent, then return
    them as a flat list of span dicts. Empty list after the window means
    "no spans captured" — the driver maps that to NO_SPANS_CAPTURED.

    The window is anchored to invocation time (the driver calls this right
    after /chat returns), widened ±5m to absorb clock skew + indexing lag,
    matching the e2e WaitForTraces behaviour.
    """
    now = datetime.now(timezone.utc)
    start_time = _rfc3339(now - timedelta(minutes=5))
    end_time = _rfc3339(now + timedelta(minutes=5))
    base = observer_base_url.rstrip("/")
    session = requests.Session()

    deadline = time.monotonic() + timeout_s
    got_clean_response = False
    last_error: str | None = None
    best_spans: list[dict] = []
    last_count = -1
    stable_polls = 0
    while True:
        trace_ids, err = _list_trace_ids(session, client, base, deployed, start_time, end_time)
        if err is None:
            got_clean_response = True
        else:
            last_error = err

        spans: list[dict] = []
        for trace_id in trace_ids:
            for span_id in _list_span_ids(
                session, client, base, deployed, trace_id, start_time, end_time
            ):
                detail = _get_span_detail(session, client, base, trace_id, span_id)
                if detail is not None:
                    spans.append(_to_validator_span(detail))

        if spans:
            best_spans = spans
            # Settled once the count holds steady across consecutive polls —
            # this lets the late llm span join the early chain/embedding ones.
            if len(spans) == last_count:
                stable_polls += 1
                if stable_polls >= _SETTLE_POLLS:
                    return spans
            else:
                stable_polls = 0
            last_count = len(spans)

        # Stop before a sleep would overrun the deadline.
        if time.monotonic() + _POLL_S >= deadline:
            break
        time.sleep(_POLL_S)

    if best_spans:
        return best_spans
    # Distinguish "observer answered 200 but no traces" (genuinely empty → the
    # driver maps [] to no-spans-captured) from "every list call errored"
    # (transport/HTTP problem → raise so the driver records a pipeline-error,
    # not a misleading no-spans-captured).
    if not got_clean_response:
        raise RuntimeError(
            f"trace listing never succeeded within {timeout_s}s "
            f"(last error: {last_error})"
        )
    return []


def _auth_get(session, client: AmpClient, url: str, params: dict | None = None):
    return session.get(
        url,
        params=params,
        headers={"Authorization": f"Bearer {client.access_token()}"},
        timeout=_HTTP_TIMEOUT_S,
    )


def _list_trace_ids(session, client, base, deployed, start_time, end_time):
    """Returns (trace_ids, error). error is None on a clean 200; a short
    string on transport/HTTP failure so the caller can tell "empty" from
    "broken"."""
    try:
        resp = _auth_get(
            session, client, f"{base}/api/v1/traces",
            params={
                "organization": deployed.org,
                "project": deployed.project_name,
                "agent": deployed.agent_name,
                "environment": deployed.environment,
                "startTime": start_time,
                "endTime": end_time,
                "limit": 50,
                "sortOrder": "desc",
            },
        )
    except requests.RequestException as e:
        return [], f"request failed: {e}"
    if resp.status_code != 200:
        return [], f"status {resp.status_code}: {resp.text[:200]}"
    return [t["traceId"] for t in resp.json().get("traces", [])], None


def _list_span_ids(session, client, base, deployed, trace_id, start_time, end_time) -> list[str]:
    resp = _auth_get(
        session, client, f"{base}/api/v1/traces/{trace_id}/spans",
        params={
            # Proven list-traces names …
            "organization": deployed.org,
            "project": deployed.project_name,
            "agent": deployed.agent_name,
            "environment": deployed.environment,
            # … plus the e2e summaries-endpoint names (mapping TBD on first run).
            "namespace": deployed.project_name,
            "component": deployed.agent_name,
            "startTime": start_time,
            "endTime": end_time,
        },
    )
    if resp.status_code != 200:
        return []
    return [s["spanId"] for s in resp.json().get("spans", [])]


def _get_span_detail(session, client, base, trace_id, span_id) -> dict | None:
    resp = _auth_get(
        session, client, f"{base}/api/v1/traces/{trace_id}/spans/{span_id}"
    )
    if resp.status_code != 200:
        return None
    return resp.json()


def _to_validator_span(detail: dict) -> dict:
    """Coerce an observer span-detail (opensearch.Span JSON) into the dict
    shape the ContractValidator + classify_span expect."""
    return {
        "name": detail.get("name", ""),
        "kind": detail.get("kind", ""),
        "attributes": detail.get("attributes", {}) or {},
        "traceId": detail.get("traceId", ""),
        "spanId": detail.get("spanId", ""),
        "parentSpanId": detail.get("parentSpanId"),
        "resource": detail.get("resource", {}) or {},
    }
