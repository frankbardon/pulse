package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// multinomial_penalty.go: the penalised REG_MULTINOMIAL fit (U68 E2-S3,
// #658, decision D6) — glmnet's symmetric multinomial with the
// per-coefficient ("ungrouped") elastic-net penalty. Every category k
// carries its own β_k (no reference: multinomialProblem.ref is −1, so
// every category is a block) and the fit minimises
//
//	−ℓ(B)/N* + α·Σ_j pf_j·[(1−l1_ratio)/2·Σ_k β_kj² + l1_ratio·Σ_k |β_kj|]
//
// with N* = Σw* (glmnet's objective is invariant to rescaling the
// weights), pf_j = 0 for the intercept and every `unpenalized`
// predictor, 1 otherwise, and no standardisation — glmnet(family =
// "multinomial", type.multinomial = "ungrouped", standardize = FALSE).
//
// ℓ is invariant to adding one constant to every category's
// coefficient on a term, so an unpenalised term (pf_j = 0) is fitted
// with the first category's coefficient held at 0 — the baseline
// problem on that term — and centred to mean zero over the categories
// afterwards, as glmnet reports it. A penalised term is pinned by the
// penalty: uniquely under l2 / elasticnet; under l1 any shift inside
// the coefficients' median interval leaves the objective unchanged, so
// the term is shifted to centre that interval on zero (glmnet's median
// centring: a no-op for an odd category count, whose median is 0 at
// the optimum).
//
// The fit is proximal Newton on N*·objective: each iteration solves the
// penalised quadratic model at the current B over the free
// coordinates —
//
//	min ½·zᵀIz − (Iβ + ∇ℓ)ᵀz + λ₂/2·Σ pf·z² + λ₁·Σ pf·|z|,
//	λ₂ = N*·α·(1−l1_ratio), λ₁ = N*·α·l1_ratio,
//
// I the information — in closed form for l2 (I + λ₂·D)z = Iβ + ∇ℓ, by
// cyclic coordinate descent finished by an exact active-set solve for
// l1 / elasticnet (glm_penalty.go's scheme with per-coordinate penalty
// factors) — then halves the step until the objective does not rise,
// stopping when the full step's relative change is at most tol. Near
// the optimum the step is Newton's on the active set, so convergence is
// quadratic and the estimate is the objective's minimiser to double
// precision.
//
// Inference: none analytically (regpenalty.Symmetric) — the intercepts
// are identified only by the centring and the l1 active set by the
// data — so the standard errors, p-values and intervals are NaN (null,
// key kept) and vcov is refused up front; under Resample the replicate
// figures replace them. Deviance, null deviance and pseudo-R² are the
// unpenalised figures at the penalised estimate; no likelihood figure
// (two_log_lik / aic) is reported, as for penalised REG_GLM.
//
// FMA-free: every product feeding a sum is barriered and every
// transcendental is detmath's (multinomialProblem's kernels).

// multinomialPenalty is one penalised multinomial fit's penalty.
type multinomialPenalty struct {
	kind    string
	alpha   float64
	l1Ratio float64
	// pen[a] reports whether term a (0 the intercept, then the
	// predictors in fit order) is penalised.
	pen []bool
}

// newMultinomialPenalty returns spec's penalty over its predictors, or
// nil for an unpenalised spec.
func newMultinomialPenalty(spec *types.RegressionSpec) *multinomialPenalty {
	if spec == nil || spec.Penalty == "" {
		return nil
	}
	p := &multinomialPenalty{kind: spec.Penalty, alpha: spec.Alpha}
	switch spec.Penalty {
	case "l1":
		p.l1Ratio = 1
	case "elasticnet":
		p.l1Ratio = spec.L1Ratio
	}
	mask := regpenalty.UnpenalizedMask(spec, spec.Predictors)
	p.pen = make([]bool, len(spec.Predictors)+1)
	for j := range spec.Predictors {
		p.pen[j+1] = mask == nil || !mask[j]
	}
	return p
}

// lambdas are λ₁ = N*·α·l1_ratio and λ₂ = N*·α·(1−l1_ratio).
func (p *multinomialPenalty) lambdas(nStar float64) (l1, l2 float64) {
	s := float64(nStar * p.alpha)
	return float64(s * p.l1Ratio), float64(s * (1 - p.l1Ratio))
}

// value is the penalty on N*·objective's scale: Σ pf·(λ₂/2·β² + λ₁·|β|)
// over every category's coefficients.
func (p *multinomialPenalty) value(beta []float64, q int, l1, l2 float64) float64 {
	var sq, abs float64
	for i, b := range beta {
		if p.pen[i%q] {
			sq += float64(b * b)
			abs += math.Abs(b)
		}
	}
	return float64(0.5*l2*sq) + float64(l1*abs)
}

const (
	// multinomialCDMaxSweeps caps the coordinate-descent sweeps of one
	// proximal-Newton iteration; the active-set finish normally ends the
	// descent long before.
	multinomialCDMaxSweeps = 100000
)

