```yaml
name: op-attr-scale-score
description: Per-row rating-battery score — the valid-item mean or prorated sum across a vector of members, with reverse-keyed items and a min_valid floor.
kind: operator
category: ATTR
operator: ATTR_SCALE_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
```

## Use when

Adds to every row its score on a rating battery: the average of its answered items or a prorated total, reverse-worded items flipped.

Questions it answers:

- What is each respondent's satisfaction score across the five satisfaction items, counting only people who answered at least four?
- What is each patient's symptom total when two of the items are worded the other way round?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `stat` | enum | `mean` | `mean` or `sum`: ALWAYS prorated (valid mean × item count); no raw sum. |
| `min_valid` | int | — | Integer 1…p; absent: p. Fewer valid items → null, never 0. |
| `reverse` | list | — | Member LABELS, scored lo + hi − x; needs a range. |
| `scale_min` | float | — | Range low bound; both bounds or neither, else a `kind: scale` vector's. |
| `scale_max` | float | — | Range high bound. No range + `reverse` → `SERVICE_VALIDATION`. |

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`. Default label `ATTR_SCALE_SCORE_<vector>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

One `f64` per record on the item scale (`sum`: × p).

## Components

- `n_out_of_range` (values), `n_rows_out_of_range` (rows): only with a range.
- The floor counts present / null scores; no weighted keys.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`; optional `n_out_of_range`, `n_rows_out_of_range`.

## Gotchas

- With a range, a value outside it is MISSING (never clamped): one `PULSE_ATTR_SCALE_OUT_OF_RANGE` warning, same counts. `MAT_RELIABILITY` refuses that data instead.
- Row-local: streams, fuses; weight ignored. Merges: shards / parallel decode fold its per-run tally (counts and warning once, cohort-wide); a chain stage admits it.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: [`attribute-composition`](attribute-composition.md), [`multivariate-design`](multivariate-design.md)
