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

Feature operators emit derived columns; no `Response.Components`.

## Use when

Replaces each category with the share of records that carry it, one number that says how common the category is.

Questions it answers:

- How common is each customer's city, as a single number a model can use?
- Which records belong to rare product codes?

Use something else:

- `GROUP_CATEGORY` when you want the count table itself, one row per category: group by the field, then count.

## Params

None. `Field` (required, categorical) — `params` block is unused.

## Inputs

`Field` — `categorical_u8`, `categorical_u16`, `categorical_u32`.

## Output

One `f64` column written to `Label` (default `FREQ_<field>`). Value per row = `count[category] / total_non_null` over the FULL cohort (pre-filter). Range `(0, 1]` for non-null inputs.

## Reading the output

- `value`: The share of records carrying this record's category, as a fraction between 0 and 1: 0.25 means one in four records with a category have this one. Every record in a category gets the same value.
  - Caveat: The share is out of records WITH a category: records missing the field read missing and are left out of the total, so shares across categories add up to 1 among those records only.
  - Caveat: Computed over every record of the cohort before any filter runs, so a filtered result still carries figures from the records it filtered out.

## Gotchas

- GLOBAL-PASS: PrePass tallies categories cohort-wide, Finalize freezes the denominator, EmitRow serves O(1) per row. Streamable via `iter.Reset()`; file-backed iterators pay a second I/O.
- Frequencies reflect the RAW cohort because FEAT runs PRE-FILTER. To encode within a filtered subset, stage via Compose / ProcessChain.
- Cohort with zero non-null categories → every row emits `null`.
- Null categories on individual rows emit `null`.
- Non-categorical fields rejected at construction with `PROCESSING_CONFIG` ("must be categorical").

## See

- `pulse_examples_search tags=[cardinality-analysis]`, `tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-feat-one-hot`](op-feat-one-hot.md), [`op-feat-target-encode`](op-feat-target-encode.md)
