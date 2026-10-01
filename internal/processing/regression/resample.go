package regression

import (
	"math"
	"math/rand/v2"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// defaultBootstrapIters is the replicate count when the spec leaves
// BootstrapIters at zero. 1000 is the standard textbook minimum for a
// non-parametric percentile interval; production users who care about
// the deep tails should raise it.
const defaultBootstrapIters = 1000

// runResample drives the Resample modifier. The base fit (β at every
// coefficient) is taken from baseResult; the modifier replaces only the
// uncertainty quantities (StdErrors, PValues) and echoes the resample
// kind. baseResult.Coefficients is preserved unchanged — the resample
// is around the point estimate, not a replacement for it.
//
// The driver dispatches on spec.Resample:
//
//   - "jackknife" : leave-one-out refit, jackknife SE formula.
//   - "bootstrap" : B = max(BootstrapIters, defaultBootstrapIters)
//     replicates with replacement; SE is the sample std
//     of the replicate coefficient set, p-values from the
//     fraction of replicates that cross zero (percentile-
//     style two-sided test).
//
// fitter is the closure that refits the inner engine on a record
// subset. The OLS and GLM paths supply different closures so each
// family stays aligned with its own solver and warning surface.
//
// The Resample, BootstrapIters, and RNGSeed fields are echoed on the
// returned result so consumers can identify the uncertainty source.
//
// On any blocking refit error the driver short-circuits and returns
// the error; partial coefficient sets are never folded back into a
// result.
func runResample(
	spec *types.RegressionSpec,
	records []Record,
	predictors []string,
	baseResult *types.RegressionResult,
	fitter func([]Record) (*refitResult, error),
	pValueFn func(coef, se float64) float64,
) (*types.RegressionResult, error) {
	switch spec.Resample {
	case "jackknife":
		return runJackknife(spec, records, predictors, baseResult, fitter, pValueFn)
	case "bootstrap":
		return runBootstrap(spec, records, predictors, baseResult, fitter, pValueFn)
	default:
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"unknown Resample value: "+spec.Resample+" (expected jackknife | bootstrap)",
			map[string]any{"resample": spec.Resample},
		)
	}
}

// runJackknife implements leave-one-out resampling. For each i ∈ [0, n)
// we refit on all rows except i, collect the β replicate, then compute
//
//	SE_jack[j] = sqrt((n − 1) / n · Σᵢ (β_(i)[j] − β̄[j])²)
//
// where β̄ is the mean of the replicates. The point estimate stays at
// the full-data fit (baseResult.Coefficients). p-values use the inner
// engine's pValueFn applied to (β_full, SE_jack); for OLS that's a
// Student's-t with df = n − 1, for GLM the Wald-z form.
//
// Naive implementation: n refits, O(n · p²) memory at most for the
// replicate matrix. The Sherman-Morrison rank-1 downdate optimization
// is acknowledged in the phase doc but deferred.
//
// Listwise null deletion happens inside fitter; the driver loops over
// the raw record slice and trusts each refit to skip null rows. As a
// result the effective replicate count can be lower than len(records)
// when the target or some predictor has many nulls, but every retained
// replicate still contributes to the SE sum.
func runJackknife(
	spec *types.RegressionSpec,
	records []Record,
	predictors []string,
	baseResult *types.RegressionResult,
	fitter func([]Record) (*refitResult, error),
	pValueFn func(coef, se float64) float64,
) (*types.RegressionResult, error) {
	n := len(records)
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"jackknife resampling requires at least 3 observations",
			map[string]any{"n": n},
		)
	}
	p := len(predictors)
	// Replicate β matrix: rows are the n leave-one-out fits, columns are
	// (intercept, slope_1, …, slope_p).
	replicates := make([][]float64, 0, n)
	leaveOneOut := make([]Record, 0, n-1)
	for i := 0; i < n; i++ {
		leaveOneOut = leaveOneOut[:0]
		leaveOneOut = append(leaveOneOut, records[:i]...)
		leaveOneOut = append(leaveOneOut, records[i+1:]...)
		fit, err := fitter(leaveOneOut)
		if err != nil {
			return nil, err
		}
		row := make([]float64, p+1)
		row[0] = fit.Intercept
		for j := 0; j < p; j++ {
			if j < len(fit.Slopes) {
				row[j+1] = fit.Slopes[j]
			}
		}
		replicates = append(replicates, row)
	}
	if len(replicates) < 2 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"jackknife produced fewer than 2 replicates after listwise null deletion",
			map[string]any{"n": n, "replicates": len(replicates)},
		)
	}

	// Mean of replicates.
	mean := make([]float64, p+1)
	for _, r := range replicates {
		for j := 0; j < p+1; j++ {
			mean[j] += r[j]
		}
	}
	for j := 0; j < p+1; j++ {
		mean[j] /= float64(len(replicates))
	}
	// Jackknife SE.
	se := make([]float64, p+1)
	for _, r := range replicates {
		for j := 0; j < p+1; j++ {
			d := r[j] - mean[j]
			se[j] += d * d
		}
	}
	scale := float64(len(replicates)-1) / float64(len(replicates))
	for j := 0; j < p+1; j++ {
		se[j] = math.Sqrt(scale * se[j])
	}

	return packageResampleResult(spec, baseResult, predictors, se, pValueFn, len(replicates), nil), nil
}

