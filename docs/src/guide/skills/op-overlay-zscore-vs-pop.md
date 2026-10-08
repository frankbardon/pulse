```yaml
name: op-overlay-zscore-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_POP
description: Per-value population-comparison z-score for a Facet host — (subset_freq − pop_freq) / sd_pop.
type: reference
applies_to: facet
examples_tags: [overlay, facet, outlier-detection, streaming-friendly]
```

Rides on `FacetRequest.Overlays`. Overlays decorate the host; no `Response.Components`.

## Use when

Puts each category's share gap with a comparison population on a z-score scale; for numeric bins it says nothing about the subset.

Questions it answers:

- Which answers does this segment pick more or less often than the comparison population, relative to how much answer shares vary?
- Which categories stand out in this region compared with the whole business?

## Params

`Scope` must be `group`. `Ref.Population.Cohort` (string, required) — comparison-population cohort name. `Level`/`Within` must be `0`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

FACET — `FacetResult`, discrete or numeric arm. Streamable sibling to the population index: the two streamable FACET kinds, one Facet pass.

## Output

SERIES — one `SeriesEntry` per host value in payload order carrying the z-score on `Summary.Statistic`. Layer `Baseline = 0`.

## Reading the output

- `summary.statistic`: In each series entry, for a categorical field: the category's share in the subset minus its share in the population, divided by the standard deviation of the population's shares across categories. For a numeric field: a histogram bin's centre against the population's mean, in population standard deviations.
  - Sign: positive means the value sits above its centre; negative means the value sits below its centre.

## Gotchas

- Discrete: `(subset_freq - pop_freq) / sd_pop`, `sd_pop = FacetPopulationView.DiscreteFrequencyStdev()` (population Welford SD across per-category frequencies).
- Numeric: per-bin centre standardised against the population's Welford `(mean, sd)` from `FacetPopulationView`.
- `sd_pop == 0` → ONE `PULSE_OVERLAY_REF_ZERO` per affected entry + SKIP it (`Statistic` unset). An absent population entry reads as `pop_freq = 0` and IS emitted, no warning.
- Streamable — post-finalize fold over the materialised host + population view; carrier not widened.

## See

- Skills: [`overlay-system`](overlay-system.md), [`facet-design`](facet-design.md), [`op-overlay-index-vs-pop`](op-overlay-index-vs-pop.md), [`op-overlay-chisq-vs-pop`](op-overlay-chisq-vs-pop.md).
