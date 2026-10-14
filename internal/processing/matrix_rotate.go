package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
)

// matrix_rotate.go is the shared rotation engine: it turns a p × k
// loading matrix A into rotated loadings L = A·T with one of three
// criteria, and puts every result through ONE sign-and-order rule.
// It knows nothing about the operator that calls it — MAT_PCA's
// params.rotation today, MAT_FACTOR and ATTR_PC_SCORE next — and takes
// and returns plain row-major slices.
//
// Each criterion reproduces its R reference algorithm step for step,
// with R's default constants, so a fit stops on the same iterate R
// stops on:
//
//   - varimax (stats::varimax, normalize = TRUE): Kaiser-normalised
//     rows, then the SVD fixed point T ← U·Vᵀ of B = Aᵀ(Z³ − Z·diag(ΣZ²)/p),
//     stopping when Σσ grows by less than a relative 1e-5 (1000 sweeps
//     at most). Orthogonal: Φ = I.
//   - promax (stats::promax, m = kappa): varimax, then the target
//     Q = L∘|L|^(κ−1), the least-squares fit U = (LᵀL)⁻¹LᵀQ with its
//     columns rescaled so Φ = (UᵀU)⁻¹ has a unit diagonal; T = T_v·U.
//   - oblimin (GPArotation::oblimin, gam = gamma, normalize = FALSE):
//     the oblique gradient-projection algorithm with the package's
//     default Barzilai–Borwein step and 10-iterate non-monotone line
//     search, stopping when the projected gradient's norm drops below
//     1e-5 (2000 iterations at most). L = A·(T_g⁻¹)ᵀ, Φ = T_gᵀT_g.
//
// The sign-and-order rule (psych::fa's): columns ordered by descending
// variance accounted, diag(Φ·LᵀL) — the column's sum of squared
// loadings when orthogonal — a tie keeping the earlier column; then
// each column flipped so it sums positive (a zero sum stays). The
// rotation matrix's columns and Φ's rows and columns follow the same
// permutation and signs, so L = A·Rotation and Φ = Rotation⁻¹·Rotation⁻ᵀ
// hold on the returned values.
//
// A single column (k < 2) is returned unrotated — R refuses or returns
// it as is — with Rotation = Φ = I and no reordering or flip.
//
// Numerics: products feeding a sum are written float64(a*b) (FMA-free,
// the reference-kernel style), but the varimax SVD is gonum-backed, so
// a result is same-machine bitwise and agrees across architectures
// within the oracle tolerance (rotationOracleTol in the tests, 1e-9
// absolute + relative; matrix-and-vectors/matrix-decomposition.md, Rotation engine).

// Rotation engine constants: R's defaults.
const (
	varimaxMaxIter = 1000
	varimaxEps     = 1e-5
	gpaMaxIter     = 2000
	gpaEps         = 1e-5
	gpaWindow      = 10
	gpaLineSteps   = 10
	// promaxDefaultKappa is stats::promax's m.
	promaxDefaultKappa = 4
)

// rotationSpec selects a criterion and its parameter.
type rotationSpec struct {
	// Method is vectors.RotationVarimax, RotationPromax or
	// RotationOblimin.
	Method string
	// Kappa is promax's power (> 1); 0 means promaxDefaultKappa.
	Kappa float64
	// Gamma is oblimin's γ (0 quartimin, 0.5 biquartimin).
	Gamma float64
}

// rotationResult is a rotated loading matrix after the sign-and-order
// rule.
type rotationResult struct {
	// Loadings is the p × k rotated pattern L = A·Rotation.
	Loadings [][]float64
	// Rotation is the k × k matrix T with Loadings = A·T.
	Rotation [][]float64
	// Phi is the k × k component (factor) correlation matrix — the
	// identity for an orthogonal criterion.
	Phi [][]float64
	// Variance is each output column's variance accounted,
	// diag(Φ·LᵀL): the sum of squared loadings when orthogonal.
	Variance []float64
	// Order[c] is the source column (before the rule) of output column
	// c; Signs[c] is the ±1 it was multiplied by.
	Order []int
	Signs []float64
	// Oblique reports a non-orthogonal criterion (Φ ≠ I).
	Oblique bool
	// Solver names the iterative routine for a NOT_CONVERGED warning;
	// Iterations / MaxIterations / Tolerance its run and caps;
	// Converged false when it stopped at the cap.
	Solver        string
	Iterations    int
	MaxIterations int
	Tolerance     float64
	Converged     bool
}

