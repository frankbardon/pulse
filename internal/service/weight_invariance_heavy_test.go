//go:build heavy

package service

import (
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestWeightInvariance_CrosstabHeavy: the one crosstab arm
// DecodeWorkers changes is the buffered record materialisation above
// the threshold; parallel materialisation reproduces the serial record
// stream, so overlay, Components and matrix equal the serial fused
// answer, and the fused arm ignores DecodeWorkers.
func TestWeightInvariance_CrosstabHeavy(t *testing.T) {
	t.Parallel()

	cfg, osFs, dir := svcInvDisk(t)
	path := filepath.Join(dir, "wz_decode.pulse")
	recs, nullAt := wzsRows(parallelDecodeRecordThreshold+4096, 0)
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, wzsSchema(), recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	decode := invariance.DecodeGroup("decode", invariance.BigFile)
	for i := range decode.Cells {
		decode.Cells[i].Mode = invariance.Buffered
	}
	decode.Ref.Arm = observe.ArmFusedCrosstab
	decode.Cells = append(decode.Cells,
		invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.BigFile, DecodeWorkers: 1, ShardWorkers: 1, Mode: invariance.Buffered}, Arm: observe.ArmBuffered},
		invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.BigFile, DecodeWorkers: 4, ShardWorkers: 1}, Arm: observe.ArmFusedCrosstab})
	cohorts := map[invariance.Layout]svcInvCohort{invariance.BigFile: {cfg, path}}
	xt := func() *types.Request { return wzsCrosstabRequest("") }
	invariance.Run(t, weightInvFamily("crosstab", cohorts, []invariance.Group{decode}, xt, weightInvFusion, nil, weightInvCrosstabCheck))
}
