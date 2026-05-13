package regression

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// glmEngine fits a generalized linear model via iteratively reweighted
// least squares (IRLS). REG_GLM is the first regression operator in
// the buffered family — the streaming sufficient stats trick that
// powers REG_OLS does not work for GLM because the weight matrix W
// changes each iteration. The engine therefore consumes a fully
// materialized record slice via FitBuffered (no StreamingEngine
// implementation).
//
// Algorithm (per family g with link `g`, variance V):
//
//  1. Build design matrix X (n × (p+1)) with intercept column and
//     target vector y (length n) by walking the record slice with
//     listwise null deletion.
//  2. η₀ = g(ȳ_safe); β₀ = [η₀, 0, …, 0].
//  3. For iter = 0..MaxIters-1:
//     μ_i  = g⁻¹(η_i)
//     dμdη = ∂μ/∂η at η_i
//     W_i  = dμdη² / V(μ_i)
//     z_i  = η_i + (y_i − μ_i) / dμdη
//     β_new = (XᵀWX)⁻¹ XᵀWz  (solved via Cholesky)
//     if ||Δβ|| / (||β|| + ε) < Tol → converged
//     β ← β_new; η ← Xβ
//  4. If not converged → PROCESSING_REGRESSION_NO_CONVERGE.
//
// At convergence the standard errors come from
//
//	Cov(β) = (XᵀWX)⁻¹
//
// evaluated at the final weights. The dispersion parameter is fixed at
// 1 for binomial / poisson (the family assumption), so the Wald
// statistic is β/SE compared to a standard normal — not a Student's t.
// Gamma path is wired but not numerically validated this phase.
type glmEngine struct {
	spec   *types.RegressionSpec
	schema *encoding.Schema
	family glmFamily

	finalized bool
}

const (
	defaultGLMMaxIters = 50
	defaultGLMTol      = 1e-8

	// convergenceEps is the small additive constant on the denominator
	// of the relative-change convergence check: ||Δβ|| / (||β|| + ε).
	convergenceEps = 1e-12
)

// newGLMEngine validates the REG_GLM spec and constructs an engine.
// Modifiers (Resample / Selection) and Penalty are rejected at this
// phase; Phase 5 (modifiers) and a later phase (GLM regularization)
// will widen the supported configurations.
func newGLMEngine(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error) {
	if spec.Type != types.REG_GLM {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"glmEngine constructed for non-GLM spec",
			map[string]any{"type": string(spec.Type)},
		)
	}
	if err := ValidateRegression(spec); err != nil {
		return nil, err
	}
	if spec.Resample != "" || spec.Selection != "" {
		// Modifier wrappers land in Phase 5; route to stub for now.
		return newNotImplemented(spec, schema)
	}
	if len(spec.Predictors) == 0 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"REG_GLM requires at least one predictor",
			map[string]any{"name": spec.Name},
		)
	}
	if spec.Target == "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"REG_GLM requires a Target field",
			map[string]any{"name": spec.Name},
		)
	}
	fam, err := selectFamily(spec.Family, spec.Link)
	if err != nil {
		return nil, err
	}
	return &glmEngine{
		spec:   spec,
		schema: schema,
		family: fam,
	}, nil
}

// Fit returns INSUFFICIENT_DATA: GLM is buffered-only, so a no-record
// call cannot produce a meaningful fit. The orchestrator routes through
// FitBuffered; this method exists so the Phase 0 Engine.Fit contract
// stays satisfied.
func (e *glmEngine) Fit() (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"glmEngine.Fit called after Finalize",
		)
	}
	e.finalized = true
	return nil, errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
		"REG_GLM requires the buffered orchestrator path; engine received no records",
		map[string]any{"name": e.spec.Name, "type": string(e.spec.Type)},
	)
}

