# Voice Chat Pipeline — Implementation Plan

**Goal:** `POST /apps/{slug}/voice/chat` — client sends audio, server returns audio.
Pipeline: STT → orchestrator (LLM) → TTS. Reuses `execution.Lifecycle` exactly like WS/SSE.

---

## How it fits the existing model

```
WS EP   → Lifecycle.Admit → Lifecycle.Start → event bus → stream text tokens back
SSE EP  → Lifecycle.Admit → Lifecycle.Start → event bus → stream text tokens back
Voice EP→ Lifecycle.Admit → Lifecycle.Start → event bus → collect full reply → TTS → stream audio
```

The voice handler IS an execution handler — it just has audio I/O wrapping the same orchestrator call.
The `/transcribe` and `/tts` endpoints stay as standalone dev/test tools.

---

## File changes

### 1. `go/internal/voice/pgx.go` — add OrchestratorID to query

`EPVoiceConfig` needs the orchestrator ID so the handler can look up the TTS provider key separately
from the STT key (they may use different providers).

```go
// Add to EPVoiceConfig:
OrchestratorID string // app_orchestrators.id — for provider key lookup
```

SQL change in `PgxLoader.LoadVoiceConfig`:
```sql
SELECT ep.enabled, a.enabled,
       COALESCE(ep.access_policy->>'mode','token'),
       a.id, a.tenant_id,
       COALESCE(ao.id::text, ''),           -- NEW: orchestrator ID
       COALESCE(ao.transcription_provider,''),
       COALESCE(ao.transcription_model,''),
       COALESCE(ao.tts_provider,''),
       COALESCE(ao.tts_voice,''),
       COALESCE(ao.llm_model,'')
FROM them.entry_points ep
JOIN them.applications a ON a.id = ep.application_id
LEFT JOIN them.app_orchestrators ao ON ao.id = ep.app_orchestrator_id
WHERE ep.tenant_id = $1::uuid
  AND ep.slug = $2
  AND ep.entry_point_type = 'voice'
```

Also scan `&cfg.OrchestratorID` in the `rows.Scan(...)` call.

---

### 2. `go/internal/execution/lifecycle.go` — remove voice EP guard

Currently line ~160 rejects voice EPs with 404. Remove that guard — the voice handler
now goes through Lifecycle properly:

```go
// DELETE these lines:
if resolvedCfg.EPType == "voice" {
    return nil, admitErr(AdmitErrNotFound)
}
```

---

### 3. `go/internal/voice/handler.go` — add Chat method

Add dependencies to `Handler`:

```go
type Handler struct {
    loader   ConfigLoader
    keys     KeyResolver
    auth     Authenticator
    lc       *execution.Lifecycle   // NEW
    bus      event.Bus              // NEW
    tenantID string
    logger   *slog.Logger
}
```

Update `NewHandler` signature to accept `lc *execution.Lifecycle` and `bus event.Bus`.

Add route in `Routes()`:
```go
r.Post("/{slug}/voice/chat", h.Chat)
```

**`Chat` method — full pipeline:**

