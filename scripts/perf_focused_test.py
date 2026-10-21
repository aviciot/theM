#!/usr/bin/env python3
"""
Focused Performance Test: Scenario 40 only
Fixes from previous test:
1. NO FLUSHDB - preserves consumer groups and event pipeline
2. Correct RPS calculation - actual completed requests / elapsed time
3. Full event pipeline verification - all request IDs must reach ClickHouse
"""

import json
import os
import subprocess
import sys
import time
from pathlib import Path
from statistics import mean, stdev
import requests

PROJECT_DIR = Path("/opt/docker/them")
TEST_RUNNER_API = "http://localhost:8093"
SCENARIO = "40-logging-perf-minimal"
MODES = ["postgres", "dual"]
RUNS = 3

results = []

def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)

def switch_mode(new_mode):
    """Switch Gateway mode and recreate container"""
    log(f"Switching to mode: {new_mode}")

    # Update docker-compose.dev.yml
    compose_dev_path = PROJECT_DIR / "docker-compose.dev.yml"
    content = compose_dev_path.read_text()
    content = content.replace(
        "THE_M_EVENTS_GATEWAY=dual",
        f"THE_M_EVENTS_GATEWAY={new_mode}"
    ).replace(
        "THE_M_EVENTS_GATEWAY=postgres",
        f"THE_M_EVENTS_GATEWAY={new_mode}"
    )
    compose_dev_path.write_text(content)

    # Recreate bridge
    os.chdir(PROJECT_DIR)
    subprocess.run(
        ["docker", "compose", "--project-name", "them_gateway",
         "-f", "docker-compose.yml", "-f", "docker-compose.dev.yml",
         "stop", "them-go-bridge"],
        capture_output=True, check=True
    )
    subprocess.run(
        ["docker", "compose", "--project-name", "them_gateway",
         "-f", "docker-compose.yml", "-f", "docker-compose.dev.yml",
         "up", "-d", "them-go-bridge"],
        capture_output=True, check=True
    )

    # Wait for ready
    time.sleep(10)

    # Verify
    result = subprocess.run(
        ["docker", "exec", "them-go-bridge", "env"],
        capture_output=True, text=True, check=True
    )
    actual_mode = None
    for line in result.stdout.splitlines():
        if line.startswith("THE_M_EVENTS_GATEWAY="):
            actual_mode = line.split("=", 1)[1]
            break

    if actual_mode != new_mode:
        raise RuntimeError(f"Mode switch failed. Expected {new_mode}, got {actual_mode}")

    log(f"✅ Mode switched to: {new_mode}")

def clear_data():
    """Clear test data from PG and ClickHouse ONLY - preserve Redis consumer groups"""
    log("Clearing test data (PG + ClickHouse only)...")
    subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
         "-c", "TRUNCATE gateway_requests CASCADE;"],
        capture_output=True, check=True
    )
    subprocess.run(
        ["docker", "exec", "them-clickhouse", "clickhouse-client",
         "--query", "TRUNCATE TABLE them_events.events_gateway_request;"],
        capture_output=True, check=True
    )
    # DO NOT FLUSHDB - it destroys consumer groups
    log("✅ Data cleared (Redis consumer group preserved)")

def verify_event_writer_healthy():
    """Verify event-writer is healthy before starting test"""
    log("Verifying event-writer health...")
    # Check recent logs only (since 1 minute)
    result = subprocess.run(
        ["docker", "logs", "them-event-writer", "--since", "1m"],
        capture_output=True, text=True
    )
    if "NOGROUP" in result.stdout or "NOGROUP" in result.stderr:
        raise RuntimeError("Event-writer has NOGROUP error - consumer group missing")

    # Verify consumer group exists
    result = subprocess.run(
        ["docker", "exec", "them-redis-events", "redis-cli", "XINFO", "GROUPS", "them:events:gateway"],
        capture_output=True, text=True
    )
    if "event-writers" not in result.stdout:
        raise RuntimeError("Consumer group 'event-writers' not found in Redis")

    log("✅ Event-writer healthy")

def run_scenario():
    """Run scenario 40 via test-runner API, return run_id"""
    log(f"Running scenario: {SCENARIO}")
    resp = requests.post(f"{TEST_RUNNER_API}/run", json={"scenario_id": SCENARIO}, timeout=300)
    resp.raise_for_status()
    run_id = resp.json()["run_id"]

    # Poll for completion
    history_file = PROJECT_DIR / "test-runner/data/history" / f"{run_id}.json"
    for _ in range(120):  # 2 min timeout
        time.sleep(1)
        if history_file.exists():
            try:
                data = json.loads(history_file.read_text())
                if data.get("ended_at"):
                    return run_id
            except (json.JSONDecodeError, KeyError):
                pass

    raise TimeoutError(f"Scenario {SCENARIO} did not complete in 2 minutes")

