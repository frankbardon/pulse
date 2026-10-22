#!/usr/bin/env bash
# Regenerate the semopy reference fits the parity tests read
# (*_semopy_default.json at semopy's defaults, *_semopy_tight.json at
# ftol 1e-14). Needs python3; creates a venv with semopy 2.3.11.
#   internal/sem/testdata/gen_semopy.sh [VENV_DIR]
# hier_data.csv is simulated: simulate.py 1200 7 hier_model.txt;
# std_data.csv likewise (simulate.py 500 11 std_model.txt), and the std
# references are fitted to its z-scored columns (zscore.py: population
# SD), the external twin of VectorSpec.standardize "zscore".
# The singular-covariance references (MAT_SEM singular "nearest") use
# fit_semopy_nearest.py: singular_data.csv is simulate.py 8 5
# singular_model.txt (n < p); constant_data.csv is simulate.py 80 13
# constant_model.txt with d4 overwritten by the constant 4, fitted
# z-scored. nearest_statsmodels.json is gen_nearest.py (cov_nearest).
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
z="$(mktemp -d)/std_z.csv"
"$py" "$here/zscore.py" "$here/std_data.csv" > "$z"
"$py" "$here/fit_semopy.py" "$z" "$here/std_model.txt" > "$here/std_semopy_default.json"
"$py" "$here/fit_semopy.py" "$z" "$here/std_model.txt" 1e-14 > "$here/std_semopy_tight.json"
for case in "singular:" "constant:zscore"; do
  name="${case%%:*}"; z="${case##*:}"
  "$py" "$here/fit_semopy_nearest.py" "$here/${name}_data.csv" "$here/${name}_model.txt" $z > "$here/${name}_semopy_default.json"
  "$py" "$here/fit_semopy_nearest.py" "$here/${name}_data.csv" "$here/${name}_model.txt" $z 1e-14 > "$here/${name}_semopy_tight.json"
done
"$py" "$here/gen_nearest.py" > "$here/nearest_statsmodels.json"
