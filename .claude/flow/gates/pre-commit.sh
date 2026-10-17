#!/usr/bin/env bash
# .claude/flow/gates/pre-commit.sh
# Runs before every flow-driven commit. Non-zero exit blocks the action.
# Fast checks only; the full lint + tests run at pre-push.
set -euo pipefail

GOTOOLCHAIN=go1.26.1 make fmt-check vet
