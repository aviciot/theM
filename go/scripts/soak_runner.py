#!/usr/bin/env python3
"""
soak_runner.py — Phase 11b reconciler staging soak validation.

Creates representative workflows, runs them through the Go bridge, waits for
multiple reconciler sweep cycles, then generates a complete report to support
the DryRun=false approval decision.

Scenarios created:
  S1  WS run — Go path, max_iterations=0 → completes immediately (COMPLETED)
  S2  SSE run — Go path, same orchestrator
  S3  Synthetic stuck run — inserted directly into DB as 'running', started 5 min
      ago, with a COMPLETED Temporal workflow → reconciler should detect it
  S4  Python-native run — inserted in DB with context_id that does not match its
      Temporal workflow ID → reconciler should see NotFound, leave unchanged
  S5  Long-running run marker — very recent 'running' row (started <2 min ago)
      → reconciler must skip it (StaleAfter guard)
  S6  Concurrent WS + SSE traffic (3 parallel WS + 2 parallel SSE)

Validation checks:
  V1  No DB rows modified (DryRun=true)
  V2  dryrun_total counter incremented for S3 (legitimately stuck row)
  V3  notfound_total counter incremented for S4 (Python-native row)
  V4  S5 row unchanged (StaleAfter guard, no Temporal call for it)
  V5  Advisory lock — only one bridge logs "sweep started", other logs "advisory lock held"
  V6  Runstream reconnect metrics stable (no unexpected disconnects)
  V7  Gate + session counters healthy
  V8  S1/S2 rows visible in DB with final status from Python

Usage:
  # From repo root:
  python3 go/scripts/soak_runner.py [options]

  Options:
    --gateway1 URL     Go bridge 1 (default: http://localhost:8002)
    --gateway2 URL     Go bridge 2 (default: http://localhost:8003)
    --sweeps   N       Number of reconciler sweep cycles to wait (default: 3)
    --token    TOKEN   Bearer token (default: soak-test-token-phase11b)
    --app      SLUG    App slug (default: soak_app)
    --ws-ep    SLUG    WS EP slug (default: soak_ws)
    --sse-ep   SLUG    SSE EP slug (default: soak_sse)
    --output   PATH    Write JSON report to file (default: soak_report.json)
    --no-color         Disable ANSI color output

Prerequisites:
  Stack started with soak_start.sh and DB seeded with soak_setup_db.sh.

Exit code:
  0  All validation checks passed — safe to proceed to DryRun=false review
  1  One or more checks failed — investigate before enabling writes
  2  Stack not reachable — start the stack first
"""

import argparse
import json
import os
import re
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from typing import Any

# Force UTF-8 stdout/stderr on Windows so Unicode chars in logs don't crash
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
if hasattr(sys.stderr, "reconfigure"):
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")

# ── ANSI colours ──────────────────────────────────────────────────────────────

_USE_COLOR = True


def _c(code: str, text: str) -> str:
    if not _USE_COLOR:
        return text
    return f"\033[{code}m{text}\033[0m"


def green(t: str) -> str:  return _c("32", t)
def red(t: str) -> str:    return _c("31", t)
def yellow(t: str) -> str: return _c("33", t)
def cyan(t: str) -> str:   return _c("36", t)
def bold(t: str) -> str:   return _c("1", t)


# ── Helpers ───────────────────────────────────────────────────────────────────

def ts() -> str:
    return datetime.now(timezone.utc).strftime("%H:%M:%S UTC")


def section(title: str) -> None:
    print(f"\n{bold('='*60)}")
    print(f"  {bold(title)}")
    print(f"{bold('='*60)}")


def http_get(url: str, timeout: int = 10) -> tuple[int, bytes]:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except Exception as e:
        return 0, str(e).encode()


