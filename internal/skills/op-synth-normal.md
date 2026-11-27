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

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does (`synthetic-data`).

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `mean` | float | `0` | Mean. |
| `std` | float | `1` | Standard deviation; must be `> 0`. |
| `min` | float | — | Lower clamp; absent: `-inf`. |
| `max` | float | — | Upper clamp; absent: `+inf`. |

Each bound clamps its own side; an absent one leaves that side open.

## Inputs

Field `type:` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date` (days-since-epoch). Not for a categorical (fails `ENCODING_TYPE_MISMATCH` at generation) or a `packed_bool` (a draw ≥ 0.5 writes true; use `bernoulli` for a prevalence).

## Output

Per-row `float64` from `N(mean, std²)`, cast/clamped to the declared type at write time; days-since-epoch on a `date` field.

<!-- generated: contract -->

## Gotchas

- Heavy clamping distorts pairwise correlations — `|rho|_actual < |rho|_requested` when a partner field is clamp-narrow.
- `std <= 0` → `SERVICE_VALIDATION` before any row is written.
- Nullable fields draw the inner value first, then the null mask, so the seeded stream is invariant to which rows end up null.
- Same spec and seed (`--seed`) ⇒ the same rows; seed `0` is a fixed seed, not a random one.

## See

- Skills: `synthetic-data`, `op-synth-lognormal`, `op-synth-uniform`
