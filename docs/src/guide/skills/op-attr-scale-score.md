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

- `stat` — `mean` (default) or `sum`: ALWAYS prorated (valid mean × item count); no raw sum.
- `min_valid` — integer 1…p, default p: fewer valid items → null, never 0.
- `reverse` — member LABELS, scored lo + hi − x; needs a range.
- `scale_min` / `scale_max` — both or neither; else a `kind: scale` vector's bounds. No range + `reverse` → `SERVICE_VALIDATION`.

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`. Default label `ATTR_SCALE_SCORE_<vector>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

One `f64` per record on the item scale (`sum`: × p).

## Components

`components.attributes` entry: floor `{n, n_null}` (present / null scores) + `n_out_of_range` (values), `n_rows_out_of_range` (rows) only with a range. `Mergeable`; no weighted keys.

## Gotchas

- With a range, a value outside it is MISSING (never clamped): one `PULSE_ATTR_SCALE_OUT_OF_RANGE` warning, same counts. `MAT_RELIABILITY` refuses that data instead.
- Row-local: streams, fuses; weight ignored. Never merged (its tally is per run): shards / parallel decode run serially, a chain stage is refused.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: [`attribute-composition`](attribute-composition.md), [`multivariate-design`](multivariate-design.md)
