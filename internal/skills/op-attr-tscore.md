---
name: op-attr-tscore
description: Per-row T-score column — z-score rescaled to mean 50, stddev 10.
kind: operator
category: ATTR
operator: ATTR_TSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

None. Weight (both kinds, same scores; `"weight": null` opts out): weighted mean and population sd √(Σw(x − μ)²/Σw); a zero / invalid-weight row adds no mass but is still scored.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |
| `Label` | required — new column name |

## Output

One `float64` per record — `50 + 10 * zscore`. Null source → `null` (counted in `n_null`), left out of the mean / sd.

<!-- generated: reading-the-output -->

## Gotchas

- Two-pass: a Welford pre-pass computes the mean and sd. Reading-friendly scale for survey / education contexts (mean 50, sd 10, no negatives in the typical range).
- Zero stddev → `50` per row.
- Not a percentile — same shape as the underlying distribution.
- `set_*` and categorical rejected at build time with `PULSE_FIELD_TYPE_UNSUPPORTED` — a bitmask or dictionary index has no value to standardise.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `attribute-composition`, `op-attr-zscore`, `op-attr-normalized`
