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
| `pulse_synth_from_profile` | profile JSON + the source cohort it was captured from | tagged top-up: add rows matching a real cohort's marginals, source untouched |

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

`correlations` is a list of `{a, b, rho}` triples. The engine draws a correlated standard-normal vector via Cholesky, then sets each field to `mean + std*u` (clamped if `normal` with declared `min`/`max`); `mean`/`std` are closed-form for `normal`/`uniform`/`lognormal`/`exponential` only — any other distribution named in `correlations` refuses with `SERVICE_VALIDATION`. Exact for jointly-`normal` pairs (what `SpecFromProfile` always reconstructs); preserves mean/std but not skew for non-normal marginals. `|rho| ≥ 1` rejected at validation. Chosen over rank-based copula because schema-mode specs have no historical samples to rank against — replaces a removed v1 blend (±5%·std nudge, untested fidelity); see `TestSynth_CorrelationReconstructionWithinTolerance`.

## Profile mode

Capture via `pulse_profile_create`; synth via `pulse_synth_from_profile`. Profile JSON captures per field:

- Numeric: mean, std, min, max, optional percentiles, null-rate.
- Categorical: top-K values + frequencies, cardinality, null-rate.
- Date: observed range, weekday histogram, null-rate.
- Pairwise: strongest `|rho|` correlations (capped by `--correlation-top-k`).
- Conditional (`--conditional`, additive/omitempty): `conditional.numeric_pairs`, row-aligned `{a, b, rho, n}` where `n` is the true co-occurrence count (both non-null, same row) — more accurate than Pairwise's independently-capped reservoirs. `SpecFromProfile` prefers it over `pairwise` when present; absent (old or plain-`--include-correlations` documents) falls back unchanged. `n < 30` (`synth.MinPairObservations`) still ships, never refused — appends a warning naming the pair; the mechanism is generic across pair kind for later categorical/set_* reuse.
- Conditional categorical-categorical (`conditional.categorical_pairs`, same `--conditional` flag): one `{a, b, cells, n}` entry per categorical-categorical field pair, `cells` a bounded contingency table of `{a_value, b_value, count}` co-occurrences and `n` the pair's true co-occurrence count (both non-null, same row). Two caps compose: each field's own `--top-k` cap collapses an out-of-top-K value to `"other"` before the joint table is built, then `synth.ContingencyCellCap` (128) bounds the joint table itself — any raw cell count over the cap is ranked by count descending and the tail (plus any pre-existing `("other","other")` cell from the per-field collapse) folds into one merged `("other","other")` catch-all, never a second competing entry, saturating to exactly the cap whenever raw cardinality exceeds it. Thin cells (`count < synth.MinPairObservations`) reuse the same warning helper as numeric pairs — no separate mechanism. Capture-only in this story; `SpecFromProfile` does not yet consume `categorical_pairs` for generation.

`synth.SpecFromProfile` reconstructs a Spec: numeric → `normal` clamped to observed min/max, categorical → `weighted_categorical`, date → `uniform_date`. Captured correlations become the conditional-Gaussian reconstruction described above. Unsupported types → `PULSE_PROFILE_FIELD_UNSUPPORTED`; drop to schema mode for those.

### Tagged top-up contract

`synth from-profile` (`SynthOptions.SourceCohort` / `--source`) always: (1) appends one `_synthetic` `packed_bool` field to the output schema — `false` on every row copied from the source, `true` on every newly generated row; (2) writes to a **new** output path, distinct from `--source` — the source cohort is opened read-only and never mutated; (3) treats `--rows`/`RowCount` as an explicit count of *new* rows, never "top up to N total" (`--rows 500` against a 200-row source → 700-row output). `SourceCohort` empty (`pulse.Pulse.Synth` / `synth from-schema`'s default) is the unmodified plain path — no tag column, output may equal any path. Refusals: `PULSE_SYNTH_SOURCE_REQUIRED`, `PULSE_SYNTH_OUTPUT_REQUIRED`, `PULSE_SYNTH_OUTPUT_COLLISION` (output == source), `PULSE_SYNTH_ALREADY_TAGGED` (source already carries `_synthetic`), `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH` (profile's fields don't line up with the source's own schema).

`--fidelity-report <path.json>` (`SynthOptions.FidelityReportPath`, only with `SourceCohort` set) writes a `synth.FidelityReport` JSON document after generation: numeric fields via `TEST_KS` (`split_by: _synthetic`), categorical fields via `TEST_CHISQ` (contingency against `_synthetic`) — existing operators, no new stat math. `_synthetic` is on-wire `packed_bool`, but both operators need a categorical field, so the bridge presents it through a categorical_u8 view schema (`synth.SyntheticAsCategoricalSchema`) for that call only; the `.pulse` byte format is unchanged. The bridge lives in `pulse.go` (`writeSynthFidelityReport`), not `synth/` — `synth` importing `processing` would close a cycle through `descriptor` (imports `synth`) and `processing`'s own test files (import `descriptor`) — so `synth.BuildFidelityReport` takes an injected `TestRunner` instead. A field whose test fails reports `error`, not `result`, without aborting the rest. When `spec.Correlations` is non-empty (from `--conditional` or `--include-correlations`), the report also carries `pairwise`: one `{a, b, source_rho, synthetic_rho, delta, n}` entry per captured numeric-numeric pair, `delta = abs(source_rho - synthetic_rho)` computed with the same `pearson` helper profile capture itself uses (`synth.BuildPairwise`) — never a second, divergent metric. A pair whose synthetic partition can't produce a defined correlation gets `error` instead. `SynthOptions.FidelityWarnings` (typically `Profile.Warnings` — e.g. a thin-pair warning) is copied verbatim onto the report's own `warnings`. Both `pairwise` and `warnings` are absent, never an empty placeholder, when there is nothing to report. Omitting the flag writes nothing.

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file. Any sampler change that breaks determinism is a contract break.

Seed splitting uses a 64-bit avalanche; seeds differing by 1 produce uncorrelated streams. `Seed == 0` is stable, not "random". `nullableSampler` always draws the inner value first, then the null mask — the seeded stream is invariant to which rows are null.

## Library embedding

`pulse.Pulse.Synth` and `pulse.Pulse.Profile` route through the embedded filesystem — `pulse.New(pulse.Options{FS: afero.NewMemMapFs()})` for hermetic tests.

## Gotchas

- Constraints + `monotonic_from`: monotonic ignores RNG, so a rejected row still increments the counter.
- Correlations + clamping: heavy-clamped `normal` distorts the target — `|rho|_actual < |rho|_requested`.
- Correlations + non-normal marginal: a schema-mode `lognormal`/`uniform`/`exponential` field named in `correlations` keeps its mean/std but loses its shape (pulled toward Gaussian) once correlated.
- `weighted_categorical` weights normalize at sample time; absent weights default to uniform.
- `uniform_date` is inclusive both ends.
- `regex` is restricted: literal / charclass / fixed-repeat / alternation / bounded `*+{m,n}`. No backreferences.
- Profile capture emits exact statistics — see Privacy above.

## See

- Recipes: `pulse_examples_search tags=["synth"]` plus atomic `op-synth-<kind>`.
- `cohort-schema-design` — field types, dictionaries, null bitmap.
- `import-best-practices` — round-trip via importers.
- `error-code-reference` — `PULSE_SYNTH_*` / `PULSE_PROFILE_*` recovery.
