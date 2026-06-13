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

### Map-valued cells

Most aggregators emit scalar cells — `MatrixCell.Value` carries a `float64` and serializes as a JSON number. **Map-valued aggregators** (advertised on `manifest.crosstab.map_valued_cell_aggregators`) emit per-key rich payloads instead. The widening lives entirely in `MatrixCell.Value`, which is typed `any`; scalar cells still marshal byte-for-byte as they did before.

| Aggregator | `MatrixCell.Value` payload | Notes |
|---|---|---|
| Every scalar aggregator (`AGG_COUNT`, `AGG_SUM`, `AGG_AVERAGE`, …) | `float64` | Unchanged. JSON shape identical to pre-widening. |
| `AGG_SET_FREQUENCY` | `map[string]int` | One key per dictionary label that appeared at least once in the cell's bucket; zero-count labels are omitted. JSON shape: `{"VISA": 3, "MC": 1}`. |
| `AGG_SET_UNION`, `AGG_SET_INTERSECTION` | scalar (popcount) by default; `[]string` of labels surfaced through the `RichAggregator` interface when reading at the dispatch layer | Cells still serialize as a number for backward compat; the rich `[]string` form is used by `Response.Data` rows from `Process` for ungrouped/grouped paths. |

Margins for map-valued cells are recomputed against the row/column/grand buckets and emit maps as well. The row margin for `AGG_SET_FREQUENCY` over the "north" row is the per-label histogram of every "north" record, NOT the sum of the per-cell maps in that row (they happen to agree for counts but the API contract is "margins recompute over raw rows" — see the next section).

#### `normalize` is incompatible with map-valued cells

Dividing one map by another is undefined. Pairing `normalize=row/column/total` with a map-valued cell aggregator raises `PULSE_CROSSTAB_NORMALIZE_MAP_VALUED` (both at predict time and at runtime). To get normalized output from set columns, switch the cell aggregator to a scalar form:

- `AGG_SET_CARDINALITY_SUM` — total bits set across cells; summable, normalizes cleanly.
- `AGG_SET_CARDINALITY_AVG` — mean popcount per row in the cell.

#### Reading map cells

Callers consuming `Response.Crosstab.Matrix.Cells[i][j].Value` must type-switch:

```go
switch v := cell.Value.(type) {
case float64:
    // scalar aggregator path
case map[string]int:
    // AGG_SET_FREQUENCY path — one entry per non-zero dictionary label
case []string:
    // (reserved) rich-set fall-through; not emitted in Crosstab cells today
}
```

`MatrixCell.Scalar()` is a convenience accessor that returns the `float64` form when present and zero otherwise — useful for downstream code that only needs scalar cells and can ignore rich payloads.

#### Welford-triple cells (`AGG_WELFORD`)

The `AGG_WELFORD` aggregator (see `skills/aggregation-guide.md` "Welford triple aggregator") widens `MatrixCell.Value` to a third Rich payload shape: `processing.WelfordTriple{Mean float64; Variance float64; N uint64}`. Each cell carries the streaming Welford-Pébaÿ `(mean, sample_variance, n)` triple for the records that landed in it; the scalar `MatrixCell.Scalar()` fallback returns the running mean (`NaN` when `N == 0`) so callers that only need a mean still see one. JSON shape is `{"mean": 72.4, "variance": 81.6, "n": 412}`.

The Welford triple is the consumption surface for the four Compose-host parametric overlay kinds — `OVERLAY_T_CELL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_CELL`, `OVERLAY_Z_VS_REF`. Each handler auto-detects the Rich triple on either side of the test, reads `(mean, variance, n)` directly, and computes the per-cell Welch t / two-sample z statistic without consulting `Params["variance_*"]` / `Params["sample_size_*"]`. The resulting overlay p-value is byte-equal to the corresponding native row test (`TEST_T` two-sample / `TEST_Z_TWO_SAMPLE`) per group for the same input stream because the recurrence type (`processing/welford_bucket.go`) and the survival helpers (`studentTTwoSidedP` / `standardNormalCDF`) are shared across the overlay and row-test surfaces. Pairing `AGG_WELFORD` on the cell axis with one of the four overlay kinds is therefore the canonical "exact per-cell parametric test" pattern — see `skills/overlay-system.md` "Welch overlay upgrade (Rich-triple consumption)" for the full additive contract and the scalar-plus-Params fallback chain.

```go
switch v := cell.Value.(type) {
case float64:
    // scalar aggregator path (AGG_AVERAGE, AGG_SUM, ...)
case map[string]int:
    // AGG_SET_FREQUENCY path
case processing.WelfordTriple:
    // AGG_WELFORD path — v.Mean / v.Variance / v.N
}
```

Margins for Welford-triple cells are recomputed against the row / column / grand buckets by re-folding the Welford recurrence over the raw rows in that margin — matches the `MarginRecompute` classification on `AGG_WELFORD` (`skills/aggregation-guide.md`). Pairing Welford cells with `normalize=row/column/total` raises `PULSE_CROSSTAB_NORMALIZE_MAP_VALUED` for the same reason map-valued cells do — dividing one variance triple by another is undefined. To get a normalized rendering of Welford output, pair `AGG_WELFORD` with one of the overlay kinds above and read the per-cell p-value layer; that is the additive, statistically-correct projection.

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

