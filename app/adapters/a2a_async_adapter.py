"""
A2aAsyncAdapter — A2A v1.0 for long-running agents.

Protocol:
  1. POST SendMessage → receive remote_task_id immediately (non-blocking)
  2. Poll GetTask until terminal state (or SSE stream if supports_streaming=True)
  3. Yield AdapterEvents: task_created → status → artifact(s) → done | error

Auth: Authorization: Bearer <decrypted token>
"""

import asyncio
import json
from typing import AsyncGenerator, Optional
from uuid import uuid4

import httpx

from app.adapters.base import AdapterEvent, AgentAdapter
from app.utils.crypto import decrypt_value
from app.utils.logger import logger

# A2A SDK v1.1 returns proto enum names (TASK_STATE_*) in JSON responses.
# Also accept lowercase variants for forward-compatibility.
_TERMINAL = {
    "TASK_STATE_COMPLETED", "TASK_STATE_FAILED",
    "TASK_STATE_CANCELED", "TASK_STATE_REJECTED",
    "completed", "failed", "canceled", "rejected",
}
_INPUT_REQUIRED = {"TASK_STATE_INPUT_REQUIRED", "input-required"}
_ROLE_USER = 1  # lf.a2a.v1.Role.ROLE_USER


class A2aAsyncAdapter(AgentAdapter):
    def __init__(
        self,
        *,
        agent_slug: str,
        endpoint_url: str,
        auth_token_encrypted: str | None,
        context_id: str | None = None,
        push_url: str | None = None,
        supports_streaming: bool = False,
        poll_interval: float = 1.0,
        max_poll_seconds: float = 300.0,
        input_modes: list[str] | None = None,
    ) -> None:
        self._slug = agent_slug
        self._endpoint_url = endpoint_url.rstrip("/") + "/"
        self._auth_token_encrypted = auth_token_encrypted
        self._context_id = context_id
        self._push_url = push_url
        self._supports_streaming = supports_streaming
        self._poll_interval = poll_interval
        self._max_poll_seconds = max_poll_seconds
        self._input_modes = input_modes or ["text/plain"]

    def _headers(self) -> dict:
        token = decrypt_value(self._auth_token_encrypted) if self._auth_token_encrypted else ""
        h: dict = {"Content-Type": "application/json", "A2A-Version": "1.0"}
        if token:
            h["Authorization"] = f"Bearer {token}"
        return h

    def _build_parts(self, input: str | dict) -> list[dict]:
        """
        Build the A2A message parts list based on the agent's declared input_modes.

        - If the agent declares application/json and input is a dict: send a data part.
          A leading text part carries any context summary (keyed as "__context__").
        - Otherwise: send a single text part.
        """
        if "application/json" in self._input_modes and isinstance(input, dict):
            parts: list[dict] = []
            # Context summary is passed as a separate text part, not mixed into data
            ctx = input.pop("__context__", None)
            if ctx:
                parts.append({"text": ctx})
            parts.append({"data": input})
            return parts
        # Plain text fallback — extract message string if input is a dict
        text = input if isinstance(input, str) else input.get("message", str(input))
        return [{"text": text}]

    def _send_message_body(self, input: str | dict) -> dict:
        # role=1 is ROLE_USER in the A2A v1.0 proto enum
        msg: dict = {
            "role": _ROLE_USER,
            "parts": self._build_parts(input),
            "messageId": str(uuid4()),
        }
        if self._context_id:
            msg["contextId"] = self._context_id
        params: dict = {
            "message": msg,
            "configuration": {"returnImmediately": True},
        }
        if self._push_url:
            params["configuration"]["taskPushNotificationConfig"] = {"url": self._push_url}
        return {
            "jsonrpc": "2.0",
            "id": str(uuid4()),
            "method": "SendMessage",
            "params": params,
        }

    def _get_task_body(self, task_id: str) -> dict:
        return {
            "jsonrpc": "2.0",
            "id": str(uuid4()),
            "method": "GetTask",
            "params": {"id": task_id},
        }

    async def submit(self, client: httpx.AsyncClient, input: str | dict) -> str:
        """POST SendMessage and return the remote_task_id."""
        resp = await client.post(
            self._endpoint_url,
            headers=self._headers(),
            json=self._send_message_body(input),
        )
        resp.raise_for_status()
        body = resp.json()

        if "error" in body and body["error"]:
            err = body["error"]
            msg = err.get("message", str(err)) if isinstance(err, dict) else str(err)
            raise RuntimeError(f"A2A SendMessage RPC error: {msg}")

        result = body.get("result", {})
        # SDK v1.1: result is SendMessageResponse with a "task" or "message" field
        task = result.get("task") or result
        task_id = task.get("id")
        if not task_id:
            raise RuntimeError(f"A2A SendMessage returned no task id. result={result}")
        return task_id

    async def _poll_events(
        self,
        client: httpx.AsyncClient,
        remote_task_id: str,
        timeout: float,
    ) -> AsyncGenerator[AdapterEvent, None]:
        """Poll GetTask until terminal state, yielding events along the way."""
        deadline = asyncio.get_event_loop().time() + min(timeout, self._max_poll_seconds)
        last_state: str | None = None
        yielded_artifact_ids: set[str] = set()

        while True:
            if asyncio.get_event_loop().time() >= deadline:
                logger.warning("A2aAsyncAdapter poll timeout", agent=self._slug, task_id=remote_task_id)
                yield AdapterEvent(type="error", error=f"agent timed out after {self._max_poll_seconds}s")
                return

            poll_resp = await client.post(
                self._endpoint_url,
                headers=self._headers(),
                json=self._get_task_body(remote_task_id),
            )
            poll_resp.raise_for_status()
            poll_body = poll_resp.json()
            task = poll_body.get("result", {})
            state = task.get("status", {}).get("state", "")

            if state and state != last_state:
                last_state = state
                if state in _INPUT_REQUIRED:
                    yield AdapterEvent(type="status", state=state, input_required=True)
                else:
                    yield AdapterEvent(type="status", state=state)

            for artifact in task.get("artifacts", []):
                artifact_id = artifact.get("artifactId") or artifact.get("index")
                if artifact_id not in yielded_artifact_ids:
                    yielded_artifact_ids.add(artifact_id)
                    yield AdapterEvent(type="artifact", artifact=artifact)

            if state in _TERMINAL:
                if state in ("TASK_STATE_COMPLETED", "completed"):
                    result_text = _extract_text(task)
                    yield AdapterEvent(type="done", result=result_text)
                else:
                    err_text = _extract_text(task) or f"A2A task ended in state: {state}"
                    logger.warning("A2aAsyncAdapter task terminal", agent=self._slug, state=state)
                    yield AdapterEvent(type="error", error=err_text)
                return

            await asyncio.sleep(self._poll_interval)

    async def stream_events(
        self,
        client: httpx.AsyncClient,
        remote_task_id: str,
        timeout: float,
    ) -> AsyncGenerator[AdapterEvent, None]:
        """Dispatch to SSE stream or polling based on agent capability."""
        if self._supports_streaming:
            async for event in self._stream_sse(client, remote_task_id, timeout):
                yield event
            return
        async for event in self._poll_events(client, remote_task_id, timeout):
            yield event

    async def _stream_sse(
        self,
        client: httpx.AsyncClient,
        remote_task_id: str,
        timeout: float,
    ) -> AsyncGenerator[AdapterEvent, None]:
        """Subscribe to SSE stream from the remote agent for live events."""
        sse_url = self._endpoint_url + f"tasks/{remote_task_id}/events"
        headers = {**self._headers(), "Accept": "text/event-stream"}
        yielded_artifact_ids: set[str] = set()

        try:
            async with client.stream("GET", sse_url, headers=headers, timeout=timeout) as resp:
                resp.raise_for_status()
                async for line in resp.aiter_lines():
                    if not line.startswith("data:"):
                        continue
                    raw = line[5:].strip()
                    if not raw or raw == "[DONE]":
                        continue
                    try:
                        event = json.loads(raw)
                    except json.JSONDecodeError:
                        continue

                    etype = event.get("type", "")
                    if etype in ("task_status_update", "status"):
                        state = event.get("status", {}).get("state") or event.get("state", "")
                        if state:
                            if state in _INPUT_REQUIRED:
                                yield AdapterEvent(type="status", state=state, input_required=True)
                            else:
                                yield AdapterEvent(type="status", state=state)
                            if state in _TERMINAL:
                                task = event.get("task", event)
                                if state in ("TASK_STATE_COMPLETED", "completed"):
                                    yield AdapterEvent(type="done", result=_extract_text(task))
                                else:
                                    err = _extract_text(task) or f"A2A task ended in state: {state}"
                                    yield AdapterEvent(type="error", error=err)
                                return
                    elif etype in ("task_artifact_update", "artifact"):
                        artifact = event.get("artifact", event)
                        artifact_id = artifact.get("artifactId") or artifact.get("index")
                        if artifact_id not in yielded_artifact_ids:
                            yielded_artifact_ids.add(artifact_id)
                            yield AdapterEvent(type="artifact", artifact=artifact)
                        for part in artifact.get("parts", []):
                            if "text" in part:
                                yield AdapterEvent(type="token", text=part["text"])
        except httpx.HTTPStatusError:
            logger.warning(
                "A2aAsyncAdapter SSE stream unavailable, falling back to poll",
                agent=self._slug,
                task_id=remote_task_id,
            )
            async for event in self._poll_events(client, remote_task_id, timeout):
                yield event

    async def stream_invoke(
        self,
        input: dict,
        timeout: float,
    ) -> AsyncGenerator[AdapterEvent, None]:
        # Pass the full input dict so _build_parts can detect typed vs text mode.
        # For text-only agents, _build_parts extracts input["message"] as a string.
        effective_timeout = min(timeout, self._max_poll_seconds)

        try:
            async with asyncio.timeout(effective_timeout):
                async with httpx.AsyncClient(timeout=httpx.Timeout(effective_timeout), follow_redirects=True) as client:
                    remote_task_id = await self.submit(client, dict(input))
                    logger.info(
                        "A2aAsyncAdapter task submitted",
                        agent=self._slug,
                        task_id=remote_task_id,
                    )
                    yield AdapterEvent(type="task_created", remote_task_id=remote_task_id)

                    async for event in self.stream_events(client, remote_task_id, effective_timeout):
                        yield event

        except asyncio.TimeoutError:
            logger.warning("A2aAsyncAdapter timeout", agent=self._slug, timeout=effective_timeout)
            yield AdapterEvent(type="error", error=f"agent timed out after {effective_timeout}s")
        except httpx.HTTPStatusError as exc:
            logger.error(
                "A2aAsyncAdapter HTTP error",
                agent=self._slug,
                status=exc.response.status_code,
                error=str(exc),
            )
            yield AdapterEvent(
                type="error",
                error=f"HTTP {exc.response.status_code}: {exc.response.text[:200]}",
            )
        except Exception as exc:
            logger.error("A2aAsyncAdapter error", agent=self._slug, error=str(exc))
            yield AdapterEvent(type="error", error=str(exc))


def _extract_text(task: dict) -> str:
    """Assemble all text parts from all artifacts in a completed task."""
    parts: list[str] = []
    for artifact in task.get("artifacts", []):
        for part in artifact.get("parts", []):
            if "text" in part:
                parts.append(part["text"])
    if not parts:
        msg = task.get("status", {}).get("message", {})
        for part in msg.get("parts", []):
            if "text" in part:
                parts.append(part["text"])
    return "".join(parts)
