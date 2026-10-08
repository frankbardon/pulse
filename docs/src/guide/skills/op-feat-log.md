```yaml
name: op-feat-log
description: Per-row log1p(x) of a numeric field; emits one f64 column. Standard skew-tamer.
kind: operator
category: FEAT
operator: FEAT_LOG
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, distribution-shape, pre-filter, streaming-friendly]
```

Feature operators emit derived columns; no `Response.Components`.

## Use when

Adds a column holding the natural log of 1 + the value, which pulls in a long tail of large values so a skewed field is easier to model.

Questions it answers:

- Can I tame the long tail of customer spend before fitting a model on it?
- How does income look on a log-like scale, where for large values doubling counts about the same everywhere?

## Params

None. `Field` (required, numeric) — `params` block is unused.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `decimal128` accepted via f64 approximation. (no categorical / `packed_bool`) |

## Output

One `f64` column written to `Label` (default `LOG_<field>`). Formula `log1p(x) = ln(1 + x)` — the +1 shift keeps `x=0 -> 0` instead of `-inf`.

## Reading the output

- `value`: The natural log of 1 + the value: ln(1 + x). 0 reads 0, e - 1 (about 1.72) reads 1, 9 reads about 2.3 and 99 about 4.6; each step of about 0.69 is a doubling of 1 + x, so equal steps are equal ratios of 1 + x, not equal amounts.
  - Sign: positive means the original value is above 0; negative means the original value is between -1 and 0.
  - Caveat: A value of -1 or less has no log and reads missing, with no error raised: count the missing rows before trusting a model built on the column.
  - Caveat: A missing input gives a missing output.

## Gotchas

- Inputs with `x <= -1` (where `1 + x <= 0`) produce a `null`, not an error — the rest of the cohort keeps going.
- Null inputs propagate to null outputs.
- Streamable per-row — pairs cleanly with online aggregators downstream.
- Pre-filter slot: a range filter on the log column references `LOG_<field>` (default label).

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-feat-sqrt`](op-feat-sqrt.md), [`op-feat-poly`](op-feat-poly.md)
