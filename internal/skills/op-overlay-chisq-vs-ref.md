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

Compose-only. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `matrix`. `Reference` required — reference slot label. `Targets` required — one target slot label.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched (`multiplicity-correction`).
<!-- /feature -->

## Host shape

COMPOSE — MATRIX crosstab on reference + target. Schema-match + key-alignment gates at the slot barrier; `OverlayOptions.DictPrefixFast` enables the byte-equal dictionary prefix probe.

## Output

SCALAR — `Payload.Scalar` carries the p-value (= `Summary.PValue`), NOT χ²; χ² is on `Summary.Statistic`, plus `Parameters["df"]`. One layer per target.

<!-- generated: reading-the-output -->

## Gotchas

- Weighted slots (both kinds): cells are Σw; under probability the target table is scaled to its Kish `n_eff` (the reference is the fixed distribution; expected-low on the scaled table) — first-order Kish, not Rao-Scott. `Summary.Parameters` adds the target's `sum_weights` (+ `n_eff`). A probability TARGET with components disabled ⇒ `PROCESSING_CONFIG` (predict AND runtime).
- Reference scaled to target N: `expected = ref_cell × (target_N / ref_N)`. Reuses `chiSquareSurvival` — byte-equal to the χ² test on the same contingency.
- `df = (target cells with expected > 0) - 1`.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer (canonical χ² low-count rule).
- `target_N == 0`, `ref_N == 0`, or every `expected == 0` → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Dict-prefix drift between slots → `PULSE_OVERLAY_COMPOSE_DICT_DIVERGENCE` (under `DictPrefixFast`).
- Buffered (inferential; the Compose slot barrier always buffers).

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-chisq-matrix`, `op-overlay-prop-z-cell`.
