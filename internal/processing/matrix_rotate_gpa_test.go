package processing

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/vectors"
)

// matrix_rotate_gpa_test.go pins the generic gradient-projection driver
// (gpaRotate: gpforth + gpfoblq). Oblimin, refactored onto gpfoblq,
// must stay BIT-identical to the pre-driver inline loop, retained below
// verbatim as obliminRotateReference and compared live in the same
// process (both sides Gauss–Jordan and FMA-free, so no stored bits);
// gpforth, whose first production criteria land with the next
// rotations, is pinned through the varimax criterion against the SVD
// varimax the engine ships.

// gpaRotationInputs is every mv_rotation.json input plus seeded
// synthetic loadings at wider k, so the bitwise gate also covers more
// columns, harder fits and the capped (non-converged) path.
func gpaRotationInputs(t *testing.T) map[string][][]float64 {
	t.Helper()
	doc := loadReference[rotationCase](t, "mv_rotation")
	if len(doc.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	in := map[string][][]float64{}
	for _, c := range doc.Cases {
		in[fmt.Sprintf("%s/%s/k%d", c.Fixture, c.Weight, c.K)] = c.Unrotated
	}
	rng := rand.New(rand.NewPCG(406, 1))
	for _, dims := range [][2]int{{6, 2}, {9, 3}, {12, 4}, {15, 5}} {
		a := make([][]float64, dims[0])
		for i := range a {
			a[i] = make([]float64, dims[1])
			for c := range a[i] {
				a[i][c] = float64(rng.Float64()*1.6) - 0.8
			}
		}
		in[fmt.Sprintf("synthetic/p%d/k%d", dims[0], dims[1])] = a
	}
	return in
}

func gpaSameBits(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

func sameBitRows(t *testing.T, where string, got, want [][]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, reference %d", where, len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("%s[%d]: %d cols, reference %d", where, i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			if !gpaSameBits(got[i][j], want[i][j]) {
				t.Errorf("%s[%d][%d] = %.17g, reference %.17g", where, i, j, got[i][j], want[i][j])
			}
		}
	}
}

func sameBitRotation(t *testing.T, got, want rotationResult) {
	t.Helper()
	sameBitRows(t, "loadings", got.Loadings, want.Loadings)
	sameBitRows(t, "rotation", got.Rotation, want.Rotation)
	sameBitRows(t, "phi", got.Phi, want.Phi)
	if len(got.Variance) != len(want.Variance) {
		t.Fatalf("variance %v, reference %v", got.Variance, want.Variance)
	}
	for c := range want.Variance {
		if !gpaSameBits(got.Variance[c], want.Variance[c]) {
			t.Errorf("variance[%d] = %.17g, reference %.17g", c, got.Variance[c], want.Variance[c])
		}
	}
	if fmt.Sprint(got.Order, got.Signs) != fmt.Sprint(want.Order, want.Signs) {
		t.Errorf("order/signs %v %v, reference %v %v", got.Order, got.Signs, want.Order, want.Signs)
	}
	if got.Solver != want.Solver || got.Iterations != want.Iterations || got.MaxIterations != want.MaxIterations ||
		!gpaSameBits(got.Tolerance, want.Tolerance) || got.Converged != want.Converged || got.Oblique != want.Oblique {
		t.Errorf("run %s/%d/%d/%g/%v/%v, reference %s/%d/%d/%g/%v/%v",
			got.Solver, got.Iterations, got.MaxIterations, got.Tolerance, got.Converged, got.Oblique,
			want.Solver, want.Iterations, want.MaxIterations, want.Tolerance, want.Converged, want.Oblique)
	}
}

