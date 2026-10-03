---
name: op-overlay-pairwise-weighted-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on AGG_WEIGHTED_MEAN cells under a required n_basis (weights | kish).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

Weighted sibling of the pairwise two-means z: pairs rows (`row` scope) or columns (`column`) of one crosstab on weighted means. Reads `Response.Components`.

## Params

`Scope` (enum, required) — `row` or `column`. `Ref` empty. `params.n_basis` (enum, REQUIRED, no default) — `weights` or `kish`. `params.pair_along_dim` (int) — same-bucket pairs. `n_source` / `p_source` / bad `n_basis` → `PULSE_OVERLAY_PARAM_MISSING`.

## Host shape

MATRIX crosstab whose cell aggregator is `AGG_WEIGHTED_MEAN`; reads `weighted_mean`, `m2_weighted`, `sum_weights`, `sum_weights_sq` — never floor `n`. Else `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

Pair × opposite-axis two-sided p-values. `weights`: `var = m2/(Σw−1)`, `n = Σw`. `kish`: `var = m2/(Σw−Σw²/Σw)`, `n = n_eff`. `z = (m_i−m_j)/sqrt(var_i/n_i + var_j/n_j)`, `p = 2Φ(−|z|)`.

## Gotchas

- Skips (aggregated `PULSE_OVERLAY_REF_ZERO`): `weights` leg `Σw ≤ 1`; `kish` leg with one weighted row; zero SE.
- Mergeable cell: the scan stays fused.

## See

- Skills: `op-overlay-pairwise-two-means-z`, `op-agg-weighted-mean`, `overlay-system`.
