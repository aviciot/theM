# Voice Go Implementation Plan
# Last updated: 2026-08-27

Migrate voice (STT + TTS) from dead Python bridge to Go. All config/keys live at app runtime level, not canvas.

---

## 1. API Keys — Provider Key Extension

**File:** `go/internal/admin/service/applications.go`

- Add `"elevenlabs"` to `validProviders` map (currently: `openai`, `anthropic`, `groq`)
- Add `"groq"` if not already present (needed for Groq Whisper STT)
- Voice key resolution: `openai` key → Whisper STT + OpenAI TTS; `elevenlabs` key → ElevenLabs TTS
- No DB schema change — `applications.provider_keys` JSONB already handles arbitrary provider names
- Keys set via existing `PUT /admin/applications/{id}/provider-keys/{provider}`

---

## 2. Voice Config — App Orchestrator PATCH Endpoint

**New route:** `PATCH /admin/applications/{id}/orchestrators/{orch_id}/voice`

**Handler file:** `go/internal/admin/applications.go` — add `PatchOrchestratorVoice` handler

**Service file:** `go/internal/admin/service/applications.go` — add `PatchOrchestratorVoice(ctx, appID, orchID, VoiceConfig) error`

**DAL file:** `go/internal/admin/dal/applications.go` — add `PatchOrchestratorVoice(ctx, appID, orchID string, cfg VoiceConfig) error`

```
SQL: UPDATE them.app_orchestrators
     SET voice_enabled=$3, transcription_provider=$4, transcription_model=$5,
         tts_enabled=$6, tts_provider=$7, tts_voice=$8
     WHERE id=$1::uuid AND application_id=$2::uuid
```

**VoiceConfig struct:**
```go
type VoiceConfig struct {
    VoiceEnabled          bool    `json:"voice_enabled"`
    TranscriptionProvider string  `json:"transcription_provider"` // "openai" | "groq"
    TranscriptionModel    string  `json:"transcription_model"`    // "whisper-1" | "whisper-large-v3"
    TTSEnabled            bool    `json:"tts_enabled"`
    TTSProvider           string  `json:"tts_provider"`           // "openai" | "elevenlabs"
    TTSVoice              string  `json:"tts_voice"`              // "alloy" etc or ElevenLabs voice ID
}
```

- Does NOT store API keys — those go in `applications.provider_keys`
- Publishes cache-invalidation on `them:ep:config:changed` for all EPs bound to this orchestrator
- Mount in `applications.go` Routes: `app.Patch("/orchestrators/{orch_id}/voice", h.PatchOrchestratorVoice)`

---

## 3. Voice HTTP Endpoints — Go Package

**New package:** `go/internal/voice/`

### Files

#### `go/internal/voice/handler.go`
- `Handler` struct with: `db VoiceQuerier`, `keyResolver KeyResolver`, `auth transport.Authenticator`
- `POST /apps/{slug}/voice/transcribe` — multipart `audio` field → `{"text": "...", "provider": "...", "model": "..."}`
- `POST /apps/{slug}/voice/tts` — JSON body `{"text": "..."}` → `audio/mpeg` streaming response
- Auth: extract bearer token; if EP access_mode == "token", validate token (reject 401 if absent/invalid)
- Config resolution: query `app_orchestrators` via EP slug → voice config; resolve API key from `applications.provider_keys`

#### `go/internal/voice/service.go`
- `Transcribe(ctx, provider, model, apiKey string, audioBytes []byte, filename string) (string, error)`
  - `openai`: POST to `https://api.openai.com/v1/audio/transcriptions` (multipart)
  - `groq`: POST to `https://api.groq.com/openai/v1/audio/transcriptions` (multipart, OpenAI-compatible)
- `StreamTTS(ctx, provider, voice, model, apiKey, text string) (io.ReadCloser, string, error)`
  - `openai`: POST `https://api.openai.com/v1/audio/speech` → streaming mp3
  - `elevenlabs`: POST `https://api.elevenlabs.io/v1/text-to-speech/{voice}/stream` → streaming mp3
  - Returns reader, content-type, error

