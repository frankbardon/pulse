package synth

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestContingencyStrength_KnownTables pins the bias-corrected Cramér's V
// on tables whose answer is known in closed form.
func TestContingencyStrength_KnownTables(t *testing.T) {
	t.Parallel()

	cell := func(a, b string, n int) CategoricalPairCellSpec {
		return CategoricalPairCellSpec{AValue: a, BValue: b, Count: n}
	}
	// A perfect 2x2 association: φ = 1, and the bias correction at
	// n = 1000 leaves it within a fraction of a percent of 1.
	perfect := []CategoricalPairCellSpec{cell("x", "p", 500), cell("y", "q", 500)}
	if v := contingencyStrength(perfect); v < 0.99 || v > 1 {
		t.Errorf("perfect association: V = %v, want ~1", v)
	}
	// Exact independence: every cell at its expected count.
	indep := []CategoricalPairCellSpec{
		cell("x", "p", 250), cell("x", "q", 250), cell("y", "p", 250), cell("y", "q", 250),
	}
	if v := contingencyStrength(indep); v != 0 {
		t.Errorf("independent table: V = %v, want 0", v)
	}
	// A structural zero (q never occurs without x) is association, and
	// it must count: the unobserved (y, q) combination is a zero cell,
	// not a missing one. φ = (200·400 − 200·0)/√(400·400·600·200) ≈ 0.577.
	implication := []CategoricalPairCellSpec{cell("x", "q", 200), cell("x", "p", 200), cell("y", "p", 400)}
	if v := contingencyStrength(implication); math.Abs(v-0.5774) > 0.005 {
		t.Errorf("implication table: V = %v, want ~0.577", v)
	}
	// One observed column carries no association to measure.
	if v := contingencyStrength([]CategoricalPairCellSpec{cell("x", "p", 10), cell("y", "p", 10)}); v != 0 {
		t.Errorf("single-column table: V = %v, want 0", v)
	}
}

// TestContingencyStrength_BiasCorrectionLevelsTableShapes is why the
// corrected V is the arbitration measure: plain V on a wide table of an
// independent field sits well above zero, and would out-bid a weak real
// two-level driver.
func TestContingencyStrength_BiasCorrectionLevelsTableShapes(t *testing.T) {
	t.Parallel()

	// Ten levels, each with the same small wobble around independence
	// that sampling noise produces: plain V ~ 0.1, corrected ~0.
	var noisy []CategoricalPairCellSpec
	for i := 0; i < 10; i++ {
		a := string(rune('a' + i))
		d := 3
		if i%2 == 0 {
			d = -3
		}
		noisy = append(noisy,
			CategoricalPairCellSpec{AValue: a, BValue: "p", Count: 50 + d},
			CategoricalPairCellSpec{AValue: a, BValue: "q", Count: 50 - d})
	}
	if v := contingencyStrength(noisy); v > 0.02 {
		t.Errorf("noise over ten levels: corrected V = %v, want ~0", v)
	}
}

// TestGroupedStrength_KnownGroups pins adjusted η² and its refusal to
// guess when a count is missing.
func TestGroupedStrength_KnownGroups(t *testing.T) {
	t.Parallel()

	// Two groups of 100, means 0 and 2, sd 1: SSB = 200, SSW = 198,
	// ε² = (200 − 1·198/198)/398 = 0.5.
	two := []CategoricalNumericCategorySpec{
		{Category: "a", Mean: 0, Std: 1, N: 100},
		{Category: "b", Mean: 2, Std: 1, N: 100},
	}
	v, ok := groupedStrength(two)
	if !ok || math.Abs(v-0.5) > 1e-9 {
		t.Errorf("two groups: ε² = %v (ok %v), want 0.5", v, ok)
	}
	same := []CategoricalNumericCategorySpec{
		{Category: "a", Mean: 5, Std: 1, N: 100},
		{Category: "b", Mean: 5, Std: 1, N: 100},
	}
	if v, ok := groupedStrength(same); !ok || v != 0 {
		t.Errorf("equal means: ε² = %v (ok %v), want 0", v, ok)
	}
	uncounted := []CategoricalNumericCategorySpec{
		{Category: "a", Mean: 0, Std: 1, N: 100},
		{Category: "b", Mean: 2, Std: 1},
	}
	if _, ok := groupedStrength(uncounted); ok {
		t.Error("a category without n must leave the strength unknown")
	}
}

// TestWithinSetAcc_Table checks the 2x2 derivation from co-selection
// counts, including the structural zero being an absent cell.
func TestWithinSetAcc_Table(t *testing.T) {
	t.Parallel()

	acc := newWithinSetAcc(2)
	var none encoding.SetMask
	both := none.WithBit(0).WithBit(1)
	acc.add(both)
	acc.add(both)
	acc.add(none.WithBit(0))
	acc.add(none)
	got := acc.table(0, 1)
	want := map[[2]string]int{
		{"selected", "selected"}:         2,
		{"selected", "not_selected"}:     1,
		{"not_selected", "not_selected"}: 1,
	}
	if len(got) != len(want) {
		t.Fatalf("table = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("cell %v = %d, want %d", k, got[k], v)
		}
	}
}
