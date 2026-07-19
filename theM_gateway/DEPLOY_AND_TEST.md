# THEM Go Gateway — Deploy & Verification Plan

> **Goal:** Start the Go bridge alongside the existing Python stack and verify every layer works end-to-end.
> **Platform:** Windows 11 + Docker Desktop (or Linux VM — both paths covered).
> **Status before starting:** DB dump provided, `.env` filled in, Docker running.

---

## Prerequisites checklist

Before running anything, confirm all of these:

- [ ] Docker Desktop is running (`docker info` returns no error)
- [ ] You are in the `theM_gateway/` directory for all commands
- [ ] `.env` file exists (copy from `.env.example`, fill in required values)
- [ ] DB dump file is available (path noted below)
- [ ] `ANTHROPIC_API_KEY` is set in `.env`
- [ ] Go 1.23+ installed at `%USERPROFILE%\go-sdk\go\bin\go.exe` (already done)

---

## Phase 0 — Prepare the Go service for Docker

These files do not exist yet and must be created before the stack can start.

### 0-A. Create `Dockerfile.go`

Create file `theM_gateway/Dockerfile.go`:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go/go.mod go/go.sum ./
RUN go mod download
COPY go/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -mod=readonly -o /them ./cmd/them/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /them ./them
EXPOSE 8002
ENTRYPOINT ["./them"]
```

### 0-B. Add `them-go-bridge` service to `docker-compose.yml`

Add this service block to `theM_gateway/docker-compose.yml` inside `services:`:

```yaml
  # ============================================================
  # the-M Go Bridge — Go rewrite of the orchestration platform
  # Runs alongside Python bridge for validation. Port 8002.
  # ============================================================
  them-go-bridge:
    build:
      context: .
      dockerfile: Dockerfile.go
    container_name: them-go-bridge
    profiles: [go]
    depends_on:
      them-postgres:
        condition: service_healthy
      them-redis:
        condition: service_healthy
    environment:
      APP_ENV: ${APP_ENV:-development}
      APP_PORT: "8002"
      THE_M_INSTANCE_ID: go-bridge-1
      DATABASE_HOST: them-postgres
      DATABASE_PORT: "5432"
      DATABASE_NAME: them
      DATABASE_USER: ${THE_M_DB_USER:-them}
      DATABASE_PASSWORD: ${THE_M_DB_PASSWORD}
      REDIS_HOST: them-redis
      REDIS_PORT: "6379"
      REDIS_PASSWORD: ${THE_M_REDIS_PASSWORD:-}
      REDIS_DB: "0"
      SECRET_KEY: ${THE_M_SECRET_KEY}
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
      LOG_LEVEL: ${LOG_LEVEL:-INFO}
      LOG_FORMAT: json
    networks:
      - them-network
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:8002/health/live || exit 1"]
      interval: 15s
      timeout: 5s
      retries: 3
    labels:
      - "traefik.enable=false"
    logging:
      driver: "json-file"
      options:
        max-size: "20m"
        max-file: "5"
```

---

## Phase 1 — Start the core stack (Postgres + Redis only)

Start infrastructure first, before applying the schema.

```powershell
# Windows PowerShell — from theM_gateway/ directory
docker compose up -d them-postgres them-redis
docker compose ps
```

**Expected:** both containers healthy within 30 seconds.

```powershell
# Verify Postgres is ready
docker exec them-postgres pg_isready -U them -d them
# Expected: them-postgres:5432 - accepting connections
```

---

## Phase 2 — Apply the database schema

### 2-A. Copy and apply your dump

Replace `<path-to-your-dump.sql>` with the actual file path you provide.

```powershell
# Copy dump into container
docker cp <path-to-your-dump.sql> them-postgres:/tmp/them_dump.sql

# Apply it
docker exec them-postgres psql -U them -d them -f /tmp/them_dump.sql
```

### 2-B. Verify schema was applied

```powershell
docker exec them-postgres psql -U them -d them -c "\dt them.*"
```

**Expected tables (minimum):**
- `them.agents`
- `them.orchestrators`
- `them.access_tokens`
- `them.applications`
- `them.entry_points`
- `them.runs`
- `them.run_steps`
- `them.run_usage`
- `them.task_messages`
- `them.tasks`

```powershell
# Verify row counts — should have seed data
docker exec them-postgres psql -U them -d them -c "
  SELECT 'agents' as tbl, count(*) FROM them.agents
  UNION ALL SELECT 'orchestrators', count(*) FROM them.orchestrators
  UNION ALL SELECT 'access_tokens', count(*) FROM them.access_tokens;
