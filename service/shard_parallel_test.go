package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// fourShardScoreCohort builds a 16-row score cohort across four
// shards. Used by the parallel-vs-serial parity tests so we exercise
// the multi-worker merge path with non-trivial cardinality.
func fourShardScoreCohort(t *testing.T) (*fs.Config, *encoding.Schema, [][]uint64) {
	t.Helper()
	schema := twoShardScoreSchema()
	concat := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
		{4, math.Float64bits(40.0)},
		{5, math.Float64bits(50.0)},
		{6, math.Float64bits(60.0)},
		{7, math.Float64bits(70.0)},
		{8, math.Float64bits(80.0)},
		{9, math.Float64bits(90.0)},
		{10, math.Float64bits(100.0)},
		{11, math.Float64bits(110.0)},
		{12, math.Float64bits(120.0)},
		{13, math.Float64bits(130.0)},
		{14, math.Float64bits(140.0)},
		{15, math.Float64bits(150.0)},
		{16, math.Float64bits(160.0)},
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: concat[0:4]},
		{Name: "b.pulse", Records: concat[4:8]},
		{Name: "c.pulse", Records: concat[8:12]},
		{Name: "d.pulse", Records: concat[12:16]},
	}
	cfg := fs.NewMemMap()
	archive := buildShardArchive(t, schema, shards)
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", archive, 0644); err != nil {
		t.Fatalf("WriteFile archive: %v", err)
	}
	single := writeSinglePulse(t, schema, concat)
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse.concat", single, 0644); err != nil {
		t.Fatalf("WriteFile concat: %v", err)
	}
	return cfg, schema, concat
}

// TestShardArchiveProcessParallel is the required non-skippable CI
// gate per §5.3 of the sharding design contract. Verifies parallel
// reduction across 4 shards with 2/4 workers produces:
//
//   - byte-equal results vs serial for COUNT/SUM/MIN/MAX/NULL_COUNT/
//     FREQUENCY (associative+commutative aggregators).
//   - within-tolerance (~1 ULP) for AGG_AVERAGE (Welford parallel
//     merge introduces float drift on the order of float64 epsilon).
func TestShardArchiveProcessParallel(t *testing.T) {
	cfg, _, _ := fourShardScoreCohort(t)

	cases := []struct {
		label string
		agg   *types.Aggregation
		want  float64
		ulp   bool
	}{
		{"count", &types.Aggregation{Type: types.AGG_COUNT, Field: "score", Label: "v"}, 16, false},
		{"sum", &types.Aggregation{Type: types.AGG_SUM, Field: "score", Label: "v"}, 1360, false},
		{"min", &types.Aggregation{Type: types.AGG_MIN, Field: "score", Label: "v"}, 10, false},
		{"max", &types.Aggregation{Type: types.AGG_MAX, Field: "score", Label: "v"}, 160, false},
		{"null_count", &types.Aggregation{Type: types.AGG_NULL_COUNT, Field: "score", Label: "v"}, 0, false},
		{"frequency", &types.Aggregation{Type: types.AGG_FREQUENCY, Field: "score", Label: "v"}, 1, false},
		{"average", &types.Aggregation{Type: types.AGG_AVERAGE, Field: "score", Label: "v"}, 85, true},
	}

	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: "arch.pulse"},
				Aggregations: []*types.Aggregation{c.agg},
			}

			// Serial baseline: ShardWorkers = 1.
			svcSerial := New(cfg)
			svcSerial.SetShardWorkers(1)
			respSerial, err := svcSerial.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process serial: %v", err)
			}

			// Parallel: 2 workers.
			svc2 := New(cfg)
			svc2.SetShardWorkers(2)
			resp2, err := svc2.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process workers=2: %v", err)
			}

			// Parallel: 4 workers (one per shard).
			svc4 := New(cfg)
			svc4.SetShardWorkers(4)
			resp4, err := svc4.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process workers=4: %v", err)
			}

			s, _ := respSerial.Data[0]["v"].(float64)
			p2, _ := resp2.Data[0]["v"].(float64)
			p4, _ := resp4.Data[0]["v"].(float64)

			if c.ulp {
				if math.Abs(s-c.want) > 1e-9 {
					t.Errorf("serial=%v expected=%v", s, c.want)
				}
				if math.Abs(p2-s) > 1e-9 {
					t.Errorf("parallel(2) drift > 1 ULP: serial=%v parallel=%v", s, p2)
				}
				if math.Abs(p4-s) > 1e-9 {
					t.Errorf("parallel(4) drift > 1 ULP: serial=%v parallel=%v", s, p4)
				}
			} else {
				if s != c.want {
					t.Errorf("serial=%v expected=%v", s, c.want)
				}
				if p2 != s {
					t.Errorf("parallel(2) NOT byte-equal: serial=%v parallel=%v", s, p2)
				}
				if p4 != s {
					t.Errorf("parallel(4) NOT byte-equal: serial=%v parallel=%v", s, p4)
				}
			}
			// Metadata.TotalRows must aggregate identically across paths.
			if respSerial.Metadata.TotalRows != 16 {
				t.Errorf("serial total_rows=%d want 16", respSerial.Metadata.TotalRows)
			}
			if resp2.Metadata.TotalRows != 16 || resp4.Metadata.TotalRows != 16 {
				t.Errorf("parallel total_rows=%d/%d want 16",
					resp2.Metadata.TotalRows, resp4.Metadata.TotalRows)
			}
		})
	}
}

