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

`correlations` is a list of `{a, b, rho}` triples. Gaussian-copula construction (`synth/copula.go`): draw a correlated standard-normal vector `u` via Cholesky, map each `u_i` through the standard normal CDF Φ (`math.Erf`) to `p_i = Φ(u_i) ~ Uniform(0,1)`, then apply each field's OWN quantile function `Q_i(p_i)` (clamped if `normal` with declared `min`/`max`) — `normal`/`uniform`/`lognormal`/`exponential` only (`fieldMoments`/`quantileFor`); any other distribution named in `correlations` refuses with `SERVICE_VALIDATION`. `normal` reduces exactly to `mean + std*u` since `Φ⁻¹(p_i) == u_i` by construction. Preserves each field's OWN marginal shape (mean, std, AND skew), not just mean/std — the copula targets Spearman (rank) correlation exactly; realized Pearson lands within test tolerance for the supported distributions, closer to exact as the non-normal field's variance shrinks (see `TestSynth_CopulaPreservesLognormalMarginal`). `|rho| ≥ 1` rejected at validation. Chosen over a rank-based empirical copula because schema-mode specs have no historical samples to rank against — replaces a removed v0 blend (±5%·std nudge, untested fidelity) and a removed v1 direct `mean + std*u` override (exact rho, Gaussian-forced marginal); see `TestSynth_CorrelationReconstructionWithinTolerance`.

## Profile mode

Capture via `pulse_profile_create`; synth via `pulse_synth_from_profile`. Profile JSON captures per field:

- Numeric: mean, std, min, max, optional percentiles, null-rate.
- Categorical: top-K values + frequencies, cardinality, null-rate.
- Date: observed range, weekday histogram, null-rate.
- Pairwise: strongest `|rho|` correlations (capped by `--correlation-top-k`).
- Conditional (`--conditional`, additive/omitempty): `conditional.numeric_pairs`, row-aligned `{a, b, rho, n}` where `n` is the true co-occurrence count (both non-null, same row) — more accurate than Pairwise's independently-capped reservoirs. `SpecFromProfile` prefers it over `pairwise` when present; absent (old or plain-`--include-correlations` documents) falls back unchanged. `n < 30` (`synth.MinPairObservations`) still ships, never refused — appends a warning naming the pair; the mechanism is generic across pair kind for later categorical/set_* reuse.
- Conditional categorical-categorical (`conditional.categorical_pairs`, same `--conditional` flag): one `{a, b, cells, n}` entry per categorical-categorical field pair, `cells` a bounded contingency table of `{a_value, b_value, count}` co-occurrences and `n` the pair's true co-occurrence count (both non-null, same row). Two caps compose: each field's own `--top-k` cap collapses an out-of-top-K value to `"other"` before the joint table is built, then `synth.ContingencyCellCap` (128) bounds the joint table itself — any raw cell count over the cap is ranked by count descending and the tail (plus any pre-existing `("other","other")` cell from the per-field collapse) folds into one merged `("other","other")` catch-all, never a second competing entry, saturating to exactly the cap whenever raw cardinality exceeds it. Thin cells (`count < synth.MinPairObservations`) reuse the same warning helper as numeric pairs — no separate mechanism. `SpecFromProfile` consumes this into `Spec.CategoricalPairs`.
- Conditional categorical-numeric (`conditional.categorical_numeric_pairs`, same `--conditional` flag): one `{a, b, categories, n}` entry per categorical-numeric field pair (`a` categorical, `b` numeric) — `categories` is one `{category, mean, std, n}` conditional summary per observed category of `a`, computed online from an exact running sum/sumSq (no reservoir cap), and `n` the pair's true co-occurrence count summed across categories. Only one cap applies here — the categorical field's own `--top-k` collapses an out-of-top-K value to `"other"` before its conditional mean/std is computed — there is no second, joint-cardinality cap, because this section emits one numeric summary per already-capped category rather than a joint table over two categorical axes. A thin category (`n < synth.MinPairObservations`) reuses the same warning helper as the other two pair kinds. `SpecFromProfile` consumes this into `Spec.CategoricalNumericPairs`.
- Shape (`--fit-shape`, additive/omitempty, numeric fields only): `numeric.shape = {means, stds, weights}`, a fitted 2-component Gaussian mixture, captured only when it is a genuine improvement over the plain normal by **BIC** (`-2*logLikelihood + k*log(n)`, k=2 for normal vs k=5 for the 2-component mixture — the extra-parameter penalty is what stops a near-normal field from being force-fit) AND the two fitted means are at least `0.75*avgStd` apart (a second guard against a BIC-accepted but spuriously-overlapping local optimum). EM (`synth/shape.go`, `fitTwoComponentEM`) runs a fixed 50 iterations from a deterministic percentile-based init (25th/75th) — no RNG, so capture stays deterministic — with a std floor (5% of the field's overall std) preventing variance collapse. `SpecFromProfile` emits `mixture` (reusing `DistMixture`, E4-S1's sampler, verbatim — no new sampling logic) instead of `normal` whenever `shape` is present; absent for every pre-`--fit-shape` document and for any field whose fit wasn't kept, in which case reconstruction is byte-for-byte the pre-existing `normal` path. Fixed at exactly 2 components; no BIC sweep over component count. A field carrying `shape` is excluded from `Spec.Correlations` wiring (copula only knows closed-form moments for `normal`/`uniform`/`lognormal`/`exponential`) and from `--conditional`'s categorical-numeric pair wiring for that field (which only fires when the reconstructed distribution is `normal`) — shape-fitting and those two features have not been asked to compose, so the shape fit silently wins over either.

