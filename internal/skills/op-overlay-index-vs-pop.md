---
name: op-overlay-index-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_POP
description: Per-value population-comparison index for a Facet host (subset_freq / pop_freq × 100).
type: reference
applies_to: facet
examples_tags: [overlay, facet, comparison]
---

Rides on `FacetRequest.Overlays`. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` must be `group`. `Ref.Population.Cohort` (string, required) — comparison-population cohort name. `Level`/`Within` must be `0`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

FACET — `FacetResult`, discrete arm or numeric arm with `IncludeHistogram=true`. Population view via `processing.FacetPopulationView`.

## Output

SERIES — one `SeriesEntry` per host value in payload order (categorical walks `FacetDiscrete.Values`, numeric walks histogram bins), carrying `index = subset_freq / pop_freq × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- `pop_freq == 0`, or a value absent from the population dict (treated as zero pop_freq) → ONE `PULSE_OVERLAY_REF_ZERO` per affected entry + SKIP it (`Statistic` unset).
- Numeric host without `IncludeHistogram` → zero entries (no per-value buckets).
- Unknown population field → `PULSE_OVERLAY_REF_UNKNOWN` from the resolver.
- Streamable — post-finalize fold; byte-identical streaming vs buffered.

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-zscore-vs-pop`, `op-overlay-chisq-vs-pop`.
