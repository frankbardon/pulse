---
name: op-overlay-index-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_MARGIN
description: Per-cell index against the matching axis margin (100 × cell / margin).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, comparison]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `cell`; any other scope → `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (predict and runtime). `Ref.Margin.Axis` (enum, required) — `row` / `column` / `grand`. `Level`/`Within` optional (default `0`): nested-axis prefix truncation of the margin denominator, each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Any other `Ref` arm beside `Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Ratio sibling of the margin delta and the margin z-score. Foundational kind establishing the share-margin pattern.

## Output

MATRIX. `Cells[r][c].Value = 100 × cell / margin`. Mirrors host RowKeys / ColumnKeys. Absent host cells stay absent. Layer `Baseline = 100` (ratio family centerpoint).

<!-- generated: contract -->

## Gotchas

- MATRIX: An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of any Level / Within bucket sum.
- `margin == 0` → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected slice (not per cell).
- All three axes supported. `Axis = grand` is the grand-total share × 100.
- Empty `Ref.Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Distinct from `OVERLAY_SHARE_OF_*` (raw ratio, no ×100). Kind names kept distinct — don't authoring-confuse.
- Weighted host → weighted figure (reads the host payload).
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-row`, `op-overlay-delta-vs-margin`.
