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
| `--fit-models`           |      | bool   | false      | Fit one linear model per numeric field on the same scan — the field regressed on the cohort's categorical levels and set options — capturing coefficients, residual scale and fitted residuals (see below) |
| `--sample-limit`         |      | int    | 0 (unlimited) | Cap rows ingested for the profile (0 disables) |
| `--seed`                 |      | int    | 0          | Deterministic RNG seed for `--conditional`'s categorical-categorical reservoir sampling and `--fit-models`' residual reservoir (see below); each draws from its own stream, so the two flags never perturb each other, and the same `(--input, --seed)` produces byte-identical captured output |
| `--json`                 |      | bool   | false      | Also print the envelope to stdout |

## What the profile captures

| Field type | What is recorded |
|---|---|
| Numeric (`u*`, `f*`, `decimal128`) | Count, min, max, mean, stddev; percentiles if `--include-stats` |
| Categorical | Top-K most-frequent values + their frequencies; "other" tail weight |
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
approximation rather than an optimal one. A field's captured shape and
`--conditional`'s categorical-numeric conditional structure (or a
numeric-numeric correlation) for that same field have not been asked
to compose, but the conflict is no longer resolved silently: at
`synth from-profile` generation time a `--fit-shape`-captured field is
pre-claimed under `"captured shape (--fit-shape)"` before any
conditional-pairing or correlation stage runs
(`synth.resolveConflicts`, `synth/conflict.go`), so the shape fit
always wins that field — but the excluded relationship (the dropped
categorical-numeric pair, or that field's exclusion from a correlation
whose other participants still correlate) is reported as one warning
naming both the field and which relationship lost. That warning lands
on `synth.Result.Warnings` (`data.warnings` under `--json`) for every
`synth from-schema` / `synth from-profile` run, and additionally folds
into `--fidelity-report`'s own `warnings` array for a `synth
from-profile` run, alongside `profile create`'s own capture-time
thin-pair warnings. The same priority-ordered claim mechanism resolves
every other conditional-relationship collision too (e.g. two
categorical-numeric pairs both naming the same numeric field) — a
shape fit is simply the highest-priority claimant, not a special case.

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

The fit rides the scan `profile create` already makes — no second pass
over the cohort. The regression engine is a streaming accumulator whose
per-field state is quadratic in the predictor count and **independent of
row count**, so the flag's cost does not grow with cohort size.

Each categorical contributes one column per level except one
**reference** level (the first dictionary entry), whose effect is
absorbed into the model's intercept; every other coefficient is read
relative to it. A full level set alongside an intercept is
rank-deficient by construction, which is why one has to go. `set_*`
options are **not** subject to this — a multi-select is not a partition,
so each option is an independent indicator and all of them are kept.

Alongside the coefficients the capture keeps each model's residual scale
and its **fitted residuals** (observed minus predicted) over a bounded,
row-aligned sample of the scan. Residuals are what remains once a
field's systematic variation is explained, and they are the honest input
to any later measurement of how two numeric fields co-move.

Skipping is never a refusal. A field with no usable predictors, too few
non-null rows to fit, or a rank-deficient design (two nested
categoricals, say `region` inside `dma`, or a single-valued `wave`
column) loses **its own** model and is named in the profile's
`warnings`; every other field, and the whole rest of the document, is
captured exactly as usual.

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
    "residual_std": 1.41
  }
]
```

A coefficient is addressed by **`(field, level)`** — never by the
solver's internal design-column name, which is an implementation detail
and is deliberately absent, so the encoding stays free to change without
invalidating documents already on disk. `references` names the level
each categorical DROPPED into the intercept, so a reader holding *k−1*
of *k* levels does not have to guess which one is the zero baseline (a
model with only `set_*` predictors carries no `references` key — a
multi-select is not a partition and has no baseline arm).

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

### What `models` replaces at generation time

`synth from-profile` reads the section instead of `--conditional`'s
**numeric-target** pairs: when `models` is present,
`conditional.categorical_numeric_pairs` and
`conditional.set_numeric_pairs` are not applied at all. They exist to
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

A model that cannot be applied to the reconstructed spec — its target
was `--fit-shape`-fitted (a mixture has no closed-form quantile a linear
predictor can ride), or it names a field the profile does not carry — is
dropped with a `model for numeric field "x" not applied: …` warning. It
does **not** revive the retired arms: the retirement follows from the
section being present, not from any individual model surviving.

Without the section — every document written before this flag existed
included — nothing changes: all five arms populate exactly as before.

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

A `correlations` entry naming a modelled field is **not yet applied** —
correlated residuals are the intended composition and have not landed —
and is reported as such rather than silently overwriting the model's
draw.

## Output

The profile JSON is always written to `--output`. With `--json`, the
envelope is also written to stdout (typically piped or `jq`-d).

Profile schema lives in `synth/profile.go` and is documented in
`skills/synthetic-data.md`.

### Text mode summary

```
Profiled 50000 rows from sales.pulse -> sales.profile.json
```

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
