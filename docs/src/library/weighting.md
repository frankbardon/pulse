# Row Weighting

**Audience:** analysts and embedders running survey or sampled data,
where each row stands for a different number of people, accounts or
events.

Name a weight column once and every weight-aware figure in the request
counts each row as `w` rows: counts become Σw, sums Σw·x, means
Σw·x / Σw, and crosstab cells, margins and shares follow. A request
that names no weight produces exactly the output it always did, byte
for byte, and `format_version` stays `"1.1"`.

## Where a weight comes from

There are three places to name one. For each slot Pulse takes the
first that applies:

1. the slot's own `weight` (an explicit `null` opts that slot out, and
   resolution stops there);
2. the request's `weight`;
3. the engine default, `pulse.Options.DefaultWeight`;
4. none.

```json
{
  "cohort": {"filename": "survey.pulse"},
  "weight": {"field": "wt", "kind": "probability"},
  "groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "weighted_n"},
    {"type": "AGG_COUNT", "field": "id", "label": "respondents", "weight": null},
    {"type": "AGG_AVERAGE", "field": "spend", "label": "mean_spend"}
  ]
}
```

Here `weighted_n` and `mean_spend` are weighted and `respondents` is
the raw count of respondents.

A per-slot `weight` is a field name (`"wt"`), a `{field, kind}` object
or `null`. The slot is on aggregations (which covers the crosstab
`cell` and each `margin_aggregations` entry), tests and post-tests,
regressions, attributes, overlays, groups and both crosstab axes.
Windows have no slot. Compose and process chains have no top-level
weight: each inner request carries its own. Facets are never weighted,
and `DefaultWeight` does not reach them.

From Go:

```go
req.Weight = &types.WeightSpec{Field: "wt"}               // request weight
req.Aggregations[1].Weight = types.NullSlotWeight()       // opt one slot out
req.Aggregations[2].Weight = types.SlotWeightField("w2")  // a different column

p, err := pulse.New(pulse.Options{
    DefaultWeight: &types.WeightSpec{Field: "wt", Kind: types.WeightKindFrequency},
})
```

`types.SlotWeight`'s zero value means absent (inherit).
`NullSlotWeight()`, `SlotWeightField(f)` and `SlotWeightOf(spec)` build
the other two states. The distinction survives JSON, request hashing,
MCP and the payload schema.

The weight column must be an unsigned-integer (`u4` to `u64`) or float
(`f32`, `f64`) field of the cohort. Derived columns are not accepted,
but a joined, prefixed name is. Any other type, an empty field or an
unknown `kind` is `PROCESSING_CONFIG`; an unknown name is
`SERVICE_VALIDATION`. `pulse.New` checks `DefaultWeight` up front, but
judges its field only on a request where it applies, so an instance
default never breaks a cohort that lacks the column for a request that
does not use it.

## Probability or frequency

| `kind` | Meaning | Consequence |
|---|---|---|
| `probability` (default) | design / sampling weights | Ratios, means, shares and quantiles are scale-free: multiplying every weight by a constant changes nothing. Totals scale. Components report Kish `n_eff`. |
| `frequency` | each row stands for `w` identical rows | Integer weights reproduce the physically duplicated cohort exactly. A fractional weight is invalid. |

Weighted median and percentile follow Hmisc `wtd.quantile`. Frequency
weights give R's / numpy's type 7 (linear) on the duplicated rows.
Probability weights are first rescaled to sum to the row count
(`normwt = TRUE`), which makes the figure scale-invariant. Cumulative
weights within 1e-9 of an integer are snapped to it, so the answer
does not depend on float summation order.

## Invalid weights

Invalid weights are never coerced:

| Weight | Treatment |
|---|---|
| positive, finite | used |
| zero | valid; contributes nothing |
| null, negative, NaN / ±Inf | row excluded from that figure and counted |
| non-integer under `frequency` | row excluded and counted |

Each weighted slot reports its exclusions as `n_weight_invalid`. Each
response carries one `PULSE_WEIGHT_INVALID_ROWS` warning per weight
column, with `details.by_reason` counting `null`, `negative`, `nan_inf`
and `non_integer_frequency`. Under `Options.Strict` (`--strict`) that
warning is an error.

## What a weight applies to

Check the manifest rather than this page: every operator that computes
a weighted figure carries `weight_aware: true`. Extension operators
declare it the same way.

