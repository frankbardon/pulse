package linalg_test

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// coMomentTol is the documented merge / reference tolerance (see the
// CoMoment doc comment): figures agree to 1e-10 relative to their scale.
const coMomentTol = 1e-10

var bothModes = []linalg.CoMomentMode{linalg.Listwise, linalg.Pairwise}

func mustCoMoment(t *testing.T, p int, mode linalg.CoMomentMode) *linalg.CoMoment {
	t.Helper()
	c, err := linalg.NewCoMoment(p, mode)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type wrow struct {
	x []float64
	w float64
}

// randomRows draws n rows of p values with a mean offset and a scale per
// column, ~15% NaN cells, mostly positive weights plus some zero and
// some invalid (NaN, ±Inf, negative) ones.
func randomRows(r *rand.Rand, n, p int) []wrow {
	offset := make([]float64, p)
	scale := make([]float64, p)
	for j := range offset {
		offset[j] = (r.Float64() - 0.5) * 2000
		scale[j] = 0.1 + float64(r.Float64()*50)
	}
	rows := make([]wrow, n)
	for i := range rows {
		x := make([]float64, p)
		base := r.NormFloat64()
		for j := range x {
			if r.Float64() < 0.15 {
				x[j] = math.NaN()
				continue
			}
			// Correlated columns so the off-diagonal is non-trivial.
			x[j] = offset[j] + float64(scale[j]*(float64(0.6*base)+float64(0.8*r.NormFloat64())))
		}
		var w float64
		switch u := r.Float64(); {
		case u < 0.05:
			w = 0
		case u < 0.07:
			w = math.NaN()
		case u < 0.08:
			w = math.Inf(1)
		case u < 0.09:
			w = -1.5
		case u < 0.40:
			w = 1
		default:
			w = r.Float64() * 5
		}
		rows[i] = wrow{x, w}
	}
	return rows
}

func feed(t *testing.T, p int, mode linalg.CoMomentMode, rows []wrow) *linalg.CoMoment {
	t.Helper()
	c := mustCoMoment(t, p, mode)
	for _, r := range rows {
		c.Add(r.x, r.w)
	}
	return c
}

func within(a, b, scale float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Abs(a-b) <= coMomentTol*scale
}

// assertCoMomentClose compares every read of got and want: counts
// exactly, sums / means / covariances within coMomentTol of their scale,
// correlations within coMomentTol absolutely.
func assertCoMomentClose(t *testing.T, label string, got, want *linalg.CoMoment) {
	t.Helper()
	if got.N() != want.N() || got.NWeightInvalid() != want.NWeightInvalid() {
		t.Fatalf("%s: counts N=%d/%d NWeightInvalid=%d/%d", label,
			got.N(), want.N(), got.NWeightInvalid(), want.NWeightInvalid())
	}
	if !within(got.W(), want.W(), math.Max(1, math.Abs(want.W()))) {
		t.Fatalf("%s: W %v vs %v", label, got.W(), want.W())
	}
	if !within(got.NEff(), want.NEff(), math.Max(1, want.NEff())) {
		t.Fatalf("%s: NEff %v vs %v", label, got.NEff(), want.NEff())
	}
	p := want.P()
	gc, wc := got.Cov(0), want.Cov(0)
	gm, wm := got.Mean(), want.Mean()
	gr, wr := got.Corr(), want.Corr()
	for i := 0; i < p; i++ {
		sd := math.Sqrt(math.Abs(wc.At(i, i)))
		if !within(gm.At(i), wm.At(i), math.Abs(wm.At(i))+sd+1) {
			t.Fatalf("%s: mean[%d] %v vs %v", label, i, gm.At(i), wm.At(i))
		}
		for j := i; j < p; j++ {
			if got.PairN(i, j) != want.PairN(i, j) {
				t.Fatalf("%s: PairN(%d,%d) %d vs %d", label, i, j, got.PairN(i, j), want.PairN(i, j))
			}
			if !within(got.PairW(i, j), want.PairW(i, j), math.Max(1, want.PairW(i, j))) {
				t.Fatalf("%s: PairW(%d,%d) %v vs %v", label, i, j, got.PairW(i, j), want.PairW(i, j))
			}
			scale := math.Sqrt(math.Abs(wc.At(i, i))*math.Abs(wc.At(j, j))) + 1e-300
			if !within(gc.At(i, j), wc.At(i, j), scale) {
				t.Fatalf("%s: cov(%d,%d) %v vs %v", label, i, j, gc.At(i, j), wc.At(i, j))
			}
			if !within(gr.At(i, j), wr.At(i, j), 1) {
				t.Fatalf("%s: corr(%d,%d) %v vs %v", label, i, j, gr.At(i, j), wr.At(i, j))
			}
		}
	}
}

// TestCoMomentMergeMatchesSerial: for random data, weights, NaN
// patterns and split points, merging the pieces (left fold and binary
// tree) equals one serial pass within coMomentTol, in both modes.
func TestCoMomentMergeMatchesSerial(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for _, mode := range bothModes {
		for trial := 0; trial < 200; trial++ {
			p := 1 + r.IntN(5)
			rows := randomRows(r, r.IntN(300), p)
			serial := feed(t, p, mode, rows)

			k := 1 + r.IntN(6)
			cuts := []int{0}
			for i := 1; i < k; i++ {
				cuts = append(cuts, r.IntN(len(rows)+1))
			}
			cuts = append(cuts, len(rows))
			sortInts(cuts)
			parts := make([]*linalg.CoMoment, 0, k)
			for i := 0; i+1 < len(cuts); i++ {
				parts = append(parts, feed(t, p, mode, rows[cuts[i]:cuts[i+1]]))
			}

			fold := mustCoMoment(t, p, mode)
			for _, part := range parts {
				if err := fold.Merge(part); err != nil {
					t.Fatal(err)
				}
			}
			assertCoMomentClose(t, mode.String()+"/fold", fold, serial)

			tree := mergeTree(t, parts)
			assertCoMomentClose(t, mode.String()+"/tree", tree, serial)
		}
	}
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func mergeTree(t *testing.T, parts []*linalg.CoMoment) *linalg.CoMoment {
	t.Helper()
	for len(parts) > 1 {
		next := make([]*linalg.CoMoment, 0, (len(parts)+1)/2)
		for i := 0; i < len(parts); i += 2 {
			left := parts[i].Clone()
			if i+1 < len(parts) {
				if err := left.Merge(parts[i+1]); err != nil {
					t.Fatal(err)
				}
			}
			next = append(next, left)
		}
		parts = next
	}
	return parts[0]
}

// TestCoMomentMergeAssociative: (a·b)·c equals a·(b·c) within tolerance.
func TestCoMomentMergeAssociative(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 5))
	for _, mode := range bothModes {
		for trial := 0; trial < 100; trial++ {
			p := 1 + r.IntN(4)
			a := feed(t, p, mode, randomRows(r, r.IntN(80), p))
			b := feed(t, p, mode, randomRows(r, r.IntN(80), p))
			c := feed(t, p, mode, randomRows(r, r.IntN(80), p))

			left := a.Clone()
			_ = left.Merge(b)
			_ = left.Merge(c)

			bc := b.Clone()
			_ = bc.Merge(c)
			right := a.Clone()
			_ = right.Merge(bc)

			assertCoMomentClose(t, mode.String(), left, right)
		}
	}
}

