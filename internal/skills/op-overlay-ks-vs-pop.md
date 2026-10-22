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

Rides on `FacetRequest.Overlays`. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `group`. `Ref.Population.Cohort` required — the comparison-population cohort; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0`.


## Host shape

FACET — numeric arm only. Categorical host → `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (the χ²-vs-population sibling covers the discrete arm). p is `ks.test`'s two-sided p through the row KS test's port (`op-test-ks`). Histogram path: `ks.test` on the bucketised samples (each record at its bucket index, sizes = bin totals), exact conditional on the bucket ties when m·n < 10000, else the limiting form. Percentile path (no tie pattern): the limiting form at `sqrt(n_subset·n_pop/(n_subset + n_pop))·D`. D and p are pinned to R at 1e-10.

## Output

SCALAR — `Payload.Scalar` carries KS `D`; `OverlaySummary{Statistic, PValue, Parameters{"n_subset", "n_pop"}}`. Layer `Baseline` unset (inferential).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Empirical-CDF path: histogram (preferred), else percentile-map, else Welford-only (degenerate → NaN + `PULSE_OVERLAY_REF_ZERO`).
- The resolver retains only Welford / histogram / percentiles; raw values are discarded. Set `IncludeHistogram=true` or `NumericPercentiles=[...]` on BOTH arms.
- Empty host or pop (`n_subset == 0` / `n_pop == 0`) → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Mismatched histogram edges fall to the percentile path, else `PULSE_OVERLAY_REF_ZERO`.

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-chisq-vs-pop`, `op-test-ks`.
