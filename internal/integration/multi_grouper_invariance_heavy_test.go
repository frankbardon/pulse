//go:build heavy

package integration_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestMultiGrouperInvariance_Heavy is the heavy-tier half of the
// multi-grouper family (multi_grouper_invariance_test.go): every
// mergeable case under DecodeWorkers 2/3/7/8 over a file above the
// parallel-decode threshold ≡ serial, each parallel run on its
// parallel-decode arm, and over the full archive group.
func TestMultiGrouperInvariance_Heavy(t *testing.T) {
	t.Parallel()

	groups := []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile), invariance.ArchiveGroup()}
	for _, f := range mgInvFamilies(newMGInvFixture(t, true), func(mgCase) bool { return true }, groups) {
		invariance.Run(t, f)
	}
}
