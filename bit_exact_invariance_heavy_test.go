//go:build heavy

package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/invariance"
)

// TestBitExact_Heavy is the heavy-tier body of the bit-exactness family
// (bit_exact_invariance_test.go): the archive groups of every shape the
// fast tier's smoke leaves out, and every shape's decode group over the
// 120,000-record file — DecodeWorkers 2/3/7/8 ≡
// serial on the parallel-decode arm (the sequential total() shapes on
// their serial arm; a crosstab's fused arm serial, its buffered arm
// fanned out).
func TestBitExact_Heavy(t *testing.T) {
	t.Parallel()

	cohorts := newBitInvFixture(t, true)
	for _, s := range bitInvShapes() {
		for _, heavy := range []bool{false, true} {
			if !heavy && bitInvSmoke[s.name] {
				continue // TestBitExact_Archive runs it
			}
			f := bitInvFamily(cohorts, s, heavy)
			f.Name += map[bool]string{false: "/archive", true: "/decode"}[heavy]
			invariance.Run(t, f)
		}
	}
}