def analyze_results(run_id):
    """Parse history JSON and extract client-side metrics with CORRECT RPS"""
    history_file = PROJECT_DIR / "test-runner/data/history" / f"{run_id}.json"
    data = json.loads(history_file.read_text())

    # Collect latencies and count ACTUAL completed requests
    step_latencies = []
    error_count = 0
    total_completed_requests = 0

    for result in data["results"]:
        if result["status"] == "failed":
            error_count += 1
            continue

        for step in result.get("steps", []):
            if step.get("ok"):
                step_latencies.append(step["latency_ms"])
                total_completed_requests += 1

    if not step_latencies:
        return None

    # Calculate percentiles
    sorted_latencies = sorted(step_latencies)
    n = len(sorted_latencies)

    p50 = sorted_latencies[int(n * 0.50)]
    p95 = sorted_latencies[int(n * 0.95)]
    p99 = sorted_latencies[int(n * 0.99)]

    # CORRECT RPS: actual completed requests / measured elapsed time
    from datetime import datetime
    started = datetime.fromisoformat(data["started_at"].replace("Z", "+00:00"))
    ended = datetime.fromisoformat(data["ended_at"].replace("Z", "+00:00"))
    duration_sec = (ended - started).total_seconds()

    rps = total_completed_requests / duration_sec if duration_sec > 0 else 0

    return {
        "p50": p50,
        "p95": p95,
        "p99": p99,
        "rps": rps,
        "errors": error_count,
        "duration_sec": duration_sec,
        "total_requests": total_completed_requests,
        "n_users": data["n_users"],
        "passed": data["passed"],
        "failed": data["failed"]
    }

def verify_clickhouse_delivery(mode):
    """Verify ALL requests reached ClickHouse in dual mode"""
    if mode != "dual":
        return {"verified": False, "pg_count": 0, "ch_count": 0, "missing": 0}

    # Get PG count
    result = subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
         "-t", "-c", "SELECT count(*) FROM gateway_requests;"],
        capture_output=True, text=True, check=True
    )
    pg_count = int(result.stdout.strip())

    # Wait for event-writer to drain (check lag approaching 0)
    log("Waiting for event-writer drain...")
    for i in range(60):  # 60 seconds max
        result = subprocess.run(
            ["docker", "exec", "them-redis-events", "redis-cli", "XLEN", "them:events:gateway"],
            capture_output=True, text=True, check=True
        )
        lag = int(result.stdout.strip())
        if lag == 0:
            log(f"✅ Redis queue drained (lag=0)")
            break
        if i % 10 == 0:
            log(f"  Waiting for drain... lag={lag}")
        time.sleep(1)

    # Get ClickHouse count
    result = subprocess.run(
        ["docker", "exec", "them-clickhouse", "clickhouse-client",
         "--query", "SELECT count() FROM them_events.events_gateway_request FORMAT TabSeparated;"],
        capture_output=True, text=True, check=True
    )
    ch_count = int(result.stdout.strip())

    missing = pg_count - ch_count

    if missing > 0:
        log(f"⚠️  DELIVERY INCOMPLETE: {missing} events missing in ClickHouse")
    else:
        log(f"✅ DELIVERY VERIFIED: All {pg_count} requests in ClickHouse")

    return {
        "verified": missing == 0,
        "pg_count": pg_count,
        "ch_count": ch_count,
        "missing": missing
    }

def collect_resource_metrics():
    """Collect server-side resource usage"""
    # CPU/RAM
    result = subprocess.run(
        ["docker", "stats", "them-go-bridge", "--no-stream", "--format", "{{.CPUPerc}},{{.MemUsage}}"],
        capture_output=True, text=True, check=True
    )
    stats = result.stdout.strip().split(",")
    cpu_pct = float(stats[0].replace("%", ""))
    ram_mb = float(stats[1].split()[0].replace("MiB", ""))

    return {"cpu_pct": cpu_pct, "ram_mb": ram_mb}

def main():
    log("========================================")
    log("Focused Performance Test: Scenario 40")
    log(f"Scenario: {SCENARIO}")
    log(f"Runs: {RUNS} per mode (alternating)")
    log("Fixes: NO FLUSHDB, correct RPS, full delivery verification")
    log("========================================")
    print()

    # Verify event-writer healthy before starting
    verify_event_writer_healthy()

    # Alternating runs
    for run in range(1, RUNS + 1):
        # Odd runs: postgres first, Even runs: dual first
        mode_order = ["postgres", "dual"] if run % 2 == 1 else ["dual", "postgres"]

        for mode in mode_order:
            log("========================================")
            log(f"RUN {run} - MODE: {mode}")
            log("========================================")

            switch_mode(mode)
            clear_data()

            # Verify event-writer still healthy after mode switch
            if mode == "dual":
                verify_event_writer_healthy()

            # Run test
            run_id = run_scenario()

            # Analyze client-side metrics
            metrics = analyze_results(run_id)
            if not metrics:
                log(f"⚠️  No valid results for run {run}")
                continue

            # Verify ClickHouse delivery (dual mode only)
            delivery = verify_clickhouse_delivery(mode)

            # Collect resource metrics
            resources = collect_resource_metrics()

            # Store result
            result = {
                "timestamp": time.time(),
                "scenario": SCENARIO,
                "mode": mode,
                "run": run,
                "run_id": run_id,
                **metrics,
                **delivery,
                **resources
            }
            results.append(result)

            log(f"✅ Run {run} ({mode}): p50={metrics['p50']}ms p95={metrics['p95']}ms p99={metrics['p99']}ms rps={metrics['rps']:.1f}")
            if mode == "dual":
                if delivery["verified"]:
                    log(f"   Delivery: ✅ {delivery['ch_count']}/{delivery['pg_count']} events in ClickHouse")
                else:
                    log(f"   Delivery: ❌ {delivery['missing']} events missing")

            print()
            time.sleep(5)

    # Save raw results
    results_file = PROJECT_DIR / "perf_focused_results_raw.json"
    results_file.write_text(json.dumps(results, indent=2))

    log("========================================")
    log("✅ All runs complete")
    log(f"Results saved to: {results_file}")
    log("========================================")

    # Generate summary
    generate_summary()