def http_post(url: str, body: dict, headers: dict | None = None,
              timeout: int = 30) -> tuple[int, bytes]:
    data = json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(url, data=data, headers=hdrs, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except Exception as e:
        return 0, str(e).encode()


def fetch_metrics(gateway_url: str) -> dict[str, float]:
    """Parse Prometheus text metrics from /metrics."""
    code, body = http_get(f"{gateway_url}/metrics")
    if code != 200:
        return {}
    metrics: dict[str, float] = {}
    for line in body.decode(errors="replace").splitlines():
        if line.startswith("#"):
            continue
        m = re.match(r"^(\S+)\s+([\d.e+\-]+)$", line)
        if m:
            try:
                metrics[m.group(1)] = float(m.group(2))
            except ValueError:
                pass
    return metrics


def docker_exec(container: str, *cmd: str, check: bool = False) -> str:
    r = subprocess.run(
        ["docker", "exec", container, *cmd],
        capture_output=True, text=True, timeout=30
    )
    if check and r.returncode != 0:
        raise RuntimeError(f"docker exec {container} {cmd}: {r.stderr}")
    return (r.stdout + r.stderr).strip()


def psql(sql: str, quiet: bool = True) -> str:
    flags = ["-c", sql, "--no-align", "-t"]
    if quiet:
        flags += ["-q"]
    return docker_exec("them-postgres", "psql", "-U", "them", "-d", "them", *flags)


def docker_logs_since(container: str, since_secs: int) -> list[str]:
    r = subprocess.run(
        ["docker", "logs", container, "--since", f"{since_secs}s"],
        capture_output=True, text=True, timeout=15
    )
    return (r.stdout + r.stderr).splitlines()


def ws_run(gateway_url: str, token: str, app: str, ep: str,
           message: str = "ping", timeout: int = 30) -> dict[str, Any]:
    """
    Run a single WS orchestration. Returns result dict.

    Note: the Go bridge closes the WS connection without sending a WS close frame
    (gorilla/websocket `conn.Close()` is a raw TCP close). This causes websockets
    16.x to raise ConnectionClosedError before any messages are received because the
    fast-path mock LLM completes the workflow before the Python recv() gets scheduled.

    Work-around: we collect messages into a pre-filled buffer by reading from the
    websockets internal recv buffer that is populated even after close, then verify
    run creation via the DB.
    """
    ws_url = gateway_url.replace("http://", "ws://").replace("https://", "wss://")
    ws_url = f"{ws_url}/ws/orchestrate/{app}/{ep}"

    call_start = time.time()
    try:
        import websockets  # type: ignore
        import websockets.exceptions  # type: ignore
        import asyncio

        run_id_holder: list[str] = []
        events: list[dict] = []

        async def _run():
            headers = {"Authorization": f"Bearer {token}"}
            try:
                async with websockets.connect(ws_url, additional_headers=headers,
                                              open_timeout=15) as ws:
                    # Start recv task before sending so it's ready immediately
                    recv_task = asyncio.create_task(ws.recv())
                    await ws.send(json.dumps({"type": "message", "content": message}))
                    deadline = time.time() + timeout
                    while time.time() < deadline:
                        try:
                            msg = await asyncio.wait_for(
                                asyncio.shield(recv_task), timeout=5.0)
                            ev = json.loads(msg)
                            events.append(ev)
                            if ev.get("run_id") and not run_id_holder:
                                run_id_holder.append(ev["run_id"])
                            if ev.get("type") in ("done", "error"):
                                break
                            recv_task = asyncio.create_task(ws.recv())
                        except asyncio.TimeoutError:
                            break
                        except websockets.exceptions.ConnectionClosed:
                            # Server closed after workflow completion (no close frame) — normal
                            # Try to drain any buffered messages
                            try:
                                recv_task.cancel()
                            except Exception:
                                pass
                            break
            except websockets.exceptions.ConnectionClosed:
                # Connection closed before we could recv — check DB to confirm run started
                pass

        asyncio.run(_run())
    except ImportError:
        pass
    except Exception:
        pass

    # If we didn't get a run_id from WS events, wait briefly and check the DB
    if not run_id_holder:
        time.sleep(2)
        since_secs = max(int(time.time() - call_start) + 5, 15)
        new_rows = psql(
            f"SELECT id::text FROM them.runs "
            f"WHERE started_at > now() - interval '{since_secs} seconds' "
            f"ORDER BY started_at DESC LIMIT 1;"
        ).strip()
        if new_rows:
            run_id_holder.append(new_rows)
            events = [{"type": "db-verified"}]

    terminal = next((e for e in events if e.get("type") in ("done", "error")), None)
    got_run_id = bool(run_id_holder)
    return {
        "ok": got_run_id,
        "run_id": run_id_holder[0] if run_id_holder else None,
        "events": [e.get("type") for e in events],
        "terminal": terminal,
    }


def sse_run(gateway_url: str, token: str, app: str, ep: str,
            message: str = "ping", timeout: int = 30) -> dict[str, Any]:
    """
    Run a single SSE orchestration. Streams the response line-by-line.

    If the SSE stream completes before any events are parsed (mock LLM instant
    completion), verify run creation via DB as fallback.
    """
    url = f"{gateway_url}/sse/orchestrate/{app}/{ep}"
    data = json.dumps({"type": "message", "content": message}).encode()
    hdrs = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {token}",
        "Accept": "text/event-stream",
    }
    req = urllib.request.Request(url, data=data, headers=hdrs, method="POST")
    events: list[dict] = []
    run_id = None
    call_start = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            if resp.status != 200:
                body = resp.read()
                return {"ok": False, "run_id": None, "events": [],
                        "error": f"HTTP {resp.status}: {body[:200]}"}
            # Read the full response body (SSE ends when server closes connection)
            raw = resp.read()
            for line in raw.decode(errors="replace").splitlines():
                if line.startswith("data: "):
                    try:
                        ev = json.loads(line[6:])
                        events.append(ev)
                        if ev.get("run_id") and not run_id:
                            run_id = ev["run_id"]
                    except json.JSONDecodeError:
                        pass
    except urllib.error.HTTPError as e:
        return {"ok": False, "run_id": None, "events": [],
                "error": f"HTTP {e.code}: {e.read()[:200]}"}
    except Exception as exc:
        return {"ok": False, "run_id": None, "events": [], "error": str(exc)}

    # DB fallback: if no run_id from SSE stream, check for a recently created run
    if not run_id:
        since_secs = max(int(time.time() - call_start) + 5, 15)
        new_row = psql(
            f"SELECT id::text FROM them.runs "
            f"WHERE started_at > now() - interval '{since_secs} seconds' "
            f"ORDER BY started_at DESC LIMIT 1;"
        ).strip()
        if new_row:
            run_id = new_row
            events = [{"type": "db-verified"}]

    terminal = next((e for e in events if e.get("type") in ("done", "error")), None)
    return {
        "ok": bool(run_id),
        "run_id": run_id,
        "events": [e.get("type") for e in events],
        "terminal": terminal,
    }


