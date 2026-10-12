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

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` — `cell` (MATRIX) / `group` (SERIES) / `total` (SCALAR). `params.formula` required — an `expr-lang/expr` expression.

## Host shape

`Request.Overlays`: a crosstab only (`cell`, `margin_row|col|grand`, `sd_row|col|grand`); a grouped request is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.shapes`). Compose: the first target, any shape of the reference — MATRIX adds `ref_cell`; SERIES `value`, `total`, `prior`, `ref_value`; SCALAR `value`, `ref`; plus `slot.<label>.cell|value` for the reference and targets, aligned by key (missing → NaN → null).

## Output

Shape matches host; one `float64` per evaluation. No `Baseline`.

## Gotchas

- Predict refuses an identifier, function or slot path outside the namespace — `PULSE_OVERLAY_FORMULA_INVALID_IDENT` + allowed set; a Compose host refuses it identically at runtime.
- Parse error → `PULSE_OVERLAY_FORMULA_PARSE_ERROR`; non-coercible result → `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
- Embedder `ExprFunctions` widen functions, never variables. `lookup(...)` needs a LookupTable.
- Built-ins: scalar `sqrt` / `log` / `exp` / `pow` and the set helpers; the vector functions (`vmean`, `dot`, ...) are `PULSE_OVERLAY_FORMULA_INVALID_IDENT` on predict (request and Compose hosts) and runtime.
- `total(expr)` is `PULSE_FORMULA_TOTAL_SITE` here; use the `total` variable (SERIES).
- Buffered (margins / totals / SDs are post-fold).

## See

- Skills: `overlay-system`, `op-attr-formula`; `docs/src/internals/extension-points.md`.
