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
