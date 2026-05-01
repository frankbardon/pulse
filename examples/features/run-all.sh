#!/usr/bin/env bash
# Smoke-runs every example request through `pulse api process`. Assumes
# the fixture cohorts have been built into ./.data via
# examples/features/fixtures/build.sh.
#
# Exits non-zero on the first failure.
set -euo pipefail

cd "$(dirname "$0")/../.."

PULSE_BIN=${PULSE_BIN:-bin/pulse}
if [[ ! -x "$PULSE_BIN" ]]; then
  echo "$PULSE_BIN not found; running 'make build'" >&2
  make build
fi

if [[ ! -d .data ]]; then
  echo ".data not found; running fixtures/build.sh" >&2
  ./examples/features/fixtures/build.sh
fi

shopt -s nullglob
for f in examples/features/0*.json examples/features/10_*.json; do
  printf '%-60s' "$f"
  out=$("$PULSE_BIN" api process --request "$f" --json 2>&1)
  if echo "$out" | python3 -c '
import json, sys
d = json.load(sys.stdin)
errs = d.get("errors") or []
if errs:
  print("FAIL:", [e.get("code") for e in errs])
  sys.exit(1)
data = d.get("data") or {}
rows = data.get("data") if isinstance(data, dict) else None
print("ok (rows={})".format(len(rows) if rows is not None else "?"))
'; then
    :
  else
    echo "$out" | head -20
    exit 1
  fi
done

echo
echo "Predict-only leakage check:"
"$PULSE_BIN" api predict --request examples/features/09_target_encode_leaky.json --json --strict | \
  python3 -c '
import json, sys
d = json.load(sys.stdin)
codes = [e["code"] for e in (d.get("errors") or [])]
if "PULSE_FEAT_TARGET_LEAKAGE_RISK" in codes:
  print("  ok (PULSE_FEAT_TARGET_LEAKAGE_RISK fired in --strict)")
else:
  print("  FAIL: expected PULSE_FEAT_TARGET_LEAKAGE_RISK; got", codes); sys.exit(1)
'
