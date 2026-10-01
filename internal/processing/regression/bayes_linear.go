package regression

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// bayesLinearEngine fits a Bayesian linear regression under a conjugate
// Normal-Inverse-Gamma prior on (β, σ²). The posterior is also NIG and
// admits a closed form; the engine consumes the same streaming Welford
// sufficient statistics as REG_OLS, then performs a single finalize-time
// posterior update.
//
// Prior:
//
//	σ²    ~ InverseGamma(a₀, b₀)
//	β | σ² ~ Normal(μ₀, σ² · Λ₀⁻¹)
//
// where Λ₀ is the prior precision matrix. This engine accepts only a
// scalar prior precision: Λ₀ = ε · I with ε = PriorPrecision (default
// 1e-3). Per-coefficient precision matrices are intentionally out of
// scope; callers who need them request the extension via Phase 9.
//
// Posterior after observing X (n × (p+1) with intercept column) and y
// (length n):
//
//	Λ_n = Λ₀ + XᵀX
//	μ_n = Λ_n⁻¹ · (Λ₀ μ₀ + Xᵀy)
//	a_n = a₀ + n/2
//	b_n = b₀ + ½ (yᵀy + μ₀ᵀΛ₀μ₀ − μ_nᵀΛ_nμ_n)
//
// All four (XᵀX, Xᵀy, yᵀy, n) are derivable at finalize time from the
// centered Welford moments and the running means — see buildGramFromWelford
// below — so the engine reuses olsAccumulator unchanged.
//
// Posterior marginals (after integrating out σ²):
//
//	β_j ~ t_{2·a_n} ( μ_n[j], (b_n / a_n) · (Λ_n⁻¹)[j,j] )
//
// from which the std errors and credible intervals are taken. The
// posterior-mean estimate of σ is √(b_n / a_n).
type bayesLinearEngine struct {
	spec   *types.RegressionSpec
	schema *encoding.Schema
	acc    *olsAccumulator

	// Resolved prior parameters (defaults filled in by newBayesLinearEngine).
	priorMu        []float64 // length p+1; first entry is intercept
	priorPrecision float64
	priorShape     float64
	priorRate      float64
	credibleLevel  float64

	// finalized flips when Finalize / FitBuffered has run; further
	// UpdateRow calls would corrupt the fit and are rejected.
	finalized bool
}

const (
	defaultBayesPriorPrecision = 1e-3
	defaultBayesPriorShape     = 1e-3
	defaultBayesPriorRate      = 1e-3
	defaultBayesCredibleLevel  = 0.95
)

// newBayesLinearEngine validates the spec and constructs a fresh engine
// with prior parameters resolved (defaults filled in for any unset
// fields). The spec is assumed schema-checked by
// descriptor.validateRegressions upstream; ValidateRegression here
// catches semantic violations (bad Prior enum, mismatched PriorMu length,
// negative prior scalars, OLS/GLM knobs stowed away on a Bayes spec).
func newBayesLinearEngine(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error) {
	if spec.Type != types.REG_BAYES_LINEAR {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"bayesLinearEngine constructed for non-Bayes spec",
			map[string]any{"type": string(spec.Type)},
		)
	}
	if err := ValidateRegression(spec); err != nil {
		return nil, err
	}
	if spec.Resample != "" || spec.Selection != "" {
		// Modifier wrappers land in Phase 5; route to the stub so the
		// response carries PROCESSING_REGRESSION_NOT_IMPLEMENTED with
		// the operator name in details (same as Phase 0).
		return newNotImplemented(spec, schema)
	}
	if len(spec.Predictors) == 0 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"REG_BAYES_LINEAR requires at least one predictor",
			map[string]any{"name": spec.Name},
		)
	}
	if spec.Target == "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"REG_BAYES_LINEAR requires a Target field",
			map[string]any{"name": spec.Name},
		)
	}

	p := len(spec.Predictors)
	// Resolve prior μ₀: empty defaults to zero vector of length p+1
	// (intercept first, then per-predictor entries).
	mu := make([]float64, p+1)
	if len(spec.PriorMu) != 0 {
		// ValidateRegression already enforced len(PriorMu) == p+1.
		copy(mu, spec.PriorMu)
	}
	precision := spec.PriorPrecision
	if precision == 0 {
		precision = defaultBayesPriorPrecision
	}
	shape := spec.PriorShape
	if shape == 0 {
		shape = defaultBayesPriorShape
	}
	rate := spec.PriorRate
	if rate == 0 {
		rate = defaultBayesPriorRate
	}
	level := spec.CredibleLevel
	if level == 0 {
		level = defaultBayesCredibleLevel
	}

	return &bayesLinearEngine{
		spec:           spec,
		schema:         schema,
		acc:            newOLSAccumulator(p),
		priorMu:        mu,
		priorPrecision: precision,
		priorShape:     shape,
		priorRate:      rate,
		credibleLevel:  level,
	}, nil
}

