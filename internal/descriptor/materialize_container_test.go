package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestMaterializeContainerPlan_ShardCount: the container plan is a file
// without the keys; a shard archive's MaxShards is ceil(records /
// max_records), at least 1 for an empty source, absent when the record
// count is unknown; the shard-limit risk fires only past 65,535 shards,
// and the run-time limit opens at max_records × 65,535 rows.
func TestMaterializeContainerPlan_ShardCount(t *testing.T) {
	t.Parallel()

	if p := MaterializeContainerPlan(&types.Request{}, 10); p.Kind != "file" || p.MaxShards != nil || p.ElideConstants {
		t.Errorf("absent output plan = %+v", p)
	}
	out := func(n int) *types.Request {
		return &types.Request{Output: &types.MaterializeOutput{Shards: &types.MaterializeShards{MaxRecords: n}, ElideConstants: true}}
	}
	for _, tc := range []struct {
		records int64
		max     int
		want    int64
	}{{0, 5, 1}, {1, 5, 1}, {5, 5, 1}, {6, 5, 2}, {12, 5, 3}, {65535, 1, 65535}, {65536, 1, 65536}} {
		p := MaterializeContainerPlan(out(tc.max), tc.records)
		if p.Kind != "shard_archive" || p.MaxRecords != tc.max || !p.ElideConstants || p.MaxShards == nil || *p.MaxShards != tc.want {
			t.Errorf("records %d / max %d: plan = %+v, want %d shards", tc.records, tc.max, p, tc.want)
		}
		if risk := MaterializeShardLimitRisk(p); (risk != nil) != (tc.want > 65535) {
			t.Errorf("records %d / max %d: risk = %+v", tc.records, tc.max, risk)
		}
	}
	if p := MaterializeContainerPlan(out(5), -1); p.MaxShards != nil || MaterializeShardLimitRisk(p) != nil {
		t.Errorf("unknown record count: plan = %+v", p)
	}
	if MaterializeShardLimitReached(65534, 1) || !MaterializeShardLimitReached(65535, 1) || !MaterializeShardLimitReached(131070, 2) || MaterializeShardLimitReached(1<<40, 0) {
		t.Error("MaterializeShardLimitReached boundary wrong")
	}
}
