# gamma-family-fixtures — Gamma GLM numerical fixtures

## Motivation
Phase 3 wired the `Family:"gamma"` / `Link:"inverse"` combination through
`processing/regression/glm_families.go` and added a smoke test
(`TestRegGLM_GammaInverse_Smoke`) that accepts non-convergence as a skip.
What is missing is a numerical fixture: a curated `(X, y)` corpus with
known-good coefficients, deviance, and pseudo-R² validated against R's
`glm(family=Gamma(link="inverse"))` or statsmodels' `GLM` so future
refactors cannot silently regress the gamma path. Users hitting an
insurance-claim or rainfall use case currently land on a wired-but-unverified
operator. The smallest item in the candidate list — likely a single-PR
follow-up, not a full phase.

## Proposed operator / spec shape
No spec change. The work is purely fixture + test:

```go
// processing/regression/testdata/glm_gamma_inverse.golden.json
// processing/regression/glm_gamma_test.go
func TestRegGLM_GammaInverse_Fixture(t *testing.T) { ... }
```

Replace the existing smoke skip with an equality check (within `1e-6`
relative tolerance) against the golden.

## Algorithm sketch
Already implemented. The fixture work is: pick a small (n≈50, p≈3)
dataset with strictly-positive `y`, fit it in R/statsmodels offline,
commit the dataset + the reference coefficients, residual deviance,
null deviance, and convergence iteration count.

## Streamability
Unchanged — gamma GLM is buffered like the rest of `REG_GLM`.

## Error codes
None new. Convergence failures continue to map to
`PROCESSING_REGRESSION_NO_CONVERGE`.

## Update Demand impact
None — no new operator, no new error code, no manifest delta. The
existing `skills/regression-modeling.md` already mentions the gamma
family. May want to add a fixture-presence test to the regression
package's CI gates, but it is not Update-Demand-table material.

## Dependency cost
Zero. Reference values come from an external statistical package run
offline; only the JSON fixture lands in the repo.

## Estimated phase count
0.5 — runs as a follow-up PR, not a phase. No review checkpoint
required.

## Open questions
- Reference implementation: R `glm()` or statsmodels? Pick one and
  document the call so the fixture can be regenerated.
- Should the same PR add gamma+log and gamma+identity fixtures? The
  current code marks both as "reserved"; deferring is fine but worth
  flagging.
- Tolerance: `1e-6` vs `1e-8`? IRLS converges to different fixed points
  depending on starting μ; pick the looser bound and document it.