| Operator class | Explicit weight (slot or request) | `DefaultWeight` only |
|---|---|---|
| weight-aware aggregators | weighted | weighted |
| other aggregators (min, max, range, distinct-style, null count, set union / intersection) | `PROCESSING_CONFIG` | skipped |
| windows | request weight: `PROCESSING_CONFIG` | skipped |
| filters, features, row-local attributes, other groupers | skipped | skipped |
| the moment tests — t (one- and two-sample), Welch t, paired t, two-sample z, one-way ANOVA F, Welch ANOVA, Pearson r — plus the two-proportion z-test and the χ² independence test | weighted (both kinds) | weighted |
| the rank tests — Mann-Whitney U, Wilcoxon signed-rank, Kruskal-Wallis, Spearman ρ, Kendall τ-b — plus Fisher's exact test, the two-sample Kolmogorov-Smirnov test and Brown-Forsythe | `kind: frequency`: weighted; `kind: probability`: `PULSE_WEIGHT_UNSUPPORTED` | same, by the default's kind |
| the mean-comparison overlays — cell and reference t and z, pairwise Welch t — and the χ² (row, column, matrix, vs reference) and proportion z (cell, panel, pairwise) overlays | weighted (both kinds) | weighted |
| the Fisher exact cell overlay | `kind: frequency`: weighted; `kind: probability`: `PULSE_WEIGHT_UNSUPPORTED` | same, by the default's kind |
| ordinary least squares (plain, ridge, lasso, elastic net), the GLM (binomial, poisson, gamma), the regression attributes (fitted value, residual, leverage) | weighted (both kinds) | weighted |
| Bayesian linear regression | `kind: frequency`: weighted; `kind: probability`: `PULSE_WEIGHT_UNSUPPORTED` | same, by the default's kind |
| other tests, any regression with `resample` or `selection`, reference-distribution attributes (z-score, t-score, percentile rank), the quantile grouper, the pairwise two-means z and probit t overlays | `PULSE_WEIGHT_UNSUPPORTED` | `PULSE_WEIGHT_UNSUPPORTED` |

**Weighted tests.** A weighted moment test uses the frequency formula
with its sample size read as Σw under `kind: frequency` (a weight of 3
is three identical rows) or as Kish's effective n,
n_eff = (Σw)²/Σw², under `kind: probability`. Means and r are the same
under either kind; standard errors and degrees of freedom (which may be
fractional) read that effective size. The result's `details.n` stays
the raw row count; `sum_weights` and, for probability weights, `n_eff`
appear beside it in the same shape. A group whose n_eff falls below
the test's minimum warns `PULSE_WEIGHT_LOW_NEFF` (an error under
strict mode). This is not design-based (strata / cluster) variance.

The two-proportion z-test reads each group's rate as Σw of successes
over Σw, with the group's sample size read as above; `successes`
becomes the weighted success total. The χ² independence test builds
its table from Σw per cell: under `kind: frequency` it is the ordinary
Pearson test on that table (identical to the expanded rows); under
`kind: probability` the cell proportions are scaled to the table's
n_eff before the ordinary Pearson test, and the expected-count check,
Cramér's V and φ read that scaled table. This is a first-order Kish
approximation, not the Rao-Scott correction survey software
(`survey::svychisq`) applies.

The rank tests take frequency weights only. A row of weight w ranks as
w identical rows (a run of tied values shares the mid-rank of its
total weight), the tie corrections read those expanded tie sizes and
Kendall's τ-b counts a row pair w_i·w_j times, so each answer equals
the unweighted test on the physically expanded rows. `details.n` and
the other row counts stay raw; `sum_weights` appears beside `n`.
Because every weighted row is itself a tie on the expansion, a
frequency-weighted rank test often warns `PULSE_TEST_TIES_DOMINATE`.
There is no standard probability-weighted rank test, so a probability
weight is refused with a message naming the kind; switch the weight to
`kind: frequency` (if the weights are replication counts) or set the
slot's `weight: null`.

Fisher's exact test, the Kolmogorov-Smirnov test and Brown-Forsythe
take frequency weights only, for the same reason. Fisher's exact test
runs on the Σw table (`details.contingency` reports it); KS steps each
empirical CDF by a row's weight and reads its asymptotic p-value on the
Σw group sizes; Brown-Forsythe takes each group's median of the
expanded rows (the frequency weighted median of `AGG_MEDIAN`) and runs
the one-way ANOVA on the weighted absolute deviations. Each equals the
unweighted test on the expanded rows. The KS statistic compares the two
CDFs only after every row tied at a value is counted on both sides,
which is also the unweighted rule (ties inside one sample no longer
inflate D).

