#!/usr/bin/env bash
# .claude/flow/gates/pre-push.sh
# Runs before every flow-driven push (once per test-group checkpoint).
# Non-zero exit blocks the action. Scope-aware: FLOW_GATE_SCOPE=changed tests
# only the packages the effort touched; the full suite runs at pre-pr-ready.
# Tests run with -tags heavy, which compiles both tiers (the fast tier plus
# the `//go:build heavy` files), so one run covers fast + heavy. No
# -count=1: Go's test cache re-runs only packages whose code, inputs
# or env changed, so a push after a small change takes minutes, not 20.
set -euo pipefail

GOTOOLCHAIN=go1.26.1 make lint
if [ "${FLOW_GATE_SCOPE:-full}" = changed ]; then
  pkgs="$(~/.claude/skills/flow-shared/scripts/go-affected-pkgs.sh 2>/dev/null || echo ./...)"
  [ -z "$pkgs" ] || go test -tags heavy -timeout=30m $pkgs
else
  go test -tags heavy -timeout=30m ./...
fi