### Partial-depth normalization

When the normalization axis has nested groupers (e.g. `columns: [region, segment, product]`), the default denominator is the **leaf composite** — cells in each leaf `(region, segment, product)` column sum to 1. `normalize_level` lets the caller pick a higher grouper as the 100% denominator:

- `normalize_level: 0` — denominator is the value of the first (top-level) grouper. Cells under the same top-level value, across every nested child column, sum to 1.
- `normalize_level: 1` — denominator is the value of the second grouper. Cells under the same `(top, second)` tuple sum to 1.
- omitted or `len(axis)-1` — leaf-tuple denominator (the original behavior). The two are byte-equal.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "outcome"}],
    "columns": [
      {"type": "GROUP_CATEGORY", "field": "region"},
      {"type": "GROUP_CATEGORY", "field": "segment"},
      {"type": "GROUP_CATEGORY", "field": "product"}
    ],
    "cell":            {"type": "AGG_COUNT", "field": "id", "label": "share"},
    "normalize":       "column",
    "normalize_level": 0
  }
}
```

Each region (the top-level column grouper) holds 100% of the count; cells partition that count across the `(segment, product)` leaves below it.

Symmetric for `normalize: row` — `normalize_level` picks a parent grouper on the nested row axis.

Partial margins are recomputed from raw rows just like leaf margins, so `recompute`-class aggregators (`AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_STDDEV`) produce statistically correct partial denominators — the median for `region=north` is the standalone median of every `north` record, not the median of the per-`(segment, product)` cell medians.

Long-shape (`shape: long`) emission with `margins.columns: true` (or `margins.rows: true` for row partials) emits one extra row per partial bucket tagged `_margin: "column_at_<depth>"` (or `row_at_<depth>`), alongside the existing leaf `_margin: "column"` rows. Matrix shape keeps the leaf-level margin vectors unchanged; the partial denominator surfaces only via the long-shape tag.

Rejection rules:

- `PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE` — value outside `[0, len(axis)-1]` for the axis selected by `normalize`.
- `PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS` — `normalize_level` set with `normalize: none`. The level only has meaning when a direction is chosen.
- `PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE` — `normalize_level` set with `normalize: total`. Total uses a scalar grand-total denominator with no axis to descend.

### Cross-axis partitioned denominator (`normalize_within`)

`normalize_level` collapses depth on the same axis as `normalize`. `normalize_within` is its cross-axis sibling: it fixes a prefix of the **opposite** axis inside the 100% denominator. The two compose independently — one truncates the normalize axis, the other partitions the other axis.

- `normalize: row` + `normalize_within: W` ⇒ denominator partitions records by `(full row key, columns[:W+1])`. Cells in each `(rowKey, outerColPrefix)` slab sum to 1 across the inner-column dimension.
- `normalize: column` + `normalize_within: W` ⇒ denominator partitions records by `(full column key, rows[:W+1])`. Symmetric.
- Omit `normalize_within` to keep the standard row / column marginal (no cross-axis partition — original behavior).

Canonical survey scenario — `rows=[brand]`, `columns=[wavedate, xxx]`, `cell=AGG_SUM(weight)`, `normalize=row`, `normalize_within=0`: each cell is divided by `Σ weight over xxx` within the fixed `(brand, wavedate)` slab. The brand × wavedate slab of xxx cells sums to 1.0, so each cell is the share of that brand's weight in that wave going to that xxx response. Plain `normalize=row` would collapse the entire column axis (all wavedate × xxx) into one denominator; plain `normalize=column normalize_level=0` would collapse all brands into one denominator. Neither matches the survey-share semantics — `normalize_within` does.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "brand"}],
    "columns": [
      {"type": "GROUP_DATE", "field": "wavedate", "interval": "month"},
      {"type": "GROUP_CATEGORY", "field": "xxx"}
    ],
    "cell":             {"type": "AGG_SUM", "field": "weight", "label": "share"},
    "normalize":        "row",
    "normalize_within": 0
  }
}
```

Composing with `normalize_level`: `rows=[region, brand]`, `columns=[wavedate, xxx]`, `normalize=row`, `normalize_level=0`, `normalize_within=0` ⇒ denominator partitions by `(region, wavedate)` — both axes' deeper levels collapse. Cells in each `(region, wavedate)` slab sum to 1.0 across `(brand, xxx)`.

Long-shape emission does not add a new `_margin` tag for the cross-axis partition; cells emit as normal data rows with the normalized values. Displayed row / column margin vectors (when `margins.rows`/`margins.columns` are set) remain the full marginals — the cross-axis denominator is internal to the cell normalization and not surfaced as a separate margin row.

Rejection rules:

- `PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE` — value outside `[0, len(other-axis)-1]` where other-axis is `columns` when `normalize=row` and `rows` when `normalize=column`.
- `PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS` — `normalize_within` set with `normalize: none`.
- `PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE` — `normalize_within` set with `normalize: total`.

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

### Fused mergeable path (automatic)

