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

Defaults: `shape: matrix`, `normalize: none`. Result: `Response.Crosstab.Matrix` — `RowKeys`, `ColumnKeys`, `Cells`, margins, `GrandTotal`.

## Axes, cell

`rows`, `columns` are each `[]Group` — any grouper, either axis. Multiple per axis = nested headers, sorted composite-key order. Empty axes / missing cell ⇒ `PULSE_CROSSTAB_EMPTY_ROWS` / `_EMPTY_COLUMNS` / `_MISSING_CELL`.

**Include ordering per axis.** Each axis honors its own `Group.Include` order independently — non-empty `include` emits keys in listed order (per key position on a nested axis), else alphabetical. Buffered + fused agree; zero-record include values drop.

`cell` is one `Aggregation` — any `AGG_*`. Scalar cells (`MatrixCell.Value: float64`) are common. Rich variants: `AGG_SET_FREQUENCY` (`map[string]int`), set union/intersection (`[]string`), `AGG_WELFORD` (`{n,mean,variance,m2}` via `CellComponents`). Rich + non-`none` normalize ⇒ `PULSE_CROSSTAB_NORMALIZE_MAP_VALUED`; use `AGG_SET_CARDINALITY_SUM`/`_AVG` instead.

## Margins recompute from raw rows

Load-bearing: row / column / grand margins aggregate **raw rows for that margin**, NOT cell values. Mean / median / stddev / percentile margins are correct under this rule; cell-sum agreement holds only for true sums. Manifest: `crosstab.{summable,mean_reducible,recompute}_aggregators`.

## Normalize

`none` (default), `row`, `column`, `total` — each cell divided by the matching margin. Zero margin ⇒ `MatrixCell.Present=false`. Normalize implies the matching margin even when `margins.*` is false.

- `normalize_level: L` — same-axis rollup; cells sharing the first `L+1` groupers sum to 1. Rejections: `_OUT_OF_RANGE`, `_WITHOUT_NESTED_AXIS`, `_INCOMPATIBLE` (with `total`).
- `normalize_within: W` — fixes a prefix of the **opposite** axis in the denominator; composes with `normalize_level`. Canonical: `rows=[brand]`, `columns=[wave,response]`, `normalize=row`, `normalize_within=0` — each cell is brand's wave-share to that response. Rejections: `_WITHIN_OUT_OF_RANGE`, `_WITHIN_WITHOUT_AXIS`, `_WITHIN_INCOMPATIBLE`.

## Shape

`matrix` (default) — `Response.Crosstab.Matrix`: headers, key tuples, dense `Cells`, margins, `NormalizeApplied`. `long` — one row per cell on `Response.Data`, margin rows tagged `_margin: "row"|"column"|"grand"|"<axis>_at_<depth>"`. Lossless round-trip.

## Streamability

Buffered when `shape: matrix`, any `margins`, non-`none` `normalize`, or nested axes. The one streamable shape (long, no margins, `none`) still routes through the orchestrator in v1. `pulse predict --json` reports `StreamableReasons`.

### Fused mergeable path

When the cell aggregator is mergeable + non-recompute AND every axis grouper implements a per-record keying interface — `StreamableGrouper.KeyFor`, or `MultiKeyStreamingGrouper.KeysForRow` (`GROUP_SET_PER_ELEMENT` fan-out, admitted at ANY position, on either or both axes) — records fold into per-cell / per-margin online state in one decode pass. Memory `O(records) → O(cells + margins)`: ~30–47% faster, peak heap 8.8–20.8× lower across 25k→400k rows.

`Request.Overlays` does NOT disqualify — `RunCrosstabFused` folds layers after `Finalize()` through the same `applyOverlaysToResponse` hook the buffered exit uses, so cells, components, layers and warnings are byte-identical across paths. Other disqualifiers: non-mergeable / recompute cell (incl. `AGG_WELFORD`), `GROUP_QUANTILE`, tests / features / `ATTR_FORMULA` / `FILTER_EXPRESSION`, decimal128 cell, opaque extension (no `FieldInputs`).

Margins still recompute from raw rows, and under a fan-out axis are non-additive on BOTH paths — a 3-label record counts 3× across row margins, once in the grand total.

## Components — `Response.Components.Crosstab`

Mirrors `MatrixPayload`:

- `CellComponents[r][c]` ↔ `Matrix.Cells[r][c]` — `Operator`-map per the cell aggregator's `ComponentSchema`. `CellCounts[r][c]` is the RECORD count routed to that cell, i.e. floor `n + n_null`.
- `RowKeyComponents[r]` / `ColumnKeyComponents[c]` ↔ `Matrix.RowKeys[r]` / `Matrix.ColumnKeys[c]`.
- `RowMarginCounts[r]` / `*Components[r]` ↔ `Matrix.RowMargins[r]` (column symmetric); `GrandTotalCount` / `*Components` ↔ `Matrix.GrandTotal`.

Universal cell floor `{n, n_null}` (`int`) sits on the cell aggregator's declared keys; the builder injects it, so operators must not declare `n`/`n_null`. Empty cells ⇒ `CellComponents[r][c] == nil` (null-check first). Margin slots populate iff the matching `Matrix.*` slot is present.

Multi-grouper axis: `*KeyComponents[i]` carries `{axes:[{field, bucket}]}` in declaration order; single-grouper axis carries the grouper map directly.

`AGG_WELFORD` cells emit `{n, mean, variance, m2}` into `CellComponents[r][c]` via `MetaAggregator` — load-bearing for the parity overlays (`OVERLAY_T_CELL` / `_Z_CELL` / `_T_VS_REF` / `_Z_VS_REF`). `AGG_WELFORD` is non-mergeable → always buffered.

## Tests + overlays compose

Tier-1 `tests` / tier-2 `post_tests` ride raw rows; the crosstab-conflict guard fires only for top-level groups+aggregations. Crosstab is also the v1 overlay host — share triad, margin comparison, inferential, Compose vs-ref, intra-matrix pairwise. Specs ride `Request.Overlays`, layers `Response.Overlays[i]`. An overlay-carrying crosstab FUSES unless the cell-aggregator arm pushes it back to buffered.

## See

- `response-components`, `overlay-system`, `grouper-design` (fused eligibility), `aggregation-design` (margin reducibility), `statistical-testing`.
