---
name: synthetic-data
description: `pulse_synth_from_schema` vs `pulse_synth_from_profile`, multi-predictor models, correlations, determinism via seed. Topical design; per-distribution detail in atomic op-synth-* skills, `rules[]` / `constraints[]` in synth-structural-rules.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [pulse_synth_from_schema, pulse_synth_from_profile, synth, distributions, correlations, models, determinism]
---

# Synthetic data

Pulse synthesizes deterministic `.pulse` cohorts via `pulse_synth_from_schema` / `pulse_synth_from_profile` (matching CLI leaves). This file covers mode choice, conditioning, correlations, determinism. Per-distribution detail in atomic `op-synth-*` skills; the `rules[]` / `constraints[]` structural surface in `synth-structural-rules`.

Synth does not emit `Response.Components` — it writes a `.pulse` file; Components is reserved for Process results.

## Two modes

| Mode | Input | When |
|---|---|---|
| `pulse_synth_from_schema` | hand-written JSON spec | caller knows desired shape — fixtures, CI seeds, demos |
| `pulse_synth_from_profile` | profile JSON + the source cohort it was captured from | tagged top-up: add rows matching a real cohort's marginals, source untouched |

**Privacy.** Synth does NOT preserve privacy on its own. A profile without DP noise leaks the empirical distribution — top-K categoricals reveal rare values, percentiles reveal ranges, correlations and fitted coefficients expose structure. Add a calibrated noise mechanism if the source is sensitive.

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
- `constant` — `value`; sentinel fields. Coerced ONCE at spec-compile time to the field's own row shape (bool to 1/0 on any scalar, array of option names to a set mask, string required for a categorical, verbatim string for `decimal128` since `ParseDecimal128` is exact). It is the only sampler whose value comes from the document rather than a draw, so it was the only one that could put a Go `bool` where `sentinelFor` promises a `float64` and break every expression over the field at run time; a shape the row cannot hold is now refused at parse.
- `set_bernoulli` — `options`, `frequencies`; one independent Bernoulli draw per declared bit, or a joint-structure resample when a set-categorical/set-numeric/set-set pair targets that option. `options` also pre-registers the field's dictionary at schema-build time — see the Set section below.

