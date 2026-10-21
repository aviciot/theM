#!/bin/bash
# reconcile_events.sh — Compare Postgres and ClickHouse event records
# Verifies dual-mode event delivery correctness

set -e

echo "=== Events Reconciliation Report ==="
echo "Date: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
echo ""

# ─── 1. Check configuration ────────────────────────────────────────────────────
echo "## Configuration"
echo ""
echo "Gateway mode: $(docker exec them-go-bridge sh -c 'echo $THE_M_EVENTS_GATEWAY' 2>/dev/null || echo 'container not running')"
echo "Auth mode: $(docker exec them-auth-go sh -c 'echo $THE_M_EVENTS_AUTH' 2>/dev/null || echo 'container not running')"
echo "Tools mode: $(docker exec them-mcp-service sh -c 'echo $THE_M_EVENTS_TOOLS' 2>/dev/null || echo 'container not running')"
echo ""

# ─── 2. Postgres counts ────────────────────────────────────────────────────────
echo "## Postgres Event Counts"
echo ""
PG_GATEWAY=$(docker exec them-postgres psql -U them -d them -tAc "SELECT COUNT(*) FROM them.gateway_requests" 2>/dev/null || echo "0")
echo "Gateway requests (PG): $PG_GATEWAY"
echo ""

# ─── 3. ClickHouse counts ──────────────────────────────────────────────────────
echo "## ClickHouse Event Counts"
echo ""

# Check if ClickHouse is running
if ! docker ps | grep -q them-clickhouse; then
    echo "⚠️  ClickHouse container not running"
    exit 1
fi

CH_GATEWAY=$(docker exec them-clickhouse clickhouse-client --query "SELECT COUNT(*) FROM them_events.events_gateway_request" 2>/dev/null || echo "0")
echo "Gateway requests (CH): $CH_GATEWAY"

CH_AUTH=$(docker exec them-clickhouse clickhouse-client --query "SELECT COUNT(*) FROM them_events.events_user_activity" 2>/dev/null || echo "0")
echo "User activity (CH): $CH_AUTH"

CH_TOOLS=$(docker exec them-clickhouse clickhouse-client --query "SELECT COUNT(*) FROM them_events.events_tool_call" 2>/dev/null || echo "0")
echo "Tool calls (CH): $CH_TOOLS"
echo ""

# ─── 4. Gateway request reconciliation ─────────────────────────────────────────
echo "## Gateway Request Reconciliation"
echo ""

if [ "$PG_GATEWAY" -eq 0 ] && [ "$CH_GATEWAY" -eq 0 ]; then
    echo "No gateway requests found."
elif [ "$PG_GATEWAY" -gt 0 ] || [ "$CH_GATEWAY" -gt 0 ]; then
    echo "Postgres: $PG_GATEWAY requests"
    echo "ClickHouse: $CH_GATEWAY requests"
    
    DIFF=$((PG_GATEWAY - CH_GATEWAY))
    PCT=$(awk "BEGIN {printf \"%.1f\", ($CH_GATEWAY * 100.0) / $PG_GATEWAY}")
    
    echo "Delivery rate: $PCT%"
    
    if [ "$DIFF" -eq 0 ]; then
        echo "✓ Counts match exactly"
    elif [ "$DIFF" -gt 0 ]; then
        echo "⚠️  Missing $DIFF events in ClickHouse ($PCT% delivered)"
    else
        echo "⚠️  ClickHouse has $((-DIFF)) MORE events than Postgres (duplicates or time skew)"
    fi
    echo ""
    
    # Recent PG records
    echo "### Most Recent 3 Postgres Records:"
    docker exec them-postgres psql -U them -d them -c \
        "SELECT id, tenant_id, provider, model_served, status, tokens_in, tokens_out, cost_usd, 
                to_char(created_at, 'YYYY-MM-DD HH24:MI:SS') as created_at
         FROM them.gateway_requests 
         ORDER BY created_at DESC 
         LIMIT 3" 2>/dev/null || echo "Error reading PG"
    echo ""
    
    # Recent CH records
    echo "### Most Recent 3 ClickHouse Records:"
    docker exec them-clickhouse clickhouse-client --query \
        "SELECT event_id, tenant_id, request_id, provider, model_served, status, 
                tokens_in, tokens_out, cost_usd, 
                formatDateTime(ts, '%Y-%m-%d %H:%M:%S') as ts
         FROM them_events.events_gateway_request FINAL
         ORDER BY ts DESC 
         LIMIT 3
         FORMAT PrettyCompact" 2>/dev/null || echo "Error reading CH"
    echo ""
fi

# ─── 5. Auth events ────────────────────────────────────────────────────────────
echo "## Auth Events"
echo ""

if [ "$CH_AUTH" -eq 0 ]; then
    echo "No auth events in ClickHouse"
else
    echo "User activity events: $CH_AUTH"
    echo ""
    echo "Recent auth events:"
    docker exec them-clickhouse clickhouse-client --query \
        "SELECT event_id, tenant_id, user_id, action, outcome, 
                formatDateTime(ts, '%Y-%m-%d %H:%M:%S') as ts
         FROM them_events.events_user_activity FINAL
         ORDER BY ts DESC 
         LIMIT 5
         FORMAT PrettyCompact" 2>/dev/null || echo "Error"
    echo ""
fi

echo "=== End of Report ==="
