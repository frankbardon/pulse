# pulse profile create

**Audience:** CLI users capturing a statistical profile of an
existing cohort — typically to feed into
[`pulse synth from-profile`](synth-from-profile.md).

`pulse profile create` reads a `.pulse` file and writes a JSON
profile: per-field type, descriptive statistics, top-K categorical
entries, optional pairwise correlations. **The profile retains no
individual rows from the source.**

> **LLM agents using MCP:** see the `pulse_profile` MCP tool.

## Synopsis

```
pulse profile create --input PATH --output PATH
                     [--top-k N] [--include-stats]
                     [--include-correlations] [--correlation-top-k N]
                     [--conditional] [--fit-shape] [--fit-models]
                     [--residual-correlations] [--suggest-rules PATH]
                     [--sample-limit N] [--seed N] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input`                | `-i` | string | (required) | Source `.pulse` cohort |
| `--output`               | `-o` | string | (required) | Output profile JSON path |
| `--top-k`                |      | int    | 32         | Top-K categorical entries to retain per field |
| `--include-stats`        |      | bool   | true       | Include percentile / std stats |
| `--include-correlations` |      | bool   | false      | Capture pairwise numeric correlations |
| `--correlation-top-k`    |      | int    | 16         | Cap on retained correlation pairs |
| `--conditional`          |      | bool   | false      | Capture row-aligned numeric-numeric pair structure (`conditional.numeric_pairs`), categorical-categorical contingency tables (`conditional.categorical_pairs`), categorical-numeric conditional means (`conditional.categorical_numeric_pairs`), and — per option of any `set_*` field — the same three pair kinds against categorical/numeric/other-set fields (`conditional.set_categorical_pairs`, `conditional.set_numeric_pairs`, `conditional.set_set_pairs`) |
| `--fit-shape`            |      | bool   | false      | Fit a 2-component Gaussian mixture per numeric field, kept as `numeric.shape` only when it's a genuine improvement over plain normal (BIC) |
| `--fit-models`           |      | bool   | false      | Fit one linear model per numeric field — the field regressed on the categorical levels and set options automatic predictor selection admits — capturing coefficients, residual scale and fitted residuals (see below) |
| `--residual-correlations` |     | bool   | false      | Capture the full correlation submatrix among `--fit-models`' fitted residuals (`residual_correlations`); every pair, measured or explicitly unmeasured — never a top-K sample and never a fabricated zero (see below). Requires `--fit-models` |
| `--suggest-rules`        |      | string | (off)      | Detect structural rules on the same scan — GATING relationships (`set_null`) and CO-MISSING question blocks (`null_together`) — and write them to this path as a standalone rules file, the bare JSON array `synth from-profile --rules` consumes unmodified. PROPOSED, never applied; the profile document itself gains no section (see below) |
| `--sample-limit`         |      | int    | 0 (unlimited) | Cap rows ingested for the profile (0 disables) |
| `--seed`                 |      | int    | 0          | Deterministic RNG seed for `--conditional`'s categorical-categorical reservoir sampling and `--fit-models`' residual reservoir (see below); each draws from its own stream, so the two flags never perturb each other, and the same `(--input, --seed)` produces byte-identical captured output |
| `--json`                 |      | bool   | false      | Also print the envelope to stdout |

## What the profile captures

| Field type | What is recorded |
|---|---|
| Numeric (`u*`, `f*`, `decimal128`) | Count, min, max, mean, stddev; percentiles if `--include-stats` |
| Integer (`u4`, `u8`, `u16`, `u32`, `u64`) | The same numeric summary, **plus an exact per-level histogram** (`numeric.discrete`) when the column carries at most 64 distinct values — and **reconstructed from that histogram, not as a clamped normal**. See "Small-integer fields" below |
| `packed_bool` | The same numeric summary (a boolean falls to the numeric accumulator), but **reconstructed as `bernoulli`, not as a clamped normal** — see "Boolean fields" below |
| Categorical | Cardinality, plus the top-K most-frequent values with their observed weights. The tail below the cut is **not** retained as an `"other"` bucket — `synth from-profile` renormalises the retained weights, so a 1,900-level field regenerates as `--top-k` levels and the tail's share is redistributed across them. (The `"other"` spelling that *does* appear in `conditional.*` tables and in `--fit-models` designs is a different, per-section collapse.) |
| `date` | Min, max, count |
| `nullable_*` | Null count alongside the above |

## Boolean fields

A `packed_bool` is summarised by the numeric accumulator — it is neither
date, categorical nor set — so its profile entry looks exactly like any
other numeric's: `mean`, `std`, `min: 0`, `max: 1`. The obvious reading of
that entry is `normal(mean, std)` clamped to `[0, 1]`, and it is wrong on
the wire.

The field holds **one bit**. Generation therefore has to reduce a
continuous draw to 0 or 1, and no threshold over a clamped normal lands in
the right place — a clamped normal is exactly 0 only where the underlying
draw fell below zero, so the historical `value != 0` threshold gave
`P(false) = Φ(−p/σ)`:

| Source prevalence | Generated (before) |
|---|---|
| 0.20 | 0.691 |
| 0.50 | 0.842 |
| 0.80 | 0.977 |

On a real 381,324-row survey cohort with 90 `packed_bool` fields, the mean
prevalence error was **0.47** and **89 of 90** fields were off by more than
0.05 — every 11–14% brand-attribute item generated at around 64%, which
turns a rare attribute into the majority answer. The marginals looked
plausible in isolation; nothing compared them to the source.

`synth from-profile` now reconstructs a `packed_bool` as **`bernoulli`**
with `p` = the observed mean. No threshold is involved, so the prevalence
is exact by construction (same cohort after the fix: mean error 0.0032,
max 0.0116, nothing off by more than 0.05). Two details follow from it:

- **The boolean arm outranks `--fit-shape`.** A two-component mixture fits
  a 0/1 column very well by BIC — two near-zero-variance spikes — and
  reproduces the prevalence no better while costing a numeric inversion per
  draw. A boolean's shape is one number, not something to discover.
- **A modelled boolean is a probit.** `--fit-models` on a boolean target
  still works, and its coefficients still order rows, but the composed draw
  is `P(1 | row) = Φ((μ − Φ⁻¹(1−p)) / σ)`. A coefficient is **not** a
  change in probability. See "Reading a coefficient" below; the fidelity
  report reports these targets as unidentified rather than fabricating a
  recovered figure, because a 0/1 value does not determine the latent that
  produced it.

## Small-integer fields

Same defect class as "Boolean fields" above, one type wider, and it is the
one that bites a survey cohort hardest: a `u4`/`u8`/`u16`/`u32`/`u64`
column falls to the numeric accumulator too, so its entry is `mean`, `std`,
`min`, `max` — and the writer stores `floor(v + 0.5)`, so a clamped-normal
reconstruction is quantized on the way to the file. What comes back is a
bell where the source had a U, a J or a spike, and **the mean is roughly
right**, which is why it went unnoticed.

Measured on the same 381,324-row survey cohort, generating 40,000 rows:

| Field | Level | Source share | Generated (before) |
|---|---|---|---|
| `familiarity` (u4, 1–7) | 1 | 0.2526 | **0.1373** |
| `familiarity` | 7 | 0.2198 | 0.1323 |
| `nps` (u4, 0–10) | 10 | 0.3200 | 0.2253 |
| `nps` | 0 | 0.0284 | **0.0020** |
| `useCon` (u4, 1–6) | 6 | 0.3143 | 0.1806 |
| `sow` (u16, 0–15) | 0 | 0.6469 | **0.3804** |

`sow`'s levels 10 and 12–15 were never generated at all. Every one of those
fields' means came back within about 3%.

The capture therefore records the **exact per-level histogram** on
`numeric.discrete` — one `{value, count, frequency}` entry per observed
level, in ascending order — and `synth from-profile` reconstructs the field
as the `discrete` distribution from it. A level's observed count *is* its
weight, so no threshold is involved and the marginal is exact by
construction. Same cohort after the fix: `familiarity` level 1 at 0.2493,
`nps` level 10 at 0.3310 and level 0 at 0.0269, `useCon` level 6 at 0.3154,
`sow` level 0 at 0.6460, every level present.

```json
"nps": {
  "min": 0, "max": 10, "mean": 7.5489, "std": 2.6561,
  "discrete": {
    "levels": [
      {"value": 0, "count": 1883, "frequency": 0.02838},
      {"value": 1, "count": 1176, "frequency": 0.01772}
    ]
  }
}
```

Four details follow from it.

- **Above 64 distinct levels the histogram is ABANDONED, not truncated.**
  A top-64 view of a 3,000-level column would make every share a share of
  an arbitrary subset, so such a column keeps the clamped normal instead.
  The **absence** of the `discrete` key on an integer field is the record
  that this happened — there is no warning and no flag, because presence or
  absence of the key *is* the answer to "which reconstruction will this
  field get". On the motivating cohort 12 of 14 integer columns qualified;
  `respondent` (a u64 ID) and `catSpend` (a u32 currency amount) abandoned.
  The cap is a package constant, not a flag, matching every other tuned
  threshold in `synth`.
- **The boundary is pragmatic, not a fidelity cliff.** A clamped normal's
  per-level *relative* error does not shrink as levels are added — its
  central levels come out around 1.4× their true share at any number of
  levels. What shrinks is the *absolute* error (roughly 1/K). So a column
  just over the cap is wrong by under about 1.5 percentage points per level
  while one just under it can be wrong by 12.
