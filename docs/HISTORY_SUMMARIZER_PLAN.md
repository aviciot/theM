# History + Summarizer Implementation Plan (Go worker)
# Authored by Opus 4.8 — 2026-08-17

## 0. Key findings / constraints

1. **`task_messages.role` CHECK is `('user','agent','system')`** — NOT `assistant`/`tool`. Map `assistant→agent`, `tool→agent`. Store true canonical role inside JSONB envelope for lossless round-trip.
2. **`task_messages` has no `context_id` column.** History-by-context requires: `task_messages JOIN tasks ON tasks.id = task_messages.task_id WHERE tasks.context_id = $1`.
3. **`task_messages` requires unique `(task_id, seq)`** and `parts JSONB NOT NULL`.
4. **`WithHistoryLoader` / `WithCheckpointer` exist but are wired nowhere.** Greenfield.
5. **`provider_keys` stores encrypted `enc:...` Fernet ciphertext.** `loadProviderKey` currently returns raw ciphertext — broken. Fix via `crypto.DecryptStored`. Summarizer key path must decrypt.
6. **All DB columns already exist** (`memory_enabled`, `summarize_every_n_calls`, `memory_raw_fallback_n`, `summarizer_provider`, `summarizer_model`, `summarizer_api_key_encrypted`, `history_window`, `budget_tokens`). No new migration needed.
7. **`Config` in orchestrator.go** carries no memory/summarizer fields yet.

---

## 1. New files to create

### `go/internal/history/pgx.go` (new package)
Concrete `HistoryLoader` + `CheckpointWriter` + `SummaryStore` backed by pgxpool.

- `type Store struct { pool *pgxpool.Pool; logger *slog.Logger }`
- `func NewStore(pool, logger) *Store`
- `func (s *Store) LoadHistory(ctx, contextID string, limit int) ([]domain.Message, error)` — query in §3
- `func (s *Store) WriteMessage(ctx, contextID, runID string, msg domain.Message) error` — design in §4
- `func (s *Store) LoadSummary(ctx, contextID string) (string, error)` — returns latest summary text
- `func (s *Store) SaveSummary(ctx, contextID, runID, summary string) error` — persists summary as system message
- Helpers: `canonicalToDBRole`, `resolveRootTaskID`, `nextSeq`

### `go/internal/history/pgx_test.go`
Unit tests: role mapping, seq assignment, summary round-trip, empty-history path.

### `go/internal/summarizer/summarizer.go` (new package)
In-process summarizer LLM call (no microservice).

- `type Summarizer struct { provider llm.Provider; model string; logger *slog.Logger }`
- `func New(provider llm.Provider, model string, logger *slog.Logger) *Summarizer`
- `func (s *Summarizer) Summarize(ctx context.Context, prior string, msgs []domain.Message) (string, error)`
  - Builds prompt from prior summary + older messages
  - Calls `provider.Stream` with tools=nil, drains text_delta → string
  - MaxTokens: 1024

### `go/internal/summarizer/summarizer_test.go`
Uses `llm.MockProvider`; asserts drained summary includes prior + older msgs.

### `go/internal/orchestrator/summary.go` (same package, new file)
Trigger logic and context replacement:

- `type Summarizer interface { Summarize(ctx, prior string, msgs []domain.Message) (string, error) }`
- `type SummaryStore interface { LoadSummary(ctx, contextID string) (string, error); SaveSummary(ctx, contextID, runID, summary string) error }`
- `type SummaryConfig struct { MemoryEnabled bool; SummarizeEveryN int; RawFallbackN int; HistoryWindow int }`
- `func (o *Orchestrator) WithSummarizer(s Summarizer, store SummaryStore, cfg SummaryConfig) *Orchestrator`
- `func (o *Orchestrator) maybeSummarize(ctx, contextID, runID string, history []domain.Message) []domain.Message`

---

## 2. Changes to existing files

### `go/internal/orchestrator/orchestrator.go`
Add to `Config`:
```go
MemoryEnabled        bool
SummarizeEveryNCalls int
MemoryRawFallbackN   int
SummarizerProvider   string
SummarizerModel      string
```
Add to `Orchestrator` struct: `summarizer Summarizer`, `summaryStore SummaryStore`, `summaryCfg SummaryConfig`.

In `Run`, after history load, before `buildMessages`:
```go
history = o.maybeSummarize(ctx, contextID, runID, history)
```

Checkpoint user message right after history finalization (currently missing):
```go
if o.checkpointer != nil {
    _ = o.checkpointer.WriteMessage(ctx, contextID, runID, userMsg)
}
```

