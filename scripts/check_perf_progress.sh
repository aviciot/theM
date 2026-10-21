#!/bin/bash
# Quick progress check for performance test

OUTPUT_FILE="/tmp/claude-1006/-opt-docker-them/6150ade6-7d9e-48fe-a2ac-1e8acf619e12/tasks/bol8hgxpm.output"

if [ ! -f "$OUTPUT_FILE" ]; then
    echo "❌ Test output file not found"
    exit 1
fi

echo "========================================="
echo "Performance Test Progress Check"
echo "Time: $(date '+%H:%M:%S')"
echo "========================================="
echo ""

# Count completed scenarios
COMPLETED=$(grep -c "✅.*-perf-.*: p50=" "$OUTPUT_FILE" 2>/dev/null || echo "0")
echo "Completed tests: $COMPLETED / 42"
echo ""

# Show last 15 lines
echo "Recent output:"
echo "---"
tail -15 "$OUTPUT_FILE"
echo "---"
echo ""

# Check if finished
if grep -q "All tests complete" "$OUTPUT_FILE" 2>/dev/null; then
    echo "🎉 TEST COMPLETE!"
    echo ""
    echo "Results files:"
    ls -lh /opt/docker/them/perf_test_*.json 2>/dev/null
elif grep -q "Error:" "$OUTPUT_FILE" 2>/dev/null; then
    echo "❌ ERROR DETECTED - check output file"
else
    echo "⏳ Test still running..."
    echo ""
    REMAINING=$((42 - COMPLETED))
    echo "Estimated remaining tests: $REMAINING"
fi
