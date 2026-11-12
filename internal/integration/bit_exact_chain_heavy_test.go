//go:build heavy

package integration_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/spf13/afero"
)

// TestBitExact_ChainTotalsDecode is TestBitExact_ChainTotals' decode
// group: over the 120,000-row file, the total() chain under
// DecodeWorkers 2/3/7/8 (stage 0 on the parallel-decode arm, two-phase)
// equals the serial chain, stage by stage, bitwise.
func TestBitExact_ChainTotalsDecode(t *testing.T) {
	t.Parallel()

	fsys := afero.NewBasePathFs(afero.NewOsFs(), bitExactCohorts(t, bitExactRows, false))
	cohorts := map[invariance.Layout]invCohort{invariance.BigFile: {fsys, "flat.pulse"}}
	ref := chainCell{layout: invariance.BigFile, dw: 1, sw: 1}
	var cells []chainCell
	for _, dw := range invariance.BigFileDecoders {
		cells = append(cells, chainCell{layout: invariance.BigFile, dw: dw, sw: 1, stage0: observe.ArmParallelDecode, laterArm: observe.ArmStreaming})
	}
	checkBitExactChain(t, cohorts, ref, cells)
}