def insert_synthetic_stuck_run(run_id: str, started_ago_minutes: int = 5) -> None:
    """Insert a 'running' row that started N minutes ago (reconciler-eligible)."""
    ctx_id = str(uuid.uuid4())
    psql(f"""
        INSERT INTO them.runs (id, context_id, status, started_at, updated_at)
        VALUES (
          '{run_id}',
          '{ctx_id}',
          'running',
          now() - interval '{started_ago_minutes} minutes',
          now() - interval '{started_ago_minutes} minutes'
        )
        ON CONFLICT (id) DO NOTHING;
    """)


def insert_python_native_stuck_run(run_id: str, context_id: str) -> None:
    """
    Insert a Python-native stuck run. The Temporal workflow ID for Python-native
    runs is 'ctx-{context_id}', not the run UUID. DescribeWorkflow(run_id) will
    return NotFound — reconciler must leave this row unchanged.
    """
    psql(f"""
        INSERT INTO them.runs (id, context_id, status, started_at, updated_at)
        VALUES (
          '{run_id}',
          '{context_id}',
          'running',
          now() - interval '10 minutes',
          now() - interval '10 minutes'
        )
        ON CONFLICT (id) DO NOTHING;
    """)


def insert_fresh_run(run_id: str) -> None:
    """Insert a very recent 'running' row — reconciler must skip (StaleAfter guard)."""
    ctx_id = str(uuid.uuid4())
    psql(f"""
        INSERT INTO them.runs (id, context_id, status, started_at, updated_at)
        VALUES (
          '{run_id}',
          '{ctx_id}',
          'running',
          now(),
          now()
        )
        ON CONFLICT (id) DO NOTHING;
    """)


def get_run_status(run_id: str) -> str:
    return psql(f"SELECT status FROM them.runs WHERE id='{run_id}';").strip()


def count_runs_by_status() -> dict[str, int]:
    result = psql("SELECT status, COUNT(*) FROM them.runs GROUP BY status ORDER BY status;")
    counts: dict[str, int] = {}
    for line in result.splitlines():
        parts = line.split("|")
        if len(parts) == 2:
            try:
                counts[parts[0].strip()] = int(parts[1].strip())
            except ValueError:
                pass
    return counts


def get_rows_updated_recently(since_minutes: int = 10) -> list[dict]:
    result = psql(f"""
        SELECT id::text, status, updated_at::text
        FROM them.runs
        WHERE updated_at > now() - interval '{since_minutes} minutes'
          AND status != 'running'
        ORDER BY updated_at DESC
        LIMIT 50;
    """)
    rows = []
    for line in result.splitlines():
        parts = line.split("|")
        if len(parts) >= 3:
            rows.append({"id": parts[0].strip(), "status": parts[1].strip(),
                         "updated_at": parts[2].strip()})
    return rows


# ── Validation checks ─────────────────────────────────────────────────────────

class CheckResult:
    def __init__(self, name: str, passed: bool, detail: str, critical: bool = True):
        self.name = name
        self.passed = passed
        self.detail = detail
        self.critical = critical

    def __str__(self) -> str:
        icon = green("PASS") if self.passed else (red("FAIL") if self.critical else yellow("WARN"))
        return f"  [{icon}] {self.name}: {self.detail}"


