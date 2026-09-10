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

Fourteen kinds. Per-kind params + clamp semantics in atomic `op-synth-<kind>` skills. Registry: `synth.AllDistributions()`.

- `uniform` — closed-open `[min, max)`.
- `normal` — `mean`, `std`, optional `min`/`max` clamp.
- `lognormal` — `mu`, `sigma` (log-space); positive output.
- `exponential` — `lambda`; mean = 1/lambda.
- `poisson` — `lambda`; Knuth for λ<30, normal approx above.
- `pareto` — `xm`, `alpha`; heavy-tailed.
- `bernoulli` — `p`; pairs with `packed_bool` or uint.
- `monotonic_from` — `start`, `step`; deterministic, ignores RNG. Primary keys.
- `weighted_categorical` — `values`, optional `weights`; uniform when absent.
- `mixture` — `means`, `stds`, optional `weights` (parallel lists, `>= 2` components); reproduces bimodal/multimodal or skewed shapes a single `normal` collapses to.
- `uniform_date` — `start`, `end` (YYYY-MM-DD); inclusive.
- `regex` — `pattern`, `max_repeat`; walks `regexp/syntax` AST.
- `constant` — `value`; sentinel fields.
- `set_bernoulli` — `options`, `frequencies`; one independent Bernoulli draw per declared bit, or a joint-structure resample when a set-categorical/set-numeric/set-set pair targets that option (E5-S3). `options` also pre-registers the field's dictionary at schema-build time — see the Set section below.

