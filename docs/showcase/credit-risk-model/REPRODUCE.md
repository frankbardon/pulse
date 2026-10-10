# Reproducing the credit-risk showcase

Everything runs through the `pulse` CLI. The cohort is regenerated from its synth spec with a fixed seed. `synth from-schema` is deterministic, so the same spec and seed give a byte-identical `loans.pulse`.

## 1. Build Pulse (from a fresh checkout)

```sh
git clone https://github.com/frankbardon/pulse.git && cd pulse
go build -o /tmp/pulse ./cmd/pulse
```

## 2. Copy the example to a scratch location

```sh
rm -rf /tmp/credit-risk && cp -r docs/showcase/credit-risk-model /tmp/credit-risk
cd /tmp/credit-risk
```

## 3. Run every step

```sh
PULSE=/tmp/pulse ./run.sh
```

`run.sh` does the following, in order:

```sh
pulse synth from-schema --spec data/loans.synth.json --output loans.pulse --seed 20240630
pulse cohort inspect loans.pulse                                      # -> results/00_inspect.txt
pulse api predict --request req/07_target_encode_predict.json --json  # -> results/07_target_encode_predict.predict.json
pulse api process --request req/<NN>.json --json                      # -> results/<NN>.json, for each of:
    01_profile 02a_emp_length_missing 02b_emp_length_present 03_rate_by_band 04_rate_by_purpose
    05_leak_check 06_features_split 06b_dti_buckets 07_target_encode_predict
    08_leaky_vs_clean_fit 08b_in_sample_fitted_ks 09_stepwise_bic
    10_purpose_rates_train_only 10b_train_default_rate 11_stepwise_bic_with_safe_te
    12_final_model_bootstrap 13_holdout_deciles 13b_holdout_deciles_leaky 14_holdout_ks_and_calibration
pulse explain --request req/14_holdout_ks_and_calibration.json                       # -> results/15_explain_request.txt
pulse explain --request req/14_... --response results/14_holdout_ks_and_calibration.json  # -> results/15_explain_response.txt
```

Wall time is about 3 minutes on a laptop-class machine. Two steps account for most of it: step 12 (1,000 bootstrap refits of a 14-parameter logistic model on 42,000 rows, ~2.5 min) and the two stepwise searches, steps 9 and 11 (~15–20 s each). Every other step takes under a second.

To run one step by hand:

```sh
/tmp/pulse api predict -r req/13_holdout_deciles.json --json   # validate without reading records
/tmp/pulse api process -r req/13_holdout_deciles.json --json
```

The committed `results/` directory holds the responses from the reference run. A rerun should match them, apart from timing fields (none are emitted here).

## Requests that embed earlier results

Some requests carry numbers transcribed from earlier responses, so that Pulse can evaluate them row by row:

| Request | Embeds | Taken from |
|---|---|---|
| `11`, `12`, `13`, `14` | `purpose_te_train` formula: per-purpose training `train_defaults` / `train_n`, and the training default rate `0.11935714285714286` | `results/10_purpose_rates_train_only.json`, `results/10b_train_default_rate.json` |
| `12` | the 13 predictors in `selected_features` | `results/11_stepwise_bic_with_safe_te.json` |
| `13`, `14` | `pd_score` coefficients | `results/12_final_model_bootstrap.json`, regression `pd_final` |
| `13b`, `14` | `pd_leaky` coefficients | `results/08_leaky_vs_clean_fit.json`, regression `with_fico_at_month_12` |
| `14` | `pd_all22` coefficients | `results/08_leaky_vs_clean_fit.json`, regression `clean_all_candidates` |

Because the cohort is deterministic, the committed requests stay valid as-is. If you change the synth spec or seed, these numbers must be re-transcribed from the new responses.

## Expected key numbers

| Step | Response file → path | Expected |
|---|---|---|
| synth | stdout | `Generated 60000 rows -> loans.pulse (rejected 5289)` |
| 1 | `01_profile.json` → `data.data[0]` | `applications` 60000, `defaults` 7162, `default_rate` 0.11936666…, `total_principal_usd` `"724730103.82"`, `mean_principal_usd` `"12078.8351"`, `median_fico` 694, `null_emp_length` 3603, `null_revol_util` 1798 |
| 1 | `01_profile.json` → `components` `mean_dti_pct.operator.sum` | 1082182.2600000112 |
| 2 | `02a` / `02b` → `default_rate` | 0.16985845… (3603 rows) / 0.11614092… (56397 rows) |
| 3 | `03_rate_by_band.json` | A 0.027623… (7204) … G 0.296603… (2532) |
| 5 | `05_leak_check.json` | defaulted: `mean_fico_at_month_12` 607.33…, `collections_rate` 0.848506…; performing: 704.04…, 0.009671… |
| 6 | `06_features_split.json` | split 0: 42000 loans, rate 0.119357…, `"508110479.20"`; split 1: 18000, 0.119388…, `"216619624.62"` |
| 7 | `07_target_encode_predict.predict.json` → `warnings[0].code` | `PULSE_FEAT_TARGET_LEAKAGE_RISK` |
| 8 | `08_leaky_vs_clean_fit.json` → `pseudo_r2` | clean 0.10546631…, with `fico_at_month_12` 0.60103287… |
| 8b | `08b_in_sample_fitted_ks.json` → `tests[].statistic` | clean 0.33133877…, leaky 0.76878632… |
| 9 | `09_stepwise_bic.json` → `selected_features` | 12 predictors, `pseudo_r2` 0.10356824… |
| 10b | `10b_train_default_rate.json` | `train_defaults` 5013 of `train_n` 42000 |
| 11 | `11_stepwise_bic_with_safe_te.json` | 13 predictors incl. `purpose_te_train`, `pseudo_r2` 0.10903768… |
| 12 | `12_final_model_bootstrap.json` → `pd_final.coefficients` | `lti` 2.0806711…, `dti` 0.0324995…, `purpose_te_train` 9.8092279…; bootstrap SE `lti` 0.1455… |
| 13 | `13_holdout_deciles.json` | D10 `observed_rate` 0.33888…, D1 0.012777…; `cum_defaults` 610, 978, …, 2149 |
| 13b | `13b_holdout_deciles_leaky.json` | D10 `observed_rate` 0.86111…, `mean_true_pd` 0.21028… |
| 14 | `14_holdout_ks_and_calibration.json` | `mean_predicted_pd` 0.11864497…, `observed_default_rate` 0.11938888…; KS: final 0.36543704…, all-22 0.35628477…, true-PD 0.36846524…, leaky 0.76695952… |

Reference run (this commit's build), from a fresh copy of the directory: every value above matched.

## Notes

- `loans.pulse` (≈ 6.6 MB) is not committed (`*.pulse` is gitignored); `run.sh` regenerates it.
- `gt_true_pd` and `gt_uniform_draw` are simulation ground-truth columns. They are used only for evaluation in steps 13–14, never as model inputs.
