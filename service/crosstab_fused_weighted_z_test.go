package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Service-level companions to
// processing.TestFusedCrosstab_WeightedTwoMeansZMatchesBuffered: the
// weighted pairwise z overlay over an AGG_WEIGHTED_MEAN crosstab through
// the real dispatch in service/crosstab.go, with the concurrency knobs
// (ShardWorkers, DecodeWorkers) turned up.
//
// Float policy (shared with the processing-side matrix): every JSON
// number is compared with relative tolerance wzsRelTol; keys, strings,
// presence flags and shapes exactly. Crosstab never merges partial
// state — the shard archive is walked serially into one fold and the
// decode workers only materialise records in order — so these
// comparisons are expected to be bit-equal and the tolerance only
// absorbs per-call-site FMA contraction. The grouped Process test at the
// bottom is the one place where reduction order LEGITIMATELY differs
// (Chan-merged per-shard partials vs one serial fold), and there the
// tolerance is load-bearing.
const wzsRelTol = 1e-12

// Field index of the nullable weight column in wzsSchema.
const wzsWeightIdx = 4

func wzsSchema() *encoding.Schema {
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			_, _ = d.Add(v)
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "row", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict("r0", "r1")},
		{Name: "wave", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: dict("w1", "w2")},
		{Name: "aud", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 2, CsvColumnIdx: 2, Dictionary: dict("all", "owner")},
		{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 3, CsvColumnIdx: 3},
		{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 11, CsvColumnIdx: 4, Nullable: true},
	}}
}

// wzsRows generates n rows starting at global index offset, cycling
// through all eight (row, wave, aud) cells. Every 7th row has weight 0
// and every 11th a NULL weight; both carry value 1000, so folding either
// would visibly move the weighted moments.
func wzsRows(n, offset int) ([][]uint64, func(r, f int) bool) {
	recs := make([][]uint64, n)
	nulls := map[int]bool{}
	for i := range recs {
		g := offset + i
		cell := g % 8
		r, w, a := cell>>2&1, cell>>1&1, cell&1
		value := float64(10 + (r*7+w*3+a*5+g*11)%9 + 2*r + 3*w)
		weight := float64(1 + (g/8+r+a)%3)
		switch {
		case g%11 == 5:
			value = 1000
			nulls[i] = true
		case g%7 == 3:
			value, weight = 1000, 0
		}
		recs[i] = []uint64{uint64(r), uint64(w), uint64(a), math.Float64bits(value), math.Float64bits(weight)}
	}
	return recs, func(r, f int) bool { return f == wzsWeightIdx && nulls[r] }
}

