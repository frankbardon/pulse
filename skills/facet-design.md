---
name: facet-design
description: Design rich multi-field cohort summaries with the FacetSchema endpoint — per-value counts, null tallies, numeric statistics, percentiles, fixed-width histograms, and additive contribution counts for builder-UI flows.
type: guide
applies_to: facet
---

# Facet Design

<skill_overview>
Pulse ships two facet entry points. `pulse.Facet(ctx, path, field)` is the simple distinct-values returner — categorical fields short-circuit through the dictionary, numeric fields stream the file collecting unique values. `pulse.FacetSchema(ctx, *FacetRequest)` is the rich multi-field endpoint that returns counts, null tallies, streaming numeric statistics, optional percentiles, optional fixed-width histograms, and optional "additive" contribution counts.

Use FacetSchema when you need any of: more than one field per call, counts (not just distinct values), null tallies, numeric statistics, percentiles, histograms, or additive contribution counts. Otherwise the simpler Facet is enough.
</skill_overview>

<reference>
## Request shape

```jsonc
{
  "cohort": {"filename": "cohort.pulse"},
  "fields": ["region", "age", "is_active"],

  // Optional row-level filters (same shape as Request.Filterers).
  "filterers": [
    {"type": "FILTER_INCLUDE", "field": "country", "values": ["US"]}
  ],

  // Optional additive-contribution analysis. For every additive field,
  // the engine accumulates a parallel discrete count with that field's
  // own filter clauses stripped — answers "if I added value V to my
  // filter on F, how many records survive?" for every V independently.
  "additive_fields": ["region"],

  // Cap distinct values returned per discrete field. Zero = no cap.
  // When a field is capped, FacetField.Discrete.TruncatedAt carries the
  // count of values dropped.
  "discrete_top_k": 50,

  // Optional numeric percentiles. Strictly in (0, 1). Forces the
  // buffered execution path for the requested numeric fields.
  "numeric_percentiles": [0.5, 0.95, 0.99],

  // Optional fixed-width histogram. Streams cleanly when
  // histogram_range is supplied (Option A — caller-known bounds).
  "include_histogram": true,
  "histogram_bins": 20,
  "histogram_range": [0.0, 100.0]
}
```

## Response shape

`FacetResult.Fields` carries one `FacetField` per requested field.
`FacetResult.Additive` carries one `FacetField` per `AdditiveFields`
entry. Each `FacetField` has `Kind = "discrete"|"numeric"`, the field's
type name, the schema description, the null count, and either a
`Discrete` or `Numeric` summary.

Discrete summaries sort descending by count (ties broken ascending by
value-string for determinism). Numeric summaries always carry
count/sum/min/max/mean/stddev; percentiles and histogram are populated
only when requested.

## Discrete vs numeric semantics

Field type drives the dispatch:

