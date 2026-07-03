"""
A2aAdapter — A2A v1.0 (Agent-to-Agent) JSON-RPC 2.0 over HTTP.

Compatible with Omni's A2A router (/a2a/{gateway_name}/).

Protocol:
  1. POST {endpoint_url} with JSON-RPC 2.0 SendMessage
  2. Message body: {"parts": [{"text": input["message"]}]}
  3. Omni returns a completed Task synchronously (no streaming)
  4. Result extracted from task.artifacts[0].parts[0].text
  5. Falls back to GetTask poll if response state is WORKING/SUBMITTED

Auth: Authorization: Bearer <decrypted token>
"""

import asyncio
import json
from typing import AsyncGenerator
from uuid import uuid4

import httpx

from app.adapters.base import AdapterEvent, AgentAdapter
from app.utils.crypto import decrypt_value
from app.utils.logger import logger

# A2A task terminal states
_TERMINAL = {"TASK_STATE_COMPLETED", "TASK_STATE_FAILED", "TASK_STATE_CANCELED", "TASK_STATE_REJECTED"}
_POLL_INTERVAL = 0.5
_MAX_POLLS = 60  # 30 seconds max poll time


class A2aAdapter(AgentAdapter):
    def __init__(
        self,
        *,
        agent_slug: str,
        endpoint_url: str,
        auth_token_encrypted: str | None,
    ) -> None:
        self._slug = agent_slug
        self._endpoint_url = endpoint_url.rstrip("/") + "/"
        self._auth_token_encrypted = auth_token_encrypted

    def _headers(self) -> dict:
        token = decrypt_value(self._auth_token_encrypted) if self._auth_token_encrypted else ""
        if token:
            return {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
        return {"Content-Type": "application/json"}

    def _send_message_body(self, message: str) -> dict:
        return {
            "jsonrpc": "2.0",
            "id": str(uuid4()),
            "method": "SendMessage",
            "params": {
                "message": {
                    "role": "user",
                    "parts": [{"text": message}],
                    "messageId": str(uuid4()),
                }
            },
        }

    def _get_task_body(self, task_id: str) -> dict:
        return {
            "jsonrpc": "2.0",
            "id": str(uuid4()),
            "method": "GetTask",
            "params": {"id": task_id},
        }

    @staticmethod
    def _extract_result(task: dict) -> str | None:
        """Pull result text from A2A task artifacts."""
        for artifact in task.get("artifacts", []):
            for part in artifact.get("parts", []):
                if "text" in part:
                    return part["text"]
        # Fallback: error message in status
        msg = task.get("status", {}).get("message", {})
        for part in msg.get("parts", []):
            if "text" in part:
                return part["text"]
        return None

    async def stream_invoke(
        self,
        input: dict,
        timeout: float,
    ) -> AsyncGenerator[AdapterEvent, None]:
        message = input.get("message", "")
        headers = self._headers()

        try:
            async with asyncio.timeout(timeout):
                async with httpx.AsyncClient(timeout=timeout) as client:
                    # 1. Send message
                    resp = await client.post(
                        self._endpoint_url,
                        headers=headers,
                        json=self._send_message_body(message),
                    )
                    resp.raise_for_status()
                    body = resp.json()

                    if "error" in body and body["error"]:
                        err = body["error"]
                        msg = err.get("message", str(err)) if isinstance(err, dict) else str(err)
                        logger.error("A2aAdapter RPC error", agent=self._slug, error=msg)
                        yield AdapterEvent(type="error", error=msg)
                        return

                    task = body.get("result", {})
                    task_id = task.get("id")
                    state = task.get("status", {}).get("state", "")

                    # 2. Poll if not yet terminal (Omni is sync, but be safe)
                    polls = 0
                    while state not in _TERMINAL and task_id and polls < _MAX_POLLS:
                        await asyncio.sleep(_POLL_INTERVAL)
                        poll_resp = await client.post(
                            self._endpoint_url,
                            headers=headers,
                            json=self._get_task_body(task_id),
                        )
                        poll_resp.raise_for_status()
                        poll_body = poll_resp.json()
                        task = poll_body.get("result", task)
                        state = task.get("status", {}).get("state", "")
                        polls += 1

                    # 3. Extract result
                    if state == "TASK_STATE_COMPLETED":
                        result_text = self._extract_result(task) or ""
                        # Stream word by word so the orchestrator sees tokens
                        words = result_text.split()
                        for i, word in enumerate(words):
                            chunk = word + (" " if i < len(words) - 1 else "")
                            yield AdapterEvent(type="token", text=chunk)
                        yield AdapterEvent(type="done", result=result_text)

                    elif state in ("TASK_STATE_FAILED", "TASK_STATE_REJECTED"):
                        err_text = self._extract_result(task) or f"A2A task {state.lower()}"
                        logger.warning("A2aAdapter task failed", agent=self._slug, state=state)
                        yield AdapterEvent(type="error", error=err_text)

                    else:
                        yield AdapterEvent(type="error", error=f"A2A task ended in unexpected state: {state}")

        except asyncio.TimeoutError:
            logger.warning("A2aAdapter timeout", agent=self._slug, timeout=timeout)
            yield AdapterEvent(type="error", error=f"agent timed out after {timeout}s")
        except httpx.HTTPStatusError as exc:
            logger.error("A2aAdapter HTTP error", agent=self._slug,
                         status=exc.response.status_code, error=str(exc))
            yield AdapterEvent(type="error", error=f"HTTP {exc.response.status_code}: {exc.response.text[:200]}")
        except Exception as exc:
            logger.error("A2aAdapter error", agent=self._slug, error=str(exc))
            yield AdapterEvent(type="error", error=str(exc))
