package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// The ONE weighted IRLS loop behind REG_GLM: glmEngine.FitBuffered (the
// plain path, prior weights from the slot's weight) and glmFitter (the
// resample / selection refits, every prior weight 1) both call irlsFit.
// It follows R's glm.fit (stats 4.6) where R is the oracle, and departs
// from it where Pulse refuses rather than misleads:
//
//  1. Up-front rank check. A pivot-free QR (modified Gram-Schmidt,
//     re-orthogonalised) of the prior-weighted design √w*·X runs ONCE,
//     before any iteration; a column whose residual norm falls to
//     rankTol of its own norm is aliased with the columns before it and
//     the fit is refused PROCESSING_REGRESSION_RANK_DEFICIENT naming
//     those columns. That is the ONLY place RANK_DEFICIENT is raised: a
//     working Gram that stops being positive-definite mid-loop is the
//     working weights collapsing (μ at its boundary), never collinearity.
//  2. Per-row starting values (R's family `initialize`): μ⁰ᵢ = the
//     family's MuStart(yᵢ, w*ᵢ) — binomial (w·y + 0.5)/(w + 1), poisson
//     y + 0.1, gamma y — and η⁰ᵢ = g(μ⁰ᵢ). No starting β exists.
//  3. Each iteration: working weights Wᵢ = w*ᵢ·μ'(ηᵢ)²/V(μᵢ), working
//     response zᵢ = ηᵢ + (yᵢ − μᵢ)/μ'(ηᵢ), β_new solves (XᵀWX)β = XᵀWz
//     (Cholesky on the exactly power-of-two-equilibrated Gram, or the
//     penalty's own solve — glmPenalty's penalised working least
//     squares).
//  4. Converged when a FULL (un-halved) step moves β by a relative
//     ||Δβ|| / (||β|| + ε) < tol (to a finite objective and a valid η)
//     — checked before halving; a halved step is short by construction
//     and never counts.
//  5. Step-halving (glm2's rule, a superset of R's, which halves only a
//     non-finite deviance or an invalid η): from the second iteration
//     on, while the candidate's objective (deviance + penalty) is
//     non-finite, its η invalid for the family, or the objective did not
//     decrease (up to the round-off allowance stepHalvingTol), β_new
//     moves halfway back toward the previous β — at most
//     maxStepHalvings times; exhaustion stops the fit (step 6). An η
//     is invalid where the link's μ leaves the family's range
//     (glmFamily.ValidEta: gamma inverse, and the identity / sqrt
//     links of poisson and gamma — R's valideta / validmu). Under a
//     non-canonical link a FIRST step that lands invalid (no previous
//     β; R's glm.fit gives up there) is halved toward the
//     intercept-only fit (g(ȳ), 0, …) instead; the canonical links keep
//     their first step as before. Every working weight and response
//     reads μ'(η) and V(μ) (step 3), never a canonical shortcut, so the
//     fixed point is the MLE under any link — Fisher scoring, which
//     converges linearly rather than quadratically off the canonical
//     link.
//  6. A fit that stops without converging (the iteration cap, a
//     collapsed working Gram, halving exhausted) is classified: a
//     binomial fit with fitted probabilities numerically 0 or 1 while a
//     predictor's coefficient keeps growing is SEPARATED —
//     PROCESSING_REGRESSION_SEPARATION naming those predictors (the MLE
//     lies at infinity; the coefficients are wherever the loop stopped).
//     Anything else is PROCESSING_REGRESSION_NO_CONVERGE.

