package numrange

import (
	"math/big"
	"testing"
)

// TestSet_MatchRatExactAtBounds: MatchRat compares exactly — a value
// one below a 2^53+4 bound is below it although its float64 rounds onto
// it — and keeps the half-open rule at both edges.
func TestSet_MatchRatExactAtBounds(t *testing.T) {
	t.Parallel()

	const bound = float64(1<<53 + 4)
	set, err := Compile([]Spec{{Label: "below", End: f(bound)}, {Label: "above", Start: f(bound)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		v    *big.Rat
		want string
	}{
		{new(big.Rat).SetUint64(1<<53 + 3), "below"},
		{new(big.Rat).SetUint64(1<<53 + 4), "above"},
		{big.NewRat(-1, 3), "below"},
	} {
		if got, ok := set.MatchRat(c.v); !ok || got != c.want {
			t.Errorf("MatchRat(%s) = %q %v, want %q", c.v, got, ok, c.want)
		}
	}
	mid, err := Compile([]Spec{{Label: "mid", Start: f(10), End: f(20)}})
	if err != nil {
		t.Fatal(err)
	}
	for v, want := range map[int64]bool{9: false, 10: true, 19: true, 20: false} {
		if _, ok := mid.MatchRat(big.NewRat(v, 1)); ok != want {
			t.Errorf("mid.MatchRat(%d) = %v, want %v", v, ok, want)
		}
	}
}

// TestSet_Gapless: true only when the lowest range is open below, the
// highest open above and each range ends where the next starts.
func TestSet_Gapless(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name  string
		specs []Spec
		want  bool
	}{
		{"one open range", []Spec{{Label: "all"}}, true},
		{"meeting", []Spec{{Label: "hi", Start: f(10)}, {Label: "lo", End: f(10)}}, true},
		{"gap", []Spec{{Label: "lo", End: f(10)}, {Label: "hi", Start: f(11)}}, false},
		{"bounded below", []Spec{{Label: "lo", Start: f(0), End: f(10)}, {Label: "hi", Start: f(10)}}, false},
		{"bounded above", []Spec{{Label: "lo", End: f(10)}, {Label: "hi", Start: f(10), End: f(20)}}, false},
	} {
		set, err := Compile(c.specs)
		if err != nil {
			t.Fatal(err)
		}
		if got := set.Gapless(); got != c.want {
			t.Errorf("%s: Gapless = %v, want %v", c.name, got, c.want)
		}
	}
}
