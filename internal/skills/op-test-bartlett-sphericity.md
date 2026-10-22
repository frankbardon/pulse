---
name: op-test-bartlett-sphericity
description: Bartlett's sphericity test on a vector's correlation matrix (is it the identity?); the factorability check, bit-equal to the PCA operator's Bartlett scalars.
kind: operator
category: TEST
operator: TEST_BARTLETT_SPHERICITY
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, streaming-friendly]
---

Tests emit statistic / p-value; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`. `missing` — `listwise` only.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields`, ≥ 2 members; no `field`, `vector2` / `fields2` or `split_by`.
- `weight` — both kinds; N* = Σw or n_eff.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

`Statistic` = χ² = −ln|R|·(N* − 1 − (2p + 5)/6), `DF` = p(p − 1)/2, `PValue`. `Details`: `members`, `n`, `n_dropped`, `determinant` (|R|).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable. Same rows ⇒ same bits as the PCA operator's Bartlett scalars<!-- feature: MAT_PCA --> (`MAT_PCA`)<!-- /feature -->.
- Singular R: `PULSE_MATRIX_SINGULAR`; N* ≤ 1 + (2p + 5)/6: `PULSE_MATRIX_INSUFFICIENT_N`. Both warn, null figures.
- Large n rejects almost always.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: `multivariate-design`, `op-mat-pca`, `statistical-testing`