// UpdateRow folds one record into the running sufficient statistics.
// Listwise null deletion identical to olsEngine.UpdateRow.
func (e *bayesLinearEngine) UpdateRow(rec Record) error {
	if e.finalized {
		return errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"bayesLinearEngine.UpdateRow called after Finalize",
		)
	}
	y, ok := rec.NumericValue(e.spec.Target)
	if !ok {
		return nil
	}
	x := make([]float64, len(e.spec.Predictors))
	for i, name := range e.spec.Predictors {
		v, ok := rec.NumericValue(name)
		if !ok {
			return nil
		}
		x[i] = v
	}
	e.acc.UpdateRow(x, y)
	return nil
}

// Finalize closes the streaming fit and returns the populated result.
func (e *bayesLinearEngine) Finalize() (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"bayesLinearEngine.Finalize called twice",
		)
	}
	e.finalized = true
	return e.finalizeFromAccumulator()
}

// FitBuffered runs the same accumulator pipeline over an in-memory
// record slice. Bit-for-bit identical to the streaming path.
func (e *bayesLinearEngine) FitBuffered(records []Record) (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"bayesLinearEngine.FitBuffered called after Finalize",
		)
	}
	for _, rec := range records {
		if err := e.UpdateRow(rec); err != nil {
			return nil, err
		}
	}
	e.finalized = true
	return e.finalizeFromAccumulator()
}

// Fit satisfies the Engine interface for callers that never feed rows.
// With no records the fit is degenerate and surfaces INSUFFICIENT_DATA,
// matching the olsEngine.Fit() contract.
func (e *bayesLinearEngine) Fit() (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"bayesLinearEngine.Fit called after Finalize",
		)
	}
	e.finalized = true
	return e.finalizeFromAccumulator()
}