```go
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
    const timeout = 60 * time.Second
    ctx, cancel := context.WithTimeout(r.Context(), timeout)
    defer cancel()

    slug := chi.URLParam(r, "slug")

    // 1. Parse audio from multipart
    if err := r.ParseMultipartForm(32 << 20); err != nil {
        http.Error(w, `{"error":"invalid multipart"}`, http.StatusBadRequest)
        return
    }
    f, header, err := r.FormFile("audio")
    if err != nil {
        http.Error(w, `{"error":"audio field required"}`, http.StatusBadRequest)
        return
    }
    defer f.Close()
    audioBytes, _ := io.ReadAll(f)

    // 2. Load voice config + resolve STT key
    cfg, sttKey, err := h.resolveAndAuth(r, slug, "stt")
    if err != nil {
        h.writeErr(w, err); return
    }

    // 3. STT — transcribe
    transcript, err := Transcribe(ctx, cfg.STTProvider, cfg.STTModel, sttKey,
        audioBytes, header.Filename, header.Header.Get("Content-Type"))
    if err != nil {
        http.Error(w, `{"error":"transcription failed"}`, http.StatusBadGateway)
        return
    }
    if transcript == "" {
        http.Error(w, `{"error":"empty transcript"}`, http.StatusBadRequest)
        return
    }

    // 4. Lifecycle.Admit (same as WS/SSE)
    rawToken := extractRawToken(r)
    req := execution.ExecutionRequest{
        TenantID:    h.tenantID,
        EPSlug:      slug,
        RawToken:    rawToken,
        UserMessage: []domain.ContentPart{{Type: "text", Text: transcript}},
    }
    handle, err := h.lc.Admit(ctx, req)
    if err != nil {
        // Map AdmitError to HTTP status
        writeAdmitError(w, err); return
    }
    defer h.lc.Release(context.Background(), handle)  // gate + session cleanup

    // 5. Subscribe to event bus BEFORE Start (ordering invariant)
    evCh, termCh, unsub := h.bus.Subscribe(ctx, handle.ContextID, 64)
    defer unsub()

    // 6. Lifecycle.Start
    input := temporal.WorkflowInput{
        OrchestratorName: handle.EPConfig.OrchestratorName,
        UserMessage:      req.UserMessage,
    }
    if _, err := h.lc.Start(ctx, handle, input); err != nil {
        http.Error(w, `{"error":"orchestrator failed to start"}`, http.StatusInternalServerError)
        return
    }

    // 7. Collect LLM reply — drain events until "done" or timeout
    replyText := collectReply(ctx, evCh, termCh)
    if replyText == "" {
        http.Error(w, `{"error":"empty reply from orchestrator"}`, http.StatusBadGateway)
        return
    }

    // 8. Resolve TTS key (may be a different provider from STT)
    _, ttsKey, err := h.resolveAndAuth(r, slug, "tts")
    if err != nil {
        http.Error(w, `{"error":"TTS not configured"}`, http.StatusBadRequest)
        return
    }

    // 9. TTS — stream audio back
    w.Header().Set("Content-Type", "audio/mpeg")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("X-Transcript", transcript) // useful for client debugging
    if _, err := StreamTTS(ctx, w, cfg.TTSProvider, cfg.TTSVoice, cfg.TTSModel,
        ttsKey, replyText); err != nil {
        h.logger.Warn("voice: tts stream failed", "ep_slug", slug, "error", err)
    }
}
```

**`collectReply` helper** — concatenates text tokens, stops on `done` or `error`:

```go
func collectReply(ctx context.Context, evCh, termCh <-chan event.Event) string {
    var sb strings.Builder
    for {
        select {
        case <-ctx.Done():
            return sb.String()
        case ev, ok := <-termCh:
            if !ok { return sb.String() }
            if ev.Type == "done" { return sb.String() }
            return sb.String() // error event — return whatever we have
        case ev, ok := <-evCh:
            if !ok { return sb.String() }
            if ev.Type == "token" {
                var tok struct{ Text string `json:"text"` }
                if json.Unmarshal(ev.Payload, &tok) == nil {
                    sb.WriteString(tok.Text)
                }
            }
        }
    }
}
```

**`writeAdmitError` helper** — maps `*execution.AdmitError` to HTTP status:

```go
func writeAdmitError(w http.ResponseWriter, err error) {
    var ae *execution.AdmitError
    if !errors.As(err, &ae) {
        http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
        return
    }
    switch ae.Code {
    case execution.AdmitErrUnauthorized:
        http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
    case execution.AdmitErrForbidden:
        http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
    case execution.AdmitErrNotFound:
        http.Error(w, `{"error":"entry point not found"}`, http.StatusNotFound)
    case execution.AdmitErrCapExceeded, execution.AdmitErrQueueFull:
        http.Error(w, `{"error":"capacity exceeded"}`, http.StatusServiceUnavailable)
    case execution.AdmitErrRateLimited:
        http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
    default:
        http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
    }
}
```

