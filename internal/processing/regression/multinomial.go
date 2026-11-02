package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regmultinom"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// multinomial.go: REG_MULTINOMIAL, the baseline-category (multinomial)
// logit of a categorical target (U65 E4-S2) — R's nnet::multinom on
// the exact maximum-likelihood estimate.
//
// With L fitted levels, the reference r and the predictors x_i
// (intercept first), each non-reference level k has its own β_k:
//
//	η_ik = x_iᵀβ_k (η_ir = 0),  π_ik = exp(η_ik) / Σ_l exp(η_il)
//	ℓ    = Σ_i w*_i · log π_{i, y_i}
//
// The fit is Newton-Raphson on ℓ (the multinomial logit is canonical,
// so the observed and expected information agree): from the
// intercept-only MLE (intercepts log(share_k / share_r), slopes 0) each
// step solves I·Δ = ∇ℓ on the K(p+1)-square information
//
//	I_(k,a),(l,b) = Σ_i w*_i · π_ik(δ_kl − π_il) · x_ia · x_ib
//
// halving a step that lowers ℓ, until the step's relative change in β
// is at most tol (the IRLS rule). Convergence is quadratic, so the β a
// tol-1e-8 stop returns sits at the MLE to double precision; nnet's
// BFGS stops some 1e-8 short (the oracle polishes it the same way).
// Cov(β) = I⁻¹ at β̂ (multinom's solve(Hessian)), the standard errors
// its diagonal's root, Wald z p-values and normal intervals.
//
// Categories: the target's dictionary order; a level no fitted row
// carries is dropped (multinom's "group is empty"). The reference is
// RegressionSpec.Reference or the first fitted level. Coefficient keys
// are "<level>:<term>" (regmultinom.Key), category-major in that order
// — nnet's vcov row order.
//
// Weights (.claude/reference/weighting.md, Weighted inference): REG_GLM's
// prior-weight rule — w* = w·N*/Σw, so frequency weights give the
// rep() expansion's fit, probability weights multinom(weights = w*) —
// with no likelihood figure under probability weights
// (logLikDefined). Deviance = −2ℓ and the null deviance −2Σw*·log share
// are reported on w* under both.
//
// FMA-free: every product feeding a sum is barriered, every
// transcendental is detmath's, so the fit is bit-identical on every
// GOARCH and across DecodeWorkers / ShardWorkers (the buffered rows
// arrive in record order).
type multinomialEngine struct {
	spec   *types.RegressionSpec
	levels []string // the target's dictionary, in order

	finalized bool

	// fitted, when set, receives the converged fitted probabilities,
	// row-major n × len(categories) over the result's Categories, of the
	// rows that entered the fit in record order. A test seam (the
	// oracle's fitted probabilities); nil in every production fit.
	fitted func(categories []string, probs []float64)
}

// newMultinomialEngine validates a REG_MULTINOMIAL spec against schema
// (regmultinom.Check, the shared ValidateRegression rules, then the
// target's dictionary: regmultinom.CheckTarget — the rules predict
// applies) and constructs the engine.
func newMultinomialEngine(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error) {
	if spec.Type != types.REG_MULTINOMIAL {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"multinomialEngine constructed for a non-multinomial spec", map[string]any{"type": string(spec.Type)})
	}
	if err := ValidateRegression(spec); err != nil {
		return nil, err
	}
	if len(spec.Predictors) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"REG_MULTINOMIAL requires at least one predictor", map[string]any{"name": spec.Name})
	}
	if spec.Target == "" {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"REG_MULTINOMIAL requires a Target field", map[string]any{"name": spec.Name})
	}
	if cerr := regmultinom.CheckTarget(spec, schema); cerr != nil {
		return nil, cerr
	}
	levels, _ := regmultinom.Levels(schema, spec.Target)
	return &multinomialEngine{spec: spec, levels: levels}, nil
}

// Fit returns INSUFFICIENT_DATA: the fit is buffered-only.
func (e *multinomialEngine) Fit() (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "multinomialEngine.Fit called after Finalize")
	}
	e.finalized = true
	return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
		"REG_MULTINOMIAL requires the buffered orchestrator path; engine received no records",
		map[string]any{"name": e.spec.Name, "type": string(e.spec.Type)})
}

