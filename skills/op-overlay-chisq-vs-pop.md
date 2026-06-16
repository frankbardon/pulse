---
name: op-overlay-chisq-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_VS_POP
description: χ² goodness-of-fit comparing host Facet subset distribution against the resolved population distribution.
type: reference
applies_to: facet
examples_tags: [overlay, facet, hypothesis-test]
---

Rides on `FacetRequest.Overlays`. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.Population` | object | (required) | `{Cohort: "<name>"}` — comparison-population cohort. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

FACET — `FacetResult` discrete arm only. Numeric host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. Pairs with descriptive `OVERLAY_INDEX_VS_POP` / `OVERLAY_ZSCORE_VS_POP`.

## Output

SCALAR — `Payload.Scalar` carries χ²; `OverlaySummary{Statistic, PValue, Parameters["df"]}` where `df = len(observed) - 1`. Layer `Baseline` unset.

## Gotchas

- `expected[v] = pop_freq(v) × subset_N`. Reuses `chiSquareSurvival` — byte-equal to `TEST_CHISQ` on the same contingency.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` warning per layer carrying count of low-expected categories.
- Empty host distribution, `subset_N == 0`, or all `pop_freq == 0` → NaN statistic + `PULSE_OVERLAY_REF_ZERO`.
- Single category (`df = 0`) → NaN p-value (chi-square undefined).
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inferential family — FacetSchema post-finalize hook; byte-identical streaming vs buffered host).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-ks-vs-pop`, `op-overlay-index-vs-pop`.
