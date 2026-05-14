# mixed-effects — REG_LMM linear mixed-effects models

## Motivation
Hierarchical / panel / longitudinal data (patients within clinics,
students within schools, repeated observations per subject) violate the
i.i.d. residuals assumption that `REG_OLS` and `REG_GLM` rely on.
Mixed-effects models add per-group random intercepts and/or slopes,
which is a textbook upgrade analysts routinely need beyond the
Indeed-13. Currently blocked: any user with grouped data has to fall
back on the "ecological" composition (group means → OLS), which loses
within-group information.

## Proposed operator / spec shape
```go
const REG_LMM RegressionType = "REG_LMM"

// Additions to RegressionSpec:
RandomGroup     string   // field name identifying group membership
RandomIntercept bool     // include per-group intercept (default true when
                         // Type == REG_LMM)
RandomSlopes    []string // predictors with per-group random slopes
REMLOrML        string   // "reml" (default) | "ml"
```

Open design choice (see below): whether `RandomGroup` is a field on
`RegressionSpec` or whether an upstream `GROUP_*` slot feeds in — the
latter is more "Pulse-shaped" but conflates GROUP_* semantics
(aggregation grouping) with random-effects grouping.

## Algorithm sketch
Profile-likelihood / REML via repeated penalized weighted least squares
(the lme4 approach: minimize a profiled deviance over the variance-
component parameters with an inner Cholesky solve). Two passes for
sufficient stats are insufficient — iteration needs random access to
the design matrix grouped by `RandomGroup`.

Alternative: EM. Slower but simpler to implement; useful as a reference
correctness check.

## Streamability
Buffered. `O(n·p + Σ_g g·q²)` where `q` is the random-effects design
width per group. The grouped structure must be held in memory; cannot
stream.

## Error codes
- `PROCESSING_REGRESSION_LMM_SINGULAR_VARIANCE` — variance component
  estimated as zero (boundary fit).
- `PROCESSING_REGRESSION_LMM_NO_CONVERGE` (or reuse the existing
  `PROCESSING_REGRESSION_NO_CONVERGE`).
- `PROCESSING_REGRESSION_LMM_INSUFFICIENT_GROUPS` — fewer than 2
  groups (random effects undefined).

## Update Demand impact
- Registered regression operator row fires.
- New error codes → fixup metadata + manifest.
- `RegressionResult` extends with `RandomEffects map[string]map[string]float64`
  (group → coefficient → BLUP). Wire shape change is additive — locked
  shape at Phase 0 didn't anticipate this; consider adding the field
  preemptively at promotion time and documenting that older operators
  leave it nil.

## Dependency cost
None new. gonum's Cholesky + symmetric eigensolvers suffice.

## Estimated phase count
3 — engine, REML/ML toggle + variance components, BLUPs + post-fit
attributes (per-group residuals).

## Open questions
- **Spec vs. composition for grouping:** option A puts `RandomGroup` on
  `RegressionSpec`; option B requires an upstream `GROUP_CATEGORY` slot
  feeding into the regression. Option A is cleaner because LMM grouping
  is semantically different from aggregation grouping (it is a model
  feature, not a partition).
- Crossed vs. nested random effects in v1? Recommend nested-only for
  v1; crossed effects are materially harder.
- Generalized LMM (GLMM) as a separate `REG_GLMM` or a `Family` value
  on `REG_LMM`? Defer past v1.
- Do we report random-effect variance components in the standard
  `Coefficients` map, or carve out a separate `VarianceComponents`
  map to avoid confusion with fixed effects?
