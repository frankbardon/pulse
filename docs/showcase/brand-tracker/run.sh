#!/usr/bin/env bash
# Rebuilds the brand-tracker cohort and runs every step of the walkthrough.
# Usage: PULSE=/path/to/pulse ./run.sh   (run from anywhere; outputs land in ./out and ./deliver)
set -euo pipefail
PULSE=${PULSE:-pulse}
cd "$(dirname "$0")"
export PULSE_LABEL_TABLES_DIR=./labels
mkdir -p out deliver

echo "== 00 synthesize cohort (seed 2025)"
"$PULSE" synth from-schema -s spec/brand_tracker.synth.json -o brand_tracker.pulse --seed 2025 --json > out/00_synth.json
"$PULSE" cohort inspect brand_tracker.pulse --json > out/00_inspect.json

run() {  # run <request file>: predict first, then execute
  local f=$1 n; n=$(basename "$f" .json)
  if grep -q '"requests"' "$f"; then
    "$PULSE" api predict-compose -r "$f" --json > "out/${n}.predict.json"
    "$PULSE" api compose -r "$f" --json > "out/${n}.json"
  else
    "$PULSE" api predict -r "$f" --json > "out/${n}.predict.json"
    "$PULSE" api process -r "$f" --json > "out/${n}.json"
  fi
  echo "== ${n}: errors=$(jq -c '.errors|length' "out/${n}.json") warnings=$(jq -c '.warnings|length' "out/${n}.json")"
}

for f in req/0*.json req/10_*.json; do run "$f"; done

echo "== 11 client hand-off: SPSS .sav (+ labelled Excel)"
"$PULSE" export predict -i brand_tracker.pulse --format spss --sanitize-names --json > out/11_export_predict.json
"$PULSE" export spss -i brand_tracker.pulse -o deliver/brand_tracker_2025.sav --sanitize-names --json > out/11_export_spss.json
"$PULSE" export excel -i brand_tracker.pulse -o deliver/brand_tracker_2025.xlsx \
  --labels wave=wave_names:augment --labels region=region_names:augment --json > out/11_export_excel.json
rm -f deliver/roundtrip.pulse deliver/roundtrip.pulse.spss.json
"$PULSE" import spss -i deliver/brand_tracker_2025.sav -o deliver/roundtrip.pulse --json > out/11_import_spss.json
"$PULSE" cohort inspect deliver/roundtrip.pulse --json > out/11_roundtrip_inspect.json

run req/12_roundtrip_check.json