// rotateLoadings rotates the p × k loadings a by spec. a is not
// mutated. An unknown method or a criterion whose least-squares or
// inversion step fails returns the error (a coded linalg error, or
// PROCESSING_INTERNAL for an unknown method).
func rotateLoadings(a [][]float64, spec rotationSpec) (rotationResult, error) {
	switch spec.Method {
	case vectors.RotationVarimax, vectors.RotationPromax, vectors.RotationOblimin:
	default:
		return rotationResult{}, errors.NewCodedError(errors.PROCESSING_INTERNAL, "rotation method "+spec.Method+" is not known")
	}
	p := len(a)
	k := 0
	if p > 0 {
		k = len(a[0])
	}
	if k < 2 {
		res := rotationResult{
			Loadings: cloneRows(a), Rotation: identityRows(k), Phi: identityRows(k),
			Order: make([]int, k), Signs: make([]float64, k), Converged: true,
			Solver: spec.Method, Oblique: spec.Method != vectors.RotationVarimax,
		}
		for c := 0; c < k; c++ {
			res.Order[c], res.Signs[c] = c, 1
		}
		res.Variance = rotationVariance(res.Loadings, res.Phi)
		return res, nil
	}
	var (
		res rotationResult
		err error
	)
	switch spec.Method {
	case vectors.RotationVarimax:
		res = varimaxRotate(a)
	case vectors.RotationPromax:
		res, err = promaxRotate(a, spec.Kappa)
	default: // vectors.RotationOblimin
		res, err = obliminRotate(a, spec.Gamma)
	}
	if err != nil {
		return rotationResult{}, err
	}
	canonicalizeRotation(&res)
	return res, nil
}

// varimaxRotate is stats::varimax(a, normalize = TRUE, eps = 1e-5). A
// row of zero norm is left unscaled (R would divide by 0); it stays a
// zero row.
func varimaxRotate(a [][]float64) rotationResult {
	p, k := len(a), len(a[0])
	x := cloneRows(a)
	sc := make([]float64, p)
	for i := range x {
		ss := 0.0
		for _, v := range x[i] {
			ss += float64(v * v)
		}
		sc[i] = math.Sqrt(ss)
		if sc[i] == 0 {
			sc[i] = 1
		}
		for c := range x[i] {
			x[i][c] /= sc[i]
		}
	}
	tt := identityRows(k)
	d := 0.0
	res := rotationResult{Solver: "varimax", MaxIterations: varimaxMaxIter, Tolerance: varimaxEps}
	pf := float64(p)
	for it := 1; it <= varimaxMaxIter; it++ {
		res.Iterations = it
		z := mulRows(x, tt)
		colSq := make([]float64, k)
		for i := 0; i < p; i++ {
			for c := 0; c < k; c++ {
				colSq[c] += float64(z[i][c] * z[i][c])
			}
		}
		m := make([][]float64, p)
		for i := 0; i < p; i++ {
			m[i] = make([]float64, k)
			for c := 0; c < k; c++ {
				zc := z[i][c]
				m[i][c] = float64(float64(zc*zc)*zc) - float64(zc*colSq[c])/pf
			}
		}
		b := mulRowsTN(x, m)
		u, s, v := svdRows(b)
		tt = mulRowsNT(u, v)
		dpast := d
		d = 0
		for _, sv := range s {
			d += sv
		}
		if d < float64(dpast*(1+varimaxEps)) {
			res.Converged = true
			break
		}
	}
	z := mulRows(x, tt)
	for i := range z {
		for c := range z[i] {
			z[i][c] = float64(z[i][c] * sc[i])
		}
	}
	res.Loadings = z
	res.Rotation = tt
	res.Phi = identityRows(k)
	return res
}

