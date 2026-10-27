//go:build heavy

package encoding

import (
	"fmt"
	"testing"
)

// TestReadRecordReusedWithPlan_MatchesReusedRestrictedPerType sweeps every
// 2^N projection subset of each per-type fixture and asserts the plan-aware
// reuse output equals the full-reuse output restricted to that subset —
// including subsets that drop a bit-packed member sharing a byte with a
// retained member and subsets that drop/retain a nullable field.
func TestReadRecordReusedWithPlan_MatchesReusedRestrictedPerType(t *testing.T) {
	t.Parallel()

	for _, f := range perTypeFixtures() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()

			if got := len(f.schema.Fields); got > 12 {
				t.Fatalf("fixture %s has %d fields; subset sweep intractable", f.name, got)
			}
			records := generatePerTypeRecords(t, f, 0x9A11+int64(len(f.name)))
			fieldCount := uint(len(f.schema.Fields))
			masks := uint64(1) << fieldCount
			for mask := uint64(0); mask < masks; mask++ {
				retained := subsetFromMask(f.schema, mask)
				for ri, raw := range records {
					label := fmt.Sprintf("%s/mask=%0*b/rec=%d", f.name, fieldCount, mask, ri)
					assertPlanReusedMatchesRestricted(t, f.schema, raw, retained, label)
					if t.Failed() {
						return
					}
				}
			}
		})
	}
}
