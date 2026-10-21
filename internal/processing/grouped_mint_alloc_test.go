package processing

import (
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
)

// TestGroupMemory_GroupedTestAdmitAllocationFree: the grouped test
// fan-out's MaxGroups admission (tests x groups, checked before any
// bucket folds) allocates nothing while in bounds.
func TestGroupMemory_GroupedTestAdmitAllocationFree(t *testing.T) {
	g := &groupedRowTests{nTests: 3, limits: limits.Defaults()}
	got := testing.AllocsPerRun(1000, func() {
		if err := g.admit(1000); err != nil {
			t.Fatal(err)
		}
	})
	if got != 0 {
		t.Fatalf("grouped test admit allocates %.1f times per call, want 0", got)
	}
}

// TestGroupMemory_GroupedMatricesMintAllocationFree: a grouped matrix
// bucket mint on a slot-free plan set into a pre-sized bucket map — the
// path minus the slots it mints — allocates nothing, so the MaxGroups
// check at the mint costs no heap.
func TestGroupMemory_GroupedMatricesMintAllocationFree(t *testing.T) {
	const runs = 1000
	keys := make([]string, runs+1)
	for i := range keys {
		keys[i] = "k" + string(rune('A'+i%26)) + string(rune('a'+i/26%26)) + string(rune('0'+i/676))
	}
	g := &GroupedMatrices{
		set:     &matrixPlanSet{},
		buckets: make(map[string][]*matrixSlot, 4*runs),
		limits:  limits.Defaults(),
	}
	next := 0
	got := testing.AllocsPerRun(runs, func() {
		if _, err := g.bucket(keys[next]); err != nil {
			t.Fatal(err)
		}
		next++
	})
	if got != 0 {
		t.Fatalf("grouped matrix bucket mint allocates %.1f times per call, want 0", got)
	}
	if len(g.buckets) != runs+1 {
		t.Fatalf("minted %d buckets, want %d (every call must mint)", len(g.buckets), runs+1)
	}
}
