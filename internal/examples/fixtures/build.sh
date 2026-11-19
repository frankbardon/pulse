#!/usr/bin/env bash
# Build the shared .pulse cohort fixtures from the checked-in CSVs and
# hand-tuned schemas. Every example category under internal/examples/ resolves
# its cohorts from the same output directory.
#
# By default that's the repo-local ".data" directory; override by
# exporting PULSE_DATA_DIR before invoking.
#
# Usage:
#   ./internal/examples/fixtures/build.sh
#   PULSE_DATA_DIR=/tmp/pulse-data ./internal/examples/fixtures/build.sh
set -euo pipefail

cd "$(dirname "$0")/../../.."

PULSE_BIN=${PULSE_BIN:-bin/pulse}
if [[ ! -x "$PULSE_BIN" ]]; then
  echo "$PULSE_BIN not found; running 'make build'" >&2
  make build
fi

DATA_DIR=${PULSE_DATA_DIR:-.data}
mkdir -p "$DATA_DIR"

FIXTURES=internal/examples/fixtures

for cohort in transactions customers orders training_data all_types experiment repeated_measures card_issuers tier_migrations survey subscribers warehouse_orders plan_history plan_history_wide battery intake mmm_weekly brand_survey; do
  echo "==> building $cohort.pulse"
  "$PULSE_BIN" import csv \
    --input  "$FIXTURES/$cohort.csv" \
    --output "$DATA_DIR/$cohort.pulse" \
    --schema "$FIXTURES/schemas/$cohort.json" \
    --json > /dev/null
done

# The point-lookup index a {record} comparison reference reads
# (attributes/20_record_reference.json).
echo "==> indexing experiment.pulse on id"
"$PULSE_BIN" index build "$DATA_DIR/experiment.pulse" --key id --json > /dev/null

# The synthetic twin of customers.pulse the union example
# (tests/38_union_source_vs_twin.json) compares with its source: a
# default profile, its spec at 200 rows, generated rows only, seed 7 —
# the same twin internal/integration's buildCustomersTwinFixture writes.
echo "==> generating customers_twin.pulse"
rm -f "$DATA_DIR/customers_twin.pulse" "$DATA_DIR/customers.profile.json"
"$PULSE_BIN" profile create --input "$DATA_DIR/customers.pulse" \
  --output "$DATA_DIR/customers.profile.json" > /dev/null
"$PULSE_BIN" synth from-profile --profile "$DATA_DIR/customers.profile.json" \
  --source "$DATA_DIR/customers.pulse" --output "$DATA_DIR/customers_twin.pulse" \
  --rows 200 --seed 7 --synthetic-only --json > /dev/null

echo
echo "Cohorts written to: $DATA_DIR"
echo "Examples reference cohort.data_dir = \"$DATA_DIR\" so requests run unmodified."