// gpaVarimaxCriterion is GPArotation's vgQ.varimax: QL = L² −
// colMeans(L²), gradient −L∘QL, value −ΣQL²/4.
func gpaVarimaxCriterion(l [][]float64) ([][]float64, float64) {
	p, k := len(l), len(l[0])
	mean := make([]float64, k)
	for i := range l {
		for c, v := range l[i] {
			mean[c] += float64(v * v)
		}
	}
	for c := range mean {
		mean[c] /= float64(p)
	}
	gq := make([][]float64, p)
	f := 0.0
	for i := range l {
		gq[i] = make([]float64, k)
		for c, v := range l[i] {
			q := float64(v*v) - mean[c]
			gq[i][c] = -float64(v * q)
			f += float64(q * q)
		}
	}
	return gq, -f / 4
}

// gpforthKaiserVarimax is GPArotation::Varimax(a, normalize = TRUE):
// Kaiser-normalised rows (a zero row stays, as in varimaxRotate), the
// orthogonal driver on vgQ.varimax, rows scaled back.
func gpforthKaiserVarimax(a [][]float64) rotationResult {
	x := cloneRows(a)
	sc := make([]float64, len(x))
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
	res := gpforth(x, gpaVarimaxCriterion, "gpa_varimax")
	for i := range res.Loadings {
		for c := range res.Loadings[i] {
			res.Loadings[i][c] = float64(res.Loadings[i][c] * sc[i])
		}
	}
	return res
}

// TestGPForth_VarimaxMatchesROracle pins the orthogonal driver step
// for step: GPForth on vgQ.varimax with Kaiser normalization against
// GPArotation::GPForth (mv_rotation.json gpforth_varimax) after the
// sign-and-order rule — order and signs exactly, loadings and rotation
// within rotationOracleTol (the trial-step polar factor is gonum's SVD
// against LAPACK's, so rounding only), and the same iteration count.
func TestGPForth_VarimaxMatchesROracle(t *testing.T) {
	doc := loadReference[rotationCase](t, "mv_rotation")
	for _, c := range doc.Cases {
		t.Run(fmt.Sprintf("%s/%s/k%d", c.Fixture, c.Weight, c.K), func(t *testing.T) {
			want := c.GPForthVarimax
			if want.Iterations == nil {
				t.Fatal("oracle case has no gpforth_varimax")
			}
			got := gpforthKaiserVarimax(c.Unrotated)
			if !got.Converged {
				t.Fatalf("not converged after %d iterations", got.Iterations)
			}
			if got.Iterations != *want.Iterations {
				t.Errorf("GPA iterations %d, R %d", got.Iterations, *want.Iterations)
			}
			canonicalizeRotation(&got)
			if fmt.Sprint(got.Order, got.Signs) != fmt.Sprint(want.Order, want.Signs) {
				t.Fatalf("order %v signs %v, R %v %v", got.Order, got.Signs, want.Order, want.Signs)
			}
			rotNearRows(t, "loadings", got.Loadings, want.Loadings)
			rotNearRows(t, "rotation", got.Rotation, want.Rotation)
			rotNearRows(t, "phi", got.Phi, want.Phi)
			for i, w := range want.Variance {
				rotNear(t, fmt.Sprintf("variance[%d]", i), got.Variance[i], w)
			}
		})
	}
}

// gpforthVarimaxTol is how close two DIFFERENT varimax algorithms land
// on the same optimum. stats::varimax stops when Σσ grows by < 1e-5
// relative, GPForth when ‖Gp‖_F < 1e-5, so neither iterate is the
// optimum to better than its own stopping rule: in R itself
// GPForth(varimax, normalize = TRUE) and stats::varimax differ by up to
// 8.7e-4 per rotation cell on the oracle inputs, and the engine's pair
// by 1.0e-3. Step-for-step agreement is TestGPForth_VarimaxMatchesROracle's
// job; this bound proves the driver reaches the production optimum.
const gpforthVarimaxTol = 2e-3

