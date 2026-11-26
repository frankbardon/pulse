---
name: op-overlay-ks-vs-pop
kind: operator
category: OVERLAY
operator: OVERLAY_KS_VS_POP
description: Kolmogorov-Smirnov distance + p-value comparing the facet NUMERIC distribution against the resolved population.
type: reference
applies_to: facet
examples_tags: [overlay, facet, hypothesis-test, distribution-shape]
---

Rides on facet `overlays`. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | The facet `fields` entry to score; required with two or more (else `PULSE_OVERLAY_PARAM_MISSING`); an unlisted one → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. |

`scope` must be `group`. `ref.population.cohort` required — the comparison-population cohort; other `ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `level` / `within` must be `0`.

## Host shape

FACET — numeric fields only; a categorical one → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict too; the χ²-vs-population sibling covers it). p is `ks.test`'s two-sided p through the row KS test's port (`op-test-ks`). Histogram path: `ks.test` on the bucketised samples (each record at its bucket index, sizes = bin totals), exact conditional on the bucket ties when m·n < 10000, else the limiting form. Percentile path (no tie pattern): the limiting form at `sqrt(n_subset·n_pop/(n_subset + n_pop))·D`.

## Output

SCALAR — `payload.scalar` carries KS `D`; layer `summary` `statistic`, `p_value`, `parameters.n_subset` / `n_pop`. `summary.baseline` unset (inferential).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Empirical-CDF path: histogram (preferred), else percentile-map, else none: `null` + `PULSE_OVERLAY_REF_ZERO`.
- Only the facet summary is compared, never raw values: set `include_histogram: true` (with a `histogram_range`) or `numeric_percentiles` on the request.
- Empty host or population (`n_subset` / `n_pop` 0) → `null` + `PULSE_OVERLAY_REF_ZERO`.
- Mismatched histogram edges fall to the percentile path, else `PULSE_OVERLAY_REF_ZERO`.

## See

- Skills: `overlay-system`, `facet-design`, `op-overlay-chisq-vs-pop`, `op-test-ks`.
