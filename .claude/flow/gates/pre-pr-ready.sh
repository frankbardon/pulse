#!/usr/bin/env bash
# .claude/flow/gates/pre-pr-ready.sh
# Runs before the effort PR is marked ready for review. Non-zero exit blocks.
# -tags heavy runs the full fast suite plus the full heavy tier.
set -euo pipefail

GOTOOLCHAIN=go1.26.1 make lint
go test -count=1 -tags heavy -timeout=30m ./...
make smoke