`synth.SpecFromProfile` reconstructs a Spec: numeric → `normal` clamped to observed min/max (or `mixture` when a captured `shape` is present, see below), categorical → `weighted_categorical`, date → `uniform_date`. Captured correlations become the conditional-Gaussian reconstruction described above. Unsupported types → `PULSE_PROFILE_FIELD_UNSUPPORTED`; drop to schema mode for those.

### Set (multi-select) field profiling

`set_*` fields (`skills/type-set-u8.md` et al.) are "select all that apply" bitmasks over one shared dictionary — bit `i` ↔ dictionary entry `i`. **Design decision (E5-S1):** each dictionary entry is profiled as an independent Bernoulli-selected sub-field for *marginal* purposes — `set.options[i].frequency` is `P(bit i set | field non-null)`, exactly the semantics a respondent answers each option's own yes/no question. Rejected alternatives: collapsing the whole mask into one categorical value treats every distinct combination as an arbitrary, combinatorially exploding category (2^n possible masks); treating options as N fully independent binaries with no shared identity would ignore that all N ride one bounded dictionary and would give E5-S2's joint capture nothing to reuse. This resolves the open PRD question for marginals only — joint/conditional structure between options, or between a set field and another field, is E5-S2's job, and it reuses this same per-option addressing.

`profile create` always captures `set = {n, options: [{value, count, frequency}]}` for a `set_*` field (no new flag — on by the same unconditional discipline as Numeric/Categorical/Date), additive/`omitempty`. `options` lists EVERY dictionary entry in bit/dictionary order — never sorted by frequency like `categorical.top` — so two profiles of the same schema compare position-for-position regardless of which option is more popular. `n` is the non-null row count (`null_rate`'s own denominator); `frequency = count / n`. Before this story a `set_*` field fell through the profiler's `default` branch and was silently accumulated as a plain numeric field (its raw bitmask integer averaged as if it were a scalar) — `numeric` and `categorical` are now correctly `nil` for a `set_*` field, and it no longer pollutes `--conditional`'s numeric-numeric pair capture by riding along in `jointFieldNames`. `SpecFromProfile` reconstructs it as `set_bernoulli` (E5-S3, see below).

**Set joint/conditional capture (E5-S2).** `--conditional` extends to pairs involving a `set_*` field by running E3's per-pair-type capture machinery ONCE PER OPTION — E5-S1's per-option Bernoulli sub-field addressing composes directly with E3's pairwise mechanisms rather than needing a parallel one. Three new `ConditionalProfile` sections, all additive/`omitempty`, all reusing existing types verbatim:

- `conditional.set_categorical_pairs`: one `{set, option, categorical, cells, n}` entry per (set field option, categorical field) combination — `cells` a `ContingencyCell` table with `a_value` fixed to `"selected"`/`"not_selected"` and `b_value` the categorical field's value (subject to that field's own `--top-k` collapse to `"other"`, exactly as `categorical_pairs` already does on its own axes) — reuses `computeConditionalCategoricalPairs`'s `collapseCells`/`ContingencyCellCap`/`otherCategoryLabel` machinery unchanged. The option axis never itself needs a top-K collapse: it is always the fixed two-value domain.
- `conditional.set_numeric_pairs`: one `{set, option, numeric, categories, n}` entry per (set field option, numeric field) combination — `categories` is `CategoricalNumericCategoryStat` entries keyed `"selected"`/`"not_selected"`, computed online exactly as `categorical_numeric_pairs`' per-category mean/std.
- `conditional.set_set_pairs`: one `{set_a, option_a, set_b, option_b, cells, n}` entry per option-pair between two DIFFERENT set_* fields — `cells` a 2x2 `ContingencyCell` table over `{"selected","not_selected"}` x `{"selected","not_selected"}`. Never captured between two options of the SAME field.

