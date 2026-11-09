#!/usr/bin/env bash
# forbidcheck: fail if any non-test Go file outside internal/executor deletes,
# renames, truncates or runs commands (docs/adr/0003-executor-is-the-only-mutator.md).
# Exceptions: tools/forbidcheck/allow.txt.
set -euo pipefail
cd "$(dirname "$0")/.."
exec "${GO:-go}" run ./tools/forbidcheck "$@" ./...