17 of the 18 `.pulse` field types are reachable — **`datetime` is not**: `fieldTypeFromName` (`synth/writer.go`) has no case for it, so a spec declaring `"type": "datetime"` refuses with `unknown field type`. Use `date` (epoch days) or model the instant as `u64` epoch seconds. `decimal128` requires `params.scale` matching declared scale (banker's rounding). Bit-packed (`u4`, `packed_bool`) use one byte per row in the writer. `nullable: true` opts into the per-record null bitmap; distributions report nulls via the bitmap, never inline sentinels.

### Constraints and structural rules → `synth-structural-rules`

The `constraints[]` reject-and-redraw slot and the whole `rules[]` structural surface — slot semantics, declaration order and last-write-wins, the `set_expr` coercion matrix, `null_together` blocks, the shared `expr-lang` row environment and the six eager `PULSE_SYNTH_RULE_*` validation codes — moved to the `synth-structural-rules` skill. Read it before authoring a spec that gates, masks or derives a field rather than merely distributing one.

### Pairwise correlations

`correlations` is a list of `{a, b, rho}` triples, realized by a Gaussian copula (`synth/copula.go`): draw a correlated standard-normal vector `u` via Cholesky, map each `u_i` through the standard normal CDF Φ to `p_i = Φ(u_i) ~ Uniform(0,1)`, then apply each field's OWN quantile function `Q_i(p_i)` (clamped if `normal` declares `min`/`max`; heavy clamping distorts, `|rho|_actual < |rho|_requested`). Supported marginals: `normal` / `uniform` / `lognormal` / `exponential` / `mixture` (`fieldMoments` validates, `quantileFor` builds `Q_i`); any other distribution named in `correlations` refuses with `SERVICE_VALIDATION`. `normal` reduces exactly to `mean + std*u` since `Φ⁻¹(p_i) == u_i`. The copula targets Spearman (rank) correlation exactly and preserves each field's OWN marginal shape — mean, std AND skew; realized Pearson attenuates for a wide-variance non-normal marginal (a known copula effect, not a bug). `|rho| ≥ 1` rejected at validation. Gates: `TestSynth_CorrelationReconstructionWithinTolerance`, `TestSynth_CopulaPreservesLognormalMarginal`.

The `mixture` arm exists for the MODEL draw, not for `correlations` — a `--fit-shape` field is excluded from the value-scale matrix either way (pre-claimed when unmodelled, permanently refused when modelled; its structure rides `residual_correlations` instead).

**Unmeasured matrix entries: ASSUME AND RECORD.** `correlations` is a LIST and the Cholesky needs a MATRIX, so every pair among participants the list does not name has to be filled. `buildCorrelator` fills it with 0 — those two are drawn independent — and warns with the count (`N of M pair(s) … were never supplied and are drawn as independent (rho = 0); an absent pair is unknown, not known to be uncorrelated`). **Refusal was rejected**: an incomplete list is the NORMAL input (a spec correlating a–b and b–c has said nothing about a–c; a profile-derived list is capped by `--correlation-top-k`), and zero is the only completion adding no structure the caller did not ask for. Silence was the defect — on a 10-field matrix from a top-16 list, 29 of 45 pairs were asserted independent with nothing saying so. The **ridge in `cholesky` also speaks**: it remains the safety net for jointly-inconsistent measured entries (a=0.9b, b=0.9c, a=−0.9c has no joint distribution) and returns accumulated diagonal jitter, reported as `ridge-regularized (diagonal jitter …)`. A regularized matrix pulls realized correlations toward zero. Neither warning changes a number.

## Profile mode

Capture via `pulse_profile_create`; synth via `pulse_synth_from_profile`. Profile JSON captures per field:

- Numeric: mean, std, min, max, optional percentiles, null-rate.
- Categorical: top-K values + frequencies, cardinality, null-rate.
- Date: observed range, weekday histogram, null-rate.
- Pairwise: strongest `|rho|` correlations (capped by `--correlation-top-k`).
- Conditional numeric-numeric (`--conditional`, additive/`omitempty`): `conditional.numeric_pairs`, row-aligned `{a, b, rho, n}`, `n` the true co-occurrence count (both non-null, same row) — more accurate than Pairwise's independently-capped reservoirs. `SpecFromProfile` prefers it over `pairwise` when present, falling back unchanged when absent. **Thin-pair warning:** `n < 30` (`synth.MinPairObservations`) still ships, never refused — appends a warning naming the pair (`thinPairWarning`); every pair kind below reuses this mechanism.
- Conditional categorical-categorical (`conditional.categorical_pairs`): one `{a, b, cells, n}` per pair — `cells` a bounded contingency table of `{a_value, b_value, count}`. Captured over at most 10,000 rows via genuine Algorithm R reservoir sampling (`--seed`), not first-N, so a block-ordered source is captured unbiased. Two caps compose: each field's own `--top-k` collapses out-of-top-K values to `"other"` before the joint table is built, then `synth.ContingencyCellCap` (128) bounds the table itself — over-cap cells rank by count descending and the tail folds into one merged `("other","other")` catch-all. → `Spec.CategoricalPairs`.
- Conditional categorical-numeric (`conditional.categorical_numeric_pairs`): one `{a, b, categories, n}` per pair (`a` categorical, `b` numeric) — `categories` is one `{category, mean, std, n}` summary per observed category of `a`, computed online from an exact running sum/sumSq (no reservoir cap). Only the categorical's own `--top-k` collapse applies; no second joint cap, since this emits one numeric summary per already-capped category. → `Spec.CategoricalNumericPairs`.
- Models (`--fit-models`, additive/`omitempty` `models` section) and residual correlations (`--residual-correlations`, additive/`omitempty` `residual_correlations` section, requires `--fit-models`) — the multi-predictor construction. Its own chapter below.
- Shape (`--fit-shape`, additive/`omitempty`, numeric only): `numeric.shape = {means, stds, weights}`, a fitted 2-component Gaussian mixture kept only when it beats plain normal by **BIC** (`-2*logLikelihood + k*log(n)`, k=2 vs k=5 — penalizes the extra params so a near-normal field isn't force-fit) AND the two means are ≥ `0.75*avgStd` apart (guards a BIC-accepted but overlapping local optimum). `fitTwoComponentEM` (`synth/shape.go`) runs a fixed 50 iterations from a deterministic percentile init (25th/75th, no RNG) with a std floor (5% of overall std) against variance collapse. `SpecFromProfile` emits `mixture` (`DistMixture`) instead of `normal` whenever `shape` is present. Fixed at 2 components, no BIC sweep over component count. A shape-fitted field **composes with a model** — see "Shape-fitted targets compose" below.

`synth.SpecFromProfile` reconstructs a Spec: `packed_bool` → `bernoulli` (see below), other numeric → `normal` clamped to observed min/max (or `mixture` when `shape` is present), categorical → `weighted_categorical`, date → `uniform_date`, `set_*` → `set_bernoulli`. Unsupported types → `PULSE_PROFILE_FIELD_UNSUPPORTED`; drop to schema mode for those.

### Boolean marginals

**A `packed_bool` reconstructs as `bernoulli` with `p` = the observed mean, and that arm sits AHEAD of `--fit-shape`.** A boolean is summarised by the numeric accumulator (it is neither date, categorical nor set), and the obvious reading of that summary — `normal(mean, std)` clamped to `[0, 1]` — is wrong on the wire. The field holds one bit, so the writer must reduce a continuous draw to 0 or 1, and no threshold over a clamped normal lands in the right place: `P(false)` is `Φ(−p/σ)`, so a 20% boolean generated at 69%, a 50% one at 84%, an 80% one at 98%. Measured on a 90-boolean survey cohort: mean prevalence error **0.47**, **89 of 90** fields off by more than 0.05, every 11–14% brand attribute generating at ~64%. `bernoulli` needs no threshold — the observed mean *is* `p`.

Three writers reach a boolean and all three are covered. Its own sampler draws `bernoulli` directly. A conditional pair carries `Bernoulli: true` on `CategoricalNumericPairSpec` / `SetNumericPairSpec`, so each cell's captured `Mean` is drawn as a prevalence rather than a location (`Std` unused) — without it the same defect recurs once per cell, worst exactly where the signal is. A model draws through `quantileFor`'s step `Q`, which makes it a **probit**: `P(1 | row) = Φ((μ − Φ⁻¹(1−p)) / σ)`. Coefficients order rows and hold the marginal exactly; they are not probability changes.

Two consequences worth knowing. A boolean observed at `p` of exactly 0 or 1 has zero variance, so a model on it is dropped with a warning rather than producing an infinite `1/std`. And a bernoulli target's **model recovery is not identified** — a 0/1 value does not determine the latent that produced it, so `latentFor` refuses and the fidelity report's `models` entry carries an `error` and no delta instead of a fabricated attenuation. The continuous arm is retained for a hand-authored spec that puts `normal` on a `packed_bool`; it is biased, not correct, and `toBool` rounds at 0.5 there so it is at least not inverted.

### Set (multi-select) field profiling

`set_*` fields (`skills/type-set-u8.md` et al.) are "select all that apply" bitmasks over one shared dictionary — bit `i` ↔ dictionary entry `i`. **Design decision:** each entry profiles as an independent Bernoulli sub-field for *marginal* purposes — `set.options[i].frequency` is `P(bit i set | field non-null)`. Rejected: one categorical value per combination (2^n explosion), or N independent binaries with no shared identity (ignores the shared dictionary, gives the joint capture nothing to reuse).

`profile create` always captures `set = {n, options: [{value, count, frequency}]}` for a `set_*` field (no new flag, additive/`omitempty`). `options` lists EVERY dictionary entry in bit/dictionary order — never sorted by frequency like `categorical.top` — so two profiles of the same schema compare position-for-position. `n` is the non-null row count; `frequency = count / n`.

**Set joint/conditional capture.** `--conditional` extends to pairs involving a `set_*` field by running the per-pair-type capture machinery ONCE PER OPTION — the per-option addressing composes with the existing mechanisms rather than needing a parallel one. Three additive/`omitempty` `ConditionalProfile` sections:

| Section | One entry per | Shape | Reuses (unchanged) |
|---|---|---|---|
| `conditional.set_categorical_pairs` | (set option, categorical field) | `{set, option, categorical, cells, n}`; `cells` a `ContingencyCell` table, `a_value` fixed `"selected"`/`"not_selected"`, `b_value` the categorical value (subject to its own `--top-k` collapse) | `collapseCells` / `ContingencyCellCap` / `otherCategoryLabel` |
| `conditional.set_numeric_pairs` | (set option, numeric field) | `{set, option, numeric, categories, n}`; `categories` is `CategoricalNumericCategoryStat` keyed `"selected"`/`"not_selected"` | online per-category mean/std, exactly as `categorical_numeric_pairs` |
| `conditional.set_set_pairs` | option pair between two DIFFERENT set_* fields (never two options of the same field) | `{set_a, option_a, set_b, option_b, cells, n}`; `cells` a 2x2 `ContingencyCell` table | same contingency-cell mechanics as the row above |

The option axis never needs a top-K collapse — always the fixed two-value domain. Cardinality stays bounded: option count is capped by `FieldType.MaxSetEntries()` (8/16/32/64), and `ContingencyCellCap` (128) bounds every cell table. Thin cells/categories reuse `thinPairWarning` (kinds `"set-categorical"`/`"set-numeric"`/`"set-set"`).

**Set generation.** `SpecFromProfile` reconstructs a `set_*` field as `set_bernoulli` and wires the three pair slots. Each declared pair resamples ONE option's bit conditioned on the paired field's already-drawn value (set-categorical/set-numeric) or another set field's already-drawn option (set-set), in the fixed order set-set → set-categorical → set-numeric, after categorical joint structure and before the numeric correlator. A `set_*` dictionary is pre-populated ONCE from `params.options`' declared order at schema-build time, never lazily during generation — the row value is a `map[string]bool` and Go map iteration order is randomized per process, so pre-registration is what keeps bit assignment (and determinism) independent of it. `AugmentFromProfile` validates both dictionaries name the identical option universe in the identical order (`PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH` otherwise). A profile with only marginal `set` data still generates correctly — joint structure is additive, never required.

### Categorical joint structure (generation)

`Spec.CategoricalPairs` / `Spec.CategoricalNumericPairs` drive two generation-time resample steps, applied after every field's own independent draw and before the numeric correlator: categorical-categorical resamples field B from the captured per-`a_value` conditional distribution over B's cells (falling back to B's pooled marginal for an A value with no captured row); categorical-numeric resamples numeric B from `Normal(mean, std)` keyed on A's drawn value, clamped to B's observed `[min, max]`. Both are no-ops when `Conditional` was absent at capture — a plain profile reproduces independent-marginal generation byte-for-byte.

## Multi-predictor models

`--fit-models` is how **several drivers condition one numeric at once**. The conditional-pair arms above are pick-one: each one overwrites the numeric it targets, so on a wide cohort every categorical claims the same target and all but the first are dropped. One additive model accounts for all of them in a single expression instead.

### Capture (`--fit-models`)

One least-squares linear model per numeric field, that field regressed on the categorical **levels** and `set_*` **options** selection admitted, through `processing/regression`'s streaming OLS engine.

The cohort is read ONCE, but the fits do **not** run per row: the scan's only job is retaining a bounded Algorithm-R row snapshot (`modelResidualCap`, 10,000, on its own RNG stream off `--seed` so `--conditional`'s reservoir is never perturbed). Selection, fitting and residuals all run over that snapshot afterwards. That ordering is forced — both halves of the narrowing below are MEASURED, and a frequency ranking does not exist until rows have been seen. Consequence on the wire: **`n_obs` is the fit's own support within the snapshot** (≤ 10,000), not the cohort's row count (`Profile.RowCount` is that).

One reference level per categorical is dropped and folded into the intercept — a full level set plus an intercept is rank-deficient (the dummy trap) — while set options are all kept, because a multi-select is not a partition.

Document shape, one entry per fitted numeric:

```
{field, intercept, predictors, references, n_obs, r2, residual_std, shrinkage_alpha}
  predictors[] = {kind, field, level, coefficient}    kind ∈ categorical_level | set_option | numeric
  references[] = the level each categorical dropped into the intercept
```

A coefficient is addressed by **(field, level)** — the solver's internal design-column name is `json:"-"` and never reaches the document, because serialising it would freeze that encoding into the file format. `references[]` means a reader holding *k−1* of *k* levels does not have to guess which arm is the zero baseline; a set-only model carries no `references` key. `shrinkage_alpha` is `omitempty` — absent ⇔ the fit was unpenalized OLS.

The row-aligned **fitted residual reservoir** (`FieldModel.Residuals` / `ResidualPresent`) is also `json:"-"` — thousands of numbers per field whose only consumer runs in the same process as the capture. Read them off `synth.Profile.FittedModels()`, never from a re-read document.

A capture WITHOUT the flag emits no `models` key and is byte-identical to a pre-flag document. A design that is unsolvable loses only ITS model and is named in `Profile.Warnings` — never a refusal. A fit that would serialise a non-finite float is dropped with a warning rather than failing the whole document's marshal.

### Predictor selection — automatic, absolute, no knob

Two mechanisms, in this order (`synth/profile_models_select.go`).

**(1) Top-K collapse — structural.** A candidate categorical contributes one column per top-`--top-k` level by frequency plus ONE catch-all column carried as an ordinary level named `"other"` (`otherCategoryLabel`) — the same collapse `Categorical.Top` and `conditional.categorical_pairs` already apply, so a document's collapsed buckets mean one thing throughout. This is what makes the flag work at all rather than a refinement of it: on the motivating 381,324-row cohort the unbounded expansion of 16 categoricals was **2,420 columns** (`brand` alone 1,906 levels, 79% of the width) and all 105 numeric targets were skipped as too wide — `--fit-models` produced NOTHING on real data. Collapsed, the same cohort is under 200 columns before any statistical criterion runs. The catch-all is never the reference level (a baseline meaning "one of the other 1,874 brands" is uninterpretable).

**(2) Variance-explained floor — statistical**, over the collapsed candidate set. A candidate FIELD enters a target's model iff the **adjusted** share of that target's total sum of squares lying between the candidate's groups reaches `synth.minVarianceExplained` (**0.01**, Cohen's small-effect boundary). A package constant tuned in code, deliberately not a `ProfileOptions` field (`TestModelSelection_NoUserFacingOverride`).

**Why not significance.** At 381k rows every candidate is significant, including ones explaining a thousandth of the variance — a p-value gate admits everything and is "all predictors enter" in disguise, silently, since every coefficient it admits is *real*, merely negligible. An absolute floor is n-invariant by construction: the same effect size decides the same way at 1k rows and at 100k (`TestModelSelection_AdmissionInvariantToRowCount`). Adjusted rather than raw η², so a 33-group collapsed field is not admitted for its width alone.

**Scoring is MARGINAL** — one candidate at a time against the raw target, never against another's residual. Two collinear candidates (`age` is a banding of `ageExact`; `region` nested inside `dma`) BOTH clear the floor and both enter. Deliberate: incremental scoring would make the admitted set depend on candidate order. Redundancy is resolved by the **solver** instead — a design refused as rank-deficient is refitted with its weakest-scoring admitted predictor dropped, up to `maxModelRefitRounds` (4) times, so the candidate surviving a nesting is the one explaining more. Detecting the nesting directly is deliberately not attempted: the dependency that actually bites is LINEAR, not functional (a retained set of exact ages covering a whole age band makes that band's indicator the sum of theirs), so a functional-dependency probe misses it while claiming to have handled it. On the motivating cohort the refit is the difference between 23 skipped targets and 2.