// promaxRotate is stats::promax(a, m = kappa) on top of varimaxRotate;
// Φ = (UᵀU)⁻¹ over the rescaled U (psych's solve(rotmat) %*%
// t(solve(rotmat)), the same matrix).
func promaxRotate(a [][]float64, kappa float64) (rotationResult, error) {
	if kappa == 0 {
		kappa = promaxDefaultKappa
	}
	vm := varimaxRotate(a)
	x := vm.Loadings
	p, k := len(x), len(x[0])
	q := make([][]float64, p)
	for i := range x {
		q[i] = make([]float64, k)
		for c, v := range x[i] {
			q[i][c] = float64(v * math.Pow(math.Abs(v), kappa-1))
		}
	}
	// lm.fit(x, Q): U = (xᵀx)⁻¹·xᵀQ.
	xtxInv, err := linalg.InverseSPD(symFromRows(mulRowsTN(x, x)))
	if err != nil {
		return rotationResult{}, err
	}
	u := mulRows(xtxInv.ToRows(), mulRowsTN(x, q))
	utuInv, err := linalg.InverseSPD(symFromRows(mulRowsTN(u, u)))
	if err != nil {
		return rotationResult{}, err
	}
	for c := 0; c < k; c++ {
		s := math.Sqrt(utuInv.At(c, c))
		for r := 0; r < k; r++ {
			u[r][c] = float64(u[r][c] * s)
		}
	}
	phi, err := linalg.InverseSPD(symFromRows(mulRowsTN(u, u)))
	if err != nil {
		return rotationResult{}, err
	}
	return rotationResult{
		Loadings: mulRows(x, u),
		Rotation: mulRows(vm.Rotation, u),
		Phi:      phi.ToRows(),
		Oblique:  true,
		// The iterative part is the varimax start.
		Solver: "varimax", Iterations: vm.Iterations, MaxIterations: vm.MaxIterations,
		Tolerance: vm.Tolerance, Converged: vm.Converged,
	}, nil
}

// obliminRotate is GPArotation::oblimin(a, gam = gamma) — GPFoblq with
// the default algorithm "bb" (Barzilai–Borwein step, clamped to
// [1e-10, 20]; the first step doubles α from 1), the non-monotone
// Armijo test against the largest criterion value of the last 10
// iterates, at most 11 halvings per iteration, eps 1e-5, maxit 2000.
func obliminRotate(a [][]float64, gamma float64) (rotationResult, error) {
	k := len(a[0])
	tm := identityRows(k)
	alpha := 1.0
	var tPrev, gpPrev [][]float64
	tInv := safeInverse(tm)
	l := mulRowsNT(a, tInv) // A·(T⁻¹)ᵀ
	gq, f := obliminCriterion(l, gamma)
	g := negTranspose(mulRows(mulRowsTN(l, gq), tInv))
	fs := make([]float64, 0, 64)
	res := rotationResult{Solver: "gpa_oblimin", MaxIterations: gpaMaxIter, Tolerance: gpaEps, Oblique: true}
	var s float64
	for iter := 0; iter <= gpaMaxIter; iter++ {
		res.Iterations = iter
		// Gp = G − T·diag(colSums(T∘G))
		gp := cloneRows(g)
		for c := 0; c < k; c++ {
			cs := 0.0
			for r := 0; r < k; r++ {
				cs += float64(tm[r][c] * g[r][c])
			}
			for r := 0; r < k; r++ {
				gp[r][c] -= float64(tm[r][c] * cs)
			}
		}
		ss := 0.0
		for r := range gp {
			for _, v := range gp[r] {
				ss += float64(v * v)
			}
		}
		s = math.Sqrt(ss)
		fs = append(fs, f)
		if s < gpaEps {
			break
		}
		if tPrev != nil {
			sdT, sdTdG, sdG := 0.0, 0.0, 0.0
			for r := 0; r < k; r++ {
				for c := 0; c < k; c++ {
					dt := tm[r][c] - tPrev[r][c]
					dg := gp[r][c] - gpPrev[r][c]
					sdT += float64(dt * dt)
					sdTdG += float64(dt * dg)
					sdG += float64(dg * dg)
				}
			}
			if sdG > 0 {
				alpha = sdT / math.Abs(sdTdG)
				alpha = math.Max(1e-10, math.Min(alpha, 20))
			}
		} else {
			alpha = 2 * alpha
		}
		target := math.Inf(-1)
		for _, v := range fs[max(0, iter+1-gpaWindow):] {
			target = math.Max(target, v)
		}
		var tNext, tNextInv, gqt [][]float64
		var ft float64
		for i := 0; i <= gpaLineSteps; i++ {
			x := make([][]float64, k)
			for r := range x {
				x[r] = make([]float64, k)
				for c := range x[r] {
					x[r][c] = tm[r][c] - float64(alpha*gp[r][c])
				}
			}
			for c := 0; c < k; c++ {
				cs := 0.0
				for r := 0; r < k; r++ {
					cs += float64(x[r][c] * x[r][c])
				}
				v := 1 / math.Sqrt(cs)
				for r := 0; r < k; r++ {
					x[r][c] = float64(x[r][c] * v)
				}
			}
			tNext = x
			tNextInv = safeInverse(tNext)
			l = mulRowsNT(a, tNextInv)
			gqt, ft = obliminCriterion(l, gamma)
			if target-ft > float64(float64(0.5*float64(s*s))*alpha) {
				break
			}
			alpha /= 2
		}
		tPrev, gpPrev = tm, gp
		tm = tNext
		f = ft
		g = negTranspose(mulRows(mulRowsTN(l, gqt), tNextInv))
	}
	res.Converged = s < gpaEps
	res.Loadings = l
	res.Rotation = transposeRows(safeInverse(tm))
	res.Phi = mulRowsTN(tm, tm)
	return res, nil
}

