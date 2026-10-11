package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_factor.go is MAT_FACTOR: an exploratory factor analysis off the
// slot's merged co-moments, so the operator is streamable and mergeable
// like MAT_PCA. It always analyses the members' correlation R
// (CoMoment.Corr: listwise, or pairwise with each pair over its own
// rows), which passes the shared PSD guard (guardPSD; params.repair
// "nearest") before anything is fitted.
//
// The k common factors (params.factors: an integer, or the Kaiser rule —
// the eigenvalues of the guarded R above 1) are extracted by one of the
// faExtractors, every one starting from the squared multiple
// correlations (minresStart, psych::smc):
//
//   - minres (faMinres): the minimum-residual fit, psych
//     fa(fm = "minres")'s objective Σ_{i≠j}(r_ij − (LLᵀ)_ij)² over the
//     communalities h, with L the k leading eigenpairs of R with h on
//     its diagonal (L = V·√max(λ, 0)). Its optimum is the point where
//     the diagonal residual d(h) = diag(LLᵀ) − h vanishes (there the
//     off-diagonal residual equals the full unweighted-least-squares
//     residual, which h minimises), so it is solved as d(h) = 0 by a
//     damped Newton iteration with the exact eigen-perturbation
//     Jacobian (faNewtonStep), falling back to the principal-axis step
//     when Newton does not reduce |d|. psych reaches the same optimum
//     with L-BFGS-B and stops early (its default factr; its gradient
//     is the ULS one), so its loadings sit ~1e-5 away; Pulse matches
//     psych's exact optimum (the converged principal-axis fixed point)
//     to the solver tolerance. h is unconstrained: a Heywood case
//     (uniqueness 1 − h ≤ 0) is reported as is, where psych bounds the
//     uniqueness to [0.005, 1].
//   - principal_axis (faPrincipalAxis): psych fa(fm = "pa") step for
//     step — eigen-decompose R with h on the diagonal, h ← diag(LLᵀ),
//     until the summed communality moves by at most faPATol (0.001)
//     within faPAMaxIter (50) iterations; the loadings are the last
//     iterate's.
//
// The unrotated loadings are signed so each column sums positive (a zero
// sum keeps the solver's sign) and ordered by descending eigenvalue,
// psych::fa's rule. params.rotation rotates them through the shared
// rotation engine (matrix_rotate.go), whose sign-and-order rule is the
// same.
//
// Outputs:
//
//   - primary: the p × k rotated loadings (rectangular, columns
//     "F1" … "Fk"); unrotated when params.rotation is none;
//   - auxiliary.unrotated (rotated runs), auxiliary.factor_correlation
//     (oblique rotations: the k × k Φ, square, keyed F1 … Fk, written
//     full), pairwise auxiliary.n;
//   - vectors: communalities diag(LLᵀ) (rotation-invariant),
//     uniquenesses 1 − communalities, variance_accounted (each output
//     column's diag(Φ·LᵀL));
//   - scalars: rmsr = √(Σ_{i<j} res²/(p(p − 1))) (psych's rms),
//     empirical_chisq = Σ_{i<j} res²·N* on df = p(p − 1)/2 − p·k +
//     k(k − 1)/2 and its upper tail empirical_p (null when df ≤ 0) —
//     psych's "empirical" chi-square, a residual size, NOT a likelihood
//     test — kmo, bartlett_chisq / bartlett_df / bartlett_p (the shared
//     sphericityStats core over the guarded R), iterations, converged
//     (1 or 0), factors_retained. res = R − LLᵀ, N* the weighting.md
//     inference size (pcaNStar).
//
// Guards: (p − k)² < p + k (vectors.FactorIdentified) nulls the fit with
// PULSE_MATRIX_NOT_IDENTIFIED; a uniqueness ≤ 0 adds
// PULSE_MATRIX_HEYWOOD (the fit is kept); an extraction or rotation at
// its cap adds PULSE_MATRIX_NOT_CONVERGED (solver fa_minres / fa_pa /
// varimax / gpa_oblimin); an undefined input nulls every figure.

