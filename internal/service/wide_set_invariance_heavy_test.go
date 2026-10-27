//go:build heavy

package service

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestWideSetInvariance_Heavy is the heavy-tier half of the wide-set
// family (wide_set_invariance_test.go): DecodeWorkers 2/3/7/8 over an
// on-disk set_u256 cohort above the parallel-decode threshold ≡ serial,
// each parallel cell on its parallel-decode arm, plus the archive group.
func TestWideSetInvariance_Heavy(t *testing.T) {
	t.Parallel()

	groups := []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile), invariance.ArchiveGroup()}
	invariance.Run(t, wideSetInvFamily(newWideSetInvFixture(t, true), groups))
}
