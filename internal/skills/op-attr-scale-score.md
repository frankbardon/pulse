---
name: op-attr-scale-score
description: Per-row rating-battery score — the valid-item mean or prorated sum across a vector of members, with reverse-keyed items and a min_valid floor.
kind: operator
category: ATTR
operator: ATTR_SCALE_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `stat` | enum | `mean` | `mean` or `sum`: ALWAYS prorated (valid mean × item count); no raw sum. |
| `min_valid` | int | — | Integer 1…p; absent: p. Fewer valid items → null, never 0. |
| `reverse` | list | — | Member LABELS, scored lo + hi − x; needs a range. |
| `scale_min` | float | — | Range low bound; both bounds or neither, else a `kind: scale` vector's. |
| `scale_max` | float | — | Range high bound. No range + `reverse` → `SERVICE_VALIDATION`. |

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields`; no `field`. Default label `ATTR_SCALE_SCORE_<vector>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

One `f64` per record on the item scale (`sum`: × p).

## Components

- `n_out_of_range` (values), `n_rows_out_of_range` (rows): only with a range.
- The floor counts present / null scores; no weighted keys.

<!-- generated: contract -->

## Gotchas

- With a range, a value outside it is MISSING (never clamped): one `PULSE_ATTR_SCALE_OUT_OF_RANGE` warning, same counts.<!-- feature: MAT_RELIABILITY --> `MAT_RELIABILITY` refuses that data instead.<!-- /feature -->
- Row-local: streams, fuses; weight ignored. Merges: shards / parallel decode fold its per-run tally (counts and warning once, cohort-wide); a chain stage admits it.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: `attribute-composition`, `multivariate-design`
