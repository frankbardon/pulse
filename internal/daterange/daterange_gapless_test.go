package daterange

import "testing"

// TestSet_Gapless: true only when the lowest range is open below, the
// highest open above and each range ends the day before the next starts
// (bounds are inclusive).
func TestSet_Gapless(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name  string
		specs []Spec
		want  bool
	}{
		{"one open range", []Spec{{Label: "all"}}, true},
		{"contiguous", []Spec{{Label: "b", Start: str("2024-04-01")}, {Label: "a", End: str("2024-03-31")}}, true},
		{"one-day gap", []Spec{{Label: "a", End: str("2024-03-30")}, {Label: "b", Start: str("2024-04-01")}}, false},
		{"bounded below", []Spec{{Label: "a", Start: str("2024-01-01"), End: str("2024-03-31")}, {Label: "b", Start: str("2024-04-01")}}, false},
		{"bounded above", []Spec{{Label: "a", End: str("2024-03-31")}, {Label: "b", Start: str("2024-04-01"), End: str("2024-06-30")}}, false},
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
