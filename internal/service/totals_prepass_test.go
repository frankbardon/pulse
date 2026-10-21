package service

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
)

func prepassSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU8, ByteOffset: 0},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 1},
		{Name: "fw", Type: encoding.FieldTypeU8, ByteOffset: 9},
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 10},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 18},
		{Name: "pw", Type: encoding.FieldTypeF64, ByteOffset: 26},
		{Name: "z", Type: encoding.FieldTypeU8, ByteOffset: 34}, // read by nothing
	}}
}

func prepassRecords(from, to int) [][]uint64 {
	out := make([][]uint64, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, []uint64{
			uint64(i % 7), math.Float64bits(float64(i%113) + 0.25), uint64(1 + i%3),
			math.Float64bits(float64(i%5) + 0.5), math.Float64bits(float64(i%11) + 0.125), math.Float64bits(0.5 + float64(i%4)), uint64(i % 9),
		})
	}
	return out
}

// prepassRequest: a filter on k, a row-local x2 = x * 2, and a total()
// formula whose ROW expression also reads a; the aggregations read sh
// and b. Weighted, the total() slot carries a frequency weight fw and
// the b sum a probability weight pw.
func prepassRequest(path string, weighted bool) *types.Request {
	sh := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "a + x2 / total(x2)", Label: "sh"}
	bsum := &types.Aggregation{Type: types.AGG_SUM, Field: "b", Label: "sb"}
	if weighted {
		sh.Weight = types.SlotWeightOf(types.WeightSpec{Field: "fw", Kind: types.WeightKindFrequency})
		bsum.Weight = types.SlotWeightOf(types.WeightSpec{Field: "pw", Kind: types.WeightKindProbability})
	}
	return &types.Request{Cohort: &types.Cohort{Filename: path},
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "k > 1"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Expression: "x * 2", Label: "x2"},
			sh,
		},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sh", Label: "ssh"}, bsum}}
}

func retainedNames(schema *encoding.Schema, keep encx.FieldFilter) []string {
	var out []string
	for _, f := range schema.Fields {
		if keep == nil || keep(f.Name) {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestTotalsPrepass_ProjectionNarrowsToTotalInputs (U51 E3-S1): on both
// parallel arms the phase-1 pre-pass decodes ONLY the filter fields,
// the fields its total() arguments read (through the row-local x2) and
// the total() slot's own weight — not the fields only the formula's
// row expression (a) or the aggregations (b, pw) read — strictly fewer
// than the reduce's own projection of the same request.
func TestTotalsPrepass_ProjectionNarrowsToTotalInputs(t *testing.T) {
	ctx := context.Background()
	schema := prepassSchema()

	mem := afero.NewMemMapFs()
	archive := buildShardArchive(t, schema, []struct {
		Name    string
		Records [][]uint64
	}{{"s0.pulse", prepassRecords(0, 40)}, {"s1.pulse", prepassRecords(40, 90)}, {"s2.pulse", prepassRecords(90, 120)}})
	if err := afero.WriteFile(mem, "arch.pulse", archive, 0o644); err != nil {
		t.Fatal(err)
	}
	memCfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	if err := afero.WriteFile(osFs, filepath.Join(dir, "big.pulse"),
		writeSinglePulse(t, schema, prepassRecords(0, parallelDecodeRecordThreshold+4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	osCfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}

	arms := []struct {
		name string
		svc  func() *Service
		path string
	}{
		{"shard_parallel", func() *Service { s := New(memCfg); s.SetShardWorkers(3); s.SetProjectBufferedFields(true); return s }, "arch.pulse"},
		{"parallel_decode", func() *Service { s := New(osCfg); s.SetDecodeWorkers(4); s.SetProjectBufferedFields(true); return s }, filepath.Join(dir, "big.pulse")},
	}
	defer func() { prepassProjectionHook = nil }()
	for _, arm := range arms {
		for _, weighted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/weighted=%v", arm.name, weighted), func(t *testing.T) {
				var calls []struct {
					keep      encx.FieldFilter
					projected int
				}
				prepassProjectionHook = func(keep encx.FieldFilter, projected int) {
					calls = append(calls, struct {
						keep      encx.FieldFilter
						projected int
					}{keep, projected})
				}
				svc := arm.svc()
				if _, err := svc.Process(ctx, prepassRequest(arm.path, weighted)); err != nil {
					t.Fatalf("Process: %v", err)
				}
				if len(calls) != 1 {
					t.Fatalf("pre-pass ran %d times, want 1 (the run did not take the two-phase arm)", len(calls))
				}
				want := []string{"k", "x"}
				full := []string{"a", "b", "k", "x"}
				if weighted {
					want = []string{"fw", "k", "x"}
					full = []string{"a", "b", "fw", "k", "pw", "x"}
				}
				got := retainedNames(schema, calls[0].keep)
				if fmt.Sprint(got) != fmt.Sprint(want) || calls[0].projected != len(want) {
					t.Fatalf("pre-pass retains %v (projected_fields %d), want %v", got, calls[0].projected, want)
				}
				fullKeep, _, fullN := svc.decodeProjection(ctx, prepassRequest(arm.path, weighted), schema)
				if gotFull := retainedNames(schema, fullKeep); fmt.Sprint(gotFull) != fmt.Sprint(full) || fullN != len(full) {
					t.Fatalf("reduce projection %v (%d), want %v", gotFull, fullN, full)
				}
				if calls[0].projected >= fullN {
					t.Fatalf("pre-pass projected_fields %d not below the reduce's %d", calls[0].projected, fullN)
				}
			})
		}
	}
}
