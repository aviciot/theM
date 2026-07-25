# Wave 5 — Admin Tokens + Sessions Service Layer
# Go Gateway Migration — Refactor 4 (implementation plan, PLANNING ONLY)
# Author: Go planning session, 2026-07-24
# Module: github.com/aviciot/them  ·  source: /opt/docker/them/go/

---

## 0. TL;DR / Design Decisions

1. **Two new services** — `TokenService` (fills the existing seam in `service/tokens.go`) and a
   new `SessionAdminService` (`service/sessions.go`). Both follow the exact `AgentService`/`RunService`
   pattern: consumer-defined interfaces, constructor DI, typed errors, HTTP-free.
2. **Token revocation is done by hash, not raw token.** The admin path never sees the plaintext after
   creation, so `TokenService` must NOT call `auth.Cache.Revoke` (which hashes a raw token). Instead
   it invalidates directly through the admin `Cache` interface: `Del("them:session:token:"+hash)` +
   `Publish("them:token:revoked", hash)` — byte-identical to what Python's `invalidate_token(token_hash)`
   does. The DAL therefore returns `token_hash` on the read-before-mutate path.
3. **Sessions reuse the live `*session.Store`.** The admin package gets a narrow `SessionReader`
   interface (List/Get/SignalDisconnect) satisfied structurally by `*session.Store`. Two new methods
   (`ListEPSessions`, `ListAppSessions`) and one signature change (`SignalDisconnect` returns delivered
   count) must be added to `internal/session/session.go` — the only file touched outside `internal/admin`.

---

## 1. Scope

### 1.1 Routes moved to Go (tokens)

Python source: `app/routers/admin_tokens.py` — prefix `/admin/tokens`, mounted under `/api/v1`.

| Method | Path | Python handler | Status codes |
|---|---|---|---|
| GET | `/api/v1/admin/tokens` (opt `?user_id=<int>`) | `list_tokens` | 200 |
| POST | `/api/v1/admin/tokens` | `create_token` | 201, 404 (orch not found) |
| GET | `/api/v1/admin/tokens/{token_id}` | `get_token` | 200, 404 |
| PATCH | `/api/v1/admin/tokens/{token_id}` | `update_token` | 200, 404 |
| DELETE | `/api/v1/admin/tokens/{token_id}` | `delete_token` | 204, 404 |

### 1.2 Routes moved to Go (sessions)

Python source: `app/routers/admin_sessions.py` — prefix `/admin/sessions`, mounted under `/api/v1`.

| Method | Path | Python handler | Status codes |
|---|---|---|---|
| GET | `/api/v1/admin/sessions?app_id=<uuid>` OR `?ep_slug=<slug>` | `list_sessions` | 200, 400 (neither provided) |
| POST | `/api/v1/admin/sessions/{session_id}/disconnect` | `disconnect_session` | 200, 400 (bad uuid), 404 (not found) |

### 1.3 Explicitly OUT of scope

No changes to: DB schema / FKs, Python worker, orchestration, A2A, WS/SSE runtime handlers, auth-service,
`internal/auth/token_cache.go` (bearer validation is untouched), any route outside the two prefixes above.
No dashboard routes, no runtime-limit routes, no `admin_applications` session views.

---

## 2. Gap Analysis (Python needs vs Go DAL/Store today)

### 2.1 Tokens — DAL gap

The `internal/admin/dal/` package has **zero** token methods today (confirmed: only agents / orchestrators /
applications / runs). Everything below is NEW. `them.access_tokens` columns (from `db/001_schema.sql`,
NO changes allowed):

```
id UUID PK, token_hash TEXT UNIQUE NOT NULL, label TEXT NOT NULL, user_id INTEGER NOT NULL,
orchestrator_id UUID NULL (FK→orchestrators ON DELETE CASCADE), enabled BOOL default true,
expires_at TIMESTAMPTZ NULL, last_used_at TIMESTAMPTZ NULL, created_at TIMESTAMPTZ default now()
```

| Python behavior | Go DAL method needed | Notes |
|---|---|---|
| `list_tokens` (order by created_at desc, opt user_id filter) | `ListTokens(ctx, userID *int64) ([]Token, error)` | never returns `token_hash` in JSON |
| validate orch exists when scoped | `OrchestratorExists(ctx, orchID string) (bool, error)` | POST returns 404 if false |
| `create_token` (insert hash+label+user+orch+expiry, enabled=true) | `CreateToken(ctx, in TokenCreateRow) (Token, error)` | returns full row (for `created_at`, `id`); hash generated in service |
| `get_token` | `GetToken(ctx, id string) (Token, error)` | 404 via `dal.IsNoRows` |
| `update_token` (patch label/enabled/expires) + return hash for invalidation | `UpdateToken(ctx, id string, patch TokenPatchRow) (updatedHash string, out Token, err error)` | need hash back to invalidate cache |
| `delete_token` (hard delete) + return hash | `DeleteToken(ctx, id string) (deletedHash string, err error)` | HARD delete (Python `db.delete`), NOT soft — differs from agents |

**Note (behavior difference to preserve):** token DELETE is a **hard row delete** in Python
(`await db.delete(row)`), unlike agents/orchestrators which soft-delete via `enabled=false`. Do NOT
apply the soft-delete pattern here.

