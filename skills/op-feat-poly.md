---
name: op-feat-poly
description: Per-row polynomial expansion x^2..x^Degree of a numeric field; pair with REG_OLS for polynomial regression.
kind: operator
category: FEAT
operator: FEAT_POLY
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, polynomial, pre-filter, streaming-friendly]
---

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `degree` | int | (required) | Polynomial degree. Must be `>= 2` AND `<= 10` (`MaxPolyDegree`). Degree 1 (the linear term) is the original column — reference it directly downstream. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `decimal128` accepted via f64 approximation. (no categorical, no `packed_bool`) |

## Output

`Degree - 1` columns named `<prefix>_<k>` for `k = 2..Degree`, where `prefix` defaults to `<field>_poly` (override via `Label`). Each column holds `x^k` per row, computed as iterative multiplication (`power *= v`). The ORIGINAL column is left untouched so REG_OLS over `{x, x_poly_2, x_poly_3, ...}` becomes the linear-model basis for polynomial regression.

## Gotchas

- `degree < 2` → `PROCESSING_CONFIG` ("use the original column for the linear term").
- `degree > 10` → `PROCESSING_CONFIG` ("Degree capped at 10; consider standardizing inputs first or using an orthogonal basis").
- OVERFLOW WITHOUT STANDARDISATION: naive `x^10` with `|x| = 100` already yields `1e20`. Centre / standardise the predictor first; for serious work use an orthogonal basis (out of scope for v1).
- Null inputs → null on every emitted column.
- Streamable per-row.
- Designed for `REG_OLS` polynomial fits (Indeed #8). Pair with `regression-modeling`.

## See

- `pulse_examples_search tags=[polynomial]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `regression-modeling`, `op-feat-log` (skew alternative), `op-feat-sqrt`
