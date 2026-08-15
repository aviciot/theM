# Wave 7 Plan — LLM Provider CRUD + Fernet Compatibility
# Generated: 2026-07-25
# Status: APPROVED FOR IMPLEMENTATION

---

## Selected Operations (5 routes)

| Method | Path | Python handler | Status |
|--------|------|----------------|--------|
| `GET` | `/api/v1/admin/llm-providers` | `admin_llm_providers.py:list_providers` | Migrate to Go |
| `POST` | `/api/v1/admin/llm-providers` | `admin_llm_providers.py:create_provider` | Migrate to Go |
| `GET` | `/api/v1/admin/llm-providers/{id}` | `admin_llm_providers.py:get_provider` | Migrate to Go |
| `PATCH` | `/api/v1/admin/llm-providers/{id}` | `admin_llm_providers.py:update_provider` | Migrate to Go |
| `DELETE` | `/api/v1/admin/llm-providers/{id}` | `admin_llm_providers.py:delete_provider` | Migrate to Go |

**Not in scope:** `/api/v1/admin/llm-providers/routing/config` (already migrated in Wave 6).

**Traefik priority:** New router `them-go-llm-providers` at priority 120.
Rule: `PathPrefix(/api/v1/admin/llm-providers) && !Path(/api/v1/admin/llm-providers/routing/config)`
(The `/routing/config` exact-match rule at priority 120 already beats this prefix — confirmed safe by Traefik priority-match semantics.)

---

## Python Contract Analysis

### Request schemas

**POST body (LLMProviderCreate):**
```json
{
  "name": "anthropic",          // required, unique slug
  "display_name": "Anthropic",  // required
  "api_key": "sk-...",           // optional plaintext; stored encrypted
  "base_url": null,              // optional
  "default_model": "claude-sonnet-4-6", // required
  "model_pricing": {},           // optional, defaults to {}
  "enabled": true                // optional, defaults to true
}
```

**PATCH body (LLMProviderUpdate) — all fields optional:**
```json
{
  "display_name": "...",    // omit = leave unchanged
  "api_key": "sk-...",      // omit = keep current key; set = rotate
  "base_url": null,
  "default_model": "...",
  "model_pricing": {},
  "enabled": false
}
```

### Response schema (LLMProviderOut)

```json
{
  "id": 1,
  "name": "anthropic",
  "display_name": "Anthropic",
  "api_key_set": true,
  "api_key_masked": "sk-a...XYZ7",  // first4 + "..." + last4; "****" if ≤8 chars; null if no key
  "base_url": null,
  "default_model": "claude-sonnet-4-6",
  "model_pricing": {},
  "enabled": true
}
```

**Masking rules (from `_mask_key`):**
- `api_key_encrypted` is NULL or empty → `api_key_set: false`, `api_key_masked: null`
- Decrypt fails → `api_key_set: true`, `api_key_masked: "****"`
- `len(plaintext) <= 8` → `api_key_set: true`, `api_key_masked: "****"`
- `len(plaintext) > 8` → `api_key_set: true`, `api_key_masked: plain[:4] + "..." + plain[-4:]`

### HTTP status codes

| Scenario | Status |
|----------|--------|
| POST success | 201 Created |
| GET/PATCH/DELETE success | 200 / 204 |
| POST with duplicate `name` | 409 Conflict |
| GET/PATCH/DELETE non-existent id | 404 Not Found |
| Missing required field | 400 Bad Request |
| Bad JSON | 400 Bad Request |

### Ordering and defaults

- `GET /llm-providers` lists ordered by `id ASC` (SQLAlchemy default sort on the PK).
- Empty list returns `[]` not `null`.
- `model_pricing` defaults to `{}` on read if DB value is null.
- DELETE is a hard delete (`db.delete(row)` + `db.commit()`), not a soft disable.

---

## DB Schema

```sql
CREATE TABLE IF NOT EXISTS them.llm_providers (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    api_key_encrypted TEXT,          -- NULL when no key; non-null = "enc:<fernet_token>"
    base_url TEXT,
    default_model TEXT NOT NULL,
    model_pricing JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Key constraints:
- `id` is `SERIAL` (integer auto-increment), **not UUID** — the Go type is `int64`.
- `name` has a UNIQUE index — duplicate name → PostgreSQL SQLSTATE 23505 → 409.
- `api_key_encrypted` is nullable TEXT — no encryption at the DB level.
- No schema changes required for Wave 7.

---

## Fernet Compatibility — Critical Analysis

### Python key derivation (exact algorithm)

```python
# app/utils/crypto.py
import base64, hashlib
from app.config import settings