All 17 `.pulse` field types reachable. `decimal128` requires `params.scale` matching declared scale (banker's rounding). Bit-packed (`u4`, `packed_bool`) use one byte per row in the writer. `nullable: true` opts into the per-record null bitmap; distributions report nulls via the bitmap, never inline sentinels.

### Constraints

`constraints[]` reuse `expr-lang/expr`. Row rejected if any constraint returns false; generator keeps drawing until `row_count` is reached. Default reject cap 50%; beyond that `PULSE_SYNTH_CONSTRAINT_INFEASIBLE` fires. Override via `max_rejection_rate`. Constraints read any field on the same row; no cross-row reach in v1.

### Pairwise correlations

`correlations` is a list of `{a, b, rho}` triples. Gaussian-copula construction (`synth/copula.go`): draw a correlated standard-normal vector `u` via Cholesky, map each `u_i` through the standard normal CDF Φ (`math.Erf`) to `p_i = Φ(u_i) ~ Uniform(0,1)`, then apply each field's OWN quantile function `Q_i(p_i)` (clamped if `normal` declares `min`/`max`) — `normal`/`uniform`/`lognormal`/`exponential` only (`fieldMoments`/`quantileFor`); any other named distribution refuses with `SERVICE_VALIDATION`. `normal` reduces exactly to `mean + std*u` since `Φ⁻¹(p_i) == u_i` by construction. Preserves each field's OWN marginal shape (mean, std, AND skew) — the copula targets Spearman (rank) correlation exactly; realized Pearson lands within test tolerance, closer to exact as the non-normal field's variance shrinks (`TestSynth_CopulaPreservesLognormalMarginal`). `|rho| ≥ 1` rejected at validation. Supersedes a removed v0 ±5%·std blend and a removed v1 direct `mean + std*u` override (exact rho, Gaussian-forced marginal); chosen over a rank-based empirical copula since schema-mode specs have no historical sample to rank against — see `TestSynth_CorrelationReconstructionWithinTolerance`.

## Profile mode

Capture via `pulse_profile_create`; synth via `pulse_synth_from_profile`. Profile JSON captures per field:

- Numeric: mean, std, min, max, optional percentiles, null-rate.
- Categorical: top-K values + frequencies, cardinality, null-rate.
- Date: observed range, weekday histogram, null-rate.
- Pairwise: strongest `|rho|` correlations (capped by `--correlation-top-k`).
- Conditional (`--conditional`, additive/omitempty): `conditional.numeric_pairs`, row-aligned `{a, b, rho, n}`, `n` the true co-occurrence count (both non-null, same row) — more accurate than Pairwise's independently-capped reservoirs. `SpecFromProfile` prefers it over `pairwise` when present, falling back unchanged when absent (old/plain-`--include-correlations` documents). **Thin-pair warning:** `n < 30` (`synth.MinPairObservations`) still ships, never refused — appends a warning naming the pair (helper: `thinPairWarning`); every pair kind below reuses this same mechanism rather than a separate one per kind.
- Conditional categorical-categorical (`conditional.categorical_pairs`, same `--conditional` flag): one `{a, b, cells, n}` entry per categorical-categorical pair — `cells` a bounded contingency table of `{a_value, b_value, count}`, `n` the true co-occurrence count. Captured over at most 10,000 rows via genuine Algorithm R reservoir sampling (`--seed`), not first-N, so a block-ordered source (e.g. sorted by region) is captured unbiased. Two caps compose: each field's own `--top-k` collapses an out-of-top-K value to `"other"` before the joint table is built, then `synth.ContingencyCellCap` (128) bounds the table itself — cells over the cap are ranked by count descending and the tail (plus any pre-existing `("other","other")` cell) folds into one merged `("other","other")` catch-all, saturating to exactly the cap. Thin-pair warning applies (above). `SpecFromProfile` consumes this into `Spec.CategoricalPairs`.
- Conditional categorical-numeric (`conditional.categorical_numeric_pairs`, same `--conditional` flag): one `{a, b, categories, n}` entry per categorical-numeric pair (`a` categorical, `b` numeric) — `categories` is one `{category, mean, std, n}` conditional summary per observed category of `a`, computed online from an exact running sum/sumSq (no reservoir cap), `n` the pair's true co-occurrence count summed across categories. Only the categorical field's own `--top-k` collapse applies here (to `"other"`, before its conditional mean/std is computed) — no second, joint-cardinality cap, since this emits one numeric summary per already-capped category rather than a joint table over two categorical axes. Thin-pair warning applies (above). `SpecFromProfile` consumes this into `Spec.CategoricalNumericPairs`.
- Models (`--fit-models`, **capture-only at this stage — nothing is written to the profile JSON**): one least-squares linear model per numeric field, that field regressed on every categorical **level** and `set_*` **option** in the cohort, fitted on the same single scan via `processing/regression`'s streaming OLS engine (per-field state is O(p²) and independent of row count; no second pass). One reference level per categorical is dropped and folded into the intercept — a full level set plus an intercept is rank-deficient (the dummy trap) — while set options are all kept, because a multi-select is not a partition. Also captures each model's residual scale and its row-aligned **fitted residuals** over a bounded reservoir (own RNG stream off `--seed`, so `--conditional`'s reservoir is never perturbed). A field with no usable predictors, too few non-null rows, or a rank-deficient design (nested `region`/`dma`, single-valued `wave`) loses only ITS model and is named in `Profile.Warnings` — never a refusal. Reachable from the library as `synth.Profile.FittedModels()`; the emitted document is byte-identical with and without the flag, and `SpecFromProfile` / generation are unchanged.
- Shape (`--fit-shape`, additive/omitempty, numeric only): `numeric.shape = {means, stds, weights}`, a fitted 2-component Gaussian mixture kept only when a genuine improvement over plain normal by **BIC** (`-2*logLikelihood + k*log(n)`, k=2 normal vs k=5 mixture — penalizes the extra params so a near-normal field isn't force-fit) AND the two means are ≥ `0.75*avgStd` apart (guards a BIC-accepted but overlapping local optimum). `fitTwoComponentEM` (`synth/shape.go`) runs a fixed 50 iterations from a deterministic percentile init (25th/75th, no RNG) with a std floor (5% of overall std) against variance collapse. `SpecFromProfile` emits `mixture` (`DistMixture`, E4-S1's sampler, verbatim) instead of `normal` whenever `shape` is present; absent otherwise, reconstructing byte-for-byte the pre-existing `normal` path. Fixed at 2 components, no BIC sweep over component count. A `shape` field is excluded from `Spec.Correlations` (copula only has closed-form moments for `normal`/`uniform`/`lognormal`/`exponential`) and from categorical-numeric conditional wiring (fires only for `normal`) — the shape fit silently wins over both.

`synth.SpecFromProfile` reconstructs a Spec: numeric → `normal` clamped to observed min/max (or `mixture` when a captured `shape` is present, see below), categorical → `weighted_categorical`, date → `uniform_date`. Captured correlations become the conditional-Gaussian reconstruction described above. Unsupported types → `PULSE_PROFILE_FIELD_UNSUPPORTED`; drop to schema mode for those.

### Set (multi-select) field profiling

`set_*` fields (`skills/type-set-u8.md` et al.) are "select all that apply" bitmasks over one shared dictionary — bit `i` ↔ dictionary entry `i`. **Design decision (E5-S1):** each entry profiles as an independent Bernoulli sub-field for *marginal* purposes — `set.options[i].frequency` is `P(bit i set | field non-null)`. Rejected: one categorical value per combination (2^n explosion), or N independent binaries with no shared identity (ignores the shared dictionary, gives E5-S2's joint capture nothing to reuse). Marginals only — joint/conditional structure between options, or a set field and another field, is E5-S2's job, reusing this per-option addressing.

`profile create` always captures `set = {n, options: [{value, count, frequency}]}` for a `set_*` field (no new flag, additive/`omitempty`). `options` lists EVERY dictionary entry in bit/dictionary order — never sorted by frequency like `categorical.top` — so two profiles of the same schema compare position-for-position. `n` is the non-null row count (`null_rate`'s denominator); `frequency = count / n`. Previously a `set_*` field fell through the profiler's `default` branch and was silently averaged as a plain numeric field; `numeric`/`categorical` are now correctly `nil` for it, and it no longer pollutes `--conditional`'s numeric-numeric capture via `jointFieldNames`. `SpecFromProfile` reconstructs it as `set_bernoulli` (E5-S3, below).

**Set joint/conditional capture (E5-S2).** `--conditional` extends to pairs involving a `set_*` field by running E3's per-pair-type capture machinery ONCE PER OPTION — E5-S1's addressing composes directly with E3's mechanisms rather than needing a parallel one. Three new `ConditionalProfile` sections, additive/`omitempty`, each reusing an E3 mechanism verbatim:

| Section | One entry per | Shape | Reuses (unchanged) |
|---|---|---|---|
| `conditional.set_categorical_pairs` | (set option, categorical field) | `{set, option, categorical, cells, n}`; `cells` a `ContingencyCell` table, `a_value` fixed `"selected"`/`"not_selected"`, `b_value` the categorical value (subject to its own `--top-k` collapse) | `computeConditionalCategoricalPairs`'s `collapseCells`/`ContingencyCellCap`/`otherCategoryLabel` machinery |
| `conditional.set_numeric_pairs` | (set option, numeric field) | `{set, option, numeric, categories, n}`; `categories` is `CategoricalNumericCategoryStat` keyed `"selected"`/`"not_selected"` | online per-category mean/std, exactly as `categorical_numeric_pairs` |
| `conditional.set_set_pairs` | option pair between two DIFFERENT set_* fields (never two options of the same field) | `{set_a, option_a, set_b, option_b, cells, n}`; `cells` a 2x2 `ContingencyCell` table over `{"selected","not_selected"} x {"selected","not_selected"}` | same contingency-cell mechanics as the categorical-pair row above |

The option axis never needs a top-K collapse — always the fixed two-value domain. Cardinality stays bounded: option count is capped by `FieldType.MaxSetEntries()` (8/16/32/64 for `set_u8/u16/u32/u64`), and `ContingencyCellCap` (128) bounds every cell table exactly as for two plain categorical fields — no new "outer" cap, matching `categorical_pairs`' own precedent (caps cells within a pair, never the number of pairs). Thin cells/categories: same thin-pair warning as above (`thinPairWarning`, kinds `"set-categorical"`/`"set-numeric"`/`"set-set"`).

**Set generation (E5-S3).** `SpecFromProfile` reconstructs a `set_*` field as `set_bernoulli` (`Spec.Fields[i].Params = {options, frequencies}`, from `FieldProfile.Set.Options`) and wires `Conditional.SetCategoricalPairs`/`SetNumericPairs`/`SetSetPairs` into the matching `Spec` slots (same reconstructed-distribution guard the categorical wiring applies; set axis fixed to `set_bernoulli`). Each declared pair resamples ONE option's bit conditioned on the paired field's already-drawn value (set-categorical/set-numeric) or another set field's already-drawn option (set-set), in the fixed order set-set → set-categorical → set-numeric (`drawRow`), after categorical joint structure and before the numeric-numeric correlator. A `set_*` field's dictionary is pre-populated ONCE from `params.options`' declared order at schema-build time, never lazily during generation — the row value is a `map[string]bool` and Go map iteration order is randomized per process, so pre-registration is what keeps bit assignment (and Determinism, below) independent of that randomization. `AugmentFromProfile`'s merged schema validates both dictionaries name the identical option universe in the identical order before trusting either side's bits (`PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH` otherwise). A profile with only marginal `set` data (no `--conditional`) still generates correctly — joint structure is additive, never required.

### Categorical joint structure (generation)

`Spec.CategoricalPairs` / `Spec.CategoricalNumericPairs` (additive/omitempty, from the matching `Conditional` fields) drive two generation-time resample steps, applied after every field's own independent draw and before the numeric-numeric correlator: categorical-categorical resamples field B from the captured per-`a_value` conditional distribution over B's cells (falling back to B's pooled marginal for an A value with no captured row, e.g. cap-folded away); categorical-numeric resamples numeric B from `Normal(mean, std)` keyed on A's drawn value, clamped to B's observed `[min, max]` exactly as B's unconditional `normal` clamps. Both are no-ops when `Conditional` was absent at capture — a plain profile reproduces independent-marginal generation byte-for-byte. See `TestAugmentFromProfile_ReconstructsCategoricalContingencyWithinTolerance` / `...CategoricalNumericMeanWithinTolerance`.

### Conditional relationship conflicts

Every relationship above writes into one shared claim space at generation time: a field name for scalar targets (categorical-pair/categorical-numeric B side, set-numeric's numeric side, correlation participants), or `(field, option)` for a set_* field's own bit. **Two options on the SAME set_* field are two DIFFERENT targets, never a conflict** — each writes only its own entry in the row's `map[string]bool`. A `--fit-shape` field (`DistMixture`) is pre-claimed first, since its sampler already drew the row's value before any conditional stage runs.

`resolveConflicts` (`synth/conflict.go`, E6-S1) runs ONCE per `Spec` at generation setup, never per row, walking the fixed `drawRow` priority order: shape-fit pre-claim → `catPairs` → `catNumPairs` → `setSetPairs` → `setCatPairs` → `setNumPairs` → `correlations`. First claim wins; every later relationship naming the same target is dropped and reported, one warning each, instead of resolving to "whichever stage runs last." `Spec.Correlations` is one joint claimant across all its participants (single Cholesky draw) — losing one participant to an earlier claim excludes only that field; `buildCorrelator` rebuilds from whatever pairs survive.

Warnings surface two ways: `generate()` (`synth/writer.go`) returns them on `Result.Warnings` for every synth call. For `synth from-profile --fidelity-report`, `SpecFromProfile` independently reruns the pass at spec-composition time (capture precedes generation, so a persisted profile can't carry a synth-time conflict) and merges its warnings with the profile's capture-time thin-pair warnings into `FidelityWarnings` — landing on the fidelity report's `warnings` array. Both calls always agree — same Spec, same priority order.

### Tagged top-up contract

`synth from-profile` (`SynthOptions.SourceCohort` / `--source`) always: (1) appends one `_synthetic` `packed_bool` field — `false` on rows copied from source, `true` on newly generated rows; (2) writes to a **new** output path, distinct from `--source` — source is opened read-only, never mutated; (3) treats `--rows`/`RowCount` as an explicit count of *new* rows, never "top up to N total" (`--rows 500` against 200 source rows → 700-row output). `SourceCohort` empty (`synth from-schema`'s default) is the unmodified plain path — no tag column, output may equal any path. Refusals: `PULSE_SYNTH_SOURCE_REQUIRED`, `PULSE_SYNTH_OUTPUT_REQUIRED`, `PULSE_SYNTH_OUTPUT_COLLISION` (output == source), `PULSE_SYNTH_ALREADY_TAGGED` (source already tagged), `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH` (profile fields don't line up with source schema).

`--fidelity-report <path.json>` (`SynthOptions.FidelityReportPath`, only with `SourceCohort` set) writes a `synth.FidelityReport` JSON document after generation: numeric fields via `TEST_KS` (`split_by: _synthetic`), categorical fields via `TEST_CHISQ` (contingency against `_synthetic`) — existing operators, no new stat math. `_synthetic` is on-wire `packed_bool`; both operators need categorical, so the bridge presents it through a `categorical_u8` view schema (`synth.SyntheticAsCategoricalSchema`) for that call only, byte format unchanged. The bridge lives in `pulse.go` (`writeSynthFidelityReport`), not `synth/` (avoids an import cycle through `descriptor`/`processing`'s tests) — `synth.BuildFidelityReport` takes an injected `TestRunner` instead. A failed field test reports `error`, not `result`, without aborting the rest. When there are any surviving numeric-numeric pairs (below), the report also carries `pairwise`: one `{a, b, source_rho, synthetic_rho, delta, n}` per pair, `delta = abs(source_rho - synthetic_rho)` via the same `pearson` helper capture uses (`synth.BuildPairwise`). A pair whose synthetic partition can't produce a defined correlation gets `error`. `SynthOptions.FidelityWarnings` (`Profile.Warnings` plus synth-time conflict warnings, above) is copied verbatim onto `warnings`. Both `pairwise` and `warnings` are absent, never an empty placeholder, when there's nothing to report; omitting the flag writes nothing.

**Every pairwise section is scored against `synth.ResolveConflicts(spec)`, never `spec`'s own unpruned pair slices.** `spec` carries every relationship `SpecFromProfile` captured — the full cross product a `--conditional` profile can produce (every categorical field × every numeric field, for the categorical-numeric section) — but `generate()` only ever applies the subset conflict resolution resolves out of it (Conditional relationship conflicts, above). Fidelity-checking a conflict-dropped pair would compute a delta against a relationship the generator never modeled — misleading, not merely redundant with its own conflict warning. `ResolveConflicts` re-runs `resolveConflicts` against the identical `*Spec` `generate()` was given (same call site, `Pulse.Synth`), so the resolution is guaranteed to match: a dropped pair simply has no entry in its pairwise section, and its own `"conditional relationship conflict"` warning (already on `warnings`) is what explains the absence. A profile's full captured cross product can dwarf the surviving set — one conditioning relationship per target field — so most of what `--conditional` captured legitimately never appears in the fidelity report at all.

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file. Any sampler change that breaks determinism is a contract break.

Seed splitting uses a 64-bit avalanche; seeds differing by 1 produce uncorrelated streams. `Seed == 0` is stable, not "random". `nullableSampler` always draws the inner value first, then the null mask — the seeded stream is invariant to which rows are null.

## Library embedding

`pulse.Pulse.Synth` and `pulse.Pulse.Profile` route through the embedded filesystem — `pulse.New(pulse.Options{FS: afero.NewMemMapFs()})` for hermetic tests.

## Gotchas

- Constraints + `monotonic_from`: monotonic ignores RNG, so a rejected row still increments the counter.
- Correlations + clamping: heavy-clamped `normal` distorts the target — `|rho|_actual < |rho|_requested`.
- Correlations + non-normal marginal: the Gaussian-copula construction preserves a `lognormal`/`uniform`/`exponential` field's own marginal shape once correlated (not just mean/std), but the REALIZED Pearson correlation is attenuated relative to the requested rho for a wide-variance non-normal marginal (a known copula effect, not a bug) — keep sigma/spread modest for a non-normal correlated field if the realized Pearson must land tightly on the requested rho.
- `weighted_categorical` weights normalize at sample time; absent weights default to uniform.
- `uniform_date` is inclusive both ends.
- `regex` is restricted: literal / charclass / fixed-repeat / alternation / bounded `*+{m,n}`. No backreferences.
- Profile capture emits exact statistics — see Privacy above.

## See

- Recipes: `pulse_examples_search tags=["synth"]` plus atomic `op-synth-<kind>`.
- `cohort-schema-design` — field types, dictionaries, null bitmap.
- `import-best-practices` — round-trip via importers.
- `error-code-reference` — `PULSE_SYNTH_*` / `PULSE_PROFILE_*` recovery.
