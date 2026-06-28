#!/bin/bash
set -e
cd "$(dirname "${BASH_SOURCE[0]}")/.."
pytest tests/ -v --tb=short "$@"
