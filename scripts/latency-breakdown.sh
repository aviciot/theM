#!/bin/bash
# Gateway Latency Breakdown Analysis
# Analyzes a single request to understand where time is spent

set -e

echo "=== Gateway Latency Breakdown ==="
echo
echo "This will analyze the most recent gateway request."
echo "Run a single-user test first, then run this script."
echo
read -p "Press ENTER to analyze last request..."

# Get the most recent gateway request
RECENT_REQ=$(docker logs them-go-bridge --since 5m 2>&1 | grep "llmgateway: request started" | tail -1)

if [ -z "$RECENT_REQ" ]; then
  echo "❌ No recent gateway requests found in last 5 minutes"
  exit 1
fi

REQUEST_ID=$(echo "$RECENT_REQ" | grep -oP 'request_id":"[^"]+' | cut -d'"' -f3)
echo "Analyzing request: $REQUEST_ID"
echo

# Extract all events for this request
echo "=== Event Timeline ==="
docker logs them-go-bridge --since 5m 2>&1 | grep "$REQUEST_ID" | \
  grep -E "started|workflow|policy|budget|complete" | \
  jq -r '[.time, .msg] | @tsv' 2>/dev/null || \
  grep -oP '{"time":"[^"]+","level":"[^"]+","msg":"[^"]+' | \
  sed 's/{"time":"//' | sed 's/","level":"[^"]*","msg":"/ | /'

echo
echo "=== Timing Analysis ==="

# Get start and complete timestamps
START_TIME=$(docker logs them-go-bridge --since 5m 2>&1 | grep "$REQUEST_ID" | grep "request started" | head -1 | grep -oP '"time":"[^"]+' | cut -d'"' -f3)
WORKFLOW_START=$(docker logs them-go-bridge --since 5m 2>&1 | grep "$REQUEST_ID" | grep "starting appflow workflow" | head -1 | grep -oP '"time":"[^"]+' | cut -d'"' -f3 || echo "")
COMPLETE_TIME=$(docker logs them-go-bridge --since 5m 2>&1 | grep "$REQUEST_ID" | grep "request complete" | head -1 | grep -oP '"time":"[^"]+' | cut -d'"' -f3)

if [ -n "$START_TIME" ] && [ -n "$COMPLETE_TIME" ]; then
  START_SEC=$(date -d "$START_TIME" +%s.%N 2>/dev/null || echo "0")
  COMPLETE_SEC=$(date -d "$COMPLETE_TIME" +%s.%N 2>/dev/null || echo "0")

  if [ "$START_SEC" != "0" ] && [ "$COMPLETE_SEC" != "0" ]; then
    TOTAL_MS=$(awk "BEGIN {printf \"%.0f\", ($COMPLETE_SEC - $START_SEC) * 1000}")
    echo "Total E2E: ${TOTAL_MS} ms"

    if [ -n "$WORKFLOW_START" ]; then
      WORKFLOW_SEC=$(date -d "$WORKFLOW_START" +%s.%N 2>/dev/null || echo "0")
      if [ "$WORKFLOW_SEC" != "0" ]; then
        PRE_WORKFLOW_MS=$(awk "BEGIN {printf \"%.0f\", ($WORKFLOW_SEC - $START_SEC) * 1000}")
        WORKFLOW_MS=$(awk "BEGIN {printf \"%.0f\", ($COMPLETE_SEC - $WORKFLOW_SEC) * 1000}")

        echo "├─ Pre-Temporal (auth, policy, budget): ${PRE_WORKFLOW_MS} ms ($(awk "BEGIN {printf \"%.1f\", ($PRE_WORKFLOW_MS/$TOTAL_MS)*100}")%)"
        echo "└─ Temporal Workflow: ${WORKFLOW_MS} ms ($(awk "BEGIN {printf \"%.1f\", ($WORKFLOW_MS/$TOTAL_MS)*100}")%)"
      fi
    fi
  else
    echo "⚠️  Could not parse timestamps (date command may not support nanoseconds)"
    echo "Start: $START_TIME"
    echo "Complete: $COMPLETE_TIME"
  fi
else
  echo "❌ Could not find start/complete events"
fi

echo
echo "=== Recommendations ==="
LATENCY=$(docker logs them-go-bridge --since 5m 2>&1 | grep "$REQUEST_ID" | grep "latency_ms" | grep -oP 'latency_ms":\K[0-9]+' | head -1)

if [ -n "$LATENCY" ]; then
  echo "Observed latency: ${LATENCY} ms"

  if [ "$LATENCY" -gt 5000 ]; then
    echo "⚠️  High latency (>5s) detected"
    echo "   Likely causes:"
    echo "   1. Temporal worker queue saturation (100 slots)"
    echo "   2. Mock provider artificial delay"
    echo "   3. Database query slow under load"
  elif [ "$LATENCY" -gt 2000 ]; then
    echo "⚠️  Moderate latency (>2s) detected"
    echo "   Check Temporal workflow overhead"
  else
    echo "✅ Good latency (<2s)"
  fi
fi
