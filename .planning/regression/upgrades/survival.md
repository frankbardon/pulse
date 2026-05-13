# survival — REG_COX (and possibly REG_AFT)

## Motivation
Time-to-event analysis (patient survival, churn, equipment failure) is
the single most-requested regression family Pulse doesn't cover.
Currently blocked: any user with right-censored observations has no
way to model the censoring information; running OLS on observed
durations and ignoring censoring biases coefficients toward zero.
Not on the Indeed-13 but widely demanded; lifts Pulse from
"general-purpose tabular" to "viable for biostats / reliability /
churn-modeling" workloads.

## Proposed operator / spec shape
```go
const REG_COX RegressionType = "REG_COX"
const REG_AFT RegressionType = "REG_AFT"  // optional v2

// Additions to RegressionSpec:
Duration       string // field name carrying observed time-to-event or censoring
EventIndicator string // field name carrying 1 = event observed, 0 = censored
                     // Replaces Target for survival operators.
TiesMethod     string // "breslow" | "efron" | "exact" (Cox; default "efron")
Baseline       bool   // emit Breslow baseline hazard in result (Cox)
```

Survival's "Y" is the pair (Duration, EventIndicator), not a single
field. We model that as two RegressionSpec fields rather than inventing
a paired-field type, because field-type proliferation has a higher cost
than adding one struct field.

## Algorithm sketch
Cox PH: maximize the partial likelihood via Newton-Raphson — same IRLS-
like structure as GLM, but the gradient/Hessian iterate over risk sets
ordered by descending event time. Efron's tie correction is the
sensible default; Breslow is faster and less accurate; exact is
combinatorial and rarely needed at Pulse-scale.

AFT (accelerated failure time): a parametric alternative with a
chosen distribution (Weibull / log-normal / log-logistic). Effectively
a GLM with the appropriate link; could reuse most of the IRLS
machinery.

## Streamability
Buffered. Cox needs the risk set at every event time, which requires
sorting by Duration and revisiting subjects who are still at risk.
`O(n·p)` memory.

## Error codes
- `PROCESSING_REGRESSION_SURVIVAL_NO_EVENTS` — every row censored.
- `PROCESSING_REGRESSION_SURVIVAL_INVALID_DURATION` — non-positive
  duration.
- `PROCESSING_REGRESSION_SURVIVAL_INVALID_INDICATOR` — indicator not in
  `{0, 1}`.
- Reuse `NO_CONVERGE`, `RANK_DEFICIENT`, `INSUFFICIENT_DATA`.

## Update Demand impact
- Registered regression operator row fires twice (REG_COX, REG_AFT).
- `RegressionResult` extension: optional `BaselineHazard [][2]float64`
  (time, cumulative hazard) when `Baseline: true`. New field on locked
  struct — needs explicit promotion-time approval.
- New error codes → fixup metadata + manifest.

## Dependency cost
None new. gonum suffices.

## Estimated phase count
3 — Cox engine + tie methods, AFT engine, post-fit diagnostics
(Schoenfeld residuals, baseline hazard, possibly time-varying
coefficients).

## Open questions
- Time-varying covariates / counting-process input format in v1? Lean:
  no, defer to v2.
- Stratified Cox (multiple baseline hazards) in v1? Lean: yes, via a
  `Stratify string` field — cheap once the risk-set machinery exists.
- AFT in the same phase or separate phase? Recommend separate; AFT
  shares less infrastructure than it looks like.
- Should `EventIndicator` accept a `packed_bool` field type, or require
  `u8` / `u16`? Recommend: accept both; coerce at engine boundary.
