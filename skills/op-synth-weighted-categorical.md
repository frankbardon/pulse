---
name: op-synth-weighted-categorical
description: Discrete categorical draws from a values list with optional weights; uniform when weights absent.
kind: operator
category: SYNTH
operator: weighted_categorical
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, cohort-analysis, cardinality-analysis]
---

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | list[string] | required | Dictionary entries to draw from; non-empty. |
| `weights` | list[float] | uniform | Per-value weight; length MUST match `values`. Non-negative; sum `> 0`. |

When `weights` absent the sampler installs equal weights internally.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | `categorical_u8`/`u16`/`u32` (the writer registers `values` as the inline dictionary). |

## Output

Per-row sampled string from `values`, looked up via binary search over the cumulative-weight table. Encoded as a dictionary index in the host `categorical_*` field.

## Gotchas

- `weights` length mismatch → `SERVICE_VALIDATION` at spec parse.
- Negative weight or zero-sum → `SERVICE_VALIDATION`.
- Empty `values` → `SERVICE_VALIDATION` ("requires non-empty values").
- Dictionary cardinality at write time must fit the declared categorical width — over-`u8` (>256 values) → cohesion check fails at encode.
- For boolean-like coin flips use `bernoulli` instead — avoids the dictionary block.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-bernoulli`, `op-synth-regex`
