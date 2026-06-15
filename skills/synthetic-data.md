---
name: synthetic-data
description: `pulse_synth_from_schema` vs `pulse_synth_from_profile`, pairwise correlations, constraints, determinism via seed. Topical design; per-distribution detail in atomic op-synth-* skills.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [pulse_synth_from_schema, pulse_synth_from_profile, synth, distributions, correlations, constraints, determinism]
---

# Synthetic data

Pulse synthesizes deterministic `.pulse` cohorts via `pulse_synth_from_schema` / `pulse_synth_from_profile` (matching CLI leaves). This file covers mode choice, correlations, constraints, determinism. Per-distribution detail in atomic `op-synth-*` skills.

Synth does not emit `Response.Components` — it writes a `.pulse` file; Components is reserved for Process results.

## Two modes

| Mode | Input | When |
|---|---|---|
| `pulse_synth_from_schema` | hand-written JSON spec | caller knows desired shape — fixtures, CI seeds, demos |
| `pulse_synth_from_profile` | profile JSON captured from a real cohort | marginals must match a real cohort without leaking values |

**Privacy.** Synth does NOT preserve privacy on its own. A profile without DP noise leaks the empirical distribution — top-K categoricals reveal rare values, percentiles reveal ranges, pairwise correlations expose structure. Add a calibrated noise mechanism if the source is sensitive.

## Schema-mode spec

```json
{
  "row_count": 100000,
  "fields": [
    {"name": "user_id", "type": "u64", "distribution": "monotonic_from", "params": {"start": 1}},
    {"name": "age", "type": "u8", "distribution": "normal", "params": {"mean": 35, "std": 12}},
    {"name": "country", "type": "categorical_u8", "distribution": "weighted_categorical", "params": {"values": ["US","UK"], "weights": [0.6,0.4]}},
    {"name": "amount", "type": "f64", "distribution": "lognormal", "params": {"mu": 4.2, "sigma": 0.8}}
  ],
  "constraints": [{"expr": "amount >= 0"}],
  "max_rejection_rate": 0.5
}
```

Verify with `pulse_inspect`.

### Distribution registry

Twelve kinds. Per-kind params + clamp semantics in atomic `op-synth-<kind>` skills. Registry: `synth.AllDistributions()`.

- `uniform` — closed-open `[min, max)`.
- `normal` — `mean`, `std`, optional `min`/`max` clamp.
- `lognormal` — `mu`, `sigma` (log-space); positive output.
- `exponential` — `lambda`; mean = 1/lambda.
- `poisson` — `lambda`; Knuth for λ<30, normal approx above.
- `pareto` — `xm`, `alpha`; heavy-tailed.
- `bernoulli` — `p`; pairs with `packed_bool` or uint.
- `monotonic_from` — `start`, `step`; deterministic, ignores RNG. Primary keys.
- `weighted_categorical` — `values`, optional `weights`; uniform when absent.
- `uniform_date` — `start`, `end` (YYYY-MM-DD); inclusive.
- `regex` — `pattern`, `max_repeat`; walks `regexp/syntax` AST.
- `constant` — `value`; sentinel fields.

All 17 `.pulse` field types reachable. `decimal128` requires `params.scale` matching declared scale (banker's rounding). Bit-packed (`u4`, `packed_bool`) use one byte per row in the writer. `nullable: true` opts into the per-record null bitmap; distributions report nulls via the bitmap, never inline sentinels.

### Constraints

`constraints[]` reuse `expr-lang/expr`. Row rejected if any constraint returns false; generator keeps drawing until `row_count` is reached. Default reject cap 50%; beyond that `PULSE_SYNTH_CONSTRAINT_INFEASIBLE` fires. Override via `max_rejection_rate`. Constraints read any field on the same row; no cross-row reach in v1.

### Pairwise correlations

`correlations` is a list of `{a, b, rho}` triples; engine applies Gaussian-copula post-processing on the row stream. Both fields must be numeric. Correlations chain transitively only by accident — specify the pairs you care about. `|rho| ≥ 1` rejected at validation.

## Profile mode

Capture via `pulse_profile_create`; synth via `pulse_synth_from_profile`. Profile JSON captures per field:

- Numeric: mean, std, min, max, optional percentiles, null-rate.
- Categorical: top-K values + frequencies, cardinality, null-rate.
- Date: observed range, weekday histogram, null-rate.
- Pairwise: strongest `|rho|` correlations (capped by `--correlation-top-k`).

`synth.SpecFromProfile` reconstructs a Spec: numeric → `normal` clamped to observed min/max, categorical → `weighted_categorical`, date → `uniform_date`. Captured correlations become Gaussian-copula post-processing. Unsupported types → `PULSE_PROFILE_FIELD_UNSUPPORTED`; drop to schema mode for those.

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file. Any sampler change that breaks determinism is a contract break.

Seed splitting uses a 64-bit avalanche; seeds differing by 1 produce uncorrelated streams. `Seed == 0` is stable, not "random". `nullableSampler` always draws the inner value first, then the null mask — the seeded stream is invariant to which rows are null.

## Library embedding

`pulse.Pulse.Synth` and `pulse.Pulse.Profile` route through the embedded filesystem — `pulse.New(pulse.Options{FS: afero.NewMemMapFs()})` for hermetic tests.

## Gotchas

- Constraints + `monotonic_from`: monotonic ignores RNG, so a rejected row still increments the counter.
- Correlations + clamping: heavy-clamped `normal` distorts the copula target — `|rho|_actual < |rho|_requested`.
- `weighted_categorical` weights normalize at sample time; absent weights default to uniform.
- `uniform_date` is inclusive both ends.
- `regex` is restricted: literal / charclass / fixed-repeat / alternation / bounded `*+{m,n}`. No backreferences.
- Profile capture emits exact statistics — see Privacy above.

## See

- Recipes: `pulse_examples_search tags=["synth"]` plus atomic `op-synth-<kind>`.
- `cohort-schema-design` — field types, dictionaries, null bitmap.
- `import-best-practices` — round-trip via importers.
- `error-code-reference` — `PULSE_SYNTH_*` / `PULSE_PROFILE_*` recovery.
