---
name: op-overlay-index-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_POP
description: Per-value population-comparison index for a facet host (subset_freq / pop_freq × 100).
type: reference
applies_to: facet
examples_tags: [overlay, facet, comparison]
---

Rides on facet `overlays`. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | The facet `fields` entry to score; required with two or more (else `PULSE_OVERLAY_PARAM_MISSING`); an unlisted one → `PULSE_OVERLAY_FIELD_UNKNOWN`. |

`scope` must be `group`. `ref.population.cohort` (string, required) — comparison-population cohort name. `level`/`within` must be `0`. Other `ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

FACET — a discrete field, or a numeric one with `include_histogram: true` (bins compared bin by bin).

## Output

SERIES — one entry per host value in payload order (discrete values, or histogram bins), `summary.statistic` = `subset_freq / pop_freq × 100`. Layer `summary.baseline` = `100`.

<!-- generated: contract -->

## Gotchas

- `pop_freq == 0`, or a value absent from the population dict (treated as zero pop_freq) → ONE `PULSE_OVERLAY_REF_ZERO` per affected entry + no `statistic` on it.
- Numeric host without `include_histogram` → zero entries.
- Unknown population field → `PULSE_OVERLAY_REF_UNKNOWN` from the resolver.
- Streamable (a fold after the facet finalizes).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-zscore-vs-pop`, `op-overlay-chisq-vs-pop`.
