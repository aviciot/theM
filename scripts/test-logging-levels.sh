#!/bin/bash
# Test Logging Levels — Automated Test Execution
# Date: 2026-10-20
# Usage: ./scripts/test-logging-levels.sh [scenario-id]
#   scenario-id: 40 (minimal), 41 (metrics), 42 (debug), 43 (audit), or "all"

set -e

BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Logging Level Testing — the-M Gateway                    ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Check if test-runner backend is running
if ! curl -s http://localhost:8093/health > /dev/null 2>&1; then
    echo -e "${RED}❌ Test runner backend not running on port 8093${NC}"
    echo -e "${YELLOW}Start it with: cd test-runner/backend && go run main.go${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Test runner backend is healthy${NC}"
echo ""

# Function to run a single scenario
run_scenario() {
    local scenario_id=$1
    local scenario_name=$2

    echo -e "${BLUE}▶ Running Scenario ${scenario_id}: ${scenario_name}${NC}"

    # Trigger the test
    response=$(curl -s -X POST "http://localhost:8093/api/scenarios/${scenario_id}-logging-perf-${scenario_name}/run")

    # Extract run_id from response (assumes JSON response with "run_id" field)
    run_id=$(echo "$response" | grep -o '"run_id":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$run_id" ]; then
        echo -e "${RED}  ❌ Failed to start scenario${NC}"
        echo -e "  Response: $response"
        return 1
    fi

    echo -e "${YELLOW}  ⏳ Test started (run_id: ${run_id})${NC}"
    echo -e "${YELLOW}  Waiting for completion...${NC}"

    # Poll for completion (max 5 minutes)
    max_wait=300
    elapsed=0
    while [ $elapsed -lt $max_wait ]; do
        sleep 5
        elapsed=$((elapsed + 5))

        # Check if results endpoint exists (simple check)
        if curl -s "http://localhost:8093/api/results/${run_id}" | grep -q '"status"'; then
            echo -e "${GREEN}  ✓ Scenario completed in ${elapsed}s${NC}"
            echo -e "${GREEN}  View results: http://localhost:3210/results/${run_id}${NC}"
            return 0
        fi

        printf "."
    done

    echo -e "${RED}  ❌ Timeout waiting for completion${NC}"
    return 1
}

# Determine which scenarios to run
case "${1:-all}" in
    40|minimal)
        run_scenario 40 "minimal"
        ;;
    41|metrics)
        run_scenario 41 "metrics"
        ;;
    42|debug)
        run_scenario 42 "debug"
        ;;
    43|audit)
        run_scenario 43 "audit"
        ;;
    all)
        echo -e "${BLUE}Running all 4 logging level scenarios...${NC}"
        echo ""
        run_scenario 40 "minimal"
        echo ""
        run_scenario 41 "metrics"
        echo ""
        run_scenario 42 "debug"
        echo ""
        run_scenario 43 "audit"
        echo ""
        echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
        echo -e "${GREEN}All scenarios complete!${NC}"
        echo ""
        echo -e "${YELLOW}Next steps:${NC}"
        echo -e "  1. Verify body capture: psql -U them -d them -f scripts/verify-logging-levels.sql"
        echo -e "  2. View results in UI: http://localhost:3210/scenarios"
        echo -e "  3. See testing plan: docs/_archive/LOGGING_LEVEL_TESTING_PLAN.md"
        ;;
    *)
        echo -e "${RED}Unknown scenario: $1${NC}"
        echo ""
        echo "Usage: $0 [scenario-id]"
        echo ""
        echo "Options:"
        echo "  40 | minimal  — Test MINIMAL logging level"
        echo "  41 | metrics  — Test METRICS logging level"
        echo "  42 | debug    — Test DEBUG logging level"
        echo "  43 | audit    — Test AUDIT logging level"
        echo "  all           — Run all 4 scenarios (default)"
        exit 1
        ;;
esac

echo ""
echo -e "${GREEN}✓ Test execution complete${NC}"