// TestShardArchiveProcessParallel_NonMergeableFallsThrough confirms
// that requests containing a non-mergeable operator (here AGG_MEDIAN,
// a buffered aggregator) bypass the parallel reducer entirely — the
// serial shardIter path runs, and the result matches the concatenated
// single-file cohort exactly. Regression guard for the
// shouldFanOut() gate.
func TestShardArchiveProcessParallel_NonMergeableFallsThrough(t *testing.T) {
	cfg, _, _ := fourShardScoreCohort(t)
	svc := New(cfg)
	svc.SetShardWorkers(4)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_MEDIAN, Field: "score", Label: "v"},
			},
		}
	}
	respA, err := svc.Process(context.Background(), req("arch.pulse"))
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	respB, err := svc.Process(context.Background(), req("arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process concat: %v", err)
	}
	a, _ := respA.Data[0]["v"].(float64)
	b, _ := respB.Data[0]["v"].(float64)
	if a != b {
		t.Errorf("AGG_MEDIAN parallel-fallthrough: archive=%v concat=%v (must match)", a, b)
	}
	// 16 rows (10..160 step 10): median = (80+90)/2 = 85.
	if a != 85 {
		t.Errorf("AGG_MEDIAN = %v, want 85", a)
	}
}

// TestShardArchiveProcessParallel_SerialEqualsOne confirms that
// SetShardWorkers(1) takes the same code path as the pre-S6 serial
// shardIter — the byte-for-byte parity guarantee documented in the
// design contract.
func TestShardArchiveProcessParallel_SerialEqualsOne(t *testing.T) {
	cfg, _, _ := fourShardScoreCohort(t)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	svc1 := New(cfg)
	svc1.SetShardWorkers(1)
	resp1, err := svc1.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process workers=1: %v", err)
	}
	// Default (0 -> NumCPU) — should match exactly on byte-equal aggs.
	svcDefault := New(cfg)
	respDefault, err := svcDefault.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process default: %v", err)
	}
	if resp1.Data[0]["s"] != respDefault.Data[0]["s"] {
		t.Errorf("SUM serial=%v default=%v", resp1.Data[0]["s"], respDefault.Data[0]["s"])
	}
	if resp1.Data[0]["n"] != respDefault.Data[0]["n"] {
		t.Errorf("COUNT serial=%v default=%v", resp1.Data[0]["n"], respDefault.Data[0]["n"])
	}
}

