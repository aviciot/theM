# A2A Reference — SDK v1.1.0
# Ground truth from live SDK inspection + spec
# Last updated: 2026-07-09

---

## What A2A is

JSON-RPC 2.0 over HTTP. Agent publishes a card at `/.well-known/agent-card.json`.
Caller sends `SendMessage` → gets a task ID → polls `GetTask` or streams until terminal.
The power is in **typed message parts** — not text blobs.

---

## Part types (the contract between agents)

`Part` has exactly ONE of these fields:

| Field   | Proto type              | Wire JSON               | Use for                        |
|---------|-------------------------|-------------------------|--------------------------------|
| `text`  | string                  | `{"text": "..."}` | Plain text, instructions |
| `data`  | google.protobuf.Value   | `{"data": {...}}`  | **Typed structured input** — any JSON shape |
| `raw`   | bytes                   | `{"raw": "<base64>"}` | Binary files |
| `url`   | string                  | `{"url": "https://..."}` | Remote file reference |

Plus optional `filename` and `media_type` on any part.

**Key:** `Part.data` is `google.protobuf.Value` — holds any JSON object, array, or primitive.
Over the wire it's just a JSON object under the `"data"` key.

---

## Sending a data part (wire JSON — what our adapter sends)

```json
{
  "jsonrpc": "2.0",
  "method": "SendMessage",
  "params": {
    "message": {
      "role": 1,
      "messageId": "<uuid>",
      "contextId": "<uuid>",
      "parts": [
        {"data": {"format": "html", "title": "APM Analysis", "content": "## Findings..."}}
      ]
    },
    "configuration": {"returnImmediately": true}
  }
}
```

Mixed parts (context as text + typed input as data):
```json
"parts": [
  {"text": "[Context summary]\n...prior findings..."},
  {"data": {"format": "html", "title": "APM Analysis", "content": "..."}}
]
```

---

## Reading parts in an agent executor (Python SDK v1.1.0)

```python
from google.protobuf import json_format

class MyExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue):
        data = {}
        text_parts = []

        for part in context.message.parts:
            if part.HasField("data"):
                # data is google.protobuf.Value — convert to dict
                data = json_format.MessageToDict(part.data.struct_value)
            elif part.HasField("text"):
                text_parts.append(part.text)
            elif part.HasField("raw"):
                raw_bytes = part.raw  # bytes
            elif part.HasField("url"):
                url = part.url

        # Use typed fields directly — no regex
        fmt = data.get("format", "html")
        title = data.get("title", "Documentation")
        content = data.get("content", " ".join(text_parts))
```

**Confirmed working:** `HasField("data")` correctly identifies data parts.
`json_format.MessageToDict(part.data.struct_value)` returns a Python dict.

---

## AgentSkill fields (SDK v1.1.0 — from live proto inspection)

```
id                   string        unique identifier
name                 string        human-readable label
description          string        what this skill does — LLM uses this for routing decisions
tags                 []string      keywords for discovery
examples             []string      sample inputs/prompts
input_modes          []string      MIME types accepted:  "text/plain" | "application/json" | "text/html" ...
output_modes         []string      MIME types returned:  "text/plain" | "text/html" | "text/markdown" ...
security_requirements []SecurityRequirement  optional
```

**No `inputSchema`/`outputSchema` in v1.1.0.** The schema contract is implied by `input_modes`:
- `text/plain` → agent expects `text` parts
- `application/json` → agent expects `data` parts with a JSON object
- The shape of that JSON object is documented in `description` / `examples`

### Typed skill example:
```python
skill = card.skills.add()
skill.id = "render_html"
skill.name = "Render HTML Documentation"
skill.description = (
    "Renders technical analysis into a self-contained HTML file. "
    "Input: JSON with fields: format (html|markdown|slides), title (string), content (markdown string). "
    "Output: complete HTML file artifact."
)
skill.input_modes.append("application/json")
skill.output_modes.append("text/html")
skill.examples.append('{"format":"html","title":"APM Analysis","content":"## Findings..."}')
```

---

## AgentCard fields (SDK v1.1.0)

```
name                 string
description          string
version              string
provider             AgentProvider { organization, url }
capabilities         AgentCapabilities { streaming, pushNotifications, extendedAgentCard }
supported_interfaces []AgentInterface { url, type }
default_input_modes  []string   MIME types
default_output_modes []string   MIME types
skills               []AgentSkill
security_schemes     map<string, SecurityScheme>
security_requirements []SecurityRequirement
signatures           []AgentCardSignature   (v1.0 signed cards)
icon_url             string
```

---

## Task lifecycle

```
submitted → working → completed   (terminal)
                    → failed      (terminal)
                    → canceled    (terminal)
                    → rejected    (terminal)
                    → input-required → working  (resume with same taskId)
                    → auth-required            (needs authentication)
```

---

## JSON-RPC methods

| Method (v1.0)           | What it does                                      |
|-------------------------|---------------------------------------------------|
| `SendMessage`           | Submit task, blocking or async (returnImmediately)|
| `GetTask`               | Poll task state + artifacts                       |
| `CancelTask`            | Cancel in-progress task                           |
| `subscribeToTask`       | SSE stream of task events                         |
| `listTasks`             | List tasks for a contextId                        |
| `getExtendedAgentCard`  | Authenticated full agent card                     |

**Note:** Our `A2aAsyncAdapter` sends PascalCase (`SendMessage`, `GetTask`).
SDK v1.1.0 compat layer accepts both PascalCase and camelCase.

---

## Multi-turn / context threading

