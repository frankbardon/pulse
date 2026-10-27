//go:build heavy

package service

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
)

// TestMatrixSerialInvariance_Heavy is the heavy-tier half of the rank
// family (matrix_serial_invariance_test.go): over a 101,234-record file
// above the parallel-decode threshold, DecodeWorkers 2/3/7/8 never fan a
// rank-method request out (every cell on the serial buffered arm) and
// answer the serial bits, and an on-disk archive of the same rows
// answers them under every ShardWorkers count.
func TestMatrixSerialInvariance_Heavy(t *testing.T) {
	t.Parallel()

	groups := []invariance.Group{
		svcInvSerialDecodeGroup("decode", invariance.BigFile, observe.ArmBuffered),
		svcInvSerialArchiveGroup(observe.ArmBuffered),
	}
	invariance.Run(t, rankInvFamily(t, newRankInvFixture(t, true), groups))
}