key = base64.urlsafe_b64encode(
    hashlib.sha256(settings.security.secret_key.encode()).digest()
)
# → 44-byte urlsafe-base64 of a 32-byte SHA-256 digest
# This IS a valid Fernet key (Fernet requires exactly 32 bytes when base64-decoded).
```

The env var driving this is `SECRET_KEY` (mapped in `app/config.py:107` as `SECRET_KEY: str`), exposed to the Go bridge as `THE_M_SECRET_KEY` (renamed in `docker-compose.yml` during the Wave 6 JWT env-var fix).

**Go equivalent:**
```go
rawKey := sha256.Sum256([]byte(secretKey))  // [32]byte
fernetKey := base64.URLEncoding.EncodeToString(rawKey[:]) // 44-char urlsafe-base64
```

Note: Python's `urlsafe_b64encode` does not strip padding (`=`). `base64.URLEncoding` in Go also preserves `=` padding. This must be byte-identical — confirmed.

### Fernet wire format (RFC-level specification)

Python `cryptography.fernet.Fernet` uses:

```
Token layout (before base64url encoding):
  Version:    0x80 (1 byte)
  Timestamp:  8 bytes, big-endian uint64, Unix seconds
  IV:         16 bytes, random AES-CBC initialization vector
  Ciphertext: (len(plaintext) padded to 16-byte boundary via PKCS7) bytes
  HMAC:       32 bytes, HMAC-SHA256 of Version+Timestamp+IV+Ciphertext

Stored as: base64url(Version + Timestamp + IV + Ciphertext + HMAC)
```

Key usage (Fernet splits the 32-byte key into two halves):
- `key_bytes = base64url_decode(fernetKey)` → 32 bytes
- `signing_key = key_bytes[0:16]` → used for HMAC-SHA256
- `encryption_key = key_bytes[16:32]` → used for AES-128-CBC

**Stored in DB:** The Python code wraps encrypted values with an `"enc:"` prefix:
```
api_key_encrypted = "enc:" + base64url(fernet_token)
```
Go must strip the `"enc:"` prefix before decoding. Go must add the `"enc:"` prefix before storing.

### Go implementation: stdlib-only is viable

All required primitives are available in stdlib + `golang.org/x/crypto` (already an indirect dep):

| Operation | Go package |
|-----------|------------|
| SHA-256 (key derivation) | `crypto/sha256` |
| HMAC-SHA256 | `crypto/hmac` + `crypto/sha256` |
| AES-128-CBC | `crypto/aes` + `crypto/cipher` |
| PKCS7 padding/unpadding | ~20 lines (stdlib-only, no package needed) |
| Base64 URL encoding | `encoding/base64` |
| Constant-time HMAC compare | `crypto/subtle` |

`golang.org/x/crypto` is NOT needed — AES-CBC and HMAC-SHA256 are in stdlib. The `golang.org/x/crypto` dep already in `go.mod` is pulled by Temporal SDK for unrelated reasons.

**No new module dependencies required.** This is a pure stdlib implementation.

### Recommended crypto approach: stdlib AES-128-CBC + HMAC-SHA256

Implement a new package `go/internal/crypto/fernet/fernet.go` with:

```
func Encrypt(key []byte, plaintext []byte) ([]byte, error)
func Decrypt(key []byte, token []byte) ([]byte, error)
func DeriveKey(secretKey string) []byte   // sha256(secretKey) → 32 bytes (not base64-encoded yet)
```

The package operates on raw bytes; the caller base64url-encodes/decodes and handles the `"enc:"` prefix.

**Why a new package (not inline in service):** The crypto logic is independently testable, can generate known-vector tests, and may be reused by the orchestrators handler (which has `llm_api_key_encrypted` column with the same Fernet scheme).

### Compatibility proof requirement

The plan mandates two-directional proof:

**Direction 1: Python encrypts → Go decrypts**
- Generate known test vectors using the Python `cryptography` library (executed inside `them-bridge` container).
- Store: plaintext, secret_key, expected ciphertext token.
- Go test: `fernet.Decrypt(DeriveKey(secretKey), base64Decode(token)) == plaintext`.

**Direction 2: Go encrypts → Python decrypts**
- Go test generates a token, writes it to a temp location.
- A Python script inside `them-bridge` reads and decrypts it.
- OR: verify structural compatibility by having Go encrypt and then Go re-decrypt (proves round-trip), PLUS Python decrypting a Go-generated token via a contract test script.

Contract test script: `scripts/test_wave7_fernet_compat.py` — run inside `them-bridge` container.

**Known-vector test vectors to generate before implementing Go:**

```
secret_key = "test-secret"
plaintext  = "sk-ant-api03-testkey"