// Factor solver settings — documented constants, never request knobs.
const (
	// faMinresMaxIter caps the Newton iterations; faMinresTol is the
	// convergence bound on max_i |d_i(h)|.
	faMinresMaxIter = 200
	faMinresTol     = 1e-10
	// faPAMaxIter and faPATol are psych::fa's max.iter and min.err.
	faPAMaxIter = 50
	faPATol     = 1e-3
	// faNewtonHalvings caps the step halvings of one damped Newton step.
	faNewtonHalvings = 30
)

// faFit is an unrotated k-factor solution over a p × p correlation.
type faFit struct {
	// Loadings is the p × k unrotated L, columns by descending
	// eigenvalue, each signed to sum positive.
	Loadings [][]float64
	// Communalities is diag(LLᵀ).
	Communalities []float64
	// Solver names the routine for PULSE_MATRIX_NOT_CONVERGED;
	// Iterations / MaxIterations / Tolerance its run and caps;
	// Converged false when it stopped at the cap.
	Solver        string
	Iterations    int
	MaxIterations int
	Tolerance     float64
	Converged     bool
}

// faExtractor fits k ≥ 1 factors to the correlation r (finite, unit
// diagonal, PSD after the guard).
type faExtractor func(r *linalg.Sym, k int) (faFit, error)

// faExtractors is the extraction dispatch, keyed by params.extraction
// (vectors.FactorExtraction*). A new method (maximum likelihood) adds
// its solver here.
var faExtractors = map[string]faExtractor{
	vectors.FactorExtractionMinres:        faMinres,
	vectors.FactorExtractionPrincipalAxis: faPrincipalAxis,
}

// faEigen is the k leading eigenpairs of r with h on its diagonal.
type faEigen struct {
	values  []float64   // all p, descending
	vectors [][]float64 // vectors[a][i] = component i of eigenvector a
}

func faDecompose(r *linalg.Sym, h []float64) (faEigen, error) {
	p := r.N()
	a, _ := linalg.NewSym(p, nil)
	for i := 0; i < p; i++ {
		a.Set(i, i, h[i])
		for j := i + 1; j < p; j++ {
			a.Set(i, j, r.At(i, j))
		}
	}
	eig, err := linalg.SymEigen(a)
	if err != nil {
		return faEigen{}, err
	}
	out := faEigen{values: eig.Values.Slice(), vectors: make([][]float64, p)}
	for c := 0; c < p; c++ {
		out.vectors[c] = make([]float64, p)
		for i := 0; i < p; i++ {
			out.vectors[c][i] = eig.Vectors.At(i, c)
		}
	}
	return out, nil
}

// diagModel is diag(LLᵀ) for the k leading eigenpairs: Σ_a max(λ_a, 0)·v_ai².
func (e faEigen) diagModel(k int) []float64 {
	p := len(e.vectors)
	out := make([]float64, p)
	for i := 0; i < p; i++ {
		s := 0.0
		for c := 0; c < k; c++ {
			v := e.vectors[c][i]
			s += float64(math.Max(e.values[c], 0) * float64(v*v))
		}
		out[i] = s
	}
	return out
}

// loadings is the p × k L = V·√max(λ, 0), each column signed to sum
// positive (psych::fa's flip; a zero sum keeps the solver's sign).
func (e faEigen) loadings(k int) [][]float64 {
	p := len(e.vectors)
	l := make([][]float64, p)
	for i := range l {
		l[i] = make([]float64, k)
	}
	for c := 0; c < k; c++ {
		root := math.Sqrt(math.Max(e.values[c], 0))
		sum := 0.0
		for i := 0; i < p; i++ {
			l[i][c] = float64(e.vectors[c][i] * root)
			sum += l[i][c]
		}
		if sum < 0 {
			for i := 0; i < p; i++ {
				l[i][c] = -l[i][c]
			}
		}
	}
	return l
}

