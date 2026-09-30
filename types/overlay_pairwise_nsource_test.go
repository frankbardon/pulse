package types

import "testing"

// TestValidPairwiseNSource_NWithinDistinct pins the new mode into the
// validator set. descriptor.ValidateOverlays rejects any n_source this
// predicate does not know, so a mode missing here is unreachable
// through predict even though the runtime would honour it.
func TestValidPairwiseNSource_NWithinDistinct(t *testing.T) {
	if !ValidPairwiseNSource(PairwiseNSourceNWithinDistinct) {
		t.Fatalf("ValidPairwiseNSource(%q) = false, want true", PairwiseNSourceNWithinDistinct)
	}
	if PairwiseNSourceNWithinDistinct != "n_within_distinct" {
		t.Fatalf("PairwiseNSourceNWithinDistinct = %q, want n_within_distinct", PairwiseNSourceNWithinDistinct)
	}
	if ValidPairwiseNSource("n_within_distinct_typo") {
		t.Fatal("ValidPairwiseNSource accepted an unknown mode")
	}
}

// TestValidPairwiseNSource_MarginDistinct pins the E1-S4 modes into the
// enum by their exact wire spelling. Predict refuses an unknown
// n_source outright (descriptor.validateOverlayPairwise), so a constant
// that never reaches ValidPairwiseNSource is a mode no request can name.
func TestValidPairwiseNSource_MarginDistinct(t *testing.T) {
	for constant, want := range map[string]string{
		PairwiseNSourceRowMarginDistinct:    "row_margin_distinct",
		PairwiseNSourceColumnMarginDistinct: "column_margin_distinct",
	} {
		if constant != want {
			t.Errorf("constant = %q, want %q", constant, want)
		}
		if !ValidPairwiseNSource(constant) {
			t.Errorf("ValidPairwiseNSource(%q) = false, want true", constant)
		}
	}
	if ValidPairwiseNSource("row_margin_distinct_typo") {
		t.Fatal("ValidPairwiseNSource accepted an unknown margin mode")
	}
}

// TestPairwiseNSourceUsesWithinDepth covers the slab-mode predicate the
// n_within_depth range guard keys off — both slab modes in, every
// non-slab mode out.
func TestPairwiseNSourceUsesWithinDepth(t *testing.T) {
	in := []string{PairwiseNSourceNWithin, PairwiseNSourceNWithinDistinct}
	for _, s := range in {
		if !PairwiseNSourceUsesWithinDepth(s) {
			t.Errorf("PairwiseNSourceUsesWithinDepth(%q) = false, want true", s)
		}
	}
	out := []string{
		"", PairwiseNSourceCellNUnweighted, PairwiseNSourceCellValueWeight,
		PairwiseNSourceRowMarginN, PairwiseNSourceColumnMarginN,
		PairwiseNSourceRowMarginDistinct, PairwiseNSourceColumnMarginDistinct,
		PairwiseNSourceCellWeightSum,
	}
	for _, s := range out {
		if PairwiseNSourceUsesWithinDepth(s) {
			t.Errorf("PairwiseNSourceUsesWithinDepth(%q) = true, want false", s)
		}
	}
}
