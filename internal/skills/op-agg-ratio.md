---
name: op-agg-ratio
description: Emits sum(numerator_field) / sum(denominator_field). The slot's own `field` is required and type-checked but never read.
kind: operator
category: AGG
operator: AGG_RATIO
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `numerator_field` | field | (required) | Summed numerator field or label. |
| `denominator_field` | field | (required) | Summed denominator field or label. |

Each a schema field OR a derived label (attribute<!-- feature: ATTR_REG_PREDICT --> incl. `ATTR_REG_PREDICT`<!-- /feature -->, feature output), read numerically; a categorical, `set_*` or `vec_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED`, numerator first. Unknown → `SERVICE_VALIDATION`, predict and runtime alike.

Weighted: Σw·num / Σw·den. Weighted `decimal128` num/den → `PULSE_WEIGHT_UNSUPPORTED`.

## Inputs

`field` is never read (`ignores_field`) but required (absent → `SERVICE_VALIDATION`) and type-checked: any type but `decimal128` (`PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`). Name the numerator unless it is decimal.

## Output

`float64` Σnum / Σden. Den sum 0 → NaN (Go), `null` (JSON); no error.

<!-- generated: reading-the-output -->

## Components

- `numerator`, `denominator` (sums), `ratio` (`null` if den 0).

## ROI recipe

Attribute `contrib` per row; `params {"numerator_field":"contrib","denominator_field":"spend"}` → Σcontrib/Σspend exactly, per group too. Marginal ROI or an interval bound: numerator = its own per-row label.

<!-- generated: contract -->

## Gotchas

- A row null on either field skips both sums; `n` counts the rest.
- Both sums are binned reproducible sums: the same bits on every worker count and shard split<!-- feature: AGG_SUM -->, equal to `AGG_SUM`'s<!-- /feature -->.
- `weight` must name a cohort field, not a label.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: `aggregation-design`, `response-components`
