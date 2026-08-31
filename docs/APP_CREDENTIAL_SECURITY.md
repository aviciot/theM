# Application Layer — Credential Security
# Last updated: 2026-08-20

---

## Current State

| Secret | Column | Storage | Status |
|---|---|---|---|
| Access tokens | `them.access_tokens.token_hash` | SHA-256 hash, plaintext never stored | ✓ Correct |
| Orchestrator LLM keys | `them.orchestrators.llm_api_key_encrypted` | AES-GCM (`crypto.EncryptStored`) | ✓ Correct |
| Agent auth tokens | `them.agents.auth_token_encrypted` | AES-GCM | ✓ Correct |
| Canvas agent credential slots | `them.app_agent_bindings.credential_bindings` | AES-GCM | ✓ Correct |
| App runtime config | `them.applications.runtime_config` | Plaintext JSONB | ✓ No secrets (token hashes, numeric IDs, limits) |
| **App LLM provider keys** | `them.applications.provider_keys` | **Plaintext JSON** | ✗ Gap — must encrypt |

---

## Decisions

### 1. Encrypt `provider_keys` at rest

Use `crypto.EncryptStored` (AES-GCM, same key as orchestrator keys) on write; `crypto.DecryptStored` on read. The `fernetKey []byte` is already threaded through `router.go` and available to all other handlers — wire it into `NewApplicationsHandler` and `NewAppService`.

**Key hint:** extract last 4 chars of the **plaintext** key before encrypting, store as `{"anthropic": {"ct": "enc:...", "hint": "XXXX"}}`. This changes the JSONB schema — update DAL scan logic accordingly. The `GetProviderKeys` response continues returning `{provider, key_set, key_hint}` with no plaintext.

**Migration:** one-time re-encrypt pass for existing rows at startup or via a migration script — read plaintext, encrypt, write back.

### 2. `runtime_config` — no change needed

`blocked_tokens` are already-hashed opaque strings. `blocked_user_ids` are numeric. `rate_limit_rpm`, `max_concurrent_sessions`, `session_timeout_minutes` are limits, not secrets. No encryption required.

### 3. Agent-runtime LLM key resolution order

When a canvas agent LLM step executes, the agent-runtime resolves the API key in this order:

```
(1) app_agent_bindings.credential_bindings["anthropic_api_key"]  — per-binding override
(2) applications.provider_keys["anthropic"]                       — per-app key (decrypted)
(3) ANTHROPIC_API_KEY env var                                     — platform fallback
```

The agent-runtime already has `ApplicationID` in `InvocationContext` and DB access. It adds a `loadAppAPIKey(ctx, appID)` query after `parseInvocationContext()`, decrypts using `THE_M_SECRET_KEY` (already in container env), and stores the result in `InvocationContext.AnthropicAPIKey`. The `anthropicLLMFactory.NewProvider()` prefers this field over the platform env key. The key is never logged.

---

## Implementation Order

1. **Wire `fernetKey` into `AppService`** — `NewApplicationsHandler(db, cache, fernetKey)` → `NewAppService(dal, cache, fernetKey)`
2. **Update `SetProviderKey`** — encrypt before storing; extract hint first
3. **Update `GetPlaintextProviderKey`** — decrypt after fetching (used by `TestLLM`)
4. **Update `GetProviderKeys`** — read hint from new JSONB structure
5. **Migration** — re-encrypt existing plaintext rows
6. **Agent-runtime** — `loadAppAPIKey`, `InvocationContext.AnthropicAPIKey`, factory priority

Files changed: `go/internal/admin/service/applications.go`, `go/internal/admin/applications.go`, `go/internal/admin/router.go`, `go/cmd/agent-runtime/main.go`, `go/internal/agentgen/` (InvocationContext).

---

## Do NOT

- Encrypt `runtime_config` — no secrets, adds complexity for no gain
- Hash provider keys — LLM calls need the plaintext at runtime
- Store provider keys in `app_agent_bindings` — use `credential_bindings` only for per-binding overrides, not the default app key
- Log the decrypted key or any fragment of it at any log level
- Skip the platform fallback — dev/test environments may not set per-app keys
