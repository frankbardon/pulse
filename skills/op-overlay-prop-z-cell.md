---
name: op-overlay-prop-z-cell
kind: operator
category: OVERLAY
operator: OVERLAY_PROP_Z_CELL
description: Compose-host per-cell two-proportion z-test against the reference slot's matching cell.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, proportion-analysis]
---

Compose-only. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Reference` | string | (required) | Reference slot label. |
| `Targets` | []string | (required) | Target slot labels. |

## Host shape

COMPOSE — MATRIX crosstab on both reference + target slot. Cell value = success count; matching row margin = sample size. Sample reference reuses pooled-SE recurrence backing `TEST_PROP_Z`.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value as `float64`. Mirrors reference matrix's RowKeys / ColumnKeys. One layer per target. Layer `Baseline` unset (inferential).

## Gotchas

- Pooled SE: `sqrt(pooled × (1-pooled) × (1/n_target + 1/n_ref))` where `pooled = (target+ref) / (n_target+n_ref)`. Reuses `standardNormalCDF` — byte-equal to `TEST_PROP_Z` on the same `(success, n)` pair.
- Degenerate inputs (`pooled ∈ {0,1}`, missing row margin, `se == 0`) → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` warning per affected cell.
- Schema-match (E7-S7) + key-alignment (E7-S6) gates at the slot barrier.
- Buffered (inferential family).

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-prop-z-panel`, `op-test-prop-z`.