| Field type | Summary kind | Notes |
|---|---|---|
| `categorical_u8/u16/u32` | discrete | Dictionary fast path: count by dict id, resolve to string at finalize. |
| `packed_bool` | discrete | Emits `"true"` and `"false"` counts; `null_count` carries the bitmap-flagged missing tally when the field is `Nullable: true`. |
| `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `decimal128` | numeric | Welford online for mean/stddev; min/max/sum tracked alongside. Decimal fields convert via `Decimal128.Float64(scale)`. Null cells (bitmap-flagged) are excluded from every statistic. |

Asking for `numeric_percentiles` on a non-numeric field is a no-op
(the `ValidateFacet` predict surface emits a warning). Histograms work
only on numeric fields and require a caller-supplied `histogram_range`.

## TopK truncation

When `discrete_top_k > 0` and a field has more distinct non-null values
than the cap, the response sorts by count descending (ties ascending by
value string), keeps the top K, and sets `TruncatedAt` to
`distinct_count - K`. A warning lands on `FacetResult.Warnings` so
callers can surface "showing 50 of 4823" affordances.

## Percentiles

Listed values must be strictly in `(0, 1)`. They force a buffered path
for the requested numeric fields (the engine collects every non-null
value into a `[]float64`, sorts once, and linearly interpolates each
percentile). Percentile keys are formatted as `p<integer>` when the
value maps cleanly (e.g. `"p50"`, `"p95"`); other fractions use `p%g`
(e.g. `"p33.3"`).

Percentile semantics match `AGG_PERCENTILE` to float64 precision —
calling FacetSchema with `numeric_percentiles=[0.5]` returns the same
value `AGG_PERCENTILE` would for the same field.

## Histograms

`include_histogram=true` requires `histogram_range=[min,max]` so the
engine can stream values into fixed-width buckets in a single pass.
Auto-range (the two-pass alternative) is intentionally not supported —
callers know their domain better than the engine, and the single-pass
contract is more valuable than the convenience.

Bin width is `(max-min)/bins`. Values outside `[min, max]` are dropped
from the histogram (they still contribute to count/mean/stddev). Values
exactly at `max` land in the last bin. `histogram_bins` defaults to 20
when zero, capped at 256.

## Additive contribution counts

Use case: a builder UI shows multi-field filters and asks "if I add
value V to my filter on field F, how many records survive?".

For each `AdditiveFields` entry F, the engine builds a *scope filter*
= the base `Filterers` with every clause that targets F removed, then
runs a parallel discrete accumulator over the same row stream. The
result lands under `FacetResult.Additive[F]`. Counts answer the
question *for every distinct value of F independently* without the UI
making N additional round-trips.

When the base filter has no clauses naming F, the scope filter equals
the base filter and the additive accumulator mirrors the base
accumulator — useful for symmetric UI flows.

**FILTER_EXPRESSION restriction.** The scope-stripping cannot
semantically remove a clause hidden inside a `FILTER_EXPRESSION`
predicate body. Requests that reference an additive field inside
`FILTER_EXPRESSION` are rejected up front with `SERVICE_VALIDATION`;
express the predicate as discrete `FILTER_INCLUDE`/`FILTER_EXCLUDE`
filterers instead.

## Streamability

Single-pass when:
- no `numeric_percentiles` requested for any numeric field, AND
- `include_histogram=false` or `histogram_range` is supplied, AND
- every base filterer is row-local streamable
  (`FILTER_INCLUDE`, `FILTER_EXCLUDE`, `FILTER_RANGE`,
  `FILTER_NULL`, `FILTER_EXPRESSION`).

Buffered when:
- any numeric field has percentiles requested, OR
- a custom non-streamable filter is in play.

The manifest exposes the rule set under `manifest.facet.streamable_conditions`
so LLM clients can reason about cost.

## Filter scope

`Filterers` reuses the existing `types.Filterer` shape. Same evaluation
pipeline as `Process` — `FILTER_INCLUDE`, `FILTER_EXCLUDE`,
`FILTER_RANGE`, `FILTER_NULL`, `FILTER_EXPRESSION`. Filters apply to the base accumulators
only; additive accumulators see the per-field scope filter described
above.

## When to prefer FacetSchema over a Process request

| Want | Use |
|---|---|
| Distinct values of one field, no counts | `pulse.Facet` |
| Per-value counts of one field | `FacetSchema` |
| Per-value counts of N fields in one round-trip | `FacetSchema` |
| Counts grouped by N keys (cross-tab) | `Process` with `groups` + `AGG_COUNT` |
| Numeric statistics across the whole cohort | `FacetSchema` |
| Numeric statistics per group | `Process` with `groups` + `AGG_AVERAGE`/`AGG_STDDEV` |
| "If I added V to my filter on F, what survives?" | `FacetSchema` with `additive_fields: [F]` |

`FacetSchema` is intentionally not a grouper — it answers per-field
distribution questions. Cross-tabs belong in `process` with a `groups`
operator.
</reference>
