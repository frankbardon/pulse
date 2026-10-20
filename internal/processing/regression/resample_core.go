package regression

import (
	"context"
	"math"
	"math/rand/v2"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// resample_core.go holds the resample drivers' replicate loops over a
// coefficient VECTOR in a fixed key order, so one loop serves REG_OLS /
// REG_GLM — whose replicate is (intercept, slope_1, …, slope_p) — and an
// extension REG_* model, whose replicate is its reported coefficients in
// the base fit's order (RunExtensionModifiers). Each loop reads ctx
// before every refit: a request deadline (Options.Limits.RequestTimeout)
// or a cancel stops it between two refits with the ctx's own error.

// replicateFn refits one resample replicate over a record subset and
// returns its coefficient vector in the driver's key order.
type replicateFn func([]Record) ([]float64, error)

// refitReplicate adapts a built-in refit closure to a replicateFn over
// p active predictors: (intercept, slope_1, …, slope_p), a slope the
// refit did not return reading 0.
func refitReplicate(p int, fit func([]Record) (*refitResult, error)) replicateFn {
	return func(recs []Record) ([]float64, error) {
		r, err := fit(recs)
		if err != nil {
			return nil, err
		}
		return refitRow(p, r), nil
	}
}

// refitRow is a built-in refit's replicate vector.
func refitRow(p int, fit *refitResult) []float64 {
	row := make([]float64, p+1)
	row[0] = fit.Intercept
	for j := 0; j < p; j++ {
		if j < len(fit.Slopes) {
			row[j+1] = fit.Slopes[j]
		}
	}
	return row
}

// ctxErr is ctx.Err() for a possibly-nil ctx.
func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// replicateMean is the column mean of the replicate vectors.
func replicateMean(replicates [][]float64, q int) []float64 {
	mean := make([]float64, q)
	for _, r := range replicates {
		for j := 0; j < q; j++ {
			mean[j] += r[j]
		}
	}
	for j := 0; j < q; j++ {
		mean[j] /= float64(len(replicates))
	}
	return mean
}

// diagSqrt is the square root of cov's diagonal — the resample SE.
func diagSqrt(cov [][]float64) []float64 {
	se := make([]float64, len(cov))
	for j := range cov {
		se[j] = math.Sqrt(cov[j][j])
	}
	return se
}

// jackknifeCore is the leave-one-out jackknife over the fitted rows
// (jackknifeRows): n refits, each without one row, and the SE /
// covariance of the q-coefficient replicates,
//
//	SE_jack[j] = sqrt((n − 1) / n · Σᵢ (β_(i)[j] − β̄[j])²).
//
// A failed refit fails the whole jackknife with its own error.
func jackknifeCore(ctx context.Context, spec *types.RegressionSpec, rows []Record, q int, rep replicateFn) ([]float64, [][]float64, error) {
	n := len(rows)
	if n < 3 {
		return nil, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"jackknife resampling requires at least 3 observations",
			map[string]any{"n": n},
		)
	}
	replicates := make([][]float64, 0, n)
	leaveOneOut := make([]Record, 0, n-1)
	for i := 0; i < n; i++ {
		if err := ctxErr(ctx); err != nil {
			return nil, nil, err
		}
		leaveOneOut = leaveOneOut[:0]
		leaveOneOut = append(leaveOneOut, rows[:i]...)
		leaveOneOut = append(leaveOneOut, rows[i+1:]...)
		row, err := rep(leaveOneOut)
		if err != nil {
			return nil, nil, err
		}
		replicates = append(replicates, row)
	}
	if len(replicates) < 2 {
		return nil, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"jackknife produced fewer than 2 replicates after listwise null deletion",
			map[string]any{"n": n, "replicates": len(replicates)},
		)
	}
	mean := replicateMean(replicates, q)
	// Jackknife covariance (n − 1)/n · Σᵢ (β_(i) − β̄)(β_(i) − β̄)ᵀ; the
	// SE is the square root of its diagonal, so √diag(Vcov) == SE
	// exactly. The off-diagonal is only formed when Vcov is asked for.
	scale := float64(len(replicates)-1) / float64(len(replicates))
	cov := replicateCov(replicates, mean, spec.Vcov, func(s float64) float64 { return scale * s })
	return diagSqrt(cov), cov, nil
}

