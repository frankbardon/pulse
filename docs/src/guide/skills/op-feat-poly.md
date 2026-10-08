```yaml
name: op-feat-poly
description: Per-row polynomial expansion x^2..x^Degree of a numeric field — the basis for a polynomial regression.
kind: operator
category: FEAT
operator: FEAT_POLY
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, polynomial, pre-filter, streaming-friendly]
```

Feature operators emit derived columns; no `Response.Components`.

## Use when

Adds columns holding the value squared, cubed and so on up to a chosen power, so a straight-line model can bend into a curve.

Questions it answers:

- Does satisfaction rise with tenure and then level off?
- Is the effect of temperature on yield curved rather than straight?

Use something else:

- `REG_OLS` when you want to fit the curve itself, not just build its columns.

## Params

`degree` — int, required. `>= 2` AND `<= 10` (`MaxPolyDegree`). Degree 1 is the original column; reference it directly downstream.

## Inputs

`Field` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`; `decimal128` via f64. No categorical, `packed_bool`, `datetime` or `set_*` (predict refuses them; the runtime does not yet).

## Output

`Degree - 1` columns `<prefix>_<k>` for `k = 2..Degree`, `prefix` default `<field>_poly` (override via `Label`). Each holds `x^k` by iterative multiplication (`power *= v`). The ORIGINAL column is untouched, so a linear regression over `{x, x_poly_2, x_poly_3, …}` is the polynomial-regression basis.

## Reading the output

- `value.*`: One column per power k from 2 up to the degree, named <prefix>_<k> (prefix is the label, by default <field>_poly), each holding the value raised to that power: x_poly_2 is x squared, x_poly_3 is x cubed. There is no power-1 column; the original field is it.
  - Caveat: A single power column means little alone: the curve shows in a model's coefficients on x and its powers together, and those change when the field is centred first.
  - Caveat: Even powers lose the sign: x = -3 and x = 3 both give 9 in the squared column.

## Gotchas

- `degree` outside `[2, 10]` → `PROCESSING_CONFIG`.
- OVERFLOW WITHOUT STANDARDISATION: `x^10` at `|x| = 100` is already `1e20`. Centre / standardise first; an orthogonal basis is out of scope for v1.
- Null inputs → null on every emitted column. Streamable per-row.

## See

- `pulse_examples_search tags=[polynomial]`, `tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`regression-modeling`](regression-modeling.md), [`op-feat-log`](op-feat-log.md) (skew alternative), [`op-feat-sqrt`](op-feat-sqrt.md)
