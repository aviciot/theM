# Documentation Index — the-M
# Last updated: 2026-07-04

One line per doc. Read this first, then open only what you need.
**Also read:** `scripts/tests/INDEX.md` before touching tests.

---

## How to use this index

- **Read** column: what you'll find inside — use this to decide if you need the doc
- **Update when** column: mandatory — if you do one of these things, update that doc before the session ends
- Trust code over docs. If they diverge, fix the doc.

---

## Session Entrypoint

| File | Read when | Update when |
|---|---|---|
| `/CLAUDE.md` | Every session — always read first | Stack changes, new rules, state changes |

---

## System Reference

| File | Read when | Update when |
|---|---|---|
| `ARCHITECTURE.md` | Touching `app/` — covers agentic loop, adapter fan-out, WS protocol, scalability design | Any flow or component changes |
| `SCHEMA.md` | Touching `app/models.py`, `db/001_schema.sql`, or writing queries — covers all `them.*` tables with column descriptions and FK rationale | Adding/changing any DB table or column |
| `REDIS.md` | Touching anything that reads/writes Redis — covers every key pattern, TTL, owner, pub/sub channels | Adding/renaming any Redis key or channel |
| `ADAPTERS.md` | Adding or changing an agent transport — covers `AgentAdapter` contract, `AdapterEvent`, `omni_ws` protocol, how to add new transports | New transport type, changed adapter interface |
| `A2A_REFERENCE.md` | A2A SDK v1.1.0 reference — Part types (text/data/raw/url), AgentCard/AgentSkill fields, wire format, typed input examples, current gaps vs true A2A | A2A SDK version bump or spec changes |
| `A2A_AGENTS.md` | Working with A2A test agents — start/stop commands, DB enable/disable, cache bust, playground prompts, raw JSON-RPC test, adapter integration test | A2A agent code changes, new test agents added |
| `AUTH.md` | Touching auth flow, cookies, JWT, bearer tokens — covers JWT (dashboard) vs bearer tokens (WS), cache flow, token hashing, cookie names | Auth flow changes, new token types |
| `FLOWS.md` | Understanding end-to-end orchestration — covers full sequence for a multi-agent run, Redis pub/sub trace events | Orchestration flow changes |

---

## Status & History

| File | Read when | Update when |
|---|---|---|
| `STATUS.md` | Start of session — know what's broken/pending before touching anything | End of session: update build progress, open items, infrastructure state |
| `LESSONS.md` | Before any non-obvious judgment call — covers past bugs and fixes | Any bug fix or non-obvious behavior discovered — append only, never edit existing entries |
| `KNOWLEDGE_BASE_PLAN.md` | Adding a new doc — lists allowed files and maintenance rules | When a new doc is added or a doc is retired |

---

## What lives where (quick lookup)

| You want to know about... | Read |
|---|---|
| How the LLM agentic loop works | `ARCHITECTURE.md` |
| What columns `them.agents` has | `SCHEMA.md` |
| What `them:orchestrators:*` TTL is | `REDIS.md` |
| How to add a new agent transport | `ADAPTERS.md` |
| Why cookie names are `them_access_token` | `AUTH.md` |
| Full sequence of a WS orchestration run | `FLOWS.md` |
| What tests exist and when to run them | `scripts/tests/INDEX.md` |
| What's currently broken | `STATUS.md` |
| Why we use `init` callback not `server_settings` | `LESSONS.md` |
