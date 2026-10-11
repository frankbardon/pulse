```yaml
name: op-agg-ratio
description: Emits sum(numerator_field) / sum(denominator_field). The Aggregation's own Field is ignored.
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

`numerator_field`, `denominator_field` — required; a schema field OR a derived label (attribute incl. `ATTR_REG_PREDICT`, feature output), read numerically (categorical → its code). Unknown → `SERVICE_VALIDATION`, predict and runtime alike.

Weight-aware (`"weight": null` opts out): Σw·num / Σw·den. Weighted `decimal128` num/den → `PULSE_WEIGHT_UNSUPPORTED`.

## Inputs

`Field` IGNORED (`ignores_field`) but required; any type.

## Output

`float64` Σnum / Σden. Den sum 0 → NaN (Go), `null` (JSON); no error.

## Reading the output

- `value`: The total of the numerator field divided by the total of the denominator field over the group's rows (a ratio of totals), such as revenue per order. Its units are the numerator's per unit of the denominator's.
  - Caveat: It is not the average of each row's own ratio: rows with larger denominators count for more, which is usually what a rate should do.
  - Caveat: A row missing either field is left out of BOTH totals.
  - Caveat: Empty (NaN) when the denominator total is 0.

## Components

Floor `{n, n_null}` (weighted: `sum_weights`, `n_eff`, `n_weight_invalid`), plus `numerator`, `denominator` (sums), `ratio` (`null` if den 0). `Mergeable`; streams.

## ROI recipe

Attribute `contrib` per row; `params {"numerator_field":"contrib","denominator_field":"spend"}` → Σcontrib/Σspend exactly, per group too. Marginal ROI or an interval bound: numerator = its own per-row label.

## Gotchas

- A row null on either column skips both sums; `n` counts the rest.
- `weight` must name a cohort field, not a label.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
