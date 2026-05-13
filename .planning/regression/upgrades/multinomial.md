# multinomial — REG_MULTINOMIAL for k>2 classification

## Motivation
`REG_GLM{Family:"binomial"}` covers binary classification only. Many
real-world problems have three-plus mutually exclusive classes:
product category prediction, customer segment classification, multi-
class diagnosis. Currently blocked: users with `k > 2` targets must
fall back on one-vs-rest decomposition (`k` separate binomial fits)
which loses the multinomial coupling and reports inconsistent class
probabilities.

## Proposed operator / spec shape
Two viable design routes:

**Route A — new operator:**
```go
const REG_MULTINOMIAL RegressionType = "REG_MULTINOMIAL"
// Target points at a categorical_u8/u16/u32 field
// Predictors as usual
// k is inferred from the dictionary
```

**Route B — `Family:"multinomial"` on REG_GLM:**
```go
spec := RegressionSpec{
    Type:   REG_GLM,
    Family: "multinomial",
    Link:   "softmax", // or "logit" for the reference-cell parameterization
    ...
}
```

Route A is cleaner because the response shape diverges sharply from
the binomial case — one coefficient vector per class (excluding the
reference) means the locked `Coefficients map[string]float64` cannot
represent the fit. Route B forces a result-shape extension on the
existing `REG_GLM` path.

Recommend Route A. Extend `RegressionResult` with:
```go
ClassCoefficients map[string]map[string]float64 // class → predictor → β
ReferenceClass    string                         // baseline category
```

## Algorithm sketch
IRLS with the multinomial logit / softmax likelihood. Iteration cost is
`O(n·p·k)` per pass. Standard reference-cell parameterization (one
class fixed at 0). Optional `Family:"ordinal"` extension via the
proportional-odds / cumulative-link model — defer to a v2.

## Streamability
Buffered. Same constraints as `REG_GLM`.

## Error codes
- `PROCESSING_REGRESSION_MULTINOMIAL_INVALID_TARGET` — Target field not
  categorical, or k < 3.
- `PROCESSING_REGRESSION_MULTINOMIAL_SEPARATION` — perfect or quasi-
  separation detected (any class perfectly predicted).
- Reuse `NO_CONVERGE`, `RANK_DEFICIENT`, `INSUFFICIENT_DATA`.

## Update Demand impact
- Registered regression operator row fires (Route A) — adds entry to
  `types.AllRegressionTypes()`, `capabilities_regressions.go`,
  `skills/regression-modeling.md`.
- `RegressionResult` adds `ClassCoefficients`, `ReferenceClass`,
  `ClassNames []string` — additive, additive-only policy says no
  `format_version` bump, but is a contract change worth flagging at
  promotion review.
- `types/streamability.go` entry.

## Dependency cost
None new. gonum's matrix ops cover the IRLS Hessian inversion.

## Estimated phase count
2 — engine + result-shape extension, skill / example / standard errors.

## Open questions
- Reference-class selection: first class in dictionary, lowest-frequency,
  user-specified via a new `ReferenceClass string` field? Recommend
  default to lowest-encoded value with explicit override.
- Should standard errors be the full block-diagonal Hessian inverse
  (k-1 blocks of p × p) or per-class diagonal-only? Latter is wrong
  for hypothesis tests; recommend full inverse, accept the O(k²p²)
  cost.
- Probability output: surface as `ATTR_REG_MULTINOMIAL_PROBA` per-row
  attribute, or only as fitted-class label? Probably both, in a
  separate follow-up phase.
- Does `Resample` / `Selection` apply meaningfully? Selection on
  multinomial coefficients is interpretively fraught (a feature can
  improve one class and hurt another).
