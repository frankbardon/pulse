package service

import (
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
)

// TestGroupMemory_FacetMintAllocationFree: minting a faceted value's
// bucket counts it against MaxGroups and allocates nothing — the limit
// check at the mint must not heap-copy the run's Limits.
func TestGroupMemory_FacetMintAllocationFree(t *testing.T) {
	a := newBoolAccumulator(nil, limits.Defaults())
	got := testing.AllocsPerRun(1000, func() {
		if err := a.mint(); err != nil {
			t.Fatal(err)
		}
	})
	if got != 0 {
		t.Fatalf("bool facet mint allocates %.1f times per call, want 0", got)
	}
}
