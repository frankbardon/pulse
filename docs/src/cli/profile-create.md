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
                     [--residual-correlations]
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
| `--sample-limit`         |      | int    | 0 (unlimited) | Cap rows ingested for the profile (0 disables) |
| `--seed`                 |      | int    | 0          | Deterministic RNG seed for `--conditional`'s categorical-categorical reservoir sampling and `--fit-models`' residual reservoir (see below); each draws from its own stream, so the two flags never perturb each other, and the same `(--input, --seed)` produces byte-identical captured output |
| `--json`                 |      | bool   | false      | Also print the envelope to stdout |

## What the profile captures

| Field type | What is recorded |
|---|---|
| Numeric (`u*`, `f*`, `decimal128`) | Count, min, max, mean, stddev; percentiles if `--include-stats` |
| Categorical | Cardinality, plus the top-K most-frequent values with their observed weights. The tail below the cut is **not** retained as an `"other"` bucket — `synth from-profile` renormalises the retained weights, so a 1,900-level field regenerates as `--top-k` levels and the tail's share is redistributed across them. (The `"other"` spelling that *does* appear in `conditional.*` tables and in `--fit-models` designs is a different, per-section collapse.) |
| `date` | Min, max, count |
| `nullable_*` | Null count alongside the above |

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

## Output

The profile JSON is always written to `--output`. With `--json`, the
envelope is also written to stdout (typically piped or `jq`-d).

Profile schema lives in `synth/profile.go` and is documented in
`skills/synthetic-data.md`.

### Text mode summary

```
Profiled 50000 rows from sales.pulse -> sales.profile.json
```

### Where the warnings are

Every diagnostic this command raises — thin pairs, shrunk levels,
zero-predictor models, skipped models, unmeasured residual pairs — lands
in the profile document's own `warnings` array. **The text summary above
prints the row count and nothing else**, so on a capture where something
was thin or dropped, the terminal looks exactly like a capture where
nothing was. Read the array:

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
[`pulse synth from-profile`](synth-from-profile.md). Look for those on
that command's `--json` output (`data.warnings`) or in its
`--fidelity-report`, where they are appended after this document's own
warnings.

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
