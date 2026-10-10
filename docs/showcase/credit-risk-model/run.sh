#!/usr/bin/env bash
# Regenerates the cohort and runs every request of the credit-risk showcase, in order.
# Usage: PULSE=/path/to/pulse ./run.sh        (run from this directory, or a copy of it)
# Responses land in results/NN_*.json; the cohort (loans.pulse) is written next to this script.
set -euo pipefail
cd "$(dirname "$0")"
PULSE=${PULSE:-pulse}
mkdir -p results

"$PULSE" synth from-schema --spec data/loans.synth.json --output loans.pulse --seed 20240630
"$PULSE" cohort inspect loans.pulse > results/00_inspect.txt

# The target-encoding request is PREDICTED first: the leakage warning arrives before anything runs.
"$PULSE" api predict --request req/07_target_encode_predict.json --json > results/07_target_encode_predict.predict.json

for r in 01_profile 02a_emp_length_missing 02b_emp_length_present 03_rate_by_band 04_rate_by_purpose \
         05_leak_check 06_features_split 06b_dti_buckets 07_target_encode_predict \
         08_leaky_vs_clean_fit 08b_in_sample_fitted_ks 09_stepwise_bic \
         10_purpose_rates_train_only 10b_train_default_rate 11_stepwise_bic_with_safe_te \
         12_final_model_bootstrap 13_holdout_deciles 13b_holdout_deciles_leaky 14_holdout_ks_and_calibration; do
  echo "== $r" >&2
  "$PULSE" api process --request "req/$r.json" --json > "results/$r.json"
done

"$PULSE" explain --request req/14_holdout_ks_and_calibration.json > results/15_explain_request.txt
"$PULSE" explain --request req/14_holdout_ks_and_calibration.json \
  --response results/14_holdout_ks_and_calibration.json > results/15_explain_response.txt
echo "done: responses in results/" >&2