**Weighted regressions.** Ordinary least squares fits weighted least
squares: the coefficients minimise Σw·(y − ŷ)² and are the same under
either kind. Standard errors, degrees of freedom (N* − p − 1, possibly
fractional), p-values, adjusted R² and the residual standard error use
the effective size N* — Σw under `kind: frequency`, Kish's n_eff under
`kind: probability`. Under frequency weights the fit equals the one on
the expanded rows. R's `lm(weights = w)` gives the same coefficients but
counts the rows, not Σw or n_eff, for its residual degrees of freedom,
so its standard errors differ. Ridge, lasso and elastic net scale the
penalty by Σw, so their coefficients do not change when every weight is
multiplied by the same constant, and they match `glmnet(weights = w)`
under frequency weights. Their standard errors stay plug-in
approximations, now on N*. Bayesian linear regression takes frequency
weights only: X'WX, X'Wy and Σw enter the conjugate posterior, which
equals the posterior on the expanded rows. `n_obs` stays the raw row
count. A weighted result adds `sum_weights` and, for probability
weights, `n_eff`. A probability-weighted fit whose n_eff falls below
the predictor count plus one warns `PULSE_WEIGHT_LOW_NEFF`. A
regression with `resample` or `selection` refuses any weight, because
neither has a standard weighted form.

The GLM runs iteratively reweighted least squares with the row weights
as prior weights on w* = w·N*/Σw, so its fit is R's
`glm(weights = w*)`: the coefficients are the same under either kind,
the standard errors come from (XᵀW*X)⁻¹ and the deviance and null
deviance sum w*·d. Under frequency weights that is `glm(weights = w)`
and equals the fit on the expanded rows. The dispersion stays fixed at
1 for every family, gamma included, so treat gamma standard errors with
the same caution as unweighted ones. The regression attributes refit
ordinary least squares with the slot's weight and accept both kinds:
the fitted value comes from the weighted coefficients, the residual is
the raw y − ŷ, and the leverage is R's `hatvalues()` on the weighted
`lm`, the diagonal of W½X(XᵀWX)⁻¹XᵀW½. A row whose weight is zero or
invalid stays out of the refit and gets leverage 0.

**Weighted confidence bounds.** `AGG_CI_LOWER` and `AGG_CI_UPPER` are
weight-aware aggregators under either kind. The bound is the weighted
mean ∓ z·√(s²/N*): s² is the sample variance on w* = w·N*/Σw, N* is Σw
under `kind: frequency` (the bound on the expanded rows) or Kish's
n_eff under `kind: probability`, and z is the same normal critical
value the unweighted bound uses (`qnorm(1 − α/2)`, reported as
`t_critical`), not a t on N* − 1. They stay mergeable, so they run on
every streaming, shard and parallel-decode path.

**Other weighted inference is not available yet.** The last row is
refused rather than silently computed unweighted beside weighted
figures. To
run one of those operators in a weighted request, give its slot
`"weight": null`. On an instance with a `DefaultWeight`, every
inferential slot needs that opt-out. A weight-aware aggregator over a
`decimal128` field is also `PULSE_WEIGHT_UNSUPPORTED`, because the
decimal path has no weighted form.

`pulse predict` reports the outcome for every slot under
`data.weights[]`: `{slot, operator, field, kind, status, source}`.
`status` is `applied`, `skipped_not_weight_aware`, `opted_out` or
`none`. `source` is `slot`, `request`, `options` or `none`. Predict
refuses exactly what the run would refuse.

`AGG_WEIGHTED_MEAN` is the weighted `AGG_AVERAGE`. Its
`params.weight_field` is shorthand for a slot weight with
`kind: probability` and is now optional: without it, the slot inherits
the request weight or the default.

## Crosstabs and the unweighted base

A weighted crosstab recomputes cells, row, column and grand margins,
and every normalization from the weighted rows, on both execution
arms. `CellCounts` and the margin counts stay raw row counts. The
standard survey table shows a weighted figure beside an unweighted
base, and an auxiliary margin aggregation that opts out gives you
that base:

```json
{
  "weight": {"field": "wt"},
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "weighted_n"},
    "margins": {"rows": true, "columns": true, "grand": true},
    "margin_aggregations": [
      {"type": "AGG_COUNT", "field": "id", "label": "base", "weight": null}
    ]
  }
}
```

A record reaches an auxiliary margin only if it reached a cell. So the
base counts exactly the respondents the weighted cells beside it
counted.

## Overlays

Share and index overlays (share of row / column / total, index vs
margin, total, prior, baseline, rolling mean, sibling, reference) read
the host's figures. On a weighted host they are therefore weighted,
with no setting needed. The pairwise two-means z and probit t
overlays refuse when a weight reaches the overlay itself: its own
`weight`, the request's, or the default. Its `"weight": null` opts it
out. A host weighted only by its own slot weight does not trigger the
refusal.
`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` is the exception, built for
weighted cells: its host is an `AGG_WEIGHTED_MEAN` cell or a weighted
`AGG_AVERAGE` cell.

