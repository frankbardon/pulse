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

Overlays decorate the host result; do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `cell` (MATRIX) / `group` (SERIES) / `total` (SCALAR). |
| `params.formula` | string | (required) | `expr-lang/expr` expression. |
| `params.baseline_position` | int | optional | SERIES only — opt-in `baseline` variable. |

## Host shape

ANY (MATRIX / SERIES / SCALAR). Per-shape namespace via `types.FormulaNamespace`:

- MATRIX: `cell`, `margin_row|col|grand`, `sd_row|col|grand` (+ `ref_cell` on Compose).
- SERIES: `value`, `total`, `prior` (+ opt-in `baseline`, + `ref_value` Compose).
- SCALAR: `value` (+ `ref` Compose).

## Output

Shape matches host. Each evaluation produces one `float64`. Layer `Baseline` unset.

## Gotchas

- Compile-once / run-many via `expr.Compile` (like `ATTR_FORMULA`).
- Predict-time AST walk validates identifiers; unknown → `PULSE_OVERLAY_FORMULA_INVALID_IDENT` with allowed set.
- Parse error → `PULSE_OVERLAY_FORMULA_PARSE_ERROR`; non-coercible result → `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
- Embedder `ExprFunctions` widen function surface; variables fixed per host shape (custom `OverlayKinds` entry to widen).
- `lookup(...)` NOT in env at v1 — register as `ExprFunctions` if needed.
- Buffered at v1 (margins / totals / SDs need post-fold state).

## See

- Skills: `overlay-system`, `op-attr-formula`, `extension-points`.
