---
name: synthetic-data
description: `pulse_synth_from_schema` vs `pulse_synth_from_profile`, multi-predictor models, correlations, determinism via seed. Topical design; per-distribution detail in atomic op-synth-* skills, `rules[]` / `constraints[]` in synth-structural-rules.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [pulse_synth_from_schema, pulse_synth_from_profile, synth, distributions, correlations, models, determinism]
---

# Synthetic data

Pulse synthesizes deterministic `.pulse` cohorts via `pulse_synth_from_schema` / `pulse_synth_from_profile` (matching CLI leaves).

The CONTRACT surface — rules not inferable from the code you are editing, whose violation is SILENT. Per-distribution params: atomic `op-synth-*`. `rules[]` / `constraints[]`: `synth-structural-rules`. The measurements behind each rule and every closed design question: `docs/src/cli/synth-calibration.md`.

Synth does not emit `Response.Components` — it writes a `.pulse` file.

## Two modes

| Mode | Input | When |
|---|---|---|
| `pulse_synth_from_schema` | hand-written JSON spec | caller knows desired shape — fixtures, CI seeds, demos |
| `pulse_synth_from_profile` | profile JSON + the source cohort it was captured from | tagged top-up: add rows matching a real cohort's marginals, source untouched |

**Privacy.** Synth does NOT preserve privacy. A profile without DP noise leaks the empirical distribution — top-K categoricals reveal rare values, percentiles reveal ranges, coefficients expose structure. Add a calibrated noise mechanism if the source is sensitive.

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

Fifteen kinds; params and clamp semantics per kind in `op-synth-<kind>`. Registry: `synth.AllDistributions()`.

`uniform` `[min,max)` · `normal` (optional `min`/`max` clamp) · `lognormal` · `exponential` · `poisson` · `pareto` · `bernoulli` · `monotonic_from` (ignores RNG — primary keys) · `weighted_categorical` (uniform when `weights` absent) · `uniform_date` (inclusive both ends) · `regex` (literal/charclass/fixed-repeat/alternation/bounded quantifier; no backreferences) · `mixture` (≥2 components; bimodal or skewed shapes a single `normal` collapses).

Three carry rules you cannot guess:

- `discrete` — `values` strictly ASCENDING + optional `weights`. The exact per-level histogram of an integer column; what `profile create` reconstructs every capped integer field from (see Small-integer marginals).
- `constant` — coerced ONCE at spec-compile time to the field's own ROW shape: bool → 1/0 on any scalar, array or object of option names → a `set_*` mask, string REQUIRED for a categorical, verbatim string for `decimal128` (`ParseDecimal128` is exact). The only sampler taking its value from the document, so the only one that can put a Go `bool` where the expr environment promises `float64`; a shape the row cannot hold is refused at parse.
- `set_bernoulli` — `options` also PRE-REGISTERS the field's dictionary at schema-build time. A determinism requirement, not an optimisation (see Set profiling).

