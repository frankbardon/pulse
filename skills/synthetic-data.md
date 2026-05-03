---
name: synthetic-data
description: Generate deterministic synthetic .pulse cohorts from a schema or a captured statistical profile, with constraints and pairwise correlations.
type: guide
applies_to: inspect, predict, manifest
---

# Synthetic Data Generation

Pulse ships a `synth` package and the matching CLI commands `pulse synth from-schema`, `pulse synth from-profile`, and `pulse profile create`. Use them when you need realistic-shaped cohorts without exposing production data — onboarding, CI fixtures, demos, and load tests.

The library entry points are `pulse.Pulse.Synth(ctx, spec, output, opts)` and `pulse.Pulse.Profile(ctx, path, opts)`. Both methods route through the embedded filesystem, so a `pulse.New(pulse.Options{FS: afero.NewMemMapFs()})` instance works in tests without touching disk.

## When to use which mode

| Mode | Input | Use it when |
| ---- | ----- | ----------- |
| **from-schema** | Hand-written JSON spec listing fields and distributions | You know the shape of the data you want and need a canonical fixture. Best for onboarding examples, CI seed data, schema demos. |
| **from-profile** | A profile JSON captured from a real cohort with `pulse profile create` | You want a synthetic cohort whose marginals match a real one. The profile is the only thing that crosses the trust boundary. |

Synthetic data does **not** automatically preserve privacy. A profile captured without differential-privacy noise leaks the empirical distribution of the original — top-K categoricals reveal rare values, percentiles reveal ranges, and pairwise correlations expose structure. Treat profiles as sensitive unless you also add a DP mechanism around the publish step.

## Determinism contract

Same `(spec, opts.Seed)` pair MUST produce a byte-identical `.pulse` file. The generator is tested for this; any change to a sampler that breaks determinism is a contract break.

The seed is split via a 64-bit avalanche so seeds that differ by 1 do not produce strongly correlated streams. Seeding with `0` is fine and stable.

## Schema spec (from-schema)

```json
{
  "row_count": 100000,
  "fields": [
    { "name": "user_id",   "type": "u64",            "distribution": "monotonic_from",       "params": { "start": 1 } },
    { "name": "age",       "type": "u8",             "distribution": "normal",               "params": { "mean": 35, "std": 12, "min": 0, "max": 120 } },
    { "name": "country",   "type": "categorical_u8", "distribution": "weighted_categorical", "params": { "values": ["US","UK","DE","FR","JP"], "weights": [0.4,0.2,0.2,0.1,0.1] } },
    { "name": "signup_ts", "type": "date",           "distribution": "uniform_date",         "params": { "start": "2025-01-01", "end": "2026-04-30" } },
    { "name": "amount",    "type": "f64",            "distribution": "lognormal",            "params": { "mu": 4.2, "sigma": 0.8 } },
    { "name": "is_active", "type": "packed_bool",    "distribution": "bernoulli",            "params": { "p": 0.78 } }
  ],
  "constraints": [
    { "expr": "amount >= 0" },
    { "expr": "age >= 18" }
  ],
  "max_rejection_rate": 0.5
}
```

Run it:

```bash
pulse synth from-schema --spec demo.synth.json --output demo.pulse --seed 42
pulse inspect --json demo.pulse
```

### Supported distributions

| Kind | Params | Notes |
| ---- | ------ | ----- |
| `uniform` | `min`, `max` | Closed-open over `[min, max)`. |
| `normal` | `mean`, `std`, optional `min`, `max` | Clamps to `[min, max]` if either is set. |
| `lognormal` | `mu`, `sigma` | Output is always positive; mu/sigma are the log-space parameters. |
| `exponential` | `lambda` | Mean = 1/lambda. |
| `poisson` | `lambda` | Knuth's algorithm for `lambda < 30`, normal approximation above. Output is rounded to integer. |
| `pareto` | `xm`, `alpha` | Heavy-tailed; clamp via constraints if needed. |
| `bernoulli` | `p` | Pairs with `packed_bool` or any unsigned integer type. |
| `monotonic_from` | `start`, optional `step` | Deterministic, ignores RNG. Good for primary keys. |
| `weighted_categorical` | `values`, optional `weights` | Use with categorical types; weights default to uniform when absent. |
| `uniform_date` | `start`, `end` (YYYY-MM-DD) | Inclusive of both endpoints. |
| `regex` | `pattern`, optional `max_repeat` | Walks `regexp/syntax` AST. Use for synthetic IDs / SKUs. |
| `constant` | `value` | Always emits the same value. Useful for sentinel/test fields. |