- `contextId` groups related tasks across sessions — same contextId = same conversation
- `taskId` references a specific task for follow-up (input-required resume)
- `referenceTaskIds` in a message links to prior tasks for context
- Our `A2aAsyncAdapter` already passes `contextId` in every `SendMessage` ✓

---

## Artifact structure

```
artifact_id    string       unique within task
name           string       human-readable
description    string
parts          []Part       same Part types as messages
metadata       Struct       custom key-value
```

Artifacts are the **output** of a task. Separate from message history.
An agent can emit multiple artifacts (e.g., one HTML file + one summary text).

---

## Push notifications

```
SendMessageConfiguration.task_push_notification_config:
  url    string   webhook endpoint
  token  string   optional bearer token
```

Agent POSTs `StreamResponse` (containing `TaskStatusUpdateEvent` or `TaskArtifactUpdateEvent`)
to the webhook URL on every state change.
Our platform implements the inbound side at `POST /a2a/push/{task_id}` ✓

---

## Agent executor boilerplate (SDK v1.1.0)

```python
from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.events.event_queue import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.routes import add_a2a_routes_to_fastapi, create_agent_card_routes, create_jsonrpc_routes
from a2a.server.tasks import InMemoryTaskStore
from a2a.types import (
    AgentCard, AgentSkill, Artifact, Task,
    TaskArtifactUpdateEvent, TaskState, TaskStatusUpdateEvent,
)
from google.protobuf import json_format

class MyExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        # 1. Read input
        data = {}
        for part in context.message.parts:
            if part.HasField("data"):
                data = json_format.MessageToDict(part.data.struct_value)
            elif part.HasField("text"):
                pass  # handle text

        # 2. Emit submitted
        task = Task()
        task.id = context.task_id
        task.context_id = context.context_id
        task.status.state = TaskState.TASK_STATE_SUBMITTED
        await event_queue.enqueue_event(task)

        # 3. Emit working
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_WORKING
        await event_queue.enqueue_event(ev)

        # 4. Do work...
        result = "output"

        # 5. Emit artifact
        artifact = Artifact()
        artifact.artifact_id = "result"
        artifact.name = "result.html"
        part = artifact.parts.add()
        part.text = result
        part.filename = "result.html"
        part.media_type = "text/html"

        art_ev = TaskArtifactUpdateEvent()
        art_ev.task_id = context.task_id
        art_ev.context_id = context.context_id
        art_ev.artifact.CopyFrom(artifact)
        art_ev.last_chunk = True
        await event_queue.enqueue_event(art_ev)

        # 6. Emit completed
        done = TaskStatusUpdateEvent()
        done.task_id = context.task_id
        done.context_id = context.context_id
        done.status.state = TaskState.TASK_STATE_COMPLETED
        await event_queue.enqueue_event(done)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(ev)
```

---

## Sending a data part from our adapter (what to change in A2aAsyncAdapter)

Current (`_send_message_body`):
```python
"parts": [{"text": message}]
```

For typed agents (when input is a dict, not a string):
```python
# If input is a dict with typed fields:
"parts": [{"data": input_dict}]

# If context summary exists alongside typed input:
"parts": [
    {"text": context_summary},   # context as text part
    {"data": input_dict}          # typed input as data part
]
```

The adapter needs to know whether to send `text` or `data` — determined by the agent's
`input_modes` from its card (`text/plain` vs `application/json`).

---

## What "true A2A" means for the orchestrator

1. **Orchestrator system prompt is generic** — describes the goal, never agent-specific formats
2. **Skill `description` + `input_modes` tells the LLM what to pass** — no magic text instructions
3. **LLM fills typed fields** — not magic `FORMAT:/TITLE:/CONTENT:` text
4. **Agent card is the sole contract** — adding a new agent = publish its card, zero prompt changes
5. **Context as a separate part** — not concatenated into the input string

---

## Current gaps in the-M vs true A2A

| Gap | File | Fix |
|----|------|-----|
| All agents receive `{"message": string}` | `task_runner._invoke_agent` | Send `data` part when agent declares `application/json` input |
| `docu_writer` parses input with regex | `agents/docu_writer/main.py` | Read `data` part with `HasField("data")` + `MessageToDict` |
| `docu_writer` skill has no `input_modes` | `agents/docu_writer/main.py` | Add `application/json` to skill `input_modes` |
| Orchestrator system prompt encodes `FORMAT:/TITLE:` | DB `them.orchestrators.system_prompt` | Remove — skill description replaces it |
| Memory injected via string concat | `task_runner.py:777` | Pass as separate `text` part alongside `data` part |
| `A2aAsyncAdapter` always sends text part | `app/adapters/a2a_async_adapter.py` | Detect agent `input_modes`, send appropriate part type |

---

## Implementation order (no breaking changes, each step independently testable)

1. **`docu_writer`**: add `application/json` to skill, read `data` part, keep text fallback
2. **`A2aAsyncAdapter`**: pass agent `input_modes` in constructor; send `data` part when `application/json`
3. **`task_runner._invoke_agent`**: pass structured dict when agent has typed schema; pass context as separate text part
4. **Orchestrator system prompt**: remove agent-specific format instructions
5. **`_load_orchestrator_row` proxy**: replace `setattr` hack with a proper dataclass (robustness)

---

## References

- Spec: https://a2a-protocol.org/latest/specification/
- Python SDK: https://github.com/a2aproject/a2a-python
- Samples: https://github.com/a2aproject/a2a-samples
- Skills tutorial: https://a2a-protocol.org/latest/tutorials/python/3-agent-skills-and-card/
