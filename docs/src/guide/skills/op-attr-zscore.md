```yaml
name: op-attr-zscore
description: Per-row standardized z-score column — (value − mean) / stddev via two-pass Welford.
kind: operator
category: ATTR
operator: ATTR_ZSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [outlier-detection, distribution-shape, buffered-pipeline]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean.

Questions it answers:

- Which orders are unusually large compared with all orders?
- How do scores measured on different scales compare once put on one footing?

Use something else:

- `AGG_ZSCORE` when you want the centre and spread of each group, not a score per row.

## Params

None. Weight (both kinds, same scores; `"weight": null` opts out): weighted mean and population sd √(Σw(x − μ)²/Σw); a zero / invalid-weight row adds no mass but is still scored.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `u4` |
| `Label` | required — new column name |

## Output

One `float64` per record — `(value − pop_mean) / pop_stddev`. Null source → `0` (not null).

## Reading the output

- `value`: How many standard deviations the row's value sits from the mean: (value - mean) / standard deviation, with the population standard deviation (dividing by n). 0 is exactly average, 1 is one standard deviation above it.
  - Sign: positive means the row's value is above the mean; negative means the row's value is below the mean.
  - Caveat: Rules such as 'about 95% of rows lie within 2 standard deviations' hold only for a roughly bell-shaped (normal) field; on a skewed field far more rows can sit beyond 2 on the long side.

## Gotchas

- Two-pass: pre-pass computes mean/stddev over filter-passing rows, then pass 2 emits per row. Orchestrator handles transparently — no full buffering unless paired with a buffered op downstream.
- Zero stddev (constant field) → `0` per row.
- `decimal128` rejected.
- Under sharded cohorts the two-pass runs per-shard along the `Mergeable` path — produces global standardization.
- `set_*` rejected at build time with `PROCESSING_CONFIG` — a bitmask has no value to standardise.

## See

- `pulse_examples_search tags=[outlier-detection]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-tscore`](op-attr-tscore.md), [`op-agg-zscore`](op-agg-zscore.md)
