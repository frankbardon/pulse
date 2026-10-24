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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `start` | int | `0` | First emitted value. |
| `step` | int | `1` | Non-zero. Negative ⇒ decreasing. |

Row 0 emits `start`; the step commits only after a row is ACCEPTED.

## Inputs

Field `type:` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`. `u64` for primary keys.

## Output

Per-row `float64` `start + i * step` for ACCEPTED row `i` (the ordinal `row()` reads). RNG state untouched, so seeded streams stay independent of monotonic columns.

<!-- generated: contract -->

## Gotchas

- `step == 0` → `SERVICE_VALIDATION` at spec parse.
- Consumes NO RNG — adding / removing a `monotonic_from` field is the only spec edit preserving byte-equality of every other field's stream.
- A constraint-rejected attempt redraws the SAME value, so the column has no gaps (before U58 it did).
- Casting overflow is SILENT — `start=250, step=10, row_count=10` overflows `u8` after row 1. Declare wide enough, or use `u64`.
- Pairs with `pulse synth from-schema` for fixtures; `Seed` is irrelevant here.

## See

- Skills: `synthetic-data`, `op-synth-constant`, `op-synth-uniform-date`
