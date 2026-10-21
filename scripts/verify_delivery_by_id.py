#!/usr/bin/env python3
"""
Verify ClickHouse delivery by tracking specific request IDs from a test run

Schema:
- PostgreSQL gateway_requests.id (UUID) - primary key
- ClickHouse events_gateway_request.request_id (String) - maps to PG id
"""

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
# Convert ISO timestamps to PostgreSQL format (replace Z with +00)
start_ts = data['started_at'].replace('Z', '+00')
end_ts = data['ended_at'].replace('Z', '+00')

result = subprocess.run(
    ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-t", "-c",
     f"SELECT id::text FROM gateway_requests WHERE created_at BETWEEN '{start_ts}' AND '{end_ts}' ORDER BY created_at;"],
    capture_output=True, text=True, check=True
)
pg_request_ids = [line.strip() for line in result.stdout.splitlines() if line.strip()]
print(f"Found {len(pg_request_ids)} requests in PostgreSQL")

if not pg_request_ids:
    print("No requests found in PostgreSQL for this time window")
    sys.exit(0)

print(f"First 5 request IDs: {pg_request_ids[:5]}")
print()

# Check how many of these IDs are in ClickHouse
print("Checking ClickHouse delivery...")

# Sample check - first 10 IDs
sample_ids = pg_request_ids[:min(10, len(pg_request_ids))]
ids_quoted = "','".join(sample_ids)
query = f"SELECT request_id FROM them_events.events_gateway_request WHERE request_id IN ('{ids_quoted}') FORMAT TabSeparated"

result = subprocess.run(
    ["docker", "exec", "them-clickhouse", "clickhouse-client", "--query", query],
    capture_output=True, text=True, check=True
)
ch_request_ids_sample = set(line.strip() for line in result.stdout.splitlines() if line.strip())

print(f"Sample check (first 10 IDs):")
print(f"  In PostgreSQL: {len(sample_ids)}")
print(f"  In ClickHouse: {len(ch_request_ids_sample)}")
print(f"  Missing: {len(sample_ids) - len(ch_request_ids_sample)}")

if len(ch_request_ids_sample) < len(sample_ids):
    print(f"\nMissing request IDs (sample):")
    missing = set(sample_ids) - ch_request_ids_sample
    for req_id in list(missing)[:5]:
        print(f"  {req_id}")
    if len(missing) > 5:
        print(f"  ... and {len(missing) - 5} more")
print()

# Full count check - use timestamp range for efficiency
print("Full delivery check...")
start_ch = data['started_at'].replace('Z', '')
end_ch = data['ended_at'].replace('Z', '')

result = subprocess.run(
    ["docker", "exec", "them-clickhouse", "clickhouse-client", "--query",
     f"SELECT count() FROM them_events.events_gateway_request WHERE ts BETWEEN toDateTime('{start_ch}') AND toDateTime('{end_ch}') FORMAT TabSeparated"],
    capture_output=True, text=True, check=True
)
ch_total = int(result.stdout.strip())

print(f"Count-based verification:")
print(f"  PostgreSQL: {len(pg_request_ids)}")
print(f"  ClickHouse: {ch_total}")
print(f"  Missing: {len(pg_request_ids) - ch_total}")
print(f"  Delivery rate: {ch_total / len(pg_request_ids) * 100:.1f}%")
print()

# ID-based verification - check ALL IDs in batches
print("ID-based verification (checking all request IDs)...")
batch_size = 100
missing_ids = []
found_count = 0

for i in range(0, len(pg_request_ids), batch_size):
    batch = pg_request_ids[i:i+batch_size]
    ids_quoted = "','".join(batch)
    query = f"SELECT request_id FROM them_events.events_gateway_request WHERE request_id IN ('{ids_quoted}') FORMAT TabSeparated"

    result = subprocess.run(
        ["docker", "exec", "them-clickhouse", "clickhouse-client", "--query", query],
        capture_output=True, text=True
    )

    if result.returncode == 0:
        ch_ids = set(line.strip() for line in result.stdout.splitlines() if line.strip())
        found_count += len(ch_ids)
        batch_missing = set(batch) - ch_ids
        missing_ids.extend(batch_missing)

print(f"\nID-based results:")
print(f"  Total expected: {len(pg_request_ids)}")
print(f"  Found in ClickHouse: {found_count}")
print(f"  Missing: {len(missing_ids)}")
print(f"  Delivery rate: {found_count / len(pg_request_ids) * 100:.1f}%")

if missing_ids:
    print(f"\nMissing request IDs (first 10):")
    for req_id in missing_ids[:10]:
        print(f"  {req_id}")
    if len(missing_ids) > 10:
        print(f"  ... and {len(missing_ids) - 10} more")

    # Save missing IDs to file
    missing_file = Path(f"/opt/docker/them/missing_ids_{run_id}.txt")
    missing_file.write_text('\n'.join(missing_ids))
    print(f"\nFull list of missing IDs saved to: {missing_file}")
else:
    print("\n✅ ALL REQUEST IDS DELIVERED TO CLICKHOUSE")
