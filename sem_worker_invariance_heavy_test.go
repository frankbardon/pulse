//go:build heavy

package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestSEMInvariance_Heavy is the heavy-tier body of the MAT_SEM family
// (sem_worker_invariance_test.go): every shape over the archive group, the
// mergeable ones also under DecodeWorkers 2/3/7/8 over the rows repeated past the parallel-decode threshold ≡
// serial, each on its parallel-decode arm.
func TestSEMInvariance_Heavy(t *testing.T) {
	t.Parallel()

	for _, s := range semInvShapes() {
		invariance.Run(t, semInvFamily(t, s, true))
	}
}
