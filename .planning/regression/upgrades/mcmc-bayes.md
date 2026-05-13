# mcmc-bayes — REG_BAYES_MCMC operator

## Motivation
Phase 4 shipped `REG_BAYES_LINEAR` with a closed-form conjugate
Normal-Inverse-Gamma posterior. That covers the textbook Bayesian linear
case but rules out non-conjugate priors, hierarchical structure,
non-Gaussian likelihoods, and credible intervals that aren't derivable
in closed form. A `REG_BAYES_MCMC` operator unlocks user-supplied priors
and downstream variants (Bayesian logistic, Bayesian Poisson, weakly-
informative Cauchy priors) without re-deriving conjugacy for each.
Not on Indeed-13, but a frequent ask among Bayesian-leaning analysts;
also a foundation for any future hierarchical / mixed-effects work.

## Proposed operator / spec shape
```go
const REG_BAYES_MCMC RegressionType = "REG_BAYES_MCMC"

// Additions to RegressionSpec (gated by Type == REG_BAYES_MCMC):
Sampler        string  // "nuts" | "hmc" | "metropolis"
Chains         int     // default 4
WarmupIters    int     // default 1000
SamplingIters  int     // default 1000
PosteriorSamples bool  // if true, RegressionResult.PosteriorSamples populated
```

`RegressionResult` already carries `CredibleIntervals`; add an optional
`PosteriorSamples map[string][]float64` (omitempty) for callers who
want raw draws — but only when explicitly requested, to keep the wire
small.

## Algorithm sketch
Two routes:
1. **Vendor a NUTS implementation** (~1500 LOC for a clean NUTS-with-
   dual-averaging port). Pure Go, no cgo, no extra deps. Higher initial
   cost, full control over numerics + reproducibility.
2. **Depend on an external Go MCMC lib** (e.g., `github.com/jingyu/...`
   if a quality option exists). Faster to ship, gives up audit
   surface and pins us to upstream's license/maintenance.

Route 1 is the more defensible choice given Pulse's "no surprises in the
deps tree" posture.

## Streamability
Always buffered. NUTS needs full-dataset log-posterior evaluation each
leapfrog step. Memory `O(n·p + chains·samples·p)`. Multi-chain
parallelism falls out naturally; one goroutine per chain.

## Error codes
- `PROCESSING_REGRESSION_MCMC_DIVERGENT` — divergent transitions exceed
  threshold (NUTS only).
- `PROCESSING_REGRESSION_MCMC_LOW_ESS` — effective sample size below
  configurable floor.
- `PROCESSING_REGRESSION_INVALID_SAMPLER` — `Sampler` not in known set.

## Update Demand impact
- "registered regression operator" row fires →
  `skills/regression-modeling.md` + `descriptor/capabilities_regressions.go`.
- New error codes → `errors/fixup_metadata.go`, manifest error-code list.
- `types/streamability.go` add entry for `REG_BAYES_MCMC`.
- `TestSkillsCoverAllRegressions`, `TestManifestRegressionsComplete`,
  `TestRegressionStreamabilityMatchesTypes`, `TestCodesHaveFixups`.

## Dependency cost
Route 1: none new (we already have gonum). Route 2: one new BSD/MIT
Go MCMC library. Either way, no cgo.

## Estimated phase count
3 — sampler core, integration with the `Regressions` slot, posterior
diagnostics + skill documentation.

## Open questions
- Reproducibility: how strict? `RNGSeed` exists on `RegressionSpec`;
  is fully deterministic chain output a contract or a best-effort?
- Posterior-sample emission: capped at how many samples per coefficient
  to keep the JSON response sane? Default `min(SamplingIters, 1000)`?
- Convergence diagnostics: R-hat, ESS — surface in `RegressionResult`
  or only via warnings?
- Does this strictly require Phase 4's closed-form to coexist, or
  should `REG_BAYES_LINEAR{Prior:"nig"}` eventually delegate to the
  MCMC engine? (Current answer: keep them separate; conjugate path is
  10000× faster.)
