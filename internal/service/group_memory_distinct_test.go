package service

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// distinctMemoryCeiling is the bytes one AGG_DISTINCT_COUNT slot may add
// to a group bucket (components off) while the group holds at most
// eight distinct values — a typical per-customer order count. Before
// #723 every bucket carried its own map[float64]struct{} (a map header
// and its first group) plus a 24-byte aggregator: ~232 B a key at one
// to eight values. The inline value set (one 48-byte aggregator and a
// slice of at most 16 words) holds it at ~76–130 B.
const distinctMemoryCeiling = 160

// TestGroupMemory_DistinctCountBytesPerGroup (#723): a grouped
// partition of the parallel reducers holding one AGG_DISTINCT_COUNT
// slot, at one and four distinct values a key, and the merge of two
// such partitions (two and eight values a key), costs at most
// distinctMemoryCeiling bytes a key beyond the same bucket with no
// aggregation — live heap after a full GC (measureAggMemory).
func TestGroupMemory_DistinctCountBytesPerGroup(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	for _, rows := range []int{1, 4} {
		none := measureAggMemory(t, aggMemoryCase{off: true, rows: rows, merge: true}, aggMemoryKeys)
		f := measureAggMemory(t, aggMemoryCase{aggs: []*types.Aggregation{aggMemorySlot(types.AGG_DISTINCT_COUNT, "d", 0)}, off: true, rows: rows, merge: true}, aggMemoryKeys)
		part, merged := f.partition-none.partition, f.merged-none.merged
		t.Logf("rows=%d: partition %.1f B a key (%d values), merged %.1f B a key (%d values)", rows, part, rows, merged, 2*rows)
		if part > distinctMemoryCeiling {
			t.Errorf("rows=%d: a partition's AGG_DISTINCT_COUNT slot holds %.1f B a key, ceiling %d", rows, part, distinctMemoryCeiling)
		}
		if merged > distinctMemoryCeiling {
			t.Errorf("rows=%d: the merged AGG_DISTINCT_COUNT slot holds %.1f B a key, ceiling %d", rows, merged, distinctMemoryCeiling)
		}
	}
}