// finalizeFromAccumulator runs the posterior update and packages the
// result. The math splits into three phases:
//
//  1. Recover the un-centered Gram (XᵀX with intercept) and Xᵀy and
//     yᵀy from the Welford-centered moments.
//  2. Solve the posterior precision matrix Λ_n = Λ₀ + XᵀX and the
//     posterior mean μ_n = Λ_n⁻¹ · (Λ₀ μ₀ + Xᵀy) via Cholesky.
//  3. Compute the posterior shape / rate, then derive Student-t marginal
//     std errors and credible intervals.
func (e *bayesLinearEngine) finalizeFromAccumulator() (*types.RegressionResult, error) {
	n := e.acc.n
	p := e.acc.p
	q := p + 1 // design-matrix column count, including intercept

	if n < q {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"REG_BAYES_LINEAR requires n ≥ p + 1 observations after filtering",
			map[string]any{"n": n, "p": p, "required": q},
		)
	}

	e.acc.finalize()

	// Build un-centered Gram XᵀX and Xᵀy with an explicit intercept
	// column (index 0). This is the design that the NIG posterior is
	// derived against — there is no centering trick that survives the
	// prior update unchanged.
	gram, xtY, ytY := buildGramFromWelford(e.acc)

	// Prior precision matrix Λ₀ = priorPrecision · I (scalar prior).
	// Plus Λ_n = Λ₀ + XᵀX written into a fresh symmetric dense.
	lambdaN := mat.NewSymDense(q, nil)
	for i := 0; i < q; i++ {
		for j := i; j < q; j++ {
			val := gram[i*q+j]
			if i == j {
				val += e.priorPrecision
			}
			lambdaN.SetSym(i, j, val)
		}
	}

	// Posterior precision Cholesky factor.
	var chol mat.Cholesky
	if ok := chol.Factorize(lambdaN); !ok {
		// With a positive prior precision this should never trip; if it
		// does, the augmented system is too ill-conditioned for the
		// numerical Cholesky and we surface RANK_DEFICIENT (consumers
		// can route through more regularization or drop a predictor).
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"posterior precision Λ_n = Λ₀ + XᵀX is not positive-definite (predictors near-collinear and prior precision too weak to compensate)",
			map[string]any{"n": n, "p": p, "prior_precision": e.priorPrecision},
		)
	}

	// μ_n = Λ_n⁻¹ · (Λ₀ μ₀ + Xᵀy).
	rhs := make([]float64, q)
	for i := 0; i < q; i++ {
		rhs[i] = e.priorPrecision*e.priorMu[i] + xtY[i]
	}
	rhsVec := mat.NewVecDense(q, rhs)
	var muN mat.VecDense
	if err := chol.SolveVecTo(&muN, rhsVec); err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky solve failed for the posterior precision matrix",
			map[string]any{"n": n, "p": p, "gonum_error": err.Error()},
		)
	}
	muNSlice := make([]float64, q)
	for i := 0; i < q; i++ {
		muNSlice[i] = muN.AtVec(i)
	}

	// Posterior shape and rate.
	//   a_n = a₀ + n/2
	//   b_n = b₀ + ½ (yᵀy + μ₀ᵀΛ₀μ₀ − μ_nᵀΛ_nμ_n)
	aN := e.priorShape + float64(n)/2.0
	// μ₀ᵀΛ₀μ₀ for scalar Λ₀ = ε·I: just ε · Σ μ₀_j².
	mu0Quad := 0.0
	for _, v := range e.priorMu {
		mu0Quad += v * v
	}
	mu0Quad *= e.priorPrecision

	// μ_nᵀΛ_nμ_n via Λ_n · μ_n = (Λ₀ μ₀ + Xᵀy), so
	// μ_nᵀ Λ_n μ_n = μ_nᵀ · (Λ₀ μ₀ + Xᵀy) — exact, no extra solve.
	muNQuad := 0.0
	for i := 0; i < q; i++ {
		muNQuad += muNSlice[i] * rhs[i]
	}

	bN := e.priorRate + 0.5*(ytY+mu0Quad-muNQuad)
	if bN < 0 {
		// Should not happen under a well-formed prior; clamp tiny negatives
		// from cancellation. If genuinely negative, surface RANK_DEFICIENT
		// since downstream √(b_n/a_n) would be NaN.
		if bN > -1e-9*(ytY+1) {
			bN = 0
		} else {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"posterior rate b_n is negative; data + prior combination is degenerate",
				map[string]any{"b_n": bN, "a_n": aN, "ytY": ytY},
			)
		}
	}

	// Posterior marginal: β_j ~ t_{2·a_n} ( μ_n[j], (b_n/a_n)·(Λ_n⁻¹)[j,j] ).
	// Get the diagonal of Λ_n⁻¹ via Cholesky.InverseTo.
	var invLambda mat.SymDense
	if err := chol.InverseTo(&invLambda); err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky inverse failed for the posterior precision matrix",
			map[string]any{"gonum_error": err.Error()},
		)
	}

	sigma2Post := bN / aN
	residualStdErr := sqrt(sigma2Post)

	stdErrors := make([]float64, q)
	for j := 0; j < q; j++ {
		v := sigma2Post * invLambda.At(j, j)
		stdErrors[j] = sqrt(v)
	}

	// Credible intervals: μ_n[j] ± t_q · SE[j] with t_q the (1−α/2)
	// quantile of t(2·a_n). credibleInterval handles the degenerate
	// SE=0 case.
	dfPost := 2 * aN
	intervals := make([][2]float64, q)
	for j := 0; j < q; j++ {
		intervals[j] = credibleInterval(muNSlice[j], stdErrors[j], dfPost, e.credibleLevel)
	}

	// R² / Adjusted R² from the posterior-mean point estimate.
	// RSS computed via the OLS identity (full design including intercept):
	//   RSS = yᵀy − 2·μ_nᵀ·Xᵀy + μ_nᵀ · XᵀX · μ_n
	// using the un-centered gram. The intercept term is handled because
	// XᵀX includes the intercept row/column.
	xtxMuN := make([]float64, q)
	for i := 0; i < q; i++ {
		row := 0.0
		for j := 0; j < q; j++ {
			row += gram[i*q+j] * muNSlice[j]
		}
		xtxMuN[i] = row
	}
	rss := ytY
	for j := 0; j < q; j++ {
		rss -= 2 * muNSlice[j] * xtY[j]
		rss += muNSlice[j] * xtxMuN[j]
	}
	if rss < 0 {
		if rss > -1e-9*ytY {
			rss = 0
		}
	}
	// TSS = Σ(y_i − ȳ)² = e.acc.m2YY (centered target sum of squares).
	tss := e.acc.m2YY
	var r2, adjR2 float64
	if tss > 0 {
		r2 = 1 - rss/tss
		df := n - q // standard OLS-style adjustment
		if df > 0 {
			adjR2 = 1 - (1-r2)*float64(n-1)/float64(df)
		}
	}

	// Package the result. PValues left nil — Bayesian context; credible
	// intervals replace frequentist p-values.
	predictors := e.spec.Predictors
	coefMap := make(map[string]float64, q)
	seMap := make(map[string]float64, q)
	ciMap := make(map[string][2]float64, q)

	coefMap[InterceptKey] = muNSlice[0]
	seMap[InterceptKey] = stdErrors[0]
	ciMap[InterceptKey] = intervals[0]
	for i, name := range predictors {
		coefMap[name] = muNSlice[i+1]
		seMap[name] = stdErrors[i+1]
		ciMap[name] = intervals[i+1]
	}

	res := &types.RegressionResult{
		Name:              e.spec.Name,
		Type:              types.REG_BAYES_LINEAR,
		Prior:             "nig",
		Coefficients:      coefMap,
		StdErrors:         seMap,
		CredibleIntervals: ciMap,
		R2:                r2,
		AdjR2:             adjR2,
		NObs:              n,
		ResidualStdErr:    residualStdErr,
		ConvergedIters:    0,
	}
	return res, nil
}