// wzsArchive writes a store-only shard archive whose shards are
// consecutive slices of the same generator, plus the concatenation as a
// single file at path+".concat".
func wzsArchive(t *testing.T, cfg *fs.Config, path string, shardRows []int) {
	t.Helper()
	schema := wzsSchema()
	var total uint64
	var concat [][]uint64
	var concatNull []bool
	payloads := make([][]byte, len(shardRows))
	offset := 0
	for i, n := range shardRows {
		recs, nullAt := wzsRows(n, offset)
		payloads[i] = writeNullablePulse(t, schema, recs, nullAt)
		for ri := range recs {
			concat = append(concat, recs[ri])
			concatNull = append(concatNull, nullAt(ri, wzsWeightIdx))
		}
		total += uint64(n)
		offset += n
	}
	var doc bytes.Buffer
	if err := encoding.WriteSchemaDoc(&doc, schema, total, uint16(len(shardRows))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	write(encoding.ReservedSchemaName, doc.Bytes())
	for i := range payloads {
		write(fmt.Sprintf("s%d.pulse", i), payloads[i])
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile archive: %v", err)
	}
	single := writeNullablePulse(t, schema, concat, func(r, f int) bool { return f == wzsWeightIdx && concatNull[r] })
	if err := afero.WriteFile(cfg.Fs(), path+".concat", single, 0o644); err != nil {
		t.Fatalf("WriteFile concat: %v", err)
	}
}

func wzsOverlay(name string, scope types.OverlayScope, nBasis string, dim *int) types.OverlaySpec {
	params, _ := json.Marshal(types.PairwiseOverlayParams{NBasis: nBasis, PairAlongDim: dim})
	return types.OverlaySpec{Name: name, Kind: types.OverlayKindPairwiseWeightedTwoMeansZ, Scope: scope, Params: params}
}

// wzsCrosstabRequest carries every overlay shape at once — both scopes,
// both n_basis values, pair_along_dim — plus auxiliary margin
// aggregations, so one request per knob setting covers the matrix.
func wzsCrosstabRequest(path string) *types.Request {
	dim := 1
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "row"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "wave"},
				{Type: types.GROUP_CATEGORY, Field: "aud"},
			},
			Cell: &types.Aggregation{
				Type: types.AGG_WEIGHTED_MEAN, Field: "value", Label: "wmean",
				Params: json.RawMessage(`{"weight_field":"weight"}`),
			},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			MarginAggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "value", Label: "base"},
			},
		},
		Overlays: []types.OverlaySpec{
			wzsOverlay("row_weights", types.OverlayScopeRow, types.PairwiseNBasisWeights, nil),
			wzsOverlay("row_kish", types.OverlayScopeRow, types.PairwiseNBasisKish, nil),
			wzsOverlay("col_weights", types.OverlayScopeColumn, types.PairwiseNBasisWeights, nil),
			wzsOverlay("col_dim_kish", types.OverlayScopeColumn, types.PairwiseNBasisKish, &dim),
		},
	}
}

// TestCrosstabFused_WeightedTwoMeansZ_WorkerKnobsParity drives the
// weighted overlay crosstab through Service.Process over a shard archive
// and over the single-file concatenation of the same rows, on the fused
// arm and (SetDisableCrosstabFusion) the buffered arm, with
// ShardWorkers/DecodeWorkers forced serial (1) and turned up (4). Every
// combination must match the serial single-file fused reference.
func TestCrosstabFused_WeightedTwoMeansZ_WorkerKnobsParity(t *testing.T) {
	cfg := fs.NewMemMap()
	wzsArchive(t, cfg, "wz.pulse", []int{37, 29, 41})
	schema := wzsSchema()
	ctx := context.Background()

	run := func(t *testing.T, file string, fused bool, workers int) *types.Response {
		t.Helper()
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		svc.SetDecodeWorkers(workers)
		svc.SetDisableCrosstabFusion(!fused)
		req := wzsCrosstabRequest(file)
		if fused {
			if ok, reason := processing.CanFuseCrosstab(req, schema, svc.Extensions()); !ok {
				t.Fatalf("CanFuseCrosstab rejected the weighted overlay crosstab: %s", reason)
			}
		}
		resp, err := svc.Process(ctx, req)
		if err != nil {
			t.Fatalf("Process(%s, fused=%t, workers=%d): %v", file, fused, workers, err)
		}
		return resp
	}

	ref := run(t, "wz.pulse.concat", true, 1)
	wzsAssertNonVacuous(t, ref, len(wzsCrosstabRequest("").Overlays))

	for _, file := range []string{"wz.pulse", "wz.pulse.concat"} {
		for _, fused := range []bool{true, false} {
			for _, workers := range []int{1, 4} {
				t.Run(fmt.Sprintf("%s/fused_%t/workers_%d", file, fused, workers), func(t *testing.T) {
					got := run(t, file, fused, workers)
					for _, slot := range []struct {
						name      string
						want, got any
					}{
						{"Crosstab", ref.Crosstab, got.Crosstab},
						{"Components", ref.Components.Crosstab, got.Components.Crosstab},
						{"Overlays", ref.Overlays, got.Overlays},
						{"Warnings", ref.Warnings, got.Warnings},
					} {
						wzsAssertJSONNear(t, slot.name, slot.want, slot.got)
					}
				})
			}
		}
	}
}

