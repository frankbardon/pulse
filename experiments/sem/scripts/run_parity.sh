#!/usr/bin/env bash
# Reproduce the semopy parity comparison end to end.
#   scripts/run_parity.sh [WORKDIR]
# Needs python3 (a venv with semopy is created in WORKDIR) and go.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
work="${1:-$(mktemp -d)}"
mkdir -p "$work"
if [ ! -x "$work/venv/bin/python" ]; then
  python3 -m venv "$work/venv"
  "$work/venv/bin/pip" install -q semopy==2.3.11 pandas
fi
py="$work/venv/bin/python"
(cd "$here" && go build -o "$work/semfit" ./cmd/semfit)
td="$here/sem/testdata"
for case in hier:hier poldem:poldem hz:hz hz_ext:hz; do
  name="${case%%:*}"; data="${case##*:}"
  csv="$td/${data}_data.csv"; model="$td/${name}_model.txt"
  "$py" "$here/scripts/fit_semopy.py" "$csv" "$model" > "$work/semopy_${name}.json"
  "$py" "$here/scripts/fit_semopy.py" "$csv" "$model" 1e-14 > "$work/semopy_${name}_tight.json"
  "$work/semfit" -csv "$csv" -model "$model" > "$work/pulse_${name}.json"
  "$work/semfit" -csv "$csv" -model "$model" -solver fisher -information rank2 > "$work/pulse_${name}_fisher_rank2.json"
  echo "== $name: Pulse default (SLSQP port) vs semopy default"
  python3 "$here/scripts/compare.py" "$work/semopy_${name}.json" "$work/pulse_${name}.json"
  echo "== $name: Pulse fisher + rank2 vs semopy converged (ftol 1e-14)"
  python3 "$here/scripts/compare.py" "$work/semopy_${name}_tight.json" "$work/pulse_${name}_fisher_rank2.json"
done
echo "outputs in $work"
