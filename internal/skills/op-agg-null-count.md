---
name: op-agg-null-count
description: Count records where the field is null. The inverse of a non-null count.
kind: operator
category: AGG
operator: AGG_NULL_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [data-quality, streaming-friendly]
---

<!-- generated: use-when -->

## Params

None.

## Inputs

- `Field` — any cohort field type (categorical_*, numeric, date, packed_bool, set_*, decimal128)

## Output

Scalar `int64` — count of null inputs. Per-group when wired under a grouper.

<!-- generated: contract -->

## Gotchas

- Field must be nullable in the schema; non-nullable fields always return 0.
- Set-typed empty mask is NOT a null — distinct from missing.
- `decimal128`: reads the null bitmap only (no decimal math); emits a JSON integer.

## See

- `pulse_examples_search tags=[data-quality]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
