//go:build heavy

package sem

import (
	"math"
	"testing"
)

// TestPolychoric_ScoresMatchLavaan holds the casewise scores (Scores:
// lavaan's SC rows), their cross-product INNER and, for every
// ordered × ordered pair, A21 = Σ ρ-score · threshold-score (PairScores)
// to lavaan's muthen1984. The scores are evaluated AT LAVAAN'S
// ESTIMATES (its thresholds, moments and correlations swapped into the
// statistics), so the comparison isolates the score code from lavaan's
// nlminb stop (~1e-7 off the root, which a 1200-row INNER sum would
// amplify past 1e-5).
func TestPolychoric_ScoresMatchLavaan(t *testing.T) {
	near := func(got, want, tol float64) bool {
		return math.Abs(got-want) <= tol*math.Max(1, math.Abs(want))
	}
	for _, fx := range twoStepFixtures {
		var ref twoStepRef
		readJSON(t, "testdata/"+fx.ref+"_lavaan.json", &ref)
		if ref.Scores == nil {
			continue
		}
		t.Run(fx.ref, func(t *testing.T) {
			_, in, ss := fitTwoStepFixture(t, fx.ref, fx.data)
			s := atLavaan(ss[0], ref)
			l := s.Layout()
			if l.Size != len(ref.Scores.Rows[0]) {
				t.Fatalf("layout width %d, lavaan SC %d", l.Size, len(ref.Scores.Rows[0]))
			}
			sc := make([]float64, l.Size)
			for r, want := range ref.Scores.Rows {
				if err := s.Scores(in.Rows[r], sc); err != nil {
					t.Fatal(err)
				}
				for c := range sc {
					if !near(sc[c], want[c], 1e-9) {
						t.Errorf("SC[%d][%d] = %.10g, lavaan %.10g", r, c, sc[c], want[c])
					}
				}
			}
			inner := make([][]float64, l.Size)
			for i := range inner {
				inner[i] = make([]float64, l.Size)
			}
			a21 := make([][]float64, len(s.Pairs))
			for k := range a21 {
				a21[k] = make([]float64, l.Cor)
			}
			for _, row := range in.Rows {
				if err := s.Scores(row, sc); err != nil {
					t.Fatal(err)
				}
				for i := range sc {
					for j := range sc {
						inner[i][j] += sc[i] * sc[j]
					}
				}
				for k, p := range s.Pairs {
					if p.Kind != PairPolychoric {
						continue
					}
					ps := s.PairScores(k, row)
					for m, d := range ps.ThI {
						a21[k][l.Th[p.I]+m] += ps.Rho * d
					}
					for m, d := range ps.ThJ {
						a21[k][l.Th[p.J]+m] += ps.Rho * d
					}
				}
			}
			for i := range inner {
				for j := range inner[i] {
					if !near(inner[i][j], ref.Scores.Inner[i][j], 1e-9) {
						t.Errorf("INNER[%d][%d] = %.10g, lavaan %.10g", i, j, inner[i][j], ref.Scores.Inner[i][j])
					}
				}
			}
			checked := 0
			for k, p := range s.Pairs {
				if p.Kind != PairPolychoric {
					continue
				}
				for c := range a21[k] {
					checked++
					if !near(a21[k][c], ref.Scores.A21[k][c], 1e-9) {
						t.Errorf("A21[%d][%d] = %.10g, lavaan %.10g", k, c, a21[k][c], ref.Scores.A21[k][c])
					}
				}
			}
			t.Logf("%d SC rows, INNER %d², %d A21 cells checked", len(ref.Scores.Rows), l.Size, checked)
		})
	}
}
