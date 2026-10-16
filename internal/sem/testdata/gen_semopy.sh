#!/usr/bin/env bash
# Regenerate the semopy reference fits the parity tests read
# (*_semopy_default.json at semopy's defaults, *_semopy_tight.json at
# ftol 1e-14). Needs python3; creates a venv with semopy 2.3.11.
#   internal/sem/testdata/gen_semopy.sh [VENV_DIR]
# hier_data.csv is simulated: simulate.py 1200 7 hier_model.txt.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
venv="${1:-$(mktemp -d)/venv}"
if [ ! -x "$venv/bin/python" ]; then
  python3 -m venv "$venv"
  "$venv/bin/pip" install -q semopy==2.3.11 pandas
fi
py="$venv/bin/python"
for case in hier:hier poldem:poldem hz:hz hz_ext:hz; do
  name="${case%%:*}"; data="${case##*:}"
  "$py" "$here/fit_semopy.py" "$here/${data}_data.csv" "$here/${name}_model.txt" > "$here/${name}_semopy_default.json"
  "$py" "$here/fit_semopy.py" "$here/${data}_data.csv" "$here/${name}_model.txt" 1e-14 > "$here/${name}_semopy_tight.json"
done