"
```

---

## Phase 3 — Verify `.env` is correct for Go bridge

The Go bridge reads these env vars. Confirm each is set:

```powershell
# Check your .env has all required vars
Select-String -Path .env -Pattern "THE_M_DB_PASSWORD|THE_M_SECRET_KEY|ANTHROPIC_API_KEY"
```

**Required values (must not be blank or default):**
- `THE_M_DB_PASSWORD` — must match what Postgres was started with
- `THE_M_SECRET_KEY` — must not be `change-this-in-production`, min 32 chars
- `ANTHROPIC_API_KEY` — required for live orchestration tests

---

## Phase 4 — Build and start the Go bridge

```powershell
# Build the Go image (first time takes ~2 min to download deps)
docker compose --profile go build them-go-bridge

# Start it
docker compose --profile go up -d them-go-bridge

# Watch startup logs
docker compose --profile go logs -f them-go-bridge
```

**Expected startup log lines (in order):**
```
configuration loaded  ...secret_key=*** jwt_middleware=disabled
postgres connected    host=them-postgres dbname=them
redis connected       addr=them-redis:6379 db=0
server listening      addr=0.0.0.0:8002
```

**Failure modes and fixes:**

| Log message | Cause | Fix |
|---|---|---|
| `SECRET_KEY is required` | `THE_M_SECRET_KEY` not in `.env` | Add it |
| `SECRET_KEY must not use the default value` | Value is `change-this-in-production` | Generate a real key |
| `startup: postgres` | DB not ready or wrong password | Check `THE_M_DB_PASSWORD` |
| `startup: redis` | Redis not running | `docker compose up -d them-redis` |

---

## Phase 5 — Health checks (Go bridge)

Run from host machine.

### T-01: Liveness
```powershell
Invoke-WebRequest -Uri http://localhost:8002/health/live | Select-Object StatusCode, Content
```
**Expected:** `200` — `{"status":"ok"}`

### T-02: Readiness (DB + Redis probed)
```powershell
Invoke-WebRequest -Uri http://localhost:8002/health/ready | Select-Object StatusCode, Content
```
**Expected:** `200` — `{"status":"ok","postgres":"ok","redis":"ok"}`

**If 503:** one of the probes failed. Check `docker compose logs them-go-bridge` for the error.

### T-03: Metrics endpoint
```powershell
Invoke-WebRequest -Uri http://localhost:8002/metrics | Select-Object StatusCode
```
**Expected:** `200` with Prometheus text format.

---

## Phase 6 — Authentication tests

### T-04: Unauthenticated request returns 401

```powershell
Invoke-WebRequest -Uri http://localhost:8002/api/v1/admin/agents `
  -Method GET -ErrorAction SilentlyContinue | Select-Object StatusCode
```
**Expected:** `401`

### T-05: Bearer token validation

Get a valid bearer token from the existing `them.access_tokens` table:

```powershell
# List tokens in DB
docker exec them-postgres psql -U them -d them -c "
  SELECT id, name, LEFT(token_hash, 16)||'...' as token_hash_prefix,
         revoked, expires_at
  FROM them.access_tokens
  WHERE revoked = false
  LIMIT 5;
"
```

You'll need the raw token value (the hash is stored, not the token). Use a token you know or create one:

```powershell
# Create a test token via the Python bridge admin API (if running)
# OR insert directly for testing:
docker exec them-postgres psql -U them -d them -c "
  INSERT INTO them.access_tokens (name, token_hash, permissions, revoked)
  VALUES (
    'go-test-token',
    encode(sha256('test-bearer-token-value-abc123'::bytea), 'hex'),
    ARRAY['read','write'],
    false
  ) RETURNING id;
"
```

Then test:
```powershell
$token = "test-bearer-token-value-abc123"
Invoke-WebRequest -Uri http://localhost:8002/api/v1/admin/agents `
  -Headers @{ Authorization = "Bearer $token" } | Select-Object StatusCode
```
**Expected:** `200` with JSON array (even if empty: `[]`)

---

## Phase 7 — Admin API tests

All require the bearer token from T-05.

```powershell
$token = "test-bearer-token-value-abc123"
$headers = @{ Authorization = "Bearer $token"; "Content-Type" = "application/json" }
$base = "http://localhost:8002/api/v1/admin"
```

### T-06: List agents
```powershell
Invoke-WebRequest -Uri "$base/agents" -Headers $headers | Select-Object StatusCode, Content
```
**Expected:** `200`, JSON array (never `null`)

### T-07: List orchestrators
```powershell
Invoke-WebRequest -Uri "$base/orchestrators" -Headers $headers | Select-Object StatusCode, Content
```
**Expected:** `200`, JSON array

### T-08: List applications
```powershell
Invoke-WebRequest -Uri "$base/applications" -Headers $headers | Select-Object StatusCode, Content
```
**Expected:** `200`, JSON array

### T-09: Get nonexistent resource returns 404
```powershell
Invoke-WebRequest -Uri "$base/agents/99999" -Headers $headers `
  -ErrorAction SilentlyContinue | Select-Object StatusCode
```
**Expected:** `404`

