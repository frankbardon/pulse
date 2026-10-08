```yaml
name: op-overlay-formula
kind: operator
category: OVERLAY
operator: OVERLAY_FORMULA
description: Expression-driven projection computed via expr-lang against a per-host-shape variable namespace.
type: reference
applies_to: process, compose
examples_tags: [overlay, feature-engineering]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Computes a custom figure for every cell, group or total from an expression over the value and its margins, totals or prior point.

Questions it answers:

- Can I show each cell as its gap from the row margin divided by the grand total?
- Can I flag each month whose value is more than 10% above the prior month?

Use something else:

- `ATTR_FORMULA` when you need a derived field on every row before aggregation.

## Params

`Scope` required — `cell` (MATRIX) / `group` (SERIES) / `total` (SCALAR). `params.formula` required — an `expr-lang/expr` expression. `params.baseline_position` (int, optional) — SERIES only, opts in the `baseline` variable.

## Host shape

ANY shape. Per-shape namespace via `types.FormulaNamespace`:

- MATRIX: `cell`, `margin_row|col|grand`, `sd_row|col|grand` (+ `ref_cell` on Compose).
- SERIES: `value`, `total`, `prior` (+ opt-in `baseline`, `ref_value` on Compose).
- SCALAR: `value` (+ `ref` on Compose).

## Output

Shape matches host; each evaluation yields one `float64`. Layer `Baseline` unset.

## Gotchas

- Compile-once / run-many via `expr.Compile`.
- Predict-time AST walk validates identifiers; unknown → `PULSE_OVERLAY_FORMULA_INVALID_IDENT` + allowed set.
- Parse error → `PULSE_OVERLAY_FORMULA_PARSE_ERROR`; non-coercible result → `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
- Embedder `ExprFunctions` widen the function surface; variables are fixed per host shape (widen via a custom `OverlayKinds` entry). `lookup(...)` is NOT in the v1 env.
- Buffered at v1 (margins / totals / SDs need post-fold state).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-attr-formula`](op-attr-formula.md); `docs/src/internals/extension-points.md`.