- **The integer arm outranks `--fit-shape`**, for the same reason the
  boolean arm does: a mixture fits a 7-level column well by BIC and
  reproduces the per-level shares no better, while costing a numeric
  inversion per draw. A coded scale's shape is its histogram.
- **A modelled integer target is an ordered probit.** `--fit-models` on one
  still works and its coefficients still order rows, but the staircase `Q`
  means they shift the *latent*, not the value: a coefficient is **not**
  "this many points on the scale". The fidelity report reports these
  targets as unidentified rather than fabricating a recovered figure,
  because a level pins the latent to an interval rather than a point.

The capture is unconditional — there is no flag — because this is the
default reconstruction being wrong rather than an enhancement. It rides the
existing single scan (one bounded map per eligible column) and adds one
`omitempty` key: a document captured before the key existed still
reconstructs exactly as it did, through the clamped normal.

## What the profile does NOT capture

- Individual rows.
- The full categorical dictionary beyond `--top-k`.
- Correlations unless `--include-correlations` or `--conditional` is set.

This is by design — profiles are intended to be safe to share with
parties who shouldn't see the underlying data.

## `--conditional`: row-aligned pair reconstruction

`--include-correlations` computes each numeric field's `Pairwise` entry
from independently-capped per-field reservoirs; once any field carries
nulls those reservoirs can drift out of row alignment, and the reported
observation count is only an approximation of true co-occurrence.
`--conditional` instead keeps a row-aligned joint snapshot (capped at
10,000 rows) and writes a `conditional.numeric_pairs` section: each
entry's `rho` and `n` are computed only from rows where BOTH fields were
simultaneously non-null. `synth.SpecFromProfile` prefers
`conditional.numeric_pairs` over `pairwise` when both are present, and
`synth/copula.go`'s conditional-Gaussian construction (the mechanism
that actually reconstructs the requested correlation, replacing the old
approximate ±5%·std blend) consumes either shape identically. A pair
whose `n` falls below **30** (`synth.MinPairObservations`) is still
shipped — never refused — but appends a warning to `warnings` naming the
pair. The section is entirely absent (not empty) when `--conditional`
is not passed, and profiles captured before this flag existed remain
valid `synth from-profile` input.

## `--conditional`: categorical-categorical contingency capture

`--conditional` also builds a bounded contingency table for every
categorical-categorical field pair, written to
`conditional.categorical_pairs`: each entry is `{a, b, cells, n}`, where
`cells` is a list of `{a_value, b_value, count}` co-occurrences and `n`
is the pair's true co-occurrence count (rows where both fields were
simultaneously non-null), capped at 10,000 captured rows the same way
`conditional.numeric_pairs`' row-aligned snapshot is. A source cohort
over that cap is captured via genuine Algorithm R reservoir sampling
(`--seed`, above) rather than the first 10,000 rows, so a cohort sorted
or otherwise ordered by block (e.g. by region) does not bias the
captured table toward whichever block was read first. Two caps compose
to keep the table bounded regardless of either field's raw cardinality:
each field's own
`--top-k` cap (default 32) first collapses any value outside that
field's top-K into `"other"`, then `synth.ContingencyCellCap` (128)
bounds the resulting joint table itself — a pair of two 50+-category
fields can still produce over a thousand joint combinations after the
per-field collapse, and the joint cap is what keeps that bounded. When
the raw cell count exceeds the cap, the top `ContingencyCellCap - 1`
cells by co-occurrence count are kept and everything else — the ranked
tail plus any pre-existing `("other","other")` cell from the per-field
collapse — folds into one merged `("other","other")` catch-all cell,
never a second competing entry, so the table always saturates to
exactly the cap (never more, and never silently fewer) whenever the
raw cardinality exceeds it. A cell whose count falls below **30**
(`synth.MinPairObservations`) still ships — never dropped — but appends
the same thin-pair warning numeric pairs use, to `warnings`. This story
only captures the contingency table; `synth from-profile` does not yet
sample from it (a later addition).

## `--conditional`: categorical-numeric conditional-mean capture

`--conditional` also captures, for every categorical-numeric field
pair, the numeric field's conditional mean/std broken out per observed
category of the categorical field, written to
`conditional.categorical_numeric_pairs`: each entry is `{a, b,
categories, n}`, where `a` is the categorical field, `b` the numeric
field, `categories` a list of `{category, mean, std, n}` per-category
summaries, and `n` the pair's true co-occurrence count (rows where both
fields were simultaneously non-null) summed across every category.
Unlike `conditional.numeric_pairs` and `conditional.categorical_pairs`,
this section is computed **online** from an exact running sum /
sum-of-squares per category as the cohort streams past — no row-aligned
10,000-row reservoir snapshot and no dependency on `--seed`, because a
conditional mean/std needs one pass of running statistics, not a stored
sample.

Only **one** cap applies here, unlike categorical-categorical's two:
the categorical field's own `--top-k` (default 32) collapses any value
outside its top-K into `"other"` before that category's conditional
mean/std is computed. There is no second, joint-cardinality cap the way
`conditional.categorical_pairs` layers `synth.ContingencyCellCap` on
top of the per-field top-K — this section emits one numeric summary
per already-capped category rather than a joint table over two
categorical axes, so the retained `categories` count is already
bounded by `--top-k` (plus one `"other"` bucket) with nothing further
to saturate.

A category whose `n` falls below **30** (`synth.MinPairObservations`)
still ships — never dropped — but appends the same thin-pair warning
the other two pair kinds use, to `warnings`. The section is entirely
absent (not empty) when `--conditional` is not passed.
`synth.SpecFromProfile` consumes it into `Spec.CategoricalNumericPairs`;
at generation time, numeric field B is resampled from `Normal(mean,
std)` parameterized by categorical field A's already-drawn value,
clamped to B's own observed `[min, max]` exactly as B's unconditional
`normal` reconstruction clamps.

## `--conditional`: set_* pair capture (set-categorical / set-numeric / set-set)

Every `set_*` field is profiled marginally as N independent Bernoulli
sub-fields, one per dictionary option (bit position) — see
`skills/synthetic-data.md` ("Set (multi-select) field profiling").
`--conditional` extends the pair-reconstruction sections above to any
pair involving a `set_*` field by running the SAME machinery once PER
OPTION rather than once per field:

