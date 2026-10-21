# Schema Documentation: Gateway Requests & Events
# Date: 2026-10-21
# Purpose: Document ID mapping for delivery verification

---

## PostgreSQL: gateway_requests

**Table**: `them.gateway_requests`  
**Primary Key**: `id` (UUID, auto-generated via `gen_random_uuid()`)

### Columns

| Column | Type | Nullable | Default | Description |
|---|---|---|---|---|
| `id` | UUID | NOT NULL | `gen_random_uuid()` | **Primary key** - unique request identifier |
| `tenant_id` | UUID | NOT NULL | - | Foreign key to tenants table |
| `client_id` | UUID | NULL | - | Foreign key to gateway_clients table |
| `token_hash` | TEXT | NULL | - | Hashed access token |
| `provider` | TEXT | NULL | - | LLM provider (openai, anthropic, etc) |
| `model_requested` | TEXT | NULL | - | Model requested by client |
| `model_served` | TEXT | NULL | - | Actual model served |
| `status` | TEXT | NOT NULL | `'ok'` | Request status (ok, error, etc) |
| `http_status` | INTEGER | NULL | - | HTTP response status code |
| `error_code` | TEXT | NULL | - | Error code if failed |
| `tokens_in` | INTEGER | NOT NULL | 0 | Input tokens count |
| `tokens_out` | INTEGER | NOT NULL | 0 | Output tokens count |
| `cost_usd` | NUMERIC(12,8) | NOT NULL | 0 | Request cost in USD |
| `latency_ms` | INTEGER | NULL | - | Request latency (ms) |
| `ttfb_ms` | INTEGER | NULL | - | Time to first byte (ms) |
| `streamed` | BOOLEAN | NOT NULL | false | Whether response was streamed |
| `created_at` | TIMESTAMP WITH TIME ZONE | NOT NULL | `now()` | Request timestamp |

### Indexes

- **Primary**: `gateway_requests_pkey` on `id`
- **Tenant lookup**: `gw_req_tenant_created_idx` on `(tenant_id, created_at DESC)`
- **Client lookup**: `gw_req_client_idx` on `client_id`

### Foreign Keys

- `gateway_requests_client_id_fkey`: `client_id` → `gateway_clients(id)` ON DELETE SET NULL
- `gateway_requests_tenant_id_fkey`: `tenant_id` → `tenants(id)` ON DELETE CASCADE

### Row-Level Security

**Policy**: `gw_req_tenant_isolation`  
**Enforcement**: Forced RLS enabled  
**Rule**: `tenant_id = current_setting('app.tenant_id')`

---

## ClickHouse: events_gateway_request

**Table**: `them_events.events_gateway_request`  
**Engine**: ReplacingMergeTree (deduplication on `event_id`)

### Columns

| # | Column | Type | Description |
|---|---|---|---|
| 1 | `event_id` | UUID | **Event identifier** (NOT request ID) |
| 2 | `schema_version` | UInt16 | Event schema version |
| 3 | `ts` | DateTime64(3, 'UTC') | Event timestamp (millisecond precision) |
| 4 | `tenant_id` | UUID | Tenant identifier |
| 5 | `application_id` | UUID | Application identifier |
| 6 | `client_id` | UUID | Gateway client identifier |
| 7 | `user_id` | String | User identifier |
| 8 | **`request_id`** | **String** | **Maps to PG `gateway_requests.id`** ← KEY FIELD |
| 9 | `trace_id` | String | Trace identifier for distributed tracing |
| 10 | `provider` | LowCardinality(String) | LLM provider |
| 11 | `model_requested` | String | Model requested |
| 12 | `model_served` | String | Model actually served |
| 13 | `status` | LowCardinality(String) | Request status |
| 14 | `http_status` | UInt16 | HTTP status code |
| 15 | `error_code` | LowCardinality(String) | Error code if failed |
| 16 | `tokens_in` | UInt32 | Input tokens |
| 17 | `tokens_out` | UInt32 | Output tokens |
| 18 | `cost_usd` | Decimal(12, 8) | Cost in USD |
| 19 | `latency_ms` | UInt32 | Request latency |
| 20 | `ttfb_ms` | UInt32 | Time to first byte |
| 21 | `streamed` | Bool | Streaming flag |
| 22 | `body_ref` | String | Reference to request/response body storage |

---

## ID Mapping for Delivery Verification

### Key Relationship

```
PostgreSQL: gateway_requests.id (UUID)
    ↓ emitted via event
ClickHouse: events_gateway_request.request_id (String)
```

