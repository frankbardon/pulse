//go:build heavy

package integration_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestExtensionRegressionInvariance_Heavy is the heavy-tier half of the
// custom-regression family (extensions_regression_invariance_test.go):
// every shape under DecodeWorkers 3 and 8 over a 104,096-record cohort
// (above the parallel-decode threshold) ≡ serial, every cell on the
// serial arm (the archive group is the fast tier's).
func TestExtensionRegressionInvariance_Heavy(t *testing.T) {
	t.Parallel()

	cohorts := newRegInvFixture(t, true)
	for _, s := range regInvShapes() {
		invariance.Run(t, regInvFamily(cohorts, s, regInvGroups(s, true)))
	}
}