// TestCrosstab_WeightedTwoMeansZ_DecodeWorkersParity covers the one
// crosstab arm DecodeWorkers really changes: buffered record
// materialisation above parallelDecodeRecordThreshold on a mmap-able
// file (MemMapFs bails to serial, so this needs a temp-dir OsFs).
// Parallel materialisation must reproduce the serial record stream, so
// overlay, Components and matrix match serial buffered and fused.
func TestCrosstab_WeightedTwoMeansZ_DecodeWorkersParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping decode-worker crosstab parity in -short mode")
	}
	const rowCount = parallelDecodeRecordThreshold + 4096
	schema := wzsSchema()
	recs, nullAt := wzsRows(rowCount, 0)

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/wz_decode.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	ctx := context.Background()

	run := func(t *testing.T, fused bool, workers int) *types.Response {
		t.Helper()
		svc := New(cfg)
		svc.SetDecodeWorkers(workers)
		svc.SetDisableCrosstabFusion(!fused)
		resp, err := svc.Process(ctx, wzsCrosstabRequest(path))
		if err != nil {
			t.Fatalf("Process(fused=%t, workers=%d): %v", fused, workers, err)
		}
		return resp
	}

	ref := run(t, false, 1)
	wzsAssertNonVacuous(t, ref, len(wzsCrosstabRequest("").Overlays))

	// Gate check: a silent bail would compare serial to serial.
	probe := New(cfg)
	cohort, err := probe.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, cleanup, available, err := buildParallelDecodeContext(probe, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields))
	if err != nil {
		t.Fatalf("buildParallelDecodeContext: %v", err)
	}
	if cleanup != nil {
		defer func() { _ = cleanup() }()
	}
	if !available {
		t.Fatal("parallel decode unavailable for an OsFs cohort above threshold")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}

	for _, tc := range []struct {
		name    string
		fused   bool
		workers int
	}{
		{"buffered_workers_4", false, 4},
		{"fused_workers_4", true, 4},
		{"fused_workers_1", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, tc.fused, tc.workers)
			wzsAssertJSONNear(t, "Crosstab", ref.Crosstab, got.Crosstab)
			wzsAssertJSONNear(t, "Components", ref.Components.Crosstab, got.Components.Crosstab)
			wzsAssertJSONNear(t, "Overlays", ref.Overlays, got.Overlays)
			wzsAssertJSONNear(t, "Warnings", ref.Warnings, got.Warnings)
		})
	}
}

// TestShardWorkers_WeightedMeanMomentsParity is the reduce-side
// counterpart: an ungrouped AGG_WEIGHTED_MEAN Process over a shard archive
// (ungrouped because that is the arm that surfaces per-slot
// Components.Aggregations)
// fans out across ShardWorkers and Chan-merges per-shard partials
// (MergeOnline), which is where the four weighted-moment keys the
// overlay reads could drift. Reduction order differs from the serial
// fold, so this is the comparison the tolerance exists for.
func TestShardWorkers_WeightedMeanMomentsParity(t *testing.T) {
	cfg := fs.NewMemMap()
	wzsArchive(t, cfg, "wzg.pulse", []int{37, 29, 41})
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "wzg.pulse"},
			Aggregations: []*types.Aggregation{{
				Type: types.AGG_WEIGHTED_MEAN, Field: "value", Label: "wmean",
				Params: json.RawMessage(`{"weight_field":"weight"}`),
			}},
		}
	}
	if !processing.CanMergeRequest(req(), wzsSchema()) {
		t.Fatal("fixture request is not mergeable; the shard reducer would never fan out")
	}

	serial := New(cfg)
	serial.SetShardWorkers(1)
	want, err := serial.Process(ctx, req())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if want.Components == nil || len(want.Components.Aggregations) == 0 {
		t.Fatal("serial run emitted no aggregation components")
	}
	wzsAssertMomentKeys(t, want.Components.Aggregations)

	for _, workers := range []int{2, 3} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			svc := New(cfg)
			svc.SetShardWorkers(workers)
			cohort, err := svc.Open(ctx, "wzg.pulse")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, ok := svc.shouldFanOut(req(), cohort); !ok {
				t.Fatalf("shouldFanOut refused a %d-worker fan-out; this would compare serial against serial", workers)
			}
			got, err := svc.Process(ctx, req())
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			wzsAssertJSONNear(t, "Data", want.Data, got.Data)
			wzsAssertJSONNear(t, "Components", want.Components, got.Components)
		})
	}
}