const (
	// maxStepHalvings bounds the halvings of one iteration's step
	// (2⁻³⁰ ≈ 1e-9 of the full step).
	maxStepHalvings = 30

	// stepHalvingTol is the round-off allowance of "the objective
	// decreased": a step stands when objective − previous ≤
	// stepHalvingTol·(|objective| + 0.1). Far tighter than glm2's 1e-8 —
	// a looser allowance lets an overshooting step through as long as
	// its rise is small, so the loop can oscillate without converging —
	// yet above the summation noise of a deviance over ~10⁸ rows
	// (√n·eps). Convergence is checked on the full step BEFORE any
	// halving, so noise-level rises never stall an arrived fit.
	stepHalvingTol = 1e-12

	// rankTol is the up-front rank check's relative residual-norm
	// threshold (R lm's qr tolerance).
	rankTol = 1e-7

	// separationMuEps: a fitted probability below it or above 1 − it is
	// "numerically 0 or 1" for this engine (the binomial family's
	// binomialEps). It is only the separation EVIDENCE threshold, judged
	// on a fit that already stopped short: a converged fit may carry
	// fitted probabilities far beyond it (the variance and working
	// weights are R's, unclamped). R warns at 10·eps; a bound that tight
	// would miss separations this loop stops on before its fitted
	// probabilities get there.
	separationMuEps = binomialEps

	// separationGrowthSteps: a predictor "keeps growing" when |β_j|
	// strictly rose, with its sign unchanged, over each of this many
	// consecutive accepted iterations ending at the stop, its last
	// increment at least separationMinGrowthRatio of the one before — a
	// separated coefficient climbs by a near-constant amount per
	// iteration (Newton on e^{-t} steps t by 1), a converging one by
	// quadratically shrinking amounts.
	separationGrowthSteps    = 3
	separationMinGrowthRatio = 0.5
)

// irlsPenalty is the hook a penalised fit plugs into the loop —
// glmPenalty (glm_penalty.go) for REG_GLM's l2 / l1 / elasticnet; nil is
// the unpenalised MLE. solve replaces the Cholesky
// step on the working normal equations — xtwx is the row-major k × k
// XᵀWX, xtwz the XᵀWz, beta the previous β (nil on the first
// iteration) — reporting false when it cannot produce a step, and
// returning a FRESH slice (the loop keeps it in its β history and halves
// it in place); value is the penalty term on the deviance scale, added
// to the objective step-halving monitors.
type irlsPenalty interface {
	solve(k int, xtwx, xtwz, beta []float64) ([]float64, bool)
	value(beta []float64) float64
}

// irlsProblem is one IRLS fit's input.
type irlsProblem struct {
	// X is the row-major n × K design, column 0 the intercept's 1.
	X []float64
	Y []float64
	// Prior holds the prior weights w* (len n); nil means every row
	// weighs exactly 1.
	Prior []float64
	K     int
	// Columns names the K design columns for refusal details
	// (Columns[0] = InterceptKey).
	Columns  []string
	Family   glmFamily
	MaxIters int // <= 0: defaultGLMMaxIters
	Tol      float64
	// Penalty, when set, penalises the fit (see irlsPenalty).
	Penalty irlsPenalty
	// Start, when set (length K), warm-starts the loop at η = X·Start
	// instead of the family's per-row starting values — a jackknife
	// replicate starts from the full-data fit, a few iterations from
	// its own. A start whose objective or η is invalid for the family
	// falls back to the per-row start. The converged fit is the same
	// fixed point either way (to the convergence tolerance), not the
	// same bits.
	Start []float64
	// WantInverse asks for (XᵀWX)⁻¹ at the converged working weights.
	WantInverse bool
	// WantWeights, beside WantInverse, also returns those working weights
	// per row (irlsSolution.Weights): the collinearity block's model
	// basis reads them.
	WantWeights bool

	// onStep, when set, sees every accepted iteration: its 0-based
	// index, the objective at the accepted β and the halvings it took.
	// A test seam; nil in production.
	onStep func(iter int, objective float64, halvings int)
}

// irlsSolution is a converged fit.
type irlsSolution struct {
	Beta []float64 // length K
	Eta  []float64 // η = Xβ per row, through LinearPredictor
	Mu   []float64 // μ = g⁻¹(η)
	// Iters is the number of IRLS iterations run.
	Iters int
	// Inverse is (XᵀWX)⁻¹ at the converged weights; nil unless
	// WantInverse, and nil on a penalised fit.
	Inverse *linalg.Sym
	// Gram is XᵀWX (row-major K × K, both triangles) at the converged
	// weights; nil unless WantInverse.
	Gram []float64
	// Weights are the working weights W (prior × μ'(η)²/V(μ)) per row at
	// the converged β — the ones Gram is formed with; nil unless
	// WantInverse and WantWeights.
	Weights []float64
	// Deviance is 2·Σ w*ᵢ·d(yᵢ, μᵢ) at Beta.
	Deviance float64
}

