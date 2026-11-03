package sem

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// The SEM matrix helpers (matops.go) replay gonum's mat methods over
// internal/gonumorder. Two tests pin them:
//
//   - TestSEM_MatOpsMatchGonum: on amd64 every helper equals the gonum
//     mat method it replaces bit for bit, over a corpus reaching every
//     dispatch arm (Dgemm with either operand transposed, Dsymm left and
//     right through the transpose workspaces, the generic loop, Dgemv,
//     Dsymv), the blocked LU and level-3 paths (n > 64), ill-conditioned,
//     exactly singular, tiny-pivot and non-finite inverses, and refused
//     factorisations. So routing SEM through them moved no amd64 figure.
//     gonum's bits are its amd64 assembly order only, so the comparison
//     binds on amd64; elsewhere it checks agreement to rounding.
//   - TestSEM_MatOpsBitPinned: the corpus digests to one pinned SHA-256
//     on every architecture — the cross-architecture bit contract.

// matOpsRNG is a uniform-only generator: its draws are integer
// arithmetic plus one exact scaling, the same on every architecture.
func matOpsRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x51ed2701fa1c3b7d))
}

// uniformDense is an r×c matrix of draws in (−1, 1)·(1 + j mod 3).
func uniformDense(rng *rand.Rand, r, c int) *mat.Dense {
	d := mat.NewDense(r, c, nil)
	for i := 0; i < r; i++ {
		for j := 0; j < c; j++ {
			d.Set(i, j, float64((rng.Float64()*2-1)*float64(1+j%3)))
		}
	}
	return d
}

// gramSym is XᵀX for a (n+3)×n uniform X (barriered row-order sums), a
// symmetric positive-definite matrix.
func gramSym(rng *rand.Rand, n int) *mat.SymDense {
	x := uniformDense(rng, n+3, n)
	s := mat.NewSymDense(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			v := 0.0
			for r := 0; r < n+3; r++ {
				v += float64(x.At(r, i) * x.At(r, j))
			}
			s.SetSym(i, j, v)
		}
	}
	return s
}

// symOf is the symmetric matrix of a square dense one's upper triangle.
func symOf(d *mat.Dense) *mat.SymDense {
	n, _ := d.Dims()
	s := mat.NewSymDense(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			s.SetSym(i, j, d.At(i, j))
		}
	}
	return s
}

// matOpsSizes cross the unblocked / blocked (> 64) boundary of Dgemm,
// Dgetrf, Dgetri, Dpotrf, Dtrtri and Dlauum.
var matOpsSizes = []int{1, 2, 3, 4, 5, 7, 9, 16, 33, 64, 65, 66, 100, 130}

// matOpsFigure is one corpus output: a helper's result and gonum's.
type matOpsFigure struct {
	name      string
	got, want []float64
	// gotErr / wantErr are the condition the helper and gonum report
	// (0 for none, the mat.Condition value otherwise, -1 for a refused
	// factorisation).
	gotErr, wantErr float64
	// chaotic marks a figure of a (near-)singular problem, whose last
	// bits are amplified past any rounding bound: compared on amd64 only.
	chaotic bool
}

func condOf(err error) float64 {
	if err == nil {
		return 0
	}
	if c, ok := err.(mat.Condition); ok {
		return float64(c)
	}
	return -1
}

func denseData(d mat.Matrix) []float64 {
	r, c := d.Dims()
	out := make([]float64, 0, r*c)
	for i := 0; i < r; i++ {
		for j := 0; j < c; j++ {
			out = append(out, d.At(i, j))
		}
	}
	return out
}

