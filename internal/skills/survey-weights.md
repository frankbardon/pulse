---
name: survey-weights
description: Computing survey weights — Pulse.Rake rim weighting (raking) and cell weighting (post-stratification) to inline census targets, per-wave raking with `by`, base weights, caps, convergence, the efficiency report (Kish n_eff / deff) and the new cohort it writes.
type: guide
kind: design
applies_to: process, compose
covers: [RakeRequest, RakeMargin, RakeCell, RakeResult, RakeEfficiency, rake_weight]
requires: [capability:rake]
---

# Computing survey weights

A sample rarely matches its population. `Pulse.Rake` computes a weight per record so the weighted sample reproduces known margins (census shares), and writes a NEW cohort: every source column verbatim plus a nullable `f64` weight column (default `rake_weight`). Later requests name that column as their `weight` (see `weighting`). Weights are always a schema field, so computing them is its own step.

## Request

```json
{"cohort": "survey.pulse", "out": "survey_weighted.pulse",
 "margins": [
   {"field": "age", "targets": {"18-34": 29, "35-54": 33, "55+": 38}},
   {"field": "gender", "targets": {"F": 0.51, "M": 0.49}},
   {"fields": ["region", "urban"], "cells": [{"values": ["NE", "true"], "target": 0.12}, ...]}],
 "by": "wave", "base_weight": "design_wt",
 "min_weight": 0.3, "max_weight": 3, "max_iter": 100, "tol": 1e-6}
```

| Key | Meaning |
|---|---|
| `margins[].field` + `targets` | a rim margin: category → share. Keys are the stored spelling (dictionary label, decimal integer, `true` / `false`) |
| `margins[].fields` + `cells` | a JOINT margin of 2+ fields — cell weighting. One cell per category tuple |
| `targets_as` | `shares` (default; must sum to 1, or 100 as percentages) or `counts` (any total; proportions used) |
| `by` | rake each group (e.g. wave) separately to the SAME targets; each group normalised on its own |
| `base_weight` | start from a design weight: output = base × adjustment |
| `missing` | `refuse` (default) or `base`: a record lacking a margin value keeps its factor for that margin |
| `min_weight` / `max_weight` | caps relative to the group's MEAN weight (0 = none); need min ≤ 1 ≤ max |
| `total` | weights sum to this per group; default the record count (mean weight 1) |
| `weight_field`, `overwrite` | name the column; `overwrite` replaces an existing target and an existing column of that name |

Margin and `by` fields must be categorical, `packed_bool` or unsigned integers. Targets come inline — never from a population file (that holds a saved vector fit, not shares).<!-- feature: OVERLAY_RAKE --> Not `OVERLAY_RAKE` either: it rakes a crosstab table and writes nothing.<!-- /feature -->

## How it solves

Iterative proportional fitting over the records: each sweep rescales every margin's categories in request order to their target share, then applies the caps. It stops when the largest |achieved − target| share gap ≤ `tol`, or after `max_iter` sweeps — then the cohort is STILL written, with warning `PULSE_RAKE_NOT_CONVERGED` (binding caps or contradictory targets). One margin (rim or joint) is exact after one sweep. Deterministic: the same request gives the same bits.

## Result

`RakeResult`: `target`, `records`, `weight_field`, `iterations`, `converged`, `residual`; `margins[]` with per category `observed_n`, `observed` (share before raking, under the base weight), `target`, `achieved`, plus `n_missing`; `groups[]` per `by` group with the same fields; `efficiency`:

| Key | Meaning |
|---|---|
| `n`, `sum_weights` | records, Σw |
| `n_eff` | Kish effective n (Σw)²/Σw² |
| `deff_kish` | n / n_eff = 1 + `weight_cv`² |
| `efficiency_pct` | 100 · n_eff / n |
| `weight_min`, `weight_max`, `weight_range_ratio` | extremes and max / min |
| `weight_cv` | population SD / mean of the weights |
| `n_capped` | records held at a cap |

An undefined figure (a range ratio with a zero weight) is `null`, never 0.

**`deff_kish` is the unequal-weighting design effect ONLY.** No strata, clusters or finite-population correction enter it; complex-sample variance is out of scope.

## Using the weights

Raked weights are PROBABILITY weights: `{"weight": {"field": "rake_weight"}}` (kind `probability`, the default). Shares, means and the census margins come out right; totals scale with `total`. Never declare them `frequency` (fractional weights are invalid there).

## Refusals

Nothing is written on any refusal: `PULSE_RAKE_INVALID` (`details.reason`), `PULSE_RAKE_UNKNOWN_CATEGORY` (a target the field cannot hold), `PULSE_RAKE_MISSING_VALUE` (null margin value under `refuse`, any null `by`), `PULSE_RAKE_UNTARGETED_CATEGORY` (a record's category has no target — add it, 0 zeroes those records), `PULSE_RAKE_EMPTY_CELL` (a positive target no record of the group can carry — collapse categories). An existing `out` without `overwrite` is `SERVICE_VALIDATION`. Source: a single file, a shard archive or an anchored shard; the output is one single-file cohort. Source sidecars are not carried.

## See

`weighting` · `response-components` · `pulse_errors_lookup` for `PULSE_RAKE_*`.