- `conditional.set_categorical_pairs`: one `{set, option, categorical,
  cells, n}` entry per (option, categorical field) combination — `cells`
  is the same `{a_value, b_value, count}` shape as
  `conditional.categorical_pairs`, with `a_value` always
  `"selected"`/`"not_selected"` and `b_value` the categorical field's
  value (subject to that field's own `--top-k` collapse). The same
  `ContingencyCellCap` (128) bounds each option's cell table.
- `conditional.set_numeric_pairs`: one `{set, option, numeric,
  categories, n}` entry per (option, numeric field) combination —
  `categories` holds the numeric field's conditional mean/std for
  `"selected"` and `"not_selected"` rows, the same shape
  `conditional.categorical_numeric_pairs` uses per category.
- `conditional.set_set_pairs`: one `{set_a, option_a, set_b, option_b,
  cells, n}` entry per option pair between two DIFFERENT `set_*`
  fields — a bounded 2x2 contingency table. Never captured between two
  options of the same field.

Cardinality stays bounded without a new, separate cap: a set field's
option count is capped by its own type (8/16/32/64 for
`set_u8`/`u16`/`u32`/`u64`), and `ContingencyCellCap` still bounds every
individual cell table exactly as it does for two plain categorical
fields. Thin combinations (`n` below `synth.MinPairObservations`, 30)
still ship — never dropped — appending the same warning shape every
other pair kind uses. `synth from-profile` does not yet sample from any
of these three sections — a later addition.

## `--fit-shape`: mixture-of-normals shape fitting for numeric fields

By default every numeric field is summarized as a single normal
(`mean`, `std`, `min`, `max`) regardless of its actual shape — a
strongly bimodal or skewed source distribution collapses to that one
bell curve. `--fit-shape` attempts, for every numeric field, a
2-component Gaussian mixture fit (reusing the `mixture` distribution
registered by `synth.DistMixture`) and keeps it — written as
`numeric.shape: {means, stds, weights}` — only when it is a genuine
improvement over the plain normal, decided by comparing **BIC**
(Bayesian Information Criterion) between the two models: a 2-component
mixture can always fit the training sample at least as well as a single
normal in raw likelihood terms, so BIC's extra-parameter penalty
(`3*log(n)` more free parameters) is what keeps a field that is already
close to normal from being force-fit into a needlessly complex shape. A
second guard rejects a fit whose two components are not meaningfully
separated (at least 0.75 combined-std apart) even when BIC alone would
accept it, since GMM likelihood surfaces have spurious local optima
near the single-component case.

`numeric.shape` is additive and `omitempty`: absent from every profile
document captured without `--fit-shape`, and absent for any individual
numeric field whose fit was not kept — `synth.SpecFromProfile` falls
back to the ordinary normal reconstruction for that field either way.
No new flag is needed at `synth from-profile` time — whether a field
regenerates from `normal` or `mixture` is decided entirely by whether
the profile document carries a captured shape for it.

Limitations (see `synth/shape.go` for the full algorithm and its
documented trade-offs): fixed at exactly 2 components (no component-
count search); a single deterministic EM run per field, not
multi-start, so a genuinely trimodal source fits a 2-component
approximation rather than an optimal one. A captured shape **does** compose with `--fit-models`: a
shape-fitted numeric that lands a linear model draws through that
model with its fitted mixture as the marginal, keeping both halves.
See "`--fit-shape` and `--fit-models` compose" below.

A field's captured shape and `--conditional`'s categorical-numeric
conditional structure (or a numeric-numeric correlation) for that same
field still do not compose, but the conflict is not resolved silently:
at `synth from-profile` generation time a `--fit-shape`-captured field
that carries no model is pre-claimed under
`"captured shape (--fit-shape)"` before any conditional-pairing or
correlation stage runs (`synth.resolveConflicts`,
`synth/conflict.go`), so the shape fit wins that field — but the
excluded relationship (the dropped categorical-numeric pair, or that
field's exclusion from a correlation whose other participants still
correlate) is reported as one warning naming both the field and which
relationship lost. That warning lands
on `synth.Result.Warnings` (`data.warnings` under `--json`) for every
`synth from-schema` / `synth from-profile` run, and additionally folds
into `--fidelity-report`'s own `warnings` array for a `synth
from-profile` run, alongside `profile create`'s own capture-time
thin-pair warnings. The same priority-ordered claim mechanism resolves
every other conditional-relationship collision too (e.g. two
categorical-numeric pairs both naming the same numeric field) — a
shape fit is simply a high-priority claimant, not a special case. The
one claimant above it is a linear model, which does not displace the
shape fit but composes with it.

## `--fit-models`: per-numeric linear models

By default a numeric field's relationship to the cohort's categorical
structure is captured one pair at a time — `--conditional` records a
separate conditional mean per category, per categorical field — and
generation applies those pairs by overwriting the drawn value once per
claimed pair. `--fit-models` captures the relationship as a single
**additive linear model** instead: for each numeric field, one
least-squares fit of that field on the cohort's categorical **levels**
and `set_*` **options**, so `region`, `tier` and a multi-select all
contribute to the same prediction rather than competing to be the last
writer.

The cohort is read **once**. What the scan itself does is retain a
bounded, uniformly sampled row snapshot; the predictors are then
selected, the models fitted and the residuals computed over that
snapshot after the scan finishes. That ordering is not an optimisation —
both halves of predictor selection are *measured*, and neither a level
frequency ranking nor a variance share exists before any row has been
read. One visible consequence: `n_obs` reports the fit's own support
within the snapshot, so on a cohort larger than the snapshot it is
smaller than `row_count`.

### Which predictors enter

Selection is fully automatic. There is no way to declare, override or
weight a predictor, and no threshold flag.

**Levels are collapsed by `--top-k`.** A categorical contributes one
column per top-`--top-k` level by frequency, plus a single catch-all
column labelled `"other"` standing for everything below the cut — the
same collapse `--top-k` already applies to captured marginals and
contingency tables. This is what keeps the flag usable at all: a
survey cohort with a 1,900-level `brand` column expands to thousands of
design columns unbounded, past any sane width limit, and every numeric
field would be skipped. It is also more honest — 1,900 levels cannot
support 1,900 free coefficients, however many rows there are.

**Fields are admitted by variance explained.** A candidate categorical
or `set_*` field enters a numeric field's model only if it accounts for
at least **1%** of that field's variance, adjusted for the degrees of
freedom its groups spend. The criterion is deliberately *not*
statistical significance: on a large cohort every candidate is
significant, including ones explaining a thousandth of the variance, so
a significance test would admit everything while looking selective. An
absolute floor decides the same way regardless of how many rows the
cohort has.

Two consequences worth knowing. A field like `wave` on a single-wave
cohort explains nothing by construction and is dropped by the same
arithmetic that drops a real but negligible predictor — no special case.
And two overlapping candidates (`age` is a banding of `ageExact`;
`region` is nested inside `dma`) are scored independently, so both
enter. Sorting out the redundancy is the fit's job, not the selection
rule's: when the solver refuses the design as rank-deficient, the model
is refitted with its weakest-scoring predictor dropped — up to four
times — so the candidate that survives a nesting is the one that
explains more of the target. That narrowing applies to *unpenalized*
designs only: a ridge penalty makes a collinear design solvable, so a
model that carries a thin level (below) keeps both overlapping
candidates and shrinks them together instead.

Each admitted categorical contributes one column per retained level
except one **reference** level, whose effect is absorbed into the
model's intercept; every other coefficient is read relative to it. A
full level set alongside an intercept is rank-deficient by construction,
which is why one has to go. The `"other"` catch-all is never chosen as
the reference — a baseline meaning "one of the remaining 1,874 brands"
is not something a reader can interpret. `set_*` options are **not**
subject to reference-dropping at all: a multi-select is not a partition,
so each option is an independent indicator and all of them are kept.

**Thin levels are shrunk, not dropped.** Choosing which *fields* enter a
model is one question; how much to trust the individual *levels* of a
field that already entered is another. Retaining a field's top 32 levels
bounds how many there are, not how many rows each has — the 32nd most
common `brand` is still rare, and rows are dropped again for every model
whose target or predictors are null on them. A coefficient fitted from
four rows is mostly sampling noise, and generation would reproduce that
noise as a confident offset for every row it draws at that level.

So a model with any design column supported by fewer than **50** of the
rows it admits is fitted with a ridge penalty instead of plain least
squares. The penalty is worth 50 observations at zero, which means a
level keeps `n / (n + 50)` of the coefficient it would otherwise get:
three rows keep 6% of it, thirty keep 38%, fifty keep half, five hundred
keep 91%. The shrinkage therefore *scales* with how thin the level is
rather than switching on at the threshold, and it pulls the level toward
the model's reference level — the honest reading of "too few rows to say
this level differs". A model fitted this way records the penalty as
`shrinkage_alpha`; one whose levels all clear 50 records nothing and is
plain least squares.

Each shrunk level is named once in the profile's `warnings`, with its
support and the model it was thinnest in. Thinness is a property of the
level rather than of the target, so the lines are aggregated across
models and then capped at twenty, thinnest first, with a counted summary
for the rest — lowering `--top-k` folds rare levels into `"other"` and
is the actual fix. A thin level is never refused and never silently
dropped.

Alongside the coefficients the capture keeps each model's residual scale
and its **fitted residuals** (observed minus predicted) over a bounded,
row-aligned sample of the scan. Residuals are what remains once a
field's systematic variation is explained, and they are the honest input
to any later measurement of how two numeric fields co-move.

A numeric field that **no** candidate explains is not skipped. It gets a
model with zero predictors — its own mean as the intercept, its own
spread as the residual scale, `r2` of 0 — and a warning saying it
*carries no predictors*. That is a finding about the cohort, and it is
deliberately worded differently from a skip.

Skipping is never a refusal either. A field whose design stays
unresolvable after those refits loses **its own** model, is named in the
profile's `warnings` as *skipped* — with the reason and the predictors
it was carrying — and costs the rest of the document nothing. It also
keeps its `--conditional` pair, so it is reconstructed from measured
structure rather than from nothing.

### What selection admits in practice

Selection is calibrated for cohorts with enough rows to support a
per-level coefficient — a survey panel or an operational extract, not a
few hundred responses. A few numbers from a real 381,324-row, 122-field
survey cohort give the shape of a typical result — the same cohort every
measured figure on this page and on
[`synth from-profile`](synth-from-profile.md) comes from:

| | |
|---|---|
| Numeric fields, all of which got a model | 105 |
| …of those, carrying at least one predictor | 55 |
| Distinct (predictor field → target) relationships admitted | 113 |
| Targets skipped | 0 |
| Capture time, `--include-stats --conditional --fit-shape --fit-models` | ~37s |

**Roughly half the models carrying no predictor is a normal result, not
a breakage.** Those 50 fields are ones where no categorical or `set_*`
in the cohort explained 1% of their variance — a finding about the
cohort. They are reported as *carrying no predictors*, never as
*skipped*, and the two words mean different things throughout the
document: a zero-predictor model is complete and usable (it is the
field's own mean and spread), while a skip means the field lost its
model and fell back to whatever `--conditional` measured for it.

On a **small** cohort expect the opposite pressure: with a few hundred
rows most levels fall under the 50-observation mark, so most models are
ridge-penalized, `shrinkage_alpha` appears widely, and the coefficients
are pulled hard toward the reference level. That is the flag behaving
correctly — it is refusing to state a level difference that a handful of
rows cannot support — but it does mean the capture is not telling you
much. Read the warnings before trusting a small-sample capture.

### What lands in the document

The fits are written as an additive `models` section — one entry per
fitted numeric field:

```json
"models": [
  {
    "field": "spend",
    "intercept": 41.87,
    "predictors": [
      {"kind": "categorical_level", "field": "region", "level": "west", "coefficient": 11.02},
      {"kind": "set_option",        "field": "features", "level": "premium", "coefficient": 24.91}
    ],
    "references": [{"field": "region", "level": "east"}],
    "n_obs": 1160,
    "r2": 0.94,
    "residual_std": 1.41,
    "shrinkage_alpha": 0.0431
  }
]
```

`shrinkage_alpha` appears only when the fit was penalized (see *thin
levels* above); its absence means plain least squares. A shrunk
coefficient and a free one are not the same kind of number, and this key
is the only thing on the wire that says which one you are holding —
`shrinkage_alpha × n_obs` gives back the 50-observation pseudo-count.

A coefficient is addressed by **`(field, level)`** — never by the
solver's internal design-column name, which is an implementation detail
and is deliberately absent, so the encoding stays free to change without
invalidating documents already on disk. `references` names the level
each categorical DROPPED into the intercept, so a reader holding *k−1*
of *k* levels does not have to guess which one is the zero baseline (a
model with only `set_*` predictors carries no `references` key — a
multi-select is not a partition and has no baseline arm).

`kind` is a closed three-value vocabulary — `categorical_level`,
`set_option`, `numeric` — and it is on the wire because it is not
derivable from `(field, level)`: a categorical level is one arm of a
partition, read against the dropped reference the same field names in
`references`, while a set option is an independent indicator with no
reference at all. The `--top-k` catch-all column is **a
`categorical_level` whose `level` is the literal string `"other"`**, not
a kind of its own — the same spelling the collapsed buckets in
`categorical_pairs` and the captured marginals use, so a consumer
applies it exactly as it applies `region=west`. `numeric` is reserved
for a genuine scalar predictor and `synth from-profile` refuses a model
carrying one, since there is no generation-time term for it. That
refusal is wholesale — one unusable predictor drops the entire model,
because dropping a single term would leave the surviving coefficients
read against a baseline that no longer exists — so a document written by
a build that mislabelled its catch-alls loses those models outright and
must be re-captured rather than repaired.

The **fitted residuals are not written**. They are a bounded row-aligned
sample — thousands of numbers per numeric field — and their consumer
runs in the same process as the capture, so they stay reachable through
`synth.Profile.FittedModels()` and are absent from a document read back
off disk.

A capture **without** `--fit-models` emits no `models` key at all and is
byte-for-byte the document it has always been; a capture **with** it
moves nothing but that one key. `--conditional`,
`--include-correlations`, `--correlation-top-k` and `--fit-shape` all
keep their exact meaning, flag present or absent.

### Reading a coefficient — it is not "points on the scale"

Every coefficient is read **against the model's reference level** for
that field: `region=west 11.02` beside `references: [{region, east}]`
means *west relative to east*, not *west relative to zero*. A `set_*`
option has no reference — it is read against not selecting the option.

The subtler point is the **units**. Generation does not add a
coefficient to a drawn value; it adds it to the field's *latent* — an
internal standard-normal position — and the field's own captured
marginal then turns that position into a value:

```
value = Q( Φ( μ(row) + σ·z ) )
```

That indirection is invisible for the simple case and unavoidable for
the rest:

- **A continuous field reconstructed as a plain normal** has an affine
  marginal, so the round trip cancels: a coefficient of `+30` on such a
  field really does move the drawn value by `+30` units, and reading it
  as "thirty dollars of spend" is correct.
- **A field carrying a `--fit-shape` mixture** does not. The same
  coefficient shifts mass between the fitted modes rather than
  translating the distribution, so how far a row moves depends on where
  in the distribution it landed. Direction and ordering hold; magnitude
  in data units does not.
- **A field whose on-wire type is coarse** does not either, and this is
  the case most survey cohorts are made of. A `u4` field storing a 0–10
  scale is written to the nearest whole number; a `packed_bool` field is
  written as 0 or 1 and nothing else. A latent shift of a quarter of a
  standard deviation moves most rows by nothing at all and a few rows by
  a whole scale point — it changes the *proportion* of rows that cross
  each boundary, which is a real effect but not a per-row offset. On a
  boolean target almost the whole latent effect disappears at write
  time; see [`synth from-profile`](synth-from-profile.md)'s guidance on
  reading a flagged model, where the same property shows up as a
  measured recovery gap.

So: read a coefficient's **sign and its size relative to the model's
other coefficients** freely. Read it as an amount of the field only when
the field is continuous, unrounded and reconstructed as a plain normal.

### What `models` replaces at generation time

`synth from-profile` reads the section instead of `--conditional`'s
**numeric-target** pairs, field by field: a numeric field that lands a
model has its entries in `conditional.categorical_numeric_pairs` and
`conditional.set_numeric_pairs` dropped. They exist to
resample a numeric field from one paired field's conditional moments, so
on a cohort with several categorical and `set_*` fields every one of
them claims the same numeric target and all but the first are dropped as
conflicts — one warning each, thousands on a wide cohort. A model
accounts for all of those predictors at once, so the pairs have nothing
left to add.

The three **non**-numeric-target arms —
`conditional.categorical_pairs`, `conditional.set_categorical_pairs` and
`conditional.set_set_pairs` — are unaffected. A model predicts a numeric
FROM categorical and set structure; it says nothing about how that
structure co-varies with itself, so passing `--conditional` and
`--fit-models` together is supported and keeps both halves.

A model that cannot be applied to the reconstructed spec — it names a
field the profile does not carry, or it is the zero-predictor model
above — is dropped with a
`model for numeric field "x" not applied: …` warning, and that field
**keeps** its captured conditional pair. Retirement is per field, not
per document: a field that gains no model must not also lose the
structure `--conditional` measured for it.

Without the section — every document written before this flag existed
included — nothing changes: all five arms populate exactly as before.

### `--fit-shape` and `--fit-models` compose

A `--fit-shape` target used to be a drop reason: its mixture had no
quantile function a linear predictor could ride, so `synth
from-profile` kept the fitted shape and discarded the model. On a real
cohort that removed every measured conditioning relationship from
exactly the numerics whose distributions had been interesting enough
to earn a shape fit.

They now compose. A modelled numeric is drawn as
`value = Q(Φ(μ(row) + σ·z))`, and `Q` is the field's own marginal — so
a fitted mixture simply *becomes* `Q` while the predictors shift the
latent, the same way a hand-authored `lognormal` target already keeps
its skew under a linear predictor. Nothing is dropped and nothing is
warned. A shape-fitted field that lands **no** model is untouched: it
draws its own mixture and still holds the pre-claim described above.

Two consequences worth knowing before reading such a model's numbers:

- **Coefficients are on the latent scale.** For a plain `normal`
  target the mapping is affine and a coefficient of `+30` moves the
  drawn value by `+30`. For a fitted mixture it does **not** — the
  same coefficient moves values by an amount that depends on where in
  the distribution the row landed, because it shifts mass between the
  modes rather than translating them. Direction and monotonicity hold;
  magnitude in data units does not. Do not read such a coefficient as
  "this many units of the field".
- **The marginal is preserved exactly.** The alternative — drawing
  from the mixture and then adding the linear prediction — would make
  coefficients read in data units, at the cost of smearing the fitted
  modes across the level offsets. That destroys the shape the flag
  exists to capture, so it is not what happens.

### How a modelled numeric is drawn

`synth from-profile` composes the value in one step rather than drawing
a marginal and overwriting it:

```
value = Q( Φ( μ(row) + σ·z ) )
```

`μ(row)` is the model's prediction for that row — the intercept plus the
coefficient of every predictor that fires — standardised by the field's
own captured mean and standard deviation; `σ` is `residual_std` on that
same scale, and `z` is a fresh standard-normal draw. `Φ` and `Q` are the
same normal CDF and per-field quantile function the correlation
reconstruction uses, so a modelled field and a correlated field agree on
what the field's own shape is. For a numeric reconstructed as `normal` —
which is every numeric a profile carries a model for — this reduces
**exactly** to `prediction + residual_std × z`.

A coefficient fires only on an exact match. A categorical level the fit
never saw, and a null predictor, both contribute **nothing** — they read
as the reference level, which is what the dropped-baseline encoding
means. The drawn value is clamped once to the field's observed
`[min, max]`.

Generation stays deterministic: the same `(profile, --rows, --seed)`
still produces a byte-identical cohort. Each model consumes exactly one
residual draw per row, in schema field order, whether or not any
predictor fired.

A `correlations` entry naming a modelled field is **not applied**, and
permanently so: a `correlations` figure is a correlation between two
fields' *values*, and the only way to realize one is to draw both values
from a shared copula — which would delete the model's whole account of
the field. Rerouting it into the residual instead would apply every
predictor the two fields share a second time. The run reports the
refusal and names the surface that *does* correlate a modelled field:
`--residual-correlations`, below.

## `--residual-correlations`: the residual submatrix

`--include-correlations` and `--conditional` both measure the
correlation between two fields' **raw values**, which is the right
quantity when each field is reconstructed from its own marginal and
nothing else. It stops being the right quantity the moment a field
carries a model: once a numeric's systematic variation is explained by
its predictors, the part still free to move at generation time is the
**residual**. Two fields both driven by `region` correlate strongly on
raw values while their residuals may be independent — imposing the raw
figure on the residual draw would apply `region`'s effect twice.

`--residual-correlations` (which requires `--fit-models`) measures the
correlation between every pair of fitted residuals and writes it as the
additive `residual_correlations` section. It reads no extra bytes from
the cohort: the residual reservoir is already in memory when the fits
finish.

```json
{
  "residual_correlations": {
    "fields": ["alpha", "beta", "gamma"],
    "pairs": [
      { "a": "alpha", "b": "beta",  "rho": 0.81, "n": 600 },
      { "a": "alpha", "b": "gamma", "rho": 0.02, "n": 600 }
    ],
    "unmeasured": [
      { "a": "beta", "b": "gamma", "n": 1, "reason": "insufficient_overlap" }
    ]
  }
}
```

### The full submatrix, not a top-K sample

`--correlation-top-k` retains the strongest *K* pairs because its
consumer applies one pair at a time. This section's eventual consumer is
a joint draw over every participant at once — a Cholesky factor of the
whole matrix — and a matrix is not a ranked list. A pair left out of it
is not "weak", it is a hole the factorization has to fill with
something. So every pair among participants is visited.

`--include-correlations` and `--correlation-top-k` are untouched and
keep their exact meaning. This is a different measurement (residuals,
not raw values) with a different shape (a submatrix, not a ranked list),
and sharing either flag would silently change what those two already
mean. It is likewise **not** implied by `--fit-models`: the section is
quadratic in modelled fields (105 targets is 5,460 pairs), so switching
it on for free would grow every such document without notice.

### Measured zero is not the same as unmeasured

`fields` + `pairs` + `unmeasured` is exhaustive and non-overlapping:
every unordered pair among `fields` appears in exactly one of the two
lists.

A dense *N*×*N* array would have one slot per pair and every slot would
have to hold a number, so "unmeasured" would have nowhere to live except
a sentinel — and the sentinel that reads most naturally is `0`, which is
also a perfectly ordinary measurement. Two lists make the two states
*structurally* different rather than conventionally different, and a
JSON round trip preserves a structural difference for free. **An
unmeasured entry carries no `rho` key at all** — there is no slot for a
zero to be written into.

Two reasons, a closed vocabulary:

| `reason` | Means |
|---|---|
| `insufficient_overlap` | Fewer than three rows carry both residuals. Two points always produce a Pearson coefficient of exactly ±1 — a line through two points is a perfect fit — so a figure from a handful of rows is an artifact of the arithmetic, not a weak measurement |
| `no_variance` | The rows overlapped but one side's residual is constant across them, so the correlation is 0/0. An exactly fitted model on the overlap is the ordinary cause |

That floor of three is deliberately **not** the 30-observation
thin-pair threshold. Thirty answers "is this stable enough to trust",
and its answer is a warning — a shaky measurement of something real
beats no measurement. Three answers "is there a measurement here at
all", and below it there is not. The two compose: a pair at or above
three but below thirty is measured *and* warned.

Warnings are bounded. One summary line names how many pairs are
unmeasured (the per-pair reasons are already in the document, so the
warning's job is to make sure the gap is noticed, not to re-list it);
thin measured pairs are named individually up to twenty, thinnest-first,
then collapse into one counted summary.

### Who participates

Every model carrying a residual vector, **including a model with zero
predictors**. Selection can legitimately leave a target with nothing
admitted, and such a target still has a residual: its whole deviation
from its own mean. Excluding it would drop real structure for a reason
unrelated to that structure, and would do it asymmetrically, since the
same field paired against a predictor-carrying target is exactly as
measurable. Its residual correlation simply coincides with its raw
correlation, which is the correct answer for a field nothing predicts.

A model whose residual vector is *absent* — a `Profile` decoded from a
document, where the reservoir is deliberately not serialised — is not a
participant. Treating it as one would produce a submatrix in which every
pair is unmeasured, which describes the reader's situation rather than
the cohort's.

### How generation uses it

`synth from-profile` translates the **measured** pairs onto the spec and
draws every participating model's residual from one shared correlated
standard-normal vector per row — the same Cholesky, the same completion
policy and the same ridge report the value-scale `correlations` matrix
uses, with a different consumer rather than a second construction. The
predictors move the prediction, the shared vector correlates the
residual, and neither overwrites the other, so a field is finally able
to be both conditioned and correlated.

The `unmeasured` list is translated into **nothing at all**. An absent
pair is completed as independent by the generator, which counts and
names the assumption (see below); writing it out as a zero here would
present a gap as a measurement and would silence that count as well.

A pair whose endpoint lost its model in translation is dropped — the
model drop was already reported by field, with its reason — and the run
names the consequence once rather than per pair.

The commonest such endpoint is a **zero-predictor** target. Selection
emits a model for a target nothing explained, and it participates in the
capture above — it still has a residual, its whole deviation from its own
mean — but translation drops it, because a field nothing explains is
better served by its captured conditional pair than by an empty model.
Its residual pairs therefore have nothing to attach to. On a wide cohort
roughly half the modelled targets carry no predictor, so
`residual_correlations` reaches the predictor-carrying half.

### What the generator does with an incomplete matrix

Both correlation surfaces — `correlations` on the value scale and
`residual_correlations` on the residual scale — now say out loud what
they had to invent, in the same words and through the same code. A
correlation surface is a list and the Cholesky needs a matrix, so
every pair the list does not name is filled with zero — those fields are
drawn independent — and the run emits a warning counting how many pairs
were **completed by assumption**. Refusing was considered and rejected:
an incomplete list is the normal input (a spec correlating *a*–*b* and
*b*–*c* has said nothing about *a*–*c*, and a profile-derived list is
capped by `--correlation-top-k` by construction), and zero is the only
completion that adds no structure the caller did not ask for. What was
not acceptable is doing it silently.

The ridge fallback likewise stays — it is the safety net for a matrix
whose *supplied* entries are not jointly realizable — but now reports
the diagonal jitter it had to add. A regularized matrix means the
realized correlations will be pulled toward zero, which a caller
previously could only infer from the output.

## `--suggest-rules`: proposing structural rules from the data

The `rules[]` layer (`docs/src/cli/synth-from-schema.md`) can state the
facts no statistical summary can — a question block not ASKED of a
respondent who failed a screener, a flag that is a band of another
column — but its value is bounded entirely by rule COVERAGE, and rules
get written from memory. On the motivating 122-field survey the analyst
supplied exactly one; a second and larger one was found by accident
while verifying something else. An unstated rule is invisible in the
output because **every marginal inside it is individually correct**.

`--suggest-rules <path>` runs three detectors on the scan `profile create`
already makes — **no additional cohort read** — and writes their
candidates to one file:

- **Gating.** For every low-cardinality field (`categorical_*`,
  `packed_bool`, `u4`) and every other field it measures
  `P(target null | gate = level)`, and proposes a `set_null` rule for
  each field whose levels split a target's null rate into ~1 and ~0.
- **Co-missing.** It proposes a `null_together` rule for each set of
  fields null on EXACTLY the same rows, and reports separately the
  near misses and the always-null columns.
- **Exact dependency.** It proposes a `set_expr` rule for each field that
  IS a function of one other field on every row where both are present —
  `promoter` is `nps >= 9` — and reports separately the almost-determined
  pairs and the constant columns.

The first two read NULL STATE only; the third reads VALUES.

```
pulse profile create -i cohort.pulse -o profile.json --suggest-rules candidates.json
pulse synth from-profile -p profile.json --source cohort.pulse -o out.pulse \
      --rows 20000 --rules candidates.json
```

### Proposed, never applied

A detected pattern can be a coincidence of the sample, and a rule applied
without review is a silent structural claim about the data. Nothing is
auto-applied and the profile DOCUMENT is untouched: absent the flag it is
byte-identical, and with the flag the only key that moves is `warnings`.
The candidates are never a profile section — an unreviewed structural
claim must not ride inside the document that drives generation.

### Detection finds the STATISTICAL gate, not the SEMANTIC cause

This is measured, not hypothetical. On the motivating cohort detection
proposes BOTH `round(aware) == 0` and `round(familiarity) == 1`, with
identical evidence to sixteen digits — the two select the same 96,326
rows with zero exceptions either way, so nothing in the data can
separate them, and the analyst's own rule is the second. Each candidate
says so in its `_evidence.note` and invites correction rather than
asserting cause.

### The `_evidence` block

Each candidate carries its own supporting measurement on `_evidence` —
the ONE INERT slot of a rule. Generation never reads it, a rule carrying
only `_evidence` is still refused as `PULSE_SYNTH_RULE_EMPTY`, and
deleting a candidate takes its evidence with it. The leading underscore
follows the repo's `_meta` convention for a block that is documentation
rather than payload.

```json
[
  {
    "when": "round(useCon) == 3 || round(useCon) == 4 || round(useCon) == 5 || round(useCon) == 6 || isnull(useCon)",
    "set_null": ["nps", "detractor", "passive", "promoter"],
    "_evidence": {
      "detector": "gating",
      "note": "measured, not asserted: \"useCon\" is the field whose levels split these 4 target(s) …",
      "gate_field": "useCon",
      "gated_levels": ["3", "4", "5", "6", "(null)"],
      "open_levels": ["1", "2"],
      "levels": [{"level": "1", "side": "open", "n": 40517, "mean_target_null_rate": 0}, "…"],
      "targets": [{"field": "nps", "null_rate": 0.826, "gated_null_rate": 1, "open_null_rate": 0}, "…"],
      "rows_observed": 381324,
      "rows_affected": 314981,
      "gated_share": 0.826019343130776,
      "max_null_rate_deviation": 0,
      "min_level_support": 19313
    }
  }
]
```

`gated_share` is the figure to compare against each target's own
`null_rate` in the profile document — that agreement is how the
relationship was first found by hand. It is a CHECKING aid rather than
the ranking signal: the split test arithmetically implies it, so every
admitted candidate scores well on it. Ranking is target count, then rows
affected.

### Predicates are emitted through `round()`

Uniformly, including for a `packed_bool` whose row value is already
exactly `0.0` or `1.0`. A generated row holds the sampler's float and
the file holds the stored integer, so a bare `familiarity == 1` selects
only the draws that landed exactly on 1 — measured at 531 of 901 rows in
the committed regression. The uniformity is deliberate: a file where one
numeric gate is spelled bare and another through `round()` teaches a
reader that bare is sometimes fine, and nothing in the file says which.

### What it will NOT propose, and says so

Both are COUNTED in `warnings` rather than dropped silently.

- A field with more than **16** observed levels. A gate is a branch, and
  a branch with more arms than that is an attribute. The candidate is
  ABANDONED, never truncated — a partial level map makes every rate
  below it a rate over an arbitrary subset of the cohort.
- A gate whose ONLY gated level is the null pseudo-level. That is
  co-missingness between fields rather than value gating, and every
  member of an N-field co-missing block reports the other N−1 that way;
  proposing them restates one finding N times and buries everything
  else. On the motivating cohort that is 3,932 relationships — and the
  co-missing detector below proposes those blocks as ONE candidate
  each, so the finding is counted here and resolved there.

Candidates resting on THIN levels are **not** suppressed — they ship with
`thin_support: true` and their `min_level_support`, warned thinnest-first
with a counted remainder. The analyst is better placed than a threshold
to judge a twelve-row level.

### Thresholds are constants, not flags

`gateHighNullRate` (0.98) and `gateLowNullRate` (0.02) are package
constants with their reasoning at the declaration in
`synth/profile_gating.go`, matching the `minVarianceExplained` /
`minLevelObservations` precedent: a threshold whose purpose is to mean
the same thing across cohorts must not be tunable per run. They are tight
because skip logic is EXACT in the source. A looser pair would not find
more gates; it would find ordinary ASSOCIATION and propose it as
structure, and a rule built from a 10%/30%/50% null rate is applied
unconditionally and wrong on most of the rows it selects.

### Two caveats that survive detection

**Candidates compose in declaration order.** They are not independent
statements. On the motivating cohort `aware` nulls the five `*Aware`
flags, and the five later candidates — each gated partly on
`isnull(<x>Aware)` — then fire on those nulls too. That is the
declaration-order contract working, and it is why the file's order is
the applied order. The file is ordered so that a candidate WRITING a
field another candidate READS comes before it; see *Emitted order is
applied order* below.

**A `set_null` gate repairs co-missingness only.** Each target's own
`null_rate` still fires beneath the gate, so the targets' marginal null
rate rises above the captured one. Rules cannot suppress a field's own
null draw.

### Measured on the motivating cohort

381,324 rows, 122 fields. Detection proposes **8** candidates and adds
**no cohort read** (+13% CPU against a full
`--conditional --fit-models --residual-correlations` capture; the output
is byte-identical with those flags and without them).

| gate | targets | `gated_share` | deviation |
|---|---|---|---|
| `round(aware) == 0` | 63 | 0.2526093295989762 | 1.2e-02 |
| `round(familiarity) == 1` | 63 | 0.2526093295989762 | 1.2e-02 |
| `round(useCon) ∈ {3,4,5,6} ∨ isnull` | 4 | 0.8260193431307760 | 0 |
| `round(peopleAware) == 0 ∨ isnull` | 1 (`people`) | 0.427288 | 0 |
| `round(promotionAware) == 0 ∨ isnull` | 1 (`promotion`) | 0.321231 | 0 |
| `round(placementAware) == 0 ∨ isnull` | 1 (`placement`) | 0.304429 | 0 |
| `round(productAware) == 0 ∨ isnull` | 1 (`product`) | 0.293158 | 0 |
| `round(priceAware) == 0 ∨ isnull` | 1 (`price`) | 0.288857 | 0 |

The first two are the proxy pair above. The third is the four-field NPS
block that was found by ACCIDENT during research — recovered here
automatically, with rate exactly 1 at every gated level and exactly 0 at
every open one. `aware`'s 63 targets are a strict SUPERSET of the 50
fields whose `null_rate` is exactly `0.2526093295989762`: the extra 13
are the `0.2649` cluster that research recorded as *"no matching single
boolean gate — UNEXPLAINED"*. They are gated by `aware` too, with a
further ~1.2% of missingness on the open side. The `0.3564` cluster (26
fields) has no single low-cardinality gate either — it is EXPLAINED by
the co-missing detector below, as an exact 26-field block.

Fed back unmodified through `synth from-profile --rules`, all 8 fire on
**100%** of their gated rows over 20,000 generated rows; the same seed
without the rules leaves `aware` coherent on 0 of 4,964 and the NPS block
on 7,819 of 16,806.

## Co-missing blocks (`null_together` candidates)

A survey question block is asked or skipped as a unit, so its fields are
present or absent together. Generation draws each field's null
independently from its own scalar `null_rate`, which turns the block into
a lottery — the defect `null_together` exists to fix
(`docs/src/cli/synth-from-schema.md`). This detector finds the blocks.

### An identical `null_rate` is never enough

This is the whole subtlety of the detector, and getting it wrong is
silent. Two entirely independent fields can share a null rate to sixteen
digits and overlap only by chance; grouping by rate proposes them as one
question block, `null_together` then makes the claim TRUE in generated
output, and the cohort acquires a structural fact the source does not
have.

So the rate is only the grouping key, and admission is **identical null
PATTERN** — null on exactly the same rows. Every member of an emitted
block therefore carries `agreement: 1`, an identical `null_count`, and
`max_null_rate_deviation: 0`.

That last figure is exactly the measure `null_together` itself warns on:
the rule copies the FIRST named member's null decision and ignores every
other member's declared `null_rate`, warning above a 0.02 divergence. For
an emitted block the divergence is zero, so no declared rate is
discarded — and the member ORDER is arbitrary by construction rather than
a ranking someone has to get right.

```json
{
  "null_together": ["nps", "detractor", "passive", "promoter"],
  "_evidence": {
    "detector": "co_missing",
    "note": "measured, not asserted: these 4 field(s) are null on exactly the same 314981 row(s) …",
    "block": [
      {"field": "nps", "type": "u4", "null_count": 314981, "null_rate": 0.826019343130776, "agreement": 1},
      {"field": "detractor", "type": "packed_bool", "null_count": 314981, "null_rate": 0.826019343130776, "agreement": 1}
    ],
    "rows_observed": 381324,
    "rows_affected": 314981,
    "gated_share": 0.826019343130776,
    "max_null_rate_deviation": 0,
    "min_level_support": 66343
  }
}
```

The candidate carries no `when`: an absent `when` means EVERY ROW, which
is the true statement — the members are null together on the rows where
the block is null and present together on the rows where it is not.

### A near block is reported, never proposed

Fields that agree on MOST rows but not all are reported in `warnings`
with their agreement and their disagreeing row count:

```
rule suggestion: "regard" (a block of 50) and "appropriate" (a block of 13)
are null together on 0.9537 of the rows where either is null
(4676 row(s) disagree) but not on exactly the same rows …
```

They are not emitted, and the asymmetry with the gating detector's thin
candidates is deliberate: a thin gate's RELATIONSHIP is exact and only
its support is thin, whereas a near block's relationship is measurably
false on some rows. `null_together` has no dial for "almost" — applying
one silently rewrites the rows that disagreed — and unlike a gating
candidate, whose `when` an analyst can correct, there is nothing in it
to fix.

The agreement is the overlap of the two null SETS (of the rows where
either is null, the share where both are), not row-level agreement. Two
independent fields each null at 1% agree on 98% of ROWS, so a row-level
threshold anywhere near 1 would flood the report with unrelated pairs.

### Always-null columns

A column null on every row is its own finding, named with its type:

```
always-null column "lgbt" (categorical_u8): null on all 381324 row(s) profiled,
so its marginal is summarised over zero observations and generation fabricates
a distribution for it; it has no gate and no block, and is not proposed as a rule
```

It is deliberately kept out of every block and every gate — it is not
co-missing with anything, it is simply absent — and no rule is proposed
for it. What generation should DO about such a column is a separate
decision.

### Emitted order is applied order

**A candidate that WRITES a field another candidate READS is emitted
BEFORE it.** Within that constraint the preference order — gating, then
co-missing blocks, then exact dependencies — is preserved as closely as
possible: candidates are walked in that order and a candidate a walked
one depends on is hoisted to just before it, never further.

The preference is the readable order and is kept wherever nothing forces
a move. For a block holding a gate's TARGETS the two orders are
equivalent, and that is arithmetic rather than luck: block members are
admitted only when their null patterns are identical, so they carry
identical conditional null rates at every level of every gate and the
gating detector classifies them identically. A gate takes a WHOLE block
or none of one. Gate-first is then chosen for what the file is FOR —
being edited: a hand-narrowed `set_null` over part of a block is repaired
by a block that follows it and broken by one that precedes it.

**That equivalence governs the fields a gate WRITES, and is false for the
field a gate READS.** When a block member is also a gate's `when` field,
a block placed after that gate moves the gate's own INPUT after the gate
has read it: the gate fires on the drawn value, the block then nulls the
field the gate was reading, and a target it left present is now an answer
on a row whose screener is absent. On the motivating cohort five gates
sit in exactly that shape (`peopleAware`, `promotionAware`,
`placementAware`, `productAware`, `priceAware` are all members of the
50-field block) and a sixth reads a field a `set_expr` rewrites
(`aware = round(familiarity) >= 2`).

| | orphan rows of 20,000 |
|---|---|
| preference order alone | **8,693** |
| writer-before-reader | **0** |

An orphan row is one carrying a value for a gate's target while the
gate's own field is absent.

**What it costs.** A block hoisted ahead of a gate loses, for that gate,
the repair property above: narrow that gate's target list by hand and the
hoisted block no longer follows the edit. The trade is not symmetric —
the repair property protects an edit that may never be made, the ordering
fault corrupts every generated row unconditionally. Only the candidates
that MUST move, move: on the motivating cohort the headline 63-target
gate stays first and one block is hoisted ahead of the five gates that
read it.

**If you reorder the file by hand, keep writers before readers.** A
mutual pair — each writing a field the other reads — has no satisfying
order; one edge is dropped, the surviving one decides the pair, and the
drop is reported as a warning naming both. It does not arise on the
motivating cohort.

### Bounded, on the same scan

The accumulator is a per-field bitset over a fixed chunk of rows, folded
into a pairwise co-null matrix by popcount and cleared. Memory is flat in
the row count and bounded by the field count; the per-row cost is one bit
write per null field. Over **256** nullable fields the detector abandons
rather than truncating — a truncated field set yields blocks that are
exact among the fields it kept and silently missing the rest, which is
the defect this detector exists to remove.

Blocks rank largest-first, capped at 20 with a counted remainder, and
each detector bounds its own listing rather than sharing one budget.

### Measured on the motivating cohort

Same 381,324 rows. Four blocks, one always-null column and four near
misses, alongside the eight gating candidates — 12 candidates in one
file, consumed by `synth from-profile --rules` unmodified.

| members | `gated_share` | first members |
|---|---|---|
| 50 | 0.2526093295989762 | `regard`, `meaningfulness`, `uniqueness`, … |
| 26 | 0.3564055763602606 | `funcAppearance`, `attentionToDetail`, `brandAssets`, … |
| 13 | 0.2648718674932603 | `appropriate`, `beliefsValues`, `clarity`, … |
| 4  | 0.8260193431307760 | `nps`, `detractor`, `passive`, `promoter` |

The **26-field `0.3564` cluster** is the one the gating detector could not
explain and research recorded as unexplained. It has no single
low-cardinality gate, and it is an EXACT co-missing block. The 50-field
and 13-field clusters are the `aware` targets, split apart here by their
patterns: they overlap on 0.9537 and are reported as a near miss rather
than merged.

One always-null column: `lgbt`. Near misses: the 50/13 pair above,
`sow` against the 26-field block (0.9989, 145 rows), and `useCon`
against `sow` (0.9798) and the 26-field block (0.9788).

Block detection costs **+2%** CPU on a full
`--conditional --fit-models --residual-correlations` capture (37.8s →
38.5s against a 34.0s baseline) and reads no additional byte. The
gating half of the candidate file is byte-identical with and without it.

Fed back unmodified, all four blocks are all-or-nothing on **20,000 of
20,000** generated rows. At the same seed without the rules: partial on
20,000 / 20,000 / 19,660 / 10,761.

## Exact dependencies (`set_expr` candidates)

`promoter` IS `nps >= 9`. Generation samples it from its own marginal and
gives it a captured linear model, so the synthetic cohort contains
promoters with a score of 3 — and the profile document has been carrying
the proof the whole time (`promoter + passive + detractor = 1.0000`,
exactly) with nothing reading it. This detector reads it.

### The search is NARROW, deliberately, and the bounds are published

A general functional-dependency search over 122 fields is quadratic,
mostly finds noise, and would have to be believed on the strength of a
claim nobody can check. What is searched:

| | Admitted |
|---|---|
| **Target** | `packed_bool`, `u4` — a boolean flag or a small integer |
| **Source** | `categorical_*`, `packed_bool`, `u4`, with at most **16** observed levels |
| **Arity** | exactly ONE source |

Everything else is out: wider numerics (`u8`+, `f32`/`f64`,
`decimal128`), `date`, `set_*`, categorical-VALUED targets (a derived
category needs the declared domain and a wrong arm silently grows the
dictionary), and joint dependencies on two fields at once.

**A field missing from the candidate file was not cleared — it was not
examined.** The bound is stated in every candidate's `_evidence.note`, on
stderr and here, because an analyst who believes a field was checked
stops looking.

### Band edges are DISCOVERED, never assumed

The measurement is a LOOKUP — source level to target value. Rendering it
as `round(nps) >= 9` rather than a nine-way disjunction is a separate
judgement, and the edges come from the data. The standard NPS 9-10 / 7-8
/ 0-6 definition is this detector's OUTPUT on the motivating cohort; it
is nowhere in its input.

A threshold form is preferred wherever the target's value regions are
contiguous in the source's own order, because it is a **total function**
of the source: a value generation produces that the cohort never carried
lands in the nearest band instead of falling off the end of an
enumeration. `_evidence.dependency[].form` names which reading was
chosen — `threshold` / `threshold_chain` are total, while `membership` /
`membership_complement` / `enumeration_chain` (a categorical source has
no order to be contiguous in) fall to a default arm.

### One candidate per SOURCE, with its `null_together` in the SAME rule

A partition is three measurements against one field. Three separate rules
would have to be kept consistent by hand — delete one and the remaining
two silently stop partitioning — so every target a source determines
rides ONE rule:

```json
{
  "set_expr": {
    "detractor": "round(nps) <= 6",
    "passive":   "round(nps) >= 7 && round(nps) <= 8",
    "promoter":  "round(nps) >= 9"
  },
  "null_together": ["nps", "detractor", "passive", "promoter"],
  "_evidence": {
    "detector": "dependency",
    "source_field": "nps",
    "source_levels": 11,
    "dependency": [
      {"field": "promoter", "type": "packed_bool", "form": "threshold",
       "mapping": [{"level": "0", "value": false, "n": 6272}, "…"],
       "exceptions": 0}
    ],
    "rows_observed": 381324,
    "rows_affected": 66343,
    "gated_share": 0.17398119,
    "max_null_rate_deviation": 0,
    "min_level_support": 1176
  }
}
```

Three properties of that snippet are load-bearing, and each was measured
wrong before it was measured right (see
[synth from-schema](synth-from-schema.md)):

- **`round()`, uniformly.** A generated row holds the sampler's float and
  the file holds the stored integer, so a bare `nps >= 9` classifies
  against a value the file does not show. Measured on this story's own
  suite: 838 of 4,495 scored rows disagree without it.
- **No normalisation rule.** The rounding is INSIDE each band predicate
  rather than in a separate `{"set_expr": {"nps": "round(nps)"}}` rule,
  so nothing writes to the source and nothing can un-null it — the
  `!isnull()` guard that remedy needs is unnecessary because the write
  does not happen.
- **`null_together` in THIS rule**, naming the SOURCE first. A `set_expr`
  clears its target's null mask; within a rule `null_together` is the
  last write, so the flags are computed and then take the score's null
  decision. Split into two rules the derivation runs afterwards and
  un-nulls every member: 1,505 orphan rows on the same suite.

The rule carries **no `when`**, which is the true statement (the target
is a function of the source on every co-present row) and which makes its
targets **pre-claim-eligible**: accepting one RETIRES the target's
captured linear model, conditional pairs and residual correlations rather
than computing them and overwriting the result. The note says so, because
that is the most valuable consequence of accepting a candidate.

### Admission: identical null patterns, or nothing

A candidate is emitted only when the source and every target are null on
EXACTLY the same rows — the same test the co-missing detector applies,
served from that detector's own co-null accumulator rather than a second
one. When the patterns differ, the dependency is REPORTED and not
emitted: a `set_expr` clears the target's null mask, so the rule would
un-null the target on every row its source is absent from. When the
accumulator is unavailable (abandoned over 256 nullable fields) the
answer is "unknown" and the candidate is reported, never guessed.

### What it reports rather than proposes

- An **almost-determined** pair — at least one and at most **8**
  contradicting rows — with its exception count. `set_expr` has no dial
  for "almost" and would rewrite exactly the rows that disagreed, and
  unlike a gating candidate's `when` there is nothing in it to correct.
  Over 8 the pair is dropped entirely and not reported, because it is not
  "almost" anything. The count is taken against the two most common
  values at each source level, so it does not depend on the order the
  rows arrived in.
- A **constant column**: determined by every other field and by none of
  them. Excluded from both roles and named once.
- A **mutually-determining pair**: both directions are proposed and both
  are consistent, but either alone is sufficient and the pair is usually
  one column under two names.
- A **contested target** determined by two sources: written once, by the
  strongest candidate, because two rules writing one field leaves the
  earlier one firing with no effect.

Thin candidates are **not** suppressed — they ship with
`thin_support: true` and their `min_level_support`.

### Emitted LAST, and the order has teeth

Gating candidates, then co-missing blocks, then dependencies. A
dependency rule writes VALUES and carries its own `null_together`, so
placed last it re-resolves that block from the source AFTER every
null-state rule has decided the source's own null state. Measured on this
story's suite with a hand-narrowed `set_null` over the source alone:
dependency-last leaves **0** orphan rows, dependency-first leaves **940**.

The one exception is the writer-before-reader rule above: a dependency
whose `set_expr` writes a field a GATE reads is hoisted ahead of that
gate, because otherwise the gate fires on a value the dependency rewrites
afterwards. `aware = round(familiarity) >= 2` against a gate on `aware`
is exactly that shape on the motivating cohort.

### Measured on the motivating cohort

Same 381,324 rows, 122 fields. **Two** dependency candidates, alongside
the eight gating candidates and four blocks — 14 in one file.

| source | targets | expression | form | co-present |
|---|---|---|---|---|
| `nps` (11 levels) | `detractor` | `round(nps) <= 6` | threshold | 66,343 |
| | `passive` | `round(nps) >= 7 && round(nps) <= 8` | threshold | |
| | `promoter` | `round(nps) >= 9` | threshold | |
| `familiarity` (7 levels) | `aware` | `round(familiarity) >= 2` | threshold | 381,324 |

The NPS partition comes back as **ONE** candidate with the standard
9-10 / 7-8 / 0-6 band edges, discovered rather than assumed, carrying
`null_together: ["nps", "detractor", "passive", "promoter"]` — the same
four fields the co-missing detector proposes as a block, restated inside
the rule that derives them because that is where it has to be.

**The second candidate answers the proxy question the gating detector
could only report.** `aware` and `familiarity == 1` select the same
96,326 rows with identical evidence to sixteen digits, and nothing in a
null-state measurement can separate them. Reading VALUES does: `aware` is
an exact function of `familiarity` on all 381,324 rows, so they are not
two independent gates — one is DERIVED from the other, and the analyst's
own rule names the source.

Reported, not proposed: three constant columns (`wave`, `country`,
`_synthetic` — the last an artefact of taking the real partition), and
five fields over the level cap (`ageExact`, `brand`, `category`, `dma`,
`region`). No almost-determined pair and no null-shape mismatch survive
on this cohort.

Fed back unmodified through `synth from-profile --rules`, both candidates
reproduce the source mapping on **100%** of co-present rows with **zero**
orphan rows. At the same seed without them: `detractor` 86.3%, `promoter`
76.8%, `passive` 56.1% agreement, each with ~5,700 rows carrying a band
flag and no score, and `aware` 81.8%.

Detection costs **+7.5%** CPU against the two-detector figure (38.6s →
41.5s on a full `--conditional --fit-models --residual-correlations`
capture, against a 34.6s no-detection baseline) and reads **no additional
byte**. The candidate output is byte-identical with those flags and
without them, and the eight gating candidates and four blocks are
byte-identical to the file the previous two detectors wrote.

## Output

The profile JSON is always written to `--output`. With `--json`, the
envelope is also written to stdout (typically piped or `jq`-d).

Profile schema lives in `synth/profile.go` and is documented in
`skills/synthetic-data.md`.

### Reproducibility — across machines, not just across runs

The same `(--input, --seed, flags)` produces a **byte-identical** profile
document, and that now holds **across CPU architectures** as well as
across runs on one machine. A profile document is something you
commit, review in a diff and hand to a colleague, so a guarantee that
only held per-machine was not much of a guarantee: two engineers, one on
an arm64 laptop and one on an amd64 build box, captured the same cohort
and got documents that differed in the last bits of every `std` and
`rho`.

Two causes, both closed:

- **Fold order.** Anywhere capture folds several accumulators into one
  shared bucket — canonically the `--top-k` collapse folding every
  out-of-top-K category into `"other"` — the fold walks sorted keys.
  Float addition is not associative and Go randomizes map iteration, so
  a map-order fold gave a different answer per *process*.
- **Float fusion.** Go permits `a + b*c` to be contracted into a single
  fused multiply-add; arm64 does it, amd64 does not. Every product in a
  capture formula now carries an explicit `float64(...)` conversion,
  which the language defines as forbidding contraction.

Two residuals remain, and they are stated rather than papered over:

- `--fit-shape` runs an EM fit through `math.Exp` / `math.Log`, whose
  standard-library implementations are architecture-specific. A
  `shape` section's last bits can therefore still differ between
  machines. Every other section is architecture-independent.
- `--fit-models` solves its least squares in `processing/regression`,
  which has not been made fusion-free. A `models` coefficient's last
  bits can differ between machines.

Neither affects a value you would read or report — the divergence is at
the 1e-15 relative level — but a byte-for-byte `diff` of two documents
captured with those flags on different CPUs may still show movement.

### Text mode summary

```
Profiled 50000 rows from sales.pulse -> sales.profile.json
```

That single line is the whole of **stdout**, so `pulse profile create …
> log` and any pipeline over it keep exactly the bytes they always had.

### Warning summary

Diagnostics go to **stderr**, grouped by kind, counted, and capped at
three examples per kind. On the 381,324-row cohort that produces 3,005
warnings, the terminal shows twenty-one lines:

```
Warnings: 3005 in 5 kind(s) — 1 needing attention, 3004 expected
  ! residual correlation pairs unmeasured (1)
      residual correlations: 104 of 5460 pair(s) among 105 modelled field(s) could not be measured …
  - thin categorical pair (2931)
      thin categorical pair wave=20230220 x ethnicity=1005: only 12 supporting observation(s) (below 30) …
      thin categorical pair wave=20230220 x income=1012: only 23 supporting observation(s) (below 30) …
      thin categorical pair wave=20230220 x brand=11: only 29 supporting observation(s) (below 30) …
      +2928 more of this kind
  - model carries no predictors (50)
      model for numeric field "placementAware" carries no predictors: no candidate explained at least 1.0% …
      +47 more of this kind
  …
  Full list: sales.profile.json (.warnings)
```

Three properties are load-bearing:

- **Kinds needing attention come first**, marked `!`, whatever their
  count. A single model that could not be applied stays visible above
  two thousand nine hundred thin-pair lines; sorting by volume would
  bury it, which is exactly how a defect that disabled most of
  `--fit-models` survived undetected.
- **Expected outcomes are counted separately** and marked `-`. A
  zero-predictor model is a *complete* model, a thin pair still ships,
  and an arbitrated pair claim is the design working; none of them
  inflate the `needing attention` figure.
- **The cap is a screen budget, not a data limit.** Three examples per
  kind (`maxWarningExamples`, `internal/cli/warnings.go`) is smaller
  than the twenty synth's own `warnings` array uses for its internal
  roll-ups, because every kind shares one terminal. The document keeps
  every line.

With `--json` the summary is not printed at all: the envelope already
carries `data.warnings` in full.

### Where the warnings are

Every diagnostic this command raises — thin pairs, shrunk levels,
zero-predictor models, skipped models, unmeasured residual pairs — lands
in the profile document's own `warnings` array, **and a grouped summary
of it is printed to stderr** (see [Warning summary](#warning-summary)
above). The summary is bounded on purpose and names only three examples
per kind, so the document is still where the full list lives:

```bash
# how many, and what kind
jq '.warnings | length' sales.profile.json
jq -r '.warnings[]' sales.profile.json | sed 's/[0-9][0-9]*/N/g' | sort | uniq -c | sort -rn

# the two that change what you can trust
jq -r '.warnings[] | select(contains("skipped"))'          sales.profile.json
jq -r '.warnings[] | select(contains("no predictors"))'    sales.profile.json
```

Volume is expected and is bounded on purpose: `--conditional` on a wide
cohort emits a thin-pair line per thin pair (thousands is normal), while
thin *levels* are aggregated per level and capped at twenty with a
counted summary. A large count is not itself a problem — the two lines
worth grepping for are the ones above, since a *skipped* model is a
field that lost its fit and a *no predictors* model is a field the
cohort does not explain.

Two related warning kinds are **not** here, because they are not
produced here: `conditional relationship conflict: …` and
`model for numeric field "x" not applied: …` arise when the profile is
translated into a generation spec, which happens inside
[`pulse synth from-profile`](synth-from-profile.md). That command's own
stderr summary covers them — it spans all three channels — and the full
list is in its `--fidelity-report`, where they are appended after this
document's own warnings.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Read error, unsupported field type (`PULSE_PROFILE_FIELD_UNSUPPORTED`), or write failure |

## Examples

### Minimal profile

```bash
pulse profile create --input sales.pulse --output sales.profile.json
```

### Rich profile with correlations

```bash
pulse profile create --input sales.pulse --output sales.profile.json \
    --include-stats --include-correlations --top-k 64 --correlation-top-k 32
```

### One numeric conditioned by several categoricals

This is what `--fit-models` adds that no earlier flag could express.
`--conditional` measures a numeric field against **one** categorical at
a time, and at generation time only one of those pairs can claim the
field — the rest are dropped as conflicts, one warning each. A linear
model carries all of them at once.

```bash
pulse profile create --input survey.pulse --output survey.profile.json \
    --include-stats --conditional --fit-models
```

Ask the document which fields condition `spend`:

```bash
jq '.models[] | select(.field == "spend")
    | {n_obs, r2, shrinkage_alpha,
       fields: [.predictors[].field] | unique,
       terms: (.predictors | length),
       references}' survey.profile.json
```

```json
{
  "n_obs": 9840,
  "r2": 0.31,
  "shrinkage_alpha": null,
  "fields": ["region", "segment", "tier"],
  "terms": 27,
  "references": [
    {"field": "region",  "level": "east"},
    {"field": "segment", "level": "consumer"},
    {"field": "tier",    "level": "bronze"}
  ]
}
```

Three separate categoricals now condition `spend` additively: a `west`
row in the `enterprise` segment on the `gold` tier gets all three
offsets, not whichever pair happened to claim the field first. The
`"shrinkage_alpha": null` is `jq` making an absent key explicit — every
level in this design cleared 50 rows, so the fit is plain least squares.

The `--conditional` pairs for `spend` are still in the document — the
capture keeps everything it measured — and it is
[`synth from-profile`](synth-from-profile.md#generating-a-numeric-conditioned-by-several-categoricals)
that stands them down for a modelled target. The retirement is per
**target**: a numeric that got no usable model keeps its pair, so the
fallback is measured structure rather than nothing.

On the 381k-row survey cohort behind this feature the same query against
`nps` returns seven conditioning fields — `age`, `ageExact`, `brand`,
`category`, `educationLevel`, `ethnicity` and `income` — over a
126-column design. Under `--conditional` alone, six of those seven
relationships were unrepresentable.

### Sample-limited profile for a huge cohort

```bash
pulse profile create --input ops.pulse --output ops.profile.json --sample-limit 1000000
```

## Round-trip with synth

```bash
pulse profile create --input sales.pulse --output sales.profile.json
pulse synth from-profile --profile sales.profile.json --output sales.synth.pulse --rows 10000 --seed 1
pulse cohort inspect sales.synth.pulse
```

## Related

- [`pulse synth from-profile`](synth-from-profile.md) — the
  consumer of profile JSON
- [`pulse synth from-schema`](synth-from-schema.md) — the alternative
  spec-driven path
- `skills/synthetic-data.md` — full profile and spec grammar
- [Library: pulse.Profile](../library/overview.md)
