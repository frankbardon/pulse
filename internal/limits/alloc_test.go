package limits

import "testing"

// TestGroupMemory_MaxGroupsCheckAllocationFree: the MaxGroups check runs
// at every bucket mint of every grouped arm, so the in-bounds path must
// not allocate — a Limits value that escaped to the heap through the
// field-accessor table cost ~100 B per mint (0.23 GB on a 1/20
// retention-shaped run).
func TestGroupMemory_MaxGroupsCheckAllocationFree(t *testing.T) {
	// serial: testing.AllocsPerRun counts process-wide allocations, which concurrent tests inflate

	l := Defaults()
	names := Names()
	var sink int64
	cases := map[string]func(){
		"Check":    func() { _ = Check(l, MaxGroups, 42) },
		"Evaluate": func() { _, _ = Evaluate(l, MaxGroups, 42, Possible) },
		"Value": func() {
			for _, n := range names {
				sink += Value(l, n)
			}
		},
		"Validate": func() { _ = Validate(l) },
	}
	for name, fn := range cases {
		if got := testing.AllocsPerRun(1000, fn); got != 0 {
			t.Errorf("%s allocates %.1f times per call, want 0", name, got)
		}
	}
	_ = sink
}

// TestValue_EveryNameRoundTrips: Value reads the field each Name binds
// (a distinct value per field, so a crossed accessor shows), and 0 for
// an unknown name.
func TestValue_EveryNameRoundTrips(t *testing.T) {
	t.Parallel()

	var l Limits
	for i, n := range Names() {
		l = setField(t, l, n, int64(1000+i))
	}
	for i, n := range Names() {
		if got := Value(l, n); got != int64(1000+i) {
			t.Errorf("Value(%s) = %d, want %d", n, got, 1000+i)
		}
	}
	if got := Value(l, "nope"); got != 0 {
		t.Errorf("Value(unknown) = %d, want 0", got)
	}
}