# In Python (inside them-bridge):
from app.utils.crypto import encrypt_value, decrypt_value
encrypted = encrypt_value(plaintext)
# Record (secret_key, plaintext, encrypted) as a static test vector
```

The Go unit test uses this static vector to assert decryption correctness without running Python.

### Key source and environment configuration

The `SECRET_KEY` value is injected into the Go bridge as `THE_M_SECRET_KEY` (confirmed from `docker-compose.yml` Wave 6 fix). In `go/internal/config/config.go`, this maps to `cfg.SecretKey` (verify field name — may need to be added if not already present).

**Action:** Check `internal/config/config.go` for a `SecretKey` / `THE_M_SECRET_KEY` field. If absent, add it. The field must never appear in `cfg.SafeString()` output and must never be logged.

### Behavior when key is missing, invalid, or changed

| Scenario | Encrypt behavior | Decrypt behavior |
|----------|-----------------|-----------------|
| `THE_M_SECRET_KEY` empty | Fail startup (validation error) | Same |
| Key changed (rotation) | New values use new key | Old values undecryptable → `api_key_set: true`, `api_key_masked: "****"` |
| Corrupted ciphertext | N/A | → `api_key_set: true`, `api_key_masked: "****"` (mirror Python's `except Exception: return True, "****"`) |

The Go `_mask_key` equivalent must never surface the error or the ciphertext — silently treat decrypt failure as "key set but unreadable."

### Masking: must decrypt, cannot shortcut with ciphertext tail

The `api_key_masked` field shows the **first 4 and last 4 characters of the plaintext**, not of the ciphertext. Therefore Go **must** decrypt the stored value to produce the mask. There is no safe shortcut using ciphertext bytes — Fernet's CBC output is not suffix-preserving.

The masking function runs on every `GET` and `LIST` response. Performance is not a concern (admin endpoints, low-frequency).

### Security constraints

- Plaintext API keys must be zeroed from memory immediately after masking. In Go, use `defer func() { for i := range plain { plain[i] = 0 } }()` on the plaintext byte slice.
- Keys must never appear in log output. The `slog` fields in the LLM providers handler must never include `api_key`, `plaintext`, or any intermediate crypto state.
- The `api_key` request field from POST/PATCH is the only point where plaintext is received; it must be encrypted and the plaintext reference discarded before any DB call or log line.
- `THE_M_SECRET_KEY` must be validated at startup to be non-empty; fail-fast (return error from config.Load()) rather than panic at first encrypt call.

---

## Go Implementation Structure

### New files

```
go/internal/crypto/
  fernet.go          -- Encrypt, Decrypt, DeriveKey; PKCS7 pad/unpad
  fernet_test.go     -- Unit tests: known-vector round-trip, bad HMAC, bad version, wrong key
                        Contract test (tagged integration): Python-encrypted vectors

go/internal/admin/dal/
  llm_providers.go   -- LLMProvider type, LLMProviderInput, ListProviders, GetProvider,
                        CreateProvider, UpdateProvider, DeleteProvider

go/internal/admin/service/
  llm_providers.go   -- LLMProviderService: List, Get, Create, Update, Delete
                        maskKey() helper: decrypt + produce masked string
  llm_providers_test.go -- Unit tests using fake DAL + fake crypto

go/internal/admin/
  llm_providers.go   -- LLMProvidersHandler: Routes, List, Create, Get, Update, Delete
  (no new test file — tests go in admin_test.go using the existing fakeDB pattern)

scripts/
  test_wave7_fernet_compat.py  -- Contract test: Python↔Go Fernet vectors
