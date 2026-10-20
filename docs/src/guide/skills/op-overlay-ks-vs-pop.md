```yaml
name: op-overlay-ks-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_KS_VS_POP
description: Kolmogorov-Smirnov distance + p-value comparing host Facet NUMERIC distribution against the resolved population.
type: reference
applies_to: facet
examples_tags: [overlay, facet, hypothesis-test, distribution-shape]
```

Rides on `FacetRequest.Overlays`. Overlays decorate the host; no `Response.Components`.

## Use when

Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's.

Questions it answers:

- Does this segment's spend distribution differ from all customers'?
- Has the shape of response times in this region drifted from the fleet-wide shape?

Use something else:

- `OVERLAY_CHISQ_VS_POP` when the field is categorical.
- `TEST_KS` when you have two separate groups of raw rows.

## Params

`Scope` must be `group`. `Ref.Population.Cohort` required — the comparison-population cohort; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0`.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

FACET — numeric arm only. Categorical host → `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (the χ²-vs-population sibling covers the discrete arm). p is `ks.test`'s two-sided p through the row KS test's port ([`op-test-ks`](op-test-ks.md)). Histogram path: `ks.test` on the bucketised samples (each record at its bucket index, sizes = bin totals), exact conditional on the bucket ties when m·n < 10000, else the limiting form. Percentile path (no tie pattern): the limiting form at `sqrt(n_subset·n_pop/(n_subset + n_pop))·D`. D and p are pinned to R at 1e-10.

## Output

SCALAR — `Payload.Scalar` carries KS `D`; `OverlaySummary{Statistic, PValue, Parameters{"n_subset", "n_pop"}}`. Layer `Baseline` unset (inferential).

## Reading the output

- `scalar`: D, also in summary.statistic: the largest vertical gap between the subset's and the population's cumulative distributions, from 0 (the same curve) to 1 (no overlap at all).
- `summary.statistic`: The same D as scalar.
- `summary.p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `summary.parameters.n_pop`: The number of rows behind the population's numeric summary; the other count the percentile path's p-value uses.

## Gotchas

- Empirical-CDF path: histogram (preferred), else percentile-map, else Welford-only (degenerate → NaN + `PULSE_OVERLAY_REF_ZERO`).
- The resolver retains only Welford / histogram / percentiles; raw values are discarded. Set `IncludeHistogram=true` or `NumericPercentiles=[...]` on BOTH arms.
- Empty host or pop (`n_subset == 0` / `n_pop == 0`) → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Mismatched histogram edges fall to the percentile path, else `PULSE_OVERLAY_REF_ZERO`.
- Buffered (inferential).

## See

- Skills: [`overlay-system`](overlay-system.md), [`facet-design`](facet-design.md), [`op-overlay-chisq-vs-pop`](op-overlay-chisq-vs-pop.md), [`op-test-ks`](op-test-ks.md).
