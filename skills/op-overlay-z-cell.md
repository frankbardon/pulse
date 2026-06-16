---
name: op-overlay-z-cell
kind: operator
category: OVERLAY
operator: OVERLAY_Z_CELL
description: Compose-host per-cell two-sample z-test on the means against the reference slot's matching cell; parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, z, byte-equal-test]
---

Compose-only parity overlay. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Reference` | string | (required) | Reference slot label. |
| `Targets` | []string | (required) | Target slot labels. |
| `params.variance_target/_ref` | float | `1.0` | Optional. |
| `params.sample_size_target/_ref` | int | `2` | Optional. |

## Host shape

COMPOSE — MATRIX crosstab on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` populated by `AGG_WELFORD` via `MetaAggregator` (E3-S7/S8). Falls back to `params`-supplied triple when absent.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value via standard normal survival. One layer per target. Layer `Baseline` unset.

## Gotchas

- **Byte-equal** to `TEST_Z_TWO_SAMPLE` over the same inputs — both share `standardNormalCDF`.
- Distinct from `OVERLAY_T_CELL` only by distribution (normal vs Student's t) — same SE: `sqrt(var_t/n_t + var_r/n_r)`.
- Legacy `processing.WelfordTriple` smuggle through `MatrixCell.Value` REMOVED v0.20.0 — `MatrixCell.Value` carries scalar mean.
- Defaults `var=1.0, n=2` keep handler usable against minimal Compose surface.
- Buffered (inferential family).

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-t-cell`, `op-overlay-z-vs-ref`, `op-agg-welford`.
