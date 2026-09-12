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

Feature operators emit derived columns; no `Response.Components`.

## Params

`degree` — int, required. `>= 2` AND `<= 10` (`MaxPolyDegree`). Degree 1 is the original column; reference it directly downstream.

## Inputs

`Field` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`; `decimal128` via f64 approximation. No categorical, no `packed_bool`.

## Output

`Degree - 1` columns `<prefix>_<k>` for `k = 2..Degree`, `prefix` default `<field>_poly` (override via `Label`). Each holds `x^k` by iterative multiplication (`power *= v`). The ORIGINAL column is untouched, so `REG_OLS` over `{x, x_poly_2, x_poly_3, …}` is the polynomial-regression basis.

## Gotchas

- `degree` outside `[2, 10]` → `PROCESSING_CONFIG`.
- OVERFLOW WITHOUT STANDARDISATION: `x^10` at `|x| = 100` is already `1e20`. Centre / standardise first; an orthogonal basis is out of scope for v1.
- Null inputs → null on every emitted column. Streamable per-row.

## See

- `pulse_examples_search tags=[polynomial]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `regression-modeling`, `op-feat-log` (skew alternative), `op-feat-sqrt`
