package regression

import (
	"math"

	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// glmPenalty is REG_GLM's elastic-net penalty (l2 / l1 / elasticnet),
// plugged into the one IRLS loop through irlsProblem.Penalty. It
// minimises glmnet's objective (standardize = FALSE)
//
//	−loglik(β)/N* + α·[(1−l1_ratio)/2·‖β‖² + l1_ratio·‖β‖₁]
//
// with N* = Σ w*ᵢ (the prior weights the fit runs on — n unweighted; the
// objective is invariant to rescaling the weights, as glmnet's is) and
// the norms over the predictors only: the intercept is never penalised
// and no predictor is standardised. On the deviance scale the loop
// monitors (deviance = −2·loglik + const) that is
//
//	deviance + λ₂·‖β‖² + 2·λ₁·‖β‖₁,  λ₂ = N*·α·(1−l1_ratio), λ₁ = N*·α·l1_ratio.
//
// Each IRLS iteration solves the penalised working least-squares
// problem on the working normal equations A = XᵀWX, b = XᵀWz:
//
//	min ½·βᵀAβ − βᵀb + λ₂/2·‖β‖² + λ₁·‖β‖₁
//
// in closed form for l2 (A + λ₂·D)β = b, D = diag(0, 1, …, 1) — through
// the same power-of-two-equilibrated Cholesky as the unpenalised step —
// and by cyclic coordinate descent for l1 / elasticnet (glmnet's
// covariance updates):
//
//	β₀ ← r₀ / A₀₀,  β_j ← S(r_j, λ₁) / (A_jj + λ₂),  r_j = b_j − Σ_{k≠j} A_jk·β_k
//
// finished by an exact active-set solve: once a sweep leaves the zero
// pattern unchanged, (A_SS + λ₂·D_S)·β_S = b_S − λ₁·sign(β_S) is solved
// directly over the intercept and the nonzero predictors S and accepted
// when its signs agree and every dropped predictor satisfies the KKT
// bound |r_j| ≤ λ₁ — so the result does not hinge on how far the
// (linearly converging) descent was run.
//
// Per-term penalty factors (U69 E1-S1, glmnet's penalty.factor 0 / 1):
// an `unpenalized` predictor carries no λ₁ / λ₂ term — D's entry is 0,
// its coordinate update is the plain r_j / A_jj, it never shrinks to 0
// and it stays in the active set — exactly as the intercept.
// Multinomial's ridgeMasked / descendMasked are the same scheme.
type glmPenalty struct {
	kind    string // "l1", "l2" or "elasticnet"
	alpha   float64
	l1Ratio float64
	nStar   float64
	// pen[j] reports whether design term j (0 the intercept, then the
	// predictors in fit order) is penalized
	// (regpenalty.PenalizedTerms). Nil = every predictor penalized, the
	// intercept never (the historical penalty).
	pen []bool
}

// penalized reports whether design term j carries the penalty: never
// the intercept, every predictor unless pen marks it unpenalized.
func (p *glmPenalty) penalized(j int) bool {
	if j == 0 {
		return false
	}
	return p.pen == nil || p.pen[j]
}

const (
	// glmCDMaxSweeps caps the coordinate-descent sweeps of one IRLS
	// iteration; the active-set finish normally ends the descent long
	// before.
	glmCDMaxSweeps = 100000
	// glmCDTol: a sweep whose largest scaled move √A_jj·|Δβ_j| is at
	// most glmCDTol·(1 + √A_jj·|β_j|) has converged.
	glmCDTol = 1e-15
	// glmKKTSlack is the relative slack on the dropped predictors' KKT
	// bound |r_j| ≤ λ₁ (round-off in r_j, never a modelling allowance).
	glmKKTSlack = 1e-10
)

// newGLMPenalty returns the penalty a REG_GLM spec asks for over prior
// weights summing to nStar, or nil for an unpenalised spec. l1 is
// l1_ratio 1 and l2 is l1_ratio 0; elasticnet reads the spec's.
// predictors are the design's predictor columns in fit order (a refit's
// active subset): spec.Unpenalized is matched against them by name.
func newGLMPenalty(spec *types.RegressionSpec, predictors []string, nStar float64) *glmPenalty {
	if spec == nil || spec.Penalty == "" {
		return nil
	}
	p := &glmPenalty{kind: spec.Penalty, alpha: spec.Alpha, nStar: nStar}
	if len(spec.Unpenalized) != 0 {
		p.pen = regpenalty.PenalizedTerms(spec, predictors)
	}
	switch spec.Penalty {
	case "l1":
		p.l1Ratio = 1
	case "elasticnet":
		p.l1Ratio = spec.L1Ratio
	}
	return p
}

func (p *glmPenalty) lambda2() float64 { return float64(p.nStar * p.alpha * (1 - p.l1Ratio)) }
func (p *glmPenalty) lambda1() float64 { return float64(p.nStar * p.alpha * p.l1Ratio) }

// approximateSE reports whether the fit selects its predictors from the
// data (l1 / elasticnet), so its standard errors are undefined (NaN).
func (p *glmPenalty) approximateSE() bool { return p != nil && p.l1Ratio > 0 }

// value is the penalty on the deviance scale: λ₂·‖β‖² + 2·λ₁·‖β‖₁ over
// the penalized predictors.
func (p *glmPenalty) value(beta []float64) float64 {
	var sq, abs float64
	for j := 1; j < len(beta); j++ {
		if !p.penalized(j) {
			continue
		}
		b := beta[j]
		sq += float64(b * b)
		abs += math.Abs(b)
	}
	return float64(p.lambda2()*sq) + float64(2*p.lambda1()*abs)
}

// solve returns the penalised working least-squares solution (a fresh
// slice), reporting false when the l2 system cannot be factored.
func (p *glmPenalty) solve(k int, xtwx, xtwz, beta []float64) ([]float64, bool) {
	if p.l1Ratio == 0 {
		return p.ridgeSolve(k, xtwx, xtwz)
	}
	return p.descend(k, xtwx, xtwz, beta)
}

// ridgeSolve solves (A + λ₂·D)β = b.
func (p *glmPenalty) ridgeSolve(k int, a, b []float64) ([]float64, bool) {
	m := append([]float64(nil), a...)
	l2 := p.lambda2()
	for j := 1; j < k; j++ {
		if p.penalized(j) {
			m[j*k+j] += l2
		}
	}
	return solveEquilibrated(k, m, b)
}

// descend runs coordinate descent warm-started at beta (zeros when nil)
// with the exact active-set finish.
func (p *glmPenalty) descend(k int, a, b, start []float64) ([]float64, bool) {
	l1, l2 := p.lambda1(), p.lambda2()
	beta := make([]float64, k)
	if start != nil {
		copy(beta, start)
	}
	if !(a[0] > 0) {
		return nil, false
	}
	prevPattern := make([]bool, k)
	for sweep := 0; sweep < glmCDMaxSweeps; sweep++ {
		converged := true
		for j := 0; j < k; j++ {
			r := b[j]
			row := a[j*k : (j+1)*k]
			for c := 0; c < k; c++ {
				if c != j {
					r -= float64(row[c] * beta[c])
				}
			}
			var next float64
			if !p.penalized(j) {
				// The intercept or an unpenalized predictor: the plain
				// least-squares coordinate (a non-positive A_jj leaves 0).
				if row[j] > 0 {
					next = r / row[j]
				}
			} else if d := row[j] + l2; d > 0 {
				next = softThreshold(r, l1) / d
			}
			s := math.Sqrt(math.Max(row[j], 0))
			if s*math.Abs(next-beta[j]) > glmCDTol*(1+float64(s*math.Abs(next))) {
				converged = false
			}
			beta[j] = next
		}
		if converged {
			return beta, true
		}
		stable := true
		for j := 1; j < k; j++ {
			nz := beta[j] != 0
			if nz != prevPattern[j] {
				stable = false
			}
			prevPattern[j] = nz
		}
		if stable && sweep > 0 {
			if exact, ok := p.activeSetSolve(k, a, b, beta); ok {
				return exact, true
			}
		}
	}
	return beta, true
}

// activeSetSolve solves the penalised problem restricted to the
// intercept, the unpenalized predictors and the penalized predictors
// nonzero in beta with their signs fixed, returning the full-length
// solution when it is a KKT point of the whole problem.
func (p *glmPenalty) activeSetSolve(k int, a, b, beta []float64) ([]float64, bool) {
	l1, l2 := p.lambda1(), p.lambda2()
	active := []int{0}
	for j := 1; j < k; j++ {
		if beta[j] != 0 || !p.penalized(j) {
			active = append(active, j)
		}
	}
	q := len(active)
	m := make([]float64, q*q)
	rhs := make([]float64, q)
	for u, ju := range active {
		for v, jv := range active {
			m[u*q+v] = a[ju*k+jv]
		}
		rhs[u] = b[ju]
		if p.penalized(ju) {
			m[u*q+u] += l2
			rhs[u] -= float64(l1 * sign(beta[ju]))
		}
	}
	sol, ok := solveEquilibrated(q, m, rhs)
	if !ok {
		return nil, false
	}
	out := make([]float64, k)
	for u, ju := range active {
		if p.penalized(ju) && sol[u]*sign(beta[ju]) <= 0 {
			return nil, false
		}
		out[ju] = sol[u]
	}
	for j := 1; j < k; j++ {
		if out[j] != 0 || !p.penalized(j) {
			continue
		}
		r := b[j]
		for _, c := range active {
			r -= float64(a[j*k+c] * out[c])
		}
		if math.Abs(r) > l1*(1+glmKKTSlack) {
			return nil, false
		}
	}
	return out, true
}

func sign(x float64) float64 {
	if x < 0 {
		return -1
	}
	return 1
}

// covariance is the penalised-Fisher sandwich H⁻¹·A·H⁻¹, H = A + λ₂·D
// (D = diag of the per-term factors: 0 on the intercept and every
// unpenalized predictor),
// at the converged working Gram a (k × k, row-major) — over the whole
// design for l2, over the intercept and the nonzero predictors for l1 /
// elasticnet. It returns the active columns and their covariance
// (len(active) square), or ok = false when H cannot be inverted. Only
// l2 reports it: an l1 / elasticnet fit's standard errors are NaN
// (penalizedResult), and the active-set form survives only as the
// positive-definiteness check behind its NO_CONVERGE refusal.
func (p *glmPenalty) covariance(k int, a, beta []float64) (active []int, cov [][]float64, ok bool) {
	active = []int{0}
	for j := 1; j < k; j++ {
		if p.l1Ratio == 0 || beta[j] != 0 || !p.penalized(j) {
			active = append(active, j)
		}
	}
	q := len(active)
	sub := make([]float64, q*q)
	for u, ju := range active {
		for v, jv := range active {
			sub[u*q+v] = a[ju*k+jv]
		}
	}
	h := append([]float64(nil), sub...)
	l2 := p.lambda2()
	for u, ju := range active {
		if p.penalized(ju) {
			h[u*q+u] += l2
		}
	}
	inv, ok := inverseEquilibrated(q, h)
	if !ok {
		return nil, nil, false
	}
	sandwich, err := ridgeSandwich(inv, q, sub)
	if err != nil {
		return nil, nil, false
	}
	cov = make([][]float64, q)
	for u := range cov {
		cov[u] = make([]float64, q)
		for v := range cov[u] {
			// The dense product can differ from symmetric in the last
			// ulp; read the upper triangle.
			i, j := u, v
			if i > j {
				i, j = j, i
			}
			cov[u][v] = sandwich.At(i, j)
		}
	}
	return active, cov, true
}

// hatInverse is the penalized fit's carried regularized inverse
// (RegressionResult.PenalizedInverse, regression-depth-2 E1-S4): H⁻¹,
// H = A_SS + λ₂·D_S, at the converged working Gram a over S = the
// intercept and the hat's active predictors — every one under l2, the
// nonzero ones under l1 / elasticnet (leverageDesign's convention: λ₂ =
// 0 under l1, so the lasso's is the unpenalized hat of its active set).
// A bound fit's leverage reads h = w·x̃ᵀH⁻¹x̃ at the row's working
// weight w (BoundInfluence), R's hat of the penalized IRLS's final
// weighted least squares. A singular H is carried all-NaN (inv nil).
func (p *glmPenalty) hatInverse(k int, a, beta []float64) (active []int, inv [][]float64) {
	active = []int{0}
	for j := 1; j < k; j++ {
		if p.l1Ratio == 0 || beta[j] != 0 {
			active = append(active, j)
		}
	}
	q := len(active)
	h := make([]float64, q*q)
	l2 := p.lambda2()
	for u, ju := range active {
		for v, jv := range active {
			h[u*q+v] = a[ju*k+jv]
		}
		if p.penalized(ju) {
			h[u*q+u] += l2
		}
	}
	sym, ok := inverseEquilibrated(q, h)
	if !ok {
		return active, nil
	}
	inv = make([][]float64, q)
	for u := range inv {
		inv[u] = make([]float64, q)
		for v := range inv[u] {
			inv[u][v] = sym.At(u, v)
		}
	}
	return active, inv
}

// solveEquilibrated solves the k × k SPD system m·x = rhs through the
// exactly power-of-two-equilibrated Cholesky factor (equilibrate scales
// m in place). It is irlsProblem.step's unpenalised solve, operation for
// operation.
func solveEquilibrated(k int, m, rhs []float64) ([]float64, bool) {
	d := equilibrate(k, m)
	chol, ok := factorSPD(k, m)
	if !ok {
		return nil, false
	}
	scaled := make([]float64, k)
	for a := range scaled {
		scaled[a] = d[a] * rhs[a]
	}
	next, err := solveSPD(chol, scaled)
	if err != nil {
		return nil, false
	}
	for a := range next {
		next[a] *= d[a]
	}
	return next, true
}

// inverseEquilibrated returns m⁻¹ for the k × k SPD m through the same
// equilibrated factor (m is scaled in place).
func inverseEquilibrated(k int, m []float64) (*linalg.Sym, bool) {
	d := equilibrate(k, m)
	chol, ok := factorSPD(k, m)
	if !ok {
		return nil, false
	}
	inv, err := chol.Inverse()
	if err != nil {
		return nil, false
	}
	packed := make([]float64, 0, k*(k+1)/2)
	for i := 0; i < k; i++ {
		for j := i; j < k; j++ {
			packed = append(packed, d[i]*d[j]*inv.At(i, j))
		}
	}
	out, err := linalg.NewSym(k, packed)
	if err != nil {
		return nil, false
	}
	return out, true
}
