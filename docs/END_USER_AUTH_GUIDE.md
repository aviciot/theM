# End-User Authentication — Operator Guide
# the-M Platform · Phase 4 · Last updated: 2026-09-10

This guide covers how to configure entry points so that external users (bank customers,
partner systems) can reach a the-M application — and how to control exactly who can
connect and how.

---

## Concepts

### Access Mode

Every entry point has an **Access Mode** that determines what credential a caller must present.

| Mode | Credential required | Typical use |
|---|---|---|
| `token` | Opaque bearer token from `them.access_tokens` | Backend services, internal tools |
| `public` | None | Public demos, webhooks |
| `user_jwt` | the-M HS256 user JWT (staff login) | Internal staff facing the-M UI |
| `external_jwt` | RS256 JWT from tenant's own IdP (e.g. bank Keycloak) | Bank customers calling the-M directly |

### Allowed Principals

Every entry point also has an **Allowed Principals** policy that controls the *type* of caller,
independently of the credential check.

| Value | Who can connect | Who is blocked |
|---|---|---|
| `internal` | Regular opaque tokens, the-M user JWTs | Backend service tokens, external_jwt callers |
| `external` | Backend service tokens (`is_backend=true`), external_jwt callers | Regular tokens, user JWTs |
| `both` | Everyone (no restriction beyond the access mode) | Nobody |

---

## The Three Caller Paths

### Path A — Bank calls the-M on behalf of a customer (mediated)

```
Bank customer → Bank backend → the-M
```

The bank authenticates its customer internally. The bank's **backend system** holds a
the-M service token with `is_backend=true`. It calls the-M WS/SSE and passes the
customer's identity in the `X-External-User` header.

- the-M trusts `X-External-User` **only** from a caller presenting an `is_backend=true` token.
- The customer never interacts with the-M directly and has no the-M credentials.
- the-M records the customer identity from the header for session attribution.

**Required EP configuration:**
- Access Mode: `token`
- Allowed Principals: `external` (or `both` if internal callers also allowed)

**Required token setup:**
- Create a token via Admin → Tokens
- After creation, set `is_backend=true` via DB (currently no UI toggle): `UPDATE them.access_tokens SET is_backend=true WHERE label='<label>';`

---

### Path B — Bank customer calls the-M directly (direct RS256 JWT)

```
Bank customer → the-M  (presents bank Keycloak JWT)
```

The customer's app (browser/mobile) obtains a JWT directly from the bank's Keycloak
(or any OIDC IdP). It presents that JWT as a Bearer token to the-M. the-M validates it
via JWKS and extracts the `sub` claim as the user's identity.

No the-M account is required for the customer.

**Required EP configuration:**
- Access Mode: `external_jwt`
- Allowed Principals: `external`

**Required tenant configuration (Runtime Identity):**
- Tenant Settings → Runtime Identity tab → fill in JWKS URI + Issuer → Save
- Or via API: `PUT /api/v1/tenant/runtime-idp`

---

### Path C — the-M staff user (internal)

```
Staff member → the-M (presents the-M user JWT from login)
```

A user with a the-M account logs in through the dashboard. Their HS256 session JWT is
used directly as the bearer credential for WS/SSE connections.

**Required EP configuration:**
- Access Mode: `user_jwt`
- Allowed Principals: `internal`

---

## Configuring Runtime Identity (for Path B)

The Runtime Identity tab (Tenant Settings) stores the per-tenant JWKS configuration
used to validate bank-issued JWTs at admission time.

| Field | Required | Notes |
|---|---|---|
| JWKS URI | **Yes** | Full URL to the IdP's JWKS endpoint. Must be HTTPS in production. |
| Issuer | **Yes** | Must match the `iss` claim in tokens exactly (case-sensitive). |
| Audience | No | If set, must match the `aud` claim. Leave empty to skip audience validation. |
| Sub Claim | No | Defaults to `sub`. Change if your IdP uses a different claim for user identity (e.g. `preferred_username`). |

Example values for a bank Keycloak:
```
JWKS URI:  https://keycloak.bank.com/realms/bank/protocol/openid-connect/certs
Issuer:    https://keycloak.bank.com/realms/bank
Audience:  (leave empty unless you want to restrict to a specific client)
Sub Claim: sub
```

> **Important:** The issuer must exactly match what the IdP puts in the `iss` claim.
> For Keycloak behind a reverse proxy, this is the **external** (Traefik/nginx) URL,
> not the internal Docker hostname.

---

## Access Control Matrix

Which combinations admit which callers:

