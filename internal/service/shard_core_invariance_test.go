package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// shard_core_invariance_test.go is the shard-core family on the shared
// worker-invariance harness: the per-shard reducer's plain requests —
// one scalar aggregator at a time, a filtered count, a GROUP_CATEGORY
// sum, row-local read attributes, a Sort on a GROUP_RANGE key — answer
// the same whole response (LayoutFree, bit-exact) on a shard archive
// under ShardWorkers 1/2/3/8 and DecodeWorkers 7 as on its single-file
// twin read serially, each fan-out cell on the shard-parallel arm. The
// two-pass ATTR_ZSCORE request is refused by the merge gate, so every
// cell of its group stays on the serial arm and still equals the twin.

// shardCoreCohorts is fourShardScoreCohort's archive (four shards of
// four rows) and its single-file concatenation.
func shardCoreCohorts(t *testing.T) map[invariance.Layout]svcInvCohort {
	t.Helper()
	cfg, _, _ := fourShardScoreCohort(t)
	return map[invariance.Layout]svcInvCohort{
		invariance.File:    {cfg, "arch.pulse.concat"},
		invariance.Archive: {cfg, "arch.pulse"},
	}
}

// shardCoreFamily runs mk over cohorts; check guards the reference.
func shardCoreFamily(name string, cohorts map[invariance.Layout]svcInvCohort, groups []invariance.Group, mk func() *types.Request, check func(t *testing.T, resp *types.Response)) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   name,
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			c, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built", k.Layout)
			}
			return svcInvProcess(t, c, k, mk(), nil)
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) { check(t, resp) },
	}
}

// shardCoreValue is the ungrouped row's float label.
func shardCoreValue(t *testing.T, resp *types.Response, label string) float64 {
	t.Helper()
	if len(resp.Data) != 1 {
		t.Fatalf("want one ungrouped row, got %v", resp.Data)
	}
	v, ok := resp.Data[0][label].(float64)
	if !ok {
		t.Fatalf("row %v has no float %q", resp.Data[0], label)
	}
	return v
}

// TestShardCoreInvariance_Scalar: each order-free scalar aggregator —
// integer counts, sentinel scans, the binned reproducible sum, AVERAGE
// over an exact n — plus a FILTER_RANGE count + sum, on the shard
// reducer ≡ the single file, and the hand-computed value on the
// reference. This is the sharding contract's parallel-reduction gate
// (§5.3; formerly TestShardArchiveProcessParallel).
func TestShardCoreInvariance_Scalar(t *testing.T) {
	t.Parallel()

	cohorts := shardCoreCohorts(t)
	for _, c := range []struct {
		label string
		agg   types.AggregationType
		want  float64
	}{
		{"count", types.AGG_COUNT, 16},
		{"sum", types.AGG_SUM, 1360},
		{"min", types.AGG_MIN, 10},
		{"max", types.AGG_MAX, 160},
		{"null_count", types.AGG_NULL_COUNT, 0},
		{"mode_count", types.AGG_MODE_COUNT, 1},
		{"average", types.AGG_AVERAGE, 85},
	} {
		mk := func() *types.Request {
			return &types.Request{Aggregations: []*types.Aggregation{{Type: c.agg, Field: "score", Label: "v"}}}
		}
		invariance.Run(t, shardCoreFamily(c.label, cohorts, []invariance.Group{invariance.ArchiveGroup()}, mk, func(t *testing.T, resp *types.Response) {
			if got := shardCoreValue(t, resp, "v"); got != c.want {
				t.Fatalf("%s = %v, want %v", c.label, got, c.want)
			}
			if resp.Metadata == nil || resp.Metadata.TotalRows != 16 {
				t.Fatalf("metadata %+v, want 16 total rows", resp.Metadata)
			}
		}))
	}
	filtered := func() *types.Request {
		return &types.Request{
			Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "score", Values: []string{"40", "120"}}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				{Type: types.AGG_SUM, Field: "score", Label: "s"},
			},
		}
	}
	invariance.Run(t, shardCoreFamily("filtered", cohorts, []invariance.Group{invariance.ArchiveGroup()}, filtered, func(t *testing.T, resp *types.Response) {
		// 40..120 step 10: nine rows summing 720.
		if n, s := shardCoreValue(t, resp, "n"), shardCoreValue(t, resp, "s"); n != 9 || s != 720 {
			t.Fatalf("filtered n=%v s=%v, want 9 / 720", n, s)
		}
		if resp.Metadata == nil || resp.Metadata.FilteredRows != 9 {
			t.Fatalf("metadata %+v, want 9 filtered rows", resp.Metadata)
		}
	}))
}