// coMomentBits renders every read of c as raw float bits plus counts,
// for bit-for-bit comparisons.
func coMomentBits(c *linalg.CoMoment) []uint64 {
	out := []uint64{uint64(c.N()), uint64(c.NWeightInvalid()), math.Float64bits(c.W()), math.Float64bits(c.NEff())}
	p := c.P()
	m := c.Mean()
	cov := c.Cov(1)
	corr := c.Corr()
	for i := 0; i < p; i++ {
		out = append(out, math.Float64bits(m.At(i)))
		for j := i; j < p; j++ {
			out = append(out, uint64(c.PairN(i, j)), math.Float64bits(c.PairW(i, j)),
				math.Float64bits(cov.At(i, j)), math.Float64bits(corr.At(i, j)))
		}
	}
	return out
}

func sameWords(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCoMomentMergeIdentityBitwise: merging with an empty accumulator,
// on either side, leaves every read bit-identical. An accumulator that
// holds only zero-weight rows (N > 0, W = 0) carries no mass either: it
// adds its N and changes no moment bit.
func TestCoMomentMergeIdentityBitwise(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, mode := range bothModes {
		for trial := 0; trial < 50; trial++ {
			p := 1 + r.IntN(4)
			a := feed(t, p, mode, randomRows(r, 1+r.IntN(100), p))
			want := coMomentBits(a)

			right := a.Clone()
			if err := right.Merge(mustCoMoment(t, p, mode)); err != nil {
				t.Fatal(err)
			}
			if !sameWords(coMomentBits(right), want) {
				t.Fatalf("%s: a.Merge(empty) changed bits", mode)
			}
			left := mustCoMoment(t, p, mode)
			if err := left.Merge(a); err != nil {
				t.Fatal(err)
			}
			if !sameWords(coMomentBits(left), want) {
				t.Fatalf("%s: empty.Merge(a) changed bits", mode)
			}

			zero := mustCoMoment(t, p, mode)
			full := make([]float64, p)
			zero.Add(full, 0)
			zero.Add(full, 0)
			z := a.Clone()
			_ = z.Merge(zero)
			gotBits := coMomentBits(z)
			if z.N() != a.N()+2 {
				t.Fatalf("%s: zero-weight merge N %d, want %d", mode, z.N(), a.N()+2)
			}
			// Everything but N and the per-pair N is bit-identical.
			zb := coMomentBits(a)
			zb[0] = uint64(a.N() + 2)
			idx := 4
			for i := 0; i < p; i++ {
				idx++
				for j := i; j < p; j++ {
					zb[idx] = uint64(a.PairN(i, j) + 2)
					idx += 4
				}
			}
			if !sameWords(gotBits, zb) {
				t.Fatalf("%s: merging a zero-weight accumulator changed moment bits", mode)
			}
		}
	}
}

// TestCoMomentMergeDoesNotMutateOther: Merge mutates only its receiver.
func TestCoMomentMergeDoesNotMutateOther(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 9))
	for _, mode := range bothModes {
		a := feed(t, 3, mode, randomRows(r, 50, 3))
		b := feed(t, 3, mode, randomRows(r, 50, 3))
		before := coMomentBits(b)
		if err := a.Merge(b); err != nil {
			t.Fatal(err)
		}
		if !sameWords(coMomentBits(b), before) {
			t.Fatalf("%s: Merge mutated its argument", mode)
		}
		// A self-merge doubles the mass and keeps the mean.
		s := feed(t, 2, mode, []wrow{{[]float64{1, 2}, 1}, {[]float64{3, 6}, 1}})
		if err := s.Merge(s); err != nil {
			t.Fatal(err)
		}
		if s.N() != 4 || s.W() != 4 || s.Mean().At(0) != 2 || s.Cov(0).At(0, 0) != 1 {
			t.Fatalf("%s: self-merge N=%d W=%v mean=%v var=%v", mode, s.N(), s.W(), s.Mean().At(0), s.Cov(0).At(0, 0))
		}
	}
}

