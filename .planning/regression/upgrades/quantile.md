# quantile — REG_QUANTILE regression

## Motivation
OLS estimates the conditional mean. Quantile regression estimates the
conditional median, P10, P90, or any τ ∈ (0,1) — the right tool when
the response is skewed, the user cares about the tails, or
heteroscedasticity makes the mean uninformative. Currently blocked:
"what drives the worst-case latency / highest-spend customers?" can't
be answered with Pulse's existing regression family. Common ask in
econometrics, ops/SRE analytics, healthcare cost modeling.

## Proposed operator / spec shape
```go
const REG_QUANTILE RegressionType = "REG_QUANTILE"

// Additions to RegressionSpec:
Quantiles []float64 // requested τ values, e.g. [0.1, 0.5, 0.9]
                    // each emits its own coefficient vector
Solver    string    // "smoothed" (default) | "lp" (interior-point)
Bandwidth float64   // smoothing parameter for smoothed check-loss
```

`RegressionResult` extension: when `len(Quantiles) > 1`, the existing
`Coefficients` map can't represent the result. Two options:
1. One `RegressionResult` per quantile (engine emits multiple results
   for one spec). Cleanest; matches how `Resample` works.
2. Add a `QuantileCoefficients map[float64]map[string]float64` field.

Option 1 is preferred — keeps the result shape stable.

## Algorithm sketch
Two viable methods:
- **Smoothed check-loss** with a differentiable approximation
  (Horowitz-style smoothing): converges with standard Newton, easier to
  implement, slightly biased at small samples.
- **Linear programming** via the dual simplex / interior-point method:
  exact but heavier and pulls in LP machinery gonum doesn't ship by
  default.

Recommend smoothed check-loss for v1; LP as a `Solver: "lp"` extension
later if accuracy demands it.

## Streamability
Buffered. The check-loss objective is non-additive in a way that
streaming sufficient stats cannot capture; the optimizer needs full
access to residuals each iteration.

## Error codes
- `PROCESSING_REGRESSION_QUANTILE_INVALID_TAU` — τ ∉ (0,1).
- `PROCESSING_REGRESSION_QUANTILE_INSUFFICIENT_DATA` — sample too small
  for requested τ (e.g. τ = 0.99 with n = 50).
- Reuse `NO_CONVERGE`, `RANK_DEFICIENT`.

## Update Demand impact
- Registered regression operator row fires.
- `types/streamability.go` entry: always buffered.
- New error codes → fixup metadata + manifest.
- Skill update: `skills/regression-modeling.md` adds quantile section.

## Dependency cost
Smoothed check-loss: none (gonum). LP route: would need a Go LP solver
(e.g., gonum's `optimize` package — limited LP support) or a vendored
revised simplex. Recommend punting LP until smoothed-loss is shipped.

## Estimated phase count
2 — engine + smoothed solver, multi-quantile result emission + skill /
example.

## Open questions
- Standard errors: bootstrap (slow), sandwich (controversial),
  asymptotic (requires density estimate at τ)? Recommend bootstrap as
  default and surface the choice as a spec field.
- How does this interact with `Resample` / `Selection` modifiers? Some
  combinations are nonsensical (bootstrapped quantile regression with
  τ near 0 or 1 explodes); document the constraint.
- Should `Quantiles` default to `[0.5]` (median) when empty, or error?
  Recommend default to `[0.5]` — the median case is the most common.
