//go:build heavy

package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestPreviousStateInvariance_Heavy is the heavy-tier half of the
// previous-state family (previous_state_invariance_test.go): every
// matrix request over the filler-interleaved file ≡ serial — a crosstab
// under DecodeWorkers 2/3/7/8 on its parallel segment decode, the rest
// serial under 3 and 8 — the reference equal to the panel's single-file
// answer (the archive group is the fast tier's).
func TestPreviousStateInvariance_Heavy(t *testing.T) {
	t.Parallel()

	panel, _, _ := prevModeBaseline(t)
	cohorts := newPrevInvFixture(t, true)
	for _, name := range prevInvNames() {
		invariance.Run(t, prevInvFamily(cohorts, name, prevInvGroups(name, true), panel))
	}
}