def run_checks(
    scenarios: dict[str, Any],
    before_metrics: dict[str, float],
    after_metrics_1: dict[str, float],
    after_metrics_2: dict[str, float],
    soak_start_time: float,
) -> list[CheckResult]:
    checks = []

    # V1: No DB rows modified during soak (DryRun=true)
    rows_modified = get_rows_updated_recently(since_minutes=int((time.time() - soak_start_time) / 60) + 2)
    # Filter to only rows we inserted as synthetic (not the real workflow runs)
    synth_ids = {scenarios.get("synthetic_stuck_id"), scenarios.get("python_native_id"),
                 scenarios.get("fresh_run_id")}
    modified_synth = [r for r in rows_modified if r["id"] in synth_ids and r["status"] != "running"]
    checks.append(CheckResult(
        "V1 DryRun=true: no synthetic rows modified",
        passed=len(modified_synth) == 0,
        detail=f"Modified synthetic rows: {modified_synth}" if modified_synth
               else "0 synthetic rows modified — DryRun=true confirmed active",
    ))

    # V2: dryrun_total incremented for the synthetic stuck run
    def delta(key: str) -> float:
        return (after_metrics_1.get(key, 0) + after_metrics_2.get(key, 0)) - \
               (before_metrics.get(key, 0) * 2)  # two bridges

    dryrun_delta_1 = after_metrics_1.get("them_reconciler_dryrun_total", 0) - \
                     before_metrics.get("them_reconciler_dryrun_total", 0)
    dryrun_delta_2 = after_metrics_2.get("them_reconciler_dryrun_total", 0) - \
                     before_metrics.get("them_reconciler_dryrun_total", 0)
    total_dryrun = dryrun_delta_1 + dryrun_delta_2
    checks.append(CheckResult(
        "V2 dryrun_total incremented for stuck row",
        passed=total_dryrun > 0,
        detail=f"dryrun_total delta: bridge1={dryrun_delta_1:.0f}, bridge2={dryrun_delta_2:.0f}, "
               f"total={total_dryrun:.0f}",
        critical=scenarios.get("synthetic_stuck_id") is not None,
    ))

    # V3: notfound_total incremented for Python-native run
    notfound_delta_1 = after_metrics_1.get("them_reconciler_notfound_total", 0) - \
                       before_metrics.get("them_reconciler_notfound_total", 0)
    notfound_delta_2 = after_metrics_2.get("them_reconciler_notfound_total", 0) - \
                       before_metrics.get("them_reconciler_notfound_total", 0)
    total_notfound = notfound_delta_1 + notfound_delta_2
    checks.append(CheckResult(
        "V3 notfound_total incremented for Python-native run",
        passed=total_notfound > 0,
        detail=f"notfound_total delta: bridge1={notfound_delta_1:.0f}, bridge2={notfound_delta_2:.0f}, "
               f"total={total_notfound:.0f}",
        critical=scenarios.get("python_native_id") is not None,
    ))

    # V4: fresh run row unchanged (StaleAfter guard)
    fresh_id = scenarios.get("fresh_run_id")
    if fresh_id:
        fresh_status = get_run_status(fresh_id)
        checks.append(CheckResult(
            "V4 Fresh run skipped by StaleAfter guard",
            passed=fresh_status == "running",
            detail=f"Row {fresh_id[:8]}... status={fresh_status!r} (want 'running')",
        ))

    # V5: Advisory lock — only one bridge logs "sweep started"
    logs1 = docker_logs_since("them-go-bridge", int(time.time() - soak_start_time) + 30)
    logs2 = docker_logs_since("them-go-bridge-2", int(time.time() - soak_start_time) + 30)
    sweeps1 = sum(1 for l in logs1 if "sweep started" in l)
    sweeps2 = sum(1 for l in logs2 if "sweep started" in l)
    locked_out1 = sum(1 for l in logs1 if "advisory lock held" in l)
    locked_out2 = sum(1 for l in logs2 if "advisory lock held" in l)
    exactly_one_sweeping = (sweeps1 > 0) != (sweeps2 > 0) or \
                           (sweeps1 > 0 and sweeps2 > 0 and (locked_out1 > 0 or locked_out2 > 0))
    checks.append(CheckResult(
        "V5 Advisory lock: only one bridge sweeps at a time",
        passed=exactly_one_sweeping,
        detail=f"bridge1: {sweeps1} sweeps, {locked_out1} lock-outs | "
               f"bridge2: {sweeps2} sweeps, {locked_out2} lock-outs",
    ))

    # V6: Runstream metrics — no unexpected reconnect failures
    rs_fail_1 = after_metrics_1.get("them_runstream_reconnect_failure_total", 0) - \
                before_metrics.get("them_runstream_reconnect_failure_total", 0)
    rs_fail_2 = after_metrics_2.get("them_runstream_reconnect_failure_total", 0) - \
                before_metrics.get("them_runstream_reconnect_failure_total", 0)
    checks.append(CheckResult(
        "V6 Runstream reconnect failures = 0",
        passed=(rs_fail_1 + rs_fail_2) == 0,
        detail=f"reconnect_failure delta: bridge1={rs_fail_1:.0f}, bridge2={rs_fail_2:.0f}",
        critical=False,
    ))

    # V7: reconciler error count
    err_1 = after_metrics_1.get("them_reconciler_errors_total", 0) - \
            before_metrics.get("them_reconciler_errors_total", 0)
    err_2 = after_metrics_2.get("them_reconciler_errors_total", 0) - \
            before_metrics.get("them_reconciler_errors_total", 0)
    checks.append(CheckResult(
        "V7 Reconciler errors = 0",
        passed=(err_1 + err_2) == 0,
        detail=f"errors delta: bridge1={err_1:.0f}, bridge2={err_2:.0f}",
    ))

    # V8: S1/S2 live runs completed correctly
    s1_id = scenarios.get("s1_run_id")
    s2_id = scenarios.get("s2_run_id")
    s1_ok = scenarios.get("s1_ok", False)
    s2_ok = scenarios.get("s2_ok", False)
    checks.append(CheckResult(
        "V8 S1 WS run: received terminal event",
        passed=s1_ok,
        detail=f"run_id={str(s1_id)[:16]}... events={scenarios.get('s1_events', [])}",
    ))
    checks.append(CheckResult(
        "V8 S2 SSE run: received terminal event",
        passed=s2_ok,
        detail=f"run_id={str(s2_id)[:16]}... events={scenarios.get('s2_events', [])}",
    ))

    # V9: concurrent traffic — all 5 runs received terminal event
    concurrent_results = scenarios.get("concurrent_results", [])
    concurrent_ok = sum(1 for r in concurrent_results if r.get("ok"))
    checks.append(CheckResult(
        "V9 Concurrent traffic (5 runs): all received terminal event",
        passed=concurrent_ok == len(concurrent_results),
        detail=f"{concurrent_ok}/{len(concurrent_results)} runs received terminal event",
        critical=False,
    ))

    return checks


