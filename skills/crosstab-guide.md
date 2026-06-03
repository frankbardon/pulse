---
name: crosstab-guide
description: Build cross-tabulations with the Crosstab request section — rows × columns groupers, cell aggregation, margins-are-reaggregations, normalize directions, streamability tradeoff, matrix vs long shape. Pair with TEST_CHISQ / TEST_FISHER_EXACT for inference on count crosstabs.
type: guide
applies_to: process, compose, predict
---

# Crosstab guide

A crosstab pivots one cell aggregation across a row-axis × column-axis grid. Pulse exposes it as an optional top-level `crosstab` section on `Request`. The engine composes the existing grouper + aggregator machinery for cell computation, then reshapes + computes margins + normalizes on top.

A crosstab is NOT a new `AGG_*` operator. Margins and normalization are cross-cell operations; baking them into an aggregator breaks streamability and the per-cell aggregator contract. The crosstab section is the right surface.

## Minimal example

```json
{
  "cohort": {"filename": "sales.pulse"},
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"}
  }
}
```

Default shape is `matrix` and default normalize is `none`. The response lands at `Response.Crosstab.Matrix` with `RowKeys`, `ColumnKeys`, and a 2-D `Cells` array.

## Axes

`rows` and `columns` are each `[]Group` — every existing grouper works on either axis:

- `GROUP_CATEGORY` — categorical fields
- `GROUP_RANGE` / `GROUP_ROUNDED` — numeric binning (so `age × segment` works without any new operator)
- `GROUP_QUANTILE` — percentile bins
- `GROUP_DATE` — day / week / month / year / quarter

Multiple groupers per axis produce nested headers. `rows: [{field: region}, {field: tier}]` gives one row per `(region, tier)` tuple, materialised in axis order.

## The cell

`cell` is a single `Aggregation`. Reuses every registered `AGG_*` operator: a `cell` of `AGG_AVERAGE` of `revenue` by `region × segment` produces a per-cell mean with no extra cell code.

## Margins are re-aggregations, NOT sums of cells

This is the most important correctness invariant.

A margin for `AGG_AVERAGE`, `AGG_MEDIAN`, `AGG_STDDEV`, `AGG_PERCENTILE`, `AGG_MODE` etc. is the aggregation **of the raw rows for that margin**, NOT the aggregation of cell values.

```
row margin (region=North) for AGG_AVERAGE(revenue)
  = mean of every revenue value where region=North
NOT
  = mean of the per-segment cell means
```

The two are equal only for true sums (`AGG_COUNT`, `AGG_SUM`). For anything order- or distribution-dependent the cell-derived margin is wrong.

The manifest's `crosstab.summable_aggregators`, `mean_reducible_aggregators`, and `recompute_aggregators` lists classify every registered aggregator. v1 always recomputes margins from raw rows (see `crosstab.recompute_aggregators`), so `AGG_MEDIAN` margins are statistically correct regardless of the cell aggregator. Future optimization may shortcut `summable` aggregators by summing cells; the classification is exposed today so callers can reason about it.

## Normalize directions

- `none` (default) — raw cell values.
- `row` — divide each cell by its row margin. Cells in a row sum to 1.
- `column` — divide each cell by its column margin. Cells in a column sum to 1.
- `total` — divide each cell by the grand total. Whole table sums to 1.

A normalize direction implies the corresponding margin computation even when `margins.{rows,columns,grand}` is false for display. The displayed margin vector still respects the `margins` flag — normalization can depend on a margin the caller chose not to render.

Divide-by-zero policy: when the required margin is zero (empty row, empty column, empty table), the cell is dropped (`MatrixCell.Present=false`). Downstream renderers must show null/empty, not zero.

## Shape

- `matrix` (default) — explicit structured payload on `Response.Crosstab.Matrix`. Carries `RowHeader`, `ColumnHeader`, ordered `RowKeys` / `ColumnKeys` tuples, the dense `Cells` matrix, optional `RowMargins` / `ColumnMargins` / `GrandTotal`, the `CellLabel`, and `NormalizeApplied`. Designed for downstream renderers (Prism heatmap) without re-deriving axis structure.
- `long` — one tuple row per `(row-key, column-key)` cell on `Response.Data`. Each row has every axis field set plus the cell label. Margin rows (when requested) are flagged via the `_margin` field set to `"row"`, `"column"`, or `"grand"` so they are visually distinct from data rows.

