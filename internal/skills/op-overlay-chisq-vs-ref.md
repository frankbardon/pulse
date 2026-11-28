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

`scope` must be `matrix`. `reference`, `targets` (slot labels) required.

| Name | Type | Default | Description |
|---|---|---|---|
| `reference_kind` | enum | `fixed` | `fixed` (goodness of fit) or `sample` (homogeneity); else `PULSE_OVERLAY_PARAM_MISSING`. |

## Host shape

COMPOSE — MATRIX crosstab on reference + target. Slots must match on schema and keys; `options.dict_prefix_fast` enables the dictionary-prefix probe.

## Output

SCALAR — `payload.scalar` carries the p-value (= `summary.p_value`), NOT χ²; χ² is on `summary.statistic`, plus `summary.parameters.df`. One layer per target, in `targets` order: one target keeps `name`; two or more are `<name>__<target>`, each the one-target layer. Over `options.max_panel_targets` (16) → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `fixed`: the reference is a KNOWN distribution (population / benchmark). C = reference count > 0, `expected = ref_cell × target_N / ref_N`, `df = |C| - 1` — R `chisq.test(target[C], p = ref[C], rescale.p = TRUE)`; a target count in a zero-reference cell is dropped + one `PULSE_OVERLAY_REF_ZERO` per cell. Between two samples it overstates the evidence (experiment.pulse variant vs control: χ² 37.3).
- `sample`: the reference is a second sample — the 2 × |C| table R `chisq.test(rbind(target[C], ref[C]))`, C = cells with target + ref > 0, `df = |C| - 1` (same data: χ² 14.6).
- Weights: cells are Σw; under probability a read slot's table is scaled to its Kish `n_eff` (target only under `fixed`, each slot under `sample`) — not Rao-Scott. `summary.parameters` adds `sum_weights` (+ `n_eff`) of the slots read. Such a slot with components disabled ⇒ `PROCESSING_CONFIG`.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW`; an empty side or table → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Dict-prefix drift → `PULSE_OVERLAY_COMPOSE_DICT_DIVERGENCE` (under `dict_prefix_fast`).
<!-- feature: AGG_MODE -->- A code cell (`AGG_MODE` over `categorical_*` / `packed_bool`) on either slot → `PROCESSING_CONFIG`, reason `categorical_code`; use a count cell.<!-- /feature -->

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-chisq-matrix`, `op-overlay-prop-z-cell`.
