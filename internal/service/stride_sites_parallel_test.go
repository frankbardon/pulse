package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Fixed-stride regression suite, parallel-decode arm. The buffered
// Process path splits a single-file cohort into segments at multiples of
// Schema.RecordByteSize — the PHYSICAL stride — and each worker's
// RecordReader expands its own segment. On a deduped (0x02) cohort a
// split by the logical stride, or a count by it, would land segment
// boundaries mid-row and every worker after the first would decode
// garbage (or silently drop rows). Like TestWideSet_DecodeWorkersParity
// the fixture sits above parallelDecodeRecordThreshold on the real OS
// filesystem, and the gate is asserted open before anything is
// compared, because the eligibility predicate bails silently.

const strideParallelRows = parallelDecodeRecordThreshold + 3001 // not a multiple of any worker count

// strideParallelSchema: a child row (id, amount) under a parent block
// (parent, region, weight — weight nullable, null for every fifth
// parent) plus a global constant (src).
func strideParallelSchema() *encoding.Schema {
	region := encoding.NewDictionary()
	for _, v := range []string{"north", "south", "east", "west"} {
		_, _ = region.Add(v)
	}
	src := encoding.NewDictionary()
	_, _ = src.Add("batch-a")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "parent", Type: encoding.FieldTypeU32, ByteOffset: 4, CsvColumnIdx: 1},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, CsvColumnIdx: 2, Dictionary: region},
		{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 9, CsvColumnIdx: 3, Nullable: true},
		{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 17, CsvColumnIdx: 4},
		{Name: "src", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 25, CsvColumnIdx: 5, Dictionary: src},
	}}
}

func strideParallelCohorts(t *testing.T) map[string][]byte {
	t.Helper()
	schema := strideParallelSchema()
	recs := make([][]uint64, strideParallelRows)
	for r := range recs {
		p := r / 9
		recs[r] = []uint64{
			uint64(r + 1),
			uint64(5000 + p),
			uint64(p % 4),
			math.Float64bits(float64(10 + p%7)),
			math.Float64bits(float64(r%101) + 0.25),
			0,
		}
	}
	flat := writeNullablePulse(t, schema, recs, func(r, f int) bool { return f == 3 && (r/9)%5 == 0 })
	dedup := func(specs []encx.GroupSpec) []byte {
		var out bytes.Buffer
		if _, n, err := encx.DedupCohort(&out, bytes.NewReader(flat), specs); err != nil || n != strideParallelRows {
			t.Fatalf("DedupCohort: %d rows, %v", n, err)
		}
		return out.Bytes()
	}
	return map[string][]byte{
		"flat": flat,
		"grouped": dedup([]encx.GroupSpec{
			{Kind: encoding.GroupKindIndexed, Members: []string{"parent", "region", "weight"}, Key: []string{"parent"}},
			{Kind: encoding.GroupKindConstant, Members: []string{"src"}},
		}),
		"elided": dedup([]encx.GroupSpec{{Kind: encoding.GroupKindConstant, Members: []string{"src"}}}),
	}
}

