package statdist

import (
	"math"
	"testing"
)

// TestFSurvival_MatchesR: FSurvival against R 4.6.1's
// pf(f, df1, df2, lower.tail = FALSE) at 1e-12 relative, both branches
// of the x / y split, plus the edges.
func TestFSurvival_MatchesR(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ f, df1, df2, want float64 }{
		{7.15, 2, 56, 0.0017159935678180706},
		{0.3, 1, 5, 0.60743549407592434},
		{45.6, 2, 57, 1.4902515521389741e-12},
		{1.74, 2, 86, 0.18164207741226243},
		{250, 3, 10, 1.061771263379611e-09},
	} {
		got := FSurvival(c.f, c.df1, c.df2)
		if math.Abs(got-c.want) > 1e-12*c.want {
			t.Errorf("FSurvival(%g, %g, %g) = %.17g, want %.17g", c.f, c.df1, c.df2, got, c.want)
		}
	}
	if got := FSurvival(0, 2, 5); got != 1 {
		t.Errorf("FSurvival(0) = %g, want 1", got)
	}
	if got := FSurvival(-1, 2, 5); got != 1 {
		t.Errorf("FSurvival(-1) = %g, want 1", got)
	}
	if got := FSurvival(math.Inf(1), 2, 5); got != 0 {
		t.Errorf("FSurvival(+Inf) = %g, want 0", got)
	}
	for _, bad := range [][3]float64{{1, 0, 5}, {1, 2, 0}, {math.NaN(), 2, 5}, {1, math.NaN(), 5}} {
		if got := FSurvival(bad[0], bad[1], bad[2]); !math.IsNaN(got) {
			t.Errorf("FSurvival(%v) = %g, want NaN", bad, got)
		}
	}
}
