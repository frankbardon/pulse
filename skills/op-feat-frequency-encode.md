---
name: op-feat-frequency-encode
description: Replace each categorical value with its observed relative frequency in the cohort.
kind: operator
category: FEAT
operator: FEAT_FREQUENCY_ENCODE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, cardinality-analysis, pre-filter]
---

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

None. `Field` (required, categorical) — `params` block is unused.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `categorical_u8`, `categorical_u16`, `categorical_u32` |

## Output

One `f64` column written to `Label` (default `FREQ_<field>`). Value per row = `count[category] / total_non_null` over the FULL cohort (pre-filter). Range `(0, 1]` for non-null inputs.

## Gotchas

- GLOBAL-PASS: PrePass tallies categories cohort-wide, Finalize freezes the denominator, EmitRow serves O(1) per row. Streamable via `iter.Reset()`; file-backed iterators pay a second I/O.
- Frequencies reflect the RAW cohort because FEAT runs PRE-FILTER. To encode within a filtered subset, stage via Compose / ProcessChain.
- Cohort with zero non-null categories → every row emits `null`.
- Null categories on individual rows emit `null`.
- Non-categorical fields rejected at construction with `PROCESSING_CONFIG` ("must be categorical").

## See

- `pulse_examples_search tags=[cardinality-analysis]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-one-hot`, `op-feat-target-encode`
