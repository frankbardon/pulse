```yaml
name: op-agg-null-count
description: Count records where the field is null. The inverse of a non-null count.
kind: operator
category: AGG
operator: AGG_NULL_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [data-quality, streaming-friendly]
```

## Use when

Number of rows where a field has no value, over all rows or per group.

Questions it answers:

- How many respondents skipped the income question?
- Which regions have the most orders missing a delivery date?

Use something else:

- `AGG_COUNT` when you want the rows that do have a value.
- `FILTER_NULL` when you want to keep or drop the rows with no value.

## Params

None.

## Inputs

- `Field` — any cohort field type (categorical_*, numeric, date, packed_bool, set_*, decimal128)

## Output

Scalar `int64` — count of null inputs. Per-group when wired under a grouper.

## Contract

- Types: any type.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`.

## Gotchas

- Field must be nullable in the schema; non-nullable fields always return 0.
- Set-typed empty mask is NOT a null — distinct from missing.
- `decimal128`: reads the null bitmap only (no decimal math); emits a JSON integer.

## See

- `pulse_examples_search tags=[data-quality]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
