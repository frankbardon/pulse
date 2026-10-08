```yaml
name: op-synth-poisson
description: Poisson-distributed non-negative integer counts with mean lambda; Knuth multiplicative below 30, normal-approximation above.
kind: operator
category: SYNTH
operator: poisson
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, cardinality-analysis]
```

Synth distributions emit per-row values; no `Response.Components`.

## Use when

Draws whole-number counts of events per period, such as visits per day, around a chosen average.

Questions it answers:

- How many orders might a store get per hour?
- How do I simulate defect counts per batch?

Use something else:

- `exponential` when you want the time between events, not how many.
- `bernoulli` when each row is a yes/no outcome.
- `discrete` when you know the exact share of each count.

## Params

- `lambda` — float, default `1.0`. Rate parameter; equals both the mean and the variance. Must be `> 0`.

## Inputs

field `type:` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`. Cast at write time — `u4` overflows silently if `lambda` is large.

## Output

Per-row non-negative integer (returned as `float64`). Two algorithms:

- `lambda < 30`: Knuth's multiplicative method — exact, draws `k` uniforms until product `< exp(-lambda)`.
- `lambda >= 30`: normal approximation `N(lambda, lambda)` rounded to nearest non-negative integer.

## Gotchas

- Knuth runtime is `O(lambda)` per row — large lambda would dominate without the normal-approx switch.
- `lambda <= 0` → `SERVICE_VALIDATION` at spec parse.
- Variance equals mean; for over-dispersed counts pair with a `lognormal` mixer or use rejection sampling via `constraints`.
- Same `(spec, opts.Seed)` is deterministic across both algorithm branches.

## See

- `pulse_examples_search tags=[synth]`
- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-exponential`](op-synth-exponential.md), [`op-synth-bernoulli`](op-synth-bernoulli.md)
