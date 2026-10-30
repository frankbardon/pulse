//go:build heavy

package integration_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestGroupedComponentsInvariance_Heavy is the heavy-tier half of the
// grouped-components family (grouped_components_invariance_test.go):
// DecodeWorkers 2/3/7/8 over a file above the parallel-decode threshold
// ≡ serial, extension slot merged on every parallel run.
func TestGroupedComponentsInvariance_Heavy(t *testing.T) {
	t.Parallel()

	groups := []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile)}
	for _, f := range gaInvFamilies(newGAInvFixture(t, true), groups) {
		invariance.Run(t, f)
	}
}
