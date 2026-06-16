---
name: op-synth-normal
description: Gaussian samples with optional [min, max] clamp; default mean=0, std=1.
kind: operator
category: SYNTH
operator: normal
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape]
---

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `mean` | float | `0.0` | Distribution mean. |
| `std` | float | `1.0` | Standard deviation; must be `> 0`. |
| `min` | float | `-inf` | Optional lower clamp; enables clamp path. |
| `max` | float | `+inf` | Optional upper clamp; enables clamp path. |

Setting either `min` or `max` flips an internal `clamped` flag — once on, both bounds are applied even when only one was specified.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date` (days-since-epoch). Bit-packed (`u4`) writes one byte per row. |

Categorical / `packed_bool` not supported — use `weighted_categorical` or `bernoulli`.

## Output

Per-row `float64` sample drawn from `N(mean, std²)`. Cast/clamped to the declared field type at write time. Days-since-epoch when bound to a `date` field.

## Gotchas

- Heavy clamping distorts pairwise correlations — `|rho|_actual < |rho|_requested` when a partner field is clamp-narrow.
- `std <= 0` → `SERVICE_VALIDATION` at spec parse.
- Nullable fields draw the inner value first, then the null mask — the seeded stream stays invariant to which rows end up null.
- Determinism: same `(spec, opts.Seed)` produces byte-identical output. `Seed == 0` is stable, not random.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-lognormal`, `op-synth-uniform`
