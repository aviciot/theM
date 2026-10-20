#!/bin/bash
# Analyze Logging Level Test Results and Generate Analysis
# Date: 2026-10-20
# Usage: ./scripts/analyze-logging-results.sh

set -e

BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Logging Level Test Results Analysis                      ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Function to get DB metrics for a logging level
get_metrics() {
    local level=$1

    docker exec them-postgres psql -U them -d them -t -A -F'|' <<EOF
SELECT
  COUNT(*) as requests,
  COUNT(CASE WHEN status = 'ok' THEN 1 END) as success_count,
  ROUND(AVG(latency_ms)) as avg_ms,
  ROUND(MIN(latency_ms)) as min_ms,
  ROUND(MAX(latency_ms)) as max_ms,
  ROUND(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms)) as p95_ms
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gp.logging_level = '$level'
  AND gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '2 hours'
  AND gr.status = 'ok';
EOF
}

# Function to get body count for a logging level
get_body_count() {
    local level=$1

    docker exec them-postgres psql -U them -d them -t -A <<EOF
SELECT COUNT(*)
FROM them.gateway_request_bodies grb
JOIN them.gateway_requests gr ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gp.logging_level = '$level'
  AND gc.label LIKE 'perf-test-%'
  AND grb.created_at > now() - interval '2 hours';
EOF
}

# Get baseline (MINIMAL) for overhead calculation
echo -e "${YELLOW}Fetching baseline (MINIMAL level)...${NC}"
minimal_metrics=$(get_metrics "minimal")
minimal_avg=$(echo "$minimal_metrics" | cut -d'|' -f3)

if [ -z "$minimal_avg" ] || [ "$minimal_avg" == "" ]; then
    echo -e "${RED}❌ No MINIMAL test data found. Run tests first:${NC}"
    echo -e "   ./scripts/test-logging-levels.sh all"
    exit 1
fi

echo -e "${GREEN}✓ Baseline: ${minimal_avg}ms average${NC}"
echo ""

# Function to generate analysis for one scenario
generate_analysis() {
    local level=$1
    local scenario_id=$2
    local expected_bodies=$3

    echo -e "${BLUE}═══ Analyzing ${level^^} (scenario $scenario_id) ═══${NC}"

    metrics=$(get_metrics "$level")
    body_count=$(get_body_count "$level")

    if [ -z "$metrics" ] || [ "$(echo $metrics | cut -d'|' -f1)" == "0" ]; then
        echo -e "${RED}❌ No test data found for $level${NC}"
        return
    fi

    requests=$(echo "$metrics" | cut -d'|' -f1)
    success=$(echo "$metrics" | cut -d'|' -f2)
    avg_ms=$(echo "$metrics" | cut -d'|' -f3)
    min_ms=$(echo "$metrics" | cut -d'|' -f4)
    max_ms=$(echo "$metrics" | cut -d'|' -f5)
    p95_ms=$(echo "$metrics" | cut -d'|' -f6)

    # Calculate overhead vs MINIMAL
    overhead_pct=$(awk "BEGIN {printf \"%.1f\", (($avg_ms - $minimal_avg) / $minimal_avg * 100)}")

    # Check body capture
    if [ "$expected_bodies" == "yes" ]; then
        if [ "$body_count" -gt 250 ]; then
            body_status="${GREEN}✅ PASS${NC} — Bodies captured ($body_count rows)"
        else
            body_status="${RED}❌ FAIL${NC} — Bodies NOT captured ($body_count rows, expected ~300)"
        fi
    else
        if [ "$body_count" -eq 0 ]; then
            body_status="${GREEN}✅ PASS${NC} — No bodies (as expected)"
        else
            body_status="${RED}❌ FAIL${NC} — Unexpected bodies found ($body_count rows)"
        fi
    fi

    # Overall grade
    success_rate=$(awk "BEGIN {printf \"%.1f\", ($success / $requests * 100)}")

    if [ "$success_rate" == "100.0" ] && ([ "$expected_bodies" == "yes" ] && [ "$body_count" -gt 250 ] || [ "$expected_bodies" == "no" ] && [ "$body_count" -eq 0 ]); then
        grade="A+"
    elif [ "$success_rate" == "100.0" ]; then
        grade="B"
    else
        grade="C"
    fi

    # Print summary
    echo -e "Grade: ${GREEN}${grade}${NC}"
    echo -e "Success: $success/$requests requests (${success_rate}%)"
    echo -e "Latency: avg=${avg_ms}ms, min=${min_ms}ms, max=${max_ms}ms, p95=${p95_ms}ms"
    echo -e "Overhead: ${overhead_pct}% vs MINIMAL"
    echo -e "Body Capture: $body_status"
    echo ""

    # Generate test_result_ai_analysis text
    cat > /tmp/analysis-${scenario_id}.txt <<EOF
✅ Grade: ${grade}

📊 Result: ${success}/${requests} requests passed (${success_rate}%)
⏱️ Latency: avg ${avg_ms}ms, p95 ${p95_ms}ms
📈 Overhead: ${overhead_pct}% vs MINIMAL baseline (${minimal_avg}ms)
🔧 Config: Logging level '${level}' via governance policy

🔍 Findings:
EOF

    if [ "$expected_bodies" == "yes" ]; then
        if [ "$body_count" -gt 250 ]; then
            echo "• ✅ Body capture working: ${body_count} request/response bodies written to gateway_request_bodies" >> /tmp/analysis-${scenario_id}.txt
        else
            echo "• ❌ Body capture FAILED: Only ${body_count} bodies written (expected ~300)" >> /tmp/analysis-${scenario_id}.txt
        fi
    else
        if [ "$body_count" -eq 0 ]; then
            echo "• ✅ No body capture (as expected): 0 rows in gateway_request_bodies" >> /tmp/analysis-${scenario_id}.txt
        else
            echo "• ❌ Unexpected body capture: ${body_count} rows found (should be 0)" >> /tmp/analysis-${scenario_id}.txt
        fi
    fi

    echo "• Performance: ${avg_ms}ms average (+${overhead_pct}% overhead)" >> /tmp/analysis-${scenario_id}.txt
    echo "• All ${requests} requests completed successfully" >> /tmp/analysis-${scenario_id}.txt
    echo "" >> /tmp/analysis-${scenario_id}.txt
    echo "💡 Recommendation: $(if [ "$grade" == "A+" ]; then echo "Implementation working as expected. Ready for production use."; else echo "Review failures/body-capture issues before production rollout."; fi)" >> /tmp/analysis-${scenario_id}.txt

    echo -e "${YELLOW}Analysis saved to: /tmp/analysis-${scenario_id}.txt${NC}"
    echo ""
}

# Analyze all 4 levels
generate_analysis "minimal" "40" "no"
generate_analysis "metrics" "41" "no"
generate_analysis "debug"   "42" "yes"
generate_analysis "audit"   "43" "yes"

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}Analysis Complete!${NC}"
echo ""
echo -e "${YELLOW}Next Steps:${NC}"
echo "1. Review analysis files:"
echo "   cat /tmp/analysis-40.txt  # MINIMAL"
echo "   cat /tmp/analysis-42.txt  # DEBUG"
echo ""
echo "2. Update scenario JSON files:"
echo "   cd test-runner/data/scenarios/"
echo "   # Copy analysis text into 'test_result_ai_analysis' field"
echo ""
echo "3. Or view in UI:"
echo "   http://localhost:3210/scenarios"
echo "   Click Edit → Paste analysis → Save"
echo ""