// TestCoMomentWeightSemantics pins the U11 weight rules: invalid weights
// are counted and skipped, w = 0 raises N but not W, NEff is Kish.
func TestCoMomentWeightSemantics(t *testing.T) {
	for _, mode := range bothModes {
		c := mustCoMoment(t, 2, mode)
		c.Add([]float64{1, 2}, 2)
		c.Add([]float64{3, 4}, math.NaN())
		c.Add([]float64{3, 4}, math.Inf(1))
		c.Add([]float64{3, 4}, math.Inf(-1))
		c.Add([]float64{3, 4}, -1)
		c.Add([]float64{5, 6}, 0)
		c.Add([]float64{6, 8}, 3)
		if c.NWeightInvalid() != 4 {
			t.Fatalf("%s: NWeightInvalid %d, want 4", mode, c.NWeightInvalid())
		}
		if c.N() != 3 {
			t.Fatalf("%s: N %d, want 3 (w=0 counts)", mode, c.N())
		}
		if c.W() != 5 {
			t.Fatalf("%s: W %v, want 5 (w=0 adds no mass)", mode, c.W())
		}
		if want := 25.0 / 13.0; c.NEff() != want {
			t.Fatalf("%s: NEff %v, want Kish %v", mode, c.NEff(), want)
		}
		// Mean (2·1 + 3·6)/5 = 4; the w=0 row moved nothing.
		if m := c.Mean().At(0); m != 4 {
			t.Fatalf("%s: mean %v, want 4", mode, m)
		}
		if c.PairN(0, 1) != 3 || c.PairW(0, 1) != 5 {
			t.Fatalf("%s: pair N/W %d/%v", mode, c.PairN(0, 1), c.PairW(0, 1))
		}
	}

	// A row the mode already discards for a missing value is never
	// judged for its weight (the weighting floor's n_null rule).
	lw := mustCoMoment(t, 2, linalg.Listwise)
	lw.Add([]float64{math.NaN(), 1}, -1)
	if lw.NWeightInvalid() != 0 || lw.N() != 0 {
		t.Fatalf("listwise: incomplete row judged: invalid=%d N=%d", lw.NWeightInvalid(), lw.N())
	}
	pw := mustCoMoment(t, 2, linalg.Pairwise)
	pw.Add([]float64{math.NaN(), math.NaN()}, -1)
	if pw.NWeightInvalid() != 0 || pw.N() != 0 {
		t.Fatalf("pairwise: all-missing row judged: invalid=%d N=%d", pw.NWeightInvalid(), pw.N())
	}
	pw.Add([]float64{math.NaN(), 1}, -1)
	if pw.NWeightInvalid() != 1 {
		t.Fatalf("pairwise: partly present row with invalid weight not counted")
	}

	// Empty: Kish is 0 (as the weighting floor), means are NaN.
	e := mustCoMoment(t, 1, linalg.Listwise)
	if e.NEff() != 0 || !math.IsNaN(e.Mean().At(0)) {
		t.Fatalf("empty: NEff=%v mean=%v", e.NEff(), e.Mean().At(0))
	}
}

