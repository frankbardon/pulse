package linalg_test

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/linalg"
)

// TestCoMomentPairM2MatchesCov pins PairM2 as the raw co-moment Cov
// divides: Cov(ddof) at (i, j) is PairM2(i, j)/(PairW(i, j) − ddof) to
// the BIT wherever that denominator is positive, for both modes, weighted
// and unit-weight rows, every ddof and both index orders; PairM2 is
// symmetric. A denominator ≤ 0 leaves Cov NaN while PairM2 stays finite.
func TestCoMomentPairM2MatchesCov(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewPCG(31, 7))
	weighted := randomRows(r, 300, 4)
	unit := make([]wrow, len(weighted))
	for i, row := range weighted {
		unit[i] = wrow{row.x, 1}
	}
	cases := []struct {
		name string
		rows []wrow
	}{
		{"weighted", weighted},
		{"unit", unit},
		{"single_row", []wrow{{[]float64{1, 2, 3, 4}, 1}}},
		{"empty", nil},
	}
	for _, tc := range cases {
		for _, mode := range bothModes {
			c := feed(t, 4, mode, tc.rows)
			for _, ddof := range []int{0, 1} {
				cov := c.Cov(ddof)
				for i := 0; i < 4; i++ {
					for j := 0; j < 4; j++ {
						m2 := c.PairM2(i, j)
						if math.Float64bits(m2) != math.Float64bits(c.PairM2(j, i)) {
							t.Fatalf("%s/%s: PairM2(%d,%d)=%v != PairM2(%d,%d)=%v", tc.name, mode, i, j, m2, j, i, c.PairM2(j, i))
						}
						if math.IsNaN(m2) || math.IsInf(m2, 0) {
							t.Fatalf("%s/%s: PairM2(%d,%d)=%v, want finite", tc.name, mode, i, j, m2)
						}
						d := c.PairW(i, j) - float64(ddof)
						got := cov.At(i, j)
						if !(d > 0) {
							if !math.IsNaN(got) {
								t.Fatalf("%s/%s ddof=%d: Cov(%d,%d)=%v with W−ddof=%v, want NaN", tc.name, mode, ddof, i, j, got, d)
							}
							continue
						}
						if want := m2 / d; math.Float64bits(got) != math.Float64bits(want) {
							t.Fatalf("%s/%s ddof=%d: Cov(%d,%d)=%v, PairM2/(PairW−ddof)=%v", tc.name, mode, ddof, i, j, got, want)
						}
					}
				}
			}
		}
	}
}

// TestCoMomentPairM2HandComputed pins the figure itself on a tiny
// hand-computed fold: x = (1, 2, 3), y = (2, 4, 9) under unit weights
// have means 2 and 5, so Σ(x−2)² = 2, Σ(y−5)² = 26, Σ(x−2)(y−5) = 7.
// Pairwise, a NaN in y drops that row from the (x, y) and (y, y) pairs
// but not from (x, x).
func TestCoMomentPairM2HandComputed(t *testing.T) {
	t.Parallel()

	for _, mode := range bothModes {
		c := mustCoMoment(t, 2, mode)
		c.Add([]float64{1, 2}, 1)
		c.Add([]float64{2, 4}, 1)
		c.Add([]float64{3, 9}, 1)
		for _, tc := range []struct {
			i, j int
			want float64
		}{{0, 0, 2}, {1, 1, 26}, {0, 1, 7}, {1, 0, 7}} {
			if got := c.PairM2(tc.i, tc.j); got != tc.want {
				t.Fatalf("%s: PairM2(%d,%d)=%v, want %v", mode, tc.i, tc.j, got, tc.want)
			}
		}
	}
	pw := mustCoMoment(t, 2, linalg.Pairwise)
	pw.Add([]float64{1, 2}, 1)
	pw.Add([]float64{3, 4}, 1)
	pw.Add([]float64{5, math.NaN()}, 1)
	// (x, x) over 1, 3, 5: mean 3, Σ = 8. (x, y) and (y, y) over the
	// first two rows: means 2 and 3, Σ(x−2)(y−3) = 2, Σ(y−3)² = 2.
	for _, tc := range []struct {
		i, j int
		want float64
	}{{0, 0, 8}, {0, 1, 2}, {1, 1, 2}} {
		if got := pw.PairM2(tc.i, tc.j); got != tc.want {
			t.Fatalf("pairwise: PairM2(%d,%d)=%v, want %v", tc.i, tc.j, got, tc.want)
		}
	}
}

func TestCoMomentPairM2PanicsOutOfRange(t *testing.T) {
	t.Parallel()

	for _, mode := range bothModes {
		c := mustCoMoment(t, 2, mode)
		for _, ij := range [][2]int{{0, 2}, {-1, 0}, {2, 2}} {
			func() {
				defer func() {
					if r := recover(); r == nil || !strings.Contains(r.(string), "out of range") {
						t.Fatalf("%s PairM2(%d,%d): want an out-of-range panic, got %v", mode, ij[0], ij[1], r)
					}
				}()
				c.PairM2(ij[0], ij[1])
			}()
		}
	}
}
