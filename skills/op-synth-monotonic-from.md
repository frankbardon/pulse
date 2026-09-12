---
name: op-synth-monotonic-from
description: Strictly increasing or decreasing integer counter; deterministic per row, ignores RNG. Synthetic primary keys.
kind: operator
category: SYNTH
operator: monotonic_from
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, cohort-analysis]
---

Synth distributions emit per-row values; no `Response.Components`.

## Params

- `start` — int, default `0`. First emitted value.
- `step` — int, default `1`, non-zero. Negative ⇒ decreasing.

The sampler holds `cur = start - step` and adds `step` before emitting, so row 0 emits `start`.

## Inputs

Field `type:` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`. `u64` for primary keys.

## Output

Per-row `float64` `start + i * step` for row `i`. RNG state untouched, so seeded streams stay independent of monotonic columns.

## Gotchas

- `step == 0` → `SERVICE_VALIDATION` at spec parse.
- Consumes NO RNG — adding / removing a `monotonic_from` field is the only spec edit preserving byte-equality of every other field's stream.
- Rejected rows still advance the counter, so a post-constraint cohort has gaps. For dense IDs, sanitize constraints.
- Casting overflow is SILENT — `start=250, step=10, row_count=10` overflows `u8` after row 1. Declare wide enough, or use `u64`.
- Pairs with `pulse_synth_from_schema` for fixtures; `Seed` is irrelevant here.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-constant`, `op-synth-uniform-date`
