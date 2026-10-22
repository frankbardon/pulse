---
name: op-overlay-pairwise-weighted-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on weighted-mean cells under a required n_basis (weights | kish).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

Weighted pairwise two-means z: pairs rows (`row` scope) or columns (`column`) on weighted means. Reads `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` (required) — `row` or `column`. `Ref` empty. `params.n_basis` (REQUIRED, no default) — `weights` or `kish`. `params.pair_along_dim` (int) — same-bucket pairs. `n_source` / `p_source` / bad `n_basis` → `PULSE_OVERLAY_PARAM_MISSING`; `weights` on a probability-weighted cell (incl. `weight_field`) → `PROCESSING_CONFIG`. `params.letters` (object, unset) — `{}` or `{"alpha": a}`, `0 < a < 1` (else `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime): adds the `letters` part.


## Host shape

MATRIX crosstab, weighted-moment cell:<!-- feature: AGG_WEIGHTED_MEAN --> `AGG_WEIGHTED_MEAN`<!-- /feature --><!-- feature: AGG_AVERAGE, capability:weighting --> or a weighted `AGG_AVERAGE`<!-- /feature -->. Reads `weighted_mean`, `m2_weighted`, `sum_weights`, `sum_weights_sq` — never floor `n`. Else `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED` (predict and runtime, after the `n_basis` / selector checks).

## Output

Pair × opposite-axis two-sided p-values. `weights`: `var = m2/(Σw−1)`, `n = Σw`. `kish`: `var = m2/(Σw−Σw²/Σw)`, `n = n_eff`. `z = (m_i−m_j)/sqrt(var_i/n_i + var_j/n_j)`, `p = 2Φ(−|z|)`. `payload.parts.letters` (with `params.letters`): per host cell of a compared index `{letter, group, higher, above}` — the letters its weighted mean beats (sign of `z`) and the significant subset (raw p < `0.05` or `alpha`<!-- feature: capability:multiplicity -->; `significant_adjusted` under a correction<!-- /feature -->).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Skips (`PULSE_OVERLAY_REF_ZERO`): `weights` leg `Σw ≤ 1`; `kish` leg with one weighted row; zero SE.
- Mergeable cell: stays fused. `n_basis` sets n: `weights` needs frequency weights (Σw is no sample size under probability); probability → `kish`.

## See

- Skills: `op-overlay-pairwise-two-means-z`, `op-agg-weighted-mean`, `overlay-system`.
