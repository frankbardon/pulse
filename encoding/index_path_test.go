package encoding

import "testing"

func TestSidecarIndexPath_Deterministic(t *testing.T) {
	p1 := SidecarIndexPath("cohort.pulse", []string{"user_id"})
	p2 := SidecarIndexPath("cohort.pulse", []string{"user_id"})
	if p1 != p2 {
		t.Fatalf("SidecarIndexPath must be deterministic: %q vs %q", p1, p2)
	}
	if p1 == "" {
		t.Fatal("SidecarIndexPath returned empty string")
	}
}

func TestSidecarIndexPath_FollowsNamingConvention(t *testing.T) {
	path := SidecarIndexPath("cohort.pulse", []string{"user_id"})
	// cohort.pulse.<keyhash>.idx
	if len(path) < len("cohort.pulse.") || path[:len("cohort.pulse.")] != "cohort.pulse." {
		t.Fatalf("path %q does not start with %q", path, "cohort.pulse.")
	}
	if path[len(path)-4:] != ".idx" {
		t.Fatalf("path %q does not end with .idx", path)
	}
}

func TestSidecarIndexPath_DistinctForDistinctKeySets(t *testing.T) {
	pUserID := SidecarIndexPath("cohort.pulse", []string{"user_id"})
	pRegion := SidecarIndexPath("cohort.pulse", []string{"region"})
	if pUserID == pRegion {
		t.Fatalf("distinct key sets must derive distinct paths, both got %q", pUserID)
	}
}

func TestSidecarIndexPath_KeyColumnOrderSignificant(t *testing.T) {
	pAB := SidecarIndexPath("cohort.pulse", []string{"a", "b"})
	pBA := SidecarIndexPath("cohort.pulse", []string{"b", "a"})
	if pAB == pBA {
		t.Fatalf("key column order must be significant: (a,b) and (b,a) both derived %q", pAB)
	}
}

func TestSidecarIndexPath_NoNaiveConcatenationCollision(t *testing.T) {
	// ["ab", "c"] and ["a", "bc"] must not collide under naive
	// (no-separator) string concatenation.
	p1 := SidecarIndexPath("cohort.pulse", []string{"ab", "c"})
	p2 := SidecarIndexPath("cohort.pulse", []string{"a", "bc"})
	if p1 == p2 {
		t.Fatalf("naive-concatenation collision: [ab,c] and [a,bc] both derived %q", p1)
	}
}

func TestSidecarIndexPath_DistinctForDistinctCohorts(t *testing.T) {
	p1 := SidecarIndexPath("cohort_a.pulse", []string{"user_id"})
	p2 := SidecarIndexPath("cohort_b.pulse", []string{"user_id"})
	if p1 == p2 {
		t.Fatalf("distinct cohort paths must derive distinct sidecar paths, both got %q", p1)
	}
}