// irlsFit runs the weighted IRLS loop to convergence (see the file
// comment). Errors: PROCESSING_REGRESSION_RANK_DEFICIENT (up front
// only), PROCESSING_REGRESSION_SEPARATION, PROCESSING_REGRESSION_NO_CONVERGE.
func irlsFit(pr *irlsProblem) (*irlsSolution, error) {
	n, k, fam := len(pr.Y), pr.K, pr.Family
	maxIters := pr.MaxIters
	if maxIters <= 0 {
		maxIters = defaultGLMMaxIters
	}
	tol := pr.Tol
	if tol <= 0 {
		tol = defaultGLMTol
	}
	prior := pr.Prior
	if prior == nil {
		prior = make([]float64, n)
		for i := range prior {
			prior[i] = 1
		}
	}

	if aliased := aliasedColumns(pr.X, prior, n, k); len(aliased) > 0 {
		names := make([]string, len(aliased))
		for i, j := range aliased {
			names[i] = pr.columnName(j)
		}
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"REG_GLM design matrix is rank deficient: a column is a linear combination of the columns before it (collinear predictors, or a constant predictor aliased with the intercept); drop it",
			map[string]any{"columns": names, "n": n, "p": k - 1, "family": fam.Name, "link": fam.Link},
		)
	}

	eta := make([]float64, n)
	mu := make([]float64, n)
	var beta []float64 // nil until the first step lands (or a warm start)
	var objOld float64
	if len(pr.Start) == k {
		// Warm start at η = X·Start: the first step is then judged for
		// convergence and step-halving against Start like any other.
		start := append([]float64(nil), pr.Start...)
		if obj := pr.candidate(start, eta, mu, prior); pr.valid(obj, eta) {
			beta, objOld = start, obj
		}
	}
	if beta == nil {
		// Per-row start: μ⁰ = MuStart(y, w*), η⁰ = g(μ⁰).
		for i := 0; i < n; i++ {
			eta[i] = fam.LinkFunc(fam.MuStart(pr.Y[i], prior[i]))
		}
		objOld = pr.objective(eta, mu, prior, nil)
	}

	w := make([]float64, n)
	z := make([]float64, n)
	xtwx := make([]float64, k*k)
	xtwz := make([]float64, k)
	etaNew := make([]float64, n)
	var history [][]float64
	iters := 0

	stop := func(reason string) error {
		return pr.failure(reason, beta, history, eta, iters, maxIters, tol)
	}

	for iter := 0; iter < maxIters; iter++ {
		pr.workingSystem(eta, prior, w, z, xtwx, xtwz)
		betaNew, ok := pr.step(xtwx, xtwz, beta)
		if !ok {
			// Up-front rank passed, so a non-PD working Gram is the
			// working weights collapsing.
			return nil, stop("working_weights_collapsed")
		}
		obj := pr.candidate(betaNew, etaNew, mu, prior)
		// Converged on a FULL step moving β by a relative
		// ||Δβ|| / (||β|| + ε) < tol — checked before any halving, so
		// round-off in the objective can never stall a fit that has
		// already arrived. The first step (no previous β) never converges.
		converged := beta != nil && relativeChange(betaNew, beta) < tol && pr.valid(obj, etaNew)
		if beta == nil && !fam.Canonical && !pr.valid(obj, etaNew) {
			// A non-canonical link's first step left η (or the
			// objective) invalid — an identity link pushing a fitted
			// mean through 0. There is no previous β to halve toward
			// (R's glm.fit stops: "no valid set of coefficients has been
			// found"), so the step is halved toward the intercept-only
			// fit β = (g(ȳ), 0, …), valid for every family; the MLE the
			// loop converges to is the same.
			beta = pr.nullStart(prior)
			objOld = pr.candidate(beta, eta, mu, prior)
			obj = pr.candidate(betaNew, etaNew, mu, prior)
		}
		halvings := 0
		if beta != nil && !converged {
			for !pr.acceptable(obj, objOld, etaNew) {
				if halvings == maxStepHalvings {
					return nil, stop("step_halving_exhausted")
				}
				halvings++
				for a := range betaNew {
					betaNew[a] = (betaNew[a] + beta[a]) / 2
				}
				obj = pr.candidate(betaNew, etaNew, mu, prior)
			}
		}
		beta = betaNew
		eta, etaNew = etaNew, eta
		objOld = obj
		iters = iter + 1
		history = append(history, beta)
		if pr.onStep != nil {
			pr.onStep(iter, obj, halvings)
		}
		if converged {
			sol := &irlsSolution{Beta: beta, Eta: eta, Mu: mu, Iters: iters}
			sol.Deviance = pr.deviance(mu, prior)
			if pr.WantInverse {
				pr.workingSystem(eta, prior, w, z, xtwx, xtwz)
				sol.Gram = append([]float64(nil), xtwx...)
				if pr.WantWeights {
					sol.Weights = append([]float64(nil), w...)
				}
				// A penalised fit's covariance is its penalty's sandwich
				// over Gram (glmPenalty.covariance), never (XᵀWX)⁻¹.
				if pr.Penalty == nil {
					inv, ok := inverseEquilibrated(k, xtwx)
					if !ok {
						return nil, stop("working_weights_collapsed")
					}
					sol.Inverse = inv
				}
			}
			return sol, nil
		}
	}
	return nil, stop("max_iters")
}

