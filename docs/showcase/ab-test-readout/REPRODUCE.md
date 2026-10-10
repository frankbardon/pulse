# Reproducing the A/B test readout

Everything is regenerated from the two synth specs in `data/`, with fixed seeds. No `.pulse` file is
committed. The commands use `jq` only to pull numbers out of the Pulse JSON envelopes.

## 1. Build Pulse and copy the example to a scratch directory

```bash
# from a fresh checkout of the repo root
go build -o /tmp/pulse ./cmd/pulse
rm -rf /tmp/ab-test-readout && cp -r docs/showcase/ab-test-readout /tmp/ab-test-readout
cd /tmp/ab-test-readout
```

## 2. Run every step

```bash
PULSE=/tmp/pulse ./run_all.sh        # writes 17 JSON envelopes to out/
```

`run_all.sh` runs these commands in order (all from this directory):

```bash
P=/tmp/pulse
$P synth from-schema -s data/ab_test.synth.json -o ab_test.pulse --seed 64 --json
$P synth from-schema -s data/design.synth.json  -o design.pulse  --seed 1  --json
$P cohort inspect ab_test.pulse --json
$P api predict -r req/01_predict_battery.json --json
$P api predict -r req/04_metric_battery.json --json
$P api facet   -r req/02_srm_gof.facet.json --json
$P api process -r req/03_srm_by_week_and_balance.json --json
$P api process -r req/04_metric_battery.json --json
$P api compose -r req/05_revenue_shape.compose.json --json
$P api process -r req/06_revenue_tests.json --json
$P api process -r req/07_regression_adjustment.json --json
$P api process -r req/08_cuped_welch.json --json
$P api compose -r req/09_segments.compose.json --json
$P api process -r req/10_heterogeneity.json --json
$P api predict-compose -r req/11_readout.compose.json --json
$P api compose -r req/11_readout.compose.json --json
$P api compose -r req/11_readout.compose.json --json --parallel 0
```

Expected:
- `ab_test.pulse` is 2,121,853 bytes, SHA-256 `019da9318bd8436958f0a76ffbfa5a6143e77da5162afbe003ef87875404033b`.
- `design.pulse` is 181 bytes, SHA-256 `2cc31b09e7e70530976aab06a7e6dfbf5bc5dc2fd4c65567cf5cca924cbec344`.
- Every envelope has `errors: []` and `warnings: []`.

## 3. Check the key numbers

```bash
# every envelope clean
for f in out/*.json; do echo "$f $(jq -c '[(.errors|length),(.warnings|length)]' $f)"; done

jq -c '.data | {streamable, streamable_reasons, p_values}' out/01_predict_battery.json out/01_predict_corrected.json
jq -c '.data.fields.arm.discrete.values, .data.overlays[0].summary' out/02_srm_gof.json
jq -c '.data.tests[] | {label, statistic, p_value, p_adjusted}' out/03_srm_by_week_and_balance.json
jq -c '.data.tests[] | {label, diff: .details.diff, ci: [.details.ci_low, .details.ci_high], p_value, p_adjusted}' out/04_metric_battery.json
jq -c '.data.responses[].data' out/05_revenue_shape.json
jq -c '.data.tests[] | {label, statistic, p_value, z: .details.z, warnings}' out/06_revenue_tests.json
jq -c '.data.data, (.data.regressions[] | {name, coefficients, std_errors, p_values, r2})' out/07_regression_adjustment.json
jq -c '.data.tests[] | {label, diff: .details.diff, ci: [.details.ci_low, .details.ci_high], variance: .details.variance, p_value}' out/08_cuped_welch.json
jq -r '.data.responses[] | [.data[0].users, .tests[0].details.diff, .tests[0].p_value, .tests[0].p_adjusted] | @tsv' out/09_segments.json
jq -c '.data.regressions[] | {name, coefficients, std_errors, p_values}' out/10_heterogeneity.json
jq -c '.data.responses[1].tests[] | {label, p_value, p_adjusted}' out/11_readout.json
cmp out/11_readout.json out/11_readout_parallel.json && echo "serial == parallel"
```

| Step | Output | Expected |
|---|---|---|
| 1 | predict 01: `streamable` / reasons / `p_values` | `false` / `test TEST_MANN_WHITNEY_U is not streamable` / total 7, uncorrected 7, threshold 10 |
| 1 | predict 04: `streamable` / `p_values` | `true` / total 6, uncorrected 0 |
| 2 | arm counts | new_page 20087, old_page 19913 |
| 2 | SRM GOF χ², df, p | 0.7569, 1, 0.38430 |
| 3 | srm_by_week χ², p | 3.0865, 0.37848 |
| 3 | balance p: device / plan / region / user_type / prior_sessions | 0.29011 / 0.42243 / 0.70785 / 0.53457 / 0.80158; all `p_adjusted` 1 |
| 4 | trial_start diff, CI, p, Holm p | 0.010162, [0.004086, 0.016239], 0.0010499, 0.0062996 |
| 4 | paid_conversion diff, p, Holm p | 0.0039715, 0.024093, 0.096374 |
| 4 | revenue_per_user diff, p | −0.16099, 0.68108 |
| 4 | pages_viewed diff, p, Holm p | 0.093823, 0.017148, 0.085739 |
| 4 | days_to_convert diff, p, Holm p; n | −0.67636, 0.20141, 0.60423; [683, 598] |
| 4 | retention_30d diff, p | −0.0013014, 0.79238 |
| 5 | revenue/user new, old; payers; revenue/payer | 4.45553, 4.61652; 683, 598; 131.037, 153.727 |
| 6 | Welch p; Mann-Whitney z, p | 0.68108; 2.18793, 0.028675 (+ `PULSE_TEST_TIES_DOMINATE` test warning) |
| 7 | is_variant coef / SE / p, unadjusted | 0.093823 / 0.039362 / 0.017150 |
| 7 | is_variant coef / SE / p, adjusted; R² | 0.086158 / 0.024882 / 0.00053539; 0.60048 |
| 7 | θ (cuped_theta prior_sessions); mean prior_sessions | 0.760123; 10.007625 |
| 8 | pages_cuped diff, CI, p; variance | 0.086158, [0.037392, 0.134925], 0.00053500; [6.2499, 6.1313] |
| 9 | Holm p (m = 12) below 0.05 | mobile 3.1368e-8, EMEA 0.044217; all other slots ≥ 0.055 |
| 9 | raw p below 0.05 | mobile, team (0.00553), NA (0.01024), EMEA (0.00402), new (0.00772) |
| 10 | new_page_x_mobile coef / SE / p | 0.032758 / 0.0063227 / 2.2171e-7 |
| 10 | new_page_x_emea coef / p | 0.0089454 / 0.18819 |
| 11 | scorecard Holm p: trial / pages_cuped / paid | 0.0052497 / 0.0032100 / 0.096374 |
| 11 | serial vs `--parallel 0` | byte-identical envelopes |
