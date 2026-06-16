---
name: op-synth-uniform
description: Uniform real-valued samples in [min, max); default [0, 1).
kind: operator
category: SYNTH
operator: uniform
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape]
---

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `min` | float | `0.0` | Lower bound (inclusive). |
| `max` | float | `1.0` | Upper bound (exclusive). Must be `> min`. |

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date` (interpreted as days-since-epoch). |

For integer fields, fractional samples cast via truncation at write time.

## Output

Per-row `float64` sample `min + U * (max - min)` where `U ~ Uniform[0, 1)`. Half-open by construction.

## Gotchas

- `max <= min` → `SERVICE_VALIDATION` at spec parse.
- Half-open interval matters for integer casts — `max=10` over `u8` yields max-observed `9`.
- For calendar dates use `uniform_date` — it parses ISO-8601 strings and is inclusive both ends.
- Default `[0, 1)` is the lingua franca for noise injection / jitter columns.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-uniform-date`, `op-synth-normal`
