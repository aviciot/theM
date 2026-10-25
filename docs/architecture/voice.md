---
title: Voice (HTTP voice endpoints and LiveKit)
type: architecture
status: current
owns:
  - go/internal/voice/
  - agents/livekit_agent/
  - livekit/livekit.yaml
last_verified: 2026-10-25
---

# Voice

## Purpose
Lets a voice **entry point** of an app take audio in and give speech or text back. Two separate paths exist; only the first is wired into the Go bridge.

## How it works

### 1. HTTP voice endpoints (`go/internal/voice/`)
Mounted by `cmd/them/main.go` (section 19b) through `appsDispatcher`, for any path containing `/voice/`:

| Route (`/{tenant}/apps/{app}/{ep}/voice/…`) | Does |
|---|---|
| `chat` | audio → STT → orchestrator (LLM) → TTS → audio stream. The voice EP must be connected to an orchestrator in the canvas. |
| `stream` | same, streamed |
| `transcribe` | audio → text only |
| `tts` | text → audio only |

Steps in `resolveAndAuth`: tenant slug → UUID (bearer-token tenant overrides it) → load the voice config for `(tenant, app, ep)` → reject if the EP or app is disabled → if `access_mode` is `token`, require a valid bearer token → decrypt the app's provider key for STT/TTS (`GetPlaintextProviderKey`).
Providers (`service.go`): STT `openai` or `groq` (Whisper); TTS `openai` or `elevenlabs`. Keys are per application ("Runtime settings"); a missing key returns 400. Voice config is stored on `app_orchestrators` and edited via `PATCH …/orchestrators/{id}/voice` and `POST …/test-voice` (`admin/applications_llm.go`). `chat` records the run through `runrecorder` and the event bus like a normal run.

### 2. LiveKit (profile `voice`)
Compose services `livekit` (SFU, Traefik path `/livekit`, UDP 7882) and `livekit-agent` (`agents/livekit_agent/main.py`, 99 lines): joins a room and forwards the user's text to the bridge over WebSocket. Needs `LIVEKIT_API_KEY/SECRET` and `OPENAI_API_KEY` in `.env`.

## Key invariants
- Provider keys are decrypted only inside the request and never returned.
- `access_mode: token` must reject requests without a valid token.

## Failure modes
- No STT/TTS key stored → 400 "no API key stored for provider".
- Voice EP without a connected orchestrator → `chat` fails.
- Provider error → 502 (`transcription failed`, `orchestrator error`).

## Known gaps
Backlog F-118…F-121: no admission/rate limit/session cap on voice calls, uploads read fully into memory, the LiveKit agent uses an old route, and keys are passed as plain env vars.

## Where to change things
Handlers: `voice/handler.go`; provider calls: `voice/service.go`; config loading: `voice/pgx.go`. Tests: `go test ./internal/voice/...` (7 tests; STT/TTS provider calls are not covered).
