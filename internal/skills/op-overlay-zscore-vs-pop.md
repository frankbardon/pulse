---
name: op-overlay-zscore-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_POP
description: Per-value population-comparison z-score for a facet host — (subset_freq − pop_freq) / sd_pop.
type: reference
applies_to: facet
examples_tags: [overlay, facet, outlier-detection, streaming-friendly]
---

Rides on facet `overlays`. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | The facet `fields` entry to score; required with two or more (else `PULSE_OVERLAY_PARAM_MISSING`); an unlisted one → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. |

`scope` must be `group`. `ref.population.cohort` (string, required) — comparison-population cohort name. `level`/`within` must be `0`. Other `ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

FACET — a discrete field, or a numeric one with `include_histogram: true`. Streamable, as the population index.

## Output

SERIES — one entry per host value (or histogram bin) in payload order, the z-score on `summary.statistic`. Layer `summary.baseline` = `0`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Discrete: `(subset_freq − pop_freq) / sd_pop`, `sd_pop` the population SD across the population's per-category frequencies.
- Numeric: each bin centre standardised against the population's mean and SD; without a histogram, zero entries.
- `sd_pop == 0` → ONE `PULSE_OVERLAY_REF_ZERO` per affected entry + no `statistic` on it. An absent population entry reads as `pop_freq = 0` and IS emitted, no warning.
- Streamable (a fold after the facet finalizes).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-index-vs-pop`, `op-overlay-chisq-vs-pop`.
