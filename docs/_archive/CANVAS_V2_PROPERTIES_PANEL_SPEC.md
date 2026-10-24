# Canvas v2 — Properties Panel Design Spec

**Scope:** `frontend/src/app/admin/applications/page.tsx` — `CanvasBuilderView.renderPropertiesPanel()` (currently lines ~3091–3255), plus the `EpNodeData` type (line 583), `canvasToDoc` serializer (line ~627) and `docToCanvas` loader (line ~660).

**Goal:** Complete, non-overwhelming Orchestrator + Entry-Point properties panels for the ReactFlow v2 canvas. Add per-EP LLM overrides stored in `EPInstance.config`; keep the shared brain (system prompt, defaults) on the orchestrator's `ComponentInstance.config`. Backend compiler currently ignores unknown EP `config` fields — frontend is free to design the shape.

---

## 0. Non-negotiable pre-work (the big gotcha)

`EPInstance.config` **exists in the API type but is currently thrown away by the canvas.** Before ANY UI work, the round-trip must carry it:

1. **`EpNodeData` type (line 583)** — add `config: Record<string, unknown>;`
   ```ts
   interface EpNodeData { _kind: 'ep'; instance_id: string; slug: string;
     protocol: 'websocket'|'sse'|'webrtc'|'a2a'|'voice'; label: string;
     config: Record<string, unknown>;   // NEW
     _error?: boolean; _shake?: boolean; _errorMsg?: string; }
   ```
2. **`canvasToDoc` (line ~627)** — serialize config:
   ```ts
   entry_points.push({ instance_id: n.id, slug: d.slug, protocol: d.protocol,
     root: rootByEp.get(n.id) ?? '', config: d.config ?? {} });
   ```
3. **`docToCanvas` (line ~660)** — hydrate config on load:
   ```ts
   data: { _kind: 'ep', instance_id: ep.instance_id, slug: ep.slug,
     protocol: ep.protocol, label: EP_META[ep.protocol]?.title ?? ep.protocol,
     config: ep.config ?? {} }
   ```
4. **Drop-new-EP path (line ~3078)** — initialize `config: {}` on the new node's data.
5. **`EPInstance` in `lib/api.ts` (line 436)** — add `config?: Record<string, unknown>;` (matches the doc-level schema already described in the task).

Without these five edits, every EP override the user sets is silently lost on save. Do them first, verify a save/reload round-trips one dummy key, then build the UI.

---

## 1. Orchestrator panel — exact field list

All fields write to `OrchNodeData.config` (JSON key in parens) **except** `display_name`, which is a top-level node field mirrored into `config.display_name` by `canvasToDoc`.

### Section A — Identity (always visible, no header)
| Field | Control | Key | Notes |
|---|---|---|---|
| Instance ID | read-only chip | `instance_id` | existing |
| Display Name | text input | `display_name` | existing |
| Delegatable | status pill (read-only, derived from edges) | — | existing; keep at bottom of this section |

### Section B — Brain (always visible, header "BRAIN")
| Field | Control | Key | Notes |
|---|---|---|---|
| System Prompt | textarea (min 90px) | `system_prompt` | existing — the single shared brain |

### Section C — Default Model (always visible, header "DEFAULT MODEL")
This is the **fallback** LLM used when an EP has no override.
| Field | Control | Key | Notes |
|---|---|---|---|
| Provider | `<select>` | `llm_provider` | options: anthropic / openai / groq / gemini |
| Model | `<select>` + custom escape | `llm_model` | options driven by provider (see §8 model map); "Custom…" reveals a text input, matching the old-canvas pattern at lines 1477–1517 |

Upgrade from the current free-text inputs (lines 3119–3124) to the provider→model dependent selects. On provider change, auto-set model to the first model for that provider (same behavior as old canvas line 1477).

