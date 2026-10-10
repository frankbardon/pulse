# Reproducing the privacy-safe sharing showcase

Every figure in [README.md](README.md) comes from a Pulse response produced by `run.sh`. Each run
regenerates the `.pulse` cohorts (they are git-ignored) and writes the responses to `out/`.

## From a fresh checkout

```bash
# 1. build the CLI
cd <repo root>
go build -o /tmp/pulse ./cmd/pulse

# 2. work on a scratch copy so nothing is written into the repo
rm -rf /tmp/pss && cp -r docs/showcase/privacy-safe-sharing /tmp/pss && cd /tmp/pss

# 3. run every step (about 25 s)
PULSE=/tmp/pulse ./run.sh
```

`run.sh` executes these commands in order, all from the example directory:

| Step | Command |
|---|---|
| 1 | `pulse import csv -i data/customers.csv -o customers.pulse --schema data/customers.schema.json --json` |
| 1 | `pulse api process -r req/01_source_overview.json --json` |
| 2 | `pulse profile create -i customers.pulse -o synth/profile.json --include-stats --include-correlations --conditional --fit-models --residual-correlations --fit-shape --suggest-rules synth/suggested_rules.json --seed 7 --json` |
| 3/4 | `pulse synth from-profile -p synth/profile.json --source customers.pulse -o tagged_norules.pulse --rows 40000 --seed 11 --fidelity-report synth/fidelity_norules.json --emit-spec synth/spec_norules.json --json` |
| 4 | `pulse synth from-profile -p synth/profile.json --source customers.pulse -o tagged.pulse --rows 40000 --seed 11 --rules synth/rules.json --fidelity-report synth/fidelity.json --emit-spec synth/spec.json --json` |
| 5 | `pulse synth from-schema -s synth/spec.json -o twin.pulse --seed 11 --json` (and `spec_norules.json` → `twin_norules.pulse`) |
| 5 | `pulse cohort inspect --json twin.pulse` |
| 5 | `pulse synth from-schema -s synth/spec.json -o twin_rerun.pulse --seed 11 --json`, then `cmp twin.pulse twin_rerun.pulse` |
| 5 | `pulse api compose -r req/02_twin_equals_scored_rows.json --json` |
| 6 | `pulse api compose -r req/03_business_rule_violations.json --json` |
| 7 | `pulse api compose -r req/04_headline_metrics.json --json`, `req/04b_headline_twin_vs_source.json`, `req/05_segments_twin_vs_source.json`, `req/06_churn_crosstab_twin_vs_source.json`, `req/07_chisq_tests.json`, `req/08_churn_logistic_model.json`, `req/09_correlation_matrix.json` |
| 8 | `pulse api compose -r req/10_exact_record_match.json --json` |
| 8 | `pulse cohort filter` ×3 (`source_half_a/b.pulse`, `twin_half_a.pulse`, split on `int(round(monthly_spend * 100)) % 2`), then `pulse api compose -r req/11_profile_match_vs_chance.json --json` |
| 9 | `pulse profile create … --conditional` (no `--fit-models`) → `synth from-profile` → `synth from-schema` → `pulse api compose -r req/08b_churn_logistic_conditional_only_twin.json --json` |

Validate any single request before running it with `pulse api predict -r req/01_source_overview.json --json`
(single requests) or `pulse api predict-compose -r req/NN_….json --json` (Compose batches).

With `--json`, a refused request still exits 0 and reports itself in the envelope's `errors`
array. Check that every response came back clean:

```bash
grep -L '"errors": \[\]' out/*.json    # prints nothing when every envelope is error-free
```

The files in `synth/` are rewritten by the run and should come back byte-identical to the
committed copies (`git diff --stat synth/` in a checkout, or `diff -r` against the repo copy).

## Expected key numbers

| Where | Figure | Expected |
|---|---|---|
| `out/00_import.json` | `data.RowsImported` | 40000 (no `RowErrors`) |
| `out/01_source_overview.json` | churn_rate / dormant_accounts / frequent_callers | 0.190825 / 1167 / 1147 |
| `synth/suggested_rules.json` | candidates | 2 (gating `round(churned) == 0` → `end_date`; dependency `frequent_caller = round(support_contacts) >= 4`) |
| `synth/profile.json` | model R² for monthly_spend / tenure_months / churned | 0.915 / 0.214 / 0.114 (rounded) |
| `out/04_synth_rules.json` | rows_generated / rows_rejected | 40000 / 0 |
| `synth/fidelity.json` | χ² p region / plan / contract | 0.7389 / 0.1365 / 0.5104 |
| `synth/fidelity.json` | KS D age / monthly_spend / tenure_months | 0.01345 / 0.025425 / 0.0555 |
| `synth/fidelity.json` | models flagged | `monthly_spend` only |
| `synth/fidelity.json` | churned Two-Year captured / recovered | −0.7678 / −0.7933 |
| run log | determinism | `twin.pulse and twin_rerun.pulse are byte-identical` |
| `out/06_twin_equals_scored_rows.json` | both slots: total_spend / total_tenure / null_end_dates | 2770586.2506… / 1109889 / 32358 |
| `out/07_violations.json` | twin_without_rules: end_before_start / active_with_end_date / churned_without_end_date / tenure_disagrees / no_products_but_spend / flag_disagrees / tv_without_broadband | 660 / 6122 / 6145 / 39732 / 1240 / 1825 / 5155 |
| `out/07_violations.json` | twin_with_rules: same order | 0 / 0 / 0 / 0 / 0 / 0 / 5113 |
| `out/08_headline.json` | twin churn_rate / avg_monthly_spend_usd / avg_tenure_months | 0.19105 / 69.2647 / 27.7472 |
| `out/08_headline_overlays.json` | index churn / spend / tenure | 100.118 / 98.017 / 102.748 |
| `out/08_segments.json` | churn by contract, source (M/1Y/2Y) | 0.33723 / 0.10572 / 0.03453 |
| `out/08_segments.json` | churn by contract, twin (M/1Y/2Y) | 0.29851 / 0.12159 / 0.08789 |
| `out/08_segments.json` | `contract_twin_index` Two-Year | 254.545 |
| `out/08_segments.json` | avg spend of tv holders, source / twin | 113.220 / 94.318 |
| `out/08_crosstab.json` | `churn_pp_gap` Basic × Monthly | −0.08794 |
| `out/08_chisq.json` | plan × churned χ², source / twin | 165.748 / 6.906 (p 1.05e−35 / 0.0749) |
| `out/08_glm.json` | pseudo_r2 source / twin | 0.19815 / 0.07709 |
| `out/08_glm.json` | tenure_months β source / twin | −0.020572 / −0.005797 |
| `out/08_corr.json` | tenure–churned r source / twin; determinant | −0.2196 / −0.1339; 0.8548 / 0.9349 |
| `out/09_exact_match.json` | control / twin matching_row_pairs | 40002 / 0 |
| `out/09_half_a.json`, `09_half_b.json`, `09_twin_half_a.json` | written_records | 20591 / 19409 / 20710 |
| `out/09_profile_match.json` | real-half vs real-half / twin-half vs real-half | 95 / 46 |
| `out/10_glm_conditional_only.json` | twin pseudo_r2 | 0.000197 |

The numbers were produced with Pulse `v0.0.0-20261010140947-7ea2edd01277`. A later build that changes
the synth samplers or the profile capture may move the synthetic figures. The source figures depend
only on `data/customers.csv`.