17 of the 18 field types are reachable — **`datetime` is NOT**: `fieldTypeFromName` (`synth/writer.go`) has no case for it, so `"type": "datetime"` refuses with `unknown field type`. Use `date` (epoch days) or `u64` epoch seconds. `decimal128` needs `params.scale` matching the declared scale (banker's rounding). Bit-packed (`u4`, `packed_bool`) use one byte per row in the writer. `nullable: true` opts into the null bitmap; nulls NEVER ride an inline sentinel.

### Constraints and structural rules → `synth-structural-rules`

`constraints[]` (reject-and-redraw) and the whole `rules[]` surface — slot semantics, declaration order and last-write-wins, the `set_expr` coercion matrix, `null_together`, the shared `expr-lang` row environment, the eager `PULSE_SYNTH_RULE_*` codes — live there. Read it before authoring a spec that gates, masks or derives a field rather than merely distributing one.

### Pairwise correlations

`correlations` is a list of `{a, b, rho}` realized by a Gaussian copula (`synth/copula.go`): correlated standard normals `u` via Cholesky → `p_i = Φ(u_i)` → each field's OWN quantile `Q_i(p_i)`.

- **Supported marginals:** `normal` / `uniform` / `lognormal` / `exponential` / `mixture` / `bernoulli` / `discrete` (`fieldMoments` validates, `quantileFor` builds `Q_i`). Anything else in `correlations` → `SERVICE_VALIDATION`. `|rho| ≥ 1` rejected at validation.
- **A participant must also be a SCALAR field TYPE, and that predicate is DERIVED, never transcribed.** `isNumericFieldType` reads `fieldTypeFromName`: every declarable non-`categorical_*`, non-`set_*` type qualifies (`u4`…`u64`, `f32`/`f64`, `date`, `decimal128`, `packed_bool`) and nothing else. A hand-written list silently dropped a whole type out of `Spec.Correlations` once — do not reintroduce one. The boundary is TYPE, not distribution: a `date`'s `uniform_date` is refused one call later, NAMING the distribution.
- **`normal` reduces exactly to `mean + std*u`.** Otherwise the copula targets RANK correlation exactly and preserves each field's own marginal (mean, std AND skew); realized PEARSON attenuates for a wide-variance non-normal marginal, and `min`/`max` clamping distorts further.
- **A step/staircase participant (`bernoulli`, `discrete`) holds its marginal EXACTLY and attenuates the realised correlation**, costing rank as well as Pearson once ties dominate.
- The `mixture` arm exists for the MODEL draw, not for `correlations`: a `--fit-shape` field is excluded from the value-scale matrix either way (pre-claimed when unmodelled, permanently refused when modelled — its structure rides `residual_correlations`).
- **Unmeasured entries: ASSUME AND RECORD.** `correlations` is a LIST, the Cholesky needs a MATRIX, so every unnamed pair is filled with 0 (drawn independent) and `buildCorrelator` WARNS with the count. Refusal was rejected — an incomplete list is the normal input. `cholesky`'s ridge also reports its accumulated diagonal jitter, which pulls realized correlations toward zero. Neither warning changes a number.

Gates: `TestSynth_CorrelationReconstructionWithinTolerance`, `TestSynth_CopulaPreservesLognormalMarginal`.

## Profile mode

Capture via `pulse_profile_create`; synth via `pulse_synth_from_profile`. Per field:

- Numeric: mean, std, min, max, optional percentiles, null-rate, plus `discrete` (per-level histogram) for a capped integer column. Categorical: top-K + frequencies, cardinality, null-rate. Date: range, weekday histogram, null-rate. `set_*`: below.
- Pairwise: strongest `|rho|` (capped by `--correlation-top-k`).
- `--conditional` (additive / `omitempty`, all under `conditional.`): `conditional.numeric_pairs` `{a,b,rho,n}` row-aligned, `n` the TRUE co-occurrence count — preferred over `pairwise` when present, falling back unchanged when absent. `conditional.categorical_pairs` `{a,b,cells,n}` over at most 10,000 rows by genuine Algorithm-R reservoir sampling (`--seed`), NOT first-N, so a block-ordered source is unbiased; two caps compose — each field's `--top-k` collapses to `"other"` FIRST, then `ContingencyCellCap` (128) folds the tail into one merged `("other","other")`. `conditional.categorical_numeric_pairs` `{a,b,categories,n}`, one online `{category,mean,std,n}` per category (no reservoir cap, no second joint cap).
- **Thin-pair warning:** `n < 30` (`synth.MinPairObservations`) still SHIPS — never refused — with a warning naming the pair (`thinPairWarning`). Every pair kind reuses this mechanism.
- `--fit-models` (`models`) and `--residual-correlations` (`residual_correlations`, requires `--fit-models`): `synth-models`.
- `--fit-shape` (`numeric.shape`, numeric only): a 2-component Gaussian mixture (`synth/shape.go`) kept only when it beats plain normal on **BIC** AND the means are ≥ `0.75*avgStd` apart. `fitTwoComponentEM` runs a FIXED 50 iterations from a deterministic percentile init (no RNG) with a std floor at 5% of overall std. Fixed at 2 components, no sweep. `SpecFromProfile` then emits `mixture` instead of `normal`.

`SpecFromProfile` reconstructs: `packed_bool` → `bernoulli`; capped small integer → `discrete`; other numeric → `normal` clamped to observed min/max (or `mixture` when `shape` is present); categorical → `weighted_categorical`; date → `uniform_date`; `set_*` → `set_bernoulli`. Unsupported → `PULSE_PROFILE_FIELD_UNSUPPORTED`.

Both non-normal numeric arms below share one shape, and it is the part to get right: the arm runs **AHEAD of `--fit-shape`** (a mixture fits such a column happily and reproduces its shares no better), capture is UNCONDITIONAL (this is the DEFAULT reconstruction being wrong, not an enhancement), and **all THREE writers that can reach the field are covered — fixing one leaves the others wrong with no signal**: its own sampler, a conditional pair, and the model draw.

### Boolean marginals

**A `packed_bool` reconstructs as `bernoulli` with `p` = the observed mean.** A boolean lands in the NUMERIC accumulator, and the obvious reading of that summary — `normal(mean, std)` clamped to `[0,1]` — is wrong on the wire, severely and silently: the field holds ONE BIT, the writer must reduce a continuous draw to 0/1, and no threshold over a clamped normal lands right (a 20% boolean generated at 69%, a 50% one at 84%). `bernoulli` needs no threshold — the mean IS `p`.

Three writers: its own sampler draws `bernoulli`; a conditional pair draws each cell's captured `Mean` as a PREVALENCE, not a location (`Std` unused), resolved from the target's own `FieldSpec` so pair and marginal cannot disagree; a model draws through the step `Q`, making it a **probit** — `P(1 | row) = Φ((μ − Φ⁻¹(1−p)) / σ)`, so coefficients order rows, not probabilities.

Consequences: `p` of exactly 0 or 1 has zero variance, so a model on it is DROPPED with a warning rather than producing an infinite `1/std` (the constant is deliberately not floored). A bernoulli target has no POINT latent inverse, so `latentFor` refuses it and recovery runs on the calibrated probit score. The continuous arm survives only for a hand-authored `normal` on a `packed_bool`: biased, not correct, and `toBool` rounds at 0.5 there so it is at least not inverted.

**A MODELLED boolean's prevalence is held at `p` only while the generated latent is standard normal.** `Φ(u)` is uniform only when `Var(u) == 1`: true at FIT time by construction, not at GENERATION time, where predictors come from their own reconstructed marginals. The realised prevalence therefore carries a small systematic bias — not the sampler and not the write path, both measured exact. The same mechanism compresses a `normal` target's SD and shifts a `discrete` target's level shares, and a gated field inherits it: a `null_together` block lands on the modelled field's REALISED rate, not its captured one. Gate: `TestSynthModel_BooleanPrevalenceFollowsTheGeneratedLatentVariance`.

### Small-integer marginals

**An integer column with at most 64 observed levels reconstructs as `discrete` — its own exact per-level histogram.** The boolean defect one type wider: the writer stores `floor(v+0.5)`, so a clamped normal is QUANTIZED on the way to the file — the bell flattens the scale's real shape and the clamp piles asymmetric mass on the near bound, while the MEAN comes back roughly right, which is how it survived. A level's observed count IS its weight.

**Which types claim the arm is not a taste judgement.** `isIntegerQuantizedFieldType` is exactly the set of `writeFieldValueForField` arms applying `Floor(f+0.5)` — so `f32`/`f64` (the float is stored), `decimal128` (exact at its scale), `date` (owns `uniform_date`) and `packed_bool` (`bernoulli` runs ahead) are excluded. A type list ALONE is insufficient (a `u16` share-of-wallet is in the defect, a `u64` ID must not be), so the observed-level cap separates them.

**`maxDiscreteLevels` (64, package constant, no flag) is where capture ABANDONS the histogram, never truncates it** — a top-64-of-3,000 histogram makes every share a share of an arbitrary subset — and the ABSENCE of the `discrete` key IS that record, structurally rather than conventionally. Above the cap the field keeps the clamped normal. The boundary is PRAGMATIC, not a fidelity cliff: the clamped normal's per-level RELATIVE error is scale-invariant; only its absolute error shrinks (~1/K).

Three writers: its own sampler draws the staircase; a conditional pair locates the cell's captured moments ON that staircase — `value = Q(Φ((cellMean−fieldMean)/fieldStd + (cellStd/fieldStd)·z))`, the SAME construction the model stage uses — resolved from the target's own `FieldSpec`, never a wire flag; a model draws through the staircase `Q`, an **ordered probit** (predictors shift the latent, the histogram holds exactly, direction and ordering carry, scale-point magnitude does not).

Consequences: no POINT latent inverse, so `latentFor` refuses it as it refuses `bernoulli` and recovery runs on the probit score. Value-scale correlation on a `discrete` participant attenuates. And the **pre-rounding gotcha DISAPPEARS** for these fields — the row value already IS the stored integer, so `{"set_expr": {"nps": "round(nps)"}}` is a no-op. That advice stays live only for `f32`/`f64`, an integer column over the cap, and a hand-authored continuous distribution on an integer field.

### Set (multi-select) field profiling

`set_*` fields are "select all that apply" bitmasks over one shared dictionary — bit `i` ↔ dictionary entry `i`. Each entry profiles as an independent Bernoulli sub-field for MARGINAL purposes: `set.options[i].frequency` is `P(bit i set | field non-null)`.

`profile create` always captures `set = {n, options: [{value, count, frequency}]}` (no flag). **`options` lists EVERY dictionary entry in bit order — never sorted by frequency like `categorical.top`** — so two profiles of one schema compare position-for-position. `n` is the non-null row count.

**Joint capture.** `--conditional` extends to `set_*` pairs by running the per-pair-type machinery ONCE PER OPTION:

| Section | One entry per | Shape |
|---|---|---|
| `conditional.set_categorical_pairs` | (set option, categorical field) | `{set, option, categorical, cells, n}`; `a_value` fixed `"selected"`/`"not_selected"`, `b_value` the categorical value (own `--top-k` collapse) |
| `conditional.set_numeric_pairs` | (set option, numeric field) | `{set, option, numeric, categories, n}`; `categories` keyed `"selected"`/`"not_selected"` |
| `conditional.set_set_pairs` | option pair between two DIFFERENT set fields (never two options of one field) | `{set_a, option_a, set_b, option_b, cells, n}`, a 2x2 table |

The option axis never needs a top-K collapse (fixed two-value domain). Bounded by `FieldType.MaxSetEntries()` (8/16/32/64) and `ContingencyCellCap` (128). Thin cells reuse `thinPairWarning`.

**Generation.** Each pair resamples ONE option's bit conditioned on the paired field's already-drawn value, in the fixed order set-set → set-categorical → set-numeric, after categorical joint structure and before the correlator. **A `set_*` dictionary is pre-populated ONCE from `params.options`' declared order at schema-build time, never lazily during generation** — the row value is a `map[string]bool` and Go randomizes map iteration per process, so pre-registration is what keeps bit assignment (hence determinism) independent of it. `AugmentFromProfile` requires both dictionaries to name the identical option universe in the identical ORDER (`PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`). A marginal-only profile still generates correctly — joint structure is additive, never required.

### Categorical joint structure (generation)

`Spec.CategoricalPairs` / `CategoricalNumericPairs` drive two resample steps after every field's own draw and before the correlator: categorical-categorical resamples B from the captured per-`a_value` distribution over B's cells (falling back to B's POOLED marginal for an unseen A value); categorical-numeric resamples numeric B keyed on A's drawn value, clamped to B's observed `[min, max]`. Both are no-ops when `Conditional` was absent at capture — a plain profile reproduces independent-marginal generation byte-for-byte.

## Multi-predictor models → `synth-models`

`--fit-models` is how several drivers condition one numeric at once (the pair arms above are pick-one). Capture, predictor selection, thin-level shrinkage, the composed latent draw, latent-scale effects, shape-fitted composition, `residual_correlations`, what a model retires, and the two fidelity RECOVERY sections are all in `synth-models`. Read it before touching `Spec.Models`, `Spec.ResidualCorrelations` or `FidelityReport.Models`.

## Conditional relationship conflicts

Every relationship writes into one shared claim space: a field name for scalar targets (categorical-pair / categorical-numeric B side, set-numeric's numeric side, correlation participants), or `(field, option)` for a `set_*` bit. **Two options on the SAME set field are two DIFFERENT targets, never a conflict.**

`resolveConflicts` (`synth/conflict.go`) runs ONCE per `Spec` at generation setup, never per row, in the fixed `drawRow` priority order:

```
structural-rule pre-claim → linear-model pre-claim → shape-fit pre-claim
  → catPairs → catNumPairs → setSetPairs → setCatPairs → setNumPairs
  → correlations
```

A `Spec.Rules` entry that DETERMINES a field claims it first, because the rule pass runs last in `drawRow` and wins outright; which rule shapes qualify (and the four exclusions, each silent if got backwards) is in `synth-structural-rules`.

First claim wins; every later relationship naming that target is dropped and REPORTED, one warning each, rather than resolving to "whichever stage runs last". `Spec.Correlations` is one joint claimant across all participants (single Cholesky draw) — losing one participant excludes only that field and `buildCorrelator` rebuilds from the survivors.

**The model and shape pre-claims compose; they do not compete.** The model claims first (one additive expression already accounts for every predictor, and a later overwrite would discard the whole account rather than layer onto it); a `--fit-shape` field claims second (its sampler already drew the value). A shape-fitted field carrying a model is claimed by the model, draws through the model, and gets its fitted mixture as `Q` — nothing dropped, no warning. The shape pre-claim still owns every `DistMixture` field no model took.

Warnings surface two ways: `generate()` returns them on `Result.Warnings`; for `--fidelity-report`, `SpecFromProfile` reruns the pass at spec-composition time and merges with capture-time warnings into `FidelityWarnings`. Both always agree — same Spec, same order.

## Tagged top-up contract

`synth from-profile` (`SynthOptions.SourceCohort` / `--source`) always: (1) appends one `_synthetic` `packed_bool` field, `false` on copied rows, `true` on generated ones; (2) writes a **new** output path, distinct from `--source`, which is opened read-only and never mutated; (3) treats `--rows` as a count of NEW rows, never "top up to N total" (`--rows 500` against 200 source rows → 700 rows). `SourceCohort` empty (`synth from-schema`) is the plain path — no tag column. Refusals: `PULSE_SYNTH_SOURCE_REQUIRED`, `PULSE_SYNTH_OUTPUT_REQUIRED`, `PULSE_SYNTH_OUTPUT_COLLISION`, `PULSE_SYNTH_ALREADY_TAGGED`, `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`.

## Fidelity report

`--fidelity-report <path.json>` (only with `SourceCohort` set) writes a `synth.FidelityReport` after generation. Marginals: numeric via `TEST_KS` (`split_by: _synthetic`), categorical via `TEST_CHISQ` — existing operators, no new stat math. `_synthetic` is on-wire `packed_bool` and both operators need categorical, so the bridge presents it through a `categorical_u8` VIEW schema for that call only. That bridge lives in `pulse.go` (`writeSynthFidelityReport`), not `synth/`, to avoid an import cycle: `synth.BuildFidelityReport` takes an injected `TestRunner`. A failed field test reports `error`, not `result`, without aborting the rest. Sections `omitempty` throughout.

**Every section scores ONLY relationships generation ACTUALLY APPLIED.** `spec` carries the full captured cross product; `generate()` applies the subset conflict resolution leaves. Scoring a conflict-dropped pair computes a delta against a relationship the generator never modelled — a number that LOOKS like evidence. `synth.ResolveConflicts` re-runs the identical arbitration against the identical `*Spec`, so a dropped pair has no entry and its own conflict warning explains the absence.

`pairwise` is one `{a, b, source_rho, synthetic_rho, delta, n}` per surviving numeric-numeric pair, via the same `pearson` helper capture uses.

The two structure-recovery sections — `models` and `model_residual_correlations`, asking whether the captured CONDITIONING survived rather than whether the rows look alike — are in `synth-models`.

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file. Any sampler change breaking that is a contract break.

Seed splitting uses a 64-bit avalanche; seeds differing by 1 give uncorrelated streams. `Seed == 0` is stable, not "random". `nullableSampler` always draws the inner value FIRST, then the null mask — the stream is invariant to which rows are null.

**Capture is held to the same bar**: same `(--input, --seed)` MUST produce a byte-identical profile document, or the pipeline is only deterministic downstream of a spec that itself drifts. Two threats, neither the RNG:

1. **Map iteration order.** Anywhere capture folds several accumulators into one shared bucket — canonically the top-K collapse folding out-of-top-K categories into `"other"` — the fold MUST walk SORTED keys: float addition is not associative and Go randomizes map iteration. The `(sumSq - mean*sum)/(n-1)` variance form amplifies rather than absorbs the last-bit difference, so a map-order fold surfaces as a ~1e-10 relative drift in the emitted `std`. Signature: only `"other"` entries move, because only `"other"` has more than one source. Sort — do NOT switch to compensated summation, which is still order-dependent in principle.
2. **Float fusion.** Go may contract `a + b*c` into one FMA; arm64 does, amd64 does not, and the contraction is permitted ACROSS statements. Every product in a capture formula therefore carries an explicit `float64(...)` conversion — the only construct that forbids contraction — and `synth/moments_internal_test.go` fails if one is dropped (it can only DETECT on a contracting architecture, so it is silent on CI by construction). `math.FMA` is the wrong lever: it forces fusion everywhere. **Test fixtures computing float columns are part of the contract** — accumulate in integer units and divide once. Residual, stated not hidden: `--fit-shape` (`math.Exp`/`math.Log`) and `--fit-models` (`processing/regression`, not yet fusion-free) can still differ in last bits across architectures.

## Library embedding

`pulse.Pulse.Synth` / `pulse.Pulse.Profile` route through the embedded filesystem — `pulse.New(pulse.Options{FS: afero.NewMemMapFs()})` for hermetic tests.

## Gotchas

- Constraints + `monotonic_from`: monotonic ignores RNG, so a rejected row still increments the counter.
- Correlations + non-normal marginal: keep sigma modest if Pearson must land tightly.
- **A model coefficient is latent-scale** (never data units unless `Q` is `normal`) and **a quantized (`packed_bool` / `u4`) modelled target attenuates at write time** — recovery flags there are quantization, not a generation fault. Both in `synth-models`.
- `--fit-models` does not imply `--residual-correlations`, and neither implies `--conditional`. `--fit-shape` composes with all of them.
- `weighted_categorical` weights normalize at sample time; absent weights default to uniform.

## See

- Recipes: `pulse_examples_search tags=["synth"]` plus atomic `op-synth-<kind>`.
- `synth-models` — `--fit-models`: capture, selection, shrinkage, the composed latent draw, residual correlations, the fidelity recovery sections.
- `synth-structural-rules` — `rules[]` / `constraints[]`: gating, masking, derived fields.
- `docs/src/cli/synth-calibration.md` — the measurements behind every rule here, and the closed design questions.
- `cohort-schema-design` — field types, dictionaries, null bitmap.
- `regression-modeling` — the `REG_OLS` engine both capture and the recovery refit drive.
- `pulse_errors_lookup` — `PULSE_SYNTH_*` / `PULSE_PROFILE_*` recovery.
