---
name: op-overlay-ks-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_KS_VS_POP
description: Kolmogorov-Smirnov distance + p-value comparing host Facet NUMERIC distribution against the resolved population.
type: reference
applies_to: facet
examples_tags: [overlay, facet, hypothesis-test, distribution-shape]
---

Rides on `FacetRequest.Overlays`. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.Population.Cohort` | string | (required) | Comparison-population cohort name. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

FACET — numeric arm only. Categorical host → `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (sibling `OVERLAY_CHISQ_VS_POP` covers discrete arm). Reuses `kolmogorovSurvival` backing `TEST_KS`.

## Output

SCALAR — `Payload.Scalar` carries KS `D`; `OverlaySummary{Statistic, PValue, Parameters{"n_subset", "n_pop"}}`. Layer `Baseline` unset (inferential).

## Gotchas

- Empirical-CDF reconstruction path: (1) histogram (both arms have one — preferred), (2) percentile-map (fallback), (3) Welford-only (degenerate → NaN + `PULSE_OVERLAY_REF_ZERO`).
- Population resolver retains only Welford / histogram / percentiles — raw values discarded. Set `IncludeHistogram=true` or `NumericPercentiles=[...]` on BOTH arms.
- Empty host or pop (`n_subset == 0` / `n_pop == 0`) → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Mismatched histogram edges → fall through to percentile path, else `PULSE_OVERLAY_REF_ZERO`.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inferential family).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-chisq-vs-pop`, `op-test-ks`.
