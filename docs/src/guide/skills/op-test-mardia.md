```yaml
name: op-test-mardia
description: Mardia's multivariate normality test on a vector — skewness χ² and kurtosis z; buffered.
kind: operator
category: TEST
operator: TEST_MARDIA
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, normality-test, multivariate, buffered-pipeline]
```

Tests emit statistic / p-value; no `Response.Components`.

## Use when

Checks whether a set of numeric fields looks jointly normal, through their combined skew and tail weight.

Questions it answers:

- Before comparing segment profiles, are the metrics close enough to jointly normal?
- Are the distance-based outlier flags safe to read as chi-square p-values?

Use something else:

- `TEST_SHAPIRO_WILK` when there is only one numeric field and you want a sharper normality test.

## Params

- `statistic` — `skewness` (default) or `kurtosis`: the headline. `alpha` — default `0.05`. `missing` — `listwise` only.

Slot: one of `vector` (a `vectors` entry) / `fields`, ≥ 1 member; no `split_by`.
- `weight` — `frequency` only (= expanded rows); `sum_weights` beside raw `n`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

Skewness: χ² = N·b1p/6 on p(p + 1)(p + 2)/6 df. Kurtosis: z, `DF` 0. `Variant` names the headline. `Details`: `skewness` (b1p), `skewness_chisq` / `_df` / `_p_value`, `kurtosis` (b2p), `kurtosis_z` / `_p_value`, `small_sample`, `members`, `n`, `n_dropped`.

## Reading the output

- `statistic`: The skewness chi-square (default) or the kurtosis z named by variant; a small p-value says the fields are not jointly normal.
  - Caveat: A large p only means no departure was detected, which is likely with few rows.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.skewness`: Mardia's multivariate skewness, 0 for jointly normal data; it grows as the joint shape leans to one side.

## Gotchas

- Buffered: keeps n × p values.
- N < 20: skewness gets the small-sample factor.
- Singular covariance: `PULSE_MATRIX_SINGULAR`, null figures.

## See

- `pulse_examples_search tags=[normality-test]`
- Skills: [`multivariate-design`](multivariate-design.md), [`statistical-testing`](statistical-testing.md), [`op-test-shapiro-wilk`](op-test-shapiro-wilk.md)