```

### Modified files

```
go/internal/config/config.go          -- Add SecretKey field (THE_M_SECRET_KEY) if absent
go/internal/admin/service/service.go  -- Add LLM provider DAL methods to Dal interface
go/internal/admin/router.go           -- Register LLMProvidersHandler in BuildRouter
go/internal/admin/admin_test.go       -- May need new fakeDB stubs
go/TEST_INDEX.md                      -- Add new test entries (same commit as each test file)
docker-compose.yml                    -- Add them-go-llm-providers Traefik router (Wave 7 cutover)
```

### Handler → Service → DAL → Crypto layering

```
HTTP handler (admin/llm_providers.go)
  ↓ parse JSON body; path param id (int64); write HTTP
LLMProviderService (service/llm_providers.go)
  ↓ validate input; call crypto.Encrypt/Decrypt; apply defaults
  ↓ map service errors → ErrNotFound / ErrUnprocessable
DAL (dal/llm_providers.go)
  ↓ SQL SELECT/INSERT/UPDATE/DELETE; map 23505 → unique violation
Crypto (internal/crypto/fernet.go)
  ↓ AES-128-CBC + HMAC-SHA256; DeriveKey from config.SecretKey
```

**Import constraint:** `internal/crypto` must import only stdlib. No circular imports: `crypto → nothing in internal/`. Service imports crypto directly. Handler does not import crypto.

### Dal interface additions (service.go)

```go
// LLM Providers
ListProviders(ctx context.Context) ([]dal.LLMProvider, error)
GetProvider(ctx context.Context, id int64) (dal.LLMProvider, error)
CreateProvider(ctx context.Context, in dal.LLMProviderInput) (dal.LLMProvider, error)
UpdateProvider(ctx context.Context, id int64, in dal.LLMProviderPatch) (dal.LLMProvider, error)
DeleteProvider(ctx context.Context, id int64) error
```

### DAL types (dal/llm_providers.go)

```go
// LLMProvider is the internal DB row type. api_key_encrypted is the raw stored
// value (with "enc:" prefix). Masking and decryption happen in the service layer.
type LLMProvider struct {
    ID              int64
    Name            string
    DisplayName     string
    APIKeyEncrypted *string   // nil when no key set
    BaseURL         *string
    DefaultModel    string
    ModelPricing    []byte    // raw JSONB
    Enabled         bool
}

// LLMProviderInput is used for CREATE.
type LLMProviderInput struct {
    Name            string
    DisplayName     string
    APIKeyEncrypted *string   // nil if no key; pre-encrypted by service
    BaseURL         *string
    DefaultModel    string
    ModelPricing    []byte
    Enabled         bool
}

