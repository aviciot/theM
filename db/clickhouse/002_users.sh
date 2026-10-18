#!/bin/bash
# the-M events store — least-privilege ClickHouse users.
# Runs once on first start (docker-entrypoint-initdb.d), after 001_events.sql.
#
#   events_writer — INSERT only   (used by them-event-writer)
#   events_reader — SELECT only   (used by the Go query layer; tenant filter is added in code, §7.1)
#
# Neither user can run DDL. Passwords come from the container environment
# (derived by scripts/events-env.sh) — never hardcoded here.
set -euo pipefail

: "${EVENTS_WRITER_PASSWORD:?EVENTS_WRITER_PASSWORD is required}"
: "${EVENTS_READER_PASSWORD:?EVENTS_READER_PASSWORD is required}"

clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --multiquery <<SQL
CREATE USER IF NOT EXISTS events_writer IDENTIFIED WITH sha256_password BY '${EVENTS_WRITER_PASSWORD}';
CREATE USER IF NOT EXISTS events_reader IDENTIFIED WITH sha256_password BY '${EVENTS_READER_PASSWORD}';
GRANT INSERT ON them_events.* TO events_writer;
GRANT SELECT ON them_events.* TO events_reader;
SQL

echo "events users created: events_writer (INSERT), events_reader (SELECT)"