### 2.2 Sessions — Store gap

`internal/session/session.go` (`*session.Store`) already has: `Get`, `CountEPSessions`,
`CountAppSessions` (both prune ghosts), `SignalDisconnect(ctx, sid) error`, `SubscribeControl`.

| Python behavior | Go Store method today | Gap |
|---|---|---|
| `list_ep_sessions(ep_slug)` → `[]session_id` | only `CountEPSessions` (returns int) | **MISSING** — add `ListEPSessions(ctx, epSlug) ([]string, error)` |
| `list_app_sessions(app_id)` → `[]session_id` | only `CountAppSessions` | **MISSING** — add `ListAppSessions(ctx, appID) ([]string, error)` |
| `get(session_id)` → dict | `Get(ctx, sid) (*SessionInfo, error)` | present |
| `signal_disconnect` returns `receivers` (int) → `signal_delivered` | `SignalDisconnect` returns only `error` | **SIGNATURE CHANGE** — return `(int, error)` for delivered count |

The two new list methods should reuse the existing prune-and-return Lua pattern (SMEMBERS + shadow-key
check) so admin listing does not surface ghost session IDs — but to keep the change minimal and match
Python (which does a plain `SMEMBERS`), the simplest faithful approach is a plain SMEMBERS wrapper.
**Decision:** add a `luaPruneAndList` mirroring `luaPruneAndCount` that returns the live member array,
so listing is ghost-free (an improvement over Python that costs nothing and matches the count semantics
already used at the gate). Handler then fetches `Get` per id, dropping ids that 404 (matches Python loop).

### 2.3 Redis Publish receiver count

`cache.AdminCacheClient.Publish` and `cache.SessionRedisClient.Publish` currently return only `error`.
rueidis `PUBLISH` returns an integer (subscriber count). For `signal_delivered` parity we need that int.
**Decision:** do NOT change the shared `Cache.Publish` signature (used by agents/orchestrators). Instead
the session disconnect path goes through `session.Store.SignalDisconnect`, whose Redis client
(`session.RedisClient.Publish`) also returns only `error` today. Rather than widen that interface,
`SignalDisconnect` will return `(delivered bool, err error)` where `delivered` is `err == nil` — Python's
`signal_delivered` is truthy when publish succeeded (it returns the raw receiver int, but the Go contract
test asserts the field is present + JSON-boolean-or-int truthy). See §9.3 for the exact parity assertion.
**If exact int parity is required**, add `PublishN(ctx, channel, payload) (int64, error)` to
`session.RedisClient` + `cache.SessionRedisClient`; this is flagged as the only optional interface
widening and is called out in §12 Blockers as a decision the implementer confirms against the contract test.

---

## 3. `service.Dal` interface additions

Add the following block to the `Dal` interface in `internal/admin/service/service.go`. `*dal.DB` will
satisfy it once the DAL methods in §5.1 exist (structural — no explicit `implements`).

```go
	// Tokens
	ListTokens(ctx context.Context, userID *int64) ([]dal.Token, error)
	GetToken(ctx context.Context, id string) (dal.Token, error)
	OrchestratorExists(ctx context.Context, orchID string) (bool, error)
	CreateToken(ctx context.Context, in dal.TokenCreateRow) (dal.Token, error)
	UpdateToken(ctx context.Context, id string, patch dal.TokenPatchRow) (hash string, out dal.Token, err error)
	DeleteToken(ctx context.Context, id string) (hash string, err error)
```

Sessions do NOT go through `service.Dal` (they read Redis, not Postgres). They use a separate
`SessionReader` interface declared in `service/sessions.go` (§5.4).

### 3.1 New DAL types (in `internal/admin/dal/dal.go`)

```go
// Token is the JSON representation of a them.access_tokens row.
// token_hash is NEVER serialized (json:"-"). Field names match Python TokenOut.
type Token struct {
	ID             string  `json:"id"`
	Label          string  `json:"label"`
	UserID         int64   `json:"user_id"`
	OrchestratorID *string `json:"orchestrator_id"`  // nullable → null in JSON
	Enabled        bool    `json:"enabled"`
	ExpiresAt      *string `json:"expires_at"`       // RFC3339 or null
	LastUsedAt     *string `json:"last_used_at"`     // RFC3339 or null
	CreatedAt      string  `json:"created_at"`
	TokenHash      string  `json:"-"`                // internal only, for invalidation
}

// TokenCreateRow is the persisted shape for CreateToken (hash computed in service).
type TokenCreateRow struct {
	TokenHash      string
	Label          string
	UserID         int64
	OrchestratorID *string
	ExpiresAt      *string  // ISO8601 string or nil; DAL casts $n::timestamptz
}

// TokenPatchRow carries the PATCH fields; nil pointer = field absent (leave unchanged).
type TokenPatchRow struct {
	Label     *string
	Enabled   *bool
	ExpiresAt *string
}

// TokenCreatedOut is TokenOut + one-time plaintext. Returned only from POST.
type TokenCreatedOut struct {
	Token         // embeds all TokenOut fields
	Plaintext string `json:"token"`
}
```

