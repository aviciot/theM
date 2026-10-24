# End-User Authentication — Operator Guide
# the-M Platform · Phase 4 · Last updated: 2026-09-10

This guide explains how to configure entry points so that external users (customers
of a tenant's service, partner systems, or internal staff) can reach a the-M application —
and how to control exactly who is admitted.

---

## Concepts

### What is an Entry Point?

An **entry point (EP)** is a door into your application. You create one (or more) when you
build your app in the Canvas. The EP **type** — WebSocket, SSE, A2A — is the *protocol*:
how the client communicates with the-M. The **access policy** is the *lock*: what credential
a caller must present. These two settings are independent.

Example: you can have a WebSocket EP that admits only service-account tokens, or two WebSocket
EPs on the same app — one for mediated calls (tenant-backend token), one for direct end-user
JWTs. Protocol choice and access control are configured separately.

---

## Access Policy — the Lock

When you select an entry point in the Canvas, the right-hand panel shows an **Access Policy** dropdown.
This controls what credential the caller must present:

| Label in UI | Internal name | Credential required | Typical use |
|---|---|---|---|
| Token required | `token` | Opaque bearer token (Admin → Tokens) | Backend service calls, internal tools |
| Public (no auth) | `public` | None | Public demos, open webhooks |
| User JWT (the-M users) | `user_jwt` | the-M HS256 session JWT (staff login) | Internal staff using the-M dashboard |
| External JWT (RS256) | `external_jwt` | RS256 JWT from a third-party IdP (Keycloak etc.) | End-users calling the-M directly via their own IdP |

---

## Allowed Principals — the Caller Type Filter

Every EP also has an **Allowed Principals** column in the database. This controls the *type* of
caller, independently of the credential check.

> **UI:** Select an entry point in the Canvas → the **Allowed Callers** dropdown controls this.
> Voice and WebRTC EPs hide this field (they use a separate admission path).

| Value | Who can connect | Who is blocked |
|---|---|---|
| `internal` *(default)* | Regular opaque tokens, the-M user JWTs | Backend service tokens (`is_backend=true`), external RS256 JWT callers |
| `external` | Backend service tokens (`is_backend=true`), external RS256 JWT callers | Regular tokens, user JWTs |
| `both` | Anyone who passes the access-mode check | No caller-type restriction |

To set it after EP creation:
```sql
UPDATE them.entry_points
SET allowed_principals = 'external'
WHERE application_id = '<app_id>' AND slug = '<ep_slug>';
```

---

## The Three Caller Paths

### Path A — Tenant mediates on behalf of an end-user

```
End user → Tenant backend system → the-M WS/SSE/A2A endpoint
```

The tenant's own backend authenticates its users internally, then calls the-M using a
**service account token** (`is_backend=true`). The end user's identity is passed to the-M
via the `X-External-User` header.

**What this means for the operator:**

1. **Create a service token** — Admin → Tokens → New Token. Note the label.
2. **Mark it as a backend token** (not yet in UI — use DB):
   ```sql
   UPDATE them.access_tokens SET is_backend = true WHERE label = '<label>';
   ```
3. **Configure the EP** — Canvas → select EP → Access Policy: **Token required**, Allowed Callers: **External only**

**What the caller must present:**

- HTTP header: `Authorization: Bearer <service_token>`
- HTTP header: `X-External-User: <end_user_identity>` (any string — email, UUID, etc.)

**What the-M does:**

- Validates the opaque token against the database.
- Checks `is_backend=true` — only then is `X-External-User` trusted and recorded.
- Records the value of `X-External-User` as the session's external user identity.
- The end user has no the-M account and presents no the-M credential.

---

### Path B — End user connects directly (RS256 JWT from their own IdP)

```
End user's app (browser / mobile) → the-M WS/SSE/A2A endpoint
                                      (presents their IdP JWT)
```

The end user holds a JWT issued by a third-party Identity Provider (e.g. Keycloak, Auth0,
Azure AD). They present it as the Bearer token when connecting. the-M validates it via the
tenant's configured JWKS endpoint and extracts the `sub` claim as the user's identity.

No the-M account is needed for the end user.

**What this means for the operator:**

1. **Configure Runtime Identity for the tenant** — Tenant Settings → Runtime Identity tab:

   | Field | Required | What to put |
   |---|---|---|
   | JWKS URI | **Yes** | Full URL to the IdP's JWKS endpoint, e.g. `https://sso.company.com/realms/myapp/protocol/openid-connect/certs` |
   | Issuer | **Yes** | The `iss` claim value from tokens — must match exactly, e.g. `https://sso.company.com/realms/myapp` |
   | Audience | No | The `aud` claim value if you want to enforce it; leave empty to skip |
   | Sub Claim | No | Defaults to `sub`; change if your IdP uses a different claim for user identity (e.g. `preferred_username`) |

   > The issuer must match what the IdP actually puts in the `iss` claim.
   > For Keycloak behind a reverse proxy, use the **external** URL (the one users see),
   > not the internal Docker hostname.

2. **Configure the EP** — Canvas → select EP → Access Policy: **External JWT (RS256)**, Allowed Callers: **External only**

**What the caller must present:**

- HTTP header: `Authorization: Bearer <jwt_from_idp>`

**What the-M does:**

- Fetches the tenant's JWKS URI (cached).
- Validates the JWT signature with RS256.
- Checks `iss` matches the configured Issuer.
- Checks `aud` if configured.
- Extracts the configured sub claim as the session's external user identity.
- `X-External-User` headers are **ignored** on `external_jwt` EPs — the identity comes
  exclusively from the validated JWT.

---

### Path C — the-M staff user (internal)

```
Staff member → the-M WS/SSE endpoint (presents the-M login JWT)
```

A user with a the-M account logs in through the dashboard. Their HS256 session JWT is used
as the bearer credential.

**What this means for the operator:**

- EP Access Policy: **User JWT (the-M users)** — no other setup needed.
- Allowed Principals defaults to `internal` — no DB change needed.

**What the caller must present:**

- HTTP header: `Authorization: Bearer <the_m_user_jwt>` (obtained via `/auth/login`)

---

## Access Control Matrix

| Access Mode | Allowed Principals | RS256 JWT (direct) | Backend token + X-External-User | Regular opaque token | the-M user JWT |
|---|---|---|---|---|---|
| `external_jwt` | `external` | ✅ | ❌ wrong mode | ❌ | ❌ |
| `external_jwt` | `internal` | ❌ 403 | ❌ | ❌ | ❌ |
| `token` | `external` | ❌ | ✅ | ❌ 403 | ❌ |
| `token` | `internal` | ❌ | ❌ 403 | ✅ | ❌ |
| `token` | `both` | ❌ | ✅ | ✅ | ❌ |
| `user_jwt` | `internal` | ❌ | ❌ | ❌ | ✅ |
| `public` | any | ✅ | ✅ | ✅ | ✅ |

---

## Common Scenarios

### Q1: An end user connects through the tenant's backend — how does this work?

Use **Path A**. The tenant's backend system authenticates its own users internally.
It connects to the-M using a service account token with `is_backend=true`, passing
the end user's identity in `X-External-User`.

Configure the EP: `access_mode=token`, `allowed_principals=external`.

From the-M's perspective, the caller is the tenant's backend service. The end user
identity is metadata on the session — they have no the-M credentials.

### Q2: Can the same end user connect directly to the-M, bypassing the tenant backend?

**Only if you configure a separate EP with `access_mode=external_jwt`.**

A `token`+`external` EP expects an opaque backend token. An end-user presenting
their IdP JWT directly will be rejected — wrong mode. You must create a second EP
specifically for direct connections, and configure Runtime Identity for the tenant.

### Q3: How do I restrict the application so only the tenant backend can call it?

Use `access_mode=token`, `allowed_principals=external` on all EPs.

- Backend service token (`is_backend=true`) → ✅ admitted
- End-user IdP JWT presented directly → ❌ rejected (wrong mode)
- Regular the-M token → ❌ rejected (`allowed_principals=external`)
- the-M user JWT → ❌ rejected

### Q4: How do I allow end users to connect directly (without going through the tenant backend)?

Use `access_mode=external_jwt`, `allowed_principals=external` on all EPs, and configure
Runtime Identity for the tenant (JWKS URI + Issuer).

- End-user IdP JWT → ✅ admitted (validated via JWKS)
- Backend service token → ❌ rejected (`external_jwt` mode only accepts JWTs, not opaque tokens)
- Regular the-M token → ❌ rejected
- the-M user JWT → ❌ rejected

### Q5: Can both paths (mediated AND direct) work on the same application?

Yes — use two EPs:

- **EP-mediated**: `access_mode=token`, `allowed_principals=external` — for backend service calls
- **EP-direct**: `access_mode=external_jwt`, `allowed_principals=external` — for direct end-user JWTs

Each EP has its own URL. Route clients to the correct EP based on how they authenticate.

---

## WS / SSE URL Pattern

```
WebSocket:  ws://<host>/{tenant_slug}/apps/{app_slug}/{ep_slug}/ws
SSE:        http://<host>/{tenant_slug}/apps/{app_slug}/{ep_slug}/sse
```

---

## Identity Spoofing — Security Notes

**`X-External-User` is only trusted from backend tokens.**
If a regular token (`is_backend=false`) or a user JWT includes this header, it is silently
ignored. The header cannot be used to spoof another user's identity.

**On `external_jwt` EPs**, the user's identity comes exclusively from the validated JWT's
sub claim (or the configured sub_claim field). The `X-External-User` header is ignored
entirely — even from backend tokens.

Verified by test P1-05 in the E2E suite.

---

## Automated E2E Test

**Script:** `scripts/tests/test_38_phase4_external_jwt.py`

**Requirements:**
- Stack running on `http://localhost:8088`
- Keycloak SSO profile enabled (`--profile sso`)
- `bank` realm present in Keycloak (loaded from `keycloak/bank-realm.json`)
- Python 3.12 with `websockets` library

**Run:**
```bash
python3.12 scripts/tests/test_38_phase4_external_jwt.py
```

**What it proves (37 assertions):**

| Test | What it proves |
|---|---|
| P1-01 | Valid RS256 JWT from IdP → WS admitted (full JWKS validation path working) |
| P1-02 | No token → 401 on `external_jwt` EP |
| P1-03 | Malformed JWT → 401 |
| P1-04 | Wrong audience configured → JWT rejected |
| P1-05 | Spoofed `X-External-User` ignored; sub comes from JWT only |
| P1-06 | Wrong tenant slug → 404 (cross-tenant isolation) |
| P1-07 | `allowed_principals=internal` EP blocks external JWT callers (403) |
| P2-03 | Backend token + `X-External-User` → admitted on `both` EP |
| P2-04 | Backend token alone → admitted |
| P2-05 | Regular token → admitted on `both` EP |
| P2-06 | Backend token on `internal`-only EP → 403 |
| P3-01 | the-M user JWT → admitted on `user_jwt` EP |
| P3-02 | No token → 401 on `user_jwt` EP |
| P3-03 | Invalid JWT → 401 |
| P3-04 | Opaque token → 401 on `user_jwt` EP |

The script creates its own apps and EPs, runs all assertions, and cleans up after itself.
The tenant runtime IDP config is restored to its pre-test state on teardown.

---

## Constraints and Limitations

- **Algorithm:** RS256 only. ES256 is not yet supported.
- **JWKS URI:** Must be `https://` in production. `http://localhost*` and `http://them-*`
  (Docker-internal hostnames) are accepted in development environments.
- **Runtime IDP config is per-tenant.** Each tenant configures its own IdP independently.
  A tenant without runtime IDP config cannot use `external_jwt` EPs — connections are rejected with 401.
- **One IdP per tenant.** Each tenant can configure exactly one JWKS URI / issuer.
  To accept tokens from multiple IdPs, use separate tenants.
- **`is_backend` flag** on access tokens is not settable via the Admin UI today — set it
  directly in the DB after token creation:
  ```sql
  UPDATE them.access_tokens SET is_backend = true WHERE label = '<label>';
  ```
- **Voice EPs** (`voice`, `webrtc`) use their own admission handler and do not enforce
  `allowed_principals`. The Allowed Callers setting is hidden for these EP types in the UI.
