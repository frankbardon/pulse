package linalg

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/rand/v2"
	"runtime"
	"strconv"
	"testing"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas64"
	lapackgonum "gonum.org/v1/gonum/lapack/gonum"
	"gonum.org/v1/gonum/mat"
)

// The SPD path (FactorSPD, SPDFactor.Solve / Inverse / ConditionNumber,
// Mul) is an FMA-free port of gonum v0.17.0's linux/amd64 operation
// order. Two tests pin that contract:
//
//   - TestSPD_MatchesGonumOrder: on amd64 every routine — each ported
//     BLAS kernel over many shapes, and the full factor / condition /
//     solve / inverse / product over a corpus that reaches the blocked
//     (> 64) paths, ill-conditioned and refused matrices, extreme scaling
//     and non-finite elements — equals the raw gonum call bit for bit. So
//     the port moved no amd64 figure. gonum's bits are the amd64 assembly
//     order only (arm64 runs gonum's FMA-contracted pure Go), so the
//     comparison binds on amd64; CI's linux/amd64 run is the tripwire.
//   - TestSPD_BitPinned: the same kernel cases and corpus digest to one
//     pinned SHA-256 on every architecture — the cross-architecture bit
//     contract.

// spdCase is one corpus matrix: the full symmetric rows plus a
// right-hand side.
type spdCase struct {
	name string
	rows [][]float64
	b    []float64
}

// gonumOrderRNG is a uniform-only generator (math/rand/v2 PCG): its
// draws are integer arithmetic plus one exact scaling, so the corpus is
// the same on every architecture.
func gonumOrderRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// gramRows returns XᵀX for an (n+5)×n X of uniform draws in
// (−1, 1)·(1 + j mod 3), summed in row order with barriered products;
// collin > 0 makes the last column col0 + collin·(its own draw).
func gramRows(rng *rand.Rand, n int, collin float64) [][]float64 {
	rows := n + 5
	x := make([][]float64, rows)
	for r := range x {
		x[r] = make([]float64, n)
		for j := range x[r] {
			x[r][j] = float64((rng.Float64()*2 - 1) * float64(1+j%3))
		}
		if n > 1 && collin > 0 {
			x[r][n-1] = x[r][0] + float64(collin*x[r][n-1])
		}
	}
	g := make([][]float64, n)
	for i := range g {
		g[i] = make([]float64, n)
		for j := range g[i] {
			s := 0.0
			for r := 0; r < rows; r++ {
				s += float64(x[r][i] * x[r][j])
			}
			g[i][j] = s
		}
	}
	return g
}

func uniformVec(rng *rand.Rand, n int) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = float64(rng.Float64()*2) - 1
	}
	return v
}

// spdCorpus is the shared corpus of both tests.
func spdCorpus() []spdCase {
	rng := gonumOrderRNG(61)
	var out []spdCase
	add := func(name string, rows [][]float64) {
		out = append(out, spdCase{name: name, rows: rows, b: uniformVec(rng, len(rows))})
	}
	// Well-conditioned, across the unblocked / blocked (n > 64) and the
	// Dgemm k-chunked (n > 256) boundaries of Dpotrf, Dtrtri and Dlauum.
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 9, 13, 16, 31, 33, 63, 64, 65, 66, 100, 129, 130, 200, 260} {
		add("gram", gramRows(rng, n, 0))
	}
	// Near-collinear: condition numbers on both sides of
	// ConditionTolerance, and factorisations that fail.
	for _, n := range []int{2, 3, 6, 10, 70} {
		for e := -16.0; e <= -2; e += 1 {
			add("collinear", gramRows(rng, n, math.Ldexp(1, int(e*3.32))))
		}
	}
	// Extreme scaling D·G·D, d_i = 2^k with k spread over ±500: column
	// norms past Dlatrs' bignum, so its scaled (careful) paths run.
	for _, n := range []int{3, 10, 80} {
		g := gramRows(rng, n, 0)
		for i := range g {
			for j := range g[i] {
				ki := -500 + (1000*i)/max(1, n-1)
				kj := -500 + (1000*j)/max(1, n-1)
				g[i][j] = math.Ldexp(g[i][j], ki/2+kj/2)
			}
		}
		add("scaled", g)
	}
	// Indefinite and exactly singular.
	ind := gramRows(rng, 6, 0)
	for i := range ind {
		ind[i][i] -= 50
	}
	add("indefinite", ind)
	dup := gramRows(rng, 5, 0)
	for i := range dup {
		dup[i][4], dup[4][i] = dup[i][0], dup[0][i]
	}
	dup[4][4] = dup[0][0]
	add("duplicate", dup)
	// Non-finite elements.
	for _, v := range []float64{math.NaN(), math.Inf(1)} {
		off := gramRows(rng, 4, 0)
		off[1][3], off[3][1] = v, v
		add("nonfinite_off", off)
		diag := gramRows(rng, 4, 0)
		diag[2][2] = v
		add("nonfinite_diag", diag)
	}
	return out
}