// wzsAssertMomentKeys demands the four weighted-moment keys on every
// aggregation components entry, with positive values, so the parity
// comparison cannot pass over two key-less maps.
func wzsAssertMomentKeys(t *testing.T, entries []types.AggregationComponents) {
	t.Helper()
	raw := wzsDecode(t, entries)
	found := 0
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["sum_weights_sq"]; ok {
				found++
				for _, k := range []string{"m2_weighted", "sum_weights_sq", "weighted_variance", "n_eff"} {
					f, ok := x[k].(float64)
					if !ok || !(f > 0) {
						t.Fatalf("weighted-moment key %q = %v, want > 0 in %v", k, x[k], x)
					}
				}
			}
			for _, vv := range x {
				walk(vv)
			}
		case []any:
			for _, vv := range x {
				walk(vv)
			}
		}
	}
	walk(raw)
	if found == 0 {
		t.Fatalf("no weighted-moment component maps found in %v", raw)
	}
}

// wzsAssertNonVacuous: every overlay layer carries at least one computed
// p-value and the cell components carry the weighted-moment keys.
func wzsAssertNonVacuous(t *testing.T, resp *types.Response, wantLayers int) {
	t.Helper()
	if len(resp.Overlays) != wantLayers {
		t.Fatalf("%d overlay layers, want %d", len(resp.Overlays), wantLayers)
	}
	for _, layer := range resp.Overlays {
		present := 0
		if mx := layer.Payload.Matrix; mx != nil {
			for _, row := range mx.Cells {
				for _, c := range row {
					if c.Present {
						present++
					}
				}
			}
		}
		if present == 0 {
			t.Fatalf("layer %q produced zero present p-values", layer.Name)
		}
	}
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatal("no crosstab components block")
	}
	for r, row := range resp.Components.Crosstab.CellComponents {
		for c, cell := range row {
			if cell == nil {
				t.Fatalf("CellComponents[%d][%d] is nil; every cell has rows", r, c)
			}
			for _, k := range []string{"m2_weighted", "sum_weights_sq", "weighted_variance", "n_eff"} {
				if f, ok := cell[k].(float64); !ok || !(f > 0) {
					t.Fatalf("CellComponents[%d][%d][%q] = %v, want > 0", r, c, k, cell[k])
				}
			}
		}
	}
}

func wzsDecode(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func wzsAssertJSONNear(t *testing.T, label string, want, got any) {
	t.Helper()
	if diff := wzsNearDiff(label, wzsDecode(t, want), wzsDecode(t, got)); diff != "" {
		t.Errorf("%s diverges: %s", label, diff)
	}
}

func wzsNearDiff(path string, want, got any) string {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		if !ok {
			return fmt.Sprintf("%s: want number %v, got %T", path, w, got)
		}
		scale := math.Max(1, math.Max(math.Abs(w), math.Abs(g)))
		if math.Abs(w-g) > wzsRelTol*scale {
			return fmt.Sprintf("%s: want %.17g, got %.17g", path, w, g)
		}
		return ""
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: object shape differs (want %d keys, got %v)", path, len(w), got)
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return fmt.Sprintf("%s: missing key %q", path, k)
			}
			if d := wzsNearDiff(path+"."+k, wv, gv); d != "" {
				return d
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: array shape differs", path)
		}
		for i := range w {
			if d := wzsNearDiff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); d != "" {
				return d
			}
		}
		return ""
	default:
		if want != got {
			return fmt.Sprintf("%s: want %v, got %v", path, want, got)
		}
		return ""
	}
}