When the cell aggregator is mergeable and every axis grouper exposes a per-record key (`StreamableGrouper.KeyFor`), the orchestrator skips record materialization entirely and folds each filter-passing record into per-cell / per-margin online aggregator state during the same single decode pass. Wide-cohort crosstabs that would otherwise materialize the full filter-passing record set drop from `O(records)` working memory to `O(cells + margins)`. The classification is automatic — there is no flag and the output is byte-equal to the buffered path on accepted requests (asserted by an equivalence golden suite plus a build-tagged perf gate).

Eligibility (all must hold):

- **Cell aggregator is mergeable + non-recompute.** `AggregationType.Mergeable()` is true and `MarginReducibility()` is `summable` or `mean_reducible`. The recompute class (`AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_ZSCORE`, `AGG_STDDEV` when classified as recompute) cannot be folded online — they fall back.
- **Every row/column grouper implements `StreamableGrouper`.** `GROUP_CATEGORY`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_DATE`, and `GROUP_SET_VALUE` qualify. `GROUP_QUANTILE` needs finalize-time sort and `GROUP_SET_PER_ELEMENT` fans one record into many keys — neither qualifies. The gate consults the interface directly (not the static `GroupType.Streamable()` table), so `GROUP_DATE` is fusable on an axis even though it is buffered at the top-level Process layer.
- **No tier-1 tests, no tier-2 post-tests, no features.** Tests fold over the buffered row set; features run a buffered pre-filter pass.
- **No `ATTR_FORMULA` with a non-empty expression and no `FILTER_EXPRESSION`.** The expression runtime widens the projection extractor to "every field"; the fused path's tight decode bound depends on a precise projection.
- **No decimal128 cell field.** The decimal aggregation path is buffered today.
- **No extension operator registered without a `FieldInputs` hook.** An opaque extension widens the projection set, defeating the fused path's decode-cost advantage.
- **No `Request.Overlays`.** The overlay fold runs against the finalised buffered `RunCrosstab` exit (`processing/crosstab.go applyOverlaysToResponse`). The fused finalize does not invoke that hook in E1 — overlay-bearing requests therefore fall back to the buffered path so `Response.Overlays` is populated end-to-end. Every registered overlay kind is also non-streamable per `types.OverlayStreamability`, so the gate composes with the predict-level streamability surface.

Everything else (margins, normalize, normalize_level, normalize_within, nested axes on either side, the shape selector) composes with the fused path identically to the buffered path. **Margins still recompute from raw rows in the fused case** — each margin slot runs its own dedicated online aggregator over the records that touch it, so the median-on-margin correctness rule from the buffered section above carries over unchanged. **Cross-axis null handling (E4-S4Q):** axis composite keys are interned per axis the moment they resolve. A record whose row key is non-null but column key is null still updates the row margin (and the grand margin); cells land only at intersections where both axes resolved. The fused path tracks row keys and column keys independently — symmetric with the buffered path's `PartitionByAxis(rows, ...)` and `PartitionByAxis(columns, ...)` calls, which build the two partitions over the full filtered slice regardless of partner-axis nullity. `normalize_level` and `normalize_within` semantics are unchanged from the buffered section: same-axis truncation for `normalize_level`, opposite-axis prefix for `normalize_within`, both gates compose.

What gets short-circuited: no `service.materializeRecords` drain, no recursive `processing.PartitionByAxis` per axis, no second buffered traversal for margin recompute. The orchestrator opens the cohort, applies defaults, runs the gate, and (on accept) streams the iterator directly into a `processing.FusedCrosstabState` whose `Update(rec)` folds the record into every accumulator it touches.

#### Eligible request

Matches the canonical `examples/crosstab/huge-request.json` shape — survey-style row-normalize-within: brand × (wave, response), sum of weight, row normalize partitioned by wave. Mergeable cell, two streamable groupers on the column axis (`GROUP_DATE` admitted via the interface), no tests, no expression.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "brand"}],
    "columns": [
      {"type": "GROUP_DATE", "field": "waveDate", "interval": "quarter"},
      {"type": "GROUP_CATEGORY", "field": "response"}
    ],
    "cell":             {"type": "AGG_SUM", "field": "weight", "label": "share"},
    "margins":          {"rows": true, "columns": true, "grand": true},
    "normalize":        "row",
    "normalize_within": 0
  }
}
```

#### Non-eligible request