// nullStart is the intercept-only fit β = (g(ȳ), 0, …, 0), ȳ the
// prior-weighted mean of y clamped into the family's interior
// (SafeMean): every row's η is g(ȳ), valid under every link.
func (pr *irlsProblem) nullStart(prior []float64) []float64 {
	b := make([]float64, pr.K)
	b[0] = pr.Family.LinkFunc(pr.Family.SafeMean(weightedMean(pr.Y, prior)))
	return b
}

func (pr *irlsProblem) columnName(j int) string {
	if j < len(pr.Columns) {
		return pr.Columns[j]
	}
	return InterceptKey
}

// workingSystem fills the working weights W, the working response z and
// the normal equations XᵀWX (row-major, both triangles) / XᵀWz at η.
func (pr *irlsProblem) workingSystem(eta, prior, w, z, xtwx, xtwz []float64) {
	fam, k := pr.Family, pr.K
	for i := range eta {
		mu := fam.InverseLink(eta[i])
		dmu := fam.DmuDeta(eta[i])
		v := fam.Variance(mu)
		if v < 1e-300 {
			v = 1e-300
		}
		// Prior weight × IRLS weight (R glm's weights · μ'(η)²/V(μ)).
		w[i] = prior[i] * (dmu * dmu / v)
		// A saturated row (μ'(η) → 0) carries a vanishing W; keep its z
		// finite.
		if math.Abs(dmu) < 1e-300 {
			z[i] = eta[i]
		} else {
			z[i] = eta[i] + (pr.Y[i]-mu)/dmu
		}
	}
	for a := range xtwx {
		xtwx[a] = 0
	}
	for a := range xtwz {
		xtwz[a] = 0
	}
	for i := range eta {
		wi, zi := w[i], z[i]
		xi := pr.X[i*k : (i+1)*k]
		for a := 0; a < k; a++ {
			wa := wi * xi[a]
			xtwz[a] += float64(wa * zi)
			row := a * k
			for b := a; b < k; b++ {
				xtwx[row+b] += float64(wa * xi[b])
			}
		}
	}
	for a := 0; a < k; a++ {
		for b := a + 1; b < k; b++ {
			xtwx[b*k+a] = xtwx[a*k+b]
		}
	}
}

// step solves the working normal equations for the next β: the
// penalty's own solve, or the equilibrated Cholesky solve
// (solveEquilibrated, which scales xtwx in place).
func (pr *irlsProblem) step(xtwx, xtwz, beta []float64) ([]float64, bool) {
	if pr.Penalty != nil {
		return pr.Penalty.solve(pr.K, xtwx, xtwz, beta)
	}
	return solveEquilibrated(pr.K, xtwx, xtwz)
}