### Field-type matrix

Every type supported by the .pulse format works with synth. Use `decimal128` with a `params.scale` matching the field's declared scale; the writer rescales as needed via banker's rounding. `point_f64` accepts a `{lat, lon}` map or a WKT POINT string. `h3_cell` consumes a uint64 from a numeric distribution. `packed_bool`, `nullable_bool`, `nullable_u4` consume one byte per row in the writer to keep the layout aligned with the importer.

### Constraints

Constraints reuse the `expr-lang/expr` evaluator. Each row is rejected if any constraint returns false; the generator continues drawing until it has the requested `row_count`. Default reject cap is 50% — beyond that, the generator returns `PULSE_SYNTH_CONSTRAINT_INFEASIBLE` rather than producing biased output. Override via `max_rejection_rate`.

Constraints can read any field on the row; they cannot reach across rows (no time series within constraints in v1).

## Profile-driven synth (from-profile)

```bash
pulse profile create --input real.pulse --output real.profile.json --include-stats --include-correlations
pulse synth from-profile --profile real.profile.json --output synthetic.pulse --rows 1000000 --seed 42
```

The profile captures, per field:

- **Numeric:** mean, std, min, max, optional percentiles {p1, p5, p25, p50, p75, p95, p99}, null-rate.
- **Categorical:** top-K values + frequencies, total cardinality, null-rate.
- **Date:** observed range, weekday histogram, null-rate.
- **Pairwise:** strongest |rho| numeric correlations (capped by `--correlation-top-k`).

`pulse profile create` writes the profile to a JSON file. `pulse synth from-profile` reads it and reconstructs an internal Spec via `synth.SpecFromProfile`. Numeric fields become `normal` (clamped to observed min/max), categoricals become `weighted_categorical`, and dates become `uniform_date`. The captured pairwise correlations become Gaussian-copula post-processing on the row stream.

`point_f64` and `h3_cell` are not summarized in v1; the profile records them with a warning and synth emits a constant zero placeholder. Use schema-mode for geospatial cohorts.

## Embedded library use

```go
spec, err := synth.ParseSpec(specBytes)
if err != nil { return err }

p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
if err != nil { return err }

res, err := p.Synth(ctx, spec, "/cohorts/demo.pulse", pulse.SynthOptions{Seed: 42})
if err != nil { return err }
```

To round-trip in tests:

```go
prof, err := p.Profile(ctx, "/cohorts/real.pulse", pulse.ProfileOptions{
    IncludeStats:        true,
    IncludeCorrelations: true,
})
syntheticSpec := synth.SpecFromProfile(prof, 100_000)
res, err := p.Synth(ctx, syntheticSpec, "/cohorts/synth.pulse", pulse.SynthOptions{Seed: 7})
```

## Privacy guidance

If you publish a profile derived from sensitive data, treat it as if you were publishing the data itself unless every captured statistic was passed through a calibrated noise mechanism. The synth pipeline does not assume privacy on its own. Improvement 23 introduces a `--dp epsilon=N` flag for the profile path; until that lands, `pulse profile create` emits exact statistics.

## Errors emitted

- `PULSE_SYNTH_DISTRIBUTION_UNKNOWN` — the spec referenced a kind not in the registry. Check `synth.AllDistributions()`.
- `PULSE_SYNTH_CONSTRAINT_INFEASIBLE` — rejection rate exceeded the threshold. Relax the constraint, change the underlying distribution, or raise `max_rejection_rate`.
- `PULSE_PROFILE_FIELD_UNSUPPORTED` — the profiler skipped a field type it cannot summarize (currently `point_f64` and `h3_cell`). Use schema-mode for those fields.
- `SERVICE_VALIDATION` — spec shape errors (missing field name, duplicate field, malformed params).