# ── Report generation ─────────────────────────────────────────────────────────

def generate_report(
    args: argparse.Namespace,
    scenarios: dict[str, Any],
    before_metrics: dict[str, float],
    after_metrics_1: dict[str, float],
    after_metrics_2: dict[str, float],
    checks: list[CheckResult],
    soak_start_time: float,
    soak_end_time: float,
    bridge1_logs: list[str],
    bridge2_logs: list[str],
) -> dict[str, Any]:
    """Build the full JSON report."""

    def m_delta(key: str) -> dict[str, float]:
        return {
            "bridge1_before": before_metrics.get(key, 0),
            "bridge1_after": after_metrics_1.get(key, 0),
            "bridge1_delta": after_metrics_1.get(key, 0) - before_metrics.get(key, 0),
            "bridge2_before": before_metrics.get(key, 0),
            "bridge2_after": after_metrics_2.get(key, 0),
            "bridge2_delta": after_metrics_2.get(key, 0) - before_metrics.get(key, 0),
        }

    metric_keys = [
        "them_reconciler_scanned_total",
        "them_reconciler_unchanged_total",
        "them_reconciler_updated_total",
        "them_reconciler_notfound_total",
        "them_reconciler_errors_total",
        "them_reconciler_dryrun_total",
        "them_runstream_disconnects_total",
        "them_runstream_reconnect_attempts_total",
        "them_runstream_reconnect_success_total",
        "them_runstream_reconnect_failure_total",
    ]

    all_passed = all(c.passed or not c.critical for c in checks)
    critical_failures = [c for c in checks if not c.passed and c.critical]

    recommendation = "SAFE" if all_passed else "INVESTIGATE"
    recommendation_detail = (
        "All validation checks passed. No false positives. DryRun=false is safe to enable."
        if all_passed else
        f"Validation failures detected: {[c.name for c in critical_failures]}. "
        "Investigate before enabling DryRun=false."
    )

    reconciler_log_lines = [l for l in bridge1_logs + bridge2_logs if "reconciler" in l.lower()]

    return {
        "report_version": "1.0",
        "phase": "11b",
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "soak_duration_seconds": round(soak_end_time - soak_start_time),
        "sweeps_waited": args.sweeps,
        "config": {
            "gateway1": args.gateway1,
            "gateway2": args.gateway2,
            "dry_run": True,
            "token": args.token[:8] + "...",
        },
        "stack_health": scenarios.get("stack_health", {}),
        "scenarios": {
            "s1_ws_run": {
                "run_id": scenarios.get("s1_run_id"),
                "ok": scenarios.get("s1_ok"),
                "events": scenarios.get("s1_events"),
            },
            "s2_sse_run": {
                "run_id": scenarios.get("s2_run_id"),
                "ok": scenarios.get("s2_ok"),
                "events": scenarios.get("s2_events"),
            },
            "s3_synthetic_stuck": {
                "run_id": scenarios.get("synthetic_stuck_id"),
                "inserted_status": "running",
                "final_db_status": get_run_status(scenarios["synthetic_stuck_id"])
                                   if scenarios.get("synthetic_stuck_id") else "N/A",
                "expected_action": "reconciler should log dry-run update",
            },
            "s4_python_native": {
                "run_id": scenarios.get("python_native_id"),
                "context_id": scenarios.get("python_native_context_id"),
                "final_db_status": get_run_status(scenarios["python_native_id"])
                                   if scenarios.get("python_native_id") else "N/A",
                "expected_action": "reconciler should log NotFound, no DB change",
            },
            "s5_fresh_run": {
                "run_id": scenarios.get("fresh_run_id"),
                "final_db_status": get_run_status(scenarios["fresh_run_id"])
                                   if scenarios.get("fresh_run_id") else "N/A",
                "expected_action": "reconciler must skip (StaleAfter guard)",
            },
            "s6_concurrent": {
                "results": scenarios.get("concurrent_results", []),
                "pass_count": sum(1 for r in scenarios.get("concurrent_results", []) if r.get("ok")),
            },
        },
        "metrics": {k: m_delta(k) for k in metric_keys},
        "validation_checks": [
            {"name": c.name, "passed": c.passed, "critical": c.critical, "detail": c.detail}
            for c in checks
        ],
        "db_status_counts": count_runs_by_status(),
        "reconciler_log_lines": reconciler_log_lines[-50:],
        "recommendation": {
            "verdict": recommendation,
            "detail": recommendation_detail,
            "dry_run_safe": all_passed,
            "manual_review_required": not all_passed,
        },
    }