### Section D — Loop Tuning (collapsible, header "LOOP TUNING", collapsed by default)
2×2 grid, existing controls:
| Field | Control | Key | Default |
|---|---|---|---|
| Max Iterations | number | `max_iterations` | 10 |
| Max Parallel Tools | number | `max_parallel_tools` | 5 |
| History Window | number | `history_window` | 20 |
| Budget Tokens | number (empty = none) | `budget_tokens` | null |

### Section E — Voice (conditional + collapsible, header "VOICE (STT / TTS)")
**Render only when this orchestrator is the root of at least one `voice` EP.** Detection in-panel:
```ts
const isVoiceRoot = edges.some(e =>
  e.target === selectedNode.id &&
  (nodes.find(n => n.id === e.source)?.data as EpNodeData | undefined)?.protocol === 'voice'
);
```
Fields (mirrors old canvas lines 1640–1762), all under `config`:
| Field | Control | Key |
|---|---|---|
| STT Provider | select (openai / groq) | `stt_provider` |
| STT Model | text (auto-filled: openai→`whisper-1`, groq→`whisper-large-v3`) | `stt_model` |
| TTS Provider | select | `tts_provider` |
| TTS Voice | select/text | `tts_voice` |

**Decision:** STT/TTS live on the **orchestrator**, not the EP. Rationale: voice config is graph-shaped (one voice pipeline per orchestrator brain), and putting it on the EP would fragment it if two voice EPs hit the same orchestrator. The EP panel only surfaces a read-only note pointing here (see §2, voice case). API keys for STT/TTS are **not** entered here — bind via `secret_bindings`/provider config, never raw keys in the JSONB doc.

---

## 2. Entry-Point panel — exact field list

Top-level fields (`slug`) stay on node data; everything else writes to `EpNodeData.config`. JSON keys below are the `EPInstance.config` shape.

### Section A — Identity (always visible, no header)
| Field | Control | Key | Notes |
|---|---|---|---|
| Instance ID | read-only chip | `instance_id` | existing |
| Slug | text input + validity hint | `slug` | existing; regex `^[a-z0-9_-]{1,64}$` |
| Protocol | read-only text | `protocol` | existing |
| Root Orchestrator | read-only (derived from edge) | — | existing |

### Section B — LLM Override (always visible, header "LLM OVERRIDE") — NEW
The headline feature. See §4 for the "use orchestrator default" mechanics.
| Field | Control | Key | Notes |
|---|---|---|---|
| Override toggle | checkbox / switch | `llm_override_enabled` (bool) | OFF by default |
| Provider | `<select>` (disabled when toggle off) | `llm_provider` | same options as orch |
| Model | `<select>` + custom (disabled when toggle off) | `llm_model` | provider-driven map §8 |

When toggle is OFF: show a muted line "Uses orchestrator default: `{provider} / {model}`" resolved from the root orchestrator's config. Do **not** write `llm_provider`/`llm_model` keys while OFF (see §4).

### Section C — Access (always visible, header "ACCESS")
| Field | Control | Key | Default |
|---|---|---|---|
| Access Mode | select: token / public | `access_mode` | token |

### Section D — Capacity (collapsible, header "CAPACITY", collapsed by default)
| Field | Control | Key | Notes |
|---|---|---|---|
| Conversation Token Limit | number | `conversation_token_limit` | per-session cap; empty = unset |
| Queue Timeout (s) | number | `queue_timeout_seconds` | empty = unset |
| Queue Message | text | `queue_message` | placeholder "All agents are busy, please wait…" |

(Deliberately dropped `maxConcurrentSessions` from the panel — it is an app/EP runtime-manager concern set elsewhere (`db/024_ep_queue.sql`, runtime layer), not a canvas-authoring field. Keep the canvas focused on graph shape. If needed later, add here as `max_concurrent_sessions`.)

