---
name: op-feat-poly
description: Per-row polynomial expansion x^2..x^Degree of a numeric field — the basis for a polynomial regression.
kind: operator
category: FEAT
operator: FEAT_POLY
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, polynomial, pre-filter, streaming-friendly]
---

Feature operators write derived fields before filters run; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `degree` | int | (required) | `>= 2` AND `<= 10`. Degree 1 is the source field; reference it directly downstream. |

## Inputs

`field` (required) — numeric (types: `## Contract`); `decimal128` reads as its float64 echo.

## Output

`degree − 1` derived fields `<prefix>_<k>` for `k = 2..degree`, `prefix` default `<field>_poly` (override via `label`). Each holds `x^k` by repeated multiplication. The source field is untouched, so a linear regression over `{x, x_poly_2, x_poly_3, …}` is the polynomial-regression basis.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `degree` outside `[2, 10]` → `PROCESSING_CONFIG`.
- OVERFLOW WITHOUT STANDARDISATION: `x^10` at `|x| = 100` is already `1e20`. Centre / standardise first; there is no orthogonal basis.
- Null inputs → null on every emitted column. Streamable per-row.

## See

- `pulse_examples_search tags=[polynomial]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `regression-modeling`, `op-feat-log` (skew alternative), `op-feat-sqrt`
