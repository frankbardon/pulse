```yaml
name: op-overlay-chisq-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_VS_POP
description: χ² goodness-of-fit comparing host Facet subset distribution against the resolved population distribution.
type: reference
applies_to: facet
examples_tags: [overlay, facet, hypothesis-test]
```

Rides on `FacetRequest.Overlays`. Overlays decorate the host; no `Response.Components`.

## Use when

Chi-square goodness-of-fit test on a facet: checks whether a subset's category mix departs from a comparison population's mix.

Questions it answers:

- Does this segment's brand mix differ from the whole customer base's?
- Is the subset's spread of ticket types unlike the population's?

Use something else:

- `OVERLAY_KS_VS_POP` when the field is numeric.

## Params

`Scope` required, must be `group`. `Ref.Population` required — `{Cohort: "<name>"}`, the comparison-population cohort; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0`.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

FACET — discrete arm only; numeric host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Output

SCALAR — `Payload.Scalar` carries χ²; `OverlaySummary{Statistic, PValue, Parameters["df"]}`, `df` = compared categories − 1. `Baseline` unset.

## Reading the output

- `scalar`: The chi-square statistic, also in summary.statistic: the subset's category counts against the counts the population's shares predict at the subset's size, with the shares rescaled over the categories both sides show (population nulls and categories cut from a top-K listing are left out). 0 means the subset's mix matches the population's; read summary.p_value rather than the raw value.
- `summary.statistic`: The same chi-square statistic as scalar.

## Gotchas

- Pop shares renormalise over the compared categories (pop nulls / top-K tail ignored): R `chisq.test(x, p, rescale.p=TRUE)`. A subset value the pop lacks is dropped from χ², N, `df` + one `PULSE_OVERLAY_REF_ZERO`. Byte-equal survival to the χ² test.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer, carrying the low-expected category count.
- Empty host distribution, empty population, or no compared category → NaN statistic + `PULSE_OVERLAY_REF_ZERO`. Single category (`df = 0`) → NaN p-value.
- Buffered (inferential — FacetSchema post-finalize hook; byte-identical streaming vs buffered).

## See

- Skills: [`overlay-system`](overlay-system.md), [`facet-design`](facet-design.md), [`op-overlay-ks-vs-pop`](op-overlay-ks-vs-pop.md), [`op-overlay-index-vs-pop`](op-overlay-index-vs-pop.md).
