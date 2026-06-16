---
name: op-synth-bernoulli
description: Bernoulli samples emitting 0 or 1 with probability p; pairs with packed_bool or unsigned int fields.
kind: operator
category: SYNTH
operator: bernoulli
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, proportion-analysis]
---

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `p` | float | `0.5` | Probability of emitting `1`; must lie in `[0, 1]`. |

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | `packed_bool` (1 bit per row, one byte in the writer), unsigned int (`u4`/`u8`/`u16`/`u32`/`u64`), or `f32`/`f64`. |

## Output

Per-row sample: `1.0` with probability `p`, else `0.0`. Cast to declared field type at write time. `packed_bool` packs into the bit-shared neighbour byte.

## Gotchas

- `p` outside `[0, 1]` → `SERVICE_VALIDATION` at spec parse.
- For experiment-arm assignment, pair with `weighted_categorical` for `> 2` arms.
- Empirical proportion converges at rate `O(1/sqrt(n))` — small-sample cohorts will show observed `p_hat` materially different from declared `p`.
- Determinism: same `(spec, opts.Seed)` produces identical bit pattern.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-weighted-categorical`, `op-synth-constant`
