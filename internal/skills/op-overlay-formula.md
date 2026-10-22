---
name: op-overlay-formula
kind: operator
category: OVERLAY
operator: OVERLAY_FORMULA
description: Expression-driven projection computed via expr-lang against a per-host-shape variable namespace.
type: reference
applies_to: process, compose
examples_tags: [overlay, feature-engineering]
---

An `expr-lang/expr` expression evaluated per host coordinate. The variables, functions and refusal codes on each host: `overlay-formula-namespace`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | SERIES host: the OUTPUT LABEL read as `value` (label, else TYPE_field) or one element column `"<label>[<element label>]"`; absent, the first aggregation. |
| `partition_by` | list | — | Over two or more groupers, the leading group key columns each partition shares (default every key but the last; [] = one walk over every row). |
| `formula` | string | (required) | The expression; absent, empty or not a string → `PULSE_OVERLAY_PARAM_MISSING`, unparsable → `PULSE_OVERLAY_FORMULA_PARSE_ERROR`. |
| `baseline_position` | int | — | SERIES host: the 0-based row of each partition `baseline` reads. |

`Scope` — `cell` (MATRIX) / `group` (SERIES) / `total` (SCALAR).

## Host shape

`Request.Overlays` on a crosstab (`cell`, `margin_*`, `sd_*`) or a grouped Process (`value`, `total`, `prior`, `baseline`; empty `Ref`; per partition over N ≥ 2 groups); Compose on the first target against a reference of any shape (`ref_cell` / `ref_value` / `ref`, `slot.<label>.*`). Ungrouped → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Output

Shape matches host; one `float64` per evaluation. No `Baseline`.

<!-- generated: contract -->

## Gotchas

- A NaN MATRIX host cell is MISSING: never evaluated, the cell stays absent, no warning.
- A name outside the namespace is `PULSE_OVERLAY_FORMULA_INVALID_IDENT` (+ allowed set), predict and runtime; a non-numeric result is `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
- No vector functions; `total(expr)` is `PULSE_FORMULA_TOTAL_SITE` here (use the `total` variable). Embedder `ExprFunctions` widen functions, never variables.
- Margins, totals and SDs are post-fold, hence buffered.
<!-- feature: AGG_MODE -->
- MATRIX `Request.Overlays` reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-formula-namespace`, `overlay-system`, `op-attr-formula`; `docs/src/internals/extension-points.md`.
