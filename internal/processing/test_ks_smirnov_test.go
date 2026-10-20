package processing

import (
	"math"
	"testing"
)

// TestKSTwoSampleP_WeightedIsExpansion: a frequency weight w is w tied
// rows, so ksTwoSampleP over (values, weights) answers the expanded
// samples bit for bit — the sizes (Σw, which also pick the exact or
// asymptotic branch) and the tie pattern psmirnov conditions on both
// come from the expansion. Large weights push Σw₁·Σw₂ past 10000 to
// cover the asymptotic branch.
func TestKSTwoSampleP_WeightedIsExpansion(t *testing.T) {
	expand := func(vs, ws []float64) []float64 {
		var out []float64
		for i, v := range vs {
			for range int(ws[i]) {
				out = append(out, v)
			}
		}
		return out
	}
	for _, tc := range []struct {
		name            string
		a, wa, b, wb    []float64
		wantExactRegime bool
	}{
		{"exact_ties", []float64{1, 2, 4, 5}, []float64{2, 1, 3, 1}, []float64{2, 3, 4, 6}, []float64{1, 4, 1, 2}, true},
		{"exact_unit", []float64{0.5, 1.5, 2.5}, []float64{1, 1, 1}, []float64{1, 2, 3}, []float64{1, 1, 1}, true},
		{"asymptotic", []float64{1, 2, 4, 5}, []float64{30, 20, 40, 25}, []float64{2, 3, 4, 6}, []float64{10, 50, 20, 30}, false},
	} {
		if got := ksUsesExact(sum(tc.wa), sum(tc.wb)); got != tc.wantExactRegime {
			t.Fatalf("%s: exact = %v, want %v", tc.name, got, tc.wantExactRegime)
		}
		ea, eb := expand(tc.a, tc.wa), expand(tc.b, tc.wb)
		D := ksTwoSampleDW(tc.a, tc.wa, tc.b, tc.wb)
		got := ksTwoSampleP(tc.a, tc.wa, tc.b, tc.wb, D)
		want := ksTwoSampleP(ea, nil, eb, nil, ksTwoSampleD(ea, eb))
		if got != want || math.IsNaN(got) || got <= 0 || got > 1 {
			t.Fatalf("%s: weighted p %v, expanded %v", tc.name, got, want)
		}
	}
}

// TestKSTieBoundaries: z marks the pooled order statistics where the
// value changes — R's c(0L, diff(sort(c(x, y))) != 0, 1L).
func TestKSTieBoundaries(t *testing.T) {
	// pooled sorted: 1 2 2 2 2 3 3 4 → boundaries after positions 1, 5, 7, 8
	z := ksTieBoundaries([]float64{1, 2, 2}, nil, []float64{2, 3, 4}, []float64{2, 2, 1}, 8)
	want := []bool{false, true, false, false, false, true, false, true, true}
	for k := range want {
		if z[k] != want[k] {
			t.Fatalf("z = %v, want %v", z, want)
		}
	}
}