// packSym builds the Sym of full symmetric rows.
func packSym(t testing.TB, rows [][]float64) *Sym {
	t.Helper()
	s, err := NewSymFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// spdRun is everything the port computes for one corpus case.
type spdRun struct {
	ok      bool      // Dpotrf succeeded
	u       []float64 // the factor, upper triangle row-major
	cond    float64
	x       []float64 // Dpotrs on b (computed even past the tolerance)
	invOK   bool      // Dpotri succeeded
	inv     []float64 // its upper triangle
	product []float64 // Mul(inverse, A)
}

func runPort(t testing.TB, c spdCase) spdRun {
	t.Helper()
	n := len(c.rows)
	f, err := FactorSPD(packSym(t, c.rows))
	if err != nil {
		return spdRun{}
	}
	r := spdRun{ok: true, u: upper(n, f.u), cond: f.ConditionNumber()}
	r.x = append([]float64(nil), c.b...)
	goDpotrsUpperVec(n, f.u, n, r.x)
	inv := append([]float64(nil), f.u...)
	r.invOK = goDpotriUpper(n, inv, n)
	r.inv = upper(n, inv)
	if r.invOK {
		full := symFull(n, inv)
		a, _ := NewMatrix(n, n, full)
		g, _ := NewMatrixFromRows(c.rows)
		p, err := Mul(a, g)
		if err != nil {
			t.Fatal(err)
		}
		r.product = p.RowMajor()
	}
	return r
}

func runGonum(c spdCase) spdRun {
	n := len(c.rows)
	full := make([]float64, 0, n*n)
	for _, row := range c.rows {
		full = append(full, row...)
	}
	var ch mat.Cholesky
	if !ch.Factorize(mat.NewSymDense(n, full)) {
		return spdRun{}
	}
	raw := ch.RawU().(*mat.TriDense).RawTriangular()
	u := make([]float64, n*n)
	for i := 0; i < n; i++ {
		copy(u[i*n+i:(i+1)*n], raw.Data[i*raw.Stride+i:i*raw.Stride+n])
	}
	r := spdRun{ok: true, u: upper(n, u), cond: ch.Cond()}
	var x mat.VecDense
	_ = ch.SolveVecTo(&x, mat.NewVecDense(n, append([]float64(nil), c.b...)))
	r.x = make([]float64, n)
	for i := range r.x {
		r.x[i] = x.AtVec(i)
	}
	// Raw Dpotri on gonum's own factor — the call InverseTo makes — so a
	// breakdown is told apart from a +Inf condition estimate.
	invU := append([]float64(nil), u...)
	r.invOK = lapackgonum.Implementation{}.Dpotri(blas.Upper, n, invU, n)
	r.inv = upper(n, invU)
	if r.invOK {
		var p mat.Dense
		p.Mul(mat.NewDense(n, n, symFull(n, invU)), mat.NewDense(n, n, full))
		r.product = append([]float64(nil), p.RawMatrix().Data...)
	}
	return r
}

func upper(n int, a []float64) []float64 {
	out := make([]float64, 0, n*(n+1)/2)
	for i := 0; i < n; i++ {
		out = append(out, a[i*n+i:(i+1)*n]...)
	}
	return out
}

func symFull(n int, a []float64) []float64 {
	out := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			out[i*n+j], out[j*n+i] = a[i*n+j], a[i*n+j]
		}
	}
	return out
}

// sameFloatBits is bit equality with every NaN equal to every NaN (a NaN's
// sign and payload are not part of the contract: 0/0 is 0x7ff8… on arm64
// and 0xfff8… on amd64).
func sameFloatBits(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

func sameSlice(t *testing.T, what string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len %d, gonum %d", what, len(got), len(want))
	}
	for i := range got {
		if !sameFloatBits(got[i], want[i]) {
			t.Fatalf("%s[%d] = %x (%v), gonum %x (%v)", what, i,
				math.Float64bits(got[i]), got[i], math.Float64bits(want[i]), want[i])
		}
	}
}

