#!/bin/bash
# Performance Test Runner: postgres vs dual mode comparison
# Date: 2026-10-21
# Scenarios: 10/11/12/40/41/42/43 (3 runs each, alternating mode)

set -e

SCRIPT_DIR=$(dirname "$0")
PROJECT_DIR="/opt/docker/them"
RESULTS_FILE="$PROJECT_DIR/perf_test_results_raw.csv"

# Test scenarios
SCENARIOS=(10 11 12 40 41 42 43)
MODES=("postgres" "dual")
RUNS=3

# Initialize results CSV
echo "timestamp,scenario,mode,run,p50_ms,p95_ms,p99_ms,rps,errors,duration_s,pg_count,ch_count,missing,cpu_pct,ram_mb,pg_conn,redis_lag,redis_pending" > "$RESULTS_FILE"

# Helper: Get JWT token
get_jwt() {
    curl -s -X POST http://localhost:8088/api/v1/auth/login \
        -H "Content-Type: application/json" \
        -d '{"username":"admin","password":"admin123"}' | \
        python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])" 2>/dev/null
}

# Helper: Switch Gateway mode
switch_mode() {
    local new_mode=$1
    echo "[$(date +%H:%M:%S)] Switching to mode: $new_mode"

    # Update .env
    sed -i "s/THE_M_EVENTS_GATEWAY=.*/THE_M_EVENTS_GATEWAY=$new_mode/" "$PROJECT_DIR/.env"

    # Recreate bridge
    cd "$PROJECT_DIR"
    docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml stop them-go-bridge > /dev/null 2>&1
    docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge > /dev/null 2>&1

    # Wait for ready
    sleep 10

    # Verify
    actual_mode=$(docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY | cut -d= -f2)
    if [ "$actual_mode" != "$new_mode" ]; then
        echo "ERROR: Mode switch failed. Expected $new_mode, got $actual_mode"
        exit 1
    fi

    echo "[$(date +%H:%M:%S)] ✅ Mode switched to: $new_mode"
}

# Helper: Clear data
clear_data() {
    echo "[$(date +%H:%M:%S)] Clearing test data..."
    docker exec them-postgres psql -U them -d them -c "TRUNCATE gateway_requests CASCADE;" > /dev/null 2>&1
    docker exec them-clickhouse clickhouse-client --query "TRUNCATE TABLE them_events.events_gateway_request;" > /dev/null 2>&1
    docker exec them-redis-events redis-cli FLUSHDB > /dev/null 2>&1
    echo "[$(date +%H:%M:%S)] ✅ Data cleared"
}

# Helper: Collect metrics
collect_metrics() {
    local scenario=$1
    local mode=$2
    local run=$3
    local timestamp=$(date +%s)

    # Client-side metrics from test output (placeholder - will parse test runner output)
    local p50=0 p95=0 p99=0 rps=0 errors=0 duration=0

    # Server-side metrics
    local pg_count=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM gateway_requests;" | tr -d ' ')
    local ch_count=0
    local missing=0
    local redis_lag=0
    local redis_pending=0

    if [ "$mode" = "dual" ]; then
        # Wait for event-writer drain
        sleep 30
        ch_count=$(docker exec them-clickhouse clickhouse-client --query "SELECT count() FROM them_events.events_gateway_request FORMAT TabSeparated;" 2>/dev/null || echo "0")
        missing=$((pg_count - ch_count))
        redis_lag=$(docker exec them-redis-events redis-cli XLEN them:events:gateway 2>/dev/null || echo "0")
        redis_pending=$(docker exec them-redis-events redis-cli XPENDING them:events:gateway event-writers - + 1 2>/dev/null | wc -l)
    fi

    # CPU/RAM (parse docker stats - first value only)
    local stats=$(docker stats them-go-bridge --no-stream --format "{{.CPUPerc}},{{.MemUsage}}" | head -1)
    local cpu_pct=$(echo "$stats" | cut -d, -f1 | tr -d '%')
    local ram_mb=$(echo "$stats" | cut -d, -f2 | awk '{print $1}' | sed 's/MiB//')

    # PG connections
    local pg_conn=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM pg_stat_activity WHERE datname='them';" | tr -d ' ')

    # Write results
    echo "$timestamp,$scenario,$mode,$run,$p50,$p95,$p99,$rps,$errors,$duration,$pg_count,$ch_count,$missing,$cpu_pct,$ram_mb,$pg_conn,$redis_lag,$redis_pending" >> "$RESULTS_FILE"

    if [ "$mode" = "dual" ] && [ "$missing" -gt 0 ]; then
        echo "[$(date +%H:%M:%S)] ⚠️  Missing $missing events in ClickHouse"
    elif [ "$mode" = "dual" ]; then
        echo "[$(date +%H:%M:%S)] ✅ All $pg_count requests in ClickHouse"
    fi
}

# Main test loop
main() {
    echo "========================================="
    echo "Gateway Performance Test: postgres vs dual"
    echo "Date: $(date)"
    echo "Scenarios: ${SCENARIOS[@]}"
    echo "Runs per scenario/mode: $RUNS"
    echo "Total tests: $((${#SCENARIOS[@]} * ${#MODES[@]} * RUNS))"
    echo "========================================="
    echo ""

    # Get JWT once
    echo "[$(date +%H:%M:%S)] Getting admin JWT..."
    ADMIN_JWT=$(get_jwt)
    if [ -z "$ADMIN_JWT" ]; then
        echo "ERROR: Failed to get JWT token"
        exit 1
    fi
    echo "[$(date +%H:%M:%S)] ✅ JWT obtained"
    echo ""

    # Alternating runs: postgres→dual→postgres→dual→postgres→dual
    for run in $(seq 1 $RUNS); do
        # Determine mode order for this run
        if [ $((run % 2)) -eq 1 ]; then
            # Odd runs: postgres first
            mode_order=("postgres" "dual")
        else
            # Even runs: dual first
            mode_order=("dual" "postgres")
        fi

        for mode in "${mode_order[@]}"; do
            echo "========================================="
            echo "RUN $run - MODE: $mode"
            echo "========================================="

            # Switch mode
            switch_mode "$mode"

            # Run all scenarios
            for scenario in "${SCENARIOS[@]}"; do
                echo ""
                echo "[$(date +%H:%M:%S)] Running scenario $scenario (mode: $mode, run: $run)"

                # Clear data before test
                clear_data

                # Run test
                echo "[$(date +%H:%M:%S)] Executing test..."
                ADMIN_JWT=$ADMIN_JWT python3.12 "$PROJECT_DIR/scripts/tests/run_tests.py" $scenario

                # Collect metrics
                collect_metrics $scenario $mode $run

                # Short pause between tests
                sleep 5
            done

            echo ""
        done
    done

    echo ""
    echo "========================================="
    echo "✅ All tests complete"
    echo "Results saved to: $RESULTS_FILE"
    echo "========================================="
}

# Execute
main