Selection also prunes columns degenerate ON THE ROWS THE FIT ADMITS — selection scores pairwise, the fit deletes listwise, and a level with pairwise support but no listwise rows is an identically-zero column the solver refuses. A single-level candidate (`wave` on a one-wave cohort) has zero between-group variance and is dropped by the same arithmetic, not by a special case.

**A model is MAIN EFFECTS only — no interactions.** One coefficient per (field, level), summed. It cannot express "brand X matters only in region Y". That is a real limit, not an oversight: a pairwise interaction between two collapsed 33-column fields is 1,089 columns on its own, which is exactly the width problem the collapse exists to solve, and the marginal scoring rule, the wire format and the recovery refit all assume one term per (field, level).

**A target no candidate clears is NOT skipped** — it gets a **zero-predictor model** (its own mean + its own spread, `r2: 0`), warned as `carries no predictors: …` rather than `skipped: …`. The two mean opposite things: one is a complete model, the other a failed capture. `maxModelColumns` (256) is retained as a backstop against a pathological schema, not as a working limit; the accumulator is O(p²) per row in both memory and arithmetic.

**Measured on the motivating cohort** (with thin-level shrinkage in place): all **105 targets modelled** (before selection landed, zero fitted), **55 carrying predictors**, 113 admitted (field, target) relationships against the predecessor's 86 applied pairs, **0 skips**.