func faFinish(r *linalg.Sym, h []float64, k int, fit faFit) (faFit, error) {
	e, err := faDecompose(r, h)
	if err != nil {
		return faFit{}, err
	}
	fit.Loadings = e.loadings(k)
	fit.Communalities = rowSquares(fit.Loadings)
	return fit, nil
}

// rowSquares is each row's Σ_c l_ic².
func rowSquares(l [][]float64) []float64 {
	out := make([]float64, len(l))
	for i, row := range l {
		s := 0.0
		for _, v := range row {
			s += float64(v * v)
		}
		out[i] = s
	}
	return out
}

// faPrincipalAxis is psych::fa(fm = "pa"): from the SMC, eigen-decompose
// R with the communalities on its diagonal and replace them by the
// model's diag(LLᵀ), until |Σh_new − Σh| ≤ faPATol, at most faPAMaxIter
// iterations. The loadings are the last eigen-decomposition's.
func faPrincipalAxis(r *linalg.Sym, k int) (faFit, error) {
	return faPrincipalAxisSolve(r, k, faPAMaxIter)
}

func faPrincipalAxisSolve(r *linalg.Sym, k, maxIter int) (faFit, error) {
	h, _ := minresStart(r)
	comm := 0.0
	for _, v := range h {
		comm += v
	}
	fit := faFit{Solver: "fa_pa", MaxIterations: maxIter, Tolerance: faPATol}
	var last faEigen
	for fit.Iterations < maxIter {
		e, err := faDecompose(r, h)
		if err != nil {
			return faFit{}, err
		}
		last = e
		fit.Iterations++
		h = e.diagModel(k)
		comm1 := 0.0
		for _, v := range h {
			comm1 += v
		}
		change := math.Abs(comm - comm1)
		comm = comm1
		if change <= faPATol {
			fit.Converged = true
			break
		}
	}
	fit.Loadings = last.loadings(k)
	fit.Communalities = rowSquares(fit.Loadings)
	return fit, nil
}

// faMinres is the minimum-residual fit: solve d(h) = diag(LLᵀ) − h = 0
// from the SMC by damped Newton (faNewtonStep) until max|d| ≤
// faMinresTol, at most faMinresMaxIter steps.
func faMinres(r *linalg.Sym, k int) (faFit, error) {
	return faMinresSolve(r, k, faMinresMaxIter)
}

func faMinresSolve(r *linalg.Sym, k, maxIter int) (faFit, error) {
	h, _ := minresStart(r)
	fit := faFit{Solver: "fa_minres", MaxIterations: maxIter, Tolerance: faMinresTol}
	e, err := faDecompose(r, h)
	if err != nil {
		return faFit{}, err
	}
	d := faResidual(e, h, k)
	for {
		if maxAbs(d) <= faMinresTol {
			fit.Converged = true
			break
		}
		if fit.Iterations >= maxIter {
			break
		}
		h, e, d, err = faNewtonStep(r, k, h, e, d)
		if err != nil {
			return faFit{}, err
		}
		fit.Iterations++
	}
	return faFinish(r, h, k, fit)
}

// faResidual is d(h) = diag(LLᵀ) − h.
func faResidual(e faEigen, h []float64, k int) []float64 {
	m := e.diagModel(k)
	for i := range m {
		m[i] -= h[i]
	}
	return m
}