Cardinality stays bounded because both option-count factors are already capped by the type system before this story ever runs: a set field's option count is `FieldType.MaxSetEntries()` (8/16/32/64 for `set_u8/u16/u32/u64`), and `ContingencyCellCap` (128) still bounds every individual cell table exactly as it does for two plain categorical fields — no new, separate "outer" cap was introduced, matching the precedent that `categorical_pairs` itself has no cap on the NUMBER of field pairs, only on cells within one. Every warning path reuses the same `thinPairWarning` helper (kinds `"set-categorical"`/`"set-numeric"`/`"set-set"`) — no fifth, divergent warning mechanism.

**Set generation (E5-S3).** `SpecFromProfile` reconstructs a `set_*` field as `set_bernoulli` (`Spec.Fields[i].Params = {options, frequencies}`, straight from `FieldProfile.Set.Options`) and wires `Conditional.SetCategoricalPairs`/`SetNumericPairs`/`SetSetPairs` into `Spec.SetCategoricalPairs`/`SetNumericPairs`/`SetSetPairs` — same "both sides reconstructed to the expected distribution" guard the categorical wiring already applies (set axis fixed to `set_bernoulli`). At generation time each declared pair resamples ONE option's bit conditioned on the paired field's already-drawn value (set-categorical/set-numeric) or another set field's already-drawn option (set-set) — applied after categorical joint structure and before the numeric-numeric correlator, in the fixed order set-set → set-categorical → set-numeric documented on `drawRow`. A `set_*` field's dictionary is pre-populated ONCE, from `params.options`' own declared order, at schema-build time — never lazily by first-touch during row generation — because the sampler's row value is a `map[string]bool` and Go map iteration order is randomized per process; pre-registration is what keeps bit assignment, and therefore the Determinism contract below, independent of that randomization. `AugmentFromProfile`'s merged schema validates the profile-derived and source dictionaries name the identical option universe in the identical order before trusting either side's bits against the other (`PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH` otherwise) and pre-populates the merged dictionary from the source's own order. A profile with only marginal `set` data (no `--conditional`) still generates correctly — joint structure is additive, never required.

### Categorical joint structure (generation)

`Spec.CategoricalPairs` / `Spec.CategoricalNumericPairs` (both additive/omitempty, populated by `SpecFromProfile` from `Conditional.CategoricalPairs`/`Conditional.CategoricalNumericPairs` when present) drive two generation-time resample steps, applied after every field's own independent draw and before the numeric-numeric correlator: for a categorical-categorical pair, field B is resampled from the captured per-`a_value` conditional distribution over B's cells (falling back to the pair's pooled B marginal for an A value with no captured row, e.g. one the joint-cell cap folded away); for a categorical-numeric pair, numeric field B is resampled from `Normal(mean, std)` using the category A's own drawn value looked up, clamped to B's observed `[min, max]` exactly as B's own unconditional `normal` reconstruction clamps. Both steps are no-ops (nil slices) when `Conditional` was absent at capture time — a profile without `--conditional` reproduces today's independent-marginal generation byte-for-byte, and `AugmentFromProfile` consumes the SAME `Spec` fields as the bare `SpecFromProfile`+`Synth` path (no divergent wiring). See `TestAugmentFromProfile_ReconstructsCategoricalContingencyWithinTolerance` / `...CategoricalNumericMeanWithinTolerance`.

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
