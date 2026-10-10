#!/usr/bin/env bash
# Runs the whole privacy-safe sharing walkthrough with the Pulse CLI only.
# Usage (from this directory):  PULSE=/path/to/pulse ./run.sh
set -euo pipefail
PULSE=${PULSE:-pulse}
mkdir -p out

echo "== 1. import the source table"
$PULSE import csv -i data/customers.csv -o customers.pulse --schema data/customers.schema.json --json > out/00_import.json
$PULSE api process -r req/01_source_overview.json --json > out/01_source_overview.json

echo "== 2. capture the profile (+ rule candidates)"
$PULSE profile create -i customers.pulse -o synth/profile.json \
  --include-stats --include-correlations --conditional \
  --fit-models --residual-correlations --fit-shape \
  --suggest-rules synth/suggested_rules.json --seed 7 --json > out/02_profile.json

echo "== 3. synthesize WITHOUT structural rules (for comparison)"
$PULSE synth from-profile -p synth/profile.json --source customers.pulse -o tagged_norules.pulse \
  --rows 40000 --seed 11 --fidelity-report synth/fidelity_norules.json \
  --emit-spec synth/spec_norules.json --json > out/03_synth_norules.json

echo "== 4. synthesize WITH the reviewed structural rules"
$PULSE synth from-profile -p synth/profile.json --source customers.pulse -o tagged.pulse \
  --rows 40000 --seed 11 --rules synth/rules.json --fidelity-report synth/fidelity.json \
  --emit-spec synth/spec.json --json > out/04_synth_rules.json

echo "== 5. build the shareable synthetic-only cohorts from the emitted specs"
$PULSE synth from-schema -s synth/spec.json -o twin.pulse --seed 11 --json > out/05_twin.json
$PULSE synth from-schema -s synth/spec_norules.json -o twin_norules.pulse --seed 11 --json > out/05_twin_norules.json
$PULSE cohort inspect --json twin.pulse > out/05_inspect_twin.json

echo "== 6. determinism: regenerate and compare"
$PULSE synth from-schema -s synth/spec.json -o twin_rerun.pulse --seed 11 --json > out/06_twin_rerun.json
cmp twin.pulse twin_rerun.pulse && echo "twin.pulse and twin_rerun.pulse are byte-identical"
$PULSE api compose -r req/02_twin_equals_scored_rows.json --json > out/06_twin_equals_scored_rows.json

echo "== 7. business-rule violations"
$PULSE api compose -r req/03_business_rule_violations.json --json > out/07_violations.json

echo "== 8. the agency's analysis on source vs twin"
$PULSE api compose -r req/04_headline_metrics.json --json > out/08_headline.json
$PULSE api compose -r req/04b_headline_twin_vs_source.json --json > out/08_headline_overlays.json
$PULSE api compose -r req/05_segments_twin_vs_source.json --json > out/08_segments.json
$PULSE api compose -r req/06_churn_crosstab_twin_vs_source.json --json > out/08_crosstab.json
$PULSE api compose -r req/07_chisq_tests.json --json > out/08_chisq.json
$PULSE api compose -r req/08_churn_logistic_model.json --json > out/08_glm.json
$PULSE api compose -r req/09_correlation_matrix.json --json > out/08_corr.json

echo "== 9. disclosure checks"
$PULSE api compose -r req/10_exact_record_match.json --json > out/09_exact_match.json
$PULSE cohort filter -i customers.pulse -o source_half_a.pulse --filter 'int(round(monthly_spend * 100)) % 2 == 0' --json > out/09_half_a.json
$PULSE cohort filter -i customers.pulse -o source_half_b.pulse --filter 'int(round(monthly_spend * 100)) % 2 == 1' --json > out/09_half_b.json
$PULSE cohort filter -i twin.pulse -o twin_half_a.pulse --filter 'int(round(monthly_spend * 100)) % 2 == 0' --json > out/09_twin_half_a.json
$PULSE api compose -r req/11_profile_match_vs_chance.json --json > out/09_profile_match.json

echo "== 10. (comparison) the same pipeline without --fit-models"
$PULSE profile create -i customers.pulse -o synth/profile_conditional_only.json \
  --include-stats --include-correlations --conditional --seed 7 --json > out/10_profile_conditional_only.json
$PULSE synth from-profile -p synth/profile_conditional_only.json --source customers.pulse \
  -o tagged_conditional_only.pulse --rows 40000 --seed 11 --rules synth/rules.json \
  --emit-spec synth/spec_conditional_only.json --json > out/10_synth_conditional_only.json
$PULSE synth from-schema -s synth/spec_conditional_only.json -o twin_conditional_only.pulse --seed 11 --json > out/10_twin_conditional_only.json
$PULSE api compose -r req/08b_churn_logistic_conditional_only_twin.json --json > out/10_glm_conditional_only.json

echo "done; responses are in out/"