Nullable timestamp handling: scan into `*string` using `COALESCE(expires_at::text, '')` → empty means
NULL; DAL converts `""` back to a nil `*string` before building the `Token` value so JSON emits `null`
(matches Python `Optional[datetime]` → `null`). Same pattern already used for runs `ended_at`.

---

## 4. TokenService design (`internal/admin/service/tokens.go`)

Replace the current seam. Keep the `TokenGenerator` interface but give it a concrete method the auth
package can satisfy. The plan uses a **self-contained generator** to avoid importing `auth` into
`service` (auth imports would risk a cycle and pull `net/http`-adjacent deps). The generator lives in
`service/tokens.go` as the default impl.

```go
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"github.com/aviciot/them/internal/admin/dal"
)

// TokenGenerator issues an opaque bearer token and its storage hash.
type TokenGenerator interface {
	// Generate returns (plaintext, sha256HexHash, err). plaintext is shown once;
	// hash is what the DAL persists in them.access_tokens.token_hash.
	Generate(ctx context.Context) (plaintext string, hash string, err error)
}

// randTokenGenerator matches Python: secrets.token_urlsafe(32) + sha256 hex.
type randTokenGenerator struct{}

func (randTokenGenerator) Generate(_ context.Context) (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext := base64.RawURLEncoding.EncodeToString(b) // url-safe, no padding — matches token_urlsafe
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, hex.EncodeToString(sum[:]), nil
}

// TokenService owns access-token CRUD business logic.
type TokenService struct {
	dal   Dal
	cache Cache
	gen   TokenGenerator
}

func NewTokenService(d Dal, c Cache, g TokenGenerator) *TokenService {
	if g == nil {
		g = randTokenGenerator{}
	}
	return &TokenService{dal: d, cache: c, gen: g}
}
```

### 4.1 Methods

```go
func (s *TokenService) List(ctx context.Context, userID *int64) ([]dal.Token, error)
func (s *TokenService) Get(ctx context.Context, id string) (dal.Token, error)
func (s *TokenService) Create(ctx context.Context, in dal.TokenCreateRow, orchID *string) (dal.TokenCreatedOut, error)
func (s *TokenService) Update(ctx context.Context, id string, patch dal.TokenPatchRow) (dal.Token, error)
func (s *TokenService) Delete(ctx context.Context, id string) error
```

Behavior per method (parity-locked to `admin_tokens.py`):

- **List** — passthrough to `dal.ListTokens(ctx, userID)`. Return `[]dal.Token{}` never nil (Go rule:
  lists return `[]`). Order-by-created_at-desc is in the SQL.
- **Get** — `dal.GetToken`; on any DAL error return `ErrNotFound` (matches `_get_or_404`). (Consistent
  with existing `AgentService.Get` blanket-404 convention.)
- **Create** —
  1. If `orchID != nil`: `exists, _ := dal.OrchestratorExists(ctx, *orchID)`; if `!exists` return
     `&FieldError{Kind: ErrNotFound, Message: "Orchestrator "+*orchID+" not found"}` → 404.
  2. `plaintext, hash, err := s.gen.Generate(ctx)`; wrap err as generic 500.
  3. Set `in.TokenHash = hash`; `row, err := dal.CreateToken(ctx, in)`.
  4. Return `dal.TokenCreatedOut{Token: row, Plaintext: plaintext}`.
  5. No cache invalidation on create (Python doesn't invalidate on create — a brand-new token has no
     cached entry; matches Python exactly).
- **Update** —
  1. `hash, out, err := dal.UpdateToken(ctx, id, patch)`; if `dal.IsNoRows(err)` → `ErrNotFound`.
  2. `s.invalidate(ctx, hash)` (see §7). Matches Python calling `invalidate_token(row.token_hash)`.
  3. Return `out`.
- **Delete** —
  1. `hash, err := dal.DeleteToken(ctx, id)`; if `dal.IsNoRows(err)` → `ErrNotFound`.
  2. `s.invalidate(ctx, hash)`. Matches Python.
  3. Return nil.

```go
func (s *TokenService) invalidate(ctx context.Context, hash string) {
	if s.cache == nil || hash == "" {
		return
	}
	_ = s.cache.Del(ctx, "them:session:token:"+hash)        // L2 evict
	_ = s.cache.Publish(ctx, "them:token:revoked", hash)    // cross-pod L1 evict
}
```

**Why not `auth.Cache.Revoke`:** it takes the raw token and hashes internally; admin only holds the hash.
Re-implementing the two Redis ops directly through the injected `Cache` keeps `service` free of an `auth`
import and is byte-identical to Python's `invalidate_token`.

---

## 5. DAL implementation (`internal/admin/dal/tokens.go`, NEW file)

Follow `dal/runs.go` structure (const col list + scan helper + methods). Key SQL:

```go
const tokenSelectCols = `
	id::text, label, user_id,
	COALESCE(orchestrator_id::text, ''),
	enabled,
	COALESCE(expires_at::text, ''),
	COALESCE(last_used_at::text, ''),
	created_at::text,
	token_hash`

// scanToken maps '' → nil for nullable *string fields so JSON emits null.
```

Methods:

