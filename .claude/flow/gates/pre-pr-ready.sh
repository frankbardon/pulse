#!/usr/bin/env bash
# .claude/flow/gates/pre-pr-ready.sh
# Runs before the effort PR is marked ready for review. Non-zero exit blocks.
set -euo pipefail

GOTOOLCHAIN=go1.26.1 make lint
go test -count=1 ./...
make smoke
GOTOOLCHAIN=go1.26.1 make contrib
