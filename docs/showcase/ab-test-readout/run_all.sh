#!/usr/bin/env bash
# Regenerates the cohorts and runs every step of the readout, writing each
# Pulse JSON envelope to out/. Run from this directory:
#   PULSE=/tmp/pulse ./run_all.sh
set -euo pipefail
PULSE=${PULSE:-pulse}
cd "$(dirname "$0")"
mkdir -p out

# Step 0 — data
"$PULSE" synth from-schema -s data/ab_test.synth.json -o ab_test.pulse --seed 64 --json > out/00_synth.json
"$PULSE" synth from-schema -s data/design.synth.json  -o design.pulse  --seed 1  --json > out/00_design.json
"$PULSE" cohort inspect ab_test.pulse --json > out/00_inspect.json

# Step 1 — predict (no data read beyond header + schema)
"$PULSE" api predict -r req/01_predict_battery.json --json            > out/01_predict_battery.json
"$PULSE" api predict -r req/04_metric_battery.json --json             > out/01_predict_corrected.json

# Steps 2-10
"$PULSE" api facet   -r req/02_srm_gof.facet.json --json             > out/02_srm_gof.json
"$PULSE" api process -r req/03_srm_by_week_and_balance.json --json   > out/03_srm_by_week_and_balance.json
"$PULSE" api process -r req/04_metric_battery.json --json            > out/04_metric_battery.json
"$PULSE" api compose -r req/05_revenue_shape.compose.json --json     > out/05_revenue_shape.json
"$PULSE" api process -r req/06_revenue_tests.json --json             > out/06_revenue_tests.json
"$PULSE" api process -r req/07_regression_adjustment.json --json     > out/07_regression_adjustment.json
"$PULSE" api process -r req/08_cuped_welch.json --json               > out/08_cuped_welch.json
"$PULSE" api compose -r req/09_segments.compose.json --json          > out/09_segments.json
"$PULSE" api process -r req/10_heterogeneity.json --json             > out/10_heterogeneity.json

# Step 11 — the whole readout in one Compose call (serial and parallel)
"$PULSE" api predict-compose -r req/11_readout.compose.json --json   > out/11_predict.json
"$PULSE" api compose -r req/11_readout.compose.json --json           > out/11_readout.json
"$PULSE" api compose -r req/11_readout.compose.json --json --parallel 0 > out/11_readout_parallel.json

echo "wrote $(ls out | wc -l) envelopes to out/"
