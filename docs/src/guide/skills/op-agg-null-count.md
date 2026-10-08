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

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type (categorical_*, numeric, date, packed_bool, set_*, decimal128) |

## Output

Scalar `int64` — count of null inputs. Per-group when wired under a grouper.

## Components

Floor only — no operator-specific keys. Universal `{n, n_null}` per response-components contract.

- Mergeability: `Mergeable`
- Streaming: per-chunk; orchestrator sums

## Gotchas

- Field must be nullable in the schema; non-nullable fields always return 0.
- Set-typed empty mask is NOT a null — distinct from missing.

## See

- `pulse_examples_search tags=[data-quality]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