- `ListTokens(ctx, userID *int64)` — two query forms (with/without `WHERE user_id=$1`), `ORDER BY created_at DESC`. Return `[]Token{}` (non-nil).
- `GetToken(ctx, id)` — `WHERE id=$1::uuid`, single row, scanToken.
- `OrchestratorExists(ctx, orchID)` — `SELECT EXISTS(SELECT 1 FROM them.orchestrators WHERE id=$1::uuid)`.
- `CreateToken(ctx, in)` — `INSERT ... (token_hash,label,user_id,orchestrator_id,expires_at,enabled) VALUES ($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::timestamptz,true) RETURNING <tokenSelectCols>`; scanToken on the returning row.
- `UpdateToken(ctx, id, patch)` — build a COALESCE-style update that only changes provided fields:
  ```sql
  UPDATE them.access_tokens SET
    label      = COALESCE($2, label),
    enabled    = COALESCE($3, enabled),
    expires_at = CASE WHEN $4 THEN $5::timestamptz ELSE expires_at END
  WHERE id=$1::uuid
  RETURNING <tokenSelectCols>
  ```
  Pass `patch.Label` (nil-able → `*string`), `patch.Enabled` (`*bool`), and for expires: a bool
  "expiresProvided" plus the string value (pgx binds `*string`/`*bool` as NULL when nil). Return
  `(scannedRow.TokenHash, scannedRow, nil)`; propagate `pgx.ErrNoRows` unwrapped so the service can call
  `dal.IsNoRows`.
- `DeleteToken(ctx, id)` — `DELETE FROM them.access_tokens WHERE id=$1::uuid RETURNING token_hash`;
  scan hash; on no-rows return `("", pgx.ErrNoRows)`. (HARD delete — see §2.1 note.)

### 5.4 SessionAdminService design (`internal/admin/service/sessions.go`, NEW)

Sessions read Redis via the live `*session.Store`, not Postgres. Declare a narrow consumer interface:

```go
package service

import "context"

// SessionReader is the admin service's view of the session store. *session.Store
// satisfies it structurally after the two List methods + SignalDisconnect signature
// change land in internal/session/session.go.
type SessionReader interface {
	ListEPSessions(ctx context.Context, epSlug string) ([]string, error)
	ListAppSessions(ctx context.Context, appID string) ([]string, error)
	Get(ctx context.Context, sessionID string) (*SessionSnapshot, error)
	SignalDisconnect(ctx context.Context, sessionID string) (delivered bool, err error)
}
```

**Type-coupling caveat:** `session.Store.Get` returns `*session.SessionInfo`, and `service` must NOT
import `internal/session` if that would create a cycle. It does not (session does not import admin), so
importing `session` into `service` is allowed. **Decision:** to avoid leaking a transport type into the
admin service and to keep the interface self-owned, define `SessionReader` in terms of a service-local
`SessionSnapshot` (a plain `map[string]any` alias or a struct). But `*session.Store.Get` returns
`*session.SessionInfo`, so `*session.Store` would NOT structurally satisfy an interface whose `Get`
returns `*SessionSnapshot`.

Resolution (chosen, simplest, zero cycle): `service` imports `internal/session` for the concrete type,
and `SessionReader.Get` returns `*session.SessionInfo`:

```go
import "github.com/aviciot/them/internal/session"

type SessionReader interface {
	ListEPSessions(ctx context.Context, epSlug string) ([]string, error)
	ListAppSessions(ctx context.Context, appID string) ([]string, error)
	Get(ctx context.Context, sessionID string) (*session.SessionInfo, error)
	SignalDisconnect(ctx context.Context, sessionID string) (bool, error)
}
```

`*session.Store` satisfies this exactly once §6.2 lands. `service` importing `session` is a new but legal
edge (`session` imports only stdlib + slog; no reverse edge to `admin` or `service`).

```go
type SessionAdminService struct {
	sessions SessionReader
}

func NewSessionAdminService(r SessionReader) *SessionAdminService { return &SessionAdminService{sessions: r} }

// ListResult mirrors Python's {"sessions":[...], "count":N} body.
type SessionListResult struct {
	Sessions []*session.SessionInfo `json:"sessions"`
	Count    int                    `json:"count"`
}
```

Methods:

```go
// List returns sessions for exactly one of appID / epSlug. Caller guarantees one is set.
func (s *SessionAdminService) ListByApp(ctx context.Context, appID string) (SessionListResult, error)
func (s *SessionAdminService) ListByEP(ctx context.Context, epSlug string) (SessionListResult, error)
func (s *SessionAdminService) Disconnect(ctx context.Context, sessionID string) (delivered bool, err error)
```

- **ListByApp/ListByEP** — call `ListAppSessions`/`ListEPSessions` to get ids, then `Get` each; skip ids
  whose `Get` returns `ErrSessionNotFound` (matches Python `if data is not None`). Build
  `SessionListResult{Sessions: non-nil-slice, Count: len}`. `Sessions` must be `[]` not null.
- **Disconnect** — call `s.sessions.Get(ctx, id)`; if `ErrSessionNotFound` return `ErrNotFound` (→ 404);
  else `delivered, err := s.sessions.SignalDisconnect(ctx, id)`; return `(delivered, err)`.
  (The uuid-format 400 check stays in the handler — HTTP request-shape validation.)

