#!/usr/bin/env bash
# Smoke-runs every crosstab example through `pulse api process`.
# Assumes the shared fixture cohorts have been built into ./.data via
# examples/fixtures/build.sh.
set -euo pipefail

cd "$(dirname "$0")/../.."

PULSE_BIN=${PULSE_BIN:-bin/pulse}
if [[ ! -x "$PULSE_BIN" ]]; then
  echo "$PULSE_BIN not found; running 'make build'" >&2
  make build
fi

if [[ ! -d .data ]]; then
  echo ".data not found; running examples/fixtures/build.sh" >&2
  ./examples/fixtures/build.sh
fi

shopt -s nullglob
for f in examples/crosstab/*.json; do
  printf '%-60s' "$f"
  out=$("$PULSE_BIN" api process --request "$f" --json 2>&1)
  if echo "$out" | python3 -c '
import json, sys
d = json.load(sys.stdin)
errs = d.get("errors") or []
if errs:
  print("FAIL:", [e.get("code") for e in errs]); sys.exit(1)
data = d.get("data") or {}
rows = data.get("data") if isinstance(data, dict) else None
ct = data.get("crosstab") if isinstance(data, dict) else None
tests = data.get("tests") if isinstance(data, dict) else None
shape = ct.get("shape") if isinstance(ct, dict) else None
matrix = ct.get("matrix") if isinstance(ct, dict) else None
nrows = len(matrix.get("row_keys", [])) if isinstance(matrix, dict) else 0
ncols = len(matrix.get("column_keys", [])) if isinstance(matrix, dict) else 0
print("ok (shape={} rows={} cols={} long_rows={} tests={})".format(
  shape, nrows, ncols,
  len(rows) if rows else 0,
  len(tests) if tests else 0,
))
'; then
    :
  else
    echo "$out" | head -20
    exit 1
  fi
done