// buildGramFromWelford reconstructs the un-centered design Gram XᵀX
// (size (p+1) × (p+1), intercept column 0), the un-centered cross-
// product Xᵀy (length p+1), and yᵀy from the Welford-centered moments.
//
// Identities (after `Σ` over all n observations):
//
//	Σ x_i x_j      = m2XX[i,j]    + n · μ_x[i] · μ_x[j]
//	Σ x_i y        = m2XY[i]      + n · μ_x[i] · μ_y
//	Σ y²           = m2YY         + n · μ_y²
//	Σ 1            = n
//	Σ 1 · x_j      = n · μ_x[j]
//	Σ 1 · y        = n · μ_y
//
// Returns the Gram row-major in a flat slice for cache locality.
//
// Pre-condition: the caller has already invoked acc.finalize() so m2XX
// is symmetric (upper and lower triangles mirrored).
func buildGramFromWelford(a *olsAccumulator) (gram, xtY []float64, ytY float64) {
	p := a.p
	q := p + 1
	n := float64(a.n)
	gram = make([]float64, q*q)
	xtY = make([]float64, q)

	// Intercept row / column.
	gram[0*q+0] = n
	for j := 0; j < p; j++ {
		val := n * a.meanX[j]
		gram[0*q+(j+1)] = val
		gram[(j+1)*q+0] = val
	}
	// Predictor × predictor block.
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			gram[(i+1)*q+(j+1)] = a.m2XX[i*p+j] + n*a.meanX[i]*a.meanX[j]
		}
	}

	// Cross-product Xᵀy.
	xtY[0] = n * a.meanY
	for i := 0; i < p; i++ {
		xtY[i+1] = a.m2XY[i] + n*a.meanX[i]*a.meanY
	}

	// yᵀy.
	ytY = a.m2YY + n*a.meanY*a.meanY
	return gram, xtY, ytY
}