// TestStrideSites_ParallelDecodeSplitsOnPhysicalRecords runs a mergeable
// request that groups and filters on parent-group MEMBERS and aggregates
// child fields, serially and with 2/4/7 decode workers, over the 0x01
// cohort and its grouped and constant-elided twins. Every run must equal
// the absolute answer computed from the generator.
func TestStrideSites_ParallelDecodeSplitsOnPhysicalRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping >100K-record parallel decode fixture in -short mode")
	}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}

	// The absolute answer, straight from the generator: per region, the
	// row count, sum(amount) and sum(weight) over non-null weights, for
	// src == batch-a (every row) and weight >= 12 (null weights fail).
	type agg struct {
		n            int
		amount, wsum float64
	}
	want := map[string]*agg{}
	for r := 0; r < strideParallelRows; r++ {
		p := r / 9
		w := float64(10 + p%7)
		if p%5 == 0 || w < 12 {
			continue
		}
		reg := []string{"north", "south", "east", "west"}[p%4]
		if want[reg] == nil {
			want[reg] = &agg{}
		}
		want[reg].n++
		want[reg].amount += float64(r%101) + 0.25
		want[reg].wsum += w
	}

	req := func(path string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "src", Values: []string{"batch-a"}},
				{Type: types.FILTER_RANGE, Field: "weight", Values: []string{"12", "100"}},
			},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "id", Label: "n"},
				{Type: types.AGG_SUM, Field: "amount", Label: "amount"},
				{Type: types.AGG_SUM, Field: "weight", Label: "wsum"},
			},
		}
	}

	for name, data := range strideParallelCohorts(t) {
		t.Run(name, func(t *testing.T) {
			path := dir + "/" + name + ".pulse"
			if err := afero.WriteFile(osFs, path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			svc := New(cfg)
			cohort, err := svc.Open(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			schema := cohort.Schema()
			if schema.HasGroups() != (name != "flat") {
				t.Fatalf("fixture %s: HasGroups = %v", name, schema.HasGroups())
			}
			if !processing.CanMergeRequest(req(path), schema) {
				t.Fatal("request is not mergeable; the parallel path would never engage")
			}
			if ok, why := svc.canParallelDecode(req(path), schema, cohort, 4, strideParallelRows); !ok {
				t.Fatalf("parallel decode gate closed: %s", why)
			}
			check := func(label string, resp *types.Response) {
				t.Helper()
				got := map[string]*agg{}
				for _, row := range resp.Data {
					got[fmt.Sprint(row["region"])] = &agg{
						n:      int(toF(t, row["n"])),
						amount: toF(t, row["amount"]),
						wsum:   toF(t, row["wsum"]),
					}
				}
				if len(got) != len(want) {
					t.Fatalf("%s: %d region rows, want %d: %v", label, len(got), len(want), resp.Data)
				}
				for reg, w := range want {
					g := got[reg]
					if g == nil || g.n != w.n || math.Abs(g.amount-w.amount) > 1e-6*w.amount || g.wsum != w.wsum {
						t.Fatalf("%s region %s = %+v, want %+v", label, reg, g, w)
					}
				}
			}

			// The segmenting reducer itself, driven over the context the
			// dispatcher builds — full decode and the projected plan. This
			// is the arm that must split on PHYSICAL record boundaries;
			// Process alone cannot prove it ran, because a mis-derived
			// record count silently drops below the threshold and falls
			// back to serial.
			needed := processing.NeededFields(req(path), schema, nil)
			keep := encx.FieldFilter(func(name string) bool { return needed.Has(name) })
			projected, err := encx.BuildDecodePlan(schema, retainedFromFilter(schema, keep))
			if err != nil {
				t.Fatal(err)
			}
			for _, arm := range []struct {
				name string
				plan *encx.DecodePlan
				keep encx.FieldFilter
				hint int
			}{{"full", nil, nil, len(schema.Fields)}, {"projected", projected, keep, needed.Len()}} {
				pctx, cleanup, available, err := buildParallelDecodeContext(svc, path, schema, arm.plan, arm.keep, arm.hint)
				if err != nil {
					t.Fatal(err)
				}
				if !available {
					t.Fatal("parallel decode context unavailable — the comparison below would be serial vs serial")
				}
				if pctx.stride != schema.RecordByteSize() || pctx.totalRecords != strideParallelRows {
					_ = cleanup()
					t.Fatalf("parallel context stride/total = %d/%d, want physical stride %d and %d records",
						pctx.stride, pctx.totalRecords, schema.RecordByteSize(), strideParallelRows)
				}
				for _, workers := range []int{2, 4, 7} {
					resp, err := svc.reduceParallelBuffered(context.Background(), req(path), schema, pctx, workers)
					if err != nil {
						_ = cleanup()
						t.Fatalf("%s workers=%d: %v", arm.name, workers, err)
					}
					check(fmt.Sprintf("reduceParallelBuffered %s workers=%d", arm.name, workers), resp)
				}
				_ = cleanup()
			}

			// And end to end through Process, serial and parallel.
			for _, workers := range []int{1, 2, 4, 7} {
				s := New(cfg)
				s.SetDecodeWorkers(workers)
				resp, err := s.Process(context.Background(), req(path))
				if err != nil {
					t.Fatalf("workers=%d: %v", workers, err)
				}
				check(fmt.Sprintf("Process workers=%d", workers), resp)
			}
		})
	}
}

func toF(t *testing.T, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	t.Fatalf("unexpected aggregate type %T (%v)", v, v)
	return 0
}