// obliminCriterion is GPArotation's vgQ.oblimin: X = rowSums(L²) − L²
// (column-centred by γ/p·colSums(X) when γ ≠ 0), gradient L∘X and
// criterion Σ(L²∘X)/4.
func obliminCriterion(l [][]float64, gamma float64) ([][]float64, float64) {
	p := len(l)
	k := len(l[0])
	x := make([][]float64, p)
	for i := range l {
		rs := 0.0
		for _, v := range l[i] {
			rs += float64(v * v)
		}
		x[i] = make([]float64, k)
		for c, v := range l[i] {
			x[i][c] = rs - float64(v*v)
		}
	}
	if gamma != 0 {
		g := gamma / float64(p)
		for c := 0; c < k; c++ {
			cs := 0.0
			for i := 0; i < p; i++ {
				cs += x[i][c]
			}
			for i := 0; i < p; i++ {
				x[i][c] -= float64(g * cs)
			}
		}
	}
	gq := make([][]float64, p)
	f := 0.0
	for i := range l {
		gq[i] = make([]float64, k)
		for c, v := range l[i] {
			gq[i][c] = float64(v * x[i][c])
			f += float64(float64(v*v) * x[i][c])
		}
	}
	return gq, f / 4
}

// canonicalizeRotation applies the sign-and-order rule in place.
func canonicalizeRotation(res *rotationResult) {
	k := len(res.Phi)
	v := rotationVariance(res.Loadings, res.Phi)
	order := make([]int, k)
	for c := range order {
		order[c] = c
	}
	sort.SliceStable(order, func(i, j int) bool { return v[order[i]] > v[order[j]] })
	signs := make([]float64, k)
	for out, src := range order {
		sum := 0.0
		for i := range res.Loadings {
			sum += res.Loadings[i][src]
		}
		signs[out] = 1
		if sum < 0 {
			signs[out] = -1
		}
	}
	permute := func(m [][]float64) [][]float64 {
		out := make([][]float64, len(m))
		for i := range m {
			out[i] = make([]float64, k)
			for c, src := range order {
				out[i][c] = m[i][src] * signs[c]
			}
		}
		return out
	}
	phi := make([][]float64, k)
	for r, sr := range order {
		phi[r] = make([]float64, k)
		for c, sc := range order {
			phi[r][c] = res.Phi[sr][sc] * signs[r] * signs[c]
		}
	}
	variance := make([]float64, k)
	for c, src := range order {
		variance[c] = v[src]
	}
	res.Loadings = permute(res.Loadings)
	res.Rotation = permute(res.Rotation)
	res.Phi = phi
	res.Variance = variance
	res.Order = order
	res.Signs = signs
}

// rotationVariance is diag(Φ·LᵀL): column c's variance accounted.
func rotationVariance(l, phi [][]float64) []float64 {
	k := len(phi)
	ltl := mulRowsTN(l, l)
	out := make([]float64, k)
	for c := 0; c < k; c++ {
		s := 0.0
		for m := 0; m < k; m++ {
			s += float64(phi[c][m] * ltl[m][c])
		}
		out[c] = s
	}
	return out
}

// safeInverse is GPArotation's safe_inverse: the LU inverse (partial
// pivoting, LAPACK dgesv's choice), or on an exactly singular pivot the
// SVD pseudo-inverse dropping singular values ≤ √ε.
func safeInverse(m [][]float64) [][]float64 {
	if inv, ok := luInverse(m); ok {
		return inv
	}
	u, s, v := svdRows(m)
	n := len(m)
	tol := math.Sqrt(linalg.Epsilon)
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			acc := 0.0
			for c, sv := range s {
				if sv > tol {
					acc += float64(float64(v[i][c]/sv) * u[j][c])
				}
			}
			out[i][j] = acc
		}
	}
	return out
}