`AGG_MEDIAN` on the cell is recompute-class — it needs a sorted view per cell. Falls back to buffered. Same fallback if you add `tests: [{type: "TEST_CHISQ", ...}]`, or set the cell field to a `decimal128` column, or use `GROUP_QUANTILE` on either axis.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_MEDIAN", "field": "revenue", "label": "med"}
  }
}
```

#### Perf characteristics

- **Synth 200-field × 100K-row matrix crosstab, sum-of-weight cell, two-grouper column axis, row-normalize-within:** fused ~0.51 × buffered wall-clock (~47 % faster). This is the perf gate's accept window (the test asserts ≤ 0.80 ×; the maintainer's M1 Max comfortably clears it).
- **Canonical 1 M-row real cohort:** fused 2.57 s vs E2 buffered baseline 3.82 s (~32 % faster). The smaller delta vs synth reflects the wider variance of decoded payloads on the real cohort.
- **Allocs/op:** fused is slightly higher (~+25 %) because each unique cell allocates a fresh `OnlineAggregator` instance on first sight (vs the buffered path's one slab allocation per partition bucket).
- **Bytes/op:** fused is ~−18 % lower because the per-record materialisation cost dominates the buffered path on wide cohorts.

When fusion does NOT engage (the gate rejects), the request transparently runs the buffered path with the existing record-set projection (the previous section). Run `pulse predict --json` to see streamability classification — predict still reports a buffered crosstab as buffered because the buffer-vs-fuse distinction is internal to the orchestrator.

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

## Overlays

A Crosstab result can be decorated with one or more **overlays** — additive, read-only number grids attached to the response in matching `Request.Overlays[i]` ↔ `Response.Overlays[i]` slot order. Overlays never mutate the base matrix; they ride alongside it carrying derived projections (share ratios, index scores, deltas, z-scores, χ² statistics, Fisher's exact p-values) so renderers paint a single host grid with one or more decoration layers without re-deriving the math on the client.

Crosstab is the v1 overlay host. Ten overlay kinds target the Crosstab matrix today, organised into three families:

- **Share triad (descriptive ratios).** Per-cell `cell / margin` against a structurally fixed axis. Cells in a single row / column / matrix sum to 1.0 in the absence of missing cells.
- **Margin-comparison family (descriptive deviation).** Per-cell index / delta / z-score against a caller-chosen axis margin (row / column / grand).
- **Inferential family (statistical tests).** χ² independence over the whole matrix, χ² goodness-of-fit per row / per column, and per-cell Fisher's exact against a 2×2 contingency built from the row + column margins.

Every Crosstab overlay is buffered today — they ride the buffered `RunCrosstab` exit (`processing/crosstab.go applyOverlaysToResponse`). The fused crosstab path (see "Fused mergeable path" above) automatically falls back to the buffered path when `Request.Overlays` is non-empty so `Response.Overlays` is populated end-to-end.

For the general overlay framework — kinds × shapes × scopes × refs taxonomy, the three-shape model (scalar / series / matrix), the renderer-facing parallel-slice contract for series payloads, the streamable vs buffered contract, validation rules, the manifest capability block, and the recipe for adding a new kind — see `skills/overlay-system.md`. This section keeps the focus on the **Crosstab-host application**: per-kind JSON recipes you can drop into a request body alongside an existing `crosstab` section.

### Quick reference table

| Kind | Scope | Shape | Ref family | Implicit-margin | Math |
|---|---|---|---|---|---|
| `OVERLAY_INDEX_VS_MARGIN` | `cell` | `matrix` | `Margin` (row / column / grand) | no (caller picks axis) | `100 * cell / margin` |
| `OVERLAY_SHARE_OF_ROW` | `cell` | `matrix` | `Margin` (axis fixed to row) | no | `cell / row_margin` |
| `OVERLAY_SHARE_OF_COL` | `cell` | `matrix` | `Margin` (axis fixed to column) | no | `cell / col_margin` |
| `OVERLAY_SHARE_OF_TOTAL` | `cell` | `matrix` | `Margin` (axis fixed to grand) | no | `cell / grand_total` |
| `OVERLAY_DELTA_VS_MARGIN` | `cell` | `matrix` | `Margin` (row / column / grand) | no | `cell - margin` |
| `OVERLAY_ZSCORE_VS_MARGIN` | `cell` | `matrix` | `Margin` (row / column / grand) | no | `(cell - margin) / sd` |
| `OVERLAY_CHISQ_MATRIX` | `matrix` | `scalar` | — | **yes** (no `ref`) | `Σ (observed - expected)² / expected`; `df = (rows - 1) * (cols - 1)` |
| `OVERLAY_CHISQ_ROW` | `row` | `series` | — | **yes** (no `ref`) | Per row `r`: `Σ_c (observed[c] - expected[c])² / expected[c]`; `df = cols - 1` |
| `OVERLAY_CHISQ_COL` | `column` | `series` | — | **yes** (no `ref`) | Per column `c`: `Σ_r (observed[r] - expected[r])² / expected[r]`; `df = rows - 1` |
| `OVERLAY_FISHER_EXACT_CELL` | `cell` | `matrix` | — | **yes** (no `ref`) | Per cell `(i, j)`: two-sided exact p-value over the 2×2 `{a=cell, b=row-cell; c=col-cell, d=grand-row-col+cell}` |

`expected[r, c] = row_margin[r] * col_margin[c] / grand_total` for every χ² / Fisher kind. The implicit-margin family must NOT populate `ref` — supplying any ref-family pointer (`margin`, `sibling`, `baseline_index`, `population`, `stage`, `slot`) fires `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. The descriptive families MUST populate `ref.margin`; the share triad's axis is structurally fixed (the runtime reads the locked axis regardless of what the caller wrote) and the index / delta / zscore family dispatches off `ref.margin.axis`.

Every kind is buffered today (`streamable: false` in `types.OverlayStreamability` and `manifest.overlays[*].buffered = true`). Run `pulse predict --json` to confirm the streamability classification before an expensive matrix overlay.

### Recipes — descriptive ratios (the share triad)

The three share kinds are structurally axis-locked. Each cell becomes a ratio against the matching margin slot, so a single row (or column, or matrix) sums to 1.0 in the absence of missing cells. Use the share triad for "what fraction of the row / column / grand total lives in this cell?"