// TestCoMomentPairwiseMissing: pairwise keeps per-pair N, W and moments;
// listwise drops the whole row.
func TestCoMomentPairwiseMissing(t *testing.T) {
	nan := math.NaN()
	rows := []wrow{
		{[]float64{1, 10, nan}, 1},
		{[]float64{2, nan, 5}, 2},
		{[]float64{3, 30, 7}, 1},
		{[]float64{nan, 40, 9}, 1},
	}
	pw := feed(t, 3, linalg.Pairwise, rows)
	if pw.N() != 4 || pw.W() != 5 {
		t.Fatalf("pairwise N/W %d/%v", pw.N(), pw.W())
	}
	cases := []struct {
		i, j int
		n    int64
		w    float64
	}{{0, 0, 3, 4}, {0, 1, 2, 2}, {0, 2, 2, 3}, {1, 1, 3, 3}, {1, 2, 2, 2}, {2, 2, 3, 4}}
	for _, tc := range cases {
		if pw.PairN(tc.i, tc.j) != tc.n || pw.PairW(tc.i, tc.j) != tc.w ||
			pw.PairN(tc.j, tc.i) != tc.n {
			t.Fatalf("pair (%d,%d): N=%d W=%v, want %d/%v", tc.i, tc.j,
				pw.PairN(tc.i, tc.j), pw.PairW(tc.i, tc.j), tc.n, tc.w)
		}
	}
	// Pair (0,1) sees rows 0 and 2 only: x0 {1,3}, x1 {10,30} → r = 1.
	if r := pw.Corr().At(0, 1); !within(r, 1, 1e-4) {
		t.Fatalf("pairwise corr(0,1) %v, want 1", r)
	}
	// Mean of x0 over its present rows: (1 + 4 + 3)/4 = 2.
	if m := pw.Mean().At(0); !within(m, 2, 1e-4) {
		t.Fatalf("pairwise mean(0) %v, want 2", m)
	}

	lw := feed(t, 3, linalg.Listwise, rows)
	if lw.N() != 1 || lw.W() != 1 || lw.PairN(0, 1) != 1 || lw.PairW(2, 0) != 1 {
		t.Fatalf("listwise N/W %d/%v pair %d", lw.N(), lw.W(), lw.PairN(0, 1))
	}
}

