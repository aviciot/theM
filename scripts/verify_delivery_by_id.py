#!/usr/bin/env python3
"""Verify ClickHouse delivery by tracking specific request IDs from a test run"""

import json
import subprocess
import sys
from pathlib import Path

if len(sys.argv) < 2:
    print("Usage: verify_delivery_by_id.py <run_id>")
    sys.exit(1)

run_id = sys.argv[1]
history_file = Path(f"/opt/docker/them/test-runner/data/history/{run_id}.json")

if not history_file.exists():
    print(f"Error: History file not found: {history_file}")
    sys.exit(1)

# Load test run data
data = json.loads(history_file.read_text())
print(f"Run ID: {run_id}")
print(f"Started: {data['started_at']}")
print(f"Ended: {data['ended_at']}")
print(f"Users: {data['n_users']}, Passed: {data['passed']}")
print()

# Get all request IDs from PostgreSQL that match this run's timestamp window
print("Fetching request IDs from PostgreSQL...")
result = subprocess.run(
    ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-t", "-c",
     f"SELECT request_id FROM gateway_requests WHERE created_at BETWEEN '{data['started_at']}' AND '{data['ended_at']}' ORDER BY created_at;"],
    capture_output=True, text=True, check=True
)
pg_request_ids = [line.strip() for line in result.stdout.splitlines() if line.strip()]
print(f"Found {len(pg_request_ids)} requests in PostgreSQL")

if not pg_request_ids:
    print("No requests found in PostgreSQL for this time window")
    sys.exit(0)

# Check how many of these IDs are in ClickHouse
print("\nChecking ClickHouse delivery...")
# Use IN clause with first 10 IDs as sample
sample_ids = pg_request_ids[:min(10, len(pg_request_ids))]
ids_quoted = "','".join(sample_ids)
query = f"SELECT request_id FROM them_events.events_gateway_request WHERE request_id IN ('{ids_quoted}')"

result = subprocess.run(
    ["docker", "exec", "them-clickhouse", "clickhouse-client",
     "--query", query + " FORMAT TabSeparated"],
    capture_output=True, text=True, check=True
)
ch_request_ids = [line.strip() for line in result.stdout.splitlines() if line.strip()]

print(f"Sample check (first 10 IDs):")
print(f"  In PostgreSQL: {len(sample_ids)}")
print(f"  In ClickHouse: {len(ch_request_ids)}")
print(f"  Missing: {len(sample_ids) - len(ch_request_ids)}")

if len(ch_request_ids) < len(sample_ids):
    print(f"\nMissing request IDs:")
    missing = set(sample_ids) - set(ch_request_ids)
    for req_id in list(missing)[:5]:
        print(f"  {req_id}")
    if len(missing) > 5:
        print(f"  ... and {len(missing) - 5} more")

# Full count check
result = subprocess.run(
    ["docker", "exec", "them-clickhouse", "clickhouse-client", "--query",
     f"SELECT count() FROM them_events.events_gateway_request WHERE ts BETWEEN toDateTime('{data['started_at'].replace('Z', '')}') AND toDateTime('{data['ended_at'].replace('Z', '')}') FORMAT TabSeparated"],
    capture_output=True, text=True, check=True
)
ch_total = int(result.stdout.strip())

print(f"\nFull count:")
print(f"  PostgreSQL: {len(pg_request_ids)}")
print(f"  ClickHouse: {ch_total}")
print(f"  Missing: {len(pg_request_ids) - ch_total}")
print(f"  Delivery rate: {ch_total / len(pg_request_ids) * 100:.1f}%")
