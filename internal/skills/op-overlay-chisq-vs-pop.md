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

Rides on `FacetRequest.Overlays`. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` required, must be `group`. `Ref.Population` required — `{Cohort: "<name>"}`, the comparison-population cohort; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0`.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched (`multiple-comparisons`).
<!-- /feature -->

## Host shape

FACET — discrete arm only; numeric host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Output

SCALAR — `Payload.Scalar` carries χ²; `OverlaySummary{Statistic, PValue, Parameters["df"]}`, `df` = compared categories − 1. `Baseline` unset.

## Gotchas

- Pop shares renormalise over the compared categories (pop nulls / top-K tail ignored): R `chisq.test(x, p, rescale.p=TRUE)`. A subset value the pop lacks is dropped from χ², N, `df` + one `PULSE_OVERLAY_REF_ZERO`. Byte-equal survival to the χ² test.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer, carrying the low-expected category count.
- Empty host distribution, empty population, or no compared category → NaN statistic + `PULSE_OVERLAY_REF_ZERO`. Single category (`df = 0`) → NaN p-value.
- Buffered (inferential — FacetSchema post-finalize hook; byte-identical streaming vs buffered).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-ks-vs-pop`, `op-overlay-index-vs-pop`.