Matrix ↔ long round-trips lose no cells. Axis ordering is deterministic across runs (sorted composite-key order: categorical and numeric-bin keys natural-sorted; date keys chronological — never first-seen / hash order).

## Streamability tradeoff

A crosstab is **inherently buffered** in all of these cases:

- `shape: matrix` (cannot pivot before all columns are seen),
- any `margins` flag set,
- any non-`none` `normalize`,
- nested axes (multi-grouper rows or columns).

The single streamable case: `shape: long` + every `margins` flag false + `normalize: none`. That degenerates into a plain grouped aggregation and could pass through the streaming path — but v1 still routes through the crosstab orchestrator because the standard process path does not yet support multi-grouper composite keys for nested axes.

`pulse predict` reports the exact buffered reasons via `StreamableReasons`. Run it before an expensive crosstab so the buffered cost is intentional, not accidental.

### Buffered-set field projection (automatic)

Because the crosstab path materializes the filter-passing record set, memory cost on wide cohorts (hundreds of fields) was historically catastrophic — every buffered record carried every schema field even when only a handful were referenced. The orchestrator now projects each buffered record to only the fields actually referenced by the request: rows-axis groupers, columns-axis groupers, the cell aggregation (including `weight_field` / `numerator_field` / `denominator_field` on `AGG_WEIGHTED_MEAN` and `AGG_RATIO`), filterers, label bindings, tier-1 tests, regressions, features, and attribute source fields. Per-record memory drops from `O(|schema|)` to `O(|referenced|)`.

The projection is automatic and silent — there is no flag to set and the behavior is unchanged for narrow cohorts. Requests carrying a `FILTER_EXPRESSION` whose expression fails to parse, an extension operator without a registered `FieldInputs` hook, or any construct whose field set the walker cannot prove complete fall back transparently to the full-decode path. Output bytes are byte-equal across the two paths.

## Recipes for common cross-tabulations

Use these as starting points; every recipe is a runnable shape.

### Frequency cross-tab (raw counts)

The classic contingency-table view. Cell aggregator is `AGG_COUNT`, margins on.

```json
{
  "cohort": {"filename": "trial.pulse"},
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "outcome"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true, "columns": true, "grand": true}
  }
}
```

Runnable equivalent in this repo: `examples/crosstab/01_count_with_column_normalize.json`.

### Conversion rate per row (row %)

`normalize: row` divides each cell by its row margin. The (region, converted=yes) cell becomes the conversion rate within that region. Each row sums to 1.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "rate"},
    "margins": {"rows": true},
    "normalize": "row"
  }
}
```

Runnable: `examples/crosstab/02_row_normalize_proportions.json`.

### Column proportions (column %)

`normalize: column` — answers "what share of the column belongs to each row?" Each column sums to 1.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "share"},
    "margins": {"columns": true},
    "normalize": "column"
  }
}
```

### Joint distribution (total %)

`normalize: total` divides each cell by the grand total. The whole table sums to 1; each cell is the joint probability P(row=r, col=c).

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "p"},
    "margins": {"grand": true},
    "normalize": "total"
  }
}
```

Runnable: `examples/crosstab/03_total_normalize_joint.json`.

### Mean of a numeric cell (ARPU table)

Cell aggregator is `AGG_AVERAGE`. The (region, treatment) cell is the mean revenue under that combination. **Row, column, and grand margins are recomputed from raw rows** — they are NOT averages of the cell means. Order matters: a region-margin mean over (n_A=10 revenue=100, n_B=1000 revenue=50) is closer to 50, not 75.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "cell":    {"type": "AGG_AVERAGE", "field": "revenue", "label": "arpu"},
    "margins": {"rows": true, "columns": true, "grand": true}
  }
}
```

Runnable: `examples/crosstab/04_mean_revenue_arpu.json`.

### Median per cell (recompute classification)

`AGG_MEDIAN` is `recompute`-class — its margin cannot be derived from cell values. Pulse always recomputes it from raw rows so the row margin for region=R is the median of every revenue in region=R, NOT the median of cell medians.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_MEDIAN", "field": "revenue", "label": "median_revenue"},
    "margins": {"rows": true, "columns": true, "grand": true}
  }
}
```

Runnable: `examples/crosstab/05_median_revenue_recompute.json`.

### Binning a continuous variable on an axis

`GROUP_RANGE` on the row axis turns a continuous variable into ranges that index the rows. `GROUP_ROUNDED` and `GROUP_QUANTILE` work the same way; `GROUP_DATE` slices a date column by day / week / month / quarter / year.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_RANGE", "field": "revenue_before", "interval": 100}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true}
  }
}
```