### Thin-level shrinkage

Selection picks predictor *fields*; a field can explain a target convincingly while individual retained levels rest on a handful of rows, and a coefficient from 4 observations is mostly noise that generation then reproduces as a confident invented offset. The top-K collapse bounds RANK, not SUPPORT — the 32nd most common `brand` is still rare, and listwise deletion cuts it again.

A design column whose **listwise** support (rows the fit ADMITS, not cohort frequency) falls below `synth.minLevelObservations` (**50**) switches the whole fit to the engine's ridge (`Penalty: "l2"`), with `alpha = 50 / n_obs`. Its own constant, deliberately not `MinPairObservations` (30) — that answers "does this pair have enough co-occurrences to mean anything", this answers "does this level have enough rows for a free coefficient to describe the level rather than the sample".

**The shrinkage scales with thinness rather than switching on at the threshold**, which is what makes one global alpha sufficient. Read `alpha` as a pseudo-count: the engine adds `n·alpha` to each Gram diagonal, so a level keeps `n_j/(n_j + 50)` of its free coefficient — 3 obs keeps 6%, 30 keeps 38%, 50 keeps half, 500 keeps 91%. Shrinkage is toward the field's **reference level** (that is what a dummy coefficient measures); a thin level collapsing onto the baseline is the conservative reading. A thin level is **never** refused and never dropped. `shrinkage_alpha` rides the wire because a shrunk coefficient and a free one are different kinds of number and nothing else says which you hold; `alpha × n_obs` recovers the pseudo-count.

Three consequences that are invisible in the output:

1. Within a penalized fit, well-supported columns move too, by ≈`50/n_j` — bounded and small, but not zero. No single-alpha ridge can make it zero.
2. It is GATED on a thin column actually being present, so a clean design stays exact OLS (recovering an exactly-linear cohort to 1e-6) at the cost of a bounded discontinuity at the boundary.
3. A penalized Gram is positive-definite, so the rank-deficiency refusal the refit above uses as its redundancy signal does not fire — collinear predictors are jointly shrunk instead of one being dropped.

Warnings reuse the shared thin-support helper (`thinSupportWarning`) and are **doubly bounded**: aggregated by (field, level) across targets — thinness is a property of the level, so per-(target, level) emission restated 89 findings in 477 lines on the motivating cohort — then capped at `maxThinLevelWarnings` (20), thinnest-first, with one counted `+N further thin model level(s)` summary. Lower `--top-k` to fold rare levels into `"other"` instead.

### The composed draw

A numeric carrying a `Spec.Models` entry is NOT drawn from its marginal and then overwritten. It is composed, in one step, at the END of `drawRow`:

```
value = Q( Φ( μ(row) + σ·z ) )      μ = intercept + Σ fired coefficients   (raw data scale,
                                        standardised by the field's own mean/std)
                                    σ = residual_std / std,  z ~ N(0,1)
```

`Φ` and `Q` are `copula.go`'s own `phi` / `quantileFor` — the same functions the correlation stage uses, so a modelled and a correlated field agree on what the field's marginal means. Standardising first is load-bearing: an OLS fit is in data units and `Φ` accepts only a standard-normal argument. **For a plain `normal` target the whole round trip collapses to `prediction + residual_std·z`** (because `Q(p) = mean + std·u` and `Φ⁻¹(Φ(u)) == u`) — the ordinary OLS draw. Routing through `Φ`/`Q` anyway is what lets a `uniform`/`lognormal`/`mixture` target keep its own marginal shape while the linear predictor drives it.

**A term fires only on an exact match, and a term that does not fire contributes ZERO.** That is the dummy-coded reading, not a convenience default: categorical predictors are read against a dropped reference level folded into the intercept, so "nothing fired for this field" *is* "this row sits at the baseline". A level the fit never saw reads as the reference — the model holds no coefficient for it and inventing one would fabricate structure. A **null** predictor likewise contributes zero: the fit applied listwise deletion, so its coefficients say nothing about such rows.