---

## 6. Files to change / add

### 6.1 New files
| File | Contents |
|---|---|
| `internal/admin/dal/tokens.go` | token SQL + scan + 6 DAL methods (§5) |
| `internal/admin/service/sessions.go` | `SessionReader`, `SessionAdminService`, `SessionListResult` (§5.4) |
| `internal/admin/tokens.go` | `TokensHandler` (thin HTTP) (§8.1) |
| `internal/admin/sessions.go` | `SessionsHandler` (thin HTTP) (§8.2) |

### 6.2 Edited files
| File | Change |
|---|---|
| `internal/admin/service/service.go` | add 6 token methods to `Dal` interface (§3) |
| `internal/admin/dal/dal.go` | add `Token`, `TokenCreateRow`, `TokenPatchRow`, `TokenCreatedOut` types (§3.1) |
| `internal/admin/service/tokens.go` | replace seam with real `TokenService` + generator (§4) |
| `internal/admin/router.go` | construct + mount `TokensHandler` and `SessionsHandler`; add `SessionReader` param to `BuildRouter` (§8.3) |
| `internal/session/session.go` | add `ListEPSessions`, `ListAppSessions`, `luaPruneAndList`; change `SignalDisconnect` to return `(bool, error)` (§2.2) |
| `internal/ws/handler.go`, `internal/sse/handler.go` | ONLY if they call `SignalDisconnect` — adjust call sites to new signature (grep first; they use `SubscribeControl`, not `SignalDisconnect`, so likely NO change) |
| `cmd/them/main.go` | pass `sessionStore` into `admin.BuildRouter` (§8.3) |
| `TEST_INDEX.md` | new test rows + count + trigger map (same commits) |
| `docs/architecture-v2/implementation-status.md` | route map: tokens + sessions now Go-owned |
| `go/CLAUDE.md` trigger map | add `internal/admin/dal/tokens.go`, `internal/admin/service/sessions.go`, `internal/session/session.go` (SignalDisconnect) rows |

**Verify before editing ws/sse:** run
`grep -rn "SignalDisconnect" go/internal/` — only the session package + admin should reference it. If
ws/sse do not, the signature change is contained to `internal/session` + the new admin session service.

---

## 7. Cache invalidation matrix

| Operation | Redis keys touched | Rationale |
|---|---|---|
| Token **create** | none | new token, nothing cached yet (matches Python) |
| Token **update** (PATCH) | `Del them:session:token:{hash}` + `Publish them:token:revoked {hash}` | enable/disable/relabel must evict L2 + all pods' L1 |
| Token **delete** | `Del them:session:token:{hash}` + `Publish them:token:revoked {hash}` | revoked token must stop validating immediately |
| Session **disconnect** | `Publish them:sess:control:{session_id} "disconnect"` (via `session.Store`) | cross-replica WS/SSE close, code 4000 |
| Session **list** | none | read-only |

All key prefixes already exist and are documented (`them:session:token:`, `them:token:revoked`,
`them:sess:control:`). NO new Redis keys → no `docs/REDIS.md` addition required (confirm during impl).

---

## 8. Handler rewire plan

### 8.1 `internal/admin/tokens.go` (new — mirrors `agents.go` style)

```go
type TokensHandler struct { svc *service.TokenService }

func NewTokensHandler(db DBQuerier, cache CacheInvalidator) *TokensHandler {
	return &TokensHandler{svc: service.NewTokenService(dal.NewDB(db), cache, nil)} // nil → default generator
}

func (h *TokensHandler) Routes(r chi.Router) {
	r.Get("/tokens", h.List)
	r.Post("/tokens", h.Create)
	r.Get("/tokens/{token_id}", h.Get)
	r.Patch("/tokens/{token_id}", h.Update)
	r.Delete("/tokens/{token_id}", h.Delete)
}
```

- **List** — parse `?user_id` (`strconv.ParseInt`; absent → nil pointer). 200 + `[]Token`.
- **Create** — decode `TokenCreate` body `{label,user_id,orchestrator_id?,expires_at?}`; build
  `dal.TokenCreateRow` + `orchID *string`. On service err use `writeServiceError` (404 for orch-missing
  and generic 500). Success → **201** + `TokenCreatedOut` (includes plaintext `token`). Set `Location:
  /api/v1/admin/tokens/{id}`.
- **Get** — 200 or 404.
- **Update** — decode `{label?,enabled?,expires_at?}` into `TokenPatchRow` (all `*` fields). 200 or 404.
- **Delete** — 404 if missing, else **204 No Content** (empty body — match Python `204`).

Body shapes must match Python: request `orchestrator_id` is a UUID string (or null); `expires_at` is an
ISO8601 string (or null). Response `TokenOut` fields exactly: `id,label,user_id,orchestrator_id,enabled,
expires_at,last_used_at,created_at`; POST adds `token`.

### 8.2 `internal/admin/sessions.go` (new)

```go
type SessionsHandler struct { svc *service.SessionAdminService }

func NewSessionsHandler(r service.SessionReader) *SessionsHandler {
	return &SessionsHandler{svc: service.NewSessionAdminService(r)}
}

func (h *SessionsHandler) Routes(r chi.Router) {
	r.Get("/sessions", h.List)
	r.Post("/sessions/{session_id}/disconnect", h.Disconnect)
}
```

