```yaml
name: op-synth-lognormal
description: Log-normal samples parameterised by log-space mu and sigma; output strictly positive.
kind: operator
category: SYNTH
operator: lognormal
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape, financial]
```

Synth distributions emit per-row values; no `Response.Components`.

## Use when

Draws positive numbers with a long right tail, like incomes or order values: most are modest and a few are very large.

Questions it answers:

- How do I simulate skewed order values?
- How do I generate realistic household incomes?

Use something else:

- `normal` when values are spread evenly around a centre.
- `pareto` when the tail should follow a power law, as with wealth or city sizes.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `mu` | float | `0` | Log-space mean (mean of `ln(X)`). |
| `sigma` | float | `1` | Log-space standard deviation; must be `> 0`. |

The defaults are the standard log-normal. The emitted mean is `exp(mu + sigma²/2)`.

## Inputs

field `type:` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Output is always `> 0`.

## Output

Per-row `float64` sample `exp(mu + sigma * Z)` where `Z ~ N(0, 1)`. Cast to declared field type at write time.

## Contract

- Applies to: `numeric` fields.

## Gotchas

- Right tail is heavy; larger `sigma` increases dispersion exponentially. Use `pareto` for true power-law tails.
- `sigma <= 0` → `SERVICE_VALIDATION` before any row is written.
- No built-in clamp — pair with a `constraint` (`amount < 1_000_000`) to bound outliers.
- Synthetic money: `lognormal` on a `decimal128` field; each draw is written at the field's `scale`.

## See

- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-normal`](op-synth-normal.md), [`op-synth-pareto`](op-synth-pareto.md)
