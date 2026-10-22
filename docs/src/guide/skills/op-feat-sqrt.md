```yaml
name: op-feat-sqrt
description: Per-row sqrt(x) of a numeric field; emits one f64 column.
kind: operator
category: FEAT
operator: FEAT_SQRT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, distribution-shape, pre-filter, streaming-friendly]
```

Feature operators emit derived columns; no `Response.Components`.

## Use when

Adds a column holding the square root of the value, a gentler squeeze than a log for counts with a moderate long tail.

Questions it answers:

- Can I soften the few very large complaint counts without a full log?
- What does the field look like with its big values pulled in a little?

Use something else:

- `FEAT_LOG` when the tail is very long and needs a stronger squeeze.

## Params

None. `Field` (required, numeric) — `params` block is unused.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

One `f64` column written to `Label` (default `SQRT_<field>`). Formula `sqrt(x)`.

## Reading the output

- `value`: The square root of the value: 4 reads 2, 100 reads 10, 10,000 reads 100. Large values are pulled in more than small ones, but less sharply than a log.
  - Caveat: A negative value has no real square root and reads missing, with no error raised; 0 reads 0.
  - Caveat: Between 0 and 1 the root is LARGER than the value (0.25 reads 0.5), so a field of fractions is stretched, not squeezed.
  - Caveat: Averages, sums and differences of the transformed column are on the new scale: transforming them back does not give the average of the original values.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Negative inputs (`x < 0`) emit `null` — sqrt of negative is undefined for real-valued features. No error.
- Null inputs propagate to null outputs.
- Gentler skew compression than a log — prefer it for moderate-skew counts.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-feat-log`](op-feat-log.md), [`op-feat-poly`](op-feat-poly.md)