#### `go/internal/voice/dal.go`
- `VoiceQuerier` interface
- `QueryVoiceConfig(ctx, epSlug string) (*VoiceConfigRow, error)` — joins `entry_points → app_orchestrators → applications`
- Returns: EP type, access_mode, voice/tts config columns, app_id (for provider key lookup)
- `QueryProviderKey(ctx, appID, provider string) ([]byte, error)` — fetches encrypted key from `applications.provider_keys`

### Wiring in `go/cmd/them/main.go`

```go
// appsDispatcher extended:
case strings.Contains(r.URL.Path, "/voice/"):
    voiceApps.ServeHTTP(w, r)
```

- Construct `voice.Handler` with DB pool + key decryptor (reuse existing AES-GCM from admin service)
- Mount: `voiceHandler.Routes()` returns chi router for `/{slug}/voice/transcribe` and `/{slug}/voice/tts`

### Remove 501 block

**File:** `go/internal/execution/lifecycle.go` lines 156-161  
Remove the voice EP type check that returns `AdmitErrNotImplemented`. Voice EPs are HTTP-only — they never reach the WS/SSE execution lifecycle.

---

## 4. RuntimeView — Voice Config Panel

**File:** `frontend/src/app/admin/applications/components/RuntimeView.tsx`

For each orchestrator section, when any bound EP has `entry_point_type === 'voice'`:
- Show a **Voice** subsection (collapsed by default) alongside the LLM subsection
- **Display only** (read-only):
  - STT: provider + model (from `app_orchestrator.transcription_provider/model`)
  - TTS: provider + voice (from `app_orchestrator.tts_provider/tts_voice`)
  - Set via canvas (show note: "Configure provider/voice in canvas")
- **Editable** (API keys — same pattern as LLM provider keys panel):
  - `openai` key status + set/delete button
  - `elevenlabs` key status + set/delete button
  - Calls existing `PUT /admin/applications/{id}/provider-keys/{provider}`

**Frontend API types** (`frontend/src/lib/api.ts`):
- `AppOrchestrator` already has `voice_enabled`, `transcription_provider`, `transcription_model`, `tts_enabled`, `tts_provider`, `tts_voice` — no changes needed

---

## 5. Tests

### Go unit tests

**`go/internal/voice/handler_test.go`**
- `TestTranscribeEndpoint_TokenEPRequiresAuth` — returns 401 when no bearer token
- `TestTranscribeEndpoint_PublicEPAllowed` — proceeds without token
- `TestTranscribeEndpoint_NoVoiceConfig` — returns 400 when orchestrator has no STT configured
- `TestTTSEndpoint_EmptyText` — returns 400

**`go/internal/voice/service_test.go`**
- `TestTranscribe_OpenAI` — mock HTTP server, validates multipart upload
- `TestStreamTTS_OpenAI` — mock HTTP, validates streaming response
- `TestStreamTTS_ElevenLabs` — mock HTTP, validates xi-api-key header

**`go/internal/admin/applications_wave8_test.go`** (extend existing file)
- `TestPatchOrchestratorVoice_OK` — happy path
- `TestPatchOrchestratorVoice_InvalidOrch` — returns 404

### TEST_INDEX.md update

Add rows for:
- `internal/voice/` package (new)
- `PatchOrchestratorVoice` in `internal/admin/`

---

## Implementation Order

1. `go/internal/admin/service/applications.go` — add `elevenlabs` to validProviders  
2. `go/internal/admin/dal/applications.go` + `go/internal/admin/service/applications.go` + `go/internal/admin/applications.go` — PatchOrchestratorVoice  
3. `go/internal/voice/` — new package (dal, service, handler)  
4. `go/cmd/them/main.go` — wire voice handler into appsDispatcher  
5. `go/internal/execution/lifecycle.go` — remove 501 voice block  
6. `frontend/src/app/admin/applications/components/RuntimeView.tsx` — voice panel  
7. Tests + `go/TEST_INDEX.md`  
8. Commit all together  