// TestGPForth_VarimaxCriterionMatchesSVDVarimax: the orthogonal driver
// on the varimax criterion (Kaiser-normalised, GPArotation's Varimax)
// converges to the optimum the SVD fixed point (stats::varimax, the
// production varimax) reaches — same column order and signs after the
// sign-and-order rule, loadings and rotation within gpforthVarimaxTol —
// and its result is a proper orthogonal rotation: TᵀT = I, Φ = I
// exactly, L = A·T, row communalities unchanged. The synthetic inputs
// (no factor structure, several local varimax optima) check only the
// orthogonality invariants: the two algorithms may legitimately stop
// in different optima there.
func TestGPForth_VarimaxCriterionMatchesSVDVarimax(t *testing.T) {
	for name, a := range gpaRotationInputs(t) {
		t.Run(name, func(t *testing.T) {
			got := gpforthKaiserVarimax(a)
			if !got.Converged {
				t.Fatalf("not converged after %d iterations", got.Iterations)
			}
			if got.Oblique || got.Solver != "gpa_varimax" || got.MaxIterations != gpaMaxIter || got.Tolerance != gpaEps {
				t.Errorf("run oblique=%v solver=%s cap=%d tol=%g", got.Oblique, got.Solver, got.MaxIterations, got.Tolerance)
			}
			const tight = 1e-12
			k := len(got.Rotation)
			ttt := mulRowsTN(got.Rotation, got.Rotation)
			for i := 0; i < k; i++ {
				for j := 0; j < k; j++ {
					want := 0.0
					if i == j {
						want = 1
					}
					if math.Abs(ttt[i][j]-want) > tight {
						t.Errorf("TᵀT[%d][%d] = %v", i, j, ttt[i][j])
					}
					if got.Phi[i][j] != want {
						t.Errorf("phi[%d][%d] = %v, want exactly %v", i, j, got.Phi[i][j], want)
					}
				}
			}
			at := mulRows(a, got.Rotation)
			for i := range a {
				h0, h1 := 0.0, 0.0
				for c := range a[i] {
					if math.Abs(at[i][c]-got.Loadings[i][c]) > tight*(1+math.Abs(at[i][c])) {
						t.Errorf("A·T[%d][%d] = %v, loadings %v", i, c, at[i][c], got.Loadings[i][c])
					}
					h0 += float64(a[i][c] * a[i][c])
					h1 += float64(got.Loadings[i][c] * got.Loadings[i][c])
				}
				if math.Abs(h0-h1) > tight*(1+h0) {
					t.Errorf("communality[%d] %v → %v", i, h0, h1)
				}
			}
			if strings.HasPrefix(name, "synthetic/") {
				return
			}
			canonicalizeRotation(&got)
			want, err := rotateLoadings(a, rotationSpec{Method: vectors.RotationVarimax})
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got.Order, got.Signs) != fmt.Sprint(want.Order, want.Signs) {
				t.Fatalf("order/signs %v %v, SVD varimax %v %v", got.Order, got.Signs, want.Order, want.Signs)
			}
			for _, m := range []struct {
				what      string
				got, want [][]float64
			}{{"loadings", got.Loadings, want.Loadings}, {"rotation", got.Rotation, want.Rotation}} {
				for i := range m.want {
					for c := range m.want[i] {
						if d := math.Abs(m.got[i][c] - m.want[i][c]); d > gpforthVarimaxTol {
							t.Errorf("%s[%d][%d] = %.12g, SVD varimax %.12g (|Δ| %.2g)", m.what, i, c, m.got[i][c], m.want[i][c], d)
						}
					}
				}
			}
		})
	}
}

// obliminRotateReference is GPArotation::oblimin(a, gam = gamma) — GPFoblq with
// the default algorithm "bb" (Barzilai–Borwein step, clamped to
// [1e-10, 20]; the first step doubles α from 1), the non-monotone
// Armijo test against the largest criterion value of the last 10
// iterates, at most 11 halvings per iteration, eps 1e-5, maxit 2000.
func obliminRotateReference(a [][]float64, gamma float64) (rotationResult, error) {
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