// twoPass is the naive weighted two-pass reference: per pair, the rows
// the mode admits with a valid, positive weight; mean Σwx/Σw then
// Σw(xi−mi)(xj−mj).
func twoPass(rows []wrow, p int, mode linalg.CoMomentMode, ddof float64) (cov, corr [][]float64) {
	cov = make([][]float64, p)
	corr = make([][]float64, p)
	for i := range cov {
		cov[i] = make([]float64, p)
		corr[i] = make([]float64, p)
	}
	admit := func(r wrow, i, j int) bool {
		if math.IsNaN(r.w) || math.IsInf(r.w, 0) || r.w < 0 {
			return false
		}
		if mode == linalg.Listwise {
			for _, v := range r.x {
				if math.IsNaN(v) {
					return false
				}
			}
			return true
		}
		return !math.IsNaN(r.x[i]) && !math.IsNaN(r.x[j])
	}
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			var W, si, sj float64
			for _, r := range rows {
				if admit(r, i, j) {
					W += r.w
					si += r.w * r.x[i]
					sj += r.w * r.x[j]
				}
			}
			mi, mj := si/W, sj/W
			var cij, cii, cjj float64
			for _, r := range rows {
				if admit(r, i, j) {
					di, dj := r.x[i]-mi, r.x[j]-mj
					cij += r.w * di * dj
					cii += r.w * di * di
					cjj += r.w * dj * dj
				}
			}
			cov[i][j] = math.NaN()
			if W-ddof > 0 {
				cov[i][j] = cij / (W - ddof)
			}
			corr[i][j] = cij / math.Sqrt(cii*cjj)
		}
	}
	return cov, corr
}

// TestCoMomentMatchesTwoPass: Cov and Corr equal the naive two-pass
// reference within tolerance, in both modes and for ddof 0 and 1.
func TestCoMomentMatchesTwoPass(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 43))
	for _, mode := range bothModes {
		for trial := 0; trial < 100; trial++ {
			p := 1 + r.IntN(5)
			rows := randomRows(r, 20+r.IntN(200), p)
			c := feed(t, p, mode, rows)
			for _, ddof := range []int{0, 1} {
				wantCov, wantCorr := twoPass(rows, p, mode, float64(ddof))
				gotCov := c.Cov(ddof)
				gotCorr := c.Corr()
				for i := 0; i < p; i++ {
					for j := 0; j < p; j++ {
						scale := math.Sqrt(math.Abs(wantCov[i][i] * wantCov[j][j]))
						if !within(gotCov.At(i, j), wantCov[i][j], scale) {
							t.Fatalf("%s ddof=%d cov(%d,%d) %v vs %v", mode, ddof, i, j, gotCov.At(i, j), wantCov[i][j])
						}
						if !within(gotCorr.At(i, j), wantCorr[i][j], 1) {
							t.Fatalf("%s corr(%d,%d) %v vs %v", mode, i, j, gotCorr.At(i, j), wantCorr[i][j])
						}
					}
				}
			}
		}
	}

	// Unit weights reproduce the textbook unweighted sample covariance.
	c := feed(t, 2, linalg.Listwise, []wrow{{[]float64{1, 2}, 1}, {[]float64{2, 4}, 1}, {[]float64{3, 7}, 1}})
	if v := c.Cov(1); !within(v.At(0, 0), 1, 1e-4) || !within(v.At(0, 1), 2.5, 1e-4) || !within(v.At(1, 1), 19.0/3, 1e-4) {
		t.Fatalf("unweighted sample cov %v", v.ToRows())
	}
	// Too few degrees of freedom is NaN, never an error.
	one := feed(t, 1, linalg.Listwise, []wrow{{[]float64{5}, 1}})
	if !math.IsNaN(one.Cov(1).At(0, 0)) {
		t.Fatalf("cov with W - ddof = 0 should be NaN, got %v", one.Cov(1).At(0, 0))
	}
}