func TestSPD_MatchesGonumOrder(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH != "amd64" {
		// gonum's own bits differ off amd64 (its pure Go is contracted
		// into FMAs there); TestSPD_BitPinned carries the contract on this
		// architecture, and CI's linux/amd64 run binds this test.
		t.Logf("GOARCH=%s: the gonum comparison binds on amd64 only", runtime.GOARCH)
		return
	}
	t.Run("kernels", func(t *testing.T) {
		walkGonumOrderKernels(func(what string, port []float64, gonum func() []float64) {
			sameSlice(t, what, port, gonum())
		})
	})
	for i, c := range spdCorpus() {
		got, want := runPort(t, c), runGonum(c)
		what := c.name + "#" + strconv.Itoa(i)
		if got.ok != want.ok {
			t.Fatalf("%s: factor ok %v, gonum %v", what, got.ok, want.ok)
		}
		if !got.ok {
			continue
		}
		sameSlice(t, what+" U", got.u, want.u)
		sameSlice(t, what+" cond", []float64{got.cond}, []float64{want.cond})
		sameSlice(t, what+" x", got.x, want.x)
		if got.invOK != want.invOK {
			t.Fatalf("%s: inverse ok %v, gonum %v", what, got.invOK, want.invOK)
		}
		if got.invOK {
			sameSlice(t, what+" inv", got.inv, want.inv)
			sameSlice(t, what+" product", got.product, want.product)
		}
	}
}

// walkGonumOrderKernels runs every ported BLAS kernel over the shapes and
// scalars its callers pass and more, handing visit each port result and
// a closure computing gonum's on the same operands.
func walkGonumOrderKernels(visit func(what string, port []float64, gonum func() []float64)) {
	bi := blas64.Implementation()
	rng := gonumOrderRNG(7)
	for n := 0; n <= 19; n++ {
		for _, inc := range []int{1, 2, 3} {
			x, y := uniformVec(rng, n*inc+1), uniformVec(rng, n*inc+1)
			visit("Ddot n="+strconv.Itoa(n)+" inc="+strconv.Itoa(inc), []float64{goDdot(n, x, inc, y, 1)},
				func() []float64 { return []float64{bi.Ddot(n, x, inc, y, 1)} })
		}
	}
	for _, beta := range []float64{0, 1, -0.75} {
		for _, alpha := range []float64{1, -1, 0.5, 0} {
			for m := 0; m <= 9; m++ {
				for n := 0; n <= 9; n++ {
					for _, trans := range []bool{false, true} {
						lda := n + 2
						a := uniformVec(rng, max(1, m)*lda)
						lenX, lenY := n, m
						if trans {
							lenX, lenY = m, n
						}
						incX, incY := 1, 3
						if trans {
							incX, incY = 3, 1
						}
						x := uniformVec(rng, lenX*incX+1)
						y := uniformVec(rng, lenY*incY+1)
						gy, wy := append([]float64(nil), y...), append([]float64(nil), y...)
						what := "Dgemv m=" + strconv.Itoa(m) + " n=" + strconv.Itoa(n)
						tA := blas.NoTrans
						if trans {
							tA = blas.Trans
							goDgemvT(m, n, alpha, a, lda, x, incX, beta, gy, incY)
						} else {
							goDgemvN(m, n, alpha, a, lda, x, incX, beta, gy, incY)
						}
						visit(what, gy, func() []float64 {
							bi.Dgemv(tA, m, n, alpha, a, lda, x, incX, beta, wy, incY)
							return wy
						})
					}
				}
			}
		}
	}
	tri := func(n, lda int) []float64 {
		a := uniformVec(rng, max(1, n)*lda)
		for i := 0; i < n; i++ {
			a[i*lda+i] = 1.5 + rng.Float64()
		}
		return a
	}
	for _, alpha := range []float64{1, -1, 0.5, 0} {
		for _, d := range [][2]int{{0, 3}, {1, 1}, {3, 1}, {5, 4}, {9, 7}, {70, 3}, {3, 70}} {
			m, n := d[0], d[1]
			ldb := n + 1
			b := uniformVec(rng, max(1, m)*ldb)
			for _, trans := range []bool{false, true} {
				lda := m + 1
				a := tri(m, lda)
				gb, wb := append([]float64(nil), b...), append([]float64(nil), b...)
				tA := blas.NoTrans
				if trans {
					tA = blas.Trans
				}
				goDtrsmLeftUpper(trans, m, n, alpha, a, lda, gb, ldb)
				visit("Dtrsm left", gb, func() []float64 {
					bi.Dtrsm(blas.Left, blas.Upper, tA, blas.NonUnit, m, n, alpha, a, lda, wb, ldb)
					return wb
				})
			}
			ar := tri(n, n+1)
			gb, wb := append([]float64(nil), b...), append([]float64(nil), b...)
			goDtrsmRightUpperNoTrans(m, n, alpha, ar, n+1, gb, ldb)
			visit("Dtrsm right", gb, func() []float64 {
				bi.Dtrsm(blas.Right, blas.Upper, blas.NoTrans, blas.NonUnit, m, n, alpha, ar, n+1, wb, ldb)
				return wb
			})

			al := tri(m, m+1)
			gb, wb = append([]float64(nil), b...), append([]float64(nil), b...)
			goDtrmmLeftUpperNoTrans(m, n, alpha, al, m+1, gb, ldb)
			visit("Dtrmm left", gb, func() []float64 {
				bi.Dtrmm(blas.Left, blas.Upper, blas.NoTrans, blas.NonUnit, m, n, alpha, al, m+1, wb, ldb)
				return wb
			})
			gb, wb = append([]float64(nil), b...), append([]float64(nil), b...)
			goDtrmmRightUpperTrans(m, n, alpha, ar, n+1, gb, ldb)
			visit("Dtrmm right", gb, func() []float64 {
				bi.Dtrmm(blas.Right, blas.Upper, blas.Trans, blas.NonUnit, m, n, alpha, ar, n+1, wb, ldb)
				return wb
			})
		}
		for _, d := range [][2]int{{0, 2}, {1, 0}, {4, 3}, {9, 1}, {13, 70}} {
			n, k := d[0], d[1]
			for _, beta := range []float64{0, 1, 2} {
				for _, trans := range []bool{false, true} {
					rowsA, colsA := n, k
					tA := blas.NoTrans
					if trans {
						rowsA, colsA, tA = k, n, blas.Trans
					}
					lda := colsA + 1
					a := uniformVec(rng, max(1, rowsA)*lda)
					c := uniformVec(rng, max(1, n)*(n+1))
					gc, wc := append([]float64(nil), c...), append([]float64(nil), c...)
					goDsyrkUpper(trans, n, k, alpha, a, lda, beta, gc, n+1)
					visit("Dsyrk", gc, func() []float64 {
						bi.Dsyrk(blas.Upper, tA, n, k, alpha, a, lda, beta, wc, n+1)
						return wc
					})
				}
			}
		}
		for _, d := range [][3]int{{0, 3, 2}, {3, 4, 0}, {5, 6, 7}, {70, 130, 90}, {200, 64, 150}, {64, 200, 3}} {
			m, n, k := d[0], d[1], d[2]
			for _, beta := range []float64{0, 1, -0.5} {
				for _, tr := range [][2]bool{{false, false}, {true, false}, {false, true}} {
					aT, bT := tr[0], tr[1]
					ra, ca := m, k
					if aT {
						ra, ca = k, m
					}
					rb, cb := k, n
					if bT {
						rb, cb = n, k
					}
					a := uniformVec(rng, max(1, ra)*(ca+1))
					b := uniformVec(rng, max(1, rb)*(cb+1))
					c := uniformVec(rng, max(1, m)*(n+1))
					gc, wc := append([]float64(nil), c...), append([]float64(nil), c...)
					tA, tB := blas.NoTrans, blas.NoTrans
					if aT {
						tA = blas.Trans
					}
					if bT {
						tB = blas.Trans
					}
					goDgemm(aT, bT, m, n, k, alpha, a, ca+1, b, cb+1, beta, gc, n+1)
					visit("Dgemm "+strconv.Itoa(m)+"x"+strconv.Itoa(n)+"x"+strconv.Itoa(k), gc, func() []float64 {
						bi.Dgemm(tA, tB, m, n, k, alpha, a, ca+1, b, cb+1, beta, wc, n+1)
						return wc
					})
				}
			}
		}
	}
}

