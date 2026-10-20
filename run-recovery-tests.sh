#!/bin/bash
set -e

echo "Building test binary..."
docker run --rm \
  -v /opt/docker/them/go:/build \
  -w /build \
  golang:1.23 \
  go test -c -o /build/recovery_tests ./cmd/event-writer

echo "Running tests with Redis access..."
docker run --rm \
  --network them_gateway_them-network \
  -v /opt/docker/them/go:/build \
  -w /build \
  -e REDIS_HOST=them-redis-events:6379 \
  golang:1.23 \
  ./recovery_tests -test.v

echo "Cleanup test binary..."
rm -f /opt/docker/them/go/recovery_tests