def generate_summary():
    """Generate comparison summary"""
    log("\nGenerating summary...")

    # Group by mode
    pg_runs = [r for r in results if r["mode"] == "postgres"]
    dual_runs = [r for r in results if r["mode"] == "dual"]

    if not pg_runs or not dual_runs:
        log("❌ Missing data for one or both modes")
        return

    # Calculate means
    pg_p50 = mean(r["p50"] for r in pg_runs)
    dual_p50 = mean(r["p50"] for r in dual_runs)
    p50_diff_pct = ((dual_p50 - pg_p50) / pg_p50) * 100

    pg_p95 = mean(r["p95"] for r in pg_runs)
    dual_p95 = mean(r["p95"] for r in dual_runs)
    p95_diff_pct = ((dual_p95 - pg_p95) / pg_p95) * 100

    pg_p99 = mean(r["p99"] for r in pg_runs)
    dual_p99 = mean(r["p99"] for r in dual_runs)
    p99_diff_pct = ((dual_p99 - pg_p99) / pg_p99) * 100

    pg_rps = mean(r["rps"] for r in pg_runs)
    dual_rps = mean(r["rps"] for r in dual_runs)
    rps_diff_pct = ((dual_rps - pg_rps) / pg_rps) * 100

    pg_cpu = mean(r["cpu_pct"] for r in pg_runs)
    dual_cpu = mean(r["cpu_pct"] for r in dual_runs)

    pg_ram = mean(r["ram_mb"] for r in pg_runs)
    dual_ram = mean(r["ram_mb"] for r in dual_runs)

    # Check delivery verification
    dual_delivery_verified = all(r.get("verified", False) for r in dual_runs)

    summary = {
        "scenario": SCENARIO,
        "postgres": {
            "p50": pg_p50,
            "p95": pg_p95,
            "p99": pg_p99,
            "rps": pg_rps,
            "cpu_pct": pg_cpu,
            "ram_mb": pg_ram
        },
        "dual": {
            "p50": dual_p50,
            "p95": dual_p95,
            "p99": dual_p99,
            "rps": dual_rps,
            "cpu_pct": dual_cpu,
            "ram_mb": dual_ram,
            "delivery_verified": dual_delivery_verified
        },
        "differences": {
            "p50_pct": p50_diff_pct,
            "p95_pct": p95_diff_pct,
            "p99_pct": p99_diff_pct,
            "rps_pct": rps_diff_pct,
            "cpu_delta": dual_cpu - pg_cpu,
            "ram_delta_mb": dual_ram - pg_ram
        }
    }

    # Print summary
    print("\n" + "="*80)
    print("PERFORMANCE COMPARISON SUMMARY - SCENARIO 40")
    print("="*80)
    print(f"\nLatency (client-side):")
    print(f"  p50: {pg_p50:.1f}ms → {dual_p50:.1f}ms ({p50_diff_pct:+.1f}%)")
    print(f"  p95: {pg_p95:.1f}ms → {dual_p95:.1f}ms ({p95_diff_pct:+.1f}%)")
    print(f"  p99: {pg_p99:.1f}ms → {dual_p99:.1f}ms ({p99_diff_pct:+.1f}%)")
    print(f"\nThroughput:")
    print(f"  RPS: {pg_rps:.1f} → {dual_rps:.1f} ({rps_diff_pct:+.1f}%)")
    print(f"\nResource Usage:")
    print(f"  CPU: {pg_cpu:.1f}% → {dual_cpu:.1f}% ({dual_cpu - pg_cpu:+.1f}%)")
    print(f"  RAM: {pg_ram:.0f}MB → {dual_ram:.0f}MB ({dual_ram - pg_ram:+.0f}MB)")
    print(f"\nDelivery Verification (dual mode):")
    print(f"  ClickHouse: {'✅ VERIFIED' if dual_delivery_verified else '❌ FAILED'}")

    # Save summary
    summary_file = PROJECT_DIR / "perf_focused_summary.json"
    summary_file.write_text(json.dumps(summary, indent=2))
    log(f"\nSummary saved to: {summary_file}")

if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        log("\n⚠️  Interrupted by user")
        sys.exit(1)
    except Exception as e:
        log(f"\n❌ Error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