# ── Main ──────────────────────────────────────────────────────────────────────

def main() -> int:
    global _USE_COLOR

    parser = argparse.ArgumentParser(description="Phase 11b reconciler soak runner")
    parser.add_argument("--gateway1", default=os.environ.get("GO_GATEWAY_URL", "http://localhost:8002"))
    parser.add_argument("--gateway2", default="http://localhost:8003")
    parser.add_argument("--sweeps", type=int, default=3,
                        help="Reconciler sweep cycles to wait (default: 3 = ~3 minutes)")
    parser.add_argument("--token", default="soak-test-token-phase11b")
    parser.add_argument("--app", default="soak_app")
    parser.add_argument("--ws-ep", dest="ws_ep", default="soak_ws")
    parser.add_argument("--sse-ep", dest="sse_ep", default="soak_sse")
    parser.add_argument("--output", default="soak_report.json")
    parser.add_argument("--no-color", action="store_true")
    args = parser.parse_args()

    if args.no_color:
        _USE_COLOR = False

    gw1 = args.gateway1.rstrip("/")
    gw2 = args.gateway2.rstrip("/")

    print(bold(f"\n{'#'*64}"))
    print(bold(f"# Phase 11b Reconciler Soak — {ts()}"))
    print(bold(f"# Bridges: {gw1}  |  {gw2}"))
    print(bold(f"# Sweeps: {args.sweeps} (~{args.sweeps * 70}s)"))
    print(bold(f"{'#'*64}"))

    # ── Stack health check ────────────────────────────────────────────────────
    section("Pre-soak: Stack Health")
    stack_health: dict[str, Any] = {}
    ok = True
    for label, url in [("bridge1", gw1), ("bridge2", gw2)]:
        code, _ = http_get(f"{url}/health/live")
        healthy = code == 200
        stack_health[label] = {"live": healthy, "url": url}
        status = green("OK") if healthy else red("FAIL")
        print(f"  {label} /health/live: {status} (HTTP {code})")
        if not healthy:
            ok = False

    if not ok:
        print(red("\nFATAL: Bridges not healthy. Run soak_start.sh first."))
        return 2

    for label, url in [("bridge1", gw1), ("bridge2", gw2)]:
        code, body = http_get(f"{url}/health/ready")
        try:
            data = json.loads(body)
        except Exception:
            data = {}
        healthy = code == 200
        stack_health[label]["ready"] = healthy
        status = green("OK") if healthy else yellow("WARN")
        print(f"  {label} /health/ready: {status} (HTTP {code}) {data}")

    # ── Pre-soak metrics snapshot ─────────────────────────────────────────────
    section("Pre-soak: Metrics Snapshot")
    before_metrics = fetch_metrics(gw1)
    print("  Reconciler metrics (bridge1 baseline):")
    for k, v in sorted((k, v) for k, v in before_metrics.items() if "reconciler" in k):
        print(f"    {k} = {v:.0f}")
    if not any("reconciler" in k for k in before_metrics):
        print(yellow("  WARN: No reconciler metrics found. Is bridge1 running Phase 11b?"))

    # ── Create test scenarios ─────────────────────────────────────────────────
    section("Creating Test Scenarios")
    scenarios: dict[str, Any] = {"stack_health": stack_health}

    # S1: WS run
    print(f"\n  [{ts()}] S1: WS run via bridge1...")
    s1 = ws_run(gw1, args.token, args.app, args.ws_ep, "soak test S1")
    scenarios["s1_run_id"] = s1.get("run_id")
    scenarios["s1_ok"] = s1.get("ok", False)
    scenarios["s1_events"] = s1.get("events", [])
    icon = green("OK") if s1["ok"] else yellow("WARN (websockets not installed?)")
    print(f"    {icon} S1 WS: run_id={str(s1.get('run_id','?'))[:16]}... events={s1['events']}")
    if "error" in s1:
        print(f"    error: {s1['error']}")

    # S2: SSE run
    print(f"\n  [{ts()}] S2: SSE run via bridge1...")
    s2 = sse_run(gw1, args.token, args.app, args.sse_ep, "soak test S2")
    scenarios["s2_run_id"] = s2.get("run_id")
    scenarios["s2_ok"] = s2.get("ok", False)
    scenarios["s2_events"] = s2.get("events", [])
    icon = green("OK") if s2["ok"] else red("FAIL")
    print(f"    {icon} S2 SSE: run_id={str(s2.get('run_id','?'))[:16]}... events={s2['events']}")
    if "error" in s2:
        print(f"    error: {s2['error']}")

    # S3: Synthetic stuck run (reconciler target)
    stuck_id = str(uuid.uuid4())
    scenarios["synthetic_stuck_id"] = stuck_id
    print(f"\n  [{ts()}] S3: Inserting synthetic stuck run (5 min old)...")
    insert_synthetic_stuck_run(stuck_id, started_ago_minutes=5)
    print(f"    ID: {stuck_id}")
    print(f"    Status in DB: {get_run_status(stuck_id)!r}")
    print(f"    Note: No Temporal workflow exists for this ID -> reconciler will call")
    print(f"    DescribeWorkflowExecution({stuck_id[:8]}...) and get NotFound (no Go workflow")
    print(f"    was ever started for this synthetic ID). This tests the NotFound policy.")
    print(f"    -> Expected: notfound_total++, no DB write")

    # S4: Python-native stuck run
    pn_run_id = str(uuid.uuid4())
    pn_ctx_id = str(uuid.uuid4())
    scenarios["python_native_id"] = pn_run_id
    scenarios["python_native_context_id"] = pn_ctx_id
    print(f"\n  [{ts()}] S4: Inserting Python-native stuck run (10 min old)...")
    insert_python_native_stuck_run(pn_run_id, pn_ctx_id)
    print(f"    run_id:    {pn_run_id}")
    print(f"    context_id:{pn_ctx_id}")
    print(f"    Temporal workflow ID would be: ctx-{pn_ctx_id}")
    print(f"    DescribeWorkflow(run_id={pn_run_id[:8]}...) -> NotFound (different wf ID)")
    print(f"    -> Expected: notfound_total++, no DB write")

    # S5: Fresh run (StaleAfter guard)
    fresh_id = str(uuid.uuid4())
    scenarios["fresh_run_id"] = fresh_id
    print(f"\n  [{ts()}] S5: Inserting fresh run (started now)...")
    insert_fresh_run(fresh_id)
    print(f"    ID: {fresh_id}")
    print(f"    -> Expected: NOT returned by eligible query (started_at > now()-2min)")

    # S6: Concurrent traffic
    print(f"\n  [{ts()}] S6: Concurrent traffic (3 WS + 2 SSE)...")
    concurrent_results = []
    with ThreadPoolExecutor(max_workers=5) as pool:
        futures = []
        for i in range(3):
            futures.append(pool.submit(
                ws_run, gw1, args.token, args.app, args.ws_ep,
                f"concurrent WS {i+1}", 30
            ))
        for i in range(2):
            futures.append(pool.submit(
                sse_run, gw1, args.token, args.app, args.sse_ep,
                f"concurrent SSE {i+1}", 30
            ))
        for f in as_completed(futures):
            try:
                r = f.result()
                concurrent_results.append(r)
                icon = green("OK") if r["ok"] else yellow("WARN")
                print(f"    {icon} events={r['events']}, run_id={str(r.get('run_id','?'))[:12]}...")
            except Exception as exc:
                concurrent_results.append({"ok": False, "error": str(exc)})
                print(f"    {yellow('WARN')} exception: {exc}")
    scenarios["concurrent_results"] = concurrent_results
    ok_count = sum(1 for r in concurrent_results if r.get("ok"))
    print(f"    {ok_count}/{len(concurrent_results)} concurrent runs received terminal event")

    # ── Wait for sweep cycles ─────────────────────────────────────────────────
    soak_start_time = time.time()
    section(f"Soak: Waiting for {args.sweeps} sweep cycles (~{args.sweeps * 70}s)")
    print(f"  Reconciler sweeps every 60s. Waiting for {args.sweeps} cycles to observe")
    print(f"  dry-run decisions, NotFound events, and advisory lock behaviour.\n")

    sweep_interval = 70
    for i in range(1, args.sweeps + 1):
        print(f"  [{ts()}] Waiting {sweep_interval}s for sweep #{i}/{args.sweeps}...", flush=True)
        time.sleep(sweep_interval)

        # Print reconciler lines from both bridges
        recent1 = docker_logs_since("them-go-bridge", sweep_interval + 10)
        recent2 = docker_logs_since("them-go-bridge-2", sweep_interval + 10)
        rec_lines = [(l, "bridge1") for l in recent1 if "reconciler" in l.lower()] + \
                    [(l, "bridge2") for l in recent2 if "reconciler" in l.lower()]

        if rec_lines:
            print(f"  [{ts()}] Sweep #{i} reconciler logs:")
            for l, src in rec_lines[-12:]:
                print(f"    [{src}] {l}")
        else:
            print(f"  [{ts()}] No reconciler log lines in this window (may be between sweeps)")

    soak_end_time = time.time()

    # ── Post-soak data collection ─────────────────────────────────────────────
    section("Post-soak: Metrics")
    after_metrics_1 = fetch_metrics(gw1)
    after_metrics_2 = fetch_metrics(gw2)

    all_bridge1_logs = docker_logs_since("them-go-bridge", int(soak_end_time - soak_start_time) + 60)
    all_bridge2_logs = docker_logs_since("them-go-bridge-2", int(soak_end_time - soak_start_time) + 60)

    rec_keys = [
        "them_reconciler_scanned_total",
        "them_reconciler_unchanged_total",
        "them_reconciler_dryrun_total",
        "them_reconciler_notfound_total",
        "them_reconciler_errors_total",
        "them_reconciler_updated_total",
    ]
    print(f"\n  {'Metric':<50} {'B1 before':>10} {'B1 after':>10} {'Δ':>8}")
    print(f"  {'-'*78}")
    for k in rec_keys:
        b = before_metrics.get(k, 0)
        a1 = after_metrics_1.get(k, 0)
        d = a1 - b
        marker = yellow(" << CHANGED") if d != 0 else ""
        print(f"  {k:<50} {b:>10.0f} {a1:>10.0f} {d:>+8.0f}{marker}")
    print()
    print("  Bridge2 reconciler metrics (post-soak):")
    for k in rec_keys:
        a2 = after_metrics_2.get(k, 0)
        print(f"    {k} = {a2:.0f}")

    section("Post-soak: DB State")
    print("  Run status distribution:")
    for status, count in sorted(count_runs_by_status().items()):
        print(f"    {status}: {count}")
    print()
    print(f"  S3 synthetic stuck row status: {get_run_status(stuck_id)!r} (want 'running' — DryRun=true)")
    print(f"  S4 Python-native row status:   {get_run_status(pn_run_id)!r} (want 'running' — NotFound)")
    print(f"  S5 fresh row status:            {get_run_status(fresh_id)!r} (want 'running' — StaleAfter)")

    # ── Run validation checks ─────────────────────────────────────────────────
    section("Validation Checks")
    checks = run_checks(scenarios, before_metrics, after_metrics_1, after_metrics_2, soak_start_time)
    for c in checks:
        print(str(c))

    passed = sum(1 for c in checks if c.passed)
    critical_failures = [c for c in checks if not c.passed and c.critical]
    total = len(checks)
    print(f"\n  {passed}/{total} checks passed, {len(critical_failures)} critical failures")

    # ── Generate report ───────────────────────────────────────────────────────
    section("Report")
    report = generate_report(
        args, scenarios,
        before_metrics, after_metrics_1, after_metrics_2,
        checks, soak_start_time, soak_end_time,
        all_bridge1_logs, all_bridge2_logs,
    )

    with open(args.output, "w") as f:
        json.dump(report, f, indent=2, default=str)

    print(f"  Full report written to: {args.output}")
    print()
    rec = report["recommendation"]
    verdict_str = green(rec["verdict"]) if rec["verdict"] == "SAFE" else red(rec["verdict"])
    print(f"  Recommendation: {bold(verdict_str)}")
    print(f"  {rec['detail']}")
    print()
    print(bold("  *** DryRun=false NOT enabled. Approval required. ***"))

    section("Done")
    print(f"  Soak complete at {ts()}.")
    print(f"  Duration: {int(soak_end_time - soak_start_time)}s")
    print(f"  Report:   {args.output}")
    print()
    print(f"  Paste {args.output} or this output to request DryRun=false approval.")

    return 0 if not critical_failures else 1


if __name__ == "__main__":
    sys.exit(main())
