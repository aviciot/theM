---
name: docs-update
description: Checklist for keeping docs/ correct when you add or change code. Invoke before committing any code change, and when writing or verifying an architecture doc.
---

# Docs update checklist

Rules live in `docs/DOCS_REBOOT_PLAN.md`. Short version:

1. **Find the owner:** `python3.12 scripts/docs_lint.py owners <changed paths>`. No owner → either the change is too small to document, or a doc needs a new `owns:` entry (never a new top-level file).
2. **Verify against code/tests, not other docs.** Old docs in `docs/_archive/` are hints only.
3. **Edit the owner doc in the same commit.** Change only what the code change affected.
4. **Bump `last_verified`** (today's date) only for what you actually re-checked.
5. **Non-obvious bug or behavior** → append to `docs/lessons/<area>.md` (or `docs/LESSONS.md` until split).
6. **Decision with trade-offs** → new numbered ADR in `docs/decisions/`; never edit an accepted ADR, supersede it.
7. **Run** `python3.12 scripts/docs_lint.py check && python3.12 scripts/docs_lint.py index`, and commit the regenerated `docs/INDEX.md`.
8. **Never** create `*_STATUS`, `*_RESULTS`, `HANDOVER_*` files. State goes in `docs/CURRENT.md`; results go in the commit message.

New doc: copy `docs/_templates/architecture.md` into one of `architecture/ reference/ guides/ decisions/ plans/ lessons/`.
