---
name: op-agg-ratio
description: Emits sum(numerator_field) / sum(denominator_field). The Aggregation's own Field is ignored.
kind: operator
category: AGG
operator: AGG_RATIO
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

`numerator_field`, `denominator_field` — required; a schema field OR a derived label (attribute<!-- feature: ATTR_REG_PREDICT --> incl. `ATTR_REG_PREDICT`<!-- /feature -->, feature output), read numerically; a categorical (its dictionary index) or `set_*` column is refused `PULSE_FIELD_TYPE_UNSUPPORTED`, numerator first. Unknown → `SERVICE_VALIDATION`, predict and runtime alike.

Weight-aware (`"weight": null` opts out): Σw·num / Σw·den. Weighted `decimal128` num/den → `PULSE_WEIGHT_UNSUPPORTED`.

## Inputs

`Field` IGNORED (`ignores_field`) but required; any type.

## Output

`float64` Σnum / Σden. Den sum 0 → NaN (Go), `null` (JSON); no error.

<!-- generated: reading-the-output -->

## Components

Floor `{n, n_null}`<!-- feature: capability:weighting --> (weighted: `sum_weights`, `n_eff`, `n_weight_invalid`)<!-- /feature -->, plus `numerator`, `denominator` (sums), `ratio` (`null` if den 0). `Mergeable`; streams.

## ROI recipe

Attribute `contrib` per row; `params {"numerator_field":"contrib","denominator_field":"spend"}` → Σcontrib/Σspend exactly, per group too. Marginal ROI or an interval bound: numerator = its own per-row label.

## Gotchas

- A row null on either column skips both sums; `n` counts the rest.
- `weight` must name a cohort field, not a label.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: `aggregation-design`, `response-components`