#### `OVERLAY_SHARE_OF_ROW`

One-liner: per-cell share of row margin. Cells in each row sum to 1.0. Renderers can present the layer as a 100%-stacked horizontal projection.

```json
{
  "cohort": {"filename": "sales.pulse"},
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"}
  },
  "overlays": [
    {
      "name":  "s_row",
      "kind":  "OVERLAY_SHARE_OF_ROW",
      "scope": "cell",
      "ref":   {"margin": {"axis": "row"}}
    }
  ]
}
```

#### `OVERLAY_SHARE_OF_COL`

One-liner: per-cell share of column margin. Cells in each column sum to 1.0. Renders cleanly as a 100%-stacked vertical projection.

```json
{
  "overlays": [
    {
      "name":  "s_col",
      "kind":  "OVERLAY_SHARE_OF_COL",
      "scope": "cell",
      "ref":   {"margin": {"axis": "column"}}
    }
  ]
}
```

#### `OVERLAY_SHARE_OF_TOTAL`

One-liner: per-cell share of grand total. The whole matrix sums to 1.0. Renders as a single-population share projection.

```json
{
  "overlays": [
    {
      "name":  "s_total",
      "kind":  "OVERLAY_SHARE_OF_TOTAL",
      "scope": "cell",
      "ref":   {"margin": {"axis": "grand"}}
    }
  ]
}
```

### Recipes — margin-comparison family (index, delta, z-score)

All three kinds dispatch off `ref.margin.axis` — pick `row`, `column`, or `grand`. Unlike the share triad's structural axis lock, you can layer multiple specs of the same kind side-by-side with different axes to give renderers a "switch denominator" dropdown.

#### `OVERLAY_INDEX_VS_MARGIN`

One-liner: per-cell index score `100 * cell / margin`. Baseline is 100 — cells above index over-perform the margin, cells below under-perform. The worked example with North / South × Enterprise / SMB cells lives in `skills/overlay-system.md` ("Worked example: `OVERLAY_INDEX_VS_MARGIN` against a 2-axis crosstab").

```json
{
  "overlays": [
    {
      "name":  "i_row",
      "kind":  "OVERLAY_INDEX_VS_MARGIN",
      "scope": "cell",
      "ref":   {"margin": {"axis": "row"}}
    }
  ]
}
```

#### `OVERLAY_DELTA_VS_MARGIN`

One-liner: per-cell additive delta `cell - margin`. Preserves the host's units (a $-valued AGG_SUM cell minus a $-valued row margin yields a $-valued deviation in the same currency). Baseline is 0 — no division, no zero-denominator warning. Renderers centre diverging colour ramps on zero.

```json
{
  "overlays": [
    {
      "name":  "d_col",
      "kind":  "OVERLAY_DELTA_VS_MARGIN",
      "scope": "cell",
      "ref":   {"margin": {"axis": "column"}}
    }
  ]
}
```

#### `OVERLAY_ZSCORE_VS_MARGIN`

One-liner: per-cell standardised deviation `(cell - margin) / sd`. The `sd` is the population standard deviation of cell values within the matching margin slice (per-row cells for `axis: row`, per-column cells for `axis: column`, every matrix cell for `axis: grand`), computed via the shared Welford-Pébaÿ recurrence. Baseline is 0 — output is unitless deviation. Degenerate slices (every cell equal, `sd == 0`) emit absent overlay cells plus `PULSE_OVERLAY_REF_ZERO` warnings.

```json
{
  "overlays": [
    {
      "name":  "z_grand",
      "kind":  "OVERLAY_ZSCORE_VS_MARGIN",
      "scope": "cell",
      "ref":   {"margin": {"axis": "grand"}}
    }
  ]
}
```

### Recipes — inferential family (χ² + Fisher's exact)

The four inferential kinds are **implicit-margin**: they compute the contingency from the host's row + column margins inline, so the spec must NOT populate `ref`. Supplying any ref-family pointer fires `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. Every kind reuses the same statistical primitives as the matching `TEST_*` operator (`chiSquareSurvival` for χ², `fisherExactTwoSided` for Fisher), so the overlay and row-test surfaces produce identical p-values for the same contingency.

#### `OVERLAY_CHISQ_MATRIX`

One-liner: whole-matrix χ² independence test. Scalar payload carries the χ² statistic; `summary.statistic`, `summary.p_value`, and `summary.parameters.df` carry the test result. Use for "is the row × column relationship meaningful at all?" Read `warnings[]` for `PULSE_OVERLAY_EXPECTED_LOW` when any expected cell falls below 5 — that is your signal to switch to Fisher's exact.

```json
{
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "treatment"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    "margins": {"rows": true, "columns": true, "grand": true}
  },
  "overlays": [
    {
      "name":  "chi2_indep",
      "kind":  "OVERLAY_CHISQ_MATRIX",
      "scope": "matrix"
    }
  ]
}
```

Math: `chisq = Σ (observed - expected)² / expected`; `df = (rows - 1) * (cols - 1)`; `p = 1 - chi2_cdf(chisq, df)`.

#### `OVERLAY_CHISQ_ROW`