// multinomialProblem is one fit's rows: the design X (n × q row-major,
// intercept first), each row's category index into cats, the prior
// weights w*, and the fitted categories with the reference's index.
type multinomialProblem struct {
	x     []float64
	y     []int
	prior []float64
	n, q  int
	cats  []string
	ref   int
	// nonRef maps a non-reference block k to its category index.
	nonRef []int
}

func (pr *multinomialProblem) dim() int { return len(pr.nonRef) * pr.q }

// rowEval writes row i's per-category probabilities over cats into pi
// and returns its log-likelihood log π_{i, y_i}. eta is scratch of
// length len(cats).
func (pr *multinomialProblem) rowEval(beta []float64, i int, eta, pi []float64) float64 {
	q := pr.q
	xi := pr.x[i*q : (i+1)*q]
	m := 0.0
	for c := range eta {
		eta[c] = 0
	}
	for k, c := range pr.nonRef {
		b := beta[k*q : (k+1)*q]
		eta[c] = LinearPredictor(b[0], b[1:], xi[1:])
		if eta[c] > m {
			m = eta[c]
		}
	}
	s := 0.0
	for c := range eta {
		pi[c] = detmath.Exp(eta[c] - m)
		s += pi[c]
	}
	for c := range pi {
		pi[c] /= s
	}
	return eta[pr.y[i]] - m - detmath.Log(s)
}

// logLik is ℓ = Σ w*·log π_{i,y_i} at beta (−Inf when a row's
// probability underflows).
func (pr *multinomialProblem) logLik(beta []float64) float64 {
	eta := make([]float64, len(pr.cats))
	pi := make([]float64, len(pr.cats))
	ll := 0.0
	for i := 0; i < pr.n; i++ {
		ll += float64(pr.prior[i] * pr.rowEval(beta, i, eta, pi))
	}
	return ll
}

// system returns ℓ, the score ∇ℓ and the information I (row-major,
// both triangles) at beta; probs, when non-nil (n × len(cats)), receives
// the fitted probabilities.
func (pr *multinomialProblem) system(beta, probs []float64) (ll float64, grad, info []float64) {
	q, kk, d := pr.q, len(pr.nonRef), pr.dim()
	grad = make([]float64, d)
	info = make([]float64, d*d)
	eta := make([]float64, len(pr.cats))
	pi := make([]float64, len(pr.cats))
	c := make([]float64, kk*kk)
	for i := 0; i < pr.n; i++ {
		w := pr.prior[i]
		ll += float64(w * pr.rowEval(beta, i, eta, pi))
		if probs != nil {
			copy(probs[i*len(pr.cats):], pi)
		}
		xi := pr.x[i*q : (i+1)*q]
		for k, ck := range pr.nonRef {
			yk := 0.0
			if pr.y[i] == ck {
				yk = 1
			}
			r := float64(w * (yk - pi[ck]))
			for a := 0; a < q; a++ {
				grad[k*q+a] += float64(r * xi[a])
			}
			wp := float64(w * pi[ck])
			for l := k; l < kk; l++ {
				cl := pr.nonRef[l]
				if l == k {
					c[k*kk+l] = float64(wp * (1 - pi[cl]))
				} else {
					c[k*kk+l] = -float64(wp * pi[cl])
				}
			}
		}
		for k := 0; k < kk; k++ {
			for l := k; l < kk; l++ {
				ckl := c[k*kk+l]
				for a := 0; a < q; a++ {
					ca := float64(ckl * xi[a])
					row := (k*q + a) * d
					bStart := 0
					if l == k {
						bStart = a
					}
					for b := bStart; b < q; b++ {
						info[row+l*q+b] += float64(ca * xi[b])
					}
				}
			}
		}
	}
	// Mirror the upper triangle: the loop above filled every (i, j ≥ i)
	// — the off-diagonal blocks k < l whole, the diagonal blocks b ≥ a.
	for i := 0; i < d; i++ {
		for j := i + 1; j < d; j++ {
			info[j*d+i] = info[i*d+j]
		}
	}
	return ll, grad, info
}

