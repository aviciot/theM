#!/usr/bin/env python3
"""
Performance Comparison: postgres vs dual mode
Runs scenarios via test-runner backend API and collects client-side metrics
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
SCENARIOS = ["10-perf-50-users-direct", "11-perf-200-users-direct", "12-perf-400-users-direct",
             "40-logging-perf-minimal", "41-logging-perf-metrics", "42-logging-perf-debug", "43-logging-perf-audit"]
MODES = ["postgres", "dual"]
RUNS = 3

results = []

def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)

def switch_mode(new_mode):
    """Switch Gateway mode and recreate container"""
    log(f"Switching to mode: {new_mode}")

    # Update docker-compose.dev.yml (where THE_M_EVENTS_GATEWAY is set)
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
    """Clear test data from PG and ClickHouse only - preserve Redis consumer groups"""
    log("Clearing test data...")
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
    # DO NOT FLUSHDB - it destroys consumer groups and queued events
    # Events from previous tests will be consumed, but new test requests are easily distinguishable by timestamp
    log("✅ Data cleared (PG + ClickHouse only, Redis consumer group preserved)")

def run_scenario(scenario_id):
    """Run a scenario via test-runner API, return run_id"""
    log(f"Running scenario: {scenario_id}")
    resp = requests.post(f"{TEST_RUNNER_API}/run", json={"scenario_id": scenario_id}, timeout=300)
    resp.raise_for_status()
    run_id = resp.json()["run_id"]

    # Poll for completion (check history file exists and is complete)
    history_file = PROJECT_DIR / "test-runner/data/history" / f"{run_id}.json"
    for _ in range(120):  # 2 min timeout
        time.sleep(1)
        if history_file.exists():
            # Check if ended_at is set
            try:
                data = json.loads(history_file.read_text())
                if data.get("ended_at"):
                    return run_id
            except (json.JSONDecodeError, KeyError):
                pass

    raise TimeoutError(f"Scenario {scenario_id} did not complete in 2 minutes")

def analyze_results(run_id):
    """Parse history JSON and extract client-side metrics"""
    history_file = PROJECT_DIR / "test-runner/data/history" / f"{run_id}.json"
    data = json.loads(history_file.read_text())

    # Collect all per-user latencies and count ACTUAL completed requests
    user_latencies = []
    step_latencies = []
    error_count = 0
    total_completed_requests = 0

    for result in data["results"]:
        if result["status"] == "failed":
            error_count += 1
            continue

        user_latencies.append(result["duration_ms"])
        for step in result.get("steps", []):
            if step.get("ok"):
                step_latencies.append(step["latency_ms"])
                total_completed_requests += 1

    if not step_latencies:
        return None

    # Calculate percentiles (simple sort-based)
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

    # Use ACTUAL completed request count (total_completed_requests), not scenario total
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

def collect_server_metrics(mode):
    """Collect server-side metrics"""
    # PG count
    result = subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
         "-t", "-c", "SELECT count(*) FROM gateway_requests;"],
        capture_output=True, text=True, check=True
    )
    pg_count = int(result.stdout.strip())

    ch_count = 0
    missing = 0
    redis_lag = 0
    redis_pending = 0

    if mode == "dual":
        # Wait for drain
        time.sleep(30)

        # ClickHouse count
        result = subprocess.run(
            ["docker", "exec", "them-clickhouse", "clickhouse-client",
             "--query", "SELECT count() FROM them_events.events_gateway_request FORMAT TabSeparated;"],
            capture_output=True, text=True, check=True
        )
        ch_count = int(result.stdout.strip())
        missing = pg_count - ch_count

        # Redis lag
        result = subprocess.run(
            ["docker", "exec", "them-redis-events", "redis-cli", "XLEN", "them:events:gateway"],
            capture_output=True, text=True, check=True
        )
        redis_lag = int(result.stdout.strip())

        if missing > 0:
            log(f"⚠️  Missing {missing} events in ClickHouse")
        else:
            log(f"✅ All {pg_count} requests in ClickHouse")

    # CPU/RAM
    result = subprocess.run(
        ["docker", "stats", "them-go-bridge", "--no-stream", "--format", "{{.CPUPerc}},{{.MemUsage}}"],
        capture_output=True, text=True, check=True
    )
    stats = result.stdout.strip().split(",")
    cpu_pct = float(stats[0].replace("%", ""))
    ram_mb = float(stats[1].split()[0].replace("MiB", ""))

    # PG connections
    result = subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
         "-t", "-c", "SELECT count(*) FROM pg_stat_activity WHERE datname='them';"],
        capture_output=True, text=True, check=True
    )
    pg_conn = int(result.stdout.strip())

    return {
        "pg_count": pg_count,
        "ch_count": ch_count,
        "missing": missing,
        "cpu_pct": cpu_pct,
        "ram_mb": ram_mb,
        "pg_conn": pg_conn,
        "redis_lag": redis_lag,
        "redis_pending": redis_pending
    }

def main():
    log("========================================")
    log("Gateway Performance Test: postgres vs dual")
    log(f"Scenarios: {SCENARIOS}")
    log(f"Runs per scenario/mode: {RUNS}")
    log(f"Total tests: {len(SCENARIOS) * len(MODES) * RUNS}")
    log("========================================")
    print()

    # Alternating runs
    for run in range(1, RUNS + 1):
        # Odd runs: postgres first, Even runs: dual first
        mode_order = ["postgres", "dual"] if run % 2 == 1 else ["dual", "postgres"]

        for mode in mode_order:
            log("========================================")
            log(f"RUN {run} - MODE: {mode}")
            log("========================================")

            switch_mode(mode)

            for scenario_id in SCENARIOS:
                print()
                clear_data()

                # Run test
                run_id = run_scenario(scenario_id)

                # Analyze client-side metrics
                metrics = analyze_results(run_id)
                if not metrics:
                    log(f"⚠️  No valid results for {scenario_id}")
                    continue

                # Collect server-side metrics
                server_metrics = collect_server_metrics(mode)

                # Store result
                result = {
                    "timestamp": time.time(),
                    "scenario": scenario_id,
                    "mode": mode,
                    "run": run,
                    "run_id": run_id,
                    **metrics,
                    **server_metrics
                }
                results.append(result)

                log(f"✅ {scenario_id}: p50={metrics['p50']}ms p95={metrics['p95']}ms p99={metrics['p99']}ms rps={metrics['rps']:.1f}")

                # Short pause
                time.sleep(5)

            print()

    # Save raw results
    results_file = PROJECT_DIR / "perf_test_results_raw.json"
    results_file.write_text(json.dumps(results, indent=2))

    log("========================================")
    log("✅ All tests complete")
    log(f"Results saved to: {results_file}")
    log("========================================")

    # Generate summary
    generate_summary()

def generate_summary():
    """Generate comparison summary"""
    log("\nGenerating summary...")

    # Group by scenario and mode
    by_scenario = {}
    for r in results:
        key = (r["scenario"], r["mode"])
        if key not in by_scenario:
            by_scenario[key] = []
        by_scenario[key].append(r)

    # Calculate means
    summary = []
    for scenario_id in SCENARIOS:
        pg_runs = by_scenario.get((scenario_id, "postgres"), [])
        dual_runs = by_scenario.get((scenario_id, "dual"), [])

        if not pg_runs or not dual_runs:
            continue

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

        summary.append({
            "scenario": scenario_id,
            "postgres_p50": pg_p50,
            "dual_p50": dual_p50,
            "p50_diff_pct": p50_diff_pct,
            "postgres_p95": pg_p95,
            "dual_p95": dual_p95,
            "p95_diff_pct": p95_diff_pct,
            "postgres_p99": pg_p99,
            "dual_p99": dual_p99,
            "p99_diff_pct": p99_diff_pct,
            "postgres_rps": pg_rps,
            "dual_rps": dual_rps,
            "rps_diff_pct": rps_diff_pct,
        })

    # Print summary table
    print("\n" + "="*80)
    print("PERFORMANCE COMPARISON SUMMARY")
    print("="*80)

    for s in summary:
        print(f"\nScenario: {s['scenario']}")
        print(f"  p50: {s['postgres_p50']:.1f}ms → {s['dual_p50']:.1f}ms ({s['p50_diff_pct']:+.1f}%)")
        print(f"  p95: {s['postgres_p95']:.1f}ms → {s['dual_p95']:.1f}ms ({s['p95_diff_pct']:+.1f}%)")
        print(f"  p99: {s['postgres_p99']:.1f}ms → {s['dual_p99']:.1f}ms ({s['p99_diff_pct']:+.1f}%)")
        print(f"  RPS: {s['postgres_rps']:.1f} → {s['dual_rps']:.1f} ({s['rps_diff_pct']:+.1f}%)")

    # Save summary
    summary_file = PROJECT_DIR / "perf_test_summary.json"
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
