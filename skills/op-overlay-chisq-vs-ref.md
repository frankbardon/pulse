---
name: op-overlay-chisq-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_VS_REF
description: Compose-host whole-matrix χ² comparing target slot's matrix against the reference slot's matrix (rescaled to target N).
type: reference
applies_to: compose
examples_tags: [overlay, compose, cross-tabulation, hypothesis-test]
---

Compose-only. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `matrix`. |
| `Reference` | string | (required) | Reference slot label. |
| `Targets` | []string | (required) | One target slot label. |

## Host shape

COMPOSE — MATRIX crosstab on both reference + target slot. Schema-match + key-alignment gates apply at the slot barrier. `OverlayOptions.DictPrefixFast` enables byte-equal dictionary prefix probe.

## Output

SCALAR — `Payload.Scalar` carries χ²; `OverlaySummary{Statistic, PValue, Parameters["df"]}`. One layer per target.

## Gotchas

- Reference distribution scaled to target N: `expected = ref_cell × (target_N / ref_N)`. Reuses `chiSquareSurvival` — byte-equal to `TEST_CHISQ` on the same contingency.
- `df = (target cells with expected > 0) - 1`.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` warning per layer (canonical χ² low-count rule).
- `target_N == 0`, `ref_N == 0`, or every `expected == 0` → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Dict-prefix drift between slots → `PULSE_OVERLAY_COMPOSE_DICT_DIVERGENCE` (when `DictPrefixFast` enabled).
- Buffered (inferential family; Compose slot barrier always buffered by construction).

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-chisq-matrix`, `op-overlay-prop-z-cell`.
