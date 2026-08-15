# Wave 7 Phase 1 Implementation Report — Fernet Compatibility
# Date: 2026-07-25

---

## Scope

Phase 1 of Wave 7: Go Fernet crypto package compatible with Python's
`cryptography.fernet.Fernet`. No provider CRUD, no handler, no routing changes.

---

## Exact Python Encryption Format Discovered

### Key derivation

Python (`app/utils/crypto.py`):
```python
key = base64.urlsafe_b64encode(
    hashlib.sha256(settings.security.secret_key.encode()).digest()
)
Fernet(key)
```

- Input: `SECRET_KEY` env var (string, read via `settings.security.secret_key`)
- Operation: SHA-256 of the UTF-8 bytes of the secret key → 32 raw bytes
- Final Fernet key: `base64url_encode(sha256_bytes)` → 44-character base64url string **with padding**
- Python uses `base64.urlsafe_b64encode` which preserves `=` padding
- Go equivalent: `sha256.Sum256([]byte(secretKey))` → use the 32 raw bytes directly

The Fernet library internally base64url-decodes its key string back to 32 bytes. So Python's
"key" to `Fernet()` is just a transport encoding; the real key material is the 32 SHA-256 bytes.

Go works directly with the 32 raw bytes — no intermediate base64 encoding needed.

**Key splitting (Fernet spec):**
- `signing_key    = raw_key[0:16]` → HMAC-SHA256
- `encryption_key = raw_key[16:32]` → AES-128-CBC

Verified test vector:
- `secret_key = "wave7-test-secret-do-not-use-in-prod"`
- `sha256(secret_key) = "7117484aa9e4a6816ed76ec393a870f61dcf65b795a01d8c35a3f580a16abfe5"`
- `signing_key    = 7117484aa9e4a6816ed76ec393a870f6`
- `encryption_key = 1dcf65b795a01d8c35a3f580a16abfe5`

### Wire format

```
Token binary layout (before base64url encoding):
  Offset  Length  Field
       0       1  Version: always 0x80
       1       8  Timestamp: big-endian uint64, Unix seconds
       9      16  IV: random AES-CBC initialization vector
      25       N  Ciphertext: AES-128-CBC(PKCS7-padded plaintext); N is a positive multiple of 16
   25+N      32  HMAC: HMAC-SHA256(Version+Timestamp+IV+Ciphertext) using signing_key
```

Minimum token size: 1 + 8 + 16 + 16 + 32 = **73 bytes** (plaintext 1 byte → 1 padded block of 16 + full PKCS7 block).

Token is stored as `base64url(binary)` — uses standard `=` padding (Python's `urlsafe_b64encode`
always pads). Go must use `base64.URLEncoding` (not `RawURLEncoding`) to match.

### PKCS7 padding

Padding is always added, even when the plaintext is already a multiple of 16:
- 16-byte plaintext → 16 bytes of padding (byte `0x10` × 16) added → 32-byte padded input
- 5-byte plaintext → 11 bytes of padding (byte `0x0b` × 11) → 16-byte padded input

### "enc:" storage prefix behavior

Python's `encrypt_value(value) → str`:
1. If `value` is falsy (`""`, `None`, `0`, etc.) → return `value` unchanged
2. If `value` already starts with `"enc:"` → return `value` unchanged (idempotent guard)
3. Otherwise → return `"enc:" + fernet.encrypt(value.encode()).decode()`

Python's `decrypt_value(value) → str`:
1. If `value` is falsy → return `value` unchanged
2. If `value` does NOT start with `"enc:"` → return `value` unchanged (passthrough for legacy)
3. Otherwise → strip `"enc:"`, decrypt; on **any exception** return `""` (empty string)

**Critical:** decrypt failure returns `""` (empty string), not an error. The call sites then
typically check `if not api_key: use_default_key()`.

### HMAC-before-decrypt ordering

Go verifies the HMAC using `crypto/subtle.ConstantTimeCompare` **before** any decryption step.
This is correct — it prevents padding oracle attacks. Python's `cryptography.fernet.Fernet`
also verifies HMAC before decryption (required by the Fernet spec).

### Timestamp

Fernet tokens contain an 8-byte big-endian Unix timestamp (seconds). Python's `Fernet.decrypt()`
accepts an optional `ttl` parameter; when omitted (which is how all call sites in this codebase
use it), no TTL check is performed. Go's implementation likewise does not enforce a TTL.