// faNewtonStep is one damped Newton step on d(h) = 0. The Jacobian of
// diag(LLᵀ) in h is G_ij = P_ij² + 2·Σ_{a≤k<b} λ_a/(λ_a − λ_b)·
// v_ai·v_aj·v_bi·v_bj (P = Σ_{a≤k} v_a·v_aᵀ; first-order perturbation of
// the k leading eigenpairs, a non-positive λ_a contributing nothing, a
// numerically tied pair skipped), so the step solves (I − G)·δ = d. The
// step is halved until |d|² falls (at most faNewtonHalvings times);
// when it never does — or I − G is singular — the principal-axis step
// δ = d is taken instead.
func faNewtonStep(r *linalg.Sym, k int, h []float64, e faEigen, d []float64) ([]float64, faEigen, []float64, error) {
	p := len(h)
	jac := make([][]float64, p)
	for i := range jac {
		jac[i] = make([]float64, p)
	}
	active := 0
	for a := 0; a < k; a++ {
		if e.values[a] > 0 {
			active = a + 1
		}
	}
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			s := 0.0
			for a := 0; a < active; a++ {
				if e.values[a] > 0 {
					s += float64(e.vectors[a][i] * e.vectors[a][j])
				}
			}
			jac[i][j] = float64(s * s)
		}
	}
	for a := 0; a < active; a++ {
		la := e.values[a]
		if !(la > 0) {
			continue
		}
		va := e.vectors[a]
		for b := k; b < p; b++ {
			gap := la - e.values[b]
			if !(math.Abs(gap) > 1e-12*math.Max(1, math.Abs(la))) {
				continue
			}
			c := 2 * la / gap
			vb := e.vectors[b]
			for i := 0; i < p; i++ {
				xi := float64(va[i] * vb[i])
				if xi == 0 {
					continue
				}
				ci := float64(c * xi)
				for j := i; j < p; j++ {
					jac[i][j] += float64(ci * float64(va[j]*vb[j]))
				}
			}
		}
	}
	m := make([][]float64, p)
	for i := range m {
		m[i] = make([]float64, p)
		for j := 0; j < p; j++ {
			g := jac[min(i, j)][max(i, j)]
			m[i][j] = -g
			if i == j {
				m[i][j] += 1
			}
		}
	}
	merit0 := sumSquares(d)
	if delta, ok := gaussSolve(m, d); ok {
		t := 1.0
		for range faNewtonHalvings + 1 {
			trial := make([]float64, p)
			for i := range trial {
				trial[i] = h[i] + float64(t*delta[i])
			}
			te, err := faDecompose(r, trial)
			if err != nil {
				return nil, faEigen{}, nil, err
			}
			td := faResidual(te, trial, k)
			if sumSquares(td) < merit0 {
				return trial, te, td, nil
			}
			t /= 2
		}
	}
	trial := make([]float64, p)
	for i := range trial {
		trial[i] = h[i] + d[i]
	}
	te, err := faDecompose(r, trial)
	if err != nil {
		return nil, faEigen{}, nil, err
	}
	return trial, te, faResidual(te, trial, k), nil
}

// gaussSolve solves m·x = b by Gaussian elimination with partial
// pivoting (FMA-free); ok is false on an exactly zero pivot. m and b are
// not mutated.
func gaussSolve(m [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	a := make([][]float64, n)
	for i := range a {
		a[i] = append(append(make([]float64, 0, n+1), m[i]...), b[i])
	}
	for col := 0; col < n; col++ {
		piv := col
		for r := col + 1; r < n; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[piv][col]) {
				piv = r
			}
		}
		if a[piv][col] == 0 || math.IsNaN(a[piv][col]) {
			return nil, false
		}
		a[col], a[piv] = a[piv], a[col]
		for r := col + 1; r < n; r++ {
			f := a[r][col] / a[col][col]
			if f == 0 {
				continue
			}
			for c := col; c <= n; c++ {
				a[r][c] -= float64(f * a[col][c])
			}
		}
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := a[i][n]
		for c := i + 1; c < n; c++ {
			s -= float64(a[i][c] * x[c])
		}
		x[i] = s / a[i][i]
	}
	return x, true
}

func maxAbs(v []float64) float64 {
	out := 0.0
	for _, x := range v {
		if a := math.Abs(x); a > out || math.IsNaN(x) {
			out = a
		}
	}
	return out
}

