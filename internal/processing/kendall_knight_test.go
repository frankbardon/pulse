package processing

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
)

// kendallCountsNaive is the O(n²) pair loop TEST_KENDALL_TAU ran before
// the Knight kernel, kept verbatim as the property oracle: every row
// pair (i, j), i < j, adds w_i·w_j to exactly one class.
func kendallCountsNaive(xs, ys, ws []float64) (c, d, tx, ty float64) {
	n := len(xs)
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			dx := xs[i] - xs[j]
			dy := ys[i] - ys[j]
			pw := 1.0
			if ws != nil {
				pw = ws[i] * ws[j]
			}
			switch {
			case dx == 0 && dy == 0:
			case dx == 0:
				tx += pw
			case dy == 0:
				ty += pw
			case (dx > 0) == (dy > 0):
				c += pw
			default:
				d += pw
			}
		}
	}
	return c, d, tx, ty
}

// TestKendallCounts_MatchesPairLoop is the Knight kernel's property
// gate: on random data with heavy ties (few distinct values per column,
// duplicated rows) the four pair masses equal the O(n²) loop's EXACTLY
// — unweighted, and under integer frequency weights including 0 — so
// τ-b, and every TEST_KENDALL_TAU figure built on the counts, is
// bit-identical to the pre-Knight test.
func TestKendallCounts_MatchesPairLoop(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for trial := 0; trial < 400; trial++ {
		n := rng.IntN(80)
		levelsX, levelsY := 1+rng.IntN(6), 1+rng.IntN(6)
		if trial%5 == 0 {
			levelsX, levelsY = 1000, 1000 // mostly distinct
		}
		xs, ys := make([]float64, n), make([]float64, n)
		for i := range xs {
			xs[i] = float64(rng.IntN(levelsX)) - 2.5
			ys[i] = float64(rng.IntN(levelsY)) * 0.75
			if i > 0 && rng.IntN(6) == 0 { // an exact duplicate row
				xs[i], ys[i] = xs[i-1], ys[i-1]
			}
		}
		var ws []float64
		if trial%2 == 1 {
			ws = make([]float64, n)
			for i := range ws {
				ws[i] = float64(rng.IntN(5)) // 0..4, zeros included
			}
		}
		t.Run(fmt.Sprintf("trial%d_n%d_w%v", trial, n, ws != nil), func(t *testing.T) {
			c, d, tx, ty := kendallCounts(xs, ys, ws)
			wc, wd, wtx, wty := kendallCountsNaive(xs, ys, ws)
			if c != wc || d != wd || tx != wtx || ty != wty {
				t.Fatalf("knight (c, d, tx, ty) = (%v, %v, %v, %v), pair loop = (%v, %v, %v, %v)", c, d, tx, ty, wc, wd, wtx, wty)
			}
			got, gotOK := kendallTauB(c, d, tx, ty)
			want, wantOK := kendallTauB(wc, wd, wtx, wty)
			if gotOK != wantOK || math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("tau-b = %v (%v), want %v (%v)", got, gotOK, want, wantOK)
			}
		})
	}
}

// TestKendallCounts_KnownCase pins a hand-counted sample: x = 1 1 2 3,
// y = 1 2 2 1 — pairs (0,1) tie x, (0,2) C, (0,3) tie y, (1,2) tie y,
// (1,3) D, (2,3) D.
func TestKendallCounts_KnownCase(t *testing.T) {
	c, d, tx, ty := kendallCounts([]float64{1, 1, 2, 3}, []float64{1, 2, 2, 1}, nil)
	if c != 1 || d != 2 || tx != 1 || ty != 2 {
		t.Fatalf("(c, d, tx, ty) = (%v, %v, %v, %v), want (1, 2, 1, 2)", c, d, tx, ty)
	}
	if _, ok := kendallTauB(0, 0, 0, 3); ok {
		t.Fatal("every pair tied in x: tau-b must be undefined")
	}
}