// LLMProviderPatch is used for PATCH. Nil pointer = field absent (leave unchanged).
type LLMProviderPatch struct {
    DisplayName     *string
    APIKeyEncrypted **string  // nil = absent; non-nil *string = new value (may be nil to clear)
    BaseURL         **string
    DefaultModel    *string
    ModelPricing    []byte    // nil = absent
    Enabled         *bool
}
```

Note: `ModelPricing` is `[]byte` in the DAL (raw JSON) and unmarshalled to `map[string]any` in the service output struct. This mirrors the agent/orchestrator pattern already established.

### Service output type (service/llm_providers.go)

```go
// LLMProviderOut is the HTTP response shape — mirrors Python's LLMProviderOut exactly.
type LLMProviderOut struct {
    ID           int64          `json:"id"`
    Name         string         `json:"name"`
    DisplayName  string         `json:"display_name"`
    APIKeySet    bool           `json:"api_key_set"`
    APIKeyMasked *string        `json:"api_key_masked"`  // null in JSON when no key
    BaseURL      *string        `json:"base_url"`
    DefaultModel string         `json:"default_model"`
    ModelPricing map[string]any `json:"model_pricing"`
    Enabled      bool           `json:"enabled"`
}
```

### DELETE semantics

Python does a **hard delete** (`db.delete(row)` → `DELETE FROM them.llm_providers WHERE id=$1`).
Go DAL must use `DELETE FROM them.llm_providers WHERE id=$1`, not a soft-delete `UPDATE enabled=false`.
This differs from `DeleteAgent` which soft-deletes. The DAL must use `ExecReturning` or check affected rows to distinguish "deleted" from "not found."

**Implementation:** `DELETE FROM them.llm_providers WHERE id=$1 RETURNING id` — if no row scanned → `pgx.ErrNoRows` → service maps to `ErrNotFound`.

### PATCH semantics

Python's `update_provider` applies each field independently only when `body.field is not None`.
Go PATCH body uses `*Type` for optional fields. The service must build a dynamic UPDATE.

**Implementation options:**
1. Dynamic SQL with conditional SET clauses (complex, error-prone).
2. Fetch-then-modify: `GetProvider(id)` → apply non-nil fields → full `UPDATE` with all columns.

**Recommendation: fetch-then-modify (option 2).** Python uses SQLAlchemy which does implicit field-level tracking; the Go equivalent is: GET the row, apply non-nil patch fields, UPDATE all columns. This produces one extra SELECT per PATCH but eliminates dynamic SQL complexity. The `updated_at = now()` is always written. The PATCH endpoint is admin-only and low-frequency — the extra SELECT is acceptable.

---

## Uniqueness and Validation

| Rule | Python behavior | Go behavior |
|------|----------------|-------------|
| POST with duplicate `name` | SQLAlchemy raises IntegrityError → 409 `"Provider 'X' already exists"` | `dal.IsUniqueViolation(err)` → `ErrConflict` → 409 |
| POST with empty `name` | Pydantic required field validation → 422 | Handler: `if body.Name == ""` → 400 |
| POST with empty `display_name` | Pydantic required → 422 | Handler: `if body.DisplayName == ""` → 400 |
| POST with empty `default_model` | Pydantic required → 422 | Handler: `if body.DefaultModel == ""` → 400 |

**New sentinel error:** `ErrConflict = errors.New("conflict")` added to `service/errors.go`.
`writeServiceError` maps `errors.Is(err, service.ErrConflict)` → `http.StatusConflict`.

---

## Authorization

All 5 routes require JWT super-admin. They are mounted inside the `admin.Route("/admin", ...)` group which already applies `jwtMiddleware` + `RequireSuperAdmin`. No new auth wiring required.

---

## Config: SecretKey Field

Check `go/internal/config/config.go`. If `THE_M_SECRET_KEY` is not already loaded:

```go
// in Config struct:
SecretKey string   // THE_M_SECRET_KEY — used for Fernet key derivation

// in Load():
SecretKey: os.Getenv("THE_M_SECRET_KEY"),

