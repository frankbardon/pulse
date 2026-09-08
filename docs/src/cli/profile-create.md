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
                     [--conditional] [--fit-shape]
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
| `--sample-limit`         |      | int    | 0 (unlimited) | Cap rows ingested for the profile (0 disables) |
| `--seed`                 |      | int    | 0          | Deterministic RNG seed for `--conditional`'s categorical-categorical reservoir sampling (see below); same `(--input, --seed)` produces byte-identical captured output |
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
approximation rather than an optimal one. A field's captured shape
takes priority over `--conditional`'s categorical-numeric conditional
structure for that same field — the two have not been asked to compose,
and the (usually more informative) shape fit silently wins rather than
either crashing or being reconciled by guesswork.

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