- **List** — read `?app_id` and `?ep_slug`. If both empty → 400 `{"error":"app_id or ep_slug required"}`.
  Prefer `app_id` when both present (Python `if app_id: elif ep_slug:`). Return
  `{"sessions":[...],"count":N}` (200).
- **Disconnect** — `session_id := chi.URLParam`; validate uuid (`uuid.Parse`) → 400 `"Invalid session_id"`
  on failure. Call service; 404 `"Session not found or already ended"` on `ErrNotFound`; else 200
  `{"session_id": id, "signal_delivered": delivered}`.

### 8.3 `router.go` + `main.go`

`BuildRouter` gains one parameter (the session reader):

```go
func BuildRouter(
	db DBQuerier, cache CacheInvalidator, temporal TemporalSignaler,
	sessions service.SessionReader,             // NEW
	jwtMiddleware func(http.Handler) http.Handler, logger *slog.Logger,
) http.Handler
```

Inside the existing `admin.Route("/admin", ...)` block add:
```go
tokens := NewTokensHandler(db, cache)
sessions := NewSessionsHandler(sessionReader)
...
tokens.Routes(a)
sessions.Routes(a)
```

`main.go` line ~271: `admin.BuildRouter(adminDB, adminCache, temporalSignaler, sessionStore, jwtMiddleware, log)`.
`sessionStore` is already constructed at line 90 and satisfies `service.SessionReader` after §6.2. Passing
nil is tolerated only if the handler guards it; but since the store always exists, pass it directly.
`admin_test.go` currently calls `admin.BuildRouter` — check: it constructs handlers directly, not via
BuildRouter (grep to confirm). If it does call BuildRouter, update those call sites to pass a fake or nil.

`admin.SessionReader` alias: add `type SessionReader = service.SessionReader` in `middleware.go` next to
the existing `CacheInvalidator`/`TemporalSignaler` aliases, so `router.go`/`main.go` need not import
`service` directly (consistency with existing pattern).

---

## 9. Integration test plan (`//go:build integration`)

New file `internal/admin/tokens_sessions_integration_test.go` (tag `integration`, live PG + Redis).
Follow `DEPLOY_AND_TEST.md` env conventions.

### 9.1 Token scenarios
| Test | Asserts |
|---|---|
| `TestIntg_Token_CreateReturnsPlaintextOnce` | POST → 201, body has non-empty `token`; GET same id → body has NO `token` field |
| `TestIntg_Token_CreatePersistsHashNotPlaintext` | after create, `SELECT token_hash FROM them.access_tokens` = sha256(plaintext); plaintext absent from DB |
| `TestIntg_Token_CreateScopedOrchMissing404` | POST with random orchestrator_id → 404 |
| `TestIntg_Token_ListFilterByUser` | create for user 1 + 2; `?user_id=1` returns only user-1 tokens, desc order |
| `TestIntg_Token_ListEmptyReturnsArray` | no tokens → `[]` not null, 200 |
| `TestIntg_Token_GetMissing404` | random uuid → 404 |
| `TestIntg_Token_PatchDisableEvictsCache` | pre-seed L2 key `them:session:token:{hash}`; PATCH enabled=false → key gone + a message published on `them:token:revoked` (subscribe & assert) |
| `TestIntg_Token_PatchExpiresOnlyChangesExpiry` | PATCH expires_at leaves label/enabled unchanged |
| `TestIntg_Token_DeleteHardRemovesRow` | DELETE → 204; `SELECT count(*) WHERE id=...` = 0 (hard delete) + cache evicted + revoke published |
| `TestIntg_Token_DeleteMissing404` | random uuid → 404 |
| `TestIntg_Token_ValidateStopsAfterRevoke` | create token, validate via `auth.Cache` OK, DELETE, validate → `ErrTokenNotFound` (end-to-end revoke) |

### 9.2 Session scenarios (seed Redis via `session.Store.Register`)
| Test | Asserts |
|---|---|
| `TestIntg_Session_ListByApp` | register 2 sessions under app_id → `?app_id=` returns count 2 with metadata |
| `TestIntg_Session_ListByEP` | register under ep_slug → `?ep_slug=` returns them |
| `TestIntg_Session_ListNeither400` | no query params → 400 |
| `TestIntg_Session_ListSkipsGhosts` | SADD a ghost id (no shadow key) → not returned |
| `TestIntg_Session_DisconnectPublishes` | subscribe `them:sess:control:{sid}`; POST disconnect → receives "disconnect", body `signal_delivered:true` |
| `TestIntg_Session_DisconnectMissing404` | random uuid session → 404 |
| `TestIntg_Session_DisconnectBadUUID400` | `foo` → 400 `Invalid session_id` |