// in validate():
if cfg.SecretKey == "" {
    return errors.New("THE_M_SECRET_KEY must not be empty")
}
```

`SafeString()` must NOT include `SecretKey`. This field is a startup secret — validate once, never log.

The key is passed from `config.Config` through `main.go` → `LLMProvidersHandler` constructor → `LLMProviderService`.

---

## Fernet Package Specification (go/internal/crypto/fernet.go)

### DeriveKey

```go
// DeriveKey produces the 32-byte Fernet key material from the application secret.
// The key material is sha256(secretKey). The caller base64url-encodes before
// passing to standard Fernet — this function returns raw bytes for direct use
// with Encrypt/Decrypt.
func DeriveKey(secretKey string) []byte {
    sum := sha256.Sum256([]byte(secretKey))
    return sum[:]
}
```

### Encrypt

```go
// Encrypt encrypts plaintext using Fernet (AES-128-CBC + HMAC-SHA256).
// key must be 32 bytes (from DeriveKey). Returns base64url-encoded token
// WITHOUT the "enc:" prefix — the caller adds the prefix.
func Encrypt(key []byte, plaintext []byte) ([]byte, error)
```

Steps:
1. Split key: `signingKey = key[0:16]`, `encryptionKey = key[16:32]`
2. Generate 16 random bytes IV
3. PKCS7-pad plaintext to 16-byte boundary
4. AES-128-CBC encrypt with `encryptionKey` and IV
5. Build token: `version(0x80) + timestamp(8B BE) + IV(16B) + ciphertext`
6. HMAC-SHA256 of token bytes using `signingKey`
7. Append HMAC to token
8. Return `base64.URLEncoding.EncodeToString(token)` (with `=` padding)

### Decrypt

```go
// Decrypt decrypts a Fernet token (base64url, WITHOUT the "enc:" prefix).
// Returns plaintext or an error. Callers must treat any error as "key set but unreadable".
func Decrypt(key []byte, token []byte) ([]byte, error)
```

Steps:
1. `base64.URLEncoding.DecodeString(string(token))`
2. Validate minimum length: `1 + 8 + 16 + 16 + 32 = 73` bytes minimum
3. Validate version byte == 0x80
4. Split: `data = raw[0:len-32]`, `mac = raw[len-32:]`
5. `signingKey = key[0:16]`, `encryptionKey = key[16:32]`
6. Recompute HMAC-SHA256 of `data` using `signingKey`
7. `subtle.ConstantTimeCompare(mac, computed)` — must be 1; else return error
8. Extract IV from `raw[9:25]`, ciphertext from `raw[25:len-32]`
9. AES-128-CBC decrypt with `encryptionKey` and IV
10. PKCS7 unpad
11. Return plaintext

Note on timestamp TTL: Python's `Fernet.decrypt()` accepts an optional `ttl` parameter. Without it, there is no expiry check. The Go implementation does NOT enforce a TTL — this matches Python behavior when called without `ttl` (which is how `app/utils/crypto.py` calls it).

### PKCS7 helpers (package-private)

```go
func pkcs7Pad(data []byte, blockSize int) []byte
func pkcs7Unpad(data []byte) ([]byte, error)  // returns error on invalid padding
```

---

## Test Plan

### Phase 1: Crypto unit tests (fernet_test.go)

| Test | Description |
|------|-------------|
| `TestEncryptDecrypt_RoundTrip` | Go encrypts → Go decrypts → plaintext matches |
| `TestDecrypt_KnownPythonVector` | Static vector: Python-encrypted token → Go decrypts correctly |
| `TestEncrypt_GoThenPythonViaScript` | (integration tag) Go encrypts, Python script decrypts via exec |
| `TestDecrypt_BadHMAC` | Tampered token → error |
| `TestDecrypt_BadVersion` | Wrong version byte → error |
| `TestDecrypt_WrongKey` | Different key → error (HMAC mismatch) |
| `TestDecrypt_TruncatedToken` | Too-short token → error |
| `TestPKCS7Pad_Unpad` | Pad then unpad is identity at various lengths |
| `TestDeriveKey_ByteIdentical` | sha256("test-secret") matches known hex |

**Static Python vector (generate before writing Go):**
```bash
docker exec them-bridge python3 -c "
from app.utils.crypto import encrypt_value, decrypt_value
import os
# Use a known secret_key to get deterministic key derivation
# (must match THE_M_SECRET_KEY in the env)
ev = encrypt_value('sk-ant-api03-testkey123456789')
print('encrypted:', ev)
print('decrypted:', decrypt_value(ev))
"
```
Record `(secret_key, plaintext, encrypted_token)` as constants in `fernet_test.go`.

**Note:** Fernet uses random IV, so Python re-encrypt of the same value produces a different token each time. The known-vector test uses a RECORDED Python-generated token, not a freshly generated one.

### Phase 2: Service unit tests (service/llm_providers_test.go)

Extend `service_test.go` fake DAL pattern (add `fakeDal` stubs for 5 provider methods).

| Test | Description |
|------|-------------|
| `TestProviderService_List_Empty` | Returns `[]` not nil |
| `TestProviderService_List_WithKey` | maskKey called; plaintext never leaks |
| `TestProviderService_Create_NoKey` | api_key_set=false, api_key_masked=null |
| `TestProviderService_Create_WithKey` | encrypt called; masked correctly |
| `TestProviderService_Create_DuplicateName` | ErrConflict returned |
| `TestProviderService_Get_NotFound` | ErrNotFound when DAL returns ErrNoRows |
| `TestProviderService_Update_RotateKey` | New key encrypted; old key discarded |
| `TestProviderService_Update_NoKeyChange` | api_key absent in patch → key unchanged |
| `TestProviderService_Delete_NotFound` | ErrNotFound when DAL returns ErrNoRows |
| `TestMaskKey_ShortPlaintext` | ≤8 chars → "****" |
| `TestMaskKey_LongPlaintext` | >8 chars → first4 + "..." + last4 |
| `TestMaskKey_DecryptError` | Bad ciphertext → "****", api_key_set=true |

### Phase 3: Handler tests (admin_test.go additions)

Extend existing `admin_test.go` fake DB/fakeRows pattern.

| Test | Description |
|------|-------------|
| `TestLLMProvidersHandler_List_200` | GET /llm-providers → 200, JSON array |
| `TestLLMProvidersHandler_List_Empty` | Empty table → 200, `[]` |
| `TestLLMProvidersHandler_Create_201` | POST valid body → 201, Location header |
| `TestLLMProvidersHandler_Create_409` | Duplicate name → 409 |
| `TestLLMProvidersHandler_Create_400_MissingName` | Missing `name` → 400 |
| `TestLLMProvidersHandler_Get_200` | GET /{id} found → 200 |
| `TestLLMProvidersHandler_Get_404` | GET /{id} not found → 404 |
| `TestLLMProvidersHandler_Patch_200` | PATCH partial fields → 200 |
| `TestLLMProvidersHandler_Patch_404` | PATCH missing → 404 |
| `TestLLMProvidersHandler_Delete_204` | DELETE found → 204 |
| `TestLLMProvidersHandler_Delete_404` | DELETE missing → 404 |

### Phase 4: Python↔Go contract tests

**Script:** `scripts/test_wave7_fernet_compat.py`

Run against live stack (Python on 8001, Go on 8002):

1. POST provider with `api_key` via Python → GET via Go → verify `api_key_masked` matches
2. POST provider with `api_key` via Go → GET via Python → verify `api_key_masked` matches
3. PATCH provider (rotate key) via Go → GET via Python → verify new mask
4. POST same `name` twice → second returns 409 (both bridges)
5. DELETE via Go → GET via Python → 404

**Fernet direction tests inside `them-bridge`:**
```bash
docker exec them-bridge python3 -c "
# Generate a vector for Go's known-vector test
from app.utils.crypto import encrypt_value, decrypt_value
import os
secret = os.environ['SECRET_KEY']
plain = 'test-plaintext-api-key-12345678'
enc = encrypt_value(plain)
print(f'SECRET_KEY={secret!r}')
print(f'plaintext={plain!r}')
print(f'encrypted={enc!r}')
# Go: DeriveKey(secret), Decrypt(key, enc[4:])  # strip 'enc:' prefix
"
```

### Phase 5: Race detector

```bash
go test -race ./internal/crypto/... ./internal/admin/...
```

Run in Linux CI (Docker) — race detector requires GCC, not available on Windows host (L-13 pattern from lessons-learned).

### Phase 6: Live smoke test (via Traefik on port 8088)

```bash
# Confirm request hits Go bridge (check go-bridge logs, not Python)
curl -s -H "Authorization: Bearer $ADMIN_JWT" \
  http://localhost:8088/api/v1/admin/llm-providers | jq .

