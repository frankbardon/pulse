//go:build heavy

package service

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestMatrixInvariance_Heavy is the heavy-tier body of the matrix family
// (matrix_invariance_test.go): every shape under DecodeWorkers 2/3/7/8
// over a 105,177-record file (ends mid-block) ≡ serial; the ungrouped
// shapes also over a 102,400-record file (ends on a block boundary) and
// a one-shard archive ≡ its single file; and the archive group (with
// the buffered arm) over the full 8-shard archive.
func TestMatrixInvariance_Heavy(t *testing.T) {
	t.Parallel()

	f := newMatInvFixture(t, true)
	matInvAdmitted(t, f, matInvShapes())
	archive := invariance.ArchiveGroup()
	archive.Cells = append(archive.Cells, invariance.BufferedCell())
	oneShard := invariance.Group{Name: "one_shard",
		Ref:   invariance.Cell{Knobs: invariance.Knobs{Layout: matInvOneFile}},
		Cells: []invariance.Cell{{Knobs: invariance.Knobs{Layout: matInvOneArchive}}}}
	for _, s := range matInvShapes() {
		groups := []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile), archive}
		if s.grouper == nil && !s.vectors {
			groups = append(groups, invariance.DecodeGroup("decode_aligned", matInvBigAligned), oneShard)
		}
		invariance.Run(t, matInvFamily(f, s, groups))
	}
}
