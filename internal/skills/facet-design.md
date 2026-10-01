---
name: facet-design
description: FacetSchema endpoint — per-field summaries, histograms, additive contribution counts, streamability tradeoffs, four FACET-host overlay kinds against Population. Per-OVERLAY math in op-overlay-* atomics.
type: guide
kind: design
applies_to: facet
covers: [FacetRequest, FacetSchema, OVERLAY_INDEX_VS_POP, OVERLAY_ZSCORE_VS_POP, OVERLAY_CHISQ_VS_POP, OVERLAY_KS_VS_POP]
---

# Facet design

Two entry points. `pulse.Facet(ctx, path, field)` returns distinct values. `pulse.FacetSchema(ctx, *FacetRequest)` is the rich multi-field endpoint — counts, null tallies, streaming numeric stats, optional percentiles, fixed-width histograms, additive contribution counts, FACET-host overlays.

```jsonc
{
  "cohort":              {"filename": "cohort.pulse"},
  "fields":              ["region", "age", "is_active"],
  "filterers":           [{"type":"FILTER_INCLUDE","field":"country","values":["US"]}],
  "additive_fields":     ["region"],
  "discrete_top_k":      50,
  "numeric_percentiles": [0.5, 0.95, 0.99],
  "include_histogram":   true,
  "histogram_bins":      20,
  "histogram_range":     [0.0, 100.0]
}
```

`FacetResult.Fields[i]` is one `FacetField` per `fields` entry; `FacetResult.Additive[F]` is one per `AdditiveFields` entry. Each `FacetField` has `Kind ∈ {discrete, numeric}`, source field type, description, null count, and either `Discrete` (sorted desc by count, ties asc by value-string) or `Numeric` (count/sum/min/max/mean/stddev via Welford; percentiles + histogram only when requested).

## Discrete vs numeric dispatch

Field type drives dispatch: `categorical_*` + `packed_bool` ⇒ discrete (dictionary fast path; bool emits `"true"`/`"false"`). All numeric / date / decimal ⇒ numeric. `numeric_percentiles` on a non-numeric field is a no-op (predict warning). Histograms require a numeric field plus caller-supplied `histogram_range` — no two-pass auto-range.

## TopK, percentiles, histograms

`discrete_top_k > 0` keeps top K by count (desc, ties asc), sets `Discrete.TruncatedAt = distinct - K`, emits a warning. `numeric_percentiles` ∈ `(0,1)` force the buffered path on requested fields (full sort + linear interp). Keys serialise as `p<int>` or `p%g`. Semantics match `AGG_PERCENTILE` to float64. Histograms stream into fixed-width buckets; bin width `(max-min)/bins`; values outside `[min,max]` drop from the histogram (still feed mean/stddev/count). `histogram_bins` defaults to 20, caps at 256.

## Additive contribution counts

For each `AdditiveFields` entry F, the engine builds a scope filter = base `Filterers` minus every clause targeting F, then runs a parallel discrete accumulator. Result at `FacetResult.Additive[F]`. Answers "if I added V to my filter on F, how many records survive?" per V — no round-trip per value. When no base clause names F, the scope filter equals the base filter.

`FILTER_EXPRESSION` restriction: the scope-stripper cannot remove a clause hidden inside an expression body — requests referencing an additive field inside `FILTER_EXPRESSION` are rejected with `SERVICE_VALIDATION`. Use discrete `FILTER_INCLUDE`/`FILTER_EXCLUDE` instead.

## Streamability

Single-pass when: no `numeric_percentiles`; AND `include_histogram=false` OR `histogram_range` supplied; AND every filterer is row-local. Buffered otherwise. Manifest: `manifest.facet.streamable_conditions`.

## FACET-host overlays

`FacetRequest.Overlays` decorates with population-comparison statistics. Each `OverlaySpec` produces one `OverlayLayer` in `FacetResult.Overlays` in slot order. Four kinds, all against a `Ref.Population` reference cohort:

| Kind | Shape | Stream | Host arm | Computes |
|---|---|---|---|---|
| `OVERLAY_INDEX_VS_POP` | per-value series | yes | discrete or numeric | `(subset_freq / pop_freq) * 100` per value/bin |
| `OVERLAY_ZSCORE_VS_POP` | per-value series | yes | discrete or numeric | `(subset_freq - pop_mean) / pop_sd` |
| `OVERLAY_CHISQ_VS_POP` | scalar | buffered | discrete only | χ² goodness-of-fit + df + p-value |
| `OVERLAY_KS_VS_POP` | scalar | buffered | numeric only | KS D-statistic + asymptotic p-value |

Common contract: `Ref.Population` REQUIRED (`Cohort` names the comparison `.pulse`); any other family ⇒ `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Scope = group` only; `Level = 0`, `Within = 0` (non-zero ⇒ `_LEVEL_OUT_OF_RANGE`). Host-field via `OverlaySpec.Params["field"]`; omit only when FacetRequest has one Field. Unknown field ⇒ `_REF_INCOMPATIBLE_WITH_SHAPE` with `{field, available_fields}`. `CHISQ_VS_POP` against numeric host ⇒ `_REF_INCOMPATIBLE_WITH_SHAPE`; `KS_VS_POP` against categorical ⇒ `_SCOPE_UNSUPPORTED`. Runtime recurses `FacetSchema` against the population, forwarding `NumericPercentiles`/`IncludeHistogram`.

Mixing streamable + buffered kinds forces the orchestrator buffered; descriptive math stays byte-equivalent. Same cohort may serve as host + population (recursion strips `Filterers`). Warning codes: `PULSE_OVERLAY_REF_ZERO` (per-entry for INDEX/ZSCORE; once per layer for degenerate populations on CHISQ/KS), `PULSE_OVERLAY_EXPECTED_LOW` (CHISQ_VS_POP when any expected cell `< 5`). Service wiring at `service/facet_overlay.go`; predict validator at `descriptor.ValidateFacetOverlays`.

## Picking FacetSchema vs Process

Distinct values of one field, no counts ⇒ `pulse.Facet`. Per-value counts of N fields ⇒ `FacetSchema`. Counts grouped by N keys ⇒ `Process` (`groups`+`AGG_COUNT`). Per-group numeric stats ⇒ `Process` (`groups`+`AGG_AVERAGE`/`AGG_STDDEV`). Additive counts ⇒ `FacetSchema` with `additive_fields`. FacetSchema is not a grouper — cross-tabs belong in `Process`.

## See

- `skills/overlay-system.md` — overlay framework + per-shape contract.
- `skills/aggregation-design.md` — `AGG_PERCENTILE` semantics.
- `skills/statistical-testing.md` — χ² / KS row-test surfaces (overlay parity).
- `skills/label-display.md` — labels on `FacetField` values.