func sumSquares(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += float64(x * x)
	}
	return s
}

// factorColumns are the factor keys "F1" … "Fk".
func factorColumns(k int) []string {
	out := make([]string, k)
	for c := range out {
		out[c] = "F" + strconv.Itoa(c+1)
	}
	return out
}

// faResidualStats are the residual figures of a fit: rmsr and the
// empirical chi-square over res = R − LLᵀ.
func faResidualStats(r *linalg.Sym, l [][]float64, nStar float64) (rmsr, chisq, df, pValue float64) {
	p := r.N()
	k := 0
	if p > 0 {
		k = len(l[0])
	}
	off := 0.0
	for i := 0; i < p; i++ {
		for j := i + 1; j < p; j++ {
			m := 0.0
			for c := 0; c < k; c++ {
				m += float64(l[i][c] * l[j][c])
			}
			res := r.At(i, j) - m
			off += float64(res * res)
		}
	}
	df = float64(p*(p-1)/2 - p*k + k*(k-1)/2)
	rmsr = math.Sqrt(off / float64(p*(p-1)))
	chisq = off * nStar
	pValue = math.NaN()
	if df > 0 && !math.IsNaN(chisq) && !math.IsInf(nStar, 0) {
		pValue = chiSquareSurvival(chisq, df)
	}
	return rmsr, chisq, df, pValue
}

