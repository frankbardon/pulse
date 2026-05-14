# robust-se — HC and CR standard errors

## Motivation
Pulse currently reports OLS standard errors under the homoscedastic
assumption (`Var(β̂) = σ²(XᵀX)⁻¹`). When residual variance is
non-constant in the predictors (a near-universal condition in real
data), classical SEs are biased and the resulting p-values lie.
Heteroscedasticity-consistent SEs (HC0–HC3, "White" / "Eicker–Huber–
White") and cluster-robust SEs (CR0–CR2) fix this without changing
the point estimates. Currently blocked: econometrics-grade workflows
that need defensible inference (regression-discontinuity, diff-in-diff,
panel data) can't use Pulse's reported p-values.

## Proposed operator / spec shape
No new operator. Postprocess hook on existing fits via a new
`RegressionSpec` field:

```go
// In RegressionSpec:
RobustSE string `json:"robust_se,omitempty"`
    // ""   -- classical (default)
    // "hc0" .. "hc3" -- heteroscedasticity-consistent
    // "cr1" | "cr2"  -- cluster-robust
ClusterField string `json:"cluster_field,omitempty"`
    // required when RobustSE is "cr*"; field whose values define
    // clusters for the cluster-robust variance estimator
```

`RegressionResult.StdErrors` becomes the robust version when set; add
a `StdErrorMethod string` field on the result so callers can confirm
which estimator was used.

## Algorithm sketch
After the point-estimate fit converges:
1. Compute residuals `e_i = y_i − ŷ_i`.
2. For HC variants:
   - `HC0`: `(XᵀX)⁻¹ Σ_i (e_i² xᵢxᵢᵀ) (XᵀX)⁻¹`.
   - `HC1`: HC0 × `n / (n − p)`.
   - `HC2`: divide each `e_i²` by `1 − h_ii` (leverage).
   - `HC3`: divide by `(1 − h_ii)²`.
3. For CR variants, sum the inner kernel over clusters rather than
   rows; apply standard small-sample corrections.

For GLM, replace `xᵢxᵢᵀ` with the score-contribution outer products.

## Streamability
Postprocess: streaming if the parent fit streams AND the leverage
vector (`h_ii`) is available streaming (HC2 / HC3 only). HC0 / HC1 can
stream by accumulating the sandwich kernel directly. CR variants are
buffered: clusters' membership must be known before the kernel sum.

## Error codes
- `PROCESSING_REGRESSION_ROBUST_SE_INVALID_METHOD` — unknown `RobustSE`
  value.
- `PROCESSING_REGRESSION_ROBUST_SE_NO_CLUSTER_FIELD` — `cr*` without
  `ClusterField`.
- `PROCESSING_REGRESSION_ROBUST_SE_CLUSTER_FIELD_INVALID` — cluster
  field is float/continuous.
- `PROCESSING_REGRESSION_ROBUST_SE_TOO_FEW_CLUSTERS` — fewer clusters
  than predictors.

## Update Demand impact
- Not a new operator. "regression modifier" row fires (effectively).
- `skills/regression-modeling.md` adds a robust-inference section.
- New error codes → fixup metadata + manifest.
- `RegressionResult` adds `StdErrorMethod string` — additive contract
  change.

## Dependency cost
None.

## Estimated phase count
2 — HC variants first, then CR variants (separate phase because CR
requires the additional `ClusterField` plumbing and small-sample
correction logic).

## Open questions
- Default when `RobustSE: ""`: classical. But should we surface a
  warning when a Breusch-Pagan-style heteroscedasticity test would
  reject? Recommend no — too opinionated.
- p-values from robust SE: still t-based with `n − p` df, or Z-based?
  Convention varies; recommend t for HC, t with `G − 1` df for CR
  (G = number of clusters).
- Does `RobustSE` interact with `Resample`? Bootstrap variance is
  itself a form of robust SE; document that combining them is
  unusual and not recommended.
