#!/usr/bin/env bash
# Build the shared .pulse cohort fixtures from the checked-in CSVs and
# hand-tuned schemas. Every example category under examples/ resolves
# its cohorts from the same output directory.
#
# By default that's the repo-local ".data" directory; override by
# exporting PULSE_DATA_DIR before invoking.
#
# Usage:
#   ./examples/fixtures/build.sh
#   PULSE_DATA_DIR=/tmp/pulse-data ./examples/fixtures/build.sh
set -euo pipefail

cd "$(dirname "$0")/../.."

PULSE_BIN=${PULSE_BIN:-bin/pulse}
if [[ ! -x "$PULSE_BIN" ]]; then
  echo "$PULSE_BIN not found; running 'make build'" >&2
  make build
fi

DATA_DIR=${PULSE_DATA_DIR:-.data}
mkdir -p "$DATA_DIR"

FIXTURES=examples/fixtures

for cohort in transactions customers orders training_data all_types experiment repeated_measures; do
  echo "==> building $cohort.pulse"
  "$PULSE_BIN" import csv \
    --input  "$FIXTURES/$cohort.csv" \
    --output "$DATA_DIR/$cohort.pulse" \
    --schema "$FIXTURES/schemas/$cohort.json" \
    --json > /dev/null
done

echo
echo "Cohorts written to: $DATA_DIR"
echo "Examples reference cohort.data_dir = \"$DATA_DIR\" so requests run unmodified."
