# Gateway Per-User Quotas — Organization Guide

**Feature:** Track and enforce quotas per individual user when using the LLM Gateway  
**Status:** ✅ Complete (Phases 1-4)  
**Use Case:** Organizations with closed agents that serve multiple end-users

---

## Table of Contents

1. [Who Is This For? (Customer Types)](#who-is-this-for-customer-types)
2. [the-M Auth Options Explained](#the-m-auth-options-explained)
3. [What Problem Does This Solve?](#what-problem-does-this-solve)
4. [How It Works](#how-it-works)
5. [The Two Tokens Explained](#the-two-tokens-explained)
6. [Real-World Example](#real-world-example)
7. [Technical Implementation](#technical-implementation)
8. [API Reference](#api-reference)
9. [Testing Guide](#testing-guide)

---

## Who Is This For? (Customer Types)

the-M serves **three types of customers**, each with different use cases and auth needs:

### 1. 🎨 **Canvas App Builders** (Interactive Applications)

**Who they are:**
- Organizations building AI-powered apps for their end-users
- Example: HR chatbot, customer support assistant, data analysis tool

**What they build:**
- Multi-agent orchestrated applications in the-M Canvas
- Apps with UI that end-users interact with via web browser

**How end-users connect:**
- **WebSocket** or **Server-Sent Events (SSE)** entry points
- Real-time, conversational interfaces

**Auth method:**
- **Option 1: Token-based** — the-M creates access tokens per session
- **Option 2: User JWT** — Users login with username/password, get JWT
- **Option 3: External JWT** (Keycloak/SSO) — Enterprise single sign-on

**Quota enforcement:**
- ✅ Per-user quotas (Phases 1-2) — already working
- Each logged-in user has their own rate/cost limits

**Example:**
```
End User (Alice) → Opens HR Chatbot in browser
                 → Logs in with alice@company.com
                 → WebSocket connection established
                 → the-M tracks Alice's usage
                 → Enforces Alice's viewer quota (100 req/min, $5/day)
```

---

### 2. 🤖 **Closed Agent Operators** (Headless Services)

**Who they are:**
- Organizations with existing AI services/bots that need LLM access
- Example: Slack bot, cron job, internal automation script, third-party integration

**What they use:**
- **LLM Gateway** — OpenAI-compatible API proxy
- Their own application (not built in the-M Canvas)

**How they connect:**
- **HTTP API** (`/llm/v1/chat/completions`)
- Standard OpenAI format requests

**Auth method:**
- **Gateway Token** (always required) — Service-level API key
- **+ User JWT** (optional, NEW in Phases 3-4) — Identify which end-user

**Quota enforcement:**
- ✅ Gateway-level quotas — rate/cost limits per service (already working)
- ✅ **Per-user quotas (Phases 3-4)** — NEW! When agent includes User JWT

**Example:**
```
Company Slack Bot → Calls the-M Gateway API
                  → Uses Gateway Token (bot authentication)
                  → Optionally includes User JWT (employee identification)
                  → the-M tracks: "Slack Bot made request for Alice"
                  → Enforces Alice's quota, not shared bot quota
```

**This document is for this customer type!** 👈

---

### 3. 🔗 **Agent-to-Agent (A2A) Integrators**

**Who they are:**
- Organizations connecting external autonomous agents
- Example: Security scanner agent, vision analysis agent, external LLM service

**What they build:**
- Agents that call other agents (JSON-RPC 2.0 protocol)
- Multi-agent workflows where agents are external services

**How they connect:**
- **A2A Entry Points** — JSON-RPC over HTTP

**Auth method:**
- **Token-based** (the-M access tokens)
- Agents authenticate with bearer tokens

**Quota enforcement:**
- App-level quotas (governed by App Canvas settings)

**Example:**
```
External Security Agent → Calls the-M A2A endpoint
                        → Analyzes code for vulnerabilities
                        → Returns findings in JSON-RPC format
```

---

### Quick Comparison Table

| Customer Type | What They Build | How Users Connect | Auth Methods | Per-User Quotas |
|---|---|---|---|---|
| **Canvas App Builders** | Interactive apps in the-M | WebSocket/SSE (browser) | Token / User JWT / External JWT | ✅ Yes (Phases 1-2) |
| **Closed Agent Operators** | External bots/services | HTTP API (Gateway) | Gateway Token + User JWT (optional) | ✅ Yes (Phases 3-4) NEW! |
| **A2A Integrators** | External agents | JSON-RPC A2A | Token | App-level only |

---

## the-M Auth Options Explained

the-M supports **four authentication methods** depending on your use case:

### Auth Option 1: Token-Based (Access Tokens)

**What it is:**
- Opaque bearer tokens created by the-M
- Stored in `them.access_tokens` table

**Who uses it:**
- Canvas applications (temporary session tokens)
- A2A agents (long-lived agent tokens)

**How it works:**
```bash
# Create a token via the-M admin API
POST /admin/apps/{app_id}/tokens
{
  "label": "User session for Alice",
  "expires_in": 900  # 15 minutes
}

# Response
{
  "token": "abc123def456...",  # 64-char hex string
  "id": "uuid-here"
}

# Use it
WebSocket: wss://them.example.com/payops_ai/apps/my-app/chat?token=abc123def456...
HTTP: Authorization: Bearer abc123def456...
```

**Quota tracking:**
- ✅ Per-token limits (can be scoped to applications)
- ✅ Per-user limits (when token is associated with user_id)

**Best for:**
- WebSocket/SSE connections (Canvas apps)
- Short-lived user sessions
- A2A agent authentication

---

### Auth Option 2: User JWT (the-M Internal Auth)

**What it is:**
- JWT tokens issued by **the-M's auth service** (them-auth-go)
- Signed with HS256 (HMAC-SHA256)
- Contains user_id, username, role_id

**Who uses it:**
- Canvas app users (after login)
- **Gateway users (NEW in Phase 3/4)** — closed agents identifying end-users

**How it works:**
```bash
# User logs in to the-M
POST /{tenant_slug}/auth/login
{
  "username": "alice@company.com",
  "password": "user-password"
}

# Response
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "...",
  "expires_in": 900
}

# JWT payload (decoded)
{
  "sub": "42",
  "user_id": 42,
  "username": "alice@company.com",
  "role_id": 2,  # developer
  "exp": 1698346500
}

# Use it
Canvas (WebSocket): ?token=eyJhbGc...
Gateway: X-User-JWT: eyJhbGc...
```

**Quota tracking:**
- ✅ Per-user limits based on role (super_admin, developer, analyst, viewer, end_user)
- User's role determines rate_limit (req/min) and cost_limit_daily (USD/day)

**Best for:**
- Canvas apps with user login
- **Gateway with per-user tracking** (Phases 3-4)
- When you want role-based quotas

**NOT for:**
- External SSO integration (use Auth Option 3)

---

### Auth Option 3: External JWT (SSO / Keycloak)

**What it is:**
- JWT tokens issued by **your own identity provider** (Keycloak, Auth0, Okta, etc.)
- the-M validates the signature against your IDP's public key

**Who uses it:**
- Enterprise customers with existing SSO infrastructure
- Canvas apps requiring corporate login (SAML, OAuth2)

**How it works:**
```bash
# User logs in to YOUR Keycloak instance
POST https://your-keycloak.com/auth/realms/{realm}/protocol/openid-connect/token
{
  "grant_type": "password",
  "client_id": "your-app",
  "client_secret": "secret",
  "username": "alice@company.com",
  "password": "user-password"
}

# Your Keycloak returns JWT
{
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...",
  "token_type": "Bearer"
}

# the-M validates it against your public key
Canvas entry point configured with:
- Keycloak URL
- Realm name
- Public key / JWKS endpoint
```

**Quota tracking:**
- ✅ Per-user limits (if JWT contains user_id claim)
- Mapped to the-M's role system

**Best for:**
- Enterprise SSO integration
- Compliance requirements (centralized auth)
- Existing identity infrastructure

---

### Auth Option 4: Gateway Token (Closed Agents Only)

**What it is:**
- Long-lived bearer token for **closed agent services**
- Not for individual users — for the service itself
- Registered in `them.gateway_clients` table

**Who uses it:**
- **Closed agent operators only** (Slack bots, cron jobs, integrations)
- NOT for Canvas apps or A2A agents

**How it works:**
```bash
# the-M admin creates a Gateway client
INSERT INTO them.gateway_clients (tenant_id, token_hash, label)
VALUES ('<tenant-uuid>', 'sha256-hash', 'Company Slack Bot');

# Admin gives you the token
Gateway Token: slack-bot-token-2024-xyz

# Your bot uses it
POST /{tenant_slug}/llm/v1/chat/completions
Authorization: Bearer slack-bot-token-2024-xyz
```

**Quota tracking:**
- ✅ Gateway-level limits (shared across all users of this bot)
- ✅ **Per-user limits (NEW!)** — when combined with User JWT header

**Best for:**
- Slack/Discord bots
- Cron jobs calling LLM APIs
- Internal automation scripts
- Third-party integrations

**Combine with User JWT:**
```http
# Service auth + user identification
Authorization: Bearer slack-bot-token-2024-xyz
X-User-JWT: eyJhbGc...  (Alice's JWT from the-M login)
```

Now the-M knows: "Slack bot made this request on behalf of Alice" → enforces Alice's quota.

---

### Which Auth Should I Use?

**I'm building a Canvas app for end-users:**
→ Use **User JWT** (Auth Option 2) or **External JWT** (Auth Option 3)

**I have a Slack bot / cron job / automation script:**
→ Use **Gateway Token** (Auth Option 4)

**I want per-user quotas for my bot's users:**
→ Use **Gateway Token + User JWT** (Auth Options 4 + 2 combined) ← **This guide!**

**I'm integrating an external agent (A2A):**
→ Use **Token-Based** (Auth Option 1)

**I have enterprise SSO (Keycloak/Okta):**
→ Use **External JWT** (Auth Option 3)

---

## What Problem Does This Solve?

### Before: Shared Quotas (Gateway-Level Only)

Your organization has a closed agent (chatbot, automation script, etc.) that uses the-M's LLM Gateway:

```
❌ PROBLEM:
┌─────────────────────────────────────────────────┐
│ Your Chatbot (PayOps AI)                       │
│   Gateway Token: abc123                         │
│                                                 │
│   All users share ONE quota:                    │
│   • 10,000 requests/hour (company-wide)         │
│   • $1,000/day budget (company-wide)            │
│                                                 │
│   User Alice makes 9,000 requests → 90% used   │
│   User Bob makes 2,000 requests → BLOCKED ❌    │
│                                                 │
│   → Power users exhaust company quota          │
│   → No per-user accountability                 │
└─────────────────────────────────────────────────┘
```

**Problem:** One user can exhaust your entire organization's quota.

### After: Per-User Quotas (User-Level Tracking)

With Phase 3 + Phase 4, your closed agent can identify which user is making each request:

```
✅ SOLUTION:
┌─────────────────────────────────────────────────┐
│ Your Chatbot (PayOps AI)                       │
│   Gateway Token: abc123 (service auth)          │
│   + User JWT (user identification)              │
│                                                 │
│   Per-user quotas by role:                      │
│   • Alice (developer): 5,000 req/min, $100/day │
│   • Bob (viewer): 100 req/min, $5/day          │
│   • Carol (analyst): 1,000 req/min, $50/day    │
│                                                 │
│   Alice uses 4,500 requests → Her 90% used ✓   │
│   Bob uses 200 requests → His quota blocks ✓   │
│   Carol unaffected → Still has full quota ✓    │
│                                                 │
│   → Fair usage per user                         │
│   → Cost accountability by team member          │
└─────────────────────────────────────────────────┘
```

**Solution:** Each user has independent quota limits based on their role.

---

## How It Works

### Phase 3: User Identification (Tracking)

Your closed agent sends **two tokens** in each Gateway request:

1. **Gateway Token** (Authorization header) — Authenticates your service
2. **User JWT** (X-User-JWT header) — Identifies which user made the request

The Gateway records `user_id` in the `gateway_requests` table for analytics.

**No enforcement yet** — just tracking who is using what.

### Phase 4: User Quota Enforcement (Limits)

The Gateway checks the user's role and enforces **two limits**:

1. **Rate Limit** (requests per minute)
   - Stored in Redis: `rl:user:{user_id}:minute:{timestamp}`
   - Returns **429 Too Many Requests** when exceeded

2. **Daily Cost Limit** (USD spent per day)
   - Computed from `SUM(cost_usd)` in `gateway_requests` table
   - Returns **402 Payment Required** when exceeded

**Role-based limits** (from `auth_service.roles`):

| Role | Rate Limit | Daily Cost Limit |
|---|---|---|
| `super_admin` | Unlimited (0) | Unlimited (0) |
| `developer` | 5,000 req/min | $100.00/day |
| `analyst` | 1,000 req/min | $50.00/day |
| `viewer` | 100 req/min | $5.00/day |
| `end_user` | 50 req/min | $1.00/day |

> **Note:** `0` means unlimited. Custom roles can be configured in `auth_service.roles` table.

---

## The Two Tokens Explained

### Token 1: Gateway Token (Service Authentication)

**What it is:**
- An opaque bearer token (like an API key)
- Authenticates your **closed agent service** (chatbot, cron job, etc.)
- Stored in `them.gateway_clients` table

**Where it comes from:**
- Created by the-M admin when registering your closed agent
- Example: `test-gateway-token-12345`

**What it proves:**
- "This request is from PayOps AI's authorized chatbot service"

**HTTP header:**
```http
Authorization: Bearer test-gateway-token-12345
```

**Used for:**
- Gateway service authentication
- Client identity (links to governance profile, app assignment)
- Logging which closed agent made the request

---

### Token 2: User JWT (User Identification)

**What it is:**
- A JSON Web Token (JWT) signed with HS256
- Contains the **the-M user's identity** (user_id, username, role)
- Issued by the-M's auth service (them-auth-go)

**Where it comes from:**
- Your closed agent gets it by calling the-M's login API on behalf of the end-user
- Example login request:

```bash
curl -X POST https://your-them-instance.com/payops_ai/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "alice@payops.com",
    "password": "user-password-here"
  }'
```

**Response:**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "...",
  "expires_in": 900
}
```

**JWT payload (decoded):**
```json
{
  "sub": "42",
  "user_id": 42,
  "username": "alice@payops.com",
  "email": "alice@payops.com",
  "role_id": 2,
  "iat": 1698345600,
  "exp": 1698346500
}
```

**What it proves:**
- "This request is made **on behalf of** Alice (user_id=42, developer role)"

**HTTP header:**
```http
X-User-JWT: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

**Used for:**
- Extracting `user_id` from JWT claims (`sub` or `user_id` field)
- Looking up user's role and quota limits
- Recording usage per user in `gateway_requests.user_id`
- Enforcing per-user rate and cost limits

---

### Side-by-Side Comparison

| Aspect | Gateway Token | User JWT |
|---|---|---|
| **Authenticates** | The closed agent service | The end-user |
| **Like** | API key for your chatbot | User's login session |
| **Created by** | the-M admin (manual) | Your app (via login API) |
| **Lifespan** | Long-lived (until revoked) | Short-lived (15 min, refresh) |
| **Stored where** | `them.gateway_clients` | Not stored (stateless JWT) |
| **Header** | `Authorization: Bearer ...` | `X-User-JWT: ...` |
| **Purpose** | "This is PayOps AI's chatbot" | "Request from user Alice" |
| **Optional?** | ❌ Required for Gateway | ✅ Optional (Phase 3/4 feature) |

---

## Real-World Example

### Scenario: PayOps AI Chatbot

**Setup:**
- Company: PayOps AI (tenant_slug: `payops_ai`)
- Closed Agent: Customer support chatbot
- Users:
  - Alice (developer, employee) → 5,000 req/min, $100/day
  - Bob (viewer, contractor) → 100 req/min, $5/day
  - Carol (analyst, team lead) → 1,000 req/min, $50/day

---

### Step 1: Register Chatbot in the-M Gateway

**the-M Admin creates a Gateway client:**

```sql
INSERT INTO them.gateway_clients (tenant_id, token_hash, label)
VALUES (
  '<payops-tenant-uuid>',
  'abc123...hash',  -- sha256 hash of bearer token
  'PayOps Support Chatbot'
);
```

**the-M Admin gives you the Gateway token:**
```
Gateway Token: payops-support-bot-token-2024
```

Store this securely in your chatbot's config.

---

### Step 2: Implement User Login in Your Chatbot

When a user starts a chat session, your chatbot backend:

1. **Authenticates the user** (your own auth)
2. **Gets a the-M JWT** for that user:

```javascript
// Chatbot backend code
async function loginUserToTheM(username, password) {
  const response = await fetch('https://them.yourcompany.com/payops_ai/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password })
  });
  
  const data = await response.json();
  return data.access_token;  // Store this for the session
}

// Store in session:
session.themJWT = await loginUserToTheM('alice@payops.com', userPassword);
```

---

### Step 3: Send Gateway Requests with Both Tokens

When Alice asks the chatbot a question, your backend sends:

```javascript
// Chatbot sends LLM request
async function askLLM(userMessage, session) {
  const response = await fetch('https://them.yourcompany.com/payops_ai/llm/v1/chat/completions', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${process.env.GATEWAY_TOKEN}`,  // Service auth
      'X-User-JWT': session.themJWT  // User identification (NEW!)
    },
    body: JSON.stringify({
      model: 'claude-sonnet-4-6',
      messages: [{ role: 'user', content: userMessage }],
      max_tokens: 256
    })
  });
  
  return response.json();
}
```

---

### Step 4: the-M Gateway Processes the Request

**What the Gateway does:**

1. **Validates Gateway token** → Looks up client in `gateway_clients`
2. **Validates User JWT** (optional) → Extracts `user_id=42` (Alice)
3. **Looks up Alice's role** → `developer` (role_id=2)
4. **Checks rate limit** → Alice has made 4,500 requests this minute (under 5,000 limit ✓)
5. **Checks cost limit** → Alice has spent $45 today (under $100 limit ✓)
6. **Forwards request to Anthropic** → Gets LLM response
7. **Records usage:**

```sql
INSERT INTO them.gateway_requests (
  tenant_id, client_id, user_id, provider, model_requested,
  tokens_in, tokens_out, cost_usd, status
) VALUES (
  '<payops-tenant-uuid>',
  '<chatbot-client-uuid>',
  42,  -- Alice's user_id (from JWT)
  'anthropic',
  'claude-sonnet-4-6',
  120, 180, 0.0045,  -- Usage from this request
  'ok'
);
```

8. **Returns response** to your chatbot

---

### Step 5: Quota Enforcement in Action

**Case 1: Alice (developer) — Under Quota**
```
✓ Request 4,500/5,000 this minute → 200 OK
✓ Daily spend $45/$100 → 200 OK
✓ Response returned
```

**Case 2: Bob (viewer) — Rate Limited**
```
✗ Request 101/100 this minute → 429 Too Many Requests
{
  "error": {
    "message": "rate limit exceeded",
    "type": "rate_limited",
    "code": "rate_limited"
  }
}
```

**Case 3: Carol (analyst) — Cost Limited**
```
✗ Daily spend $51/$50 → 402 Payment Required
{
  "error": {
    "message": "daily cost limit exceeded",
    "type": "quota_exceeded",
    "code": "quota_exceeded"
  }
}
```

---

## Technical Implementation

### Database Schema

**them.gateway_requests** (metering table):

```sql
CREATE TABLE them.gateway_requests (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    client_id       UUID,           -- Which closed agent
    token_hash      TEXT,
    user_id         INTEGER,        -- NEW (Phase 3) - Which user
    provider        TEXT,
    model_requested TEXT,
    tokens_in       INT,
    tokens_out      INT,
    cost_usd        NUMERIC(12,8),
    status          TEXT,
    created_at      TIMESTAMPTZ,
    
    FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL
);
```

**auth_service.roles** (quota limits):

```sql
CREATE TABLE auth_service.roles (
    id               SERIAL PRIMARY KEY,
    name             TEXT UNIQUE,
    rate_limit       INT DEFAULT 0,      -- requests/minute (0 = unlimited)
    cost_limit_daily NUMERIC(10,2) DEFAULT 0,  -- USD/day (0 = unlimited)
    dashboard_access BOOLEAN DEFAULT true
);
```

**auth_service.users** (user → role mapping):

```sql
CREATE TABLE auth_service.users (
    id            SERIAL PRIMARY KEY,
    username      TEXT UNIQUE,
    email         TEXT,
    role_id       INT REFERENCES auth_service.roles(id),
    password_hash TEXT,
    active        BOOLEAN
);
```

---

### Code Flow (Go Implementation)

**1. OptionalJWTMiddleware** (`go/internal/llmgateway/auth.go`):

```go
func OptionalJWTMiddleware(jwtSecret []byte) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userJWT := r.Header.Get("X-User-JWT")
            if userJWT != "" {
                claims, err := auth.ValidateHS256JWT(userJWT, jwtSecret)
                if err == nil {
                    // Valid JWT - store claims in context
                    ctx := auth.WithClaims(r.Context(), claims)
                    next.ServeHTTP(w, r.WithContext(ctx))
                    return
                }
                // Invalid JWT - log but continue (fail-open)
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

**2. Extract user_id** (`go/internal/llmgateway/handler.go`):

```go
// Section 4: Extract user_id from JWT claims (if present)
var userID int64
claims, hasClaims := auth.ClaimsFromContext(r.Context())
if hasClaims {
    if sub, ok := claims["user_id"].(float64); ok {
        userID = int64(sub)
    } else if sub, ok := claims["sub"].(string); ok {
        if parsed, err := strconv.ParseInt(sub, 10, 64); err == nil {
            userID = parsed
        }
    }
}
```

**3. Check rate limit** (`go/internal/llmgateway/handler.go`):

```go
// Section 5: Quota enforcement
if userID > 0 {
    roleConfig, err := h.platformRoleChecker.GetRoleConfig(r.Context(), userID)
    if err == nil && roleConfig != nil {
        // Rate limit check
        if roleConfig.RateLimit > 0 {
            exceeded, err := h.userRateLimiter.CheckLimit(r.Context(), userID, roleConfig.RateLimit)
            if exceeded {
                writeGatewayErrorWithID(w, requestID, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
                return
            }
        }
        
        // Cost limit check
        if roleConfig.CostLimitDaily > 0 {
            dailySpend, err := h.userQuotaChecker.GetUserDailySpendFromGateway(r.Context(), userID)
            if dailySpend >= roleConfig.CostLimitDaily {
                writeGatewayErrorWithID(w, requestID, http.StatusPaymentRequired, "quota_exceeded", "daily cost limit exceeded")
                return
            }
        }
    }
}
```

**4. Redis rate limiting** (`go/internal/ratelimit/user_limiter.go`):

```go
func (l *UserLimiter) CheckLimit(ctx context.Context, userID int64, rpm int) (bool, error) {
    now := time.Now()
    minute := now.Truncate(time.Minute).Unix()
    key := fmt.Sprintf("rl:user:%d:minute:%d", userID, minute)
    
    count, err := l.rdb.Incr(ctx, key).Result()
    if err != nil {
        return false, err
    }
    
    if count == 1 {
        l.rdb.Expire(ctx, key, 90*time.Second)  // TTL
    }
    
    return count > int64(rpm), nil
}
```

**5. Cost tracking query** (`go/internal/execution/quotas.go`):

```go
func (c *pgxUserQuotaChecker) GetUserDailySpendFromGateway(ctx context.Context, userID int64) (float64, error) {
    q := `
        SELECT COALESCE(SUM(cost_usd), 0)
        FROM them.gateway_requests
        WHERE user_id = $1
          AND created_at >= CURRENT_DATE AT TIME ZONE 'UTC'
    `
    var spend float64
    err := c.pool.QueryRow(ctx, q, userID).Scan(&spend)
    return spend, err
}
```

---

## API Reference

### Login Endpoint

**POST** `/{tenant_slug}/auth/login`

Request:
```json
{
  "username": "alice@payops.com",
  "password": "user-password"
}
```

Response:
```json
{
  "access_token": "eyJhbGc...",
  "refresh_token": "...",
  "token_type": "Bearer",
  "expires_in": 900
}
```

### Gateway Chat Completions

**POST** `/{tenant_slug}/llm/v1/chat/completions`

Headers:
```http
Content-Type: application/json
Authorization: Bearer {gateway_token}
X-User-JWT: {user_jwt}  (optional)
```

Request:
```json
{
  "model": "claude-sonnet-4-6",
  "messages": [
    { "role": "user", "content": "Hello!" }
  ],
  "max_tokens": 256
}
```

Response (success):
```json
{
  "id": "msg_...",
  "object": "chat.completion",
  "model": "claude-sonnet-4-6",
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "Hello! How can I help you?"
    }
  }],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 15,
    "total_tokens": 25
  }
}
```

Response (rate limited):
```json
{
  "error": {
    "message": "rate limit exceeded",
    "type": "rate_limited",
    "code": "rate_limited"
  }
}
```
**Status:** `429 Too Many Requests`

Response (cost limited):
```json
{
  "error": {
    "message": "daily cost limit exceeded",
    "type": "quota_exceeded",
    "code": "quota_exceeded"
  }
}
```
**Status:** `402 Payment Required`

---

## Testing Guide

### E2E Test Scenarios

Three test scenarios are available:

1. **Test 48: User JWT Tracking** (`48-gateway-user-jwt.json`)
   - Verifies `user_id` extraction and recording
   - 3 simple requests with JWT
   - Expected: 100% success, `user_id=80` in DB

2. **Test 49: Rate Limiting** (`49-gateway-rate-limit-user.json`)
   - Sends 120 requests (viewer role: 100 req/min limit)
   - Expected: ~100 succeed (200), ~20 fail (429)

3. **Test 50: Cost Limiting** (`50-gateway-cost-limit-user.json`)
   - Sends expensive requests (viewer role: $5/day limit)
   - ⚠️ **Uses real LLM** — costs $5-10 to run
   - Expected: ~5-10 succeed before 402 error

**Setup instructions:** See `test-runner/data/scenarios/GATEWAY_QUOTA_TESTS.md`

---

### Verification Queries

**Check user_id recording:**
```sql
SELECT id, user_id, model_requested, cost_usd, created_at
FROM them.gateway_requests
WHERE user_id = 80
ORDER BY created_at DESC
LIMIT 10;
```

**Check daily spend for a user:**
```sql
SELECT SUM(cost_usd) as daily_spend
FROM them.gateway_requests
WHERE user_id = 80
  AND created_at >= CURRENT_DATE AT TIME ZONE 'UTC';
```

**Check rate limit keys in Redis:**
```bash
docker exec them-redis redis-cli KEYS "rl:user:80:*"
```

**Get user's role and limits:**
```sql
SELECT u.id, u.username, r.name as role, r.rate_limit, r.cost_limit_daily
FROM auth_service.users u
JOIN auth_service.roles r ON u.role_id = r.id
WHERE u.id = 80;
```

---

## FAQ

### Q: Is the User JWT the same as SSO/Keycloak tokens?

**A:** No. The User JWT is issued by **the-M's internal auth service** (them-auth-go), not by external SSO providers. It's a the-M platform user session token.

### Q: Do I need to send User JWT for every request?

**A:** No, it's optional. The Gateway works in two modes:
- **Without User JWT:** Gateway-level auth only (token-based)
- **With User JWT:** Per-user tracking and quota enforcement

### Q: What happens if the JWT is invalid?

**A:** The Gateway **fails open** — the request continues without user tracking. This prevents the quota system from breaking your service if JWT validation fails.

### Q: Can I use this with Canvas applications?

**A:** Phases 1-2 already support per-user quotas for Canvas applications (WebSocket/SSE entry points). Phases 3-4 extend this to the Gateway for closed agents.

### Q: How do I create custom roles with different limits?

**A:** Insert a new row in `auth_service.roles`:

```sql
INSERT INTO auth_service.roles (name, rate_limit, cost_limit_daily, dashboard_access)
VALUES ('premium_user', 10000, 500.00, true);
```

Then assign users to that role:
```sql
UPDATE auth_service.users
SET role_id = (SELECT id FROM auth_service.roles WHERE name = 'premium_user')
WHERE username = 'vip@payops.com';
```

### Q: How often are quotas checked?

**A:** 
- **Rate limit:** Checked **before** each LLM call (Redis INCR, sub-millisecond)
- **Cost limit:** Checked **before** each LLM call (PostgreSQL SUM query, ~10ms)

### Q: What if a user's role changes mid-day?

**A:** The new limits apply **immediately** on the next request. Spend tracking persists (same `user_id`), so changing from viewer ($5/day) to developer ($100/day) won't reset today's spend — they just get the new higher limit.

---

## Summary

**Gateway Per-User Quotas** allow organizations to:

✅ Track LLM usage per individual user  
✅ Enforce role-based rate limits (requests/minute)  
✅ Enforce role-based cost limits (USD/day)  
✅ Prevent power users from exhausting company quotas  
✅ Maintain cost accountability by team member  

**Two tokens required:**
1. **Gateway Token** — Authenticates your closed agent service
2. **User JWT** — Identifies which end-user made the request

**Implementation:** Add `X-User-JWT` header to Gateway requests with a JWT from the-M's login API.

**Status:** Production-ready, fully tested (13 unit tests + 3 E2E scenarios).

---

**Related Documentation:**
- `docs/CURRENT.md` — Current deployment status
- `docs/SCHEMA.md` — Database schema reference
- `test-runner/data/scenarios/GATEWAY_QUOTA_TESTS.md` — E2E test setup
- `go/internal/llmgateway/` — Gateway handler implementation
- `go/internal/execution/` — Canvas quota enforcement (Phases 1-2)
