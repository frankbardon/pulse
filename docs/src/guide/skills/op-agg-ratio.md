```yaml
name: op-agg-ratio
description: Emits sum(numerator_field) / sum(denominator_field). The Aggregation's own Field is required but never read.
kind: operator
category: AGG
operator: AGG_RATIO
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, streaming-friendly]
```

## Use when

Total of one field divided by the total of another, over all rows or per group, such as revenue per order.

Questions it answers:

- What is revenue per visit in each channel?
- What share of budgeted hours were actually worked per team?

Use something else:

- `ATTR_FORMULA` when you want the average of each row's own ratio: compute it per row (guarding a 0 denominator), then average it.

## Params

`numerator_field`, `denominator_field` — required; a schema field OR a derived label (attribute incl. `ATTR_REG_PREDICT`, feature output), read numerically; a categorical (its dictionary index) or `set_*` column is refused `PULSE_FIELD_TYPE_UNSUPPORTED`, numerator first. Unknown → `SERVICE_VALIDATION`, predict and runtime alike.

Weighted: Σw·num / Σw·den. Weighted `decimal128` num/den → `PULSE_WEIGHT_UNSUPPORTED`.

## Inputs

`Field` IGNORED (`ignores_field`) but required on the wire; any type (Recommend names the numerator).

## Output

`float64` Σnum / Σden. Den sum 0 → NaN (Go), `null` (JSON); no error.

## Reading the output

- `value`: The total of the numerator field divided by the total of the denominator field over the group's rows (a ratio of totals), such as revenue per order. Its units are the numerator's per unit of the denominator's.
  - Caveat: It is not the average of each row's own ratio: rows with larger denominators count for more, which is usually what a rate should do.
  - Caveat: A row missing either field is left out of BOTH totals.
  - Caveat: Empty (NaN) when the denominator total is 0.

## Components

- `numerator`, `denominator` (sums), `ratio` (`null` if den 0).

## ROI recipe

Attribute `contrib` per row; `params {"numerator_field":"contrib","denominator_field":"spend"}` → Σcontrib/Σspend exactly, per group too. Marginal ROI or an interval bound: numerator = its own per-row label.

## Contract

- Types (`numerator_field`, `denominator_field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `numerator`, `denominator`, `ratio`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- A row null on either column skips both sums; `n` counts the rest.
- Both sums are binned reproducible sums: the same bits on every worker count and shard split, equal to `AGG_SUM`'s.
- `weight` must name a cohort field, not a label.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