**The top-K catch-all is NOT an unseen level — it has its own firing term.** `predictors[].kind` is a three-value wire vocabulary (`categorical_level`, `set_option`, `numeric`) and the collapsed catch-all is a **`categorical_level` whose `level` is the literal string `"other"`**, the same spelling every other collapsed bucket uses. It fires on a generated row whose value *is* `"other"`, and such rows are real: `--conditional`'s contingency capture collapses out-of-top-K values to that spelling before building its cells, `categoricalPairSampler` resamples straight out of them, and the model stage runs last, so it reads what the emitted row carries. The field's own marginal cannot produce it — `SpecFromProfile` builds a categorical from `Categorical.Top`, which truncates to the top K and **renormalises rather than appending a bucket** — so the pair stage is the whole path. On the motivating cohort 18,198 of 20,000 generated rows carry `brand = "other"`: the dominant arm, not an edge case.

Getting that kind wrong is SILENT and costs the WHOLE model, because `modelSpecFromProfile` refuses a model wholesale on one unusable predictor rather than dropping a term and leaving the survivors read against a vanished baseline. A `default:` arm serialised the catch-all as `numeric` for most of this feature's development: 105 captured models applied as **20**, generation succeeded, and the only signal was a warning line the CLI did not print at the time (since E6-S3 all three synth leaves render a grouped warning summary to stderr — `writeWarningSummary`). Corrected, **55 apply** (the 50 remaining drops are zero-predictor models, dropped on purpose) and `Spec.ResidualCorrelations` grows 190 → 1,485 pairs. `numeric` is still refused at translation and that is correct: it means a genuine scalar predictor, which has no generation-time term. A profile document carrying the old spelling is **not** rehabilitated on read — re-capture is the answer; a permanent read-side tolerance would freeze the bug into the file format.

**Clamping:** exactly one clamp, applied once to the final data-scale value. The model's own `min`/`max` (the target's observed bounds, on `FieldModelSpec`) win — mirroring how a categorical-numeric conditional draw clamps to the pair's carried bounds — with the field's own marginal clamp as the fallback, so the model stage and the copula stage cannot disagree about the admissible range.

**Ordering and determinism.** The model stage runs LAST in `drawRow`, after every pair stage and the correlator, because its predictors are categorical levels and set option bits those stages can still be rewriting. Drawers are sorted into **schema field order** at setup — never `models` array order, never map order — and each consumes exactly one normal per row *unconditionally*, before any branch, so the stream depends only on the number of surviving models. A `residual_std` of 0 still consumes its draw and multiplies it by zero. A spec with no `models` compiles zero drawers and is byte-identical to pre-model output.

### Latent-scale effects — read this before reading a coefficient

**A coefficient shifts the LATENT Gaussian, not the value.** `μ` lives on the standard-normal scale and the drawn value is `Q` of the shifted latent. Only an affine `Q` — i.e. `normal` — carries a coefficient through to the data scale unchanged; there, `+30` moves the value by `+30`.

For a **`mixture`, `lognormal` or any other non-normal `Q` it does not.** The same coefficient moves the value by an amount that depends on where in the distribution the row landed: under a bimodal fit, a latent shift near the trough moves mass between modes and barely moves values inside either, while the identical shift in a tail moves the value a lot.

A **`bernoulli`** `Q` is the sharpest case: it is a step, so the composed draw is a probit and a coefficient is neither a probability change nor a value change — see "Boolean marginals". It is also the one `Q` with no inverse, so these targets are reported with an `error` in the fidelity `models` section rather than a recovered figure.

**Direction and monotonicity hold. Magnitude in data units does not.** A coefficient of 0.556 on a 0–10 NPS field is *not* "0.556 points on the scale". Do not convert one to data units by multiplying, and do not compare two coefficients across targets with different `Q`s as if they were the same currency. `latent_scale` on a fidelity entry is what lets a reader multiply back into the profile document's raw units, and the unit that makes one tolerance meaningful across every field is one target standard deviation.

The value-space alternative — draw from the marginal, then add the prediction — was considered and **rejected**: it destroys the fitted marginal it was meant to protect. It must not be quietly reintroduced.

### Shape-fitted targets compose

`--fit-shape` and conditioning were mutually exclusive for most of this feature's life: a `DistMixture` field was pre-claimed ahead of the model claim, `modelSpecFromProfile` refused a mixture target, and `fieldMoments` / `quantileFor` refused it too. On a real cohort that silently stripped every conditioning relationship from precisely the numerics whose distributions had earned a shape fit.

The exclusivity was a property of the code, not of the construction: `Q(Φ(μ+σ·z))` already admits an arbitrary marginal in `Q` — it is what a `lognormal` target does. A captured mixture now carries **exact moments** (law of total variance) and a **quantile function** (`synth/mixture_quantile.go`); the fitted mixture becomes `Q`, the predictors shift the latent, and the two compose with nothing dropped and no warning. On the motivating cohort 15 of 19 shape-fitted fields now carry a model with predictors, including all four motivating ones (`nps`, `detractor`, `promoter`, `catSpend`).

**The quantile inverse is a fixed-count bisection, deliberately.** A Gaussian mixture CDF has no elementary inverse. `mixtureQuantileBisections` (64) steps run over a bracket of `min(μ_i) − 40·max(σ_i) .. max(μ_i) + 40·max(σ_i)`, where `Φ` has saturated at exactly 0 and 1 so no root escapes. There is **no convergence test and no early exit** — determinism is the contract, and a "loop until `|F(x)−p| < ε`" inverse makes the answer depend on how many steps that particular `p` needed. Bisection over Newton for the same reason plus one more: between two well-separated modes the density underflows and a Newton step explodes. Cost is ~128 `math.Erf` calls per row, paid only by fields that are both shape-fitted and modelled.

An UNMODELLED `--fit-shape` field is untouched by all of this — it still draws its own mixture, still holds its pre-claim, and still excludes a conditional pair or correlation naming it with the `captured shape (--fit-shape)` warning.

### Correlated residuals

**Capture (`--residual-correlations`, requires `--fit-models`).** The correlation structure among the fitted model RESIDUALS. Once a numeric's systematic variation is explained by its predictors, what is still free to move at generation time is the residual — and a raw correlation double-counts every predictor two targets share (two fields both driven by `region` correlate strongly on raw values while their residuals may be independent). Computed straight off `FieldModel.Residuals` in memory after the fits; **no extra read of the cohort**.

Shape: `{fields, pairs, unmeasured}` — `fields` the sorted participant set, `pairs[]` `{a, b, rho, n}` for every MEASURED pair, `unmeasured[]` `{a, b, n, reason}` for every pair that could not be measured. **Full submatrix, never top-K**: the consumer is a joint draw over every participant at once, and a matrix is not a ranked list — a pair left out is a hole the factorization has to fill, not a weak pair. `--include-correlations` / `--correlation-top-k` are untouched and keep their exact meaning (strongest top-K pairs of RAW values); this shares neither flag, and is NOT implied by `--fit-models` because it is quadratic in modelled fields (105 targets = **5,460 pairs = C(105,2)**).

