```yaml
name: op-overlay-share-of-row
kind: operator
category: OVERLAY
operator: OVERLAY_SHARE_OF_ROW
description: Per-cell share-of-row ratio (cell / row_margin) — raw share, sums to 1.0 per row.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, proportion-analysis]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each crosstab cell as a share of its row margin (0 to 1): the mix of columns within each row, as a 100% stacked bar shows it.

Questions it answers:

- Within each region, what share of orders goes to each channel?
- For each age band, how are answers split across the options?

Use something else:

- `OVERLAY_SHARE_OF_COL` when you want the mix within each column.

## Params

`Scope` must be `cell`. `Ref.Margin.Axis` (enum, required) — must be `row`; any other axis → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime). `Level`/`Within` must be `0`. Any other `Ref` arm beside `Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: explicit-margin (`Ref.Margin`). Structural twin of the column and grand-total shares. Compatible with any cell aggregator.

## Output

MATRIX — `Cells[r][c].Value = cell / row_margin` (raw ratio, no ×100). Cells along a single row sum to 1.0 in the absence of missing cells. Renderers present as 100%-stacked horizontal projection. Layer `Baseline = 1`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- A share, not an index (×100): the kind names are kept distinct so `share` is never read as `index/100`.
- `row_margin == 0` → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected row.
- An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of any Level / Within bucket sum.
- Scope MUST be `cell`. Empty `Ref.Margin` or non-row Axis → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Weighted host → weighted figure (reads the host payload).
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-share-of-col`](op-overlay-share-of-col.md), [`op-overlay-share-of-total`](op-overlay-share-of-total.md).