curl -s -X POST -H "Authorization: Bearer $ADMIN_JWT" \
  -H "Content-Type: application/json" \
  -d '{"name":"test-wave7","display_name":"Test","default_model":"claude-sonnet-4-6","api_key":"sk-test-123456789"}' \
  http://localhost:8088/api/v1/admin/llm-providers | jq .
```

### Phase 7: Python test suite (no regressions)

```bash
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

The Python `admin_llm_providers.py` routes remain in-process on port 8001. During cutover only, Traefik sends traffic to Go at 8002. The Python tests run directly against the Python bridge — they continue to pass regardless of Go cutover state.

---

## Rollback Plan

The Wave 7 Traefik router block (`them-go-llm-providers`) is in `docker-compose.yml`.
Rollback: remove the router block and restart the Go bridge. Python resumes serving all `/llm-providers` routes immediately. Identical to Wave 6 rollback pattern.

---

## Operational Risks

| Risk | Severity | Mitigation |
|------|----------|------------|
| Fernet key mismatch: Go decrypts Python data incorrectly | CRITICAL | Known-vector test proves decryption BEFORE any cutover |
| Go encrypts in incompatible format: Python can't read Go-written keys | HIGH | Two-direction contract test (script inside them-bridge) |
| `THE_M_SECRET_KEY` not set in Go bridge env | HIGH | Startup validation: fail-fast if empty |
| Key rotation: changing SECRET_KEY makes all stored keys unreadable | MEDIUM | Document; do not rotate without DB re-encryption migration |
| PKCS7 padding oracle attack | LOW | Constant-time HMAC check runs BEFORE decryption (prevents oracle) |
| Plaintext key in logs or error messages | HIGH | No logging of crypto intermediates; test with log capture |
| Race condition on LLMProviderService (concurrent PATCH) | LOW | Service is stateless; crypto is per-call; no shared mutable state |
| model_pricing JSONB unmarshal failure | LOW | Treat unmarshal error as `{}` with a WARN log (same pattern as runtime_config) |

---

## Secret-Handling Rules (enforced in review)