**Measured-zero and unmeasured are structurally different, never conventionally different.** A dense N×N array has one slot per pair and every slot must hold a number, so "unmeasured" has nowhere to live but a sentinel — and the natural sentinel, 0, is a perfectly ordinary measurement. Two lists make the distinction survive a JSON round trip for free, and an unmeasured entry carries **no `rho` key at all**. Two closed reasons: `insufficient_overlap` (fewer than `minResidualPairObservations` = 3 rows carry both residuals; two points always give ±1) and `no_variance` (the rows overlapped but one side's residual is constant — an exactly fitted model; 0/0 is not 0). That floor is deliberately NOT `MinPairObservations` (30): 30 answers "is this stable enough to trust" and its answer is a warning, this answers "is there a measurement at all" and its answer is a gap. They compose — a pair at or above 3 but below 30 is measured AND warned.

**Participants are every model carrying a residual vector, INCLUDING zero-predictor models** — such a target still has a residual, its whole deviation from its own mean, and excluding it would drop real structure for a reason unrelated to that structure. A model with a NIL residual vector (a `Profile` decoded from a document — the reservoir is `json:"-"`) is not a participant at all.

**Generation.** A modelled field whose name appears in `Spec.ResidualCorrelations` reads its own component of ONE correlated standard-normal vector drawn per row (`synth/residual_draw.go`) as the `z` of `Q(Φ(μ + σ·z))`; every other drawer takes its own fresh normal. That is the whole of how a field is **both conditioned and correlated**: the predictors move `μ`, the shared vector correlates the residual, and neither overwrites the other. The Cholesky, the assume-and-record completion policy and the ridge report are the SAME machinery the value-scale matrix uses (`factorCorrelations`) — one construction, two consumers.

Component order is **drawer order (= schema field order)**, never `residual_correlations` array order: a correlated vector makes every participant's value depend on which component it got, so binding it to the document's serialization would make the cohort depend on how the request was typed. The vector consumes exactly one normal per participant, in component order, before the first drawer runs; a non-participant still consumes its own single draw where it always did. Fewer than two surviving participants ⇒ no correlator and no draw at all.

For a `normal` target the latent-to-value map is affine, so the realized VALUE-scale residual correlation is the captured figure exactly; for a `lognormal` or `mixture` `Q` the RANK correlation is exact and Pearson attenuates — the identical property the value-scale copula documents.

**A value-scale `correlations` entry naming a modelled field is refused, permanently.** Realizing it would require overwriting the model's output; rerouting its figure into the residual would apply every predictor the two fields share a second time. `resolveConflicts` says so — *is not applied … capture residual correlations (`profile create --residual-correlations`) to correlate a modelled field* — and deliberately does NOT word it as a `conditional relationship conflict`, because no pair claimed the field.

**The zero-predictor target is the feature's real boundary.** Selection emits a zero-predictor model for a target nothing explained; capture admits it as a residual participant; but `modelSpecFromProfile` DROPS it at translation, because a field nothing explains is better served by its captured conditional pair than by an empty model. Its captured residual pairs therefore reach the spec as nothing. On the motivating cohort that is ~50 of 105 modelled targets, so **1,485 = C(55,2)** pairs reach the spec out of the 5,460 captured. Reversing the drop would retire those fields' conditional pairs in exchange — a worse trade — so the boundary is pinned by test rather than closed.

**Measured, source vs generated (50,000 rows):** `regard ~ meaningfulness` 0.791 against a source 0.835; `people ~ promotion` 0.794 / 0.835; `price ~ promotion` 0.783 / 0.830. Before the catch-all fix these three sat at ~0.00 — annihilation, not attenuation, and every marginal in the fidelity report looked healthy throughout.

### What models retire

**Numeric-target conditional pairs retire PER TARGET, never per document.** For a numeric that lands a model on the Spec, `SpecFromProfile` leaves that field out of `Spec.CategoricalNumericPairs` / `Spec.SetNumericPairs` — one additive model already accounts for every predictor at once, whereas those arms each overwrite the same numeric and all but the first are dropped as conflicts. A model additionally PRE-CLAIMS its target in `resolveConflicts`, so a hand-authored spec declaring both loses the pair and is told which.

A model **dropped in translation** (unknown predictor kind, or a zero-predictor model) leaves its field's pair STANDING and warns `model for numeric field %q not applied: …`. Under a per-document rule such a field lost the pair too and was reconstructed from nothing at all — strictly worse than before the flag existed, and the ordinary outcome once selection can legitimately leave a target unmodelled.

The three **non-numeric-target** arms — `CategoricalPairs`, `SetCategoricalPairs`, `SetSetPairs` — are untouched and still arbitrate pick-one. A linear model targets a numeric and subsumes nothing they describe. `--conditional` + `--fit-models` is a supported combination that keeps both halves. Absent `models`, every arm populates exactly as before.

## Conditional relationship conflicts

Every relationship writes into one shared claim space at generation time: a field name for scalar targets (categorical-pair / categorical-numeric B side, set-numeric's numeric side, correlation participants), or `(field, option)` for a `set_*` field's own bit. **Two options on the SAME set_* field are two DIFFERENT targets, never a conflict** — each writes only its own entry in the row's `map[string]bool`.

`resolveConflicts` (`synth/conflict.go`) runs ONCE per `Spec` at generation setup, never per row, walking the fixed `drawRow` priority order:

```
linear-model pre-claim → shape-fit pre-claim → catPairs → catNumPairs
  → setSetPairs → setCatPairs → setNumPairs → correlations
```

First claim wins; every later relationship naming the same target is dropped and reported, one warning each, instead of resolving to "whichever stage runs last". `Spec.Correlations` is one joint claimant across all its participants (single Cholesky draw) — losing one participant excludes only that field, and `buildCorrelator` rebuilds from whatever survives.

**The model and shape pre-claims compose; they do not compete.** A modelled field is claimed first, since one additive expression already accounts for every predictor and a later overwrite would discard the whole account rather than layer onto it. A `--fit-shape` field is claimed second, since its sampler already drew the row's value. A shape-fitted field carrying a model is claimed by the model, draws through the model, and gets its fitted mixture as `Q` — nothing dropped, no warning. The shape pre-claim still owns every `DistMixture` field no model took.

**Numeric targets no longer generate the bulk of these warnings.** The categorical-numeric and set-numeric arms were the volume producers — every categorical × every numeric, all but one dropped per target — and a modelled target does not populate them at all. What remains is the three non-numeric arms plus hand-authored specs.

Warnings surface two ways: `generate()` (`synth/writer.go`) returns them on `Result.Warnings` for every synth call; for `synth from-profile --fidelity-report`, `SpecFromProfile` independently reruns the pass at spec-composition time and merges its warnings with the profile's capture-time warnings into `FidelityWarnings`. Both calls always agree — same Spec, same priority order.

## Tagged top-up contract

`synth from-profile` (`SynthOptions.SourceCohort` / `--source`) always: (1) appends one `_synthetic` `packed_bool` field — `false` on rows copied from source, `true` on newly generated rows; (2) writes to a **new** output path, distinct from `--source` — source is opened read-only, never mutated; (3) treats `--rows`/`RowCount` as an explicit count of *new* rows, never "top up to N total" (`--rows 500` against 200 source rows → 700-row output). `SourceCohort` empty (`synth from-schema`'s default) is the unmodified plain path — no tag column, output may equal any path. Refusals: `PULSE_SYNTH_SOURCE_REQUIRED`, `PULSE_SYNTH_OUTPUT_REQUIRED`, `PULSE_SYNTH_OUTPUT_COLLISION`, `PULSE_SYNTH_ALREADY_TAGGED`, `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`.

### Fidelity report

`--fidelity-report <path.json>` (only with `SourceCohort` set) writes a `synth.FidelityReport` after generation. Marginals: numeric fields via `TEST_KS` (`split_by: _synthetic`), categorical fields via `TEST_CHISQ` — existing operators, no new stat math. `_synthetic` is on-wire `packed_bool` and both operators need categorical, so the bridge presents it through a `categorical_u8` view schema for that call only. The bridge lives in `pulse.go` (`writeSynthFidelityReport`), not `synth/`, to avoid an import cycle — `synth.BuildFidelityReport` takes an injected `TestRunner`. A failed field test reports `error`, not `result`, without aborting the rest. Sections are `omitempty` throughout: a report for a spec carrying nothing of a kind is byte-identical to the pre-section shape.

**Every section scores ONLY relationships generation ACTUALLY APPLIED.** `spec` carries the full captured cross product; `generate()` applies the subset conflict resolution resolves out of it. Scoring a conflict-dropped pair would compute a delta against a relationship the generator never modelled — the number would look like evidence. `synth.ResolveConflicts` re-runs the identical arbitration against the identical `*Spec`, so a dropped pair simply has no entry, and its own conflict warning explains the absence.

**`pairwise`** — one `{a, b, source_rho, synthetic_rho, delta, n}` per surviving numeric-numeric pair, via the same `pearson` helper capture uses.

**`models`** — for every linear model generation applied, a refit of that same model on the `_synthetic` partition, captured coefficient beside recovered, per predictor. This is the only section that asks whether the captured *structure* survived, rather than whether the rows *look* like the source — and it is the instrument that would have caught both silent structure-loss bugs this feature shipped through, in each of which every marginal stayed healthy while the conditioning was gone. It also settles the compression question: a modelled numeric's mean spread across a predictor's levels is visibly compressed against source (`nps` across `educationLevel`: source 1.85, synthetic 0.73), and two worlds produce that. Recovered ≈ captured means generation is faithful and the compression is the legitimate partial-effect-vs-marginal-contrast gap (widened because predictor fields draw from their own marginals, so source predictor-predictor confounding is absent by design). Recovered *itself* attenuated means a real fault. Nothing else in the report tells them apart.

- **Latent scale, both sides.** Each generated value is inverted back through `latentFor` (the algebraic inverse of the `quantileFor` the draw used, round-trip-gated per distribution) before the refit, and the captured coefficient is divided by the target's own marginal std — precisely the division `buildModelDrawers` performs. Regressing raw values would compare a value-space effect against a latent coefficient and report every `--fit-shape` target as badly recovered while generation was exactly correct. `latent_scale` rides each entry so a reader can multiply back.
- **Only applied models.** `BuildModelFidelity` re-runs `resolveConflicts` AND `buildModelDrawers` against the same `*Spec` `generate()` was given — both pure functions of the Spec — so the section reproduces generation's own surviving set including the drawer compiler's two warn-and-skip refusals. A field with no model has no entry, not an entry with empty values.
- **Unidentified terms are reported as `error`, with no computed-looking delta.** A predictor whose level the output cohort never carries cannot be checked. This is **not** the `"other"` catch-all — all 54 catch-all terms fire, 3,992–9,046 rows each. It is that the design's top-K and `Categorical.Top`'s top-K rank on **different bases**: the design ranks by frequency within the rows the fit listwise-admitted for that target, the marginal ranks over the whole cohort. Both keep 32 and they disagree — 9 of `brand`'s design levels, 4 of `ageExact`'s, 2 of `category`'s (138 of 2,235 entries overall).
- **Zero-predictor models are `marginal: true`, not failures** — a complete model, so no `predictors` array (an empty one would read as failure) and the intercept comparison only, which is still a real check.
- **Flagging band.** `flagged` fires only when the gap exceeds **both** `synth.ModelRecoveryTolerance` (0.10 latent sd) **and** twice the refit's own standard error. A gap smaller than the estimator's noise is not evidence, and a thin level over a few thousand rows has an SE near 0.10 on its own. `n_fired` separates "fired thousands of times and recovered nothing" (a fault) from "never fired" (nothing to recover).
- **The refit uses the same engine through the same adapter** capture used (`processing/regression`, `dummyRecord`) — a second estimator would make every gap ambiguous between "generation lost structure" and "two fitters disagree" — and is deliberately **unpenalized** even when capture shrank its own, since the captured coefficient being compared against is the shrunken one.

**`model_residual_correlations`** — the same question between fields: for every residual correlation generation applied, captured rho beside recovered. Deliberately not folded into `pairwise`, and **the two section names are the load-bearing distinction**: `pairwise` scores `Spec.Correlations` (value-scale, realized by the copula), this scores `Spec.ResidualCorrelations` (residual-scale, realized by the residual correlator). Different numbers over the same two fields, and `resolveConflicts` excludes a modelled field from `Spec.Correlations` precisely so the two arms fire on disjoint field sets. The recovered side uses the **refit's** residuals, not residuals against the captured coefficients — scoring against those would fold every coefficient gap `models` already reports into a correlation and state one finding twice. **Bounded because quadratic:** `compared` / `flagged` / `mean_delta` / `max_delta` cover EVERY pair; `pairs` lists the worst `maxResidualRecoveryPairs` (20) by absolute delta, worst-first, with `omitted` counting the rest — so an unlisted pair recovered at least as well as the last one listed. `flagged` fires on `ResidualRecoveryTolerance` (0.10) alone: a correlation is already unit-free and over thousands of rows its SE is under 0.02. Unmeasured pairs ride a separate list carrying `captured_rho` and no recovered slot (`insufficient_overlap` / `no_variance`, plus a recovery-only `no_model_fit`).

**Reading a flag on a QUANTIZED target — the known limitation.** The latent inversion undoes `Q`, but it cannot undo the **writer's integer rounding**, and for a `packed_bool` or `u4` target that rounding is most of the map: a 0/1 value inverts to exactly two latent values, so most of the latent effect is destroyed at write time and the refit correctly reports what survived. On the motivating cohort **every** modelled target is `packed_bool` or a small integer, and the flags concentrate exactly there — 30 of 55 models flagged, **18 `packed_bool` + 9 `u4`** + 3 wider; `peopleAware ~ age=11` recovers 0.11 against a captured 0.556 on 1,447 firing rows at an SE of 0.021, so the attenuation is well-measured, not noise. The worst residual pairs are the same fields (`aware`×`familiarity` 0.738 → 0.248). This is a **real property of the generated cohort** — the rows genuinely carry less conditioning than the model asked for — not an artefact of the instrument, and the fix is a **discrete draw for discrete targets** (logistic/ordinal), never a looser tolerance. Survey data is mostly binary and small-integer, so this is the common case rather than an edge.

Among strong, well-supported terms (|captured| > 0.3 latent sd, `n_fired` > 500) the median recovered/captured ratio is **0.85**. Report-wide on the motivating cohort: 55 models checked / 30 flagged, 2,235 predictor entries / 138 unidentified; 1,485 residual pairs compared / 147 flagged, mean delta 0.055, max 0.490, 0 unmeasured.

**`models` and the two numeric-target pair sections are disjoint by construction.** A numeric reached by a linear model has **no entry** in `categorical_numeric_pairwise` or `set_numeric_pairwise`, ever — the pick-one sampler those sections score does not run for it. Two independent guards make that structural: `SpecFromProfile` omits a modelled target's captured pair, and `resolveConflicts` pre-claims every modelled field ahead of the categorical-numeric stage. Measured: 55 fields in `models`, 46 in `categorical_numeric_pairwise`, **intersection 0**. `categorical_pairwise` / `set_categorical_pairwise` / `set_set_pairwise` are untouched.

**Cost.** The synthetic partition is decoded ONCE (`decodeSyntheticRows`) and every section rides that slice — a per-entry full decode reintroduces the O(pairs × records) blowup that cache exists to fix. Refits additionally consume at most `modelRecoveryRowCap` (10,000) rows, capture's own snapshot size, so neither side of a comparison is estimated from more rows than the other. On the motivating cohort the two model sections cost +6 KB and ~5s on a 1.65 MB / ~2 min report.

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file. Any sampler change that breaks determinism is a contract break.

Seed splitting uses a 64-bit avalanche; seeds differing by 1 produce uncorrelated streams. `Seed == 0` is stable, not "random". `nullableSampler` always draws the inner value first, then the null mask — the seeded stream is invariant to which rows are null.

**Capture is held to the same bar**: same `(--input, --seed)` MUST produce a byte-identical profile document, or the profile→synth pipeline is only deterministic downstream of a spec that itself drifts. The non-obvious threat there is not the RNG but **map iteration order**. Anywhere capture folds several accumulators into one shared bucket — canonically the top-K collapse folding every out-of-top-K category into `"other"` — the fold MUST walk sorted keys, because float addition is not associative and Go randomizes map iteration. The `(sumSq - mean*sum)/(n-1)` variance form amplifies rather than absorbs the resulting last-bit difference (values ~1e4 give `sumSq` ~1e11 against a variance ~1e8), so a map-order fold surfaces as a ~1e-10 relative drift in the emitted `std`. Signature to recognise: only `"other"` entries move, because only `"other"` has more than one source. Sort, do not switch to compensated summation — the goal is a *reproducible* answer and Kahan summation is still order-dependent in principle. The second threat is **float fusion**: Go may contract `a + b*c` into one FMA, arm64 does and amd64 does not, so every product in a capture formula carries an explicit `float64(...)` conversion (the only construct that forbids contraction) and `synth/moments_internal_test.go` fails if one is dropped. Residual, stated not hidden: `--fit-shape` (`math.Exp`/`math.Log`) and `--fit-models` (`processing/regression`, not yet fusion-free) can still differ in last bits across architectures.

## Library embedding

`pulse.Pulse.Synth` and `pulse.Pulse.Profile` route through the embedded filesystem — `pulse.New(pulse.Options{FS: afero.NewMemMapFs()})` for hermetic tests.

## Gotchas

- Constraints + `monotonic_from`: monotonic ignores RNG, so a rejected row still increments the counter.
- Correlations + non-normal marginal: keep sigma modest if Pearson must land tightly (see the attenuation note above).
- **A model coefficient is a latent-scale quantity.** Never read it as data units unless `Q` is `normal`. See "Latent-scale effects".
- **A quantized (`packed_bool` / `u4`) modelled target attenuates at write time.** Expect recovery flags there; they are real but they are quantization, not a generation fault.
- `--fit-models` does not imply `--residual-correlations`, and neither implies `--conditional`. `--fit-shape` composes with all of them.
- `weighted_categorical` weights normalize at sample time; absent weights default to uniform.
- `uniform_date` is inclusive both ends.
- `regex` is restricted: literal / charclass / fixed-repeat / alternation / bounded `*+{m,n}`. No backreferences.

## See

- Recipes: `pulse_examples_search tags=["synth"]` plus atomic `op-synth-<kind>`.
- `synth-structural-rules` — `rules[]` / `constraints[]`: gating, masking and derived fields.
- `cohort-schema-design` — field types, dictionaries, null bitmap.
- `regression-modeling` — the `REG_OLS` engine the model capture and the recovery refit both drive.
- `error-code-reference` — `PULSE_SYNTH_*` / `PULSE_PROFILE_*` recovery.
