# CLAUDE.md Guides — Alignment Report
# Generated: 2026-07-25

---

## Objective

Restructure `/opt/docker/them/CLAUDE.md` (root) and `/opt/docker/them/go/CLAUDE.md` (Go) so that:
- responsibilities are clear and non-duplicated
- root file governs the whole project
- Go file governs only Go-specific development
- handover/session lifecycle rule is canonical in root, referenced in Go

---

## Changes Made

### Root CLAUDE.md

**Removed:**
- Stale "Known State (2026-07-14)" section describing the Python-only deployment (predates Go migration)
- Python-only framing throughout (was implicit that Python was primary)
- Long A2A adapter test command block (specific enough to live in docs/A2A_AGENTS.md; not a session-start concern)

**Added:**
- **Migration Goal** section — states the long-term Python→Go migration goal, explicit migration order (Bridge → Auth → Temporal/LLM → remove Python), and one-subsystem-per-task rule
- **Model Selection** section — Opus for planning/architecture, Sonnet for implementation/testing/debugging (was buried in Rules — Code as a one-liner)
- **Long Answers** section — long explanations must go to `docs/architecture-v2/`; return only path + summary in chat
- **Workflow** section — explicit plan → implement → test → commit → report cycle
- **Session Lifecycle — Mandatory** section — handover triggers, full procedure, handover file location (`docs/architecture-v2/NEXT_SESSION_HANDOVER.md`), and the recommendation to close and reopen Claude session. This consolidates the rule that was only in `go/CLAUDE.md`.
- **Tenant-Aware Design** section — explicit requirements: application_id flows through all features, Redis keys must include app ID, rate limiting and session caps are per-app
- `them-go-bridge` container added to Container Map
- Go source root and key Go packages added to Key Source Locations table
- Go bridge startup commands added to Common Commands
- Note in "Read These First" pointing to `go/CLAUDE.md` for Go work
- Rules — Testing: added `go/TEST_INDEX.md` requirement and pointer to Go trigger map in `go/CLAUDE.md`

**Preserved without change:**
- All Python trigger map entries (tests 01–35)
- E2E test instructions
- Temporal worker restart instructions
- Database schema quick reference
- Git workflow
- All Rules — Code, Documentation, Testing

---

### go/CLAUDE.md

**Removed (duplicated from root):**
- Session Context and Handover Rule section — this was a complete copy of the handover procedure. Replaced with a reference: "The project-wide session, handover, Git, secrets, migration-goal and tenant-aware rules in `../CLAUDE.md` are mandatory."
- "Use Opus for architecture/planning decisions, Sonnet for coding and QA" (now canonical in root)
- The note about secrets never appearing in logs remained because it is Go-specific (`cfg.SafeString()`)

**Added:**
- Opening reference line to `../CLAUDE.md` (mandatory, read first)
- **Rules — Architecture** section — Handler → Service → DAL rule, no SQL in handlers, no business rules in handlers, shared WS/SSE behavior through `internal/transport/`, all of which were implicit before
- **Rules — Route Ownership** section — explicit rule that route migration cannot be claimed based only on Traefik labels or dead code; requires verified live request in Go bridge logs. This addresses lesson learned during Wave 5 (Traefik labels not applied bug).
- Integration tests rule (build tag `integration`, require real Postgres+Redis) made explicit in Rules — Tests
- `ExecLua return type` and `AT TIME ZONE parsing` decisions added to Key Architectural Decisions table (lessons from Wave 5)
- Linux/Mac binary path section added alongside the existing Windows PowerShell block

**Preserved without change:**
- Full Package Map
- All Go trigger map entries
- All Rules — Documentation
- All Common Commands
- All Key Architectural Decisions (existing rows)
- Go-specific security rules (no third-party JWT, no ORM, context cancellation propagation, list endpoints return `[]` not `null`)

---

## Contradictions Resolved

| Before | After |
|---|---|
| Handover rule existed only in `go/CLAUDE.md` — Python-side work had no equivalent | Handover rule is canonical in root CLAUDE.md; `go/CLAUDE.md` references it |
| Model selection was a one-liner buried in "Rules — Code" in root | Promoted to a dedicated **Model Selection** section in root |
| Root CLAUDE.md implied Python was the primary system with no mention of Go migration goal | Root now has an explicit **Migration Goal** section |
| `go/CLAUDE.md` had no explicit rule against claiming route ownership without verified log hits | Added **Rules — Route Ownership** section |
| Handler/Service/DAL layering rule was implied but not stated | Added as first bullet in **Rules — Architecture** |
| Root CLAUDE.md had no Go container in the Container Map | `them-go-bridge` added |
| Root CLAUDE.md had no Go source entries in Key Source Locations | Go packages added |

---

## Paths Verified to Exist

| Path | Status |
|---|---|
| `docs/INDEX.md` | exists |
| `docs/ARCHITECTURE.md` | exists |
| `docs/SCHEMA.md` | exists |
| `docs/REDIS.md` | exists |
| `docs/ADAPTERS.md` | exists |
| `docs/A2A_AGENTS.md` | exists |
| `docs/A2A_REFERENCE.md` | exists |
| `docs/STATUS.md` | exists |
| `docs/LESSONS.md` | exists |
| `scripts/tests/INDEX.md` | exists |
| `go/TEST_INDEX.md` | exists |
| `docs/architecture-v2/lessons-learned.md` | exists |
| `docs/architecture-v2/implementation-status.md` | exists |
| `go/internal/admin/dal/` | exists |
| `go/internal/auth/` | exists |
| `go/internal/transport/` | exists |
| `go/internal/gate/` | exists |
| `go/internal/session/` | exists |
| `go/cmd/them/` | exists |

Note: `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` does not yet exist — it is the target file for the next handover, created by the session lifecycle procedure.

---

## No Application Code Changed

Only documentation files were modified:
- `/opt/docker/them/CLAUDE.md`
- `/opt/docker/them/go/CLAUDE.md`
- `/opt/docker/them/docs/architecture-v2/CLAUDE_GUIDES_ALIGNMENT_REPORT.md` (this file)