// equilibrate scales the row-major k × k Gram a in place to D·a·D and
// returns D's diagonal: d_j = 2^−⌊e_j/2⌋ for a_jj = m·2^{e_j}, so every
// scaled diagonal lies in [1/4, 2). The scales are powers of two, so the
// scaling is EXACT — the Cholesky factor, solve and inverse of D·a·D are
// D-scaled copies of a's, bit for bit — and only the condition number the
// factor is judged by changes: a predictor on a large scale (epoch
// seconds, ~1e9) no longer reads as a singular working Gram. The QR rank
// check above is what judges rank.
func equilibrate(k int, a []float64) []float64 {
	d := make([]float64, k)
	for j := range d {
		d[j] = 1
		if v := a[j*k+j]; v > 0 && !math.IsInf(v, 0) {
			_, e := math.Frexp(v)
			d[j] = math.Ldexp(1, -(e / 2))
		}
	}
	for i := 0; i < k; i++ {
		for j := 0; j < k; j++ {
			a[i*k+j] *= d[i] * d[j]
		}
	}
	return d
}

// candidate evaluates η = Xβ (through the shared scoring kernel, so a
// scored row equals the fit's own η bit for bit) and μ = g⁻¹(η) into
// etaOut / mu, returning the objective there.
func (pr *irlsProblem) candidate(beta, etaOut, mu, prior []float64) float64 {
	k := pr.K
	for i := range etaOut {
		xi := pr.X[i*k : (i+1)*k]
		etaOut[i] = LinearPredictor(beta[0], beta[1:], xi[1:])
	}
	return pr.objective(etaOut, mu, prior, beta)
}

// objective fills μ = g⁻¹(η) and returns the deviance plus the penalty
// at beta (none when beta is nil — the per-row start).
func (pr *irlsProblem) objective(eta, mu, prior, beta []float64) float64 {
	for i := range eta {
		mu[i] = pr.Family.InverseLink(eta[i])
	}
	obj := pr.deviance(mu, prior)
	if pr.Penalty != nil && beta != nil {
		obj += pr.Penalty.value(beta)
	}
	return obj
}

// deviance is 2·Σ w*ᵢ·d(yᵢ, μᵢ).
func (pr *irlsProblem) deviance(mu, prior []float64) float64 {
	d := 0.0
	for i := range mu {
		d += float64(prior[i] * pr.Family.Deviance(pr.Y[i], mu[i]))
	}
	return d * 2.0
}

// valid reports whether a candidate is usable at all: a finite
// objective and every η finite and inside the family's valid range.
func (pr *irlsProblem) valid(obj float64, eta []float64) bool {
	if math.IsNaN(obj) || math.IsInf(obj, 0) {
		return false
	}
	for _, e := range eta {
		if math.IsNaN(e) || math.IsInf(e, 0) {
			return false
		}
		if pr.Family.ValidEta != nil && !pr.Family.ValidEta(e) {
			return false
		}
	}
	return true
}

// acceptable reports whether a candidate step stands: valid, and the
// objective did not rise beyond the round-off allowance stepHalvingTol.
func (pr *irlsProblem) acceptable(obj, objOld float64, eta []float64) bool {
	return pr.valid(obj, eta) && obj-objOld <= stepHalvingTol*(math.Abs(obj)+0.1)
}

// relativeChange is ||next − prev|| / (||prev|| + convergenceEps).
func relativeChange(next, prev []float64) float64 {
	var dnum, dnorm float64
	for a := range next {
		d := next[a] - prev[a]
		dnum += float64(d * d)
		dnorm += float64(prev[a] * prev[a])
	}
	return math.Sqrt(dnum) / (math.Sqrt(dnorm) + convergenceEps)
}