// spdCorpusDigestPin is the SHA-256 of the corpus outputs (see
// spdDigest). Authored on darwin/arm64 and checked bit-equal on
// linux/amd64; it must never differ between architectures. A change
// to the SPD path that moves it moves regression figures: regenerate
// only with a deliberate, recorded ulp move.
const spdCorpusDigestPin = "e3a79c36ce3a372aaaa155417e6369c3135ddd65c23fcd97163bac82cdbe616b"

// spdDigest hashes the float64 bits (NaN canonicalised) of every ported
// kernel's output in walkGonumOrderKernels, then of every corpus
// output, plus the decision flags.
func spdDigest(t testing.TB) string {
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
	flag := func(b bool) {
		if b {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
	}
	walkGonumOrderKernels(func(_ string, port []float64, _ func() []float64) {
		for _, v := range port {
			put(v)
		}
	})
	for _, c := range spdCorpus() {
		r := runPort(t, c)
		flag(r.ok)
		flag(r.invOK)
		for _, part := range [][]float64{r.u, {r.cond}, r.x, r.inv, r.product} {
			for _, v := range part {
				put(v)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestSPD_BitPinned(t *testing.T) {
	t.Parallel()
	if got := spdDigest(t); got != spdCorpusDigestPin {
		t.Fatalf("SPD corpus digest %s, pinned %s (GOARCH=%s): the SPD path's bits moved", got, spdCorpusDigestPin, runtime.GOARCH)
	}
}