### T-10: Create an agent
```powershell
$body = '{"name":"Test Agent","slug":"test_agent","description":"A test","adapter_type":"ws_mock","enabled":true}'
Invoke-WebRequest -Uri "$base/agents" -Method POST -Headers $headers -Body $body | Select-Object StatusCode, Headers
```
**Expected:** `201`, `Location` header set

---

## Phase 8 — WebSocket orchestration tests

### T-11: Unauthenticated WS returns 401

Use `wscat` (install: `npm install -g wscat`) or PowerShell:

```powershell
# Using curl (HTTP upgrade test without token)
curl -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" `
  -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" `
  http://localhost:8002/ws/orchestrate/myapp/ep1
```
**Expected:** `HTTP/1.1 401 Unauthorized`

### T-12: Authenticated WS connection upgrades

```bash
# In bash (Git Bash on Windows)
# Install wscat if needed: npm install -g wscat
wscat -c "ws://localhost:8002/ws/orchestrate/myapp/ep1" \
  -H "Authorization: Bearer test-bearer-token-value-abc123"
```
**Expected:** connection opens, no immediate error.

### T-13: Full message round-trip (requires ANTHROPIC_API_KEY)

Once connected via wscat, send:
```json
{"type":"message","content":"Say hello in exactly 3 words."}
```

**Expected sequence of server messages:**
```json
{"type":"token","content":"Hello"}
{"type":"token","content":" there"}
{"type":"token","content","!"}
{"type":"done","run_id":"<uuid>"}
```

### T-14: Run recorded in DB

After T-13 completes, verify the run was persisted:
```powershell
docker exec them-postgres psql -U them -d them -c "
  SELECT id, context_id, status, started_at, ended_at
  FROM them.runs
  ORDER BY started_at DESC
  LIMIT 3;
"
```
**Expected:** one row with `status = 'completed'`

---

## Phase 9 — SSE endpoint test

### T-15: SSE stream

```powershell
# Windows PowerShell — SSE is plain HTTP streaming
$token = "test-bearer-token-value-abc123"
curl -N "http://localhost:8002/sse/orchestrate/myapp/ep1?token=$token&message=Hello" `
  -H "Accept: text/event-stream"
```
**Expected:** SSE events streaming, ending with:
```
data: {"type":"done","run_id":"..."}
```

---

## Phase 10 — A2A server test

### T-16: Agent card

```powershell
Invoke-WebRequest -Uri http://localhost:8002/.well-known/agent.json | Select-Object StatusCode, Content
```
**Expected:** `200`, JSON with `name`, `url`, `capabilities`

### T-17: JSON-RPC message/send

```powershell
$body = @'
{
  "jsonrpc": "2.0",
  "method": "message/send",
  "params": {
    "message": {
      "role": "user",
      "parts": [{"kind": "text", "text": "Say hi."}],
      "messageId": "test-001"
    }
  },
  "id": "test-001"
}
'@
Invoke-WebRequest -Uri "http://localhost:8002/a2a/myapp" `
  -Method POST -ContentType "application/json" -Body $body | Select-Object StatusCode, Content
```
**Expected:** `200`, JSON-RPC result with `status.state = "completed"` and text in `artifacts`

### T-18: Unknown method returns JSON-RPC error

```powershell
$body = '{"jsonrpc":"2.0","method":"unknown/method","params":{},"id":"t1"}'
Invoke-WebRequest -Uri "http://localhost:8002/a2a/myapp" `
  -Method POST -ContentType "application/json" -Body $body | Select-Object StatusCode, Content
```
**Expected:** `200` (JSON-RPC always 200), body has `error.code = -32601`

---

## Phase 11 — Rate limiter verification

### T-19: Rate limit enforced

The rate limiter uses Redis INCR. To trigger it, you need a token with a low limit configured, or you can verify the Redis keys directly after a request:

```powershell
# After running T-13 (a WS message), check Redis for rate limit keys
docker exec them-redis redis-cli KEYS "rl:them:token:*"
docker exec them-redis redis-cli KEYS "rl:them:app:*"
```
**Expected:** keys exist with TTL ~90s (INCR counters set during the run)

---

## Phase 12 — Token revocation (multi-pod fix)

### T-20: Revoke a token, verify cross-pod invalidation

This verifies Critical finding #2 is fixed.

```powershell
# Revoke the test token via admin API
Invoke-WebRequest -Uri "http://localhost:8002/api/v1/admin/tokens/<token_id>/revoke" `
  -Method POST -Headers $headers | Select-Object StatusCode