The mean-comparison overlays (`OVERLAY_T_CELL`, `OVERLAY_Z_CELL`,
`OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`, `OVERLAY_PAIRWISE_WELCH_T`) run
weighted under both kinds. A host cell is weighted when its components
carry `sum_weights`, whatever set the weight — including the cell's own
slot weight. Such a cell's sample size is Σw (`frequency`) or n_eff
(`probability`), never its raw row count, and its variance is
recomputed from `m2` on the rescaled weights; degrees of freedom may be
fractional. The layer summary's `parameters` add `sum_weights` (and
`n_eff` under `probability`) over the cells the layer read. Before
this release a host weighted only by its own slot weight ran these
overlays on raw row counts, which gave standard errors that were too
small; that was a bug and is fixed. On a weighted host the pairwise
Welch t refuses an `n_source` that reads raw row counts
(`cell_n_unweighted`, `row_margin_n`, `column_margin_n`), and
`cell_weight_sum` under `probability`, with `PROCESSING_CONFIG`.

The χ² and proportion overlays also run weighted under both kinds. A
weighted host's cells and margins are weight sums (Σw). Under
`frequency` the χ² overlays run Pearson's test on the Σw table, which
equals the test on the expanded rows. Under `probability` the table
(for `OVERLAY_CHISQ_MATRIX`), each row (`_ROW`), each column (`_COL`)
or the target table (`_VS_REF`) is first scaled to its Kish n_eff.
This is a first-order Kish approximation, not the Rao-Scott correction
survey packages apply, so expect it to differ from `svychisq`. The
low-expected-count warning reads the scaled table. The proportion
overlays read p̂ = Σw of the successes over Σw of the base, with the
base's n_eff (or Σw under `frequency`) as the sample size, never the
payload's Σw under `probability`. On a weighted host the pairwise
proportion z omits `n_source` to read that sample size. Every
unweighted count (raw rows, `n_within`, the distinct-key modes) is
refused with `PROCESSING_CONFIG`, and `cell_weight_sum` /
`cell_value_weighted` are accepted under `frequency` only. This is a
change for requests that used `cell_weight_sum` over an
`AGG_WEIGHTED_MEAN` cell with `params.weight_field`, which is
`probability`. The Fisher exact cell overlay runs on the Σw table
under `frequency` only. A `probability` weight on its host cell is
refused with `PULSE_WEIGHT_UNSUPPORTED`, even when the overlay itself
carries no weight.

Under `probability` the χ² and Compose proportion overlays read each
host's n_eff from its components. A host built with components
disabled (`disable_components`, `--no-components` or
`Options.DisableComponents`) has none to read, so those overlays are
refused on it with `PROCESSING_CONFIG` naming the host, in predict and
at run time. Before, they read Σw as the sample size without a
warning. Turn components back on for that host, or use a `frequency`
weight, where Σw is the sample size.

## Reading weighted components

A weighted slot's components add three floor keys beside `{n, n_null}`:

- `sum_weights`: Σw over the rows that contributed;
- `n_eff`: Kish effective sample size, (Σw)² / Σw², for `probability`
  weights only;
- `n_weight_invalid`: rows excluded for an invalid weight.

They appear only on weighted slots, so `sum_weights` being absent is
the per-slot sign that a figure is unweighted. `n`, `n_null`,
`CellCounts`, margin counts and grouper `total_n` stay raw counts. For
the effective sample size of a probability-weighted figure, use
`n_eff`, not `n`. A figure that is undefined under the weights (0/0
when every weight in a cell is zero) is written as `null` on the wire.
See [JSON Schema](../contract/payload-schema.md) for both rules.

## SPSS weights

A cohort imported from a `.sav` file with `WEIGHT BY` keeps that
variable in its metadata sidecar. `pulse cohort inspect`,
`pulse_inspect` and `pulse_predict` then report
`suggested_weight {field, source: "spss_sidecar", kind: "probability"}`.
The suggestion is **never applied**: name the field as `weight`
yourself. SPSS `WEIGHT BY` replicates cases, so `kind: "frequency"` is
usually the faithful reading. See
[cohort inspect](../cli/cohort-inspect.md#suggested-weight-spss-cohorts).

## Feature profiles

Row weighting is the feature `capability:weighting`. A profile that
omits it removes the whole weight surface: every `weight` key is
refused as unknown, the schema and manifest drop it, and `pulse.New`
refuses `Options.DefaultWeight` with `PULSE_FEATURE_PROFILE_DEPENDENCY`.
`AGG_WEIGHTED_MEAN`'s own `weight_field` keeps working. See
[Feature Profiles](feature-profiles.md).

## Extension operators

Aggregators, attributes and tests registered through
`pulse.Options.Extensions` declare `WeightAware` and read the row
weight with `extend.Record.Weight()`. Undeclared ones are skipped or
refused (`PULSE_EXTENSION_NOT_WEIGHT_AWARE`). The recipe is in
[Extension Points](../internals/extension-points.md).
