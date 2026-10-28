#!/usr/bin/env bash
# Smoke-runs every materialize example through `pulse api materialize`
# (predict first, then the write into a scratch directory).
# Assumes the shared fixture cohorts have been built into ./.data via
# internal/examples/fixtures/build.sh.
set -euo pipefail

cd "$(dirname "$0")/../../.."

PULSE_BIN=${PULSE_BIN:-bin/pulse}
if [[ ! -x "$PULSE_BIN" ]]; then
  echo "$PULSE_BIN not found; running 'make build'" >&2
  make build
fi

if [[ ! -d .data ]]; then
  echo ".data not found; running internal/examples/fixtures/build.sh" >&2
  ./internal/examples/fixtures/build.sh
fi

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

shopt -s nullglob
for f in internal/examples/materialize/*.json; do
  printf '%-60s' "$f"
  if "$PULSE_BIN" api materialize --request "$f" --predict --json >/dev/null 2>&1 &&
     "$PULSE_BIN" api materialize --request "$f" --out "$scratch/$(basename "$f" .json).pulse" --json >/dev/null 2>&1; then
    echo OK
  else
    echo FAIL
    exit 1
  fi
done