// failure classifies a fit that stopped without converging: separation
// (binomial only — fitted probabilities numerically 0 or 1 while a
// predictor's coefficient keeps growing) or NO_CONVERGE.
func (pr *irlsProblem) failure(reason string, beta []float64, history [][]float64, eta []float64, iters, maxIters int, tol float64) error {
	fam, n, p := pr.Family, len(pr.Y), pr.K-1
	// A penalised estimate is finite even on separated data, so a
	// penalised fit that stops short is never SEPARATION.
	_, penalised := pr.Penalty.(*glmPenalty)
	if fam.Name == "binomial" && beta != nil && !penalised {
		boundary := 0
		// μ at the last ACCEPTED β (eta), never a rejected candidate.
		for _, e := range eta {
			if m := fam.InverseLink(e); m < separationMuEps || m > 1-separationMuEps {
				boundary++
			}
		}
		if growing := growingPredictors(history, pr.K); boundary > 0 && len(growing) > 0 {
			names := make([]string, len(growing))
			for i, j := range growing {
				names[i] = pr.columnName(j)
			}
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_SEPARATION,
				"REG_GLM binomial fit is separated: fitted probabilities are numerically 0 or 1 while the coefficients of the named predictors keep growing, so the maximum-likelihood estimate does not exist (it lies at infinity) and no coefficient is reported",
				map[string]any{
					"predictors": names, "n": n, "p": p,
					"n_boundary": boundary, "iters": iters,
					"family": fam.Name, "link": fam.Link,
				},
			)
		}
	}
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_NO_CONVERGE,
		"REG_GLM IRLS failed to converge within MaxIters",
		map[string]any{
			"n": n, "p": p,
			"max_iters": maxIters, "tol": tol,
			"family": fam.Name, "link": fam.Link,
			"converged_iters": iters,
			"reason":          reason,
		},
	)
}

// growingPredictors returns the design columns j ≥ 1 (predictors, never
// the intercept) whose |β_j| strictly rose with an unchanged sign over
// each of the last separationGrowthSteps accepted iterations without its
// increments shrinking below separationMinGrowthRatio.
func growingPredictors(history [][]float64, k int) []int {
	if len(history) < separationGrowthSteps+1 {
		return nil
	}
	tail := history[len(history)-separationGrowthSteps-1:]
	var out []int
	for j := 1; j < k; j++ {
		ok := true
		for s := 1; s < len(tail) && ok; s++ {
			prev, cur := tail[s-1][j], tail[s][j]
			ok = prev != 0 && (prev > 0) == (cur > 0) && math.Abs(cur) > math.Abs(prev)
		}
		if ok {
			last := math.Abs(tail[len(tail)-1][j]) - math.Abs(tail[len(tail)-2][j])
			before := math.Abs(tail[len(tail)-2][j]) - math.Abs(tail[len(tail)-3][j])
			ok = last >= separationMinGrowthRatio*before
		}
		if ok {
			out = append(out, j)
		}
	}
	return out
}

// aliasedColumns is the up-front rank check: a pivot-free QR of the
// prior-weighted design √w*·X by modified Gram-Schmidt, each column
// orthogonalised twice against the accepted columns before it. Column j
// is aliased when its residual norm is at most rankTol of its own norm
// (a zero column always is); aliased columns are left out of the basis,
// so only the later column of a collinear set is named — R lm's NA
// coefficient convention.
func aliasedColumns(x, prior []float64, n, k int) []int {
	sw := make([]float64, n)
	for i := range sw {
		sw[i] = math.Sqrt(prior[i])
	}
	basis := make([][]float64, 0, k)
	var out []int
	for j := 0; j < k; j++ {
		v := make([]float64, n)
		norm0 := 0.0
		for i := 0; i < n; i++ {
			v[i] = sw[i] * x[i*k+j]
			norm0 += float64(v[i] * v[i])
		}
		norm0 = math.Sqrt(norm0)
		for pass := 0; pass < 2; pass++ {
			for _, q := range basis {
				d := 0.0
				for i := range v {
					d += float64(q[i] * v[i])
				}
				for i := range v {
					v[i] -= float64(d * q[i])
				}
			}
		}
		norm := 0.0
		for i := range v {
			norm += float64(v[i] * v[i])
		}
		norm = math.Sqrt(norm)
		if norm0 == 0 || norm <= rankTol*norm0 {
			out = append(out, j)
			continue
		}
		for i := range v {
			v[i] /= norm
		}
		basis = append(basis, v)
	}
	return out
}
