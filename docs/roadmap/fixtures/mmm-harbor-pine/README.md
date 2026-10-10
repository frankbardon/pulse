# MMM worked example — Harbor & Pine fixture

The inputs behind the worked scenario that motivated [U40 compose-sweep](../../units/U40-compose-sweep.md). Everything here runs through the `pulse` CLI alone.

**Source of inspiration:** the claude.ai doc *Pulse for Marketing Mix Modeling: A Worked Scenario*, <https://claude.ai/code/artifact/20642905-0a1d-4d17-acdc-2d06b8580ccd> (private to the maintainer; a Claude session reads it with the Artifact tool's `read` action or the Docs connector, never with a web fetch). The doc walks through these exact requests and quotes their outputs.

## What is here

| Path | What it is |
|---|---|
| `harbor_pine_mmm.csv` | The panel: 8 regions × 104 weeks (2024-01-01 to 2025-12-22), 832 rows, synthetic with a known ground truth |
| `schema.json` | Import schema for the panel (`f64` money, `date` week, `packed_bool` flags, a description on every field) |
| `schema_features.json` | Import schema for the adstocked features cohort. **Keep its fields sorted by name**: the NDJSON stream emits keys in sorted order, and the import maps the schema to them in that order |
| `req/01`–`req/03` | Profile by quarter; correlation matrix, pooled and per region |
| `req/04_adstock_grid.json` | `WIN_EWMA` adstock columns (decays 0.1/0.3/0.5/0.7 per channel, plus 0.6/0.8/0.9 for TV) and the four budget-scenario columns |
| `req/05_single_fit.json` | One `REG_OLS` fit, the template every grid slot copies |
| `req/06_decay_grid.json` | **The scripted request U40 replaces**: 256 compose slots, one per decay combination (4⁴), labels `tv<λ>_se<λ>_so<λ>_di<λ>` |
| `req/07_tv_refine.json` | 5-slot TV refinement (decays 0.5–0.9) |
| `req/08_final_model.json` | Final fit + `ATTR_REG_RESIDUAL` + `WIN_LAG` + `post_tests` |
| `req/09_holdout.json` | Train vs holdout scoring (weeks 0–90 vs 91–103) |
| `req/10_contributions.json` | Contribution, ROI, ROI interval and marginal-ROI sums, by year and total |
| `req/11_budget_scenarios.json` | Four-slot compose + `OVERLAY_DELTA_VS_REF` per scenario |

## Recreate it in a fresh session

Work in a scratch copy (`.pulse` files are gitignored, but keep the tree clean):

```bash
go build -o /tmp/pulse ./cmd/pulse
cp -r docs/roadmap/fixtures/mmm-harbor-pine /tmp/mmm && cd /tmp/mmm && mkdir -p out
P=/tmp/pulse

$P import csv -i harbor_pine_mmm.csv -o harbor_pine_mmm.pulse --schema schema.json
$P api process -r req/01_spend_by_quarter.json --json
$P api process -r req/02_correlation.json --json
$P api process -r req/03_within_region_corr.json --json
$P api process -r req/04_adstock_grid.json --stream --no-project > out/04_rows.ndjson
$P import ndjson -i out/04_rows.ndjson -o harbor_pine_features.pulse --schema schema_features.json
$P api process -r req/05_single_fit.json --json
$P api compose -r req/06_decay_grid.json --parallel 0 --json --no-components
$P api compose -r req/07_tv_refine.json --json --no-components
$P api process -r req/08_final_model.json --json --no-components
$P api compose -r req/09_holdout.json --json --no-components
$P api compose -r req/10_contributions.json --json --no-components
$P api compose -r req/11_budget_scenarios.json --json --no-components
```

`--no-project` on step 04 is required: without it the stream carries only the columns the request references, and the features cohort loses revenue and the controls.

## Expected results (verified 2026-10-10 on `main` at `dd60240`)

| Step | Check | Value |
|---|---|---|
| features import | rows | 832 |
| 06 grid | slots | 256 |
| 06 grid | best label, `residual_std_err` | `tv70_se10_so30_di70`, 14.004354530446564 |
| 06 grid | 2nd, 3rd | `tv70_se10_so50_di70` 14.00445565925323; `tv70_se30_so50_di70` 14.004469675762179 |
| 07 refine | TV `residual_std_err` at 0.5 … 0.9 | 14.202, 14.072, **14.004**, 14.157, 14.654 |
| 08 final | `adj_r2`, `tv_sat` β | 0.9544684841433847, 8.86827918744402 |
| 08 final | lag-1 residual Pearson p, Shapiro p | 0.323, 0.194 |
| 09 holdout | MAPE train, holdout | 0.02567, 0.02182 |
| 10 total | ROI (TV, search, social, display) | 1.63, 2.61, 2.58, 3.03 |
| 10 total | marginal ROI | 0.60, 0.96, 0.97, 1.32 |
| 11 scenarios | revenue change, $k | TV→search +189.0, TV→social +103.3, display→social −816.2 |

Ground truth built into the data (for judging the estimates, not used by any request): decays TV 0.7, search 0.2, social 0.4, display 0.3; β TV 9.0, search 13.0, social 14.0, display 1.5; true ROI 1.66 / 2.78 / 3.14 / 0.46.

## The U40 acceptance target

Once U40 lands, `req/06_decay_grid.json` should be replaceable by one sweep over `req/05_single_fit.json`'s shape:

```json
{
  "sweep": {
    "axes": [
      {"name": "tv",      "values": [10, 30, 50, 70]},
      {"name": "search",  "values": [10, 30, 50, 70]},
      {"name": "social",  "values": [10, 30, 50, 70]},
      {"name": "display", "values": [10, 30, 50, 70]}
    ],
    "label": "tv{{tv}}_se{{search}}_so{{social}}_di{{display}}",
    "request": {
      "cohort": {"filename": "harbor_pine_features.pulse", "data_dir": "."},
      "features": [
        {"type": "FEAT_ONE_HOT", "field": "region", "label": "rg"},
        {"type": "FEAT_LOG", "field": "tv_ad{{tv}}", "label": "tv_sat"},
        {"type": "FEAT_LOG", "field": "search_ad{{search}}", "label": "search_sat"},
        {"type": "FEAT_LOG", "field": "social_ad{{social}}", "label": "social_sat"},
        {"type": "FEAT_LOG", "field": "display_ad{{display}}", "label": "display_sat"}
      ],
      "filterers": [{"type": "FILTER_RANGE", "field": "week_index", "values": ["0", "90"]}],
      "regressions": [{"type": "REG_OLS", "name": "mmm", "target": "revenue_k",
        "predictors": ["tv_sat", "search_sat", "social_sat", "display_sat", "email_ksends", "discount_pct",
          "holiday", "competitor_sov", "category_demand", "week_index", "rg_California", "rg_Mid-Atlantic",
          "rg_Mountain", "rg_Northeast", "rg_Pacific_NW", "rg_South_Central", "rg_Southeast"]}]
    },
    "rank": {"by": "regressions.mmm.residual_std_err", "order": "asc", "top": 3}
  }
}
```

It must produce the same 256 labels with bit-identical `residual_std_err` values as `req/06_decay_grid.json` (slot `name` aside), and its ranking must list the three rows above in that order. The sweep shape is the unit's proposal; the final wire form is settled in U40.
