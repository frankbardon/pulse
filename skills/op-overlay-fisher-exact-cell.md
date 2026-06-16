---
name: op-overlay-fisher-exact-cell
kind: operator
category: OVERLAY
operator: OVERLAY_FISHER_EXACT_CELL
description: Per-cell Fisher's exact two-sided p-value over a 2×2 contingency formed from each host crosstab cell + its margins.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, exact-test, small-sample]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Ref` | object | (empty) | Implicit-margin — leave empty. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab. Implicit-margin family (no `Ref`). Canonical low-count χ² backstop — the correct surface when `expected < 5` breaks χ² approximation. Closes the E2 inferential family.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value as `float64`. Mirrors host RowKeys / ColumnKeys. Absent host cells stay absent on the overlay. Layer `Baseline` unset (inferential).

## Gotchas

- 2×2 contingency: `[cell, row_margin-cell; col_margin-cell, grand-row_margin-col_margin+cell]`. Reuses `logHypergeometric` — byte-equal to `TEST_FISHER_EXACT` on the same 2×2.
- Cochran rule: any of four expected counts `< 1` OR `>= 20%` of expected `< 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` warning per offending cell. Advisory only — Fisher stays exact.
- `grand_total <= 0` → absent cells everywhere + ONE `PULSE_OVERLAY_REF_ZERO`.
- Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-matrix`, `op-test-fisher-exact`.
