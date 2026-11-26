```yaml
name: op-synth-normal
description: Gaussian samples with optional [min, max] clamp; default mean=0, std=1.
kind: operator
category: SYNTH
operator: normal
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape]
```

Synth distributions emit per-row values; no `Response.Components`.

## Use when

Draws numbers from a bell curve around a chosen centre and spread, optionally clipped to a low and a high bound.

Questions it answers:

- How do I generate realistic test scores that cluster around 70?
- How do I make a numeric field with a known average and spread?

Use something else:

- `lognormal` when values are always positive with a long right tail, like income.
- `mixture` when the data has two or more peaks.

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

## Contract

- Applies to: `numeric` fields.

## Gotchas

- Heavy clamping distorts pairwise correlations — `|rho|_actual < |rho|_requested` when a partner field is clamp-narrow.
- `std <= 0` → `SERVICE_VALIDATION` before any row is written.
- Nullable fields draw the inner value first, then the null mask, so the seeded stream is invariant to which rows end up null.
- Same spec and seed (`--seed`) ⇒ the same rows; seed `0` is a fixed seed, not a random one.

## See

- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-lognormal`](op-synth-lognormal.md), [`op-synth-uniform`](op-synth-uniform.md)