// TestShardArchiveProcessParallel_TwoPassZScore confirms a request
// with ATTR_ZSCORE (two-pass, non-mergeable in v1) falls through to
// the serial path even when ShardWorkers > 1, and produces the same
// result as the concatenated single-file cohort. This is the parity
// guarantee for two-pass attribute composability with sharding.
func TestShardArchiveProcessParallel_TwoPassZScore(t *testing.T) {
	cfg, _, _ := fourShardScoreCohort(t)
	svc := New(cfg)
	svc.SetShardWorkers(4)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_ZSCORE, Field: "score", Label: "z"},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "z", Label: "sum_z"},
				{Type: types.AGG_AVERAGE, Field: "z", Label: "avg_z"},
			},
		}
	}
	respA, err := svc.Process(context.Background(), req("arch.pulse"))
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	respB, err := svc.Process(context.Background(), req("arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process concat: %v", err)
	}
	sA, _ := respA.Data[0]["sum_z"].(float64)
	sB, _ := respB.Data[0]["sum_z"].(float64)
	aA, _ := respA.Data[0]["avg_z"].(float64)
	aB, _ := respB.Data[0]["avg_z"].(float64)
	if math.Abs(sA) > 1e-9 {
		t.Errorf("sum_z (archive) = %v, want ~0", sA)
	}
	if math.Abs(sA-sB) > 1e-9 {
		t.Errorf("sum_z: archive=%v concat=%v", sA, sB)
	}
	if math.Abs(aA-aB) > 1e-9 {
		t.Errorf("avg_z: archive=%v concat=%v", aA, aB)
	}
}

// TestShardArchiveProcessParallel_Grouped confirms per-shard parallel
// reduction across GROUP_CATEGORY + AGG_SUM produces byte-equal results
// vs the serial path. Group merge unions per-key buckets across shards;
// associative+commutative sum within each bucket must be order-stable.
func TestShardArchiveProcessParallel_Grouped(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	concat := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(15.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(25.0)},
		{1, math.Float64bits(30.0)},
		{2, math.Float64bits(35.0)},
		{1, math.Float64bits(40.0)},
		{2, math.Float64bits(45.0)},
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: concat[0:2]},
		{Name: "b.pulse", Records: concat[2:4]},
		{Name: "c.pulse", Records: concat[4:6]},
		{Name: "d.pulse", Records: concat[6:8]},
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", buildShardArchive(t, schema, shards), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "v"},
		},
	}
	svcSerial := New(cfg)
	svcSerial.SetShardWorkers(1)
	respSerial, err := svcSerial.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process serial: %v", err)
	}
	svcPar := New(cfg)
	svcPar.SetShardWorkers(4)
	respPar, err := svcPar.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process parallel: %v", err)
	}

	asMap := func(rows []map[string]any) map[string]float64 {
		out := make(map[string]float64, len(rows))
		for _, row := range rows {
			k, _ := row["region"].(string)
			v, _ := row["v"].(float64)
			out[k] = v
		}
		return out
	}
	mS := asMap(respSerial.Data)
	mP := asMap(respPar.Data)
	if len(mS) != len(mP) {
		t.Fatalf("group count: serial=%d parallel=%d", len(mS), len(mP))
	}
	for k, v := range mS {
		if mP[k] != v {
			t.Errorf("region %v: serial=%v parallel=%v", k, v, mP[k])
		}
	}
	// region=1 -> 10+20+30+40 = 100; region=2 -> 15+25+35+45 = 120.
	if mS["1"] != 100 || mS["2"] != 120 {
		t.Errorf("serial result wrong: %v", mS)
	}
}

// TestShardArchiveProcessParallel_FilteredCount confirms filter
// predicates run identically across the parallel reducer's per-shard
// worker fan-out. Filtered + sum counts in the worker pool must match
// the serial baseline byte-equal.
func TestShardArchiveProcessParallel_FilteredCount(t *testing.T) {
	cfg, _, _ := fourShardScoreCohort(t)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"40", "120"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
		},
	}
	svcSerial := New(cfg)
	svcSerial.SetShardWorkers(1)
	respSerial, err := svcSerial.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process serial: %v", err)
	}
	svcPar := New(cfg)
	svcPar.SetShardWorkers(4)
	respPar, err := svcPar.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process parallel: %v", err)
	}
	if respSerial.Data[0]["n"] != respPar.Data[0]["n"] {
		t.Errorf("filtered count: serial=%v parallel=%v",
			respSerial.Data[0]["n"], respPar.Data[0]["n"])
	}
	if respSerial.Data[0]["s"] != respPar.Data[0]["s"] {
		t.Errorf("filtered sum: serial=%v parallel=%v",
			respSerial.Data[0]["s"], respPar.Data[0]["s"])
	}
	if respSerial.Metadata.FilteredRows != respPar.Metadata.FilteredRows {
		t.Errorf("filtered_rows: serial=%d parallel=%d",
			respSerial.Metadata.FilteredRows, respPar.Metadata.FilteredRows)
	}
}
