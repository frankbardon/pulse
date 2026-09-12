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

Rides on `FacetRequest.Overlays`. Overlays decorate the host; they emit no `Response.Components`.

## Params

`Scope` required, must be `group`. `Ref.Population.Cohort` required — comparison-population cohort name; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0`.

## Host shape

FACET — numeric arm only. Categorical host → `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (sibling `OVERLAY_CHISQ_VS_POP` covers the discrete arm). Reuses `kolmogorovSurvival` backing `TEST_KS`.

## Output

SCALAR — `Payload.Scalar` carries KS `D`; `OverlaySummary{Statistic, PValue, Parameters{"n_subset", "n_pop"}}`. Layer `Baseline` unset (inferential).

## Gotchas

- Empirical-CDF path: (1) histogram (preferred), (2) percentile-map, (3) Welford-only (degenerate → NaN + `PULSE_OVERLAY_REF_ZERO`).
- The population resolver retains only Welford / histogram / percentiles; raw values are discarded. Set `IncludeHistogram=true` or `NumericPercentiles=[...]` on BOTH arms.
- Empty host or pop (`n_subset == 0` / `n_pop == 0`) → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Mismatched histogram edges fall to the percentile path, else `PULSE_OVERLAY_REF_ZERO`.
- Buffered (inferential).

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-chisq-vs-pop`, `op-test-ks`.