// FitBuffered builds the design (listwise null deletion, the target read
// as its categorical label), then runs Newton-Raphson to the MLE.
func (e *multinomialEngine) FitBuffered(records []Record) (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "multinomialEngine.FitBuffered called after Finalize")
	}
	e.finalized = true
	spec := e.spec
	p := len(spec.Predictors)
	q := p + 1
	rw := newRowWeights(spec)

	x := make([]float64, 0, len(records)*q)
	labels := make([]string, 0, len(records))
	prior := make([]float64, 0, len(records))
	xBuf := make([]float64, p)
	sumW, sumWSq := 0.0, 0.0
	for _, rec := range records {
		label, ok, err := targetLabel(rec, spec.Target)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		complete := true
		for j, name := range spec.Predictors {
			v, ok := InputValue(rec, name)
			if !ok {
				complete = false
				break
			}
			xBuf[j] = v
		}
		if !complete {
			continue
		}
		w, ok := rw.of(rec)
		if !ok {
			continue
		}
		x = append(x, 1)
		x = append(x, xBuf...)
		labels = append(labels, label)
		prior = append(prior, w)
		sumW += w
		sumWSq += float64(w * w)
	}
	if c := rw.basis.Scale(sumW, sumWSq); c != 1 {
		for i := range prior {
			prior[i] *= c
		}
	}
	n := len(labels)
	if rw.basis.FloorN(int64(n), sumW) < float64(q) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"REG_MULTINOMIAL requires n ≥ p + 1 observations after listwise null deletion",
			map[string]any{"n": n, "p": p, "required": q})
	}

	pr, err := e.problem(x, labels, prior, n, q)
	if err != nil {
		return nil, err
	}
	if aliased := aliasedColumns(x, prior, n, q); len(aliased) > 0 {
		names := make([]string, len(aliased))
		for i, j := range aliased {
			names[i] = InterceptKey
			if j > 0 {
				names[i] = spec.Predictors[j-1]
			}
		}
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"REG_MULTINOMIAL design matrix is rank deficient: a column is a linear combination of the columns before it (collinear predictors, or a constant predictor aliased with the intercept); drop it",
			map[string]any{"columns": names, "n": n, "p": p})
	}

	beta, iters, err := e.newton(pr)
	if err != nil {
		return nil, err
	}
	var probs []float64
	if e.fitted != nil {
		probs = make([]float64, n*len(pr.cats))
	}
	ll, _, info := pr.system(beta, probs)
	if e.fitted != nil {
		e.fitted(slices.Clone(pr.cats), probs)
	}
	d := pr.dim()
	f, ok := factorSPD(d, info)
	if !ok {
		return nil, e.noConverge(pr, iters, "information_singular")
	}
	inv, ierr := f.Inverse()
	if ierr != nil {
		return nil, e.noConverge(pr, iters, "information_singular")
	}

	keys := e.keys(pr)
	coefs := make(map[string]float64, d)
	ses := make(map[string]float64, d)
	ps := make(map[string]float64, d)
	variances := make([]float64, d)
	for j, key := range keys {
		v := inv.At(j, j)
		if v < 0 {
			v = 0
		}
		variances[j] = v
		se := math.Sqrt(v)
		coefs[key], ses[key] = beta[j], se
		ps[key] = waldZTwoSidedP(beta[j], se)
	}
	deviance := float64(-2 * ll)
	nullDev := pr.nullDeviance()
	pseudo := 0.0
	if nullDev > 0 {
		pseudo = 1 - deviance/nullDev
	}
	ref := pr.cats[pr.ref]
	res := &types.RegressionResult{
		Name:           spec.Name,
		Type:           types.REG_MULTINOMIAL,
		Reference:      ref,
		Categories:     slices.Clone(pr.cats),
		Coefficients:   coefs,
		StdErrors:      ses,
		PValues:        ps,
		Deviance:       deviance,
		NullDeviance:   nullDev,
		PseudoR2:       pseudo,
		NObs:           n,
		ConvergedIters: iters,
	}
	if logLikDefined(rw.basis) {
		// multinom's AIC: deviance + 2·edf, edf = K·(p + 1).
		attachLogLik(res, ll, d)
	}
	rw.note(res, sumW, sumWSq)
	attachIntervals(res, resolvedConfLevel(spec), 0, false)
	if spec.Vcov {
		AttachVcovKeys(res, keys, symVcov(d, inv.At, 1, variances))
	}
	return res, nil
}