// finalizeFactor is MAT_FACTOR (see the file comment).
func finalizeFactor(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	cm := in.CM
	r := cm.Corr()
	p := r.N()
	out := matrixOutput{Warnings: m.warnings(cm, r)}
	if plan.Pairwise && in.WantAuxiliary() {
		out.Auxiliary = map[string]*types.MatrixValues{
			"n": in.Square(func(i, j int) float64 { return float64(cm.PairN(i, j)) }),
		}
	}
	if !judgeable(r) {
		k := plan.Factors.K
		retained := math.NaN()
		if !plan.Factors.Kaiser() {
			retained = float64(k)
		}
		st := pcaStats{kmo: math.NaN(), chisq: math.NaN(), p: math.NaN(), df: float64(p * (p - 1) / 2)}
		faNullFit(in, &out, p, k, st, retained)
		return out, nil
	}
	g, warns, err := in.guardPSD(r, nil)
	if err != nil {
		return matrixOutput{}, err
	}
	out.Warnings = append(out.Warnings, warns...)

	k := plan.Factors.K
	if plan.Factors.Kaiser() {
		eig, err := linalg.SymEigen(g)
		if err != nil {
			return matrixOutput{}, singularMatrixError(err, plan.Name, in.Members())
		}
		for _, v := range eig.Values.Slice() {
			if v > 1 {
				k++
			}
		}
	}

	nStar, pw := pcaNStar(in, []string{"bartlett_chisq", "bartlett_p", "empirical_chisq", "empirical_p"},
		"Bartlett's test and the empirical chi-square use")
	if pw != nil {
		out.Warnings = append(out.Warnings, pw)
	}
	stats, statWarns := sphericityStats(in, g, nStar, []string{"kmo", "bartlett_chisq", "bartlett_p"},
		"KMO and Bartlett's test are null; the factors are still fitted (the squared-multiple-correlation start falls back to each member's largest correlation)")
	out.Warnings = append(out.Warnings, statWarns...)

	if !vectors.FactorIdentified(p, k) {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NOT_IDENTIFIED,
			"matrix "+plan.Name+": "+strconv.Itoa(k)+" factors are not identified on "+strconv.Itoa(p)+" members ((p − k)² must be at least p + k, so at most "+
				strconv.Itoa(vectors.MaxIdentifiedFactors(p))+" here): the loadings and fit figures are null; KMO and Bartlett's test still compute",
			map[string]any{"matrix": plan.Name, "output": "loadings", "members": append([]string(nil), in.Members()...),
				"factors": k, "max_factors": vectors.MaxIdentifiedFactors(p)}))
		faNullFit(in, &out, p, k, stats, float64(k))
		return out, nil
	}
	if k == 0 {
		// The Kaiser rule kept nothing: the zero-factor model leaves
		// every correlation as residual.
		zero := make([][]float64, p)
		for i := range zero {
			zero[i] = []float64{}
		}
		faWriteFit(in, &out, faFit{Loadings: zero, Communalities: make([]float64, p), Converged: true}, nil, stats, nStar, g)
		return out, nil
	}

	fit, err := faExtractors[plan.Extraction](g, k)
	if err != nil {
		return matrixOutput{}, err
	}
	if !fit.Converged {
		out.Warnings = append(out.Warnings, notConvergedWarning(plan.Name, fit.Solver, fit.Iterations, fit.MaxIterations, fit.Tolerance,
			"matrix "+plan.Name+": the "+plan.Extraction+" extraction stopped at its "+strconv.Itoa(fit.MaxIterations)+"-iteration cap before converging; the loadings are the last iterate, not a solution"))
	}
	var heywood []string
	var psis []float64
	for i, h := range fit.Communalities {
		if psi := 1 - h; psi <= 0 {
			heywood = append(heywood, in.Members()[i])
			psis = append(psis, psi)
		}
	}
	if len(heywood) > 0 {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_HEYWOOD,
			"matrix "+plan.Name+": a Heywood case — the fit gives "+strconv.Itoa(len(heywood))+" member(s) a uniqueness at or below 0 (communality at least 1, an impossible negative error variance); the loadings are reported as fitted, not repaired",
			map[string]any{"matrix": plan.Name, "output": "loadings", "members": heywood, "uniquenesses": psis}))
	}
	var rot *rotationResult
	if plan.Rotation != "" {
		res, err := rotateLoadings(fit.Loadings, rotationSpec{Method: plan.Rotation, Kappa: plan.Kappa, Gamma: plan.Gamma})
		if err != nil {
			return matrixOutput{}, err
		}
		if !res.Converged {
			out.Warnings = append(out.Warnings, notConvergedWarning(plan.Name, res.Solver, res.Iterations, res.MaxIterations, res.Tolerance,
				"matrix "+plan.Name+": the "+plan.Rotation+" rotation stopped at its "+strconv.Itoa(res.MaxIterations)+"-iteration cap before its criterion settled; the rotated loadings are the last iterate"))
		}
		rot = &res
	}
	faWriteFit(in, &out, fit, rot, stats, nStar, g)
	return out, nil
}

