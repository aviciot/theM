# A2A Test Agents
# Last updated: 2026-07-05

Three A2A v1.0 test agents ship with the-M under Docker Compose profile `test-agents`.
They exist to validate the full A2A adapter path: bridge → `A2aAsyncAdapter` → agent container → result back.

---

## Agents

| Agent | Slug | Container | Port | What it does |
|---|---|---|---|---|
| A2A Echo | `a2a_echo` | `a2a-echo` | 9200 | Echoes the input message verbatim. Completes instantly. |
| A2A Slow | `a2a_slow` | `a2a-slow` | 9201 | Waits 5 seconds then completes. Tests async delegation and deadline enforcement. |
| A2A Stream | `a2a_stream` | `a2a-stream` | 9202 | Streams "The quick brown fox…" word by word via artifact chunks. `supports_streaming=true`. |

All three use transport `a2a_async` → handled by `app/adapters/a2a_async_adapter.py`.

Source: `agents/a2a_echo/`, `agents/a2a_slow/`, `agents/a2a_stream/`

---

## Start / Stop

```powershell
# Start A2A test agents (alongside the main stack)
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents up -d

# Check health
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents ps

# Stop only the test agents
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents stop a2a-echo a2a-slow a2a-stream

# Rebuild after code change (agents have no volume mount — need rebuild)
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents build a2a-echo a2a-slow a2a-stream
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents up -d a2a-echo a2a-slow a2a-stream
```

---

## DB Configuration

Agents are seeded by `db/002_seed.sql` with `enabled=false` by default (containers may not be running).
Enable them before testing:

```powershell
# Enable all three
docker exec them-postgres psql -U them -d them -c "UPDATE them.agents SET enabled=true WHERE slug IN ('a2a_echo','a2a_slow','a2a_stream');"

# Disable (when not running test-agents profile)
docker exec them-postgres psql -U them -d them -c "UPDATE them.agents SET enabled=false WHERE slug IN ('a2a_echo','a2a_slow','a2a_stream');"

# Check current state
docker exec them-postgres psql -U them -d them -c "SELECT slug, enabled, endpoint_url, supports_streaming FROM them.agents WHERE transport='a2a_async';"
```

The `a2a_test` orchestrator (seeded in `db/002_seed.sql`) has all three as `allowed_agent_ids`.
It uses `claude-haiku-4-5-20251001` (fast + cheap for testing).

```powershell
# Verify a2a_test orchestrator has all three agents
docker exec them-postgres psql -U them -d them -c "
SELECT o.name, array_agg(a.slug) as agents
FROM them.orchestrators o
JOIN them.agents a ON a.id = ANY(o.allowed_agent_ids)
WHERE o.name = 'a2a_test'
GROUP BY o.name;"
```

---

## Cache Management

After enabling/disabling agents or changing orchestrator config, bust Redis cache so the bridge picks up changes immediately (otherwise TTL is 600s for orchestrators, no TTL for agent registry until pub/sub fires):

```powershell
docker exec them-redis redis-cli DEL them:orchestrators:a2a_test them:agents:registry
```

---

## Test via Playground (manual)

1. Go to **http://localhost:3200/admin/playground**
2. Select orchestrator: **`a2a_test`**
3. Send a prompt — the LLM will choose which agent to call based on the description

| Prompt | Expected agent | Expected result |
|---|---|---|
| `"echo: hello world"` | `a2a_echo` | `Echo: hello world` |
| `"echo back exactly: test message 123"` | `a2a_echo` | `Echo: test message 123` |
| `"wait 5 seconds then reply"` | `a2a_slow` | Reply after ~5s delay |
| `"stream a response word by word"` | `a2a_stream` | The quick brown fox… streamed word by word |
| `"use all agents: echo hi, wait, then stream"` | all three (parallel) | All three results |

Watch the **Trace** debug tab for real-time `task_created → status → artifact → done` events per agent.

---

## Test via Raw JSON-RPC (direct, no LLM)

Calls go directly to the agent container. Useful for debugging the agent in isolation.

**Note:** These calls go to the container's internal port — run them from inside the bridge container or expose ports in `docker-compose.dev.yml`.