// matOpsCorpus runs every helper and its gonum counterpart over the
// corpus. gonum's figures are computed on whatever architecture runs it.
func matOpsCorpus() []matOpsFigure {
	rng := matOpsRNG(78)
	var out []matOpsFigure
	add := func(name string, got, want []float64, gotErr, wantErr float64) {
		out = append(out, matOpsFigure{name: name, got: got, want: want, gotErr: gotErr, wantErr: wantErr})
	}
	addChaotic := func(name string, got, want []float64, gotErr, wantErr float64) {
		out = append(out, matOpsFigure{name: name, got: got, want: want, gotErr: gotErr, wantErr: wantErr, chaotic: true})
	}
	gonumMul := func(a, b mat.Matrix) []float64 {
		var c mat.Dense
		c.Mul(a, b)
		return denseData(&c)
	}
	for _, n := range matOpsSizes {
		m, k := n+1, max(n-1, 1)
		a := uniformDense(rng, m, k)  // m×k
		b := uniformDense(rng, k, n)  // k×n
		bt := uniformDense(rng, n, k) // n×k, used transposed
		at := uniformDense(rng, k, m) // k×m, used transposed
		sk := symOf(uniformDense(rng, k, k))
		sm := symOf(uniformDense(rng, m, m))
		tag := fmt.Sprintf("n=%d", n)
		add(tag+" mul Dense·Dense", denseData(mul(a, b)), gonumMul(a, b), 0, 0)
		add(tag+" mul Dense·Denseᵀ", denseData(mul(a, bt.T())), gonumMul(a, bt.T()), 0, 0)
		add(tag+" mul Denseᵀ·Dense", denseData(mul(at.T(), b)), gonumMul(at.T(), b), 0, 0)
		add(tag+" mul Dense·Sym", denseData(mul(a, sk)), gonumMul(a, sk), 0, 0)
		add(tag+" mul Denseᵀ·Sym", denseData(mul(at.T(), sk)), gonumMul(at.T(), sk), 0, 0)
		add(tag+" mul Sym·Dense", denseData(mul(sm, a)), gonumMul(sm, a), 0, 0)
		add(tag+" mul Sym·Denseᵀ", denseData(mul(sm, at.T())), gonumMul(sm, at.T()), 0, 0)
		add(tag+" mul Sym·Sym", denseData(mul(sm, sm)), gonumMul(sm, sm), 0, 0)

		x := make([]float64, k)
		for i := range x {
			x[i] = float64(rng.Float64()*2) - 1
		}
		var gv mat.VecDense
		gv.MulVec(a, mat.NewVecDense(k, append([]float64(nil), x...)))
		add(tag+" mulVec Dense", mulVec(a, x), gv.RawVector().Data, 0, 0)
		var gs mat.VecDense
		gs.MulVec(sk, mat.NewVecDense(k, append([]float64(nil), x...)))
		add(tag+" mulVec Sym", mulVec(sk, x), gs.RawVector().Data, 0, 0)

		// Inverses: diagonally weighted, plain (pivoting), and a
		// near-singular one (a duplicated row nudged by 2^-40).
		sq := uniformDense(rng, n, n)
		dom := mat.DenseCopyOf(sq)
		for i := 0; i < n; i++ {
			dom.Set(i, i, dom.At(i, i)+float64(n))
		}
		near := mat.DenseCopyOf(sq)
		if n > 1 {
			for j := 0; j < n; j++ {
				near.Set(n-1, j, near.At(0, j)+math.Ldexp(near.At(1, j), -40))
			}
		}
		for _, c := range []struct {
			name string
			m    *mat.Dense
		}{{"dominant", dom}, {"plain", sq}, {"near_singular", near}} {
			got, gotErr := inverse(c.m)
			var want mat.Dense
			wantErr := want.Inverse(c.m)
			if c.name == "near_singular" {
				addChaotic(tag+" inverse "+c.name, denseData(got), denseData(&want), condOf(gotErr), condOf(wantErr))
				continue
			}
			add(tag+" inverse "+c.name, denseData(got), denseData(&want), condOf(gotErr), condOf(wantErr))
		}

		// Cholesky: an SPD gram matrix, then factor / inverse / logdet /
		// solve, against mat.Cholesky.
		g := gramSym(rng, n)
		rhs := make([]float64, n)
		for i := range rhs {
			rhs[i] = float64(rng.Float64()*2) - 1
		}
		out = append(out, choleskyFigures(tag+" cholesky gram", g, rhs)...)
	}
	// Exactly singular (two equal rows), tiny pivots (below the smallest
	// normal), non-finite and pivot-at-zero inverses.
	sing := mat.NewDense(3, 3, []float64{1, 2, 3, 4, 5, 6, 1, 2, 3})
	tiny := mat.NewDense(3, 3, []float64{0x1p-1060, 0x1p-1070, 0, 0x1p-1072, 0x1p-1061, 0x1p-1071, 0, 0x1p-1073, 0x1p-1062})
	zeroLead := mat.NewDense(3, 3, []float64{0, 1, 2, 3, 0, 1, 2, 3, 0})
	nanM := mat.NewDense(2, 2, []float64{1, math.NaN(), 2, 3})
	infM := mat.NewDense(2, 2, []float64{1, math.Inf(1), 2, 3})
	for _, c := range []struct {
		name string
		m    *mat.Dense
	}{{"singular", sing}, {"tiny", tiny}, {"zero_lead", zeroLead}, {"nan", nanM}, {"inf", infM}} {
		got, gotErr := inverse(c.m)
		var want mat.Dense
		wantErr := want.Inverse(c.m)
		addChaotic("inverse "+c.name, denseData(got), denseData(&want), condOf(gotErr), condOf(wantErr))
	}
	// Cholesky edge cases: indefinite (refused), ill-conditioned past the
	// tolerance (filled, Condition reported), and extreme scaling.
	ind := gramSym(rng, 6)
	for i := 0; i < 6; i++ {
		ind.SetSym(i, i, ind.At(i, i)-50)
	}
	ill := mat.NewSymDense(3, []float64{1, 1, 1, 1, 1 + 0x1p-50, 1, 1, 1, 1 + 0x1p-49})
	scaled := gramSym(rng, 10)
	for i := 0; i < 10; i++ {
		for j := i; j < 10; j++ {
			scaled.SetSym(i, j, math.Ldexp(scaled.At(i, j), -300+60*(i+j)))
		}
	}
	for _, c := range []struct {
		name string
		s    *mat.SymDense
	}{{"indefinite", ind}, {"ill", ill}, {"scaled", scaled}} {
		n := c.s.SymmetricDim()
		rhs := make([]float64, n)
		for i := range rhs {
			rhs[i] = float64(i+1) / 7
		}
		out = append(out, choleskyFigures("cholesky "+c.name, c.s, rhs)...)
	}
	return out
}

