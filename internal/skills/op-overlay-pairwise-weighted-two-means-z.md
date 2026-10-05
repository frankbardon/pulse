---
name: op-overlay-pairwise-weighted-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on weighted-mean cells under a required n_basis (weights | kish).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

Weighted pairwise two-means z: pairs rows (`row` scope) or columns (`column`) on weighted means. Reads `Response.Components`.

## Params

`Scope` (required) — `row` or `column`. `Ref` empty. `params.n_basis` (REQUIRED, no default) — `weights` or `kish`. `params.pair_along_dim` (int) — same-bucket pairs. `n_source` / `p_source` / bad `n_basis` → `PULSE_OVERLAY_PARAM_MISSING`.

## Host shape

MATRIX crosstab, weighted-moment cell:<!-- feature: AGG_WEIGHTED_MEAN --> `AGG_WEIGHTED_MEAN`<!-- /feature --><!-- feature: AGG_AVERAGE, capability:weighting --> or a weighted `AGG_AVERAGE`<!-- /feature -->. Reads `weighted_mean`, `m2_weighted`, `sum_weights`, `sum_weights_sq` — never floor `n`. Else `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

Pair × opposite-axis two-sided p-values. `weights`: `var = m2/(Σw−1)`, `n = Σw`. `kish`: `var = m2/(Σw−Σw²/Σw)`, `n = n_eff`. `z = (m_i−m_j)/sqrt(var_i/n_i + var_j/n_j)`, `p = 2Φ(−|z|)`.

## Gotchas

- Skips (`PULSE_OVERLAY_REF_ZERO`): `weights` leg `Σw ≤ 1`; `kish` leg with one weighted row; zero SE.
- Mergeable cell: stays fused. Weight `kind` (both accepted) does not set n here: `n_basis` does, for THIS kind only — other weighted overlays read N* off the host's kind. Probability weights: use `kish`.

## See

- Skills: `op-overlay-pairwise-two-means-z`, `op-agg-weighted-mean`, `overlay-system`.
