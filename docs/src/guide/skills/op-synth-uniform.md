```yaml
name: op-synth-uniform
description: Uniform real-valued samples in [min, max); default [0, 1).
kind: operator
category: SYNTH
operator: uniform
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape]
```

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does ([`synthetic-data`](synthetic-data.md)).

## Use when

Draws numbers between a low and a high bound, every value equally likely, for flat filler fields or random noise.

Questions it answers:

- How do I fill a test field with random numbers between 0 and 100?
- How do I add flat random noise to a synthetic cohort?

Use something else:

- `normal` when values should cluster around a typical value.
- `uniform_date` when the field is a calendar date.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `min` | float | `0.0` | Lower bound (inclusive). |
| `max` | float | `1.0` | Upper bound (exclusive before rounding; on `u8`–`u64` fields rounding can produce it, and both ends come out half as often). Must be `> min`. |

## Inputs

- field `type:` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date` (interpreted as days-since-epoch).

For `u8`–`u64` fields each sample rounds to the nearest integer at write time (`floor(v+0.5)`).

## Output

Per-row `float64` sample `min + U * (max - min)` where `U ~ Uniform[0, 1)`. Half-open by construction.

## Contract

- Applies to: `numeric` fields.

## Gotchas

- `max <= min` → `SERVICE_VALIDATION` before any row is written.
- Half-open, but integer fields ROUND — `max=10` over `u8` can store `10` (any draw ≥ 9.5).
- For calendar dates use `uniform_date` — it parses ISO-8601 strings and is inclusive both ends.

## See

- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-uniform-date`](op-synth-uniform-date.md), [`op-synth-normal`](op-synth-normal.md)