// TestShardCoreInvariance_TwoPassZScore: ATTR_ZSCORE is two-pass and
// not mergeable, so the merge gate keeps the archive serial under every
// knob (the two-pass streaming arm); the per-row z values and their binned sums still equal the
// single file's bit for bit.
func TestShardCoreInvariance_TwoPassZScore(t *testing.T) {
	t.Parallel()

	mk := func() *types.Request {
		return &types.Request{
			Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "score", Label: "z"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "z", Label: "sum_z"},
				{Type: types.AGG_AVERAGE, Field: "z", Label: "avg_z"},
			},
		}
	}
	groups := []invariance.Group{svcInvSerialArchiveGroup(observe.ArmStreaming)}
	invariance.Run(t, shardCoreFamily("zscore", shardCoreCohorts(t), groups, mk, func(t *testing.T, resp *types.Response) {
		if s := shardCoreValue(t, resp, "sum_z"); math.Abs(s) > 1e-9 {
			t.Fatalf("sum_z = %v, want ~0", s)
		}
	}))
}

// TestShardCoreInvariance_Grouped: GROUP_CATEGORY + AGG_SUM over four
// two-row shards — the merge unions per-key buckets across shards.
func TestShardCoreInvariance_Grouped(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
	}}
	var concat [][]uint64
	for i := 0; i < 8; i++ {
		concat = append(concat, []uint64{uint64(1 + i%2), math.Float64bits(float64(10 + 5*i))})
	}
	cfg := shardCoreArchive(t, schema, concat, 2)
	cohorts := map[invariance.Layout]svcInvCohort{invariance.File: {cfg, "concat.pulse"}, invariance.Archive: {cfg, "arch.pulse"}}
	mk := func() *types.Request {
		return &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "v"}},
		}
	}
	invariance.Run(t, shardCoreFamily("grouped", cohorts, []invariance.Group{invariance.ArchiveGroup()}, mk, func(t *testing.T, resp *types.Response) {
		got := map[string]float64{}
		for _, row := range resp.Data {
			k, _ := row["region"].(string)
			got[k], _ = row["v"].(float64)
		}
		// region 1: 10+20+30+40; region 2: 15+25+35+45.
		if len(got) != 2 || got["1"] != 100 || got["2"] != 120 {
			t.Fatalf("grouped sums %v, want 1→100, 2→120", got)
		}
	}))
}

