//go:build heavy

package integration_test

import (
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestKMeansInvariance_Heavy is the big-file half of the GROUP_KMEANS
// family: above the parallel-decode threshold, DecodeWorkers 3, 8 and 4
// answer the serial read on the serial arm (the grouper never fans out).
func TestKMeansInvariance_Heavy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), paritySchema(t), 0, parityLargeRows)
	cohorts := map[invariance.Layout]invCohort{invariance.BigFile: {afero.NewBasePathFs(afero.NewOsFs(), dir), "large.pulse"}}
	g := invSerialDecodeGroup("decode", invariance.BigFile, kmInvArm)
	g.Ref.Arm = kmInvArm
	g.Cells = append(g.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.BigFile, DecodeWorkers: 4, ShardWorkers: 1}, Arm: kmInvArm})
	invariance.Run(t, kmInvFamily(cohorts, []invariance.Group{g}))
}
