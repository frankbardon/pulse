package main

import (
	"slices"
	"testing"
)

// TestWidenLeafIsRegistered asserts `pulse widen` is actually reachable from
// the binary's command tree.
//
// TestSkillsCoverAllCliLeaves does NOT cover this, and cannot: it walks
// buildApp() and requires every leaf it finds to be documented, so a leaf
// that is implemented in internal/cli but never mounted on the tree makes
// that gate pass VACUOUSLY — there is simply one fewer leaf to check. The
// failure mode is a `pulse widen` that exists in the package, is documented
// in the command index, and answers "command not found" at the terminal.
func TestWidenLeafIsRegistered(t *testing.T) {
	leaves := cliLeaves()
	if !slices.Contains(leaves, "pulse widen") {
		t.Errorf("`pulse widen` is not a runnable leaf of buildApp(); got %v", leaves)
	}
}
