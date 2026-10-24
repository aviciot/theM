---
title: <Subsystem name>
type: architecture        # architecture | reference | guide | decision | plan | lessons
status: draft             # current | draft | superseded
owns:                     # code paths this doc describes (must exist)
  - go/internal/<pkg>/
last_verified: YYYY-MM-DD # date you confirmed this doc matches code + tests
---

# <Subsystem name>

## Purpose
One paragraph: what it does and why it exists.

## How it works
Main flow, step by step (add a Mermaid diagram if it helps). Name the real files/functions.

## Key invariants
Rules that must not break (tenancy, ordering, idempotency, security).

## Failure modes
What happens when X is down / slow / wrong.

## Where to change things
Table: "to do X, edit Y". Tests that cover this: see `go/TEST_INDEX.md`.

## Related
Links to other docs / ADRs / lessons.