// targetLabel reads rec's target label: its categorical dictionary
// label (a corrupt code is the record's ENCODING_INVALID, returned as
// the fit's error, so the slot fails alone), ok=false when null.
func targetLabel(rec Record, target string) (string, bool, error) {
	lr, ok := rec.(LabeledRecord)
	if !ok {
		return "", false, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"REG_MULTINOMIAL needs labelled records to read its categorical target", map[string]any{"target": target})
	}
	return lr.StringValueChecked(target)
}

// problem orders the fitted categories (the dictionary order; a label
// outside the dictionary — impossible for a cohort field — after it,
// sorted), drops the levels no row carries, resolves the reference and
// indexes every row's category.
func (e *multinomialEngine) problem(x []float64, labels []string, prior []float64, n, q int) (*multinomialProblem, error) {
	seen := map[string]bool{}
	for _, l := range labels {
		seen[l] = true
	}
	var cats []string
	for _, l := range e.levels {
		if seen[l] {
			cats = append(cats, l)
			delete(seen, l)
		}
	}
	extra := make([]string, 0, len(seen))
	for l := range seen {
		extra = append(extra, l)
	}
	slices.Sort(extra)
	cats = append(cats, extra...)
	if len(cats) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"REG_MULTINOMIAL needs at least two target levels among the fitted rows",
			map[string]any{"n": n, "categories": cats, "reason": "one_category"})
	}
	ref := 0
	if e.spec.Reference != "" {
		ref = slices.Index(cats, e.spec.Reference)
		if ref < 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
				"REG_MULTINOMIAL reference "+e.spec.Reference+" is carried by no fitted row; name a fitted level as reference",
				map[string]any{"reference": e.spec.Reference, "categories": cats, "reason": "reference_empty"})
		}
	}
	index := make(map[string]int, len(cats))
	for i, c := range cats {
		index[c] = i
	}
	y := make([]int, n)
	for i, l := range labels {
		y[i] = index[l]
	}
	nonRef := make([]int, 0, len(cats)-1)
	for c := range cats {
		if c != ref {
			nonRef = append(nonRef, c)
		}
	}
	return &multinomialProblem{x: x, y: y, prior: prior, n: n, q: q, cats: cats, ref: ref, nonRef: nonRef}, nil
}

// shares are the w*-weighted category shares.
func (pr *multinomialProblem) shares() []float64 {
	s := make([]float64, len(pr.cats))
	total := 0.0
	for i, c := range pr.y {
		s[c] += pr.prior[i]
		total += pr.prior[i]
	}
	for c := range s {
		s[c] /= total
	}
	return s
}

// nullDeviance is the intercept-only model's −2ℓ: every row at the
// category shares.
func (pr *multinomialProblem) nullDeviance() float64 {
	logShare := pr.shares()
	for c := range logShare {
		logShare[c] = detmath.Log(logShare[c])
	}
	ll := 0.0
	for i, c := range pr.y {
		ll += float64(pr.prior[i] * logShare[c])
	}
	return float64(-2 * ll)
}

// keys are the coefficient keys, category-major.
func (e *multinomialEngine) keys(pr *multinomialProblem) []string {
	terms := append([]string{InterceptKey}, e.spec.Predictors...)
	out := make([]string, 0, pr.dim())
	for _, c := range pr.nonRef {
		for _, t := range terms {
			out = append(out, regmultinom.Key(pr.cats[c], t))
		}
	}
	return out
}

