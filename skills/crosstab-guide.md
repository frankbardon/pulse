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

## Pair with chi-square / Fisher exact

For inference on a count crosstab (is the row × column relationship statistically meaningful?) Pulse already ships `TEST_CHISQ` and `TEST_FISHER_EXACT` in the tier-1 test set. Do NOT build new independence-testing logic — use the existing operators alongside the crosstab. Typical pattern:

```json
{
  "cohort": {"filename": "trial.pulse"},
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "outcome"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true, "columns": true, "grand": true}
  },
  "tests": [
    {"type": "TEST_CHISQ", "rows": "treatment", "cols": "outcome"}
  ]
}
```

Use `TEST_FISHER_EXACT` instead of `TEST_CHISQ` when any expected cell count is below 5 (the classic small-sample threshold for the asymptotic χ² approximation).

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