// runBootstrap implements non-parametric bootstrap resampling. For each
// b ∈ [0, B) we sample n indices with replacement from [0, n), refit
// the inner engine on the sampled subset, and collect the β replicate.
//
// SE_boot[j] = sample standard deviation of β_(b)[j] across b.
//
// p-values come from the bootstrap percentile p̂[j] = (# replicates
// with sign(β_(b)[j]) ≠ sign(β̂[j])) / B, two-sided so:
//
//	p[j] = 2 · min(p̂[j], 1 − p̂[j])
//
// (equivalently, the fraction of replicates crossing zero, mirrored).
//
// B defaults to defaultBootstrapIters (1000) when spec.BootstrapIters
// is zero or negative. RNG seeding: spec.RNGSeed == 0 → time-derived
// seed; non-zero seeds produce reproducible runs (TestRegOLS_Bootstrap_
// Reproducible exercises this).
//
// We use math/rand/v2 with rand.NewPCG, available in Go 1.22+. Each
// replicate uses n integer draws from the same generator instance.
func runBootstrap(
	spec *types.RegressionSpec,
	records []Record,
	predictors []string,
	baseResult *types.RegressionResult,
	fitter func([]Record) (*refitResult, error),
	pValueFn func(coef, se float64) float64,
) (*types.RegressionResult, error) {
	n := len(records)
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"bootstrap resampling requires at least 3 observations",
			map[string]any{"n": n},
		)
	}
	B := spec.BootstrapIters
	if B < defaultBootstrapIters {
		B = defaultBootstrapIters
	}
	seed := spec.RNGSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9E3779B97F4A7C15))

	p := len(predictors)
	replicates := make([][]float64, 0, B)
	sample := make([]Record, n)
	for b := 0; b < B; b++ {
		for i := 0; i < n; i++ {
			sample[i] = records[rng.IntN(n)]
		}
		fit, err := fitter(sample)
		if err != nil {
			// A single failed replicate (e.g. rank deficiency on an
			// unlucky resample) shouldn't kill the whole bootstrap. Skip
			// and continue; we cap the consecutive-failure rate below to
			// surface pathological cases.
			if len(replicates) == 0 && b > 50 {
				// 50 consecutive failures at the start: almost certainly a
				// systematic problem. Surface the underlying error.
				return nil, err
			}
			continue
		}
		row := make([]float64, p+1)
		row[0] = fit.Intercept
		for j := 0; j < p; j++ {
			if j < len(fit.Slopes) {
				row[j+1] = fit.Slopes[j]
			}
		}
		replicates = append(replicates, row)
	}
	if len(replicates) < 10 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_NO_CONVERGE,
			"bootstrap produced fewer than 10 successful replicates; underlying refit is unstable on this data",
			map[string]any{"successful": len(replicates), "requested": B},
		)
	}

	// Sample mean.
	bm := make([]float64, p+1)
	for _, r := range replicates {
		for j := 0; j < p+1; j++ {
			bm[j] += r[j]
		}
	}
	for j := 0; j < p+1; j++ {
		bm[j] /= float64(len(replicates))
	}
	// Sample SD (unbiased, n − 1 divisor).
	se := make([]float64, p+1)
	for _, r := range replicates {
		for j := 0; j < p+1; j++ {
			d := r[j] - bm[j]
			se[j] += d * d
		}
	}
	for j := 0; j < p+1; j++ {
		se[j] = math.Sqrt(se[j] / float64(len(replicates)-1))
	}

	// Percentile p-values: fraction crossing zero (relative to the
	// base point estimate's sign). Indexed by [intercept, slope_1, …,
	// slope_p].
	baseCoefs := make([]float64, p+1)
	baseCoefs[0] = baseResult.Coefficients[InterceptKey]
	for j := 0; j < p; j++ {
		baseCoefs[j+1] = baseResult.Coefficients[predictors[j]]
	}
	bootP := make([]float64, p+1)
	for j := 0; j < p+1; j++ {
		below := 0
		for _, r := range replicates {
			if r[j] < 0 {
				below++
			}
		}
		pHat := float64(below) / float64(len(replicates))
		// Two-sided percentile p-value.
		pTwoSided := 2 * math.Min(pHat, 1-pHat)
		// If the base estimate is non-zero but the bootstrap mass straddles
		// zero, pTwoSided is finite. Apply a small floor so a p ≈ 0 result
		// (every replicate on the same side of zero) becomes 1 / B rather
		// than literal zero, mirroring the standard percentile-CI convention.
		if pTwoSided == 0 {
			pTwoSided = 1.0 / float64(len(replicates))
		}
		bootP[j] = pTwoSided
	}

	return packageResampleResult(spec, baseResult, predictors, se, pValueFn, len(replicates), bootP), nil
}

