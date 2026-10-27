//go:build heavy

package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
)

// TestExtensionMatrixInvariance_Heavy is the heavy-tier half of the
// extension matrix family (extensions_matrix_invariance_test.go): every
// variant under DecodeWorkers 2/3/7/8 over a 105,177-record file ≡
// serial, each parallel cell on its parallel-decode arm, the buffered
// tier serial under DecodeWorkers 8 on the co-moment tier's bits (the
// archive group is the fast tier's).
func TestExtensionMatrixInvariance_Heavy(t *testing.T) {
	t.Parallel()

	cohorts := newExtInvFixture(t, true)
	decode := invariance.DecodeGroup("decode", invariance.BigFile)
	decode.Cells = append(decode.Cells, extInvSerialCell(invariance.BigFile, 8, 1, invariance.Buffered, observe.ArmBuffered))
	for _, v := range extInvVariants() {
		invariance.Run(t, extInvFamily(cohorts, v, []invariance.Group{decode}))
	}
}
