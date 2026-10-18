package regression

import (
	"encoding/binary"
	stderrors "errors"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/types"
)

// defaultBootstrapIters is the replicate count when the spec leaves
// BootstrapIters at zero or negative; any positive count is used as
// given. 1000 is the standard textbook minimum for a
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
//   - "jackknife" : leave-one-out refit, jackknife SE formula; with
//     jackknife_groups / jackknife_by the delete-a-group form
//     (runGroupedJackknife).
//   - "bootstrap" : B = BootstrapIters (defaultBootstrapIters when
//     <= 0) replicates with replacement; SE is the sample std
//     of the replicate coefficient set, p-values from the
//     fraction of replicates that cross zero (percentile-
//     style two-sided test).
//
// fitter is the closure that refits the inner engine on a record
// subset; warm, when non-nil, is the same refit warm-started at a β
// (the delete-a-group jackknife starts every replicate from the full
// fit — REG_GLM's IRLS; nil refits cold). The OLS and GLM paths supply different closures so each
// family stays aligned with its own solver and warning surface.
//
// The Resample kind is echoed on the returned result so consumers can
// identify the uncertainty source; a bootstrap also echoes the
// replicate count it ran (BootstrapRequested) and the replicates whose
// refit succeeded (BootstrapSucceeded).
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
	warm func([]Record, []float64) (*refitResult, error),
	pValueFn func(coef, se float64) float64,
) (*types.RegressionResult, error) {
	switch spec.Resample {
	case "jackknife":
		if regresample.Grouped(spec) {
			return runGroupedJackknife(spec, records, predictors, baseResult, fitter, warm, pValueFn)
		}
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
	// Jackknife covariance (n − 1)/n · Σᵢ (β_(i) − β̄)(β_(i) − β̄)ᵀ; the
	// SE is the square root of its diagonal, so √diag(Vcov) == SE
	// exactly. The off-diagonal is only formed when Vcov is asked for.
	scale := float64(len(replicates)-1) / float64(len(replicates))
	cov := replicateCov(replicates, mean, spec.Vcov, func(s float64) float64 { return scale * s })
	se := make([]float64, p+1)
	for j := 0; j < p+1; j++ {
		se[j] = math.Sqrt(cov[j][j])
	}

	return packageResampleResult(spec, baseResult, predictors, se, cov, pValueFn, nil), nil
}

// runGroupedJackknife is the delete-a-group jackknife (JK1): the
// fitted rows — complete on the target and every active predictor,
// carrying a valid non-zero weight on a weighted slot, and (jackknife_by)
// a non-null cluster value — are split into g delete groups, and
// replicate k refits without group k:
//
//	SE[j] = √((g − 1)/g · Σₖ (β₍ₖ₎[j] − β̄[j])²)
//
// the covariance (Vcov) the same scaled cross-product (replicateCov).
// Groups: jackknife_groups assigns the m fitted rows by a seeded random
// permutation (jackknifeAssignment — rng_seed, or the bootstrap's
// data-derived seed at 0), group sizes differing by at most one;
// jackknife_by makes each distinct value of the field one group, in
// sorted key order. A weighted slot refits each replicate on the
// remaining rows with their own weights (the JK1 replicate weights'
// g/(g−1) rescale moves no coefficient: β is invariant to rescaling the
// weights, penalised fits included — the penalty's N* is Σw*).
//
// Every replicate warm-starts from the full-data β (warm, when set):
// the same fixed point, a few IRLS iterations each.
//
// Replicate failure policy: every delete group must refit — a dropped
// replicate would understate the variance with no correction, so the
// first replicate whose refit fails (separation once a group is gone,
// rank deficiency, no convergence) fails the whole slot with that
// refit's own code, its details naming the group (jackknife_group,
// 0-based; jackknife_by adds the group's value). g above the fitted row
// count, or jackknife_by with fewer than 2 distinct values, is
// PROCESSING_REGRESSION_INSUFFICIENT_DATA.
func runGroupedJackknife(
	spec *types.RegressionSpec,
	records []Record,
	predictors []string,
	baseResult *types.RegressionResult,
	fitter func([]Record) (*refitResult, error),
	warm func([]Record, []float64) (*refitResult, error),
	pValueFn func(coef, se float64) float64,
) (*types.RegressionResult, error) {
	rows := jackknifeRows(spec, records, predictors)
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

	p := len(predictors)
	start := make([]float64, p+1)
	start[0] = baseResult.Coefficients[InterceptKey]
	for j, name := range predictors {
		start[j+1] = baseResult.Coefficients[name]
	}

	replicates := make([][]float64, 0, g)
	subset := make([]Record, 0, m)
	for k := 0; k < g; k++ {
		subset = subset[:0]
		for i, rec := range rows {
			if groupOf[i] != k {
				subset = append(subset, rec)
			}
		}
		var fit *refitResult
		var err error
		if warm != nil {
			fit, err = warm(subset, start)
		} else {
			fit, err = fitter(subset)
		}
		if err != nil {
			return nil, jackknifeReplicateError(err, spec, k, labels)
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

	mean := make([]float64, p+1)
	for _, r := range replicates {
		for j := 0; j < p+1; j++ {
			mean[j] += r[j]
		}
	}
	for j := 0; j < p+1; j++ {
		mean[j] /= float64(g)
	}
	scale := float64(g-1) / float64(g)
	cov := replicateCov(replicates, mean, spec.Vcov, func(s float64) float64 { return scale * s })
	se := make([]float64, p+1)
	for j := 0; j < p+1; j++ {
		se[j] = math.Sqrt(cov[j][j])
	}
	out := packageResampleResult(spec, baseResult, predictors, se, cov, pValueFn, nil)
	out.JackknifeGroups = g
	return out, nil
}

// jackknifeRows returns the records a delete-a-group jackknife assigns:
// those that enter the fit — the target and every active predictor
// present, a valid non-zero weight on a weighted slot, and the
// jackknife_by value present — in record order.
func jackknifeRows(spec *types.RegressionSpec, records []Record, predictors []string) []Record {
	rw := newRowWeights(spec)
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		if _, ok := rec.NumericValue(spec.Target); !ok {
			continue
		}
		complete := true
		for _, name := range predictors {
			if _, ok := rec.NumericValue(name); !ok {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		if spec.JackknifeBy != "" {
			if _, ok := clusterKey(rec, spec.JackknifeBy); !ok {
				continue
			}
		}
		if _, ok := rw.of(rec); !ok {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// jackknifeAssignment assigns m rows to g delete groups: a random
// permutation drawn from a PCG generator seeded like the bootstrap's
// (seed, seed ^ 0x9E3779B97F4A7C15), the row at permutation position r
// joining group r mod g — every group non-empty when g ≤ m, sizes
// differing by at most one. A pure function of (m, g, seed).
func jackknifeAssignment(m, g int, seed int64) []int {
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9E3779B97F4A7C15))
	groupOf := make([]int, m)
	for r, i := range rng.Perm(m) {
		groupOf[i] = r % g
	}
	return groupOf
}

// clusterAssignment makes each distinct value of field among rows one
// delete group, groups numbered in sorted key order; labels[k] is group
// k's value as clusterKey spells it.
func clusterAssignment(field string, rows []Record) (groupOf []int, labels []string) {
	keys := make([]string, len(rows))
	seen := map[string]struct{}{}
	for i, rec := range rows {
		key, _ := clusterKey(rec, field)
		keys[i] = key
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			labels = append(labels, key)
		}
	}
	sort.Strings(labels)
	index := make(map[string]int, len(labels))
	for k, l := range labels {
		index[l] = k
	}
	groupOf = make([]int, len(rows))
	for i, key := range keys {
		groupOf[i] = index[key]
	}
	return groupOf, labels
}

// jackknifeReplicateError is a failed delete-group refit's error with
// the group named: a coded error keeps its code and message and gains
// details.jackknife_group (and, under jackknife_by, details.jackknife_by
// plus the group's value) on a copy; anything else passes through.
func jackknifeReplicateError(err error, spec *types.RegressionSpec, k int, labels []string) error {
	var cerr *errors.CodedError
	if !stderrors.As(err, &cerr) {
		return err
	}
	details := make(map[string]any, len(cerr.Details)+3)
	for key, v := range cerr.Details {
		details[key] = v
	}
	details["jackknife_group"] = k
	if spec.JackknifeBy != "" && k < len(labels) {
		details["jackknife_by"] = spec.JackknifeBy
		details["jackknife_value"] = clusterLabel(labels[k])
	}
	out := *cerr
	out.Details = details
	return &out
}

// clusterLabel strips clusterKey's type prefix ("s:" label, "n:" number).
func clusterLabel(key string) string {
	if len(key) > 2 && key[1] == ':' {
		return key[2:]
	}
	return key
}

// replicateCov returns the (q×q) scaled cross-product of the centred
// replicate rows: out[i][j] = finish(Σ_r (r[i] − mean[i])·(r[j] −
// mean[j])). Only the diagonal is formed unless full is set (Vcov), so
// a Vcov-free fit does no extra work; the diagonal is accumulated and
// finished by exactly the expression the resample SE has always used,
// so its square root is the reported standard error bit for bit. The
// replicate set is the one the SE sums over — a skipped bootstrap
// replicate is absent from both.
func replicateCov(replicates [][]float64, mean []float64, full bool, finish func(float64) float64) [][]float64 {
	q := len(mean)
	out := make([][]float64, q)
	for i := range out {
		out[i] = make([]float64, q)
	}
	for _, r := range replicates {
		for i := 0; i < q; i++ {
			di := r[i] - mean[i]
			if !full {
				out[i][i] += di * di
				continue
			}
			for j := i; j < q; j++ {
				out[i][j] += di * (r[j] - mean[j])
			}
		}
	}
	for i := 0; i < q; i++ {
		for j := i; j < q; j++ {
			if i != j && !full {
				break
			}
			out[i][j] = finish(out[i][j])
			out[j][i] = out[i][j]
		}
	}
	return out
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
// is zero or negative. RNG seeding: a non-zero spec.RNGSeed is used as
// given (TestRegOLS_Bootstrap_Reproducible); zero derives a seed from
// the request and the data the fit sees (deriveBootstrapSeed), never
// the clock, so the same request over the same cohort is bit-identical
// run to run (TestRegOLS_Bootstrap_ZeroSeedDeterministic).
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
	if B <= 0 {
		B = defaultBootstrapIters
	}
	seed := spec.RNGSeed
	if seed == 0 {
		seed = deriveBootstrapSeed(spec, records, predictors)
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
	// Sample covariance (unbiased, B − 1 divisor) of the successful
	// replicates; the SE is the square root of its diagonal (the sample
	// SD), so √diag(Vcov) == SE exactly.
	div := float64(len(replicates) - 1)
	cov := replicateCov(replicates, bm, spec.Vcov, func(s float64) float64 { return s / div })
	se := make([]float64, p+1)
	for j := 0; j < p+1; j++ {
		se[j] = math.Sqrt(cov[j][j])
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

	out := packageResampleResult(spec, baseResult, predictors, se, cov, pValueFn, bootP)
	out.BootstrapRequested = B
	out.BootstrapSucceeded = len(replicates)
	return out, nil
}

// packageResampleResult writes the resample-derived SE / p-values onto
// a copy of baseResult, preserving the point estimate (Coefficients)
// and every non-uncertainty quantity (R², deviance, NObs, …).
//
// When bootstrapP is non-nil it is used directly (one entry per
// [intercept, slope_1, …]); when nil the analytical pValueFn is
// applied to (β, SE) instead — this is the jackknife path which uses
// the t-distribution / Wald-z form.
//
// cov is the replicate covariance (keys (intercept) then predictors);
// when spec.Vcov is set it REPLACES the analytical covariance the base
// fit carried, so Vcov always describes the reported StdErrors.
func packageResampleResult(
	spec *types.RegressionSpec,
	baseResult *types.RegressionResult,
	predictors []string,
	se []float64,
	cov [][]float64,
	pValueFn func(coef, se float64) float64,
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
	// Wald normal intervals on the resampled standard errors — the basis
	// the resample p-values use — replacing the base fit's t intervals.
	attachIntervals(&out, resolvedConfLevel(spec), 0, false)
	out.Resample = spec.Resample
	out.Vcov, out.Correlation = nil, nil
	if spec.Vcov {
		attachVcov(&out, predictors, cov)
	}
	return &out
}

// deriveBootstrapSeed is the rng_seed 0 seed: an FNV-1a hash of the
// spec's wire form (rng_seed excluded) and of every record's target and
// predictor values, in record order. It is a pure function of the
// request and the cohort rows the fit reads — the same request over the
// same data always draws the same replicates, a different request (or
// different data) a different seed. Pin a non-zero rng_seed for a seed
// that does not move with the data.
func deriveBootstrapSeed(spec *types.RegressionSpec, records []Record, predictors []string) int64 {
	h := fnv.New64a()
	cp := *spec
	cp.RNGSeed = 0
	if body, err := types.MarshalFinite(&cp); err == nil {
		_, _ = h.Write(body)
	}
	var buf [8]byte
	word := func(u uint64) {
		binary.LittleEndian.PutUint64(buf[:], u)
		_, _ = h.Write(buf[:])
	}
	word(uint64(len(records)))
	fields := append([]string{spec.Target}, predictors...)
	for _, r := range records {
		for _, f := range fields {
			v, ok := r.NumericValue(f)
			if !ok {
				word(0x7ff8000000000001) // a null: a NaN payload no value carries
				continue
			}
			word(math.Float64bits(v))
		}
	}
	return int64(h.Sum64())
}