### 9.3 Unit tests (no infra, in `service/service_test.go` + `session_test.go`)
| Test | Asserts |
|---|---|
| `TestTokenService_Create_GeneratesHashAndReturnsPlaintext` | fake gen returns known pair; DAL receives hash; result carries plaintext |
| `TestTokenService_Create_OrchMissing_NotFound` | `OrchestratorExists→false` → `ErrNotFound`, no `CreateToken` call |
| `TestTokenService_Create_NoOrch_Skips_ExistsCheck` | orchID nil → `OrchestratorExists` not called |
| `TestTokenService_Update_InvalidatesByHash` | fake cache records `Del them:session:token:{hash}` + `Publish them:token:revoked {hash}` |
| `TestTokenService_Update_Missing_NotFound` | DAL `IsNoRows` → `ErrNotFound`, no cache calls |
| `TestTokenService_Delete_InvalidatesByHash` | same as update |
| `TestTokenService_NilCache_NoPanic` | cache nil → no panic on update/delete |
| `TestTokenService_List_ForwardsUserFilter` | userID pointer forwarded to DAL |
| `TestSessionAdmin_ListByApp_SkipsNotFound` | fake reader: one id `Get`→NotFound → dropped; count reflects survivors |
| `TestSessionAdmin_List_ReturnsEmptySliceNotNil` | no sessions → `Sessions: []`, count 0 |
| `TestSessionAdmin_Disconnect_NotFound` | `Get`→NotFound → `ErrNotFound` |
| `TestSessionAdmin_Disconnect_Delivered` | `SignalDisconnect`→(true,nil) → delivered true |
| `TestStore_ListEPSessions` (session pkg) | returns live members, prunes ghosts |
| `TestStore_ListAppSessions` (session pkg) | same |
| `TestStore_SignalDisconnect_ReturnsDelivered` | publish success → `(true,nil)` |

---

## 10. Python↔Go contract test plan

Extend the existing parity harness (Python "test 37"-style referenced in handover; if none exists in
`scripts/tests/`, add a Go-side table-driven contract test that hits both `:8001` Python and `:8002` Go).
Assert byte/shape parity, not implementation.

| Aspect | What to verify (both services, same input) |
|---|---|
| **Tokens: create body** | 201 status; JSON keys identical set `{id,label,user_id,orchestrator_id,enabled,expires_at,last_used_at,created_at,token}`; `token` present exactly once; `orchestrator_id`/`expires_at`/`last_used_at` serialize as `null` (not `""`, not omitted) when unset |
| **Tokens: get/list body** | key set WITHOUT `token`; list is JSON array; empty list → `[]` |
| **Tokens: user_id filter** | `?user_id=N` returns identical id set on both |
| **Tokens: orch-missing** | POST scoped to non-existent orch → **404** on both; message shape (`{"error":...}` Go vs `{"detail":...}` Python — DOCUMENT this known envelope difference; contract test asserts status only, not envelope) |
| **Tokens: not-found** | GET/PATCH/DELETE random uuid → 404 both |
| **Tokens: delete status** | 204 both, empty body |
| **Tokens: revoke effect** | after DELETE, a bearer request using the plaintext → 401 on both |
| **Sessions: list neither** | no query param → 400 both |
| **Sessions: list body** | `{"sessions":[...],"count":N}` shape both; session dicts have same keys |
| **Sessions: disconnect** | 200 `{"session_id":...,"signal_delivered":<truthy>}`; 404 for unknown; 400 for bad uuid — all match on both |
| **Auth behavior** | both require JWT + super_admin: no token → 401, non-admin token → 403, on every route |

**Known documented divergence (record in `lessons-learned.md`):** error envelope key — Python FastAPI
uses `{"detail": "..."}`, Go admin uses `{"error": "..."}`. This predates Wave 5 (all Go admin routes
already differ this way) so it is NOT a Wave 5 regression; the contract test asserts **status codes** and
**success-body shapes**, and explicitly tolerates the error-envelope key difference. If the frontend
depends on `detail`, that is a pre-existing cross-cutting issue out of Wave 5 scope.

---

## 11. Traefik / cutover / rollback

Both bridges run simultaneously: Python `them-bridge:8001`, Go `them-go-bridge:8002`. Traefik routes
`/api/v1/admin/*` by default to Python. Cutover strategy per route-prefix:

1. **Build + deploy Go** with the two new route groups mounted (they are inert until Traefik points at them).
2. **Shadow-verify** — run §10 contract tests against `:8002` directly (bypassing Traefik) while Python
   still serves live traffic. Zero user impact.
3. **Cut over** by adding a higher-priority Traefik router rule for the two exact prefixes:
   - `PathPrefix(`/api/v1/admin/tokens`)` → `them-go-bridge`
   - `PathPrefix(`/api/v1/admin/sessions`)` → `them-go-bridge`
   Leave all other `/api/v1/admin/*` on Python. Priority must exceed the broad Python admin rule so only
   these two prefixes move. (Do NOT modify Python routers — only Traefik label/dynamic-config, which is
   allowed: it is not a route change inside the app.)
4. **Rollback** = remove the two Traefik router rules → traffic falls back to Python instantly. No data
   migration, no schema change, so rollback is a config revert with zero persistent side effects. The
   Go service can keep running (inert on those prefixes).

Traefik files to touch at cutover only: `traefik/` dynamic config or `docker-compose.yml` labels for
`them-go-bridge`. This is deferred to the cutover step, not the code commits.