```bash
# From inside them-bridge container:
docker exec them-bridge python3 -c "
import asyncio, httpx, json, uuid

async def test():
    body = {
        'jsonrpc': '2.0',
        'id': str(uuid.uuid4()),
        'method': 'SendMessage',
        'params': {
            'message': {
                'role': 1,
                'parts': [{'text': 'hello from direct test'}],
                'messageId': str(uuid.uuid4()),
            },
            'configuration': {'returnImmediately': True},
        }
    }
    async with httpx.AsyncClient() as client:
        r = await client.post('http://a2a-echo:9200/', headers={'A2A-Version': '1.0', 'Content-Type': 'application/json'}, json=body)
        print(json.dumps(r.json(), indent=2))

asyncio.run(test())
"
```

Replace `a2a-echo:9200` with `a2a-slow:9201` or `a2a-stream:9202` for the other agents.

---

## Test via A2aAsyncAdapter (integration, no LLM)

This exercises the full adapter path — same code the orchestrator uses — without going through the LLM:

```bash
docker exec them-bridge python3 -c "
import asyncio
import sys
sys.path.insert(0, '/app')
from app.adapters.a2a_async_adapter import A2aAsyncAdapter

async def test(slug, url, message):
    adapter = A2aAsyncAdapter(
        agent_slug=slug,
        endpoint_url=url,
        auth_token_encrypted=None,
        supports_streaming=False,
        poll_interval=1.0,
        max_poll_seconds=30.0,
    )
    print(f'--- {slug} ---')
    async for event in adapter.stream_invoke({'message': message}, timeout=30.0):
        print(f'  {event}')

async def main():
    await test('a2a_echo',   'http://a2a-echo:9200',   'platform-to-agent test!')
    await test('a2a_slow',   'http://a2a-slow:9201',   'slow test')
    await test('a2a_stream', 'http://a2a-stream:9202',  'stream test')

asyncio.run(main())
"
```

**Expected output:**

```
--- a2a_echo ---
  AdapterEvent(type='task_created', remote_task_id='...')
  AdapterEvent(type='status', state='TASK_STATE_COMPLETED')
  AdapterEvent(type='artifact', ...)
  AdapterEvent(type='done', result='Echo: platform-to-agent test!')

--- a2a_slow ---
  AdapterEvent(type='task_created', ...)
  AdapterEvent(type='status', state='TASK_STATE_WORKING')
  [~5s pause]
  AdapterEvent(type='status', state='TASK_STATE_COMPLETED')
  AdapterEvent(type='artifact', ...)
  AdapterEvent(type='done', result='Completed after 5.0s delay.')

--- a2a_stream ---
  AdapterEvent(type='task_created', ...)
  AdapterEvent(type='status', state='TASK_STATE_WORKING')
  AdapterEvent(type='artifact', ...)
  AdapterEvent(type='status', state='TASK_STATE_COMPLETED')
  AdapterEvent(type='done', result='The quick brown fox jumps over the lazy dog. Streaming word by word via A2A artifacts.')
```

---

## Structural Test (CI)

```powershell
python scripts/tests/run_tests.py 16
```

Checks file existence, AST structure, docker-compose profile, seed SQL slugs, and `A2aAsyncAdapter` importability. No containers required.

---

## Protocol Reference

All three agents implement A2A v1.0 JSON-RPC 2.0. Key wire details:

| Field | Value | Notes |
|---|---|---|
| Method | `SendMessage` | CamelCase — not `message/send` |
| Header | `A2A-Version: 1.0` | Required — missing causes validation failure |
| `role` | `1` (integer) | Proto enum `ROLE_USER=1` — not string `"user"` |
| Part format | `{"text": "..."}` | No `"kind"` wrapper — direct oneof field name |
| Config field | `returnImmediately: true` | Not `blocking` |
| Terminal states | `TASK_STATE_COMPLETED` etc. | Proto enum names from SDK v1.1 |

See `docs/ADAPTERS.md` for the full `A2aAsyncAdapter` constructor and event sequence.
See `docs/LESSONS.md` for the full list of SDK v1.1 gotchas discovered during live testing.
