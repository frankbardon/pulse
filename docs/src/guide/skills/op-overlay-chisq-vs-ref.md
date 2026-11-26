```yaml
name: op-overlay-chisq-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_VS_REF
description: Compose-host whole-matrix χ² comparing target slot's matrix against the reference slot's matrix (rescaled to target N).
type: reference
applies_to: compose
examples_tags: [overlay, compose, cross-tabulation, hypothesis-test]
```

Compose-only. Overlays decorate the host; no `Response.Components`.

## Use when

Chi-square test in a Compose request: checks whether a target request's crosstab mix departs from the reference request's mix.

Questions it answers:

- Has the mix of answers in this wave shifted from last wave?
- Does the test market's category mix differ from the control market's?

Use something else:

- `OVERLAY_PROP_Z_CELL` when you want to see which cells moved rather than one overall test.

## Params

`scope` must be `matrix`. `reference` required — reference slot label. `targets` required — one or more target slot labels.


## Host shape

COMPOSE — MATRIX crosstab on reference + target. Slots must match on schema and keys; `options.dict_prefix_fast` enables the dictionary-prefix probe.

## Output

SCALAR — `payload.scalar` carries the p-value (= `summary.p_value`), NOT χ²; χ² is on `summary.statistic`, plus `summary.parameters.df`. One layer per target, in `targets` order: one target keeps `name`; two or more are `<name>__<target>`, each the one-target layer. Over `options.max_panel_targets` (16) → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.

## Reading the output

- `scalar`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `summary.p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `summary.parameters.df`: Degrees of freedom: the number of cells present in both requests with a positive reference count, minus 1.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Goodness of fit: the reference is a FIXED distribution (make it a population or benchmark slot). Between two samples (control vs variant) it ignores the reference's own sampling error and overstates the evidence; to ask whether two subsets differ, run the χ² independence test with the split on one axis ([`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md)).
- Weighted slots (both kinds): cells are Σw; under probability the target table is scaled to its Kish `n_eff` (the reference is the fixed distribution; expected-low on the scaled table) — first-order Kish, not Rao-Scott. `summary.parameters` adds the target's `sum_weights` (+ `n_eff`). A probability TARGET with components disabled ⇒ `PROCESSING_CONFIG` (predict AND runtime).
- Compared cells C = reference count > 0; reference scaled to the target's total over C: `expected = ref_cell × (target_N / ref_N)`, `df = |C| - 1` — R `chisq.test(target[C], p = ref[C], rescale.p = TRUE)`. A target count in a zero-reference cell is dropped from χ², N and `df` + one `PULSE_OVERLAY_REF_ZERO` per cell (the vs-population rule).
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer (canonical χ² low-count rule).
- `target_N == 0`, `ref_N == 0`, or every `expected == 0` → NaN + `PULSE_OVERLAY_REF_ZERO`.
- Dict-prefix drift between slots → `PULSE_OVERLAY_COMPOSE_DICT_DIVERGENCE` (under `dict_prefix_fast`).
- A code cell (`AGG_MODE` over `categorical_*` / `packed_bool`) on either slot → `PROCESSING_CONFIG`, reason `categorical_code`; use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`compose-requests`](compose-requests.md), [`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md), [`op-overlay-prop-z-cell`](op-overlay-prop-z-cell.md).
