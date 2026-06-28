# Odin Authentication
# Last updated: 2026-06-28

## Two auth paths

### 1. Dashboard login (JWT)
- User POSTs credentials to odin-auth-service:8701 `/api/v1/auth/login`
- Auth service validates against `auth_service.users` table, returns JWT
- JWT used as Bearer token for all REST admin endpoints
- Bridge validates JWT by calling `auth_client.validate_jwt(token)` → HTTP to 8701
- JWT contains: user_id, email, role

### 2. WS orchestrator access (opaque bearer token)
- Admin creates token via `POST /api/v1/admin/tokens`
- Bridge generates `secrets.token_urlsafe(32)`, stores `sha256(token)` in `odin.access_tokens`
- Plaintext returned once to admin — never stored
- Client sends `Authorization: Bearer <token>` on WS connect
- Validation: L1 in-process cache → L2 Redis `odin:session:token:{sha256(token)}` TTL 300s → DB lookup
- On DB hit: write to L2 cache, update `last_used_at`
- Token can be scoped to one orchestrator or any orchestrator

## Token cache invalidation
- `DELETE /api/v1/admin/tokens/{id}` → deletes from DB, publishes to `odin:session:user:{user_id}` for cache bust
- Cache TTL of 300s means revoked tokens can still work for up to 5 minutes (same as Omni)
