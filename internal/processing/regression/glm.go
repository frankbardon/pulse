package regression

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// glmEngine fits a generalized linear model via iteratively reweighted
// least squares (IRLS). REG_GLM is the first regression operator in
// the buffered family — the streaming sufficient stats trick that
// powers REG_OLS does not work for GLM because the weight matrix W
// changes each iteration. The engine therefore consumes a fully
// materialized record slice via FitBuffered (no StreamingEngine
// implementation).
//
// Algorithm: build the design matrix X (n × (p+1), intercept column
// first) and target y with listwise null deletion, then run the one
// weighted IRLS loop, irlsFit (glm_irls.go) — an up-front QR rank check
// (the only RANK_DEFICIENT site), R's per-row starting values,
// step-halving on a deviance rise, convergence on the relative change in
// β, and the PROCESSING_REGRESSION_SEPARATION / NO_CONVERGE
// classification of a fit that stops short.
//
// At convergence the standard errors come from
//
//	Cov(β) = (XᵀWX)⁻¹
//
// evaluated at the final weights. The dispersion parameter is fixed at
// 1 for binomial / poisson (the family assumption), so the Wald
// statistic is β/SE compared to a standard normal — not a Student's t.
// Gamma path is wired but not numerically validated this phase.
//
// Weighted (U12 E4-S2, .claude/reference/weighting.md): the slot's row
// weights enter as R glm prior weights on w* = w·N*/Σw — the working
// weight is w*·μ'(η)²/V(μ), so Cov(β) = (XᵀW*X)⁻¹ and the deviances are
// Σw*·d. β is kind-free; under kind frequency the fit is the expanded
// rows', under kind probability R glm(weights = w*). NObs stays raw.
type glmEngine struct {
	spec   *types.RegressionSpec
	schema *encoding.Schema
	family glmFamily

	finalized bool

	// fitted, when set, receives the converged linear predictor η and
	// fitted means μ = g⁻¹(η) of the rows that entered the fit, in
	// record order — the engine's own fitted values. A test seam (the
	// scoring kernel's bit-equality pin); nil in every production fit.
	fitted func(eta, mu []float64)
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
		// Phase 5: route through modifierEngine which orchestrates the
		// resample / selection modifiers around the inner IRLS engine.
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
		return newModifierEngine(spec, schema)
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
	// Build X (n × (p+1) including intercept column), y (n) and the
	// prior weights (n): the slot's row weights on w* (all exactly 1
	// unweighted, so every product below reproduces the unweighted fit
	// bit for bit). n is the raw count of rows that entered the fit.
	rw := newRowWeights(e.spec)
	// A robust covariance (RegressionSpec.Covariance) also reads each
	// row's cluster / order_by key: a null one drops the row listwise.
	xRows, yVec, prior, keys, sumW, sumWSq := buildWeightedDesign(records, e.spec.Predictors, e.spec.Target, rw, robustKeyReader(e.spec))
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

	// The one weighted IRLS loop (glm_irls.go): the up-front rank check,
	// per-row starting values, step-halving and the separation refusal.
	// yBar (the prior-weighted mean) seeds only the null deviance below.
	yBar := weightedMean(yVec, prior)
	sol, err := irlsFit(&irlsProblem{
		X: xRows, Y: yVec, Prior: prior, K: p + 1,
		Columns:  append([]string{InterceptKey}, e.spec.Predictors...),
		Family:   e.family,
		MaxIters: maxIters, Tol: tol,
		WantInverse: true,
	})
	if err != nil {
		return nil, err
	}
	beta, eta, mu := sol.Beta, sol.Eta, sol.Mu
	convergedIters := sol.Iters
	lastInvXtWX := sol.Inverse

	// Standard errors: diag of Cov(β) = (XᵀWX)⁻¹ at converged W.
	stdErrors := make([]float64, p+1)
	for a := 0; a < p+1; a++ {
		v := lastInvXtWX.At(a, a)
		if v < 0 {
			v = 0
		}
		stdErrors[a] = math.Sqrt(v)
	}

	// A robust covariance replaces (XᵀWX)⁻¹ behind the standard errors,
	// the Wald p-values, the intervals and Vcov (glm_covariance.go).
	var robust *glmRobust
	if e.spec.Covariance.Robust() {
		var err error
		robust, err = e.glmSandwich(xRows, yVec, eta, mu, prior, keys, lastInvXtWX, rw.basis.NStar(sumW, sumWSq))
		if err != nil {
			return nil, err
		}
		stdErrors = robust.stdErrors
	}
	if e.fitted != nil {
		e.fitted(eta, mu)
	}
	deviance := sol.Deviance

	// Null deviance: intercept-only model. The null fit has
	// μ_i = ȳ for every i; for binomial/poisson/gamma with their
	// canonical links the closed-form null intercept is g(ȳ).
	nullMu := e.family.SafeMean(yBar)
	nullDeviance := 0.0
	for i := 0; i < n; i++ {
		nullDeviance += prior[i] * e.family.Deviance(yVec[i], nullMu)
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

	res := &types.RegressionResult{
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
	}
	rw.note(res, sumW, sumWSq)
	// Wald normal intervals, on the same basis as the Wald-z p-values.
	attachIntervals(res, resolvedConfLevel(e.spec), 0, false)
	if robust != nil {
		res.Covariance = robust.fit.info
		if e.spec.Vcov {
			attachVcov(res, predictors, robust.fit.v)
		}
	} else if e.spec.Vcov {
		// Cov(β) = (XᵀWX)⁻¹ at the converged weights (dispersion 1), its
		// diagonal the clamped variances the standard errors read.
		diag := make([]float64, p+1)
		for a := range diag {
			if v := lastInvXtWX.At(a, a); v >= 0 || math.IsNaN(v) {
				diag[a] = v
			}
		}
		attachVcov(res, predictors, symVcov(p+1, lastInvXtWX.At, 1, diag))
	}
	return res, nil
}

// buildWeightedDesign is buildDesign plus the per-row prior weights the
// weighted IRLS reads (.claude/reference/weighting.md, Weighted
// inference): a listwise-complete row enters with its weight when it is
// valid and positive and is dropped otherwise (never entering n); the
// weights are then mapped onto w* = w·N*/Σw (a multiply only under kind
// probability), so the working weights, the covariance (XᵀW*X)⁻¹ and
// the deviances are R glm(weights = w*). Unweighted, every prior weight
// is exactly 1. sumW / sumWSq are Σw and Σw² of the raw weights.
//
// keyOf, set under a robust covariance (robustKeyReader), reads each
// otherwise complete row's cluster / order_by key before its weight is
// judged — a null key drops the row like a null predictor — and keys
// returns them in row order; nil (a classical fit) leaves the design
// exactly as before and keys nil.
func buildWeightedDesign(records []Record, predictors []string, target string, rw rowWeights, keyOf func(Record) (robustRowKey, bool)) (xRows, yVec, prior []float64, keys []robustRowKey, sumW, sumWSq float64) {
	p := len(predictors)
	xRows = make([]float64, 0, len(records)*(p+1))
	yVec = make([]float64, 0, len(records))
	prior = make([]float64, 0, len(records))
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
		var key robustRowKey
		if keyOf != nil {
			if key, ok = keyOf(rec); !ok {
				continue
			}
		}
		w, ok := rw.of(rec)
		if !ok {
			continue
		}
		if keyOf != nil {
			keys = append(keys, key)
		}
		xRows = append(xRows, 1)
		xRows = append(xRows, xBuf...)
		yVec = append(yVec, y)
		prior = append(prior, w)
		sumW += w
		sumWSq += w * w
	}
	if c := rw.basis.Scale(sumW, sumWSq); c != 1 {
		for i := range prior {
			prior[i] *= c
		}
	}
	return xRows, yVec, prior, keys, sumW, sumWSq
}

// weightedMean is Σw·x / Σw — exactly mean(x) when every w is 1.
func weightedMean(x, w []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s, sw := 0.0, 0.0
	for i, v := range x {
		s += w[i] * v
		sw += w[i]
	}
	return s / sw
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