// TestCoMomentConstantColumnCorrIsNaN: a constant column has zero
// spread, so every correlation touching it — its own diagonal included —
// is NaN; a varying column's diagonal is exactly 1.
func TestCoMomentConstantColumnCorrIsNaN(t *testing.T) {
	for _, mode := range bothModes {
		c := mustCoMoment(t, 2, mode)
		for i, w := range []float64{0.1, 0.7, 3, 0.3} {
			c.Add([]float64{float64(i) * 1.1, 0.1}, w)
		}
		corr := c.Corr()
		if !math.IsNaN(corr.At(0, 1)) || !math.IsNaN(corr.At(1, 1)) {
			t.Fatalf("%s: constant column corr %v", mode, corr.ToRows())
		}
		if corr.At(0, 0) != 1 {
			t.Fatalf("%s: varying diagonal %v, want exactly 1", mode, corr.At(0, 0))
		}
		if v := c.Cov(0).At(1, 1); v != 0 {
			t.Fatalf("%s: constant column variance %v, want exactly 0", mode, v)
		}
	}
}

// TestCoMomentCorrClamped: rounding never pushes |r| past 1. Perfectly
// (anti-)correlated columns y = ±3x under fractional weights land a
// rounding step above 1 unclamped for many of these seeds.
func TestCoMomentCorrClamped(t *testing.T) {
	for _, mode := range bothModes {
		for seed := uint64(0); seed < 200; seed++ {
			r := rand.New(rand.NewPCG(seed, 1))
			c := mustCoMoment(t, 3, mode)
			for i, n := 0, 2+r.IntN(6); i < n; i++ {
				v := r.NormFloat64()
				c.Add([]float64{v, 3 * v, -3 * v}, 0.1+r.Float64())
			}
			corr := c.Corr()
			if v := corr.At(0, 1); v > 1 || v < 0.999999 {
				t.Fatalf("%s seed %d: corr %v", mode, seed, v)
			}
			if v := corr.At(0, 2); v < -1 || v > -0.999999 {
				t.Fatalf("%s seed %d: anti-corr %v", mode, seed, v)
			}
		}
	}
}

// TestCoMomentCorrDiagonalAndScale: the diagonal of a varying column is
// exactly 1 (not a rounded ratio), and a spread too large to square
// still yields a finite correlation.
func TestCoMomentCorrDiagonalAndScale(t *testing.T) {
	r := rand.New(rand.NewPCG(21, 4))
	for _, mode := range bothModes {
		for trial := 0; trial < 50; trial++ {
			c := feed(t, 3, mode, randomRows(r, 30+r.IntN(50), 3))
			corr := c.Corr()
			for i := 0; i < 3; i++ {
				if corr.At(i, i) != 1 {
					t.Fatalf("%s: diag %d = %v, want exactly 1", mode, i, corr.At(i, i))
				}
			}
		}
		big := mustCoMoment(t, 2, mode)
		for i := 0; i < 20; i++ {
			v := r.NormFloat64() * 1e100
			big.Add([]float64{v, v + r.NormFloat64()*1e99}, 1)
		}
		if v := big.Corr().At(0, 1); !(v > 0.9 && v <= 1) {
			t.Fatalf("%s: large-scale corr %v", mode, v)
		}
	}
}

