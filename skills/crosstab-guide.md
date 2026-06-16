---
name: crosstab-guide
description: Crosstab slot — rows × columns groupers, margins-from-raw-rows, normalize / normalize_level / normalize_within, matrix vs long, fused vs buffered, CrosstabComponents indexing. Topical; per-aggregator/grouper math lives in op-* atomics.
type: guide
kind: design
applies_to: process, compose, predict
covers: [Crosstab, CrosstabComponents]
---

# Crosstab

`Request.Crosstab` pivots one cell aggregation across rows × columns. Not an `AGG_*` — margins / normalize are cross-cell. Mutually exclusive with top-level `groups`+`aggregations` (`PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS`).

```jsonc
{"crosstab": {
  "rows":    [{"type":"GROUP_CATEGORY","field":"region"}],
  "columns": [{"type":"GROUP_CATEGORY","field":"segment"}],
  "cell":    {"type":"AGG_COUNT","field":"id","label":"n"}
}}
```

Defaults: `shape: matrix`, `normalize: none`. Result: `Response.Crosstab.Matrix` with `RowKeys`, `ColumnKeys`, `Cells`, margins, `GrandTotal`.

## Axes, cell

`rows`, `columns` are each `[]Group`. Any grouper, either axis. Multiple per axis = nested headers; sorted composite-key order. Empty axes / missing cell ⇒ `PULSE_CROSSTAB_EMPTY_ROWS` / `_EMPTY_COLUMNS` / `_MISSING_CELL`.

`cell` is one `Aggregation` — every `AGG_*`. Scalar cells (`MatrixCell.Value: float64`) are common. Rich variants: `AGG_SET_FREQUENCY` (`map[string]int`), set union/intersection (rich `[]string`), `AGG_WELFORD` (`{n,mean,variance,m2}` via `CellComponents` — the legacy `processing.WelfordTriple` smuggle in `MatrixCell.Value` is removed in v0.20.0). Rich + non-`none` normalize ⇒ `PULSE_CROSSTAB_NORMALIZE_MAP_VALUED`; use `AGG_SET_CARDINALITY_SUM`/`_AVG` for normalised set columns.

## Margins recompute from raw rows

Load-bearing: row / column / grand margins aggregate **raw rows for that margin**, NOT cell values. Mean / median / stddev / percentile margins are correct under this rule; cell-sum agreement holds only for true sums. Manifest classifies under `crosstab.{summable,mean_reducible,recompute}_aggregators`.

## Normalize

`none` (default), `row`, `column`, `total` — each cell divided by the matching margin. Zero margin ⇒ `MatrixCell.Present=false`. Normalize implies the matching margin even when `margins.*` is false for display.

- `normalize_level: L` — same-axis rollup. Cells sharing the first `L+1` groupers sum to 1. Rejections: `_OUT_OF_RANGE`, `_WITHOUT_NESTED_AXIS`, `_INCOMPATIBLE` (with `total`).
- `normalize_within: W` — fixes a prefix of the **opposite** axis inside the denominator; composes with `normalize_level`. Canonical: `rows=[brand]`, `columns=[wave,response]`, `normalize=row`, `normalize_within=0` — each cell is brand's wave-share going to that response. Rejections: `_WITHIN_OUT_OF_RANGE`, `_WITHIN_WITHOUT_AXIS`, `_WITHIN_INCOMPATIBLE`.

## Shape

`matrix` (default) — `Response.Crosstab.Matrix`: headers, key tuples, dense `Cells`, margins, `NormalizeApplied`. `long` — one row per cell on `Response.Data`; margin rows tagged via `_margin: "row"|"column"|"grand"|"<axis>_at_<depth>"`. Lossless round-trip.

## Streamability

Buffered when `shape: matrix`, any `margins`, any non-`none` `normalize`, or nested axes. The only streamable shape (long + no margins + `none`) still routes through the orchestrator in v1. `pulse predict --json` reports `StreamableReasons`.

### Fused mergeable path

When the cell aggregator is mergeable + non-recompute AND every axis grouper implements `StreamableGrouper.KeyFor`, records fold directly into per-cell / per-margin online state in one decode pass. Memory `O(records) → O(cells + margins)`. Disqualifiers: recompute cell, `GROUP_QUANTILE` / `GROUP_SET_PER_ELEMENT`, tests / features / `ATTR_FORMULA` / `FILTER_EXPRESSION`, decimal128 cell, `Request.Overlays`, opaque extension (no `FieldInputs`). Margins still recompute from raw rows. ~30–47% faster on benches. Buffered fallback runs auto field projection on the materialised set.

## Components — `Response.Components.Crosstab`

Coordinate-for-coordinate sibling of `MatrixPayload`:

- `CellComponents[r][c]` ↔ `Matrix.Cells[r][c]` — `Operator`-map per cell aggregator's `ComponentSchema`.
- `CellCounts[r][c]` ↔ records routed to cell.
- `RowKeyComponents[r]` / `ColumnKeyComponents[c]` ↔ `Matrix.RowKeys[r]` / `Matrix.ColumnKeys[c]`.
- `RowMarginCounts[r]` / `*Components[r]` ↔ `Matrix.RowMargins[r]`; column symmetric.
- `GrandTotalCount` / `*Components` ↔ `Matrix.GrandTotal`.

Universal cell floor `{n, n_null}` (always `int`) on top of the cell aggregator's declared keys. Builder injects — operators must not declare `n`/`n_null`. Empty cells ⇒ `CellComponents[r][c] == nil` (null-check before read). Margin slots populated iff the matching `Matrix.*` slot is present.

Multi-grouper axis: `*KeyComponents[i]` carries `{axes:[{field, bucket}, …]}` in declaration order. Single-grouper axis carries the grouper-components map directly.

`AGG_WELFORD` cells emit `{n, mean, variance, m2}` into `CellComponents[r][c]` via the `MetaAggregator` path — load-bearing for parity overlays (`OVERLAY_T_CELL` / `OVERLAY_Z_CELL` / `OVERLAY_T_VS_REF` / `OVERLAY_Z_VS_REF`) after v0.20.0. Buffered + fused emit byte-identical `CellComponents`.

## Tests + overlays compose

Tier-1 `tests` / tier-2 `post_tests` ride raw rows; the crosstab-conflict guard fires only for top-level groups+aggregations. Crosstab is also the v1 overlay host — share triad, margin-comparison (index/delta/zscore), inferential family, Compose vs-ref kinds. Specs ride `Request.Overlays`; layers ride `Response.Overlays[i]`. Buffered today.

## See

- `skills/response-components.md` — universal `Response.Components` contract.
- `skills/overlay-system.md` — overlay framework + parity migration.
- `skills/grouper-design.md` — fused-path eligibility per grouper.
- `skills/aggregation-guide.md` — margin reducibility per `AGG_*`.
- `skills/statistical-testing.md` — picking `TEST_*` per cell aggregator.
