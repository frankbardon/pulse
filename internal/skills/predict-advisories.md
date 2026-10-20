---
name: predict-advisories
description: Predict advisories — coded, non-blocking notes in a predict result that the chosen analysis may not fit the data (a two-group test over many groups, many uncorrected p-values, an SPSS nominal or ordinal field treated as a quantity, an unused SPSS weight, Compose p-values split across families, a leave-one-out jackknife over a large cohort, a lasso fit with no valid standard errors); how they differ from warnings, what each code means, what it suggests, and how an embedder suppresses one.
kind: design
type: guide
applies_to: predict
covers: [advisories, PULSE_ADVISORY, analysis fit, suppress advisories]
---

# Predict advisories

Predict answers "will this run" (errors, warnings). An **advisory** answers a different question: "does this analysis fit the data". `pulse_predict` / `pulse api predict --json` carry them in `data.advisories`, one `{code, message, details}` per finding, omitted when none fires.

- **Not a warning.** Advisories ride their own slot, never the envelope `warnings`. `--strict` never turns one into an error and `valid` never changes.
- **Never changes execution.** The request runs exactly as written; acting on an advisory is the caller's choice.
- **Authoritative metadata only:** the request, the schema and its dictionaries, figures predict already computed — no name or value-shape guessing.
- **`details.suggested`** is a replacement operator name or a request patch to merge — never a rewritten request — and only ever names something this instance offers. `pulse_errors_lookup CODE` returns the fixup text.

## Codes

| Code | Fires when | `details` |
|---|---|---|
| `PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS` | a two-sample t-test's `split_by` is a categorical field whose dictionary holds more than two entries (exactly two never fires) | `slot`, `operator`, `field`, `split_by`, `groups`, `suggested` (the many-group ANOVA; absent when none is offered) |
<!-- feature: capability:multiplicity -->
| `PULSE_ADVISORY_MANY_TESTS` | `p_values.uncorrected` ≥ `threshold` (10) and no `multiplicity` block anywhere — request, slot or instance default | `total`, `uncorrected`, `basis`, `threshold`, `suggested: {"multiplicity": {"method": "holm"}}` |
| `PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT` | Compose only: ≥ 2 slots emit p-values, their corrected ones sit in ≥ 2 separate families other than `compose`, and the slots emit ≥ 10 in total — no correction spans the batch | `slots`, `families` (IDs), `total`, `uncorrected`, `basis`, `threshold`, `suggested: {"multiplicity": {"method": M, "family": "compose"}}` |
<!-- /feature -->
<!-- feature: io_format:spss -->
| `PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC` | the SPSS sidecar records a numeric field `nominal` and a mean-family aggregator (average, stddev, variance, moments, CI bounds) or a parametric test (t, z, paired t, ANOVA, Pearson) reads it | `slot`, `operator`, `field`, `measure`, `suggested` (a frequency count or chi-square) |
| `PULSE_ADVISORY_ORDINAL_PARAMETRIC` | the SPSS sidecar records a numeric field `ordinal` and a parametric test reads it | `slot`, `operator`, `field`, `measure`, `suggested` (the rank-based test from the operator's `not_for`; absent when it names none) |
<!-- /feature -->
<!-- feature: capability:weighting -->
| `PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED` | predict echoes `suggested_weight` (an SPSS weight variable, no slot resolving a weight) | `field`, `source`, `suggested: {"weight": {"field": F}}` |
<!-- /feature -->
<!-- feature: ATTR_FORMULA -->
| `PULSE_ADVISORY_EXTRA_PASS` | an `ATTR_FORMULA` calls `total(expr)`, so the run adds a pre-pass over the filtered rows (one per such formula) | `slot`, `operator`, `label`, `totals` (distinct arguments), `added_scans` (1; formulas in one pre-pass layer share it) |
<!-- /feature -->
| `PULSE_ADVISORY_SERIES_AXIS_NOT_DATE` | a series overlay that reads rows in order (prior, rolling, baseline, sibling) runs over ≥ 2 groupers with no `params.partition_by`, and the last grouper (the series axis) is not a date grouper | `slot`, `operator`, `series_axis`, `grouper`, `groups`, `date_axis` (a leading date grouper to move last; absent when none) |
| `PULSE_ADVISORY_JACKKNIFE_COST` | a leave-one-out jackknife (`resample: jackknife`, no `jackknife_groups` / `jackknife_by`) over ≥ `threshold` (10,000) records: cost ∝ n² | `slot`, `operator`, `records`, `threshold`, `suggested: {"jackknife_groups": 50}` |
| `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` | an `l1` / `elasticnet` fit without `resample`: SEs, p, intervals null. Also on the result's `advisories` | `slot`, `operator`, `penalty`, `suggested: {"resample": "bootstrap"}` |
| `PULSE_ADVISORY_CONSTRAINT_ACTIVE` | result only: a least-squares `bounds` bound without `resample`, nulls SEs, p, CIs, `vcov` | `slot`, `operator`, `active`, `suggested` |
| `PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED` | a slot names an embedder extension operator whose registration declares no params, and carries a non-empty `params` object: its keys pass to the operator unchecked, so a misspelled key is not refused | `slot`, `operator`, `keys` |

<!-- feature: capability:multiplicity -->
The many-tests message says "at least N" when `p_values.basis` is `lower_bound` and "an estimated N" when it is `dictionary`; any `multiplicity` block — `method: none` included — silences it.
<!-- /feature -->

The two-group rule reads the dictionary, not the data, so it fires even when a filter leaves two groups.

The three SPSS rules need a current SPSS metadata sidecar; a labelled SPSS numeric (imported as categorical) is not a numeric field.

## Roots

Request predict runs every rule. <!-- feature: capability:facet -->Facet predict runs them too, but a facet request has no test, aggregation or weight slot, so only the many-tests note can fire: one p-value per inferential population-comparison overlay (chi-square or Kolmogorov-Smirnov), counted in its own `p_values`. <!-- /feature --><!-- feature: capability:compose -->Compose predict forwards each slot's advisories in slot order, attributed by `details.request` (the slot index) and a `requests[i].` prefix on `slot`, then the split-families note, then a many-tests note over its own overlays. Its `slots[]` show each slot's `streamable`, `p_values` and `families` (`id` `compose`, or local such as `requests[0]#request`); set `family: "compose"` on each slot's block to pool them. Their overlay `p_values` are `lower_bound` (the extent depends on slot results). <!-- /feature --><!-- feature: capability:process_chain -->Chain predict carries no advisories.<!-- /feature -->

## Suppression

Embedders set `Options.SuppressAdvisories` (codes) for questions their product answers itself; it drops a code from predict and a result's `advisories` alike. `pulse.New` refuses an unknown code with `PULSE_SUPPRESS_ADVISORY_UNKNOWN`. There is no per-request or CLI knob.

## See

`tool-predict`<!-- feature: capability:multiplicity --> · `multiplicity-correction`<!-- /feature --><!-- feature: capability:weighting --> · `weighting`<!-- /feature --><!-- feature: io_format:spss --> · `spss-metadata-sidecar`<!-- /feature --> · `statistical-testing` · `pulse_errors_lookup` for `PULSE_ADVISORY_*`.