One-liner: per-row χ² goodness-of-fit across the column distribution. Series payload — one `SeriesEntry` per row tuple, `entries[i].key` matches host `row_keys[i]` element-for-element (the parallel-slice contract). Each entry carries `summary.statistic` / `summary.p_value` / `summary.parameters.df`.

```json
{
  "overlays": [
    {
      "name":  "chi2_per_row",
      "kind":  "OVERLAY_CHISQ_ROW",
      "scope": "row"
    }
  ]
}
```

Math (per row `r`): `observed[c] = host.Cell(r, c)`; `expected[c] = row_margin[r] * col_margin[c] / grand_total`; `chisq_r = Σ_c (observed[c] - expected[c])² / expected[c]`; `df = cols - 1`. The handler emits ONE `PULSE_OVERLAY_EXPECTED_LOW` warning per offending row (the row, not the cell, is the diagnostic unit for goodness-of-fit).

#### `OVERLAY_CHISQ_COL`

One-liner: per-column χ² goodness-of-fit across the row distribution. Mechanical column-axis twin of `OVERLAY_CHISQ_ROW`. Series entries align element-for-element with host `column_keys[i]`.

```json
{
  "overlays": [
    {
      "name":  "chi2_per_col",
      "kind":  "OVERLAY_CHISQ_COL",
      "scope": "column"
    }
  ]
}
```

Math (per column `c`): `observed[r] = host.Cell(r, c)`; `expected[r] = row_margin[r] * col_margin[c] / grand_total`; `chisq_c = Σ_r (observed[r] - expected[r])² / expected[r]`; `df = rows - 1`. One `PULSE_OVERLAY_EXPECTED_LOW` warning per offending column.

#### `OVERLAY_FISHER_EXACT_CELL`

One-liner: per-cell Fisher's exact two-sided test against a 2×2 contingency built from the cell, its row margin, its column margin, and the grand total. Matrix payload — each cell's value is the exact two-sided p-value as a `float64`. The canonical low-count contingency overlay; closes the inferential family as the structural backstop when `OVERLAY_CHISQ_MATRIX` would emit `PULSE_OVERLAY_EXPECTED_LOW`.

```json
{
  "overlays": [
    {
      "name":  "fisher_per_cell",
      "kind":  "OVERLAY_FISHER_EXACT_CELL",
      "scope": "cell"
    }
  ]
}
```

Math (per cell at `(i, j)`): build the 2×2 `{a = cell, b = row_margin - cell; c = col_margin - cell, d = grand - row_margin - col_margin + cell}`, then sum hypergeometric probabilities for every feasible `a` whose log-probability is `≤` the observed (two-sided convention). Reuses the same `lgamma`-backed hypergeometric primitive as `TEST_FISHER_EXACT`. Fisher's exact is structurally robust to low expected counts — the handler still emits `PULSE_OVERLAY_EXPECTED_LOW` per offending cell as a renderer-facing hint to flag cells where the cheaper χ² approximation would be unreliable and Fisher's exact is structurally required.

### Low-expected-count warnings (χ² / Fisher)

The χ² / Fisher inferential family surfaces `PULSE_OVERLAY_EXPECTED_LOW` as a **warning-class code** through the response envelope — the layer still renders, the warning flags the cells / rows / columns / matrices where the χ² approximation would be unreliable. **Callers must read `warnings[]`** to catch the flag — the layer payload itself does not encode it.

The trigger is the canonical Cochran rule applied per kind:

| Kind | Trigger | Granularity |
|---|---|---|
| `OVERLAY_CHISQ_MATRIX` | any expected cell `< 5` in the whole matrix | one warning per layer |
| `OVERLAY_CHISQ_ROW` | any expected cell `< 5` in a given row | one warning per offending row |
| `OVERLAY_CHISQ_COL` | any expected cell `< 5` in a given column | one warning per offending column |
| `OVERLAY_FISHER_EXACT_CELL` | any expected count of the 2×2 `< 1` OR `≥ 20%` of expected counts `< 5` (1 of 4 cells with four-cell 2×2) | one warning per offending cell |

`PULSE_OVERLAY_EXPECTED_LOW`'s details carry the structured context — `low_expected_cells` count and `expected_min` for the χ² family, `row_index` / `col_index` for the per-row / per-column / per-cell families. Reach the per-code prose via `pulse_errors_lookup` (MCP) or `pulse errors lookup PULSE_OVERLAY_EXPECTED_LOW` (CLI). The χ² family mirrors `PULSE_TEST_EXPECTED_COUNT_TOO_LOW` on the `TEST_CHISQ` surface — the warning fires identically against the same contingency.

For Fisher's exact, the warning is advisory: Fisher's exact stays exact in the low-count regime by construction. The warning flags the cells where χ² would be unreliable and Fisher's exact is the structurally correct choice — renderers can use it to highlight low-count cells whose p-value is exact-but-conservative.

### Level / Within nested-axis denominators

