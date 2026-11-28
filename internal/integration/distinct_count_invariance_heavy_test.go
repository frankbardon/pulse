//go:build heavy

package integration_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestDistinctCountInvariance_Heavy is the heavy-tier half of the
// AGG_DISTINCT_COUNT family (distinct_count_invariance_test.go):
// DecodeWorkers 2/3/7/8 over a file above the parallel-decode threshold
// ≡ serial.
func TestDistinctCountInvariance_Heavy(t *testing.T) {
	t.Parallel()

	groups := []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile)}
	for _, f := range distinctInvFamilies(newDistinctInvFixture(t, true), groups) {
		invariance.Run(t, f)
	}
}
