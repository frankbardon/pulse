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

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does (`synthetic-data`).

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | list | (required) | Strings: dictionary entries to draw from; non-empty. |
| `weights` | list | — | Per-value weight; length MUST match `values`. Non-negative; sum `> 0`. Absent: equal weights. |

## Inputs

field `type:` — `categorical_u8`/`u16`/`u32`; the dictionary holds the values actually drawn.

## Output

Per-row string from `values`, at its weight's share; stored as the field's dictionary code.

<!-- generated: contract -->

## Gotchas

- `weights` length mismatch → `SERVICE_VALIDATION` before any row is written.
- Negative weight or zero-sum → `SERVICE_VALIDATION`.
- Empty `values` → `SERVICE_VALIDATION` ("requires non-empty values").
- More distinct values drawn than the width holds (256 for `categorical_u8`) → `PULSE_IMPORT_CATEGORICAL_OVERFLOW` at write.
- For a coin flip use `bernoulli` on a `packed_bool` — no dictionary.

## See

- Skills: `synthetic-data`, `op-synth-bernoulli`, `op-synth-regex`
