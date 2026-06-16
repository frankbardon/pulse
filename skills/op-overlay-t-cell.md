---
name: op-overlay-t-cell
kind: operator
category: OVERLAY
operator: OVERLAY_T_CELL
description: Compose-host per-cell Welch t-test against the reference slot's matching cell; parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, welch, byte-equal-test]
---

Compose-only parity overlay. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Reference` | string | (required) | Reference slot label. |
| `Targets` | []string | (required) | Target slot labels. |
| `params.variance_target/_ref` | float | `1.0` | Optional override. |
| `params.sample_size_target/_ref` | int | `2` | Optional override. |

## Host shape

COMPOSE — MATRIX crosstab on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` populated by `AGG_WELFORD` via `MetaAggregator`. Falls back to `params`-supplied triple when `CellComponents` absent.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value. One layer per target. Layer `Baseline` unset.

## Gotchas

- **Byte-equal** to `TEST_WELCH` over the same inputs — both read `{n, mean, variance}` via Welford + share `studentTTwoSidedP`. Welch-Satterthwaite df recurrence reused from `TEST_T`.
- Legacy `processing.WelfordTriple` smuggle through `MatrixCell.Value` REMOVED v0.20.0 — `MatrixCell.Value` carries scalar mean.
- Canonical pairing: `AGG_WELFORD` + `OVERLAY_T_CELL`.
- Defaults `var=1.0, n=2` for minimal Compose surface.
- Buffered (inferential family).

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-z-cell`, `op-overlay-t-vs-ref`, `op-agg-welford`.
