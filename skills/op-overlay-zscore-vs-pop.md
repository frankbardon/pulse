---
name: op-overlay-zscore-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_POP
description: Per-value population-comparison z-score for a Facet host — (subset_freq − pop_freq) / sd_pop.
type: reference
applies_to: facet
examples_tags: [overlay, facet, outlier-detection, streaming-friendly]
---

Rides on `FacetRequest.Overlays`. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` must be `group`. `Ref.Population.Cohort` (string, required) — comparison-population cohort name. `Level`/`Within` must be `0`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

FACET — `FacetResult`, discrete or numeric arm. Streamable sibling to `OVERLAY_INDEX_VS_POP`: the two streamable FACET kinds, one Facet pass.

## Output

SERIES — one `SeriesEntry` per host value in payload order carrying the z-score on `Summary.Statistic`. Layer `Baseline = 0`.

## Gotchas

- Discrete: `(subset_freq - pop_freq) / sd_pop`, `sd_pop = FacetPopulationView.DiscreteFrequencyStdev()` (population Welford SD across per-category frequencies).
- Numeric: per-bin centre standardised against the population's Welford `(mean, sd)` from `FacetPopulationView`.
- `sd_pop == 0`, or an absent population entry → ONE `PULSE_OVERLAY_REF_ZERO` per affected entry + SKIP it (`Statistic` unset).
- Streamable — post-finalize fold over the materialised host + population view; carrier not widened.

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-index-vs-pop`, `op-overlay-chisq-vs-pop`.
