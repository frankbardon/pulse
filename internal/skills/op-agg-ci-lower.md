---
name: op-agg-ci-lower
description: Lower bound of the confidence interval for the mean.
kind: operator
category: AGG
operator: AGG_CI_LOWER
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `confidence` | float | 0.95 | Confidence level in (0, 1). |
| `method` | string | `"normal"` | `"normal"` (Welford-streamable); `"bootstrap"` is refused. |

Bound = mean ∓ z·√(s²/n), z = `qnorm(1 − α/2)`. Weight (both kinds; `"weight": null` opts out): weighted mean, s² on w* = w·N*/Σw, stderr √(s²/N*), N* = Σw (`frequency`) or Kish n_eff (`probability`); same z. Invalid weights excluded and warned.

## Inputs

`field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — lower CI bound. NaN when `n < 2` (weighted: N* ≤ 1; `null` in JSON).

<!-- generated: reading-the-output -->

## Components

- `mean` — Welford running mean
- `stderr` — Standard error of the mean
- `alpha` — `1 - confidence`
- `t_critical` — Normal critical z (`qnorm(1 − α/2)`)
- `lower` — Resolved lower bound

<!-- generated: contract -->

## Gotchas

- Normal z, not t: small samples get narrower intervals than t.
- `n < 2` (weighted N* ≤ 1) → NaN (no variance estimate).
- `"bootstrap"` → `PROCESSING_CONFIG` at run time; predict passes it.

## See

- `pulse_examples_search tags=[hypothesis-test]`
- Skills: `aggregation-design`, `statistical-testing`, `response-components`
