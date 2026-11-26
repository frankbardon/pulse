---
name: op-overlay-chisq-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_VS_POP
description: χ² goodness-of-fit comparing the facet subset distribution against the resolved population distribution.
type: reference
applies_to: facet
examples_tags: [overlay, facet, hypothesis-test]
---

Rides on facet `overlays`. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | The facet `fields` entry to score; required with two or more (else `PULSE_OVERLAY_PARAM_MISSING`); an unlisted one → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. |

`scope` required, must be `group`. `ref.population` required — `{"cohort": "<name>"}`, the comparison-population cohort; other `ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `level` / `within` must be `0`.

## Host shape

FACET — discrete arm only; numeric host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Output

SCALAR — `payload.scalar` carries χ²; layer `summary` `statistic`, `p_value`, `parameters.df` = compared categories − 1. `summary.baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Pop shares renormalise over the compared categories (pop nulls ignored): R `chisq.test(x, p, rescale.p=TRUE)`. A subset value the pop lacks is dropped from χ², N, `df` + one `PULSE_OVERLAY_REF_ZERO` per value. p equals the χ² test's.
- Complete host listing (`truncated_at` 0): a pop category the subset lacks enters at observed 0 (counts in `df` + the low-expected check). `discrete_top_k` truncation → listed values only (an unlisted category is unknown, not 0).
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer, carrying the low-expected category count.
- Empty host distribution, empty population, or no compared category → NaN statistic + `PULSE_OVERLAY_REF_ZERO`. Single category (`df = 0`) → NaN p-value.
- Buffered (inferential).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-ks-vs-pop`, `op-overlay-index-vs-pop`.