**Note**: The `request_id` in ClickHouse is stored as a **String** (UUID converted to text), while PostgreSQL stores it as native **UUID** type.

### Verification Query Pattern

```sql
-- 1. Get request IDs from PostgreSQL for a time window
SELECT id::text FROM gateway_requests 
WHERE created_at BETWEEN '2026-10-21 16:55:31+00' AND '2026-10-21 16:55:32+00';

-- 2. Check if those IDs exist in ClickHouse
SELECT request_id FROM them_events.events_gateway_request 
WHERE request_id IN ('uuid1', 'uuid2', ...);

-- 3. Find missing IDs
-- missing_ids = set(pg_ids) - set(ch_ids)
```

### Sample Verification Script

Located at: `scripts/verify_delivery_by_id.py`

**Usage**:
```bash
python3.12 scripts/verify_delivery_by_id.py <test_run_id>
```

**What it does**:
1. Loads test run metadata from test-runner history
2. Queries PostgreSQL for all `gateway_requests.id` in test time window
3. Checks ClickHouse for matching `request_id` values
4. Reports missing IDs (if any)
5. Saves missing ID list to file for investigation

---

## Event Flow

```
1. Gateway receives request
   ↓
2. Gateway writes to gateway_requests (PG) → id generated
   ↓
3. Gateway emits event to Redis Streams (dual mode)
   ↓
4. Event-writer consumes from Redis
   ↓
5. Event-writer writes to ClickHouse → request_id = PG id
```

**Latency observations**:
- PostgreSQL write: Immediate (synchronous)
- Event emission: Non-blocking (fire-and-forget)
- ClickHouse write: Asynchronous (batched, eventual consistency)

**Expected delivery time**: Seconds to minutes depending on:
- Event-writer batch size (100 events/batch)
- Redis queue backlog
- ClickHouse insert buffering

---

## Test Methodology Notes

### Count-Based Verification (INSUFFICIENT)

```python
# WRONG: Cannot confirm specific requests delivered
pg_count = count(*) from gateway_requests where created_at between X and Y
ch_count = count() from events_gateway_request where ts between X and Y
missing = pg_count - ch_count  # NOT RELIABLE
```

**Problems**:
- Time windows may not align exactly (clock skew)
- Multiple tests may run concurrently
- Cannot identify WHICH requests are missing

### ID-Based Verification (CORRECT)

```python
# CORRECT: Track specific request IDs
pg_ids = [id::text from gateway_requests where created_at between X and Y]
ch_ids = [request_id from events_gateway_request where request_id in (...)]
missing_ids = set(pg_ids) - set(ch_ids)
```

**Benefits**:
- Confirms delivery of specific requests
- Can identify exactly which IDs are missing
- No ambiguity about time windows

---

## Common Issues

### 1. UUID String Conversion

PostgreSQL stores `id` as native UUID type, but ClickHouse `request_id` is String.

**Solution**: Cast UUID to text in PostgreSQL queries:
```sql
SELECT id::text FROM gateway_requests
```

### 2. Timestamp Format Differences

- PostgreSQL: `2026-10-21 16:55:31+00` (with timezone)
- ClickHouse: `2026-10-21 16:55:31` (UTC assumed)
- Test-runner JSON: `2026-10-21T16:55:31Z` (ISO 8601)

**Solution**: Convert formats appropriately:
```python
pg_timestamp = data['started_at'].replace('Z', '+00')  # ISO → PG
ch_timestamp = data['started_at'].replace('Z', '')      # ISO → CH
```

### 3. TRUNCATE Timing

If `TRUNCATE gateway_requests` is called during verification, requests disappear.

**Solution**: Don't truncate until AFTER verification completes.

---

## Performance Test Integration

### Before Test
1. Clear old data: `TRUNCATE gateway_requests; TRUNCATE events_gateway_request;`
2. Ensure event-writer healthy and consumer group exists

### During Test
1. Run scenario via test-runner API
2. Record test run ID and timestamps

### After Test (Verification)
1. Query PostgreSQL for all `id::text` in test time window
2. Wait for event-writer drain (check Redis lag = 0)
3. Query ClickHouse for matching `request_id` values
4. Compare ID sets, report missing

### Delivery Criteria
- ✅ **100% delivery**: `len(missing_ids) == 0`
- ❌ **Partial delivery**: `len(missing_ids) > 0` → investigate

---

**Last Updated**: 2026-10-21  
**Verified Against**: PostgreSQL 16, ClickHouse 24.8
