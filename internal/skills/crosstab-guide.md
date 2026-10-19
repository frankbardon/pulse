---
name: crosstab-guide
description: Crosstab slot — rows × columns groupers, margins-from-raw-rows, normalize / normalize_level / normalize_within, matrix vs long, fused vs buffered, CrosstabComponents indexing. Topical; per-aggregator/grouper math lives in op-* atomics.
type: guide
kind: design
applies_to: process, compose, predict
covers: [Crosstab, CrosstabComponents]
requires: [capability:crosstab]
---

# Crosstab

`Request.Crosstab` pivots ONE cell aggregation across rows × columns; margins and normalize are cross-cell. Exclusive with top-level `groups` + `aggregations` (`PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS`).

```jsonc
{"crosstab": {
  "rows":    [{"type": "<grouper>", "field": "region"}],
  "columns": [{"type": "<grouper>", "field": "segment"}],
  "cell":    {"type": "<aggregator>", "field": "id", "label": "n"}
}}
```

Result: `Response.Crosstab.Matrix` (keys, cells, margins).

## Choosing a crosstab

- Need margins (row / column / grand totals) or shares (row %, column %)? → crosstab.
- Only one figure per key combination, no totals? → plain `groups` + `aggregations` (`grouper-design`).
- Several figures per cell? → <!-- feature: capability:crosstab_cells -->`cells`, one stub row per measure (`crosstab-measures`), or <!-- /feature -->one crosstab per figure (Compose them). A second figure only on the MARGINS (e.g. an unweighted base, `"weight": null`) rides `margin_aggregations` (`crosstab-margin-aggregations`).
- Comparing cells statistically? → crosstab + overlays (`overlay-system`).
- Moves in a long panel (a row per subject per time)? → <!-- feature: ATTR_PREVIOUS_STATE -->previous-state rows × state columns (`op-attr-previous-state`), then `op-overlay-markov`.<!-- /feature -->

## Axes, cell

`rows`, `columns`: lists of groupers, any grouper on either axis; several = nested headers in composite-key order. No rows / cell ⇒ `PULSE_CROSSTAB_EMPTY_ROWS` / `_MISSING_CELL`.

**No `columns` = one Total column** `["Total"]` (header type `TOTAL`) of every record whose row key resolved. Column % sums to 1; row-scope pairwise compares rows; column-axis `normalize_level` / `_within` is out of range.

**Include ordering is per axis** (per key position when nested): listed order, else alphabetical; zero-record include values drop.

**Fit.** A <!-- feature: GROUP_QUANTILE -->`GROUP_QUANTILE`<!-- /feature --> / <!-- feature: GROUP_KMEANS -->`GROUP_KMEANS`<!-- /feature --> axis fits once over the filtered set (`params.fit` `global`, as in `groups`); `within` refits per outer bucket (rows, then columns).

Map- or list-valued cells (manifest `crosstab.map_valued_cell_aggregators` and the set unions / intersections) refuse a non-`none` normalize with `PULSE_CROSSTAB_NORMALIZE_MAP_VALUED`. An array-valued cell or margin aggregation needs `params.element` (one member, a scalar<!-- feature: AGG_VEC_MEAN -->; `op-agg-vec-mean`<!-- /feature -->), else `array_valued`.

## Margins recompute from raw rows

Row / column / grand margins aggregate the **raw rows of that margin**, NOT the cell values. So mean / median / percentile margins are correct; cells sum to a margin only for true sums. **A record reaches a margin only if it reached a CELL** (both axis keys resolved): a null or empty set, or an `include`-excluded key, moves no margin or denominator — column % is "% of those asked". Distinct-count margins are the **union** — a respondent in two rows counts once in the column margin. A row weight weighs cells and every margin alike; normalize divides weighted by weighted.

The manifest classifies every cell aggregator: `crosstab.summable_aggregators`, `mean_reducible_aggregators`, `independent_aggregators` (own margin accumulator) and `recompute_aggregators` (raw-row rescan; never fuses).

## Normalize

`none` (default), `row`, `column`, `total` — each cell divided by the matching margin. Zero margin ⇒ `MatrixCell.Present=false`. It needs no `margins.*` flag.

- `normalize_level: L` — same-axis rollup; cells sharing the first `L+1` groupers sum to 1. Rejections: `_OUT_OF_RANGE`, `_WITHOUT_NESTED_AXIS`, `_INCOMPATIBLE` (with `total`).
- `normalize_within: W` — fixes a prefix of the **opposite** axis in the denominator; composes with `normalize_level`. E.g. `columns=[wave,response]`, `normalize=row`, `normalize_within=0` ⇒ per-wave response share. Rejections: `_WITHIN_OUT_OF_RANGE`, `_WITHIN_WITHOUT_AXIS`, `_WITHIN_INCOMPATIBLE`.

## Shape

`matrix` (default) — `Response.Crosstab.Matrix` incl. `NormalizeApplied`. `long` — one row per cell on `Response.Data`, margin rows tagged `_margin: "row"|"column"|"grand"|"<axis>_at_<depth>"`.

## Streamability

Output is buffered (predict: `streamable_reasons`); only how the grid is BUILT varies.

### Fused mergeable path

One decode pass into per-cell / per-margin state — memory `O(cells + margins)`. Automatic (predict: `crosstab_fusable`), identical output, when:

- the cell aggregator is not in `crosstab.recompute_aggregators`, and every `margin_aggregations` entry is mergeable;
- every axis grouper keys one record at a time — all but the rank-based quantile one, fan-out included, any axis (extension: declared `Streamable` / `FansOut`);
- nothing needs whole-cohort context: no join, no tests, features, formula attributes or expression filters, no decimal cell field.

Overlays never prevent fusing. Fan-out margins are non-additive (3 options count 3×).

**Grid cap.** Rows × columns over `max_crosstab_cells` refuses with `PULSE_LIMIT_EXCEEDED`; predict flags it `possible`.

**Joins.** A joined crosstab runs buffered over the JOINED rows (`as`-prefixed right fields)<!-- feature: capability:joins -->; see `join-design`<!-- /feature -->.

## Components — `Response.Components.Crosstab`

Mirrors the matrix coordinate-for-coordinate: `CellComponents[r][c]` ↔ `Cells[r][c]` (cell keys + floor `{n, n_null}`; `nil` when empty), `RowKeyComponents` / `ColumnKeyComponents` ↔ key tuples (nested: `{axes: [{field, bucket}]}`), `RowMarginCounts` / `RowMarginComponents` ↔ `RowMargins` (columns symmetric), `GrandTotalCount` / `GrandTotalComponents` ↔ `GrandTotal`, each populated iff its matrix slot is. `CellCounts` is a RECORD count (`n + n_null`), not the sample size.

## Tests + overlays compose

Row-level `tests` / `post_tests` run on raw rows beside a crosstab. Crosstab is the MATRIX overlay host; shares / indices of a weighted host are weighted; the cell t / z overlays read `{n, mean, variance}` from `CellComponents` (`overlay-system`).

## See

- <!-- feature: capability:multiplicity -->`multiplicity-correction`, <!-- /feature -->`crosstab-margin-aggregations`, `response-components`, `overlay-system`, `grouper-design`, `aggregation-design` (margin reducibility), `statistical-testing`.