// packageResampleResult writes the resample-derived SE / p-values onto
// a copy of baseResult, preserving the point estimate (Coefficients)
// and every non-uncertainty quantity (R², deviance, NObs, …).
//
// When bootstrapP is non-nil it is used directly (one entry per
// [intercept, slope_1, …]); when nil the analytical pValueFn is
// applied to (β, SE) instead — this is the jackknife path which uses
// the t-distribution / Wald-z form.
func packageResampleResult(
	spec *types.RegressionSpec,
	baseResult *types.RegressionResult,
	predictors []string,
	se []float64,
	pValueFn func(coef, se float64) float64,
	replicates int,
	bootstrapP []float64,
) *types.RegressionResult {
	// Shallow copy of baseResult, then replace the uncertainty maps.
	out := *baseResult
	seMap := make(map[string]float64, len(predictors)+1)
	pMap := make(map[string]float64, len(predictors)+1)

	interceptCoef := baseResult.Coefficients[InterceptKey]
	seMap[InterceptKey] = se[0]
	if bootstrapP != nil {
		pMap[InterceptKey] = bootstrapP[0]
	} else {
		pMap[InterceptKey] = pValueFn(interceptCoef, se[0])
	}
	for j, name := range predictors {
		coef := baseResult.Coefficients[name]
		seMap[name] = se[j+1]
		if bootstrapP != nil {
			pMap[name] = bootstrapP[j+1]
		} else {
			pMap[name] = pValueFn(coef, se[j+1])
		}
	}
	out.StdErrors = seMap
	out.PValues = pMap
	out.Resample = spec.Resample
	_ = replicates // future: surface as RegressionResult field; not in v1
	return &out
}