### Section E — Protocol-specific (conditional)
- **`voice`**: header "VOICE", single read-only note: "STT/TTS configured on the root orchestrator." Link/scroll affordance optional. No STT fields here (see §1E decision).
- **`a2a`**: header "A2A", two read-only rows:
  - Skill ID: `{d.slug}` (mono, cyan) — the slug *is* the exposed A2A skill id.
  - Note: "budget_tokens from the root orchestrator applies to A2A calls."
- **`websocket` / `sse` / `webrtc`**: no extra section. (Optional future: `webrtc` note that it needs a realtime-capable model, mirroring old-canvas line 1808.)

---

## 3. Collapsible vs always-visible summary

| Node | Always visible | Collapsible (collapsed default) | Conditional |
|---|---|---|---|
| Orchestrator | Identity, Brain, Default Model | Loop Tuning | Voice (only if root of a voice EP) |
| Entry Point | Identity, LLM Override, Access | Capacity | Voice note / A2A section (by protocol) |

**Collapsible implementation:** the panel already uses section-header `<div>`s (e.g. line 3105). Add a lightweight local `useState<Record<string,boolean>>` for open sections keyed by section id, and a header row that is a `<button>` with a chevron (`expand_more` / `chevron_right` material icon) toggling it. Keep it dead simple — no animation library. Collapsed state is view-only (not persisted to the doc).

Rule of thumb: put the fields a user touches on almost every node in "always visible"; hide the tuning/capacity knobs that most apps leave at defaults.

---

## 4. "Use orchestrator default" override mechanics

State model (single boolean gate + two value keys):

- `config.llm_override_enabled: boolean` — the source of truth for the toggle.
- `config.llm_provider`, `config.llm_model` — only meaningful when the toggle is ON.