// TestCoMomentCovFewerThanDdof: W − ddof ≤ 0 is NaN, including a
// fractional W below ddof.
func TestCoMomentCovFewerThanDdof(t *testing.T) {
	for _, mode := range bothModes {
		c := feed(t, 1, mode, []wrow{{[]float64{2}, 0.25}, {[]float64{4}, 0.25}})
		if v := c.Cov(1).At(0, 0); !math.IsNaN(v) {
			t.Fatalf("%s: W=0.5, ddof=1 cov %v, want NaN", mode, v)
		}
		if v := c.Cov(0).At(0, 0); v != 1 {
			t.Fatalf("%s: W=0.5, ddof=0 cov %v, want 1", mode, v)
		}
	}
}

// TestCoMomentShapeMismatch: p or mode mismatch, a nil operand and a bad
// constructor argument are all PULSE_MATRIX_SHAPE_MISMATCH.
func TestCoMomentShapeMismatch(t *testing.T) {
	a := mustCoMoment(t, 2, linalg.Listwise)
	a.Add([]float64{1, 2}, 1)
	before := coMomentBits(a)
	for name, other := range map[string]*linalg.CoMoment{
		"p":    mustCoMoment(t, 3, linalg.Listwise),
		"mode": mustCoMoment(t, 2, linalg.Pairwise),
		"nil":  nil,
	} {
		err := a.Merge(other)
		if err == nil || codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
			t.Fatalf("%s mismatch: got %v", name, err)
		}
		if !sameWords(coMomentBits(a), before) {
			t.Fatalf("%s mismatch mutated the receiver", name)
		}
	}
	if _, err := linalg.NewCoMoment(-1, linalg.Listwise); err == nil || codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatalf("negative p: %v", err)
	}
	if _, err := linalg.NewCoMoment(2, linalg.CoMomentMode(9)); err == nil || codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatalf("unknown mode: %v", err)
	}
}

// TestCoMomentAddPanicsOnLength: a row of the wrong length is a
// programming error, like an out-of-range index.
func TestCoMomentAddPanicsOnLength(t *testing.T) {
	c := mustCoMoment(t, 2, linalg.Listwise)
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "length") {
			t.Fatalf("want a length panic, got %v", r)
		}
	}()
	c.Add([]float64{1}, 1)
}

// TestCoMomentDefaultsAndAccessors pins the zero mode (Listwise), the
// mode names and Clone independence.
func TestCoMomentDefaultsAndAccessors(t *testing.T) {
	var zero linalg.CoMomentMode
	if zero != linalg.Listwise || linalg.Listwise.String() != "listwise" ||
		linalg.Pairwise.String() != "pairwise" || linalg.CoMomentMode(9).String() != "unknown" {
		t.Fatal("mode names / default")
	}
	c := mustCoMoment(t, 2, linalg.Pairwise)
	if c.P() != 2 || c.Mode() != linalg.Pairwise {
		t.Fatal("accessors")
	}
	c.Add([]float64{1, 2}, 1)
	k := c.Clone()
	k.Add([]float64{3, 4}, 1)
	if c.N() != 1 || k.N() != 2 || c.Mean().At(0) != 1 {
		t.Fatalf("Clone shares state: c.N=%d k.N=%d", c.N(), k.N())
	}
	// Unit weights: W equals N, Kish equals N.
	if k.W() != 2 || k.NEff() != 2 {
		t.Fatalf("unit weights W=%v NEff=%v", k.W(), k.NEff())
	}
	empty := mustCoMoment(t, 0, linalg.Listwise)
	empty.Add(nil, 1)
	if empty.N() != 1 || empty.Cov(0).N() != 0 {
		t.Fatalf("p=0: N=%d", empty.N())
	}
}
