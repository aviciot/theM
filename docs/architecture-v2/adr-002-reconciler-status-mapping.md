# ADR-002 — Reconciler: Temporal status mapping decisions

**Status:** Accepted  
**Date:** 2026-07-20  
**Context:** Phase 11b (run reconciliation)

---

## Context

The reconciler maps Temporal `WorkflowExecutionStatus` enum values to the
`them.runs.status` CHECK constraint values. Two mappings required explicit
decisions because the DB schema has no direct equivalent.

### DB `status` CHECK constraint (as of 2026-07-20)

```sql
CHECK (status = ANY (ARRAY[
  'running', 'completed', 'failed',
  'canceled', 'cancelled', 'stopped'
]))
```

Note: both `canceled` (single-L, Go canonical) and `cancelled` (double-L,
legacy Python) are accepted. The reconciler writes `canceled` (single-L)
exclusively.

### Temporal status values

| Temporal constant | Value | Description |
|---|---|---|
| RUNNING | 1 | Workflow is executing |
| COMPLETED | 2 | Workflow finished successfully |
| FAILED | 3 | Workflow threw an error |
| CANCELED | 4 | Workflow was cancelled by the client |
| TERMINATED | 5 | Workflow was forcibly terminated by an operator |
| CONTINUED_AS_NEW | 6 | Workflow continued as a new execution |
| TIMED_OUT | 7 | Workflow exceeded its execution timeout |

---

## Decision 1: TERMINATED → `"stopped"`

**Chosen:** `TERMINATED` → `"stopped"`

**Rationale:**
- `"stopped"` is already in the DB schema (Python uses it for `max_iterations=0` runs)
- `TERMINATED` is an operator-initiated action, not a run failure. Mapping it to
  `"failed"` would conflate two distinct operational events and make them
  indistinguishable in dashboards and alerts.
- `"stopped"` conveys "external intervention ended the run" accurately.

**Alternative rejected:** `TERMINATED` → `"failed"`  
This would lose information about whether the run failed on its own or was
killed by an operator. The user explicitly requires that "distinct statuses be
preserved where the schema supports them."

---

## Decision 2: TIMED_OUT → `"failed"`

**Chosen:** `TIMED_OUT` → `"failed"`

**Rationale:**
- The DB schema has no `"timed_out"` status. Adding one requires a migration and
  CHECK constraint change.
- A timed-out workflow did not complete successfully — mapping it to `"failed"` is
  the most operationally actionable choice (triggers the same alerts, same client
  error handling).

**Information loss:** Yes. `"failed"` does not distinguish between a Python
exception and a Temporal schedule-to-close timeout. This is acceptable for the
current phase. If the distinction becomes important, add a `failure_reason`
column in a future migration and document it here.

**Alternative rejected:** `TIMED_OUT` → new `"timed_out"` status  
Deferred to a future phase. Requires schema migration, frontend changes, and
possibly different API responses.

---

## Decision 3: CONTINUED_AS_NEW → no update

**Chosen:** No DB update; treat as still running.

**Rationale:** `CONTINUED_AS_NEW` means the workflow started a new execution to
avoid history size limits. The run is logically active. Marking it as completed
or failed would be incorrect.

---

## Decision 4: NOT_FOUND → no update

**Chosen:** Log warning + increment metric; leave DB status as `"running"`.

**Rationale:** A `NotFound` response from Temporal may mean:
- History retention has expired (default 7 days in Temporal Cloud, configurable)
- Wrong Temporal namespace (misconfiguration)
- Python-native run whose Workflow ID is `ctx-{contextID}`, not the run UUID

Silently mapping `NotFound` to `"failed"` would incorrectly fail valid runs
after history retention expires, and would permanently fail all Python-native
runs on the Go reconciler.

**Operational guidance:** See `runbook-reconciler.md` for how to handle rows
permanently stuck in `"running"` when history retention is confirmed to have
expired.
