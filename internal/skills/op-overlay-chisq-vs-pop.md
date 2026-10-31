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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | On a SERIES host, the OUTPUT LABEL of the aggregation to score (explicit label, else TYPE_field) or one expanded element column `"<label>[<element label>]"`; absent, the first aggregation. |

`Scope` required, must be `group`. `Ref.Population` required — `{Cohort: "<name>"}`, the comparison-population cohort; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0`.

## Host shape

FACET — discrete arm only; numeric host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Output

SCALAR — `Payload.Scalar` carries χ²; `OverlaySummary{Statistic, PValue, Parameters["df"]}`, `df` = compared categories − 1. `Baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Pop shares renormalise over the compared categories (pop nulls ignored): R `chisq.test(x, p, rescale.p=TRUE)`. A subset value the pop lacks is dropped from χ², N, `df` + one `PULSE_OVERLAY_REF_ZERO`. Byte-equal survival to the χ² test.
- Complete host listing (`truncated_at` 0): a pop category the subset lacks enters at observed 0 (counts in `df` + the low-expected check). `discrete_top_k` truncation → listed values only (an unlisted category is unknown, not 0).
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer, carrying the low-expected category count.
- Empty host distribution, empty population, or no compared category → NaN statistic + `PULSE_OVERLAY_REF_ZERO`. Single category (`df = 0`) → NaN p-value.
- Buffered (inferential — FacetSchema post-finalize hook; byte-identical streaming vs buffered).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-ks-vs-pop`, `op-overlay-index-vs-pop`.
