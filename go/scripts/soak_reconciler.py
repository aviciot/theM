#!/usr/bin/env python3
"""
soak_reconciler.py — Staging soak validation for Phase 11b (run reconciler).

Collects reconciler metrics, DB state, and Temporal workflow state across
multiple sweep cycles. Intended output is pasted back to the engineer as the
basis for the DryRun=false approval decision.

Prerequisites:
  Full hybrid stack running with TEMPORAL_ENABLED=true and DryRun=true:

    cd theM_gateway
    docker compose -f docker-compose.yml -f docker-compose.local.yml \\
                   -f docker-compose.integration.yml --profile temporal up -d --build

  Go bridge must be the version with the reconciler (Phase 11b, commit 7ff488d+).
  The reconciler runs a sweep every 60s starting at bridge startup.

Usage:
  # From repo root:
  python3 go/scripts/soak_reconciler.py

  # With custom gateway URL (if not on default port 8002):
  python3 go/scripts/soak_reconciler.py --gateway http://localhost:8002

  # Extended soak — wait for more sweep cycles:
  python3 go/scripts/soak_reconciler.py --sweeps 5 --interval 70

Environment variables:
  GO_GATEWAY_URL     Base URL of Go bridge (default: http://localhost:8002)
  POSTGRES_DSN       DSN string for direct DB access (optional — enables row-level analysis)
                     Example: host=localhost port=5432 dbname=them user=them password=<pw>
  TEMPORAL_HOST      Temporal frontend host (default: localhost)
  TEMPORAL_PORT      Temporal frontend gRPC port (default: 7233)
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.request
import urllib.error
from datetime import datetime, timezone


# ── Helpers ───────────────────────────────────────────────────────────────────

def _env(key: str, default: str = "") -> str:
    return os.environ.get(key, default)


def ts() -> str:
    return datetime.now(timezone.utc).strftime("%H:%M:%S UTC")


def section(title: str) -> None:
    print(f"\n{'='*60}")
    print(f"  {title}")
    print(f"{'='*60}")


def fetch_metrics(gateway_url: str) -> dict[str, float]:
    """Parse Prometheus text metrics from /metrics."""
    try:
        with urllib.request.urlopen(f"{gateway_url}/metrics", timeout=10) as r:
            body = r.read().decode()
    except Exception as exc:
        print(f"  WARN: could not fetch /metrics: {exc}")
        return {}

    metrics: dict[str, float] = {}
    for line in body.splitlines():
        if line.startswith("#"):
            continue
        m = re.match(r'^(\S+)\s+([\d.e+\-]+)$', line)
        if m:
            metrics[m.group(1)] = float(m.group(2))
    return metrics


def reconciler_metrics(all_metrics: dict[str, float]) -> dict[str, float]:
    return {k: v for k, v in all_metrics.items() if "reconciler" in k}


def runstream_metrics(all_metrics: dict[str, float]) -> dict[str, float]:
    return {k: v for k, v in all_metrics.items() if "runstream" in k}


def gate_session_metrics(all_metrics: dict[str, float]) -> dict[str, float]:
    return {k: v for k, v in all_metrics.items()
            if any(sub in k for sub in ("gate", "session", "temporal"))}


def docker_exec(container: str, *cmd: str) -> str:
    result = subprocess.run(
        ["docker", "exec", container, *cmd],
        capture_output=True, text=True, timeout=30
    )
    return result.stdout.strip()


def psql(query: str, container: str = "them-postgres") -> str:
    return docker_exec(container, "psql", "-U", "them", "-d", "them",
                       "-c", query, "--no-align", "-t")


def redis_cmd(*args: str, container: str = "them-redis") -> str:
    return docker_exec(container, "redis-cli", *args)


# ── Collection functions ──────────────────────────────────────────────────────

def collect_health(gateway_url: str) -> bool:
    section("1. Go Bridge Health")
    ok = True
    for endpoint in ("/health/live", "/health/ready"):
        try:
            with urllib.request.urlopen(f"{gateway_url}{endpoint}", timeout=5) as r:
                body = json.loads(r.read())
                print(f"  {endpoint}: HTTP {r.status} — {body}")
                if r.status != 200:
                    ok = False
        except Exception as exc:
            print(f"  {endpoint}: FAIL — {exc}")
            ok = False
    return ok


def collect_stuck_runs() -> None:
    section("2. Stuck 'running' Rows in DB (reconciler targets)")
    print("  Rows status='running' started >2 min ago (reconciler-eligible):")
    q = """
        SELECT id::text, started_at, updated_at,
               EXTRACT(EPOCH FROM (now() - started_at))::int AS age_sec,
               context_id::text
        FROM them.runs
        WHERE status = 'running'
          AND started_at < now() - interval '2 minutes'
        ORDER BY started_at
        LIMIT 20;
    """
    result = psql(q)
    if result:
        print(f"  id | started_at | updated_at | age_sec | context_id")
        for line in result.splitlines():
            print(f"  {line}")
    else:
        print("  (none — no eligible stuck rows)")

    print()
    print("  All 'running' rows (including fresh ones, for reference):")
    q2 = """
        SELECT status, COUNT(*) FROM them.runs GROUP BY status ORDER BY status;
    """
    print(f"  {psql(q2)}")


def collect_recent_runs() -> None:
    section("3. Recent Runs (last 20 minutes)")
    q = """
        SELECT id::text, status,
               EXTRACT(EPOCH FROM (now() - started_at))::int AS age_sec,
               entry_point_slug, context_id IS NOT NULL AS has_context_id
        FROM them.runs
        WHERE started_at > now() - interval '20 minutes'
        ORDER BY started_at DESC
        LIMIT 20;
    """
    result = psql(q)
    if result:
        print("  id | status | age_sec | ep_slug | has_context_id")
        for line in result.splitlines():
            print(f"  {line}")
    else:
        print("  (no runs in last 20 minutes)")


def collect_metrics_snapshot(gateway_url: str, label: str) -> dict[str, float]:
    section(f"4. Reconciler Metrics — {label}")
    all_m = fetch_metrics(gateway_url)
    rec = reconciler_metrics(all_m)
    if not rec:
        print("  WARN: No reconciler metrics found. Is the bridge running Phase 11b?")
        print("  Check: docker logs them-go-bridge | grep reconciler")
    else:
        for k, v in sorted(rec.items()):
            print(f"  {k} = {v:.0f}")
    return rec


def collect_runstream_metrics(gateway_url: str) -> None:
    section("5. Runstream + Gate/Session Metrics")
    all_m = fetch_metrics(gateway_url)
    for k, v in sorted(runstream_metrics(all_m).items()):
        print(f"  {k} = {v:.0f}")
    print()
    for k, v in sorted(gate_session_metrics(all_m).items()):
        print(f"  {k} = {v:.0f}")


def collect_bridge_logs_reconciler() -> None:
    section("6. Go Bridge Logs — Reconciler Lines (last 200 lines)")
    result = subprocess.run(
        ["docker", "logs", "them-go-bridge", "--tail", "200"],
        capture_output=True, text=True, timeout=15
    )
    lines = (result.stdout + result.stderr).splitlines()
    rec_lines = [l for l in lines if "reconciler" in l.lower()]
    if rec_lines:
        for l in rec_lines:
            print(f"  {l}")
    else:
        print("  (no reconciler log lines found in last 200 bridge log lines)")
        print("  Try: docker logs them-go-bridge 2>&1 | grep -i reconciler")


def collect_python_native_check() -> None:
    section("7. Python-Native Run Check (NotFound risk)")
    print("  Python-native runs have workflow_id = 'ctx-{context_id}', not the run UUID.")
    print("  These will return NotFound from Temporal — reconciler must NOT modify them.")
    q = """
        SELECT id::text, context_id::text,
               'ctx-' || context_id::text AS expected_temporal_workflow_id,
               started_at,
               EXTRACT(EPOCH FROM (now() - started_at))::int AS age_sec
        FROM them.runs
        WHERE status = 'running'
          AND context_id IS NOT NULL
          AND started_at < now() - interval '2 minutes'
        ORDER BY started_at
        LIMIT 10;
    """
    result = psql(q)
    if result:
        print("  IMPORTANT: The following stuck runs have context_id set.")
        print("  If they are Python-native, their Temporal workflow ID is ctx-{context_id}.")
        print("  Reconciler should log NotFound for these (check section 6).")
        print()
        print("  id | context_id | expected_temporal_wf_id | started_at | age_sec")
        for line in result.splitlines():
            print(f"  {line}")
    else:
        print("  (no stuck running rows with context_id — no Python-native NotFound risk)")


def collect_temporal_state() -> None:
    section("8. Temporal Workflow State (recent workflows via tctl/CLI)")
    print("  Listing recent workflows from Temporal (requires tctl or temporal CLI in PATH):")
    result = subprocess.run(
        ["docker", "exec", "temporal-frontend",
         "tctl", "--namespace", "default", "workflow", "list",
         "--query", 'ExecutionStatus="Running"', "--pagesize", "10"],
        capture_output=True, text=True, timeout=20
    )
    if result.returncode == 0 and result.stdout.strip():
        for line in result.stdout.splitlines()[:20]:
            print(f"  {line}")
    else:
        # Try temporal CLI as fallback
        result2 = subprocess.run(
            ["docker", "exec", "temporal-frontend",
             "temporal", "workflow", "list",
             "--namespace", "default", "--limit", "10"],
            capture_output=True, text=True, timeout=20
        )
        if result2.returncode == 0 and result2.stdout.strip():
            for line in result2.stdout.splitlines()[:20]:
                print(f"  {line}")
        else:
            print("  (tctl/temporal CLI not available in temporal-frontend container)")
            print("  Manual check: open http://localhost:3111 (or your Temporal UI URL)")
            print("  Look for any workflows with status Running that are older than 2 min")
            print("  and verify their workflow ID is a UUID (Go-path) or ctx-* (Python-native)")


def collect_advisory_lock_check() -> None:
    section("9. Advisory Lock Check (multi-pod)")
    q = """
        SELECT pid, granted, locktype, classid, objid
        FROM pg_locks
        WHERE locktype = 'advisory' AND classid = 987654321;
    """
    result = psql(q)
    if result:
        print("  Advisory lock currently held:")
        print(f"  {result}")
    else:
        print("  No advisory lock held right now (between sweeps — this is normal).")
    print()
    print("  Number of running Go bridge containers (multi-pod):")
    result2 = subprocess.run(
        ["docker", "ps", "--filter", "name=them-go-bridge", "--format", "{{.Names}}"],
        capture_output=True, text=True, timeout=10
    )
    for line in result2.stdout.splitlines():
        print(f"    {line}")


def collect_false_positive_check() -> None:
    section("10. False Positive Check — Recently Updated by Reconciler")
    print("  Rows updated_at within the last 10 minutes that are now non-running:")
    print("  (In DryRun=true mode, this will be empty — reconciler makes no writes)")
    q = """
        SELECT id::text, status, started_at, updated_at,
               EXTRACT(EPOCH FROM (now() - updated_at))::int AS updated_sec_ago
        FROM them.runs
        WHERE status != 'running'
          AND updated_at > now() - interval '10 minutes'
        ORDER BY updated_at DESC
        LIMIT 20;
    """
    result = psql(q)
    if result:
        print("  id | status | started_at | updated_at | updated_sec_ago")
        for line in result.splitlines():
            print(f"  {line}")
    else:
        print("  (none — confirms DryRun=true is active: no writes have been made)")


def diff_metrics(before: dict[str, float], after: dict[str, float]) -> None:
    section("11. Metric Delta (after sweeps vs before)")
    all_keys = sorted(set(before) | set(after))
    rec_keys = [k for k in all_keys if "reconciler" in k]
    if not rec_keys:
        print("  No reconciler metrics to diff.")
        return
    print(f"  {'Metric':<55} {'Before':>8} {'After':>8} {'Delta':>8}")
    print(f"  {'-'*79}")
    for k in rec_keys:
        b = before.get(k, 0.0)
        a = after.get(k, 0.0)
        delta = a - b
        marker = " ← CHANGED" if delta != 0 else ""
        print(f"  {k:<55} {b:>8.0f} {a:>8.0f} {delta:>+8.0f}{marker}")


def print_recommendation(before: dict[str, float], after: dict[str, float],
                         sweep_count: int) -> None:
    section("12. Recommendation Summary")

    scanned_delta = after.get("them_reconciler_scanned_total", 0) - \
                    before.get("them_reconciler_scanned_total", 0)
    notfound_delta = after.get("them_reconciler_notfound_total", 0) - \
                     before.get("them_reconciler_notfound_total", 0)
    dryrun_delta = after.get("them_reconciler_dryrun_total", 0) - \
                   before.get("them_reconciler_dryrun_total", 0)
    error_delta = after.get("them_reconciler_errors_total", 0) - \
                  before.get("them_reconciler_errors_total", 0)

    print(f"  Sweeps waited: {sweep_count}")
    print(f"  Rows scanned this soak: {scanned_delta:.0f}")
    print(f"  Rows that would be updated (dryrun_total delta): {dryrun_delta:.0f}")
    print(f"  NotFound events: {notfound_delta:.0f}")
    print(f"  Errors: {error_delta:.0f}")
    print()

    issues = []
    if error_delta > 0:
        issues.append(f"⚠  {error_delta:.0f} reconciler errors — check Temporal connectivity")
    if notfound_delta > scanned_delta * 0.5 and scanned_delta > 0:
        issues.append(f"⚠  High NotFound rate ({notfound_delta:.0f}/{scanned_delta:.0f}) — "
                      f"check Temporal namespace, history retention, or Python-native run count")
    if scanned_delta == 0:
        issues.append("ℹ  No rows scanned — either no stuck rows exist (healthy) "
                      "or reconciler is not running")

    if issues:
        print("  CONCERNS:")
        for i in issues:
            print(f"    {i}")
        print()
        print("  RECOMMENDATION: Investigate concerns above before enabling DryRun=false.")
    else:
        print("  No concerns found in this soak window.")
        print()
        if dryrun_delta > 0:
            print(f"  RECOMMENDATION: {dryrun_delta:.0f} rows would be updated. Review the")
            print("  'Stuck running rows' and 'Temporal State' sections above to confirm")
            print("  each run is genuinely stuck (not a long-running valid workflow).")
            print("  If confirmed, DryRun=false is safe to enable.")
        else:
            print("  RECOMMENDATION: No stuck rows found in this soak window.")
            print("  DryRun=false is safe to enable when stuck rows appear in production.")

    print()
    print("  *** DryRun=false NOT enabled automatically. Requires explicit approval. ***")


# ── Main ──────────────────────────────────────────────────────────────────────

def main() -> int:
    parser = argparse.ArgumentParser(description="Reconciler soak validation")
    parser.add_argument("--gateway", default=_env("GO_GATEWAY_URL", "http://localhost:8002"))
    parser.add_argument("--sweeps", type=int, default=3,
                        help="Number of reconciler sweep cycles to wait through (default: 3)")
    parser.add_argument("--interval", type=int, default=70,
                        help="Seconds between sweep checks (default: 70 — slightly >60s sweep interval)")
    args = parser.parse_args()

    gateway = args.gateway.rstrip("/")

    print(f"\n{'#'*60}")
    print(f"# Phase 11b Reconciler Soak — {ts()}")
    print(f"# Gateway: {gateway}")
    print(f"# Planned sweeps: {args.sweeps} (wait ~{args.sweeps * args.interval}s total)")
    print(f"{'#'*60}")

    # ── Pre-soak snapshot ─────────────────────────────────────────────────────
    if not collect_health(gateway):
        print("\nFATAL: Go bridge not healthy. Start the hybrid stack first.")
        print("  cd theM_gateway")
        print("  docker compose -f docker-compose.yml -f docker-compose.local.yml \\")
        print("                 -f docker-compose.integration.yml --profile temporal up -d --build")
        return 1

    collect_stuck_runs()
    collect_recent_runs()
    before_metrics = collect_metrics_snapshot(gateway, "PRE-SOAK")
    collect_python_native_check()
    collect_temporal_state()
    collect_advisory_lock_check()

    # ── Sweep wait loop ───────────────────────────────────────────────────────
    section(f"Waiting for {args.sweeps} sweep cycles (~{args.sweeps * args.interval}s)...")
    for i in range(1, args.sweeps + 1):
        print(f"  [{ts()}] Waiting {args.interval}s for sweep #{i}/{args.sweeps}...", flush=True)
        time.sleep(args.interval)
        # Print any new reconciler log lines
        result = subprocess.run(
            ["docker", "logs", "them-go-bridge", "--since", f"{args.interval + 5}s"],
            capture_output=True, text=True, timeout=10
        )
        rec_lines = [l for l in (result.stdout + result.stderr).splitlines()
                     if "reconciler" in l.lower()]
        if rec_lines:
            print(f"  [{ts()}] Sweep #{i} log lines:")
            for l in rec_lines[-10:]:
                print(f"    {l}")

    # ── Post-soak snapshot ────────────────────────────────────────────────────
    collect_bridge_logs_reconciler()
    after_metrics = collect_metrics_snapshot(gateway, "POST-SOAK")
    collect_stuck_runs()
    collect_false_positive_check()
    collect_runstream_metrics(gateway)

    # ── Delta + recommendation ────────────────────────────────────────────────
    diff_metrics(before_metrics, after_metrics)
    print_recommendation(before_metrics, after_metrics, args.sweeps)

    section("Done")
    print(f"  Soak complete at {ts()}.")
    print("  Paste this output back for the DryRun=false approval review.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