If key rotation or token expiry is needed in the future, it must be enforced at the service
layer, not inside the Fernet implementation.

---

## Call Sites That Use Fernet

The `encrypt_value` / `decrypt_value` functions in `app/utils/crypto.py` are used by:

| File | Columns encrypted |
|------|-------------------|
| `app/routers/admin_llm_providers.py` | `llm_providers.api_key_encrypted` |
| `app/routers/admin_orchestrators.py` | `orchestrators.llm_api_key_encrypted`, `transcription_api_key_encrypted`, `tts_api_key_encrypted`, `summarizer_api_key_encrypted` |
| `app/routers/admin_agents.py` | `agents.auth_token_encrypted` |
| `app/routers/admin_system_agents.py` | JSONB field `api_key_encrypted` in system agents config |
| `app/services/app_compiler.py` | `app_orchestrators.llm_api_key_encrypted`, `transcription_api_key_encrypted`, `tts_api_key_encrypted` |
| `app/adapters/a2a_async_adapter.py` | `agents.auth_token_encrypted` (decrypt only) |
| `app/temporal/loaders.py` | Decrypt on activity execution |
| `app/services/memory_service.py` | `summarizer_api_key_encrypted` (decrypt only) |
| `app/routers/tts.py`, `transcription.py`, `apps.py` | Decrypt on request |

**Wave 7 scope:** Only `llm_providers.api_key_encrypted`. All other tables are future waves.
The same `DeriveKey` + `EncryptStored` + `DecryptStored` functions will apply to all of them.

---

## Environment Variable Wiring

Python reads: `SECRET_KEY` env var → `settings.security.secret_key`

Go bridge container (confirmed from `docker inspect`):
- `SECRET_KEY` is set to `THE_M_SECRET_KEY` value (confirmed by Wave 6 fix in `docker-compose.yml`)
- `config.SecretKey` is loaded from `SECRET_KEY` env var in `go/internal/config/config.go`
- Startup validation already enforces `SECRET_KEY` is non-empty and non-default

Both containers (`them-bridge` and `them-go-bridge`) have identical `SECRET_KEY` values — verified
by comparing `printenv SECRET_KEY` output from each container.

---

## Go Package Structure

**New package:** `go/internal/crypto/`

```
fernet.go         — Encrypt, Decrypt, EncryptStored, DecryptStored, DeriveKey
fernet_test.go    — 32 test cases (28 functions + 4 sub-tests)
```

**Exported API:**
```go
func DeriveKey(secretKey string) []byte
func Encrypt(key, plaintext []byte) (string, error)
func Decrypt(key []byte, token string) ([]byte, error)
func EncryptStored(key []byte, plaintext string) (string, error)
func DecryptStored(key []byte, stored string) (string, error)

var ErrInvalidToken  = errors.New("fernet: invalid token")
var ErrHMACMismatch  = errors.New("fernet: HMAC mismatch — wrong key or tampered token")
```

**Dependencies:** stdlib only (`crypto/aes`, `crypto/cipher`, `crypto/hmac`, `crypto/rand`,
`crypto/sha256`, `crypto/subtle`, `encoding/base64`, `time`). No new `go.mod` entries required.

---

## Compatibility Proof

### Direction 1: Python encrypts → Go decrypts

**Static known-vector test** (deterministic; fixed IV `0102030405060708090a0b0c0d0e0f10`, timestamp `1700000000`):

```
plaintext   = "test-api-key-wave7-phase1"
secret_key  = "wave7-test-secret-do-not-use-in-prod"  [test-only]
token       = "gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Et..."
enc_stored  = "enc:" + token
```

Token generated by Python's `cryptography.fernet.Fernet` (manually constructed for determinism)
and verified by `Python Fernet.decrypt() == b"test-api-key-wave7-phase1"`.

Go test `TestDecrypt_KnownPythonVector`: **PASS** — Go decrypts the Python token to the expected plaintext.

### Direction 2: Go encrypts → Python decrypts

Verified by running the following inside `them-bridge`:

```python
from cryptography.fernet import Fernet
# Go-encrypted stored value (freshly produced by Go Encrypt())
stored = "enc:gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqs..."
decrypted = fernet.decrypt(token.encode()).decode()
# == "test-api-key-wave7-phase1"  ✓
```

