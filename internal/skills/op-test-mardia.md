---
name: op-test-mardia
description: Mardia's multivariate normality test on a vector — skewness χ² and kurtosis z; buffered.
kind: operator
category: TEST
operator: TEST_MARDIA
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, normality-test, multivariate, buffered-pipeline]
---

Tests emit statistic / p-value; no `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `statistic` | enum | `skewness` | Headline statistic: the skewness χ² (default) or the kurtosis z; both always appear in details. |
| `missing` | enum | `listwise` | Missing-data mode; only listwise (a row with any null member is dropped). |


Slot: one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields`, ≥ 1 member; no `split_by`.
- `weight` — `frequency` only (= expanded rows); `sum_weights` beside raw `n`.

## Inputs

Members: numeric (`## Contract`).

## Output

Skewness: χ² = N·b1p/6 on p(p + 1)(p + 2)/6 df. Kurtosis: z, `df` 0. `variant` names the headline. `details`: `skewness` (b1p), `skewness_chisq` / `_df` / `_p_value`, `kurtosis` (b2p), `kurtosis_z` / `_p_value`, `small_sample`, `members`, `n`, `n_dropped`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered: keeps n × p values.
- N < 20: skewness gets the small-sample factor.
- Singular covariance: `PULSE_MATRIX_SINGULAR`, null figures.

## See

- `pulse_examples_search tags=[normality-test]`
- Skills: `multivariate-design`, `statistical-testing`, `op-test-shapiro-wilk`