---

### 4. `go/internal/execution/lifecycle.go` — expose Release

`Release` is currently called internally by WS/SSE at connection close. Voice needs to call it
after collecting the reply. Check if it's already exported; if not, export it:

```go
// Release cleans up gate + session. Call on any early exit after Admit succeeds.
func (lc *Lifecycle) Release(ctx context.Context, h *ExecutionHandle) {
    if lc.sessions != nil {
        _ = lc.sessions.End(ctx, h.SessionID, h.EPConfig.EPSlug, h.EPConfig.AppID)
    }
    if h.gateAdmitted {
        _ = lc.gate.Release(ctx, h.gateCfg)
    }
    if lc.recorder != nil && h.runCreated {
        // Mark done — voice runs complete synchronously
        _ = lc.recorder.UpdateRunStatus(ctx, h.RunID, domain.RunStatusCompleted, "")
    }
}
```

---

### 5. `go/cmd/them/main.go` — pass Lifecycle and Bus to voice.Handler

```go
// Replace current voice handler construction (section 19b):
voiceLoader := voice.NewPgxLoader(database.Pool())
voiceAppsSvc := admin.NewApplicationsHandler(adminDB, adminCache, adminFernetKey).Svc()
voiceHandler := voice.NewHandler(
    voiceLoader,
    voiceAppsSvc,
    authenticator,
    lifecycle,   // NEW — pass the shared *execution.Lifecycle
    eventBus,    // NEW — pass the shared event.Bus
    tenantctx.BootstrapTenantID,
    log,
)
```

`lifecycle` and `eventBus` are already constructed earlier in `main.go` for WS/SSE — just pass them through.

---

### 6. Canvas — no change needed

Voice EP already connects to an orchestrator in the canvas the same way as WS/SSE.
`ep.app_orchestrator_id` is already set in the DB when the canvas is published.
`PgxLoader` already joins `app_orchestrators` via that FK.

---

## Tests needed

| Test | File | What it proves |
|---|---|---|
| `TestChat_EPNotFound` | `internal/voice/handler_test.go` | Loader error → 404 |
| `TestChat_NoSTTProvider` | `internal/voice/handler_test.go` | STT not configured → 400 |
| `TestChat_TokenEPNoAuth` | `internal/voice/handler_test.go` | Token EP + no auth → 401 |
| `TestChat_OrchestratorReply` | `internal/voice/handler_test.go` | Full happy path with fake Lifecycle + fake bus that emits token+done events → response body is non-empty |
| `TestChat_EmptyTranscript` | `internal/voice/handler_test.go` | STT returns "" → 400 |
| `TestChat_Timeout` | `internal/voice/handler_test.go` | Bus never sends done → 60s timeout, partial or empty response |
| `TestLifecycle_VoiceEPAdmitted` | `internal/execution/lifecycle_test.go` | Voice EP type no longer rejected by Admit |

For the handler tests, inject a `fakeBus` that immediately emits a `token` event + `done` event on subscribe,
and a `fakeLifecycle` (or use `NewLifecycleWithRecorder` with all fakes) that succeeds through Start.

---

## Client usage (after implementation)

```
POST /apps/ep-voice-1/voice/chat
Authorization: Bearer <token>          # if token-mode EP
Content-Type: multipart/form-data

audio=<audio file>

→ 200 audio/mpeg stream (TTS of LLM reply)
X-Transcript: "book me a flight to paris"   # header with transcript for debugging
```

One call. No client-side orchestration loop needed.

---

## What stays unchanged

- `/voice/transcribe` and `/voice/tts` — remain as standalone endpoints for testing/custom clients
- WS and SSE handlers — unchanged
- All voice runtime config (STT/TTS provider/model/voice) set in Runtime panel — unchanged
- Canvas EP→orchestrator wiring — unchanged