// FitBuffered consumes the materialized record slice, builds the design
// matrix with listwise null deletion, then runs IRLS until convergence
// or the iteration cap fires.
func (e *glmEngine) FitBuffered(records []Record) (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"glmEngine.FitBuffered called after Finalize",
		)
	}
	e.finalized = true

	p := len(e.spec.Predictors)
	// Build X (n × (p+1) including intercept column) and y (n).
	xRows, yVec := buildDesign(records, e.spec.Predictors, e.spec.Target)
	n := len(yVec)
	if n < p+1 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"REG_GLM requires n ≥ p + 1 observations after listwise null deletion",
			map[string]any{"n": n, "p": p, "required": p + 1},
		)
	}

	maxIters := e.spec.MaxIters
	if maxIters <= 0 {
		maxIters = defaultGLMMaxIters
	}
	tol := e.spec.Tol
	if tol <= 0 {
		tol = defaultGLMTol
	}

	// We never materialize X as a gonum Dense — the inner IRLS loop
	// builds XᵀWX and XᵀWz directly from the flat xRows slice. Doing it
	// this way keeps allocations to a single n × (p+1) buffer per fit;
	// the only gonum types we touch are the (p+1)×(p+1) Cholesky on
	// the normal equations.

	// β init: intercept = g(yBarSafe), slopes = 0.
	yBar := mean(yVec)
	mu0 := e.family.SafeMean(yBar)
	eta0 := e.family.LinkFunc(mu0)
	beta := make([]float64, p+1)
	beta[0] = eta0

	// eta = X β; initially eta_i = eta0 (since slope = 0).
	eta := make([]float64, n)
	for i := range eta {
		eta[i] = eta0
	}

	// Workspace reused per iteration.
	w := make([]float64, n)   // diagonal weights
	z := make([]float64, n)   // working response
	mu := make([]float64, n)  // mean response
	dmu := make([]float64, n) // ∂μ/∂η
	convergedIters := 0
	converged := false

	// Cache for the final XᵀWX inverse used for Cov(β).
	var lastInvXtWX *mat.SymDense

	for iter := 0; iter < maxIters; iter++ {
		// Per-row: compute μ, dμ/dη, W, z.
		for i := 0; i < n; i++ {
			mu[i] = e.family.InverseLink(eta[i])
			dmu[i] = e.family.DmuDeta(eta[i])
			v := e.family.Variance(mu[i])
			if v < 1e-300 {
				v = 1e-300
			}
			w[i] = dmu[i] * dmu[i] / v
			// Working response: z = η + (y − μ)/dμdη. Guard against
			// dμdη → 0 (saturated rows) by clamping to a small floor
			// — the corresponding W is also tiny so the row contributes
			// negligibly to the normal equations.
			if math.Abs(dmu[i]) < 1e-300 {
				z[i] = eta[i]
			} else {
				z[i] = eta[i] + (yVec[i]-mu[i])/dmu[i]
			}
		}

		// Build XᵀWX (p+1 × p+1) and XᵀWz (p+1) without materialising
		// the n × n W matrix. Symmetric by construction.
		xtwx := make([]float64, (p+1)*(p+1))
		xtwz := make([]float64, p+1)
		for i := 0; i < n; i++ {
			wi := w[i]
			zi := z[i]
			// X[i,*] has length p+1 (first column = 1).
			xi := xRows[i*(p+1) : (i+1)*(p+1)]
			for a := 0; a < p+1; a++ {
				wa := wi * xi[a]
				xtwz[a] += wa * zi
				row := a * (p + 1)
				for b := a; b < p+1; b++ {
					xtwx[row+b] += wa * xi[b]
				}
			}
		}
		// Mirror upper into lower triangle.
		for a := 0; a < p+1; a++ {
			for b := a + 1; b < p+1; b++ {
				xtwx[b*(p+1)+a] = xtwx[a*(p+1)+b]
			}
		}

		sym := mat.NewSymDense(p+1, xtwx)
		var chol mat.Cholesky
		if ok := chol.Factorize(sym); !ok {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"REG_GLM weighted normal equations are not positive-definite; predictors are collinear or the fit is saturated",
				map[string]any{"n": n, "p": p, "iter": iter, "family": e.family.Name, "link": e.family.Link},
			)
		}
		rhs := mat.NewVecDense(p+1, xtwz)
		var betaNew mat.VecDense
		if err := chol.SolveVecTo(&betaNew, rhs); err != nil {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"REG_GLM Cholesky solve failed inside IRLS",
				map[string]any{"n": n, "p": p, "iter": iter, "gonum_error": err.Error()},
			)
		}
		// Relative-change convergence check.
		var dnum, dnorm float64
		for a := 0; a < p+1; a++ {
			bn := betaNew.AtVec(a)
			d := bn - beta[a]
			dnum += d * d
			dnorm += beta[a] * beta[a]
		}
		dnum = math.Sqrt(dnum)
		dnorm = math.Sqrt(dnorm)
		// Copy new β and recompute η.
		for a := 0; a < p+1; a++ {
			beta[a] = betaNew.AtVec(a)
		}
		// Cache the inverse of the current XᵀWX for SE computation.
		// We need the inverse at the converged weights, which is the
		// XᵀWX from this iteration evaluated at the next β; but β is
		// what we just solved for. Recompute X β and update η.
		for i := 0; i < n; i++ {
			eta[i] = 0
			xi := xRows[i*(p+1) : (i+1)*(p+1)]
			for a := 0; a < p+1; a++ {
				eta[i] += xi[a] * beta[a]
			}
		}
		convergedIters = iter + 1
		if dnum/(dnorm+convergenceEps) < tol {
			converged = true
			// Recompute one more (XᵀWX) at the final weights for SE.
			// W at the converged β: refresh mu/dmu/w.
			for i := 0; i < n; i++ {
				mu[i] = e.family.InverseLink(eta[i])
				dmu[i] = e.family.DmuDeta(eta[i])
				v := e.family.Variance(mu[i])
				if v < 1e-300 {
					v = 1e-300
				}
				w[i] = dmu[i] * dmu[i] / v
			}
			finalXtWX := make([]float64, (p+1)*(p+1))
			for i := 0; i < n; i++ {
				wi := w[i]
				xi := xRows[i*(p+1) : (i+1)*(p+1)]
				for a := 0; a < p+1; a++ {
					wa := wi * xi[a]
					row := a * (p + 1)
					for b := a; b < p+1; b++ {
						finalXtWX[row+b] += wa * xi[b]
					}
				}
			}
			for a := 0; a < p+1; a++ {
				for b := a + 1; b < p+1; b++ {
					finalXtWX[b*(p+1)+a] = finalXtWX[a*(p+1)+b]
				}
			}
			finalSym := mat.NewSymDense(p+1, finalXtWX)
			var finalChol mat.Cholesky
			if ok := finalChol.Factorize(finalSym); !ok {
				return nil, errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
					"REG_GLM converged but final-iteration XᵀWX is singular; SE undefined",
					map[string]any{"n": n, "p": p, "family": e.family.Name, "link": e.family.Link},
				)
			}
			var invSym mat.SymDense
			if err := finalChol.InverseTo(&invSym); err != nil {
				return nil, errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
					"REG_GLM final XᵀWX inverse failed",
					map[string]any{"gonum_error": err.Error()},
				)
			}
			lastInvXtWX = &invSym
			break
		}
	}

	if !converged {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_NO_CONVERGE,
			"REG_GLM IRLS failed to converge within MaxIters",
			map[string]any{
				"n": n, "p": p,
				"max_iters": maxIters, "tol": tol,
				"family": e.family.Name, "link": e.family.Link,
				"converged_iters": convergedIters,
			},
		)
	}

	// Standard errors: diag of Cov(β) = (XᵀWX)⁻¹ at converged W.
	stdErrors := make([]float64, p+1)
	for a := 0; a < p+1; a++ {
		v := lastInvXtWX.At(a, a)
		if v < 0 {
			v = 0
		}
		stdErrors[a] = math.Sqrt(v)
	}

	// Deviance at converged β.
	mu = make([]float64, n)
	for i := 0; i < n; i++ {
		mu[i] = e.family.InverseLink(eta[i])
	}
	deviance := 0.0
	for i := 0; i < n; i++ {
		deviance += e.family.Deviance(yVec[i], mu[i])
	}
	deviance *= 2.0

	// Null deviance: intercept-only model. The null fit has
	// μ_i = ȳ for every i; for binomial/poisson/gamma with their
	// canonical links the closed-form null intercept is g(ȳ).
	nullMu := e.family.SafeMean(yBar)
	nullDeviance := 0.0
	for i := 0; i < n; i++ {
		nullDeviance += e.family.Deviance(yVec[i], nullMu)
	}
	nullDeviance *= 2.0

	pseudoR2 := 0.0
	if nullDeviance > 0 {
		pseudoR2 = 1 - deviance/nullDeviance
	}

	// Package result. Coefficients[0] is the intercept; remaining
	// entries map to spec.Predictors in order. We use Wald-z p-values
	// (dispersion = 1 for binomial/poisson; gamma path is wired but
	// not validated this phase).
	predictors := e.spec.Predictors
	coefMap := make(map[string]float64, p+1)
	seMap := make(map[string]float64, p+1)
	pMap := make(map[string]float64, p+1)

	coefMap[InterceptKey] = beta[0]
	seMap[InterceptKey] = stdErrors[0]
	pMap[InterceptKey] = waldZTwoSidedP(beta[0], stdErrors[0])

	for i, name := range predictors {
		coefMap[name] = beta[i+1]
		seMap[name] = stdErrors[i+1]
		pMap[name] = waldZTwoSidedP(beta[i+1], stdErrors[i+1])
	}

	return &types.RegressionResult{
		Name:           e.spec.Name,
		Type:           types.REG_GLM,
		Family:         e.family.Name,
		Link:           e.family.Link,
		Coefficients:   coefMap,
		StdErrors:      seMap,
		PValues:        pMap,
		Deviance:       deviance,
		NullDeviance:   nullDeviance,
		PseudoR2:       pseudoR2,
		NObs:           n,
		ConvergedIters: convergedIters,
	}, nil
}

// buildDesign walks the record slice and emits the design matrix in
// row-major form (n × (p+1), intercept column first) plus the target
// vector. Listwise null deletion drops any row missing the target or
// any predictor — same policy as REG_OLS and the standard R / numpy
// default.
//
// xRows[i*(p+1) + 0] = 1 for every retained row; subsequent columns
// hold the predictor values in spec.Predictors order.
func buildDesign(records []Record, predictors []string, target string) (xRows []float64, yVec []float64) {
	p := len(predictors)
	xRows = make([]float64, 0, len(records)*(p+1))
	yVec = make([]float64, 0, len(records))
	xBuf := make([]float64, p)
	for _, rec := range records {
		y, ok := rec.NumericValue(target)
		if !ok {
			continue
		}
		anyNull := false
		for j, name := range predictors {
			v, ok := rec.NumericValue(name)
			if !ok {
				anyNull = true
				break
			}
			xBuf[j] = v
		}
		if anyNull {
			continue
		}
		xRows = append(xRows, 1)
		xRows = append(xRows, xBuf...)
		yVec = append(yVec, y)
	}
	return xRows, yVec
}

// mean returns Σx / n. Empty slice → 0; the caller guards against the
// n=0 case before invoking.
func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}
