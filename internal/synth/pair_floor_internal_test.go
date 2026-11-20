package synth

import (
	"strings"
	"testing"
)

// floorCells is a balanced 2x2 table (n = 10,000) whose association
// grows with d: φ = d/2500, so the bias-corrected V is
// √(φ² − 1/9999). d = 30 → V ≈ 0.007, d = 40 → V ≈ 0.012 (both noise
// under the 0.02 default floor), d = 100 → V ≈ 0.039 (above it).
func floorCells(d int) []CategoricalPairCellSpec {
	return []CategoricalPairCellSpec{
		{AValue: "a", BValue: "x", Count: 2500 + d}, {AValue: "a", BValue: "y", Count: 2500 - d},
		{AValue: "b", BValue: "x", Count: 2500 - d}, {AValue: "b", BValue: "y", Count: 2500 + d},
	}
}

// floorGroups is a two-group numeric target (5,000 rows each, std 1)
// whose group means sit m apart, so ε² ≈ (2500·m² − 1)/10000.
func floorGroups(m float64) []CategoricalNumericCategorySpec {
	return []CategoricalNumericCategorySpec{
		{Category: "a", Mean: 0, Std: 1, N: 5000}, {Category: "b", Mean: m, Std: 1, N: 5000},
	}
}

// catContest is two categorical pairs bidding for t: first (declared
// first) with association dFirst, second with dSecond.
func catContest(dFirst, dSecond int, floor *float64) *Spec {
	return &Spec{
		CategoricalPairs: []CategoricalPairSpec{
			{A: "first", B: "t", Cells: floorCells(dFirst)},
			{A: "second", B: "t", Cells: floorCells(dSecond)},
		},
		PairStrengthFloor: floor,
	}
}

func f64(v float64) *float64 { return &v }

// TestPairStrengthFloor_SubFloorBiddersKeepDeclaredOrder is FR-7/FR-8:
// two noise bidders no longer pick a target by whichever measured
// larger — the first-declared keeps it, the loser's warning names the
// floor — and a floor of 0 restores the floorless rule exactly.
func TestPairStrengthFloor_SubFloorBiddersKeepDeclaredOrder(t *testing.T) {
	t.Parallel()

	// Guard the fixture: both bidders are noise, the second stronger.
	v1, v2 := contingencyStrength(floorCells(30)), contingencyStrength(floorCells(40))
	if v1 <= 0 || v1 >= v2 || v2 >= DefaultPairStrengthFloor {
		t.Fatalf("fixture strengths %v, %v: want 0 < first < second < floor %v", v1, v2, DefaultPairStrengthFloor)
	}

	res := resolveConflicts(catContest(30, 40, nil))
	if len(res.catPairs) != 1 || res.catPairs[0].A != "first" {
		t.Fatalf("default floor kept %+v, want the first-declared first->t", res.catPairs)
	}
	want := `conditional relationship conflict: field "t" is already claimed by categorical pair (first -> t); ` +
		`dropping categorical pair (second -> t) (every bidder is below the pair strength floor 0.02, so the first-declared kept it)`
	if len(res.warnings) != 1 || res.warnings[0] != want {
		t.Errorf("warnings = %q,\nwant [%q]", res.warnings, want)
	}

	// An explicit floor equal to the default is the default.
	if got := resolveConflicts(catContest(30, 40, f64(DefaultPairStrengthFloor))); got.catPairs[0].A != "first" {
		t.Errorf("explicit default floor kept %s->t, want first->t", got.catPairs[0].A)
	}

	// Floor 0: the stronger noise bidder wins, with the ordinary warning.
	off := resolveConflicts(catContest(30, 40, f64(0)))
	if len(off.catPairs) != 1 || off.catPairs[0].A != "second" {
		t.Fatalf("floor 0 kept %+v, want the stronger second->t", off.catPairs)
	}
	if len(off.warnings) != 1 || strings.Contains(off.warnings[0], "floor") {
		t.Errorf("floor 0 warnings = %q, want one ordinary conflict warning", off.warnings)
	}

	// Declared arbitration ignores the floor: the first-declared wins
	// with the ordinary warning, exactly as before the floor existed.
	declared := catContest(30, 40, nil)
	declared.PairArbitration = PairArbitrationDeclared
	dec := resolveConflicts(declared)
	if dec.catPairs[0].A != "first" || strings.Contains(dec.warnings[0], "floor") {
		t.Errorf("declared kept %s->t with %q, want first->t and no floor wording", dec.catPairs[0].A, dec.warnings)
	}
}

// TestPairStrengthFloor_AboveFloorBeatsSubFloor: a real driver declared
// SECOND still beats a first-declared noise bidder, and its loser gets
// the ordinary warning — the floor only decides among sub-floor bidders.
func TestPairStrengthFloor_AboveFloorBeatsSubFloor(t *testing.T) {
	t.Parallel()

	if v := contingencyStrength(floorCells(100)); v < DefaultPairStrengthFloor {
		t.Fatalf("fixture: driver V %v is not above the floor", v)
	}
	res := resolveConflicts(catContest(40, 100, nil))
	if len(res.catPairs) != 1 || res.catPairs[0].A != "second" {
		t.Fatalf("kept %+v, want the above-floor second->t", res.catPairs)
	}
	if len(res.warnings) != 1 || strings.Contains(res.warnings[0], "floor") {
		t.Errorf("warnings = %q, want one ordinary conflict warning", res.warnings)
	}
}

// TestPairStrengthFloor_NumericTargetComparesSquared: a numeric target's
// strength is ε², a variance share, so the floor applies squared. A
// driver with √ε² ≈ 0.03 is above a 0.02 floor even though its ε² is
// far below 0.02; two bidders with √ε² ≈ 0.01 / 0.014 are noise.
func TestPairStrengthFloor_NumericTargetComparesSquared(t *testing.T) {
	t.Parallel()

	contest := func(mFirst, mSecond float64) *Spec {
		return &Spec{CategoricalNumericPairs: []CategoricalNumericPairSpec{
			{A: "first", B: "y", Categories: floorGroups(mFirst)},
			{A: "second", B: "y", Categories: floorGroups(mSecond)},
		}}
	}
	sq := float64(DefaultPairStrengthFloor * DefaultPairStrengthFloor)
	eps := func(m float64) float64 {
		v, ok := groupedStrength(floorGroups(m))
		if !ok {
			t.Fatal("fixture strength unmeasured")
		}
		return v
	}
	noiseA, noiseB, real := eps(0.028), eps(0.035), eps(0.063)
	if noiseA <= 0 || noiseA >= noiseB || noiseB >= sq || real <= sq || real >= DefaultPairStrengthFloor {
		t.Fatalf("fixture ε² %v, %v, %v: want 0 < a < b < floor² %v < real < floor", noiseA, noiseB, real, sq)
	}

	res := resolveConflicts(contest(0.028, 0.035))
	if len(res.catNumPairs) != 1 || res.catNumPairs[0].A != "first" {
		t.Errorf("noise bidders kept %+v, want the first-declared first->y", res.catNumPairs)
	}
	if len(res.warnings) != 1 || !strings.Contains(res.warnings[0], "pair strength floor 0.02") {
		t.Errorf("warnings = %q, want the loser's warning to name the floor", res.warnings)
	}

	res = resolveConflicts(contest(0.028, 0.063))
	if len(res.catNumPairs) != 1 || res.catNumPairs[0].A != "second" {
		t.Errorf("kept %+v, want the above-floor (√ε² ≈ 0.03) second->y", res.catNumPairs)
	}
}
