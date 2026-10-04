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

`Request.Crosstab` pivots ONE cell aggregation across rows × columns; margins and normalize are cross-cell. Mutually exclusive with top-level `groups` + `aggregations` (`PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS`).

```jsonc
{"crosstab": {
  "rows":    [{"type": "<grouper>", "field": "region"}],
  "columns": [{"type": "<grouper>", "field": "segment"}],
  "cell":    {"type": "<aggregator>", "field": "id", "label": "n"}
}}
```

Defaults: `shape: matrix`, `normalize: none`. Result `Response.Crosstab.Matrix` — `RowKeys`, `ColumnKeys`, `Cells`, margins, `GrandTotal`.

## Choosing a crosstab

- Need margins (row / column / grand totals) or shares (row %, column %)? → crosstab.
- Only one figure per key combination, no totals? → plain `groups` + `aggregations` (several groupers form a key product; `grouper-design`).
- Several figures per cell? → one crosstab per figure (Compose them). A second figure only on the MARGINS (e.g. an unweighted base) rides `margin_aggregations` — a record reaches it only if it reached a CELL (`crosstab-margin-aggregations`).
- Comparing cells statistically (share index, cell z, χ², pairwise column tests)? → a crosstab plus overlays (`overlay-system`).

## Axes, cell

`rows`, `columns` are each a list of groupers — any grouper, either axis. Several per axis = nested headers in composite-key order. Empty axes / missing cell ⇒ `PULSE_CROSSTAB_EMPTY_ROWS` / `_EMPTY_COLUMNS` / `_MISSING_CELL`.

**Include ordering is per axis.** Each axis honors its own grouper `include` order independently — a non-empty list emits keys in listed order (per key position on a nested axis), else alphabetical. Zero-record include values drop.

`cell` is one aggregation. Most cells are scalars (`MatrixCell.Value: float64`). Map- or list-valued cells (manifest `crosstab.map_valued_cell_aggregators` and the set unions / intersections) refuse a non-`none` normalize with `PULSE_CROSSTAB_NORMALIZE_MAP_VALUED` — normalize a scalar aggregator instead.

## Margins recompute from raw rows

Load-bearing: row / column / grand margins aggregate the **raw rows of that margin**, NOT the cell values. So mean / median / stddev / percentile margins are correct, and cell-sum agreement holds only for true sums. Distinct-count style margins are the **union** — a respondent in two rows counts once in the column margin.

The manifest classifies every cell aggregator: `crosstab.summable_aggregators`, `mean_reducible_aggregators`, `independent_aggregators` (keeps its own margin accumulator) and `recompute_aggregators` (needs a raw-row rescan, so it never fuses).

## Normalize

`none` (default), `row`, `column`, `total` — each cell divided by the matching margin. Zero margin ⇒ `MatrixCell.Present=false`. Normalize implies the matching margin even when `margins.*` is false (computed, not displayed).

- `normalize_level: L` — same-axis rollup; cells sharing the first `L+1` groupers sum to 1. Rejections: `_OUT_OF_RANGE`, `_WITHOUT_NESTED_AXIS`, `_INCOMPATIBLE` (with `total`).
- `normalize_within: W` — fixes a prefix of the **opposite** axis in the denominator; composes with `normalize_level`. Canonical: `rows=[brand]`, `columns=[wave,response]`, `normalize=row`, `normalize_within=0` — each cell is brand's wave-share to that response. Rejections: `_WITHIN_OUT_OF_RANGE`, `_WITHIN_WITHOUT_AXIS`, `_WITHIN_INCOMPATIBLE`.

## Shape

`matrix` (default) — `Response.Crosstab.Matrix` incl. `NormalizeApplied`. `long` — one row per cell on `Response.Data`, margin rows tagged `_margin: "row"|"column"|"grand"|"<axis>_at_<depth>"`. Lossless round-trip.

## Streamability

Crosstab output is buffered (`pulse predict` reports `streamable_reasons`); what varies is how the grid is BUILT.

### Fused mergeable path

Records fold into per-cell / per-margin state in one decode pass — memory `O(cells + margins)`, not `O(records)`. Automatic (predict: `crosstab_fusable`, with reasons); identical output. It applies when:

- the cell aggregator is in `crosstab.summable_aggregators`, `mean_reducible_aggregators` or `independent_aggregators` — never `recompute_aggregators` — and every `margin_aggregations` entry is mergeable;
- every axis grouper keys one record at a time — all but the rank-based quantile one, fan-out included, any axis (extension: declared `Streamable` / `FansOut`);
- nothing needs whole-cohort context: no join, no tests, features, formula attributes or expression filters, no decimal cell field.

Overlays never prevent fusing. A fan-out axis makes margins non-additive on BOTH paths — a 3-option record counts 3× across row margins, once in the grand total.

**Joins.** A joined crosstab runs buffered over the JOINED rows: axes / cell may name `as`-prefixed right fields; an unmatched left row is counted nowhere; a 1:N match counts once per joined row<!-- feature: capability:joins --> (`join-design`)<!-- /feature -->.

## Components — `Response.Components.Crosstab`

Mirrors the matrix coordinate-for-coordinate: `CellComponents[r][c]` ↔ `Cells[r][c]` (cell aggregator keys + floor `{n, n_null}`; `nil` for an empty cell), `RowKeyComponents` / `ColumnKeyComponents` ↔ the key tuples (a nested axis carries `{axes: [{field, bucket}]}`), `RowMarginCounts` / `RowMarginComponents` ↔ `RowMargins` (column symmetric), `GrandTotalCount` / `GrandTotalComponents` ↔ `GrandTotal` — populated iff the matching matrix slot is. `CellCounts[r][c]` is a RECORD count (`n + n_null`), not the sample size. Shared shape: `response-components`.

## Tests + overlays compose

Row-level `tests` / `post_tests` run on raw rows beside a crosstab. Crosstab is the MATRIX overlay host (share, margin compare, inferential, pairwise): specs ride `Request.Overlays`, layers `Response.Overlays[i]`; the cell t / z overlays read `{n, mean, variance}` from `CellComponents` (`overlay-system`).

## See

- `crosstab-margin-aggregations`, `response-components`, `overlay-system`, `grouper-design`, `aggregation-design` (margin reducibility), `statistical-testing`.