1. `api_key` request body field → encrypt → discard plaintext reference immediately.
2. Decrypted key for masking → zero bytes after mask is computed (`defer clear()`).
3. `fernet.Encrypt` / `fernet.Decrypt` → no `slog` calls inside.
4. Handler must not log the request body (it contains `api_key`).
5. `THE_M_SECRET_KEY` → loaded into `config.SecretKey` at startup; never logged; `SafeString()` must omit it.
6. `api_key_encrypted` DB column → never returned in any JSON response field.

---

## Commit Boundaries

| Commit | Files | Description |
|--------|-------|-------------|
| Phase 1 | `go/internal/crypto/fernet.go`, `go/internal/crypto/fernet_test.go`, `go/TEST_INDEX.md` | Add Fernet crypto package with known-vector unit tests |
| Phase 2 | `go/internal/config/config.go` (if SecretKey missing), `go/internal/admin/dal/llm_providers.go`, `go/internal/admin/service/service.go` (Dal additions), `go/TEST_INDEX.md` | Add DAL types + LLM providers DAL |
| Phase 3 | `go/internal/admin/service/llm_providers.go`, `go/internal/admin/service/llm_providers_test.go`, `go/internal/admin/service/service_test.go` (fakeDal stubs), `go/TEST_INDEX.md` | Add LLMProviderService + service unit tests |
| Phase 4 | `go/internal/admin/llm_providers.go`, `go/internal/admin/router.go`, `go/internal/admin/admin_test.go` (handler tests), `go/internal/admin/service/errors.go` (ErrConflict), `go/TEST_INDEX.md` | Add handler + router wiring + handler tests |
| Phase 5 | `scripts/test_wave7_fernet_compat.py` | Python↔Go contract test script |
| Phase 6 | `docker-compose.yml` (Traefik router block) | Cutover: route /llm-providers to Go |
| Phase 7 | `docs/architecture-v2/WAVE7_IMPLEMENTATION_REPORT.md`, `docs/architecture-v2/implementation-status.md`, `docs/architecture-v2/lessons-learned.md`, `go/TEST_INDEX.md` | Documentation + handover |

Each commit must pass `go test ./...` before being made. Phases 1–4 can all be done before cutover (Phase 6).

---

## Blockers

None that prevent Wave 7 from starting. Pre-conditions confirmed:

- ✅ `THE_M_SECRET_KEY` is already wired into the Go bridge env (Wave 6 fix)
- ✅ `golang.org/x/crypto` is already an indirect dep (not needed, but confirms stdlib is sufficient)
- ✅ No DB schema changes required
- ✅ No new Redis keys required
- ✅ Fernet is fully implementable in Go stdlib (`crypto/aes`, `crypto/hmac`, `crypto/sha256`, `crypto/cipher`, `encoding/base64`, `crypto/subtle`)
- ✅ The `internal/admin` handler/service/DAL pattern is established and well-tested
- ✅ The existing fakeDB infrastructure in `admin_test.go` can be extended for provider tests

**One action required before Phase 1 implementation:**
Generate the Python known-vector test vectors (plaintext + encrypted output) using `them-bridge`. This must happen first so that the static constant in `fernet_test.go` is available when the crypto package is written. The vector generation command is in Phase 1 of the test plan above.

---

## First Implementation Task

**Phase 1: Fernet crypto package.**

Steps:
1. Generate known-vector test data (run inside `them-bridge`).
2. Create `go/internal/crypto/fernet.go` with `DeriveKey`, `Encrypt`, `Decrypt`, `pkcs7Pad`, `pkcs7Unpad`.
3. Create `go/internal/crypto/fernet_test.go` with all unit tests including the known-vector test.
4. Run `go test ./internal/crypto/...` — all tests must pass.
5. Update `go/TEST_INDEX.md`.
6. Commit Phase 1.

Stop. Do not proceed to Phase 2 until Phase 1 tests pass and the known-vector test explicitly confirms Python↔Go decryption correctness.

---

## Summary

Wave 7 is a well-scoped migration. The only non-trivial element is Fernet compatibility — but it is fully achievable with Go stdlib, requires no new module dependencies, and can be proven correct with known-vector tests before any production code touches the DB. The Handler → Service → DAL → Crypto pattern fits the established Go admin architecture cleanly. The commit plan separates crypto from DAL from service from handler, allowing each phase to be reviewed and tested in isolation.
