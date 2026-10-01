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

Synth distributions emit per-row values; no `Response.Components`.

## Params

- `mean` — float, default `0.0`.
- `std` — float, default `1.0`; must be `> 0`.
- `min` / `max` — float, default `-inf` / `+inf`. Setting EITHER flips an internal `clamped` flag; once on, BOTH bounds apply.

## Inputs

Field `type:` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date` (days-since-epoch); `u4` writes one byte per row. Categorical / `packed_bool` unsupported — use `weighted_categorical` or `bernoulli`.

## Output

Per-row `float64` from `N(mean, std²)`, cast/clamped to the declared type at write time; days-since-epoch on a `date` field.

## Gotchas

- Heavy clamping distorts pairwise correlations — `|rho|_actual < |rho|_requested` when a partner field is clamp-narrow.
- `std <= 0` → `SERVICE_VALIDATION` at spec parse.
- Nullable fields draw the inner value first, then the null mask, so the seeded stream is invariant to which rows end up null.
- Same `(spec, opts.Seed)` ⇒ byte-identical output. `Seed == 0` is stable, not random.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-lognormal`, `op-synth-uniform`