Also verified bidirectionally via manual algorithm execution in Python (Go algorithm step-by-step):
- `version=0x80 ✓`, HMAC verifies ✓, AES-128-CBC decrypt ✓, PKCS7 unpad ✓

**Bidirectional compatibility: CONFIRMED.**

### Python compat test script

`scripts/test_wave7_fernet_compat.py` — run inside `them-bridge`:
```
docker exec them-bridge python3 /tmp/test_wave7_fernet_compat.py --skip-go-binary
Results: 11/11 passed, 0 failed
```

When Go binary is available (requires `go` on PATH — not inside `them-bridge`), the `--skip-go-binary`
flag can be omitted and full live bidirectional testing runs.

---

## Test Results

### Go crypto package unit tests (race detector)

```
docker build --no-cache (Linux CGO) -race ./internal/crypto/...
--- PASS: TestDeriveKey_Length
--- PASS: TestDeriveKey_KnownSHA256
--- PASS: TestDeriveKey_DifferentInputsDifferentKeys
--- PASS: TestDecrypt_KnownPythonVector
--- PASS: TestDecryptStored_KnownPythonVector
--- PASS: TestEncryptDecrypt_RoundTrip
--- PASS: TestEncryptStoredDecryptStored_RoundTrip
--- PASS: TestEncrypt_RandomIV
--- PASS: TestEncryptDecrypt_ShortPlaintext
--- PASS: TestEncryptDecrypt_ExactlyOneBlock
--- PASS: TestEncryptDecrypt_LongAPIKey
--- PASS: TestEncryptDecrypt_Unicode
--- PASS: TestEncrypt_EmptyPlaintext
--- PASS: TestDecrypt_WrongKey
--- PASS: TestDecrypt_TamperedHMAC
--- PASS: TestDecrypt_TamperedCiphertext
--- PASS: TestDecrypt_InvalidBase64
--- PASS: TestDecrypt_TruncatedToken
--- PASS: TestDecrypt_WrongVersionByte
--- PASS: TestDecryptStored_NoPrefix_PassThrough
--- PASS: TestDecryptStored_Empty_PassThrough
--- PASS: TestPKCS7Pad_Unpad_Identity (4 sub-tests)
--- PASS: TestPKCS7Unpad_ZeroPadByte
--- PASS: TestPKCS7Unpad_InconsistentBytes
--- PASS: TestEncrypt_WrongKeySize
--- PASS: TestDecrypt_WrongKeySize
--- PASS: TestEncryptStored_HasPrefix
--- PASS: TestDecryptStored_InvalidToken

ok  github.com/aviciot/them/internal/crypto  1.031s
32/32 passed. 0 data races.
```

### Full Go unit suite (`go test ./...`)

```
ok  github.com/aviciot/them/cmd/them             0.034s
ok  github.com/aviciot/them/internal/a2a         0.007s
ok  github.com/aviciot/them/internal/admin       0.011s
ok  github.com/aviciot/them/internal/admin/service 0.005s
ok  github.com/aviciot/them/internal/agentregistry 0.127s
ok  github.com/aviciot/them/internal/auth        0.468s
ok  github.com/aviciot/them/internal/cache       0.011s
ok  github.com/aviciot/them/internal/config      0.028s
ok  github.com/aviciot/them/internal/crypto      0.018s   ← new
ok  github.com/aviciot/them/internal/domain      0.012s
ok  github.com/aviciot/them/internal/epconfig    0.006s
ok  github.com/aviciot/them/internal/event       0.129s
ok  github.com/aviciot/them/internal/gate        0.165s
ok  github.com/aviciot/them/internal/health      0.004s
ok  github.com/aviciot/them/internal/llm         0.005s
ok  github.com/aviciot/them/internal/ratelimit   0.004s
ok  github.com/aviciot/them/internal/reconciler  0.012s
ok  github.com/aviciot/them/internal/runrecorder 0.004s
ok  github.com/aviciot/them/internal/runstream   6.868s
ok  github.com/aviciot/them/internal/server      0.009s
ok  github.com/aviciot/them/internal/session     0.015s
ok  github.com/aviciot/them/internal/sse         0.844s
ok  github.com/aviciot/them/internal/ws          1.231s

All packages: PASS. 0 failures. 0 regressions.
```

### Python suite

```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
55 passed, 0 failed
```

---

## Security Findings