When a Crosstab axis has nested groupers (e.g. `rows: [region, brand]` or `columns: [wave_date, response]`), the descriptive overlay kinds (share triad + index / delta / zscore) honour two integer slots on `OverlaySpec` — `level` and `within` — that mirror the `CrosstabSpec.normalize_level` / `CrosstabSpec.normalize_within` semantics from the "Partial-depth normalization" and "Cross-axis partitioned denominator (`normalize_within`)" sections above. The overlay-side numbers compose with the crosstab-side numbers cleanly: an overlay with `(level=L, within=W)` produces byte-equivalent denominators to a crosstab `normalize=<axis>, normalize_level=L, normalize_within=W` against the same host matrix (for summable cell aggregators; recompute-class aggregators like `AGG_MEDIAN` still recompute their margins from raw rows on the crosstab side).

`level` truncates the **same axis** the overlay is centerpoint-locked to. Counting from the leaf:

- `level: 0` (default) — no truncation. The denominator is the leaf-axis margin, byte-identical to the no-`level` handler output.
- `level: N > 0` — drop `N` groupers from the right; the denominator folds across all cells whose axis tuple shares the first `(axisDepth - N)` groupers.
- `level >= axisDepth` — fires `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.

`within` fixes a prefix of the **opposite axis** at the same counting model:

- `within: 0` (default) — no opposite-axis fixing. The denominator folds across every cell in the opposite axis.
- `within: N > 0` — fix the opposite-axis prefix to the first `(oppositeDepth - N)` groupers; the denominator partitions by `(samePrefix, oppositePrefix)`.
- `within >= oppositeDepth` — fires `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.

Both slots compose independently. `OVERLAY_SHARE_OF_ROW` with `level=1` on a 2-deep row axis and `within=1` on a 2-deep column axis produces cells whose shares sum to 1.0 within each `(rowParentPrefix, colParentPrefix)` bucket — the same denominator a crosstab `normalize=row, normalize_level=1, normalize_within=1` would compute.

```json
{
  "crosstab": {
    "rows": [
      {"type": "GROUP_CATEGORY", "field": "region"},
      {"type": "GROUP_CATEGORY", "field": "brand"}
    ],
    "columns": [
      {"type": "GROUP_DATE",     "field": "wave_date", "interval": "month"},
      {"type": "GROUP_CATEGORY", "field": "response"}
    ],
    "cell": {"type": "AGG_SUM", "field": "weight", "label": "share"}
  },
  "overlays": [
    {
      "name":  "row_share_parent",
      "kind":  "OVERLAY_SHARE_OF_ROW",
      "scope": "cell",
      "ref":   {"margin": {"axis": "row"}},
      "level": 1,
      "within": 1
    }
  ]
}
```

Per-kind matrix:

| Kind | Honours `level` / `within` | Notes |
|---|---|---|
| `OVERLAY_SHARE_OF_ROW` | yes | `level` on row axis, `within` on column axis. |
| `OVERLAY_SHARE_OF_COL` | yes | `level` on column axis, `within` on row axis. |
| `OVERLAY_SHARE_OF_TOTAL` | declared but inert | The grand-axis denominator does not partition by prefix. Predict accepts in-range values; runtime ignores them. |
| `OVERLAY_INDEX_VS_MARGIN` | yes | Axis driven by `ref.margin.axis`. `level` truncates same axis; `within` fixes opposite. |
| `OVERLAY_DELTA_VS_MARGIN` | yes | Same axis dispatch as `OVERLAY_INDEX_VS_MARGIN`. |
| `OVERLAY_ZSCORE_VS_MARGIN` | margin centroid only | The `(cell - margin)` numerator honours `level` / `within`; the `sd` denominator stays full-slice (a stable z-score requires a stable variance reference). |
| `OVERLAY_CHISQ_MATRIX` / `OVERLAY_CHISQ_ROW` / `OVERLAY_CHISQ_COL` / `OVERLAY_FISHER_EXACT_CELL` | no | Implicit-margin inferential family. Non-zero `level` / `within` fire `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` — the contingency math assumes the host's row + col + grand margins inline and `level` / `within` would alter that contract. |

The math reuses the same prefix-key helpers (`processing.SameAxisPrefixDepth`, `processing.OppositeAxisPrefixDepth`, `processing.AxisKeyPrefix`) the buffered crosstab `normalize_level` / `normalize_within` path consults — the overlay slot composition lands without re-implementing the partial-depth or cross-axis-partition denominator math (per PRD § 4.C FR-C3 "Reuse existing helpers; do not duplicate math").

### Tests + overlays compose

An overlay decorates the matrix; a `TEST_*` slot rides on raw rows. Both surfaces compose cleanly in a single Request — the inferential overlay family complements (and in the `OVERLAY_CHISQ_*` / `OVERLAY_FISHER_EXACT_CELL` case mirrors) the existing `TEST_CHISQ` / `TEST_FISHER_EXACT` row-test surfaces. Use the row test for the canonical "is what I see statistically significant?" answer; use the overlay when the renderer wants the per-cell / per-row / per-column statistic surfaced alongside the matrix without a second request.

For the framework-level rules (shapes / scopes / refs taxonomy, validation rules, manifest visibility, adding a new overlay kind), see `skills/overlay-system.md`.

### Compose-overlay recipes

