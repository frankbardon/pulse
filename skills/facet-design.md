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

## Facet overlays

`FacetRequest.Overlays` is the additive decoration surface for
population-comparison statistics. Each `OverlaySpec` produces one
`OverlayLayer` in `FacetResult.Overlays` in matching order; an empty
slot keeps the JSON output byte-identical to the pre-overlay shape
(`omitempty`). Four kinds ship with the catalog (E5), each one against
a `Ref.Population` reference family:

| Kind | Shape | Streamable | Ref family | Host arm | What it computes |
|---|---|---|---|---|---|
| `OVERLAY_INDEX_VS_POP` | per-value series | yes | `Population` | discrete or numeric | `(subset_freq / pop_freq) * 100` per value / bin |
| `OVERLAY_ZSCORE_VS_POP` | per-value series | yes | `Population` | discrete or numeric | `(subset_freq - pop_mean) / pop_sd` per value |
| `OVERLAY_CHISQ_VS_POP` | scalar statistic | buffered | `Population` | discrete only | χ² goodness-of-fit + df + p-value |
| `OVERLAY_KS_VS_POP` | scalar statistic | buffered | `Population` | numeric only | Kolmogorov-Smirnov D-statistic + asymptotic p-value |

The streamability split — descriptive kinds (INDEX, ZSCORE) stream;
inferential kinds (CHISQ, KS) buffer — is the same one declared by
`types.OverlayStreamability` and surfaced through
`Manifest.Overlays[kind].Streamable`. PRD §2 "Non-Goals" pins the
inferential kinds to buffered: even though both finalize handlers
consume only post-finalize host state, mixing them into a streamable
FacetRequest forces the entire request to the buffered path through
`processing.canStreamOverlays`. INDEX_VS_POP and ZSCORE_VS_POP keep
streaming alongside the per-value Welford / count accumulators.

Every kind shares the same contract:

- `Ref.Population` REQUIRED — `Ref.Population.Cohort` names the
  comparison-cohort `.pulse` file. Any other ref-family pointer fires
  `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- `Scope=GROUP` only.
- `Level=0`, `Within=0` — population comparison is a single-value
  lookup, not an axis prefix; non-zero values fire
  `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.
- Host-field selection via `OverlaySpec.Params["field"]`. When the
  FacetRequest declares exactly one Field that slot may be omitted;
  multi-field FacetRequests require it. Unknown field names fire
  `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` with the
  `{field, available_fields}` detail map.
- Host-arm mismatch fires at predict time:
  - `OVERLAY_CHISQ_VS_POP` against a numeric host →
    `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (χ² goodness-of-fit
    needs categorical buckets to form the observed × expected
    contingency).
  - `OVERLAY_KS_VS_POP` against a categorical host →
    `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (KS is defined on a CDF, which
    is meaningless on unordered categories).
- The runtime resolves the population FacetResult by recursing
  `FacetSchema` against `Ref.Population.Cohort` with
  `NumericPercentiles` / `IncludeHistogram` forwarded from the host
  request so the per-kind handlers see the expected payload shape.

The service-layer wiring lives at `service/facet_overlay.go`; per-kind
handlers live alongside the rest of the overlay catalog in
`processing/overlay_*.go`. The predict-time validator is
`descriptor.ValidateFacetOverlays` (`descriptor/overlay_facet.go`),
invoked automatically from `descriptor.ValidateFacet`.

### Warning codes

Two warning-class codes fire from the four Facet kinds at runtime
(never errors — the overlay layer is still emitted with NaN where the
math is undefined):

- `PULSE_OVERLAY_REF_ZERO` — the population reference resolved cleanly
  but the denominator is zero. Emitted by INDEX_VS_POP / ZSCORE_VS_POP
  per affected entry, and by CHISQ_VS_POP / KS_VS_POP once per layer
  when the entire population is degenerate. The Details map carries
  `{kind, value}` (per-value variants) or `{kind, reason}` (scalar
  variants).
- `PULSE_OVERLAY_EXPECTED_LOW` — emitted by `OVERLAY_CHISQ_VS_POP`
  when any expected cell count drops below 5 (the χ² asymptotic
  approximation degrades). The Details map identifies the offending
  values. The p-value is still emitted; the warning is advisory, not
  fatal.

### Recipes

One Request fragment per kind. Each is a minimal `FacetRequest` body
that targets `cohort.pulse` against an unfiltered `population.pulse`
baseline. The full request includes the standard envelope; only the
slot-specific bits are shown.