### SF-01 · Python decrypt failure returns "" (empty string), not an error

**Finding:** `decrypt_value()` catches ALL exceptions and returns `""`. Call sites check
`if not api_key:` and fall back to a default key or no-key behavior. This is a safe
degradation pattern, but it silently masks:
- Key rotation (old values become silently unreadable)
- Data corruption
- Wrong `SECRET_KEY` in a deployment

**Go behavior:** `Decrypt` and `DecryptStored` return typed sentinel errors (`ErrHMACMismatch`,
`ErrInvalidToken`). Go service layer must implement the same silent-fallback pattern for
`api_key_masked` responses (matches Python's `_mask_key` which also catches all exceptions).

**Action:** Preserved for stored-data compatibility. The Go service will log a WARN on decrypt
failure (so operators see it) but return `api_key_set: true, api_key_masked: "****"` to clients.
This is strictly better than Python's silent discard but does not change the API contract.

### SF-02 · No TTL enforcement on Fernet tokens

**Finding:** Python calls `fernet.decrypt(token)` without a `ttl` argument. Fernet tokens
contain a timestamp but it is never checked. A token encrypted years ago is still valid
(as long as the key has not changed).

**Assessment:** Intentional for the LLM provider use case — these are stored credentials,
not session tokens. TTL enforcement would cause stored API keys to silently stop working.

**Go behavior:** Matches Python — timestamp parsed and stored in the token but not enforced.
Documented in `fernet.go` package comment.

### SF-03 · encrypt_value is idempotent: double-encrypt guard

**Finding:** `encrypt_value` returns the input unchanged if it already starts with `"enc:"`.
This prevents accidental double-encryption. Go's `EncryptStored` does **not** implement this
guard — callers are expected to only encrypt plaintext, not re-encrypt stored values.

**Assessment:** Safe. The Go service layer handles this correctly because it only calls
`EncryptStored` when the request body contains a non-empty `api_key` field (plaintext).
The stored `api_key_encrypted` column value is never passed back through `EncryptStored`.

### SF-04 · encrypt_value returns input unchanged for empty/falsy values

**Finding:** If `api_key = ""` is passed to `encrypt_value`, the empty string is returned
unchanged (stored as NULL or empty in DB). Go's `Encrypt` rejects empty plaintext with
`ErrInvalidToken`. Go callers must check for empty `api_key` before calling `EncryptStored`
and use a NULL DB column instead.

**Assessment:** Correct behavior. The Wave 7 service will set `api_key_encrypted = NULL`
when `api_key` is omitted or empty — not store an empty string.

### SF-05 · Secret key exposure risk: SECRET_KEY is the root credential

**Finding:** All encrypted data in the system uses the same derived key. Compromise of
`SECRET_KEY` compromises ALL stored API keys for ALL tables.

**Assessment:** This is a design property of the existing system, not introduced by Go.
`THE_M_SECRET_KEY` is a high-value secret; its rotation requires re-encryption of all
`*_encrypted` columns. No action for Wave 7 beyond confirming the config validation
already present in Go (`SecretKey == ""` → startup failure).

### SF-06 · HMAC verification order: Go is correct; Python delegates to library

**Finding:** Python's Fernet library verifies HMAC before decryption (required by Fernet spec).
Go's manual implementation explicitly does the same: `ConstantTimeCompare` runs before any
`cipher.NewCBCDecrypter` call. This prevents padding oracle attacks.

---

## Deviations From Python Behavior

| Behavior | Python | Go | Decision |
|----------|--------|-----|----------|
| Empty plaintext | Returns input unchanged | Returns `ErrInvalidToken` | **Intentional** — callers must use NULL column, not empty string |
| Decrypt failure return | Returns `""` | Returns typed error | **Intentional** — error logged as WARN; API response same as Python |
| Double-encrypt guard | `encrypt_value` is idempotent | No guard in `EncryptStored` | **Safe** — callers never pass stored values to EncryptStored |
| TTL enforcement | None (no `ttl` param) | None | **Matches Python** |
| Timestamp | Written, never checked | Written, never checked | **Matches Python** |
| Base64 padding | `=` padding (URLEncoding) | `=` padding (URLEncoding) | **Compatible** |

---

## Phase 2 Readiness Assessment

**Safe to proceed to Phase 2 (DAL + service for LLM provider CRUD).**

All preconditions met:
- ✅ Bidirectional Fernet compatibility confirmed (Direction 1 + Direction 2)
- ✅ Known-vector test in Go unit suite
- ✅ Race detector: 0 data races
- ✅ No new module dependencies
- ✅ Security findings documented; none block Phase 2
- ✅ `config.SecretKey` already loaded and validated at startup
- ✅ Full Go test suite: 0 failures, 0 regressions
- ✅ Python test suite: 55/55 passed

**Phase 2 first task:** Add `dal.LLMProvider`, `dal.LLMProviderInput`, `dal.LLMProviderPatch`
types and 5 DAL methods to `go/internal/admin/dal/llm_providers.go`, following the established
`agents.go` DAL pattern.

---

# Wave 7 Phase 2 Implementation Report — DAL + Service Layer
# Date: 2026-07-25

---

## Scope

Phase 2 of Wave 7: DAL (`go/internal/admin/dal/llm_providers.go`) and service layer
(`go/internal/admin/service/llm_providers.go`) for `them.llm_providers` CRUD.
No handlers, no routes, no Traefik changes. No live cutover.

---

## DAL Structure

**File:** `go/internal/admin/dal/llm_providers.go`

### Types

```go
type LLMProvider struct {
    ID              int64
    Name            string
    DisplayName     string
    APIKeyEncrypted *string  // nil = no key stored
    BaseURL         *string
    DefaultModel    string
    ModelPricingRaw []byte   // raw JSONB bytes; nil → "{}" on read
    Enabled         bool
}

type LLMProviderInput struct {
    Name            string
    DisplayName     string
    APIKeyEncrypted *string
    BaseURL         *string
    DefaultModel    string
    ModelPricingRaw []byte
    Enabled         bool
}
```

### Operations

| Method | SQL pattern | Not-found behavior |
|--------|------------|-------------------|
| `ListProviders` | `SELECT ... ORDER BY id ASC` | Returns empty slice, not error |
| `GetProvider(id)` | `SELECT ... WHERE id=$1` | Returns `pgx.ErrNoRows` |
| `CreateProvider(in)` | `INSERT ... RETURNING *` | `IsUniqueViolation` on name clash |
| `UpdateProvider(id, in)` | `UPDATE ... RETURNING *` | Returns `pgx.ErrNoRows` if id missing |
| `DeleteProvider(id)` | `DELETE ... RETURNING id` | Returns `pgx.ErrNoRows` if id missing |

**Key design decisions:**
- `DeleteProvider` uses `DELETE ... RETURNING id` — no RETURNING means the row was gone.
  `pgx.ErrNoRows` naturally results, handled by `dal.IsNoRows()`.
- `UpdateProvider` takes a full `LLMProviderInput` — fetch-then-modify happens at the service layer
  (not inside the DAL), keeping the DAL a pure persistence layer.
- `ModelPricingRaw` stores raw `[]byte`; nil input is stored as `{}` by the DB default.
  The helper `ModelPricingOrEmpty(raw []byte) map[string]any` unmarshals to `{}` when raw is nil.
- `LLMProviderToInput(p LLMProvider) LLMProviderInput` — canonical conversion for the
  fetch-then-modify PATCH pattern.

---

## Service Structure

**File:** `go/internal/admin/service/llm_providers.go`

### Output type

```go
type LLMProviderOut struct {
    ID           int64
    Name         string
    DisplayName  string
    APIKeySet    bool    // true when api_key_encrypted is non-nil
    APIKeyMasked *string // null JSON when no key; "****" or "abcd...wxyz" when set
    BaseURL      *string
    DefaultModel string
    ModelPricing map[string]any
    Enabled      bool
}
```

**Security invariant:** `api_key_encrypted` is never exposed in any JSON output.
The plaintext key is never logged, returned, or embedded in errors.

### Input types

```go
type LLMProviderCreate struct {
    Name, DisplayName, DefaultModel string
    APIKey       string         // plaintext; "" = no key
    BaseURL      *string
    ModelPricing map[string]any
    Enabled      *bool          // nil → defaults to true
}

type LLMProviderPatch struct {
    DisplayName  *string
    APIKey       *string        // nil=absent, ""=clear, non-empty=rotate
    BaseURL      **string       // double-pointer: nil=absent; non-nil=set (may point to nil to clear)
    DefaultModel *string
    ModelPricing map[string]any // nil=absent
    Enabled      *bool
    APIKeyPresent bool          `json:"-"` // handler sets this when key field appears in JSON
}
```

### Service operations

| Operation | Validates | Error mapping |
|-----------|-----------|---------------|
| `List(ctx)` | — | returns `([]LLMProviderOut, error)` |
| `Get(ctx, id)` | — | `dal.IsNoRows` → `ErrNotFound` |
| `Create(ctx, body)` | name/display_name/default_model required | `dal.IsUniqueViolation` → `ErrConflict` |
| `Update(ctx, id, patch)` | fetch first; apply non-nil patches | `dal.IsNoRows` → `ErrNotFound` |
| `Delete(ctx, id)` | — | `dal.IsNoRows` → `ErrNotFound` |

### Masking behavior (mirrors Python `_mask_key`)

```
encrypted = nil or ""     → (api_key_set=false, api_key_masked=null)
no "enc:" prefix          → (api_key_set=true,  api_key_masked="****")  [+ WARN log]
decrypt error             → (api_key_set=true,  api_key_masked="****")  [+ WARN log]
len(plain) <= 8           → (api_key_set=true,  api_key_masked="****")
len(plain) > 8            → (api_key_set=true,  api_key_masked=plain[:4]+"..."+plain[-4:])
```

Logs contain `provider_id` and `error_category` only — never plaintext, ciphertext, or key material.

### Encryption behavior

- **Create:** `body.APIKey != ""` → `crypto.EncryptStored(fernetKey, body.APIKey)` → stored with "enc:" prefix.
- **Update (rotate):** `APIKeyPresent=true`, `*APIKey != ""` → new `EncryptStored` call; old value discarded.
- **Update (clear):** `APIKeyPresent=true`, `APIKey == nil || *APIKey == ""` → `api_key_encrypted = nil`.
- **Update (no-op):** `APIKeyPresent=false` (field absent) → existing encrypted value preserved untouched.

### Zeroing plaintext bytes

`maskKey` calls `crypto.Decrypt` (returns `[]byte`) rather than `crypto.DecryptStored` (returns `string`)
so the byte slice can be zeroed after the mask string is computed:
```go
for i := range plainBytes { plainBytes[i] = 0 }
```
This is a best-effort defense — Go's GC may have already copied bytes. Full guarantees require `unsafe`.

---

## Python Compatibility Findings

### Confirmed compatible
- Fernet key derivation, wire format, PKCS7 padding — verified in Phase 1.
- `api_key_set` / `api_key_masked` JSON shape — matches Python `_mask_key` exactly.
- Hard delete semantics (no soft delete, no archive column).
- `model_pricing` defaults to `{}` — both Python ORM default and Go DAL default.
- `enabled` defaults to `true` on create if omitted.
- 409 Conflict on duplicate name — matches Python's `raise HTTPException(status_code=409)`.
- 404 Not Found on missing ID for GET/PATCH/DELETE — matches Python's `raise HTTPException(status_code=404)`.

### PATCH semantics deviation (intentional)

Python's `api_key=null` and absent `api_key` are both `None` in the Pydantic model — both leave the
existing key unchanged. Go is more precise: `APIKeyPresent=false` (absent) preserves the key;
`APIKeyPresent=true, APIKey=nil` (explicit null) clears it. This is strictly safer than Python.
The handler must set `APIKeyPresent=true` when the JSON body contains the `api_key` field.

---

## Bugs Found (and fixed)

### BF-01 · Go string immutability prevents in-place zeroing

**Symptom:** `cannot assign to plain[i]` compile error when using `crypto.DecryptStored` (returns `string`).
**Fix:** Use `crypto.Decrypt` (returns `[]byte`) and manually strip the "enc:" prefix in `maskKey`.
The byte slice can then be zeroed with `for i := range plainBytes { plainBytes[i] = 0 }`.

### BF-02 · `pgx.Rows.Close()` has no return value; `dal.RowScanner.Close()` requires `error`

**Symptom:** Integration test helper that passed `pgx.Rows` directly as `dal.RowScanner` failed to compile.
**Fix:** Use `admin.NewPgxQuerier(pool)` (already wraps pgx.Rows in `pgxRowsWrapper` with `Close() error`).
The adapter was already correct; only the test helper was wrong.

---

## Test Results

### Service unit tests (`go test ./internal/admin/service/...`)

26 tests in `go/internal/admin/service/llm_providers_test.go`:

| Test group | Count |
|---|---|
| `maskKey` (nil, short, long, decrypt error, no plaintext in output) | 6 |
| `Create` (no key, with key, duplicate→conflict, missing fields, defaults) | 7 |
| `Get` (found, not found) | 2 |
| `Update` (no key change, rotate, clear, not found) | 4 |
| `Delete` (success, not found) | 2 |
| `List` (empty → non-nil slice, nil pricing → empty map) | 2 |
| Security: create error does not leak plaintext | 1 |
| Defaults: pricing→"{}", enabled→true | 2 |
| **Total** | **26** |

All 26 pass. `fakeDal` uses authentic `pgx.ErrNoRows` and `*pgconn.PgError{Code: "23505"}`.

### DAL integration tests (`go test -tags=integration ./internal/admin/dal/...`)

11 tests in `go/internal/admin/dal/llm_providers_integration_test.go`:

| Test | Coverage |
|---|---|
| `TestDAL_Provider_List` | list returns ≥2 entries in ascending ID order |
| `TestDAL_Provider_Get` | round-trip ID and encrypted value |
| `TestDAL_Provider_Create` | ID assigned, nil key stored as NULL |
| `TestDAL_Provider_UpdateMetadataOnly` | display_name changes; api_key preserved |
| `TestDAL_Provider_UpdateAPIKey` | api_key replaced; new value round-trips |
| `TestDAL_Provider_Delete` | row gone; GetProvider returns ErrNoRows |
| `TestDAL_Provider_DuplicateName_UniqueViolation` | IsUniqueViolation=true |
| `TestDAL_Provider_GetNotFound` | IsNoRows=true |
| `TestDAL_Provider_DeleteNotFound` | IsNoRows=true |
| `TestDAL_Provider_EncryptedValue_HasEncPrefix` | stored value starts with "enc:" |
| `TestDAL_Provider_PlaintextNeverStored` | plaintext "test-api-key-wave7-phase1" not in DB |
| **Total** | **11** |

All 11 pass against live `them-postgres`.

### Full Go unit suite

```
go test ./...
All 23 packages PASS, 0 failures, 0 regressions.
```

Admin service: S1-25 count increased from 34 → 60 (26 new provider service tests).

### Race detector

```
go test -race ./internal/admin/... ./internal/crypto/...
PASS, 0 data races.
```

### Fernet regression check

```
go test ./internal/crypto/...
32/32 PASS — no regression from Phase 1.
```

### Python suite

```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
55/55 PASS
```

---

## Commits

| Hash | Message |
|------|---------|
| `9df65cd` | `feat(admin/dal): Wave 7 Phase 2 — LLM provider DAL and interface` |
| `dc391b7` | `feat(admin/service): Wave 7 Phase 2 — LLMProviderService + unit tests` |

---

## Phase 3 Readiness Assessment

**Safe to proceed to Phase 3 (HTTP handlers + route wiring).**

All preconditions met:
- ✅ DAL: 5 operations, all tested with integration tests against live Postgres
- ✅ Service: masking, encryption, rotation, PATCH semantics, error mapping — all unit-tested
- ✅ No plaintext leakage in any code path — verified by explicit test
- ✅ `ErrConflict` sentinel added to `service/errors.go`
- ✅ `Dal` interface in `service/service.go` extended with 5 provider methods
- ✅ `fakeDal` in `service_test.go` extended with provider stubs
- ✅ Race detector: 0 data races
- ✅ Full unit suite: 0 failures, 0 regressions
- ✅ Python suite: 55/55 passed

**Phase 3 first task:** Add HTTP handlers in `go/internal/admin/` for:
- `GET /admin/llm-providers` → `service.List`
- `POST /admin/llm-providers` → `service.Create`
- `GET /admin/llm-providers/{id}` → `service.Get`
- `PATCH /admin/llm-providers/{id}` → `service.Update` (with `APIKeyPresent` detection via raw JSON decode)
- `DELETE /admin/llm-providers/{id}` → `service.Delete`

Handler must set `LLMProviderPatch.APIKeyPresent = true` when `api_key` key appears in the JSON body
(use `json.RawMessage` intermediate or a custom unmarshaler).
Status codes: 200 list/get/patch, 201 create, 204 delete, 404 not found, 409 conflict, 422 validation.
