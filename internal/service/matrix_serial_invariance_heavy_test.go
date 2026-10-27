//go:build heavy

package service

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
)

// TestMatrixSerialInvariance_Heavy is the heavy-tier half of the rank
// family (matrix_serial_invariance_test.go): over a 101,234-record file
// above the parallel-decode threshold, DecodeWorkers 8 never fans a
// rank-method request out (the serial buffered arm) and answers the
// serial bits, and an on-disk archive of the same rows answers them
// under ShardWorkers 8 × DecodeWorkers 7 (the full knob sweep is the
// fast tier's, on the small archive).
func TestMatrixSerialInvariance_Heavy(t *testing.T) {
	t.Parallel()

	groups := []invariance.Group{{Name: "spot",
		Ref: invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.File, DecodeWorkers: 1, ShardWorkers: 1}},
		Cells: []invariance.Cell{
			{Knobs: invariance.Knobs{Layout: invariance.BigFile, DecodeWorkers: 8, ShardWorkers: 1}, Arm: observe.ArmBuffered},
			{Knobs: invariance.Knobs{Layout: invariance.Archive, DecodeWorkers: 7, ShardWorkers: 8}, Arm: observe.ArmBuffered},
		}}}
	invariance.Run(t, rankInvFamily(t, newRankInvFixture(t, true), groups))
}
