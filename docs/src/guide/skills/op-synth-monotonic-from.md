```yaml
name: op-synth-monotonic-from
description: Strictly increasing or decreasing integer counter; deterministic per row, ignores RNG. Synthetic primary keys.
kind: operator
category: SYNTH
operator: monotonic_from
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, cohort-analysis]
```

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does ([`synthetic-data`](synthetic-data.md)).

## Use when

Counts up (or down) by a fixed step from a start value, one step per row, to make unique IDs or row numbers.

Questions it answers:

- How do I give each synthetic row a unique customer ID?
- How do I number rows from 1000 upward?

Use something else:

- `constant` when every row should get the same value.
- `uniform` when the numbers should be random, not in order.
- `regex` when IDs should be text codes like AB-1234.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `start` | int | `0` | First emitted value. |
| `step` | int | `1` | Non-zero. Negative ⇒ decreasing. |

Row 0 emits `start`; the step commits only after a row is ACCEPTED.

## Inputs

Field `type:` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`. `u64` for primary keys.

## Output

Per-row `float64` `start + i * step` for ACCEPTED row `i` (the ordinal `row()` reads). Consumes no randomness.

## Contract

- Applies to: `numeric` fields.

## Gotchas

- `step == 0` → `SERVICE_VALIDATION` before any row is written.
- Consumes NO RNG — adding / removing a `monotonic_from` field is the only spec edit preserving byte-equality of every other field's stream.
- A constraint-rejected attempt redraws the SAME value, so the field has no gaps.
- Out-of-range values saturate SILENTLY — `start=250, step=10` over `u8` writes 255 from row 1 on (a negative run stops at 0). Declare wide enough, or use `u64`.
- The seed does not affect it.

## See

- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-constant`](op-synth-constant.md), [`op-synth-uniform-date`](op-synth-uniform-date.md)
