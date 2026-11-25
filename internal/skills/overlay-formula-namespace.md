---
name: overlay-formula-namespace
description: What a formula overlay's expression can read on each host — the crosstab cell and margin variables, the grouped-series row variables and baseline position, the Compose slot paths — plus the functions it may call and every refusal code.
type: guide
kind: design
applies_to: process, compose, predict
covers: [OVERLAY, formula, namespace, baseline_position, ref_cell, ref_value, slot]
requires: [OVERLAY_FORMULA]
---

# Formula overlay namespace

`OVERLAY_FORMULA` evaluates `params.formula`, an `expr-lang/expr` expression, once per host coordinate and emits one `float64` per evaluation, the host's shape. This file is what the expression can name; params, output and the overlay's own gotchas: `op-overlay-formula`.

## Variables by host

| Host | `Scope` | Variables |
|---|---|---|
<!-- feature: capability:crosstab -->
| crosstab (`Request.Overlays`, MATRIX) | `cell` | `cell`, `margin_row` / `margin_col` / `margin_grand`, `sd_row` / `sd_col` / `sd_grand` |
<!-- /feature -->
| grouped Process (SERIES, empty `Ref`) | `group` | per row `value` (`params.field`, else `aggregations[0]`), `total` (the partition's sum), `prior` (previous present row; `null` on the first), `baseline` (only with `params.baseline_position`) |
<!-- feature: capability:compose -->
| Compose, MATRIX | `cell` | the crosstab variables + `ref_cell` |
<!-- /feature -->
<!-- feature: capability:compose -->
| Compose, SERIES | `group` | `value`, `total`, `prior`, `ref_value` |
<!-- /feature -->
<!-- feature: capability:compose -->
| Compose, SCALAR | `total` | `value`, `ref` |
<!-- /feature -->

<!-- feature: capability:compose -->On Compose the first target is scored against the reference of any shape, and `slot.<label>.cell` / `slot.<label>.value` read the reference and each target, aligned by key (missing → NaN → `null`). <!-- /feature -->Over N ≥ 2 groups a SERIES host<!-- feature: capability:compose --> — Process or Compose (the first target's groups) —<!-- /feature --> runs per partition (`params.partition_by`)<!-- feature: capability:compose -->; Compose `params.field` picks each slot's scored aggregation<!-- /feature -->. An ungrouped request is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## `baseline_position` (SERIES)

The 0-based row of each partition that `baseline` reads. Not a non-negative integer → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`; past the partition → `PULSE_OVERLAY_REF_UNKNOWN`. Predict raises the second too when every in-partition grouper is a category grouper over a categorical field (dictionary sizes bound the length); a data-dependent axis (range, date, quantile) is runtime-only.

## Missing cells (MATRIX)

An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: never evaluated, the overlay cell stays absent, no warning, out of `summary.count` and out of the `sd_*` slices.

## Functions

- Built-ins: scalar `sqrt` / `log` / `exp` / `pow` and the set helpers. The vector and trig functions (`vmean`, `dot`, `sin`, `pi()`, …) are `PULSE_OVERLAY_FORMULA_INVALID_IDENT` (`details.allowed_builtins`), predict and runtime, every host.
- Embedder `ExprFunctions` widen functions, never variables. `lookup(...)` needs a LookupTable.
- `total(expr)` is `PULSE_FORMULA_TOTAL_SITE` here; use the `total` variable (SERIES).

## Refusals

| Fault | Code |
|---|---|
| `formula` absent, empty or not a string | `PULSE_OVERLAY_PARAM_MISSING` (`details.param: "formula"`, `reason`), every host, predict and runtime |
| syntax error | `PULSE_OVERLAY_FORMULA_PARSE_ERROR` |
| identifier, function or slot path outside the namespace | `PULSE_OVERLAY_FORMULA_INVALID_IDENT` + the allowed set, predict and runtime alike (Request and Compose hosts), checked before compiling |
| result not coercible to a number | `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH` |

## See

`op-overlay-formula` · `overlay-system` · `expression-language` · `op-attr-formula` · `compose-requests`.
