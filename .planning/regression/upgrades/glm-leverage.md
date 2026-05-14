# glm-leverage — extend ATTR_REG_LEVERAGE to GLMs

## Motivation
Phase 7 shipped `ATTR_REG_LEVERAGE` for OLS only — the diagonal of the
hat matrix `H = X(XᵀX)⁻¹Xᵀ`. For `REG_GLM` (logistic, Poisson, gamma),
leverage is the weighted-hat-matrix diagonal `H_W = W^{1/2}X(XᵀWX)⁻¹XᵀW^{1/2}`
using the IRLS final-iteration weights `W`. Currently blocked: any
diagnostic plot or influence analysis for a logistic regression
(Cook's distance, DFBETAS, deletion residuals) needs leverage as its
foundation; without it, GLM diagnostics in Pulse stop at residuals.

## Proposed operator / spec shape
No new operator. Extend the existing `ATTR_REG_LEVERAGE` attribute:

```go
// ATTR_REG_LEVERAGE today errors when the underlying regression is GLM:
//
//   if spec.Family != "" {
//       return nil, errors.New("ATTR_REG_LEVERAGE: GLM not supported")
//   }
//
// Replace with the weighted-hat path when the attribute's RegressionSpec
// is GLM-shaped. Reuse the IRLS weight vector from the GLM engine.
```

The attribute spec on `AttributeSpec` already mirrors `RegressionSpec`
(Option A from Phase 7). All the fields needed (`Family`, `Link`,
`MaxIters`, etc.) are present.

## Algorithm sketch
1. Run IRLS to convergence (same as `REG_GLM`).
2. At convergence, compute the working weights
   `W_ii = (∂μ/∂η)² / V(μ)` for each row.
3. Compute the QR or Cholesky of `XᵀWX`.
4. For each row `i`, return `h_ii = w_i · xᵢᵀ (XᵀWX)⁻¹ xᵢ`.

Two passes total (same as the GLM fit itself, plus a final pass to
emit `h_ii`). For OLS the existing code path is preserved.

## Streamability
Buffered (same as the GLM fit it depends on). The hat-matrix diagonal
also can be emitted streaming after the GLM finalizes, similar to how
fitted values stream out of `ATTR_REG_FITTED`.

## Error codes
- Reuse `PROCESSING_REGRESSION_RANK_DEFICIENT` if `XᵀWX` is singular.
- Reuse `PROCESSING_REGRESSION_NO_CONVERGE` if the inner IRLS fails.
- New: `PROCESSING_REGRESSION_LEVERAGE_INVALID_FAMILY` — if a Family
  this code path doesn't yet support is requested (e.g., gamma until
  fixtures exist).

## Update Demand impact
- Not adding a new operator; not adding a regression type.
- `skills/attribute-composition.md` and `skills/regression-modeling.md`
  need a section update describing GLM leverage availability.
- One new error code, possibly. Fixup metadata + manifest.
- No streamability table change at the type level — already buffered.

## Dependency cost
None. Reuses gonum Cholesky.

## Estimated phase count
1 — single focused phase. Mostly engine work; skill update is small.

## Open questions
- Does Phase 7's "no refit if a sibling regression slot already fit
  this spec" optimization apply to GLM-leverage? Closely related to
  the `attr-from-reference.md` upgrade; the two are natural siblings
  and might land in the same phase.
- Should we also ship `ATTR_REG_DEVIANCE_RESIDUAL` (deviance residuals,
  the GLM analog of standardized residuals) as part of the same phase?
  Recommend yes — same machinery, same diagnostic context.
- Gamma family leverage: blocked until gamma fixtures land
  (`gamma-family-fixtures.md`). Bake that dependency in.
