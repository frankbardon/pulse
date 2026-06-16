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

Rides on `FacetRequest.Overlays`. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.Population.Cohort` | string | (required) | Comparison-population cohort name. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

FACET — `FacetResult` (discrete or numeric arm). Streamable sibling to `OVERLAY_INDEX_VS_POP` — pairs as the two streamable FACET kinds; one Facet pass.

## Output

SERIES — one `SeriesEntry` per host value in payload order, carrying z-score on `Summary.Statistic`. Layer `Baseline = 0` (z-score centerpoint).

## Gotchas

- Discrete: `(subset_freq - pop_freq) / sd_pop` where `sd_pop = FacetPopulationView.DiscreteFrequencyStdev()` (population Welford SD across per-category frequencies).
- Numeric: Welford `(mean, sd)` from `FacetPopulationView` — per-bin center standardised against population's Welford triple.
- `sd_pop == 0` → ONE `PULSE_OVERLAY_REF_ZERO` per affected entry + SKIP entry (Statistic unset).
- Absent population entry → same warning + skip behavior.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Streamable — post-finalize fold over already-materialised host + population view; carrier not widened.

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-index-vs-pop`, `op-overlay-chisq-vs-pop`.
