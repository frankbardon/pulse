package service

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// decodeFanOutSchema: k (u8, a GROUP_RANGE key) and x (f64).
func decodeFanOutSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU8, ByteOffset: 0},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 1},
	}}
}

func decodeFanOutFile(t *testing.T, rows int) []byte {
	t.Helper()
	recs := make([][]uint64, rows)
	for i := range recs {
		recs[i] = []uint64{uint64(i % 7), math.Float64bits(float64(i%113) + 0.25)}
	}
	return writeSinglePulse(t, decodeFanOutSchema(), recs)
}

// TestDecodeFanOut_PredictMatchesRuntime (U51 E2-S2, FR-39): predict's
// DecodeFanOut answers exactly the runtime's parallel-decode dispatch
// (canParallelDecode + shouldFanOutDecode, the gate
// processSingleFileParallelMaybe applies) for every DecodeWorkers value,
// over a mappable single file past the threshold, the same bytes on an
// in-memory fs (no mmap), a file under the threshold and a shard
// archive, for a mergeable sum, a grouped sum, a non-mergeable median
// and a crosstab — the way TestShardFanOut_PredictMatchesRuntime pins
// ShardWorkers. The runtime side is the run's own limit inputs
// (limitInputs), so the pre-flight and the run agree too.
//
// Falsified by dropping the Mappable condition from decodeWorkers
// (in-memory cases then predict a fan-out the run never takes).
func TestDecodeFanOut_PredictMatchesRuntime(t *testing.T) {
	ctx := context.Background()
	big := decodeFanOutFile(t, parallelDecodeRecordThreshold+4096)
	small := decodeFanOutFile(t, 1000)

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	for name, b := range map[string][]byte{"big.pulse": big, "small.pulse": small} {
		if err := afero.WriteFile(osFs, filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	osCfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	memCfg := fs.NewMemMap()
	if err := afero.WriteFile(memCfg.Fs(), "big.pulse", big, 0o644); err != nil {
		t.Fatal(err)
	}
	shardSchema := decodeFanOutSchema()
	halfRecs := make([][]uint64, 10)
	for i := range halfRecs {
		halfRecs[i] = []uint64{uint64(i % 7), math.Float64bits(float64(i))}
	}
	archive := buildShardArchive(t, shardSchema, []struct {
		Name    string
		Records [][]uint64
	}{
		{"s0.pulse", halfRecs},
		{"s1.pulse", halfRecs},
	})
	if err := afero.WriteFile(osFs, filepath.Join(dir, "arch.pulse"), archive, 0o644); err != nil {
		t.Fatal(err)
	}

	reqs := map[string]func(string) *types.Request{
		"sum": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}}
		},
		"grouped": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p},
				Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "k", Interval: 2}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}}
		},
		"median (not mergeable)": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p}, Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x", Label: "m"}}}
		},
		// U51 E3-S1: an ungrouped one-layer total() fans out (two-phase);
		// a grouped one stays serial.
		"total": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x / total(x)", Label: "sh"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sh", Label: "s"}}}
		},
		"grouped total": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p},
				Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "k", Interval: 2}},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x / total(x)", Label: "sh"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sh", Label: "s"}}}
		},
		"crosstab": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p}, Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "k", Interval: 2}},
				Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "k", Interval: 3}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "x"},
			}}
		},
	}
	cases := []struct {
		name string
		cfg  *fs.Config
		path string
	}{
		{"mappable", osCfg, filepath.Join(dir, "big.pulse")},
		{"in_memory", memCfg, "big.pulse"},
		{"under_threshold", osCfg, filepath.Join(dir, "small.pulse")},
		{"archive", osCfg, filepath.Join(dir, "arch.pulse")},
	}
	fanned := 0
	for _, c := range cases {
		for name, mk := range reqs {
			for _, workers := range []int{0, 1, 2, 8} {
				t.Run(fmt.Sprintf("%s/%s/workers=%d", c.name, name, workers), func(t *testing.T) {
					svc := New(c.cfg)
					svc.SetDecodeWorkers(workers)
					l := limits.Defaults()
					l.MaxEstimatedMemory = 1 << 50
					svc.SetLimits(l)
					cohort, err := svc.Open(ctx, c.path)
					if err != nil {
						t.Fatal(err)
					}
					req := mk(c.path)
					count, err := svc.CountRecords(ctx, c.path)
					if err != nil {
						t.Fatal(err)
					}
					eligible, _ := svc.canParallelDecode(req, cohort.Schema(), cohort, workers, int(count))
					_, fans := shouldFanOutDecode(workers, int(count))
					// A crosstab never reaches processSingleFileParallelMaybe:
					// its segment decode materialises records (no partials).
					runtimeFans := eligible && fans && req.Crosstab == nil
					in, err := svc.limitInputs(ctx, cohort, c.path)
					if err != nil {
						t.Fatal(err)
					}
					got := descx.DecodeFanOut(req, cohort.Schema(), svc.instance, in)
					if got != runtimeFans {
						t.Errorf("predict fan-out %v, runtime %v", got, runtimeFans)
					}
					// Predict's own probe answers what the run's inputs say.
					if svc.DecodeMappable(ctx, c.path) != in.Mappable {
						t.Errorf("DecodeMappable %v, limitInputs.Mappable %v", !in.Mappable, in.Mappable)
					}
					if got {
						fanned++
					}
				})
			}
		}
	}
	if fanned == 0 {
		t.Fatal("no case fanned out — the parity check compared serial against serial")
	}
}