// newton runs Newton-Raphson from the intercept-only MLE; it returns β̂
// and the iteration count, or the SEPARATION / NO_CONVERGE refusal.
func (e *multinomialEngine) newton(pr *multinomialProblem) ([]float64, int, error) {
	maxIters := e.spec.MaxIters
	if maxIters <= 0 {
		maxIters = defaultGLMMaxIters
	}
	tol := e.spec.Tol
	if tol <= 0 {
		tol = defaultGLMTol
	}
	d, q := pr.dim(), pr.q
	beta := make([]float64, d)
	shares := pr.shares()
	logRef := detmath.Log(shares[pr.ref])
	for k, c := range pr.nonRef {
		beta[k*q] = detmath.Log(shares[c]) - logRef
	}
	history := [][]float64{slices.Clone(beta)}
	for iter := 1; iter <= maxIters; iter++ {
		ll, grad, info := pr.system(beta, nil)
		f, ok := factorSPD(d, info)
		if !ok {
			return nil, iter, e.failure(pr, beta, history, iter, maxIters, tol, "information_singular")
		}
		step, err := solveSPD(f, grad)
		if err != nil {
			return nil, iter, e.failure(pr, beta, history, iter, maxIters, tol, "information_singular")
		}
		next := make([]float64, d)
		for j := range next {
			next[j] = beta[j] + step[j]
		}
		// Convergence is judged on the full step, before any halving, so
		// a round-off rise at the optimum never stalls an arrived fit.
		if relativeChange(next, beta) <= tol {
			return next, iter, nil
		}
		accepted := false
		for h := 0; h <= maxStepHalvings; h++ {
			if llNext := pr.logLik(next); !math.IsNaN(llNext) && llNext-ll >= -stepHalvingTol*(math.Abs(ll)+0.1) {
				accepted = true
				break
			}
			for j := range next {
				// The halving is a multiply by 0.5 to the compiler:
				// barriered, so it never fuses into the sum.
				step[j] = float64(step[j] * 0.5)
				next[j] = beta[j] + step[j]
			}
		}
		if !accepted {
			return nil, iter, e.failure(pr, beta, history, iter, maxIters, tol, "step_halving")
		}
		beta = next
		history = append(history, slices.Clone(beta))
	}
	return nil, maxIters, e.failure(pr, beta, history, maxIters, maxIters, tol, "max_iters")
}

// failure classifies a fit that stopped short: SEPARATION when a row's
// fitted probability of its own level is numerically 1 while a slope
// keeps growing (the MLE lies at infinity), else NO_CONVERGE.
func (e *multinomialEngine) failure(pr *multinomialProblem, beta []float64, history [][]float64, iters, maxIters int, tol float64, reason string) error {
	eta := make([]float64, len(pr.cats))
	pi := make([]float64, len(pr.cats))
	boundary := 0
	for i := 0; i < pr.n; i++ {
		pr.rowEval(beta, i, eta, pi)
		if pi[pr.y[i]] > 1-separationMuEps {
			boundary++
		}
	}
	keys := e.keys(pr)
	var growing []string
	for _, j := range growingPredictors(history, pr.dim()) {
		if j%pr.q != 0 {
			growing = append(growing, keys[j])
		}
	}
	if boundary > 0 && len(growing) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_SEPARATION,
			"REG_MULTINOMIAL fit is separated: fitted probabilities are numerically 0 or 1 while the named coefficients keep growing, so the maximum-likelihood estimate does not exist (it lies at infinity) and no coefficient is reported",
			map[string]any{"coefficients": growing, "n": pr.n, "p": pr.q - 1, "n_boundary": boundary, "iters": iters})
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_NO_CONVERGE,
		"REG_MULTINOMIAL Newton-Raphson failed to converge within MaxIters",
		map[string]any{"n": pr.n, "p": pr.q - 1, "max_iters": maxIters, "tol": tol, "converged_iters": iters, "reason": reason})
}

// noConverge is the refusal of a converged β whose information is not
// invertible.
func (e *multinomialEngine) noConverge(pr *multinomialProblem, iters int, reason string) error {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_NO_CONVERGE,
		"REG_MULTINOMIAL information matrix is not positive definite at the estimate",
		map[string]any{"n": pr.n, "p": pr.q - 1, "converged_iters": iters, "reason": reason})
}