// fitPenalized runs proximal Newton from the intercept-only fit (the
// category shares' log-odds against the first category, slopes 0) and
// returns the centred estimate and the iteration count, or the
// SEPARATION / NO_CONVERGE refusal.
func (e *multinomialEngine) fitPenalized(pr *multinomialProblem, p *multinomialPenalty) ([]float64, int, error) {
	maxIters := e.spec.MaxIters
	if maxIters <= 0 {
		maxIters = defaultGLMMaxIters
	}
	tol := e.spec.Tol
	if tol <= 0 {
		tol = defaultGLMTol
	}
	q, d := pr.q, pr.dim()
	nStar := 0.0
	for _, w := range pr.prior {
		nStar += w
	}
	l1, l2 := p.lambdas(nStar)
	// The free coordinates: every one but the first category's
	// coefficient on an unpenalised term.
	free := make([]int, 0, d)
	for i := 0; i < d; i++ {
		if i >= q || p.pen[i] {
			free = append(free, i)
		}
	}
	beta := make([]float64, d)
	shares := pr.shares()
	log0 := detmath.Log(shares[0])
	for k := range pr.nonRef {
		beta[k*q] = detmath.Log(shares[k]) - log0
	}
	history := [][]float64{slices.Clone(beta)}
	objective := func(b []float64) float64 {
		return float64(-pr.logLik(b)) + p.value(b, q, l1, l2)
	}
	for iter := 1; iter <= maxIters; iter++ {
		ll, grad, info := pr.system(beta)
		f := float64(-ll) + p.value(beta, q, l1, l2)
		z, ok := p.solveModel(free, d, q, info, grad, beta, l1, l2)
		if !ok {
			return nil, iter, e.failure(pr, beta, history, iter, maxIters, tol, "information_singular")
		}
		if relativeChange(z, beta) <= tol {
			p.centre(z, q, len(pr.nonRef), l2)
			return z, iter, nil
		}
		step := make([]float64, d)
		for j := range step {
			step[j] = z[j] - beta[j]
		}
		next := z
		accepted := false
		for h := 0; h <= maxStepHalvings; h++ {
			if fNext := objective(next); !math.IsNaN(fNext) && fNext-f <= stepHalvingTol*(math.Abs(f)+0.1) {
				accepted = true
				break
			}
			next = make([]float64, d)
			for j := range next {
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

// solveModel minimises the penalised quadratic model at beta over the
// free coordinates (the others stay 0): A = info, b = info·β + ∇ℓ
// restricted to free. It returns the full-length solution, false when
// the l2 system cannot be factored.
func (p *multinomialPenalty) solveModel(free []int, d, q int, info, grad, beta []float64, l1, l2 float64) ([]float64, bool) {
	m := len(free)
	a := make([]float64, m*m)
	b := make([]float64, m)
	pen := make([]bool, m)
	start := make([]float64, m)
	for u, i := range free {
		row := info[i*d : (i+1)*d]
		r := grad[i]
		for c := 0; c < d; c++ {
			r += float64(row[c] * beta[c])
		}
		b[u] = r
		for v, j := range free {
			a[u*m+v] = row[j]
		}
		pen[u] = p.pen[i%q]
		start[u] = beta[i]
	}
	var sol []float64
	var ok bool
	if l1 == 0 {
		sol, ok = ridgeMasked(m, a, b, pen, l2)
	} else {
		sol, ok = descendMasked(m, a, b, start, pen, l1, l2)
	}
	if !ok {
		return nil, false
	}
	out := make([]float64, d)
	for u, i := range free {
		out[i] = sol[u]
	}
	return out, true
}

// ridgeMasked solves (A + λ₂·D)z = b, D the penalised coordinates'
// indicator.
func ridgeMasked(m int, a, b []float64, pen []bool, l2 float64) ([]float64, bool) {
	mat := append([]float64(nil), a...)
	for u := 0; u < m; u++ {
		if pen[u] {
			mat[u*m+u] += l2
		}
	}
	return solveEquilibrated(m, mat, b)
}

// descendMasked runs cyclic coordinate descent on ½zᵀAz − bᵀz +
// Σ pen·(λ₂/2·z² + λ₁·|z|) warm-started at start, finished by the exact
// active-set solve (activeSetMasked) once a sweep leaves the zero
// pattern unchanged.
func descendMasked(m int, a, b, start []float64, pen []bool, l1, l2 float64) ([]float64, bool) {
	z := append([]float64(nil), start...)
	prev := make([]bool, m)
	for sweep := 0; sweep < multinomialCDMaxSweeps; sweep++ {
		converged := true
		for j := 0; j < m; j++ {
			row := a[j*m : (j+1)*m]
			r := b[j]
			for c := 0; c < m; c++ {
				if c != j {
					r -= float64(row[c] * z[c])
				}
			}
			var next float64
			switch {
			case !pen[j]:
				if !(row[j] > 0) {
					return nil, false
				}
				next = r / row[j]
			case row[j]+l2 > 0:
				next = softThreshold(r, l1) / (row[j] + l2)
			}
			s := math.Sqrt(math.Max(row[j], 0))
			if s*math.Abs(next-z[j]) > glmCDTol*(1+float64(s*math.Abs(next))) {
				converged = false
			}
			z[j] = next
		}
		if converged {
			return z, true
		}
		stable := true
		for j := 0; j < m; j++ {
			nz := z[j] != 0
			if pen[j] && nz != prev[j] {
				stable = false
			}
			prev[j] = nz
		}
		if stable && sweep > 0 {
			if exact, ok := activeSetMasked(m, a, b, z, pen, l1, l2); ok {
				return exact, true
			}
		}
	}
	return z, true
}

// activeSetMasked solves the quadratic model restricted to the
// unpenalised coordinates and the penalised ones nonzero in z, their
// signs fixed, returning the full-length solution when it is a KKT
// point of the whole model.
func activeSetMasked(m int, a, b, z []float64, pen []bool, l1, l2 float64) ([]float64, bool) {
	active := make([]int, 0, m)
	for j := 0; j < m; j++ {
		if !pen[j] || z[j] != 0 {
			active = append(active, j)
		}
	}
	n := len(active)
	mat := make([]float64, n*n)
	rhs := make([]float64, n)
	for u, ju := range active {
		for v, jv := range active {
			mat[u*n+v] = a[ju*m+jv]
		}
		rhs[u] = b[ju]
		if pen[ju] {
			mat[u*n+u] += l2
			rhs[u] -= float64(l1 * sign(z[ju]))
		}
	}
	sol, ok := solveEquilibrated(n, mat, rhs)
	if !ok {
		return nil, false
	}
	out := make([]float64, m)
	for u, ju := range active {
		if pen[ju] && sol[u]*sign(z[ju]) <= 0 {
			return nil, false
		}
		out[ju] = sol[u]
	}
	for j := 0; j < m; j++ {
		if !pen[j] || out[j] != 0 {
			continue
		}
		r := b[j]
		for _, c := range active {
			r -= float64(a[j*m+c] * out[c])
		}
		if math.Abs(r) > l1*(1+glmKKTSlack) {
			return nil, false
		}
	}
	return out, true
}

// centre puts beta (K category blocks of q terms) in glmnet's reported
// form: an unpenalised term's coefficients sum to zero over the
// categories; under pure l1 a penalised term's median interval is
// centred on zero.
func (p *multinomialPenalty) centre(beta []float64, q, k int, l2 float64) {
	vals := make([]float64, k)
	for a := 0; a < q; a++ {
		for c := 0; c < k; c++ {
			vals[c] = beta[c*q+a]
		}
		var shift float64
		switch {
		case !p.pen[a]:
			for _, v := range vals {
				shift += v
			}
			shift /= float64(k)
		case l2 == 0:
			slices.Sort(vals)
			if k%2 == 1 {
				shift = vals[k/2]
			} else {
				shift = 0.5 * (vals[k/2-1] + vals[k/2])
			}
		}
		if shift != 0 {
			for c := 0; c < k; c++ {
				beta[c*q+a] -= shift
			}
		}
	}
}

// penalizedResult is the penalised fit's result over pr: the symmetric
// coefficients keyed "<category>:<term>" for every category, NaN
// standard errors / p-values / intervals (InferenceUndefined; the
// service attaches the advisory), the unpenalised deviance at the
// estimate, the null deviance and pseudo-R²; no reference, no
// likelihood figure.
func (e *multinomialEngine) penalizedResult(pr *multinomialProblem, p *multinomialPenalty, rw rowWeights, sumW, sumWSq float64) (*types.RegressionResult, []string, error) {
	beta, iters, err := e.fitPenalized(pr, p)
	if err != nil {
		return nil, nil, err
	}
	keys := e.keys(pr)
	coefs := make(map[string]float64, len(keys))
	ses := make(map[string]float64, len(keys))
	ps := make(map[string]float64, len(keys))
	for j, key := range keys {
		coefs[key], ses[key], ps[key] = beta[j], math.NaN(), math.NaN()
	}
	deviance := float64(-2 * pr.logLik(beta))
	nullDev := pr.nullDeviance()
	pseudo := 0.0
	if nullDev > 0 {
		pseudo = 1 - deviance/nullDev
	}
	res := &types.RegressionResult{
		Name:           e.spec.Name,
		Type:           types.REG_MULTINOMIAL,
		Categories:     slices.Clone(pr.cats),
		Penalty:        e.spec.Penalty,
		Alpha:          e.spec.Alpha,
		L1Ratio:        e.spec.L1Ratio,
		Coefficients:   coefs,
		StdErrors:      ses,
		PValues:        ps,
		Deviance:       deviance,
		NullDeviance:   nullDev,
		PseudoR2:       pseudo,
		NObs:           pr.n,
		ConvergedIters: iters,
	}
	rw.note(res, sumW, sumWSq)
	attachIntervals(res, resolvedConfLevel(e.spec), 0, false)
	if e.spec.Vcov {
		// regpenalty.VcovRefusal refuses it up front.
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"REG_MULTINOMIAL penalised fit reached vcov", map[string]any{"penalty": p.kind})
	}
	return res, keys, nil
}