// choleskyFigures is factorize / inverse / logDet / solveVec against
// mat.Cholesky Factorize / InverseTo / LogDet / SolveVecTo.
func choleskyFigures(tag string, s *mat.SymDense, rhs []float64) []matOpsFigure {
	f, ok := factorize(s)
	var ch mat.Cholesky
	gok := ch.Factorize(s)
	okFig := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	out := []matOpsFigure{{name: tag + " factorize ok", got: []float64{okFig(ok)}, want: []float64{okFig(gok)}}}
	if !ok || !gok {
		return out
	}
	inv, ierr := f.inverse()
	var ginv mat.SymDense
	gierr := ch.InverseTo(&ginv)
	chaotic := f.cond > 1e10
	out = append(out, matOpsFigure{name: tag + " inverse", got: denseData(inv), want: denseData(&ginv), gotErr: condOf(ierr), wantErr: condOf(gierr), chaotic: chaotic})
	out = append(out, matOpsFigure{name: tag + " logdet", got: []float64{f.logDet()}, want: []float64{ch.LogDet()}})
	out = append(out, matOpsFigure{name: tag + " cond", got: []float64{f.cond}, want: []float64{ch.Cond()}, chaotic: chaotic})
	x, xerr := f.solveVec(rhs)
	var gx mat.VecDense
	gxerr := ch.SolveVecTo(&gx, mat.NewVecDense(len(rhs), append([]float64(nil), rhs...)))
	out = append(out, matOpsFigure{name: tag + " solve", got: x, want: gx.RawVector().Data, gotErr: condOf(xerr), wantErr: condOf(gxerr), chaotic: chaotic})
	return out
}

// sameBits is bit equality with every NaN equal to every NaN (a NaN's
// sign is not part of the contract: 0/0 is 0x7ff8… on arm64, 0xfff8… on
// amd64).
func sameBits(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

func TestSEM_MatOpsMatchGonum(t *testing.T) {
	t.Parallel()

	exact := runtime.GOARCH == "amd64"
	for _, f := range matOpsCorpus() {
		if !exact && f.chaotic {
			continue
		}
		if !sameBits(f.gotErr, f.wantErr) && (exact || (f.gotErr == 0) != (f.wantErr == 0)) {
			t.Errorf("%s: condition %v, gonum %v", f.name, f.gotErr, f.wantErr)
		}
		if len(f.got) != len(f.want) {
			t.Errorf("%s: %d figures, gonum %d", f.name, len(f.got), len(f.want))
			continue
		}
		for i := range f.got {
			g, w := f.got[i], f.want[i]
			if exact {
				if !sameBits(g, w) {
					t.Errorf("%s[%d] = %v (%#016x), gonum %v (%#016x)", f.name, i, g, math.Float64bits(g), w, math.Float64bits(w))
					break
				}
				continue
			}
			// Off amd64 gonum's own last bits are its FMA-contracted pure
			// Go: agreement to rounding, scaled by the figure (an
			// ill-conditioned inverse amplifies it).
			if math.IsNaN(g) != math.IsNaN(w) || (!math.IsNaN(g) && math.Abs(g-w) > 1e-6*math.Max(1, math.Abs(w))) {
				t.Errorf("%s[%d] = %v, gonum %v", f.name, i, g, w)
				break
			}
		}
	}
}

// matOpsDigestPin is the SHA-256 of the helpers' corpus figures (NaN
// canonical), generated on darwin/arm64 and checked by CI on
// linux/amd64: the helpers' bits are architecture-independent.
const matOpsDigestPin = "5d41d5fc56a02a16bb2a0f33f189515a9966a6ae0cc2d50b612b855353795478"

func matOpsDigest() string {
	h := sha256.New()
	var buf [8]byte
	put := func(v float64) {
		bits := math.Float64bits(v)
		if math.IsNaN(v) {
			bits = 0x7ff8000000000001
		}
		binary.LittleEndian.PutUint64(buf[:], bits)
		h.Write(buf[:])
	}
	for _, f := range matOpsCorpus() {
		h.Write([]byte(f.name))
		put(f.gotErr)
		for _, v := range f.got {
			put(v)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestSEM_MatOpsBitPinned(t *testing.T) {
	t.Parallel()

	if got := matOpsDigest(); got != matOpsDigestPin {
		t.Fatalf("SEM matrix-helper corpus digest %s, pinned %s (GOARCH=%s): the helpers' bits moved", got, matOpsDigestPin, runtime.GOARCH)
	}
}
