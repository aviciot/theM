---
title: A2A Test Agents (a2a-echo, a2a-slow, a2a-stream)
type: guide
status: current
last_verified: 2026-10-25   # echo agent called live on this date; slow/stream not called
---

# A2A Test Agents

## Purpose
Three tiny Python A2A agents (source in `agents/a2a_echo/`, `agents/a2a_slow/`, `agents/a2a_stream/`) used to test the platform's
A2A path and to give App Canvas / AppFlow tests a real agent to call (`scripts/tests/test_40…42` use `a2a_echo`).
They run only with Docker Compose profile **`test-agents`**; ports are internal (not published).

| Agent | Slug | Container | Port | Behaviour | DB `agents` row (default tenant) |
|---|---|---|---|---|---|
| A2A Echo | `a2a_echo` | `a2a-echo` | 9200 | returns the input text as an artifact, instantly | transport `a2a_async`, enabled |
| A2A Slow | `a2a_slow` | `a2a-slow` | 9201 | waits ~5 s, then completes (tests async/deadlines) | `a2a_async`, **disabled** |
| A2A Stream | `a2a_stream` | `a2a-stream` | 9202 | streams text word by word via artifact chunks | `a2a_async`, enabled, `supports_streaming` |

The DB rows (and the `component_definitions` an App Canvas needs for `kind: agent` nodes) already exist in the `default` tenant of the
dev stack. They are not created by any migration you can rely on for a fresh install (see backlog F-35).

## Start / stop
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents up -d a2a-echo a2a-slow a2a-stream
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents ps
# after changing agents/a2a_*/: rebuild AND recreate (restart alone keeps the old image)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents up -d --build a2a-echo
```

## Call one directly (verified for a2a-echo)
Ports are internal, so call from inside a container on `them-network` (the agent image has Python but no `wget`):
```bash
docker exec a2a-echo python -c "
import json,urllib.request
b={'jsonrpc':'2.0','id':'1','method':'SendMessage','params':{'message':{'messageId':'m1','role':'ROLE_USER','parts':[{'text':'hello'}]},'configuration':{'returnImmediately':False}}}
r=urllib.request.Request('http://localhost:9200/',json.dumps(b).encode(),{'Content-Type':'application/json','A2A-Version':'1.0'})
print(urllib.request.urlopen(r,timeout=20).read().decode())"
```
Expected: `result.task.status.state = TASK_STATE_COMPLETED` with an artifact `echo-result` containing the input text.
Wire format is A2A v1.0 JSON-RPC (`SendMessage`, role `ROLE_USER`, parts with `text`, header `A2A-Version: 1.0`).

## How the platform calls them
- Orchestrators: `internal/agentregistry` with transport `a2a_async` (the Python `A2aAsyncAdapter` this guide used to describe no longer exists).
- AppFlow agent nodes: `cmd/dag-worker` `pgxAgentA2ACaller.InvokeByID` posts the same `SendMessage` to the agent's `endpoint_url`.
- Details: `docs/architecture/a2a-agents-agentgen.md` (canvas agents served by `them-agent-runtime`) and `docs/architecture/app-canvas-appflow.md`.

## Gotchas
- `a2a_slow` is disabled in the DB; enable it (`UPDATE them.agents SET enabled=true WHERE slug='a2a_slow'`) before using it in an app.
- If an app's canvas shows `component_not_found` for these agents while they exist, see backlog F-92 (E2E tests 40–42 currently fail this way).
- These are test agents: no authentication on their A2A port, reachable by any container on the network.
