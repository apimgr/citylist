#!/usr/bin/env bash
set -eo pipefail

# Binary integration tests require Docker; Incus, VM and browser suites stay explicit.
if command -v docker &>/dev/null; then
  echo "Docker detected - running the basic tests..."
  exec "$(dirname "$0")/docker.sh"
fi

echo "ERROR: docker not found: every test script builds with Docker" >&2
echo "Install Docker: https://docker.com/" >&2
exit 69
