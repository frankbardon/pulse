package descriptor

import (
	"slices"
	"testing"
)

func TestLevels_OrderAndFresh(t *testing.T) {
	t.Parallel()

	got := Levels()
	if !slices.Equal(got, []Level{LevelBasic, LevelIntermediate, LevelAdvanced}) {
		t.Fatalf("Levels() = %v", got)
	}
	got[0] = "mutated"
	if Levels()[0] != LevelBasic {
		t.Fatal("Levels shares its backing array")
	}
}

func TestExplainDetails_OrderAndFresh(t *testing.T) {
	t.Parallel()

	got := ExplainDetails()
	if !slices.Equal(got, []ExplainDetail{ExplainTerse, ExplainFull}) {
		t.Fatalf("ExplainDetails() = %v", got)
	}
	got[0] = "mutated"
	if ExplainDetails()[0] != ExplainTerse {
		t.Fatal("ExplainDetails shares its backing array")
	}
}

// TestExplainRequestRoots_RequestModeOnly: the request-mode roots in
// ExplainRequest field order — never a response-mode spelling.
func TestExplainRequestRoots_RequestModeOnly(t *testing.T) {
	t.Parallel()

	got := ExplainRequestRoots()
	want := []ExplainRoot{ExplainRootRequest, ExplainRootComposed, ExplainRootChain, ExplainRootFacet, ExplainRootSample}
	if !slices.Equal(got, want) {
		t.Fatalf("ExplainRequestRoots() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if ExplainRequestRoots()[0] != ExplainRootRequest {
		t.Fatal("ExplainRequestRoots shares its backing array")
	}
}