// faWriteFit fills out from a fit (rotated by rot when non-nil).
func faWriteFit(in *matrixFinalizeInput, out *matrixOutput, fit faFit, rot *rotationResult, stats pcaStats, nStar float64, r *linalg.Sym) {
	plan := in.Plan()
	p := len(fit.Loadings)
	k := 0
	if p > 0 {
		k = len(fit.Loadings[0])
	}
	cols := factorColumns(k)
	unrotated := in.Rect(cols, func(i, c int) float64 { return fit.Loadings[i][c] })
	out.Primary = unrotated
	variance := make([]float64, k)
	for c := 0; c < k; c++ {
		for i := 0; i < p; i++ {
			variance[c] += float64(fit.Loadings[i][c] * fit.Loadings[i][c])
		}
	}
	if plan.Rotation != "" {
		if in.WantAuxiliary() {
			if out.Auxiliary == nil {
				out.Auxiliary = map[string]*types.MatrixValues{}
			}
			out.Auxiliary["unrotated"] = unrotated
		}
		if rot != nil {
			out.Primary = in.Rect(cols, func(i, c int) float64 { return rot.Loadings[i][c] })
			variance = rot.Variance
			if plan.Rotation != vectors.RotationVarimax && in.WantAuxiliary() {
				out.Auxiliary["factor_correlation"] = factorCorrelation(cols, func(a, b int) float64 { return rot.Phi[a][b] })
			}
		} else if plan.Rotation != vectors.RotationVarimax && in.WantAuxiliary() {
			// No factor to rotate (the Kaiser rule kept none).
			out.Auxiliary["factor_correlation"] = factorCorrelation(cols, nil)
		}
	}
	if in.WantVectors() {
		uniq := make([]float64, p)
		for i, h := range fit.Communalities {
			uniq[i] = 1 - h
		}
		out.Vectors = map[string]any{
			"communalities":      append([]float64(nil), fit.Communalities...),
			"uniquenesses":       uniq,
			"variance_accounted": variance,
		}
	}
	if in.WantScalars() {
		rmsr, chisq, df, pv := faResidualStats(r, fit.Loadings, nStar)
		converged := 0.0
		if fit.Converged {
			converged = 1
		}
		out.Scalars = faScalars(stats, float64(k))
		out.Scalars["rmsr"] = rmsr
		out.Scalars["empirical_chisq"] = chisq
		out.Scalars["df"] = df
		out.Scalars["empirical_p"] = pv
		out.Scalars["iterations"] = float64(fit.Iterations)
		out.Scalars["converged"] = converged
	}
}

// faNullFit fills out with a withheld fit: k null columns (k may be 0),
// null vectors and fit scalars, beside the sphericity figures stats.
func faNullFit(in *matrixFinalizeInput, out *matrixOutput, p, k int, stats pcaStats, retained float64) {
	plan := in.Plan()
	cols := factorColumns(k)
	nan := func(int, int) float64 { return math.NaN() }
	out.Primary = in.Rect(cols, nan)
	if plan.Rotation != "" && in.WantAuxiliary() {
		if out.Auxiliary == nil {
			out.Auxiliary = map[string]*types.MatrixValues{}
		}
		out.Auxiliary["unrotated"] = in.Rect(cols, nan)
		if plan.Rotation != vectors.RotationVarimax {
			out.Auxiliary["factor_correlation"] = factorCorrelation(cols, func(int, int) float64 { return math.NaN() })
		}
	}
	if in.WantVectors() {
		out.Vectors = map[string]any{
			"communalities":      nanVector(p),
			"uniquenesses":       nanVector(p),
			"variance_accounted": nanVector(k),
		}
	}
	if in.WantScalars() {
		out.Scalars = faScalars(stats, retained)
		for _, key := range []string{"rmsr", "empirical_chisq", "empirical_p", "iterations", "converged"} {
			out.Scalars[key] = math.NaN()
		}
		out.Scalars["df"] = float64(p*(p-1)/2 - p*k + k*(k-1)/2)
	}
}

// faScalars are the sphericity scalars plus factors_retained.
func faScalars(stats pcaStats, retained float64) map[string]float64 {
	return map[string]float64{
		"kmo":              stats.kmo,
		"bartlett_chisq":   stats.chisq,
		"bartlett_df":      stats.df,
		"bartlett_p":       stats.p,
		"factors_retained": retained,
	}
}

// factorCorrelation is the k × k factor correlation Φ (square, keyed by
// the factors, always full); at nil is the identity.
func factorCorrelation(cols []string, at func(a, b int) float64) *types.MatrixValues {
	k := len(cols)
	vals := make([][]float64, k)
	for a := range vals {
		vals[a] = make([]float64, k)
		for b := range vals[a] {
			switch {
			case at != nil:
				vals[a][b] = at(a, b)
			case a == b:
				vals[a][b] = 1
			}
		}
	}
	return &types.MatrixValues{
		Kind:       types.MatrixKindSquareSymmetric,
		Encoding:   types.MatrixEncodingFull,
		RowKeys:    append([]string(nil), cols...),
		ColumnKeys: append([]string(nil), cols...),
		Values:     vals,
	}
}