Crosstab is also the v1 host for the **Compose-only** overlay catalog — cross-Request comparisons that decorate one slot's matrix with a reference against another slot's matrix in the same `ComposedRequest`. The Compose surface adds a `(Reference, Targets)` slot-label pair on top of the per-Request `OverlayRef` discriminated union; every Compose-only kind passes through the slot-label resolution + key-alignment + schema-match + dict-prefix-drift gates BEFORE the per-kind handler dispatches. The dict-drift warning lives at `skills/overlay-system.md` ("Compose overlays" → "Gate order") — the by-label safe path is correct but slow, `OverlayOptions.DictPrefixFast` opts into byte-equal-prefix comparison with a per-invocation probe.

The three recipes below cover the canonical matrix-host Compose kinds. Pair them with a `compose --json` request or `pulse_compose` MCP call.

#### `OVERLAY_INDEX_VS_REF` — quarter-over-quarter index

Compare Q4 sales against Q3 sales as a per-cell index `100 * Q4 / Q3`. Both slots share the same `region × segment` crosstab shape; the resolver locks key alignment on `(rows, columns)` before computing the ratio. Layer naming follows `spec.Name` ("qoq"); the matrix arm is forced buffered through the Compose slot barrier.

```json
{
  "requests": [
    {"label": "q3", "cohort": {"filename": "q3.pulse"},
     "crosstab": {
       "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
       "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
       "cell":    {"type": "AGG_SUM", "field": "revenue", "label": "rev"}
     }},
    {"label": "q4", "cohort": {"filename": "q4.pulse"},
     "crosstab": {
       "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
       "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
       "cell":    {"type": "AGG_SUM", "field": "revenue", "label": "rev"}
     }}
  ],
  "overlays": [
    {"name": "qoq", "kind": "OVERLAY_INDEX_VS_REF", "scope": "cell",
     "reference": "q3", "targets": ["q4"]}
  ]
}
```

Each cell of the emitted overlay layer is `100 * (q4_cell / q3_cell)`. Index above `100` flags over-performing cells; below `100` flags under-performing. A zero reference cell fires `PULSE_OVERLAY_REF_ZERO` with NaN substitution; a missing reference cell (target carries a key the reference did not surface) emits the same code with `ref_missing=true` Details and the affected overlay cell stays absent. Set `Params["scale"] = 1` to read raw ratios instead of percentages.

#### `OVERLAY_PROP_Z_CELL` — A/B conversion-rate significance

Test whether the per-cell conversion rate in the treatment cohort differs significantly from the control cohort. Each cell is treated as a success count; its row margin is treated as the sample size on each side. The overlay cell carries the two-sided pooled-SE z-test p-value, computed via the same `standardNormalCDF` helper backing `TEST_PROP_Z`.

```json
{
  "requests": [
    {"label": "control", "cohort": {"filename": "control.pulse"},
     "crosstab": {
       "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
       "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
       "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
       "margins": {"rows": true}
     }},
    {"label": "treatment", "cohort": {"filename": "treatment.pulse"},
     "crosstab": {
       "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
       "columns": [{"type": "GROUP_CATEGORY", "field": "converted"}],
       "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"},
       "margins": {"rows": true}
     }}
  ],
  "overlays": [
    {"name": "ab_signif", "kind": "OVERLAY_PROP_Z_CELL", "scope": "cell",
     "reference": "control", "targets": ["treatment"]}
  ]
}
```

Each overlay cell carries the two-sided p-value as a `float64`. Renderers can colour cells with `p < 0.05` to flag significant lift / drop in conversion rate per `(region, converted=yes)` bucket. The row-margin `n` floor is canonical — missing row margins fall back to the cell value as the sample size, surfacing NaN via the pooled-SE gate rather than silently producing meaningless statistics. Degenerate `pooled ∈ {0, 1}` emits NaN + `PULSE_OVERLAY_REF_ZERO`.

#### `OVERLAY_CHISQ_VS_REF` — distribution-shift goodness-of-fit

Whole-matrix χ² test answering "does the target slot's distribution differ from the reference slot's distribution?" Scalar payload — the layer carries the χ² statistic on `OverlayPayload.Scalar` plus `OverlaySummary{Statistic, PValue, Parameters{"df"}}`. Reuses `chiSquareSurvival` so the overlay surface produces identical p-values to `TEST_CHISQ` for the same contingency.

```json
{
  "requests": [
    {"label": "expected", "cohort": {"filename": "baseline.pulse"},
     "crosstab": {
       "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
       "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
       "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"}
     }},
    {"label": "observed", "cohort": {"filename": "current.pulse"},
     "crosstab": {
       "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
       "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
       "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"}
     }}
  ],
  "overlays": [
    {"name": "shift_test", "kind": "OVERLAY_CHISQ_VS_REF", "scope": "matrix",
     "reference": "expected", "targets": ["observed"]}
  ]
}
```

The reference distribution is scaled to the target's grand total before computing per-cell expected counts: `expected[i,j] = ref_cell * (target_N / ref_N)`. `df = (cells with expected > 0) - 1`. Small p-value flags a meaningful distribution shift between the two cohorts. The canonical χ² low-count rule applies — any `expected < 5` fires one `PULSE_OVERLAY_EXPECTED_LOW` warning per layer; switch to a per-cell Fisher's-exact-style backstop on small samples. Degenerate `target_N == 0` or `ref_N == 0` emits NaN + NaN with one `PULSE_OVERLAY_REF_ZERO` warning.

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