// shardCoreArchive writes concat as concat.pulse and as arch.pulse,
// an archive of consecutive shards of per rows each.
func shardCoreArchive(t *testing.T, schema *encoding.Schema, concat [][]uint64, per int) *fs.Config {
	t.Helper()
	var shards []struct {
		Name    string
		Records [][]uint64
	}
	for i := 0; i < len(concat); i += per {
		shards = append(shards, struct {
			Name    string
			Records [][]uint64
		}{fmt.Sprintf("s%d.pulse", i/per), concat[i:min(i+per, len(concat))]})
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", buildShardArchive(t, schema, shards), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(cfg.Fs(), "concat.pulse", writeSinglePulse(t, schema, concat), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestShardCoreInvariance_RowLocalReadAttributes (#391): ATTR_CODE_IN,
// ATTR_SET_HAS and ATTR_SET_POPCOUNT are pure per-row reads, so the
// merge gate admits them and a sharded run fans out to the twin's
// answer.
func TestShardCoreInvariance_RowLocalReadAttributes(t *testing.T) {
	t.Parallel()

	dict := func(entries ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, e := range entries {
			if _, err := d.Add(e); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("N", "S", "E", "W"), ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict("a", "b", "c", "d"), ByteOffset: 1, CsvColumnIdx: 1},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
	}}
	var concat [][]uint64
	for r := 0; r < 24; r++ {
		concat = append(concat, []uint64{uint64(r % 4), uint64((r*5 + 3) % 16), math.Float64bits(float64(r) + 0.5)})
	}
	cfg := shardCoreArchive(t, schema, concat, 6)
	cohorts := map[invariance.Layout]svcInvCohort{invariance.File: {cfg, "concat.pulse"}, invariance.Archive: {cfg, "arch.pulse"}}
	mk := func() *types.Request {
		return &types.Request{
			Attributes: []*types.Attribute{
				{Type: types.ATTR_CODE_IN, Field: "region", Label: "in", Params: []byte(`{"codes":["N","E"]}`)},
				{Type: types.ATTR_SET_HAS, Field: "tags", Label: "has_b", Params: []byte(`{"label":"b"}`)},
				{Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "pc"},
			},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "in", Label: "in_n"},
				{Type: types.AGG_SUM, Field: "has_b", Label: "b_n"},
				{Type: types.AGG_SUM, Field: "pc", Label: "pc_sum"},
				{Type: types.AGG_SUM, Field: "score", Label: "score_sum"},
			},
		}
	}
	invariance.Run(t, shardCoreFamily("row_local_read", cohorts, []invariance.Group{invariance.ArchiveGroup()}, mk, func(t *testing.T, resp *types.Response) {
		var in float64
		for _, row := range resp.Data {
			v, _ := row["in_n"].(float64)
			in += v
		}
		if len(resp.Data) != 4 || in != 12 {
			t.Fatalf("degenerate fixture: %v", resp.Data)
		}
	}))
}

// TestShardCoreInvariance_GrouperKeySort: Request.Sort on a GROUP_RANGE
// key orders the buckets by their numeric low edge ("9-12" before
// "12-15", never byte-wise), ascending and descending, and the shard
// reducer's shared FinalizeGroupedStream tail answers in exactly the
// serial archive's order. The shards' parents overlap and their region
// dictionaries differ in order, so there is no single-file twin: the
// reference is the archive read serially.
func TestShardCoreInvariance_GrouperKeySort(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{}
	var names []string
	for i, g := range parityShards() {
		flat, _, _ := g.build(t, nil)
		n := fmt.Sprintf("f/s%d.pulse", i)
		files[n] = flat
		names = append(names, n)
	}
	svc, _ := gEnv(t, files)
	if _, err := svc.CreateShardArchive(context.Background(), "flat.pulse", names); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	cohorts := map[invariance.Layout]svcInvCohort{invariance.Archive: {svc.fs, "flat.pulse"}}
	group := invariance.ArchiveGroup()
	group.Ref = invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.Archive, DecodeWorkers: 1, ShardWorkers: 1}}
	group.Cells = group.Cells[1:] // the archive's serial read is the reference
	lowEdge := func(t *testing.T, key string) float64 {
		// "lo-hi"; lo may itself be negative ("-3-0").
		i := strings.Index(key[1:], "-") + 1
		v, err := strconv.ParseFloat(key[:i], 64)
		if err != nil {
			t.Fatalf("bucket key %q: %v", key, err)
		}
		return v
	}
	for _, desc := range []bool{false, true} {
		mk := func() *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "weight", Interval: 3}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
				Sort:         []types.OrderKey{{Field: "weight", Desc: desc}},
			}
		}
		invariance.Run(t, shardCoreFamily(fmt.Sprintf("desc=%v", desc), cohorts, []invariance.Group{group}, mk, func(t *testing.T, resp *types.Response) {
			keys := make([]string, len(resp.Data))
			for i, row := range resp.Data {
				keys[i], _ = row["weight"].(string)
			}
			if len(keys) < 3 {
				t.Fatalf("%d buckets, want several", len(keys))
			}
			byteWise := false
			for i := 1; i < len(keys); i++ {
				a, b := lowEdge(t, keys[i-1]), lowEdge(t, keys[i])
				if (!desc && a >= b) || (desc && a <= b) {
					t.Errorf("%q then %q is not by low edge: %v", keys[i-1], keys[i], keys)
				}
				if (keys[i-1] < keys[i]) != (a < b) {
					byteWise = true
				}
			}
			if !byteWise {
				t.Fatalf("fixture keys %v sort the same byte-wise; the family would pass vacuously", keys)
			}
		}))
	}
}