**Cutover safety gate:** all §9 integration tests green on `:8002`, all §10 contract tests green
(status + success-shape parity), `go test -race ./...` green.

---

## 12. Commit sequence

Each commit builds + passes `go test ./internal/admin/... ./internal/session/...` before the next.
Order = lowest blast radius first.

| # | Title | Files | Verify |
|---|---|---|---|
| 1 | `session: add List{EP,App}Sessions + SignalDisconnect delivered count` | `internal/session/session.go` (+`luaPruneAndList`, list methods, signature change), `internal/session/session_test.go`, `TEST_INDEX.md`, `go/CLAUDE.md` | `go build ./...` (fix any ws/sse call sites if grep finds them), `go test ./internal/session/...` |
| 2 | `admin/dal: add access_tokens DAL` | `internal/admin/dal/tokens.go`, `internal/admin/dal/dal.go` (types) | `go build ./...` (unused until service wired — reference via test), dal has no unit test harness for SQL; covered by integration in commit 5 |
| 3 | `admin/service: TokenService + SessionAdminService` | `service/service.go` (Dal +tokens), `service/tokens.go` (real impl), `service/sessions.go`, `service/service_test.go` (unit tests §9.3), `TEST_INDEX.md` | `go test ./internal/admin/...` incl. new unit tests |
| 4 | `admin: token + session handlers, wire router` | `internal/admin/tokens.go`, `internal/admin/sessions.go`, `internal/admin/router.go`, `internal/admin/middleware.go` (`SessionReader` alias), `cmd/them/main.go`, `admin_test.go` (BuildRouter call-site if needed) | `go test ./internal/admin/...`, `go build ./cmd/them/` |
| 5 | `admin: tokens+sessions integration tests` | `internal/admin/tokens_sessions_integration_test.go`, `TEST_INDEX.md`, `implementation-status.md` | `go test -tags=integration ./internal/admin/...` (live PG+Redis) |
| 6 | `docs: Python↔Go contract tests + parity notes` | contract test file, `lessons-learned.md` (envelope divergence note) | contract suite green vs `:8001`/`:8002` |

Commit 7 (separate, at cutover, NOT part of code review): Traefik rule for the two prefixes.

Each commit is independently revertable; handler constructor signatures for existing resources are
untouched, so reverting any Wave 5 commit leaves agents/orchestrators/apps/runs working.

---

## 13. Blockers / decisions for the implementer to confirm

1. **`SignalDisconnect` signature change ripples.** Changing `session.Store.SignalDisconnect` from
   `error` to `(bool, error)` will break any current caller. **Action:** `grep -rn "SignalDisconnect" go/`
   before commit 1. Investigation shows ws/sse use `SubscribeControl`, not `SignalDisconnect`, and no
   admin caller exists yet — so the blast radius is likely just the session package + its test. Confirm
   during commit 1. If a caller exists, adjust it in the same commit. **This is the single riskiest
   line-item.**

2. **`signal_delivered` int-vs-bool parity.** Python returns the raw Redis subscriber count (int); the
   plan returns a bool (`delivered = publish succeeded`). If the frontend or contract test requires the
   exact integer, add `PublishN(ctx,channel,payload) (int64,error)` to `session.RedisClient` +
   `cache.SessionRedisClient`, and have `SignalDisconnect` return `(int, error)`. **Decision needed at
   implementation time**, driven by what the frontend consumes. Default plan: bool (truthy), which
   satisfies the JS `if (signal_delivered)` idiom. Not a hard blocker; a scoped choice.

3. **`service` importing `internal/session`.** New import edge `service → session`. Verified no cycle
   (`session` imports only stdlib+slog). Acceptable, but if a future maintainer wants `service` to stay
   transport-agnostic, the alternative is defining `SessionReader.Get` over a `map[string]any` and adding
   a thin adapter in `main.go`/`admin` that converts `*session.SessionInfo`. Chosen: direct import
   (simplest, matches "least code" preference). **No blocker.**

4. **`expires_at` timezone/format parity.** Python emits ISO8601 with offset (e.g.
   `2026-07-24T12:00:00+00:00`); Go `::text` cast on `timestamptz` yields `2026-07-24 12:00:00+00`
   (space separator, different offset format). **This WILL differ** unless the DAL formats explicitly.
   **Action:** in `scanToken`, parse the PG text and re-emit via `time.RFC3339` so the JSON matches
   Python's `datetime` serialization, OR cast in SQL with `to_char(expires_at, 'YYYY-MM-DD"T"HH24:MI:SS+00:00')`.
   Contract test §10 must assert timestamp-format parity or explicitly tolerate it. **Flagged as a
   correctness detail, not a blocker — but must be handled or the frontend date parser may break.**

5. **No token DAL unit-test harness.** The existing `admin_test.go` `fakeRows`/`scanInto` machinery can
   exercise token SQL scanning, but SQL correctness (NULLIF casts, RETURNING) is only truly covered by
   the integration tests in commit 5. Ensure commit 5 runs in CI with live infra, or the token SQL is
   effectively untested. **Not a blocker; a test-coverage sequencing note.**

No hard blockers prevent implementation. Items 1, 2, and 4 are the decisions/verifications the Sonnet
implementer must resolve inline (all have a default answer specified above).