Behavior:
1. **Toggle OFF (default):** provider/model selects are disabled and greyed. Panel shows resolved default text (read root orch config: `rootOrch.config.llm_provider` / `llm_model`). On save, **delete** `llm_provider`/`llm_model` from EP config (don't persist stale overrides). Keep `llm_override_enabled: false` or omit it.
2. **Toggle ON:** enable selects. If provider/model are empty, seed them from the resolved orchestrator default so the user starts from a sensible value rather than blank. Persist both keys.
3. **Resolving the default for display:** find the root orchestrator via the same edge lookup already used for "Root Orchestrator" (line 3193–3195), then read its `config`. If no root is connected yet, show "Connect an orchestrator to see the default."

Why a boolean gate instead of "empty = inherit": explicit intent. An empty model string is ambiguous (did they clear it or inherit?); the toggle makes inheritance unambiguous and lets the muted default-preview render correctly. It also gives the future Go compiler a clean signal (`llm_override_enabled === true` → apply override) without guessing.

Helper to keep writes clean:
```ts
function setEpConfig(instanceId: string, patch: Record<string, unknown>, remove: string[] = []) {
  setNodes(ns => ns.map(n => {
    if (n.id !== instanceId) return n;
    const cfg = { ...(n.data.config as Record<string, unknown>), ...patch };
    for (const k of remove) delete cfg[k];
    return { ...n, data: { ...n.data, config: cfg } };
  }));
  setIsDirty(true);
}
```
Toggling OFF → `setEpConfig(id, { llm_override_enabled: false }, ['llm_provider','llm_model'])`.

---

## 5. Voice EP considerations

- STT/TTS config is **orchestrator-owned** (§1E). The Voice section on the orchestrator panel is gated on "is root of a voice EP".
- The EP's Voice section is a one-line pointer, not a form — avoids duplicate/conflicting voice config when multiple voice EPs share one orchestrator.
- A voice EP may still set an LLM override (e.g. a low-latency model) — the override section applies normally.
- Never store STT/TTS API keys in `config`. Use `secret_bindings` on the orchestrator component. The old canvas took raw keys (lines 1675, 1762) — do **not** carry that into v2; it would land plaintext secrets in the JSONB definition doc. This is a hard rule (CLAUDE.md: "Never commit or print real secrets").

## 6. A2A EP considerations

- `skill_id` is **not a separate field** — it equals the slug. Surface it read-only so the author understands the slug's second role as the exposed A2A skill id.
- `budget_tokens` is an orchestrator-level cap; note that it applies to A2A calls. Do not duplicate a budget field on the EP.
- A2A EPs commonly want a higher-quality model (the user's "claude-opus for quality" case) — the LLM Override section serves this directly.

## 7. Gotchas / implementer notes

1. **Do the §0 serialization edits first.** UI without them silently loses all EP config on save. This is the single most important item.
2. **`fieldStyle` is defined twice** in this file (lines 2921 and 3473) plus a third at 4611 in other views. The v2 `CanvasBuilderView` uses the one at ~2921 — reuse it; don't invent new input styles. For `<select>` add `cursor: 'pointer'` (matches line 1573).
3. **Number inputs:** follow existing coercion (`Number(e.target.value)`, empty→`null` for budget/limits) at lines 3129–3141. Empty-string vs 0 matters for "unset" semantics — treat `''` as delete-the-key for capacity fields.
4. **Design tokens:** orchestrator accent = `C.purple`, EP accent = `C.cyan`, voice = `C.amber`, section header pattern already at line 3105 (`fontSize:12, fontWeight:700, textTransform:'uppercase', letterSpacing:'0.06em'`). Do not hardcode hex; use `C.*`.
5. **280px width:** the 2×2 grid (`gridTemplateColumns:'1fr 1fr', gap:8`, line 3126) fits. Selects and textareas are full-width already. Keep labels at `fontSize:11 C.textMuted`.
6. **`setIsDirty(true)`** must fire on every EP config mutation (the current EP panel calls it; keep parity) so Save/Validate/Publish buttons light up.
7. **Provider/model list is duplicated** (old canvas line 1420–1423). Extract a module-level `MODELS_BY_PROVIDER` const and reuse in both orchestrator Default Model and EP Override to avoid drift. Keep model ids exact (see §8) — the worker matches on these strings.
8. **Backend is out of scope but forward-compatible:** the boolean-gated shape (`llm_override_enabled` + provider/model) is what the Go compiler will later read from `entry_points[].config`. Unknown keys are ignored today, so shipping the frontend now is safe.

## 8. Provider → model map (source of truth, mirror of existing old-canvas list)

```ts
const MODELS_BY_PROVIDER: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
  openai:    ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'o1', 'o3-mini'],
  groq:      ['llama-3.3-70b-versatile', 'llama-3.1-8b-instant', 'mixtral-8x7b-32768'],
  gemini:    ['gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-pro'],
};
```
Provider options order: `anthropic, openai, groq, gemini`. "Custom…" sentinel reveals a free-text model input (pattern at old-canvas lines 1490–1517) for models not in the list.

---

## 9. Final EP `config` JSON shape (reference)

```jsonc
{
  "llm_override_enabled": true,
  "llm_provider": "anthropic",
  "llm_model": "claude-opus-4-8",
  "access_mode": "token",
  "conversation_token_limit": 50000,
  "queue_timeout_seconds": 60,
  "queue_message": "All agents are busy, please wait…"
  // slug is top-level on EPInstance, not in config
  // voice/a2a add no config keys (STT/TTS on orch; skill_id == slug)
}
```

## 10. Final Orchestrator `config` JSON shape (reference)

```jsonc
{
  "display_name": "Support Brain",
  "system_prompt": "You are…",
  "llm_provider": "anthropic",     // the DEFAULT model
  "llm_model": "claude-sonnet-4-6",
  "max_iterations": 10,
  "max_parallel_tools": 5,
  "history_window": 20,
  "budget_tokens": null,
  // only when root of a voice EP:
  "stt_provider": "openai",
  "stt_model": "whisper-1",
  "tts_provider": "openai",
  "tts_voice": "alloy"
  // API keys NEVER here — use secret_bindings
}
```