# Check Redis pub/sub channel fired (subscribe and watch for the message)
docker exec them-redis redis-cli SUBSCRIBE "them:token:revoked"
# In another terminal, trigger revocation — should see message appear
```

---

## Phase 13 — Session ghost-set verification

### T-21: No ghost sessions after pod restart

This verifies Critical finding #1 is fixed.

```powershell
# 1. Open a WS connection (T-12)
# 2. Check session exists in Redis
docker exec them-redis redis-cli SMEMBERS "them:ep:<ep_slug>:sessions"
docker exec them-redis redis-cli KEYS "them:session:shadow:*"

# 3. Restart the Go bridge (simulates pod crash)
docker compose --profile go restart them-go-bridge

# 4. Wait for shadow key TTL to expire (default: 90s)
Start-Sleep -Seconds 95

# 5. Open a new WS connection — ghost pruning runs on new registration
# Check the Set is now empty (ghost was pruned):
docker exec them-redis redis-cli SMEMBERS "them:ep:<ep_slug>:sessions"
```
**Expected:** ghost session pruned from Set after shadow TTL expires + new connection triggers pruning.

---

## Phase 14 — Go integration test suite

Run the built-in integration tests (requires all services running):

```powershell
$env:PATH = "$env:USERPROFILE\go-sdk\go\bin;$env:PATH"
$env:DATABASE_HOST = "localhost"      # map container port if needed
$env:DATABASE_PORT = "5432"
$env:DATABASE_PASSWORD = "<your_db_password>"
$env:REDIS_HOST = "localhost"
$env:SECRET_KEY = "<your_secret_key>"

Set-Location "theM_gateway/go"
go test -tags=integration -v ./... 2>&1
```
**Expected:** 4 integration tests pass (health/live, health/ready, WS upgrade, WS message→done)

---

## Phase 15 — Side-by-side comparison (Go vs Python bridge)

With both bridges running, run the same request against both and compare:

```powershell
# Python bridge (port 8001 internal, 8088 via Traefik)
$pyResponse = Invoke-WebRequest -Uri "http://localhost:8088/api/v1/admin/agents" `
  -Headers $headers

# Go bridge (port 8002 direct)
$goResponse = Invoke-WebRequest -Uri "http://localhost:8002/api/v1/admin/agents" `
  -Headers $headers

# Compare agent counts
$pyAgents = ($pyResponse.Content | ConvertFrom-Json).Count
$goAgents = ($goResponse.Content | ConvertFrom-Json).Count
Write-Output "Python agents: $pyAgents  |  Go agents: $goAgents"
```
**Expected:** same agent count from both bridges (reading the same DB).

---

## Summary checklist

| # | Test | Layer | Pass criteria |
|---|---|---|---|
| T-01 | Liveness | Health | 200 `{"status":"ok"}` |
| T-02 | Readiness | Health + DB + Redis | 200 with both probes ok |
| T-03 | Metrics | Prometheus | 200 text/plain |
| T-04 | Unauth admin | Auth | 401 |
| T-05 | Bearer token valid | Auth | 200 |
| T-06 | List agents | Admin CRUD | 200 JSON array |
| T-07 | List orchestrators | Admin CRUD | 200 JSON array |
| T-08 | List applications | Admin CRUD | 200 JSON array |
| T-09 | 404 on missing | Admin CRUD | 404 |
| T-10 | Create agent | Admin CRUD | 201 + Location |
| T-11 | Unauth WS | WebSocket | 401 |
| T-12 | Auth WS upgrade | WebSocket | 101 Switching Protocols |
| T-13 | Full message round-trip | Orchestration | token events + done |
| T-14 | Run persisted in DB | Run recorder | row in them.runs |
| T-15 | SSE stream | SSE | events + done |
| T-16 | Agent card | A2A | 200 JSON card |
| T-17 | A2A message/send | A2A | completed result |
| T-18 | A2A unknown method | A2A | error -32601 |
| T-19 | Rate limit keys in Redis | Rate limiter | rl:them: keys exist |
| T-20 | Token revocation pub/sub | Auth multi-pod | Redis message published |
| T-21 | Ghost session pruned | Session | Set empty after TTL |
| T-22 | Integration test suite | All layers | 4/4 pass |
| T-23 | Go vs Python parity | Side-by-side | same agent count |

---

## What to provide

To start Phase 1:

1. **DB dump** — paste the file path or upload the `.sql` file
2. **`.env` with real values** — specifically:
   - `THE_M_DB_PASSWORD`
   - `THE_M_SECRET_KEY` (any 32+ char random string, not the default)
   - `ANTHROPIC_API_KEY`
   - `THE_M_REDIS_PASSWORD` (blank is fine if Redis has no auth)
3. **Confirm Docker Desktop is running** — `docker info` works

Once you provide those, I'll run Phase 0 (create Dockerfile.go + add service to compose), then walk through all 23 tests.
