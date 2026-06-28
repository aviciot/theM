# Knowledge Base Plan
# This is the whitelist of allowed documentation files.
# Never create ad-hoc markdown files outside this list.

## Allowed files

| File | Purpose |
|---|---|
| `/CLAUDE.md` | Session guide — always maintain |
| `docs/INDEX.md` | Doc index — always maintain |
| `docs/ARCHITECTURE.md` | System architecture |
| `docs/SCHEMA.md` | DB schema reference |
| `docs/REDIS.md` | Redis key space |
| `docs/ADAPTERS.md` | Agent adapter contracts |
| `docs/AUTH.md` | Authentication flows |
| `docs/FLOWS.md` | End-to-end request flows |
| `docs/STATUS.md` | Living issue tracker |
| `docs/LESSONS.md` | Append-only lessons |
| `docs/KNOWLEDGE_BASE_PLAN.md` | This file |

## Rules
- Never create phase files, update logs, or analysis artifacts
- Those belong in git history and docs/STATUS.md
- When in doubt: update an existing doc, not a new one
