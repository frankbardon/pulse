# Crosstab examples

Runnable `Request.Crosstab` shapes against the shared `experiment.pulse`
A/B-test cohort. Every example resolves its cohort from `.data/` so the
JSON runs unmodified after the one-time fixture build.

## One-time setup

```sh
make build                       # builds bin/pulse
./examples/fixtures/build.sh     # imports CSVs -> .data/*.pulse
```

## Run them all

```sh
./examples/crosstab/run-all.sh
```

Prints one line per example with the resolved shape and axis sizes.

## Run one

```sh
bin/pulse api process --request examples/crosstab/04_mean_revenue_arpu.json --json
```

Add `--predict` to see the streamability verdict, applied defaults, and
warnings without executing.

```sh
bin/pulse api predict --request examples/crosstab/05_median_revenue_recompute.json --json
```

## What's in here

| # | File | Demonstrates |
|---|---|---|
| 01 | `count_with_column_normalize` | count cells + column normalization + all margins (canonical entry) |
| 02 | `row_normalize_proportions` | row% — conversion rate per region |
| 03 | `total_normalize_joint` | total% — joint P(segment, converted) table |
| 04 | `mean_revenue_arpu` | numeric cell via AGG_AVERAGE (mean-reducible) |
| 05 | `median_revenue_recompute` | recompute-class margin (AGG_MEDIAN) |
| 06 | `binning_grouper_axis` | GROUP_RANGE on the row axis (continuous variable) |
| 07 | `nested_row_axes` | nested rows: (region, segment) tuples |
| 08 | `long_shape_margins` | shape=long, margin rows tagged with `_margin` |
| 09 | `with_chi_square_inference` | count crosstab + TEST_CHISQ |
| 10 | `with_fisher_exact_small_sample` | 2×2 + TEST_FISHER_EXACT |
| 11 | `date_grouper_axis` | GROUP_DATE month bucket on row axis |
| 12 | `means_with_anova_inference` | means + TEST_ANOVA_F |
| 13 | `means_with_mann_whitney_robust` | means + TEST_MANN_WHITNEY_U (nonparametric) |

For a deeper walkthrough — recipes, the margins-are-reaggregations
contract, and the cell-aggregator → statistical-test cheat sheet — see
`skills/crosstab-guide.md`.
