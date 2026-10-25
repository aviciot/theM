---
title: Security Scanning and Guards (file gate, text gate, workers)
type: architecture
status: current
owns:
  - go/internal/middleware/
  - go/cmd/middleware-worker/main.go
  - go/internal/admin/middleware_wirings.go
  - go/internal/admin/security_config.go
  - go/internal/admin/scanjob.go
last_verified: 2026-10-25
---

# Security Scanning and Guards

Two separate things share the word "security":

1. **Guards (middleware)** — check files and text while a run executes. Code: `go/internal/middleware/`.
2. **Agent security scan** — a one-off scan of an agent card, started from the admin API. Code: `go/internal/admin/scanjob.go` (runs in a goroutine after a `202`, reports progress on Redis `them:dash:agent:{id}`, saves the result on the agent).

## Guards: the pieces

| Piece | What it does |
|---|---|
| `middleware_defs` | Global catalogue of guards (`kind` = `guard` or `cache`), with default `config`. |
| `middleware_wirings` | Per app + agent (+ optional `node_id`) switch: which def, `position`, `config_override`, `enabled`. Unique per `(application_id, agent_id, position)`. Admin CRUD at `/middleware-wirings`; app-level config at `/applications/{id}/security-config`. |
| **Processors** | `av` (ClamAV via clamd, Unix socket or TCP), `pii` (regex: email, phone, card, SSN), `promptguard` (regex phrases by sensitivity low/medium/high). Registered in a `Registry`. |
| **Pipeline** | Runs the chosen processors in order over one `Part`; a processor error is turned into a non-blocking result. |
| **FileGate** | Async path for **files**. |
| **TextGate** | Sync path for **text** from llm/agent nodes (AppFlow). |

## FileGate (files, async)

1. An agent returns a file part. `FileGate` resolves config (def default → wiring override → app security config).
2. The file is fetched, stored in the object store as **quarantine**, and a row is added to `middleware_jobs`. The caller gets status `pending`.
3. `them-middleware-worker` claims jobs with `FOR UPDATE SKIP LOCKED` (poll 500 ms, `MIDDLEWARE_POLL_INTERVAL_MS`), runs the pipeline, then releases or blocks the file. Attempts are counted; stuck `claimed` jobs are reset.
4. A **reaper** (every 15 min, `REAPER_INTERVAL_MINUTES`) deletes expired quarantine objects and rows.

ClamAV socket: `CLAMAV_SOCKET` (default `/var/run/clamav/clamd.sock`). ClamAV scanner errors → outcome `error`, which **does not block**.

## TextGate (text, sync)

Runs `pii_redact` / `prompt_inject` in-process inside the AppFlow activity, so redacted text is what the next node sees (async would be too late). It has its own small config cache and reads a **flat** processor config from `middleware_defs.config` — a different shape from FileGate's nested `SecurityConfig`. Do not reuse FileGate's resolver for it.

## Behaviour to know (fail-open)

The gates favour availability over blocking:
- Config lookup error, fetch error, no object store, quarantine error → file passes through.
- ClamAV/processor error → logged, not blocking.
- If enqueueing the scan job fails, the file is returned as `pending` and **never scanned**.
- `pii` card matching is a digit-run regex (no Luhn check); promptguard is regex only, not an LLM judge.

See backlog entries F-106…F-109.

## Tests
`cd go && go test ./internal/middleware/...` (gate, job, reaper, textgate, av, pii, promptguard).