| Access Mode | Allowed Principals | Bank RS256 JWT (direct) | Backend token + X-External-User | Regular opaque token | the-M user JWT |
|---|---|---|---|---|---|
| `external_jwt` | `external` | ✅ | ❌ (wrong mode) | ❌ | ❌ |
| `external_jwt` | `internal` | ❌ 403 | ❌ | ❌ | ❌ |
| `token` | `external` | ❌ | ✅ | ❌ 403 | ❌ |
| `token` | `internal` | ❌ | ❌ 403 | ✅ | ❌ |
| `token` | `both` | ❌ | ✅ | ✅ | ❌ |
| `user_jwt` | `internal` | ❌ | ❌ | ❌ | ✅ |
| `public` | any | ✅ | ✅ | ✅ | ✅ |

---

## Common Scenarios

### Q1: Bank customer calls the-M through the bank — how does this work?

Use **Path A** (mediated). The bank's backend system connects using a backend service
token. The bank controls authentication of its own customers and passes their identity
to the-M via `X-External-User`.

Configure the EP: `access_mode=token`, `allowed_principals=external`.

The bank customer never sees the-M credentials. From the-M's perspective, the caller
is the bank's backend service; the customer identity is metadata on the session.

### Q2: Can the same bank customer call the-M directly, bypassing the bank?

**Only if you configure a second EP with `access_mode=external_jwt`.**

If you have only a `token`+`external` EP, a bank customer presenting their Keycloak JWT
directly will be rejected — the EP expects an opaque backend token, not an RS256 JWT.

To allow direct calls you must:
1. Configure Runtime Identity for the tenant (JWKS URI + issuer)
2. Create an EP with `access_mode=external_jwt`, `allowed_principals=external`

### Q3: How do I restrict an application so only the bank (mediated) can access it — no direct calls?

Use **`access_mode=token`, `allowed_principals=external`** on all EPs.

- Bank backend token (`is_backend=true`) → ✅ admitted
- Bank customer's Keycloak JWT presented directly → ❌ rejected (wrong mode — `external_jwt` not accepted on a `token` EP)
- Regular the-M token → ❌ rejected (`allowed_principals=external` blocks internal callers)
- the-M user JWT → ❌ rejected

### Q4: How do I allow bank customers to access the-M directly, without going through the bank?

Use **`access_mode=external_jwt`, `allowed_principals=external`** on all EPs, and
configure Runtime Identity.

- Bank customer's Keycloak JWT → ✅ admitted (validated via JWKS)
- Bank backend token → ❌ rejected (`external_jwt` EP only accepts RS256 JWTs, not opaque tokens)
- Regular the-M token → ❌ rejected
- the-M user JWT → ❌ rejected

### Q5: Can I allow both paths (bank-mediated AND direct customer JWT) on the same application?

Yes, but **not on the same entry point** — access modes cannot be combined. Use two EPs:

- **EP-1**: `access_mode=external_jwt`, `allowed_principals=external` — for direct customer JWT
- **EP-2**: `access_mode=token`, `allowed_principals=external` — for bank backend mediation

Each EP gets its own WS/SSE URL. Route clients to the appropriate EP based on how they authenticate.

---

## WS / SSE URL Pattern

```
WebSocket:  ws://<host>/{tenant_slug}/apps/{app_slug}/{ep_slug}/ws
SSE:        http://<host>/{tenant_slug}/apps/{app_slug}/{ep_slug}/sse
```

Example for the `default` tenant, app `myapp`, EP `bank-direct`:
```
ws://platform.bank.com/default/apps/myapp/bank-direct/ws
```

---

## Identity Spoofing — Security Note

**`X-External-User` is only trusted from backend tokens.**

If a regular caller (opaque token with `is_backend=false`, or a user JWT) includes an
`X-External-User` header, it is silently ignored. The identity cannot be spoofed by
non-backend callers.

**For `external_jwt` EPs**, the customer's identity comes exclusively from the validated
JWT's `sub` claim (or whichever claim is configured as `sub_claim`). The `X-External-User`
header is ignored entirely on `external_jwt` EPs — even from backend tokens.

This is verified by test P1-05 in the E2E suite.

---

## Automated E2E Test

The full Phase 4 auth flow is covered by an automated integration test.

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
| P1-01 | Valid bank RS256 JWT → WS admitted (full JWKS validation path working) |
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
  A tenant without runtime IDP config cannot use `external_jwt` EPs — all connections are rejected with 401.
- **One IdP per tenant.** Each tenant can have exactly one JWKS URI / issuer configured.
  If you need to accept tokens from multiple IdPs, use separate tenants.
- **`is_backend` flag** on access tokens is not settable via the Admin UI today — set it
  directly in the DB after token creation:
  ```sql
  UPDATE them.access_tokens SET is_backend = true WHERE label = '<label>';
  ```
