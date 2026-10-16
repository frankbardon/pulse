# Computing Survey Weights (Rake)

A survey sample rarely matches its population: too many older
respondents, too few in one region. `Pulse.Rake` computes one weight
per record so the weighted sample reproduces known population margins,
and writes the weights into a NEW cohort beside every source column.
Later requests name the new column as their [row weight](weighting.md).

Weights must be a schema field (a request cannot weight itself with a
column it computes), so computing them is its own step.

## Library

```go
res, err := p.Rake(ctx, pulse.RakeRequest{
    Cohort: "survey.pulse",
    Out:    "survey_weighted.pulse",
    Margins: []pulse.RakeMargin{
        {Field: "age", Targets: map[string]float64{"18-34": 29, "35-54": 33, "55+": 38}},
        {Field: "gender", Targets: map[string]float64{"F": 0.51, "M": 0.49}},
        {Field: "region", Targets: map[string]float64{"NE": 17, "MW": 21, "S": 38, "W": 24}},
    },
})
// res.Converged, res.Iterations, res.Margins[i].Categories[j].Achieved,
// res.Efficiency.NEff / DeffKish; the cohort carries "rake_weight".
```

Then weight any request by it:

```json
{"cohort": {"filename": "survey_weighted.pulse"},
 "weight": {"field": "rake_weight"}, ...}
```

## Request

| Field | Meaning |
|---|---|
| `Margins` | One or more target distributions. `Field` + `Targets` is a rim margin (category → target). `Fields` (two or more) + `Cells` (`{Values, Target}` per category tuple) is a joint margin: cell weighting (post-stratification). Category keys are the stored spelling: the dictionary label, a decimal integer, or `true` / `false` |
| `TargetsAs` (per margin) | `shares` (default; sum to 1, or 100 as percentages) or `counts` (any positive total; only proportions are used) |
| `By` | Rake every group of this field (a tracker's wave) separately to the SAME targets; each group is normalised on its own |
| `BaseWeight` | Start from an existing design weight: the output is that weight times the raking adjustment |
| `Missing` | `refuse` (default) refuses a null margin value; `base` keeps a record's factor for a margin it has no value for |
| `MinWeight` / `MaxWeight` | Caps relative to the group's mean weight, applied after every sweep (0 = no cap); need 0 ≤ min ≤ 1 ≤ max |
| `MaxIter` / `Tol` | Sweep ceiling (default 100) and the largest allowed achieved-vs-target share gap (default 1e-6) |
| `Total` | Each group's weights sum to this; default the group's record count (mean weight 1) |
| `WeightField` / `Overwrite` | Output column name (default `rake_weight`); `Overwrite` replaces an existing target AND an existing column of that name (in place) |

Margin and `By` fields must be categorical, `packed_bool` or unsigned
integers; a base weight must be numeric with no null, negative or
non-finite value. Targets are inline: a saved population file and the
`OVERLAY_RAKE` crosstab overlay are different things.

## How it solves

Iterative proportional fitting over the records. Each sweep rescales,
margin by margin in request order, every category's records so its
weighted share meets its target; then the caps apply. Sweeps stop when
every share is within `Tol` of its target, or after `MaxIter` sweeps —
the cohort is then still written, with a `PULSE_RAKE_NOT_CONVERGED`
warning (binding caps or contradictory targets are the usual causes). A
request with a single margin — rim or joint — is exact after one sweep.
The solver is sequential: the same request always gives the same
weights, whether the source is a single file or a shard archive.

## Result

`RakeResult` carries `Target`, `Records`, `WeightField`, `Iterations`,
`Converged`, `Residual`, and:

- `Margins` — per margin and category: `ObservedN`, `Observed` (the
  share before raking, under the base weight), `Target`, `Achieved`,
  plus `NMissing` (records the margin skipped under `Missing: "base"`).
- `Groups` — one entry per `By` group with its own convergence, margins
  and efficiency (absent without `By`).
- `Efficiency` — `n`, `sum_weights`, `n_eff` (Kish (Σw)²/Σw²),
  `deff_kish` (n / n_eff = 1 + CV²), `efficiency_pct`, `weight_min`,
  `weight_max`, `weight_range_ratio`, `weight_cv` (population SD over
  the mean) and `n_capped`. An undefined figure is NaN in Go and `null`
  in JSON, never 0.

**The design effect is the unequal-weighting (Kish) component only.**
No stratification, clustering or finite-population correction enters
it; complex-sample variance is out of scope.

**The weights are probability weights.** Use them as a request `weight`
with kind `probability` (the default). They are fractional, so kind
`frequency` refuses them.

## Refusals

Nothing is written on any refusal:

| Code | When |
|---|---|
| `PULSE_RAKE_INVALID` | a malformed request; `details.reason` names the rule |
| `PULSE_RAKE_UNKNOWN_CATEGORY` | a target the field cannot hold (not a dictionary entry, not `true`/`false`, not an in-range integer) |
| `PULSE_RAKE_MISSING_VALUE` | a null margin value under `Missing: "refuse"`, or any null `By` value |
| `PULSE_RAKE_UNTARGETED_CATEGORY` | a record's category has no target (add one; 0 gives those records weight 0) |
| `PULSE_RAKE_EMPTY_CELL` | a positive target that no record of its group can carry |
| `SERVICE_VALIDATION` | an existing `Out` without `Overwrite` (checked before any record is read), or an anchored `archive#shard` target |

## Output cohort

Always an ungrouped single file, written atomically: the source's
logical fields verbatim, then the weight as a nullable `f64` with a
generated description. Source sidecars are not carried. An overwrite
that leaves stale sidecars beside the target adds a
`PULSE_RAKE_SIDECARS_INVALIDATED` warning naming them.
