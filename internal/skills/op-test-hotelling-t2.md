---
name: op-test-hotelling-t2
description: Hotelling's T² on a mean vector of p numeric members; one-sample or paired (difference vector) against params.mu, or two-sample over a 2-level split_by with pooled or unequal covariance.
kind: operator
category: TEST
operator: TEST_HOTELLING_T2
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, two-sample, paired, streaming-friendly]
---

No `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.


| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `mu` | list | — | Hypothesized mean vector (one-sample) or mean difference vector (paired), one entry per member (default zeros; ignored with `split_by`). |
| `variance` | enum | `pooled` | `pooled` or `unequal` (Krishnamoorthy–Yu); two-sample only. |
| `missing` | enum | `listwise` | Missing-data mode; only listwise (a row with any null member is dropped). |

Slot: one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields` (globs ok); no `field`. `split_by` optional, categorical or `packed_bool` (levels `"0"`/`"1"`), exactly 2 levels. Paired: `vector2` / `fields2`, same count, row-wise difference; never with `split_by`.
- `weight` — both kinds; N* = Σw or n_eff (`details` add `sum_weights`).

## Inputs

Members: numeric (`## Contract`; `packed_bool` under `coerce: binary`).

## Output

`statistic` = T², `df` = p. `details`: `f`, `df1`, `df2`, `n`, `members`, per-kind means (`multivariate-outputs`). `variant`: `one_sample`, `paired`, `two_sample_pooled`, `two_sample_unequal`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable. Needs n > p (n₁+n₂ > p+1 pooled); `unequal` refused on one-sample / paired; `missing: pairwise` refused.
- Singular covariance: null statistic + `PULSE_MATRIX_SINGULAR`.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: `statistical-testing`, `multivariate-outputs`, `op-test-t`, `op-test-welch`
