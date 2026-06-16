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

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `start` | int | `0` | First emitted value. |
| `step` | int | `1` | Per-row increment; non-zero. Negative values produce a decreasing sequence. |

Sampler stores `cur = start - step`; each `next()` adds `step` before emitting, so the first row emits `start`.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`. Most natural under `u64` for primary keys. |

## Output

Per-row `float64` carrying `start + i * step` for the `i`-th emitted row. RNG state untouched — synth seeded streams stay independent of monotonic columns.

## Gotchas

- `step == 0` → `SERVICE_VALIDATION` at spec parse.
- Does NOT consume RNG — adding / removing a `monotonic_from` field is the only safe spec edit that preserves byte-equality of every other field's stream.
- Constraint interaction: rejected rows still advance the counter — the cohort post-constraint contains gaps. For dense IDs, sanitize constraints or post-process.
- Casting overflow is silent — `start=250, step=10, row_count=10` overflows `u8` after row 1; declare the field wide enough or use `u64`.
- Pairs with `pulse_synth_from_schema` for fixtures; the host process supplies `Seed` but it is irrelevant to monotonic output.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-constant`, `op-synth-uniform-date`
