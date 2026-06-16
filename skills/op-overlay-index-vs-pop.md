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

Rides on `FacetRequest.Overlays`. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.Population.Cohort` | string | (required) | Comparison-population cohort name. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

FACET — `FacetResult` (discrete arm or numeric arm with `IncludeHistogram=true`). Population view resolved via `processing.FacetPopulationView`.

## Output

SERIES — one `SeriesEntry` per host value in payload order. Categorical: walks `FacetDiscrete.Values`; numeric: walks histogram bins. Each entry carries `index = subset_freq / pop_freq × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- `pop_freq == 0` for some value → ONE `PULSE_OVERLAY_REF_ZERO` warning per affected entry + SKIP that entry (Statistic unset).
- Value absent from population dict → treated as zero-pop_freq (skip + warning).
- Numeric host without `IncludeHistogram` → zero entries (no per-value buckets to index).
- Unknown population field → resolver returns `PULSE_OVERLAY_REF_UNKNOWN`.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Streamable — post-finalize fold; byte-identical streaming vs buffered.

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-zscore-vs-pop`, `op-overlay-chisq-vs-pop`.
