#!/usr/bin/env bash
# Runs every step of the showcase with the Pulse CLI. Run from this directory.
#   PULSE=/tmp/pulse ./run_all.sh
# Responses land in ./out/ (one JSON envelope per step).
set -euo pipefail
PULSE=${PULSE:-pulse}
mkdir -p out

# Step 0 - import the delivery feed (UTC instants with a Z suffix, so no source zone is needed).
"$PULSE" import csv -i data/deliveries.csv -o deliveries.pulse \
  --schema schemas/deliveries.schema.json --json > out/00_import_deliveries.json

# Step 7a - the overnight sorter feeds arrive as NAIVE local wall-clock times.
# Default policy (error) refuses the DST-affected rows and writes nothing.
"$PULSE" import csv -i data/feeds/syd_night_sort.csv -o syd_night_sort.pulse \
  --schema schemas/night_sort.schema.json --source-tz Australia/Sydney --json > out/13a_import_syd_default.json || true
"$PULSE" import csv -i data/feeds/nyc_night_sort.csv -o nyc_night_sort.pulse \
  --schema schemas/night_sort.schema.json --source-tz America/New_York --json > out/14a_import_nyc_default.json || true
# Explicit policies resolve them and report how many rows were affected.
for pol in earlier later; do
  "$PULSE" import csv -i data/feeds/syd_night_sort.csv -o syd_night_sort_$pol.pulse \
    --schema schemas/night_sort.schema.json --source-tz Australia/Sydney --dst-policy $pol --json > out/13b_import_syd_$pol.json
  "$PULSE" import csv -i data/feeds/nyc_night_sort.csv -o nyc_night_sort_$pol.pulse \
    --schema schemas/night_sort.schema.json --source-tz America/New_York --dst-policy $pol --json > out/14b_import_nyc_$pol.json
done
"$PULSE" export csv -i syd_night_sort_earlier.pulse -o out/syd_night_sort_earlier_local.csv --tz Australia/Sydney
"$PULSE" export csv -i syd_night_sort_later.pulse   -o out/syd_night_sort_later_local.csv   --tz Australia/Sydney

# Zone resolution check (no execution).
"$PULSE" api predict -r req/06_syd_daily_windows.json --json > out/06_predict.json

for f in req/*.json; do
  name=$(basename "$f" .json)
  if jq -e 'has("requests")' "$f" > /dev/null; then
    "$PULSE" api compose -r "$f" --json --no-components > "out/$name.json"
  else
    "$PULSE" api process -r "$f" --json --no-components > "out/$name.json"
  fi
  # --json reports failures inside the envelope; stop on any.
  if [ "$(jq '.errors | length' "out/$name.json")" != "0" ]; then
    echo "FAILED: $name"; jq '.errors' "out/$name.json"; exit 1
  fi
  echo "ok  $name"
done
