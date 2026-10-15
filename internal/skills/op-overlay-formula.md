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

`Request.Overlays`: a crosstab only (`cell`, `margin_row|col|grand`, `sd_row|col|grand`); a grouped request is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.shapes`), an ungrouped one the same code ("requires a MATRIX host"). Compose: the first target, any shape of the reference — MATRIX adds `ref_cell`; SERIES `value`, `total`, `prior`, `ref_value`; SCALAR `value`, `ref`; plus `slot.<label>.cell|value` for the reference and targets, aligned by key (missing → NaN → null).

## Output

Shape matches host; one `float64` per evaluation. No `Baseline`.

## Gotchas

- MATRIX: An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count`; never evaluated, out of the `sd_*` slices.
- An identifier, function or slot path outside the namespace is `PULSE_OVERLAY_FORMULA_INVALID_IDENT` + allowed set, from predict and the runtime alike (Request and Compose hosts; checked before compiling).
- Syntax error → `PULSE_OVERLAY_FORMULA_PARSE_ERROR`; non-coercible result → `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
- Embedder `ExprFunctions` widen functions, never variables. `lookup(...)` needs a LookupTable.
- Built-ins: scalar `sqrt` / `log` / `exp` / `pow` and the set helpers; the vector functions (`vmean`, `dot`, ...) are `PULSE_OVERLAY_FORMULA_INVALID_IDENT` (`details.allowed_builtins`) on predict and runtime, every host.
- `total(expr)` is `PULSE_FORMULA_TOTAL_SITE` here; use the `total` variable (SERIES).
- Buffered (margins / totals / SDs are post-fold).
<!-- feature: AGG_MODE -->
- MATRIX `Request.Overlays` reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `op-attr-formula`; `docs/src/internals/extension-points.md`.
