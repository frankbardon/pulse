//go:build heavy

package service

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestOverlayInvariance_Heavy is the heavy-tier half of the overlay /
// post-hook family (overlay_invariance_test.go): both shapes over a
// 104,096-record file ≡ serial — the test-free SERIES host under
// DecodeWorkers 2/3/7/8 on the parallel-decode arm under an identity
// block, the tests under DecodeWorkers 3 and 8 falling back to the
// serial arm and corrected (the archive group is the fast tier's).
func TestOverlayInvariance_Heavy(t *testing.T) {
	t.Parallel()

	cohorts, rows := newOverlayInvFixture(t, true)
	for _, s := range overlayInvShapes() {
		invariance.Run(t, overlayInvFamily(cohorts, rows, s, overlayInvGroups(s, true)))
	}
}
