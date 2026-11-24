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

// replicate is one resample replicate read in the driver's order: coef
// its coefficient vector (the driver's key order) and derived the
// statistics the slot derives from the same refit — its marginal effects
// in regmargins.ResultKeys order (U69 #639) — nil when the slot derives
// none. Every replicate of a run carries the same derived length, and a
// derived figure is summarised exactly as a coefficient is (the same
// mean, scale and percentile rule), so a derived column equal to a
// coefficient column reports the coefficient's SE and p bit for bit.
type replicate struct {
	coef, derived []float64
}

// replicateFn refits one resample replicate over a record subset.
type replicateFn func([]Record) (replicate, error)

// refitReplicate adapts a built-in refit closure to a replicateFn over
// p active predictors: (intercept, slope_1, …, slope_p), a slope the
// refit did not return reading 0. derive, when non-nil, is the
// per-replicate hook that reads the statistics the slot derives from the
// refit (its marginal effects); it sees the whole refitResult — β, the
// fitted design, prior weights and linear predictor — so any figure that
// is a function of one fit can be resampled through it.
func refitReplicate(p int, fit func([]Record) (*refitResult, error), derive func(*refitResult) []float64) replicateFn {
	return func(recs []Record) (replicate, error) {
		r, err := fit(recs)
		if err != nil {
			return replicate{}, err
		}
		out := replicate{coef: refitRow(p, r)}
		if derive != nil {
			out.derived = derive(r)
		}
		return out, nil
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

// replicateRun is one resample run's replicate statistics, shared by the
// built-in and extension drivers: the coefficient SE, covariance and —
// bootstrap only — percentile p-values; the same SE and percentile p of
// every derived figure (nil when the replicates derive none); the
// replicate counts a bootstrap echoes (requested / succeeded) and the
// delete-group count a grouped jackknife echoes.
type replicateRun struct {
	se, p               []float64
	cov                 [][]float64
	derivedSE, derivedP []float64
	// requested / succeeded are a bootstrap's replicate counts (0 on a
	// jackknife); groups a delete-a-group jackknife's g (0 otherwise).
	requested, succeeded, groups int
	// coefs, mean, finish and full keep what cov was summarised from,
	// so fullCov can form the off-diagonal cov skipped (a comparison
	// reads it whether or not vcov was asked for).
	coefs  [][]float64
	mean   []float64
	finish func(float64) float64
	full   bool
}

// fullCov is the full q×q replicate covariance: cov itself when it was
// formed full, else the same cross-product over the same replicates with
// the off-diagonal (replicateCov) — its diagonal is cov's bit for bit,
// so √diag is still the reported standard error.
func (r *replicateRun) fullCov() [][]float64 {
	if r.full {
		return r.cov
	}
	return replicateCov(r.coefs, r.mean, true, r.finish)
}

// newReplicateRun summarises the replicate set: the q×q coefficient
// covariance (replicateCov, off-diagonal only when full) and its √diag;
// with percentile set the two-sided percentile p of every column; and
// the same diagonal / percentile over the derived columns.
func newReplicateRun(reps []replicate, q int, full, percentile bool, finish func(float64) float64) *replicateRun {
	coefs := make([][]float64, len(reps))
	for i, r := range reps {
		coefs[i] = r.coef
	}
	mean := replicateMean(coefs, q)
	cov := replicateCov(coefs, mean, full, finish)
	run := &replicateRun{se: diagSqrt(cov), cov: cov, coefs: coefs, mean: mean, finish: finish, full: full}
	if percentile {
		run.p = percentileP(coefs, q)
	}
	if d := len(reps[0].derived); d > 0 {
		derived := make([][]float64, len(reps))
		for i, r := range reps {
			derived[i] = r.derived
		}
		run.derivedSE = diagSqrt(replicateCov(derived, replicateMean(derived, d), false, finish))
		if percentile {
			run.derivedP = percentileP(derived, d)
		}
	}
	return run
}

// echo writes a bootstrap's replicate counts on out (bootstrap_requested,
// bootstrap_succeeded); a no-op on a jackknife.
func (r *replicateRun) echo(out *types.RegressionResult) {
	if r.requested == 0 {
		return
	}
	out.BootstrapRequested, out.BootstrapSucceeded = r.requested, r.succeeded
}

// percentileP is the bootstrap percentile p-value of each of the q
// columns: the fraction of replicates below zero, mirrored to two sides
// and floored at 1/B.
func percentileP(replicates [][]float64, q int) []float64 {
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
	return bootP
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
func jackknifeCore(ctx context.Context, spec *types.RegressionSpec, rows []Record, q int, rep replicateFn) (*replicateRun, error) {
	n := len(rows)
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"jackknife resampling requires at least 3 observations",
			map[string]any{"n": n},
		)
	}
	replicates := make([]replicate, 0, n)
	leaveOneOut := make([]Record, 0, n-1)
	for i := 0; i < n; i++ {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		leaveOneOut = leaveOneOut[:0]
		leaveOneOut = append(leaveOneOut, rows[:i]...)
		leaveOneOut = append(leaveOneOut, rows[i+1:]...)
		row, err := rep(leaveOneOut)
		if err != nil {
			return nil, err
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
	// Jackknife covariance (n − 1)/n · Σᵢ (β_(i) − β̄)(β_(i) − β̄)ᵀ; the
	// SE is the square root of its diagonal, so √diag(Vcov) == SE
	// exactly. The off-diagonal is only formed when Vcov is asked for.
	scale := float64(len(replicates)-1) / float64(len(replicates))
	return newReplicateRun(replicates, q, spec.Vcov, false, func(s float64) float64 { return scale * s }), nil
}

// groupedJackknifeCore is the delete-a-group jackknife (JK1) over the
// fitted rows (runGroupedJackknifeCtx documents the groups and the failure
// policy): g refits, each without one group. records and predictors are
// the slot's record set and active predictors, read only by the seed of
// a jackknife_groups assignment at rng_seed 0 (deriveBootstrapSeed).
// The run echoes g (groups).
func groupedJackknifeCore(ctx context.Context, spec *types.RegressionSpec, records []Record, predictors []string, rows []Record, q int, rep replicateFn) (*replicateRun, error) {
	m := len(rows)
	var groupOf []int
	var labels []string
	g := 0
	if spec.JackknifeBy != "" {
		groupOf, labels = clusterAssignment(spec.JackknifeBy, rows)
		g = len(labels)
		if g < 2 {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
				"jackknife_by needs at least 2 distinct values among the fitted rows to form delete groups",
				map[string]any{"jackknife_by": spec.JackknifeBy, "groups": g, "n": m},
			)
		}
	} else {
		g = spec.JackknifeGroups
		if g > m {
			return nil, errors.NewCodedErrorWithDetails(
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

	replicates := make([]replicate, 0, g)
	subset := make([]Record, 0, m)
	for k := 0; k < g; k++ {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		subset = subset[:0]
		for i, rec := range rows {
			if groupOf[i] != k {
				subset = append(subset, rec)
			}
		}
		row, err := rep(subset)
		if err != nil {
			return nil, jackknifeReplicateError(err, spec, k, labels)
		}
		replicates = append(replicates, row)
	}
	scale := float64(g-1) / float64(g)
	run := newReplicateRun(replicates, q, spec.Vcov, false, func(s float64) float64 { return scale * s })
	run.groups = g
	return run, nil
}

// bootstrapCore is the non-parametric bootstrap over the fitted rows
// (runBootstrap documents B, the seed, the percentile p-value and the
// skip policy). predictors seed the rng_seed 0 derivation; q is the
// coefficient count. fatal, when non-nil,
// reports a replicate error that must stop the bootstrap instead of
// being skipped (an extension model's contract fault, never a
// data-dependent refusal).
func bootstrapCore(ctx context.Context, spec *types.RegressionSpec, rows []Record, predictors []string, q int, rep replicateFn, fatal func(error) bool) (*replicateRun, error) {
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

	replicates := make([]replicate, 0, B)
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

	// Sample covariance (unbiased, B − 1 divisor) of the successful
	// replicates; the SE is the square root of its diagonal (the sample
	// SD), so √diag(Vcov) == SE exactly. Percentile p-values: the
	// fraction of replicates below zero, mirrored to two sides
	// (percentileP).
	div := float64(len(replicates) - 1)
	run := newReplicateRun(replicates, q, spec.Vcov, true, func(s float64) float64 { return s / div })
	run.requested, run.succeeded = B, len(replicates)
	return run, nil
}
