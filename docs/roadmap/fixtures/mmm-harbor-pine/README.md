# MMM worked example — Harbor & Pine fixture

The inputs behind the worked scenario that motivated [U40 compose-sweep](../../units/U40-compose-sweep.md). Every step runs through the `pulse` CLI; the inputs and arithmetic still prepared by hand are listed under [Still outside Pulse](#still-outside-pulse).

**Source of inspiration:** the claude.ai doc *Pulse for Marketing Mix Modeling: A Worked Scenario*, <https://claude.ai/code/artifact/20642905-0a1d-4d17-acdc-2d06b8580ccd> (private to the maintainer; a Claude session reads it with the Artifact tool's `read` action or the Docs connector, never with a web fetch). The doc walks through these exact requests and quotes their outputs.

## What is here

| Path | What it is |
|---|---|
| `harbor_pine_mmm.csv` | The panel: 8 regions × 104 weeks (2024-01-01 to 2025-12-22), 832 rows, synthetic with a known ground truth |
| `schema.json` | Import schema for the panel (`f64` money, `date` week, `packed_bool` flags, a description on every field) |
| `schema_features.json` | Import schema for the adstocked features cohort. **Keep its fields sorted by name**: the NDJSON stream emits keys in sorted order, and the import maps the schema to them in that order |
| `req/01`–`req/03` | Profile by quarter; correlation matrix, pooled and per region |
| `req/04_adstock_grid.json` | `WIN_EWMA` adstock columns (decays 0.1/0.3/0.5/0.7 per channel, plus 0.6/0.8/0.9 for TV) and the four budget-scenario columns |
| `req/05_single_fit.json` | One `REG_OLS` fit, the shape the sweep body uses |
| `req/06_decay_grid.json` | **One compose `sweep`**: 4 axes × 4 decays = 256 slots labelled `tv<λ>_se<λ>_so<λ>_di<λ>`, ranked by `residual_std_err` (`asc`, top 3). Until U40 this was a script-generated file of 256 explicit slots |
| `req/07_tv_refine.json` | A 5-value TV sweep (decays 0.5–0.9) with search, social and display held at the 06 winner |
| `req/08_final_model.json` | Final fit + `ATTR_REG_RESIDUAL` + `WIN_LAG` + `post_tests` |
| `req/09_holdout.json` | Train vs holdout scoring (weeks 0–90 vs 91–103) |
| `req/10_contributions.json` | Contribution, ROI, ROI interval and marginal-ROI sums, by year and total |
| `req/11_budget_scenarios.json` | Four-slot compose + `OVERLAY_DELTA_VS_REF` per scenario |

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
$P api process -r req/04_adstock_grid.json --stream --no-project > out/04_rows.ndjson
$P import ndjson -i out/04_rows.ndjson -o harbor_pine_features.pulse --schema schema_features.json
$P api process -r req/05_single_fit.json --json
$P api predict-compose -r req/06_decay_grid.json --json        # sweep summary: expanded_count 256, the labels
$P api compose -r req/06_decay_grid.json --parallel 0 --json --no-components
$P api compose -r req/07_tv_refine.json --json --no-components
$P api process -r req/08_final_model.json --json --no-components
$P api compose -r req/09_holdout.json --json --no-components
$P api compose -r req/10_contributions.json --json --no-components
$P api compose -r req/11_budget_scenarios.json --json --no-components
```

Step 06 returns 256 `responses` in grid order (first axis slowest) plus `data.ranking`, the top three `{label, value, rank}`. Add `--echo-request` to see the 256 expanded slots the service ran.

`--no-project` on step 04 is required: without it the stream carries only the columns the request references, and the features cohort loses revenue and the controls.

## Expected results (verified 2026-10-10 on `compose-sweep` at `d2e640bf` + this conversion, darwin/arm64)

Full-precision figures are rounded to 12 significant digits. A run on amd64, where FMA fusion can change the order of rounding, may differ in the last digits. The comparisons below are exact on any one build.

| Step | Check | Value |
|---|---|---|
| features import | rows | 832 |
| 06 grid | slots | 256 (predict `expanded_count` 256) |
| 06 grid | `ranking[0]`: label, `residual_std_err` | `tv70_se10_so30_di70`, 14.0043545304 |
| 06 grid | `ranking[1]`, `ranking[2]` | `tv70_se10_so50_di70` 14.0044556593; `tv70_se30_so50_di70` 14.0044696758 |
| 07 refine | TV `residual_std_err` at 0.5 … 0.9 | 14.202, 14.072, **14.004**, 14.157, 14.654 |
| 08 final | `adj_r2`, `tv_sat` β | 0.954468484143, 8.86827918744 |
| 08 final | lag-1 residual Pearson p, Shapiro p | 0.323, 0.194 |
| 09 holdout | MAPE train, holdout | 0.02567, 0.02182 |
| 10 total | ROI (TV, search, social, display) | 1.63, 2.61, 2.58, 3.03 |
| 10 total | marginal ROI | 0.60, 0.96, 0.97, 1.32 |
| 11 scenarios | revenue change, $k | TV→search +189.0, TV→social +103.3, display→social −816.2 |

**The sweep acceptance.** The sweep in `req/06` and the 256 explicit slots it replaced (`git show d2e640bf:docs/roadmap/fixtures/mmm-harbor-pine/req/06_decay_grid.json`) were run on the same build. They produce the same 256 labels in the same order, and every `residual_std_err` is bit-identical; so is every other figure of each fit (the regression `name` aside: the sweep names it `mmm`, the old file named it after the slot). The ranking lists `tv70_se10_so30_di70`, `tv70_se10_so50_di70`, `tv70_se30_so50_di70`, in that order. That is the same order as an external sort of the old file's values. `req/07` matches its old 5-slot form the same way. A 448-slot variant (TV over all 7 decays, 7×4×4×4) runs under the default limits and ranks the same top 3.

Ground truth built into the data (for judging the estimates, not used by any request): decays TV 0.7, search 0.2, social 0.4, display 0.3; β TV 9.0, search 13.0, social 14.0, display 1.5; true ROI 1.66 / 2.78 / 3.14 / 0.46.

## Still outside Pulse

Everything else the worked example did by hand or by script. Each gap has a TODO item and an owner unit (gap numbers follow the U40 unit's [Handed on](../../units/U40-compose-sweep.md#handed-on) table).

| Gap | Still done outside Pulse | TODO | Owner |
|---|---|---|---|
| 1 (windows) | The 23-entry `WIN_EWMA` list in `req/04` is generated by a script | #283 | [U41](../../units/U41-derived-cohort.md) |
| 3 | `WIN_EWMA` takes `alpha`; the labels carry the decay, so `alpha = 1 - decay` is hand arithmetic | #284 | [U41](../../units/U41-derived-cohort.md) |
| 4 | Derived columns reach a cohort only via `--stream` to NDJSON plus a hand-written `schema_features.json` | #280 | [U41](../../units/U41-derived-cohort.md) |
| 5 | NDJSON import maps the schema to keys by position, hence the sorted-fields rule above | #279 | [U41](../../units/U41-derived-cohort.md) |
| 6 | Streaming with projection into an import silently drops columns (why `--no-project` is required) | #281 | [U41](../../units/U41-derived-cohort.md) |
| 20 | Inferred `f32` drifts on money sums, so `schema.json` is hand-written | #282 | [U41](../../units/U41-derived-cohort.md) |
| 7, 12 | Fit coefficients are retyped into `ATTR_FORMULA` strings in `req/09`–`req/11`, and every scenario slot restates the model | #286 | [U42](../../units/U42-fit-scoring.md) |
| 8 | Holdout MAPE is a hand-expanded formula; there is no error aggregator | #287 | [U42](../../units/U42-fit-scoring.md) |
| 9 | Coefficient interval bounds in `req/10` are computed outside and pasted in | #288 | [U42](../../units/U42-fit-scoring.md) |
| 10 | ROI and marginal ROI are divided by the reader | #290 | [U43](../../units/U43-post-aggregation-ratios.md) |
| 11 | Scenario scale factors in `req/04` are computed by hand from channel totals | #291 | [U43](../../units/U43-post-aggregation-ratios.md) |
| 14 | `req/07`–`req/11` name the winning decays by hand instead of referencing the ranking | #297 | [U40](../../units/U40-compose-sweep.md) follow-up |
| 15 | Decay identifiability is read by eye from the 256 values | #298 | [U40](../../units/U40-compose-sweep.md) follow-up |
| 16 | Ridge cannot leave a predictor subset unpenalised | #293 | [U36](../../units/U36-reference-oracles.md) |
| 17 | `WIN_LAG` `default: 0` biases the lag-1 test; post-tests cannot skip null | #294 | [U36](../../units/U36-reference-oracles.md) |
| 18 | OLS standard errors ignore cross-region correlation; no clustered or HAC option | #295 | [U25](../../units/U25-multivariate-tests-segmentation.md) |
| 21 | The pooled-vs-within-region cross-check tables are assembled from per-region matrices | #296 | [U28](../../units/U28-matrix-overlays.md) |
| 19 | The synthetic data generator is external; only the CSV ships | none | Out of scope |

Gaps 1 (compose slots) and 2 (ranking) are closed by U40. Gap 13 (one overlay per scenario slot) can be written with `sweep.overlays`, but `req/11` keeps explicit slots until U42 removes the restated model (gap 12).