### `go/internal/temporal/workerconfig/loader.go`
Add to `RunConfig`:
```go
SummarizerProvider string
SummarizerModel    string
SummarizerAPIKey   string // decrypted
```
Add to `orchQ` SELECT: `ao.memory_enabled, ao.summarize_every_n_calls, ao.memory_raw_fallback_n, ao.summarizer_provider, ao.summarizer_model, ao.summarizer_api_key_encrypted`.

Populate `orchestrator.Config` fields: `MemoryEnabled`, `SummarizeEveryNCalls`, `MemoryRawFallbackN`.

Summarizer key priority: app `provider_keys[summarizerProvider]` → row `summarizer_api_key_encrypted`. Both must be decrypted via `crypto.DecryptStored`. Add `fernetKey []byte` to `PgxLoader`; inject from `config.Load()`.

Fix existing `loadProviderKey` to also decrypt (currently returns ciphertext — latent bug).

### `go/cmd/worker/main.go`
Add `historyStore *history.Store` to `runOrchestratorFactory`. Create once after pool:
```go
historyStore := history.NewStore(pool, log)
```

In `Build`:
```go
orch := orchestrator.New(...).
    WithHistoryLoader(f.historyStore).
    WithCheckpointer(f.historyStore)

if cfg.OrchestratorConfig.MemoryEnabled && cfg.SummarizerProvider != "" {
    sumProvider := f.resolveSummarizerProvider(cfg)
    sum := summarizer.New(sumProvider, cfg.SummarizerModel, f.logger)
    orch = orch.WithSummarizer(sum, f.historyStore, orchestrator.SummaryConfig{...})
}
```

---

## 3. DB query — HistoryLoader

```sql
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
ORDER BY tm.id DESC
LIMIT $2
```
Reverse in Go to chronological order. Reconstruct canonical role from `parts.canonical_role` (fall back: `agent→assistant`).

---

## 4. CheckpointWriter design

For each message (user, assistant, tool-result, summary):
1. `taskID = resolveRootTaskID(ctx, contextID, runID)` — finds tasks row for this run
2. `dbRole = canonicalToDBRole(msg.Role)` — satisfies CHECK (`user`/`agent`/`system`)
3. JSONB envelope: `{ "canonical_role": "assistant", "parts": [...] }`
4. `seq = nextSeq(ctx, taskID)`
5. INSERT with `ON CONFLICT (task_id, seq) DO NOTHING` for idempotency

---

## 5. Summarizer design

**Trigger** in `maybeSummarize`: `cfg.MemoryEnabled && len(history) > cfg.HistoryWindow && cfg.SummarizeEveryNCalls > 0`.

**Split**: `older = history[:len-RawFallbackN]`, `recent = history[len-RawFallbackN:]`.

**Prompt**:
- System: `"You are a conversation summarizer. Produce a concise factual summary preserving user goals, decisions, entities, open questions, and state an assistant needs to continue. Output only the summary."`
- User: prior summary + older messages rendered as `role: text` lines

**Storage**: persisted as `role='system'` row with `{ "canonical_role":"system", "summary":true, "parts":[{"type":"text","text":"<summary>"}] }`. Loaded through same §3 query. No Redis dependency.

**Context replacement**: returns `[summaryMsg] ++ recent` — older raw turns dropped from LLM context this run but remain in DB for audit.

---

## 6. Open questions for implementation

- **O-1 (blocking):** Verify `them.tasks` row with `context_id` exists before first checkpoint. If not, `resolveRootTaskID` must INSERT one — check not-null columns on `them.tasks`.
- **O-2:** `loadProviderKey` decryption fix affects existing LLM key path — verify no caller depends on ciphertext being returned.
- **O-3:** v1 uses history length as trigger, not a persistent call counter. `summarize_every_n_calls` acts as accumulation threshold.
- **O-4:** Summarizer tokens not counted against `budget_tokens` in v1.

---

## 7. Test plan

| Package | Tests |
|---|---|
| `internal/history` | role mapping, seq, summary round-trip, empty history, integration (live PG) |
| `internal/summarizer` | mock provider drain, prior summary inclusion, ctx cancel |
| `internal/orchestrator` | maybeSummarize (disabled/nil/trigger), user message checkpointed |
| `internal/temporal/workerconfig` | new fields loaded, key priority, decryption |

Update `go/TEST_INDEX.md` in same commit (mandatory per CLAUDE.md).
