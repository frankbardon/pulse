# MMM worked example — Harbor & Pine fixture

The inputs behind the worked scenario that motivated [U40 compose-sweep](../../units/U40-compose-sweep.md). Every step runs through the `pulse` CLI; the inputs and arithmetic still prepared by hand are listed under [Still outside Pulse](#still-outside-pulse).

**Source of inspiration:** the claude.ai doc *Pulse for Marketing Mix Modeling: A Worked Scenario*, <https://claude.ai/code/artifact/20642905-0a1d-4d17-acdc-2d06b8580ccd> (private to the maintainer; a Claude session reads it with the Artifact tool's `read` action or the Docs connector, never with a web fetch). The doc walks through these exact requests and quotes their outputs.

## What is here

| Path | What it is |
|---|---|
| `harbor_pine_mmm.csv` | The panel: 8 regions × 104 weeks (2024-01-01 to 2025-12-22), 832 rows, synthetic with a known ground truth |
| `schema.json` | Import schema for the panel: it adds a description to every field. An inferred import (no `--schema`) gives the same types (`f64` money, `date` week, `packed_bool` flags) and bit-identical values |
| `req/01`–`req/03` | Profile by quarter; correlation matrix, pooled and per region |
| `req/04_adstock_grid.json` | **One materialise request** for the features cohort: four `WIN_EWMA` items, one per channel, each fanned out by `for_each` (a `zip` of a label axis `d` and a `decay` axis) into the adstock columns (decays 0.1/0.3/0.5/0.7 per channel, plus 0.6/0.8/0.9 for TV), and the four budget-scenario columns as `ATTR_FORMULA` + `WIN_EWMA` items. Each scenario formula moves $1M a year pro rata off the model year's spend, `x * (1 ∓ 1000 / total(week_index >= 52 ? x : 0))`, so no factor is typed by hand. Every window takes `decay`, not `alpha` |
| `req/05_single_fit.json` | One `REG_OLS` fit, the shape the sweep body uses |
| `req/06_decay_grid.json` | **One compose `sweep`**: 4 axes × 4 decays = 256 slots labelled `tv<λ>_se<λ>_so<λ>_di<λ>`, ranked by `residual_std_err` (`asc`, top 3). Until U40 this was a script-generated file of 256 explicit slots |
| `req/07_tv_refine.json` | A 5-value TV sweep (decays 0.5–0.9) with search, social and display held at the 06 winner |
| `req/08_final_model.json` | Final fit + `ATTR_REG_RESIDUAL` + `WIN_LAG` + `post_tests`; the regression's `save` writes `models/mmm_final.pulse-model.json` |
| `req/09_holdout.json` | Train vs holdout (weeks 0–90 vs 91–103): a compose `fits` entry fits the model once on weeks 0–90; both slots score it with `ATTR_REG_PREDICT` and report `AGG_MAPE` and `AGG_RMSE` |
| `req/10_contributions.json` | Contribution, interval-bound and marginal sums plus the ratios built on them, by year and total. Each slot loads 08's saved model with `models`; contributions and their interval bounds are `ATTR_REG_PREDICT` with `terms` and `bound`, and the marginal columns read the coefficient with `coef()`. Sixteen `AGG_RATIO` aggregations per slot return ROI, its lower and upper bound, and marginal ROI per channel (`tv_roi`, `tv_roi_lo`, `tv_roi_hi`, `tv_mroi`, …), each a ratio of two per-row labels over the channel's spend |
| `req/11_budget_scenarios.json` | One shared fit, a `baseline` slot and a 3-scenario `zip` sweep that re-points each channel's `FEAT_LOG` and scores the four media `terms`; `sweep.overlays` adds one `OVERLAY_DELTA_VS_REF` per scenario |

## Recreate it in a fresh session

Work in a scratch copy, with `PULSE_DATA_DIR` pointing at it. `.pulse` files are gitignored, but keep the tree clean:

```bash
make build                                   # bin/pulse
P=$PWD/bin/pulse
SCRATCH=$(mktemp -d)
cp -r docs/roadmap/fixtures/mmm-harbor-pine "$SCRATCH/mmm" && cd "$SCRATCH/mmm" && mkdir -p out
export PULSE_DATA_DIR=$PWD

$P import csv -i harbor_pine_mmm.csv -o harbor_pine_mmm.pulse --schema schema.json
$P api process -r req/01_spend_by_quarter.json --json
$P api process -r req/02_correlation.json --json
$P api process -r req/03_within_region_corr.json --json
$P api materialize -r req/04_adstock_grid.json -o harbor_pine_features.pulse --json   # records 832, 27 derived_columns
$P api process -r req/05_single_fit.json --json
$P api predict-compose -r req/06_decay_grid.json --json        # sweep summary: expanded_count 256, the labels
$P api compose -r req/06_decay_grid.json --parallel 0 --json --no-components
$P api compose -r req/07_tv_refine.json --json --no-components
$P api process -r req/08_final_model.json --json --no-components     # writes models/mmm_final.pulse-model.json
$P api predict-compose -r req/09_holdout.json --json           # optional: lists the fit; nothing runs
$P api compose -r req/09_holdout.json --json --no-components
$P api compose -r req/10_contributions.json --json --no-components
$P api compose -r req/11_budget_scenarios.json --json --no-components
```

Step 08 writes `models/mmm_final.pulse-model.json` under the data root, and step 10 loads it, so run 08 before 10. Running 08 again replaces the file and adds a `PULSE_MODEL_OVERWRITTEN` warning. Steps 09 and 11 fit the same model inside the request (`fits`), so they do not need the file. `data.fits.mmm` in their output is the fit's full regression result, bit-identical to 08's `regressions[0]` apart from its `name`.

Step 06 returns 256 `responses` in grid order (first axis slowest) plus `data.ranking`, the top three `{label, value, rank}`. Add `--echo-request` to see the 256 expanded slots the service ran.

Step 04 writes `harbor_pine_features.pulse`, its rows sorted by `region` then `week` (the request's `sort`): the panel's 14 fields verbatim (types, dictionaries, descriptions), then 27 `f64` columns in pipeline order (the four scenario formulas, then the 23 windows as declared), each with a generated description. `pulse api predict -r req/04_adstock_grid.json --json` reports the four fan-outs under `for_each` (`expanded_count` 7, 4, 4, 4) and one `PULSE_ADVISORY_EXTRA_PASS` advisory per scenario formula (`attributes[0]`–`attributes[3]`, `added_scans` 1, `totals` 1): each `total()` adds a pre-pass over the 832 rows before any row is evaluated. No hand-written schema is involved: steps 05–11 read the derived columns as ordinary fields.

## Expected results (steps 01–08 verified 2026-10-10 on `derived-cohort` at `6890fea1` + materialize `sort`; 09–11 re-run 2026-10-10 on `fit-scoring` after `f59ed7ca`; the whole script re-run 2026-10-11 on `post-aggregation-ratios` at `ae4b67e7`, darwin/arm64)

Full-precision figures are rounded to 12 significant digits. A run on amd64, where FMA fusion can change the order of rounding, may differ in the last digits. The comparisons below are exact on any one build.

| Step | Check | Value |
|---|---|---|
| 04 materialize | records, derived columns | 832, 27 |
| 04 materialize | scenario factors (TV, display, search, social), from `total()` | 0.808655286759, 0.620599907426, 1.2031768736, 1.19172146705 |
| 06 grid | slots | 256 (predict `expanded_count` 256) |
| 06 grid | `ranking[0]`: label, `residual_std_err` | `tv70_se10_so30_di70`, 14.0043545304 |
| 06 grid | `ranking[1]`, `ranking[2]` | `tv70_se10_so50_di70` 14.0044556593; `tv70_se30_so50_di70` 14.0044696758 |
| 07 refine | TV `residual_std_err` at 0.5 … 0.9 | 14.202, 14.072, **14.004**, 14.157, 14.654 |
| 08 final | `adj_r2`, `tv_sat` β | 0.954468484143, 8.86827918744 |
| 08 final | lag-1 residual Pearson p, Shapiro p | 0.323, 0.194 |
| 08 final | `df`, `tv_sat` 95% interval (t) | 710, [7.54300629364, 10.1935520812] |
| 09 holdout | MAPE train, holdout | 0.0256712891849, 0.0218247942172 |
| 09 holdout | RMSE train, holdout ($k) | 13.8301401593, 13.4895798879 |
| 09 holdout | predicted − actual sum, train, holdout ($k) | 0.0000 (OLS with an intercept), +178.896 |
| 10 total | ROI (TV, search, social, display): `*_roi` | 1.63, 2.61, 2.58, 3.03 |
| 10 total | ROI 95% interval: `*_roi_lo`, `*_roi_hi` | TV [1.387, 1.875], search [1.092, 4.122], social [1.225, 3.945], display [−0.983, 7.044] |
| 10 total | marginal ROI: `*_mroi` | 0.60, 0.96, 0.97, 1.32 |
| 11 scenarios | revenue change, $k | TV→search +189.0, TV→social +103.3, display→social −816.2 (full: 189.016499817, 103.285731127, −816.190965679) |

**The post-aggregation-ratios acceptance.** `req/04` and `req/10` hold no hand-typed factor and leave no division to the reader. Both were run on the same build as the old files (`git show ae4b67e7:docs/roadmap/fixtures/mmm-harbor-pine/req/04_adstock_grid.json` and `.../req/10_contributions.json`), and the figures change in the ways below. Each change is intended.

- **All four factors reproduce.** The rule behind the old factors is "$1M a year moved, pro rata, off the model year's spend". The model year is `week_index` 52–103, which runs from 2024-12-30 to 2025-12-22. Its totals are TV 5226.17, display 2635.74, search 4921.82 and social 5215.90 ($k). The factors `1 − 1000/T` for TV and display, and `1 + 1000/T` for search and social, give 0.808655, 0.620600, 1.203177 and 1.191721. Rounded to five decimals, these are the typed 0.80866, 0.62060, 1.20318 and 1.19172. An earlier check had called display, search and social unreproducible. It had summed calendar 2025 (display 2580.36, search 4825.31, social 5116.09), which leaves out week 52, the week of 2024-12-30. TV spend is zero in that week, so only TV matched on that base. In the materialised cohort, every one of the 832 rows of each scenario column equals the channel's spend times its factor, compared with `==`.
- **Scenario deltas move only in the digits the rounding dropped.** The exact factors differ from the typed ones by less than 5·10⁻⁶. TV→search moves from 189.0470 to 189.0165, TV→social from 103.2985 to 103.2857, and display→social from −816.1960 to −816.1910. Each delta reads the same at its printed precision. The `baseline` slot is byte-identical, since it reads no scenario column.
- **Only steps 10 and 11 change.** No fit reads a scenario column, so the outputs of steps 01–09, the 04 materialise summary and both `predict-compose` runs are byte-identical between the old and the new `req/04`.
- **ROI is a response field.** Each `req/10` slot adds 16 `AGG_RATIO` aggregations next to the `AGG_SUM`s, which are kept. Every pre-existing `AGG_SUM` value is unchanged. Each ratio was compared with `==` against the reader's division of the two `AGG_SUM`s in the same response row (for example `tv_contrib_k / tv_spend_k`). That is 48 ratios: 16 in each of the two `by_year` rows and the `two_year_total` row. None differ. The two-year figures are TV 1.63079635128, search 2.607235054, social 2.58475122941 and display 3.03012997536.
- **Predict names the cost.** `pulse api predict -r req/04_adstock_grid.json --json` returns four `PULSE_ADVISORY_EXTRA_PASS` advisories, one per scenario formula (`added_scans` 1, `totals` 1). Advisories are not warnings: `warnings` stays empty, and the materialise still writes 832 records and 27 derived columns.

**The fit-scoring acceptance.** `req/09`–`req/11` hold no coefficient, interval bound or model restated by hand: every number they score with comes from a fit at full precision. Run on the same build as the old files (`git show f59ed7ca:docs/roadmap/fixtures/mmm-harbor-pine/req/09_holdout.json` and its siblings), the figures change in the ways below, and each change is intended.

- **Scored values match the fit exactly.** A slot carrying both `ATTR_REG_PREDICT {fit: "mmm"}` and `ATTR_REG_FITTED` over the same predictors, on the 728 training rows, compared each pair with `==`: 0 rows differ.
- **MAPE is exact.** The old step retyped the coefficients at 4 decimals into `ATTR_FORMULA` and averaged a hand-built `ape` column. Train MAPE moves from 0.0256702588 to 0.0256712892, holdout from 0.0218224028 to 0.0218247942. The train prediction sum now equals actual revenue (316186.33), as an OLS fit with an intercept must; the 4-decimal coefficients missed it by −6.44.
- **Interval bounds are t-based.** The old `req/10` pasted bounds computed as β ± 1.96·SE. The fit now reports its own `ci_lower` / `ci_upper` on the t distribution with `df` 710, so each bound moves at about the 3rd significant digit: TV lower 7.5452 → 7.54301, upper 10.1913 → 10.19355; search 5.1269 → 5.11499, 19.2871 → 19.29909; social 5.4813 → 5.47105, 17.6067 → 17.61691; display −3.1573 → −3.17921, 22.7515 → 22.77341. The two-year ROI intervals widen in the 3rd decimal (search was [1.095, 4.119], display [−0.977, 7.037]).
- **Point figures agree to the rounding they had.** Contributions, marginal sums and scenario deltas move only in the digits the 4-decimal coefficients dropped: the TV contribution total 17085.975 → 17085.935, the TV→search delta 189.0427 → 189.0470. Every ROI, marginal ROI and scenario figure in the table above reads the same at its printed precision.
- **Gap 13 is converted.** `req/11` is one `baseline` slot plus a `zip` sweep over the three scenarios; `sweep.overlays` stamps one overlay per scenario, with the same names as before (`change_tv_to_search`, …).

**The sweep acceptance.** The sweep in `req/06` and the 256 explicit slots it replaced (`git show d2e640bf:docs/roadmap/fixtures/mmm-harbor-pine/req/06_decay_grid.json`) were run on the same build. They produce the same 256 labels in the same order, and every `residual_std_err` is bit-identical; so is every other figure of each fit (the regression `name` aside: the sweep names it `mmm`, the old file named it after the slot). The ranking lists `tv70_se10_so30_di70`, `tv70_se10_so50_di70`, `tv70_se30_so50_di70`, in that order. That is the same order as an external sort of the old file's values. `req/07` matches its old 5-slot form the same way. A 448-slot variant (TV over all 7 decays, 7×4×4×4) runs under the default limits and ranks the same top 3.

**The materialise acceptance.** Step 04 used to stream the request to NDJSON (`--stream --no-project`) and import it with a hand-written 41-field `schema_features.json` (`git show 6f06b157:docs/roadmap/fixtures/mmm-harbor-pine/req/04_adstock_grid.json` and `.../schema_features.json`). Both routes keep the request's `sort` by `region` and `week`, so the two features cohorts hold their rows in the same order. Both routes were run on the same build, the two cohorts exported to NDJSON and compared per `(region, week_index)` row, every value as its float64 bit pattern: all 832 rows in the same order, the same 41 columns, and 40 of them bit-identical, every adstock and scenario column included. The exception is `week`. The old schema typed it `u16`, so it held the epoch day number; the materialised cohort carries the panel's `date` field, and its epoch day equals the old value on every row. Steps 05–11 were then run against each cohort: every `--json` output is byte-identical between the two routes, including the 06 ranking (`residual_std_err` 14.004354530446538, 14.004455659253262, 14.00446967576211).

Ground truth built into the data (for judging the estimates, not used by any request): decays TV 0.7, search 0.2, social 0.4, display 0.3; β TV 9.0, search 13.0, social 14.0, display 1.5; true ROI 1.66 / 2.78 / 3.14 / 0.46.

## Still outside Pulse

Everything else the worked example did by hand or by script. Each gap has a TODO item and an owner unit (gap numbers follow the U40 unit's [Handed on](../../units/U40-compose-sweep.md#handed-on) table).

| Gap | Still done outside Pulse | TODO | Owner |
|---|---|---|---|
| 14 | `req/07`–`req/11` name the winning decays by hand instead of referencing the ranking | #297 | [U40](../../units/U40-compose-sweep.md) follow-up |
| 15 | Decay identifiability is read by eye from the 256 values | #298 | [U40](../../units/U40-compose-sweep.md) follow-up |
| 16 | Ridge cannot leave a predictor subset unpenalised | #293 | [U36](../../units/U36-reference-oracles.md) |
| 17 | `WIN_LAG` `default: 0` biases the lag-1 test; post-tests cannot skip null | #294 | [U36](../../units/U36-reference-oracles.md) |
| 18 | OLS standard errors ignore cross-region correlation; no clustered or HAC option | #295 | [U25](../../units/U25-multivariate-tests-segmentation.md) |
| 21 | The pooled-vs-within-region cross-check tables are assembled from per-region matrices | #296 | [U28](../../units/U28-matrix-overlays.md) |
| 19 | The synthetic data generator is external; only the CSV ships | none | Out of scope |

Gaps 1 (compose slots) and 2 (ranking) are closed by U40. Gaps 1 (windows), 3, 4, 5, 6 and 20 are closed by [U41](../../units/U41-derived-cohort.md): one materialise request with `for_each` and `decay` replaces the generated window list, the hand arithmetic, the NDJSON round trip and its hand-written schema; an explicit import schema now matches columns by name, and inferred floats are `f64`. Gaps 7, 8, 9, 12 and 13 are closed by [U42](../../units/U42-fit-scoring.md): a compose `fits` entry or a saved model loaded with `models` replaces the retyped coefficients and the restated scenario model, `AGG_MAPE` replaces the hand-built error formula, `ATTR_REG_PREDICT` `bound` and `coef()` replace the pasted interval bounds, and `sweep.overlays` replaces the per-scenario overlay list. Gaps 10 and 11 are closed by [U43](../../units/U43-post-aggregation-ratios.md): `AGG_RATIO` over attribute labels returns ROI, its interval bounds and marginal ROI as response fields, and `total()` in `ATTR_FORMULA` derives each scenario factor from the model year's channel total.
