```yaml
name: op-feat-frequency-encode
description: Replace each categorical value with its observed relative frequency in the cohort.
kind: operator
category: FEAT
operator: FEAT_FREQUENCY_ENCODE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, cardinality-analysis, pre-filter]
```

Feature operators write derived fields before filters run; no `Response.Components`.

## Use when

Replaces each category with the share of records that carry it, one number that says how common the category is.

Questions it answers:

- How common is each customer's city, as a single number a model can use?
- Which records belong to rare product codes?

Use something else:

- `GROUP_CATEGORY` when you want the count table itself, one row per category: group by the field, then count.

## Params

None; `params` is unused.

## Inputs

`field` (required) — `categorical_u8`, `categorical_u16`, `categorical_u32`, or `packed_bool` (levels `"0"`/`"1"`).

## Output

One derived field at `label` (default `FREQ_<field>`). Value per row = `count[category] / total_non_null` over the FULL cohort (pre-filter). Range `(0, 1]` for non-null inputs.

## Reading the output

- `value`: The share of records carrying this record's category, as a fraction between 0 and 1: 0.25 means one in four records with a category have this one. Every record in a category gets the same value.
  - Caveat: The share is out of records WITH a category: records missing the field read missing and are left out of the total, so shares across categories add up to 1 among those records only.
  - Caveat: Computed over every record of the cohort before any filter runs, so a filtered result still carries figures from the records it filtered out.

## Contract

- Types (`field`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- GLOBAL-PASS: a first pass tallies categories cohort-wide, the second encodes each row. Streams (a file-backed cohort is read twice).
- Frequencies reflect the RAW cohort because FEAT runs PRE-FILTER. To encode within a filtered subset, write the subset to a new cohort first (`pulse cohort filter`).
- Cohort with zero non-null categories → every row emits `null`.
- Null categories on individual rows emit `null`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`, `tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-feat-one-hot`](op-feat-one-hot.md), [`op-feat-target-encode`](op-feat-target-encode.md)
