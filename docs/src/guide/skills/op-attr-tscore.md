```yaml
name: op-attr-tscore
description: Per-row T-score column — z-score rescaled to mean 50, stddev 10.
kind: operator
category: ATTR
operator: ATTR_TSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10.

Questions it answers:

- How does each candidate's test result compare with the group, on a 50-centred scale?
- Which respondents score more than one standard deviation above average?

Use something else:

- `ATTR_ZSCORE` when you want plain standard-deviation units centred on 0.

## Params

None. Weight (both kinds, same scores; `"weight": null` opts out): weighted mean and population sd √(Σw(x − μ)²/Σw); a zero / invalid-weight row adds no mass but is still scored.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — required — new column name

## Output

One `float64` per record — `50 + 10 * zscore`. Null source → `null` (counted in `n_null`), left out of the mean / sd.

## Reading the output

- `value`: The z-score rescaled to a mean of 50 and a standard deviation of 10: 50 + 10 * z, with the population standard deviation (dividing by n). 60 is one standard deviation above the mean, 40 one below.
  - Caveat: Unrelated to Student's t or a t test: the name comes from educational testing, and the value carries no p-value.
  - Caveat: It is not a percentile: 70 does not mean the 70th percentile. Only on a roughly normal field does 60 sit near the 84th percentile.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Two-pass: a Welford pre-pass computes the mean and sd. Reading-friendly scale for survey / education contexts (mean 50, sd 10, no negatives in the typical range).
- Zero stddev (constant field, or one non-null row) → `null` per row (0/0), counted in `n_null`.
- Not a percentile — same shape as the underlying distribution.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-zscore`](op-attr-zscore.md), [`op-attr-normalized`](op-attr-normalized.md)