// luInverse inverts the square m by Gauss–Jordan elimination with
// partial pivoting; false on an exactly zero pivot.
func luInverse(m [][]float64) ([][]float64, bool) {
	n := len(m)
	a := make([][]float64, n)
	for i := range m {
		a[i] = make([]float64, 2*n)
		copy(a[i], m[i])
		a[i][n+i] = 1
	}
	for c := 0; c < n; c++ {
		piv := c
		for r := c + 1; r < n; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[piv][c]) {
				piv = r
			}
		}
		if a[piv][c] == 0 || math.IsNaN(a[piv][c]) {
			return nil, false
		}
		a[c], a[piv] = a[piv], a[c]
		d := a[c][c]
		for j := range a[c] {
			a[c][j] /= d
		}
		for r := 0; r < n; r++ {
			if r == c || a[r][c] == 0 {
				continue
			}
			f := a[r][c]
			for j := range a[r] {
				a[r][j] -= float64(f * a[c][j])
			}
		}
	}
	out := make([][]float64, n)
	for i := range a {
		out[i] = append([]float64(nil), a[i][n:]...)
	}
	return out, true
}

// svdRows is linalg.SVD over rows: U (as rows), σ, V (as rows). The
// input is finite (loadings and rotation iterates), so a failure is a
// non-convergence the caller cannot act on; it returns zero factors,
// which leave T unchanged-to-zero and stop the fit at its cap.
func svdRows(m [][]float64) ([][]float64, []float64, [][]float64) {
	mm, err := linalg.NewMatrixFromRows(m)
	if err != nil {
		return nil, nil, nil
	}
	r, err := linalg.SVD(mm)
	if err != nil {
		return identityRows(len(m)), make([]float64, len(m)), identityRows(len(m))
	}
	return r.U.ToRows(), r.Values.Slice(), r.V.ToRows()
}

// mulRows is a·b.
func mulRows(a, b [][]float64) [][]float64 {
	n, inner := len(a), len(b)
	cols := 0
	if inner > 0 {
		cols = len(b[0])
	}
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, cols)
		for j := 0; j < cols; j++ {
			s := 0.0
			for t := 0; t < inner; t++ {
				s += float64(a[i][t] * b[t][j])
			}
			out[i][j] = s
		}
	}
	return out
}

// mulRowsTN is aᵀ·b.
func mulRowsTN(a, b [][]float64) [][]float64 {
	rows := 0
	if len(a) > 0 {
		rows = len(a[0])
	}
	cols := 0
	if len(b) > 0 {
		cols = len(b[0])
	}
	out := make([][]float64, rows)
	for i := 0; i < rows; i++ {
		out[i] = make([]float64, cols)
		for j := 0; j < cols; j++ {
			s := 0.0
			for t := range a {
				s += float64(a[t][i] * b[t][j])
			}
			out[i][j] = s
		}
	}
	return out
}

// mulRowsNT is a·bᵀ.
func mulRowsNT(a, b [][]float64) [][]float64 {
	out := make([][]float64, len(a))
	for i := range a {
		out[i] = make([]float64, len(b))
		for j := range b {
			s := 0.0
			for t := range a[i] {
				s += float64(a[i][t] * b[j][t])
			}
			out[i][j] = s
		}
	}
	return out
}

func transposeRows(a [][]float64) [][]float64 {
	if len(a) == 0 {
		return nil
	}
	out := make([][]float64, len(a[0]))
	for j := range out {
		out[j] = make([]float64, len(a))
		for i := range a {
			out[j][i] = a[i][j]
		}
	}
	return out
}

func negTranspose(a [][]float64) [][]float64 {
	out := transposeRows(a)
	for i := range out {
		for j := range out[i] {
			out[i][j] = -out[i][j]
		}
	}
	return out
}

func identityRows(k int) [][]float64 {
	out := make([][]float64, k)
	for i := range out {
		out[i] = make([]float64, k)
		out[i][i] = 1
	}
	return out
}

func cloneRows(a [][]float64) [][]float64 {
	out := make([][]float64, len(a))
	for i := range a {
		out[i] = append([]float64(nil), a[i]...)
	}
	return out
}

// symFromRows packs the upper triangle of the square symmetric m.
func symFromRows(m [][]float64) *linalg.Sym {
	n := len(m)
	s, _ := linalg.NewSym(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			s.Set(i, j, m[i][j])
		}
	}
	return s
}
