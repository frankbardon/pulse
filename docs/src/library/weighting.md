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
| the moment tests — t (one- and two-sample), Welch t, paired t, two-sample z, one-way ANOVA F, Welch ANOVA, Pearson r | weighted (both kinds) | weighted |
| other tests, regressions, reference-distribution attributes (z-score, t-score, percentile rank), the quantile grouper, confidence-interval bounds, inferential overlays | `PULSE_WEIGHT_UNSUPPORTED` | `PULSE_WEIGHT_UNSUPPORTED` |

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
with no setting needed. An inferential overlay refuses when a weight
reaches the overlay itself: its own `weight`, the request's, or the
default. Its `"weight": null` opts it out. A host weighted only by its
own slot weight does not trigger the refusal.
`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` is the exception, built for
weighted cells: its host is an `AGG_WEIGHTED_MEAN` cell or a weighted
`AGG_AVERAGE` cell.

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