Runnable: `examples/crosstab/06_binning_grouper_axis.json`.

### Nested axis headers (multi-grouper rows)

Multiple groupers on `rows` produce nested row tuples. The row header lists every grouper field in order; each `RowKey` tuple carries that many components.

```json
{
  "crosstab": {
    "rows": [
      {"type": "GROUP_CATEGORY", "field": "region"},
      {"type": "GROUP_CATEGORY", "field": "segment"}
    ],
    "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true},
    "normalize": "row"
  }
}
```

Runnable: `examples/crosstab/07_nested_row_axes.json`. The same pattern works on `columns`.

### Time-series crosstab via GROUP_DATE

Row axis is a monthly bucket; columns are the categorical breakdown. Row-normalize to see the mix at each point in time.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_DATE", "field": "period", "params": {"unit": "month"}}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true},
    "normalize": "row"
  }
}
```

Runnable: `examples/crosstab/11_date_grouper_axis.json`.

### Long-form output for downstream pipelines

`shape: long` emits one row per cell on `Response.Data` instead of a matrix on `Response.Crosstab`. Margin rows are tagged via the `_margin` field (`"row"`, `"column"`, `"grand"`).

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true, "columns": true, "grand": true},
    "shape":   "long"
  }
}
```

Runnable: `examples/crosstab/08_long_shape_margins.json`.

## Statistical testing the crosstab result

A crosstab is a visualization. The inferential question — "is what I see statistically significant?" — is the job of Pulse's existing statistical tests. The two surfaces compose in a single `Request`: keep the `crosstab` section, add a `tests` (tier-1) or `post_tests` (tier-2) slot. The conflict guard (`PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS`) only fires for top-level `groups` + `aggregations`; `tests` are free to ride along.

### Independence on a count crosstab — chi-square

For "is row × column relationship meaningful?" on a count crosstab, pair `TEST_CHISQ`. Same `rows` and `cols` field names as the crosstab's row/column axis. Streamable.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true, "columns": true, "grand": true}
  },
  "tests": [
    {"type": "TEST_CHISQ", "rows": "segment", "cols": "converted", "alpha": 0.05}
  ]
}
```

Runnable: `examples/crosstab/09_with_chi_square_inference.json`. The test's response carries the χ² statistic, degrees of freedom, p-value, and per-cell expected counts.

`TEST_CHISQ` warns (`PULSE_TEST_EXPECTED_COUNT_TOO_LOW`) when any expected cell count drops below 5 — that is your signal to switch to Fisher's exact.

### Small-sample 2×2 — Fisher's exact

Replace `TEST_CHISQ` with `TEST_FISHER_EXACT` for 2×2 tables when expected counts are small. The v1 implementation supports only 2×2; larger contingency tables stay on chi-square.

```json
{
  "filterers": [{"type": "FILTER_INCLUDE", "field": "region", "values": ["east"]}],
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true, "columns": true, "grand": true}
  },
  "tests": [
    {"type": "TEST_FISHER_EXACT", "rows": "treatment", "cols": "converted", "alpha": 0.05}
  ]
}
```

Runnable: `examples/crosstab/10_with_fisher_exact_small_sample.json`.

### Comparing means across cell groups — t-test / ANOVA

When the cell is `AGG_AVERAGE`, the inferential question becomes "do these means differ significantly?" Pulse's tier-1 tests answer that directly off the raw row stream, in parallel with the crosstab:

- **Two groups → `TEST_T` / `TEST_WELCH`.** Set `field` to the numeric column and `split_by` to the categorical split. Streamable.
- **k groups (k ≥ 2) → `TEST_ANOVA_F`** (homoscedastic) or **`TEST_ANOVA_WELCH`** (unequal variances). Streamable.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "cell":    {"type": "AGG_AVERAGE", "field": "session_minutes", "label": "mean_session"},
    "margins": {"rows": true, "columns": true, "grand": true}
  },
  "tests": [
    {"type": "TEST_ANOVA_F", "field": "session_minutes", "split_by": "region", "alpha": 0.05}
  ]
}
```

Runnable: `examples/crosstab/12_means_with_anova_inference.json`. Use `post_tests` with `TEST_TUKEY_HSD` for pairwise post-hoc on a significant ANOVA.

### Nonparametric alternative — Mann-Whitney / Kruskal-Wallis

When the cell numeric is heavy-tailed (revenue, durations) and the t-test's normality assumption is suspect, swap in the rank-based alternative:

