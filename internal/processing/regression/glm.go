package regression

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/internal/regmargins"
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
// Gamma estimates it (glmFamily.EstimatesDispersion, reference-oracles
// E2-S3): φ̂ = Pearson χ² / (N* − k), Cov(β) = φ̂·(XᵀWX)⁻¹ and β/SE on a
// Student t with N* − k df — R's summary.glm on a Gamma glm.
//
// The negative binomial (negbin.go) estimates θ by alternating this loop
// with a Newton step for θ (MASS glm.nb), or holds RegressionSpec.Theta;
// its dispersion is 1, so it reads the binomial / poisson Wald path at θ̂.
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

// newGLMEngine validates the REG_GLM spec and constructs an engine; a
// spec with a Resample / Selection modifier gets the modifier wrapper.
// A penalty (l2 / l1 / elasticnet, binomial and poisson) rides the same
// IRLS loop (glm_penalty.go).
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
	// Margins over a derived predictor (no chain rule through it) — the
	// schema half of the rule predict applies too (internal/regmargins).
	if cerr := regmargins.CheckDerived(spec, schema); cerr != nil {
		return nil, cerr
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
	// Σw under frequency (weighting.Basis.FloorN): the expansion's
	// count; the rank check below answers a design of too few distinct
	// rows, as it does on the expanded rows.
	if rw.basis.FloorN(int64(n), sumW) < float64(p+1) {
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
	pr := &irlsProblem{
		X: xRows, Y: yVec, Prior: prior, K: p + 1,
		Columns:  append([]string{InterceptKey}, e.spec.Predictors...),
		Family:   e.family,
		MaxIters: maxIters, Tol: tol,
		WantInverse: true,
	}
	// A penalty's N* is Σw* (n unweighted): glmnet's −loglik/N*.
	pen := newGLMPenalty(e.spec, sum(prior))
	if pen != nil {
		pr.Penalty = pen
	}
	// The negative binomial estimates θ around the loop (negbin.go) and
	// leaves e.family at θ̂, so the dispersion, sandwich and null
	// deviance below read the variance and deviance at θ̂.
	var nb *negBinFit
	var sol *irlsSolution
	var err error
	if e.family.Name == reglink.NegativeBinomial {
		if nb, err = fitNegBin(pr, e.spec.Theta); err == nil {
			sol, e.family = nb.sol, nb.fam
		}
	} else {
		sol, err = irlsFit(pr)
	}
	if err != nil {
		return nil, err
	}
	if pen != nil {
		return e.penalizedResult(pen, sol, yVec, prior, sumW, sumWSq, rw)
	}
	beta, eta, mu := sol.Beta, sol.Eta, sol.Mu
	convergedIters := sol.Iters
	lastInvXtWX := sol.Inverse

	// The dispersion the classical covariance reads: 1 (binomial,
	// poisson), or gamma's estimate φ̂ = Pearson χ² / (N* − k) with the
	// Student-t df N* − k (estimatedDispersion). A robust covariance is
	// dispersion-free — φ cancels between bread and meat, as in R's
	// sandwich — and stays Wald z, so it never reads φ̂.
	phi, df, studentT := 1.0, 0.0, false
	if e.family.EstimatesDispersion && !e.spec.Covariance.Robust() {
		phi, df = e.estimatedDispersion(yVec, mu, prior, rw.basis.NStar(sumW, sumWSq))
		studentT = true
	}

	// Standard errors: diag of Cov(β) = φ·(XᵀWX)⁻¹ at converged W.
	// variances keeps the clamped, scaled diagonal so Vcov's diagonal is
	// exactly StdErrors².
	stdErrors := make([]float64, p+1)
	variances := make([]float64, p+1)
	for a := 0; a < p+1; a++ {
		v := lastInvXtWX.At(a, a)
		if v < 0 {
			v = 0
		}
		if studentT {
			v *= phi
		}
		variances[a] = v
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
	nullDeviance, pseudoR2 := e.nullDeviance(yVec, prior, deviance)

	// Package result. Coefficients[0] is the intercept; remaining
	// entries map to spec.Predictors in order. Wald-z p-values at
	// dispersion 1 (binomial / poisson, and every robust fit); gamma's
	// read a Student t on N* − k, R's summary.glm for an estimated
	// dispersion.
	pValue := func(coef, se float64) float64 {
		if studentT {
			return pValueForCoefficient(coef, se, df)
		}
		return waldZTwoSidedP(coef, se)
	}
	predictors := e.spec.Predictors
	coefMap := make(map[string]float64, p+1)
	seMap := make(map[string]float64, p+1)
	pMap := make(map[string]float64, p+1)

	coefMap[InterceptKey] = beta[0]
	seMap[InterceptKey] = stdErrors[0]
	pMap[InterceptKey] = pValue(beta[0], stdErrors[0])

	for i, name := range predictors {
		coefMap[name] = beta[i+1]
		seMap[name] = stdErrors[i+1]
		pMap[name] = pValue(beta[i+1], stdErrors[i+1])
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
	if nb != nil {
		attachNegBin(res, nb, yVec, mu, prior)
	}
	rw.note(res, sumW, sumWSq)
	// Intervals on the p-values' basis: Wald normal, or gamma's Student
	// t on N* − k (DF set, null when N* − k ≤ 0).
	attachIntervals(res, resolvedConfLevel(e.spec), df, studentT)
	// Cov(β) as the standard errors read it: the robust sandwich, or
	// φ·(XᵀWX)⁻¹ at the converged weights (φ = 1 but for gamma), its
	// diagonal the clamped variances.
	coefCov := func() [][]float64 {
		if robust != nil {
			return robust.fit.v
		}
		return symVcov(p+1, lastInvXtWX.At, phi, variances)
	}
	if e.spec.Margins != nil {
		// Marginal effects on the coefficient table's reference
		// distribution (margins.go).
		inf := marginsInference{level: resolvedConfLevel(e.spec), df: df, studentT: studentT}
		switch {
		case e.spec.Margins.EffectiveScale() == types.MarginsScaleLink || identityLink(e.family):
			var at []float64
			if e.spec.Margins.EffectiveType() == types.MarginsMEM {
				at = designMeans(xRows, prior, p+1)[1:]
			}
			res.Margins = coefficientMargins(e.spec, res, inf, at)
		default:
			res.Margins = glmMargins(e.spec, e.family, xRows, prior, eta, beta, coefCov(), inf)
		}
	}
	if robust != nil {
		res.Covariance = robust.fit.info
		if e.spec.Vcov {
			attachVcov(res, predictors, robust.fit.v)
		}
	} else if e.spec.Vcov {
		// Cov(β) = φ·(XᵀWX)⁻¹ at the converged weights (φ = 1 but for
		// gamma), its diagonal the clamped variances the standard errors
		// read.
		attachVcov(res, predictors, coefCov())
		if studentT {
			// The estimated φ̂ the vcov is scaled by (gamma).
			res.Dispersion = phi
		}
	}
	return res, nil
}

// estimatedDispersion is the gamma dispersion estimate R's summary.glm
// reads: the Pearson χ² Σ w*ᵢ·(yᵢ − μᵢ)²/V(μᵢ) over the fitted rows
// divided by the residual df N* − k (k = p + 1 coefficients). N* is Σw*
// — n unweighted, Σw under kind frequency (so the figure is the
// rep()-expanded glm's), Kish n_eff under kind probability (the residual
// df REG_OLS reads there; statsmodels GLM(freq_weights = w*)'s df_resid).
// Both are NaN when N* − k ≤ 0 (R's NaN dispersion on zero residual df),
// which makes every standard error, p-value and bound NaN — null on the
// wire.
func (e *glmEngine) estimatedDispersion(y, mu, prior []float64, nStar float64) (phi, df float64) {
	df = nStar - float64(len(e.spec.Predictors)+1)
	if !(df > 0) {
		return math.NaN(), math.NaN()
	}
	chi2 := 0.0
	for i := range y {
		r := y[i] - mu[i]
		chi2 += prior[i] * r * r / e.family.Variance(mu[i])
	}
	return chi2 / df, df
}

// nullDeviance returns the intercept-only model's deviance — μᵢ = ȳ
// (prior-weighted) for every row, the closed-form null intercept g(ȳ)
// under the canonical links — and the pseudo-R² 1 − D/D₀ (0 when D₀ is
// not positive).
func (e *glmEngine) nullDeviance(yVec, prior []float64, deviance float64) (float64, float64) {
	nullMu := e.family.SafeMean(weightedMean(yVec, prior))
	nullDeviance := 0.0
	for i := range yVec {
		nullDeviance += float64(prior[i] * e.family.Deviance(yVec[i], nullMu))
	}
	nullDeviance *= 2.0
	pseudoR2 := 0.0
	if nullDeviance > 0 {
		pseudoR2 = 1 - deviance/nullDeviance
	}
	return nullDeviance, pseudoR2
}

// penalizedResult packages a penalised fit (glm_penalty.go). The
// coefficients are the penalised estimate; the standard errors, Wald-z
// p-values and intervals read the penalised-Fisher sandwich
// H⁻¹·XᵀWX·H⁻¹, H = XᵀWX + λ₂·D, at the converged working weights for
// l2 (vcov allowed); for l1 / elasticnet they are NaN on every
// coefficient (null on the wire, key kept — the data-chosen active set
// has no analytical sampling distribution; vcov is refused up front),
// though the sandwich is still formed so a collapsed information matrix
// refuses exactly as before. Deviance, null deviance and
// pseudo-R² are the unpenalised figures at the penalised estimate.
func (e *glmEngine) penalizedResult(pen *glmPenalty, sol *irlsSolution, yVec, prior []float64, sumW, sumWSq float64, rw rowWeights) (*types.RegressionResult, error) {
	predictors := e.spec.Predictors
	k := len(predictors) + 1
	active, cov, ok := pen.covariance(k, sol.Gram, sol.Beta)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_NO_CONVERGE,
			"REG_GLM penalised fit: the penalised information matrix XᵀWX + λ·D is not positive-definite at the converged weights",
			map[string]any{
				"n": len(yVec), "p": k - 1,
				"family": e.family.Name, "link": e.family.Link,
				"penalty": pen.kind, "reason": "working_weights_collapsed",
			},
		)
	}
	keys := coefficientKeys(predictors)
	coefMap := make(map[string]float64, k)
	for j, key := range keys {
		coefMap[key] = sol.Beta[j]
	}
	seMap := make(map[string]float64, k)
	pMap := make(map[string]float64, k)
	if pen.approximateSE() {
		// l1 / elasticnet: the active set is chosen by the data, so no
		// analytical standard error is valid — NaN (null, key kept) on
		// every coefficient, the intercept included; the service attaches
		// PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED.
		for _, key := range keys {
			seMap[key], pMap[key] = math.NaN(), math.NaN()
		}
	} else {
		for u, j := range active {
			v := cov[u][u]
			if v < 0 {
				v = 0
			}
			se := math.Sqrt(v)
			seMap[keys[j]] = se
			pMap[keys[j]] = waldZTwoSidedP(sol.Beta[j], se)
		}
	}
	nullDeviance, pseudoR2 := e.nullDeviance(yVec, prior, sol.Deviance)
	res := &types.RegressionResult{
		Name:           e.spec.Name,
		Type:           types.REG_GLM,
		Family:         e.family.Name,
		Link:           e.family.Link,
		Penalty:        e.spec.Penalty,
		Alpha:          e.spec.Alpha,
		L1Ratio:        e.spec.L1Ratio,
		Coefficients:   coefMap,
		StdErrors:      seMap,
		PValues:        pMap,
		Deviance:       sol.Deviance,
		NullDeviance:   nullDeviance,
		PseudoR2:       pseudoR2,
		NObs:           len(yVec),
		ConvergedIters: sol.Iters,
	}
	rw.note(res, sumW, sumWSq)
	attachIntervals(res, resolvedConfLevel(e.spec), 0, false)
	if e.spec.Vcov && !pen.approximateSE() {
		attachVcov(res, predictors, cov)
	}
	if e.fitted != nil {
		e.fitted(sol.Eta, sol.Mu)
	}
	return res, nil
}

// sum is Σx.
func sum(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s
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
		y, ok := InputValue(rec, target)
		if !ok {
			continue
		}
		anyNull := false
		for j, name := range predictors {
			v, ok := InputValue(rec, name)
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
		sumWSq += float64(w * w)
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
		s += float64(w[i] * v)
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
		y, ok := InputValue(rec, target)
		if !ok {
			continue
		}
		anyNull := false
		for j, name := range predictors {
			v, ok := InputValue(rec, name)
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
