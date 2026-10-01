---
name: op-agg-null-count
description: Count records where the field is null. Inverse of AGG_COUNT.
kind: operator
category: AGG
operator: AGG_NULL_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [data-quality, streaming-friendly]
---

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

- Inverse of `AGG_COUNT`: `AGG_COUNT` counts non-null, this counts null.
- Field must be nullable in the schema; non-nullable fields always return 0.
- Set-typed empty mask is NOT a null — distinct from missing.

## See

- `pulse_examples_search tags=[data-quality]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
