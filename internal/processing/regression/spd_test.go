package regression

import (
	"math"
	"math/rand/v2"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// The regression engine reaches gonum only through linalg. These tests
// pin that the bridge (factorSPD / solveSPD / SPDFactor.Inverse /
// ridgeSandwich / backendErrorText) reproduces, bit for bit and on the
// same operands, the direct gonum calls every REG_* solver made before
// the routing: mat.NewSymDense over the row-major Gram, Cholesky
// Factorize / SolveVecTo / InverseTo, DenseCopyOf + two Dense.Mul for
// the ridge sandwich, and gonum's Condition text for the "gonum_error"
// detail. The comparison runs in-process, so it holds on every
// architecture without recording any bits. gonum is imported here as
// the independent oracle only.

// gramRowMajor returns a random p×p symmetric positive-definite matrix
// in row-major order, both triangles filled (as the accumulators leave
// it after finalize).
func gramRowMajor(rng *rand.Rand, p int) []float64 {
	rows := p + 4
	x := make([]float64, rows*p)
	for i := range x {
		x[i] = rng.NormFloat64() * float64(1+i%3)
	}
	g := make([]float64, p*p)
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			s := 0.0
			for k := 0; k < rows; k++ {
				s += x[k*p+i] * x[k*p+j]
			}
			g[i*p+j] = s
		}
	}
	return g
}

func bitsEqual(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

func TestSPDBridge_MatchesDirectGonum(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 23))
	for _, p := range []int{1, 2, 3, 4, 6, 9} {
		for trial := 0; trial < 5; trial++ {
			g := gramRowMajor(rng, p)
			b := make([]float64, p)
			for i := range b {
				b[i] = rng.NormFloat64()
			}

			// The pre-routing direct-gonum sequence.
			var chol mat.Cholesky
			if !chol.Factorize(mat.NewSymDense(p, append([]float64(nil), g...))) {
				t.Fatalf("p=%d: fixture not SPD", p)
			}
			var beta mat.VecDense
			if err := chol.SolveVecTo(&beta, mat.NewVecDense(p, append([]float64(nil), b...))); err != nil {
				t.Fatal(err)
			}
			var inv mat.SymDense
			if err := chol.InverseTo(&inv); err != nil {
				t.Fatal(err)
			}
			invDense := mat.DenseCopyOf(&inv)
			var tmp, sandwich mat.Dense
			tmp.Mul(invDense, mat.NewDense(p, p, append([]float64(nil), g...)))
			sandwich.Mul(&tmp, invDense)

			// The bridge.
			f, ok := factorSPD(p, g)
			if !ok {
				t.Fatalf("p=%d: bridge refused an SPD Gram", p)
			}
			got, err := solveSPD(f, b)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < p; i++ {
				if !bitsEqual(got[i], beta.AtVec(i)) {
					t.Fatalf("p=%d: beta[%d] %x, gonum %x", p, i, math.Float64bits(got[i]), math.Float64bits(beta.AtVec(i)))
				}
			}
			gotInv, err := f.Inverse()
			if err != nil {
				t.Fatal(err)
			}
			gotSandwich, err := ridgeSandwich(gotInv, p, g)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < p; i++ {
				for j := 0; j < p; j++ {
					if !bitsEqual(gotInv.At(i, j), inv.At(i, j)) {
						t.Fatalf("p=%d: inv[%d][%d] %x, gonum %x", p, i, j,
							math.Float64bits(gotInv.At(i, j)), math.Float64bits(inv.At(i, j)))
					}
					if !bitsEqual(gotSandwich.At(i, j), sandwich.At(i, j)) {
						t.Fatalf("p=%d: sandwich[%d][%d] %x, gonum %x", p, i, j,
							math.Float64bits(gotSandwich.At(i, j)), math.Float64bits(sandwich.At(i, j)))
					}
				}
			}
			if p > 1 {
				// The lower triangle is ignored, exactly as gonum's
				// SymDense ignored it.
				skew := append([]float64(nil), g...)
				skew[p] += 1e3 // element (1, 0)
				f2, ok := factorSPD(p, skew)
				if !ok {
					t.Fatal("lower-triangle edit changed positive definiteness")
				}
				got2, _ := solveSPD(f2, b)
				for i := range got2 {
					if !bitsEqual(got2[i], got[i]) {
						t.Fatalf("p=%d: lower triangle was read", p)
					}
				}
			}
		}
	}
}

// TestSPDBridge_SingularBehaviourUnchanged: a non-PD Gram is refused
// (every solver raises its own RANK_DEFICIENT), and an ill-conditioned
// but factorable Gram reports the "gonum_error" text gonum's own
// Condition error carried, byte for byte.
func TestSPDBridge_SingularBehaviourUnchanged(t *testing.T) {
	for name, g := range map[string][]float64{
		"rank_deficient": {1, 2, 2, 4},
		"indefinite":     {1, 3, 3, 1},
		"zero":           {0, 0, 0, 0},
	} {
		var chol mat.Cholesky
		gonumOK := chol.Factorize(mat.NewSymDense(2, append([]float64(nil), g...)))
		if _, ok := factorSPD(2, g); ok != gonumOK || ok {
			t.Errorf("%s: bridge ok=%v, gonum ok=%v", name, ok, gonumOK)
		}
	}

	g := []float64{1, 1, 1, 1 + 0x1p-52}
	var chol mat.Cholesky
	if !chol.Factorize(mat.NewSymDense(2, append([]float64(nil), g...))) {
		t.Fatal("fixture should factor")
	}
	var x mat.VecDense
	solveErr := chol.SolveVecTo(&x, mat.NewVecDense(2, []float64{1, 2}))
	var inv mat.SymDense
	invErr := chol.InverseTo(&inv)
	if solveErr == nil || invErr == nil {
		t.Fatalf("fixture not ill-conditioned for gonum (cond %v)", chol.Cond())
	}

	f, ok := factorSPD(2, g)
	if !ok {
		t.Fatal("bridge refused a factorable Gram")
	}
	_, err := solveSPD(f, []float64{1, 2})
	if err == nil || backendErrorText(err) != solveErr.Error() {
		t.Errorf("solve text %q, gonum %q", backendErrorText(err), solveErr.Error())
	}
	_, err = f.Inverse()
	if err == nil || backendErrorText(err) != invErr.Error() {
		t.Errorf("inverse text %q, gonum %q", backendErrorText(err), invErr.Error())
	}
}