**`OVERLAY_INDEX_VS_POP` — per-value subset-vs-population ratio.**

```json
{
  "cohort": {"filename": "cohort.pulse"},
  "fields": ["category"],
  "filterers": [{"type": "FILTER_INCLUDE", "field": "region", "values": ["west"]}],
  "overlays": [
    {
      "kind": "OVERLAY_INDEX_VS_POP",
      "scope": "group",
      "ref": {"population": {"cohort": "population.pulse"}}
    }
  ]
}
```

`FacetResult.Overlays[0].Discrete[v].Value == 100` ⇒ subset proportion
equals population proportion; `>100` over-represented; `<100`
under-represented.

**`OVERLAY_ZSCORE_VS_POP` — per-value z-score against population mean
+ sd.**

```json
{
  "cohort": {"filename": "cohort.pulse"},
  "fields": ["amount"],
  "filterers": [{"type": "FILTER_RANGE", "field": "year", "min": 2024, "max": 2024}],
  "overlays": [
    {
      "kind": "OVERLAY_ZSCORE_VS_POP",
      "scope": "group",
      "ref": {"population": {"cohort": "population.pulse"}}
    }
  ]
}
```

Numeric or discrete host. Each entry's z-score expresses how many
population standard deviations away from the population mean the
subset frequency / mean sits.

**`OVERLAY_CHISQ_VS_POP` — scalar χ² goodness-of-fit (discrete host
only).**

```json
{
  "cohort": {"filename": "cohort.pulse"},
  "fields": ["category"],
  "filterers": [{"type": "FILTER_INCLUDE", "field": "region", "values": ["west"]}],
  "overlays": [
    {
      "kind": "OVERLAY_CHISQ_VS_POP",
      "scope": "group",
      "ref": {"population": {"cohort": "population.pulse"}}
    }
  ]
}
```

`FacetResult.Overlays[0].Summary` carries `Statistic`, `PValue`, and
`Parameters{"df", "n_subset"}`. Same regularised-gamma helper as
`TEST_CHISQ` — byte-identical p-values for the same contingency. Watch
for `PULSE_OVERLAY_EXPECTED_LOW` warnings; below ~5-per-cell the
asymptotic approximation is unreliable. Use it as a goodness-of-fit
badge rather than a publication-grade test in those regimes.

**`OVERLAY_KS_VS_POP` — scalar Kolmogorov-Smirnov distance (numeric
host only).**

```json
{
  "cohort": {"filename": "cohort.pulse"},
  "fields": ["amount"],
  "filterers": [{"type": "FILTER_RANGE", "field": "year", "min": 2024, "max": 2024}],
  "include_histogram": true,
  "histogram_bins": 50,
  "histogram_range": [0.0, 1000.0],
  "overlays": [
    {
      "kind": "OVERLAY_KS_VS_POP",
      "scope": "group",
      "ref": {"population": {"cohort": "population.pulse"}}
    }
  ]
}
```

`Summary` carries `Statistic` (D = max CDF gap), `PValue` (asymptotic
two-sample KS), and `Parameters{"n_subset", "n_pop"}`. Categorical
fields fire `PULSE_OVERLAY_SCOPE_UNSUPPORTED` at predict time — KS is
undefined on unordered categories.

### Combining kinds

Slot order in `Overlays` matches slot order in `Overlays` on the
response. A common pairing — descriptive per-value index alongside an
inferential goodness-of-fit badge — is one Request, two overlay slots:

```json
{
  "cohort": {"filename": "cohort.pulse"},
  "fields": ["category"],
  "filterers": [{"type": "FILTER_INCLUDE", "field": "region", "values": ["west"]}],
  "overlays": [
    {"kind": "OVERLAY_INDEX_VS_POP", "scope": "group",
     "ref": {"population": {"cohort": "population.pulse"}}},
    {"kind": "OVERLAY_CHISQ_VS_POP", "scope": "group",
     "ref": {"population": {"cohort": "population.pulse"}}}
  ]
}
```

`FacetResult.Overlays[0]` carries the per-value index series;
`FacetResult.Overlays[1]` carries the scalar χ² statistic. Mixing
streamable + buffered kinds forces the whole request to the buffered
path; the descriptive layer is still byte-equivalent — the gate is on
the orchestrator, not the math. The same `.pulse` cohort may serve as
both host and population (`population.pulse == cohort.pulse`) — the
recursion produces the unfiltered baseline since the population
FacetRequest strips `Filterers`.

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