// groupedJackknifeCore is the delete-a-group jackknife (JK1) over the
// fitted rows (runGroupedJackknife documents the groups and the failure
// policy): g refits, each without one group. records and predictors are
// the slot's record set and active predictors, read only by the seed of
// a jackknife_groups assignment at rng_seed 0 (deriveBootstrapSeed).
// Returns the SE, the covariance and g.
func groupedJackknifeCore(ctx context.Context, spec *types.RegressionSpec, records []Record, predictors []string, rows []Record, q int, rep replicateFn) ([]float64, [][]float64, int, error) {
	m := len(rows)
	var groupOf []int
	var labels []string
	g := 0
	if spec.JackknifeBy != "" {
		groupOf, labels = clusterAssignment(spec.JackknifeBy, rows)
		g = len(labels)
		if g < 2 {
			return nil, nil, 0, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
				"jackknife_by needs at least 2 distinct values among the fitted rows to form delete groups",
				map[string]any{"jackknife_by": spec.JackknifeBy, "groups": g, "n": m},
			)
		}
	} else {
		g = spec.JackknifeGroups
		if g > m {
			return nil, nil, 0, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
				"jackknife_groups exceeds the fitted row count: every delete group needs at least one row",
				map[string]any{"jackknife_groups": g, "n": m},
			)
		}
		seed := spec.RNGSeed
		if seed == 0 {
			seed = deriveBootstrapSeed(spec, records, predictors)
		}
		groupOf = jackknifeAssignment(m, g, seed)
	}

	replicates := make([][]float64, 0, g)
	subset := make([]Record, 0, m)
	for k := 0; k < g; k++ {
		if err := ctxErr(ctx); err != nil {
			return nil, nil, 0, err
		}
		subset = subset[:0]
		for i, rec := range rows {
			if groupOf[i] != k {
				subset = append(subset, rec)
			}
		}
		row, err := rep(subset)
		if err != nil {
			return nil, nil, 0, jackknifeReplicateError(err, spec, k, labels)
		}
		replicates = append(replicates, row)
	}
	mean := replicateMean(replicates, q)
	scale := float64(g-1) / float64(g)
	cov := replicateCov(replicates, mean, spec.Vcov, func(s float64) float64 { return scale * s })
	return diagSqrt(cov), cov, g, nil
}

// bootstrapRun is a bootstrap's replicate statistics: the SE and
// covariance of the successful replicates, the two-sided percentile
// p-value of every coefficient, and the requested / successful
// replicate counts.
type bootstrapRun struct {
	se, p                []float64
	cov                  [][]float64
	requested, succeeded int
}

// bootstrapCore is the non-parametric bootstrap over the fitted rows
// (runBootstrap documents B, the seed, the percentile p-value and the
// skip policy). predictors seed the rng_seed 0 derivation; q is the
// coefficient count. fatal, when non-nil,
// reports a replicate error that must stop the bootstrap instead of
// being skipped (an extension model's contract fault, never a
// data-dependent refusal).
func bootstrapCore(ctx context.Context, spec *types.RegressionSpec, rows []Record, predictors []string, q int, rep replicateFn, fatal func(error) bool) (*bootstrapRun, error) {
	n := len(rows)
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"bootstrap resampling requires at least 3 observations",
			map[string]any{"n": n},
		)
	}
	B := spec.BootstrapIters
	if B <= 0 {
		B = defaultBootstrapIters
	}
	seed := spec.RNGSeed
	if seed == 0 {
		seed = deriveBootstrapSeed(spec, rows, predictors)
	}
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9E3779B97F4A7C15))

	replicates := make([][]float64, 0, B)
	sample := make([]Record, n)
	for b := 0; b < B; b++ {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		for i := 0; i < n; i++ {
			sample[i] = rows[rng.IntN(n)]
		}
		row, err := rep(sample)
		if err != nil {
			if fatal != nil && fatal(err) {
				return nil, err
			}
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
		replicates = append(replicates, row)
	}
	if len(replicates) < 10 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_NO_CONVERGE,
			"bootstrap produced fewer than 10 successful replicates; underlying refit is unstable on this data",
			map[string]any{"successful": len(replicates), "requested": B},
		)
	}

	bm := replicateMean(replicates, q)
	// Sample covariance (unbiased, B − 1 divisor) of the successful
	// replicates; the SE is the square root of its diagonal (the sample
	// SD), so √diag(Vcov) == SE exactly.
	div := float64(len(replicates) - 1)
	cov := replicateCov(replicates, bm, spec.Vcov, func(s float64) float64 { return s / div })

	// Percentile p-values: fraction of replicates below zero, mirrored
	// to two sides.
	bootP := make([]float64, q)
	for j := 0; j < q; j++ {
		below := 0
		for _, r := range replicates {
			if r[j] < 0 {
				below++
			}
		}
		pHat := float64(below) / float64(len(replicates))
		pTwoSided := 2 * math.Min(pHat, 1-pHat)
		// Floor a p ≈ 0 result (every replicate on the same side of
		// zero) at 1 / B rather than literal zero, mirroring the
		// standard percentile-CI convention.
		if pTwoSided == 0 {
			pTwoSided = 1.0 / float64(len(replicates))
		}
		bootP[j] = pTwoSided
	}
	return &bootstrapRun{se: diagSqrt(cov), p: bootP, cov: cov, requested: B, succeeded: len(replicates)}, nil
}
