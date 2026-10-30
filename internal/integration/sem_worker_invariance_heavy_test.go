//go:build heavy

package integration_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestSEMInvariance_Heavy is the heavy-tier body of the MAT_SEM family
// (sem_worker_invariance_test.go): the shapes the fast tier skips over
// their archive groups, and the smoke shapes under DecodeWorkers 2/3/7/8
// over the rows repeated past the parallel-decode threshold ≡ serial,
// each on its parallel-decode arm.
func TestSEMInvariance_Heavy(t *testing.T) {
	t.Parallel()

	fixtures := semInvFixtures{}
	for _, s := range semInvShapes() {
		invariance.Run(t, semInvFamily(t, fixtures, s, true))
	}
}
