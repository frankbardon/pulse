package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestShardWorkers_SortOnGrouperKeyFollowsKeyOrder: Request.Sort on a
// GROUP_RANGE key column orders the buckets by their numeric low edge
// ("9-12" before "12-15"), never byte-wise, and the parallel shard
// reducer (the shared FinalizeGroupedStream tail) answers in exactly the
// order the serial arm does, ascending and descending.
func TestShardWorkers_SortOnGrouperKeyFollowsKeyOrder(t *testing.T) {
	shards := parityShards()
	files := map[string][]byte{}
	var names []string
	for i, g := range shards {
		flat, _, _ := g.build(t, nil)
		n := fmt.Sprintf("f/s%d.pulse", i)
		files[n] = flat
		names = append(names, n)
	}
	svc, _ := gEnv(t, files)
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "flat.pulse", names); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	cohort, err := svc.Open(ctx, "flat.pulse")
	if err != nil {
		t.Fatal(err)
	}
	lowEdge := func(key string) float64 {
		// "lo-hi"; lo may itself be negative ("-3-0").
		i := strings.Index(key[1:], "-") + 1
		v, err := strconv.ParseFloat(key[:i], 64)
		if err != nil {
			t.Fatalf("bucket key %q: %v", key, err)
		}
		return v
	}
	for _, desc := range []bool{false, true} {
		req := &types.Request{
			Cohort:       &types.Cohort{Filename: "flat.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "weight", Interval: 3}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
			Sort:         []types.OrderKey{{Field: "weight", Desc: desc}},
		}
		var serial []string
		for _, workers := range []int{1, 3} {
			svc.SetShardWorkers(workers)
			if workers != 1 {
				if _, ok := svc.shouldFanOut(req, cohort); !ok {
					t.Fatalf("shouldFanOut refused %d workers", workers)
				}
			}
			resp, err := svc.Process(ctx, req)
			svc.SetShardWorkers(0)
			if err != nil {
				t.Fatalf("Process(workers=%d): %v", workers, err)
			}
			keys := make([]string, len(resp.Data))
			for i, row := range resp.Data {
				keys[i] = row["weight"].(string)
			}
			if len(keys) < 3 {
				t.Fatalf("workers=%d: %d buckets, want several", workers, len(keys))
			}
			byteWise := false
			for i := 1; i < len(keys); i++ {
				a, b := lowEdge(keys[i-1]), lowEdge(keys[i])
				if (!desc && a >= b) || (desc && a <= b) {
					t.Errorf("desc=%v workers=%d: %q then %q is not by low edge: %v", desc, workers, keys[i-1], keys[i], keys)
				}
				if (keys[i-1] < keys[i]) != (a < b) {
					byteWise = true
				}
			}
			if !byteWise {
				t.Fatalf("fixture keys %v sort the same byte-wise; the test would pass vacuously", keys)
			}
			if workers == 1 {
				serial = keys
			} else if strings.Join(keys, ",") != strings.Join(serial, ",") {
				t.Errorf("desc=%v workers=%d order %v != serial %v", desc, workers, keys, serial)
			}
		}
	}
}
