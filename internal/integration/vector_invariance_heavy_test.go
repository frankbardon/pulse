//go:build heavy

package integration_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestVectorInvariance_Heavy is the heavy-tier half of the vector
// family (vector_invariance_test.go): every shape under DecodeWorkers
// 2/3/7/8 over a 103,634-record file ≡ serial, each parallel cell on its
// parallel-decode arm (a crosstab's buffered arm; its fused arm stays
// serial), and the archive group over the full split.
func TestVectorInvariance_Heavy(t *testing.T) {
	t.Parallel()

	cohorts := newVecInvFixture(t, true)
	for _, s := range vecInvShapes() {
		invariance.Run(t, vecInvFamily(cohorts, s, vecInvGroups(s, true)))
	}
}
