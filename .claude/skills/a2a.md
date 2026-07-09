---
name: a2a
description: A2A expert guide for the-M platform — loads the full A2A reference doc and provides a quick-access checklist for correct implementation, typed inputs, and anti-patterns to avoid before any A2A development work.
---

# A2A Expert Guide — the-M Platform

<$read_file>docs/A2A_REFERENCE.md</$read_file>

---

## Quick-Access Index

Use these sections directly — no need to re-read the full reference each time.

### [1] Starting a new A2A agent?
→ Go to **"Agent executor boilerplate"** in the reference above.
Key points:
- Emit `submitted` → `working` → artifact → `completed` in that order
- Use `InMemoryTaskStore` for simple agents (no durability needed)
- Declare skills in `make_agent_card()` with correct `input_modes`

### [2] Agent expects structured input (not a text blob)?
→ Declare `input_modes: ["application/json"]` on the skill.
→ Read with `HasField("data")` + `json_format.MessageToDict(part.data.struct_value)`
→ **Never** use regex parsing on text — that's the anti-pattern we're eliminating.

### [3] Sending typed input from the orchestrator?
→ `A2aAsyncAdapter._send_message_body` currently always sends `{"text": message}`.
→ When agent skill has `application/json` input_mode, send `{"data": {...}}` instead.
→ Context/summary goes as a separate `{"text": "..."}` part alongside the data part.

### [4] Wiring a new agent into the orchestrator?
True A2A checklist — zero prompt changes required:
- [ ] Agent card published at `/.well-known/agent-card.json`
- [ ] Skill `description` tells LLM what to pass (replaces any system prompt instructions)
- [ ] Skill `input_modes` signals the part type (`text/plain` or `application/json`)
- [ ] Skill `output_modes` signals what comes back
- [ ] No agent-specific format strings in orchestrator system prompt
- [ ] `A2aAsyncAdapter` constructed with the agent's `input_modes` so it sends the right part

### [5] Current platform gaps (fix in this order)
| # | File | What to fix |
|---|------|-------------|
| 1 | `agents/docu_writer/main.py` | Add `application/json` to skill `input_modes`; read `data` part; remove regex `_parse_input` |
| 2 | `app/adapters/a2a_async_adapter.py` | Detect agent `input_modes`; send `data` part when `application/json` |
| 3 | `app/services/task_runner.py` | Pass structured dict when agent has typed schema; context as separate text part |
| 4 | DB `them.orchestrators.system_prompt` | Remove `FORMAT:/TITLE:/CONTENT:` instructions — skill description replaces them |
| 5 | `app/services/task_runner.py` | Replace `setattr` proxy hack in `_load_orchestrator_row` with proper dataclass |

### [6] Anti-patterns — never do these
- `{"message": string}` for agents that expect structured data
- Regex parsing on text parts (`re.search(r"FORMAT:")`) — use `data` parts
- Agent-specific format instructions in orchestrator system prompt
- `Part.data.CopyFrom(Struct())` — wrong; use `part.data.struct_value.CopyFrom(s)`
- Assuming `input_modes` is optional — it's the sole contract signal in v1.1.0

### [7] Testing after A2A changes
```bash
# Test adapter directly (no LLM):
docker exec them-bridge python3 -c "
import asyncio, sys; sys.path.insert(0, '/app')
from app.adapters.a2a_async_adapter import A2aAsyncAdapter
async def t(slug, url, msg):
    adapter = A2aAsyncAdapter(agent_slug=slug, endpoint_url=url, auth_token_encrypted=None)
    async for e in adapter.stream_invoke({'message': msg}, timeout=30.0): print(e)
asyncio.run(t('a2a_echo', 'http://a2a-echo:9200', 'hello'))
"

# Run relevant tests:
python scripts/tests/run_tests.py 23 24   # skill discovery + code_agent live
python scripts/tests/run_tests.py         # full suite before committing
```