- **Two groups → `TEST_MANN_WHITNEY_U`** (replaces `TEST_T`).
- **k groups → `TEST_KRUSKAL_WALLIS`** (replaces `TEST_ANOVA_F`).

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "cell":    {"type": "AGG_AVERAGE", "field": "revenue", "label": "mean_revenue"},
    "margins": {"columns": true, "grand": true}
  },
  "tests": [
    {"type": "TEST_MANN_WHITNEY_U", "field": "revenue", "split_by": "treatment", "alpha": 0.05}
  ]
}
```

Runnable: `examples/crosstab/13_means_with_mann_whitney_robust.json`. Both nonparametric tests are buffered (rank-based); predict will flag the request as non-streamable.

### Two-proportion z-test on a normalized crosstab

When the cell is a proportion (row- or column-normalized count crosstab on a binary outcome), `TEST_PROP_Z` tests whether two groups' success rates differ. The test reads raw rows, so it pairs cleanly with the visualization:

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "rate"},
    "margins": {"rows": true},
    "normalize": "row"
  },
  "tests": [
    {"type": "TEST_PROP_Z", "field": "converted", "split_by": "treatment",
     "params": "{\"success\": \"yes\"}"}
  ]
}
```

### Test-picking cheat sheet

| Cell aggregator | Inferential question | Tier-1 test | Notes |
|---|---|---|---|
| `AGG_COUNT` | Row × column independence | `TEST_CHISQ` | Streaming-friendly. Warns when expected < 5. |
| `AGG_COUNT` (2×2, small n) | Row × column independence | `TEST_FISHER_EXACT` | Buffered. 2×2 only in v1. |
| `AGG_COUNT` on a binary outcome | Group A rate = Group B rate | `TEST_PROP_Z` | Streamable. Needs `params.success`. |
| `AGG_AVERAGE` (2 groups, normal) | Means differ | `TEST_T` / `TEST_WELCH` | Streamable. |
| `AGG_AVERAGE` (k groups, normal) | All means equal | `TEST_ANOVA_F` / `TEST_ANOVA_WELCH` | Streamable. Pair with `TEST_TUKEY_HSD` post-test. |
| `AGG_AVERAGE` (heavy tails, 2) | Distributions differ | `TEST_MANN_WHITNEY_U` | Buffered, rank-based. |
| `AGG_AVERAGE` (heavy tails, k) | Distributions differ | `TEST_KRUSKAL_WALLIS` | Buffered. |
| Any | Series trend over time | `TEST_TREND` (tier-2) | Run as `post_tests` over windowed cells. |

The full list lives at `skills/statistical-testing.md`.

## Conflicts and rejections

The crosstab section is mutually exclusive with top-level `groups` + `aggregations` on the same Request. Predict surfaces `PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS` when both are present. Either remove the top-level slots or split into two Compose requests.

Other rejections:

- `PULSE_CROSSTAB_EMPTY_ROWS` — `rows` is empty. Use a plain grouped Process request when only one axis is needed.
- `PULSE_CROSSTAB_EMPTY_COLUMNS` — `columns` is empty.
- `PULSE_CROSSTAB_MISSING_CELL` — `cell` is required (the value emitted per cell).
- `PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE` — a normalization mode was requested whose required margin cannot be computed; v1 emits this as a warning when the cell aggregator is `recompute`-class and normalize is non-`none`, surfacing the margin cost as informational.
- `PULSE_CROSSTAB_AGG_UNCLASSIFIED` — internal guard: a new aggregator was added without an `AggregationType.MarginReducibility` classification. A CI gate prevents this from shipping.

## Smart defaults

Smart defaults still apply: a `cell` that names a numeric field and omits `Type` falls back to `AGG_SUM`; one that names a categorical field falls back to `AGG_FREQUENCY`. Same rules as the top-level `aggregations` slot — see `descriptor/defaults.go`. Run `pulse predict --json` to see `defaults_applied` for a crosstab spec.

## Manifest discovery

The full crosstab capability block lives at `manifest.crosstab`:

```json
{
  "crosstab": {
    "name": "crosstab",
    "normalize_modes": ["column", "none", "row", "total"],
    "shapes": ["long", "matrix"],
    "summable_aggregators": [...],
    "mean_reducible_aggregators": [...],
    "recompute_aggregators": [...],
    "streaming_exceptions": [...],
    "rejection_rules": [...]
  }
}
```

Use it from an LLM session to route between cell aggregators when the caller's normalization choice has different margin-recomputation cost.
